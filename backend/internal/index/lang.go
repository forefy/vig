package index

import (
	"path"
	"regexp"
	"strings"
)

// Lang is a language the extractor parses.
type Lang string

const (
	LangGo         Lang = "go"
	LangJavaScript Lang = "javascript"
	LangTypeScript Lang = "typescript"
	LangTSX        Lang = "tsx"
	LangPython     Lang = "python"
	LangRust       Lang = "rust"
	LangC          Lang = "c"
	LangCPP        Lang = "cpp"
)

// Languages lists what the extractor parses, in a stable order.
var Languages = []Lang{LangGo, LangJavaScript, LangTypeScript, LangTSX, LangPython, LangRust, LangC, LangCPP}

var byExt = map[string]Lang{
	".go": LangGo,
	".js": LangJavaScript, ".mjs": LangJavaScript, ".cjs": LangJavaScript, ".jsx": LangJavaScript,
	".ts": LangTypeScript, ".mts": LangTypeScript, ".cts": LangTypeScript,
	".tsx": LangTSX,
	".py":  LangPython,
	".rs":  LangRust,
	".c":   LangC, ".h": LangC,
	".cc": LangCPP, ".cpp": LangCPP, ".cxx": LangCPP, ".hpp": LangCPP, ".hh": LangCPP, ".hxx": LangCPP,
}

// LangOf returns the language of a path by its extension, "" for one the
// extractor does not parse.
func LangOf(p string) Lang {
	return byExt[strings.ToLower(path.Ext(p))]
}

// Directories that hold nothing of the target's own: dependencies,
// build output, version control.
var skipDirs = map[string]bool{
	".git": true, "node_modules": true, "vendor": true, "dist": true, "build": true, "out": true,
	"target": true, ".next": true, "coverage": true, "third_party": true, "thirdparty": true, "__pycache__": true,
	".venv": true, "venv": true, "bower_components": true, ".cache": true,
}

// Files that are generated or minified: parsed, they would bury the
// target's own code under its tools'.
var skipFileRe = regexp.MustCompile(`(\.min\.(js|css)$)|(\.pb\.go$)|(_generated\.go$)|(\.generated\.(ts|js)$)|(\.d\.ts$)|(\.bundle\.js$)`)

// maxFileBytes is the largest source file the extractor reads; anything
// bigger is generated or vendored.
const maxFileBytes = 2 << 20

// SkipDir reports whether a directory name is left out of the walk: the
// dependency and build folders above, and every hidden one (.git,
// .github, .claude, .cursor), which hold tooling rather than the target.
func SkipDir(name string) bool { return skipDirs[name] || strings.HasPrefix(name, ".") }

// SkipFile reports whether a file is left out by its name or size.
func SkipFile(name string, size int64) bool {
	return size > maxFileBytes || skipFileRe.MatchString(name)
}

// testPathRe marks files that hold tests; their functions are indexed,
// and the map leaves their handlers out.
var testPathRe = regexp.MustCompile(`(^|/)(test|tests|__tests__|testdata|e2e|cypress)(/|$)|_test\.go$|\.(test|spec)\.[cm]?[jt]sx?$|(^|/)test_[^/]*\.py$|_test\.py$`)

// IsTestPath reports whether a path is a test file.
func IsTestPath(p string) bool { return testPathRe.MatchString(p) }
