package api

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/forefy/vig/backend/internal/auth"
	"github.com/forefy/vig/backend/internal/github"
	"github.com/forefy/vig/backend/internal/store"
)

// GitHub App routes. Everything here answers 503 until GITHUB_APP_ID and
// GITHUB_APP_PRIVATE_KEY are set; the webhook additionally needs
// GITHUB_APP_WEBHOOK_SECRET. Installation tokens are minted per use and
// never stored.

type webhookIn struct {
	Event     string `header:"X-GitHub-Event"`
	Signature string `header:"X-Hub-Signature-256"`
	RawBody   []byte
}

type setupIn struct {
	InstallationID int64  `query:"installation_id"`
	SetupAction    string `query:"setup_action"`
	State          string `query:"state"`
}

func (d *Deps) registerGitHub(api huma.API) {
	huma.Register(api, op("githubWebhook", http.MethodPost, "/v1/github/webhook", "GitHub App installation events", "github"), d.githubWebhook)
	huma.Register(api, op("githubSetup", http.MethodGet, "/v1/github/setup", "Post-install redirect: bind the installation to the signed-in user", "github"), d.githubSetup)
}

// appReady reports whether the GitHub App can mint tokens.
func (d *Deps) appReady() error {
	if d.App == nil || !d.App.Configured() {
		return huma.Error503ServiceUnavailable("GitHub App not configured")
	}
	return nil
}

// installationToken mints a one-hour token scoped to the target's repository
// through the installation the target was created with.
func (d *Deps) installationToken(ctx context.Context, t store.Target) (github.InstallationToken, error) {
	if err := d.appReady(); err != nil {
		return github.InstallationToken{}, err
	}
	inst, err := d.Store.GetInstallationRow(ctx, t.InstallationID)
	if err != nil {
		return github.InstallationToken{}, huma.Error409Conflict("the installation this target was created with is gone")
	}
	if inst.Status != "active" {
		return github.InstallationToken{}, huma.Error409Conflict("installation is " + inst.Status)
	}
	tok, err := d.App.MintToken(ctx, inst.InstallationID, t.Repo)
	if err != nil {
		return github.InstallationToken{}, huma.Error502BadGateway("GitHub App token: " + err.Error())
	}
	return tok, nil
}

// sourceToken returns the token to fetch a target's source with: empty for
// public repositories, a fresh installation token for private ones.
func (d *Deps) sourceToken(ctx context.Context, t store.Target) (string, error) {
	if t.SourceKind != "installation" {
		return "", nil
	}
	tok, err := d.installationToken(ctx, t)
	if err != nil {
		return "", err
	}
	return tok.Token, nil
}

func (d *Deps) githubWebhook(ctx context.Context, in *webhookIn) (*struct{}, error) {
	if err := d.ready(); err != nil {
		return nil, err
	}
	if d.App == nil || d.App.WebhookSecret == "" {
		return nil, huma.Error503ServiceUnavailable("GitHub App webhook not configured")
	}
	if !d.App.VerifyWebhook(in.RawBody, in.Signature) {
		return nil, huma.Error401Unauthorized("bad signature")
	}
	if in.Event != "installation" {
		return nil, nil // ping and everything else are acknowledged and ignored
	}
	ev, err := github.ParseInstallationEvent(in.RawBody)
	if err != nil {
		return nil, huma.Error400BadRequest(err.Error())
	}
	status, ok := github.StatusForAction(ev.Action)
	if !ok {
		return nil, nil
	}
	// The webhook does not know which vig user installed the app; the row
	// is created unowned and the setup redirect binds it.
	if _, err := d.Store.UpsertInstallation(ctx, store.Installation{InstallationID: ev.Installation.ID, AccountLogin: ev.Installation.AccountLogin, AccountType: ev.Installation.AccountType, Status: status}); err != nil {
		return nil, fail(err)
	}
	slog.Info("github installation", "action", ev.Action, "installation", ev.Installation.ID, "account", ev.Installation.AccountLogin)
	return nil, nil
}

// githubSetup is where GitHub sends the browser after the app is installed.
// The signed state issued by installURL proves the flow started from this
// user's session; the installation is then looked up at GitHub (so a missed
// webhook does not matter) and bound to the user.
func (d *Deps) githubSetup(ctx context.Context, in *setupIn) (*redirect, error) {
	if err := d.ready(); err != nil {
		return nil, err
	}
	if err := d.appReady(); err != nil {
		return nil, err
	}
	dest := d.Config.FrontendURL + "/app/settings/identities"
	p := principal(ctx)
	if p.UserID == "" || p.Via != auth.ViaSession {
		return &redirect{Status: http.StatusFound, Location: d.Config.FrontendURL + "/login?next=" + "/app/settings/identities"}, nil
	}
	st, err := d.Signer.DecodeState(in.State)
	if err != nil || st.Mode != "install" || st.UserID != p.UserID {
		return &redirect{Status: http.StatusFound, Location: dest + "?error=bad_state"}, nil
	}
	if in.InstallationID == 0 {
		return &redirect{Status: http.StatusFound, Location: dest + "?error=no_installation"}, nil
	}
	info, err := d.App.Installation(ctx, in.InstallationID)
	if err != nil {
		return &redirect{Status: http.StatusFound, Location: dest + "?error=installation_unknown"}, nil
	}
	status := "active"
	if info.Suspended {
		status = "pending"
	}
	if _, err := d.Store.UpsertInstallation(ctx, store.Installation{UserID: p.UserID, InstallationID: info.ID, AccountLogin: info.AccountLogin, AccountType: info.AccountType, Status: status}); err != nil {
		return nil, fail(err)
	}
	return &redirect{Status: http.StatusFound, Location: dest}, nil
}
