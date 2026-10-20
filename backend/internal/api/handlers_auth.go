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
	"github.com/forefy/vig/backend/internal/chain"
	"github.com/forefy/vig/backend/internal/store"
)

type providerStartIn struct {
	Provider string `path:"provider" enum:"google,github,discord"`
	Next     string `query:"next" doc:"Frontend path to return to"`
}

type callbackIn struct {
	Provider string      `path:"provider" enum:"google,github,discord"`
	Code     string      `query:"code"`
	State    string      `query:"state"`
	Error    string      `query:"error"`
	Cookie   http.Cookie `cookie:"vig_oauth"`
}

type redirect struct {
	Status    int
	Location  string        `header:"Location"`
	SetCookie []http.Cookie `header:"Set-Cookie"`
}

type siwsNonceIn struct {
	Body struct {
		PublicKey string `json:"public_key,omitempty" doc:"Wallet address the message is composed for"`
	}
}

type siwsNonceOut struct {
	Body struct {
		Nonce   string `json:"nonce"`
		Message string `json:"message" doc:"Full Sign-In-With-Solana message to sign"`
	}
}

type siwsVerifyIn struct {
	Body struct {
		PublicKey string `json:"public_key"`
		Signature string `json:"signature" doc:"base58"`
		Message   string `json:"message"`
	}
}

type meOut struct {
	Body      Me
	SetCookie []http.Cookie `header:"Set-Cookie"`
}

type cookiesOut struct {
	SetCookie []http.Cookie `header:"Set-Cookie"`
}

type refreshIn struct {
	Refresh http.Cookie `cookie:"vig_refresh"`
}

type devLoginIn struct {
	User string `query:"user" doc:"Seeded user's display name or id"`
	Next string `query:"next"`
}

func (d *Deps) registerAuth(api huma.API) {
	huma.Register(api, op("authStart", http.MethodGet, "/v1/auth/{provider}/start", "Begin OAuth", "auth"), d.authStart)
	huma.Register(api, op("authCallback", http.MethodGet, "/v1/auth/{provider}/callback", "OAuth callback", "auth"), d.authCallback)
	huma.Register(api, op("siwsNonce", http.MethodPost, "/v1/auth/siws/nonce", "Nonce for Sign-In-With-Solana", "auth"), d.siwsNonce)
	huma.Register(api, op("siwsVerify", http.MethodPost, "/v1/auth/siws/verify", "Verify wallet signature and start a session", "auth"), d.siwsVerify)
	huma.Register(api, op("refreshSession", http.MethodPost, "/v1/auth/refresh", "Rotate the refresh token and mint a new access token", "auth"), d.refreshSession)
	huma.Register(api, op("logout", http.MethodPost, "/v1/auth/logout", "End session", "auth"), d.logout)
}

// registerDevLogin adds the seeded-user login. Only called with VIG_ENV=dev.
func (d *Deps) registerDevLogin(api huma.API) {
	huma.Register(api, op("devLogin", http.MethodGet, "/v1/auth/dev/login", "Dev only: sign in as a seeded user", "auth"), d.devLogin)
}

// login opens a session for the user and returns the cookie pair.
func (d *Deps) login(ctx context.Context, u store.User) ([]http.Cookie, store.Session, error) {
	r := req(ctx)
	refresh := auth.RandomToken(32)
	se, err := d.Store.CreateSession(ctx, u.ID, auth.RefreshTTL, r.IP, r.UserAgent, auth.Hash(refresh))
	if err != nil {
		return nil, store.Session{}, err
	}
	access, accessExp := d.Signer.IssueAccess(u.ID, se.ID, u.Role)
	return d.Signer.SessionCookies(access, accessExp, refresh, se.ExpiresAt), se, nil
}

func (d *Deps) frontendPath(next string) string {
	if next == "" || !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") {
		next = "/app"
	}
	return d.Config.FrontendURL + next
}

func (d *Deps) loginError(code string) *redirect {
	return &redirect{Status: http.StatusFound, Location: d.Config.FrontendURL + "/login?error=" + url.QueryEscape(code)}
}

func (d *Deps) provider(name string) (auth.Provider, error) {
	p, ok := d.Providers[name]
	if !ok || !p.Configured() {
		return auth.Provider{}, huma.Error503ServiceUnavailable(name + " sign-in is not configured")
	}
	return p, nil
}

func (d *Deps) callbackURL(provider string) string {
	return d.Config.BackendURL + "/v1/auth/" + provider + "/callback"
}

func (d *Deps) authStart(ctx context.Context, in *providerStartIn) (*redirect, error) {
	if err := d.ready(); err != nil {
		return nil, err
	}
	p, err := d.provider(in.Provider)
	if err != nil {
		return nil, err
	}
	st := auth.State{Nonce: auth.RandomToken(16), Mode: "login", Next: in.Next}
	if pr := principal(ctx); pr.UserID != "" && pr.Via == auth.ViaSession {
		st.Mode, st.UserID = "link", pr.UserID
	}
	if in.Provider == "discord" && st.Mode != "link" {
		return nil, huma.Error401Unauthorized("sign in before linking Discord")
	}
	state := d.Signer.EncodeState(st)
	cookie := http.Cookie{Name: auth.CookieOAuth, Value: st.Nonce, Path: "/v1/auth", HttpOnly: true, Secure: d.Signer.Secure, SameSite: http.SameSiteLaxMode, MaxAge: 600}
	return &redirect{Status: http.StatusFound, Location: p.AuthorizeURL(d.callbackURL(in.Provider), state), SetCookie: []http.Cookie{cookie}}, nil
}

func (d *Deps) authCallback(ctx context.Context, in *callbackIn) (*redirect, error) {
	if err := d.ready(); err != nil {
		return nil, err
	}
	p, err := d.provider(in.Provider)
	if err != nil {
		return nil, err
	}
	if in.Error != "" {
		return d.loginError(in.Error), nil
	}
	st, err := d.Signer.DecodeState(in.State)
	if err != nil || in.Cookie.Value == "" || st.Nonce != in.Cookie.Value {
		return nil, huma.Error400BadRequest("invalid or expired OAuth state")
	}
	if in.Code == "" {
		return nil, huma.Error400BadRequest("missing code")
	}
	profile, err := p.Exchange(ctx, d.httpClient(), in.Code, d.callbackURL(in.Provider))
	if err != nil {
		return d.loginError("exchange_failed"), nil
	}
	clear := http.Cookie{Name: auth.CookieOAuth, Value: "", Path: "/v1/auth", HttpOnly: true, Secure: d.Signer.Secure, SameSite: http.SameSiteLaxMode, MaxAge: -1}
	if st.Mode == "link" {
		if _, err := d.Store.AddIdentity(ctx, d.Store.Pool, st.UserID, in.Provider, profile.ProviderID, profile.Email, profile.Handle); err != nil {
			if errors.Is(err, store.ErrConflict) {
				return &redirect{Status: http.StatusFound, Location: d.frontendPath(st.Next) + "?error=identity_taken", SetCookie: []http.Cookie{clear}}, nil
			}
			return nil, fail(err)
		}
		return &redirect{Status: http.StatusFound, Location: d.frontendPath(st.Next), SetCookie: []http.Cookie{clear}}, nil
	}
	if in.Provider == "discord" {
		return d.loginError("discord_link_only"), nil
	}
	u, _, err := d.Store.FindOrCreateUser(ctx, in.Provider, profile.ProviderID, profile.Email, profile.Handle, profile.Name, profile.AvatarURL)
	if err != nil {
		return nil, fail(err)
	}
	cookies, _, err := d.login(ctx, u)
	if err != nil {
		return nil, fail(err)
	}
	return &redirect{Status: http.StatusFound, Location: d.frontendPath(st.Next), SetCookie: append(cookies, clear)}, nil
}

func (d *Deps) siwsDomain() (string, string) {
	u, err := url.Parse(d.Config.FrontendURL)
	if err != nil || u.Host == "" {
		return "localhost", d.Config.FrontendURL
	}
	return u.Host, d.Config.FrontendURL
}

func (d *Deps) siwsNonce(ctx context.Context, in *siwsNonceIn) (*siwsNonceOut, error) {
	if err := d.ready(); err != nil {
		return nil, err
	}
	if in.Body.PublicKey != "" && !chain.ValidPubkey(in.Body.PublicKey) {
		return nil, huma.Error400BadRequest("public_key is not a Solana address")
	}
	nonce := auth.RandomToken(12)
	if err := d.Store.PutSIWSNonce(ctx, nonce, 10*time.Minute); err != nil {
		return nil, fail(err)
	}
	domain, uri := d.siwsDomain()
	out := &siwsNonceOut{}
	out.Body.Nonce = nonce
	out.Body.Message = auth.SIWSMessage(domain, uri, in.Body.PublicKey, nonce, d.now())
	return out, nil
}

func (d *Deps) siwsVerify(ctx context.Context, in *siwsVerifyIn) (*meOut, error) {
	if err := d.ready(); err != nil {
		return nil, err
	}
	if !chain.ValidPubkey(in.Body.PublicKey) {
		return nil, huma.Error400BadRequest("public_key is not a Solana address")
	}
	fields, err := auth.ParseSIWS(in.Body.Message)
	if err != nil {
		return nil, huma.Error400BadRequest(err.Error())
	}
	domain, _ := d.siwsDomain()
	if fields.Domain != domain {
		return nil, huma.Error400BadRequest("message domain does not match")
	}
	if fields.Address != in.Body.PublicKey {
		return nil, huma.Error400BadRequest("message address does not match public_key")
	}
	sig, err := auth.DecodeSignature(in.Body.Signature)
	if err != nil {
		return nil, huma.Error400BadRequest(err.Error())
	}
	if err := auth.VerifyEd25519(in.Body.PublicKey, []byte(in.Body.Message), sig); err != nil {
		return nil, huma.Error401Unauthorized(err.Error())
	}
	ok, err := d.Store.ConsumeSIWSNonce(ctx, fields.Nonce)
	if err != nil {
		return nil, fail(err)
	}
	if !ok {
		return nil, huma.Error400BadRequest("nonce unknown or expired")
	}
	pr := principal(ctx)
	var u store.User
	if pr.UserID != "" && pr.Via == auth.ViaSession {
		// Signed in already: link the wallet to this account.
		if _, err := d.Store.AddIdentity(ctx, d.Store.Pool, pr.UserID, "solana", in.Body.PublicKey, "", in.Body.PublicKey); err != nil {
			if errors.Is(err, store.ErrConflict) {
				return nil, huma.Error409Conflict("wallet is linked to another account")
			}
			return nil, fail(err)
		}
		u, err = d.Store.GetUser(ctx, pr.UserID)
		if err != nil {
			return nil, fail(err)
		}
		me, err := d.me(ctx, u, pr.SessionID)
		if err != nil {
			return nil, fail(err)
		}
		return &meOut{Body: me}, nil
	}
	u, _, err = d.Store.FindOrCreateUser(ctx, "solana", in.Body.PublicKey, "", in.Body.PublicKey, shortKey(in.Body.PublicKey), "")
	if err != nil {
		return nil, fail(err)
	}
	cookies, se, err := d.login(ctx, u)
	if err != nil {
		return nil, fail(err)
	}
	me, err := d.me(ctx, u, se.ID)
	if err != nil {
		return nil, fail(err)
	}
	return &meOut{Body: me, SetCookie: cookies}, nil
}

func shortKey(k string) string {
	if len(k) > 8 {
		return k[:4] + ".." + k[len(k)-4:]
	}
	return k
}

func (d *Deps) refreshSession(ctx context.Context, in *refreshIn) (*cookiesOut, error) {
	if err := d.ready(); err != nil {
		return nil, err
	}
	if in.Refresh.Value == "" {
		return nil, huma.Error401Unauthorized("no refresh token")
	}
	next := auth.RandomToken(32)
	se, err := d.Store.RotateRefresh(ctx, auth.Hash(in.Refresh.Value), auth.Hash(next), auth.RefreshTTL)
	if err != nil {
		return &cookiesOut{SetCookie: d.Signer.ClearCookies()}, huma.Error401Unauthorized("refresh token invalid")
	}
	u, err := d.Store.GetUser(ctx, se.UserID)
	if err != nil {
		return nil, fail(err)
	}
	access, accessExp := d.Signer.IssueAccess(u.ID, se.ID, u.Role)
	return &cookiesOut{SetCookie: d.Signer.SessionCookies(access, accessExp, next, se.ExpiresAt)}, nil
}

func (d *Deps) logout(ctx context.Context, _ *struct{}) (*cookiesOut, error) {
	if err := d.ready(); err != nil {
		return nil, err
	}
	if p := principal(ctx); p.SessionID != "" {
		if err := d.Store.RevokeSession(ctx, p.SessionID); err != nil {
			return nil, fail(err)
		}
	}
	return &cookiesOut{SetCookie: d.Signer.ClearCookies()}, nil
}

// devLogin signs in as a seeded user. Registered only with VIG_ENV=dev; a
// user whose TOTP is enrolled also gets the session's TOTP stamp so the
// admin routes work in the dev UI without an authenticator.
func (d *Deps) devLogin(ctx context.Context, in *devLoginIn) (*redirect, error) {
	if err := d.ready(); err != nil {
		return nil, err
	}
	if d.Config.Env != "dev" {
		return nil, huma.Error404NotFound("not found")
	}
	u, err := d.Store.GetUser(ctx, in.User)
	if err != nil {
		u, err = d.Store.FindUserByDisplayName(ctx, in.User)
	}
	if err != nil {
		return nil, fail(err)
	}
	cookies, se, err := d.login(ctx, u)
	if err != nil {
		return nil, fail(err)
	}
	if u.TOTPEnabled {
		if err := d.Store.SetSessionTOTP(ctx, se.ID); err != nil {
			return nil, fail(err)
		}
	}
	return &redirect{Status: http.StatusFound, Location: d.frontendPath(in.Next), SetCookie: cookies}, nil
}
