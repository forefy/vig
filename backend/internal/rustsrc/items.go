package rustsrc

import (
	"regexp"
	"strings"
)

// span is one braced item in a file.
type span struct {
	open, close int // offsets of { and }
	line        int // where the item starts
}

var (
	fnRE     = regexp.MustCompile(`(?m)^[ \t]*(?:pub(?:\([^)]*\))?[ \t]+)?(?:(?:async|unsafe|const|extern[ \t]+"[^"]*")[ \t]+)*fn[ \t]+([A-Za-z_][A-Za-z0-9_]*)`)
	implRE   = regexp.MustCompile(`(?m)^[ \t]*(?:unsafe[ \t]+)?impl(?:<[^>{]*>)?[ \t]+(?:(?:[A-Za-z_][A-Za-z0-9_:]*)(?:<[^>{]*>)?[ \t]+for[ \t]+)?([A-Za-z_][A-Za-z0-9_:]*)`)
	modRE    = regexp.MustCompile(`(?m)^[ \t]*(?:pub(?:\([^)]*\))?[ \t]+)?mod[ \t]+([A-Za-z_][A-Za-z0-9_]*)[ \t]*\{`)
	structRE = regexp.MustCompile(`(?m)^[ \t]*(?:pub(?:\([^)]*\))?[ \t]+)?struct[ \t]+([A-Za-z_][A-Za-z0-9_]*)`)
)

// body finds the block that follows a signature starting at off: the first
// { at zero paren and angle depth before a ;. ok is false for a bodiless
// declaration.
func (s *source) body(off int) (open, close int, ok bool) {
	paren, angle := 0, 0
	for i := off; i < len(s.masked); i++ {
		switch s.masked[i] {
		case '(':
			paren++
		case ')':
			paren--
		case '<':
			if paren == 0 {
				angle++
			}
		case '>':
			if paren == 0 && angle > 0 && !(i > 0 && s.masked[i-1] == '-') {
				angle--
			}
		case ';':
			if paren == 0 && angle == 0 {
				return 0, 0, false
			}
		case '{':
			if paren == 0 {
				c := s.closer(i)
				if c < 0 {
					return 0, 0, false
				}
				return i, c, true
			}
		}
	}
	return 0, 0, false
}

// attributes returns the attributes right above the item whose line
// starts at off, as one string, and the offset where the first one starts.
// It walks upward: the previous non-blank text must end with ] and that
// bracket must open with #[ or #![; doc comments are blank in the masked
// text, so they are stepped over.
func (s *source) attributes(off int) (string, int) {
	text := ""
	start := off
	for {
		i := start - 1
		for i >= 0 && (s.masked[i] == '\n' || s.masked[i] == ' ' || s.masked[i] == '\t' || s.masked[i] == '\r') {
			i--
		}
		if i < 0 || s.masked[i] != ']' {
			break
		}
		depth := 0
		j := i
		for ; j >= 0; j-- {
			switch s.masked[j] {
			case ']':
				depth++
			case '[':
				depth--
			}
			if depth == 0 {
				break
			}
		}
		if j <= 0 {
			break
		}
		switch {
		case s.masked[j-1] == '#':
			j--
		case j >= 2 && s.masked[j-1] == '!' && s.masked[j-2] == '#':
			j -= 2
		default:
			return text, start
		}
		text = string(s.clean[j:i+1]) + "\n" + text
		start = j
	}
	return text, start
}

// functions finds every fn with a body and its enclosing impl type.
func (s *source) functions(crate string) []*Function {
	type implSpan struct {
		typ  string
		open int
		end  int
	}
	var impls []implSpan
	for _, m := range implRE.FindAllSubmatchIndex(s.masked, -1) {
		open, close, ok := s.body(m[1])
		if !ok {
			continue
		}
		typ := string(s.masked[m[2]:m[3]])
		if i := strings.LastIndex(typ, "::"); i >= 0 {
			typ = typ[i+2:]
		}
		impls = append(impls, implSpan{typ: typ, open: open, end: close})
	}
	var out []*Function
	for _, m := range fnRE.FindAllSubmatchIndex(s.masked, -1) {
		open, close, ok := s.body(m[1])
		if !ok {
			continue
		}
		f := &Function{Name: string(s.masked[m[2]:m[3]]), File: s.path, Line: s.line(m[2]), End: s.line(close), Crate: crate}
		for _, im := range impls {
			if im.open < m[0] && im.end > close {
				f.Impl = im.typ
			}
		}
		f.body = string(s.masked[open : close+1])
		f.text = string(s.clean[open : close+1])
		f.Calls = calls(f.body, s, open)
		out = append(out, f)
	}
	return out
}

var callRE = regexp.MustCompile(`([A-Za-z_][A-Za-z0-9_]*(?:(?:::|\.)[A-Za-z_][A-Za-z0-9_]*)*)[ \t]*\(`)

var keywords = map[string]bool{"if": true, "while": true, "match": true, "for": true, "return": true, "loop": true, "as": true, "in": true, "let": true, "fn": true, "impl": true, "unsafe": true, "move": true, "else": true, "where": true, "await": true, "break": true, "continue": true, "mut": true, "ref": true, "dyn": true, "use": true, "pub": true, "mod": true, "struct": true, "enum": true, "type": true, "trait": true, "const": true, "static": true}

// calls lists call expressions in a body. Macros (name!) never match, and
// names starting with a capital are constructors or variants, not calls,
// except paths that end in a lowercase segment such as Pubkey::find_program_address.
func calls(body string, s *source, base int) []Call {
	var out []Call
	for _, m := range callRE.FindAllStringSubmatchIndex(body, -1) {
		full := body[m[2]:m[3]]
		if m[2] > 0 && (body[m[2]-1] == '!' || isIdent(body[m[2]-1])) {
			continue
		}
		name, path, method := full, "", false
		if i := strings.LastIndexAny(full, ":."); i >= 0 {
			name = full[i+1:]
			path = strings.TrimRight(full[:i], ":")
			method = full[i] == '.'
		}
		if keywords[name] || name == "" || name[0] >= 'A' && name[0] <= 'Z' {
			continue
		}
		out = append(out, Call{Name: name, Path: path, Method: method, Line: s.line(base + m[2])})
	}
	return out
}

var constRE = regexp.MustCompile(`(?m)^[ \t]*(?:pub(?:\([^)]*\))?[ \t]+)?(?:const|static)[ \t]+([A-Z_][A-Z0-9_]*)[ \t]*:[ \t]*&(?:'static[ \t]+)?(?:\[u8\]|str|\[u8;[ \t]*\d+\])[ \t]*=[ \t]*b?"`)

// consts lists the byte and string constants a file declares, with their
// literal bytes. Offsets are taken on the masked text, where strings are
// blanked, and the bytes read from the clean text.
func (s *source) consts() map[string][]byte {
	out := map[string][]byte{}
	for _, m := range constRE.FindAllSubmatchIndex(s.masked, -1) {
		start := m[1] // just after the opening quote
		end := start
		for end < len(s.clean) && s.clean[end] != '"' {
			if s.clean[end] == '\\' {
				end++
			}
			end++
		}
		if end > len(s.clean) {
			continue
		}
		out[string(s.masked[m[2]:m[3]])] = unescapeLit(string(s.clean[start:end]))
	}
	return out
}

func unescapeLit(in string) []byte {
	if !strings.Contains(in, `\`) {
		return []byte(in)
	}
	var b []byte
	for i := 0; i < len(in); i++ {
		if in[i] == '\\' && i+1 < len(in) {
			i++
			switch in[i] {
			case 'n':
				b = append(b, '\n')
			case 't':
				b = append(b, '\t')
			case '0':
				b = append(b, 0)
			default:
				b = append(b, in[i])
			}
			continue
		}
		b = append(b, in[i])
	}
	return b
}

// mods lists braced modules with their spans.
func (s *source) mods() map[string]span {
	out := map[string]span{}
	for _, m := range modRE.FindAllSubmatchIndex(s.masked, -1) {
		open := m[1] - 1
		c := s.closer(open)
		if c < 0 {
			continue
		}
		out[string(s.masked[m[2]:m[3]])] = span{open: open, close: c, line: s.line(m[2])}
	}
	return out
}

// snake converts CamelCase to snake_case, leaving snake input as is.
func snake(in string) string {
	var b strings.Builder
	for i, r := range in {
		if r >= 'A' && r <= 'Z' {
			if i > 0 && (in[i-1] < 'A' || in[i-1] > 'Z') && in[i-1] != '_' {
				b.WriteByte('_')
			}
			b.WriteRune(r + ('a' - 'A'))
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// splitTop splits text on a separator at zero bracket depth.
func splitTop(text string, sep byte) []string {
	var out []string
	depth := 0
	start := 0
	for i := 0; i < len(text); i++ {
		switch text[i] {
		case '(', '[', '{', '<':
			depth++
		case ')', ']', '}', '>':
			if text[i] == '>' && i > 0 && text[i-1] == '-' {
				continue
			}
			depth--
		case sep:
			if depth == 0 {
				out = append(out, text[start:i])
				start = i + 1
			}
		}
	}
	if strings.TrimSpace(text[start:]) != "" {
		out = append(out, text[start:])
	}
	return out
}

var spaceRE = regexp.MustCompile(`\s+`)

func tidy(s string) string {
	return strings.TrimSpace(spaceRE.ReplaceAllString(s, " "))
}
