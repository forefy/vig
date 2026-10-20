package jobs

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/forefy/vig/backend/internal/core"
	"github.com/forefy/vig/backend/internal/notify"
	"github.com/forefy/vig/backend/internal/store"
)

// Watch keeps one live session per watchable audit. Sessions are started
// as audits become watchable and cancelled as they stop being so; a
// session that ends on its own is restarted with a growing pause, and a
// reconnect backfills from the cursor, so the map is complete or says
// where it is not.
type Watch struct {
	Store    *store.Store
	Watchers map[string]core.Watcher
	Budget   core.WatchBudget
	// Notifier tells the audit's owner when a watched program drifts;
	// nil keeps the timeline event only.
	Notifier *notify.Notifier
	// Every is how often the set of watchable audits is re-read.
	Every time.Duration
	// Max bounds concurrent sessions; the oldest audits win.
	Max int

	mu       sync.Mutex
	sessions map[string]context.CancelFunc
}

// Loop runs until ctx ends.
func (w *Watch) Loop(ctx context.Context) {
	every := w.Every
	if every == 0 {
		every = 30 * time.Second
	}
	for {
		w.reconcile(ctx)
		select {
		case <-ctx.Done():
			w.mu.Lock()
			for _, cancel := range w.sessions {
				cancel()
			}
			w.mu.Unlock()
			return
		case <-time.After(every):
		}
	}
}

// reconcile starts sessions for audits that need one and stops the rest.
func (w *Watch) reconcile(ctx context.Context) {
	audits, err := w.Store.ListWatchable(ctx)
	if err != nil {
		slog.Error("watch: list", "err", err)
		return
	}
	max := w.Max
	if max == 0 {
		max = 64
	}
	want := map[string]store.Audit{}
	for i, a := range audits {
		if i >= max {
			break
		}
		want[a.ID] = a
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.sessions == nil {
		w.sessions = map[string]context.CancelFunc{}
	}
	for id, cancel := range w.sessions {
		if _, ok := want[id]; !ok {
			cancel()
			delete(w.sessions, id)
		}
	}
	for id, a := range want {
		if _, ok := w.sessions[id]; ok {
			continue
		}
		sctx, cancel := context.WithCancel(ctx)
		w.sessions[id] = cancel
		go w.run(sctx, a)
	}
}

// run keeps one audit's session alive until its context ends.
func (w *Watch) run(ctx context.Context, a store.Audit) {
	pause := 5 * time.Second
	for {
		started := time.Now()
		err := w.session(ctx, a)
		if ctx.Err() != nil {
			return
		}
		// A session that held for a while earns a fresh, short pause.
		if time.Since(started) > 2*time.Minute {
			pause = 5 * time.Second
		}
		if err != nil {
			slog.Warn("watch: session", "audit", a.ID, "err", err, "retry_in", pause)
			_ = w.Store.SetWatchState(ctx, a.ID, core.WatchError, err.Error())
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(pause):
		}
		if pause < 5*time.Minute {
			pause *= 2
		}
	}
}

// session runs one connection's worth of watching.
func (w *Watch) session(ctx context.Context, a store.Audit) error {
	t, err := w.Store.GetTarget(ctx, a.TargetID)
	if err != nil {
		return err
	}
	watcher := w.Watchers[t.Adapter]
	if watcher == nil {
		// Nothing watches this adapter; the audit stays listed but idle.
		<-ctx.Done()
		return nil
	}
	if err := w.Store.EnsureWatch(ctx, a.ID); err != nil {
		return err
	}
	doc, err := w.Store.GetSurface(ctx, a.ID)
	if err != nil {
		return err
	}
	sample, err := w.Store.ListSurfaceEvidence(ctx, a.ID, 5000)
	if err != nil {
		return err
	}
	wt, err := w.Store.GetWatch(ctx, a.ID)
	if err != nil {
		return err
	}
	var findings []core.Finding
	if a.ReportID != "" {
		if r, err := w.Store.GetReport(ctx, a.ReportID); err == nil {
			findings = r.Document().Findings
		}
	}
	doc.Attend(sample, findings)
	var priority []string
	for _, n := range doc.Nodes {
		if len(n.Findings) > 0 {
			priority = append(priority, n.ID)
		}
	}
	budget := w.Budget
	if budget.Details == 0 {
		budget = core.DefaultWatchBudget()
	}
	in := core.WatchInput{Subjects: a.ProgramIDs, Chains: map[string]string{}, Surface: &doc, Sample: sample, Cursors: wt.Cursors, Accounts: wt.Accounts, Priority: priority, Budget: budget}
	if programs, err := w.Store.ListPrograms(ctx, a.TargetID); err == nil {
		for _, p := range programs {
			in.Chains[p.ProgramID] = p.Chain
		}
	}
	if sibs, err := w.Store.ListRepoPrograms(ctx, a.Repo, a.TargetID); err == nil {
		for _, p := range sibs {
			in.Siblings = append(in.Siblings, core.Sibling{ID: p.ProgramID, Chain: p.Chain})
		}
	}
	sink := &storeSink{store: w.Store, auditID: a.ID, audit: a, notifier: w.Notifier}
	err = watcher.Watch(ctx, in, sink)
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

// storeSink persists what a session learns.
type storeSink struct {
	store    *store.Store
	auditID  string
	audit    store.Audit
	notifier *notify.Notifier
}

// drift is a write on a program node that changes what was audited: the
// code, its authority, or its interface. It becomes a timeline event and
// a notification to the owner, beside the row on the map.
func (s *storeSink) drift(ctx context.Context, rows []core.SurfaceEvidence) {
	for _, ev := range rows {
		if ev.Kind != "write" || !strings.HasPrefix(ev.NodeID, "p:") {
			continue
		}
		kind, _ := ev.Summary["kind"].(string)
		if kind != "program_data" && kind != "idl" {
			continue
		}
		program := strings.TrimPrefix(ev.NodeID, "p:")
		payload := map[string]any{"program": program, "kind": kind, "ref": ev.Ref}
		for _, k := range []string{"slot", "bytecode_now", "authority_now", "implementation"} {
			if v, ok := ev.Summary[k]; ok {
				payload[k] = v
			}
		}
		if err := s.store.AddEvent(ctx, s.store.Pool, store.AuditEvent{AuditID: s.auditID, Kind: "surface_drift", Actor: "system", State: s.audit.State, Payload: payload}); err != nil {
			slog.Warn("watch: drift event", "audit", s.auditID, "err", err)
		}
		if s.notifier != nil && s.audit.UserID != "" {
			s.notifier.Notify(ctx, s.audit.UserID, "surface_drift", map[string]any{"audit_id": s.auditID, "program": program, "kind": kind, "ref": ev.Ref, "adapter": s.audit.Adapter})
		}
	}
}

func (s *storeSink) Evidence(ctx context.Context, rows []core.SurfaceEvidence) error {
	n, err := s.store.AddLiveEvidence(ctx, s.auditID, rows)
	if err == nil && n > 0 {
		s.drift(ctx, rows)
	}
	return err
}

func (s *storeSink) Tally(ctx context.Context, nodeID string, at time.Time, calls, failed int, units int64) error {
	return s.store.AddRollup(ctx, s.auditID, nodeID, at, calls, failed, units)
}

func (s *storeSink) Coverage(ctx context.Context, span core.CoverageSpan) (string, error) {
	if span.ID != "" {
		return span.ID, s.store.ExtendCoverage(ctx, s.auditID, span.ID, span.ToSlot, span.ToAt)
	}
	return s.store.AddCoverage(ctx, s.auditID, span)
}

func (s *storeSink) Cursor(ctx context.Context, subject string, c core.Cursor) error {
	return s.store.SaveWatchCursor(ctx, s.auditID, subject, c)
}

func (s *storeSink) Accounts(ctx context.Context, accounts []core.WatchedAccount) error {
	return s.store.SaveWatchAccounts(ctx, s.auditID, accounts)
}

func (s *storeSink) Facts(ctx context.Context, nodeID string, facts map[string]string) error {
	return s.store.UpdateSurface(ctx, s.auditID, func(doc *core.Surface) bool {
		for i := range doc.Nodes {
			if doc.Nodes[i].ID != nodeID {
				continue
			}
			if doc.Nodes[i].Facts == nil {
				doc.Nodes[i].Facts = map[string]string{}
			}
			for k, v := range facts {
				if v == "" {
					delete(doc.Nodes[i].Facts, k)
				} else {
					doc.Nodes[i].Facts[k] = v
				}
			}
			return true
		}
		return false
	})
}

func (s *storeSink) Extend(ctx context.Context, nodes []core.SurfaceNode, edges []core.SurfaceEdge) error {
	if len(nodes) == 0 && len(edges) == 0 {
		return nil
	}
	return s.store.UpdateSurface(ctx, s.auditID, func(doc *core.Surface) bool {
		if len(doc.Nodes) > 5000 {
			return false
		}
		return doc.Enrich(nodes, edges) > 0 || len(edges) > 0
	})
}

func (s *storeSink) State(ctx context.Context, state core.WatchState, msg string) error {
	return s.store.SetWatchState(ctx, s.auditID, state, msg)
}
