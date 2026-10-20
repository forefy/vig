package jobs

import (
	"context"
	"log/slog"
	"time"

	"github.com/forefy/vig/backend/internal/core"
	"github.com/forefy/vig/backend/internal/github"
	"github.com/forefy/vig/backend/internal/store"
)

// SurfaceMapper builds the surface map of every audit that lacks one, one
// audit at a time, within a budget that fits a shared public endpoint. It
// runs as soon as an audit is queued, so the customer never asks for it.
type SurfaceMapper struct {
	Store    *store.Store
	Builders map[string]core.SurfaceBuilder
	Budget   core.SurfaceBudget
	Every    time.Duration
	// GitHub checks out the audited commit so the builder can read the
	// source; nil, or a checkout that fails, maps from the chain alone.
	GitHub *github.Client
	// Inspectors re-read each program against the checkout once it is
	// here, so the panel written at intake carries what the source says.
	Inspectors map[string]core.ProgramInspector
}

// Loop runs until ctx ends.
func (m *SurfaceMapper) Loop(ctx context.Context) {
	every := m.Every
	if every == 0 {
		every = 15 * time.Second
	}
	for {
		for {
			a, ok, err := m.Store.ClaimSurfaceWork(ctx)
			if err != nil {
				slog.Error("surface: claim", "err", err)
				break
			}
			if !ok {
				break
			}
			m.Once(ctx, a)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(every):
		}
	}
}

// Once maps one audit.
func (m *SurfaceMapper) Once(ctx context.Context, a store.Audit) {
	t, err := m.Store.GetTarget(ctx, a.TargetID)
	if err != nil {
		_ = m.Store.SetSurfaceError(ctx, a.ID, "target: "+err.Error())
		return
	}
	builder := m.Builders[t.Adapter]
	if builder == nil {
		// Nothing to map for this adapter; an empty document stops the retries.
		_ = m.Store.SaveSurface(ctx, a.ID, core.Surface{Adapter: t.Adapter, Nodes: []core.SurfaceNode{}, BuiltAt: time.Now().UTC()})
		return
	}
	budget := m.Budget
	if budget.Transactions == 0 {
		budget = core.DefaultSurfaceBudget()
	}
	bctx, cancel := context.WithTimeout(ctx, budget.Deadline)
	defer cancel()
	started := time.Now()
	in := core.SurfaceInput{Subjects: a.ProgramIDs, Chains: map[string]string{}, Budget: budget}
	programs, err := m.Store.ListPrograms(ctx, a.TargetID)
	if err == nil {
		for _, p := range programs {
			in.Chains[p.ProgramID] = p.Chain
		}
	}
	if sibs, err := m.Store.ListRepoPrograms(ctx, t.Repo, a.TargetID); err == nil {
		for _, p := range sibs {
			in.Siblings = append(in.Siblings, core.Sibling{ID: p.ProgramID, Chain: p.Chain})
		}
	}
	if m.GitHub != nil && t.Repo != "" && a.HeadSHA != "" {
		if root, err := m.GitHub.Checkout(bctx, t.Repo, a.HeadSHA); err == nil {
			in.Clone = &core.Clone{Root: root, Repo: t.Repo, Commit: a.HeadSHA, Path: t.Subpath}
			m.refreshPanels(bctx, t, programs, root)
		} else {
			slog.Warn("surface: checkout", "audit", a.ID, "err", err)
		}
		// A diff audit also gets its base, so the map can say which
		// instructions the change touched.
		if a.Coverage == "diff" && a.BaseSHA != "" {
			if root, err := m.GitHub.Checkout(bctx, t.Repo, a.BaseSHA); err == nil {
				in.Base = &core.Clone{Root: root, Repo: t.Repo, Commit: a.BaseSHA, Path: t.Subpath}
			} else {
				slog.Warn("surface: base checkout", "audit", a.ID, "err", err)
			}
		}
	}
	surface, evidence, err := builder.BuildSurface(bctx, in)
	if err != nil {
		slog.Warn("surface: build", "audit", a.ID, "err", err)
		_ = m.Store.SetSurfaceError(ctx, a.ID, err.Error())
		return
	}
	if err := m.Store.SaveSurface(ctx, a.ID, surface); err != nil {
		slog.Error("surface: save", "audit", a.ID, "err", err)
		return
	}
	n, err := m.Store.AddSurfaceEvidence(ctx, a.ID, evidence)
	if err != nil {
		slog.Error("surface: evidence", "audit", a.ID, "err", err)
	}
	payload := map[string]any{"nodes": len(surface.Nodes), "evidence": n, "ms": time.Since(started).Milliseconds()}
	if surface.Sampling != nil {
		payload["transactions"] = surface.Sampling.Transactions
	}
	if err := m.Store.AddEvent(ctx, m.Store.Pool, store.AuditEvent{AuditID: a.ID, Kind: "surface_mapped", Actor: "system", State: a.State, Payload: payload}); err != nil {
		slog.Error("surface: event", "audit", a.ID, "err", err)
	}
	slog.Info("surface: mapped", "audit", a.ID, "nodes", len(surface.Nodes), "evidence", n)
}

// refreshPanels re-reads every program of the target against the checkout
// and writes the panel back, so the intake's "pending" rows resolve once
// the source is here. Ownership columns are untouched.
func (m *SurfaceMapper) refreshPanels(ctx context.Context, t store.Target, programs []store.Program, root string) {
	insp := m.Inspectors[t.Adapter]
	if insp == nil {
		return
	}
	for _, p := range programs {
		facts, err := insp.InspectProgram(ctx, p.ProgramID, core.InspectOpts{Chain: p.Chain, Root: root})
		if err != nil {
			slog.Warn("surface: panel", "program", p.ProgramID, "err", err)
			continue
		}
		if _, err := m.Store.UpsertProgram(ctx, store.Program{
			TargetID: t.ID, ProgramID: p.ProgramID, Chain: facts.Chain, Loader: facts.Loader, UpgradeAuthority: facts.UpgradeAuthority,
			LastDeploySlot: facts.LastDeploySlot, BytecodeSHA256: facts.BytecodeSHA256, IDLPresent: facts.IDLPresent, AnchorVersion: facts.IDLVersion,
			VerifiedBuild: facts.VerifiedBuild, VerifiedCommit: facts.VerifiedCommit, Panel: facts.Panel,
		}); err != nil {
			slog.Warn("surface: panel write", "program", p.ProgramID, "err", err)
		}
	}
}
