package core

import (
	"encoding/json"
	"math/big"
	"sort"
	"strconv"
	"strings"
	"time"
)

// NodeStats is what the evidence says about one node in numbers, derived
// on every read and never stored.
type NodeStats struct {
	Ok        int            `json:"ok"`
	Failed    int            `json:"failed"`
	Errors    map[string]int `json:"errors,omitempty" doc:"Failures by decoded error name"`
	UnitsMin  uint64         `json:"units_min,omitempty"`
	UnitsMed  uint64         `json:"units_med,omitempty"`
	UnitsMax  uint64         `json:"units_max,omitempty"`
	Signers   int            `json:"signers" doc:"Distinct signing keys seen"`
	FirstSeen *time.Time     `json:"first_seen,omitempty"`
	Replays   int            `json:"replays"`
	Moves     []TokenMove    `json:"moves,omitempty" doc:"Token volume per mint across sampled transactions"`
}

// TokenMove is the traffic one mint saw on a node.
type TokenMove struct {
	Mint     string `json:"mint"`
	Decimals int    `json:"decimals"`
	Volume   string `json:"volume" doc:"Sum of absolute deltas, base units"`
	Count    int    `json:"count"`
}

// SurfaceCoverage is how much of the map the evidence reached, derived on
// every read.
type SurfaceCoverage struct {
	Instructions         int        `json:"instructions"`
	InstructionsObserved int        `json:"instructions_observed"`
	InstructionsReplayed int        `json:"instructions_replayed"`
	Edges                int        `json:"edges" doc:"CPI edges, static or observed"`
	EdgesObserved        int        `json:"edges_observed" doc:"Edges with traffic"`
	Signers              int        `json:"signers" doc:"Distinct signing keys across the map"`
	FirstSeen            *time.Time `json:"first_seen,omitempty"`
	Changed              int        `json:"changed,omitempty" doc:"Instructions whose reachable code differs from the base commit"`
	Added                int        `json:"added,omitempty"`
}

// Summary keys a tx row may carry. Adapters write them; core only reads.
const (
	sumFailed  = "failed"      // bool
	sumError   = "error"       // string, decoded error name
	sumUnits   = "units"       // number, compute consumed
	sumSigners = "signer_keys" // []string
	sumMoves   = "moves"       // []{mint, decimals, delta}
	// A request row: the response status and how long it took.
	sumStatus = "status" // number
	sumMillis = "ms"     // number
)

// attendStats fills Stats on every node that has tx, request or replay
// rows and Coverage on the surface. Program and host nodes see their own
// rows plus those of their direct children, so signer sets and medians
// are computed over the union rather than summed from child stats.
func (s *Surface) attendStats(evidence []SurfaceEvidence, idx map[string]int) {
	rows := map[string][]SurfaceEvidence{}
	for _, ev := range evidence {
		i, ok := idx[ev.NodeID]
		if !ok {
			continue
		}
		rows[ev.NodeID] = append(rows[ev.NodeID], ev)
		n := s.Nodes[i]
		if pi, ok := idx[n.Parent]; ok {
			if p := s.Nodes[pi]; (p.Kind == "program" || p.Kind == "host") && !p.External {
				rows[p.ID] = append(rows[p.ID], ev)
			}
		}
	}
	cov := SurfaceCoverage{}
	signers := map[string]bool{}
	for i := range s.Nodes {
		n := &s.Nodes[i]
		st, keys := statsOf(rows[n.ID])
		if st != nil {
			n.Stats = st
		}
		for k := range keys {
			signers[k] = true
		}
		if n.External || (n.Kind != "instruction" && n.Kind != "route") {
			continue
		}
		cov.Instructions++
		if n.Observed > 0 {
			cov.InstructionsObserved++
		}
		if st != nil && st.Replays > 0 {
			cov.InstructionsReplayed++
		}
		if st != nil && st.FirstSeen != nil && (cov.FirstSeen == nil || st.FirstSeen.Before(*cov.FirstSeen)) {
			t := *st.FirstSeen
			cov.FirstSeen = &t
		}
		switch n.Facts["diff"] {
		case "changed":
			cov.Changed++
		case "added":
			cov.Added++
		}
	}
	cov.Signers = len(signers)
	for _, e := range s.Edges {
		cov.Edges++
		if e.Count > 0 {
			cov.EdgesObserved++
		}
	}
	s.Coverage = &cov
}

// statsOf reduces a node's rows to numbers. Nil when nothing on chain or
// on the wire was seen: reviews alone say nothing in numbers. A request
// counts as failed from status 400, under its status as the error name,
// and its duration stands where compute units do. The signer set comes
// back so the caller can union it across nodes.
func statsOf(rows []SurfaceEvidence) (*NodeStats, map[string]bool) {
	st := NodeStats{}
	signers := map[string]bool{}
	var units []uint64
	type move struct {
		decimals int
		volume   *big.Int
		count    int
	}
	moves := map[string]*move{}
	seen := 0
	for _, ev := range rows {
		switch ev.Kind {
		case "replay":
			st.Replays++
		case "request":
			if status, ok := asUint64(ev.Summary[sumStatus]); ok && status >= 400 {
				st.Failed++
				if st.Errors == nil {
					st.Errors = map[string]int{}
				}
				st.Errors[strconv.FormatUint(status, 10)]++
			} else {
				st.Ok++
			}
			if u, ok := asUint64(ev.Summary[sumMillis]); ok && u > 0 {
				units = append(units, u)
			}
		case "tx":
			if asBool(ev.Summary[sumFailed]) {
				st.Failed++
				name := asString(ev.Summary[sumError])
				if name == "" {
					name = "unknown"
				}
				if st.Errors == nil {
					st.Errors = map[string]int{}
				}
				st.Errors[name]++
			} else {
				st.Ok++
			}
			if u, ok := asUint64(ev.Summary[sumUnits]); ok && u > 0 {
				units = append(units, u)
			}
			for _, k := range asStrings(ev.Summary[sumSigners]) {
				signers[k] = true
			}
			for _, m := range asObjects(ev.Summary[sumMoves]) {
				mint := asString(m["mint"])
				delta, ok := asBigInt(m["delta"])
				if mint == "" || !ok {
					continue
				}
				mv := moves[mint]
				if mv == nil {
					dec, _ := asUint64(m["decimals"])
					mv = &move{decimals: int(dec), volume: new(big.Int)}
					moves[mint] = mv
				}
				mv.volume.Add(mv.volume, delta.Abs(delta))
				mv.count++
			}
		default:
			continue
		}
		seen++
		if ev.At != nil && (st.FirstSeen == nil || ev.At.Before(*st.FirstSeen)) {
			t := *ev.At
			st.FirstSeen = &t
		}
	}
	if seen == 0 {
		return nil, signers
	}
	if len(units) > 0 {
		sort.Slice(units, func(i, j int) bool { return units[i] < units[j] })
		st.UnitsMin = units[0]
		st.UnitsMed = units[(len(units)-1)/2]
		st.UnitsMax = units[len(units)-1]
	}
	st.Signers = len(signers)
	for mint, mv := range moves {
		st.Moves = append(st.Moves, TokenMove{Mint: mint, Decimals: mv.decimals, Volume: mv.volume.String(), Count: mv.count})
	}
	sort.Slice(st.Moves, func(i, j int) bool {
		if st.Moves[i].Count != st.Moves[j].Count {
			return st.Moves[i].Count > st.Moves[j].Count
		}
		return st.Moves[i].Mint < st.Moves[j].Mint
	})
	return &st, signers
}

// Summaries round-trip through jsonb, so numbers arrive as float64 and
// big values as strings; adapters in-process may hand over native types.

func asBool(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case string:
		return x == "true"
	case float64:
		return x != 0
	}
	return false
}

func asString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case json.Number:
		return x.String()
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	}
	return ""
}

func asUint64(v any) (uint64, bool) {
	switch x := v.(type) {
	case float64:
		if x < 0 {
			return 0, false
		}
		return uint64(x), true
	case int:
		if x < 0 {
			return 0, false
		}
		return uint64(x), true
	case int64:
		if x < 0 {
			return 0, false
		}
		return uint64(x), true
	case uint64:
		return x, true
	case json.Number:
		u, err := strconv.ParseUint(x.String(), 10, 64)
		return u, err == nil
	case string:
		u, err := strconv.ParseUint(strings.TrimSpace(x), 10, 64)
		return u, err == nil
	}
	return 0, false
}

func asBigInt(v any) (*big.Int, bool) {
	switch x := v.(type) {
	case string:
		return new(big.Int).SetString(strings.TrimSpace(x), 10)
	case json.Number:
		return new(big.Int).SetString(x.String(), 10)
	case float64:
		f, _ := new(big.Float).SetFloat64(x).Int(nil)
		return f, true
	case int:
		return big.NewInt(int64(x)), true
	case int64:
		return big.NewInt(x), true
	}
	return nil, false
}

func asStrings(v any) []string {
	switch x := v.(type) {
	case []string:
		return x
	case []any:
		out := make([]string, 0, len(x))
		for _, e := range x {
			if s := asString(e); s != "" {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func asObjects(v any) []map[string]any {
	switch x := v.(type) {
	case []map[string]any:
		return x
	case []any:
		out := make([]map[string]any, 0, len(x))
		for _, e := range x {
			if m, ok := e.(map[string]any); ok {
				out = append(out, m)
			}
		}
		return out
	}
	return nil
}
