// Package chain reads Solana mainnet through JSON-RPC with an in-memory
// cache and endpoint fallback. Nothing here is imported by core.
package chain

import (
	"crypto/sha256"
	"errors"
	"regexp"

	"filippo.io/edwards25519"
	"github.com/mr-tron/base58"
)

// Well-known program addresses.
const (
	BPFLoaderUpgradeable = "BPFLoaderUpgradeab1e11111111111111111111111"
	BPFLoader2           = "BPFLoader2111111111111111111111111111111111"
	BPFLoader1           = "BPFLoader1111111111111111111111111111111111"
	LoaderV4             = "LoaderV411111111111111111111111111111111111"
	TokenProgram         = "TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA"
	Token2022Program     = "TokenzQdBNbLqP5VEhdkAS6EPFLC1PHnBqCXEpPxuEb"
	AssociatedTokenProg  = "ATokenGPvbdGVxr1b2hvZbsiqW5xWH25efTNsLJA8knL"
	MemoProgram          = "MemoSq4gqABAXKb96qnH8TysNcWxMyWCqXgDLGmfcHr"
	MemoProgramV1        = "Memo1UhkJRfHyvLMcVucJwxXeuD728EqVDDwQDxFMNo"
)

var pubkeyRE = regexp.MustCompile(`^[1-9A-HJ-NP-Za-km-z]{32,44}$`)

// ValidPubkey reports whether s decodes to 32 bytes of base58.
func ValidPubkey(s string) bool {
	if !pubkeyRE.MatchString(s) {
		return false
	}
	b, err := base58.Decode(s)
	return err == nil && len(b) == 32
}

// Decode parses a base58 32-byte key.
func Decode(s string) ([]byte, error) {
	b, err := base58.Decode(s)
	if err != nil || len(b) != 32 {
		return nil, errors.New("not a 32-byte base58 key")
	}
	return b, nil
}

// Encode renders 32 bytes as base58.
func Encode(b []byte) string { return base58.Encode(b) }

// onCurve reports whether the bytes decode to an ed25519 point.
func onCurve(b []byte) bool {
	_, err := new(edwards25519.Point).SetBytes(b)
	return err == nil
}

// FindProgramAddress derives a PDA and its bump like the Solana SDK.
func FindProgramAddress(seeds [][]byte, program []byte) ([]byte, uint8, error) {
	for bump := 255; bump >= 0; bump-- {
		h := sha256.New()
		for _, s := range seeds {
			h.Write(s)
		}
		h.Write([]byte{byte(bump)})
		h.Write(program)
		h.Write([]byte("ProgramDerivedAddress"))
		addr := h.Sum(nil)
		if !onCurve(addr) {
			return addr, uint8(bump), nil
		}
	}
	return nil, 0, errors.New("no viable bump")
}

// CreateWithSeed is Pubkey::create_with_seed: sha256(base || seed || owner).
func CreateWithSeed(base []byte, seed string, owner []byte) []byte {
	h := sha256.New()
	h.Write(base)
	h.Write([]byte(seed))
	h.Write(owner)
	return h.Sum(nil)
}

// AnchorIDLAddress derives the Anchor IDL account for a program.
func AnchorIDLAddress(programID string) (string, error) {
	pid, err := Decode(programID)
	if err != nil {
		return "", err
	}
	base, _, err := FindProgramAddress(nil, pid)
	if err != nil {
		return "", err
	}
	return Encode(CreateWithSeed(base, "anchor:idl", pid)), nil
}

// AssociatedTokenAddress derives the ATA of owner for mint.
func AssociatedTokenAddress(owner, mint string) (string, error) {
	o, err := Decode(owner)
	if err != nil {
		return "", err
	}
	m, err := Decode(mint)
	if err != nil {
		return "", err
	}
	tok, _ := Decode(TokenProgram)
	ata, _ := Decode(AssociatedTokenProg)
	addr, _, err := FindProgramAddress([][]byte{o, tok, m}, ata)
	if err != nil {
		return "", err
	}
	return Encode(addr), nil
}
