// Package auth holds session tokens, API keys, OAuth code flows and
// Sign-In-With-Solana. It stores nothing itself; the store keeps hashes.
package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

const (
	// AccessTTL is the life of a stateless access token.
	AccessTTL = 15 * time.Minute
	// RefreshTTL is how long a login lasts between refreshes.
	RefreshTTL = 30 * 24 * time.Hour
	// TOTPWindow is how recent a TOTP check must be for admin routes.
	TOTPWindow = 12 * time.Hour

	CookieAccess  = "vig_access"
	CookieRefresh = "vig_refresh"
	CookieOAuth   = "vig_oauth"

	// APIKeyPrefix starts every user API key.
	APIKeyPrefix = "vig_"
)

// Claims ride the access JWT.
type Claims struct {
	Sub  string `json:"sub"`
	Sid  string `json:"sid"`
	Role string `json:"role"`
	Typ  string `json:"typ"`
	Exp  int64  `json:"exp"`
	Iat  int64  `json:"iat"`
}

// Signer mints and verifies HS256 tokens with the session secret.
type Signer struct {
	secret []byte
	Secure bool // Secure cookie flag (https)
}

// NewSigner derives the signing key from SESSION_SECRET.
func NewSigner(secret string, secure bool) *Signer {
	sum := sha256.Sum256([]byte("vig-access:" + secret))
	return &Signer{secret: sum[:], Secure: secure}
}

func b64url(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// RandomToken returns n random bytes as hex.
func RandomToken(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// Hash is the stored form of any opaque token.
func Hash(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

// IssueAccess mints an access token for a user and session.
func (s *Signer) IssueAccess(userID, sessionID, role string) (string, time.Time) {
	now := time.Now()
	exp := now.Add(AccessTTL)
	header := b64url([]byte(`{"alg":"HS256","typ":"JWT"}`))
	claims, _ := json.Marshal(Claims{Sub: userID, Sid: sessionID, Role: role, Typ: "access", Exp: exp.Unix(), Iat: now.Unix()})
	signing := header + "." + b64url(claims)
	return signing + "." + b64url(s.mac(signing)), exp
}

// VerifyAccess checks the signature, type and expiry.
func (s *Signer) VerifyAccess(token string) (Claims, bool) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return Claims{}, false
	}
	if subtle.ConstantTimeCompare([]byte(parts[2]), []byte(b64url(s.mac(parts[0]+"."+parts[1])))) != 1 {
		return Claims{}, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return Claims{}, false
	}
	var c Claims
	if json.Unmarshal(raw, &c) != nil || c.Typ != "access" || time.Now().Unix() >= c.Exp {
		return Claims{}, false
	}
	return c, true
}

func (s *Signer) mac(msg string) []byte {
	m := hmac.New(sha256.New, s.secret)
	m.Write([]byte(msg))
	return m.Sum(nil)
}

// Sign returns a URL-safe HMAC of msg, for OAuth state and tweet nonces.
func (s *Signer) Sign(msg string) string { return b64url(s.mac(msg)) }

// SessionCookies returns the Set-Cookie values for a login or refresh.
func (s *Signer) SessionCookies(access string, accessExp time.Time, refresh string, refreshExp time.Time) []http.Cookie {
	return []http.Cookie{
		{Name: CookieAccess, Value: access, Path: "/", HttpOnly: true, Secure: s.Secure, SameSite: http.SameSiteLaxMode, Expires: accessExp},
		{Name: CookieRefresh, Value: refresh, Path: "/v1/auth", HttpOnly: true, Secure: s.Secure, SameSite: http.SameSiteLaxMode, Expires: refreshExp},
	}
}

// ClearCookies returns cookies that delete the session pair.
func (s *Signer) ClearCookies() []http.Cookie {
	return []http.Cookie{
		{Name: CookieAccess, Value: "", Path: "/", HttpOnly: true, Secure: s.Secure, SameSite: http.SameSiteLaxMode, MaxAge: -1},
		{Name: CookieRefresh, Value: "", Path: "/v1/auth", HttpOnly: true, Secure: s.Secure, SameSite: http.SameSiteLaxMode, MaxAge: -1},
	}
}

// ---- API keys ----

// MintAPIKey returns a new key, its display prefix and its hash.
func MintAPIKey() (key, prefix string, hash []byte) {
	key = APIKeyPrefix + RandomToken(24)
	return key, key[:12], Hash(key)
}

// IsAPIKey reports whether a bearer value looks like a user API key.
func IsAPIKey(bearer string) bool { return strings.HasPrefix(bearer, APIKeyPrefix) }

// Bearer extracts the Authorization bearer value.
func Bearer(header string) string {
	if strings.HasPrefix(header, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(header, "Bearer "))
	}
	return ""
}

// TweetNonce derives the stable per-user nonce "vg-XXXX".
func (s *Signer) TweetNonce(userID string) string {
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	mac := s.mac("tweet-nonce:" + userID)
	var b strings.Builder
	for i := 0; i < 4; i++ {
		b.WriteByte(alphabet[int(mac[i])%len(alphabet)])
	}
	return "vg-" + b.String()
}
