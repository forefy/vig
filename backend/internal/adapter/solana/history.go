package solana

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/forefy/vig/backend/internal/chain"
	"github.com/forefy/vig/backend/internal/core"
)

// historyLimit is how many program-data signatures one listing reads: one
// call, and enough to tell "a few deploys" from "redeployed constantly".
const historyLimit = 100

// upgradeHistory is what the program-data account's signature list says.
type upgradeHistory struct {
	Upgrades int  // successful transactions, the initial deploy included
	Capped   bool // the list was full, so the count is a floor
	First    time.Time
	Last     time.Time
}

// parseHistory counts the successful transactions in a signature listing
// (newest first) and takes the oldest and newest block times it carries.
func parseHistory(sigs []chain.Signature, limit int) upgradeHistory {
	h := upgradeHistory{Capped: limit > 0 && len(sigs) >= limit}
	for _, sig := range sigs {
		if sig.Err != nil {
			continue
		}
		h.Upgrades++
		if sig.BlockTime == nil {
			continue
		}
		t := time.Unix(*sig.BlockTime, 0).UTC()
		if h.First.IsZero() || t.Before(h.First) {
			h.First = t
		}
		if h.Last.IsZero() || t.After(h.Last) {
			h.Last = t
		}
	}
	return h
}

// historyFacts turns program facts and, when the loader keeps one, the
// deploy history into node facts. h nil means the history was not read.
func historyFacts(pf chain.ProgramFacts, h *upgradeHistory) map[string]string {
	if !pf.Exists {
		return nil
	}
	f := map[string]string{"loader": pf.Loader}
	switch {
	case pf.Loader == "upgradeable" && pf.UpgradeAuthority == "", pf.Loader == "non-upgradeable":
		f["immutable"] = "true"
	}
	if pf.UpgradeAuthority != "" {
		f["authority"] = pf.UpgradeAuthority
	}
	if pf.LastDeploySlot > 0 {
		f["last_deploy_slot"] = strconv.FormatUint(pf.LastDeploySlot, 10)
	}
	if pf.BytecodeSHA256 != "" {
		f["bytecode_now"] = pf.BytecodeSHA256
	}
	if h != nil && h.Upgrades > 0 {
		n := strconv.Itoa(h.Upgrades)
		if h.Capped {
			n += "+"
		}
		f["upgrades"] = n
		if !h.First.IsZero() {
			f["first_deploy"] = h.First.Format(time.RFC3339)
		}
		if !h.Last.IsZero() {
			f["last_upgrade"] = h.Last.Format(time.RFC3339)
		}
	}
	return f
}

// history reads a subject program's loader facts and, for an upgradeable
// program, one listing of its program-data account. Any RPC failure
// leaves the facts absent: unknown, not wrong.
func (b *SurfaceBuilder) history(ctx context.Context, pid string, budget core.SurfaceBudget) map[string]string {
	pf, err := b.Chain.Program(ctx, pid)
	if err != nil || !pf.Exists {
		slog.Warn("surface: program facts", "program", pid, "exists", pf.Exists, "err", err)
		return nil
	}
	var h *upgradeHistory
	pdAddr, err := b.Chain.ProgramDataAddress(ctx, pid)
	if err != nil && pf.Loader == "upgradeable" {
		slog.Warn("surface: program data", "program", pid, "err", err)
	}
	if err == nil {
		if !pace(ctx, budget) {
			return historyFacts(pf, nil)
		}
		sigs, err := b.Chain.SignaturesFor(ctx, pdAddr, historyLimit)
		if err != nil || len(sigs) == 0 {
			// A program-data account has at least its deploy, so an empty
			// list is a fallback node with short history answering while
			// the primary rests after a subject's sampling burst. One retry
			// after a few seconds reaches the primary again.
			for i := 0; i < 10 && pace(ctx, budget); i++ {
			}
			b.Chain.Forget(fmt.Sprintf("sigs:%s:%d", pdAddr, historyLimit))
			sigs, err = b.Chain.SignaturesFor(ctx, pdAddr, historyLimit)
		}
		switch {
		case err != nil:
			slog.Warn("surface: upgrade history", "program", pid, "err", err)
		case len(sigs) == 0:
			slog.Warn("surface: upgrade history", "program", pid, "pd", pdAddr, "listed", 0)
		default:
			parsed := parseHistory(sigs, historyLimit)
			h = &parsed
		}
	}
	return historyFacts(pf, h)
}

// pace waits one budget interval; false when the context ended first.
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

// setFacts writes facts onto a node the map already holds, replacing any
// it had under the same keys. Chain facts read now beat whatever an
// earlier source guessed.
func setFacts(s *core.Surface, id string, facts map[string]string) {
	if len(facts) == 0 {
		return
	}
	for i := range s.Nodes {
		if s.Nodes[i].ID != id {
			continue
		}
		if s.Nodes[i].Facts == nil {
			s.Nodes[i].Facts = map[string]string{}
		}
		for k, v := range facts {
			s.Nodes[i].Facts[k] = v
		}
		return
	}
}
