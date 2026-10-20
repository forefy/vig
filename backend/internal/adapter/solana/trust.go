package solana

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/forefy/vig/backend/internal/chain"
	"github.com/forefy/vig/backend/internal/core"
)

// trustLimit caps how many callees one build looks up: each costs a few
// reads and a registry call, and a map rarely composes more than a handful
// of programs that are not well known.
const trustLimit = 10

// trustCandidates picks the external programs worth a look: reached
// through an edge, not a well-known program or a sysvar, not a subject.
// Sorted by id so the cap cuts the same set every build.
func trustCandidates(nodes []core.SurfaceNode, subjects []string, limit int) []string {
	skip := map[string]bool{}
	for _, pid := range subjects {
		skip[pid] = true
	}
	var out []string
	for _, n := range nodes {
		pid := n.Facts["program"]
		if !n.External || n.Kind != "program" || pid == "" || skip[pid] {
			continue
		}
		if _, known := knownPrograms[pid]; known || strings.HasPrefix(pid, "Sysvar") {
			continue
		}
		skip[pid] = true
		out = append(out, pid)
	}
	sort.Strings(out)
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// trustFacts turns a callee's program facts and registry answer into node
// facts. verified nil means the registry was not asked or did not answer.
func trustFacts(pf chain.ProgramFacts, verified *bool) map[string]string {
	if !pf.Exists {
		return nil
	}
	f := map[string]string{"loader": pf.Loader}
	switch {
	case pf.Loader == "upgradeable" && pf.UpgradeAuthority == "", pf.Loader == "non-upgradeable":
		f["immutable"] = "true"
	}
	if pf.UpgradeAuthority != "" {
		f["authority"] = pf.UpgradeAuthority
	}
	if pf.IDLPresent {
		f["idl"] = "true"
	}
	if verified != nil {
		f["verified"] = "false"
		if *verified {
			f["verified"] = "true"
		}
	}
	if !pf.DeployedAt.IsZero() {
		f["last_upgrade"] = pf.DeployedAt.UTC().Format(time.RFC3339)
	}
	return f
}

// trust looks up every callee the map reached and records who can change
// it, whether it publishes an IDL, and whether its build is verified.
// Each lookup is paced; a failure leaves that callee's facts absent.
func (b *SurfaceBuilder) trust(ctx context.Context, s *core.Surface, subjects []string, budget core.SurfaceBudget) {
	for _, pid := range trustCandidates(s.Nodes, subjects, trustLimit) {
		if !pace(ctx, budget) {
			return
		}
		pf, err := b.Chain.Program(ctx, pid)
		if err != nil {
			continue
		}
		var verified *bool
		if b.Osec != nil {
			if !pace(ctx, budget) {
				return
			}
			if vb, err := b.Osec.Status(ctx, pid); err == nil {
				v := vb.Verified
				verified = &v
			}
		}
		setFacts(s, programNodeID(pid), trustFacts(pf, verified))
	}
}
