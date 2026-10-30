package core

import (
	"strconv"
	"strings"
	"testing"
)

func TestWithoutStripsOnlyWhatItNames(t *testing.T) {
	d := proofDoc()
	AnswerAll(&d.Findings[0], true, "")
	d.Findings[0].Evidence = &Evidence{Kind: EvidenceStatic}
	d.Findings[0].SeverityChanges = []SeverityChange{{From: SeverityHigh, To: SeverityLow, Reason: "re-tested", By: "admin"}}
	if err := d.Without([]string{"history", " attachments"}); err != nil {
		t.Fatal(err)
	}
	f := d.Findings[0]
	if f.SeverityChanges != nil || f.Visuals != nil {
		t.Errorf("history or attachments kept: %v %v", f.SeverityChanges, f.Visuals)
	}
	if f.Exhaustion == nil || f.Evidence == nil {
		t.Errorf("parts not named were stripped")
	}
	if err := d.Without([]string{"pictures"}); err == nil {
		t.Error("an unknown part passed")
	}
	if err := d.Without([]string{""}); err != nil {
		t.Error(err)
	}
}

func TestRenumberReadsInSeverityOrder(t *testing.T) {
	d := proofDoc()
	AnswerAll(&d.Findings[0], true, "")
	low := d.Findings[0]
	low.Ordinal, low.Severity, low.Title = 1, SeverityLow, "A low one"
	high := d.Findings[0]
	high.Ordinal, high.Severity, high.Title = 7, SeverityHigh, "A high one"
	gone := d.Findings[0]
	gone.Ordinal, gone.Severity, gone.Title, gone.Status = 3, SeverityCritical, "An archived one", FindingArchived
	d.Findings = []Finding{low, gone, high}
	d.Renumber()
	got := []string{}
	for _, f := range d.Findings {
		got = append(got, f.Title+":"+itoa(f.Ordinal))
	}
	want := "A high one:1 A low one:2 An archived one:3"
	if strings.Join(got, " ") != want {
		t.Errorf("got %v", got)
	}
}

func itoa(n int) string { return strconv.Itoa(n) }
