package core

import (
	"context"
	"sort"
	"strings"
	"time"
)

// AttentionState is how far the audit reached one node of the surface. It
// is derived from evidence and findings, never edited.
type AttentionState string

const (
	AttentionUnseen   AttentionState = "unseen"   // mapped, nothing touched it
	AttentionObserved AttentionState = "observed" // seen in traffic or on chain
	AttentionReviewed AttentionState = "reviewed" // an operator looked at it
	AttentionFinding  AttentionState = "finding"  // a finding points at it
)

var attentionRank = map[AttentionState]int{AttentionUnseen: 0, AttentionObserved: 1, AttentionReviewed: 2, AttentionFinding: 3}

// SurfaceNode is one reachable feature of the target: a program, an
// instruction and its accounts on chain; a route and its parameters on the
// web. The vocabulary is the adapter's; the tree is the same for every one.
type SurfaceNode struct {
	ID       string            `json:"id" doc:"Stable within the audit, e.g. ix:<program>:<name>"`
	Parent   string            `json:"parent,omitempty"`
	Kind     string            `json:"kind" doc:"program, instruction, account, route, param"`
	Name     string            `json:"name"`
	Source   string            `json:"source" enum:"idl,bytecode,traffic,operator,static" doc:"Where the node was learned; static: read from the source checkout"`
	Facts    map[string]string `json:"facts,omitempty" doc:"Small facts: signer, writable, args, method"`
	External bool              `json:"external,omitempty" doc:"Outside the audited target, reached through an edge"`

	// Derived on every read from evidence and the report.
	Attention AttentionState `json:"attention" enum:"unseen,observed,reviewed,finding"`
	Observed  int            `json:"observed" doc:"Evidence rows on this node"`
	LastSeen  *time.Time     `json:"last_seen,omitempty"`
	Findings  []int          `json:"findings,omitempty" doc:"Ordinals of findings located here"`
	Stats     *NodeStats     `json:"stats,omitempty" doc:"Numbers from tx and replay rows; programs aggregate their children"`
}

// SurfaceEdge is a relation the target exercises: a CPI into another
// program, a navigation between routes.
type SurfaceEdge struct {
	From   string            `json:"from"`
	To     string            `json:"to"`
	Kind   string            `json:"kind" doc:"cpi, nav"`
	Count  int               `json:"count,omitempty" doc:"Times observed"`
	Source string            `json:"source,omitempty" enum:"traffic,operator,static" doc:"static: found in source, whether or not traffic exercised it"`
	Facts  map[string]string `json:"facts,omitempty" doc:"Small facts: signed, ops, file, line"`
}

// Sampling records how much evidence the system gathered on its own.
type Sampling struct {
	Signatures   int        `json:"signatures" doc:"Signatures listed per subject"`
	Transactions int        `json:"transactions" doc:"Transactions read"`
	From         *time.Time `json:"from,omitempty" doc:"Oldest evidence"`
	To           *time.Time `json:"to,omitempty" doc:"Newest evidence"`
	Horizon      *time.Time `json:"horizon,omitempty" doc:"Oldest moment the sample could see"`
	Blocks       uint64     `json:"blocks,omitempty" doc:"Blocks of history the sample could see"`
}

// Surface is everything the target exposes, as the audit knows it.
type Surface struct {
	Adapter  string           `json:"adapter"`
	Nodes    []SurfaceNode    `json:"nodes"`
	Edges    []SurfaceEdge    `json:"edges,omitempty"`
	Sampling *Sampling        `json:"sampling,omitempty"`
	Coverage *SurfaceCoverage `json:"coverage,omitempty" doc:"Derived on every read"`
	BuiltAt  time.Time        `json:"built_at"`
}

// SurfaceEvidence is one observation on one node.
type SurfaceEvidence struct {
	ID        string         `json:"id,omitempty"`
	NodeID    string         `json:"node_id"`
	Kind      string         `json:"kind" enum:"tx,request,review,replay,write" doc:"tx: on chain; request: HTTP; review: an operator looked; replay: a re-execution with state before and after; write: a watched account changed"`
	Ref       string         `json:"ref,omitempty" doc:"Transaction signature or request URL"`
	Actor     string         `json:"actor,omitempty" enum:"system,operator"`
	At        *time.Time     `json:"at,omitempty" doc:"When it happened"`
	Summary   map[string]any `json:"summary,omitempty"`
	CreatedAt time.Time      `json:"created_at,omitempty"`
}

// SurfaceBudget bounds what a builder may spend. Public endpoints are
// rate-limited and shared, so the defaults are small and paced.
type SurfaceBudget struct {
	Signatures   int           // listed per subject, one call
	Transactions int           // read per subject, one call each
	Profiles     int           // replayed per subject, four calls each
	Pace         time.Duration // between reads
	Deadline     time.Duration // for the whole build
}

// DefaultSurfaceBudget fits a public RPC's per-method limit of forty
// reads in ten seconds, with room for other callers on the same address.
func DefaultSurfaceBudget() SurfaceBudget {
	return SurfaceBudget{Signatures: 100, Transactions: 40, Profiles: 8, Pace: 300 * time.Millisecond, Deadline: 4 * time.Minute}
}

// SurfaceInput is what a builder starts from.
type SurfaceInput struct {
	Subjects []string          // program ids, hosts
	Chains   map[string]string // chain per subject, for adapters that serve several
	Clone    *Clone
	Base     *Clone // the base commit of a diff audit, when checked out
	Budget   SurfaceBudget
	// Siblings are other deployments of the same repository, on any
	// chain: what an adapter may borrow names from when a subject's own
	// interface does not know a call or an error.
	Siblings []Sibling
}

// Sibling is another deployment of the repository under audit.
type Sibling struct {
	ID    string // program id or address
	Chain string
}

// SurfaceBuilder is the optional adapter capability that maps a target's
// surface and gathers the first evidence. Adapters without it are skipped.
type SurfaceBuilder interface {
	BuildSurface(ctx context.Context, in SurfaceInput) (Surface, []SurfaceEvidence, error)
}

// Merge adds nodes and edges not already present. Existing nodes keep
// their facts. Returns how many were added.
func (s *Surface) Merge(nodes []SurfaceNode, edges []SurfaceEdge) int {
	have := map[string]bool{}
	for _, n := range s.Nodes {
		have[n.ID] = true
	}
	added := 0
	for _, n := range nodes {
		if n.ID == "" || have[n.ID] {
			continue
		}
		have[n.ID] = true
		s.Nodes = append(s.Nodes, n)
		added++
	}
	edgeKey := func(e SurfaceEdge) string { return e.From + "\x00" + e.To + "\x00" + e.Kind }
	haveEdge := map[string]int{}
	for i, e := range s.Edges {
		haveEdge[edgeKey(e)] = i
	}
	for _, e := range edges {
		if e.From == "" || e.To == "" {
			continue
		}
		if i, ok := haveEdge[edgeKey(e)]; ok {
			s.Edges[i].Count += e.Count
			continue
		}
		haveEdge[edgeKey(e)] = len(s.Edges)
		s.Edges = append(s.Edges, e)
		added++
	}
	return added
}

// Enrich adds facts to nodes the map already has and appends the nodes it
// lacks. A fact already set is kept; a node's source is kept. Edges merge
// like Merge, with facts filled in where the existing edge had none.
// Returns how many nodes and edges were added.
func (s *Surface) Enrich(nodes []SurfaceNode, edges []SurfaceEdge) int {
	idx := map[string]int{}
	for i, n := range s.Nodes {
		idx[n.ID] = i
	}
	added := 0
	for _, n := range nodes {
		if n.ID == "" {
			continue
		}
		i, ok := idx[n.ID]
		if !ok {
			idx[n.ID] = len(s.Nodes)
			s.Nodes = append(s.Nodes, n)
			added++
			continue
		}
		have := &s.Nodes[i]
		if have.Facts == nil && len(n.Facts) > 0 {
			have.Facts = map[string]string{}
		}
		for k, v := range n.Facts {
			if _, set := have.Facts[k]; !set {
				have.Facts[k] = v
			}
		}
	}
	edgeKey := func(e SurfaceEdge) string { return e.From + "\x00" + e.To + "\x00" + e.Kind }
	haveEdge := map[string]int{}
	for i, e := range s.Edges {
		haveEdge[edgeKey(e)] = i
	}
	for _, e := range edges {
		if e.From == "" || e.To == "" {
			continue
		}
		if i, ok := haveEdge[edgeKey(e)]; ok {
			have := &s.Edges[i]
			have.Count += e.Count
			if have.Facts == nil && len(e.Facts) > 0 {
				have.Facts = map[string]string{}
			}
			for k, v := range e.Facts {
				if _, set := have.Facts[k]; !set {
					have.Facts[k] = v
				}
			}
			if have.Source == "" {
				have.Source = e.Source
			}
			continue
		}
		haveEdge[edgeKey(e)] = len(s.Edges)
		s.Edges = append(s.Edges, e)
		added++
	}
	return added
}

// Has reports whether a node id exists.
func (s *Surface) Has(id string) bool {
	for _, n := range s.Nodes {
		if n.ID == id {
			return true
		}
	}
	return false
}

// Attend fills every node's derived fields from the evidence and the
// findings, then rolls attention up to parents. A finding lands on the
// node whose facts name its program and whose name is its instruction,
// or, on the web, on the route its URL folds to.
func (s *Surface) Attend(evidence []SurfaceEvidence, findings []Finding) {
	idx := map[string]int{}
	for i := range s.Nodes {
		idx[s.Nodes[i].ID] = i
		s.Nodes[i].Attention = AttentionUnseen
		s.Nodes[i].Observed = 0
		s.Nodes[i].LastSeen = nil
		s.Nodes[i].Findings = nil
		s.Nodes[i].Stats = nil
	}
	reviewed := map[string]bool{}
	for _, ev := range evidence {
		i, ok := idx[ev.NodeID]
		if !ok {
			continue
		}
		n := &s.Nodes[i]
		n.Observed++
		if ev.At != nil && (n.LastSeen == nil || ev.At.After(*n.LastSeen)) {
			t := *ev.At
			n.LastSeen = &t
		}
		if ev.Kind == "review" {
			reviewed[ev.NodeID] = true
		}
	}
	for _, f := range findings {
		locs := []Location{f.Location}
		for _, site := range f.Sites {
			locs = append(locs, site.Location)
		}
		seen := map[string]bool{}
		for _, loc := range locs {
			for i := range s.Nodes {
				n := &s.Nodes[i]
				if seen[n.ID] || !n.matches(loc) {
					continue
				}
				seen[n.ID] = true
				n.Findings = append(n.Findings, f.Ordinal)
			}
		}
	}
	for i := range s.Nodes {
		n := &s.Nodes[i]
		switch {
		case len(n.Findings) > 0:
			sort.Ints(n.Findings)
			n.Attention = AttentionFinding
		case reviewed[n.ID]:
			n.Attention = AttentionReviewed
		case n.Observed > 0:
			n.Attention = AttentionObserved
		}
	}
	// Parents carry the strongest state beneath them, so a program row
	// says at a glance whether anything under it was reached.
	for pass := 0; pass < 4; pass++ {
		for i := range s.Nodes {
			n := s.Nodes[i]
			if n.Parent == "" {
				continue
			}
			if pi, ok := idx[n.Parent]; ok && attentionRank[n.Attention] > attentionRank[s.Nodes[pi].Attention] {
				s.Nodes[pi].Attention = n.Attention
			}
		}
	}
	s.attendStats(evidence, idx)
}

// matches reports whether a finding location points at this node: on the
// web, the route whose host and folded path are the location's URL; on
// chain, the program in its facts and the instruction by name, ignoring
// case and underscores so snake and camel spellings meet.
func (n *SurfaceNode) matches(loc Location) bool {
	if loc.URL != "" {
		if n.Kind != "route" {
			return false
		}
		host, shape, ok := WebPlace(loc.URL)
		return ok && n.Facts["host"] == host && n.Facts["path"] == shape
	}
	if loc.Instruction == "" || n.Kind != "instruction" {
		return false
	}
	if p := n.Facts["program"]; p != "" && loc.ProgramID != "" && p != loc.ProgramID {
		return false
	}
	return fold(n.Name) == fold(loc.Instruction)
}

func fold(s string) string {
	return strings.ToLower(strings.ReplaceAll(s, "_", ""))
}
