package api

import (
	"context"
	"net"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/forefy/vig/backend/internal/auth"
	"github.com/forefy/vig/backend/internal/store"
)

// request is what handlers need from the raw request beyond their typed
// input: the client address, the anonymous fingerprint and the bearer.
type request struct {
	IP          string
	Fingerprint string
	UserAgent   string
	Bearer      string
}

type requestKey struct{}

func req(ctx context.Context) request {
	r, _ := ctx.Value(requestKey{}).(request)
	return r
}

// resolve identifies the caller from an API key, an operator token or the
// access cookie. It never fails the request; enforcement is per operation.
func (d *Deps) resolve(ctx huma.Context) (auth.Principal, bool) {
	return d.resolveFrom(ctx.Context(), ctx.Header, func(name string) string {
		c, err := huma.ReadCookie(ctx, name)
		if err != nil {
			return ""
		}
		return c.Value
	})
}

// resolveRequest is resolve for plain net/http handlers such as the spec.
func (d *Deps) resolveRequest(r *http.Request) (auth.Principal, bool) {
	return d.resolveFrom(r.Context(), r.Header.Get, func(name string) string {
		c, err := r.Cookie(name)
		if err != nil {
			return ""
		}
		return c.Value
	})
}

func (d *Deps) resolveFrom(rctx context.Context, header func(string) string, cookie func(string) string) (auth.Principal, bool) {
	if d.Store == nil {
		return auth.Principal{}, false
	}
	if bearer := auth.Bearer(header("Authorization")); bearer != "" {
		if auth.IsAPIKey(bearer) {
			u, err := d.Store.UserByAPIKeyHash(rctx, auth.Hash(bearer))
			if err != nil {
				return auth.Principal{}, false
			}
			return auth.Principal{UserID: u.ID, Role: u.Role, Via: auth.ViaAPIKey}, true
		}
		t, err := d.Store.OperatorTokenByHash(rctx, auth.Hash(bearer))
		if err != nil {
			return auth.Principal{}, false
		}
		return auth.Principal{Via: auth.ViaOperator, OperatorTokenID: t.ID, OperatorAuditID: t.AuditID}, true
	}
	access := cookie(auth.CookieAccess)
	if access == "" {
		return auth.Principal{}, false
	}
	claims, ok := d.Signer.VerifyAccess(access)
	if !ok {
		return auth.Principal{}, false
	}
	se, live := d.Store.SessionLive(rctx, claims.Sid)
	if !live || se.UserID != claims.Sub {
		return auth.Principal{}, false
	}
	u, err := d.Store.GetUser(rctx, se.UserID)
	if err != nil {
		return auth.Principal{}, false
	}
	// An admin session with a live grant resolves to the target user. The
	// grant is checked on every request, so ending it takes effect at once.
	if u.Role == "admin" {
		if imp, live := d.Store.LiveImpersonation(rctx, se.ID); live {
			t, err := d.Store.GetUser(rctx, imp.TargetUserID)
			if err != nil {
				return auth.Principal{}, false
			}
			return auth.Principal{UserID: t.ID, SessionID: se.ID, Role: t.Role, Via: auth.ViaSession,
				Impersonation: &auth.Impersonation{ID: imp.ID, AdminID: imp.AdminID, ExpiresAt: imp.ExpiresAt}}, true
		}
	}
	return auth.Principal{UserID: u.ID, SessionID: se.ID, Role: u.Role, TOTPAt: se.TOTPAt, Via: auth.ViaSession}, true
}

// authMiddleware stores the request facts, resolves the principal and
// enforces the operation's declared security schemes.
func (d *Deps) authMiddleware(ctx huma.Context, next func(huma.Context)) {
	ctx = huma.WithValue(ctx, requestKey{}, request{
		IP:          clientIP(ctx.RemoteAddr()),
		Fingerprint: strings.TrimSpace(ctx.Header("X-Fingerprint")),
		UserAgent:   ctx.Header("User-Agent"),
		Bearer:      auth.Bearer(ctx.Header("Authorization")),
	})
	p, ok := d.resolve(ctx)
	if ok {
		ctx = huma.WithValue(ctx, principalKey{}, p)
	}
	sec := ctx.Operation().Security
	if len(sec) == 0 {
		next(ctx)
		return
	}
	ctx.SetHeader("Cache-Control", "private, no-store")
	if d.Store == nil {
		_ = huma.WriteErr(d.api, ctx, 503, "database not configured")
		return
	}
	var wantUser, wantOperator, wantAdmin bool
	for _, req := range sec {
		for scheme := range req {
			switch scheme {
			case secSession, secAPIKey:
				wantUser = true
			case secOperator:
				wantOperator = true
			case secAdmin:
				wantAdmin = true
			}
		}
	}
	// Viewing as a user is read-only: no admin routes, no writes, except
	// ending the view itself.
	if ok && p.Impersonation != nil {
		if wantAdmin {
			_ = huma.WriteErr(d.api, ctx, 403, "admin routes are closed while viewing as a user")
			return
		}
		if m := ctx.Method(); m != http.MethodGet && m != http.MethodHead && ctx.Operation().OperationID != opEndImpersonation {
			_ = huma.WriteErr(d.api, ctx, 403, "read-only while viewing as a user")
			return
		}
	}
	switch {
	case wantAdmin:
		if !ok || p.Via != auth.ViaSession {
			_ = huma.WriteErr(d.api, ctx, 401, "sign in required")
			return
		}
		if p.Role != "admin" {
			_ = huma.WriteErr(d.api, ctx, 403, "admin role required")
			return
		}
		if !p.IsAdmin(d.now()) {
			_ = huma.WriteErr(d.api, ctx, 403, "recent TOTP verification required")
			return
		}
	case wantOperator:
		if !ok || p.Via != auth.ViaOperator {
			_ = huma.WriteErr(d.api, ctx, 401, "operator token required")
			return
		}
	case wantUser:
		if !ok || (p.Via != auth.ViaSession && p.Via != auth.ViaAPIKey) {
			_ = huma.WriteErr(d.api, ctx, 401, "sign in required")
			return
		}
	}
	next(ctx)
}

type principalKey struct{}

func principal(ctx context.Context) auth.Principal {
	p, _ := ctx.Value(principalKey{}).(auth.Principal)
	return p
}

// user loads the calling user's row.
func (d *Deps) user(ctx context.Context) (store.User, error) {
	p := principal(ctx)
	if p.UserID == "" {
		return store.User{}, huma.Error401Unauthorized("sign in required")
	}
	u, err := d.Store.GetUser(ctx, p.UserID)
	if err != nil {
		return store.User{}, huma.Error401Unauthorized("sign in required")
	}
	return u, nil
}

// clientIP strips the port from the remote address; chi's RealIP has
// already applied X-Forwarded-For.
func clientIP(remote string) string {
	if host, _, err := net.SplitHostPort(remote); err == nil {
		return host
	}
	return strings.TrimSpace(remote)
}
