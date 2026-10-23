package store_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/forefy/vig/backend/internal/core"
	"github.com/forefy/vig/backend/internal/db/testdb"
	"github.com/forefy/vig/backend/internal/store"
)

func TestMain(m *testing.M) {
	code := m.Run()
	testdb.Stop()
	os.Exit(code)
}

type fixture struct {
	t      *testing.T
	ctx    context.Context
	s      *store.Store
	user   store.User
	target store.Target
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	ctx := context.Background()
	s := testdb.New(t)
	u, err := s.CreateUser(ctx, s.Pool, "alice", "")
	if err != nil {
		t.Fatal(err)
	}
	tg, err := s.CreateTarget(ctx, store.Target{Adapter: "solana", OwnerUserID: u.ID, SourceKind: "public", Repo: "acme/vault", Selector: "branch", Revision: "main", CommitSHA: "aaaa1111"})
	if err != nil {
		t.Fatal(err)
	}
	return &fixture{t: t, ctx: ctx, s: s, user: u, target: tg}
}

func (f *fixture) grant(n int) {
	f.t.Helper()
	if _, err := f.s.AppendLedgerLocked(f.ctx, store.LedgerRow{UserID: f.user.ID, Kind: core.LedgerGrant, Delta: n, Reason: "test"}); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) balance() int {
	f.t.Helper()
	n, err := f.s.Balance(f.ctx, f.s.Pool, f.user.ID)
	if err != nil {
		f.t.Fatal(err)
	}
	return n
}

func (f *fixture) audit(head string, programs ...string) store.Audit {
	return store.Audit{UserID: f.user.ID, TargetID: f.target.ID, Coverage: "full", HeadSHA: head, ProgramIDs: programs, Scope: store.Scope{Files: []string{"src/lib.rs"}, Lines: 10}}
}

func TestQueueAuditDedupIsAtomicAndFree(t *testing.T) {
	f := newFixture(t)
	f.grant(2)
	a, created, err := f.s.QueueAudit(f.ctx, f.audit("aaaa1111", "P1"))
	if err != nil || !created {
		t.Fatalf("first queue: created=%v err=%v", created, err)
	}
	if f.balance() != 1 || a.CostKind != "credit" {
		t.Fatalf("credit not consumed: balance=%d cost=%s", f.balance(), a.CostKind)
	}
	// Same user, head, base and program set (order-insensitive): no charge.
	b, created, err := f.s.QueueAudit(f.ctx, f.audit("aaaa1111", "P1"))
	if err != nil || created || b.ID != a.ID {
		t.Fatalf("duplicate: created=%v id=%s want %s err=%v", created, b.ID, a.ID, err)
	}
	if f.balance() != 1 {
		t.Fatalf("duplicate charged: balance=%d", f.balance())
	}
	// A failed audit no longer blocks a re-run, and the credit comes back.
	if _, err := f.s.FailAudit(f.ctx, a.ID, "boom", "operator", ""); err != nil {
		t.Fatal(err)
	}
	if f.balance() != 2 {
		t.Fatalf("refund: balance=%d", f.balance())
	}
	c, created, err := f.s.QueueAudit(f.ctx, f.audit("aaaa1111", "P1"))
	if err != nil || !created || c.ID == a.ID {
		t.Fatalf("after failure: created=%v err=%v", created, err)
	}
}

func TestQueueAuditSlotBeforeCredit(t *testing.T) {
	f := newFixture(t)
	f.grant(1)
	now := time.Now()
	if _, err := f.s.CreatePlan(f.ctx, f.s.Pool, store.Plan{UserID: f.user.ID, Kind: "pro", StartsAt: now.Add(-time.Hour), EndsAt: now.Add(24 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	a, _, err := f.s.QueueAudit(f.ctx, f.audit("bbbb2222", "P1"))
	if err != nil {
		t.Fatal(err)
	}
	if a.CostKind != "slot" || f.balance() != 1 {
		t.Fatalf("want slot paid, got cost=%s balance=%d", a.CostKind, f.balance())
	}
	// Slot used today: the credit pays the next one.
	b, _, err := f.s.QueueAudit(f.ctx, f.audit("cccc3333", "P1"))
	if err != nil {
		t.Fatal(err)
	}
	if b.CostKind != "credit" || f.balance() != 0 {
		t.Fatalf("want credit paid, got cost=%s balance=%d", b.CostKind, f.balance())
	}
	// Nothing left: the answer names the next slot, not a missing credit.
	_, _, err = f.s.QueueAudit(f.ctx, f.audit("dddd4444", "P1"))
	var noSlot store.ErrNoSlot
	if !errors.As(err, &noSlot) {
		t.Fatalf("want ErrNoSlot, got %v", err)
	}
}

func TestCountActiveAuditsForClone(t *testing.T) {
	f := newFixture(t)
	f.grant(3)
	a, _, err := f.s.QueueAudit(f.ctx, f.audit("aaaa1111", "P1"))
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := f.s.QueueAudit(f.ctx, f.audit("aaaa1111", "P2"))
	if err != nil {
		t.Fatal(err)
	}
	n, err := f.s.CountActiveAuditsForClone(f.ctx, "acme/vault", "aaaa1111", a.ID)
	if err != nil || n != 1 {
		t.Fatalf("want 1 other live audit on the clone, got %d err=%v", n, err)
	}
	if _, err := f.s.FailAudit(f.ctx, b.ID, "x", "operator", ""); err != nil {
		t.Fatal(err)
	}
	n, err = f.s.CountActiveAuditsForClone(f.ctx, "acme/vault", "aaaa1111", a.ID)
	if err != nil || n != 0 {
		t.Fatalf("want 0 after the other failed, got %d err=%v", n, err)
	}
}

func TestCreateScanLimited(t *testing.T) {
	f := newFixture(t)
	ip := []byte("0123456789abcdef0123456789abcdef")
	since := time.Now().Add(-24 * time.Hour)
	for i := 0; i < 2; i++ {
		_, remaining, err := f.s.CreateScanLimited(f.ctx, store.Scan{Token: "tok" + string(rune('a'+i)), Repo: "acme/vault", CommitSHA: "aaaa1111", IPHash: ip, ExpiresAt: time.Now().Add(time.Hour)}, since, 2)
		if err != nil {
			t.Fatal(err)
		}
		if remaining != 1-i {
			t.Fatalf("remaining after %d: %d", i+1, remaining)
		}
	}
	_, _, err := f.s.CreateScanLimited(f.ctx, store.Scan{Token: "tokz", Repo: "acme/vault", CommitSHA: "aaaa1111", IPHash: ip, ExpiresAt: time.Now().Add(time.Hour)}, since, 2)
	if !errors.Is(err, store.ErrRateLimited) {
		t.Fatalf("want ErrRateLimited, got %v", err)
	}
	if _, err := f.s.GetScanByToken(f.ctx, "tokz"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("refused scan was inserted: %v", err)
	}
}

func TestRecoveryCodesConsumeOnce(t *testing.T) {
	f := newFixture(t)
	h1, h2 := []byte("hash-one"), []byte("hash-two")
	if err := f.s.ReplaceRecoveryCodes(f.ctx, f.user.ID, [][]byte{h1, h2}); err != nil {
		t.Fatal(err)
	}
	if n, _ := f.s.CountRecoveryCodes(f.ctx, f.user.ID); n != 2 {
		t.Fatalf("count %d", n)
	}
	ok, err := f.s.ConsumeRecoveryCode(f.ctx, f.user.ID, h1)
	if err != nil || !ok {
		t.Fatalf("first use: ok=%v err=%v", ok, err)
	}
	ok, err = f.s.ConsumeRecoveryCode(f.ctx, f.user.ID, h1)
	if err != nil || ok {
		t.Fatalf("second use must fail: ok=%v err=%v", ok, err)
	}
	if n, _ := f.s.CountRecoveryCodes(f.ctx, f.user.ID); n != 1 {
		t.Fatalf("count after use %d", n)
	}
	if err := f.s.ReplaceRecoveryCodes(f.ctx, f.user.ID, [][]byte{[]byte("fresh")}); err != nil {
		t.Fatal(err)
	}
	if ok, _ := f.s.ConsumeRecoveryCode(f.ctx, f.user.ID, h2); ok {
		t.Fatal("old code survived regeneration")
	}
}

func TestSetProgramMatchRegeneratesPanel(t *testing.T) {
	f := newFixture(t)
	f.s.PanelPatch = func(panel core.VerificationPanel, pin core.ProgramPin) core.VerificationPanel {
		for i := range panel.Rows {
			if panel.Rows[i].Key == "verified_build" {
				panel.Rows[i].Value = string(pin.Match)
			}
		}
		return panel
	}
	_, err := f.s.UpsertProgram(f.ctx, store.Program{TargetID: f.target.ID, ProgramID: "P1", UpgradeAuthority: "AUTH", VerifiedBuild: core.MatchUnverified,
		Panel: core.VerificationPanel{Subject: "P1", Rows: []core.PanelRow{{Key: "verified_build", Value: "unverified"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.s.SetProgramMatch(f.ctx, f.s.Pool, f.target.ID, core.ProgramPin{ID: "P1", Slot: 42, BytecodeSHA256: "deadbeef", Match: core.MatchVerified}); err != nil {
		t.Fatal(err)
	}
	p, err := f.s.GetProgram(f.ctx, f.target.ID, "P1")
	if err != nil {
		t.Fatal(err)
	}
	if p.VerifiedBuild != core.MatchVerified || p.LastDeploySlot != 42 || p.BytecodeSHA256 != "deadbeef" {
		t.Fatalf("columns not updated: %+v", p)
	}
	if len(p.Panel.Rows) != 1 || p.Panel.Rows[0].Value != "verified" {
		t.Fatalf("panel not regenerated: %+v", p.Panel)
	}
}

func TestUpsertProgramClearsOwnershipWhenAuthorityChanges(t *testing.T) {
	f := newFixture(t)
	if _, err := f.s.UpsertProgram(f.ctx, store.Program{TargetID: f.target.ID, ProgramID: "P1", UpgradeAuthority: "AUTH1"}); err != nil {
		t.Fatal(err)
	}
	if err := f.s.SetOwnershipProven(f.ctx, f.target.ID, "P1", "sig", core.VerificationPanel{}); err != nil {
		t.Fatal(err)
	}
	p, _ := f.s.UpsertProgram(f.ctx, store.Program{TargetID: f.target.ID, ProgramID: "P1", UpgradeAuthority: "AUTH1"})
	if p.OwnershipProvenAt == nil || p.OwnershipSignature != "sig" {
		t.Fatal("same authority must keep the proof")
	}
	p, _ = f.s.UpsertProgram(f.ctx, store.Program{TargetID: f.target.ID, ProgramID: "P1", UpgradeAuthority: "AUTH2"})
	if p.OwnershipProvenAt != nil || p.OwnershipSignature != "" {
		t.Fatal("changed authority must clear the proof")
	}
}

func TestAdminCreditIsAtomic(t *testing.T) {
	f := newFixture(t)
	admin, err := f.s.CreateUser(f.ctx, f.s.Pool, "root", "")
	if err != nil {
		t.Fatal(err)
	}
	row, action, err := f.s.AdminCredit(f.ctx, admin.ID, f.user.ID, core.LedgerGrant, 2, "welcome")
	if err != nil {
		t.Fatal(err)
	}
	if row.BalanceAfter != 2 || action.Payload["ledger_id"] != row.ID {
		t.Fatalf("grant: row=%+v action=%+v", row, action)
	}
	// Revoking more than the balance is refused and must leave no action.
	if _, _, err := f.s.AdminCredit(f.ctx, admin.ID, f.user.ID, core.LedgerRevoke, -5, "oops"); err == nil {
		t.Fatal("over-revoke succeeded")
	}
	actions, err := f.s.ListAdminActions(f.ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(actions) != 1 {
		t.Fatalf("want 1 action, got %d", len(actions))
	}
	if f.balance() != 2 {
		t.Fatalf("balance %d", f.balance())
	}
}

func TestSetRoleRevokesSessions(t *testing.T) {
	f := newFixture(t)
	se, err := f.s.CreateSession(f.ctx, f.user.ID, time.Hour, "127.0.0.1", "test", []byte("refresh-hash"))
	if err != nil {
		t.Fatal(err)
	}
	if _, live := f.s.SessionLive(f.ctx, se.ID); !live {
		t.Fatal("session should be live")
	}
	if err := f.s.SetRole(f.ctx, f.user.ID, "admin"); err != nil {
		t.Fatal(err)
	}
	if _, live := f.s.SessionLive(f.ctx, se.ID); live {
		t.Fatal("session survived a role change")
	}
}

func TestDeleteAccountKeepsLedgerAndAudits(t *testing.T) {
	f := newFixture(t)
	f.grant(1)
	if _, err := f.s.AddIdentity(f.ctx, f.s.Pool, f.user.ID, "github", "gh-1", "a@example.com", "alice"); err != nil {
		t.Fatal(err)
	}
	a, _, err := f.s.QueueAudit(f.ctx, f.audit("aaaa1111", "P1"))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.s.DeleteAccount(f.ctx, f.user.ID); err != nil {
		t.Fatal(err)
	}
	u, err := f.s.GetUser(f.ctx, f.user.ID)
	if err != nil || u.DeletedAt == nil || u.DisplayName != "" {
		t.Fatalf("user not anonymised: %+v err=%v", u, err)
	}
	if ids, _ := f.s.ListIdentities(f.ctx, f.user.ID); len(ids) != 0 {
		t.Fatalf("identities remain: %d", len(ids))
	}
	if _, err := f.s.GetAudit(f.ctx, a.ID); err != nil {
		t.Fatalf("audit gone: %v", err)
	}
	if rows, _ := f.s.ListLedger(f.ctx, f.user.ID); len(rows) != 2 {
		t.Fatalf("ledger rows %d", len(rows))
	}
}

func TestOverdueFiresOncePerETA(t *testing.T) {
	f := newFixture(t)
	f.grant(1)
	a, _, err := f.s.QueueAudit(f.ctx, f.audit("aaaa1111", "P1"))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.s.BackdateAudit(f.ctx, a.ID, 48*time.Hour); err != nil {
		t.Fatal(err)
	}
	overdue, err := f.s.ListOverdue(f.ctx, time.Hour)
	if err != nil || len(overdue) != 1 {
		t.Fatalf("want 1 overdue, got %d err=%v", len(overdue), err)
	}
	if err := f.s.MarkOverdue(f.ctx, overdue[0]); err != nil {
		t.Fatal(err)
	}
	overdue, err = f.s.ListOverdue(f.ctx, time.Hour)
	if err != nil || len(overdue) != 0 {
		t.Fatalf("overdue fired twice: %d err=%v", len(overdue), err)
	}
}

// A shared email address does not join two accounts. Whoever proves an
// address at the weakest provider we ever support would otherwise reach every
// account carrying it, and addresses change hands.
func TestSignInDoesNotLinkAccountsByEmail(t *testing.T) {
	f := newFixture(t)
	shared := "alice@example.com"

	first, created, err := f.s.FindOrCreateUser(f.ctx, "google", "google-1", shared, "alice", "Alice", "")
	if err != nil || !created {
		t.Fatalf("first sign-in should create a user: created=%v err=%v", created, err)
	}
	second, created, err := f.s.FindOrCreateUser(f.ctx, "github", "github-1", shared, "alice", "Alice", "")
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("a second provider on the same address must not resolve to the existing account")
	}
	if second.ID == first.ID {
		t.Fatal("accounts were merged on a shared email")
	}

	// The same identity still returns the same account, which is the only
	// thing sign-in is allowed to match on.
	again, created, err := f.s.FindOrCreateUser(f.ctx, "google", "google-1", shared, "alice", "Alice", "")
	if err != nil || created || again.ID != first.ID {
		t.Fatalf("a known identity must return its own account: id=%s created=%v err=%v", again.ID, created, err)
	}
}

// A renewed token continues a claimed job under a fresh credential: the old
// token stops resolving, the new one is already bound and cannot claim, and
// the customer's timeline records the swap. Renewal never applies to a job
// nobody has claimed.
func TestRenewOperatorTokenReplacesClaimToken(t *testing.T) {
	f := newFixture(t)
	f.grant(1)
	a, _, err := f.s.QueueAudit(f.ctx, f.audit("aaaa1111", "P1"))
	if err != nil {
		t.Fatal(err)
	}
	exp := time.Now().Add(time.Hour)
	first, err := f.s.CreateOperatorToken(f.ctx, a.ID, []byte("h1"), exp, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.RenewOperatorToken(f.ctx, a.ID, []byte("h2"), exp, ""); err == nil {
		t.Fatal("renewed a token on a queued audit")
	}
	if _, err := f.s.ClaimJob(f.ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	renewed, err := f.s.RenewOperatorToken(f.ctx, a.ID, []byte("h2"), exp, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.OperatorTokenByHash(f.ctx, []byte("h1")); err == nil {
		t.Fatal("old token still resolves")
	}
	got, err := f.s.OperatorTokenByHash(f.ctx, []byte("h2"))
	if err != nil || got.AuditID != a.ID || got.UsedAt == nil {
		t.Fatalf("renewed token: %+v err=%v", got, err)
	}
	if _, err := f.s.ClaimJob(f.ctx, renewed.ID); err == nil {
		t.Fatal("renewed token claimed a job")
	}
	evs, err := f.s.ListEvents(f.ctx, a.ID)
	if err != nil || len(evs) == 0 {
		t.Fatalf("events: %v", err)
	}
	if last := evs[len(evs)-1]; last.Kind != "token_renewed" || last.State != "claimed" {
		t.Fatalf("last event = %s/%s, want token_renewed/claimed", last.Kind, last.State)
	}
}

// The surface map: claimed once per audit, saved, evidence deduplicated by
// reference, attention derived on read through the API layer.
func TestSurfaceClaimSaveAndEvidenceDedup(t *testing.T) {
	f := newFixture(t)
	f.grant(1)
	a, _, err := f.s.QueueAudit(f.ctx, f.audit("aaaa1111", "P1"))
	if err != nil {
		t.Fatal(err)
	}
	claimed, ok, err := f.s.ClaimSurfaceWork(f.ctx)
	if err != nil || !ok || claimed.ID != a.ID {
		t.Fatalf("claim: ok=%v id=%s err=%v", ok, claimed.ID, err)
	}
	if st, _, _ := f.s.SurfaceStatus(f.ctx, a.ID); st != store.SurfacePending {
		t.Fatalf("status before save = %s", st)
	}
	doc := core.Surface{Adapter: "solana", BuiltAt: time.Now().UTC(), Nodes: []core.SurfaceNode{{ID: "p:P1", Kind: "program", Name: "P1"}, {ID: "ix:P1:deposit", Parent: "p:P1", Kind: "instruction", Name: "deposit"}}}
	if err := f.s.SaveSurface(f.ctx, a.ID, doc); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := f.s.ClaimSurfaceWork(f.ctx); ok {
		t.Fatal("a mapped audit was claimed again")
	}
	if st, _, _ := f.s.SurfaceStatus(f.ctx, a.ID); st != store.SurfaceReady {
		t.Fatalf("status after save = %s", st)
	}
	n, err := f.s.AddSurfaceEvidence(f.ctx, a.ID, []core.SurfaceEvidence{
		{NodeID: "ix:P1:deposit", Kind: "tx", Ref: "sig1"},
		{NodeID: "ix:P1:deposit", Kind: "tx", Ref: "sig1"},
		{NodeID: "ix:P1:deposit", Kind: "review"},
		{NodeID: "ix:P1:deposit", Kind: "review"},
	})
	if err != nil || n != 3 {
		t.Fatalf("added %d err=%v (want 3: one signature deduplicated, reviews kept)", n, err)
	}
	got, err := f.s.GetSurface(f.ctx, a.ID)
	if err != nil || len(got.Nodes) != 2 {
		t.Fatalf("get: %v %v", got, err)
	}
	evs, err := f.s.ListSurfaceEvidence(f.ctx, a.ID, 10)
	if err != nil || len(evs) != 3 {
		t.Fatalf("list: %d %v", len(evs), err)
	}
	// A rebuilt sample refreshes its own row and adds nothing.
	n, err = f.s.AddSurfaceEvidence(f.ctx, a.ID, []core.SurfaceEvidence{{NodeID: "ix:P1:deposit", Kind: "tx", Ref: "sig1", Summary: map[string]any{"units": 7}}})
	if err != nil || n != 0 {
		t.Fatalf("resample added %d err=%v", n, err)
	}
	evs, _ = f.s.ListSurfaceEvidence(f.ctx, a.ID, 10)
	refreshed := false
	for _, e := range evs {
		if e.Ref == "sig1" && e.Summary["units"] == float64(7) {
			refreshed = true
		}
	}
	if len(evs) != 3 || !refreshed {
		t.Fatalf("resample: %d rows, refreshed=%v", len(evs), refreshed)
	}
}

// A cancel from delivered refunds the credit once, takes the public report
// private, hides the audit from its owner, and a revision keeps statuses.
func TestAdminCancelHideAndRevise(t *testing.T) {
	f := newFixture(t)
	f.grant(1)
	a, _, err := f.s.QueueAudit(f.ctx, f.audit("aaaa1111", "P1"))
	if err != nil {
		t.Fatal(err)
	}
	for _, st := range []core.AuditState{core.StateClaimed, core.StateCloning, core.StateProgramsVerified, core.StateAnalysis, core.StateHumanReview} {
		if _, err := f.s.Advance(f.ctx, a.ID, st, "operator", "", nil); err != nil {
			t.Fatalf("advance to %s: %v", st, err)
		}
	}
	doc := core.FindingsDocument{
		Report: core.ReportHeader{AuditID: a.ID, Pin: core.Pin{Repo: f.target.Repo, Commit: "aaaa1111", Programs: []core.ProgramPin{{ID: "P1", Slot: 1, BytecodeSHA256: "abc", Match: core.MatchVerified}}, Excluded: []string{"audits/", "tests/"}},
			Known: []core.Known{{Title: "No order expiry", Severity: core.SeverityLow, Source: "Cyfrin 2026-03", Status: core.KnownAcknowledged, Reference: "audits/cyfrin.pdf"}}},
		Findings: []core.Finding{
			{Ordinal: 1, Severity: core.SeverityHigh, Title: "Missing signer", Location: core.Location{File: "src/lib.rs", LineStart: 4}, Description: "d", Fix: "- f", FixKind: core.FixOperational, Snippet: &core.Snippet{Code: "x"}},
			{Ordinal: 2, Severity: core.SeverityLow, Title: "Unused account", Location: core.Location{File: "src/lib.rs", LineStart: 9}, Description: "d", Fix: "- f", FixKind: core.FixOperational},
		},
	}
	r1, err := f.s.StoreReport(f.ctx, a.ID, doc)
	if err != nil {
		t.Fatal(err)
	}
	if len(r1.Known) != 1 || r1.Known[0].Status != core.KnownAcknowledged || len(r1.Pin.Excluded) != 2 || r1.Document().Report.Known[0].Source != "Cyfrin 2026-03" {
		t.Fatalf("known and excluded not stored: %+v %+v", r1.Known, r1.Pin.Excluded)
	}
	if _, err := f.s.Advance(f.ctx, a.ID, core.StateDelivered, "operator", "", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.SetFindingStatus(f.ctx, r1.ID, r1.Findings[0].ID, core.FindingAcknowledged, "", "owner", f.user.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.s.Publish(f.ctx, r1.ID, "acme-vault"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.ReviseReport(f.ctx, "not-an-id", doc, f.user.ID); err == nil {
		t.Fatal("revising a missing audit succeeded")
	}
	// Revision: one finding kept by title and file (status and snippet follow),
	// one removed, one added; the audit and the slug move to the new version.
	rev := doc
	rev.Findings = []core.Finding{
		{Ordinal: 1, Severity: core.SeverityMedium, Title: "Missing signer", Location: core.Location{File: "src/lib.rs", LineStart: 5}, Description: "d2", Fix: "- f2", FixKind: core.FixOperational},
		{Ordinal: 2, Severity: core.SeverityInfo, Title: "Closed orders kept", Location: core.Location{File: "src/lib.rs", LineStart: 20}, Description: "d", Fix: "- f", FixKind: core.FixOperational},
	}
	out, err := f.s.ReviseReport(f.ctx, a.ID, rev, f.user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if out.Added != 1 || out.Removed != 1 || out.Kept != 1 || out.Report.Revision != 2 || out.Report.Supersedes != r1.ID {
		t.Fatalf("revision: %+v", out)
	}
	if out.Report.PublishedSlug != "acme-vault" || out.Report.PublishedAt == nil {
		t.Fatalf("slug did not move: %+v", out.Report)
	}
	if len(out.Report.Known) != 1 || len(out.Report.Pin.Excluded) != 2 {
		t.Fatalf("revision dropped known or excluded: %+v", out.Report)
	}
	kept := out.Report.Findings[0]
	if kept.Status != core.FindingAcknowledged || kept.CarriedFromID != "" || kept.Snippet == nil || kept.Snippet.Code != "x" || kept.Severity != core.SeverityMedium {
		t.Fatalf("kept finding: %+v", kept)
	}
	cur, err := f.s.GetReportByAudit(f.ctx, a.ID)
	if err != nil || cur.ID != out.Report.ID {
		t.Fatalf("by audit: %v %v", cur.ID, err)
	}
	if _, err := f.s.GetReportBySlug(f.ctx, "acme-vault"); err != nil {
		t.Fatalf("slug: %v", err)
	}
	old, _ := f.s.GetReport(f.ctx, r1.ID)
	if old.PublishedSlug != "" {
		t.Fatal("old version kept the slug")
	}
	// Cancel from delivered: refund, private report, hidden from the owner.
	if f.balance() != 0 {
		t.Fatalf("balance before cancel %d", f.balance())
	}
	c, err := f.s.CancelAudit(f.ctx, a.ID, "wrong target", f.user.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if c.State != core.StateCancelled || c.HiddenAt == nil || f.balance() != 1 {
		t.Fatalf("cancel: state=%s hidden=%v balance=%d", c.State, c.HiddenAt, f.balance())
	}
	if _, err := f.s.GetReportBySlug(f.ctx, "acme-vault"); err == nil {
		t.Fatal("cancelled audit's report is still public")
	}
	if _, err := f.s.CancelAudit(f.ctx, a.ID, "again", f.user.ID, false); err == nil || f.balance() != 1 {
		t.Fatalf("second cancel: err=%v balance=%d", err, f.balance())
	}
	mine, _ := f.s.ListAudits(f.ctx, f.user.ID)
	if len(mine) != 0 {
		t.Fatalf("owner still lists %d audits", len(mine))
	}
	all, _ := f.s.ListAllAudits(f.ctx, store.AuditFilter{State: "cancelled"})
	if len(all) != 1 {
		t.Fatalf("admin list: %d", len(all))
	}
	shown := false
	all, _ = f.s.ListAllAudits(f.ctx, store.AuditFilter{Hidden: &shown})
	if len(all) != 0 {
		t.Fatalf("visible filter: %d", len(all))
	}
	u, err := f.s.SetAuditHidden(f.ctx, a.ID, false, "restored", f.user.ID)
	if err != nil || u.HiddenAt != nil {
		t.Fatalf("unhide: %v %v", u.HiddenAt, err)
	}
	mine, _ = f.s.ListAudits(f.ctx, f.user.ID)
	if len(mine) != 1 {
		t.Fatalf("owner lists %d after unhide", len(mine))
	}
	// The same target can be queued again after a cancel.
	f.grant(1)
	if _, created, err := f.s.QueueAudit(f.ctx, f.audit("aaaa1111", "P1")); err != nil || !created {
		t.Fatalf("requeue after cancel: created=%v err=%v", created, err)
	}
	evs, _ := f.s.ListEvents(f.ctx, a.ID)
	kinds := map[string]int{}
	for _, e := range evs {
		kinds[e.Kind]++
	}
	if kinds["report_revised"] != 1 || kinds["cancelled"] != 1 || kinds["hidden"] != 1 || kinds["unhidden"] != 1 {
		t.Fatalf("events: %v", kinds)
	}
}

// Sibling deployments are the other targets on the same repository, each
// program once, never the target's own.
func TestListRepoPrograms(t *testing.T) {
	f := newFixture(t)
	other, err := f.s.CreateTarget(f.ctx, store.Target{Adapter: "ethereum", OwnerUserID: f.user.ID, SourceKind: "public", Repo: f.target.Repo, Selector: "commit", Revision: "bbbb2222", CommitSHA: "bbbb2222"})
	if err != nil {
		t.Fatal(err)
	}
	elsewhere, err := f.s.CreateTarget(f.ctx, store.Target{Adapter: "ethereum", OwnerUserID: f.user.ID, SourceKind: "public", Repo: "acme/other", Selector: "commit", Revision: "cccc3333", CommitSHA: "cccc3333"})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []store.Program{{TargetID: f.target.ID, ProgramID: "P1"}, {TargetID: other.ID, ProgramID: "0xabc", Chain: "1"}, {TargetID: elsewhere.ID, ProgramID: "0xdef", Chain: "1"}} {
		if _, err := f.s.UpsertProgram(f.ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	sibs, err := f.s.ListRepoPrograms(f.ctx, f.target.Repo, f.target.ID)
	if err != nil || len(sibs) != 1 || sibs[0].ProgramID != "0xabc" || sibs[0].Chain != "1" {
		t.Fatalf("siblings: %+v %v", sibs, err)
	}
	if none, _ := f.s.ListRepoPrograms(f.ctx, "", f.target.ID); len(none) != 0 {
		t.Fatal("empty repo matched")
	}
}

// An admin puts an audit anywhere on the pipeline from anywhere: a jump
// forward, a return from cancelled that pays again, delivered only with a
// report. A delete takes the row and its events, refunds a held cost and
// leaves the ledger alone.
func TestAdminSetStateAndDelete(t *testing.T) {
	f := newFixture(t)
	f.grant(1)
	a, _, err := f.s.QueueAudit(f.ctx, f.audit("bbbb2222", "P1"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.SetAuditState(f.ctx, a.ID, core.StateDelayed, "no", f.user.ID); err == nil {
		t.Fatal("delayed is not a pipeline state")
	}
	if _, err := f.s.SetAuditState(f.ctx, a.ID, core.StateDelivered, "no report", f.user.ID); err == nil {
		t.Fatal("delivered without a report")
	}
	s1, err := f.s.SetAuditState(f.ctx, a.ID, core.StateAnalysis, "skip ahead", f.user.ID)
	if err != nil || s1.State != core.StateAnalysis || s1.ClaimedAt != nil {
		t.Fatalf("jump: %+v %v", s1.State, err)
	}
	if _, err := f.s.SetAuditState(f.ctx, a.ID, core.StateAnalysis, "again", f.user.ID); err == nil {
		t.Fatal("same state accepted")
	}
	// Cancel refunds; reopening with no credit is refused, with one it pays.
	if _, err := f.s.CancelAudit(f.ctx, a.ID, "stop", f.user.ID, false); err != nil || f.balance() != 1 {
		t.Fatalf("cancel: %v balance=%d", err, f.balance())
	}
	if _, err := f.s.AppendLedgerLocked(f.ctx, store.LedgerRow{UserID: f.user.ID, Kind: core.LedgerRevoke, Delta: -1, Reason: "test"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.SetAuditState(f.ctx, a.ID, core.StateHumanReview, "reopen", f.user.ID); err == nil {
		t.Fatal("reopened with no credit")
	}
	f.grant(1)
	s2, err := f.s.SetAuditState(f.ctx, a.ID, core.StateHumanReview, "reopen", f.user.ID)
	if err != nil || s2.State != core.StateHumanReview || f.balance() != 0 || s2.LedgerEntryID == a.LedgerEntryID || s2.FailedReason != "" {
		t.Fatalf("reopen: state=%s balance=%d ledger=%s err=%v", s2.State, f.balance(), s2.LedgerEntryID, err)
	}
	// Back to queued releases the claim; a second fail refunds the new row.
	s3, err := f.s.SetAuditState(f.ctx, a.ID, core.StateQueued, "restart", f.user.ID)
	if err != nil || s3.State != core.StateQueued {
		t.Fatalf("requeue: %v", err)
	}
	queue, _ := f.s.ListQueue(f.ctx)
	if len(queue) != 1 {
		t.Fatalf("queue lists %d", len(queue))
	}
	evs, _ := f.s.ListEvents(f.ctx, a.ID)
	sets := 0
	for _, e := range evs {
		if e.Kind == "state_set" {
			sets++
			if e.Payload["from"] == "" || e.Actor != "system" || e.ActorID != f.user.ID {
				t.Fatalf("state_set event: %+v", e)
			}
		}
	}
	if sets != 3 {
		t.Fatalf("%d state_set events", sets)
	}
	// Delete while the cost is held: refund, row gone, ledger intact.
	ledgerBefore, _ := f.s.ListLedger(f.ctx, f.user.ID)
	del, err := f.s.DeleteAudit(f.ctx, a.ID, f.user.ID)
	if err != nil || !del.Refunded || del.Events != len(evs) || del.Audit.ID != a.ID {
		t.Fatalf("delete: %+v %v", del, err)
	}
	if f.balance() != 1 {
		t.Fatalf("balance after delete %d", f.balance())
	}
	if _, err := f.s.GetAudit(f.ctx, a.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("audit still there: %v", err)
	}
	if evs, _ := f.s.ListEvents(f.ctx, a.ID); len(evs) != 0 {
		t.Fatalf("%d events survive", len(evs))
	}
	ledgerAfter, _ := f.s.ListLedger(f.ctx, f.user.ID)
	if len(ledgerAfter) != len(ledgerBefore)+1 {
		t.Fatalf("ledger %d -> %d", len(ledgerBefore), len(ledgerAfter))
	}
	if _, err := f.s.DeleteAudit(f.ctx, a.ID, f.user.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("second delete: %v", err)
	}
	// A cancelled audit was already refunded: the delete gives nothing back.
	b, _, err := f.s.QueueAudit(f.ctx, f.audit("cccc3333", "P1"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.CancelAudit(f.ctx, b.ID, "stop", f.user.ID, true); err != nil {
		t.Fatal(err)
	}
	del, err = f.s.DeleteAudit(f.ctx, b.ID, f.user.ID)
	if err != nil || del.Refunded || f.balance() != 1 {
		t.Fatalf("delete refunded twice: %+v %d %v", del, f.balance(), err)
	}
}

func TestVisualMetasAreScopedToTheAudit(t *testing.T) {
	f := newFixture(t)
	f.grant(2)
	a, _, err := f.s.QueueAudit(f.ctx, f.audit("aaaa2222", "P1"))
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := f.s.QueueAudit(f.ctx, f.audit("bbbb2222", "P1"))
	if err != nil {
		t.Fatal(err)
	}
	clip, err := f.s.SaveVisual(f.ctx, store.Visual{AuditID: a.ID, Ordinal: 1, Caption: "drain runs", Mime: "video/mp4", SHA256: "x", Bytes: []byte("0123456789")})
	if err != nil {
		t.Fatal(err)
	}
	script, err := f.s.SaveVisual(f.ctx, store.Visual{AuditID: b.ID, Ordinal: 1, Caption: "the poc", Mime: "text/x-python", Name: "poc.py", SHA256: "y", Bytes: []byte("print()")})
	if err != nil {
		t.Fatal(err)
	}
	metas, err := f.s.VisualMetas(f.ctx, a.ID, []string{clip.ID, script.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(metas) != 1 || metas[clip.ID].Size != 10 || metas[clip.ID].Mime != "video/mp4" {
		t.Fatalf("metas %+v", metas)
	}
	got, err := f.s.GetVisual(f.ctx, script.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "poc.py" || got.Size != 7 || string(got.Bytes) != "print()" {
		t.Fatalf("got %+v", got)
	}
}

func TestSeverityHistoryFollowsRevisions(t *testing.T) {
	f := newFixture(t)
	f.grant(1)
	a, _, err := f.s.QueueAudit(f.ctx, f.audit("cccc3333", "P1"))
	if err != nil {
		t.Fatal(err)
	}
	for _, st := range []core.AuditState{core.StateClaimed, core.StateCloning, core.StateProgramsVerified, core.StateAnalysis, core.StateHumanReview} {
		if _, err := f.s.Advance(f.ctx, a.ID, st, "operator", "", nil); err != nil {
			t.Fatal(err)
		}
	}
	doc := core.FindingsDocument{
		Report:   core.ReportHeader{AuditID: a.ID, Pin: core.Pin{Repo: f.target.Repo, Commit: "cccc3333", Programs: []core.ProgramPin{{ID: "P1", Slot: 1, BytecodeSHA256: "abc", Match: core.MatchVerified}}}},
		Findings: []core.Finding{{Ordinal: 1, Severity: core.SeverityHigh, Title: "Missing signer", Location: core.Location{File: "src/lib.rs", LineStart: 4}, Description: "d", Fix: "- f", FixKind: core.FixOperational}},
	}
	r, err := f.s.StoreReport(f.ctx, a.ID, doc)
	if err != nil {
		t.Fatal(err)
	}
	fid := r.Findings[0].ID
	got, err := f.s.SetFindingSeverity(f.ctx, r.ID, fid, core.SeverityMedium, "signer is the program's own PDA", "owner", f.user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Severity != core.SeverityMedium || len(got.SeverityChanges) != 1 || got.SeverityChanges[0].From != core.SeverityHigh || got.SeverityChanges[0].By != "owner" {
		t.Fatalf("got %+v", got.SeverityChanges)
	}
	if _, err := f.s.SetFindingSeverity(f.ctx, r.ID, fid, core.SeverityMedium, "again", "owner", f.user.ID); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("same severity should conflict, got %v", err)
	}
	rep, err := f.s.GetReportByAudit(f.ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Summary.High != 0 || rep.Summary.Medium != 1 {
		t.Fatalf("summary not recomputed: %+v", rep.Summary)
	}
	// A revision rates it high again: the history carries over and the
	// revision is its own entry.
	doc.Findings[0].ID = fid
	doc.Findings[0].Severity = core.SeverityHigh
	for _, st := range []core.AuditState{core.StateDelivered} {
		if _, err := f.s.Advance(f.ctx, a.ID, st, "operator", "", nil); err != nil {
			t.Fatal(err)
		}
	}
	rev, err := f.s.ReviseReport(f.ctx, a.ID, doc, f.user.ID)
	if err != nil {
		t.Fatal(err)
	}
	h := rev.Report.Findings[0].SeverityChanges
	if len(h) != 2 || h[0].Reason != "signer is the program's own PDA" || h[1].To != core.SeverityHigh || h[1].Reason != "revision 2" || h[1].By != "admin" {
		t.Fatalf("history %+v", h)
	}
}

func TestArchiveKeepsReasonAndFeedsLessons(t *testing.T) {
	f := newFixture(t)
	f.grant(1)
	a, _, err := f.s.QueueAudit(f.ctx, f.audit("dddd4444", "P1"))
	if err != nil {
		t.Fatal(err)
	}
	for _, st := range []core.AuditState{core.StateClaimed, core.StateCloning, core.StateProgramsVerified, core.StateAnalysis, core.StateHumanReview} {
		if _, err := f.s.Advance(f.ctx, a.ID, st, "operator", "", nil); err != nil {
			t.Fatal(err)
		}
	}
	doc := core.FindingsDocument{
		Report: core.ReportHeader{AuditID: a.ID, Pin: core.Pin{Repo: f.target.Repo, Commit: "dddd4444", Programs: []core.ProgramPin{{ID: "P1", Slot: 1, BytecodeSHA256: "abc", Match: core.MatchVerified}}}},
		Findings: []core.Finding{
			{Ordinal: 1, Severity: core.SeverityHigh, Title: "Missing signer", Location: core.Location{File: "src/lib.rs", LineStart: 4}, Description: "d", Fix: "- f", FixKind: core.FixOperational},
			{Ordinal: 2, Severity: core.SeverityHigh, Title: "Reentrancy", Location: core.Location{File: "src/lib.rs", LineStart: 9}, Description: "d", Fix: "- f", FixKind: core.FixOperational},
		},
	}
	r, err := f.s.StoreReport(f.ctx, a.ID, doc)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.Advance(f.ctx, a.ID, core.StateDelivered, "operator", "", nil); err != nil {
		t.Fatal(err)
	}
	got, err := f.s.SetFindingStatus(f.ctx, r.ID, r.Findings[1].ID, core.FindingArchived, "the handler is not reachable: no CPI path exists", "owner", f.user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != core.FindingArchived || len(got.StatusChanges) != 1 || got.StatusChanges[0].Reason == "" || got.StatusChanges[0].From != core.FindingOpen {
		t.Fatalf("got %+v", got.StatusChanges)
	}
	rep, err := f.s.GetReportByAudit(f.ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Summary.High != 1 {
		t.Fatalf("archived finding still counted: %+v", rep.Summary)
	}
	if _, err := f.s.SetFindingSeverity(f.ctx, r.ID, r.Findings[0].ID, core.SeverityMedium, "signer is a PDA", "admin", f.user.ID); err != nil {
		t.Fatal(err)
	}
	lessons, err := f.s.ListLessons(f.ctx, f.target.Repo, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(lessons) != 2 || lessons[0].Kind != "severity" || lessons[1].Kind != "status" || lessons[1].To != "archived" || lessons[1].Reason == "" {
		t.Fatalf("lessons %+v", lessons)
	}
	if other, _ := f.s.ListLessons(f.ctx, "someone/else", 10); len(other) != 0 {
		t.Fatalf("lessons leaked across repositories: %+v", other)
	}
}

func TestRevisionFollowsIDWhenRetitled(t *testing.T) {
	f := newFixture(t)
	f.grant(1)
	a, _, err := f.s.QueueAudit(f.ctx, f.audit("eeee5555", "P1"))
	if err != nil {
		t.Fatal(err)
	}
	for _, st := range []core.AuditState{core.StateClaimed, core.StateCloning, core.StateProgramsVerified, core.StateAnalysis, core.StateHumanReview} {
		if _, err := f.s.Advance(f.ctx, a.ID, st, "operator", "", nil); err != nil {
			t.Fatal(err)
		}
	}
	doc := core.FindingsDocument{
		Report:   core.ReportHeader{AuditID: a.ID, Pin: core.Pin{Repo: f.target.Repo, Commit: "eeee5555", Programs: []core.ProgramPin{{ID: "P1", Slot: 1, BytecodeSHA256: "abc", Match: core.MatchVerified}}}},
		Findings: []core.Finding{{Ordinal: 1, Severity: core.SeverityCritical, Title: "Oracle in discovery", Location: core.Location{File: "a.py", LineStart: 4}, Description: "d", Fix: "- f", FixKind: core.FixOperational}},
	}
	r, err := f.s.StoreReport(f.ctx, a.ID, doc)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.Advance(f.ctx, a.ID, core.StateDelivered, "operator", "", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.SetFindingSeverity(f.ctx, r.ID, r.Findings[0].ID, core.SeverityMedium, "not reachable from outside", "admin", f.user.ID); err != nil {
		t.Fatal(err)
	}
	// Retitled and relocated, but the id rides along: the same finding,
	// and its history follows.
	doc.Findings[0].Title = "DNS oracle in discovery"
	doc.Findings[0].Location.File = "b.py"
	doc.Findings[0].Severity = core.SeverityMedium
	doc.Findings[0].CarriedFromID = r.Findings[0].ID
	rev, err := f.s.ReviseReport(f.ctx, a.ID, doc, f.user.ID)
	if err != nil {
		t.Fatal(err)
	}
	h := rev.Report.Findings[0].SeverityChanges
	if rev.Kept != 1 || len(h) != 1 || h[0].Reason != "not reachable from outside" {
		t.Fatalf("kept %d, history %+v", rev.Kept, h)
	}
}
