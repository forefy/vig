package rustsrc

import (
	"regexp"
	"strings"
)

var (
	entrypointRE = regexp.MustCompile(`entrypoint!\s*\(\s*([A-Za-z_][A-Za-z0-9_:]*)\s*\)`)
	matchRE      = regexp.MustCompile(`\bmatch\b[^{]*\{`)
	armRE        = regexp.MustCompile(`(?m)^[ \t]*(?:[A-Za-z_][A-Za-z0-9_]*::)*([A-Z][A-Za-z0-9_]*)\s*(?:\{[^}]*\}|\([^)]*\))?\s*(?:\|[^=]*)?=>`)
	nextAcctRE   = regexp.MustCompile(`let\s+(?:mut\s+)?([A-Za-z_][A-Za-z0-9_]*)\s*=\s*next_account_info\s*\(`)
	isSignerRE   = regexp.MustCompile(`\b([A-Za-z_][A-Za-z0-9_]*)\s*\.\s*is_signer\b`)
	isWritableRE = regexp.MustCompile(`\b([A-Za-z_][A-Za-z0-9_]*)\s*\.\s*is_writable\b`)
)

// nativeInstructions finds the dispatch of a native program: the entrypoint
// function, the first match on an instruction enum it contains or calls
// into (one hop), and one instruction per arm. Each arm's calls seed its
// reach. Accounts are the next_account_info bindings the arm's functions
// read, in order, with signer and writable set when the code checks them.
func nativeInstructions(entry *Function, fns map[string][]*Function, sources map[string]*source) []Instruction {
	if entry == nil {
		return nil
	}
	cands := []*Function{entry}
	for _, c := range entry.Calls {
		for _, f := range fns[c.Name] {
			if len(fns[c.Name]) == 1 {
				cands = append(cands, f)
			}
		}
	}
	for _, f := range cands {
		if arms := dispatchArms(f, sources[f.File]); len(arms) > 0 {
			return arms
		}
	}
	return nil
}

// dispatchArms reads the first match whose arms are enum variants and
// returns one instruction per arm with the arm's calls attached as the
// instruction's own call list, in the Reach seed position.
func dispatchArms(f *Function, s *source) []Instruction {
	if s == nil {
		return nil
	}
	loc := matchRE.FindStringIndex(f.body)
	if loc == nil {
		return nil
	}
	open := loc[1] - 1
	depth, close := 0, -1
	for i := open; i < len(f.body); i++ {
		switch f.body[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				close = i
				i = len(f.body)
			}
		}
	}
	if close < 0 {
		return nil
	}
	body := f.body[open+1 : close]
	arms := armRE.FindAllStringSubmatchIndex(body, -1)
	if len(arms) == 0 {
		return nil
	}
	// The function's start offset in the file, to turn body offsets into lines.
	fileOff := strings.Index(string(s.masked), f.body)
	var out []Instruction
	for i, m := range arms {
		end := len(body)
		if i+1 < len(arms) {
			end = arms[i+1][0]
		}
		expr := body[m[1]:end]
		name := snake(body[m[2]:m[3]])
		ix := Instruction{Name: name, File: f.File, Context: f.Name}
		if fileOff >= 0 {
			ix.Line = s.line(fileOff + open + 1 + m[2])
		}
		// Calls in the arm expression, as a synthetic seed function.
		seed := &Function{Name: "arm:" + name, File: f.File, Crate: f.Crate, body: expr}
		if fileOff >= 0 {
			seed.Calls = calls(expr, s, fileOff+open+1+m[1])
		}
		ix.Reach = []string{key(seed)}
		armSeeds[key(seed)] = seed
		out = append(out, ix)
	}
	return out
}

// armSeeds holds synthetic functions for match arms so the graph walk can
// start from them. Keyed like every other function.
var armSeeds = map[string]*Function{}

// nativeAccounts lists next_account_info bindings across the reached
// functions, first seen first, and marks the ones the code checks.
func nativeAccounts(reach []*Function) []Account {
	var out []Account
	idx := map[string]int{}
	signer, writable := map[string]bool{}, map[string]bool{}
	for _, f := range reach {
		for _, m := range nextAcctRE.FindAllStringSubmatch(f.body, -1) {
			if _, ok := idx[m[1]]; ok {
				continue
			}
			idx[m[1]] = len(out)
			out = append(out, Account{Name: strings.TrimSuffix(strings.TrimSuffix(m[1], "_info"), "_account_info"), Wrapper: "AccountInfo", Raw: true, Line: f.Line})
		}
		for _, m := range isSignerRE.FindAllStringSubmatch(f.body, -1) {
			signer[m[1]] = true
		}
		for _, m := range isWritableRE.FindAllStringSubmatch(f.body, -1) {
			writable[m[1]] = true
		}
	}
	for name, i := range idx {
		out[i].Signer = signer[name]
		out[i].Writable = writable[name]
	}
	return out
}
