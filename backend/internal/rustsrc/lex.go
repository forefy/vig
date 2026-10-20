package rustsrc

import (
	"bytes"
	"regexp"
	"sort"
)

// source is one file after lexing: the raw bytes, a copy with comments
// blanked (clean) and a copy with comments, strings and char literals
// blanked (masked). All three are the same length, so an offset found in
// one addresses the same place in the others.
type source struct {
	path   string
	raw    []byte
	clean  []byte
	masked []byte
	starts []int // byte offset of every line start
	stats  File
}

var todoRE = regexp.MustCompile(`\b(TODO|FIXME|HACK|XXX)\b`)

// lex classifies every byte of a Rust file. It knows line comments, nested
// block comments, string literals with escapes, raw strings r#"..."#, byte
// strings and char literals, and leaves lifetimes alone.
func lex(path string, raw []byte) *source {
	n := len(raw)
	clean := make([]byte, n)
	masked := make([]byte, n)
	copy(clean, raw)
	copy(masked, raw)
	var comments []byte
	blank := func(dst []byte, i, j int) {
		for k := i; k < j && k < n; k++ {
			if dst[k] != '\n' {
				dst[k] = ' '
			}
		}
	}
	i := 0
	for i < n {
		c := raw[i]
		switch {
		case c == '/' && i+1 < n && raw[i+1] == '/':
			j := i
			for j < n && raw[j] != '\n' {
				j++
			}
			comments = append(comments, raw[i:j]...)
			comments = append(comments, '\n')
			blank(clean, i, j)
			blank(masked, i, j)
			i = j
		case c == '/' && i+1 < n && raw[i+1] == '*':
			depth := 1
			j := i + 2
			for j < n && depth > 0 {
				if raw[j] == '/' && j+1 < n && raw[j+1] == '*' {
					depth++
					j += 2
					continue
				}
				if raw[j] == '*' && j+1 < n && raw[j+1] == '/' {
					depth--
					j += 2
					continue
				}
				j++
			}
			comments = append(comments, raw[i:j]...)
			comments = append(comments, '\n')
			blank(clean, i, j)
			blank(masked, i, j)
			i = j
		case c == 'r' && i+1 < n && (raw[i+1] == '"' || raw[i+1] == '#') && (i == 0 || !isIdent(raw[i-1])):
			// raw string: r"..." or r#"..."#
			j := i + 1
			hashes := 0
			for j < n && raw[j] == '#' {
				hashes++
				j++
			}
			if j >= n || raw[j] != '"' {
				i++
				continue
			}
			j++
			start := j
			for j < n {
				if raw[j] == '"' {
					k := j + 1
					h := 0
					for k < n && raw[k] == '#' && h < hashes {
						h++
						k++
					}
					if h == hashes {
						blank(masked, start, j)
						j = k
						break
					}
				}
				j++
			}
			i = j
		case c == '"' && (i == 0 || raw[i-1] != '\'' || i+1 >= n || raw[i+1] != '\''):
			j := i + 1
			for j < n && raw[j] != '"' {
				if raw[j] == '\\' {
					j++
				}
				j++
			}
			blank(masked, i+1, j)
			i = j + 1
		case c == '\'':
			// char literal 'x', '\n', '\'' or '\u{..}'; lifetimes are 'name.
			if i+2 < n && raw[i+1] != '\\' && raw[i+2] == '\'' {
				blank(masked, i+1, i+2)
				i += 3
				continue
			}
			if i+1 < n && raw[i+1] == '\\' {
				j := i + 2
				for j < n && raw[j] != '\'' && j-i < 12 {
					j++
				}
				if j < n && raw[j] == '\'' {
					blank(masked, i+1, j)
					i = j + 1
					continue
				}
			}
			i++
		default:
			i++
		}
	}
	s := &source{path: path, raw: raw, clean: clean, masked: masked}
	s.starts = append(s.starts, 0)
	for k, b := range raw {
		if b == '\n' && k+1 <= n {
			s.starts = append(s.starts, k+1)
		}
	}
	s.stats = File{Path: path}
	rawLines := bytes.Split(raw, []byte("\n"))
	cleanLines := bytes.Split(clean, []byte("\n"))
	if len(rawLines) > 0 && len(rawLines[len(rawLines)-1]) == 0 {
		rawLines = rawLines[:len(rawLines)-1]
		cleanLines = cleanLines[:len(cleanLines)-1]
	}
	s.stats.Lines = len(rawLines)
	for k := range rawLines {
		switch {
		case len(bytes.TrimSpace(rawLines[k])) == 0:
			s.stats.Blank++
		case len(bytes.TrimSpace(cleanLines[k])) == 0:
			s.stats.Comments++
		default:
			s.stats.Code++
		}
	}
	s.stats.Todos = len(todoRE.FindAll(comments, -1))
	return s
}

// line converts a byte offset to a 1-based line number.
func (s *source) line(off int) int {
	return sort.Search(len(s.starts), func(i int) bool { return s.starts[i] > off })
}

func isIdent(b byte) bool {
	return b == '_' || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}

// closer returns the offset of the brace, bracket or paren that closes the
// one at open, or -1. It reads the masked text, so strings never confuse it.
func (s *source) closer(open int) int {
	o := s.masked[open]
	var c byte
	switch o {
	case '{':
		c = '}'
	case '(':
		c = ')'
	case '[':
		c = ']'
	case '<':
		c = '>'
	default:
		return -1
	}
	depth := 0
	for i := open; i < len(s.masked); i++ {
		switch s.masked[i] {
		case o:
			depth++
		case c:
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// CountLines classifies a file's lines the way the model does, for the
// file tree: total lines and code lines. Non-Rust files count non-blank
// lines as code. A byte 0 marks a binary and returns zeros.
func CountLines(path string, raw []byte, rustLike bool) (lines, code int) {
	if len(raw) == 0 || bytes.IndexByte(raw, 0) >= 0 {
		return 0, 0
	}
	if rustLike {
		s := lex(path, raw)
		return s.stats.Lines, s.stats.Code
	}
	for _, ln := range bytes.Split(raw, []byte("\n")) {
		lines++
		if len(bytes.TrimSpace(ln)) > 0 {
			code++
		}
	}
	if len(raw) > 0 && raw[len(raw)-1] == '\n' {
		lines--
	}
	return lines, code
}
