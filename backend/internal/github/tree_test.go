package github

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/forefy/vig/backend/internal/core"
)

func TestAutoSelect(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"programs/x/src/lib.rs", true},
		{"contracts/src/Orderbook.sol", true},
		{"src/Vault.sol", true},
		{"contracts/src/interfaces/IVault.sol", true},
		{"foundry.toml", true},
		{"test/Vault.t.sol", false},
		{"contracts/test/Vault.t.sol", false},
		{"script/Deploy.s.sol", false},
		{"lib/openzeppelin/contracts/token/ERC20.sol", false},
		{"contracts/src/mocks/MockToken.sol", false},
		{"programs/x/src/instructions/deposit.rs", true},
		{"src/lib.rs", true},
		{"programs/x/Cargo.toml", true},
		{"Cargo.toml", true},
		{"Cargo.lock", true},
		{"Anchor.toml", true},
		{"programs/x/Xargo.toml", false},
		{"programs/x/src/lib_test.rs", false},
		{"programs/x/src/tests/mod.rs", false},
		{"programs/x/tests/integration.rs", false},
		{"tests/x.ts", false},
		{"tests/Cargo.toml", false},
		{"test/lib.rs", false},
		{"app/src/main.rs", false},
		{"client/src/lib.rs", false},
		{"sdk/src/lib.rs", false},
		{"scripts/src/x.rs", false},
		{"js/src/index.ts", false},
		{"ts/src/index.ts", false},
		{"migrations/deploy.ts", false},
		{"programs/x/build.rs", false},
		{"programs/x/README.md", false},
		{"README.md", false},
		{"README", false},
		{"LICENSE", false},
		{"package.json", false},
		{"yarn.lock", false},
		{"package-lock.json", false},
		{"pnpm-lock.yaml", false},
		{"tsconfig.json", false},
		{".gitignore", false},
		{".prettierrc", false},
		{"programs/x/src/main.ts", false},
		{"docs/design.md", false},
		{"lib.rs", false},
		{"programs/x/benches/bench.rs", false},
		{"src-tauri/lib.rs", false},
	}
	for _, c := range cases {
		if got := AutoSelect(c.path); got != c.want {
			t.Errorf("AutoSelect(%q)=%v want %v", c.path, got, c.want)
		}
	}
}

// anchorWorkspace lays out a small Anchor repo. Returns the root.
func anchorWorkspace(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"Anchor.toml":                      "[toolchain]\n[features]\nresolution = true\n[programs.localnet]\nvault = \"Vau1t111111111111111111111111111111111111111\"\n[provider]\ncluster = \"localnet\"\n",
		"Cargo.toml":                       "[workspace]\nmembers = [\"programs/*\"]\n",
		"Cargo.lock":                       "# lock\nversion = 3\n",
		"README.md":                        "# vault\n",
		"package.json":                     "{}\n",
		"programs/vault/Cargo.toml":        "[package]\nname = \"vault\"\n",
		"programs/vault/Xargo.toml":        "[target.bpfel-unknown-unknown.dependencies.std]\n",
		"programs/vault/src/lib.rs":        "use anchor_lang::prelude::*;\n\ndeclare_id!(\"Vau1t111111111111111111111111111111111111111\");\n\n#[program]\npub mod vault {}\n",
		"programs/vault/src/state.rs":      "pub struct Vault;\nimpl Vault {}\n",
		"programs/vault/src/lib_test.rs":   "#[test]\nfn t() {}\n",
		"programs/tiny/src/lib.rs":         "declare_id!(\"T1ny1111111111111111111111111111111111111111\");\n",
		"tests/vault.ts":                   "describe('vault', () => {});\n",
		"app/src/index.ts":                 "console.log('app');\n",
		"migrations/deploy.ts":             "export default async () => {};\n",
		"target/deploy/vault.so":           "\x7fELF\x00\x00",
		".git/HEAD":                        "ref: refs/heads/main\n",
		"node_modules/x/index.js":          "module.exports = 1;\n",
		"assets/logo.bin":                  "PNG\x00\x00\x01\x02",
		".ok":                              "sha",
		"programs/vault/src/no_newline.rs": "fn a() {}\nfn b() {}",
	}
	for p, body := range files {
		full := filepath.Join(root, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func findNode(nodes []core.TreeNode, path string) *core.TreeNode {
	for i := range nodes {
		if nodes[i].Path == path {
			return &nodes[i]
		}
		if n := findNode(nodes[i].Children, path); n != nil {
			return n
		}
	}
	return nil
}

func TestBuildTreeAnchorWorkspace(t *testing.T) {
	root := anchorWorkspace(t)
	tree, err := BuildTree(root, "")
	if err != nil {
		t.Fatal(err)
	}

	// target/, .git/, node_modules/ and the root .ok marker are invisible.
	for _, hidden := range []string{"target/deploy/vault.so", ".git/HEAD", "node_modules/x/index.js", ".ok"} {
		if _, ok := tree.Lines[hidden]; ok {
			t.Errorf("%s must be skipped", hidden)
		}
	}
	wantLines := map[string]int{
		"Anchor.toml":                      7,
		"Cargo.toml":                       2,
		"Cargo.lock":                       2,
		"README.md":                        1,
		"package.json":                     1,
		"programs/vault/Cargo.toml":        2,
		"programs/vault/Xargo.toml":        1,
		"programs/vault/src/lib.rs":        6,
		"programs/vault/src/state.rs":      2,
		"programs/vault/src/lib_test.rs":   2,
		"programs/vault/src/no_newline.rs": 2,
		"programs/tiny/src/lib.rs":         1,
		"tests/vault.ts":                   1,
		"app/src/index.ts":                 1,
		"migrations/deploy.ts":             1,
		"assets/logo.bin":                  0, // binary: NUL byte
	}
	if tree.TotalFiles != len(wantLines) {
		t.Fatalf("TotalFiles %d want %d (%v)", tree.TotalFiles, len(wantLines), tree.Lines)
	}
	for p, n := range wantLines {
		if got, ok := tree.Lines[p]; !ok || got != n {
			t.Errorf("Lines[%q]=%d (present=%v) want %d", p, got, ok, n)
		}
	}

	selected := []string{"Anchor.toml", "Cargo.toml", "Cargo.lock", "programs/vault/Cargo.toml", "programs/vault/src/lib.rs", "programs/vault/src/state.rs", "programs/vault/src/no_newline.rs", "programs/tiny/src/lib.rs"}
	sumLines := 0
	for _, p := range selected {
		sumLines += wantLines[p]
		n := findNode(tree.Nodes, p)
		if n == nil || !n.Selected || n.Dir {
			t.Errorf("%s must be a selected file node (%+v)", p, n)
		}
	}
	if tree.SelectedFiles != len(selected) || tree.SelectedLines != sumLines {
		t.Fatalf("SelectedFiles=%d SelectedLines=%d want %d/%d", tree.SelectedFiles, tree.SelectedLines, len(selected), sumLines)
	}
	for _, p := range []string{"README.md", "package.json", "programs/vault/Xargo.toml", "programs/vault/src/lib_test.rs", "tests/vault.ts", "app/src/index.ts", "migrations/deploy.ts", "assets/logo.bin"} {
		if n := findNode(tree.Nodes, p); n == nil || n.Selected {
			t.Errorf("%s must be present and deselected", p)
		}
	}

	// Folder tri-state: a folder is selected only when every descendant is.
	folders := map[string]bool{
		"programs":           false, // vault has deselected children
		"programs/vault":     false,
		"programs/vault/src": false, // lib_test.rs
		"programs/tiny":      true,
		"programs/tiny/src":  true,
		"tests":              false,
		"app":                false,
		"app/src":            false,
		"assets":             false,
	}
	for p, want := range folders {
		n := findNode(tree.Nodes, p)
		if n == nil || !n.Dir {
			t.Errorf("%s must be a directory node", p)
			continue
		}
		if n.Selected != want {
			t.Errorf("%s selected=%v want %v", p, n.Selected, want)
		}
	}

	// Folder lines and bytes are sums of their children.
	src := findNode(tree.Nodes, "programs/vault/src")
	if src.Lines != 6+2+2+2 {
		t.Fatalf("programs/vault/src lines %d", src.Lines)
	}
	if src.Bytes == 0 {
		t.Fatal("folder bytes must be summed")
	}
	vault := findNode(tree.Nodes, "programs/vault")
	if vault.Lines != src.Lines+2+1 {
		t.Fatalf("programs/vault lines %d", vault.Lines)
	}
	programs := findNode(tree.Nodes, "programs")
	if programs.Lines != vault.Lines+1 {
		t.Fatalf("programs lines %d", programs.Lines)
	}
	if len(programs.Children) != 2 || programs.Children[0].Path != "programs/tiny" || programs.Children[1].Path != "programs/vault" {
		t.Fatalf("children must be sorted: %+v", programs.Children)
	}

	// Top-level ordering is lexical and every top-level node appears once.
	var top []string
	for _, n := range tree.Nodes {
		top = append(top, n.Path)
	}
	want := []string{"Anchor.toml", "Cargo.lock", "Cargo.toml", "README.md", "app", "assets", "migrations", "package.json", "programs", "tests"}
	if strings.Join(top, ",") != strings.Join(want, ",") {
		t.Fatalf("top-level %v want %v", top, want)
	}
}

func TestBuildTreeSubdirectory(t *testing.T) {
	root := anchorWorkspace(t)
	tree, err := BuildTree(root, "programs/vault")
	if err != nil {
		t.Fatal(err)
	}
	// Paths are relative to the subdirectory and src/ files still auto-select.
	if tree.TotalFiles != 6 {
		t.Fatalf("TotalFiles %d: %v", tree.TotalFiles, tree.Lines)
	}
	if n := findNode(tree.Nodes, "src/lib.rs"); n == nil || !n.Selected {
		t.Fatalf("src/lib.rs must be selected: %+v", n)
	}
	if n := findNode(tree.Nodes, "Xargo.toml"); n == nil || n.Selected {
		t.Fatal("Xargo.toml must be deselected")
	}
	if tree.SelectedFiles != 4 || tree.SelectedLines != 2+6+2+2 {
		t.Fatalf("selected %d files %d lines", tree.SelectedFiles, tree.SelectedLines)
	}
	if _, ok := tree.Lines["programs/vault/src/lib.rs"]; ok {
		t.Fatal("paths must be relative to the sub directory")
	}
	// Traversal in sub is neutralised: it resolves to the root itself.
	whole, err := BuildTree(root, "")
	if err != nil {
		t.Fatal(err)
	}
	escaped, err := BuildTree(root, "../../..")
	if err != nil {
		t.Fatal(err)
	}
	if escaped.TotalFiles != whole.TotalFiles {
		t.Fatalf("../../.. walked %d files, the root has %d", escaped.TotalFiles, whole.TotalFiles)
	}
}

func TestBuildTreeMissingRoot(t *testing.T) {
	tree, err := BuildTree(filepath.Join(t.TempDir(), "nope"), "")
	if err != nil {
		t.Fatal("walk errors are swallowed")
	}
	if tree.TotalFiles != 0 || len(tree.Nodes) != 0 {
		t.Fatalf("empty tree expected: %+v", tree)
	}
}

func TestFinalizeTriState(t *testing.T) {
	out := finalize([]*node{
		{path: "empty", dir: true},
		{path: "all", dir: true, children: []*node{{path: "all/a.rs", lines: 1, bytes: 10, selected: true}, {path: "all/b.rs", lines: 2, bytes: 20, selected: true}}},
		{path: "mixed", dir: true, children: []*node{{path: "mixed/a.rs", lines: 1, selected: true}, {path: "mixed/b.ts", lines: 5}}},
		{path: "nested", dir: true, children: []*node{{path: "nested/in", dir: true, children: []*node{{path: "nested/in/a.rs", lines: 3, selected: true}}}}},
	})
	if len(out) != 4 {
		t.Fatalf("len %d", len(out))
	}
	if out[0].Selected || !out[0].Dir || out[0].Lines != 0 {
		t.Fatalf("empty dir: %+v", out[0])
	}
	if !out[1].Selected || out[1].Lines != 3 || out[1].Bytes != 30 || len(out[1].Children) != 2 {
		t.Fatalf("all: %+v", out[1])
	}
	if out[2].Selected || out[2].Lines != 6 {
		t.Fatalf("mixed: %+v", out[2])
	}
	if !out[3].Selected || out[3].Lines != 3 || !out[3].Children[0].Selected {
		t.Fatalf("nested: %+v", out[3])
	}
}

func TestCountLines(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name string
		body string
		want int
	}{
		{"empty", "", 0},
		{"one line no newline", "a", 1},
		{"one line", "a\n", 1},
		{"two lines", "a\nb\n", 2},
		{"two lines no trailing newline", "a\nb", 2},
		{"blank lines count", "\n\n\n", 3},
		{"crlf", "a\r\nb\r\n", 2},
		{"binary", "abc\x00def\n", 0},
		{"binary later", "line\nline\n\x00\n", 0},
	}
	for _, c := range cases {
		p := filepath.Join(dir, strings.ReplaceAll(c.name, " ", "_"))
		os.WriteFile(p, []byte(c.body), 0o644)
		if got, _ := countLines(p); got != c.want {
			t.Errorf("%s: %d want %d", c.name, got, c.want)
		}
	}
	if n, _ := countLines(filepath.Join(dir, "missing")); n != 0 {
		t.Error("missing file counts as zero")
	}
	// Code lines drop blanks and comments in Rust.
	rs := filepath.Join(dir, "code.rs")
	os.WriteFile(rs, []byte("// c\nfn a() {}\n\n/* b\n */\nfn b() {}\n"), 0o644)
	if lines, code := countLines(rs); lines != 6 || code != 2 {
		t.Errorf("rust: %d lines %d code", lines, code)
	}
}

func TestDeclaredIDs(t *testing.T) {
	root := anchorWorkspace(t)
	extra := map[string]string{
		"programs/vault/src/other.rs":   "// spaced form\ndeclare_id! ( \"Spaced11111111111111111111111111111111111111\" );\n",
		"programs/vault/src/short.rs":   "declare_id!(\"tooshort\");\n",
		"app/src/ids.ts":                "declare_id!(\"NotRust1111111111111111111111111111111111111\")\n",
		"node_modules/x/lib.rs":         "declare_id!(\"NodeMod1111111111111111111111111111111111111\");\n",
		"target/x.rs":                   "declare_id!(\"Target11111111111111111111111111111111111111\");\n",
		"programs/tiny/Anchor.toml":     "[programs.mainnet]\ntiny_mainnet = \"T1ny1111111111111111111111111111111111111111\"\nnot_an_id = \"hello\"\n",
		"programs/vault/src/dupe.rs":    "declare_id!(\"Vau1t111111111111111111111111111111111111111\");\n",
		"programs/vault/src/comment.rs": "# hash = \"Comment1111111111111111111111111111111111111\"\n",
	}
	for p, body := range extra {
		full := filepath.Join(root, filepath.FromSlash(p))
		os.MkdirAll(filepath.Dir(full), 0o755)
		os.WriteFile(full, []byte(body), 0o644)
	}
	ids := DeclaredIDs(root)
	want := map[string]string{
		"Vau1t111111111111111111111111111111111111111": "Anchor.toml", // Anchor.toml walks before programs/ and wins the dedupe
		"T1ny1111111111111111111111111111111111111111": "programs/tiny/Anchor.toml",
		"Spaced11111111111111111111111111111111111111": "programs/vault/src/other.rs",
	}
	for id, file := range want {
		if got, ok := ids[id]; !ok || got != file {
			t.Errorf("ids[%s]=%q (present=%v) want %q", id, got, ok, file)
		}
	}
	for _, absent := range []string{"tooshort", "NotRust1111111111111111111111111111111111111", "NodeMod1111111111111111111111111111111111111", "Target11111111111111111111111111111111111111", "hello"} {
		if _, ok := ids[absent]; ok {
			t.Errorf("%s must not be reported", absent)
		}
	}
	if len(ids) != len(want)+1 { // +1 for the Comment key matched by the permissive Anchor.toml-free .rs? no: comment.rs uses declare_id? it does not
		// comment.rs has no declare_id!, so it contributes nothing; the count is exactly want.
		if len(ids) != len(want) {
			t.Fatalf("unexpected ids: %v", ids)
		}
	}
	if DeclaredIDs(filepath.Join(root, "does-not-exist")) == nil {
		t.Fatal("missing root returns an empty, non-nil map")
	}
}

func TestAutoSelectCode(t *testing.T) {
	yes := []string{"gunicorn/app/base.py", "gunicorn/http/parser.py", "setup.py", "pyproject.toml", "cmd/vig/main.go", "src/index.ts", "lib/server.rb"}
	no := []string{"tests/test_http.py", "gunicorn/workers/conftest.py", "docs/source/conf.py", "examples/echo.py", "vendor/lib/x.go", "src/server_test.go", "src/app.test.ts", "scripts/release.py", "README.md", "package.json"}
	for _, p := range yes {
		if !AutoSelectCode(p) {
			t.Errorf("%s must be selected", p)
		}
	}
	for _, p := range no {
		if AutoSelectCode(p) {
			t.Errorf("%s must be left out", p)
		}
	}
}
