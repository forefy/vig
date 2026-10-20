package auth

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/mr-tron/base58"
)

// ---- SIWS ----

func genKey(t *testing.T) (string, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return base58.Encode(pub), priv
}

func TestSIWSMessageShape(t *testing.T) {
	issued := time.Date(2026, 9, 9, 10, 30, 0, 0, time.FixedZone("plus2", 2*3600))
	msg := SIWSMessage("vig.example", "https://vig.example/login", "So11111111111111111111111111111111111111112", "n0nce", issued)
	lines := strings.Split(msg, "\n")
	if lines[0] != "vig.example wants you to sign in with your Solana account:" {
		t.Fatalf("line 0 = %q", lines[0])
	}
	if lines[1] != "So11111111111111111111111111111111111111112" {
		t.Fatalf("line 1 = %q", lines[1])
	}
	for _, want := range []string{"URI: https://vig.example/login", "Version: 1", "Chain ID: mainnet", "Nonce: n0nce", "Issued At: 2026-09-09T08:30:00Z"} {
		if !strings.Contains(msg, "\n"+want) {
			t.Errorf("message lacks %q:\n%s", want, msg)
		}
	}

	// Empty address leaves the wallet placeholder.
	msg = SIWSMessage("d", "u", "", "n", issued)
	if strings.Split(msg, "\n")[1] != "{address}" {
		t.Fatalf("empty address should leave placeholder:\n%s", msg)
	}
}

func TestParseSIWS(t *testing.T) {
	issued := time.Now()
	good := SIWSMessage("vig.example", "https://vig.example", "Addr111", "abc123", issued)
	cases := []struct {
		name    string
		msg     string
		want    SIWSFields
		wantErr bool
	}{
		{"round trip", good, SIWSFields{Domain: "vig.example", Address: "Addr111", Nonce: "abc123"}, false},
		{"crlf line endings", strings.ReplaceAll(good, "\n", "\r\n"), SIWSFields{Domain: "vig.example", Address: "Addr111", Nonce: "abc123"}, false},
		{"address padded", strings.Replace(good, "\nAddr111\n", "\n  Addr111 \n", 1), SIWSFields{Domain: "vig.example", Address: "Addr111", Nonce: "abc123"}, false},
		{"one line", "vig.example wants you to sign in with your Solana account:", SIWSFields{}, true},
		{"empty", "", SIWSFields{}, true},
		{"no nonce", strings.Replace(good, "Nonce: abc123", "Nonce:", 1), SIWSFields{}, true},
		{"nonce line removed", strings.Replace(good, "\nNonce: abc123", "", 1), SIWSFields{}, true},
	}
	for _, c := range cases {
		got, err := ParseSIWS(c.msg)
		if (err != nil) != c.wantErr {
			t.Errorf("%s: err=%v wantErr=%v", c.name, err, c.wantErr)
			continue
		}
		if !c.wantErr && got != c.want {
			t.Errorf("%s: got %+v want %+v", c.name, got, c.want)
		}
	}
}

func TestDecodeSignature(t *testing.T) {
	sig := make([]byte, ed25519.SignatureSize)
	for i := range sig {
		sig[i] = byte(i)
	}
	short := sig[:63]
	cases := []struct {
		name    string
		in      string
		wantErr bool
	}{
		{"base58", base58.Encode(sig), false},
		{"base64", base64.StdEncoding.EncodeToString(sig), false},
		{"base58 short", base58.Encode(short), true},
		{"base64 short", base64.StdEncoding.EncodeToString(short), true},
		{"hex is not accepted", strings.Repeat("ab", 64), true},
		{"empty", "", true},
		{"garbage", "!!!", true},
	}
	for _, c := range cases {
		got, err := DecodeSignature(c.in)
		if (err != nil) != c.wantErr {
			t.Errorf("%s: err=%v wantErr=%v", c.name, err, c.wantErr)
			continue
		}
		if !c.wantErr && string(got) != string(sig) {
			t.Errorf("%s: decoded bytes differ", c.name)
		}
	}
}

func TestVerifyEd25519(t *testing.T) {
	pub, priv := genKey(t)
	otherPub, _ := genKey(t)
	msg := []byte(SIWSMessage("vig.example", "https://vig.example", pub, "nonce1", time.Now()))
	sig := ed25519.Sign(priv, msg)

	tampered := append([]byte{}, msg...)
	tampered[len(tampered)-1] ^= 0x01
	badSig := append([]byte{}, sig...)
	badSig[0] ^= 0x01

	cases := []struct {
		name    string
		pub     string
		msg     []byte
		sig     []byte
		wantErr bool
	}{
		{"valid", pub, msg, sig, false},
		{"tampered message", pub, tampered, sig, true},
		{"tampered signature", pub, msg, badSig, true},
		{"wrong key", otherPub, msg, sig, true},
		{"short signature", pub, msg, sig[:63], true},
		{"empty signature", pub, msg, nil, true},
		{"pubkey not base58", "0OIl", msg, sig, true},
		{"pubkey wrong length", base58.Encode([]byte{1, 2, 3}), msg, sig, true},
		{"empty pubkey", "", msg, sig, true},
	}
	for _, c := range cases {
		err := VerifyEd25519(c.pub, c.msg, c.sig)
		if (err != nil) != c.wantErr {
			t.Errorf("%s: err=%v wantErr=%v", c.name, err, c.wantErr)
		}
	}

	// End-to-end: the wallet-signed text parses back to the same nonce and
	// address that was baked in, and the base58 signature decodes.
	f, err := ParseSIWS(string(msg))
	if err != nil || f.Address != pub || f.Nonce != "nonce1" {
		t.Fatalf("parse: %+v %v", f, err)
	}
	dec, err := DecodeSignature(base58.Encode(sig))
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyEd25519(f.Address, msg, dec); err != nil {
		t.Fatal(err)
	}
}

// ---- access tokens ----

// forge builds an access-shaped token signed by s with arbitrary claims, so
// expiry and type checks can be exercised without waiting.
func forge(s *Signer, c Claims) string {
	header := b64url([]byte(`{"alg":"HS256","typ":"JWT"}`))
	raw, _ := json.Marshal(c)
	signing := header + "." + b64url(raw)
	return signing + "." + b64url(s.mac(signing))
}

func TestAccessTokenRoundTrip(t *testing.T) {
	s := NewSigner("secret-a", false)
	before := time.Now()
	tok, exp := s.IssueAccess("user1", "sess1", "admin")
	if got := time.Until(exp); got > AccessTTL || got < AccessTTL-5*time.Second {
		t.Fatalf("expiry %v is not ~AccessTTL from now", got)
	}
	if strings.Count(tok, ".") != 2 {
		t.Fatalf("token should have three segments: %s", tok)
	}
	c, ok := s.VerifyAccess(tok)
	if !ok {
		t.Fatal("freshly issued token must verify")
	}
	if c.Sub != "user1" || c.Sid != "sess1" || c.Role != "admin" || c.Typ != "access" {
		t.Fatalf("claims %+v", c)
	}
	if c.Exp != exp.Unix() || c.Iat < before.Unix()-1 || c.Iat > time.Now().Unix()+1 {
		t.Fatalf("times: exp=%d want %d, iat=%d", c.Exp, exp.Unix(), c.Iat)
	}
	// Token is URL-safe (no padding, no +/).
	if strings.ContainsAny(tok, "=+/") {
		t.Fatalf("token is not raw URL base64: %s", tok)
	}
}

func TestAccessTokenRejects(t *testing.T) {
	s := NewSigner("secret-a", false)
	other := NewSigner("secret-b", false)
	good, _ := s.IssueAccess("user1", "sess1", "user")
	parts := strings.Split(good, ".")
	now := time.Now().Unix()

	// Payload swap: claims from a different, validly-signed token.
	other2, _ := s.IssueAccess("user2", "sess2", "admin")
	swapped := parts[0] + "." + strings.Split(other2, ".")[1] + "." + parts[2]

	cases := []struct {
		name string
		tok  string
	}{
		{"wrong secret", func() string { t, _ := other.IssueAccess("user1", "sess1", "user"); return t }()},
		{"expired", forge(s, Claims{Sub: "u", Sid: "s", Role: "user", Typ: "access", Exp: now - 1, Iat: now - 100})},
		{"expires this second", forge(s, Claims{Sub: "u", Sid: "s", Role: "user", Typ: "access", Exp: now, Iat: now - 100})},
		{"refresh typ", forge(s, Claims{Sub: "u", Sid: "s", Role: "user", Typ: "refresh", Exp: now + 600, Iat: now})},
		{"empty typ", forge(s, Claims{Sub: "u", Sid: "s", Role: "user", Exp: now + 600, Iat: now})},
		{"payload swapped", swapped},
		{"signature stripped", parts[0] + "." + parts[1]},
		{"empty signature", parts[0] + "." + parts[1] + "."},
		{"four segments", good + ".x"},
		{"empty", ""},
		{"not base64 payload", parts[0] + ".!!!." + parts[2]},
		{"role escalated", parts[0] + "." + b64url([]byte(`{"sub":"user1","sid":"sess1","role":"admin","typ":"access","exp":9999999999,"iat":1}`)) + "." + parts[2]},
	}
	for _, c := range cases {
		if _, ok := s.VerifyAccess(c.tok); ok {
			t.Errorf("%s: accepted", c.name)
		}
	}

	// Sanity: a forged-but-valid token is accepted, so the rejections above
	// are for the stated reasons and not because forge is broken.
	if _, ok := s.VerifyAccess(forge(s, Claims{Sub: "u", Sid: "s", Role: "user", Typ: "access", Exp: now + 60, Iat: now})); !ok {
		t.Fatal("forged valid token should verify")
	}
}

func TestSignerKeyDerivation(t *testing.T) {
	a1 := NewSigner("same", false)
	a2 := NewSigner("same", true)
	b := NewSigner("different", false)
	if a1.Sign("x") != a2.Sign("x") {
		t.Fatal("same secret must produce the same MAC regardless of Secure")
	}
	if a1.Sign("x") == b.Sign("x") {
		t.Fatal("different secrets must produce different MACs")
	}
	if a1.Sign("x") == a1.Sign("y") {
		t.Fatal("different messages must produce different MACs")
	}
	// The raw secret is never the signing key.
	if string(a1.secret) == "same" {
		t.Fatal("secret must be derived, not used raw")
	}
}

func TestHashAndRandomToken(t *testing.T) {
	sum := sha256.Sum256([]byte("tok"))
	if string(Hash("tok")) != string(sum[:]) {
		t.Fatal("Hash must be plain sha256 of the token")
	}
	if len(Hash("")) != 32 {
		t.Fatal("hash length")
	}
	for _, n := range []int{1, 16, 32} {
		tok := RandomToken(n)
		if len(tok) != 2*n {
			t.Fatalf("RandomToken(%d) = %d chars", n, len(tok))
		}
		if !regexp.MustCompile(`^[0-9a-f]*$`).MatchString(tok) {
			t.Fatalf("RandomToken(%d) not hex: %s", n, tok)
		}
	}
	if RandomToken(16) == RandomToken(16) {
		t.Fatal("random tokens repeated")
	}
}

// ---- API keys ----

func TestMintAPIKey(t *testing.T) {
	key, prefix, hash := MintAPIKey()
	if !strings.HasPrefix(key, APIKeyPrefix) {
		t.Fatalf("key %q lacks prefix", key)
	}
	if len(key) != len(APIKeyPrefix)+48 {
		t.Fatalf("key length %d, want %d", len(key), len(APIKeyPrefix)+48)
	}
	if prefix != key[:12] || !strings.HasPrefix(prefix, APIKeyPrefix) {
		t.Fatalf("display prefix %q should be first 12 chars of %q", prefix, key)
	}
	if string(hash) != string(Hash(key)) {
		t.Fatal("hash must be Hash(key)")
	}
	if !IsAPIKey(key) {
		t.Fatal("minted key must look like an api key")
	}
	k2, _, h2 := MintAPIKey()
	if k2 == key || string(h2) == string(hash) {
		t.Fatal("keys must be unique")
	}
}

func TestIsAPIKeyAndBearer(t *testing.T) {
	key, _, _ := MintAPIKey()
	isKey := []struct {
		in   string
		want bool
	}{
		{key, true},
		{"vig_", true},
		{"vig_abc", true},
		{"VIG_abc", false},
		{"", false},
		{"eyJhbGciOi.eyJzdWIi.sig", false},
		{" vig_abc", false},
	}
	for _, c := range isKey {
		if got := IsAPIKey(c.in); got != c.want {
			t.Errorf("IsAPIKey(%q) = %v want %v", c.in, got, c.want)
		}
	}
	bearer := []struct {
		in, want string
	}{
		{"Bearer " + key, key},
		{"Bearer   " + key + "  ", key},
		{"Bearer ", ""},
		{"Bearer", ""},
		{"bearer " + key, ""},
		{"Basic abc", ""},
		{"", ""},
		{key, ""},
	}
	for _, c := range bearer {
		if got := Bearer(c.in); got != c.want {
			t.Errorf("Bearer(%q) = %q want %q", c.in, got, c.want)
		}
	}
}

// ---- OAuth state ----

func TestOAuthStateRoundTrip(t *testing.T) {
	s := NewSigner("secret-a", false)
	in := State{Nonce: "n1", Mode: "link", UserID: "user1", Next: "/settings"}
	enc := s.EncodeState(in)
	if strings.ContainsAny(enc, "=+/ ") {
		t.Fatalf("state must be URL-safe: %q", enc)
	}
	out, err := s.DecodeState(enc)
	if err != nil {
		t.Fatal(err)
	}
	if out.Nonce != in.Nonce || out.Mode != in.Mode || out.UserID != in.UserID || out.Next != in.Next {
		t.Fatalf("got %+v want %+v", out, in)
	}
	// Exp is set by EncodeState (caller's value is overwritten) to ~10 minutes.
	if until := time.Until(time.Unix(out.Exp, 0)); until < 9*time.Minute || until > 10*time.Minute+5*time.Second {
		t.Fatalf("exp %v from now, want ~10m", until)
	}
	in.Exp = 1
	if out2, err := s.DecodeState(s.EncodeState(in)); err != nil || out2.Exp == 1 {
		t.Fatalf("caller-supplied exp must be overwritten: %+v %v", out2, err)
	}
	// Login mode omits the optional fields.
	if out, err := s.DecodeState(s.EncodeState(State{Nonce: "n", Mode: "login"})); err != nil || out.UserID != "" || out.Next != "" {
		t.Fatalf("login state: %+v %v", out, err)
	}
}

func TestOAuthStateRejects(t *testing.T) {
	s := NewSigner("secret-a", false)
	other := NewSigner("secret-b", false)
	enc := s.EncodeState(State{Nonce: "n1", Mode: "login"})
	body, sig, _ := strings.Cut(enc, ".")

	// Expired but correctly signed.
	expRaw, _ := json.Marshal(State{Nonce: "n1", Mode: "login", Exp: time.Now().Add(-time.Second).Unix()})
	expBody := base64.RawURLEncoding.EncodeToString(expRaw)
	expired := expBody + "." + s.Sign("oauth-state:"+expBody)

	// Body changed (mode escalated to link with a user id), original signature kept.
	tamperRaw, _ := json.Marshal(State{Nonce: "n1", Mode: "link", UserID: "victim", Exp: time.Now().Add(time.Hour).Unix()})
	tampered := base64.RawURLEncoding.EncodeToString(tamperRaw) + "." + sig

	// Signed body that is not valid JSON / base64.
	notJSONBody := base64.RawURLEncoding.EncodeToString([]byte("{not json"))
	notJSON := notJSONBody + "." + s.Sign("oauth-state:"+notJSONBody)
	notB64 := "!!!." + s.Sign("oauth-state:!!!")

	cases := []struct {
		name string
		in   string
	}{
		{"expired", expired},
		{"tampered body", tampered},
		{"other secret", other.EncodeState(State{Nonce: "n1", Mode: "login"})},
		{"signature truncated", body + "." + sig[:len(sig)-1]},
		{"no separator", body + sig},
		{"empty", ""},
		{"only dot", "."},
		{"signed non-json", notJSON},
		{"signed non-base64", notB64},
		{"sign of a different domain", body + "." + s.Sign(body)},
	}
	for _, c := range cases {
		if st, err := s.DecodeState(c.in); err == nil {
			t.Errorf("%s: accepted %+v", c.name, st)
		}
	}
}

func TestTweetNonce(t *testing.T) {
	s := NewSigner("secret-a", false)
	shape := regexp.MustCompile(`^vg-[ABCDEFGHJKLMNPQRSTUVWXYZ23456789]{4}$`)
	a := s.TweetNonce("user1")
	if !shape.MatchString(a) {
		t.Fatalf("nonce %q has the wrong shape", a)
	}
	if s.TweetNonce("user1") != a || NewSigner("secret-a", true).TweetNonce("user1") != a {
		t.Fatal("nonce must be deterministic for a user and secret")
	}
	if s.TweetNonce("user2") == a {
		t.Fatal("different users should get different nonces")
	}
	if NewSigner("secret-b", false).TweetNonce("user1") == a {
		t.Fatal("different secrets should give different nonces")
	}
	// Ambiguous glyphs are excluded by construction.
	for i := 0; i < 200; i++ {
		n := s.TweetNonce(RandomToken(4))
		if strings.ContainsAny(n[3:], "01IO") {
			t.Fatalf("nonce %q uses an ambiguous glyph", n)
		}
	}
}

// ---- cookies ----

func TestSessionCookies(t *testing.T) {
	accessExp := time.Now().Add(AccessTTL)
	refreshExp := time.Now().Add(RefreshTTL)
	for _, secure := range []bool{false, true} {
		s := NewSigner("x", secure)
		cs := s.SessionCookies("acc", accessExp, "ref", refreshExp)
		if len(cs) != 2 {
			t.Fatalf("want 2 cookies, got %d", len(cs))
		}
		byName := map[string]http.Cookie{}
		for _, c := range cs {
			byName[c.Name] = c
			if !c.HttpOnly || c.Secure != secure || c.SameSite != http.SameSiteLaxMode {
				t.Errorf("secure=%v cookie %s flags: %+v", secure, c.Name, c)
			}
		}
		if a := byName[CookieAccess]; a.Value != "acc" || a.Path != "/" || !a.Expires.Equal(accessExp) {
			t.Errorf("access cookie %+v", a)
		}
		// The refresh cookie must be scoped to the auth routes only.
		if r := byName[CookieRefresh]; r.Value != "ref" || r.Path != "/v1/auth" || !r.Expires.Equal(refreshExp) {
			t.Errorf("refresh cookie %+v", r)
		}

		clear := s.ClearCookies()
		if len(clear) != 2 {
			t.Fatalf("want 2 clearing cookies, got %d", len(clear))
		}
		for _, c := range clear {
			if c.Value != "" || c.MaxAge != -1 || c.Path != byName[c.Name].Path || c.Secure != secure {
				t.Errorf("clear cookie %+v does not match the set cookie path/flags", c)
			}
		}
	}
}

// ---- principal ----

func TestPrincipalIsAdmin(t *testing.T) {
	now := time.Now()
	fresh := now.Add(-time.Hour)
	stale := now.Add(-TOTPWindow - time.Second)
	edge := now.Add(-TOTPWindow)
	cases := []struct {
		name string
		p    Principal
		want bool
	}{
		{"admin session fresh totp", Principal{Via: ViaSession, Role: "admin", TOTPAt: &fresh}, true},
		{"admin session stale totp", Principal{Via: ViaSession, Role: "admin", TOTPAt: &stale}, false},
		{"admin session totp exactly at window", Principal{Via: ViaSession, Role: "admin", TOTPAt: &edge}, false},
		{"admin session no totp", Principal{Via: ViaSession, Role: "admin"}, false},
		{"user session fresh totp", Principal{Via: ViaSession, Role: "user", TOTPAt: &fresh}, false},
		{"admin via api key", Principal{Via: ViaAPIKey, Role: "admin", TOTPAt: &fresh}, false},
		{"admin via operator", Principal{Via: ViaOperator, Role: "admin", TOTPAt: &fresh}, false},
		{"admin while impersonating", Principal{Via: ViaSession, Role: "admin", TOTPAt: &fresh, Impersonation: &Impersonation{ID: "i", AdminID: "a"}}, false},
		{"zero", Principal{}, false},
	}
	for _, c := range cases {
		if got := c.p.IsAdmin(now); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

func TestPrincipalContext(t *testing.T) {
	if _, ok := FromContext(context.Background()); ok {
		t.Fatal("empty context must have no principal")
	}
	p := Principal{UserID: "u", SessionID: "s", Role: "user", Via: ViaSession, OperatorTokenID: "t", OperatorAuditID: "a"}
	got, ok := FromContext(WithPrincipal(context.Background(), p))
	if !ok || got != p {
		t.Fatalf("got %+v ok=%v", got, ok)
	}
}

// ---- providers ----

func TestProviders(t *testing.T) {
	ps := Providers("gid", "gsec", "", "", "did", "")
	if len(ps) != 3 {
		t.Fatalf("want 3 providers, got %d", len(ps))
	}
	for name, p := range ps {
		if p.Name != name {
			t.Errorf("provider %q has Name %q", name, p.Name)
		}
		if p.fetch == nil || p.AuthURL == "" || p.TokenURL == "" || len(p.Scopes) == 0 {
			t.Errorf("provider %q is incomplete: %+v", name, p)
		}
	}
	if !ps["google"].Configured() {
		t.Error("google has id and secret, should be configured")
	}
	if ps["github"].Configured() {
		t.Error("github has no credentials")
	}
	if ps["discord"].Configured() {
		t.Error("discord has an id but no secret")
	}
}

func TestAuthorizeURL(t *testing.T) {
	ps := Providers("gid", "gsec", "hid", "hsec", "did", "dsec")
	for name, p := range ps {
		u, err := url.Parse(p.AuthorizeURL("https://vig.example/cb", "st.ate"))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !strings.HasPrefix(u.String(), p.AuthURL+"?") {
			t.Errorf("%s: url %s does not start with AuthURL", name, u)
		}
		q := u.Query()
		if q.Get("client_id") != p.ClientID || q.Get("redirect_uri") != "https://vig.example/cb" || q.Get("response_type") != "code" || q.Get("state") != "st.ate" {
			t.Errorf("%s: query %v", name, q)
		}
		if q.Get("scope") != strings.Join(p.Scopes, " ") {
			t.Errorf("%s: scope %q", name, q.Get("scope"))
		}
		if q.Has("client_secret") {
			t.Errorf("%s: client secret leaked into the authorize url", name)
		}
		isGoogle := name == "google"
		if (q.Get("prompt") == "select_account") != isGoogle || (q.Get("access_type") == "online") != isGoogle {
			t.Errorf("%s: google-only params wrong: %v", name, q)
		}
	}
}

func TestExchange(t *testing.T) {
	var gotForm url.Values
	var gotHeaders http.Header
	var tokenBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method %s", r.Method)
		}
		if err := r.ParseForm(); err != nil {
			t.Error(err)
		}
		gotForm = r.PostForm
		gotHeaders = r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(tokenBody))
	}))
	defer srv.Close()

	var fetched string
	p := Provider{Name: "test", ClientID: "cid", ClientSecret: "csec", TokenURL: srv.URL,
		fetch: func(ctx context.Context, c *http.Client, token string) (Profile, error) {
			fetched = token
			return Profile{ProviderID: "42", Handle: "h"}, nil
		}}

	tokenBody = `{"access_token":"tok123","token_type":"bearer"}`
	prof, err := p.Exchange(context.Background(), srv.Client(), "the-code", "https://vig.example/cb")
	if err != nil {
		t.Fatal(err)
	}
	if prof.ProviderID != "42" || fetched != "tok123" {
		t.Fatalf("profile %+v fetched with %q", prof, fetched)
	}
	want := url.Values{"client_id": {"cid"}, "client_secret": {"csec"}, "code": {"the-code"}, "grant_type": {"authorization_code"}, "redirect_uri": {"https://vig.example/cb"}}
	if gotForm.Encode() != want.Encode() {
		t.Fatalf("form %v want %v", gotForm, want)
	}
	if gotHeaders.Get("Accept") != "application/json" || !strings.HasPrefix(gotHeaders.Get("Content-Type"), "application/x-www-form-urlencoded") {
		t.Fatalf("headers %v", gotHeaders)
	}

	for _, body := range []string{`{"error":"bad_verification_code"}`, `{}`, `not json`, ``, `{"access_token":""}`} {
		tokenBody = body
		fetched = ""
		if _, err := p.Exchange(context.Background(), srv.Client(), "c", "r"); err == nil {
			t.Errorf("body %q: expected error", body)
		}
		if fetched != "" {
			t.Errorf("body %q: profile fetched despite failed exchange", body)
		}
	}

	// A fetch failure surfaces.
	tokenBody = `{"access_token":"tok"}`
	p.fetch = func(context.Context, *http.Client, string) (Profile, error) { return Profile{}, context.Canceled }
	if _, err := p.Exchange(context.Background(), srv.Client(), "c", "r"); err != context.Canceled {
		t.Fatalf("fetch error not propagated: %v", err)
	}
}

func TestGetJSON(t *testing.T) {
	var auth, ua string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		ua = r.Header.Get("User-Agent")
		switch r.URL.Path {
		case "/ok":
			_, _ = w.Write([]byte(`{"id":7}`))
		case "/forbidden":
			w.WriteHeader(http.StatusForbidden)
		default:
			_, _ = w.Write([]byte(`{`))
		}
	}))
	defer srv.Close()
	var out struct{ ID int }
	if err := getJSON(context.Background(), srv.Client(), "tk", srv.URL+"/ok", &out); err != nil || out.ID != 7 {
		t.Fatalf("ok: %+v %v", out, err)
	}
	if auth != "Bearer tk" || ua == "" {
		t.Fatalf("headers auth=%q ua=%q", auth, ua)
	}
	if err := getJSON(context.Background(), srv.Client(), "tk", srv.URL+"/forbidden", &out); err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("forbidden: %v", err)
	}
	if err := getJSON(context.Background(), srv.Client(), "tk", srv.URL+"/broken", &out); err == nil {
		t.Fatal("broken json should error")
	}
}

// routes serves canned GitHub API responses and fails anything unlisted, so a
// test can drop one endpoint the way a rate limit or a missing scope would.
type routes map[string]string

func (r routes) RoundTrip(req *http.Request) (*http.Response, error) {
	body, ok := r[req.URL.Path]
	if !ok {
		return &http.Response{StatusCode: 403, Body: io.NopCloser(strings.NewReader(`{"message":"Forbidden"}`)), Header: http.Header{}, Request: req}, nil
	}
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{"Content-Type": {"application/json"}}, Request: req}, nil
}

// The address decides which account a sign-in resolves to, so it may only come
// from an entry GitHub marks verified - never from /user, which carries no
// such flag.
func TestGitHubProfileOnlyTakesAVerifiedEmail(t *testing.T) {
	const user = `{"id":42,"login":"alice","name":"Alice","email":"alice@example.com"}`

	cases := []struct {
		name  string
		serve routes
		want  string
	}{
		{"primary and verified is taken", routes{
			"/user":        user,
			"/user/emails": `[{"email":"other@example.com","primary":false,"verified":true},{"email":"alice@example.com","primary":true,"verified":true}]`,
		}, "alice@example.com"},
		{"unverified primary is refused", routes{
			"/user":        user,
			"/user/emails": `[{"email":"alice@example.com","primary":true,"verified":false}]`,
		}, ""},
		{"the endpoint failing does not fall back to /user", routes{
			"/user": user,
		}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, err := fetchGitHub(context.Background(), &http.Client{Transport: tc.serve}, "tok")
			if err != nil {
				t.Fatal(err)
			}
			if p.Email != tc.want {
				t.Fatalf("email = %q, want %q", p.Email, tc.want)
			}
			if p.ProviderID != "42" {
				t.Fatalf("provider id = %q", p.ProviderID)
			}
		})
	}
}
