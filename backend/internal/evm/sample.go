package evm

import (
	"context"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"
)

// The spike's question: with a keyless public endpoint and no traces,
// what does a contract's surface look like? This sampler answers it the
// way the Solana sampler would: recent blocks, every transaction sent to
// the contract decoded against its interface, every event it emitted,
// and, as its own count, the calls that reached it through another
// contract, which only the events reveal.

// FunctionSeen is one function with what the sample showed of it.
type FunctionSeen struct {
	Sig      string
	Name     string
	Calls    int
	Failed   int
	Verified bool    // named by the verified ABI, not by 4byte
	Example  Decoded // the first call's arguments
}

// Caller is one address that sent transactions to the contract.
type Caller struct {
	Address  string
	Calls    int
	Contract *bool // nil when not checked
}

// EventSeen is one event with counts direct and indirect.
type EventSeen struct {
	Sig      string
	Name     string
	Direct   int // in transactions sent to the contract
	Indirect int // in transactions sent elsewhere: reached through another contract
}

// Report is what the sample learned.
type Report struct {
	Address    string
	Verified   bool
	FromBlock  uint64
	ToBlock    uint64
	Span       time.Duration
	Txs        int // in the blocks read
	Direct     int // sent to the contract
	Unknown    int // direct calls whose selector nothing names
	Functions  []FunctionSeen
	Callers    []Caller
	Events     []EventSeen
	Indirect   int // transactions that reached the contract through another one
	Receipts   int // read in full
	Calls      int // RPC calls spent
	Elapsed    time.Duration
	Unresolved []string // selectors nothing names, hex
	Interface  int      // functions the ABI declares
	EventKinds int      // events the ABI declares
	Sampled    bool     // the block range was cut by the budget
	Endpoint   string
}

// Budget bounds the sample.
type Budget struct {
	Blocks   int // read back from the head
	Receipts int // direct transactions read in full
	Callers  int // top callers checked for code
	Pace     time.Duration
}

// Sample runs the spike against one contract.
func Sample(ctx context.Context, c *Client, l *Lookup, address string, b Budget) (*Report, error) {
	started := time.Now()
	address = strings.ToLower(address)
	r := &Report{Address: address, Endpoint: c.URLs[0]}
	abi, err := l.ABIFor(ctx, "1", address)
	switch {
	case err == nil:
		r.Verified = true
		r.Interface = len(abi.Functions)
		r.EventKinds = len(abi.Events)
	case err == ErrUnverified:
		abi = &ABI{Functions: map[[4]byte]Function{}, Events: map[[32]byte]Event{}}
	default:
		return nil, err
	}
	head, err := c.BlockNumber(ctx)
	if err != nil {
		return nil, err
	}
	r.Calls++
	r.ToBlock = head
	r.FromBlock = head - uint64(b.Blocks) + 1

	fns := map[[4]byte]*FunctionSeen{}
	callers := map[string]*Caller{}
	directTx := map[string][4]byte{} // hash -> selector, for the receipts
	var first, last time.Time
	for n := r.FromBlock; n <= r.ToBlock; n++ {
		if ctx.Err() != nil {
			r.Sampled = true
			break
		}
		blk, err := c.BlockByNumber(ctx, n)
		r.Calls++
		if err != nil {
			return nil, fmt.Errorf("block %d: %w", n, err)
		}
		if first.IsZero() {
			first = blk.Timestamp
		}
		last = blk.Timestamp
		r.Txs += len(blk.Txs)
		for _, tx := range blk.Txs {
			if tx.To != address {
				continue
			}
			r.Direct++
			cl := callers[tx.From]
			if cl == nil {
				cl = &Caller{Address: tx.From}
				callers[tx.From] = cl
			}
			cl.Calls++
			sel, ok := Selector(tx.Input)
			if !ok {
				continue
			}
			directTx[tx.Hash] = sel
			f, ok := abi.Functions[sel]
			verified := ok
			if !ok {
				if named := l.Name(ctx, sel); named != nil {
					f = *named
					ok = true
				}
			}
			if !ok {
				r.Unknown++
				h := hex.EncodeToString(sel[:])
				if !contains(r.Unresolved, h) {
					r.Unresolved = append(r.Unresolved, h)
				}
				continue
			}
			fs := fns[sel]
			if fs == nil {
				fs = &FunctionSeen{Sig: f.Sig, Name: f.Name, Verified: verified}
				fns[sel] = fs
			}
			fs.Calls++
			if fs.Example == nil {
				if _, args, err := abi.DecodeCall(tx.Input); err == nil && len(args) > 0 {
					fs.Example = args
				} else if !verified {
					// A 4byte name has no ABI entry: decode through the parsed signature.
					tmp := &ABI{Functions: map[[4]byte]Function{sel: f}}
					if _, args, err := tmp.DecodeCall(tx.Input); err == nil {
						fs.Example = args
					}
				}
			}
		}
		if b.Pace > 0 {
			select {
			case <-ctx.Done():
			case <-time.After(b.Pace):
			}
		}
	}
	if !first.IsZero() && !last.IsZero() {
		r.Span = last.Sub(first)
	}

	// Outcomes of the direct calls, to the receipt budget: a failed
	// receipt counts against the function its call selected.
	read := 0
	for h, sel := range directTx {
		if read >= b.Receipts || ctx.Err() != nil {
			break
		}
		rc, err := c.TransactionReceipt(ctx, h)
		r.Calls++
		if err != nil {
			continue
		}
		read++
		if !rc.Status {
			if fs := fns[sel]; fs != nil {
				fs.Failed++
			}
		}
		if b.Pace > 0 {
			select {
			case <-ctx.Done():
			case <-time.After(b.Pace):
			}
		}
	}
	r.Receipts = read

	// Every event the contract emitted over the range, in one call. Those
	// in transactions not sent to the contract are calls that reached it
	// through another contract.
	events := map[[32]byte]*EventSeen{}
	indirectTx := map[string]bool{}
	logs, err := c.Logs(ctx, address, r.FromBlock, r.ToBlock)
	r.Calls++
	if err == nil {
		for _, lg := range logs {
			if len(lg.Topics) == 0 {
				continue
			}
			es := events[lg.Topics[0]]
			if es == nil {
				es = &EventSeen{Sig: "0x" + hex.EncodeToString(lg.Topics[0][:4]), Name: "?"}
				if e, ok := abi.Events[lg.Topics[0]]; ok {
					es.Sig, es.Name = e.Sig, e.Name
				}
				events[lg.Topics[0]] = es
			}
			if _, direct := directTx[lg.TxHash]; direct {
				es.Direct++
			} else {
				es.Indirect++
				indirectTx[lg.TxHash] = true
			}
		}
	}
	r.Indirect = len(indirectTx)

	for _, fs := range fns {
		r.Functions = append(r.Functions, *fs)
	}
	sort.Slice(r.Functions, func(i, j int) bool {
		return r.Functions[i].Calls > r.Functions[j].Calls || (r.Functions[i].Calls == r.Functions[j].Calls && r.Functions[i].Name < r.Functions[j].Name)
	})
	for _, cl := range callers {
		r.Callers = append(r.Callers, *cl)
	}
	sort.Slice(r.Callers, func(i, j int) bool {
		return r.Callers[i].Calls > r.Callers[j].Calls || (r.Callers[i].Calls == r.Callers[j].Calls && r.Callers[i].Address < r.Callers[j].Address)
	})
	for i := range r.Callers {
		if i >= b.Callers || ctx.Err() != nil {
			break
		}
		is, err := c.IsContract(ctx, r.Callers[i].Address)
		r.Calls++
		if err == nil {
			r.Callers[i].Contract = &is
		}
	}
	for _, es := range events {
		r.Events = append(r.Events, *es)
	}
	sort.Slice(r.Events, func(i, j int) bool {
		return r.Events[i].Direct+r.Events[i].Indirect > r.Events[j].Direct+r.Events[j].Indirect
	})
	r.Elapsed = time.Since(started)
	return r, nil
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// Text renders the report for a terminal.
func (r *Report) Text() string {
	var b strings.Builder
	w := func(f string, a ...any) { fmt.Fprintf(&b, f, a...) }
	w("contract   %s  (%s)\n", r.Address, map[bool]string{true: "verified on Sourcify", false: "no verified source"}[r.Verified])
	if r.Verified {
		w("interface  %d functions, %d events\n", r.Interface, r.EventKinds)
	}
	w("blocks     %d..%d  (%s of chain, %d transactions, %d sent to the contract)\n", r.FromBlock, r.ToBlock, r.Span.Round(time.Second), r.Txs, r.Direct)
	w("reached    %d more transactions through other contracts, seen only by their events\n", r.Indirect)
	w("spent      %d rpc calls in %s, %d receipts read\n\n", r.Calls, r.Elapsed.Round(time.Millisecond), r.Receipts)
	w("functions called directly\n")
	for _, f := range r.Functions {
		tag := ""
		if !f.Verified {
			tag = "  (named by 4byte)"
		}
		w("  %5d  %s%s\n", f.Calls, f.Sig, tag)
		if len(f.Example) > 0 {
			keys := make([]string, 0, len(f.Example))
			for k := range f.Example {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			w("         %s(\n", f.Name)
			for _, k := range keys {
				v := f.Example[k]
				if len(v) > 70 {
					v = v[:66] + "…"
				}
				w("           %s: %s,\n", k, v)
			}
			w("         )\n")
		}
	}
	if r.Unknown > 0 {
		w("  %5d  unresolved selectors: %s\n", r.Unknown, strings.Join(r.Unresolved, " "))
	}
	w("\ncallers (direct)\n")
	for i, c := range r.Callers {
		if i >= 12 {
			w("  … %d more\n", len(r.Callers)-i)
			break
		}
		kind := "?"
		if c.Contract != nil {
			kind = map[bool]string{true: "contract", false: "wallet"}[*c.Contract]
		}
		w("  %5d  %s  %s\n", c.Calls, c.Address, kind)
	}
	w("\nevents emitted            direct  indirect\n")
	for _, e := range r.Events {
		w("  %-24s %6d  %8d\n", e.Name+" "+e.Sig, e.Direct, e.Indirect)
	}
	return b.String()
}
