package rustsrc

import (
	"regexp"
	"strings"
)

var (
	programAttrRE = regexp.MustCompile(`#\[program\]`)
	deriveRE      = regexp.MustCompile(`#\[derive\(([^)]*)\)\]`)
	contextRE     = regexp.MustCompile(`Context\s*<\s*(?:'[A-Za-z_]+\s*,\s*)*([A-Za-z_][A-Za-z0-9_]*)`)
	declareIDRE   = regexp.MustCompile(`declare_id!\s*\(\s*"([1-9A-HJ-NP-Za-km-z]{32,44})"`)
	fieldRE       = regexp.MustCompile(`(?m)^[ \t]*(?:pub(?:\([^)]*\))?[ \t]+)?([A-Za-z_][A-Za-z0-9_]*)[ \t]*:[ \t]*`)
	accountAttrRE = regexp.MustCompile(`#\[account\(`)
)

// programMod finds the #[program] module: its name and span.
func (s *source) programMod() (string, span, bool) {
	loc := programAttrRE.FindIndex(s.masked)
	if loc == nil {
		return "", span{}, false
	}
	m := modRE.FindSubmatchIndex(s.masked[loc[1]:])
	if m == nil {
		return "", span{}, false
	}
	open := loc[1] + m[1] - 1
	c := s.closer(open)
	if c < 0 {
		return "", span{}, false
	}
	return string(s.masked[loc[1]+m[2] : loc[1]+m[3]]), span{open: open, close: c, line: s.line(open)}, true
}

// handlers lists the pub fns directly inside the program module whose
// first parameter is a Context<T>.
func (s *source) handlers(mod span) []Instruction {
	var out []Instruction
	for _, m := range fnRE.FindAllSubmatchIndex(s.masked, -1) {
		if m[0] < mod.open || m[0] > mod.close {
			continue
		}
		open, close, ok := s.body(m[1])
		if !ok || close > mod.close {
			continue
		}
		// Directly inside the module: no other brace opens between the
		// module's { and this fn, unmatched.
		if s.depthBetween(mod.open+1, m[0]) != 0 {
			continue
		}
		sig := string(s.masked[m[3]:open])
		p := strings.IndexByte(sig, '(')
		if p < 0 {
			continue
		}
		pc := s.closer(m[3] + p)
		if pc < 0 {
			continue
		}
		params := string(s.masked[m[3]+p+1 : pc])
		parts := splitTop(params, ',')
		if len(parts) == 0 {
			continue
		}
		cm := contextRE.FindStringSubmatch(parts[0])
		if cm == nil {
			continue
		}
		ix := Instruction{Name: snake(string(s.masked[m[2]:m[3]])), File: s.path, Line: s.line(m[2]), Context: cm[1]}
		for _, a := range parts[1:] {
			name, typ, ok := strings.Cut(a, ":")
			if !ok {
				continue
			}
			ix.Args = append(ix.Args, Arg{Name: tidy(strings.TrimPrefix(tidy(name), "mut ")), Type: tidy(typ)})
		}
		out = append(out, ix)
	}
	return out
}

// depthAll is the net depth change over [from, to) counting braces,
// brackets and parens.
func (s *source) depthAll(from, to int) int {
	d := 0
	for i := from; i < to && i < len(s.masked); i++ {
		switch s.masked[i] {
		case '{', '[', '(':
			d++
		case '}', ']', ')':
			d--
		}
	}
	return d
}

// depthBetween is the net brace depth change over [from, to).
func (s *source) depthBetween(from, to int) int {
	d := 0
	for i := from; i < to && i < len(s.masked); i++ {
		switch s.masked[i] {
		case '{':
			d++
		case '}':
			d--
		}
	}
	return d
}

// accountsStructs finds every #[derive(Accounts)] struct and its fields.
func (s *source) accountsStructs() []*AccountsStruct {
	var out []*AccountsStruct
	for _, m := range structRE.FindAllSubmatchIndex(s.masked, -1) {
		attrs, _ := s.attributes(m[0])
		derives := false
		for _, d := range deriveRE.FindAllStringSubmatch(attrs, -1) {
			for _, part := range strings.Split(d[1], ",") {
				if strings.TrimSpace(part) == "Accounts" {
					derives = true
				}
			}
		}
		if !derives {
			continue
		}
		open, close, ok := s.body(m[1])
		if !ok {
			continue
		}
		st := &AccountsStruct{Name: string(s.masked[m[2]:m[3]]), File: s.path, Line: s.line(m[2]), Composite: map[string]string{}}
		st.Fields = s.fields(open+1, close)
		out = append(out, st)
	}
	return out
}

// fields parses the fields of a struct body between from and to.
func (s *source) fields(from, to int) []Account {
	var out []Account
	body := s.masked[from:to]
	for _, m := range fieldRE.FindAllSubmatchIndex(body, -1) {
		nameOff := from + m[2]
		// The field is at depth 0 of the struct body, counting parens and
		// brackets too, so lines inside a multi-line #[account(...)] such as
		// State::SEED never read as fields; a path's :: is not a field colon.
		if s.depthAll(from, nameOff) != 0 || (from+m[1] < to && s.masked[from+m[1]] == ':') {
			continue
		}
		// Type runs to the next top-level comma or the end of the body.
		typStart := from + m[1]
		typEnd := typStart
		depth := 0
	scan:
		for typEnd < to {
			switch s.masked[typEnd] {
			case '<', '(', '[', '{':
				depth++
			case '>', ')', ']', '}':
				depth--
			case ',':
				if depth == 0 {
					break scan
				}
			}
			typEnd++
		}
		a := Account{Name: string(body[m[2]:m[3]]), Line: s.line(nameOff)}
		classify(&a, tidy(string(s.masked[typStart:typEnd])))
		attrs, _ := s.attributes(s.starts[s.line(nameOff)-1])
		if idx := accountAttrRE.FindAllStringIndex(attrs, -1); len(idx) > 0 {
			for _, loc := range idx {
				inner := balanced(attrs[loc[1]-1:])
				applyAttr(&a, inner)
			}
		}
		out = append(out, a)
	}
	return out
}

// balanced returns the text inside the paren at text[0].
func balanced(text string) string {
	depth := 0
	for i := 0; i < len(text); i++ {
		switch text[i] {
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			depth--
			if depth == 0 {
				return text[1:i]
			}
		}
	}
	return text[1:]
}

var wrappers = map[string]bool{"Account": true, "Signer": true, "Program": true, "UncheckedAccount": true, "AccountInfo": true, "AccountLoader": true, "InterfaceAccount": true, "Interface": true, "Sysvar": true, "SystemAccount": true, "LazyAccount": true}

// classify reads a field type: wrapper, inner type, and what the wrapper
// implies. Box and Option are unwrapped and recorded.
func classify(a *Account, typ string) {
	for {
		switch {
		case strings.HasPrefix(typ, "Box<"):
			typ = inner(typ)
			continue
		case strings.HasPrefix(typ, "Option<"):
			a.Optional = true
			typ = inner(typ)
			continue
		}
		break
	}
	name := typ
	if i := strings.IndexByte(typ, '<'); i >= 0 {
		name = typ[:i]
	}
	if i := strings.LastIndex(name, "::"); i >= 0 {
		name = name[i+2:]
	}
	a.Wrapper = name
	args := splitTop(inner(typ), ',')
	for _, g := range args {
		g = tidy(g)
		if g == "" || strings.HasPrefix(g, "'") {
			continue
		}
		if i := strings.LastIndex(g, "::"); i >= 0 {
			g = g[i+2:]
		}
		a.Inner = g
		break
	}
	switch name {
	case "Signer":
		a.Signer = true
	case "UncheckedAccount", "AccountInfo":
		a.Raw = true
	case "AccountLoader":
		a.ZeroCopy = true
	}
}

// inner returns what sits between the outermost < and >.
func inner(typ string) string {
	i := strings.IndexByte(typ, '<')
	j := strings.LastIndexByte(typ, '>')
	if i < 0 || j <= i {
		return ""
	}
	return typ[i+1 : j]
}

// applyAttr reads one #[account(...)] clause list.
func applyAttr(a *Account, text string) {
	for _, part := range splitTop(text, ',') {
		part = tidy(part)
		key, val, hasVal := strings.Cut(part, "=")
		key = tidy(key)
		val = tidy(val)
		switch {
		case key == "mut":
			a.Writable = true
		case key == "signer":
			a.Signer = true
		case key == "init" || key == "init_if_needed" || key == "zero":
			a.Init = true
			a.Writable = true
		case key == "close":
			a.Close = true
			a.Writable = true
		case key == "realloc":
			a.Realloc = true
			a.Writable = true
		case key == "seeds" && hasVal:
			a.Seeds = tidy(strings.TrimSuffix(strings.TrimPrefix(val, "["), "]"))
		case key == "seeds::program" && hasVal:
			a.SeedsProgram = strings.TrimSpace(strings.SplitN(val, "@", 2)[0])
		case key == "bump":
			a.Bump = true
		case key == "has_one" && hasVal:
			a.HasOne = append(a.HasOne, strings.Fields(strings.SplitN(val, "@", 2)[0])[0])
		case key == "owner" && hasVal:
			a.Owner = strings.TrimSpace(strings.SplitN(val, "@", 2)[0])
		case key == "address" && hasVal:
			a.Address = strings.TrimSpace(strings.SplitN(val, "@", 2)[0])
		case key == "constraint":
			a.Constraints++
		}
	}
}

// flatten expands composite fields (a field whose wrapper is another
// Accounts struct) into parent.child entries, the way the IDL lists them.
func flatten(st *AccountsStruct, structs map[string]*AccountsStruct, prefix string, depth int) []Account {
	var out []Account
	for _, f := range st.Fields {
		if sub, ok := structs[f.Wrapper]; ok && depth < 4 && !wrappers[f.Wrapper] {
			st.Composite[f.Name] = f.Wrapper
			out = append(out, flatten(sub, structs, prefix+f.Name+".", depth+1)...)
			continue
		}
		f.Name = prefix + f.Name
		out = append(out, f)
	}
	return out
}
