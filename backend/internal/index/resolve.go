package index

import (
	"regexp"
	"strings"
)

var paramHead = regexp.MustCompile(`^[\w$]+`)

func paramNames(params []string) []string {
	out := make([]string, 0, len(params))
	for _, p := range params {
		p = strings.TrimLeft(p, "*&")
		out = append(out, paramHead.FindString(p))
	}
	return out
}

// simpleName is the last segment of a callee: `p.getUser` is getUser,
// `std::env::var` is var, `println!` is println.
func simpleName(callee string) string {
	// Drop every parenthesised group, so `r.HandleFunc("/x", h).Methods`
	// ends in Methods and `Router::new().route` in route.
	var sb strings.Builder
	depth := 0
	for _, r := range callee {
		switch {
		case r == '(':
			depth++
		case r == ')':
			if depth > 0 {
				depth--
			}
		case depth == 0:
			sb.WriteRune(r)
		}
	}
	s := sb.String()
	if i := strings.Index(s, "<"); i > 0 && !strings.ContainsAny(s[:i], ".:") {
		s = s[:i]
	}
	for _, sep := range []string{"::", "->", "."} {
		if i := strings.LastIndex(s, sep); i >= 0 {
			s = s[i+len(sep):]
		}
	}
	return strings.TrimSuffix(strings.TrimSpace(s), "!")
}
