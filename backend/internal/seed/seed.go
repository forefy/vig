// Package seed writes a development dataset through the store layer: three
// users, credits and purchases, and audits on real repositories, the ones
// the public Bugcrowd scopes name, pinned to real commits and parked in the
// pipeline states an operator works through. It writes no findings: a
// report comes from an audit that ran. It is idempotent and refuses to run
// against production.
package seed

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/forefy/vig/backend/internal/adapter/webapp"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/forefy/vig/backend/internal/adapter/repo"
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

// marker is the identity that says the seed already ran.
const marker = "seed:admin"

// An engagement is a repository a public bug bounty scope lists, pinned to
// a commit that is on GitHub. The seed asks GitHub for the commit's author
// and date; offline it keeps the pin and leaves them empty.
type engagement struct {
	repo   string
	branch string
	commit string
	scope  []string
	// A site instead of a repository: its origin and the hosts in scope.
	site  string
	hosts []string
	state core.AuditState
	ago   time.Duration
}

// The repositories, from the Bugcrowd engagements that list GitHub targets
// (bugcrowd.com/engagements/mattermost-mbb-public, freedomofpress,
// auth0-okta, binance, fireblocks-mbb-og2, clickhouse). The first one is
// the audit `make demo-index` catalogues the demo index against.
var engagements = []engagement{
	{repo: "mattermost/mattermost-plugin-github", branch: "master", commit: "2b9840ce473dfeb222ee69038f31f5d0b428e8ae", scope: []string{"server/", "webapp/"}, state: core.StateAnalysis, ago: 3 * time.Hour},
	{repo: "freedomofpress/securedrop", branch: "develop", commit: "9f86aedc9b7005ec99042242df157c4b31ac5155", scope: []string{"securedrop/", "admin/", "redwood/"}, state: core.StateHumanReview, ago: 20 * time.Hour},
	{repo: "mattermost/mattermost-plugin-calls", branch: "main", commit: "b3cc0387ff8f54a927db3e766ae63eb42dba88ba", scope: []string{"server/", "webapp/", "standalone/"}, state: core.StateProgramsVerified, ago: 9 * time.Hour},
	{repo: "auth0/auth0-spa-js", branch: "main", commit: "0ea366c6e9e82e7cdc58af5b12efa29bad6f8837", scope: []string{"src/"}, state: core.StateCloning, ago: 5 * time.Hour},
	{repo: "mattermost/mattermost-plugin-jira", branch: "master", commit: "074eb689cb505475b46c7243a335b93e78a24b1d", scope: []string{"server/", "webapp/"}, state: core.StateClaimed, ago: 2 * time.Hour},
	{repo: "trustwallet/wallet-core", branch: "master", commit: "2a8aa02011ec42b43689667682fa99ae905418d5", scope: []string{"src/", "rust/", "wasm/"}, state: core.StateDelayed, ago: 26 * time.Hour},
	{repo: "fireblocks/mpc-lib", branch: "main", commit: "00ae08b7f1bdbb887aedece384aca2f942aae432", scope: []string{"src/", "include/"}, state: core.StateQueued},
	{repo: "ClickHouse/ClickHouse", branch: "master", commit: "43e51854bef9371780c185401efdfe5904b559bc", scope: []string{"src/", "programs/"}, state: core.StateQueued},
	// A site, the capture `make demo-index` reads with the operator CLI.
	{site: "https://openrouter.ai", hosts: []string{"openrouter.ai"}, state: core.StateAnalysis, ago: 6 * time.Hour},
}

// IndexedRepo is the engagement the demo index is built for.
var IndexedRepo = engagements[0].repo

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
	// GitHub resolves the pinned commits to their author and date.
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

// commit returns the engagement's pinned commit with what GitHub says
// about it.
func (sd *Seeder) commit(ctx context.Context, e engagement) github.Commit {
	c := github.Commit{SHA: e.commit}
	if sd.GitHub == nil {
		return c
	}
	owner, name, _ := strings.Cut(e.repo, "/")
	got, err := sd.GitHub.Resolve(ctx, github.Ref{Owner: owner, Name: name}, e.commit)
	if err != nil {
		slog.Warn("seed: commit not resolved, target carries the pin alone", "repo", e.repo, "commit", e.commit, "err", err)
		return c
	}
	return got
}

// Run seeds the database. A second run finds the marker and returns the
// existing ids without writing.
func (sd *Seeder) Run(ctx context.Context) (Result, error) {
	if sd.Env == "prod" {
		return Result{}, errors.New("seed refuses to run with VIG_ENV=prod")
	}
	s := sd.Store
	res := Result{Users: map[string]string{}, TOTPSecret: TOTPSecret, APIKey: sd.APIKey(), OperatorToken: sd.OperatorToken()}
	if _, err := s.FindIdentity(ctx, s.Pool, "github", marker); err == nil {
		for _, name := range []string{UserAdmin, UserPro, UserFree} {
			if u, err := s.FindUserByDisplayName(ctx, name); err == nil {
				res.Users[name] = u.ID
			}
		}
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
	grant, err := s.LogAdminAction(ctx, store.AdminAction{AdminID: admin.ID, Kind: "credits.grant", SubjectKind: "user", SubjectID: pro.ID, Payload: map[string]any{"delta": 9, "reason": "design partner"}})
	if err != nil {
		return res, err
	}
	if _, err := s.AppendLedgerLocked(ctx, store.LedgerRow{UserID: pro.ID, Kind: core.LedgerGrant, Delta: 9, RefKind: "admin_action", RefID: grant.ID, Reason: "design partner", ActorID: admin.ID}); err != nil {
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
	if err := notify(pro.ID, "credits_granted", map[string]any{"delta": 9, "reason": "design partner"}, true); err != nil {
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

	// ---- audits on the engagements, one per pipeline state ----
	advance := func(a store.Audit, to core.AuditState, payload map[string]any) (store.Audit, error) {
		return s.Advance(ctx, a.ID, to, "operator", "", payload)
	}
	pipeline := func(a store.Audit, until core.AuditState) (store.Audit, error) {
		for a.State != until {
			var err error
			if a, err = advance(a, a.State.Next(), nil); err != nil {
				return a, err
			}
		}
		return a, nil
	}
	for _, e := range engagements {
		var a store.Audit
		if e.site != "" {
			// A site is pinned to the target itself, the way the API queues one.
			origin, _ := url.Parse(e.site)
			scope := core.SiteScope{Hosts: e.hosts}.Normalize(strings.ToLower(origin.Host))
			t, err := s.CreateTarget(ctx, store.Target{Adapter: webapp.Key, OwnerUserID: pro.ID, SourceKind: "public", Repo: e.site, BaseURL: e.site, Scope: scope, Selector: "commit"})
			if err != nil {
				return res, err
			}
			if a, _, err = s.QueueAudit(ctx, store.Audit{UserID: pro.ID, TargetID: t.ID, Coverage: "full", HeadSHA: t.ID, Scope: store.Scope{Files: scope.URLs(e.site)}}); err != nil {
				return res, fmt.Errorf("%s: %w", e.site, err)
			}
		} else {
			c := sd.commit(ctx, e)
			t, err := s.CreateTarget(ctx, store.Target{Adapter: repo.Key, OwnerUserID: pro.ID, SourceKind: "public", Repo: e.repo,
				Selector: "branch", Revision: e.branch, CommitSHA: c.SHA, CommitAuthor: c.Author, CommitDate: c.Date})
			if err != nil {
				return res, err
			}
			if a, _, err = s.QueueAudit(ctx, store.Audit{UserID: pro.ID, TargetID: t.ID, Coverage: "full", HeadSHA: c.SHA, Scope: store.Scope{Files: e.scope}}); err != nil {
				return res, fmt.Errorf("%s: %w", e.repo, err)
			}
		}
		switch e.state {
		case core.StateQueued:
			continue
		case core.StateDelayed:
			if a, err = pipeline(a, core.StateAnalysis); err != nil {
				return res, err
			}
			eta := time.Now().Add(30 * time.Hour)
			if _, err := s.DelayAudit(ctx, a.ID, eta, "queue over capacity", "operator", ""); err != nil {
				return res, err
			}
			if err := notify(pro.ID, "audit_delayed", map[string]any{"audit_id": a.ID, "eta_at": eta.UTC().Format(time.RFC3339), "reason": "queue over capacity"}, false); err != nil {
				return res, err
			}
		case core.StateClaimed:
			if a, err = pipeline(a, core.StateClaimed); err != nil {
				return res, err
			}
			// Handed to another operator once, the way the admin page does it.
			if _, err := s.Reassign(ctx, a.ID, admin.ID, "operator went offline"); err != nil {
				return res, err
			}
			if _, err := s.LogAdminAction(ctx, store.AdminAction{AdminID: admin.ID, Kind: "audit.reassign", SubjectKind: "audit", SubjectID: a.ID, Payload: map[string]any{"reason": "operator went offline"}}); err != nil {
				return res, err
			}
			if _, err = advance(a, core.StateClaimed, nil); err != nil {
				return res, err
			}
		default:
			if a, err = pipeline(a, e.state); err != nil {
				return res, err
			}
		}
		if err := s.BackdateAudit(ctx, a.ID, e.ago); err != nil {
			return res, err
		}
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
	// ---- an unbound operator token so vig-op can claim a queued audit ----
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
	return fmt.Sprintf("%s\n  admin user id    %s (role admin, TOTP secret %s)\n  pro user id      %s (API key %s)\n  free user id     %s\n  operator token   %s (unbound, claims a queued audit)\n",
		state, r.Users[UserAdmin], r.TOTPSecret, r.Users[UserPro], r.APIKey, r.Users[UserFree], r.OperatorToken)
}
