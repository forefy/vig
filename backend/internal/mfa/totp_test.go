package mfa

import (
	"encoding/base32"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"
)

// rfcSecret is the shared secret used by every test vector in RFC 4226
// appendix D and RFC 6238 appendix B: the ASCII string "12345678901234567890".
const rfcSecret = "12345678901234567890"

func rfcSecretB32() string { return b32.EncodeToString([]byte(rfcSecret)) }

// TestHOTPRFC4226 checks the raw HOTP function against the RFC 4226 vectors
// (counters 0..9). Validate builds on hotp, so these pin the core primitive.
func TestHOTPRFC4226(t *testing.T) {
	want := []string{"755224", "287082", "359152", "969429", "338314", "254676", "287922", "162583", "399871", "520489"}
	for counter, code := range want {
		if got := hotp([]byte(rfcSecret), uint64(counter)); got != code {
			t.Errorf("counter %d: got %s want %s", counter, got, code)
		}
	}
}

// TestTOTPRFC6238 checks the SHA1 rows of RFC 6238 appendix B. The RFC lists
// 8-digit codes; this implementation emits 6, which are the trailing six
// digits of the same truncated value. Parameters match: SHA1, 30s steps.
func TestTOTPRFC6238(t *testing.T) {
	cases := []struct {
		unix int64
		want string // last 6 of the RFC's 8-digit code
	}{
		{59, "287082"},          // 94287082
		{1111111109, "081804"},  // 07081804
		{1111111111, "050471"},  // 14050471
		{1234567890, "005924"},  // 89005924
		{2000000000, "279037"},  // 69279037
		{20000000000, "353130"}, // 65353130
	}
	for _, c := range cases {
		step := uint64(c.unix / 30)
		if got := hotp([]byte(rfcSecret), step); got != c.want {
			t.Errorf("T=%d (step %d): got %s want %s", c.unix, step, got, c.want)
		}
	}
}

func TestValidateWindow(t *testing.T) {
	secret := rfcSecretB32()
	key := []byte(rfcSecret)
	step := uint64(time.Now().Unix() / 30)

	// The three codes inside the window must all validate.
	inside := map[string]bool{}
	for _, c := range []uint64{step - 1, step, step + 1} {
		code := hotp(key, c)
		inside[code] = true
		if !Validate(secret, code) {
			t.Errorf("code for step %d (offset %d) rejected", c, int64(c)-int64(step))
		}
	}

	// Codes two steps away must be rejected, unless by chance they collide
	// with an accepted code (probability ~3e-6 per check; skip that case).
	for _, c := range []uint64{step - 2, step + 2} {
		code := hotp(key, c)
		if inside[code] {
			continue
		}
		if Validate(secret, code) {
			t.Errorf("code for step %d accepted outside the +/-1 window", c)
		}
	}
}

func TestValidateRejects(t *testing.T) {
	secret := rfcSecretB32()
	good := hotp([]byte(rfcSecret), uint64(time.Now().Unix()/30))
	// A code guaranteed wrong: flip the last digit.
	last := good[5]
	wrongLast := byte('0')
	if last == '0' {
		wrongLast = '1'
	}
	wrong := good[:5] + string(wrongLast)

	cases := []struct {
		name   string
		secret string
		code   string
	}{
		{"wrong digit", secret, wrong},
		{"too short", secret, good[:5]},
		{"too long", secret, good + "0"},
		{"empty code", secret, ""},
		{"eight digit RFC form", secret, "00" + good},
		{"empty secret", "", good},
		{"non-base32 secret", "not!base32", good},
		{"different secret", b32.EncodeToString([]byte("09876543210987654321")), good},
	}
	for _, c := range cases {
		if Validate(c.secret, c.code) {
			t.Errorf("%s: accepted", c.name)
		}
	}
}

func TestValidateNormalisesInput(t *testing.T) {
	secret := rfcSecretB32()
	good := hotp([]byte(rfcSecret), uint64(time.Now().Unix()/30))
	if !Validate("  "+strings.ToLower(secret)+" ", "  "+good+"\n") {
		t.Fatal("lowercase/padded secret and padded code should validate")
	}
}

func TestNewSecret(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 20; i++ {
		s := NewSecret()
		if len(s) != 32 {
			t.Fatalf("secret %q: want 32 base32 chars for 160 bits, got %d", s, len(s))
		}
		if strings.Contains(s, "=") {
			t.Fatalf("secret %q must not be padded", s)
		}
		raw, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(s)
		if err != nil || len(raw) != 20 {
			t.Fatalf("secret %q does not decode to 20 bytes: %v", s, err)
		}
		if seen[s] {
			t.Fatalf("duplicate secret %q", s)
		}
		seen[s] = true
	}
}

func TestOTPAuthURL(t *testing.T) {
	secret := rfcSecretB32()
	u := OTPAuthURL("vig", "alice@example.com", secret)

	if !strings.HasPrefix(u, "otpauth://totp/") {
		t.Fatalf("scheme/host wrong: %s", u)
	}
	// secret must come before issuer, and nothing else is emitted.
	q := u[strings.Index(u, "?")+1:]
	if !strings.HasPrefix(q, "secret="+secret+"&issuer=vig") || strings.Count(q, "&") != 1 {
		t.Fatalf("query must be exactly secret then issuer: %s", q)
	}
	parsed, err := url.Parse(u)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Query().Get("secret") != secret || parsed.Query().Get("issuer") != "vig" {
		t.Fatalf("query does not round-trip: %s", u)
	}
	label, err := url.PathUnescape(strings.TrimPrefix(parsed.Path, "/"))
	if err != nil || label != "vig:alice@example.com" {
		t.Fatalf("label = %q (%v), want vig:alice@example.com", label, err)
	}

	// Issuers with spaces or slashes must be escaped, not break the label.
	u = OTPAuthURL("Acme Corp/EU", "bob", secret)
	parsed, err = url.Parse(u)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Host != "totp" {
		t.Fatalf("host = %q, label leaked into it: %s", parsed.Host, u)
	}
	if parsed.Query().Get("issuer") != "Acme Corp/EU" {
		t.Fatalf("issuer not round-tripped: %s", u)
	}
}

func TestNewRecoveryCodes(t *testing.T) {
	const n = 10
	codes, hashes := NewRecoveryCodes(n)
	if len(codes) != n || len(hashes) != n {
		t.Fatalf("got %d codes / %d hashes, want %d", len(codes), len(hashes), n)
	}
	shape := regexp.MustCompile(`^[abcdefghjkmnpqrstuvwxyz23456789]{5}-[abcdefghjkmnpqrstuvwxyz23456789]{5}$`)
	seenCode := map[string]bool{}
	seenHash := map[string]bool{}
	for i, c := range codes {
		if !shape.MatchString(c) {
			t.Errorf("code %q has the wrong shape or uses a look-alike character", c)
		}
		if seenCode[c] {
			t.Errorf("duplicate code %q", c)
		}
		seenCode[c] = true
		if seenHash[hashes[i]] {
			t.Errorf("duplicate hash for %q", c)
		}
		seenHash[hashes[i]] = true
		if hashes[i] != HashCode(c) {
			t.Errorf("hash[%d] does not match HashCode(code)", i)
		}
		if len(hashes[i]) != 64 {
			t.Errorf("hash[%d] = %q, want 64 hex chars", i, hashes[i])
		}
		if hashes[i] == c || strings.Contains(hashes[i], c) {
			t.Errorf("hash[%d] leaks the plaintext code", i)
		}
	}

	codes, hashes = NewRecoveryCodes(0)
	if len(codes) != 0 || len(hashes) != 0 {
		t.Fatalf("n=0 should yield nothing, got %d/%d", len(codes), len(hashes))
	}
}

func TestHashCodeNormalises(t *testing.T) {
	base := HashCode("abcde-fghjk")
	for _, v := range []string{"ABCDE-FGHJK", "  abcde-fghjk  ", "\tAbCdE-fGhJk\n"} {
		if HashCode(v) != base {
			t.Errorf("HashCode(%q) differs from canonical form", v)
		}
	}
	if HashCode("abcde-fghjk") == HashCode("abcde-fghjm") {
		t.Fatal("different codes must hash differently")
	}
	if HashCode("abcdefghjk") == base {
		t.Fatal("the dash is part of the code; removing it must change the hash")
	}
}
