package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// ErrNotConfigured is returned when a provider has no client id.
var ErrNotConfigured = errors.New("provider not configured")

// Provider is one OAuth code-flow provider.
type Provider struct {
	Name         string
	ClientID     string
	ClientSecret string
	AuthURL      string
	TokenURL     string
	Scopes       []string
	fetch        func(ctx context.Context, c *http.Client, token string) (Profile, error)
}

// Profile is what every provider resolves to.
type Profile struct {
	ProviderID string
	Email      string
	Handle     string
	Name       string
	AvatarURL  string
}

// Configured reports whether the provider can be used.
func (p Provider) Configured() bool { return p.ClientID != "" && p.ClientSecret != "" }

// Providers builds the three providers from credentials.
func Providers(googleID, googleSecret, githubID, githubSecret, discordID, discordSecret string) map[string]Provider {
	return map[string]Provider{
		"google": {Name: "google", ClientID: googleID, ClientSecret: googleSecret,
			AuthURL: "https://accounts.google.com/o/oauth2/v2/auth", TokenURL: "https://oauth2.googleapis.com/token",
			Scopes: []string{"openid", "email", "profile"}, fetch: fetchGoogle},
		"github": {Name: "github", ClientID: githubID, ClientSecret: githubSecret,
			AuthURL: "https://github.com/login/oauth/authorize", TokenURL: "https://github.com/login/oauth/access_token",
			Scopes: []string{"read:user", "user:email"}, fetch: fetchGitHub},
		"discord": {Name: "discord", ClientID: discordID, ClientSecret: discordSecret,
			AuthURL: "https://discord.com/oauth2/authorize", TokenURL: "https://discord.com/api/oauth2/token",
			Scopes: []string{"identify"}, fetch: fetchDiscord},
	}
}

// State is what the OAuth state parameter carries, signed.
type State struct {
	Nonce  string `json:"n"`
	Mode   string `json:"m"` // login or link
	UserID string `json:"u,omitempty"`
	Next   string `json:"x,omitempty"`
	Exp    int64  `json:"e"`
}

// EncodeState signs a state.
func (s *Signer) EncodeState(st State) string {
	st.Exp = time.Now().Add(10 * time.Minute).Unix()
	raw, _ := json.Marshal(st)
	body := base64.RawURLEncoding.EncodeToString(raw)
	return body + "." + s.Sign("oauth-state:"+body)
}

// DecodeState verifies and parses a state.
func (s *Signer) DecodeState(v string) (State, error) {
	body, sig, ok := strings.Cut(v, ".")
	if !ok || s.Sign("oauth-state:"+body) != sig {
		return State{}, errors.New("bad state")
	}
	raw, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil {
		return State{}, err
	}
	var st State
	if err := json.Unmarshal(raw, &st); err != nil {
		return State{}, err
	}
	if time.Now().Unix() > st.Exp {
		return State{}, errors.New("state expired")
	}
	return st, nil
}

// AuthorizeURL builds the redirect to the provider.
func (p Provider) AuthorizeURL(redirectURI, state string) string {
	q := url.Values{}
	q.Set("client_id", p.ClientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("response_type", "code")
	q.Set("scope", strings.Join(p.Scopes, " "))
	q.Set("state", state)
	if p.Name == "google" {
		q.Set("access_type", "online")
		q.Set("prompt", "select_account")
	}
	return p.AuthURL + "?" + q.Encode()
}

// Exchange trades the code for a token and fetches the profile.
func (p Provider) Exchange(ctx context.Context, c *http.Client, code, redirectURI string) (Profile, error) {
	form := url.Values{}
	form.Set("client_id", p.ClientID)
	form.Set("client_secret", p.ClientSecret)
	form.Set("code", code)
	form.Set("grant_type", "authorization_code")
	form.Set("redirect_uri", redirectURI)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return Profile{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		return Profile{}, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var tok struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
	}
	if err := json.Unmarshal(body, &tok); err != nil || tok.AccessToken == "" {
		return Profile{}, fmt.Errorf("%s token exchange failed: %s", p.Name, strings.TrimSpace(string(body)))
	}
	return p.fetch(ctx, c, tok.AccessToken)
}

func getJSON(ctx context.Context, c *http.Client, token, u string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "vig")
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("%s: %s", u, resp.Status)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out)
}

func fetchGoogle(ctx context.Context, c *http.Client, token string) (Profile, error) {
	var u struct {
		Sub, Email, Name, Picture string
		EmailVerified             bool `json:"email_verified"`
	}
	if err := getJSON(ctx, c, token, "https://www.googleapis.com/oauth2/v3/userinfo", &u); err != nil {
		return Profile{}, err
	}
	if u.Sub == "" {
		return Profile{}, errors.New("google: no subject")
	}
	email := ""
	if u.EmailVerified {
		email = u.Email
	}
	return Profile{ProviderID: u.Sub, Email: email, Handle: u.Email, Name: u.Name, AvatarURL: u.Picture}, nil
}

func fetchGitHub(ctx context.Context, c *http.Client, token string) (Profile, error) {
	var u struct {
		ID        int64  `json:"id"`
		Login     string `json:"login"`
		Name      string `json:"name"`
		AvatarURL string `json:"avatar_url"`
		Email     string `json:"email"`
	}
	if err := getJSON(ctx, c, token, "https://api.github.com/user", &u); err != nil {
		return Profile{}, err
	}
	if u.ID == 0 {
		return Profile{}, errors.New("github: no id")
	}
	// Nothing until proven, as with Google's email_verified. /user carries no
	// verification flag, so seeding from it would leave an unchecked address
	// in place whenever this second call fails - and the address is what
	// decides which account a sign-in resolves to.
	email := ""
	var emails []struct {
		Email    string `json:"email"`
		Primary  bool   `json:"primary"`
		Verified bool   `json:"verified"`
	}
	if err := getJSON(ctx, c, token, "https://api.github.com/user/emails", &emails); err == nil {
		for _, e := range emails {
			if e.Primary && e.Verified {
				email = e.Email
				break
			}
		}
	}
	name := u.Name
	if name == "" {
		name = u.Login
	}
	return Profile{ProviderID: strconv.FormatInt(u.ID, 10), Email: email, Handle: u.Login, Name: name, AvatarURL: u.AvatarURL}, nil
}

func fetchDiscord(ctx context.Context, c *http.Client, token string) (Profile, error) {
	var u struct {
		ID         string `json:"id"`
		Username   string `json:"username"`
		GlobalName string `json:"global_name"`
		Avatar     string `json:"avatar"`
	}
	if err := getJSON(ctx, c, token, "https://discord.com/api/users/@me", &u); err != nil {
		return Profile{}, err
	}
	if u.ID == "" {
		return Profile{}, errors.New("discord: no id")
	}
	avatar := ""
	if u.Avatar != "" {
		avatar = "https://cdn.discordapp.com/avatars/" + u.ID + "/" + u.Avatar + ".png"
	}
	name := u.GlobalName
	if name == "" {
		name = u.Username
	}
	return Profile{ProviderID: u.ID, Handle: u.Username, Name: name, AvatarURL: avatar}, nil
}
