package api

import (
	"context"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/forefy/vig/backend/internal/auth"
	"github.com/forefy/vig/backend/internal/core"
	"github.com/forefy/vig/backend/internal/mfa"
	"github.com/forefy/vig/backend/internal/store"
)

// RecoveryCodeCount is how many one-time codes a TOTP enrolment gets.
const RecoveryCodeCount = 8

type patchMeIn struct {
	Body struct {
		DisplayName string `json:"display_name,omitempty" maxLength:"60"`
	}
}

type createKeyIn struct {
	Body struct {
		Name string `json:"name" minLength:"1" maxLength:"60"`
	}
}

type installURL struct {
	Body struct {
		URL string `json:"url"`
	}
}

type totpCodeIn struct {
	Body struct {
		Code string `json:"code" minLength:"6" maxLength:"12" doc:"Six-digit authenticator code, or a recovery code where accepted"`
	}
}

type prefsIn struct {
	Body NotificationPrefs
}

func (d *Deps) registerMe(api huma.API) {
	huma.Register(api, withAuth(op("getMe", http.MethodGet, "/v1/me", "Current user", "me"), userAuth), d.getMe)
	huma.Register(api, withAuth(op(opEndImpersonation, http.MethodPost, "/v1/me/impersonation/end", "End an admin's view-as-user session early", "me"), userAuth), d.endImpersonation)
	huma.Register(api, withAuth(op("patchMe", http.MethodPatch, "/v1/me", "Update profile", "me"), userAuth), d.patchMe)
	huma.Register(api, withAuth(op("listIdentities", http.MethodGet, "/v1/me/identities", "Linked identities", "me"), userAuth), d.listIdentities)
	huma.Register(api, withAuth(op("unlinkIdentity", http.MethodDelete, "/v1/me/identities/{id}", "Unlink identity", "me"), userAuth), d.unlinkIdentity)
	huma.Register(api, withAuth(op("listAPIKeys", http.MethodGet, "/v1/me/api-keys", "API keys", "me"), userAuth), d.listAPIKeys)
	huma.Register(api, withAuth(op("createAPIKey", http.MethodPost, "/v1/me/api-keys", "Create API key", "me"), userAuth), d.createAPIKey)
	huma.Register(api, withAuth(op("revokeAPIKey", http.MethodDelete, "/v1/me/api-keys/{id}", "Revoke API key", "me"), userAuth), d.revokeAPIKey)
	huma.Register(api, withAuth(op("listInstallations", http.MethodGet, "/v1/me/github/installations", "GitHub App installations", "me"), userAuth), d.listInstallations)
	huma.Register(api, withAuth(op("installURL", http.MethodGet, "/v1/me/github/install-url", "Where to install the GitHub App", "me"), userAuth), d.installURL)
	huma.Register(api, withAuth(op("listNotifications", http.MethodGet, "/v1/me/notifications", "Notifications", "me"), userAuth), d.listNotifications)
	huma.Register(api, withAuth(op("readNotification", http.MethodPatch, "/v1/me/notifications/{id}/read", "Mark read", "me"), userAuth), d.readNotification)
	huma.Register(api, withAuth(op("listLedger", http.MethodGet, "/v1/me/ledger", "Credit ledger", "me"), userAuth), d.listLedger)
	huma.Register(api, withAuth(op("getPlan", http.MethodGet, "/v1/me/plan", "Active plan and next slot", "me"), userAuth), d.getPlan)
	huma.Register(api, withAuth(op("totpSetup", http.MethodPost, "/v1/me/totp/setup", "Start TOTP enrolment; returns the secret once", "me"), userAuth), d.totpSetup)
	huma.Register(api, withAuth(op("totpEnable", http.MethodPost, "/v1/me/totp/enable", "Confirm the first code and enable TOTP", "me"), userAuth), d.totpEnable)
	huma.Register(api, withAuth(op("totpVerify", http.MethodPost, "/v1/me/totp/verify", "Pass a TOTP check for this session (admin routes need one within 12h); a recovery code is accepted and consumed", "me"), userAuth), d.totpVerify)
	huma.Register(api, withAuth(op("totpRecovery", http.MethodPost, "/v1/me/totp/recovery", "Regenerate recovery codes (recent TOTP required)", "me"), userAuth), d.totpRecovery)
	huma.Register(api, withAuth(op("deleteMe", http.MethodDelete, "/v1/me", "Anonymise the account: identities, sessions and keys go; ledger and audits stay", "me"), userAuth), d.deleteMe)
	huma.Register(api, withAuth(op("setNotificationPrefs", http.MethodPatch, "/v1/me/notifications/prefs", "Notification preferences", "me"), userAuth), d.setNotificationPrefs)
	huma.Register(api, withAuth(op("listSessions", http.MethodGet, "/v1/me/sessions", "Live sessions", "me"), userAuth), d.listSessions)
	huma.Register(api, withAuth(op("revokeOtherSessions", http.MethodDelete, "/v1/me/sessions", "End every session but this one", "me"), userAuth), d.revokeOtherSessions)
	d.registerGitHub(api)
}

// me assembles the current user's view.
func (d *Deps) me(ctx context.Context, u store.User, sessionID string) (Me, error) {
	idents, err := d.Store.ListIdentities(ctx, u.ID)
	if err != nil {
		return Me{}, err
	}
	credits, err := d.Store.Balance(ctx, d.Store.Pool, u.ID)
	if err != nil {
		return Me{}, err
	}
	out := Me{User: toUser(u), Identities: toIdentities(idents), Credits: credits, TOTPEnabled: u.TOTPEnabled, NotifyEmail: u.NotifyEmail, RetentionDays: core.CloneRetentionDays}
	if plan, err := d.Store.GetPlan(ctx, u.ID); err == nil {
		out.Plan = toPlanState(plan, d.now())
	} else if !errors.Is(err, store.ErrNotFound) {
		return Me{}, err
	}
	if sessionID != "" {
		if se, err := d.Store.GetSession(ctx, sessionID); err == nil {
			out.TOTPVerifiedAt = se.TOTPAt
		}
	}
	if p := principal(ctx); p.Impersonation != nil {
		if imp, err := d.Store.GetImpersonation(ctx, p.Impersonation.ID); err == nil {
			s := toImpersonation(imp)
			out.Impersonation = &s
		}
	}
	if u.TOTPEnabled {
		if n, err := d.Store.CountRecoveryCodes(ctx, u.ID); err == nil {
			out.RecoveryCodesLeft = n
		}
	}
	if d.Config.DiscordGuildID != "" {
		out.DiscordInviteURL = d.Config.DiscordInviteURL
		member := false
		for _, id := range idents {
			if id.Provider != "discord" {
				continue
			}
			if d.Discord != nil && d.Discord.Configured() {
				if ok, err := d.Discord.MemberOf(ctx, d.Config.DiscordGuildID, id.ProviderID); err == nil {
					member = ok
				}
			}
			break
		}
		out.DiscordGuildMember = &member
	}
	return out, nil
}

func (d *Deps) deleteMe(ctx context.Context, _ *struct{}) (*cookiesOut, error) {
	u, err := d.sessionUser(ctx)
	if err != nil {
		return nil, err
	}
	if err := d.Store.DeleteAccount(ctx, u.ID); err != nil {
		return nil, fail(err)
	}
	return &cookiesOut{SetCookie: d.Signer.ClearCookies()}, nil
}

func (d *Deps) setNotificationPrefs(ctx context.Context, in *prefsIn) (*body[NotificationPrefs], error) {
	u, err := d.user(ctx)
	if err != nil {
		return nil, err
	}
	if err := d.Store.SetNotifyEmail(ctx, u.ID, in.Body.Email); err != nil {
		return nil, fail(err)
	}
	return &body[NotificationPrefs]{Body: NotificationPrefs{Email: in.Body.Email}}, nil
}

func (d *Deps) listSessions(ctx context.Context, _ *struct{}) (*body[list[Session]], error) {
	u, err := d.user(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := d.Store.ListSessions(ctx, u.ID)
	if err != nil {
		return nil, fail(err)
	}
	current := principal(ctx).SessionID
	out := make([]Session, 0, len(rows))
	for _, se := range rows {
		out = append(out, toSession(se, current))
	}
	return items(out), nil
}

func (d *Deps) revokeOtherSessions(ctx context.Context, _ *struct{}) (*struct{}, error) {
	u, err := d.sessionUser(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := d.Store.RevokeOtherSessions(ctx, u.ID, principal(ctx).SessionID); err != nil {
		return nil, fail(err)
	}
	return nil, nil
}

func (d *Deps) getMe(ctx context.Context, _ *struct{}) (*body[Me], error) {
	u, err := d.user(ctx)
	if err != nil {
		return nil, err
	}
	me, err := d.me(ctx, u, principal(ctx).SessionID)
	if err != nil {
		return nil, fail(err)
	}
	return &body[Me]{Body: me}, nil
}

func (d *Deps) patchMe(ctx context.Context, in *patchMeIn) (*body[Me], error) {
	u, err := d.user(ctx)
	if err != nil {
		return nil, err
	}
	if in.Body.DisplayName != "" {
		u, err = d.Store.UpdateProfile(ctx, u.ID, in.Body.DisplayName)
		if err != nil {
			return nil, fail(err)
		}
	}
	me, err := d.me(ctx, u, principal(ctx).SessionID)
	if err != nil {
		return nil, fail(err)
	}
	return &body[Me]{Body: me}, nil
}

func (d *Deps) listIdentities(ctx context.Context, _ *struct{}) (*body[list[Identity]], error) {
	u, err := d.user(ctx)
	if err != nil {
		return nil, err
	}
	idents, err := d.Store.ListIdentities(ctx, u.ID)
	if err != nil {
		return nil, fail(err)
	}
	return items(toIdentities(idents)), nil
}

func (d *Deps) unlinkIdentity(ctx context.Context, in *idPath) (*struct{}, error) {
	u, err := d.user(ctx)
	if err != nil {
		return nil, err
	}
	if err := d.Store.DeleteIdentity(ctx, u.ID, in.ID); err != nil {
		if errors.Is(err, store.ErrConflict) {
			return nil, huma.Error409Conflict("cannot unlink the last identity")
		}
		return nil, fail(err)
	}
	return nil, nil
}

func (d *Deps) listAPIKeys(ctx context.Context, _ *struct{}) (*body[list[APIKey]], error) {
	u, err := d.user(ctx)
	if err != nil {
		return nil, err
	}
	keys, err := d.Store.ListAPIKeys(ctx, u.ID)
	if err != nil {
		return nil, fail(err)
	}
	out := make([]APIKey, 0, len(keys))
	for _, k := range keys {
		out = append(out, toAPIKey(k))
	}
	return items(out), nil
}

func (d *Deps) createAPIKey(ctx context.Context, in *createKeyIn) (*body[APIKeyCreated], error) {
	u, err := d.user(ctx)
	if err != nil {
		return nil, err
	}
	key, prefix, hash := auth.MintAPIKey()
	k, err := d.Store.CreateAPIKey(ctx, u.ID, prefix, in.Body.Name, hash)
	if err != nil {
		return nil, fail(err)
	}
	return &body[APIKeyCreated]{Body: APIKeyCreated{APIKey: toAPIKey(k), Secret: key}}, nil
}

func (d *Deps) revokeAPIKey(ctx context.Context, in *idPath) (*struct{}, error) {
	u, err := d.user(ctx)
	if err != nil {
		return nil, err
	}
	if err := d.Store.RevokeAPIKey(ctx, u.ID, in.ID); err != nil {
		return nil, fail(err)
	}
	return nil, nil
}

func (d *Deps) githubAppReady() error {
	if d.Config.GitHubAppID == "" {
		return huma.Error503ServiceUnavailable("GitHub App not configured")
	}
	return nil
}

func (d *Deps) listInstallations(ctx context.Context, _ *struct{}) (*body[list[Installation]], error) {
	u, err := d.user(ctx)
	if err != nil {
		return nil, err
	}
	if err := d.githubAppReady(); err != nil {
		return nil, err
	}
	rows, err := d.Store.ListInstallations(ctx, u.ID)
	if err != nil {
		return nil, fail(err)
	}
	out := make([]Installation, 0, len(rows))
	for _, i := range rows {
		out = append(out, toInstallation(i))
	}
	return items(out), nil
}

func (d *Deps) installURL(ctx context.Context, _ *struct{}) (*installURL, error) {
	if _, err := d.sessionUser(ctx); err != nil {
		return nil, err
	}
	if err := d.githubAppReady(); err != nil {
		return nil, err
	}
	if d.Config.GitHubAppSlug == "" {
		return nil, huma.Error503ServiceUnavailable("GitHub App slug not configured")
	}
	// The state comes back on the setup redirect and binds the installation
	// to this user; GitHub passes it through untouched.
	state := d.Signer.EncodeState(auth.State{Nonce: auth.RandomToken(16), Mode: "install", UserID: principal(ctx).UserID})
	out := &installURL{}
	out.Body.URL = "https://github.com/apps/" + d.Config.GitHubAppSlug + "/installations/new?state=" + state
	return out, nil
}

func (d *Deps) listNotifications(ctx context.Context, _ *struct{}) (*body[list[Notification]], error) {
	u, err := d.user(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := d.Store.ListNotifications(ctx, u.ID)
	if err != nil {
		return nil, fail(err)
	}
	out := make([]Notification, 0, len(rows))
	for _, n := range rows {
		out = append(out, toNotification(n))
	}
	return items(out), nil
}

func (d *Deps) readNotification(ctx context.Context, in *idPath) (*struct{}, error) {
	u, err := d.user(ctx)
	if err != nil {
		return nil, err
	}
	if err := d.Store.MarkRead(ctx, u.ID, in.ID); err != nil {
		return nil, fail(err)
	}
	return nil, nil
}

func (d *Deps) listLedger(ctx context.Context, _ *struct{}) (*body[list[LedgerRow]], error) {
	u, err := d.user(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := d.Store.ListLedger(ctx, u.ID)
	if err != nil {
		return nil, fail(err)
	}
	out := make([]LedgerRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, toLedgerRow(r))
	}
	return items(out), nil
}

func (d *Deps) getPlan(ctx context.Context, _ *struct{}) (*body[PlanState], error) {
	u, err := d.user(ctx)
	if err != nil {
		return nil, err
	}
	plan, err := d.Store.GetPlan(ctx, u.ID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, huma.Error404NotFound("no active plan")
		}
		return nil, fail(err)
	}
	return &body[PlanState]{Body: *toPlanState(plan, d.now())}, nil
}

func (d *Deps) sessionUser(ctx context.Context) (store.User, error) {
	u, err := d.user(ctx)
	if err != nil {
		return u, err
	}
	if principal(ctx).Via != auth.ViaSession {
		return u, huma.Error403Forbidden("a browser session is required")
	}
	return u, nil
}

func (d *Deps) totpSetup(ctx context.Context, _ *struct{}) (*body[TOTPSetup], error) {
	u, err := d.sessionUser(ctx)
	if err != nil {
		return nil, err
	}
	if u.TOTPEnabled {
		return nil, huma.Error409Conflict("TOTP is already enabled")
	}
	secret := mfa.NewSecret()
	if err := d.Store.SetTOTPPending(ctx, u.ID, secret); err != nil {
		return nil, fail(err)
	}
	return &body[TOTPSetup]{Body: TOTPSetup{Secret: secret, OTPAuthURL: mfa.OTPAuthURL("vig", u.DisplayName, secret)}}, nil
}

func (d *Deps) totpEnable(ctx context.Context, in *totpCodeIn) (*body[TOTPStatus], error) {
	u, err := d.sessionUser(ctx)
	if err != nil {
		return nil, err
	}
	if u.TOTPEnabled {
		return nil, huma.Error409Conflict("TOTP is already enabled")
	}
	if u.TOTPSecret == "" || !mfa.Validate(u.TOTPSecret, in.Body.Code) {
		return nil, huma.Error401Unauthorized("invalid code")
	}
	if err := d.Store.EnableTOTP(ctx, u.ID); err != nil {
		return nil, fail(err)
	}
	codes, err := d.issueRecoveryCodes(ctx, u.ID)
	if err != nil {
		return nil, fail(err)
	}
	return d.stampTOTP(ctx, codes)
}

// issueRecoveryCodes replaces the user's recovery codes and returns the
// plaintext set, which is shown exactly once.
func (d *Deps) issueRecoveryCodes(ctx context.Context, userID string) ([]string, error) {
	codes, hashes := mfa.NewRecoveryCodes(RecoveryCodeCount)
	raw := make([][]byte, 0, len(hashes))
	for _, h := range hashes {
		b, err := hex.DecodeString(h)
		if err != nil {
			return nil, err
		}
		raw = append(raw, b)
	}
	if err := d.Store.ReplaceRecoveryCodes(ctx, userID, raw); err != nil {
		return nil, err
	}
	return codes, nil
}

// looksLikeRecoveryCode distinguishes a recovery code (xxxxx-xxxxx) from
// the six digits of an authenticator.
func looksLikeRecoveryCode(code string) bool {
	return strings.Contains(strings.TrimSpace(code), "-")
}

func (d *Deps) totpVerify(ctx context.Context, in *totpCodeIn) (*body[TOTPStatus], error) {
	u, err := d.sessionUser(ctx)
	if err != nil {
		return nil, err
	}
	if !u.TOTPEnabled {
		return nil, huma.Error401Unauthorized("invalid code")
	}
	if looksLikeRecoveryCode(in.Body.Code) {
		hash, err := hex.DecodeString(mfa.HashCode(in.Body.Code))
		if err != nil {
			return nil, huma.Error401Unauthorized("invalid code")
		}
		ok, err := d.Store.ConsumeRecoveryCode(ctx, u.ID, hash)
		if err != nil {
			return nil, fail(err)
		}
		if !ok {
			return nil, huma.Error401Unauthorized("invalid code")
		}
		return d.stampTOTP(ctx, nil)
	}
	if !mfa.Validate(u.TOTPSecret, in.Body.Code) {
		return nil, huma.Error401Unauthorized("invalid code")
	}
	return d.stampTOTP(ctx, nil)
}

// totpRecovery regenerates the recovery set. It needs a TOTP check on the
// session within the admin window, so a stolen cookie alone cannot mint
// codes.
func (d *Deps) totpRecovery(ctx context.Context, _ *struct{}) (*body[TOTPStatus], error) {
	u, err := d.sessionUser(ctx)
	if err != nil {
		return nil, err
	}
	if !u.TOTPEnabled {
		return nil, huma.Error409Conflict("TOTP is not enabled")
	}
	p := principal(ctx)
	if p.TOTPAt == nil || d.now().Sub(*p.TOTPAt) > auth.TOTPWindow {
		return nil, huma.Error403Forbidden("recent TOTP verification required")
	}
	codes, err := d.issueRecoveryCodes(ctx, u.ID)
	if err != nil {
		return nil, fail(err)
	}
	return &body[TOTPStatus]{Body: TOTPStatus{Enabled: true, VerifiedAt: p.TOTPAt, RecoveryCodes: codes}}, nil
}

func (d *Deps) stampTOTP(ctx context.Context, codes []string) (*body[TOTPStatus], error) {
	sid := principal(ctx).SessionID
	if err := d.Store.SetSessionTOTP(ctx, sid); err != nil {
		return nil, fail(err)
	}
	se, err := d.Store.GetSession(ctx, sid)
	if err != nil {
		return nil, fail(err)
	}
	return &body[TOTPStatus]{Body: TOTPStatus{Enabled: true, VerifiedAt: se.TOTPAt, RecoveryCodes: codes}}, nil
}

// opEndImpersonation is the one write a viewing-as session may perform.
const opEndImpersonation = "endImpersonation"

type endedOut struct {
	Body struct {
		Ended bool `json:"ended"`
	}
}

func (d *Deps) endImpersonation(ctx context.Context, _ *struct{}) (*endedOut, error) {
	p := principal(ctx)
	if p.Impersonation == nil {
		return nil, huma.Error409Conflict("not viewing as a user")
	}
	ended, err := d.Store.EndImpersonation(ctx, p.Impersonation.ID, p.Impersonation.AdminID)
	if err != nil {
		return nil, fail(err)
	}
	if ended {
		if _, err := d.Store.LogAdminAction(ctx, store.AdminAction{AdminID: p.Impersonation.AdminID, Kind: "impersonate.end", SubjectKind: "user", SubjectID: p.UserID, Payload: map[string]any{"impersonation_id": p.Impersonation.ID}}); err != nil {
			return nil, fail(err)
		}
	}
	out := &endedOut{}
	out.Body.Ended = ended
	return out, nil
}
