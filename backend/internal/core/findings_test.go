package core

import (
	"encoding/json"
	"strings"
	"testing"
)

func sample() FindingsDocument {
	return FindingsDocument{
		Report: ReportHeader{
			AuditID: "audit_1",
			Pin: Pin{Repo: "vig-demo/vault-programs", Commit: "9c1e4d7a2b",
				Programs: []ProgramPin{{ID: "Prog111", Slot: 10, BytecodeSHA256: "abc", Match: MatchSelfBuilt}}},
		},
		Findings: []Finding{
			{Ordinal: 1, Severity: SeverityCritical, Title: "Missing signer check", Location: Location{File: "src/lib.rs", LineStart: 41, LineEnd: 58},
				Description: "d", ExploitPath: "call withdraw without signing", Fix: "- require signer", FixKind: FixOperational},
			{Ordinal: 2, Severity: SeverityLow, Title: "Unused account", Location: Location{File: "src/lib.rs", LineStart: 3},
				Description: "d", Fix: "- remove", FixKind: FixOperational, Status: FindingAcknowledged},
		},
	}
}

func TestValidateFillsSummaryAndStatus(t *testing.T) {
	d := sample()
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
	if d.Report.Summary != (Summary{Critical: 1, Low: 1}) {
		t.Fatalf("summary %+v", d.Report.Summary)
	}
	if d.Findings[0].Status != FindingOpen {
		t.Fatal("empty status should default to open")
	}
}

func TestValidateRejects(t *testing.T) {
	d := sample()
	d.Findings = append(d.Findings, Finding{Ordinal: 1, Severity: "urgent", Title: "x", Location: Location{File: "f", LineStart: 5, LineEnd: 2}, Description: "d", Fix: "- f", FixKind: FixOperational})
	err := d.Validate()
	if err == nil {
		t.Fatal("expected errors")
	}
	for _, want := range []string{"ordinal must be unique", "severity is invalid", "line_end before line_start"} {
		if !contains(err.Error(), want) {
			t.Errorf("missing %q in %v", want, err)
		}
	}
	// A source-only review pins the commit alone; no programs is valid.
	d = sample()
	d.Report.Pin.Programs = nil
	if err := d.Validate(); err != nil {
		t.Fatalf("pin without programs is a source-only review: %v", err)
	}
}

func TestForViewerStripsExploitPath(t *testing.T) {
	d := sample()
	pub := d.ForViewer(false)
	if pub.Findings[0].ExploitPath != "" {
		t.Fatal("exploit path leaked to non-owner")
	}
	if d.Findings[0].ExploitPath == "" {
		t.Fatal("original must be untouched")
	}
	if d.ForViewer(true).Findings[0].ExploitPath == "" {
		t.Fatal("owner must see exploit path")
	}
}

func TestJSONShape(t *testing.T) {
	b, _ := json.Marshal(sample())
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if _, ok := m["report"].(map[string]any)["pin"]; !ok {
		t.Fatal("report.pin missing")
	}
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func TestForViewerMarksWithheldExploitPaths(t *testing.T) {
	doc := FindingsDocument{Findings: []Finding{
		{Ordinal: 1, Title: "has one", ExploitPath: "pass the authority as a non-signer"},
		{Ordinal: 2, Title: "has none"},
	}}

	owner := doc.ForViewer(true)
	if owner.Findings[0].ExploitPath == "" {
		t.Fatal("the owner keeps the exploit path")
	}
	if owner.Findings[0].ExploitPathWithheld || owner.Findings[1].ExploitPathWithheld {
		t.Fatal("nothing is withheld from the owner")
	}

	public := doc.ForViewer(false)
	if public.Findings[0].ExploitPath != "" {
		t.Fatal("the exploit path leaked to a public reader")
	}
	if !public.Findings[0].ExploitPathWithheld {
		t.Fatal("a finding whose exploit path was stripped must say so")
	}
	if public.Findings[1].ExploitPathWithheld {
		t.Fatal("a finding that never had an exploit path must not claim one is withheld")
	}
	if doc.Findings[0].ExploitPath == "" {
		t.Fatal("ForViewer must not mutate the document it was called on")
	}
}

func TestProseShapeRejectsWalls(t *testing.T) {
	long := strings.Repeat("a reader in a hurry skips this ", 25)
	if err := proseShape(long); err == nil {
		t.Error("a 775-character paragraph passed")
	}
	if err := proseShape("short.\n\n- " + long + "\n- second"); err != nil {
		t.Errorf("a long bullet list should pass: %v", err)
	}
	if err := proseShape("one.\n\ntwo.\n\n- a\n- b"); err != nil {
		t.Errorf("shaped prose should pass: %v", err)
	}
}

func TestVisualKindsAndNames(t *testing.T) {
	id := "0199c0f0-0000-7000-8000-000000000000"
	cases := []struct {
		v    Visual
		kind VisualKind
		err  string
	}{
		{Visual{ID: id, Caption: "the drain", Mime: "image/png"}, VisualImage, ""},
		{Visual{ID: id, Caption: "the drain", Mime: "Video/MP4"}, VisualVideo, ""},
		{Visual{ID: id, Caption: "the drain", Mime: "video/webm", Name: "ignored.webm"}, VisualVideo, ""},
		{Visual{ID: id, Caption: "the drain", Mime: "text/x-python", Name: "poc.py", Size: 12}, VisualFile, ""},
		{Visual{ID: id, Caption: "the drain", Mime: "application/zip"}, "", "needs a name"},
		{Visual{ID: id, Caption: "the drain", Mime: "application/zip", Name: "../poc.zip"}, "", "bare file name"},
		{Visual{ID: id, Caption: "the drain", Mime: "not a mime"}, "", "type/subtype"},
		{Visual{ID: id, Caption: "the drain", Mime: "image/png", Kind: VisualFile}, "", "does not match"},
		{Visual{ID: "short", Caption: "the drain", Mime: "image/png"}, "", "stored attachment id"},
	}
	for i, c := range cases {
		err := c.v.Normalize()
		if c.err == "" {
			if err != nil {
				t.Errorf("case %d: %v", i, err)
			} else if c.v.Kind != c.kind {
				t.Errorf("case %d: kind %q, want %q", i, c.v.Kind, c.kind)
			}
			continue
		}
		if err == nil || !contains(err.Error(), c.err) {
			t.Errorf("case %d: err %v, want %q", i, err, c.err)
		}
	}
	// Validate fills the kind on every reference and names the bad one.
	d := sample()
	d.Findings[0].Visuals = []Visual{{ID: id, Caption: "recorded", Mime: "video/mp4"}}
	d.Findings[0].SitesKind = SitesFlow
	d.Findings[0].Sites = []Site{{Location: Location{File: "src/lib.rs", LineStart: 41}, Visuals: []Visual{{ID: id, Caption: "the script", Mime: "text/x-sh"}}}}
	err := d.Validate()
	if err == nil || !contains(err.Error(), "findings[0].sites[0].visuals[0]: a file needs a name") {
		t.Fatalf("err %v", err)
	}
	if d.Findings[0].Visuals[0].Kind != VisualVideo {
		t.Errorf("kind not filled: %+v", d.Findings[0].Visuals[0])
	}
	d.Findings[0].Sites[0].Visuals[0].Name = "poc.sh"
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
	if d.Findings[0].Sites[0].Visuals[0].Kind != VisualFile {
		t.Errorf("file kind not filled")
	}
}

func TestFixShapeIsBulletsWithFences(t *testing.T) {
	good := "- declare the account as `Signer<'info>`\n```rust\npub authority: Signer<'info>,\n```\n\n- or check `is_signer` before acting"
	if err := fixShape(good); err != nil {
		t.Fatalf("bullets with a fence: %v", err)
	}
	for _, c := range []struct{ in, want string }{
		{"Use Program<'info, Token>.", "start with '- '"},
		{"- one\nnot a bullet", "start with '- '"},
		{"- one\n```\nnever closed", "never closed"},
		{"```\ncode only\n```", "at least one"},
		{"- one\n```\n" + strings.Repeat("x", 2001) + "\n```", "code block is"},
	} {
		err := fixShape(c.in)
		if err == nil || !contains(err.Error(), c.want) {
			t.Errorf("%q: err %v, want %q", c.in, err, c.want)
		}
	}
	// Description keeps its looser shape but learns fences too.
	if err := proseShape("The header comes back as\n```\nAccess-Control-Allow-Origin: *\n```\n\n- for every origin"); err != nil {
		t.Errorf("fenced description: %v", err)
	}
	blocks, _ := proseBlocks("a\n\n```sh\ncurl -I\n\nx\n```\n- b")
	if len(blocks) != 3 || !blocks[1].code || blocks[1].text != "curl -I\n\nx" || blocks[2].text != "- b" {
		t.Errorf("blocks %+v", blocks)
	}
}

func TestFixFenceLocations(t *testing.T) {
	fix := "- replace the guard\n```rust src/lib.rs:41-45\nrequire!(a.is_signer);\n```\n\n- insert before the call\n```rust src/lib.rs:12\nlet x = 1;\n```\n\n- a value, nowhere in particular\n```\nAccess-Control-Allow-Origin: https://app.example.com\n```\n\n- same place again\n```rust src/lib.rs:41-45\nx\n```"
	if err := fixShape(fix); err != nil {
		t.Fatal(err)
	}
	locs := FixLocations(fix)
	want := []Location{{File: "src/lib.rs", LineStart: 41, LineEnd: 45}, {File: "src/lib.rs", LineStart: 12}}
	if len(locs) != 2 || locs[0] != want[0] || locs[1] != want[1] {
		t.Errorf("locations %+v", locs)
	}
	for _, bad := range []string{"- x\n```rust src/lib.rs:0\ny\n```", "- x\n```rust ../lib.rs:3\ny\n```", "- x\n```rust src/lib.rs:9-4\ny\n```", "- x\n```rust lib.rs\ny\n```"} {
		if err := fixShape(bad); err == nil {
			t.Errorf("%q passed", bad)
		}
	}
}

func TestOneRecordingPerFinding(t *testing.T) {
	id := "0199c0f0-0000-7000-8000-000000000000"
	clip := Visual{ID: id, Caption: "recorded", Mime: "video/mp4"}
	d := sample()
	d.Findings[0].Visuals = []Visual{clip}
	d.Findings[0].SitesKind = SitesFlow
	d.Findings[0].Sites = []Site{{Location: Location{File: "src/lib.rs", LineStart: 41}, Visuals: []Visual{clip}}}
	err := d.Validate()
	if err == nil || !contains(err.Error(), "findings[0] carries 2 recordings") {
		t.Fatalf("err %v", err)
	}
	d.Findings[0].Visuals = []Visual{{ID: id, Caption: "the script", Mime: "text/x-python", Name: "poc.py"}}
	if err := d.Validate(); err != nil {
		t.Fatalf("one recording and a file: %v", err)
	}
}

func TestLocatedFindingNeedsPatchOrOperational(t *testing.T) {
	d := sample()
	d.Findings[0].FixKind = ""
	d.Findings[0].Fix = "- require signer"
	err := d.Validate()
	if err == nil || !contains(err.Error(), "carries no patch") {
		t.Fatalf("bare fix on a located finding passed: %v", err)
	}
	d.Findings[0].Fix = "- require signer\n```rust src/lib.rs:41-58\nrequire!(ctx.accounts.authority.is_signer);\n```"
	if err := d.Validate(); err != nil {
		t.Fatalf("placed patch: %v", err)
	}
	d.Findings[0].Fix = "- rotate the key"
	d.Findings[0].FixKind = FixOperational
	if err := d.Validate(); err != nil {
		t.Fatalf("operational: %v", err)
	}
	d.Findings[0].FixKind = "magic"
	if err := d.Validate(); err == nil || !contains(err.Error(), "fix_kind must be") {
		t.Fatalf("bad kind passed: %v", err)
	}
	// A finding with no file (a web endpoint) is not held to it.
	d = sample()
	d.Findings[0].FixKind = ""
	d.Findings[0].Location = Location{URL: "https://app.example/x", LineStart: 1}
	d.Findings[0].Fix = "- require a session on the route"
	if err := d.Validate(); err != nil {
		t.Fatalf("url-only finding: %v", err)
	}
}
