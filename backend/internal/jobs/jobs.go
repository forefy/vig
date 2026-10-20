// Package jobs holds the background workers: the scan runner, the
// housekeeping loop (purchase expiry, credit expiry, overdue audits, clone
// cleanup) and the purchase confirmer that credits payments the user never
// reported.
package jobs

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/forefy/vig/backend/internal/chain"
	"github.com/forefy/vig/backend/internal/core"
	"github.com/forefy/vig/backend/internal/github"
	"github.com/forefy/vig/backend/internal/notify"
	"github.com/forefy/vig/backend/internal/store"
)

// ScanRunner executes pre-flight scans.
type ScanRunner struct {
	Store  *store.Store
	GitHub *github.Client
	// Adapters by key; the scan picks one from the program id's shape,
	// else from what the checkout holds.
	Adapters map[string]core.Adapter
	// Timeout bounds one scan.
	Timeout time.Duration
}

// AdapterFor picks the adapter for a scan: the one whose address shape
// the program id has, else the one whose language the checkout is in,
// else the first. Nil when a program id fits no adapter.
func (r *ScanRunner) AdapterFor(programID, root string) core.Adapter {
	if programID != "" {
		for _, a := range r.Adapters {
			if insp, ok := a.(core.ProgramInspector); ok && insp.ValidProgramID(programID) {
				return a
			}
		}
		return nil
	}
	if root != "" {
		if a := r.Adapters["ethereum"]; a != nil && hasSolidity(root) && !hasCargo(root) {
			return a
		}
	}
	if a := r.Adapters["solana"]; a != nil {
		return a
	}
	for _, a := range r.Adapters {
		return a
	}
	return nil
}

func hasSolidity(root string) bool { return hasFile(root, ".sol") }
func hasCargo(root string) bool    { return hasFile(root, "Cargo.toml") }

// hasFile says whether any file under root, three levels deep, has the
// suffix; vendored directories are skipped.
func hasFile(root, suffix string) bool {
	found := false
	base := filepath.Clean(root)
	_ = filepath.WalkDir(base, func(p string, d os.DirEntry, err error) error {
		if err != nil || found {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if p != base && (name == "node_modules" || name == "lib" || name == ".git" || name == "target") {
				return filepath.SkipDir
			}
			if strings.Count(strings.TrimPrefix(p, base), string(filepath.Separator)) > 3 {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(p, suffix) {
			found = true
		}
		return nil
	})
	return found
}

// Run executes one scan and stores the outcome.
func (r *ScanRunner) Run(ctx context.Context, sc store.Scan) error {
	timeout := r.Timeout
	if timeout == 0 {
		timeout = 3 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	result, err := r.preflight(ctx, sc)
	if err != nil {
		_ = r.Store.FinishScan(context.WithoutCancel(ctx), sc.ID, nil, err)
		return err
	}
	return r.Store.FinishScan(context.WithoutCancel(ctx), sc.ID, &result, nil)
}

func (r *ScanRunner) preflight(ctx context.Context, sc store.Scan) (core.Preflight, error) {
	repo, sub, _ := strings.Cut(sc.Repo, "#")
	root, err := r.GitHub.Checkout(ctx, repo, sc.CommitSHA)
	if err != nil {
		return core.Preflight{}, err
	}
	draft := core.IntakeDraft{"source": {"path": sub}, "programs": {"program_ids": []string{}}}
	if sc.ProgramID != "" {
		draft["programs"]["program_ids"] = []string{sc.ProgramID}
	}
	adapter := r.AdapterFor(sc.ProgramID, filepath.Join(root, filepath.FromSlash(sub)))
	if adapter == nil {
		return core.Preflight{}, errors.New("no adapter for this scan")
	}
	pf, err := adapter.Preflight(ctx, core.Clone{Root: root, Repo: repo, Commit: sc.CommitSHA}, draft)
	if err != nil {
		return core.Preflight{}, err
	}
	pf.Adapter = adapter.Key()
	return pf, nil
}

// Loop drains queued scans until ctx ends.
func (r *ScanRunner) Loop(ctx context.Context) {
	for {
		sc, err := r.Store.ClaimQueuedScan(ctx)
		if err != nil {
			if !errors.Is(err, store.ErrNotFound) && ctx.Err() == nil {
				slog.Error("scan worker", "err", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(2 * time.Second):
			}
			continue
		}
		if err := r.Run(ctx, sc); err != nil {
			slog.Warn("scan failed", "token", sc.Token, "err", err)
		}
	}
}

// Housekeeping expires pending purchases and old credits, flags overdue
// audits and removes stale clones on an interval.
type Housekeeping struct {
	Store    *store.Store
	GitHub   *github.Client
	Notifier *notify.Notifier
	Every    time.Duration
}

// Loop runs until ctx ends.
func (h *Housekeeping) Loop(ctx context.Context) {
	every := h.Every
	if every == 0 {
		every = 5 * time.Minute
	}
	for {
		h.Once(ctx)
		select {
		case <-ctx.Done():
			return
		case <-time.After(every):
		}
	}
}

// Once runs one housekeeping pass.
func (h *Housekeeping) Once(ctx context.Context) {
	if expired, err := h.Store.ExpirePurchases(ctx); err != nil {
		slog.Error("housekeeping: expire purchases", "err", err)
	} else {
		for _, p := range expired {
			h.Notifier.Notify(ctx, p.UserID, "purchase_expired", map[string]any{"purchase_id": p.ID, "sku": string(p.SKU)})
		}
		if len(expired) > 0 {
			slog.Info("housekeeping: expired purchases", "n", len(expired))
		}
	}
	if n, err := h.Store.ExpireCredits(ctx); err != nil {
		slog.Error("housekeeping: expire credits", "err", err)
	} else if n > 0 {
		slog.Info("housekeeping: expired credits", "users", n)
	}
	if overdue, err := h.Store.ListOverdue(ctx, core.OverdueGrace); err != nil {
		slog.Error("housekeeping: overdue audits", "err", err)
	} else {
		for _, a := range overdue {
			if err := h.Store.MarkOverdue(ctx, a); err != nil {
				slog.Error("housekeeping: mark overdue", "audit", a.ID, "err", err)
				continue
			}
			h.Notifier.Notify(ctx, a.UserID, "audit_overdue", map[string]any{"audit_id": a.ID, "eta_at": a.ETAAt.UTC().Format(time.RFC3339), "state": string(a.State)})
		}
	}
	if n, err := h.Store.PruneLiveEvidence(ctx, store.LiveEvidenceKept); err != nil {
		slog.Error("housekeeping: prune live evidence", "err", err)
	} else if n > 0 {
		slog.Info("housekeeping: pruned live evidence", "rows", n)
	}
	if h.GitHub != nil {
		if n := h.GitHub.Cleanup(24 * time.Hour); n > 0 {
			slog.Info("housekeeping: removed clones", "n", n)
		}
	}
}

// PurchaseConfirmer credits pending purchases whose payment landed in the
// treasury even though the payer never posted the signature (closed tab,
// wallet crash). It scans the treasury's USDC account for recent
// signatures, examines each transaction once and confirms through the same
// store path the user-triggered confirm uses.
type PurchaseConfirmer struct {
	Store    *store.Store
	Chain    *chain.Client
	Notifier *notify.Notifier
	Treasury string
	USDCMint string
	Every    time.Duration

	mu       sync.Mutex
	examined map[string]time.Time
}

// Loop runs until ctx ends. It is a no-op without a treasury or a chain
// client.
func (c *PurchaseConfirmer) Loop(ctx context.Context) {
	if c.Treasury == "" || c.Chain == nil {
		return
	}
	every := c.Every
	if every == 0 {
		every = 30 * time.Second
	}
	for {
		if n, err := c.Once(ctx); err != nil && ctx.Err() == nil {
			slog.Error("purchase confirmer", "err", err)
		} else if n > 0 {
			slog.Info("purchase confirmer: confirmed", "n", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(every):
		}
	}
}

// Once runs one pass and returns how many purchases it confirmed.
func (c *PurchaseConfirmer) Once(ctx context.Context) (int, error) {
	pending, err := c.Store.ListPendingPurchases(ctx)
	if err != nil {
		return 0, err
	}
	if len(pending) == 0 {
		return 0, nil
	}
	ata, err := chain.AssociatedTokenAddress(c.Treasury, c.USDCMint)
	if err != nil {
		return 0, err
	}
	sigs, err := c.Chain.SignaturesFor(ctx, ata, 100)
	if err != nil {
		return 0, err
	}
	confirmed := 0
	for _, sig := range sigs {
		if sig.Err != nil || c.seen(sig.Signature) {
			continue
		}
		raw, err := c.Chain.GetTransaction(ctx, sig.Signature)
		if err != nil {
			if ctx.Err() != nil {
				return confirmed, err
			}
			continue // try again next pass
		}
		c.remember(sig.Signature)
		for _, p := range pending {
			slot, err := chain.CheckPayment(raw, chain.Payment{MemoNonce: p.MemoNonce, Mint: p.USDCMint, Treasury: p.Treasury, AmountBase: p.AmountUSDC})
			if err != nil {
				continue
			}
			if _, err := c.Store.ConfirmPurchase(ctx, p.ID, sig.Signature, slot); err != nil {
				if !errors.Is(err, store.ErrConflict) {
					slog.Error("purchase confirmer: confirm", "purchase", p.ID, "err", err)
				}
				continue
			}
			c.Notifier.Notify(ctx, p.UserID, "purchase_confirmed", map[string]any{"purchase_id": p.ID, "sku": string(p.SKU), "via": "treasury_scan"})
			confirmed++
			break
		}
	}
	return confirmed, nil
}

func (c *PurchaseConfirmer) seen(sig string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.examined[sig]
	return ok
}

func (c *PurchaseConfirmer) remember(sig string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.examined == nil {
		c.examined = map[string]time.Time{}
	}
	c.examined[sig] = time.Now()
	if len(c.examined) > 5000 {
		for k, t := range c.examined {
			if time.Since(t) > 24*time.Hour {
				delete(c.examined, k)
			}
		}
	}
}
