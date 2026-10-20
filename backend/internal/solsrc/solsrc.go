// Package solsrc reads a Solidity checkout the way rustsrc reads a Rust
// one: pattern-based, no compiler, no toolchain. It finds the contracts,
// every function with the selector its signature implies, every event
// with its topic, and the sites a reviewer looks for first: external
// calls, delegatecalls, assembly, unchecked blocks and self-destructs.
package solsrc

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/forefy/vig/backend/internal/evm"
)

// Param is one parameter with its ABI type resolved.
type Param struct {
	Name    string
	Type    string // canonical ABI type: uint256, address, tuple, tuple[]
	Raw     string // as written: Order, IERC20, uint
	Indexed bool
	Fields  []Param // tuple components
}

// Function is one function a contract exposes.
type Function struct {
	Name       string
	Params     []Param
	Visibility string // external, public, internal, private
	Mutability string // view, pure, payable, or empty
	Modifiers  []string
	File       string
	Line       int
	Sig        string
	Selector   [4]byte
	// What the body calls: other functions of the contract by name, and
	// contracts outside it. Reach follows the former to collect the latter.
	Calls   []string
	Callees []Callee
}

// Callee is a contract a function body calls out to: an interface the
// source names, a token moved through the ERC-20 surface, or an ETH
// recipient. Address is set when the source fixes it.
type Callee struct {
	Name    string
	Address string
	Line    int
}

// Event is one event a contract declares.
type Event struct {
	Name   string
	Params []Param
	File   string
	Line   int
	Sig    string
	Topic  [32]byte
}

// Sites counts what the contract's code does that a reviewer wants to see.
type Sites struct {
	Calls        int // .call, interface calls through a typed reference
	DelegateCall int
	StaticCall   int
	Transfers    int // token transfers, safe or not, and value transfers
	Assembly     int
	Unchecked    int
	SelfDestruct int
	Payable      int // payable functions, receive and fallback
}

// Contract is one contract, interface, library or abstract contract.
type Contract struct {
	Name      string
	Kind      string // contract, interface, library, abstract
	File      string
	Line      int
	Inherits  []string
	Functions []Function
	Events    []Event
	Errors    []string
	Sites     Sites
	Code      int
}

// Model is a checkout read once.
type Model struct {
	Root      string
	Contracts []Contract
	Structs   map[string][]Param // by name, fields with raw types
	Enums     map[string]bool
	Values    map[string]string // user-defined value types: name to the elementary type it wraps
	Files     int
	Code      int
}

var skipDirs = map[string]bool{"node_modules": true, "lib": true, "test": true, "tests": true, "script": true, "scripts": true, "mocks": true, "mock": true, "out": true, "artifacts": true, "cache": true, ".git": true}

// Load reads every .sol file under root/sub, tests and vendored libraries
// excluded.
func Load(root, sub string) (*Model, error) {
	base := filepath.Join(root, filepath.FromSlash(sub))
	m := &Model{Root: root, Structs: map[string][]Param{}, Enums: map[string]bool{}, Values: map[string]string{}}
	type file struct {
		path, rel string
		src       string
	}
	var files []file
	err := filepath.WalkDir(base, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p != base && skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".sol") {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		files = append(files, file{path: p, rel: filepath.ToSlash(rel), src: strip(string(b))})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].rel < files[j].rel })
	m.Files = len(files)
	// Types first, across every file, so a struct declared in an interface
	// resolves in the contract that implements it.
	for _, f := range files {
		m.Code += codeLines(f.src)
		for _, sm := range structRE.FindAllStringSubmatch(f.src, -1) {
			m.Structs[sm[1]] = fieldsOf(sm[2])
		}
		for _, em := range enumRE.FindAllStringSubmatch(f.src, -1) {
			m.Enums[em[1]] = true
		}
		for _, vm := range valueTypeRE.FindAllStringSubmatch(f.src, -1) {
			m.Values[vm[1]] = vm[2]
		}
	}
	contractNames := map[string]bool{}
	for _, f := range files {
		for _, cm := range contractRE.FindAllStringSubmatch(f.src, -1) {
			contractNames[cm[3]] = true
		}
	}
	for _, f := range files {
		for _, loc := range contractRE.FindAllStringSubmatchIndex(f.src, -1) {
			sm := f.src[loc[0]:loc[1]]
			cm := contractRE.FindStringSubmatch(sm)
			body, end := block(f.src, loc[1]-1)
			c := Contract{Name: cm[3], File: f.rel, Line: lineOf(f.src, loc[0]), Code: codeLines(body)}
			c.Kind = strings.TrimSpace(cm[1] + " " + cm[2])
			if strings.HasPrefix(c.Kind, "abstract") {
				c.Kind = "abstract"
			}
			for _, in := range strings.Split(cm[4], ",") {
				if s := strings.TrimSpace(strings.Split(strings.TrimSpace(in), "(")[0]); s != "" {
					c.Inherits = append(c.Inherits, s)
				}
			}
			m.readContract(&c, body, f.rel, lineOf(f.src, loc[1]), contractNames)
			_ = end
			m.Contracts = append(m.Contracts, c)
		}
	}
	return m, nil
}

var (
	contractRE = regexp.MustCompile(`(?m)^\s*(abstract\s+)?(contract|interface|library)\s+([A-Za-z_][A-Za-z0-9_]*)\s*(?:is\s+([^{]+))?\{`)
	structRE   = regexp.MustCompile(`(?s)\bstruct\s+([A-Za-z_][A-Za-z0-9_]*)\s*\{([^}]*)\}`)
	enumRE     = regexp.MustCompile(`\benum\s+([A-Za-z_][A-Za-z0-9_]*)\s*\{`)
	// type Token is address; at file or contract level, a value type that
	// is its underlying elementary type on the wire.
	valueTypeRE = regexp.MustCompile(`\btype\s+([A-Za-z_][A-Za-z0-9_]*)\s+is\s+([A-Za-z0-9_]+)\s*;`)
	functionRE  = regexp.MustCompile(`(?s)\bfunction\s+([A-Za-z_][A-Za-z0-9_]*)\s*\(`)
	eventRE     = regexp.MustCompile(`(?s)\bevent\s+([A-Za-z_][A-Za-z0-9_]*)\s*\(([^)]*)\)`)
	errorRE     = regexp.MustCompile(`\berror\s+([A-Za-z_][A-Za-z0-9_]*)\s*\(`)
	specialRE   = regexp.MustCompile(`\b(receive|fallback)\s*\(\s*\)`)
)

// readContract fills a contract from its body.
func (m *Model) readContract(c *Contract, body, file string, firstLine int, contracts map[string]bool) {
	fixed := fixedCallees(body)
	for _, loc := range functionRE.FindAllStringSubmatchIndex(body, -1) {
		name := body[loc[2]:loc[3]]
		params, after := parens(body, loc[1]-1)
		head := headerOf(body, after)
		f := Function{Name: name, File: file, Line: firstLine + lineOf(body, loc[0]) - 1}
		if open := after + len(head); open < len(body) && body[open] == '{' {
			inner, _ := block(body, open)
			f.Calls, f.Callees = calleesOf(inner, fixed, f.Line+lineOf(body[:open], 0)-1)
			for i := range f.Callees {
				f.Callees[i].Line = firstLine + lineOf(body, open) - 1
			}
		}
		f.Params = m.params(params, contracts)
		f.Visibility, f.Mutability, f.Modifiers = qualifiers(head)
		if f.Visibility == "" {
			f.Visibility = "public"
		}
		f.Sig = name + "(" + canonicalList(f.Params) + ")"
		h := evm.Keccak([]byte(f.Sig))
		copy(f.Selector[:], h[:4])
		if f.Mutability == "payable" {
			c.Sites.Payable++
		}
		c.Functions = append(c.Functions, f)
	}
	// A public state variable is a function too: the compiler emits a
	// getter that takes the mapping keys or the array index.
	for _, gm := range getterRE.FindAllStringSubmatchIndex(body, -1) {
		typ, name := strings.TrimSpace(body[gm[2]:gm[3]]), body[gm[4]:gm[5]]
		if strings.HasPrefix(typ, "function") || strings.Contains(typ, "(") && !strings.HasPrefix(typ, "mapping") {
			continue
		}
		f := Function{Name: name, File: file, Line: firstLine + lineOf(body, gm[0]) - 1, Visibility: "public", Mutability: "view"}
		for _, key := range getterKeys(typ) {
			p := Param{Raw: key}
			m.resolve(&p, contracts, 0)
			f.Params = append(f.Params, p)
		}
		f.Sig = name + "(" + canonicalList(f.Params) + ")"
		h := evm.Keccak([]byte(f.Sig))
		copy(f.Selector[:], h[:4])
		c.Functions = append(c.Functions, f)
	}
	for _, sm := range eventRE.FindAllStringSubmatchIndex(body, -1) {
		name := body[sm[2]:sm[3]]
		e := Event{Name: name, File: file, Line: firstLine + lineOf(body, sm[0]) - 1}
		e.Params = m.params(body[sm[4]:sm[5]], contracts)
		e.Sig = name + "(" + canonicalList(e.Params) + ")"
		e.Topic = evm.Keccak([]byte(e.Sig))
		c.Events = append(c.Events, e)
	}
	for _, em := range errorRE.FindAllStringSubmatch(body, -1) {
		c.Errors = append(c.Errors, em[1])
	}
	c.Sites.Payable += len(specialRE.FindAllString(body, -1))
	c.Sites.Calls += strings.Count(body, ".call{") + strings.Count(body, ".call(")
	c.Sites.DelegateCall += strings.Count(body, ".delegatecall(")
	c.Sites.StaticCall += strings.Count(body, ".staticcall(")
	c.Sites.Transfers += strings.Count(body, "safeTransfer(") + strings.Count(body, "safeTransferFrom(") + strings.Count(body, ".transfer(") + strings.Count(body, ".transferFrom(")
	c.Sites.Assembly += strings.Count(body, "assembly {") + strings.Count(body, "assembly{") + strings.Count(body, "assembly (")
	c.Sites.Unchecked += strings.Count(body, "unchecked {") + strings.Count(body, "unchecked{")
	c.Sites.SelfDestruct += strings.Count(body, "selfdestruct(")
	// Interface calls: a typed reference invoked, IFoo(x).bar(...), counted
	// as calls out.
	c.Sites.Calls += len(ifaceCallRE.FindAllString(body, -1))
}

// getterRE matches a state variable declared public at the top level of
// a contract body: its type, then public, then its name.
var getterRE = regexp.MustCompile(`(?m)^\s*((?:mapping\s*\([^;]*?\)|[A-Za-z_][A-Za-z0-9_.]*(?:\[[^\]]*\])*)(?:\s+payable)?)\s+(?:constant\s+|immutable\s+)?public\s+(?:constant\s+|immutable\s+|override\s+)*([A-Za-z_][A-Za-z0-9_]*)\s*(?:=[^;]*)?;`)

// getterKeys lists the parameter types a getter takes: one per mapping
// key, in nesting order, and uint256 per array dimension.
func getterKeys(typ string) []string {
	var keys []string
	t := strings.TrimSpace(typ)
	for strings.HasPrefix(t, "mapping") {
		open := strings.Index(t, "(")
		inner, after := parens(t, open)
		key, value, ok := strings.Cut(inner, "=>")
		if !ok {
			break
		}
		keys = append(keys, strings.Fields(strings.TrimSpace(key))[0])
		t = strings.TrimSpace(value)
		_ = after
	}
	for strings.HasSuffix(t, "]") {
		i := strings.LastIndex(t, "[")
		if i < 0 {
			break
		}
		keys = append(keys, "uint256")
		t = t[:i]
	}
	return keys
}

var ifaceCallRE = regexp.MustCompile(`\bI[A-Z][A-Za-z0-9_]*\s*\([^()]*\)\s*\.\s*[a-zA-Z_][A-Za-z0-9_]*\s*\(`)

// params parses "Type memory name, Type name" into resolved params.
func (m *Model) params(list string, contracts map[string]bool) []Param {
	var out []Param
	for _, part := range splitTop(list) {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		words := strings.Fields(part)
		p := Param{Raw: words[0]}
		for _, w := range words[1:] {
			switch w {
			case "memory", "calldata", "storage", "payable":
			case "indexed":
				p.Indexed = true
			default:
				p.Name = w
			}
		}
		m.resolve(&p, contracts, 0)
		out = append(out, p)
	}
	return out
}

// resolve turns a written type into its ABI type: structs into tuples with
// their fields, enums into uint8, contracts into address, shorthands into
// their canonical width.
func (m *Model) resolve(p *Param, contracts map[string]bool, depth int) {
	raw := p.Raw
	suffix := ""
	for strings.HasSuffix(raw, "]") {
		i := strings.LastIndex(raw, "[")
		if i < 0 {
			break
		}
		suffix = raw[i:] + suffix
		raw = raw[:i]
	}
	if i := strings.LastIndex(raw, "."); i >= 0 {
		raw = raw[i+1:]
	}
	switch {
	case raw == "uint":
		p.Type = "uint256"
	case raw == "int":
		p.Type = "int256"
	case raw == "byte":
		p.Type = "bytes1"
	case raw == "address" || raw == "bool" || raw == "string" || raw == "bytes" || elementaryRE.MatchString(raw):
		p.Type = raw
	case m.Enums[raw]:
		p.Type = "uint8"
	case m.Values[raw] != "" && depth < 6:
		under := Param{Raw: m.Values[raw]}
		m.resolve(&under, contracts, depth+1)
		p.Type = under.Type
	case contracts[raw]:
		p.Type = "address"
	default:
		if fields, ok := m.Structs[raw]; ok && depth < 6 {
			p.Type = "tuple"
			for _, f := range fields {
				sub := f
				m.resolve(&sub, contracts, depth+1)
				p.Fields = append(p.Fields, sub)
			}
		} else {
			// Unknown user type: most often an interface from a library
			// outside the checkout, which is an address on the wire.
			p.Type = "address"
		}
	}
	p.Type += suffix
}

var elementaryRE = regexp.MustCompile(`^(u?int(8|16|24|32|40|48|56|64|72|80|88|96|104|112|120|128|136|144|152|160|168|176|184|192|200|208|216|224|232|240|248|256)|bytes([1-9]|[12][0-9]|3[0-2])|u?fixed\d*x\d*)$`)

// fieldsOf reads a struct body into raw params.
func fieldsOf(body string) []Param {
	var out []Param
	for _, line := range strings.Split(body, ";") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		words := strings.Fields(line)
		if len(words) < 2 {
			continue
		}
		out = append(out, Param{Raw: words[0], Name: words[len(words)-1]})
	}
	return out
}

// canonicalList renders params for a signature, tuples expanded.
func canonicalList(ps []Param) string {
	parts := make([]string, len(ps))
	for i, p := range ps {
		parts[i] = canonical(p)
	}
	return strings.Join(parts, ",")
}

func canonical(p Param) string {
	if strings.HasPrefix(p.Type, "tuple") {
		return "(" + canonicalList(p.Fields) + ")" + strings.TrimPrefix(p.Type, "tuple")
	}
	return p.Type
}

// ABIParams renders params in the shape the evm decoder takes.
func ABIParams(ps []Param) []evm.Param {
	out := make([]evm.Param, 0, len(ps))
	for _, p := range ps {
		e := evm.Param{Name: p.Name, Type: p.Type, Indexed: p.Indexed}
		if len(p.Fields) > 0 {
			e.Components = ABIParams(p.Fields)
		}
		out = append(out, e)
	}
	return out
}

// Interface renders a contract's functions and events as an evm.ABI, so
// calldata decodes from source when nothing on chain is verified.
func (c *Contract) Interface() *evm.ABI {
	a := &evm.ABI{Functions: map[[4]byte]evm.Function{}, Events: map[[32]byte]evm.Event{}}
	for _, f := range c.Functions {
		if f.Visibility != "external" && f.Visibility != "public" {
			continue
		}
		a.Functions[f.Selector] = evm.Function{Name: f.Name, Inputs: ABIParams(f.Params), Selector: f.Selector, Sig: f.Sig}
	}
	for _, e := range c.Events {
		a.Events[e.Topic] = evm.Event{Name: e.Name, Inputs: ABIParams(e.Params), Topic: e.Topic, Sig: e.Sig}
	}
	return a
}

// ExposedInterface is the contract's interface as deployed: its own
// external and public functions and the ones it inherits, with the
// events it declares.
func (m *Model) ExposedInterface(c *Contract) *evm.ABI {
	a := &evm.ABI{Functions: map[[4]byte]evm.Function{}, Events: map[[32]byte]evm.Event{}}
	for _, f := range m.Exposed(c) {
		a.Functions[f.Selector] = evm.Function{Name: f.Name, Inputs: ABIParams(f.Params), Selector: f.Selector, Sig: f.Sig}
	}
	seen := map[[32]byte]bool{}
	var walk func(c *Contract, depth int)
	walk = func(c *Contract, depth int) {
		for _, e := range c.Events {
			if !seen[e.Topic] {
				seen[e.Topic] = true
				a.Events[e.Topic] = evm.Event{Name: e.Name, Inputs: ABIParams(e.Params), Topic: e.Topic, Sig: e.Sig}
			}
		}
		if depth >= 2 {
			return
		}
		for _, name := range c.Inherits {
			if p, ok := m.ByName(name); ok {
				walk(p, depth+1)
			}
		}
	}
	walk(c, 0)
	return a
}

// Deployable lists the contracts that can be deployed: not interfaces,
// libraries or abstract ones.
func (m *Model) Deployable() []Contract {
	var out []Contract
	for _, c := range m.Contracts {
		if c.Kind == "contract" {
			out = append(out, c)
		}
	}
	return out
}

// ByName finds a contract.
func (m *Model) ByName(name string) (*Contract, bool) {
	for i := range m.Contracts {
		if m.Contracts[i].Name == name {
			return &m.Contracts[i], true
		}
	}
	return nil, false
}

// Exposed lists a contract's external and public functions, inherited
// interfaces included when the checkout declares them.
func (m *Model) Exposed(c *Contract) []Function {
	seen := map[[4]byte]bool{}
	var out []Function
	add := func(fs []Function) {
		for _, f := range fs {
			if f.Visibility != "external" && f.Visibility != "public" || seen[f.Selector] {
				continue
			}
			seen[f.Selector] = true
			out = append(out, f)
		}
	}
	add(c.Functions)
	for _, name := range c.Inherits {
		if p, ok := m.ByName(name); ok {
			add(p.Functions)
			for _, gp := range p.Inherits {
				if g, ok := m.ByName(gp); ok {
					add(g.Functions)
				}
			}
		}
	}
	return out
}

// qualifiers reads visibility, mutability and modifiers off a function
// header (what follows the parameter list up to the body or semicolon).
func qualifiers(head string) (visibility, mutability string, modifiers []string) {
	head = strings.TrimSpace(head)
	// Drop the returns clause.
	if i := strings.Index(head, "returns"); i >= 0 {
		head = head[:i]
	}
	for _, w := range strings.Fields(head) {
		w = strings.TrimSpace(strings.Split(w, "(")[0])
		switch w {
		case "external", "public", "internal", "private":
			visibility = w
		case "view", "pure", "payable":
			mutability = w
		case "virtual", "override", "":
		default:
			if w != "" {
				modifiers = append(modifiers, w)
			}
		}
	}
	return
}

// headerOf reads from after the parameter list to the body brace or
// semicolon.
func headerOf(body string, from int) string {
	depth := 0
	for i := from; i < len(body); i++ {
		switch body[i] {
		case '(':
			depth++
		case ')':
			depth--
		case '{':
			if depth == 0 {
				return body[from:i]
			}
		case ';':
			if depth == 0 {
				return body[from:i]
			}
		}
	}
	return body[from:]
}

// parens reads a balanced parenthesised list starting at the open paren.
func parens(s string, open int) (inner string, after int) {
	depth := 0
	for i := open; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return s[open+1 : i], i + 1
			}
		}
	}
	return s[open+1:], len(s)
}

// block reads a balanced brace block starting at the open brace.
func block(s string, open int) (inner string, after int) {
	depth := 0
	for i := open; i < len(s); i++ {
		switch s[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[open+1 : i], i + 1
			}
		}
	}
	return s[open+1:], len(s)
}

// splitTop splits on commas outside parentheses and brackets.
func splitTop(s string) []string {
	var out []string
	depth := 0
	start := 0
	for i, c := range s {
		switch c {
		case '(', '[':
			depth++
		case ')', ']':
			depth--
		case ',':
			if depth == 0 {
				out = append(out, s[start:i])
				start = i + 1
			}
		}
	}
	return append(out, s[start:])
}

// strip blanks comments and string contents so patterns never match inside
// them, keeping every newline so lines still count.
func strip(src string) string {
	var b strings.Builder
	b.Grow(len(src))
	i := 0
	for i < len(src) {
		switch {
		case strings.HasPrefix(src[i:], "//"):
			for i < len(src) && src[i] != '\n' {
				i++
			}
		case strings.HasPrefix(src[i:], "/*"):
			end := strings.Index(src[i+2:], "*/")
			if end < 0 {
				end = len(src) - i - 2
			}
			for _, c := range src[i : i+2+end+2] {
				if c == '\n' {
					b.WriteByte('\n')
				}
			}
			i += 2 + end + 2
		case src[i] == '"' || src[i] == '\'':
			q := src[i]
			b.WriteByte(q)
			i++
			for i < len(src) && src[i] != q {
				if src[i] == '\\' {
					i++
				}
				if i < len(src) && src[i] == '\n' {
					b.WriteByte('\n')
				}
				i++
			}
			if i < len(src) {
				b.WriteByte(q)
				i++
			}
		default:
			b.WriteByte(src[i])
			i++
		}
	}
	return b.String()
}

// codeLines counts lines with code after comments are stripped.
func codeLines(s string) int {
	n := 0
	for _, l := range strings.Split(s, "\n") {
		if strings.TrimSpace(l) != "" {
			n++
		}
	}
	return n
}

func lineOf(s string, off int) int {
	if off > len(s) {
		off = len(s)
	}
	return strings.Count(s[:off], "\n") + 1
}

var (
	// ISignatureTransfer private constant _PERMIT2 = ISignatureTransfer(0x...);
	fixedCalleeRE = regexp.MustCompile(`\b(I[A-Z][A-Za-z0-9_]*)\s+(?:private|internal|public)?\s*(?:constant|immutable)\s+([A-Za-z_][A-Za-z0-9_]*)\s*=\s*[A-Za-z_][A-Za-z0-9_]*\((0x[0-9a-fA-F]{40})\)`)
	// IFoo(expr).bar(...): a cast to an interface is a call out to it.
	ifaceCastRE = regexp.MustCompile(`\b(I[A-Z][A-Za-z0-9_]*)\s*\(`)
	// (expr).method(: a method on a parenthesised receiver, e.g. payable(x).sendValue(
	parenCallRE  = regexp.MustCompile(`\)\s*\.\s*([a-zA-Z_][A-Za-z0-9_]*)\s*\(`)
	innerCallRE  = regexp.MustCompile(`(?:^|[^.\w])(_?[a-z][A-Za-z0-9_]*)\s*\(`)
	varCallRE    = regexp.MustCompile(`\b([A-Za-z_][A-Za-z0-9_]*)\s*\.\s*([a-zA-Z_][A-Za-z0-9_]*)\s*\(`)
	erc20Methods = map[string]bool{"transfer": true, "transferFrom": true, "safeTransfer": true, "safeTransferFrom": true, "approve": true, "forceApprove": true, "safeIncreaseAllowance": true, "balanceOf": true, "allowance": true, "permit": true}
	ethMethods   = map[string]bool{"sendValue": true}
	notCalls     = map[string]bool{"if": true, "for": true, "while": true, "require": true, "revert": true, "assert": true, "emit": true, "return": true, "new": true, "unchecked": true, "assembly": true, "keccak256": true, "abi": true, "type": true, "address": true, "payable": true, "uint256": true, "uint128": true, "uint8": true, "bytes32": true, "bytes": true, "bool": true, "string": true, "catch": true, "try": true, "delete": true}
)

// fixedCallees finds interface-typed constants bound to a literal address.
func fixedCallees(body string) map[string]Callee {
	out := map[string]Callee{}
	for _, m := range fixedCalleeRE.FindAllStringSubmatch(body, -1) {
		out[m[2]] = Callee{Name: m[1], Address: strings.ToLower(m[3])}
	}
	return out
}

// calleesOf reads one function body: the contract's own functions it
// calls by name, and the outside contracts it reaches directly.
func calleesOf(body string, fixed map[string]Callee, line int) (calls []string, callees []Callee) {
	seenCall := map[string]bool{}
	for _, m := range innerCallRE.FindAllStringSubmatch(body, -1) {
		name := m[1]
		if notCalls[name] || seenCall[name] || strings.HasPrefix(name, "I") {
			continue
		}
		seenCall[name] = true
		calls = append(calls, name)
	}
	seen := map[string]bool{}
	add := func(c Callee) {
		key := c.Name + "@" + c.Address
		if seen[key] {
			return
		}
		seen[key] = true
		c.Line = line
		callees = append(callees, c)
	}
	for _, m := range ifaceCastRE.FindAllStringSubmatch(body, -1) {
		if strings.HasPrefix(m[1], "IERC20") {
			add(Callee{Name: "ERC-20 token"})
			continue
		}
		add(Callee{Name: m[1]})
	}
	for _, m := range parenCallRE.FindAllStringSubmatch(body, -1) {
		switch {
		case erc20Methods[m[1]]:
			add(Callee{Name: "ERC-20 token"})
		case ethMethods[m[1]]:
			add(Callee{Name: "ETH recipient"})
		}
	}
	for _, m := range varCallRE.FindAllStringSubmatch(body, -1) {
		recv, method := m[1], m[2]
		if c, ok := fixed[recv]; ok {
			add(c)
			continue
		}
		switch {
		case erc20Methods[method]:
			add(Callee{Name: "ERC-20 token"})
		case ethMethods[method] || method == "call" && strings.Contains(body, recv+".call{value"):
			add(Callee{Name: "ETH recipient"})
		case method == "call" || method == "delegatecall" || method == "staticcall":
			add(Callee{Name: "any contract"})
		}
	}
	if strings.Contains(body, "call(gas()") || strings.Contains(body, "call(gas(),") {
		add(Callee{Name: "ETH recipient"})
	}
	return calls, callees
}

// Reach is everything a function reaches outside the contract, following
// the contract's own functions (and its parents') to depth six.
func (m *Model) Reach(c *Contract, fn Function) []Callee {
	byName := map[string]Function{}
	var collect func(c *Contract, depth int)
	collect = func(c *Contract, depth int) {
		for _, f := range c.Functions {
			if _, ok := byName[f.Name]; !ok {
				byName[f.Name] = f
			}
		}
		if depth >= 2 {
			return
		}
		for _, name := range c.Inherits {
			if p, ok := m.ByName(name); ok {
				collect(p, depth+1)
			}
		}
	}
	collect(c, 0)
	seen := map[string]bool{}
	var out []Callee
	visited := map[string]bool{}
	var walk func(f Function, depth int)
	walk = func(f Function, depth int) {
		if visited[f.Name] || depth > 6 {
			return
		}
		visited[f.Name] = true
		for _, c := range f.Callees {
			key := c.Name + "@" + c.Address
			if !seen[key] {
				seen[key] = true
				out = append(out, c)
			}
		}
		for _, name := range f.Calls {
			if g, ok := byName[name]; ok {
				walk(g, depth+1)
			}
		}
	}
	walk(fn, 0)
	return out
}
