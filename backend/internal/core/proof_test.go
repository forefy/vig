package core

import (
	"strings"
	"testing"
)

func proofDoc() FindingsDocument {
	return FindingsDocument{
		Report: ReportHeader{AuditID: "audit_1", Pin: Pin{Repo: "o/n", Commit: strings.Repeat("a", 40), Programs: []ProgramPin{{ID: "Prog111", Slot: 10, BytecodeSHA256: "abc", Match: MatchSelfBuilt}}}},
		Findings: []Finding{{Ordinal: 1, Severity: SeverityHigh, Title: "Invoice of another tenant", Location: Location{File: "src/invoices.ts", LineStart: 41},
			Description: "d", Fix: "- check the tenant", FixKind: FixOperational,
			SitesKind: SitesFlow, Sites: []Site{{Location: Location{File: "src/invoices.ts", LineStart: 41}, Note: "the id comes in"}}}},
	}
}

func TestEvidenceThatRanNeedsAnAttachment(t *testing.T) {
	d := proofDoc()
	d.Findings[0].Evidence = &Evidence{Kind: EvidenceLive, Tool: "burp", Artifact: "repeater: 200 with another tenant's invoice"}
	err := d.Validate()
	if err == nil || !strings.Contains(err.Error(), "a live probe with burp") || !strings.Contains(err.Error(), "nothing is attached") {
		t.Fatalf("a tool without a capture must be refused, got %v", err)
	}
	d.Findings[0].Visuals = []Visual{{ID: strings.Repeat("1", 36), Caption: "burp repeater, both invoices", Mime: "image/png", Role: VisualProof}}
	if err := d.Validate(); err != nil {
		t.Fatalf("a proof capture satisfies it: %v", err)
	}
	// A static reading needs nothing.
	s := proofDoc()
	s.Findings[0].Evidence = &Evidence{Kind: EvidenceStatic}
	if err := s.Validate(); err != nil {
		t.Fatalf("static: %v", err)
	}
	// A tool on a static reading still ran something.
	s.Findings[0].Evidence = &Evidence{Kind: EvidenceStatic, Tool: "semgrep"}
	if err := s.Validate(); err == nil {
		t.Fatal("semgrep without its output must be refused")
	}
	bad := proofDoc()
	bad.Findings[0].Visuals = []Visual{{ID: strings.Repeat("1", 36), Caption: "x marks", Mime: "image/png", Role: "hidden"}}
	if err := bad.Validate(); err == nil || !strings.Contains(err.Error(), "role") {
		t.Fatalf("an unknown role must be refused, got %v", err)
	}
}

func TestRan(t *testing.T) {
	cases := map[string]Evidence{
		"":                             {Kind: EvidenceStatic},
		"fuzzing with trident":         {Kind: EvidenceFuzz, Tool: "trident"},
		"fuzzing":                      {Kind: EvidenceFuzz},
		"a live probe":                 {Kind: EvidenceLive},
		"an on-chain read with helius": {Kind: EvidenceOnChain, Tool: "helius"},
		"semgrep":                      {Kind: EvidenceStatic, Tool: "semgrep"},
	}
	for want, e := range cases {
		if got := e.Ran(); got != want {
			t.Errorf("%+v: %q, want %q", e, got, want)
		}
	}
	var none *Evidence
	if none.Ran() != "" {
		t.Fatal("nil evidence ran nothing")
	}
}

func TestWithoutProof(t *testing.T) {
	d := proofDoc()
	d.Findings[0].Visuals = []Visual{
		{ID: strings.Repeat("1", 36), Caption: "the script", Mime: "text/x-python", Name: "poc.py"},
		{ID: strings.Repeat("2", 36), Caption: "burp", Mime: "image/png", Role: VisualProof},
	}
	d.Findings[0].Sites[0].Visuals = []Visual{{ID: strings.Repeat("3", 36), Caption: "sqlmap run", Mime: "image/png", Role: VisualProof}}
	shown := d.WithoutProof()
	if n := shown.Findings[0].Attached(); n != 1 || shown.Findings[0].Visuals[0].Name != "poc.py" {
		t.Fatalf("the report keeps only what is shown, got %d", n)
	}
	if shown.Findings[0].Sites[0].Visuals != nil {
		t.Fatal("a step's proof leaves too")
	}
	if d.Findings[0].Attached() != 3 || len(d.Findings[0].Sites[0].Visuals) != 1 {
		t.Fatal("the stored document is untouched")
	}
}
