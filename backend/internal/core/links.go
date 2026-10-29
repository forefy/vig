package core

import (
	"fmt"
	"regexp"
	"strings"
)

// A finding may point at another one in its prose as [[Title of that
// finding]]; the page sets it as a link to it. The target is the title,
// which is also what keeps a finding across revisions, so a link survives
// renumbering and dies with the finding it names: when no finding in the
// document carries that title, vig drops the brackets and keeps the words.
// A link is for a finding the reader has to read first, not a see-also;
// the dry run says so on every finding that carries one.

var linkRE = regexp.MustCompile(`\[\[([^\[\]\n]+)\]\]`)

// linkKey is how two titles are compared: case and runs of space aside.
func linkKey(title string) string {
	return strings.ToLower(strings.Join(strings.Fields(title), " "))
}

// mapLinks rewrites every [[title]] in prose through fn, leaving code
// fences and inline code alone. fn returns the replacement for the whole
// link.
func mapLinks(text string, fn func(title string) string) string {
	lines := strings.Split(text, "\n")
	fence := false
	for i, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), "```") {
			fence = !fence
			continue
		}
		if fence || !strings.Contains(l, "[[") {
			continue
		}
		parts := strings.Split(l, "`")
		for j := 0; j < len(parts); j += 2 {
			parts[j] = linkRE.ReplaceAllStringFunc(parts[j], func(m string) string {
				return fn(strings.TrimSpace(m[2 : len(m)-2]))
			})
		}
		lines[i] = strings.Join(parts, "`")
	}
	return strings.Join(lines, "\n")
}

// Links lists the titles a text links to, in order, outside code.
func Links(text string) []string {
	var out []string
	mapLinks(text, func(t string) string {
		out = append(out, t)
		return ""
	})
	return out
}

// Linked lists the other findings this one links to, by ordinal, with
// duplicates folded.
func (d FindingsDocument) Linked(f Finding) []int {
	byTitle := map[string]int{}
	for _, g := range d.Findings {
		byTitle[linkKey(g.Title)] = g.Ordinal
	}
	var out []int
	seen := map[int]bool{}
	for _, text := range f.prose() {
		for _, t := range Links(*text) {
			if n, ok := byTitle[linkKey(t)]; ok && n != f.Ordinal && !seen[n] {
				seen[n] = true
				out = append(out, n)
			}
		}
	}
	return out
}

// prose is every text of a finding a link may sit in.
func (f *Finding) prose() []*string {
	return []*string{&f.Description, &f.NormalUse, &f.Expected, &f.Impact, &f.Fix}
}

// resolveLinks drops every link to a finding the document does not carry,
// or to the finding itself, keeping its words, and says what it dropped.
func (d *FindingsDocument) resolveLinks() []string {
	byTitle := map[string]int{}
	for _, f := range d.Findings {
		byTitle[linkKey(f.Title)] = f.Ordinal
	}
	var out []string
	for i := range d.Findings {
		f := &d.Findings[i]
		for _, text := range f.prose() {
			*text = mapLinks(*text, func(t string) string {
				n, ok := byTitle[linkKey(t)]
				switch {
				case !ok:
					out = append(out, fmt.Sprintf("finding %d: [[%s]] names no finding in this report; the link is dropped", f.Ordinal, t))
				case n == f.Ordinal:
					out = append(out, fmt.Sprintf("finding %d: links to itself; the link is dropped", f.Ordinal))
				default:
					return "[[" + t + "]]"
				}
				return t
			})
		}
	}
	return out
}
