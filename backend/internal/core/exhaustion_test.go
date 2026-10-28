package core

import (
	"strings"
	"testing"
)

func TestExhaustionItemsFollowThePlace(t *testing.T) {
	code := proofDoc().Findings[0]
	web := code
	web.Location = Location{URL: "https://app.example.test/api/invoices"}
	chain := code
	chain.Location.ProgramID = "Prog111"
	keys := func(f Finding) string {
		var k []string
		for _, it := range ExhaustionItems(f) {
			k = append(k, it.Key)
		}
		return strings.Join(k, " ")
	}
	if got := keys(code); got != "entry below inputs fresh furthest guards negative callers" {
		t.Fatalf("code: %s", got)
	}
	if got := keys(web); !strings.HasSuffix(got, "negative methods auth") {
		t.Fatalf("web: %s", got)
	}
	if got := keys(chain); !strings.HasSuffix(got, "negative accounts cpi") {
		t.Fatalf("chain: %s", got)
	}
	for _, f := range []Finding{code, web, chain} {
		if n := len(ExhaustionItems(f)); n < 5 || n > 10 {
			t.Fatalf("%d items", n)
		}
	}
}

func TestExhaustionAnswersAreChecked(t *testing.T) {
	d := proofDoc()
	if msgs := d.Unexhausted(false); len(msgs) != 1 || !strings.Contains(msgs[0], "finding 1: exhaustion not answered") || !strings.Contains(msgs[0], "callers: every caller") {
		t.Fatalf("unanswered: %v", msgs)
	}
	d.Findings[0].CarriedFromID = "x"
	if msgs := d.Unexhausted(true); len(msgs) != 0 {
		t.Fatalf("a carried finding keeps what it had: %v", msgs)
	}
	d.Findings[0].CarriedFromID = ""
	AnswerAll(&d.Findings[0], true, "")
	if err := d.Validate(); err != nil {
		t.Fatalf("all done: %v", err)
	}
	if msgs := d.Unexhausted(false); len(msgs) != 0 {
		t.Fatalf("answered: %v", msgs)
	}
	done, total := d.Findings[0].ExhaustionDone()
	if done != 8 || total != 8 || d.Findings[0].Exhaustion[0].Text == "" {
		t.Fatalf("%d of %d, text %q", done, total, d.Findings[0].Exhaustion[0].Text)
	}
	// Not done needs a note; a stranger key and a missing key are refused.
	d.Findings[0].Exhaustion[1].Done = false
	if err := d.Validate(); err == nil || !strings.Contains(err.Error(), "below is not done and carries no note") {
		t.Fatalf("note required: %v", err)
	}
	d.Findings[0].Exhaustion[1].Note = "only the owner role exists on this deployment"
	if err := d.Validate(); err != nil {
		t.Fatalf("with a note: %v", err)
	}
	d.Findings[0].Exhaustion = d.Findings[0].Exhaustion[:7]
	if err := d.Validate(); err == nil || !strings.Contains(err.Error(), "answers nothing for callers") {
		t.Fatalf("missing: %v", err)
	}
	Answer(&d.Findings[0], "sideways", true, "")
	if err := d.Validate(); err == nil || !strings.Contains(err.Error(), `"sideways" is not an item`) {
		t.Fatalf("stranger: %v", err)
	}
	// The server writes the words onto the stored answers.
	e := proofDoc()
	e.Findings[0].Exhaustion = []Exhaustion{{Key: "entry", Done: true}}
	_ = e.Validate()
	if e.Findings[0].Exhaustion[0].Text != exhaustionCommon[0].Text {
		t.Fatalf("text %q", e.Findings[0].Exhaustion[0].Text)
	}
}
