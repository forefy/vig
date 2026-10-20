package solana

import (
	"testing"
	"time"

	"github.com/forefy/vig/backend/internal/chain"
	"github.com/forefy/vig/backend/internal/core"
)

func TestTrustCandidates(t *testing.T) {
	ext := func(pid string) core.SurfaceNode {
		return core.SurfaceNode{ID: programNodeID(pid), Kind: "program", External: true, Facts: map[string]string{"program": pid}}
	}
	nodes := []core.SurfaceNode{
		{ID: "p:Subject", Kind: "program", Facts: map[string]string{"program": "Subject"}},
		ext("Zed111"), ext("TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA"), ext("SysvarRent111111111111111111111111111111111"),
		ext("Alpha11"), ext("Alpha11"), ext("Other"),
		{ID: "p:static:dynamic", Kind: "program", External: true, Name: "dynamic"},
		{ID: "ix:Subject:x", Kind: "instruction", External: true, Facts: map[string]string{"program": "Subject"}},
	}
	got := trustCandidates(nodes, []string{"Subject", "Other"}, 10)
	if len(got) != 2 || got[0] != "Alpha11" || got[1] != "Zed111" {
		t.Errorf("candidates %v", got)
	}
	if got := trustCandidates(nodes, nil, 1); len(got) != 1 || got[0] != "Alpha11" {
		t.Errorf("cap %v", got)
	}
}

func TestTrustFacts(t *testing.T) {
	yes, no := true, false
	pf := chain.ProgramFacts{Exists: true, Loader: "upgradeable", UpgradeAuthority: "Auth", IDLPresent: true, DeployedAt: time.Unix(1_700_000_000, 0).UTC()}
	f := trustFacts(pf, &yes)
	want := map[string]string{"loader": "upgradeable", "authority": "Auth", "idl": "true", "verified": "true", "last_upgrade": "2023-11-14T22:13:20Z"}
	for k, v := range want {
		if f[k] != v {
			t.Errorf("%s = %q, want %q", k, f[k], v)
		}
	}
	if _, ok := f["immutable"]; ok {
		t.Error("an authority means mutable")
	}
	f = trustFacts(chain.ProgramFacts{Exists: true, Loader: "non-upgradeable"}, &no)
	if f["immutable"] != "true" || f["verified"] != "false" || f["idl"] != "" || f["last_upgrade"] != "" {
		t.Errorf("frozen, unverified: %+v", f)
	}
	f = trustFacts(chain.ProgramFacts{Exists: true, Loader: "upgradeable"}, nil)
	if f["immutable"] != "true" {
		t.Error("upgradeable with no authority is immutable")
	}
	if _, ok := f["verified"]; ok {
		t.Error("no registry answer, no verified fact")
	}
	if trustFacts(chain.ProgramFacts{}, &yes) != nil {
		t.Error("a missing program has no facts")
	}
}
