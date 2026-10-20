package github

import (
	"bytes"
	"context"
	"crypto"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// ErrAppNotConfigured is returned when the GitHub App id or key is missing.
var ErrAppNotConfigured = errors.New("GitHub App not configured")

// App mints GitHub App JWTs and per-installation access tokens. Tokens are
// never stored; every job mints its own.
type App struct {
	ID            string
	key           *rsa.PrivateKey
	WebhookSecret string
	API           string
	HTTP          *http.Client
}

// NewApp parses the PEM private key. Escaped newlines ("\n") are accepted so
// the key can live in a single-line environment variable. A missing id or
// key yields an App whose Configured() is false.
func NewApp(api, appID, privateKeyPEM, webhookSecret string) (*App, error) {
	a := &App{ID: appID, WebhookSecret: webhookSecret, API: strings.TrimRight(api, "/"), HTTP: &http.Client{Timeout: 30 * time.Second}}
	if appID == "" || privateKeyPEM == "" {
		return a, nil
	}
	raw := strings.ReplaceAll(privateKeyPEM, `\n`, "\n")
	block, _ := pem.Decode([]byte(raw))
	if block == nil {
		return nil, errors.New("GITHUB_APP_PRIVATE_KEY is not PEM")
	}
	if k, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		a.key = k
		return a, nil
	}
	k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("GITHUB_APP_PRIVATE_KEY: %w", err)
	}
	rk, ok := k.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("GITHUB_APP_PRIVATE_KEY is not an RSA key")
	}
	a.key = rk
	return a, nil
}

// Configured reports whether tokens can be minted.
func (a *App) Configured() bool { return a != nil && a.ID != "" && a.key != nil }

// JWT signs a short-lived RS256 app token (issued a minute in the past to
// absorb clock skew, valid for nine minutes).
func (a *App) JWT(now time.Time) (string, error) {
	if !a.Configured() {
		return "", ErrAppNotConfigured
	}
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	claims, _ := json.Marshal(map[string]any{"iat": now.Add(-60 * time.Second).Unix(), "exp": now.Add(9 * time.Minute).Unix(), "iss": a.ID})
	signing := header + "." + base64.RawURLEncoding.EncodeToString(claims)
	sum := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, a.key, crypto.SHA256, sum[:])
	if err != nil {
		return "", err
	}
	return signing + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

func (a *App) do(ctx context.Context, method, path, bearer string, body any, out any) error {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, a.API+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "vig")
	req.Header.Set("Authorization", "Bearer "+bearer)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := a.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		if resp.StatusCode == http.StatusNotFound {
			return ErrNotFound
		}
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("github app %s: %s %s", path, resp.Status, bytes.TrimSpace(msg))
	}
	if out != nil {
		return json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(out)
	}
	return nil
}

// InstallationToken is a one-hour token scoped to one repository.
type InstallationToken struct {
	Token     string
	ExpiresAt time.Time
}

// MintToken creates an installation access token limited to the named
// repository (owner/name) with read access to contents and metadata.
func (a *App) MintToken(ctx context.Context, installationID int64, repoFullName string) (InstallationToken, error) {
	jwt, err := a.JWT(time.Now())
	if err != nil {
		return InstallationToken{}, err
	}
	_, name, ok := strings.Cut(repoFullName, "/")
	if !ok {
		return InstallationToken{}, errors.New("repo must be owner/name")
	}
	var out struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	body := map[string]any{"repositories": []string{name}, "permissions": map[string]string{"contents": "read", "metadata": "read"}}
	if err := a.do(ctx, http.MethodPost, "/app/installations/"+strconv.FormatInt(installationID, 10)+"/access_tokens", jwt, body, &out); err != nil {
		return InstallationToken{}, err
	}
	if out.Token == "" {
		return InstallationToken{}, errors.New("github app: empty installation token")
	}
	return InstallationToken{Token: out.Token, ExpiresAt: out.ExpiresAt}, nil
}

// InstallationInfo is what GitHub reports about an installation.
type InstallationInfo struct {
	ID           int64
	AccountLogin string
	AccountType  string
	Suspended    bool
}

type installationJSON struct {
	ID      int64 `json:"id"`
	Account struct {
		Login string `json:"login"`
		Type  string `json:"type"`
	} `json:"account"`
	SuspendedAt *string `json:"suspended_at"`
}

func (i installationJSON) info() InstallationInfo {
	return InstallationInfo{ID: i.ID, AccountLogin: i.Account.Login, AccountType: i.Account.Type, Suspended: i.SuspendedAt != nil}
}

// Installation fetches one installation by id.
func (a *App) Installation(ctx context.Context, installationID int64) (InstallationInfo, error) {
	jwt, err := a.JWT(time.Now())
	if err != nil {
		return InstallationInfo{}, err
	}
	var out installationJSON
	if err := a.do(ctx, http.MethodGet, "/app/installations/"+strconv.FormatInt(installationID, 10), jwt, nil, &out); err != nil {
		return InstallationInfo{}, err
	}
	return out.info(), nil
}

// Installations lists the app's installations (first page of 100).
func (a *App) Installations(ctx context.Context) ([]InstallationInfo, error) {
	jwt, err := a.JWT(time.Now())
	if err != nil {
		return nil, err
	}
	var out []installationJSON
	if err := a.do(ctx, http.MethodGet, "/app/installations?per_page=100", jwt, nil, &out); err != nil {
		return nil, err
	}
	infos := make([]InstallationInfo, 0, len(out))
	for _, i := range out {
		infos = append(infos, i.info())
	}
	return infos, nil
}

// VerifyWebhook checks X-Hub-Signature-256 over the raw body.
func (a *App) VerifyWebhook(body []byte, signatureHeader string) bool {
	if a == nil || a.WebhookSecret == "" {
		return false
	}
	sig, ok := strings.CutPrefix(signatureHeader, "sha256=")
	if !ok {
		return false
	}
	want, err := hex.DecodeString(sig)
	if err != nil {
		return false
	}
	m := hmac.New(sha256.New, []byte(a.WebhookSecret))
	m.Write(body)
	return hmac.Equal(m.Sum(nil), want)
}

// WebhookInstallation is the part of an installation event vig acts on.
type WebhookInstallation struct {
	Action       string
	Installation InstallationInfo
}

// ParseInstallationEvent decodes an "installation" webhook payload.
func ParseInstallationEvent(body []byte) (WebhookInstallation, error) {
	var ev struct {
		Action       string           `json:"action"`
		Installation installationJSON `json:"installation"`
	}
	if err := json.Unmarshal(body, &ev); err != nil {
		return WebhookInstallation{}, err
	}
	if ev.Installation.ID == 0 {
		return WebhookInstallation{}, errors.New("no installation in event")
	}
	return WebhookInstallation{Action: ev.Action, Installation: ev.Installation.info()}, nil
}

// StatusForAction maps a webhook action to the stored installation status.
func StatusForAction(action string) (string, bool) {
	switch action {
	case "created", "unsuspend", "new_permissions_accepted":
		return "active", true
	case "suspend":
		return "pending", true
	case "deleted":
		return "removed", true
	}
	return "", false
}
