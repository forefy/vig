package ethereum

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/forefy/vig/backend/internal/core"
	"github.com/forefy/vig/backend/internal/evm"
)

// Replay re-executes a sampled call against the chain's current state
// with eth_call, as it was sent: same sender, calldata and value. The
// public endpoint serves no historical state and no tracer, so the replay
// says what the call would do today: whether it runs or reverts, the
// decoded reason, the gas it would take and what it returns. The shape
// is the Solana replay's, so the map reads then against now the same way.
type Replay struct {
	Hash        string            `json:"replay"`
	Instruction string            `json:"instruction,omitempty"`
	Args        map[string]string `json:"args,omitempty"`
	Outcome     string            `json:"outcome"`
	Error       *ReplayError      `json:"error,omitempty"`
	Units       uint64            `json:"units,omitempty"`
	Logs        []string          `json:"logs,omitempty"`
	Returned    string            `json:"returned,omitempty"`
}

// ReplayError is a decoded revert.
type ReplayError struct {
	Name string `json:"name,omitempty"`
	Msg  string `json:"msg,omitempty"`
	Raw  string `json:"raw"`
}

const maxReturned = 256 // hex characters of return data kept

// Profile replays one transaction sent straight to the contract.
func Profile(ctx context.Context, c *evm.Client, in *iface, lookup *evm.Lookup, addr string, tx evm.Tx, name string) *Replay {
	r := &Replay{Hash: tx.Hash, Instruction: name}
	if sel, ok := evm.Selector(tx.Input); ok {
		if fn, known := in.abi.Functions[sel]; known {
			if _, decoded, err := (&evm.ABI{Functions: map[[4]byte]evm.Function{sel: fn}}).DecodeCall(tx.Input); err == nil && len(decoded) > 0 {
				r.Args = map[string]string(decoded)
			}
		}
	}
	out, err := c.CallContract(ctx, tx.From, addr, tx.Input, tx.Value)
	if err == nil {
		r.Outcome = "ok"
		if gas, err := c.EstimateGas(ctx, tx.From, addr, tx.Input, tx.Value); err == nil {
			r.Units = gas
		}
		if len(out) > 0 {
			h := hex.EncodeToString(out)
			if len(h) > maxReturned {
				h = h[:maxReturned] + "…"
			}
			r.Returned = "0x" + h
		}
		return r
	}
	rev, ok := err.(*evm.RevertError)
	if !ok {
		return nil
	}
	r.Outcome = "reverted"
	d := evm.DecodeRevert(in.abi, rev.Data)
	unnamed := func() bool { return d.Name == "0x"+hex.EncodeToString(d.Sel[:]) }
	if unnamed() && in.errors != nil {
		// A sibling deployment's verified ABI declares the error.
		d = evm.DecodeRevert(in.errors, rev.Data)
	}
	if unnamed() && lookup != nil {
		// A custom error nobody verified: the signature directories list
		// error signatures beside function ones.
		if named := lookup.Name(ctx, d.Sel); named != nil {
			d.Name = named.Name
		}
	}
	r.Error = &ReplayError{Name: d.Name, Msg: d.Msg, Raw: "0x" + hex.EncodeToString(rev.Data)}
	if len(d.Fields) > 0 {
		for k, v := range d.Fields {
			r.Logs = append(r.Logs, k+": "+v)
		}
	}
	return r
}

// Evidence wraps the replay as a row on its function, keyed by the run
// so a later replay of the same call is a second row, the drift.
func (r *Replay) Evidence(addr string, at time.Time) core.SurfaceEvidence {
	b, _ := json.Marshal(r)
	var summary map[string]any
	_ = json.Unmarshal(b, &summary)
	return core.SurfaceEvidence{NodeID: fnNodeID(addr, r.Instruction), Kind: "replay", Ref: "replay:" + r.Hash + ":" + at.UTC().Format(time.RFC3339), Actor: "system", At: &at, Summary: summary}
}
