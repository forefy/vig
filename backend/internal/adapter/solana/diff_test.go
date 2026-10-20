package solana

import (
	"testing"

	"github.com/forefy/vig/backend/internal/core"
	"github.com/forefy/vig/backend/internal/rustsrc"
)

func TestDiffFactsOnTheMap(t *testing.T) {
	head, err := rustsrc.Load("../../rustsrc/testdata/diff_head", "")
	if err != nil {
		t.Fatal(err)
	}
	base, err := rustsrc.Load("../../rustsrc/testdata/diff_base", "")
	if err != nil {
		t.Fatal(err)
	}
	pid := "Fg6PaFpoGXkYsidMpWTK6W2BeZ7FEfcYkg476zPFsLnS"
	d := diffFacts(head, base, pid)
	want := map[string]string{"deposit": "same", "withdraw": "changed", "close": "added"}
	if len(d) != len(want) {
		t.Fatalf("diff %v", d)
	}
	for k, v := range want {
		if d[k] != v {
			t.Errorf("%s = %q, want %q", k, d[k], v)
		}
	}
	if diffFacts(head, &rustsrc.Model{}, pid)["deposit"] != "added" {
		t.Error("a base without the crate makes every handler new")
	}

	c, _ := head.CrateFor(pid)
	s := core.Surface{Adapter: Key}
	s.Merge(programNodes(pid, nil, "traffic"), nil)
	s.Enrich(staticNodes(pid, c, nil))
	applyDiff(&s, pid, head, base)
	byID := map[string]core.SurfaceNode{}
	for _, n := range s.Nodes {
		byID[n.ID] = n
	}
	if p := byID[programNodeID(pid)]; p.Facts["diff_changed"] != "1" || p.Facts["diff_added"] != "1" {
		t.Errorf("program counts %+v", p.Facts)
	}
	if byID[ixNodeID(pid, "withdraw")].Facts["diff"] != "changed" || byID[ixNodeID(pid, "close")].Facts["diff"] != "added" || byID[ixNodeID(pid, "deposit")].Facts["diff"] != "same" {
		t.Errorf("instruction facts not set")
	}
	if a := byID[acctNodeID(pid, "deposit", "book")]; a.Facts["bump"] != "true" {
		t.Errorf("bump fact: %+v", a.Facts)
	}
}
