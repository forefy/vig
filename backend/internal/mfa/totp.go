// Package mfa implements RFC 6238 TOTP (SHA1, 6 digits, 30s) and one-time
// recovery codes without a third-party dependency. Secrets are base32;
// validation allows +/- one time step for clock skew and compares in
// constant time. Recovery codes are stored only as SHA-256 hashes.
package mfa

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"net/url"
	"strings"
	"time"
)

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// NewSecret returns a fresh random base32 TOTP secret (160 bits, the RFC-recommended size).
func NewSecret() string {
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand failing is unrecoverable
	}
	return b32.EncodeToString(b)
}

// OTPAuthURL builds the otpauth:// URI an authenticator app scans (or the user types by hand from the
// shown secret). Two deliberate choices:
//   - Emit ONLY secret + issuer. algorithm=SHA1, digits=6 and period=30 are the spec defaults every
//     authenticator assumes; spelling them out just lengthens the payload, forcing a higher QR version
//     (more, smaller modules). A short URL keeps the QR chunky and easy to scan.
//   - Put secret FIRST, then issuer. That is the canonical param order in the Key-Uri-Format examples,
//     and some strict/naive parsers (notably 1Password's) expect it - a query built with url.Values would
//     sort alphabetically (issuer before secret), so we assemble the query by hand instead.
func OTPAuthURL(issuer, account, secret string) string {
	label := url.PathEscape(issuer + ":" + account)
	return "otpauth://totp/" + label + "?secret=" + url.QueryEscape(secret) + "&issuer=" + url.QueryEscape(issuer)
}

func hotp(key []byte, counter uint64) string {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], counter)
	m := hmac.New(sha1.New, key)
	m.Write(buf[:])
	sum := m.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	v := (uint32(sum[off]&0x7f) << 24) | (uint32(sum[off+1]) << 16) | (uint32(sum[off+2]) << 8) | uint32(sum[off+3])
	return fmt.Sprintf("%06d", v%1_000_000)
}

// Validate reports whether code is a valid TOTP for secret right now (allowing +/- one 30s step so a
// slightly-skewed phone clock still works). Constant-time compare avoids leaking timing on the digits.
func Validate(secret, code string) bool {
	code = strings.TrimSpace(code)
	if len(code) != 6 {
		return false
	}
	key, err := b32.DecodeString(strings.ToUpper(strings.TrimSpace(secret)))
	if err != nil {
		return false
	}
	step := uint64(time.Now().Unix() / 30)
	for _, c := range []uint64{step - 1, step, step + 1} {
		if subtle.ConstantTimeCompare([]byte(hotp(key, c)), []byte(code)) == 1 {
			return true
		}
	}
	return false
}

// NewRecoveryCodes returns n human-typable one-time codes (plaintext, shown to the user once) alongside
// their SHA-256 hashes (what the server stores). A lost authenticator can still get in with one of these.
func NewRecoveryCodes(n int) (codes, hashes []string) {
	const alphabet = "abcdefghjkmnpqrstuvwxyz23456789" // no look-alikes (0/o, 1/l/i)
	for i := 0; i < n; i++ {
		buf := make([]byte, 10)
		if _, err := rand.Read(buf); err != nil {
			panic(err)
		}
		var sb strings.Builder
		for j, b := range buf {
			if j == 5 {
				sb.WriteByte('-')
			}
			sb.WriteByte(alphabet[int(b)%len(alphabet)])
		}
		code := sb.String()
		codes = append(codes, code)
		hashes = append(hashes, HashCode(code))
	}
	return codes, hashes
}

// HashCode is the stored form of a recovery code (case/space-insensitive), so plaintext codes never touch
// the database.
func HashCode(code string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(code))))
	return hex.EncodeToString(sum[:])
}
