package core

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

// ---- findings ----

func TestSummarize(t *testing.T) {
	cases := []struct {
		name string
		in   []Finding
		want Summary
	}{
		{"nil", nil, Summary{}},
		{"empty", []Finding{}, Summary{}},
		{"one of each", []Finding{
			{Severity: SeverityCritical}, {Severity: SeverityHigh}, {Severity: SeverityMedium}, {Severity: SeverityLow}, {Severity: SeverityInfo},
		}, Summary{1, 1, 1, 1, 1}},
		{"repeats", []Finding{{Severity: SeverityHigh}, {Severity: SeverityHigh}, {Severity: SeverityInfo}}, Summary{High: 2, Info: 1}},
		{"unknown severities are not counted", []Finding{{Severity: "urgent"}, {Severity: ""}, {Severity: SeverityLow}}, Summary{Low: 1}},
	}
	for _, c := range cases {
		if got := Summarize(c.in); got != c.want {
			t.Errorf("%s: got %+v want %+v", c.name, got, c.want)
		}
	}
}

func TestValidateEdges(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(d *FindingsDocument)
		want   string // substring of the error, "" for no error
	}{
		{"valid", func(*FindingsDocument) {}, ""},
		{"no audit id", func(d *FindingsDocument) { d.Report.AuditID = "" }, "audit_id is required"},
		{"repo without slash", func(d *FindingsDocument) { d.Report.Pin.Repo = "vault-programs" }, "repo must be owner/name"},
		{"repo empty", func(d *FindingsDocument) { d.Report.Pin.Repo = "" }, "repo must be owner/name"},
		{"commit too short", func(d *FindingsDocument) { d.Report.Pin.Commit = "abc123" }, "commit must be a commit sha"},
		{"commit exactly seven", func(d *FindingsDocument) { d.Report.Pin.Commit = "abc1234" }, ""},
		{"program without id", func(d *FindingsDocument) { d.Report.Pin.Programs[0].ID = "" }, "programs[0] needs id and bytecode_sha256"},
		{"program without sha", func(d *FindingsDocument) { d.Report.Pin.Programs[0].BytecodeSHA256 = "" }, "programs[0] needs id and bytecode_sha256"},
		{"program bad match", func(d *FindingsDocument) { d.Report.Pin.Programs[0].Match = "matched" }, "programs[0].match is invalid"},
		{"program empty match", func(d *FindingsDocument) { d.Report.Pin.Programs[0].Match = "" }, "programs[0].match is invalid"},
		{"second program bad", func(d *FindingsDocument) {
			d.Report.Pin.Programs = append(d.Report.Pin.Programs, ProgramPin{ID: "x", Match: MatchVerified})
		}, "programs[1] needs id and bytecode_sha256"},
		{"ordinal zero", func(d *FindingsDocument) { d.Findings[0].Ordinal = 0 }, "findings[0].ordinal must be unique and >= 1"},
		{"ordinal negative", func(d *FindingsDocument) { d.Findings[0].Ordinal = -3 }, "findings[0].ordinal must be unique and >= 1"},
		{"ordinal gaps are fine", func(d *FindingsDocument) { d.Findings[1].Ordinal = 40 }, ""},
		{"no title", func(d *FindingsDocument) { d.Findings[0].Title = "" }, "findings[0] needs title, description and fix"},
		{"no description", func(d *FindingsDocument) { d.Findings[1].Description = "" }, "findings[1] needs title, description and fix"},
		{"no fix", func(d *FindingsDocument) { d.Findings[1].Fix = "" }, "findings[1] needs title, description and fix"},
		{"no file", func(d *FindingsDocument) { d.Findings[0].Location.File = "" }, "findings[0].location needs file and line_start"},
		{"line start zero", func(d *FindingsDocument) { d.Findings[0].Location.LineStart = 0 }, "findings[0].location needs file and line_start"},
		{"line end equals start", func(d *FindingsDocument) { d.Findings[0].Location.LineEnd = d.Findings[0].Location.LineStart }, ""},
		{"line end omitted", func(d *FindingsDocument) { d.Findings[0].Location.LineEnd = 0 }, ""},
		{"line end before start", func(d *FindingsDocument) { d.Findings[0].Location.LineEnd = 1 }, "findings[0].location.line_end before line_start"},
		{"bad status", func(d *FindingsDocument) { d.Findings[0].Status = "wontfix" }, "findings[0].status is invalid"},
		{"no findings is a valid clean report", func(d *FindingsDocument) { d.Findings = nil }, ""},
	}
	for _, c := range cases {
		d := sample()
		c.mutate(&d)
		err := d.Validate()
		if c.want == "" {
			if err != nil {
				t.Errorf("%s: unexpected error %v", c.name, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err=%v, want it to contain %q", c.name, err, c.want)
		}
	}
}

func TestValidateAcceptsEveryEnumValue(t *testing.T) {
	for _, m := range []MatchStatus{MatchVerified, MatchSelfBuilt, MatchMismatch, MatchUnverified} {
		d := sample()
		d.Report.Pin.Programs[0].Match = m
		if err := d.Validate(); err != nil {
			t.Errorf("match %s: %v", m, err)
		}
	}
	for _, s := range []FindingStatus{FindingOpen, FindingFixed, FindingAcknowledged, FindingDisputed} {
		d := sample()
		d.Findings[0].Status = s
		if err := d.Validate(); err != nil {
			t.Errorf("status %s: %v", s, err)
		}
		if d.Findings[0].Status != s {
			t.Errorf("status %s was rewritten to %s", s, d.Findings[0].Status)
		}
	}
	for _, s := range severities {
		d := sample()
		d.Findings[0].Severity = s
		if err := d.Validate(); err != nil {
			t.Errorf("severity %s: %v", s, err)
		}
	}
}

func TestValidateCollectsAllErrors(t *testing.T) {
	d := sample()
	d.Report.AuditID = ""
	d.Report.Pin.Commit = "x"
	d.Findings[0].Title = ""
	err := d.Validate()
	if err == nil {
		t.Fatal("expected error")
	}
	if n := strings.Count(err.Error(), "; "); n != 2 {
		t.Fatalf("want all 3 problems joined, got %d separators in %q", n, err)
	}
}

func TestValidateRecomputesSummary(t *testing.T) {
	d := sample()
	d.Report.Summary = Summary{Critical: 99, Info: 5}
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
	if d.Report.Summary != (Summary{Critical: 1, Low: 1}) {
		t.Fatalf("summary must be overwritten from findings, got %+v", d.Report.Summary)
	}
	// The summary is recomputed even when validation fails, so a rejected
	// document never carries a stale summary.
	d = sample()
	d.Report.AuditID = ""
	d.Report.Summary = Summary{High: 7}
	_ = d.Validate()
	if d.Report.Summary != (Summary{Critical: 1, Low: 1}) {
		t.Fatalf("summary on invalid doc %+v", d.Report.Summary)
	}
}

func TestForViewerEdges(t *testing.T) {
	// Empty and nil findings do not panic and yield an empty slice.
	for _, in := range [][]Finding{nil, {}} {
		d := sample()
		d.Findings = in
		out := d.ForViewer(false)
		if len(out.Findings) != 0 {
			t.Fatalf("got %d findings", len(out.Findings))
		}
	}

	// Every field except ExploitPath survives, in order, and the header is intact.
	d := sample()
	d.Findings[1].ExploitPath = "also secret"
	d.Findings[1].References = []string{"https://ref"}
	d.Findings[1].CarriedFromID = "f_prev"
	out := d.ForViewer(false)
	if !reflect.DeepEqual(out.Report, d.Report) {
		t.Fatal("report header must be unchanged")
	}
	if len(out.Findings) != len(d.Findings) {
		t.Fatal("finding count changed")
	}
	for i := range d.Findings {
		want := d.Findings[i]
		// The path goes, and the fact that it went is recorded in its place.
		want.ExploitPathWithheld = want.ExploitPath != ""
		want.ExploitPath = ""
		got := out.Findings[i]
		if got.ExploitPath != "" {
			t.Errorf("findings[%d] exploit path leaked", i)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("findings[%d] changed beyond exploit path:\n got %+v\nwant %+v", i, got, want)
		}
	}
	// The copy is independent of the original.
	out.Findings[0].Title = "changed"
	if d.Findings[0].Title == "changed" {
		t.Fatal("ForViewer(false) must not alias the original findings")
	}

	// Owner view is the same document.
	if own := d.ForViewer(true); own.Findings[1].ExploitPath != "also secret" || len(own.Findings) != 2 {
		t.Fatalf("owner view %+v", own.Findings)
	}
}

// ---- states ----

func TestStateHelpers(t *testing.T) {
	cases := []struct {
		s                        AuditState
		terminal, active         bool
		next                     AuditState
		clearsCost, deletesClone bool
	}{
		{StateQueued, false, false, StateClaimed, false, false},
		{StateClaimed, false, true, StateCloning, false, false},
		{StateCloning, false, true, StateProgramsVerified, false, false},
		{StateProgramsVerified, false, true, StateAnalysis, false, false},
		{StateAnalysis, false, true, StateHumanReview, false, false},
		{StateHumanReview, false, true, StateDelivered, false, false},
		{StateDelivered, true, false, "", false, true},
		{StateDelayed, false, false, "", false, false},
		{StateFailed, true, false, "", true, true},
		{AuditState("bogus"), false, false, "", false, false},
	}
	for _, c := range cases {
		if got := c.s.Terminal(); got != c.terminal {
			t.Errorf("%s.Terminal() = %v", c.s, got)
		}
		if got := c.s.Active(); got != c.active {
			t.Errorf("%s.Active() = %v", c.s, got)
		}
		if got := c.s.Next(); got != c.next {
			t.Errorf("%s.Next() = %q want %q", c.s, got, c.next)
		}
		if got := c.s.ClearsCost(); got != c.clearsCost {
			t.Errorf("%s.ClearsCost() = %v", c.s, got)
		}
		if got := c.s.DeletesClone(); got != c.deletesClone {
			t.Errorf("%s.DeletesClone() = %v", c.s, got)
		}
	}
	if Pipeline[0] != StateQueued || Pipeline[len(Pipeline)-1] != StateDelivered {
		t.Fatalf("pipeline ends wrong: %v", Pipeline)
	}
}

func TestCanTransitionTable(t *testing.T) {
	cases := []struct {
		name string
		tr   Transition
		ok   bool
	}{
		{"queued to delayed", Transition{From: StateQueued, To: StateDelayed}, true},
		{"delayed back to queued", Transition{From: StateDelayed, To: StateQueued, Resume: StateQueued}, true},
		{"delayed resume to human review", Transition{From: StateDelayed, To: StateHumanReview, Resume: StateHumanReview}, true},
		{"delayed to failed without resume", Transition{From: StateDelayed, To: StateFailed}, true},
		{"delayed straight to delivered", Transition{From: StateDelayed, To: StateDelivered, Resume: StateHumanReview}, false},
		{"resume ignored on happy path", Transition{From: StateQueued, To: StateClaimed, Resume: StateAnalysis}, true},
		{"resume does not unlock skipping", Transition{From: StateQueued, To: StateAnalysis, Resume: StateAnalysis}, false},
		{"stay in place", Transition{From: StateAnalysis, To: StateAnalysis}, false},
		{"delivered to failed", Transition{From: StateDelivered, To: StateFailed}, false},
		{"delivered to delayed", Transition{From: StateDelivered, To: StateDelayed}, false},
		{"failed to queued", Transition{From: StateFailed, To: StateQueued}, false},
		{"failed to failed", Transition{From: StateFailed, To: StateFailed}, false},
		{"unknown from", Transition{From: "bogus", To: StateClaimed}, false},
		{"unknown from to failed", Transition{From: "bogus", To: StateFailed}, true},
		{"unknown to", Transition{From: StateQueued, To: "bogus"}, false},
		{"empty to", Transition{From: StateQueued, To: ""}, false},
		{"human review to delivered", Transition{From: StateHumanReview, To: StateDelivered}, true},
	}
	for _, c := range cases {
		err := CanTransition(c.tr)
		if (err == nil) != c.ok {
			t.Errorf("%s: err=%v ok=%v", c.name, err, c.ok)
		}
	}
}

// ---- plans ----

func TestNextSlotBoundaries(t *testing.T) {
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name    string
		lastRun time.Time
		want    time.Time
	}{
		{"never ran", time.Time{}, now},
		{"just ran", now, now.Add(SlotWindow)},
		{"one nanosecond ago", now.Add(-time.Nanosecond), now.Add(SlotWindow - time.Nanosecond)},
		{"exactly one window ago", now.Add(-SlotWindow), now},
		{"one nanosecond past the window", now.Add(-SlotWindow - time.Nanosecond), now},
		{"one nanosecond short of the window", now.Add(-SlotWindow + time.Nanosecond), now.Add(time.Nanosecond)},
		{"a week ago", now.Add(-7 * 24 * time.Hour), now},
		{"clock skew: last run in the future", now.Add(time.Hour), now.Add(25 * time.Hour)},
	}
	for _, c := range cases {
		if got := NextSlot(c.lastRun, now); !got.Equal(c.want) {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
	if SlotWindow != 24*time.Hour {
		t.Fatalf("slot window is %v, spec says one rolling day", SlotWindow)
	}
}

func TestPlansShape(t *testing.T) {
	keys := map[string]bool{}
	for _, p := range Plans {
		if keys[p.Key] {
			t.Errorf("duplicate plan key %s", p.Key)
		}
		keys[p.Key] = true
		if p.MaxPrograms <= 0 || p.MaxLines <= 0 {
			t.Errorf("%s: caps must be positive: %+v", p.Key, p)
		}
		switch p.Gate {
		case "usdc":
			if p.PriceUSDC <= 0 || p.SKU == "" {
				t.Errorf("%s: usdc-gated plans need a price and a sku: %+v", p.Key, p)
			}
		case "tweet", "contact":
			if p.PriceUSDC != 0 || p.SKU != "" {
				t.Errorf("%s: %s-gated plans are not purchasable: %+v", p.Key, p.Gate, p)
			}
		default:
			t.Errorf("%s: unknown gate %q", p.Key, p.Gate)
		}
	}
	for _, k := range []string{"free", "mini", "pro", "team"} {
		if !keys[k] {
			t.Errorf("plan %s missing", k)
		}
	}
	// Caps grow with the tier.
	for i := 1; i < len(Plans); i++ {
		if Plans[i].MaxLines < Plans[i-1].MaxLines || Plans[i].MaxPrograms < Plans[i-1].MaxPrograms {
			t.Errorf("caps must not shrink from %s to %s", Plans[i-1].Key, Plans[i].Key)
		}
	}
	// Pro's monthly display figure times the term equals the total.
	pro, _ := PlanBySKU(SKUPro)
	if pro.DisplayMonthly*12 != pro.PriceUSDC {
		t.Errorf("pro: 12 x %d != %d", pro.DisplayMonthly, pro.PriceUSDC)
	}
	if _, ok := PlanBySKU("free"); ok {
		t.Error("free is not a sku")
	}
}

// ---- ledger ----

func TestApplyLedgerBoundaries(t *testing.T) {
	cases := []struct {
		name    string
		balance int
		kind    LedgerKind
		delta   int
		want    int
		wantErr bool
	}{
		{"grant zero", 5, LedgerGrant, 0, 5, true},
		{"purchase zero", 5, LedgerPurchase, 0, 5, true},
		{"refund zero", 5, LedgerRefund, 0, 5, true},
		{"refund negative", 5, LedgerRefund, -1, 5, true},
		{"consume zero", 5, LedgerConsume, 0, 5, true},
		{"consume positive", 5, LedgerConsume, 1, 5, true},
		{"consume last credit", 1, LedgerConsume, -1, 0, false},
		{"consume from negative", -1, LedgerConsume, -1, -1, true},
		{"revoke zero", 5, LedgerRevoke, 0, 5, true},
		{"revoke to exactly zero", 3, LedgerRevoke, -3, 0, false},
		{"revoke one past zero", 3, LedgerRevoke, -4, 3, true},
		{"revoke from zero", 0, LedgerRevoke, -1, 0, true},
		{"expire zero", 5, LedgerExpire, 0, 5, true},
		{"expire to exactly zero", 2, LedgerExpire, -2, 0, false},
		{"expire one past zero", 2, LedgerExpire, -3, 2, true},
		{"grant large", 0, LedgerGrant, 1_000_000, 1_000_000, false},
		{"empty kind", 5, LedgerKind(""), 1, 5, true},
	}
	for _, c := range cases {
		got, err := ApplyLedger(c.balance, c.kind, c.delta)
		if (err != nil) != c.wantErr {
			t.Errorf("%s: err=%v wantErr=%v", c.name, err, c.wantErr)
		}
		if got != c.want {
			t.Errorf("%s: got %d want %d", c.name, got, c.want)
		}
		if c.wantErr && got != c.balance {
			t.Errorf("%s: a rejected movement must return the unchanged balance", c.name)
		}
	}
}

func TestLedgerReplay(t *testing.T) {
	// The balance is derived by replaying the append-only log.
	moves := []struct {
		kind  LedgerKind
		delta int
	}{
		{LedgerGrant, 1}, {LedgerPurchase, 3}, {LedgerConsume, -1}, {LedgerConsume, -1},
		{LedgerRefund, 1}, {LedgerRevoke, -1}, {LedgerConsume, -1}, {LedgerExpire, -1},
	}
	balance := 0
	for i, m := range moves {
		var err error
		balance, err = ApplyLedger(balance, m.kind, m.delta)
		if err != nil {
			t.Fatalf("move %d (%s %d): %v", i, m.kind, m.delta, err)
		}
	}
	if balance != 0 {
		t.Fatalf("replayed balance %d, want 0", balance)
	}
	if _, err := ApplyLedger(balance, LedgerConsume, -1); err != ErrInsufficientCredits {
		t.Fatalf("empty after replay: %v", err)
	}
}
