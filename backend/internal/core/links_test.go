package core

import (
	"strings"
	"testing"
)

func TestLinksSurviveOnlyWhileTheirFindingDoes(t *testing.T) {
	d := proofDoc()
	AnswerAll(&d.Findings[0], true, "")
	other := d.Findings[0]
	other.Ordinal = 2
	other.Title = "Arbitrary CPI target"
	other.Description = "Reached after [[Invoice of another tenant]].\n\n- see [[invoice OF another   tenant]] too\n- not [[Gone finding]]\n\n```\n[[Invoice of another tenant]] stays as code\n```\n\nin `[[Invoice of another tenant]]` too"
	other.Fix = "- after [[Arbitrary CPI target]] itself"
	d.Findings = append(d.Findings, other)
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
	got := d.Findings[1].Description
	for _, want := range []string{"after [[Invoice of another tenant]].", "see [[invoice OF another   tenant]] too", "not Gone finding", "```\n[[Invoice of another tenant]] stays as code\n```", "in `[[Invoice of another tenant]]` too"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %q", want, got)
		}
	}
	if d.Findings[1].Fix != "- after Arbitrary CPI target itself" {
		t.Errorf("self link kept: %q", d.Findings[1].Fix)
	}
	advice := strings.Join(d.Advise(), "\n")
	for _, want := range []string{"finding 2: [[Gone finding]] names no finding in this report; the link is dropped", "finding 2: links to itself; the link is dropped", "finding 2: links to finding 1; a link is for a finding the reader has to read first"} {
		if !strings.Contains(advice, want) {
			t.Errorf("missing %q in advice:\n%s", want, advice)
		}
	}
	if strings.Contains(advice, "finding 1: links to") {
		t.Errorf("finding 1 links to nothing:\n%s", advice)
	}
	if got := d.Linked(d.Findings[1]); len(got) != 1 || got[0] != 1 {
		t.Errorf("linked %v", got)
	}
	// A second pass finds nothing left to drop.
	if err := d.Validate(); err != nil || len(d.dropped) != 0 {
		t.Errorf("second pass: %v %v", err, d.dropped)
	}
}
