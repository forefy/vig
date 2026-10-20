package solana

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/forefy/vig/backend/internal/chain"
	"github.com/forefy/vig/backend/internal/core"
	"github.com/forefy/vig/backend/internal/rustsrc"
	"github.com/forefy/vig/backend/internal/sbpf"
)

// SurfaceBuilder maps a program's instructions and accounts from its IDL,
// or from discriminators in its bytecode, then samples recent transactions
// from the public RPC as first evidence. Nothing here needs a key or a
// paid endpoint; every read is bounded by the budget.
type SurfaceBuilder struct {
	Chain *chain.Client
	// Osec answers whether a callee's build is verified; nil skips that
	// lookup and leaves the fact absent.
	Osec *chain.Osec
}

// BuildSurface implements core.SurfaceBuilder.
func (b *SurfaceBuilder) BuildSurface(ctx context.Context, in core.SurfaceInput) (core.Surface, []core.SurfaceEvidence, error) {
	if b.Chain == nil {
		return core.Surface{}, nil, errors.New("chain not configured")
	}
	budget := in.Budget
	if budget.Transactions == 0 {
		budget = core.DefaultSurfaceBudget()
	}
	s := core.Surface{Adapter: Key, BuiltAt: time.Now().UTC(), Sampling: &core.Sampling{}}
	// The source, when the mapper has it: handler locations, constraints
	// and the CPIs the code holds whether or not traffic ever exercised them.
	var model *rustsrc.Model
	if in.Clone != nil && in.Clone.Root != "" {
		model, _ = rustsrc.Load(in.Clone.Root, in.Clone.Path)
	}
	// The base of a diff audit, so every handler can say whether the
	// change reached it.
	var base *rustsrc.Model
	if model != nil && in.Base != nil && in.Base.Root != "" {
		base, _ = rustsrc.Load(in.Base.Root, in.Base.Path)
	}
	// Deploy history is read before any sampling: a single listing per
	// subject, taken while the primary endpoint is still fresh. A short
	// history fallback answers the same listing with nothing.
	histories := map[string]map[string]string{}
	for _, pid := range in.Subjects {
		histories[pid] = b.history(ctx, pid, budget)
	}
	var evidence []core.SurfaceEvidence
	var firstErr error
	reached := false
	for _, pid := range in.Subjects {
		if ctx.Err() != nil {
			break
		}
		specs, idl, source := b.instructions(ctx, pid)
		s.Merge(programNodes(pid, specs, source), nil)
		setFacts(&s, programNodeID(pid), histories[pid])
		if model != nil {
			if c, ok := model.CrateFor(pid); ok {
				s.Enrich(staticNodes(pid, c, peersOf(model, in.Subjects)))
				setFacts(&s, programNodeID(pid), map[string]string{"crate": c.Name})
				// An Anchor handler's discriminator follows from its name, so
				// traffic on a program with no IDL lands on the named node
				// rather than on a hashed one beside it.
				specs = withStaticSpecs(specs, c)
				// With no IDL the source still names the accounts, the
				// arguments and the seeds, so replays decode and check PDAs.
				if idl == nil {
					idl = staticIDL(c, peersOf(model, in.Subjects), model.Types)
				}
				if base != nil {
					applyDiff(&s, pid, model, base)
				}
			}
		}
		nodes, edges, ev, err := b.sample(ctx, pid, specs, idl, budget, s.Sampling)
		s.Merge(nodes, edges)
		evidence = append(evidence, ev...)
		if err != nil && firstErr == nil {
			firstErr = err
		}
		if err == nil {
			reached = true
			evidence = append(evidence, b.profile(ctx, pid, idl, ev, budget)...)
		}
	}
	// A map built with the chain unreachable is a guess, not a map: fail so
	// the mapper tries again rather than saving an empty tree.
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
	// A subject reached through another subject's CPI was first seen as an
	// external callee; it is audited, so it is not external.
	for _, pid := range in.Subjects {
		for i := range s.Nodes {
			if s.Nodes[i].ID == programNodeID(pid) {
				s.Nodes[i].External = false
			}
		}
	}
	// Who the subjects compose with, and whether those callees can change
	// under them: read last, after every edge is on the map.
	b.trust(ctx, &s, in.Subjects, budget)
	// An audited program that another audited program calls was first met
	// as a caller in traffic; it is a subject, not an outside program.
	subject := map[string]bool{}
	for _, pid := range in.Subjects {
		subject[programNodeID(pid)] = true
	}
	for i := range s.Nodes {
		if subject[s.Nodes[i].ID] {
			s.Nodes[i].External = false
		}
	}
	return s, evidence, nil
}

// instructions learns a program's instructions: the IDL when it has one,
// else the Anchor discriminators its bytecode carries.
func (b *SurfaceBuilder) instructions(ctx context.Context, pid string) ([]instructionSpec, *idlSpec, string) {
	if native, ok := nativeSpec(pid); ok {
		return native.specs(), nil, "native"
	}
	if raw, err := b.Chain.IDL(ctx, pid); err == nil {
		if idl, err := parseIDL(raw); err == nil && len(idl.Instructions) > 0 {
			return idl.Instructions, idl, "idl"
		}
	}
	pf, err := b.Chain.Program(ctx, pid)
	if err != nil || len(pf.Bytecode) == 0 {
		return nil, nil, "traffic"
	}
	prof, err := sbpf.Analyze(pf.Bytecode)
	if err != nil {
		return nil, nil, "traffic"
	}
	var specs []instructionSpec
	for _, name := range prof.AnchorIxs {
		specs = append(specs, instructionSpec{Name: name, Discriminator: anchorDiscriminator("global:" + name)})
	}
	if len(specs) == 0 {
		return nil, nil, "traffic"
	}
	return specs, nil, "bytecode"
}

// Node ids: p:<program>, ix:<program>:<name>, acct:<program>:<name>:<account>.
func programNodeID(pid string) string        { return "p:" + pid }
func ixNodeID(pid, name string) string       { return "ix:" + pid + ":" + name }
func acctNodeID(pid, ix, name string) string { return "acct:" + pid + ":" + ix + ":" + name }
func discriminatorName(d []byte) string      { return "ix_" + hex.EncodeToString(d) }
func sigKey(pid string, d [8]byte) string    { return pid + ":" + hex.EncodeToString(d[:]) }

// programNodes lays out one program: the program, its instructions, their
// accounts.
func programNodes(pid string, specs []instructionSpec, source string) []core.SurfaceNode {
	nodes := []core.SurfaceNode{{ID: programNodeID(pid), Kind: "program", Name: shortID(pid), Source: source, Facts: map[string]string{"program": pid}}}
	for _, ins := range specs {
		facts := map[string]string{"program": pid, "discriminator": hex.EncodeToString(ins.Discriminator[:])}
		if len(ins.Args) > 0 {
			var args []string
			for _, a := range ins.Args {
				args = append(args, snake(a.Name)+":"+typeWord(a.Type))
			}
			facts["args"] = strings.Join(args, ", ")
		}
		if len(ins.Accounts) > 0 {
			facts["accounts"] = fmt.Sprint(len(ins.Accounts))
		}
		nodes = append(nodes, core.SurfaceNode{ID: ixNodeID(pid, ins.Name), Parent: programNodeID(pid), Kind: "instruction", Name: ins.Name, Source: source, Facts: facts})
		for _, a := range ins.Accounts {
			f := map[string]string{}
			if a.Signer {
				f["signer"] = "true"
			}
			if a.Writable {
				f["writable"] = "true"
			}
			if a.Seeds != "" {
				f["seeds"] = a.Seeds
			}
			nodes = append(nodes, core.SurfaceNode{ID: acctNodeID(pid, ins.Name, a.Name), Parent: ixNodeID(pid, ins.Name), Kind: "account", Name: a.Name, Source: source, Facts: f})
		}
	}
	return nodes
}

// sample reads recent transactions of a program and turns every call into
// evidence on the instruction it hit. Calls with a discriminator the map
// does not know become traffic-sourced nodes, so the map grows from what
// the chain actually does.
func (b *SurfaceBuilder) sample(ctx context.Context, pid string, specs []instructionSpec, idl *idlSpec, budget core.SurfaceBudget, sampling *core.Sampling) ([]core.SurfaceNode, []core.SurfaceEdge, []core.SurfaceEvidence, error) {
	byDisc := map[string]instructionSpec{}
	for _, ins := range specs {
		byDisc[sigKey(pid, ins.Discriminator)] = ins
	}
	sigs, err := b.Chain.SignaturesFor(ctx, pid, budget.Signatures)
	if err != nil {
		return nil, nil, nil, err
	}
	sampling.Signatures += len(sigs)
	acc := &rowAcc{edges: map[string]*core.SurfaceEdge{}}
	read := 0
	for _, sig := range sigs {
		if read >= budget.Transactions || ctx.Err() != nil {
			break
		}
		if read > 0 && budget.Pace > 0 {
			select {
			case <-ctx.Done():
				return acc.nodes, edgeList(acc.edges), acc.evidence, nil
			case <-time.After(budget.Pace):
			}
		}
		raw, err := b.Chain.GetTransaction(ctx, sig.Signature)
		read++
		if err != nil {
			// One retry after a pause covers a rate-limit answer; anything
			// else is skipped so the budget is spent on the next signature.
			var rpcErr *chain.RPCError
			if errors.As(err, &rpcErr) && rpcErr.NotFound() {
				continue
			}
			select {
			case <-ctx.Done():
				return acc.nodes, edgeList(acc.edges), acc.evidence, nil
			case <-time.After(4 * budget.Pace):
			}
			if raw, err = b.Chain.GetTransaction(ctx, sig.Signature); err != nil {
				continue
			}
		}
		tx, err := chain.ParseCalls(raw)
		if err != nil {
			continue
		}
		sampling.Transactions++
		acc.add(pid, specs, byDisc, idl, tx)
	}
	return acc.nodes, edgeList(acc.edges), acc.evidence, nil
}

// maxKeysKept bounds the account keys recorded per sampled call.
const maxKeysKept = 40

// withStaticSpecs adds the crate's Anchor handlers the specs lack, keyed by
// the discriminator their name implies.
func withStaticSpecs(specs []instructionSpec, c *rustsrc.Crate) []instructionSpec {
	if c.Framework != "anchor" {
		return specs
	}
	have := map[[8]byte]bool{}
	for _, sp := range specs {
		have[sp.Discriminator] = true
	}
	for _, ix := range c.Instructions {
		d := anchorDiscriminator("global:" + ix.Name)
		if have[d] {
			continue
		}
		have[d] = true
		specs = append(specs, instructionSpec{Name: ix.Name, Discriminator: d})
	}
	return specs
}

// discFromName finds the spec discriminator for a known name; for unknown
// names it returns a zero value that is never in the map.
func discFromName(name string, byDisc map[string]instructionSpec, pid string) [8]byte {
	for key, ins := range byDisc {
		if ins.Name == name && strings.HasPrefix(key, pid+":") {
			return ins.Discriminator
		}
	}
	return [8]byte{}
}

func edgeList(m map[string]*core.SurfaceEdge) []core.SurfaceEdge {
	out := make([]core.SurfaceEdge, 0, len(m))
	for _, e := range m {
		out = append(out, *e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].From+out[i].To < out[j].From+out[j].To })
	return out
}

func uniq(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func shortID(id string) string {
	if len(id) <= 12 {
		return id
	}
	return id[:4] + "…" + id[len(id)-4:]
}

var knownPrograms = map[string]string{
	"11111111111111111111111111111111":             "System",
	"TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA":  "Token",
	"TokenzQdBNbLqP5VEhdkAS6EPFLC1PHnBqCXEpPxuEb":  "Token-2022",
	"ATokenGPvbdGVxr1b2hvZbsiqW5xWH25efTNsLJA8knL": "Associated Token",
	"MemoSq4gqABAXKb96qnH8TysNcWxMyWCqXgDLGmfcHr":  "Memo",
	"ComputeBudget111111111111111111111111111111":  "Compute Budget",
	"Stake11111111111111111111111111111111111111":  "Stake",
	"BPFLoaderUpgradeab1e11111111111111111111111":  "BPF Loader",
	"SysvarRent111111111111111111111111111111111":  "Rent",
	"SysvarC1ock11111111111111111111111111111111":  "Clock",
	"metaqbxxUerdq28cj1RbAWkYQm3ybzjb6a8bt518x1s":  "Token Metadata",
	"JUP6LkbZbjS1jKKwapdHNy74zcZ3tLUZoi5QNyVTaV4":  "Jupiter",
}

// KnownProgram names a well-known program, else shortens its id.
func KnownProgram(id string) string {
	if n, ok := knownPrograms[id]; ok {
		return n
	}
	return shortID(id)
}

// profile replays the newest sampled transaction of each instruction, up
// to the budget, so every reached instruction carries one full replay
// before any operator looks at it.
func (b *SurfaceBuilder) profile(ctx context.Context, pid string, idl *idlSpec, evidence []core.SurfaceEvidence, budget core.SurfaceBudget) []core.SurfaceEvidence {
	limit := budget.Profiles
	if limit <= 0 {
		return nil
	}
	seen := map[string]bool{}
	var out []core.SurfaceEvidence
	failed := 0
	var firstErr error
	defer func() {
		if failed > 0 {
			slog.Warn("surface: replays skipped", "program", pid, "failed", failed, "err", firstErr)
		}
	}()
	for _, ev := range evidence {
		if ev.Kind != "tx" || seen[ev.NodeID] || ev.Ref == "" {
			continue
		}
		if len(out) >= limit || ctx.Err() != nil {
			break
		}
		seen[ev.NodeID] = true
		select {
		case <-ctx.Done():
			return out
		case <-time.After(budget.Pace):
		}
		r, err := Profile(ctx, b.Chain, pid, idl, ev.Ref, strings.TrimPrefix(ev.NodeID, "ix:"+pid+":"))
		if err != nil {
			failed++
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		// Dated by the run, not by the sampled transaction: the row says
		// when the chain was asked, and the next run's row says it again.
		now := time.Now().UTC()
		out = append(out, r.Evidence(pid, "system", &now))
	}
	return out
}
