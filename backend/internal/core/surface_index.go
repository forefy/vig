package core

import "time"

// The surface across audits: every node vig has mapped, flattened with the
// audit it belongs to, so one query line folds hosts, routes, programs,
// instructions, parameters and accounts from every target at once. Each
// node is read after Attend, so its attention, counts and findings are the
// ones its own audit's map shows.

// IndexAudit is the audit a node belongs to, as the index lists it.
type IndexAudit struct {
	ID       string           `json:"id"`
	TargetID string           `json:"target_id"`
	Repo     string           `json:"repo"`
	Adapter  string           `json:"adapter" enum:"solana,ethereum,webapp,repo"`
	BaseURL  string           `json:"base_url,omitempty" doc:"For a site, the origin the audit is about"`
	State    AuditState       `json:"state"`
	HeadSHA  string           `json:"head_sha"`
	Coverage string           `json:"coverage" enum:"full,diff"`
	BuiltAt  time.Time        `json:"built_at"`
	Map      *SurfaceCoverage `json:"map,omitempty" doc:"How much of the map traffic and replays reached"`
	Nodes    int              `json:"nodes"`
}

// IndexFinding is a finding pinned to a node, enough to read on a row.
type IndexFinding struct {
	Ordinal  int      `json:"ordinal"`
	Severity Severity `json:"severity" enum:"critical,high,medium,low,info"`
	Status   string   `json:"status" enum:"open,acknowledged,fixed,disputed,archived"`
	Title    string   `json:"title"`
}

// IndexNode is one node of one audit's map.
type IndexNode struct {
	AuditID   string            `json:"audit_id"`
	ID        string            `json:"id"`
	Parent    string            `json:"parent,omitempty"`
	Kind      string            `json:"kind" doc:"program, instruction, account, host, route, param"`
	Name      string            `json:"name"`
	Source    string            `json:"source"`
	External  bool              `json:"external,omitempty"`
	Facts     map[string]string `json:"facts,omitempty"`
	Attention AttentionState    `json:"attention" enum:"unseen,observed,reviewed,finding"`
	Observed  int               `json:"observed"`
	LastSeen  *time.Time        `json:"last_seen,omitempty"`
	Findings  []IndexFinding    `json:"findings,omitempty"`
	Ok        int               `json:"ok,omitempty"`
	Failed    int               `json:"failed,omitempty"`
	Signers   int               `json:"signers,omitempty"`
	Replays   int               `json:"replays,omitempty"`
}

// SurfaceIndex is the surface across audits.
type SurfaceIndex struct {
	Audits []IndexAudit `json:"audits"`
	Nodes  []IndexNode  `json:"nodes"`
}

// IndexNodes flattens an attended map into index rows. Findings are joined
// by ordinal from the audit's report, so a row carries the title and the
// severity the report shows.
func IndexNodes(auditID string, s Surface, findings []Finding) []IndexNode {
	byOrdinal := map[int]Finding{}
	for _, f := range findings {
		byOrdinal[f.Ordinal] = f
	}
	out := make([]IndexNode, 0, len(s.Nodes))
	for _, n := range s.Nodes {
		row := IndexNode{AuditID: auditID, ID: n.ID, Parent: n.Parent, Kind: n.Kind, Name: n.Name, Source: n.Source, External: n.External, Facts: n.Facts,
			Attention: n.Attention, Observed: n.Observed, LastSeen: n.LastSeen}
		if n.Stats != nil {
			row.Ok, row.Failed, row.Signers, row.Replays = n.Stats.Ok, n.Stats.Failed, n.Stats.Signers, n.Stats.Replays
		}
		for _, o := range n.Findings {
			if f, ok := byOrdinal[o]; ok {
				row.Findings = append(row.Findings, IndexFinding{Ordinal: f.Ordinal, Severity: f.Severity, Status: string(f.Status), Title: f.Title})
			}
		}
		out = append(out, row)
	}
	return out
}
