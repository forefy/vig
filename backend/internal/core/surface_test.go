package core

import (
	"testing"
	"time"
)

func TestSurfaceAttendDerivesAndRollsUp(t *testing.T) {
	s := Surface{Adapter: "solana", Nodes: []SurfaceNode{
		{ID: "p:A", Kind: "program", Name: "A", Facts: map[string]string{"program": "A"}},
		{ID: "ix:A:deposit", Parent: "p:A", Kind: "instruction", Name: "deposit", Facts: map[string]string{"program": "A"}},
		{ID: "ix:A:withdraw_all", Parent: "p:A", Kind: "instruction", Name: "withdraw_all", Facts: map[string]string{"program": "A"}},
		{ID: "ix:A:close", Parent: "p:A", Kind: "instruction", Name: "close", Facts: map[string]string{"program": "A"}},
		{ID: "acct:A:close:vault", Parent: "ix:A:close", Kind: "account", Name: "vault"},
	}}
	now := time.Now()
	ev := []SurfaceEvidence{
		{NodeID: "ix:A:deposit", Kind: "tx", Ref: "s1", At: &now},
		{NodeID: "ix:A:deposit", Kind: "tx", Ref: "s2"},
		{NodeID: "ix:A:close", Kind: "review"},
		{NodeID: "ix:nope", Kind: "tx"},
	}
	findings := []Finding{{Ordinal: 2, Location: Location{File: "a.rs", LineStart: 1, ProgramID: "A", Instruction: "withdrawAll"}}}
	s.Attend(ev, findings)
	get := func(id string) SurfaceNode {
		for _, n := range s.Nodes {
			if n.ID == id {
				return n
			}
		}
		t.Fatalf("no node %s", id)
		return SurfaceNode{}
	}
	if n := get("ix:A:deposit"); n.Attention != AttentionObserved || n.Observed != 2 || n.LastSeen == nil {
		t.Errorf("deposit: %+v", n)
	}
	if n := get("ix:A:withdraw_all"); n.Attention != AttentionFinding || len(n.Findings) != 1 || n.Findings[0] != 2 {
		t.Errorf("withdraw_all: %+v", n)
	}
	if n := get("ix:A:close"); n.Attention != AttentionReviewed {
		t.Errorf("close: %+v", n)
	}
	if n := get("acct:A:close:vault"); n.Attention != AttentionUnseen {
		t.Errorf("vault: %+v", n)
	}
	if n := get("p:A"); n.Attention != AttentionFinding {
		t.Errorf("program should roll up to finding, got %s", n.Attention)
	}
}

func TestSurfaceMergeIsIdempotent(t *testing.T) {
	s := Surface{Nodes: []SurfaceNode{{ID: "a"}}, Edges: []SurfaceEdge{{From: "a", To: "b", Kind: "cpi", Count: 1}}}
	added := s.Merge([]SurfaceNode{{ID: "a"}, {ID: "b"}, {ID: ""}}, []SurfaceEdge{{From: "a", To: "b", Kind: "cpi", Count: 2}, {From: "b", To: "a", Kind: "cpi"}})
	if added != 2 || len(s.Nodes) != 2 || len(s.Edges) != 2 || s.Edges[0].Count != 3 {
		t.Fatalf("added=%d nodes=%d edges=%v", added, len(s.Nodes), s.Edges)
	}
}

func TestSurfaceEnrichKeepsFactsAndAddsMissing(t *testing.T) {
	s := Surface{Nodes: []SurfaceNode{{ID: "ix:p:a", Kind: "instruction", Name: "a", Source: "idl", Facts: map[string]string{"args": "x:u64"}}}}
	n := s.Enrich([]SurfaceNode{
		{ID: "ix:p:a", Source: "static", Facts: map[string]string{"args": "different", "file": "src/lib.rs"}},
		{ID: "ix:p:b", Source: "static", Kind: "instruction", Name: "b"},
	}, []SurfaceEdge{{From: "ix:p:a", To: "p:t", Kind: "cpi", Source: "static", Facts: map[string]string{"signed": "true"}}})
	if n != 2 {
		t.Errorf("added %d", n)
	}
	a := s.Nodes[0]
	if a.Source != "idl" || a.Facts["args"] != "x:u64" || a.Facts["file"] != "src/lib.rs" {
		t.Errorf("existing node: %+v", a)
	}
	s.Merge(nil, []SurfaceEdge{{From: "ix:p:a", To: "p:t", Kind: "cpi", Count: 2}})
	if len(s.Edges) != 1 || s.Edges[0].Count != 2 || s.Edges[0].Facts["signed"] != "true" || s.Edges[0].Source != "static" {
		t.Errorf("edge: %+v", s.Edges)
	}
}

func statsSurface() Surface {
	return Surface{Adapter: "solana", Nodes: []SurfaceNode{
		{ID: "p:A", Kind: "program", Name: "A"},
		{ID: "ix:A:deposit", Parent: "p:A", Kind: "instruction", Name: "deposit", Facts: map[string]string{"diff": "changed"}},
		{ID: "ix:A:withdraw", Parent: "p:A", Kind: "instruction", Name: "withdraw", Facts: map[string]string{"diff": "added"}},
		{ID: "acct:A:deposit:vault", Parent: "ix:A:deposit", Kind: "account", Name: "vault"},
		{ID: "p:T", Kind: "program", Name: "T", External: true},
	}, Edges: []SurfaceEdge{
		{From: "ix:A:deposit", To: "p:T", Kind: "cpi", Count: 3},
		{From: "ix:A:withdraw", To: "p:T", Kind: "cpi", Source: "static"},
	}}
}

func TestSurfaceStatsAndCoverage(t *testing.T) {
	s := statsSurface()
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	t1 := t0.Add(time.Hour)
	t2 := t0.Add(2 * time.Hour)
	ev := []SurfaceEvidence{
		// Shapes as they come back from jsonb: float64 numbers, []any lists, string deltas.
		{NodeID: "ix:A:deposit", Kind: "tx", At: &t1, Summary: map[string]any{
			"failed": false, "units": float64(5000), "signer_keys": []any{"k1", "k2"},
			"moves": []any{map[string]any{"mint": "USDC", "decimals": float64(6), "delta": "-1500000"}, map[string]any{"mint": "SOL", "decimals": float64(9), "delta": "20"}}}},
		{NodeID: "ix:A:deposit", Kind: "tx", At: &t0, Summary: map[string]any{
			"failed": true, "error": "InsufficientFunds", "units": float64(200), "signer_keys": []any{"k1"},
			"moves": []any{map[string]any{"mint": "USDC", "decimals": float64(6), "delta": "99999999999999999999"}}}},
		{NodeID: "ix:A:deposit", Kind: "tx", At: &t2, Summary: map[string]any{"failed": true, "units": float64(0)}},
		// Native shapes an in-process adapter may hand over.
		{NodeID: "ix:A:deposit", Kind: "tx", Summary: map[string]any{"units": 900, "signer_keys": []string{"k3"}}},
		{NodeID: "ix:A:deposit", Kind: "replay", At: &t1},
		{NodeID: "ix:A:deposit", Kind: "review"},
		{NodeID: "ix:A:withdraw", Kind: "review"},
		{NodeID: "ix:nope", Kind: "tx"},
	}
	s.Attend(ev, nil)
	get := func(id string) SurfaceNode {
		for _, n := range s.Nodes {
			if n.ID == id {
				return n
			}
		}
		t.Fatalf("no node %s", id)
		return SurfaceNode{}
	}
	st := get("ix:A:deposit").Stats
	if st == nil {
		t.Fatal("deposit has no stats")
	}
	if st.Ok != 2 || st.Failed != 2 || st.Replays != 1 || st.Signers != 3 {
		t.Errorf("counts: %+v", st)
	}
	if st.Errors["InsufficientFunds"] != 1 || st.Errors["unknown"] != 1 || len(st.Errors) != 2 {
		t.Errorf("errors: %v", st.Errors)
	}
	if st.UnitsMin != 200 || st.UnitsMed != 900 || st.UnitsMax != 5000 {
		t.Errorf("units: %d %d %d", st.UnitsMin, st.UnitsMed, st.UnitsMax)
	}
	if st.FirstSeen == nil || !st.FirstSeen.Equal(t0) {
		t.Errorf("first_seen: %v", st.FirstSeen)
	}
	want := []TokenMove{{Mint: "USDC", Decimals: 6, Volume: "100000000000001499999", Count: 2}, {Mint: "SOL", Decimals: 9, Volume: "20", Count: 1}}
	if len(st.Moves) != 2 || st.Moves[0] != want[0] || st.Moves[1] != want[1] {
		t.Errorf("moves: %+v", st.Moves)
	}
	if n := get("ix:A:withdraw"); n.Stats != nil {
		t.Errorf("review-only node should carry no stats: %+v", n.Stats)
	}
	if n := get("acct:A:deposit:vault"); n.Stats != nil {
		t.Errorf("untouched node should carry no stats: %+v", n.Stats)
	}
	cov := s.Coverage
	if cov == nil {
		t.Fatal("no coverage")
	}
	if cov.Instructions != 2 || cov.InstructionsObserved != 2 || cov.InstructionsReplayed != 1 {
		t.Errorf("instructions: %+v", cov)
	}
	if cov.Edges != 2 || cov.EdgesObserved != 1 || cov.Signers != 3 {
		t.Errorf("edges/signers: %+v", cov)
	}
	if cov.FirstSeen == nil || !cov.FirstSeen.Equal(t0) {
		t.Errorf("coverage first_seen: %v", cov.FirstSeen)
	}
	if cov.Changed != 1 || cov.Added != 1 {
		t.Errorf("diff: %+v", cov)
	}
	// Derived fields are recomputed, not accumulated.
	s.Attend(nil, nil)
	if get("ix:A:deposit").Stats != nil || s.Coverage.InstructionsObserved != 0 || s.Coverage.Signers != 0 {
		t.Errorf("stale derived state after empty attend: %+v %+v", get("ix:A:deposit").Stats, s.Coverage)
	}
}

func TestSurfaceStatsProgramAggregatesChildren(t *testing.T) {
	s := statsSurface()
	ev := []SurfaceEvidence{
		{NodeID: "ix:A:deposit", Kind: "tx", Summary: map[string]any{"units": float64(100), "signer_keys": []any{"k1", "k2"},
			"moves": []any{map[string]any{"mint": "USDC", "decimals": float64(6), "delta": "-5"}}}},
		{NodeID: "ix:A:deposit", Kind: "tx", Summary: map[string]any{"units": float64(300), "signer_keys": []any{"k2"}}},
		{NodeID: "ix:A:withdraw", Kind: "tx", Summary: map[string]any{"failed": true, "error": "Unauthorized", "units": float64(700), "signer_keys": []any{"k2", "k3"},
			"moves": []any{map[string]any{"mint": "USDC", "decimals": float64(6), "delta": "7"}, map[string]any{"mint": "BONK", "decimals": float64(5), "delta": "-1"}}}},
		{NodeID: "ix:A:withdraw", Kind: "replay"},
		// A row on the program itself counts once, not twice.
		{NodeID: "p:A", Kind: "tx", Summary: map[string]any{"units": float64(50), "signer_keys": []any{"k1"}}},
	}
	s.Attend(ev, nil)
	var p *NodeStats
	for _, n := range s.Nodes {
		if n.ID == "p:A" {
			p = n.Stats
		}
	}
	if p == nil {
		t.Fatal("program has no stats")
	}
	if p.Ok != 3 || p.Failed != 1 || p.Replays != 1 || p.Errors["Unauthorized"] != 1 {
		t.Errorf("counts: %+v", p)
	}
	if p.Signers != 3 {
		t.Errorf("signers should be the union, got %d", p.Signers)
	}
	if p.UnitsMin != 50 || p.UnitsMed != 100 || p.UnitsMax != 700 {
		t.Errorf("units over all child rows: %d %d %d", p.UnitsMin, p.UnitsMed, p.UnitsMax)
	}
	want := []TokenMove{{Mint: "USDC", Decimals: 6, Volume: "12", Count: 2}, {Mint: "BONK", Decimals: 5, Volume: "1", Count: 1}}
	if len(p.Moves) != 2 || p.Moves[0] != want[0] || p.Moves[1] != want[1] {
		t.Errorf("merged moves: %+v", p.Moves)
	}
	if s.Coverage.Signers != 3 || s.Coverage.InstructionsReplayed != 1 {
		t.Errorf("coverage: %+v", s.Coverage)
	}
}

func TestSurfaceCoverageCountsDiffFacts(t *testing.T) {
	s := Surface{Nodes: []SurfaceNode{
		{ID: "ix:A:a", Kind: "instruction", Facts: map[string]string{"diff": "changed"}},
		{ID: "ix:A:b", Kind: "instruction", Facts: map[string]string{"diff": "changed"}},
		{ID: "ix:A:c", Kind: "instruction", Facts: map[string]string{"diff": "added"}},
		{ID: "ix:A:d", Kind: "instruction", Facts: map[string]string{"diff": "same"}},
		{ID: "ix:A:e", Kind: "instruction"},
		{ID: "ix:T:x", Kind: "instruction", External: true, Facts: map[string]string{"diff": "changed"}},
		{ID: "r:/login", Kind: "route", Facts: map[string]string{"diff": "added"}},
		{ID: "acct:v", Kind: "account", Facts: map[string]string{"diff": "changed"}},
	}}
	s.Attend(nil, nil)
	c := s.Coverage
	if c == nil || c.Instructions != 6 || c.Changed != 2 || c.Added != 2 || c.InstructionsObserved != 0 {
		t.Errorf("coverage: %+v", c)
	}
}
