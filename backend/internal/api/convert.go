package api

import (
	"context"
	"github.com/forefy/vig/backend/internal/github"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/forefy/vig/backend/internal/core"
	"github.com/forefy/vig/backend/internal/store"
)

func toUser(u store.User) User {
	return User{ID: u.ID, DisplayName: u.DisplayName, AvatarURL: u.AvatarURL, Role: u.Role, CreatedAt: u.CreatedAt}
}

func toIdentity(i store.Identity) Identity {
	return Identity{ID: i.ID, Provider: i.Provider, ProviderID: i.ProviderID, Email: i.Email, Handle: i.Handle, CreatedAt: i.CreatedAt}
}

func toIdentities(in []store.Identity) []Identity {
	out := make([]Identity, 0, len(in))
	for _, i := range in {
		out = append(out, toIdentity(i))
	}
	return out
}

func toPlanState(p store.Plan, now time.Time) *PlanState {
	var last time.Time
	if p.LastRunAt != nil {
		last = *p.LastRunAt
	}
	return &PlanState{Kind: p.Kind, StartsAt: p.StartsAt, EndsAt: p.EndsAt, RunsPerDay: p.RunCap, NextSlotAt: core.NextSlot(last, now)}
}

func toSession(se store.Session, current string) Session {
	return Session{ID: se.ID, Current: se.ID == current, IP: se.IP, UserAgent: se.UA, TOTPAt: se.TOTPAt, ExpiresAt: se.ExpiresAt, CreatedAt: se.CreatedAt}
}

func toAPIKey(k store.APIKey) APIKey {
	return APIKey{ID: k.ID, Name: k.Name, Prefix: k.Prefix, LastUsedAt: k.LastUsedAt, CreatedAt: k.CreatedAt}
}

func toInstallation(i store.Installation) Installation {
	return Installation{ID: i.ID, InstallationID: i.InstallationID, AccountLogin: i.AccountLogin, AccountType: i.AccountType, Status: i.Status}
}

func toNotification(n store.Notification) Notification {
	return Notification{ID: n.ID, Kind: n.Kind, Payload: n.Payload, ReadAt: n.ReadAt, CreatedAt: n.CreatedAt}
}

func toLedgerRow(r store.LedgerRow) LedgerRow {
	return LedgerRow{ID: r.ID, LedgerEntry: core.LedgerEntry{Kind: r.Kind, Delta: r.Delta, BalanceAfter: r.BalanceAfter, RefKind: r.RefKind, RefID: r.RefID, Reason: r.Reason}, CreatedAt: r.CreatedAt}
}

func toProgram(p store.Program) Program {
	return Program{ID: p.ProgramID, Chain: p.Chain, Loader: p.Loader, UpgradeAuthority: p.UpgradeAuthority, LastDeploySlot: p.LastDeploySlot, BytecodeSHA256: p.BytecodeSHA256,
		IDLPresent: p.IDLPresent, AnchorVersion: p.AnchorVersion, VerifiedBuild: p.VerifiedBuild, VerifiedCommit: p.VerifiedCommit,
		OwnershipProven: p.OwnershipProvenAt != nil, Panel: p.Panel}
}

func (d *Deps) toTarget(ctx context.Context, t store.Target) (Target, error) {
	programs, err := d.Store.ListPrograms(ctx, t.ID)
	if err != nil {
		return Target{}, err
	}
	out := Target{ID: t.ID, Adapter: t.Adapter, BaseURL: t.BaseURL, SourceKind: t.SourceKind, Repo: t.Repo, Path: t.Subpath, InstallationID: t.InstallationID,
		Revision: Revision{Selector: t.Selector, Value: t.Revision, Commit: t.CommitSHA, Author: t.CommitAuthor, Date: t.CommitDate}, Programs: []Program{}, CreatedAt: t.CreatedAt}
	if d.isSite(t) {
		site := &TargetSite{Origin: t.BaseURL, Scope: t.Scope.Normalize(originHost(t.BaseURL)), OwnershipProven: t.OwnershipProvenAt != nil, OwnershipMethod: t.OwnershipMethod, OwnershipProvenAt: t.OwnershipProvenAt}
		if t.Panel != nil {
			site.Panel = *t.Panel
		} else {
			site.Panel = core.VerificationPanel{Subject: t.BaseURL, Rows: []core.PanelRow{}}
		}
		out.Site = site
		// A site has no checkout to read declared ids from.
		out.Scanned = true
		out.Declared = []DeclaredID{}
	} else {
		out.Declared, out.Scanned = d.declaredIDs(ctx, t)
	}
	for _, p := range programs {
		out.Programs = append(out.Programs, toProgram(p))
	}
	return out, nil
}

func toAudit(a store.Audit) Audit {
	return Audit{ID: a.ID, TargetID: a.TargetID, Repo: a.Repo, Adapter: a.Adapter, Coverage: a.Coverage, BaseSHA: a.BaseSHA, HeadSHA: a.HeadSHA, ProgramIDs: a.ProgramIDs,
		Scope: Scope{Files: a.Scope.Files, Lines: a.Scope.Lines}, State: a.State, ResumeState: a.ResumeState, ETAAt: a.ETAAt, DelayedReason: a.DelayedReason,
		FailedReason: a.FailedReason, CostKind: a.CostKind, ReportID: a.ReportID, CreatedAt: a.CreatedAt, UpdatedAt: a.UpdatedAt, HiddenAt: a.HiddenAt, HiddenReason: a.HiddenReason}
}

func toEvents(in []store.AuditEvent) []AuditEvent {
	out := make([]AuditEvent, 0, len(in))
	for _, e := range in {
		out = append(out, AuditEvent{ID: e.ID, Kind: e.Kind, Actor: e.Actor, State: e.State, Payload: e.Payload, CreatedAt: e.CreatedAt})
	}
	return out
}

func (d *Deps) auditWithEvents(ctx context.Context, a store.Audit) (Audit, error) {
	out := toAudit(a)
	events, err := d.Store.ListEvents(ctx, a.ID)
	if err != nil {
		return out, err
	}
	out.Events = toEvents(events)
	return out, nil
}

func toReport(r store.Report, isOwner bool) Report {
	doc := r.Document().ForViewer(isOwner).WithoutProof()
	for i := range doc.Findings {
		doc.Findings[i].ID = r.Findings[i].ID
	}
	return Report{ID: r.ID, AuditID: r.AuditID, Adapter: r.Adapter, Document: doc, IsOwner: isOwner, PublishedSlug: r.PublishedSlug, PublishedAt: r.PublishedAt, CreatedAt: r.CreatedAt, Revision: r.Revision, Supersedes: r.Supersedes}
}

func toPurchase(p store.Purchase) Purchase {
	return Purchase{ID: p.ID, SKU: p.SKU, AmountUSDC: p.AmountUSDC, MemoNonce: p.MemoNonce, Treasury: p.Treasury, USDCMint: p.USDCMint, State: p.State,
		TxSignature: p.TxSignature, ConfirmedSlot: p.ConfirmedSlot, ExpiresAt: p.ExpiresAt, CreatedAt: p.CreatedAt}
}

func toClaim(c store.TweetClaim) TweetClaim {
	return TweetClaim{ID: c.ID, UserID: c.UserID, Nonce: c.Nonce, URL: c.URL, State: c.State, Reason: c.Reason, ReviewedAt: c.ReviewedAt, CreatedAt: c.CreatedAt}
}

func toLesson(l store.Lesson) Lesson {
	return Lesson{AuditID: l.AuditID, Repo: l.Repo, FindingID: l.FindingID, Ordinal: l.Ordinal, Title: l.Title, Severity: l.Severity, Kind: l.Kind, From: l.From, To: l.To, Reason: l.Reason, By: l.By, At: l.At}
}

func toAdminAction(a store.AdminAction) AdminAction {
	return AdminAction{ID: a.ID, AdminID: a.AdminID, Kind: a.Kind, SubjectKind: a.SubjectKind, SubjectID: a.SubjectID, Payload: a.Payload, CreatedAt: a.CreatedAt}
}

// declaredIDs reads the ids the source declares from the cached checkout
// and whether each holds an account on chain. The checkout is warmed in the
// background at target creation; before it lands the list is empty and
// scanned is false, so a client knows to ask again.
func (d *Deps) declaredIDs(ctx context.Context, t store.Target) ([]DeclaredID, bool) {
	out := []DeclaredID{}
	if d.GitHub == nil || t.CommitSHA == "" {
		return out, false
	}
	dir := d.GitHub.CloneDir(t.Repo, t.CommitSHA)
	if _, err := os.Stat(filepath.Join(dir, ".ok")); err != nil {
		return out, false
	}
	root := dir
	if t.Subpath != "" {
		root = filepath.Join(dir, filepath.FromSlash(t.Subpath))
	}
	ids := github.DeclaredIDs(root)
	keys := make([]string, 0, len(ids))
	for id := range ids {
		keys = append(keys, id)
	}
	sort.Strings(keys)
	live := d.liveness(ctx, keys)
	for _, id := range keys {
		out = append(out, DeclaredID{ID: id, File: ids[id], OnChain: live[id]})
	}
	return out, true
}

// livenessTTL is how long one answer about an address holding an account is
// reused; a program appearing or closing within it is a rare event.
const livenessTTL = 10 * time.Minute

type livenessEntry struct {
	state string
	at    time.Time
}

// liveness answers live/account/absent for each id with one RPC read for the
// misses, unknown when the chain is unreachable.
func (d *Deps) liveness(ctx context.Context, ids []string) map[string]string {
	out := map[string]string{}
	var miss []string
	now := time.Now()
	for _, id := range ids {
		if v, ok := d.liveCache.Load(id); ok && now.Sub(v.(livenessEntry).at) < livenessTTL {
			out[id] = v.(livenessEntry).state
			continue
		}
		out[id] = "unknown"
		miss = append(miss, id)
	}
	if len(miss) == 0 || d.Chain == nil {
		return out
	}
	for i := 0; i < len(miss); i += 100 {
		batch := miss[i:min(i+100, len(miss))]
		accts, err := d.Chain.GetMultipleAccounts(ctx, batch)
		if err != nil || len(accts) != len(batch) {
			continue
		}
		for j, a := range accts {
			state := "absent"
			switch {
			case a.Exists && a.Executable:
				state = "live"
			case a.Exists:
				state = "account"
			}
			out[batch[j]] = state
			d.liveCache.Store(batch[j], livenessEntry{state: state, at: now})
		}
	}
	return out
}
