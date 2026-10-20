package store_test

import (
	"testing"
	"time"

	"github.com/forefy/vig/backend/internal/core"
	"github.com/forefy/vig/backend/internal/store"
)

func (f *fixture) mapped(t *testing.T, head string) store.Audit {
	t.Helper()
	f.grant(1)
	a, _, err := f.s.QueueAudit(f.ctx, f.audit(head, "P1"))
	if err != nil {
		t.Fatal(err)
	}
	doc := core.Surface{Adapter: "solana", BuiltAt: time.Now().UTC(), Nodes: []core.SurfaceNode{
		{ID: "p:P1", Kind: "program", Name: "P1", Source: "idl", Facts: map[string]string{"program": "P1", "bytecode_now": "aaaa"}},
		{ID: "ix:P1:deposit", Parent: "p:P1", Kind: "instruction", Name: "deposit", Source: "idl", Facts: map[string]string{"program": "P1"}},
	}}
	if err := f.s.SaveSurface(f.ctx, a.ID, doc); err != nil {
		t.Fatal(err)
	}
	return a
}

func TestLiveEvidenceReplacesByRefAndRollsUp(t *testing.T) {
	f := newFixture(t)
	a := f.mapped(t, "bbbb2222")
	at := time.Date(2026, 9, 27, 10, 15, 30, 0, time.UTC)
	rows := []core.SurfaceEvidence{
		{NodeID: "ix:P1:deposit", Kind: "tx", Ref: "sig1", At: &at, Summary: map[string]any{"source": "logs", "units": 100, "failed": true}},
		{NodeID: "ix:P1:deposit", Kind: "tx", Ref: "sig2", At: &at, Summary: map[string]any{"source": "logs", "units": 50}},
	}
	if n, err := f.s.AddLiveEvidence(f.ctx, a.ID, rows); err != nil || n != 2 {
		t.Fatalf("first add: n=%d err=%v", n, err)
	}
	// The same ref again is a replacement, not a second call.
	if n, err := f.s.AddLiveEvidence(f.ctx, a.ID, []core.SurfaceEvidence{{NodeID: "ix:P1:deposit", Kind: "tx", Ref: "sig1", At: &at, Summary: map[string]any{"source": "tx", "units": 100}}}); err != nil || n != 0 {
		t.Fatalf("replace: n=%d err=%v", n, err)
	}
	totals, err := f.s.RollupTotals(f.ctx, a.ID)
	if err != nil || len(totals) != 1 || totals[0].Calls != 2 || totals[0].Failed != 1 || totals[0].Units != 150 {
		t.Fatalf("totals: %+v %v", totals, err)
	}
	minutes, err := f.s.RollupMinutes(f.ctx, a.ID, at.Add(-time.Hour))
	if err != nil || len(minutes) != 1 || !minutes[0].Minute.Equal(at.Truncate(time.Minute)) || minutes[0].Calls != 2 {
		t.Fatalf("minutes: %+v %v", minutes, err)
	}
	live, err := f.s.ListLiveEvidence(f.ctx, a.ID, "", 10)
	if err != nil || len(live) != 2 || live[0].Ref != "sig1" || live[0].Summary["source"] != "tx" {
		t.Fatalf("live rows: %+v %v", live, err)
	}
	after, err := f.s.ListLiveEvidence(f.ctx, a.ID, live[0].ID, 10)
	if err != nil || len(after) != 1 || after[0].Ref != "sig2" {
		t.Fatalf("after cursor: %+v %v", after, err)
	}
	if back, err := f.s.ListLiveEvidenceBefore(f.ctx, a.ID, at, 1); err != nil || len(back) != 1 || back[0].Ref != "sig2" {
		t.Fatalf("before: %+v %v", back, err)
	}
	if _, err := f.s.AddLiveEvidence(f.ctx, a.ID, []core.SurfaceEvidence{{NodeID: "ix:P1:deposit", Kind: "tx", Ref: "sig3", At: &at, Summary: map[string]any{"source": "tx", "keys": []string{"A", "B"}}}}); err != nil {
		t.Fatal(err)
	}
	if full, err := f.s.ListLiveEvidenceFull(f.ctx, a.ID, 10); err != nil || len(full) != 1 || full[0].Ref != "sig3" {
		t.Fatalf("full: %+v %v", full, err)
	}
	if back, err := f.s.ListLiveEvidenceBefore(f.ctx, a.ID, at.Add(-time.Second), 10); err != nil || len(back) != 0 {
		t.Fatalf("before the rows: %+v %v", back, err)
	}
	if span, err := f.s.ListLiveEvidenceBetween(f.ctx, a.ID, at.Add(-time.Second), at, 10); err != nil || len(span) != 3 || span[0].Ref != "sig1" {
		t.Fatalf("between: %+v %v", span, err)
	}
	if span, err := f.s.ListLiveEvidenceBetween(f.ctx, a.ID, at, at.Add(time.Hour), 10); err != nil || len(span) != 0 {
		t.Fatalf("between after: %+v %v", span, err)
	}
	if b, err := f.s.RollupBuckets(f.ctx, a.ID, 5*time.Minute); err != nil || len(b) != 1 || b[0].Calls != 3 || b[0].Failed != 1 || !b[0].At.Equal(at.Truncate(5*time.Minute)) {
		t.Fatalf("buckets: %+v %v", b, err)
	}
	// Sampled listings never see live rows, so the map's sample stays what
	// the report cites.
	sampled, err := f.s.ListSurfaceEvidence(f.ctx, a.ID, 10)
	if err != nil || len(sampled) != 0 {
		t.Fatalf("sampled listing saw live rows: %+v %v", sampled, err)
	}
	if n, err := f.s.CountSurfaceEvidence(f.ctx, a.ID); err != nil || n != 0 {
		t.Fatalf("count: %d %v", n, err)
	}
	// Pruning keeps the rollups.
	if _, err := f.s.Pool.Exec(f.ctx, `update surface_evidence set created_at = now() - interval '8 days' where audit_id=$1`, a.ID); err != nil {
		t.Fatal(err)
	}
	if n, err := f.s.PruneLiveEvidence(f.ctx, store.LiveEvidenceKept); err != nil || n != 3 {
		t.Fatalf("prune: %d %v", n, err)
	}
	if totals, _ := f.s.RollupTotals(f.ctx, a.ID); len(totals) != 1 || totals[0].Calls != 3 {
		t.Fatalf("rollups lost on prune: %+v", totals)
	}
}

func TestWatchRowCursorsCoverageAndFacts(t *testing.T) {
	f := newFixture(t)
	a := f.mapped(t, "cccc3333")
	if _, err := f.s.GetWatch(f.ctx, a.ID); err != store.ErrNotFound {
		t.Fatalf("before ensure: %v", err)
	}
	if err := f.s.EnsureWatch(f.ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC().Truncate(time.Second)
	if err := f.s.SaveWatchCursor(f.ctx, a.ID, "P1", core.Cursor{Ref: "sig9", Slot: 900, At: &at}); err != nil {
		t.Fatal(err)
	}
	if err := f.s.SaveWatchAccounts(f.ctx, a.ID, []core.WatchedAccount{{Address: "V", NodeID: "acct:P1:deposit:vault", Role: "state"}}); err != nil {
		t.Fatal(err)
	}
	if err := f.s.SetWatchState(f.ctx, a.ID, core.WatchLive, ""); err != nil {
		t.Fatal(err)
	}
	w, err := f.s.GetWatch(f.ctx, a.ID)
	if err != nil || w.State != core.WatchLive || w.Cursors["P1"].Ref != "sig9" || w.LastSlot != 900 || w.LastEventAt == nil || len(w.Accounts) != 1 {
		t.Fatalf("watch: %+v %v", w, err)
	}
	id, err := f.s.AddCoverage(f.ctx, a.ID, core.CoverageSpan{Subject: "P1", Kind: core.CoverageLive, FromSlot: 900, ToSlot: 900})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.s.ExtendCoverage(f.ctx, a.ID, id, 950, &at); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.AddCoverage(f.ctx, a.ID, core.CoverageSpan{Subject: "P1", Kind: core.CoverageGap, FromSlot: 800, ToSlot: 899}); err != nil {
		t.Fatal(err)
	}
	spans, err := f.s.ListCoverage(f.ctx, a.ID, 10)
	if err != nil || len(spans) != 2 || spans[0].Kind != core.CoverageGap || spans[1].ToSlot != 950 || spans[1].ToAt == nil {
		t.Fatalf("coverage: %+v %v", spans, err)
	}
	// Facts land on the stored map without losing the rest of it.
	err = f.s.UpdateSurface(f.ctx, a.ID, func(doc *core.Surface) bool {
		for i := range doc.Nodes {
			if doc.Nodes[i].ID == "p:P1" {
				doc.Nodes[i].Facts["bytecode_now"] = "bbbb"
				return true
			}
		}
		return false
	})
	if err != nil {
		t.Fatal(err)
	}
	doc, err := f.s.GetSurface(f.ctx, a.ID)
	if err != nil || len(doc.Nodes) != 2 || doc.Nodes[0].Facts["bytecode_now"] != "bbbb" {
		t.Fatalf("updated map: %+v %v", doc, err)
	}
}

func TestListWatchableFollowsDeliveryAndPlans(t *testing.T) {
	f := newFixture(t)
	a := f.mapped(t, "dddd4444")
	ids := func() map[string]bool {
		t.Helper()
		list, err := f.s.ListWatchable(f.ctx)
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]bool{}
		for _, x := range list {
			out[x.ID] = true
		}
		return out
	}
	if !ids()[a.ID] {
		t.Fatal("a mapped, queued audit is watchable")
	}
	// An audit with no map is not.
	f.grant(1)
	b, _, err := f.s.QueueAudit(f.ctx, f.audit("eeee5555", "P2"))
	if err != nil {
		t.Fatal(err)
	}
	if ids()[b.ID] {
		t.Fatal("an unmapped audit is not watchable")
	}
	// Delivered long ago on a free account: the window has passed.
	if _, err := f.s.Pool.Exec(f.ctx, `update audits set state='delivered', updated_at = now() - interval '40 days' where id=$1`, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.Pool.Exec(f.ctx, `insert into audit_events(id, audit_id, kind, actor, state, payload, created_at) values($1,$2,'delivered','operator','delivered','{}', now() - interval '40 days')`, store.NewID(), a.ID); err != nil {
		t.Fatal(err)
	}
	if ids()[a.ID] {
		t.Fatal("delivered 40 days ago without a plan is not watchable")
	}
	// An active plan keeps it live.
	if _, err := f.s.CreatePlan(f.ctx, f.s.Pool, store.Plan{UserID: f.user.ID, Kind: "pro", StartsAt: time.Now().Add(-time.Hour), EndsAt: time.Now().Add(24 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if !ids()[a.ID] {
		t.Fatal("an active plan keeps a delivered audit watchable")
	}
	// An ended watch is skipped until resumed.
	if err := f.s.EnsureWatch(f.ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-time.Minute)
	if err := f.s.EndWatch(f.ctx, a.ID, &past); err != nil {
		t.Fatal(err)
	}
	if ids()[a.ID] {
		t.Fatal("an ended watch is not watchable")
	}
	if err := f.s.EndWatch(f.ctx, a.ID, nil); err != nil {
		t.Fatal(err)
	}
	if !ids()[a.ID] {
		t.Fatal("a resumed watch is watchable")
	}
}
