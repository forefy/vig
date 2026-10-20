package core

import (
	"context"
	"time"
)

// The watch is the surface with time running through it. After the map is
// built the adapter keeps a subscription open on every subject and every
// account the sample showed to be shared state, turns each notification
// into an evidence row on the node it belongs to, and says, per slot
// range, whether it saw everything (live), read it later (backfill) or
// missed it (gap). Nothing here is a claim of completeness without the
// span that backs it.

// CoverageKind says how a slot range was seen.
type CoverageKind string

const (
	CoverageLive     CoverageKind = "live"     // every notification of the range arrived
	CoverageBackfill CoverageKind = "backfill" // read from the cursor after a disconnect
	CoverageGap      CoverageKind = "gap"      // known to be unread: budget or history limit
)

// CoverageSpan is one slot range of one subject, with how it was seen.
type CoverageSpan struct {
	ID       string       `json:"id,omitempty"`
	Subject  string       `json:"subject" doc:"Program id or host"`
	Kind     CoverageKind `json:"kind" enum:"live,backfill,gap"`
	FromSlot uint64       `json:"from_slot"`
	ToSlot   uint64       `json:"to_slot"`
	FromAt   *time.Time   `json:"from_at,omitempty"`
	ToAt     *time.Time   `json:"to_at,omitempty"`
}

// Cursor is the newest observation of one subject the watch has stored.
type Cursor struct {
	Ref  string     `json:"ref" doc:"Signature or request id"`
	Slot uint64     `json:"slot"`
	At   *time.Time `json:"at,omitempty"`
}

// WatchedAccount is one address the watch subscribes to, and the node it
// stands for on the map.
type WatchedAccount struct {
	Address string `json:"address"`
	NodeID  string `json:"node_id"`
	Role    string `json:"role" enum:"state,program,idl" doc:"state: shared state the sample showed; program: the deployable itself; idl: its interface"`
}

// WatchBudget bounds what one session may spend beyond the subscription,
// which costs nothing per call.
type WatchBudget struct {
	Backfill int           // signatures listed from the cursor after a disconnect, one call per 1000
	Details  int           // transactions read in full per minute, on top of the notification
	Pace     time.Duration // between reads
	Accounts int           // addresses subscribed per audit
	Rows     int           // rows stored per subject per minute; beyond it calls are only counted
}

// DefaultWatchBudget stays under a public endpoint's per-method limit
// (forty calls in ten seconds) alongside the mapper and the API: a
// hundred and twenty full reads a minute is half of it.
func DefaultWatchBudget() WatchBudget {
	return WatchBudget{Backfill: 1000, Details: 120, Pace: 250 * time.Millisecond, Accounts: 32, Rows: 600}
}

// WatchInput is what a session starts from: the map, the sample that
// built it, and where the last session stopped.
type WatchInput struct {
	Subjects []string
	Chains   map[string]string // chain per subject, for adapters that serve several
	Surface  *Surface
	Sample   []SurfaceEvidence // the rows the map was built from; the watcher picks shared state from their keys
	Cursors  map[string]Cursor // by subject
	Accounts []WatchedAccount  // chosen by an earlier session; empty on the first
	Priority []string          // node ids that get a full read on every call: nodes a finding names
	Budget   WatchBudget
	Siblings []Sibling // other deployments of the repository, see SurfaceInput
}

// WatchState is where a session stands.
type WatchState string

const (
	WatchConnecting  WatchState = "connecting"
	WatchBackfilling WatchState = "backfilling"
	WatchLive        WatchState = "live"
	WatchError       WatchState = "error"
	WatchEnded       WatchState = "ended"
)

// WatchSink receives what a session learns. Every method is called from the
// session's goroutine; the sink persists and returns.
type WatchSink interface {
	// Evidence adds rows. A row whose ref already exists on its node
	// replaces that row's summary, so a full read supersedes a log-derived one.
	Evidence(ctx context.Context, rows []SurfaceEvidence) error
	// Tally counts calls on a node without storing rows: what a busy
	// program's traffic becomes past the row budget.
	Tally(ctx context.Context, nodeID string, at time.Time, calls, failed int, units int64) error
	// Coverage opens a span, or extends the open span of the subject and
	// kind when its id is set.
	Coverage(ctx context.Context, span CoverageSpan) (id string, err error)
	// Cursor records the newest observation of a subject.
	Cursor(ctx context.Context, subject string, c Cursor) error
	// Accounts records the addresses the session subscribed to.
	Accounts(ctx context.Context, accounts []WatchedAccount) error
	// Facts replaces facts on a node of the map: the program's hash and
	// authority after an upgrade.
	Facts(ctx context.Context, nodeID string, facts map[string]string) error
	// Extend grows the map with what traffic showed: instructions the IDL
	// never declared, callers and callees, and their edge counts.
	Extend(ctx context.Context, nodes []SurfaceNode, edges []SurfaceEdge) error
	// State reports the session's state; msg carries the error when there is one.
	State(ctx context.Context, state WatchState, msg string) error
}

// Watcher is the optional adapter capability that keeps a surface live.
// Watch runs one session: it subscribes, backfills from the cursors, then
// streams until ctx ends or the connection fails, and returns the error.
// The caller reconnects.
type Watcher interface {
	Watch(ctx context.Context, in WatchInput, sink WatchSink) error
}

// NodeTotal is what the rollups say about one node since the watch began.
type NodeTotal struct {
	NodeID string     `json:"node_id"`
	Calls  int        `json:"calls"`
	Failed int        `json:"failed"`
	Units  int64      `json:"units" doc:"Compute units summed over the calls that reported them"`
	LastAt *time.Time `json:"last_at,omitempty"`
}

// NodeMinute is one node's traffic in one minute.
type NodeMinute struct {
	NodeID string    `json:"node_id"`
	Minute time.Time `json:"minute"`
	Calls  int       `json:"calls"`
	Failed int       `json:"failed"`
}

// ActivityBucket is every node's traffic in one slice of the watch.
type ActivityBucket struct {
	At     time.Time `json:"at"`
	Calls  int       `json:"calls"`
	Failed int       `json:"failed"`
}

// AddObserved folds live totals into the derived fields, so a node the
// watch saw counts as observed without reading its rows. Call after Attend.
func (s *Surface) AddObserved(totals []NodeTotal) {
	idx := map[string]int{}
	for i := range s.Nodes {
		idx[s.Nodes[i].ID] = i
	}
	for _, t := range totals {
		i, ok := idx[t.NodeID]
		if !ok || t.Calls == 0 {
			continue
		}
		n := &s.Nodes[i]
		n.Observed += t.Calls
		if t.LastAt != nil && (n.LastSeen == nil || t.LastAt.After(*n.LastSeen)) {
			at := *t.LastAt
			n.LastSeen = &at
		}
		if n.Attention == AttentionUnseen {
			n.Attention = AttentionObserved
		}
	}
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
}
