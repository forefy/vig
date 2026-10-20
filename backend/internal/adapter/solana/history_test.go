package solana

import (
	"testing"
	"time"

	"github.com/forefy/vig/backend/internal/chain"
	"github.com/forefy/vig/backend/internal/core"
)

func TestParseHistoryCountsSuccessesAndSpan(t *testing.T) {
	bt := func(v int64) *int64 { return &v }
	sigs := []chain.Signature{
		{Signature: "newest", BlockTime: bt(1_700_000_300)},
		{Signature: "failed", Err: map[string]any{"InstructionError": []any{}}, BlockTime: bt(1_700_000_200)},
		{Signature: "no-time"},
		{Signature: "deploy", BlockTime: bt(1_700_000_000)},
	}
	h := parseHistory(sigs, 100)
	if h.Upgrades != 3 || h.Capped {
		t.Errorf("upgrades %d capped %v", h.Upgrades, h.Capped)
	}
	if !h.First.Equal(time.Unix(1_700_000_000, 0)) || !h.Last.Equal(time.Unix(1_700_000_300, 0)) {
		t.Errorf("span %v..%v", h.First, h.Last)
	}
	if parseHistory(sigs, 4).Capped != true {
		t.Error("a full list is a floor")
	}
	if e := parseHistory(nil, 100); e.Upgrades != 0 || !e.First.IsZero() {
		t.Errorf("empty %+v", e)
	}
}

func TestHistoryFacts(t *testing.T) {
	h := &upgradeHistory{Upgrades: 100, Capped: true, First: time.Unix(1_600_000_000, 0).UTC(), Last: time.Unix(1_700_000_000, 0).UTC()}
	pf := chain.ProgramFacts{Exists: true, Loader: "upgradeable", UpgradeAuthority: "Auth", LastDeploySlot: 42, BytecodeSHA256: "abc"}
	f := historyFacts(pf, h)
	want := map[string]string{"loader": "upgradeable", "authority": "Auth", "upgrades": "100+", "first_deploy": "2020-09-13T12:26:40Z", "last_upgrade": "2023-11-14T22:13:20Z", "last_deploy_slot": "42", "bytecode_now": "abc"}
	for k, v := range want {
		if f[k] != v {
			t.Errorf("%s = %q, want %q", k, f[k], v)
		}
	}
	if _, ok := f["immutable"]; ok {
		t.Error("an authority means mutable")
	}
	if f := historyFacts(chain.ProgramFacts{Exists: true, Loader: "upgradeable"}, nil); f["immutable"] != "true" || f["upgrades"] != "" {
		t.Errorf("frozen upgradeable: %+v", f)
	}
	if f := historyFacts(chain.ProgramFacts{Exists: true, Loader: "non-upgradeable", BytecodeSHA256: "x"}, nil); f["immutable"] != "true" || f["loader"] != "non-upgradeable" {
		t.Errorf("non-upgradeable: %+v", f)
	}
	if f := historyFacts(chain.ProgramFacts{Exists: true, Loader: "loader_v4", UpgradeAuthority: "A"}, nil); f["immutable"] != "" || f["authority"] != "A" {
		t.Errorf("loader v4: %+v", f)
	}
	if historyFacts(chain.ProgramFacts{}, h) != nil {
		t.Error("a missing program has no facts")
	}
}

func TestSetFactsOverwrites(t *testing.T) {
	s := core.Surface{Nodes: []core.SurfaceNode{{ID: "p:X", Facts: map[string]string{"program": "X", "loader": "guess"}}, {ID: "p:Y"}}}
	setFacts(&s, "p:X", map[string]string{"loader": "upgradeable"})
	setFacts(&s, "p:Y", map[string]string{"idl": "true"})
	setFacts(&s, "p:Z", map[string]string{"idl": "true"})
	if s.Nodes[0].Facts["loader"] != "upgradeable" || s.Nodes[0].Facts["program"] != "X" || s.Nodes[1].Facts["idl"] != "true" || len(s.Nodes) != 2 {
		t.Errorf("nodes %+v", s.Nodes)
	}
}
