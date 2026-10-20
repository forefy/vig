package auth

import (
	"context"
	"time"
)

// Via says how a request authenticated.
type Via string

const (
	ViaSession  Via = "session"
	ViaAPIKey   Via = "api_key"
	ViaOperator Via = "operator"
)

// Impersonation marks a session an admin has switched to view as another
// user. The principal is the target; the admin behind it is recorded here.
// Such a principal is read-only and never an admin.
type Impersonation struct {
	ID        string
	AdminID   string
	ExpiresAt time.Time
}

// Principal is the resolved caller of a request.
type Principal struct {
	UserID    string
	SessionID string
	Role      string
	TOTPAt    *time.Time
	Via       Via

	Impersonation *Impersonation

	// Operator tokens carry no user; they carry a token id and, once a job
	// is claimed, the audit the token is bound to.
	OperatorTokenID string
	OperatorAuditID string
}

// IsAdmin reports whether the principal may use admin routes: admin role on
// a session that passed TOTP within the window.
func (p Principal) IsAdmin(now time.Time) bool {
	return p.Impersonation == nil && p.Via == ViaSession && p.Role == "admin" && p.TOTPAt != nil && now.Sub(*p.TOTPAt) < TOTPWindow
}

type ctxKey struct{}

// WithPrincipal stores the principal in the context.
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, ctxKey{}, p)
}

// FromContext returns the principal, if any.
func FromContext(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(ctxKey{}).(Principal)
	return p, ok
}
