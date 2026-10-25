package github

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/forefy/vig/backend/internal/core"
	"github.com/forefy/vig/backend/internal/rustsrc"
)

// Tree is the file tree of a checkout with auto selection and line counts.
type Tree struct {
	Nodes         []core.TreeNode
	SelectedLines int
	SelectedCode  int
	SelectedFiles int
	TotalFiles    int
	// Lines maps every file path to its line count; Code to its code lines
	// (no blanks, no comments). The scope cap applies to Code.
	Lines map[string]int
	Code  map[string]int
}

var skipDirs = map[string]bool{".git": true, "node_modules": true, "target": true, ".github": true, "dist": true, "build": true}

var deselectNames = map[string]bool{
	".gitignore": true, "package.json": true, "yarn.lock": true, "package-lock.json": true, "pnpm-lock.yaml": true,
	"tsconfig.json": true, "Xargo.toml": true, "README.md": true, "README": true, "LICENSE": true, ".prettierrc": true,
}

var selectNames = map[string]bool{"Cargo.toml": true, "Anchor.toml": true, "Cargo.lock": true, "foundry.toml": true, "remappings.txt": true, "hardhat.config.ts": true, "hardhat.config.js": true}

// AutoSelect decides whether a file is in the default scope: Rust program
// or Solidity contract sources plus the manifests, never tests, scripts,
// vendored libraries or JS tooling.
func AutoSelect(path string) bool {
	base := filepath.Base(path)
	parts := strings.Split(path, "/")
	for _, p := range parts[:len(parts)-1] {
		if p == "tests" || p == "test" || p == "migrations" || p == "app" || p == "client" || p == "sdk" || p == "scripts" || p == "js" || p == "ts" {
			return false
		}
	}
	if deselectNames[base] {
		return false
	}
	if selectNames[base] {
		return true
	}
	if strings.HasSuffix(base, ".rs") {
		if strings.HasSuffix(base, "_test.rs") || strings.Contains(path, "/tests/") {
			return false
		}
		return strings.Contains(path, "/src/") || strings.HasPrefix(path, "src/")
	}
	if strings.HasSuffix(base, ".sol") {
		if strings.HasSuffix(base, ".t.sol") || strings.HasSuffix(base, ".s.sol") {
			return false
		}
		for _, p := range parts[:len(parts)-1] {
			if p == "lib" || p == "mocks" || p == "mock" || p == "script" {
				return false
			}
		}
		return true
	}
	return false
}

// codeExts are the source files any repository audit selects by default.
var codeExts = map[string]bool{
	".py": true, ".go": true, ".rs": true, ".ts": true, ".tsx": true, ".js": true, ".jsx": true, ".mjs": true, ".cjs": true,
	".java": true, ".kt": true, ".swift": true, ".c": true, ".h": true, ".cpp": true, ".cc": true, ".hpp": true, ".cs": true,
	".rb": true, ".php": true, ".scala": true, ".sol": true, ".move": true, ".ex": true, ".exs": true, ".erl": true, ".zig": true, ".lua": true,
}

var codeManifests = map[string]bool{"pyproject.toml": true, "setup.py": true, "setup.cfg": true, "requirements.txt": true, "go.mod": true, "Cargo.toml": true, "Gemfile": true, "composer.json": true, "build.gradle": true, "pom.xml": true}

var skipCodeDirs = map[string]bool{"tests": true, "test": true, "testing": true, "e2e": true, "examples": true, "example": true, "docs": true, "doc": true, "vendor": true, "third_party": true, "benchmarks": true, "bench": true, "fixtures": true, "scripts": true}

// AutoSelectCode is the default scope of a repository with no chain: the
// code of any language plus the manifests, never tests, docs, examples,
// vendored code or tooling.
func AutoSelectCode(path string) bool {
	base := filepath.Base(path)
	parts := strings.Split(path, "/")
	for _, p := range parts[:len(parts)-1] {
		if skipCodeDirs[p] {
			return false
		}
	}
	if deselectNames[base] {
		return false
	}
	if codeManifests[base] {
		return true
	}
	lower := strings.ToLower(base)
	if strings.HasPrefix(lower, "test_") || lower == "conftest.py" || strings.Contains(lower, "_test.") || strings.Contains(lower, ".test.") || strings.Contains(lower, ".spec.") || strings.HasSuffix(lower, "_spec.rb") {
		return false
	}
	return codeExts[strings.ToLower(filepath.Ext(base))]
}

// BuildTree walks a checkout under an optional subdirectory with the
// chain adapters' selection.
func BuildTree(root, sub string) (Tree, error) { return BuildTreeWith(root, sub, AutoSelect) }

// BuildTreeWith walks a checkout under an optional subdirectory, selecting
// files with the given rule.
func BuildTreeWith(root, sub string, selected func(path string) bool) (Tree, error) {
	// The sub path is rooted before it is cleaned, so "../.." cannot climb
	// out of the checkout: it resolves to the root itself.
	base := root
	if sub != "" {
		base = filepath.Join(root, filepath.Clean("/"+sub))
	}
	t := Tree{Lines: map[string]int{}, Code: map[string]int{}}
	type entry struct {
		path  string
		bytes int64
		lines int
		code  int
	}
	var files []entry
	err := filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(base, p)
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return nil
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return fs.SkipDir
			}
			return nil
		}
		if d.Name() == ".ok" && filepath.Dir(p) == root {
			return nil
		}
		info, err := d.Info()
		if err != nil || !info.Mode().IsRegular() {
			return nil
		}
		lines, code := 0, 0
		if info.Size() < 8<<20 {
			lines, code = countLines(p)
		}
		files = append(files, entry{path: rel, bytes: info.Size(), lines: lines, code: code})
		return nil
	})
	if err != nil {
		return t, err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].path < files[j].path })
	top := &node{dir: true}
	index := map[string]*node{"": top}
	for _, f := range files {
		t.TotalFiles++
		t.Lines[f.path] = f.lines
		t.Code[f.path] = f.code
		sel := selected(f.path)
		if sel {
			t.SelectedFiles++
			t.SelectedLines += f.lines
			t.SelectedCode += f.code
		}
		parts := strings.Split(f.path, "/")
		cur := ""
		parent := top
		for _, part := range parts[:len(parts)-1] {
			if cur == "" {
				cur = part
			} else {
				cur += "/" + part
			}
			n, ok := index[cur]
			if !ok {
				n = &node{path: cur, dir: true}
				index[cur] = n
				parent.children = append(parent.children, n)
			}
			parent = n
		}
		parent.children = append(parent.children, &node{path: f.path, bytes: f.bytes, lines: f.lines, code: f.code, selected: sel})
	}
	t.Nodes = finalize(top.children)
	return t, nil
}

// MarkChanged flags the nodes whose path (relative to the tree's base) is
// in changed and narrows the auto selection to changed source files, as a
// diff review needs. A directory is changed when any child is. Paths in
// changed are repository-relative; sub is the tree's base directory.
func MarkChanged(t *Tree, changed []string, sub string) {
	set := map[string]bool{}
	prefix := ""
	if sub != "" {
		prefix = strings.Trim(sub, "/") + "/"
	}
	for _, p := range changed {
		if prefix == "" {
			set[p] = true
		} else if rel, ok := strings.CutPrefix(p, prefix); ok {
			set[rel] = true
		}
	}
	t.SelectedFiles, t.SelectedLines, t.SelectedCode = 0, 0, 0
	var walk func(nodes []core.TreeNode) (bool, bool)
	walk = func(nodes []core.TreeNode) (anyChanged, allSelected bool) {
		allSelected = len(nodes) > 0
		for i := range nodes {
			n := &nodes[i]
			if n.Dir {
				ch, all := walk(n.Children)
				n.Changed, n.Selected = ch, all
			} else {
				n.Changed = set[n.Path]
				n.Selected = n.Changed && AutoSelect(n.Path)
				if n.Selected {
					t.SelectedFiles++
					t.SelectedLines += n.Lines
					t.SelectedCode += n.Code
				}
			}
			anyChanged = anyChanged || n.Changed
			allSelected = allSelected && n.Selected
		}
		return anyChanged, allSelected
	}
	walk(t.Nodes)
}

type node struct {
	path     string
	dir      bool
	bytes    int64
	lines    int
	code     int
	selected bool
	children []*node
}

// finalize converts the graph, marking a directory selected when every
// child is and summing its lines and bytes.
func finalize(nodes []*node) []core.TreeNode {
	out := make([]core.TreeNode, 0, len(nodes))
	for _, n := range nodes {
		tn := core.TreeNode{Path: n.path, Dir: n.dir, Bytes: n.bytes, Lines: n.lines, Code: n.code, Selected: n.selected}
		if n.dir {
			tn.Children = finalize(n.children)
			all := len(tn.Children) > 0
			for _, c := range tn.Children {
				tn.Lines += c.Lines
				tn.Code += c.Code
				tn.Bytes += c.Bytes
				if !c.Selected {
					all = false
				}
			}
			tn.Selected = all
		}
		out = append(out, tn)
	}
	return out
}

// countLines returns a file's lines and code lines. Rust and the other
// C-comment languages are classified; everything else counts non-blank
// lines as code. A binary counts zero.
func countLines(p string) (int, int) {
	b, err := os.ReadFile(p)
	if err != nil {
		return 0, 0
	}
	switch strings.ToLower(filepath.Ext(p)) {
	case ".rs", ".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs", ".go", ".c", ".h", ".cpp", ".cc", ".hpp", ".cs", ".sol", ".move", ".java", ".kt", ".swift", ".scala", ".php", ".zig":
		return rustsrc.CountLines(p, b, true)
	}
	return rustsrc.CountLines(p, b, false)
}

var declareIDRE = regexp.MustCompile(`declare_id!\s*\(\s*"([1-9A-HJ-NP-Za-km-z]{32,44})"`)
var anchorTomlRE = regexp.MustCompile(`(?m)^\s*[A-Za-z0-9_-]+\s*=\s*"([1-9A-HJ-NP-Za-km-z]{32,44})"`)

// DeclaredIDs greps declare_id! and Anchor.toml program ids in a checkout.
// The map value is the file where each id was found.
func DeclaredIDs(root string) map[string]string {
	out := map[string]string{}
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return fs.SkipDir
			}
			return nil
		}
		name := d.Name()
		if !strings.HasSuffix(name, ".rs") && name != "Anchor.toml" {
			return nil
		}
		info, err := d.Info()
		if err != nil || info.Size() > 4<<20 {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		re := declareIDRE
		if name == "Anchor.toml" {
			re = anchorTomlRE
		}
		for _, m := range re.FindAllSubmatch(b, -1) {
			if _, seen := out[string(m[1])]; !seen {
				out[string(m[1])] = rel
			}
		}
		return nil
	})
	return out
}

// Segment returns the enclosing item around [start, end] in file under root:
// the function, struct, enum, impl, trait or mod whose braces contain the
// range, plus its attribute and doc lines. When the range sits at top level
// with no enclosing item, or the item is longer than maxSegment lines, it
// falls back to the range with two lines of context. The report folds the
// parts a reader did not ask for, but the code is never cut mid-block.
func Segment(root, file string, start, end int) (int, string, bool) {
	if start < 1 || file == "" {
		return 0, "", false
	}
	if end < start {
		end = start
	}
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(file)))
	if err != nil || len(b) > 4<<20 {
		return 0, "", false
	}
	lines := strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n")
	if start > len(lines) {
		return 0, "", false
	}
	if end > len(lines) {
		end = len(lines)
	}
	from, to := enclosingItem(lines, start, end)
	const maxSegment = 120
	if to-from+1 > maxSegment {
		from, to = start-2, end+2
		if from < 1 {
			from = 1
		}
		if to > len(lines) {
			to = len(lines)
		}
	}
	return from, strings.Join(lines[from-1:to], "\n"), true
}

var itemRE = regexp.MustCompile(`^\s*(pub(\([a-z]+\))?\s+)?(async\s+|unsafe\s+|const\s+)*(fn|struct|enum|impl|trait|mod|union)\b`)

// enclosingItem finds the item that contains [start, end] by brace balance.
// It ignores braces inside strings and line comments, which is enough for
// Rust and most C-like sources.
func enclosingItem(lines []string, start, end int) (int, int) {
	type open struct{ line, depthBefore int }
	depthAt := make([]int, len(lines)+2) // depth at the beginning of each line (1-based)
	var stack []open
	best := -1 // index of the innermost item opener enclosing start
	depth := 0
	openers := map[int]int{} // opener line -> matching closer line
	for i, ln := range lines {
		depthAt[i+1] = depth
		inStr := false
		for j := 0; j < len(ln); j++ {
			c := ln[j]
			if inStr {
				if c == '\\' {
					j++
				} else if c == '"' {
					inStr = false
				}
				continue
			}
			if c == '/' && j+1 < len(ln) && ln[j+1] == '/' {
				break
			}
			switch c {
			case '"':
				inStr = true
			case '{':
				stack = append(stack, open{i + 1, depth})
				depth++
			case '}':
				if depth > 0 {
					depth--
					o := stack[len(stack)-1]
					stack = stack[:len(stack)-1]
					openers[o.line] = i + 1
				}
			}
		}
	}
	depthAt[len(lines)+1] = depth
	// Find item openers whose block contains [start, end]; keep the innermost.
	for o, c := range openers {
		if o <= start && c >= end && itemRE.MatchString(lines[o-1]) {
			if best == -1 || o > best {
				best = o
			}
		}
	}
	if best == -1 {
		// Top level: take the item that starts at or just before start, if it is one.
		for i := start; i >= 1 && i >= start-3; i-- {
			if itemRE.MatchString(lines[i-1]) {
				if c, ok := openers[i]; ok {
					return leadIn(lines, i), c
				}
				return leadIn(lines, i), end
			}
		}
		from, to := start-2, end+2
		if from < 1 {
			from = 1
		}
		if to > len(lines) {
			to = len(lines)
		}
		return from, to
	}
	return leadIn(lines, best), openers[best]
}

// leadIn walks up from an item's opener over its attributes and doc comments.
func leadIn(lines []string, line int) int {
	for line > 1 {
		prev := strings.TrimSpace(lines[line-2])
		if strings.HasPrefix(prev, "#[") || strings.HasPrefix(prev, "///") || strings.HasPrefix(prev, "//!") {
			line--
			continue
		}
		break
	}
	return line
}

// AttachSnippets fills every finding's snippet, and every site's, from the
// checkout at root when it is missing. Findings without a file, or whose
// file is not in the checkout, are left as they are.
func AttachSnippets(root string, findings []core.Finding) {
	for i := range findings {
		f := &findings[i]
		if f.Snippet == nil && f.Location.File != "" {
			if from, code, ok := Segment(root, f.Location.File, f.Location.LineStart, f.Location.LineEnd); ok {
				f.Snippet = &core.Snippet{StartLine: from, Code: code}
			}
		}
		for j := range f.Sites {
			st := &f.Sites[j]
			if st.Snippet != nil || st.Location.File == "" {
				continue
			}
			if from, code, ok := Segment(root, st.Location.File, st.Location.LineStart, st.Location.LineEnd); ok {
				st.Snippet = &core.Snippet{StartLine: from, Code: code}
			}
		}
		// The code each fix fence applies to, so the page can show the
		// fence as a patch against the lines it replaces. Captured anew
		// on every submission: the fix text is what names the places.
		f.FixSites = nil
		for _, loc := range core.FixLocations(f.Fix) {
			site := core.FixSite{Location: loc}
			if from, code, ok := Segment(root, loc.File, loc.LineStart, loc.LineEnd); ok {
				site.Snippet = &core.Snippet{StartLine: from, Code: code}
			}
			f.FixSites = append(f.FixSites, site)
		}
	}
}
