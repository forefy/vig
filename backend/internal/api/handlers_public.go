package api

import (
	"context"
	"crypto/sha256"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/forefy/vig/backend/internal/auth"
	"github.com/forefy/vig/backend/internal/github"
	"github.com/forefy/vig/backend/internal/store"
)

// ScansPerDay is the anonymous limit per hashed IP or fingerprint.
const ScansPerDay = 5

// ScanTTL is how long an anonymous result stays reachable.
const ScanTTL = 7 * 24 * time.Hour

type scanIn struct {
	Body struct {
		RepoURL   string `json:"repo_url" doc:"Public GitHub URL, optionally /tree/<rev>/<path>"`
		ProgramID string `json:"program_id,omitempty"`
	}
}

type tokenPath struct {
	Token string `path:"token"`
}

// scanOut carries the scan and tells crawlers to leave the tokenised URL
// alone: a scan result is reachable by anyone holding the link, not by
// search.
type scanOut struct {
	Robots string `header:"X-Robots-Tag"`
	Body   Scan
}

func scanResult(sc Scan) *scanOut {
	return &scanOut{Robots: "noindex", Body: sc}
}

type slugPath struct {
	Slug string `path:"slug"`
}

// hashKey hashes an IP or fingerprint with the session secret so raw
// addresses never reach the database.
func (d *Deps) hashKey(kind, v string) []byte {
	if v == "" {
		return nil
	}
	sum := sha256.Sum256([]byte(kind + ":" + d.Config.SessionSecret + ":" + v))
	return sum[:]
}

func (d *Deps) scansRemaining(ctx context.Context) (int, error) {
	r := req(ctx)
	n, err := d.Store.CountScansSince(ctx, d.hashKey("ip", r.IP), d.hashKey("fp", r.Fingerprint), d.now().Add(-24*time.Hour))
	if err != nil {
		return 0, err
	}
	if n >= ScansPerDay {
		return 0, nil
	}
	return ScansPerDay - n, nil
}

func (d *Deps) toScan(sc store.Scan, remaining int) Scan {
	repo, _, _ := cutRepo(sc.Repo)
	return Scan{Token: sc.Token, Repo: repo, Commit: sc.CommitSHA, ProgramID: sc.ProgramID, State: sc.State, Result: sc.Result, ExpiresAt: sc.ExpiresAt, Remaining: remaining}
}

func (d *Deps) createScan(ctx context.Context, in *scanIn) (*scanOut, error) {
	if err := d.ready(); err != nil {
		return nil, err
	}
	if d.GitHub == nil {
		return nil, huma.Error503ServiceUnavailable("source access not configured")
	}
	ref, err := github.ParseURL(in.Body.RepoURL)
	if err != nil {
		return nil, huma.Error400BadRequest(err.Error())
	}
	if in.Body.ProgramID != "" && d.Scans != nil && d.Scans.AdapterFor(in.Body.ProgramID, "") == nil {
		return nil, huma.Error400BadRequest("program_id is not a program id or contract address")
	}
	// Cheap pre-check so a spent allowance does not cost a GitHub call; the
	// authoritative check runs inside CreateScanLimited's transaction.
	remaining, err := d.scansRemaining(ctx)
	if err != nil {
		return nil, fail(err)
	}
	if remaining == 0 {
		return nil, huma.Error429TooManyRequests("scan limit reached for today")
	}
	commit, err := d.GitHub.Resolve(ctx, ref, ref.Rev)
	if err != nil {
		return nil, fail(err)
	}
	r := req(ctx)
	sc, remaining, err := d.Store.CreateScanLimited(ctx, store.Scan{
		Token:           auth.RandomToken(16),
		Repo:            joinRepo(ref.FullName(), ref.Path),
		CommitSHA:       commit.SHA,
		ProgramID:       in.Body.ProgramID,
		IPHash:          d.hashKey("ip", r.IP),
		FingerprintHash: d.hashKey("fp", r.Fingerprint),
		ExpiresAt:       d.now().Add(ScanTTL),
	}, d.now().Add(-24*time.Hour), ScansPerDay)
	if err != nil {
		return nil, fail(err)
	}
	if d.Scans != nil {
		// Run inline when a background slot is free; otherwise the scan
		// stays queued for the worker loop.
		if err := d.Store.SetScanRunning(ctx, sc.ID); err == nil {
			sc.State = "running"
			if !d.background(func(ctx context.Context) { _ = d.Scans.Run(ctx, sc) }) {
				_ = d.Store.RequeueScan(ctx, sc.ID)
				sc.State = "queued"
			}
		}
	}
	return scanResult(d.toScan(sc, remaining)), nil
}

func (d *Deps) getScan(ctx context.Context, in *tokenPath) (*scanOut, error) {
	if err := d.ready(); err != nil {
		return nil, err
	}
	sc, err := d.Store.GetScanByToken(ctx, in.Token)
	if err != nil {
		return nil, fail(err)
	}
	remaining, err := d.scansRemaining(ctx)
	if err != nil {
		return nil, fail(err)
	}
	return scanResult(d.toScan(sc, remaining)), nil
}

func (d *Deps) getPublishedReport(ctx context.Context, in *slugPath) (*body[Report], error) {
	if err := d.ready(); err != nil {
		return nil, err
	}
	r, err := d.Store.GetReportBySlug(ctx, in.Slug)
	if err != nil {
		return nil, fail(err)
	}
	return &body[Report]{Body: toReport(r, false)}, nil
}

// joinRepo packs a subdirectory into the scans repo column as owner/name#path.
func joinRepo(full, path string) string {
	if path == "" {
		return full
	}
	return full + "#" + path
}

func cutRepo(s string) (string, string, bool) {
	for i := 0; i < len(s); i++ {
		if s[i] == '#' {
			return s[:i], s[i+1:], true
		}
	}
	return s, "", false
}
