package core

import (
	"fmt"
	"strings"
)

// Exhaustion is what a finding's reproduction still had to rule out before
// it was called done: the other ways in, the lower privileges, the guards
// on the path. vig lists the items for a finding from the finding itself,
// the same list every time, and the writer answers each one honestly. The
// report shows the answers as a row of dots, the items behind a click.

// ExhaustionItem is one thing to have tried.
type ExhaustionItem struct {
	Key  string `json:"key"`
	Text string `json:"text"`
}

// Exhaustion is the writer's answer to one item.
type Exhaustion struct {
	Key  string `json:"key" doc:"One of the items vig lists for this finding (the dry run prints them)"`
	Done bool   `json:"done" doc:"It was tried, to the end"`
	Note string `json:"note,omitempty" maxLength:"140" doc:"What was tried, or why it was not; required when not done"`
	Text string `json:"text,omitempty" doc:"Set by the server: the item's words"`
}

var exhaustionCommon = []ExhaustionItem{
	{"entry", "every path that reaches the same code was tried, not only the one shown"},
	{"below", "tried from the lowest privilege that can reach it, and from one below"},
	{"inputs", "boundary and malformed variants of the triggering input"},
	{"fresh", "reproduced from a fresh session, account or state, not the one it was found with"},
	{"furthest", "followed to the furthest effect, not stopped at the first signal"},
	{"guards", "every check, limit or guard on the path was exercised, not assumed"},
	{"negative", "a change that should block it was confirmed to block it"},
}

var exhaustionWeb = []ExhaustionItem{
	{"methods", "other methods and content types on the same endpoint"},
	{"auth", "with each auth material present, absent and swapped: cookie, bearer, api key"},
}

var exhaustionChain = []ExhaustionItem{
	{"accounts", "every account slot with an unexpected owner, state or program"},
	{"cpi", "reached through another program, not only by a direct call"},
}

var exhaustionCode = []ExhaustionItem{
	{"callers", "every caller of the function, tests and tooling included"},
}

// placeKind says what kind of thing the finding sits on: web (an
// endpoint), chain (a program) or code.
func (f Finding) placeKind() string {
	if f.Location.URL != "" {
		return "web"
	}
	for _, s := range f.Sites {
		if s.Location.URL != "" {
			return "web"
		}
	}
	if f.Location.ProgramID != "" || f.Location.Instruction != "" {
		return "chain"
	}
	for _, s := range f.Sites {
		if s.Location.ProgramID != "" {
			return "chain"
		}
	}
	return "code"
}

// ExhaustionItems lists what this finding had to rule out, from the finding
// itself, so the list is the same wherever it is computed.
func ExhaustionItems(f Finding) []ExhaustionItem {
	out := append([]ExhaustionItem{}, exhaustionCommon...)
	switch f.placeKind() {
	case "web":
		out = append(out, exhaustionWeb...)
	case "chain":
		out = append(out, exhaustionChain...)
	default:
		out = append(out, exhaustionCode...)
	}
	return out
}

// exhaustionErrors checks answers that were given: every item once, no
// item that is not on the list, a note on everything not done. It also
// writes each item's words onto its answer, so the stored document carries
// them.
func (f *Finding) exhaustionErrors(i int) []string {
	if len(f.Exhaustion) == 0 {
		return nil
	}
	items := ExhaustionItems(*f)
	text := map[string]string{}
	for _, it := range items {
		text[it.Key] = it.Text
	}
	var errs []string
	seen := map[string]bool{}
	for j := range f.Exhaustion {
		a := &f.Exhaustion[j]
		a.Key = strings.TrimSpace(a.Key)
		a.Note = strings.TrimSpace(a.Note)
		t, ok := text[a.Key]
		if !ok {
			errs = append(errs, fmt.Sprintf("findings[%d].exhaustion[%d]: %q is not an item for this finding", i, j, a.Key))
			continue
		}
		if seen[a.Key] {
			errs = append(errs, fmt.Sprintf("findings[%d].exhaustion[%d]: %s answered twice", i, j, a.Key))
		}
		seen[a.Key] = true
		a.Text = t
		if !a.Done && a.Note == "" {
			errs = append(errs, fmt.Sprintf("findings[%d].exhaustion[%d]: %s is not done and carries no note on why", i, j, a.Key))
		}
	}
	var missing []string
	for _, it := range items {
		if !seen[it.Key] {
			missing = append(missing, it.Key)
		}
	}
	if len(missing) > 0 {
		errs = append(errs, fmt.Sprintf("findings[%d].exhaustion answers nothing for %s", i, strings.Join(missing, ", ")))
	}
	return errs
}

// ExhaustionDone counts the answers: done and in all.
func (f Finding) ExhaustionDone() (done, total int) {
	for _, a := range f.Exhaustion {
		if a.Done {
			done++
		}
	}
	return done, len(f.Exhaustion)
}

// Unexhausted lists the findings with no answers, each with the items it
// has to answer. newOnly leaves out findings carried from an earlier
// report, which keep what they had.
func (d FindingsDocument) Unexhausted(newOnly bool) []string {
	var out []string
	for _, f := range d.Findings {
		if len(f.Exhaustion) > 0 || (newOnly && f.CarriedFromID != "") {
			continue
		}
		var lines []string
		for _, it := range ExhaustionItems(f) {
			lines = append(lines, it.Key+": "+it.Text)
		}
		out = append(out, fmt.Sprintf("finding %d: exhaustion not answered; each item takes done true or false and a note when not done: %s", f.Ordinal, strings.Join(lines, "; ")))
	}
	return out
}

// AnswerAll answers every item of the finding the same way.
func AnswerAll(f *Finding, done bool, note string) {
	f.Exhaustion = nil
	for _, it := range ExhaustionItems(*f) {
		f.Exhaustion = append(f.Exhaustion, Exhaustion{Key: it.Key, Done: done, Note: note, Text: it.Text})
	}
}

// Answer sets one item, adding it when it is not answered yet.
func Answer(f *Finding, key string, done bool, note string) {
	for j := range f.Exhaustion {
		if f.Exhaustion[j].Key == key {
			f.Exhaustion[j].Done = done
			f.Exhaustion[j].Note = note
			return
		}
	}
	f.Exhaustion = append(f.Exhaustion, Exhaustion{Key: key, Done: done, Note: note})
}
