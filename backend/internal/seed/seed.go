// Package seed writes a realistic development dataset through the store
// layer: three users, four targets on public repositories, audits in every
// pipeline state, delivered reports, ledger movements, purchases, tweet
// claims, notifications and admin actions. It is idempotent and refuses to
// run against production.
package seed

import (
	"context"
	"crypto/ed25519"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/forefy/vig/backend/internal/adapter/solana"
	"github.com/forefy/vig/backend/internal/adapter/webapp"
	"github.com/forefy/vig/backend/internal/auth"
	"github.com/forefy/vig/backend/internal/chain"
	"github.com/forefy/vig/backend/internal/core"
	"github.com/forefy/vig/backend/internal/github"
	"github.com/forefy/vig/backend/internal/store"
)

// Display names of the seeded users; also what `?user=` takes on the dev
// login route.
const (
	UserAdmin = "admin"
	UserPro   = "pro"
	UserFree  = "free"
)

// TOTPSecret is the admin's enrolled secret. Dev only; the seed prints it.
const TOTPSecret = "VIGDEVSEEDTOTPSECRETVIGDEVSEED23"

// PublishedSlug is the public report page the seed creates.
const PublishedSlug = "vault-signer-check"

// marker is the identity that says the seed already ran.
const marker = "seed:admin"

// Result is what the seed command prints.
type Result struct {
	Created       bool
	Users         map[string]string // display name to id
	TOTPSecret    string
	APIKey        string
	OperatorToken string
}

// Seeder holds what the seed needs besides the store.
type Seeder struct {
	Store  *store.Store
	Signer *auth.Signer
	// GitHub checks out the pinned commits so findings carry code captured
	// from the real repository, the same way an operator's report does.
	GitHub *github.Client
	// Secret derives the printable dev credentials so reruns print the same
	// values. It is the session secret in dev.
	Secret string
	Env    string
}

// derive returns hex from a labelled hash of the secret.
func (sd *Seeder) derive(label string) string {
	sum := sha256.Sum256([]byte("vig-seed:" + label + ":" + sd.Secret))
	return hex.EncodeToString(sum[:])
}

// APIKey is the Pro user's key, derived so it can be printed on every run.
func (sd *Seeder) APIKey() string { return auth.APIKeyPrefix + sd.derive("api-key")[:48] }

// OperatorToken is the unbound claim token, derived for the same reason.
func (sd *Seeder) OperatorToken() string { return "vop_" + sd.derive("operator-token")[:48] }

func keypair(label string) (string, ed25519.PrivateKey) {
	sum := sha256.Sum256([]byte("vig-seed-key:" + label))
	priv := ed25519.NewKeyFromSeed(sum[:])
	return chain.Encode(priv.Public().(ed25519.PublicKey)), priv
}

func fakeSHA(label string) string {
	sum := sha1.Sum([]byte("vig-seed-commit:" + label))
	return hex.EncodeToString(sum[:])
}

func hash32(label string) string {
	sum := sha256.Sum256([]byte("vig-seed-bytecode:" + label))
	return hex.EncodeToString(sum[:])
}

// Run seeds the database. A second run finds the marker and returns the
// existing ids without writing.
func (sd *Seeder) Run(ctx context.Context) (Result, error) {
	if sd.Env == "prod" {
		return Result{}, errors.New("seed refuses to run with VIG_ENV=prod")
	}
	s := sd.Store
	// Findings carry code the product captured, not code the seed shipped: the
	// pinned commit is checked out and AttachSnippets runs over it, exactly as
	// it does when an operator submits a report.
	capture = func(repo, commit string, fs []core.Finding) {
		if sd.GitHub == nil {
			return
		}
		root, err := sd.GitHub.Checkout(ctx, repo, commit)
		if err != nil {
			slog.Warn("seed: no checkout, findings carry the seed's own demo code", "repo", repo, "commit", commit, "err", err)
			attachDemoCode(fs)
			return
		}
		github.AttachSnippets(root, fs)
		attachDemoCode(fs)
	}
	captureRefs = func(doc *core.FindingsDocument) {
		if sd.GitHub != nil {
			sd.GitHub.CaptureReferences(ctx, doc)
		}
	}
	res := Result{Users: map[string]string{}, TOTPSecret: TOTPSecret, APIKey: sd.APIKey(), OperatorToken: sd.OperatorToken()}
	if ident, err := s.FindIdentity(ctx, s.Pool, "github", marker); err == nil {
		for _, name := range []string{UserAdmin, UserPro, UserFree} {
			if u, err := s.FindUserByDisplayName(ctx, name); err == nil {
				res.Users[name] = u.ID
			}
		}
		_ = ident
		return res, nil
	} else if !errors.Is(err, store.ErrNotFound) {
		return res, err
	}

	// ---- users and identities ----
	admin, err := s.CreateUser(ctx, s.Pool, UserAdmin, "")
	if err != nil {
		return res, err
	}
	if err := s.SetRole(ctx, admin.ID, "admin"); err != nil {
		return res, err
	}
	if err := s.SetTOTPPending(ctx, admin.ID, TOTPSecret); err != nil {
		return res, err
	}
	if err := s.EnableTOTP(ctx, admin.ID); err != nil {
		return res, err
	}
	if _, err := s.AddIdentity(ctx, s.Pool, admin.ID, "github", marker, "admin@example.com", "vig-admin"); err != nil {
		return res, err
	}
	if _, err := s.AddIdentity(ctx, s.Pool, admin.ID, "google", "seed:admin:google", "admin@example.com", "admin@example.com"); err != nil {
		return res, err
	}
	// A Pro plan so the admin can exercise multi-program audits locally.
	if _, err := s.CreatePlan(ctx, s.Pool, store.Plan{UserID: admin.ID, Kind: "pro", StartsAt: time.Now().Add(-time.Hour), EndsAt: time.Now().Add(365 * 24 * time.Hour), RunCap: 1}); err != nil {
		return res, err
	}

	pro, err := s.CreateUser(ctx, s.Pool, UserPro, "")
	if err != nil {
		return res, err
	}
	proWallet, _ := keypair("pro-wallet")
	if _, err := s.AddIdentity(ctx, s.Pool, pro.ID, "github", "seed:pro", "pro@example.com", "vig-pro"); err != nil {
		return res, err
	}
	if _, err := s.AddIdentity(ctx, s.Pool, pro.ID, "solana", proWallet, "", proWallet); err != nil {
		return res, err
	}

	free, err := s.CreateUser(ctx, s.Pool, UserFree, "")
	if err != nil {
		return res, err
	}
	freeWallet, _ := keypair("free-wallet")
	if _, err := s.AddIdentity(ctx, s.Pool, free.ID, "google", "seed:free:google", "free@example.com", "free@example.com"); err != nil {
		return res, err
	}
	if _, err := s.AddIdentity(ctx, s.Pool, free.ID, "solana", freeWallet, "", freeWallet); err != nil {
		return res, err
	}
	res.Users = map[string]string{UserAdmin: admin.ID, UserPro: pro.ID, UserFree: free.ID}

	// ---- API key and operator token (derived so reruns can print them) ----
	key := sd.APIKey()
	if _, err := s.CreateAPIKey(ctx, pro.ID, key[:12], "ci", auth.Hash(key)); err != nil {
		return res, err
	}

	// ---- credits: admin grant, tweet approval, mini purchase ----
	grant, err := s.LogAdminAction(ctx, store.AdminAction{AdminID: admin.ID, Kind: "credits.grant", SubjectKind: "user", SubjectID: pro.ID, Payload: map[string]any{"delta": 8, "reason": "design partner"}})
	if err != nil {
		return res, err
	}
	if _, err := s.AppendLedgerLocked(ctx, store.LedgerRow{UserID: pro.ID, Kind: core.LedgerGrant, Delta: 8, RefKind: "admin_action", RefID: grant.ID, Reason: "design partner", ActorID: admin.ID}); err != nil {
		return res, err
	}
	notify := func(userID, kind string, payload map[string]any, read bool) error {
		n, err := s.CreateNotification(ctx, store.Notification{UserID: userID, Kind: kind, Payload: payload, Channels: []string{"inapp"}, Delivered: map[string]string{"inapp": "ok"}})
		if err != nil {
			return err
		}
		if read {
			return s.MarkRead(ctx, userID, n.ID)
		}
		return nil
	}
	if err := notify(pro.ID, "credits_granted", map[string]any{"delta": 8, "reason": "design partner"}, true); err != nil {
		return res, err
	}

	proNonce := sd.Signer.TweetNonce(pro.ID)
	claimA, err := s.CreateTweetClaim(ctx, pro.ID, proNonce, "https://x.com/vigpro/status/1830000000000000001")
	if err != nil {
		return res, err
	}
	if _, err := s.ApproveClaim(ctx, claimA.ID, admin.ID, ""); err != nil {
		return res, err
	}
	if _, err := s.LogAdminAction(ctx, store.AdminAction{AdminID: admin.ID, Kind: "tweet_claim.approve", SubjectKind: "tweet_claim", SubjectID: claimA.ID, Payload: map[string]any{"user_id": pro.ID}}); err != nil {
		return res, err
	}
	if err := notify(pro.ID, "claim_approved", map[string]any{"claim_id": claimA.ID}, true); err != nil {
		return res, err
	}

	miniPurchase, err := s.CreatePurchase(ctx, store.Purchase{UserID: pro.ID, SKU: core.SKUMini, AmountUSDC: 25 * core.USDCDecimals, MemoNonce: "vig-" + sd.derive("memo-mini")[:16],
		Treasury: "vigTreasury1111111111111111111111111111111", USDCMint: "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v", ExpiresAt: time.Now().Add(2 * time.Hour)})
	if err != nil {
		return res, err
	}
	if _, err := s.ConfirmPurchase(ctx, miniPurchase.ID, chain.Encode([]byte(sd.derive("sig-mini")[:64])), 285_100_200); err != nil {
		return res, err
	}
	if err := notify(pro.ID, "purchase_confirmed", map[string]any{"purchase_id": miniPurchase.ID, "sku": "mini"}, false); err != nil {
		return res, err
	}
	proPurchase, err := s.CreatePurchase(ctx, store.Purchase{UserID: pro.ID, SKU: core.SKUPro, AmountUSDC: 540 * core.USDCDecimals, MemoNonce: "vig-" + sd.derive("memo-pro")[:16],
		Treasury: "vigTreasury1111111111111111111111111111111", USDCMint: "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v", ExpiresAt: time.Now().Add(2 * time.Hour)})
	if err != nil {
		return res, err
	}
	if _, err := s.ConfirmPurchase(ctx, proPurchase.ID, chain.Encode([]byte(sd.derive("sig-pro")[:64])), 285_100_900); err != nil {
		return res, err
	}
	if err := notify(pro.ID, "purchase_confirmed", map[string]any{"purchase_id": proPurchase.ID, "sku": "pro"}, false); err != nil {
		return res, err
	}

	// ---- targets and programs ----
	authority, authorityKey := keypair("upgrade-authority")
	vaultID, _ := keypair("program-vault")
	escrowID, _ := keypair("program-escrow")
	const tokenProgram = chain.TokenProgram
	stakepool, _ := keypair("program-stakepool")

	// Demo repositories under the vig-demo organisation. When they exist and
	// hold these commits the seed checks them out and the findings carry the
	// code that is there; otherwise the reports seed without snippets.
	commitA := fakeSHA("vault-programs-a")
	commitB := fakeSHA("vault-programs-b")
	t1, err := s.CreateTarget(ctx, store.Target{Adapter: solana.Key, OwnerUserID: pro.ID, SourceKind: "public", Repo: "vig-demo/vault-programs",
		Selector: "branch", Revision: "main", CommitSHA: commitA, CommitAuthor: "demo", CommitDate: time.Now().Add(-40 * 24 * time.Hour)})
	if err != nil {
		return res, err
	}
	t2, err := s.CreateTarget(ctx, store.Target{Adapter: solana.Key, OwnerUserID: pro.ID, SourceKind: "public", Repo: "vig-demo/token-program", Subpath: "token/program",
		Selector: "tag", Revision: "v4.0.0", CommitSHA: fakeSHA("token-program"), CommitAuthor: "demo", CommitDate: time.Now().Add(-120 * 24 * time.Hour)})
	if err != nil {
		return res, err
	}
	t3, err := s.CreateTarget(ctx, store.Target{Adapter: solana.Key, OwnerUserID: pro.ID, SourceKind: "public", Repo: "vig-demo/stake-pool",
		Selector: "branch", Revision: "main", CommitSHA: fakeSHA("stake-pool"), CommitAuthor: "demo", CommitDate: time.Now().Add(-9 * 24 * time.Hour)})
	if err != nil {
		return res, err
	}
	t4, err := s.CreateTarget(ctx, store.Target{Adapter: solana.Key, OwnerUserID: pro.ID, SourceKind: "public", Repo: "vig-demo/vault-programs",
		Selector: "commit", Revision: commitB[:12], CommitSHA: commitB, CommitAuthor: "demo", CommitDate: time.Now().Add(-3 * 24 * time.Hour)})
	if err != nil {
		return res, err
	}

	vaultFacts := solana.Facts{ProgramID: vaultID, Exists: true, Loader: "upgradeable", UpgradeAuthority: authority, LastDeploySlot: 284_912_000,
		DeployedAt: time.Now().Add(-45 * 24 * time.Hour), BytecodeSHA256: hash32("vault"), IDLPresent: true, IDLVersion: "0.30.1",
		DeclaredMatch: "match", DeclaredFile: "programs/0-signer-authorization/insecure/src/lib.rs", VerifiedBuild: core.MatchVerified, VerifiedCommit: commitA}
	vaultProvenFacts := vaultFacts
	vaultProvenFacts.OwnershipProven = true
	escrowFacts := solana.Facts{ProgramID: escrowID, Exists: true, Loader: "upgradeable", UpgradeAuthority: authority, LastDeploySlot: 283_400_500,
		DeployedAt: time.Now().Add(-70 * 24 * time.Hour), BytecodeSHA256: hash32("escrow"), IDLPresent: false,
		DeclaredMatch: "mismatch", VerifiedBuild: core.MatchMismatch}
	tokenFacts := solana.Facts{ProgramID: tokenProgram, Exists: true, Loader: "upgradeable", UpgradeAuthority: "", LastDeploySlot: 239_140_000,
		DeployedAt: time.Now().Add(-400 * 24 * time.Hour), BytecodeSHA256: hash32("token"), IDLPresent: false, DeclaredMatch: "match", DeclaredFile: "token/program/src/lib.rs",
		VerifiedBuild: core.MatchUnverified}
	stakepoolFacts := solana.Facts{ProgramID: stakepool, Exists: true, Loader: "upgradeable", UpgradeAuthority: authority, LastDeploySlot: 281_770_000,
		DeployedAt: time.Now().Add(-25 * 24 * time.Hour), BytecodeSHA256: hash32("stakepool"), IDLPresent: true, IDLVersion: "0.29.0",
		DeclaredMatch: "match", DeclaredFile: "programs/stakepool/src/lib.rs", VerifiedBuild: core.MatchVerified, VerifiedCommit: fakeSHA("stakepool")}

	program := func(targetID string, f solana.Facts) error {
		_, err := s.UpsertProgram(ctx, store.Program{TargetID: targetID, ProgramID: f.ProgramID, Loader: f.Loader, UpgradeAuthority: f.UpgradeAuthority,
			LastDeploySlot: f.LastDeploySlot, BytecodeSHA256: f.BytecodeSHA256, IDLPresent: f.IDLPresent, AnchorVersion: f.IDLVersion,
			VerifiedBuild: f.VerifiedBuild, VerifiedCommit: f.VerifiedCommit, Panel: solana.Panel(f)})
		return err
	}
	for _, p := range []struct {
		target string
		facts  solana.Facts
	}{{t1.ID, vaultFacts}, {t1.ID, escrowFacts}, {t2.ID, tokenFacts}, {t3.ID, stakepoolFacts}, {t4.ID, vaultFacts}} {
		if err := program(p.target, p.facts); err != nil {
			return res, err
		}
	}
	// Prove ownership of the vault program on both vault targets with a
	// genuine signature by the seeded upgrade authority.
	for _, tid := range []string{t1.ID, t4.ID} {
		msg := solana.OwnershipMessage(sd.derive("nonce-" + tid)[:24], vaultID, time.Now())
		sig := ed25519.Sign(authorityKey, []byte(msg))
		if err := s.SetOwnershipNonce(ctx, tid, vaultID, msg); err != nil {
			return res, err
		}
		if err := s.SetOwnershipProven(ctx, tid, vaultID, chain.Encode(sig), solana.Panel(vaultProvenFacts)); err != nil {
			return res, err
		}
	}

	// ---- audits in every state (all by the Pro user) ----
	vaultScope := store.Scope{Files: []string{
		"programs/0-signer-authorization/insecure/src/lib.rs", "programs/1-account-data-matching/insecure/src/lib.rs",
		"programs/2-owner-checks/insecure/src/lib.rs", "programs/3-type-cosplay/insecure/src/lib.rs",
		"programs/5-arbitrary-cpi/insecure/src/lib.rs", "programs/9-closing-accounts/insecure/src/lib.rs", "Cargo.toml", "Anchor.toml",
	}, Lines: 1_240}
	tokenScope := store.Scope{Files: []string{"token/program/src/lib.rs", "token/program/src/processor.rs", "token/program/src/state.rs", "token/program/src/instruction.rs", "token/program/src/error.rs", "token/program/Cargo.toml"}, Lines: 6_810}
	stakepoolScope := store.Scope{Files: []string{"programs/stakepool/src/lib.rs", "programs/stakepool/src/state/mod.rs", "programs/stakepool/src/instructions/deposit.rs", "programs/stakepool/src/instructions/liquid_unstake.rs", "programs/stakepool/src/instructions/admin/mod.rs", "Cargo.toml"}, Lines: 5_420}

	queue := func(target store.Target, programs []string, scope store.Scope, coverage, base string) (store.Audit, error) {
		a, _, err := s.QueueAudit(ctx, store.Audit{UserID: pro.ID, TargetID: target.ID, Coverage: coverage, BaseSHA: base, HeadSHA: target.CommitSHA, ProgramIDs: programs, Scope: scope})
		return a, err
	}
	advance := func(a store.Audit, to core.AuditState, payload map[string]any) (store.Audit, error) {
		return s.Advance(ctx, a.ID, to, "operator", "", payload)
	}
	pipeline := func(a store.Audit, until core.AuditState) (store.Audit, error) {
		for a.State != until {
			next := a.State.Next()
			var err error
			a, err = advance(a, next, nil)
			if err != nil {
				return a, err
			}
			if next == core.StateProgramsVerified {
				programs := []map[string]any{}
				for _, id := range a.ProgramIDs {
					programs = append(programs, map[string]any{"id": id, "slot": 285_000_000, "bytecode_sha256": hash32(id), "match": "self_built"})
				}
				if err := s.AddEvent(ctx, s.Pool, store.AuditEvent{AuditID: a.ID, Kind: "programs_recorded", Actor: "operator", State: a.State, Payload: map[string]any{"programs": programs}}); err != nil {
					return a, err
				}
			}
		}
		return a, nil
	}
	deliver := func(a store.Audit, doc core.FindingsDocument, ago time.Duration, read bool) (store.Report, error) {
		a, err := pipeline(a, core.StateHumanReview)
		if err != nil {
			return store.Report{}, err
		}
		doc.Report.AuditID = a.ID
		exhaustSeed(&doc)
		if err := attachProofs(ctx, s, a.ID, &doc); err != nil {
			return store.Report{}, err
		}
		r, err := s.StoreReport(ctx, a.ID, doc)
		if err != nil {
			return r, err
		}
		if _, err := advance(a, core.StateDelivered, map[string]any{"report_id": r.ID}); err != nil {
			return r, err
		}
		if err := s.BackdateAudit(ctx, a.ID, ago); err != nil {
			return r, err
		}
		return r, notify(pro.ID, "audit_delivered", map[string]any{"audit_id": a.ID, "report_id": r.ID}, read)
	}

	// Three delivered audits, oldest first, so the diff audit can carry over.
	a1, err := queue(t1, []string{vaultID}, vaultScope, "full", "")
	if err != nil {
		return res, err
	}
	r1, err := deliver(a1, vaultReport(t1.Repo, commitA, vaultID), 30*24*time.Hour, true)
	if err != nil {
		return res, err
	}
	// The owner fixed the critical one before re-auditing.
	if _, err := s.SetFindingStatus(ctx, r1.ID, r1.Findings[0].ID, core.FindingFixed, "", "owner", ""); err != nil {
		return res, err
	}
	if _, err := s.SetFindingStatus(ctx, r1.ID, r1.Findings[2].ID, core.FindingAcknowledged, "", "owner", ""); err != nil {
		return res, err
	}
	// The owner re-rated one finding with a reason: the report shows the
	// move under the finding and in its changes log.
	if len(r1.Findings) > 2 {
		if _, err := s.SetFindingSeverity(ctx, r1.ID, r1.Findings[2].ID, core.SeverityLow, "token_program is a fixed PDA in every caller we ship", "owner", pro.ID); err != nil {
			return res, err
		}
	}
	// And archived one as not relevant, with the reason: it leaves the
	// counts, stays on the report, and is a lesson for the next audit.
	if len(r1.Findings) > 4 {
		if _, err := s.SetFindingStatus(ctx, r1.ID, r1.Findings[4].ID, core.FindingArchived, "Metadata is never deserialized from user-supplied data", "owner", pro.ID); err != nil {
			return res, err
		}
	}
	if err := s.Publish(ctx, r1.ID, PublishedSlug); err != nil {
		return res, err
	}

	a2, err := queue(t3, []string{stakepool}, stakepoolScope, "full", "")
	if err != nil {
		return res, err
	}
	if _, err := deliver(a2, stakepoolReport(t3.Repo, t3.CommitSHA, stakepool), 12*24*time.Hour, true); err != nil {
		return res, err
	}

	a3, err := queue(t4, []string{vaultID}, vaultScope, "diff", commitA)
	if err != nil {
		return res, err
	}
	if _, err := deliver(a3, vaultDiffReport(t4.Repo, commitB, commitA, vaultID), 2*24*time.Hour, false); err != nil {
		return res, err
	}

	// Failed: build broke during cloning; the credit comes back.
	a4, err := queue(t1, []string{escrowID}, vaultScope, "full", "")
	if err != nil {
		return res, err
	}
	if a4, err = pipeline(a4, core.StateCloning); err != nil {
		return res, err
	}
	if _, err := s.FailAudit(ctx, a4.ID, "cargo build-sbf failed: edition 2021 crate needs solana 1.18 toolchain", "operator", ""); err != nil {
		return res, err
	}
	if err := s.BackdateAudit(ctx, a4.ID, 20*24*time.Hour); err != nil {
		return res, err
	}
	if err := notify(pro.ID, "audit_failed", map[string]any{"audit_id": a4.ID, "reason": "build failed"}, true); err != nil {
		return res, err
	}

	// ---- the web demo: a site with a source snapshot, delivered ----
	t5, err := s.CreateTarget(ctx, store.Target{Adapter: webapp.Key, OwnerUserID: pro.ID, SourceKind: "public", Repo: ledgerlyRepo,
		Selector: "branch", Revision: "main", CommitSHA: fakeSHA("ledgerly"), CommitAuthor: "demo", CommitDate: time.Now().Add(-6 * 24 * time.Hour),
		BaseURL: ledgerlyApp, Scope: core.SiteScope{Hosts: ledgerlyHosts}})
	if err != nil {
		return res, err
	}
	aw, err := queue(t5, []string{}, store.Scope{Files: []string{ledgerlyApp + "/", ledgerlyAPI + "/"}, Lines: 0}, "full", "")
	if err != nil {
		return res, err
	}
	wdoc := ledgerlyReport(t5.CommitSHA)
	rw, err := deliver(aw, wdoc, 5*24*time.Hour, true)
	if err != nil {
		return res, err
	}
	// The owner fixed the invoice lookup, acknowledged the login one and
	// re-rated it down with a reason, and archived the stack trace one.
	if _, err := s.SetFindingStatus(ctx, rw.ID, rw.Findings[0].ID, core.FindingFixed, "", "owner", ""); err != nil {
		return res, err
	}
	if _, err := s.SetFindingStatus(ctx, rw.ID, rw.Findings[3].ID, core.FindingAcknowledged, "", "owner", ""); err != nil {
		return res, err
	}
	if _, err := s.SetFindingSeverity(ctx, rw.ID, rw.Findings[3].ID, core.SeverityLow, "SSO is enforced for every organisation since March; the password form is staff only", "owner", pro.ID); err != nil {
		return res, err
	}
	if _, err := s.SetFindingStatus(ctx, rw.ID, rw.Findings[4].ID, core.FindingArchived, "NODE_ENV=production masks the stack in the deployed handler", "owner", pro.ID); err != nil {
		return res, err
	}
	if err := s.Publish(ctx, rw.ID, LedgerlySlug); err != nil {
		return res, err
	}
	webSurface, webEvidence := ledgerlySurface(wdoc.Findings, time.Now().UTC())
	if err := s.SaveSurface(ctx, aw.ID, webSurface); err != nil {
		return res, err
	}
	if _, err := s.AddSurfaceEvidence(ctx, aw.ID, webEvidence); err != nil {
		return res, err
	}

	// One audit parked in each active state.
	stages := []struct {
		state core.AuditState
		ago   time.Duration
	}{{core.StateHumanReview, 20 * time.Hour}, {core.StateAnalysis, 14 * time.Hour}, {core.StateProgramsVerified, 9 * time.Hour}, {core.StateCloning, 5 * time.Hour}, {core.StateClaimed, 2 * time.Hour}}
	targetsForStages := []store.Target{t2, t3, t1, t3, t2}
	programsForStages := [][]string{{tokenProgram}, {stakepool}, {vaultID, escrowID}, {stakepool}, {tokenProgram}}
	scopesForStages := []store.Scope{tokenScope, stakepoolScope, vaultScope, stakepoolScope, tokenScope}
	// Each one differs from the delivered audits in commit, base or program
	// set, so the dedup rule does not hand back an existing audit.
	coverages := []string{"full", "diff", "full", "diff", "diff"}
	bases := []string{"", fakeSHA("stakepool-old"), "", fakeSHA("stakepool-mid"), fakeSHA("token-program-old")}
	for i, st := range stages {
		a, err := queue(targetsForStages[i], programsForStages[i], scopesForStages[i], coverages[i], bases[i])
		if err != nil {
			return res, err
		}
		if a, err = pipeline(a, st.state); err != nil {
			return res, err
		}
		if err := s.BackdateAudit(ctx, a.ID, st.ago); err != nil {
			return res, err
		}
		if st.state == core.StateClaimed {
			if _, err := s.Reassign(ctx, a.ID, admin.ID, "operator went offline"); err != nil {
				return res, err
			}
			if _, err := s.LogAdminAction(ctx, store.AdminAction{AdminID: admin.ID, Kind: "audit.reassign", SubjectKind: "audit", SubjectID: a.ID, Payload: map[string]any{"reason": "operator went offline"}}); err != nil {
				return res, err
			}
			if _, err = advance(a, core.StateClaimed, nil); err != nil {
				return res, err
			}
		}
	}

	// Delayed during analysis with a new ETA.
	a5, err := queue(t3, []string{stakepool}, store.Scope{Files: stakepoolScope.Files[:3], Lines: 2_980}, "diff", fakeSHA("stakepool-older"))
	if err != nil {
		return res, err
	}
	if a5, err = pipeline(a5, core.StateAnalysis); err != nil {
		return res, err
	}
	if _, err := s.DelayAudit(ctx, a5.ID, time.Now().Add(30*time.Hour), "queue over capacity", "operator", ""); err != nil {
		return res, err
	}
	if err := s.BackdateAudit(ctx, a5.ID, 26*time.Hour); err != nil {
		return res, err
	}
	if err := notify(pro.ID, "audit_delayed", map[string]any{"audit_id": a5.ID, "eta_at": time.Now().Add(30 * time.Hour).UTC().Format(time.RFC3339), "reason": "queue over capacity"}, false); err != nil {
		return res, err
	}

	// Queued: the balance is zero by now, so this one takes today's Pro slot.
	if _, err := queue(t2, []string{tokenProgram}, store.Scope{Files: tokenScope.Files[:2], Lines: 3_105}, "diff", fakeSHA("spl-token-older")); err != nil {
		return res, err
	}

	// ---- remaining tweet claims and credit movements ----
	claimB, err := s.CreateTweetClaim(ctx, pro.ID, proNonce, "https://x.com/vigpro/status/1830000000000000002")
	if err != nil {
		return res, err
	}
	if _, err := s.ApproveClaim(ctx, claimB.ID, admin.ID, ""); err != nil {
		return res, err
	}
	if _, err := s.RevokeClaim(ctx, claimB.ID, admin.ID, "post was deleted"); err != nil {
		return res, err
	}
	if _, err := s.LogAdminAction(ctx, store.AdminAction{AdminID: admin.ID, Kind: "tweet_claim.revoke", SubjectKind: "tweet_claim", SubjectID: claimB.ID, Payload: map[string]any{"reason": "post was deleted"}}); err != nil {
		return res, err
	}
	if err := notify(pro.ID, "claim_revoked", map[string]any{"claim_id": claimB.ID, "reason": "post was deleted"}, false); err != nil {
		return res, err
	}
	claimC, err := s.CreateTweetClaim(ctx, pro.ID, proNonce, "https://x.com/vigpro/status/1830000000000000003")
	if err != nil {
		return res, err
	}
	if _, err := s.RejectClaim(ctx, claimC.ID, admin.ID, "nonce missing from the post"); err != nil {
		return res, err
	}
	if _, err := s.LogAdminAction(ctx, store.AdminAction{AdminID: admin.ID, Kind: "tweet_claim.reject", SubjectKind: "tweet_claim", SubjectID: claimC.ID, Payload: map[string]any{"reason": "nonce missing from the post"}}); err != nil {
		return res, err
	}
	if _, err := s.CreateTweetClaim(ctx, free.ID, sd.Signer.TweetNonce(free.ID), "https://x.com/vigfree/status/1830000000000000009"); err != nil {
		return res, err
	}

	topup, err := s.LogAdminAction(ctx, store.AdminAction{AdminID: admin.ID, Kind: "credits.grant", SubjectKind: "user", SubjectID: pro.ID, Payload: map[string]any{"delta": 2, "reason": "goodwill after delay"}})
	if err != nil {
		return res, err
	}
	if _, err := s.AppendLedgerLocked(ctx, store.LedgerRow{UserID: pro.ID, Kind: core.LedgerGrant, Delta: 2, RefKind: "admin_action", RefID: topup.ID, Reason: "goodwill after delay", ActorID: admin.ID}); err != nil {
		return res, err
	}

	revoke, err := s.LogAdminAction(ctx, store.AdminAction{AdminID: admin.ID, Kind: "credits.revoke", SubjectKind: "user", SubjectID: pro.ID, Payload: map[string]any{"delta": -1, "reason": "duplicate grant"}})
	if err != nil {
		return res, err
	}
	if _, err := s.AppendLedgerLocked(ctx, store.LedgerRow{UserID: pro.ID, Kind: core.LedgerRevoke, Delta: -1, RefKind: "admin_action", RefID: revoke.ID, Reason: "duplicate grant", ActorID: admin.ID}); err != nil {
		return res, err
	}
	if err := notify(pro.ID, "credits_revoked", map[string]any{"delta": 1, "reason": "duplicate grant"}, false); err != nil {
		return res, err
	}
	// ---- an unbound operator token so vig-op can claim the queued audit ----
	tok := sd.OperatorToken()
	t, err := s.CreateOperatorToken(ctx, "", auth.Hash(tok), time.Now().Add(48*time.Hour), admin.ID)
	if err != nil {
		return res, err
	}
	if _, err := s.LogAdminAction(ctx, store.AdminAction{AdminID: admin.ID, Kind: "operator_token.create", SubjectKind: "operator_token", SubjectID: t.ID, Payload: map[string]any{"hours": 48}}); err != nil {
		return res, err
	}
	res.Created = true
	return res, nil
}

// Summary renders the result for the terminal.
func (r Result) Summary() string {
	state := "already seeded, nothing written"
	if r.Created {
		state = "seeded"
	}
	return fmt.Sprintf("%s\n  admin user id    %s (role admin, TOTP secret %s)\n  pro user id      %s (API key %s)\n  free user id     %s\n  operator token   %s (unbound, claims the queued audit)\n  published report /r/%s\n",
		state, r.Users[UserAdmin], r.TOTPSecret, r.Users[UserPro], r.APIKey, r.Users[UserFree], r.OperatorToken, PublishedSlug)
}
