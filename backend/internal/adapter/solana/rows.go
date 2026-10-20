package solana

import (
	"github.com/forefy/vig/backend/internal/chain"
	"github.com/forefy/vig/backend/internal/core"
)

// rowAcc collects what transactions add to the map: instructions the IDL
// never declared, CPI edges, and one evidence row per call of the subject.
// The sampler at map time and the watch after it fill the same accumulator,
// so a live row and a sampled row are the same row.
type rowAcc struct {
	nodes    []core.SurfaceNode
	edges    map[string]*core.SurfaceEdge
	evidence []core.SurfaceEvidence
}

// add turns every call of pid inside tx into evidence and edges.
func (acc *rowAcc) add(pid string, specs []instructionSpec, byDisc map[string]instructionSpec, idl *idlSpec, tx chain.TxCalls) {
	// A program is reached directly or through another program's CPI;
	// both are its surface, and the second tells who composes it.
	// via is the program that made the call itself, from the stack
	// heights; root the top-level program when that is a different one.
	type hit struct {
		call chain.Call
		via  string
		root string
		top  bool
		seq  int // position among every call of the transaction, in execution order
	}
	var hits []hit
	seq := 0
	for _, call := range tx.Calls {
		seq++
		if call.ProgramID == pid {
			hits = append(hits, hit{call: call, top: true, seq: seq})
		}
		for _, inner := range call.InnerCalls {
			seq++
			if inner.ProgramID != pid {
				continue
			}
			h := hit{call: inner, via: call.ProgramID, seq: seq}
			if inner.Parent >= 0 && inner.Parent < len(call.InnerCalls) {
				h.via = call.InnerCalls[inner.Parent].ProgramID
			}
			if h.via != call.ProgramID {
				h.root = call.ProgramID
			}
			hits = append(hits, h)
		}
	}
	for _, h := range hits {
		call := h.call
		// Anchor programs key instructions by an 8 byte discriminator;
		// everything else by a leading tag byte, so a native program's
		// transfers land on one node rather than one per amount.
		name := ""
		native, isNative := nativeSpec(pid)
		switch {
		case isNative:
			if spec, ok := native.resolveCall(call); ok {
				name = spec.Name
			} else if len(call.Data) >= native.TagWidth {
				name = discriminatorName(call.Data[:native.TagWidth])
			} else {
				name = "ix_empty"
			}
		case len(specs) > 0 && len(call.Data) >= 8:
			var d [8]byte
			copy(d[:], call.Data[:8])
			if ins, ok := byDisc[sigKey(pid, d)]; ok {
				name = ins.Name
			} else {
				name = discriminatorName(call.Data[:8])
			}
		case len(call.Data) > 0:
			n := 1
			if len(specs) > 0 {
				n = len(call.Data)
			}
			name = discriminatorName(call.Data[:n])
		default:
			name = "ix_empty"
		}
		id := ixNodeID(pid, name)
		_, nativeKnown := native.resolveCall(call)
		if _, known := byDisc[sigKey(pid, discFromName(name, byDisc, pid))]; !known && !(isNative && nativeKnown) {
			acc.nodes = append(acc.nodes, core.SurfaceNode{ID: id, Parent: programNodeID(pid), Kind: "instruction", Name: name, Source: "traffic", Facts: map[string]string{"program": pid}})
		}
		signers := 0
		for _, a := range call.Accounts {
			if a.Signer {
				signers++
			}
		}
		summary := map[string]any{"slot": tx.Slot, "accounts": len(call.Accounts), "signers": signers}
		// The keys the call was given, in order, so the map can say which
		// address stood in for each declared account.
		if n := len(call.Accounts); n > 0 {
			keys := make([]string, 0, min(n, maxKeysKept))
			for _, a := range call.Accounts[:min(n, maxKeysKept)] {
				keys = append(keys, a.Pubkey)
			}
			summary["keys"] = keys
		}
		// Outcome facts the meta already carries: per-call units from
		// the log line when present, else the whole transaction's.
		if u, ok := tx.UnitsFor(pid); ok {
			summary["units"] = u
		} else if tx.Units > 0 {
			summary["units"] = tx.Units
		}
		if tx.Failed {
			summary["failed"] = true
			summary["error"] = decodeError(tx.Err, idl).Raw
			if in := tx.FailedIn(); in != "" {
				summary["error_program"] = in
			}
		}
		if len(tx.Signers) > 0 {
			summary["signer_keys"] = tx.Signers
		}
		if len(tx.TokenDeltas) > 0 {
			moves := make([]map[string]any, 0, len(tx.TokenDeltas))
			for _, d := range tx.TokenDeltas {
				moves = append(moves, map[string]any{"mint": d.Mint, "owner": d.Owner, "decimals": d.Decimals, "delta": d.Delta})
			}
			summary["moves"] = moves
		}
		// The inputs of every sampled call, decoded from what the map
		// already holds: no extra read.
		if args := callArgs(pid, call, idl); len(args) > 0 {
			summary["args"] = args
		}
		if h.root != "" {
			summary["root"] = h.root
		}
		summary["seq"] = h.seq
		if call.Depth > 0 {
			summary["depth"] = call.Depth
		}
		if h.via != "" {
			summary["via"] = h.via
			key := programNodeID(h.via) + ">" + id
			if e, ok := acc.edges[key]; ok {
				e.Count++
			} else {
				acc.edges[key] = &core.SurfaceEdge{From: programNodeID(h.via), To: id, Kind: "cpi", Count: 1}
				acc.nodes = append(acc.nodes, core.SurfaceNode{ID: programNodeID(h.via), Kind: "program", Name: KnownProgram(h.via), Source: "traffic", External: true, Facts: map[string]string{"program": h.via}})
			}
		}
		// A top-level call lists what it invokes itself when the RPC
		// says so; an inner call's Inner is already its direct callees.
		callees := call.Inner
		if h.top && len(call.Direct) > 0 {
			callees = call.Direct
		}
		if len(callees) > 0 {
			var targets []string
			for _, inner := range uniq(callees) {
				if inner == pid {
					continue
				}
				targets = append(targets, inner)
				key := id + ">" + inner
				if e, ok := acc.edges[key]; ok {
					e.Count++
				} else {
					acc.edges[key] = &core.SurfaceEdge{From: id, To: programNodeID(inner), Kind: "cpi", Count: 1}
					acc.nodes = append(acc.nodes, core.SurfaceNode{ID: programNodeID(inner), Kind: "program", Name: KnownProgram(inner), Source: "traffic", External: true, Facts: map[string]string{"program": inner}})
				}
			}
			if len(targets) > 0 {
				summary["cpi"] = targets
			}
		}
		acc.evidence = append(acc.evidence, core.SurfaceEvidence{NodeID: id, Kind: "tx", Ref: tx.Signature, Actor: "system", At: tx.BlockTime, Summary: summary})
	}
}
