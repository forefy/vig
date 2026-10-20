package auth

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mr-tron/base58"
)

// SIWSMessage builds the Sign-In-With-Solana text a wallet signs. The
// address line is left for the wallet to fill when it composes the message
// itself; wallets that only offer signMessage sign this text verbatim after
// inserting their address.
func SIWSMessage(domain, uri, address, nonce string, issuedAt time.Time) string {
	if address == "" {
		address = "{address}"
	}
	return fmt.Sprintf("%s wants you to sign in with your Solana account:\n%s\n\nSign in to vig\n\nURI: %s\nVersion: 1\nChain ID: mainnet\nNonce: %s\nIssued At: %s",
		domain, address, uri, nonce, issuedAt.UTC().Format(time.RFC3339))
}

// SIWSFields are the parts of a signed message the server checks.
type SIWSFields struct {
	Domain  string
	Address string
	Nonce   string
}

// ParseSIWS extracts domain, address and nonce from a signed message.
func ParseSIWS(msg string) (SIWSFields, error) {
	lines := strings.Split(strings.ReplaceAll(msg, "\r\n", "\n"), "\n")
	if len(lines) < 2 {
		return SIWSFields{}, errors.New("message too short")
	}
	var f SIWSFields
	f.Domain, _, _ = strings.Cut(lines[0], " wants you to sign in")
	f.Address = strings.TrimSpace(lines[1])
	for _, l := range lines[2:] {
		if v, ok := strings.CutPrefix(l, "Nonce: "); ok {
			f.Nonce = strings.TrimSpace(v)
		}
	}
	if f.Nonce == "" {
		return f, errors.New("message has no nonce")
	}
	return f, nil
}

// DecodeSignature accepts base58 (wallet adapter default) or base64.
func DecodeSignature(s string) ([]byte, error) {
	if b, err := base58.Decode(s); err == nil && len(b) == ed25519.SignatureSize {
		return b, nil
	}
	if b, err := base64.StdEncoding.DecodeString(s); err == nil && len(b) == ed25519.SignatureSize {
		return b, nil
	}
	return nil, errors.New("signature must be 64 bytes, base58 or base64")
}

// VerifyEd25519 checks a signature over msg by a base58 public key.
func VerifyEd25519(pubkeyB58 string, msg, sig []byte) error {
	pk, err := base58.Decode(pubkeyB58)
	if err != nil || len(pk) != ed25519.PublicKeySize {
		return errors.New("public key must be a base58 ed25519 key")
	}
	if len(sig) != ed25519.SignatureSize || !ed25519.Verify(ed25519.PublicKey(pk), msg, sig) {
		return errors.New("signature does not verify")
	}
	return nil
}
