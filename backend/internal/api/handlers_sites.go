package api

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/forefy/vig/backend/internal/auth"
	"github.com/forefy/vig/backend/internal/core"
	"github.com/forefy/vig/backend/internal/github"
	"github.com/forefy/vig/backend/internal/store"
)

// A live target: the adapter declares a site step where a chain adapter
// declares a source step, and the handlers below are the site's half of
// the target routes. Nothing here names an adapter; the step keys and the
// optional core.SiteOwnership capability are the whole contract.

// StepSite is the step key that marks an adapter's target as a live origin
// rather than a repository; StepSource marks a repository.
const (
	StepSite   = "site"
	StepSource = "source"
)

// SiteNonceTTL is how long a site nonce may be placed after it was issued.
// DNS takes time to propagate, so it is a day where a signature gets minutes.
const SiteNonceTTL = 24 * time.Hour

// ProveAction is the panel action a not-proven ownership row carries. The
// handler rewrites the row that carries it once the proof lands.
const ProveAction = "prove_ownership"

type setScopeIn struct {
	ID   string `path:"id"`
	Body core.SiteScope
}

type siteNonceOut struct {
	Body struct {
		Nonce     string    `json:"nonce"`
		DNSName   string    `json:"dns_name" doc:"TXT record name to create"`
		Value     string    `json:"value" doc:"The TXT record's value, and the file's whole body"`
		FileURL   string    `json:"file_url" doc:"Where the file must answer"`
		ExpiresAt time.Time `json:"expires_at"`
	}
}

// hasStep says whether an adapter declares a step.
func hasStep(a core.Adapter, key string) bool {
	for _, s := range a.IntakeSteps() {
		if s.Key == key {
			return true
		}
	}
	return false
}

// isSite says whether a stored target is a live origin.
func (d *Deps) isSite(t store.Target) bool {
	a, ok := d.Adapters[t.Adapter]
	return ok && hasStep(a, StepSite)
}

// siteDraft is the intake draft the site handlers validate through the adapter.
func siteDraft(base, repoURL string, scope core.SiteScope) core.IntakeDraft {
	return core.IntakeDraft{
		StepSite:   {"base_url": base, "repo_url": repoURL},
		"scope":    {"hosts": scope.Hosts, "paths": scope.Paths},
		StepSource: {},
	}
}

func firstIssue(issues []core.Issue) error {
	if len(issues) == 0 {
		return nil
	}
	return huma.Error400BadRequest(issues[0].Field + ": " + issues[0].Msg)
}

// originHost is the host of a stored site's origin.
func originHost(base string) string {
	u, err := url.Parse(base)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

// createSiteTarget resolves a live origin into a target: the adapter
// validates the origin and the scope, the origin is read once for its
// panel, and a repository, when one is named, is resolved like any
// source and kept as the snapshot the operator reads.
func (d *Deps) createSiteTarget(ctx context.Context, u store.User, adapter core.Adapter, in *createTargetIn) (*body[Target], error) {
	base := strings.TrimSpace(in.Body.BaseURL)
	if base == "" {
		return nil, huma.Error400BadRequest("base_url: a site needs an origin")
	}
	repoURL := strings.TrimSpace(in.Body.RepoURL)
	scope := core.SiteScope{Hosts: in.Body.Hosts, Paths: in.Body.Paths}
	issues, err := adapter.Validate(ctx, siteDraft(base, repoURL, scope))
	if err != nil {
		return nil, fail(err)
	}
	if err := firstIssue(issues); err != nil {
		return nil, err
	}
	origin, _ := url.Parse(base)
	base = (&url.URL{Scheme: origin.Scheme, Host: strings.ToLower(origin.Host)}).String()
	scope = scope.Normalize(originHost(base))

	t := store.Target{Adapter: adapter.Key(), OwnerUserID: u.ID, SourceKind: "public", Repo: base, BaseURL: base, Scope: scope, Selector: "commit"}
	if in.Body.SourceKind != "" {
		t.SourceKind = in.Body.SourceKind
	}
	if repoURL != "" {
		if err := d.sourceReady(); err != nil {
			return nil, err
		}
		ref, err := github.ParseURL(repoURL)
		if err != nil {
			return nil, huma.Error400BadRequest(err.Error())
		}
		rev := strings.TrimSpace(in.Body.Revision)
		if rev == "" {
			rev = ref.Rev
		}
		selector := in.Body.Selector
		if rev == "" {
			rev, err = d.GitHub.DefaultBranch(ctx, ref)
			if err != nil {
				return nil, fail(err)
			}
			selector = "branch"
		}
		if selector == "" {
			selector = "commit"
		}
		commit, err := d.GitHub.Resolve(ctx, ref, rev)
		if err != nil {
			return nil, fail(err)
		}
		t.Repo, t.Subpath, t.Selector, t.Revision = ref.FullName(), ref.Path, selector, rev
		t.CommitSHA, t.CommitAuthor, t.CommitDate = commit.SHA, commit.Author, commit.Date
	}
	// One read of the origin, bounded by the adapter's client; a site that
	// does not answer still becomes a target, with the fact on its panel.
	vctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	panels, err := adapter.Verify(vctx, siteDraft(base, "", scope))
	cancel()
	if err == nil && len(panels) > 0 {
		t.Panel = &panels[0]
	} else if err != nil && !errors.Is(err, core.ErrNotWired) {
		return nil, huma.Error502BadGateway("site read failed: " + err.Error())
	}
	created, err := d.Store.CreateTarget(ctx, t)
	if err != nil {
		return nil, fail(err)
	}
	out, err := d.toTarget(ctx, created)
	if err != nil {
		return nil, fail(err)
	}
	return &body[Target]{Body: out}, nil
}

// setTargetScope replaces what the customer authorises on a site. The
// adapter checks every host stays under the origin.
func (d *Deps) setTargetScope(ctx context.Context, in *setScopeIn) (*body[Target], error) {
	t, _, err := d.ownedTarget(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	adapter := d.Adapters[t.Adapter]
	if adapter == nil || !hasStep(adapter, StepSite) {
		return nil, huma.Error409Conflict("target is not a site")
	}
	scope := core.SiteScope{Hosts: in.Body.Hosts, Paths: in.Body.Paths}
	issues, err := adapter.Validate(ctx, siteDraft(t.BaseURL, "", scope))
	if err != nil {
		return nil, fail(err)
	}
	if err := firstIssue(issues); err != nil {
		return nil, err
	}
	scope = scope.Normalize(originHost(t.BaseURL))
	if err := d.Store.SetTargetScope(ctx, t.ID, scope); err != nil {
		return nil, fail(err)
	}
	t, err = d.Store.GetTarget(ctx, t.ID)
	if err != nil {
		return nil, fail(err)
	}
	out, err := d.toTarget(ctx, t)
	if err != nil {
		return nil, fail(err)
	}
	return &body[Target]{Body: out}, nil
}

// siteOwnership is the adapter capability behind the two routes below.
func (d *Deps) siteOwnership(t store.Target) (core.SiteOwnership, error) {
	adapter := d.Adapters[t.Adapter]
	if adapter == nil || !hasStep(adapter, StepSite) {
		return nil, huma.Error409Conflict("target is not a site")
	}
	so, ok := adapter.(core.SiteOwnership)
	if !ok {
		return nil, huma.Error503ServiceUnavailable("adapter cannot prove ownership")
	}
	return so, nil
}

// siteOwnershipNonce issues the nonce and says where it goes.
func (d *Deps) siteOwnershipNonce(ctx context.Context, in *idPath) (*siteNonceOut, error) {
	t, _, err := d.ownedTarget(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	so, err := d.siteOwnership(t)
	if err != nil {
		return nil, err
	}
	nonce := auth.RandomToken(16)
	places, err := so.OwnershipPlaces(t.BaseURL, nonce)
	if err != nil {
		return nil, huma.Error400BadRequest(err.Error())
	}
	if err := d.Store.SetTargetOwnershipNonce(ctx, t.ID, nonce); err != nil {
		return nil, fail(err)
	}
	out := &siteNonceOut{}
	out.Body.Nonce, out.Body.DNSName, out.Body.Value, out.Body.FileURL = nonce, places.DNSName, places.Value, places.FileURL
	out.Body.ExpiresAt = d.now().Add(SiteNonceTTL)
	return out, nil
}

// siteOwnershipVerify looks for the nonce where it was told to go.
func (d *Deps) siteOwnershipVerify(ctx context.Context, in *idPath) (*body[Target], error) {
	t, _, err := d.ownedTarget(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	so, err := d.siteOwnership(t)
	if err != nil {
		return nil, err
	}
	if t.OwnershipNonce == "" {
		return nil, huma.Error409Conflict("request a nonce first")
	}
	if t.OwnershipNonceAt == nil || d.now().Sub(*t.OwnershipNonceAt) > SiteNonceTTL {
		return nil, huma.Error409Conflict("nonce expired; request a new one")
	}
	vctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	method, err := so.ProveOwnership(vctx, t.BaseURL, t.OwnershipNonce)
	cancel()
	if err != nil {
		if errors.Is(err, core.ErrNotWired) {
			return nil, huma.Error503ServiceUnavailable("site access not configured")
		}
		return nil, huma.Error502BadGateway("proof read failed: " + err.Error())
	}
	if method == "" {
		return nil, huma.Error409Conflict("nonce not found in the TXT record or the well-known file")
	}
	panel := core.VerificationPanel{Subject: t.BaseURL}
	if t.Panel != nil {
		panel = *t.Panel
	}
	for i := range panel.Rows {
		if panel.Rows[i].Action == ProveAction {
			panel.Rows[i].Value, panel.Rows[i].State, panel.Rows[i].Detail, panel.Rows[i].Action = "proven", core.BadgeOK, method, ""
		}
	}
	if err := d.Store.SetTargetOwnershipProven(ctx, t.ID, method, panel); err != nil {
		return nil, fail(err)
	}
	t, err = d.Store.GetTarget(ctx, t.ID)
	if err != nil {
		return nil, fail(err)
	}
	out, err := d.toTarget(ctx, t)
	if err != nil {
		return nil, fail(err)
	}
	return &body[Target]{Body: out}, nil
}

func (d *Deps) registerSites(api huma.API) {
	huma.Register(api, withAuth(op("setTargetScope", http.MethodPut, "/v1/targets/{id}/scope", "Hosts and path prefixes the review may probe", "targets"), userAuth), d.setTargetScope)
	huma.Register(api, withAuth(op("siteOwnershipNonce", http.MethodPost, "/v1/targets/{id}/ownership/nonce", "Nonce to place in DNS or under /.well-known", "targets"), userAuth), d.siteOwnershipNonce)
	huma.Register(api, withAuth(op("siteOwnershipVerify", http.MethodPost, "/v1/targets/{id}/ownership/verify", "Prove ownership of the site", "targets"), userAuth), d.siteOwnershipVerify)
}
