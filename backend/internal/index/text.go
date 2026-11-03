package index

import (
	"strings"
	"unicode/utf8"
)

// clean makes a string fit for a Parquet column: valid UTF-8, and at most
// max runes when max is set. A cut never lands inside a rune.
func clean(s string, max int) string {
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "\uFFFD")
	}
	if max > 0 && len(s) > max {
		n := 0
		for i := range s {
			if n == max {
				return s[:i]
			}
			n++
		}
	}
	return s
}
