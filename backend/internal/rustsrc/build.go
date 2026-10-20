package rustsrc

import (
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var skipDirs = map[string]bool{".git": true, "node_modules": true, "target": true, ".github": true, "dist": true, "build": true, "tests": true, "test": true}

const (
	maxFileBytes  = 4 << 20
	maxFiles      = 4000
	maxReach      = 400
	maxTotalBytes = 96 << 20
)

var (
	pkgNameRE   = regexp.MustCompile(`(?ms)^\[package\].*?^name\s*=\s*"([^"]+)"`)
	crateTypeRE = regexp.MustCompile(`crate-type\s*=\s*\[[^\]]*"cdylib"`)
)

// Load reads a checkout under root, optionally narrowed to sub, and builds
// the model. Errors are only for an unreadable root; a file that fails to
// read is skipped.
func Load(root, sub string) (*Model, error) {
	base := root
	if sub != "" {
		base = filepath.Join(root, filepath.Clean(sub))
	}
	if _, err := os.Stat(base); err != nil {
		return nil, err
	}
	m := &Model{Functions: map[string]*Function{}, Consts: map[string][]byte{}, Types: map[string]TypeDef{}}
	var cargos []string
	sources := map[string]*source{}
	total := 0
	_ = filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return fs.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		name := d.Name()
		if name == "Cargo.toml" {
			cargos = append(cargos, rel)
			return nil
		}
		if !strings.HasSuffix(name, ".rs") || len(sources) >= maxFiles {
			return nil
		}
		info, err := d.Info()
		if err != nil || info.Size() > maxFileBytes || total+int(info.Size()) > maxTotalBytes {
			return nil
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		total += len(raw)
		sources[rel] = lex(rel, raw)
		return nil
	})
	sort.Strings(cargos)
	// Crate roots, longest first so a nested crate wins over its parent.
	type crateRoot struct {
		dir, name string
		cdylib    bool
	}
	var roots []crateRoot
	for _, c := range cargos {
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(c)))
		if err != nil {
			continue
		}
		pm := pkgNameRE.FindSubmatch(b)
		if pm == nil {
			continue
		}
		dir := path.Dir(c)
		if dir == "." {
			dir = ""
		}
		roots = append(roots, crateRoot{dir: dir, name: string(pm[1]), cdylib: crateTypeRE.Match(b)})
	}
	sort.Slice(roots, func(i, j int) bool { return len(roots[i].dir) > len(roots[j].dir) })
	crateOf := func(file string) (crateRoot, bool) {
		for _, r := range roots {
			if r.dir == "" || strings.HasPrefix(file, r.dir+"/") {
				return r, true
			}
		}
		return crateRoot{}, false
	}
	// Files per crate, functions per file.
	byCrate := map[string][]string{}
	crateMeta := map[string]crateRoot{}
	var allFns []*Function
	for _, rel := range sortedKeys(sources) {
		s := sources[rel]
		m.Files = append(m.Files, s.stats)
		r, ok := crateOf(rel)
		if !ok {
			continue
		}
		byCrate[r.dir] = append(byCrate[r.dir], rel)
		crateMeta[r.dir] = r
		for _, f := range s.functions(r.name) {
			allFns = append(allFns, f)
			m.Functions[key(f)] = f
		}
		for name, v := range s.consts() {
			if _, dup := m.Consts[name]; !dup {
				m.Consts[name] = v
			}
		}
		for name, def := range s.typeDefs() {
			if _, dup := m.Types[name]; !dup {
				m.Types[name] = def
			}
		}
	}
	byName := index(allFns)
	for _, dir := range sortedKeys(byCrate) {
		files := byCrate[dir]
		r := crateMeta[dir]
		c := Crate{Name: r.name, Root: dir, Framework: "unknown", Files: files, Structs: map[string]*AccountsStruct{}, Instructions: []Instruction{}, Consts: map[string][]byte{}}
		for _, rel := range files {
			for name, v := range sources[rel].consts() {
				c.Consts[name] = v
			}
		}
		for name, v := range m.Consts {
			if _, own := c.Consts[name]; !own {
				c.Consts[name] = v
			}
		}
		var handlers []Instruction
		var entry *Function
		for _, rel := range files {
			s := sources[rel]
			for _, dm := range declareIDRE.FindAllSubmatch(s.clean, -1) {
				c.DeclaredIDs = appendUnique(c.DeclaredIDs, string(dm[1]))
			}
			if name, sp, ok := s.programMod(); ok {
				c.Framework, c.Module = "anchor", name
				handlers = append(handlers, s.handlers(sp)...)
			}
			for _, st := range s.accountsStructs() {
				c.Structs[st.Name] = st
			}
			if em := entrypointRE.FindSubmatch(s.masked); em != nil && entry == nil {
				name := string(em[1])
				if i := strings.LastIndex(name, "::"); i >= 0 {
					name = name[i+2:]
				}
				for _, f := range byName[name] {
					if f.Crate == r.name {
						entry = f
					}
				}
				if c.Framework == "unknown" {
					c.Framework = "native"
				}
			}
		}
		program := len(c.DeclaredIDs) > 0 || c.Framework != "unknown" || r.cdylib
		if !program {
			continue
		}
		if c.Framework == "anchor" {
			for _, ix := range handlers {
				c.Instructions = append(c.Instructions, finishAnchor(ix, &c, byName, sources))
			}
		} else if entry != nil {
			for _, ix := range nativeInstructions(entry, byName, sources) {
				c.Instructions = append(c.Instructions, finishNative(ix, &c, byName, sources))
			}
		}
		sort.SliceStable(c.Instructions, func(i, j int) bool {
			a, b := c.Instructions[i], c.Instructions[j]
			return a.File < b.File || a.File == b.File && a.Line < b.Line
		})
		m.Crates = append(m.Crates, c)
	}
	return m, nil
}

// finishAnchor fills an Anchor handler: accounts from its context struct,
// the reachable functions from the handler, the sites in them.
func finishAnchor(ix Instruction, c *Crate, byName map[string][]*Function, sources map[string]*source) Instruction {
	acctByName := map[string]Account{}
	if st, ok := c.Structs[ix.Context]; ok {
		ix.ContextFile = st.File
		ix.Accounts = flatten(st, c.Structs, "", 0)
		for _, a := range ix.Accounts {
			acctByName[a.Name] = a
			if i := strings.LastIndex(a.Name, "."); i >= 0 {
				acctByName[a.Name[i+1:]] = a
			}
		}
	}
	if ix.Accounts == nil {
		ix.Accounts = []Account{}
	}
	var seeds []*Function
	for _, f := range byName[snakeBack(ix.Name, byName)] {
		if f.File == ix.File && f.Line == ix.Line {
			seeds = append(seeds, f)
		}
	}
	return finish(ix, seeds, byName, sources, acctByName)
}

// snakeBack finds the declared spelling of a handler whose snake name is
// known; handlers are snake_case in practice, so this is the name itself.
func snakeBack(name string, byName map[string][]*Function) string {
	if _, ok := byName[name]; ok {
		return name
	}
	for n := range byName {
		if snake(n) == name {
			return n
		}
	}
	return name
}

// finishNative fills a native arm: its seed is the synthetic arm function.
func finishNative(ix Instruction, c *Crate, byName map[string][]*Function, sources map[string]*source) Instruction {
	var seeds []*Function
	for _, k := range ix.Reach {
		if f, ok := armSeeds[k]; ok {
			seeds = append(seeds, f)
		}
	}
	ix.Reach = nil
	ix = finish(ix, seeds, byName, sources, nil)
	// Drop the synthetic arm from the reach list; keep its counts.
	if len(ix.Reach) > 0 && strings.HasPrefix(ix.Reach[0], ix.File+":arm:") {
		ix.Reach = ix.Reach[1:]
		ix.Counts.Functions = len(ix.Reach)
	}
	if ix.Accounts == nil {
		ix.Accounts = []Account{}
	}
	return ix
}

// finish walks the reach and gathers sites and counts.
func finish(ix Instruction, seeds []*Function, byName map[string][]*Function, sources map[string]*source, acctByName map[string]Account) Instruction {
	ctxType := ""
	if acctByName != nil {
		ctxType = ix.Context
	}
	reached, unresolved := reach(seeds, byName, maxReach, ctxType)
	ix.Unresolved = unresolved
	ix.Reach = nil
	for _, f := range reached {
		ix.Reach = append(ix.Reach, key(f))
		s := sources[f.File]
		if s == nil {
			continue
		}
		off := strings.Index(string(s.masked), f.body)
		if off < 0 {
			off = 0
		}
		ix.Sites = append(ix.Sites, sites(f, s, off, acctByName)...)
		if remainingRE.MatchString(f.body) {
			ix.Remaining = true
		}
	}
	if ix.Reach == nil {
		ix.Reach = []string{}
	}
	if acctByName == nil && len(reached) > 0 {
		ix.Accounts = nativeAccounts(reached)
	}
	// A CpiContext built in one function and signed in another (.with_signer)
	// is a signed CPI for the instruction: the sites in the signing function
	// are marked, else every unsigned context in reach.
	if signing := signingFunctions(reached); len(signing) > 0 {
		marked := false
		for i := range ix.Sites {
			st := &ix.Sites[i]
			if st.Kind == "cpi" && st.Op == "cpi_context" && signing[st.Function] {
				st.Signed, st.Op, marked = true, "cpi_signer", true
			}
		}
		if !marked {
			for i := range ix.Sites {
				st := &ix.Sites[i]
				if st.Kind == "cpi" && st.Op == "cpi_context" {
					st.Signed, st.Op = true, "cpi_signer"
				}
			}
		}
	}
	ix.Counts = Counts{Functions: len(ix.Reach), Accounts: len(ix.Accounts)}
	for _, a := range ix.Accounts {
		if a.Raw {
			ix.Counts.Raw++
		}
	}
	for _, st := range ix.Sites {
		switch st.Kind {
		case "cpi":
			if st.Op == "anchor_cpi" {
				continue // counted through its CpiContext
			}
			ix.Counts.CPI++
			if st.Signed {
				ix.Counts.SignedCPI++
			}
		case "pda":
			ix.Counts.PDA++
		case "token":
			ix.Counts.Token++
		case "lamports":
			ix.Counts.Lamports++
		case "unsafe":
			ix.Counts.Unsafe++
		case "realloc":
			ix.Counts.Realloc++
		case "serde":
			ix.Counts.Serde++
		}
	}
	return ix
}

// signingFunctions names the reached functions that call .with_signer.
func signingFunctions(reached []*Function) map[string]bool {
	out := map[string]bool{}
	for _, f := range reached {
		if withSignerRE.MatchString(f.body) {
			out[f.Name] = true
		}
	}
	return out
}

func appendUnique(list []string, v string) []string {
	for _, x := range list {
		if x == v {
			return list
		}
	}
	return append(list, v)
}
