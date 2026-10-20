package ethereum

import (
	"context"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/forefy/vig/backend/internal/core"
	"github.com/forefy/vig/backend/internal/evm"
	"github.com/forefy/vig/backend/internal/solsrc"
)

// The surface of an EVM contract: the contract and its functions from the
// verified ABI, else from the Solidity the checkout declares, else from
// what traffic names through the 4byte directory; every transaction sent
// to it as evidence on the function it selected, with the sender as
// signer and, when another contract sent it, that contract as the
// caller; every event it emitted as a write on the contract; and, for a
// quiet contract, the events of the last days walked back to their
// transactions, so the map is never empty because the minute was.

// iface is what the map knows of one contract's interface and where from.
type iface struct {
	abi    *evm.ABI
	source string // idl (verified), static (from the checkout), traffic
	model  *solsrc.Model
	name   string // contract name in source, when matched
	// borrowed is what sibling deployments of the same repository know
	// that this contract's own interface does not: a function or an
	// error by selector, and which deployment named it.
	borrowed map[[4]byte]borrowed
	errors   *evm.ABI // the siblings' errors, for reverts the own ABI cannot name
}

type borrowed struct {
	fn   evm.Function
	from string // "0x0000…becf on Ethereum"
}

// borrow reads the verified interfaces of the repository's other
// deployments and keeps what this one lacks. An unverified deployment of
// the same code is then named by its verified sibling, not by a
// directory guess.
func (a *Adapter) borrow(ctx context.Context, in *iface, self string, siblings []core.Sibling) {
	if a.Lookup == nil || len(siblings) == 0 {
		return
	}
	for _, sib := range siblings {
		id := strings.ToLower(sib.ID)
		if id == strings.ToLower(self) || !strings.HasPrefix(id, "0x") {
			continue
		}
		ch, ok := chainByID(sib.Chain)
		if !ok {
			continue
		}
		abi, err := a.Lookup.ABIFor(ctx, ch.Sourcify, id)
		if err != nil || abi == nil {
			continue
		}
		from := shortAddr(id) + " on " + ch.Name
		for sel, fn := range abi.Functions {
			if _, own := in.abi.Functions[sel]; own {
				continue
			}
			if in.borrowed == nil {
				in.borrowed = map[[4]byte]borrowed{}
			}
			if _, seen := in.borrowed[sel]; !seen {
				in.borrowed[sel] = borrowed{fn: fn, from: from}
			}
		}
		for sel, e := range abi.Errors {
			if _, own := in.abi.Errors[sel]; own {
				continue
			}
			if in.errors == nil {
				in.errors = &evm.ABI{Functions: map[[4]byte]evm.Function{}, Events: map[[32]byte]evm.Event{}, Errors: map[[4]byte]evm.Error{}}
			}
			if _, seen := in.errors.Errors[sel]; !seen {
				in.errors.Errors[sel] = e
			}
		}
	}
}

// chainByID finds a served chain by its id.
func chainByID(id string) (Chain, bool) {
	for _, c := range Chains {
		if c.ID == id {
			return c, true
		}
	}
	return Chain{}, false
}

// nodeNames gives every function a node name: its name, or its signature
// when the contract overloads it.
func (i *iface) nodeNames() map[[4]byte]string {
	count := map[string]int{}
	for _, f := range i.abi.Functions {
		count[f.Name]++
	}
	out := map[[4]byte]string{}
	for sel, f := range i.abi.Functions {
		if count[f.Name] > 1 {
			out[sel] = f.Sig
		} else {
			out[sel] = f.Name
		}
	}
	return out
}

// labels gives every function its name on the map. An overloaded name
// carries its argument types as the source writes them,
// `createOrder(Order, Permit2Data)`; without source, the ABI types with
// tuples folded; the full signature only when nothing shorter tells the
// overloads apart. Node ids keep nodeNames, so a relabel moves nothing.
func (i *iface) labels(src map[[4]byte]solsrc.Function) map[[4]byte]string {
	count := map[string]int{}
	for _, f := range i.abi.Functions {
		count[f.Name]++
	}
	out := map[[4]byte]string{}
	used := map[string]int{}
	for sel, f := range i.abi.Functions {
		if count[f.Name] <= 1 {
			out[sel] = f.Name
			continue
		}
		if s, ok := src[sel]; ok {
			parts := make([]string, 0, len(s.Params))
			for _, p := range s.Params {
				if p.Raw != "" {
					parts = append(parts, p.Raw)
				} else {
					parts = append(parts, p.Type)
				}
			}
			out[sel] = s.Name + "(" + strings.Join(parts, ", ") + ")"
		} else {
			out[sel] = foldTuples(f.Sig)
		}
		used[out[sel]]++
	}
	for sel, f := range i.abi.Functions {
		if count[f.Name] > 1 && used[out[sel]] > 1 {
			out[sel] = f.Sig
		}
	}
	return out
}

// foldTuples shortens a signature's tuple arguments to `(…)`.
func foldTuples(sig string) string {
	open := strings.IndexByte(sig, '(')
	if open < 0 {
		return sig
	}
	var b strings.Builder
	b.WriteString(sig[:open+1])
	depth := 0
	for _, r := range sig[open+1:] {
		switch {
		case r == '(':
			if depth == 0 {
				b.WriteString("(…")
			}
			depth++
		case r == ')' && depth > 0:
			depth--
			if depth == 0 {
				b.WriteRune(')')
			}
		case depth == 0:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// interfaceOf assembles the interface of one contract.
func (a *Adapter) interfaceOf(ctx context.Context, addr string, chain Chain, clone *core.Clone, siblings []core.Sibling) (Facts, *iface) {
	root := ""
	if clone != nil {
		root = clone.Root
	}
	f, err := a.Inspect(ctx, addr, chain, root)
	if err != nil {
		f = Facts{Address: addr, Chain: chain}
	}
	out := &iface{}
	if root != "" {
		out.model, _ = solsrc.Load(root, clone.Path)
	}
	switch {
	case f.ABI != nil:
		out.abi, out.source = f.ABI, "idl"
	case out.model != nil && f.SourceContract != "":
		if c, ok := out.model.ByName(f.SourceContract); ok {
			out.abi, out.source = out.model.ExposedInterface(c), "static"
			out.name = c.Name
		}
	}
	if out.abi == nil {
		out.abi, out.source = &evm.ABI{Functions: map[[4]byte]evm.Function{}, Events: map[[32]byte]evm.Event{}}, "traffic"
	}
	if f.SourceContract != "" {
		out.name = f.SourceContract
	}
	a.borrow(ctx, out, addr, siblings)
	return f, out
}

func programNodeID(addr string) string  { return "p:" + strings.ToLower(addr) }
func fnNodeID(addr, name string) string { return "ix:" + strings.ToLower(addr) + ":" + name }
func selectorName(sel [4]byte) string   { return "fn_" + hex.EncodeToString(sel[:]) }
func shortAddr(addr string) string      { return addr[:6] + "…" + addr[len(addr)-4:] }
func argsFact(ps []evm.Param) string {
	parts := make([]string, 0, len(ps))
	for _, p := range ps {
		name := p.Name
		if name == "" {
			name = "_"
		}
		parts = append(parts, name+":"+p.Type)
	}
	return strings.Join(parts, ", ")
}

// knownContracts names the addresses source code fixes that every EVM
// reader recognises.
var knownContracts = map[string]string{
	"0x000000000022d473030f116ddee9f6b43ac78ba3": "Permit2",
	"0xc02aaa39b223fe8d0a0e5c4f27ead9083c756cc2": "WETH",
}

// calleeNode is the node a declared callee lands on: a fixed address is a
// program of its own, anything else is one node per kind of callee.
func calleeNode(c solsrc.Callee) core.SurfaceNode {
	if c.Address != "" {
		name := knownContracts[c.Address]
		if name == "" {
			name = shortAddr(c.Address)
		}
		return core.SurfaceNode{ID: programNodeID(c.Address), Kind: "program", Name: name, Source: "static", External: true, Facts: map[string]string{"program": c.Address, "interface": c.Name}}
	}
	slug := strings.ToLower(strings.NewReplacer(" ", "_", "-", "_").Replace(c.Name))
	return core.SurfaceNode{ID: "p:static:" + slug, Kind: "program", Name: c.Name, Source: "static", External: true}
}

// nodesOf lays out one contract: the contract node with its life, one node
// per function, facts from the source where it was matched, and one edge
// per outside contract a function's body reaches.
func nodesOf(addr string, chain Chain, f Facts, in *iface) ([]core.SurfaceNode, []core.SurfaceEdge) {
	facts := map[string]string{"program": addr, "chain": chain.ID, "chain_name": chain.Name, "loader": "evm", "explorer": chain.Explorer}
	if in.name != "" {
		facts["crate"] = in.name
	}
	if f.CodeKeccak != "" {
		facts["bytecode_now"] = f.CodeKeccak
	}
	if f.Implementation != "" {
		facts["loader"] = "evm proxy"
		facts["implementation"] = f.Implementation
		facts["immutable"] = "false"
	} else {
		facts["immutable"] = "true"
	}
	if f.Admin != "" {
		facts["authority"] = f.Admin
	}
	if f.SourceTotal > 0 {
		facts["selectors_in_bytecode"] = strconv.Itoa(f.InCode) + "/" + strconv.Itoa(f.SourceTotal)
	}
	if f.Compiler != "" {
		facts["compiler"] = f.Compiler
		facts["via_ir"] = strconv.FormatBool(f.ViaIR)
		if f.EVMVersion != "" {
			facts["evm_version"] = f.EVMVersion
		}
	}
	if f.Match != "" {
		facts["match_quality"] = f.Match
	}
	if len(f.Holdings) > 0 {
		facts["holdings"] = evm.FormatHoldings(f.Holdings)
		facts["holdings_count"] = strconv.Itoa(len(f.Holdings))
	}
	var events []string
	for _, e := range in.abi.Events {
		events = append(events, e.Name)
	}
	sort.Strings(events)
	if len(events) > 0 {
		facts["events"] = strings.Join(events, ", ")
	}
	nodes := []core.SurfaceNode{{ID: programNodeID(addr), Kind: "program", Name: shortAddr(addr), Source: in.source, Facts: facts}}
	var edges []core.SurfaceEdge
	var src map[[4]byte]solsrc.Function
	var contract *solsrc.Contract
	if in.model != nil && in.name != "" {
		if c, ok := in.model.ByName(in.name); ok {
			contract = c
			src = map[[4]byte]solsrc.Function{}
			for _, fn := range in.model.Exposed(c) {
				src[fn.Selector] = fn
			}
		}
	}
	names := in.nodeNames()
	labels := in.labels(src)
	external := map[string]bool{}
	for sel, fn := range in.abi.Functions {
		nf := map[string]string{"program": addr, "discriminator": hex.EncodeToString(sel[:]), "signature": fn.Sig}
		if len(fn.Inputs) > 0 {
			nf["args"] = argsFact(fn.Inputs)
		}
		if s, ok := src[sel]; ok {
			nf["file"], nf["line"] = s.File, strconv.Itoa(s.Line)
			nf["module"] = strings.TrimSuffix(s.File[strings.LastIndex(s.File, "/")+1:], ".sol")
			if s.Mutability != "" {
				nf["mutability"] = s.Mutability
			}
			if len(s.Modifiers) > 0 {
				nf["modifiers"] = strings.Join(s.Modifiers, ", ")
			}
		}
		id := fnNodeID(addr, names[sel])
		nodes = append(nodes, core.SurfaceNode{ID: id, Parent: programNodeID(addr), Kind: "instruction", Name: labels[sel], Source: in.source, Facts: nf})
		if s, ok := src[sel]; ok && contract != nil {
			for _, c := range in.model.Reach(contract, s) {
				n := calleeNode(c)
				if !external[n.ID] {
					external[n.ID] = true
					nodes = append(nodes, n)
				}
				edges = append(edges, core.SurfaceEdge{From: id, To: n.ID, Kind: "cpi", Source: "static", Facts: map[string]string{"file": s.File, "line": strconv.Itoa(c.Line)}})
			}
		}
	}
	return nodes, edges
}

// rows turns one transaction sent to (or reaching) a contract into a row
// on the function it selected, and callers into edges.
type rowAcc struct {
	addr     string
	in       *iface
	names    map[[4]byte]string
	lookup   *evm.Lookup
	nodes    []core.SurfaceNode
	edges    map[string]*core.SurfaceEdge
	evidence []core.SurfaceEvidence
	known    map[[4]byte]bool
	live     bool
	eventN   map[string]int
	// direct keeps the transactions sent straight to the contract, by
	// function node, newest first as sampled: what a replay re-sends.
	direct map[string][]evm.Tx
	// symbol names an outside contract the receipt shows was reached;
	// nil leaves the short address.
	symbol func(ctx context.Context, addr string) string
}

// reached records that a function's transaction made another contract
// emit: an observed call out, drawn as a wire to that contract.
func (acc *rowAcc) reached(ctx context.Context, from string, rc *evm.Receipt) []string {
	var targets []string
	seen := map[string]bool{}
	for _, lg := range rc.Logs {
		to := strings.ToLower(lg.Address)
		if to == acc.addr || seen[to] {
			continue
		}
		seen[to] = true
		targets = append(targets, to)
		key := from + ">" + programNodeID(to)
		if e, ok := acc.edges[key]; ok {
			e.Count++
			continue
		}
		acc.edges[key] = &core.SurfaceEdge{From: from, To: programNodeID(to), Kind: "cpi", Count: 1, Source: "traffic"}
		name := knownContracts[to]
		if name == "" && acc.symbol != nil {
			name = acc.symbol(ctx, to)
		}
		if name == "" {
			name = shortAddr(to)
		}
		acc.nodes = append(acc.nodes, core.SurfaceNode{ID: programNodeID(to), Kind: "program", Name: name, Source: "traffic", External: true, Facts: map[string]string{"program": to}})
	}
	return targets
}

func newRowAcc(addr string, in *iface, lookup *evm.Lookup, live bool) *rowAcc {
	acc := &rowAcc{addr: addr, in: in, names: in.nodeNames(), lookup: lookup, edges: map[string]*core.SurfaceEdge{}, known: map[[4]byte]bool{}, live: live, direct: map[string][]evm.Tx{}}
	for sel := range in.abi.Functions {
		acc.known[sel] = true
	}
	return acc
}

// add records one transaction. rc is nil when the receipt was not read.
func (acc *rowAcc) add(ctx context.Context, tx evm.Tx, block uint64, at time.Time, rc *evm.Receipt) {
	summary := map[string]any{"slot": block, "source": "tx"}
	if acc.live {
		summary["live"] = true
	}
	if tx.From != "" {
		summary["signer_keys"] = []string{tx.From}
		summary["signers"] = 1
	}
	if rc != nil {
		if rc.GasUsed > 0 {
			summary["units"] = rc.GasUsed
		}
		if !rc.Status {
			summary["failed"] = true
			summary["error"] = "reverted"
		}
	}
	via := ""
	if tx.To != "" && tx.To != acc.addr {
		// Sent to another contract that reached this one: that contract is
		// the caller, the sender still signed.
		via = tx.To
		summary["via"] = via
	}
	sel, ok := evm.Selector(tx.Input)
	if !ok {
		summary["unresolved"] = true
		acc.evidence = append(acc.evidence, core.SurfaceEvidence{NodeID: programNodeID(acc.addr), Kind: "tx", Ref: tx.Hash, Actor: "system", At: &at, Summary: summary})
		return
	}
	if via != "" {
		// Calldata to another contract names that contract's function, not
		// ours: the row lands on the contract, named by the event that
		// showed it, which the write rows carry.
		summary["unresolved"] = true
		summary["caller_selector"] = hex.EncodeToString(sel[:])
		acc.edge(programNodeID(via), programNodeID(acc.addr), via)
		acc.evidence = append(acc.evidence, core.SurfaceEvidence{NodeID: programNodeID(acc.addr), Kind: "tx", Ref: tx.Hash, Actor: "system", At: &at, Summary: summary})
		return
	}
	fn, known := acc.in.abi.Functions[sel]
	namedVia := ""
	if !known {
		if b, ok := acc.in.borrowed[sel]; ok {
			fn, known, namedVia = b.fn, true, b.from
			acc.in.abi.Functions[sel] = fn
			acc.names[sel] = fn.Name
		} else if acc.lookup != nil {
			if named := acc.lookup.Name(ctx, sel); named != nil {
				fn, known = *named, true
				acc.in.abi.Functions[sel] = fn
				acc.names[sel] = fn.Name
			}
		}
	}
	name := selectorName(sel)
	if known {
		name = acc.names[sel]
		if name == "" {
			name = fn.Name
			acc.names[sel] = name
		}
		if _, decoded, err := (&evm.ABI{Functions: map[[4]byte]evm.Function{sel: fn}}).DecodeCall(tx.Input); err == nil && len(decoded) > 0 {
			summary["args"] = map[string]string(decoded)
		}
	}
	id := fnNodeID(acc.addr, name)
	acc.direct[id] = append(acc.direct[id], tx)
	if rc != nil && len(rc.Logs) > 0 {
		if targets := acc.reached(ctx, id, rc); len(targets) > 0 {
			summary["cpi"] = targets
		}
	}
	if !acc.known[sel] {
		acc.known[sel] = true
		facts := map[string]string{"program": acc.addr, "discriminator": hex.EncodeToString(sel[:])}
		if known {
			facts["signature"] = fn.Sig
			if len(fn.Inputs) > 0 {
				facts["args"] = argsFact(fn.Inputs)
			}
			if namedVia != "" {
				facts["named_via"] = namedVia
			}
		}
		acc.nodes = append(acc.nodes, core.SurfaceNode{ID: id, Parent: programNodeID(acc.addr), Kind: "instruction", Name: name, Source: "traffic", Facts: facts})
	}
	acc.evidence = append(acc.evidence, core.SurfaceEvidence{NodeID: id, Kind: "tx", Ref: tx.Hash, Actor: "system", At: &at, Summary: summary})
}

// event records one log as a write on the contract.
func (acc *rowAcc) event(lg evm.Log, at time.Time, n int) {
	summary := map[string]any{"slot": lg.BlockNumber, "kind": "event", "source": "log"}
	if acc.live {
		summary["live"] = true
	}
	if len(lg.Topics) > 0 {
		if e, ok := acc.in.abi.Events[lg.Topics[0]]; ok {
			summary["event"] = e.Name
			if _, fields, err := acc.in.abi.DecodeLog(lg.Topics, lg.Data); err == nil && len(fields) > 0 {
				summary["fields"] = map[string]string(fields)
			}
		} else {
			summary["event"] = "0x" + hex.EncodeToString(lg.Topics[0][:4])
		}
	}
	acc.evidence = append(acc.evidence, core.SurfaceEvidence{NodeID: programNodeID(acc.addr), Kind: "write", Ref: lg.TxHash + "#" + strconv.Itoa(n), Actor: "system", At: &at, Summary: summary})
}

func (acc *rowAcc) edge(from, to, program string) {
	key := from + ">" + to
	if e, ok := acc.edges[key]; ok {
		e.Count++
		return
	}
	acc.edges[key] = &core.SurfaceEdge{From: from, To: to, Kind: "cpi", Count: 1, Source: "traffic"}
	acc.nodes = append(acc.nodes, core.SurfaceNode{ID: programNodeID(program), Kind: "program", Name: shortAddr(program), Source: "traffic", External: true, Facts: map[string]string{"program": program, "chain_caller": "true"}})
}

func (acc *rowAcc) edgeList() []core.SurfaceEdge {
	out := make([]core.SurfaceEdge, 0, len(acc.edges))
	for _, e := range acc.edges {
		out = append(out, *e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].From+out[i].To < out[j].From+out[j].To })
	return out
}

// lookback is how far the sampler looks for a quiet contract's events,
// in blocks; a logs call starts at logsPerCall blocks and halves on a
// refusal down to logsMinCall, where a refusal is the endpoint's archive
// horizon and the lookback ends.
const (
	lookbackBlocks = 50_000
	logsPerCall    = 10_000
	logsMinCall    = 100
)

// BuildSurface implements core.SurfaceBuilder.
func (a *Adapter) BuildSurface(ctx context.Context, in core.SurfaceInput) (core.Surface, []core.SurfaceEvidence, error) {
	if a.Clients == nil {
		return core.Surface{}, nil, core.ErrNotWired
	}
	budget := in.Budget
	if budget.Transactions == 0 {
		budget = core.DefaultSurfaceBudget()
	}
	s := core.Surface{Adapter: Key, BuiltAt: time.Now().UTC(), Sampling: &core.Sampling{}}
	var evidence []core.SurfaceEvidence
	var firstErr error
	reached := false
	for _, addr := range in.Subjects {
		if ctx.Err() != nil {
			break
		}
		chain := chainFor(in.Chains, addr)
		addr = strings.ToLower(addr)
		c := a.Clients(chain)
		var explorer *evm.Explorer
		if a.Explorers != nil {
			explorer = a.Explorers(chain)
		}
		facts, ifc := a.interfaceOf(ctx, addr, chain, in.Clone, in.Siblings)
		s.Merge(nodesOf(addr, chain, facts, ifc))
		acc := newRowAcc(addr, ifc, a.Lookup, false)
		acc.symbol = a.symbolOf(c, explorer)
		if err := a.sample(ctx, c, explorer, acc, budget, s.Sampling); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		reached = true
		acc.evidence = append(acc.evidence, a.profile(ctx, c, acc, budget)...)
		s.Merge(acc.nodes, acc.edgeList())
		evidence = append(evidence, acc.evidence...)
	}
	if !reached && firstErr != nil {
		return s, nil, fmt.Errorf("chain unreachable: %w", firstErr)
	}
	for _, ev := range evidence {
		if ev.At == nil {
			continue
		}
		if s.Sampling.From == nil || ev.At.Before(*s.Sampling.From) {
			t := *ev.At
			s.Sampling.From = &t
		}
		if s.Sampling.To == nil || ev.At.After(*s.Sampling.To) {
			t := *ev.At
			s.Sampling.To = &t
		}
	}
	return s, evidence, nil
}

// sample reads the newest blocks for transactions sent to the contract,
// then the contract's events back through the lookback, and walks the
// newest of those to their transactions, so a contract that moves once
// a day still shows what moved it.
func (a *Adapter) sample(ctx context.Context, c *evm.Client, explorer *evm.Explorer, acc *rowAcc, budget core.SurfaceBudget, sampling *core.Sampling) error {
	head, err := c.BlockNumber(ctx)
	if err != nil {
		return err
	}
	headers := map[uint64]evm.Header{}
	headerOf := func(n uint64) (evm.Header, bool) {
		if h, ok := headers[n]; ok {
			return h, true
		}
		h, err := c.HeaderByNumber(ctx, n)
		if err != nil {
			return evm.Header{}, false
		}
		headers[n] = h
		return h, true
	}
	timeOf := func(n uint64) time.Time {
		if h, ok := headerOf(n); ok {
			return h.Timestamp
		}
		return time.Now().UTC()
	}
	// The sample's reach: the oldest block it could see, and the oldest
	// moment, which is what "nothing in the sample" is measured against.
	oldest := head
	reach := func(n uint64, at *time.Time) {
		if n < oldest {
			oldest = n
		}
		sampling.Blocks = head - oldest
		if at != nil && (sampling.Horizon == nil || at.Before(*sampling.Horizon)) {
			t := at.UTC()
			sampling.Horizon = &t
		}
	}
	read := 0
	seen := map[string]bool{}
	// Recent blocks in full: every transaction sent to the contract.
	recent := uint64(budget.Signatures / 5)
	if recent < 5 {
		recent = 5
	}
	for n := head; n+recent > head && n > 0 && read < budget.Transactions; n-- {
		blk, err := c.BlockByNumber(ctx, n)
		if err != nil {
			return err
		}
		headers[n] = evm.Header{Number: blk.Number, Timestamp: blk.Timestamp}
		reach(n, &blk.Timestamp)
		for _, tx := range blk.Txs {
			if tx.To != acc.addr {
				continue
			}
			sampling.Signatures++
			seen[tx.Hash] = true
			var rc *evm.Receipt
			if read < budget.Transactions {
				if r, err := receiptOf(ctx, c, explorer, tx.Hash); err == nil {
					rc = &r
					read++
					sampling.Transactions++
				}
			}
			acc.add(ctx, tx, blk.Number, blk.Timestamp, rc)
		}
		if !pace(ctx, budget) {
			return ctx.Err()
		}
	}
	// Events back through the lookback, newest window first, until some
	// are found or the lookback ends. A public endpoint refuses windows
	// past its archive horizon (and some refuse wide ones): a refusal
	// halves the window, and a refusal at the smallest is the horizon.
	var logs []evm.Log
	from := head
	window := uint64(logsPerCall)
	for from > 0 && head-from < lookbackBlocks && len(logs) == 0 && ctx.Err() == nil {
		lo := uint64(0)
		if from > window {
			lo = from - window
		}
		got, err := c.Logs(ctx, acc.addr, lo, from)
		if err != nil {
			if window <= logsMinCall {
				break
			}
			window /= 2
			continue
		}
		logs = got
		from = lo
		if h, ok := headerOf(lo); ok {
			reach(lo, &h.Timestamp)
		} else {
			reach(lo, nil)
		}
		if !pace(ctx, budget) {
			return ctx.Err()
		}
	}
	// Newest events first; their transactions are the contract's real
	// callers when the recent blocks had none.
	sort.Slice(logs, func(i, j int) bool { return logs[i].BlockNumber > logs[j].BlockNumber })
	byTx := map[string][]evm.Log{}
	var order []string
	for _, lg := range logs {
		if _, ok := byTx[lg.TxHash]; !ok {
			order = append(order, lg.TxHash)
		}
		byTx[lg.TxHash] = append(byTx[lg.TxHash], lg)
	}
	n := 0
	for _, h := range order {
		if ctx.Err() != nil {
			break
		}
		list := byTx[h]
		at := timeOf(list[0].BlockNumber)
		for i, lg := range list {
			acc.event(lg, at, i)
		}
		if seen[h] || read >= budget.Transactions {
			continue
		}
		tx, block, err := c.TransactionByHash(ctx, h)
		read++
		if err != nil {
			continue
		}
		sampling.Signatures++
		sampling.Transactions++
		seen[h] = true
		var rc *evm.Receipt
		if r, err := receiptOf(ctx, c, explorer, h); err == nil {
			rc = &r
		}
		acc.add(ctx, tx, block, at, rc)
		n++
		if !pace(ctx, budget) {
			return ctx.Err()
		}
	}
	// Past the endpoint's horizon the explorer lists what the contract
	// received; the transactions themselves are still read from the
	// endpoint, which serves any transaction by hash.
	if explorer != nil && read < budget.Transactions && ctx.Err() == nil {
		list, whole, err := explorer.Transactions(ctx, acc.addr, budget.Transactions)
		if err == nil {
			for _, et := range list {
				if ctx.Err() != nil || read >= budget.Transactions {
					break
				}
				at := et.At
				reach(et.Block, &at)
				if seen[et.Hash] {
					continue
				}
				tx, block, err := c.TransactionByHash(ctx, et.Hash)
				read++
				if err != nil {
					continue
				}
				sampling.Signatures++
				sampling.Transactions++
				seen[et.Hash] = true
				var rc *evm.Receipt
				if r, err := receiptOf(ctx, c, explorer, et.Hash); err == nil {
					rc = &r
				}
				acc.add(ctx, tx, block, at, rc)
				if !pace(ctx, budget) {
					return ctx.Err()
				}
			}
			if whole && len(list) < budget.Transactions {
				// The whole history fit: the sample saw everything.
				reach(0, nil)
			}
		}
	}
	return nil
}

// receiptOf reads a receipt from the endpoint, or from the explorer when
// the endpoint no longer holds it. A transaction whose outcome nobody can
// give has no receipt, and is not marked failed.
func receiptOf(ctx context.Context, c *evm.Client, explorer *evm.Explorer, hash string) (evm.Receipt, error) {
	r, err := c.TransactionReceipt(ctx, hash)
	if err == nil {
		return r, nil
	}
	if explorer != nil {
		if r, err2 := explorer.Receipt(ctx, hash); err2 == nil {
			return r, nil
		}
	}
	return evm.Receipt{}, err
}

func pace(ctx context.Context, budget core.SurfaceBudget) bool {
	if budget.Pace <= 0 {
		return ctx.Err() == nil
	}
	select {
	case <-ctx.Done():
		return false
	case <-time.After(budget.Pace):
		return true
	}
}

// profile replays the newest sampled call of each function, up to the
// budget, so every reached function carries one replay before any
// operator looks at it.
func (a *Adapter) profile(ctx context.Context, c *evm.Client, acc *rowAcc, budget core.SurfaceBudget) []core.SurfaceEvidence {
	limit := budget.Profiles
	if limit <= 0 {
		return nil
	}
	var out []core.SurfaceEvidence
	seen := map[string]bool{}
	for _, ev := range acc.evidence {
		if ev.Kind != "tx" || seen[ev.NodeID] || len(acc.direct[ev.NodeID]) == 0 {
			continue
		}
		if len(out) >= limit || ctx.Err() != nil {
			break
		}
		seen[ev.NodeID] = true
		tx := acc.direct[ev.NodeID][0]
		name := ev.NodeID[len("ix:"+acc.addr+":"):]
		if !pace(ctx, budget) {
			break
		}
		if r := Profile(ctx, c, acc.in, a.Lookup, acc.addr, tx, name); r != nil {
			out = append(out, r.Evidence(acc.addr, time.Now().UTC()))
		}
	}
	return out
}

// symbolOf names outside contracts by their ERC-20 symbol through one
// client, else by the name the explorer has for a verified contract,
// remembering answers for the run.
func (a *Adapter) symbolOf(c *evm.Client, explorer *evm.Explorer) func(ctx context.Context, addr string) string {
	cache := map[string]string{}
	var mu sync.Mutex
	return func(ctx context.Context, addr string) string {
		mu.Lock()
		if s, ok := cache[addr]; ok {
			mu.Unlock()
			return s
		}
		mu.Unlock()
		s := c.Symbol(ctx, addr)
		if s == "" && explorer != nil {
			s = explorer.Name(ctx, addr)
		}
		mu.Lock()
		cache[addr] = s
		mu.Unlock()
		return s
	}
}
