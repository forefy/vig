package chain

import (
	"bytes"
	"crypto/sha256"
	"strings"
	"testing"
)

const usdcMint = "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v"

// testKey derives a deterministic, valid 32-byte base58 key from a label.
func testKey(label string) string {
	sum := sha256.Sum256([]byte("vig-test-key:" + label))
	return Encode(sum[:])
}

func TestValidPubkey(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"usdc mint", usdcMint, true},
		{"token program", TokenProgram, true},
		{"system program", "11111111111111111111111111111111", true},
		{"upgradeable loader", BPFLoaderUpgradeable, true},
		{"empty", "", false},
		{"too short", "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGG", false},
		{"too long", usdcMint + "abcd", false},
		{"zero char is not base58", strings.Replace(usdcMint, "E", "0", 1), false},
		{"capital O is not base58", strings.Replace(usdcMint, "E", "O", 1), false},
		{"capital I is not base58", strings.Replace(usdcMint, "E", "I", 1), false},
		{"lowercase l is not base58", strings.Replace(usdcMint, "E", "l", 1), false},
		{"44 chars but decodes to 33 bytes", "1" + usdcMint[:43] + "z", false},
		{"whitespace", " " + usdcMint, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ValidPubkey(c.in); got != c.want {
				t.Fatalf("ValidPubkey(%q)=%v want %v", c.in, got, c.want)
			}
		})
	}
}

func TestDecodeEncodeRoundTrip(t *testing.T) {
	b, err := Decode(usdcMint)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) != 32 {
		t.Fatalf("len %d", len(b))
	}
	if Encode(b) != usdcMint {
		t.Fatalf("round trip %s", Encode(b))
	}
	for _, bad := range []string{"", "abc", "0000", strings.Repeat("1", 31), strings.Repeat("1", 33)} {
		if _, err := Decode(bad); err == nil {
			t.Errorf("Decode(%q) should fail", bad)
		}
	}
	// 32 leading ones is the all-zero key.
	z, err := Decode(strings.Repeat("1", 32))
	if err != nil || !bytes.Equal(z, make([]byte, 32)) {
		t.Fatalf("all-ones key: %v %x", err, z)
	}
}

func TestOnCurve(t *testing.T) {
	// Real keypair-backed accounts sit on the curve; a PDA never does.
	for _, k := range []string{usdcMint, TokenProgram, testKeyFromCurve(t)} {
		b, _ := Decode(k)
		if !onCurve(b) {
			t.Errorf("%s should be on curve", k)
		}
	}
}

// testKeyFromCurve returns a known on-curve key (the memo program id).
func testKeyFromCurve(t *testing.T) string {
	t.Helper()
	return MemoProgram
}

func TestFindProgramAddress(t *testing.T) {
	prog, _ := Decode(AssociatedTokenProg)
	addr, bump, err := FindProgramAddress([][]byte{[]byte("seed")}, prog)
	if err != nil {
		t.Fatal(err)
	}
	if len(addr) != 32 {
		t.Fatalf("len %d", len(addr))
	}
	if onCurve(addr) {
		t.Fatal("PDA must be off curve")
	}
	// Deterministic and equal to the manual derivation for the found bump.
	addr2, bump2, _ := FindProgramAddress([][]byte{[]byte("seed")}, prog)
	if !bytes.Equal(addr, addr2) || bump != bump2 {
		t.Fatal("derivation must be deterministic")
	}
	h := sha256.New()
	h.Write([]byte("seed"))
	h.Write([]byte{bump})
	h.Write(prog)
	h.Write([]byte("ProgramDerivedAddress"))
	if !bytes.Equal(h.Sum(nil), addr) {
		t.Fatal("address is not sha256(seeds||bump||program||marker)")
	}
	// Different seeds or program give a different address.
	other, _, _ := FindProgramAddress([][]byte{[]byte("seed2")}, prog)
	if bytes.Equal(other, addr) {
		t.Fatal("seed change must change the address")
	}
	tok, _ := Decode(TokenProgram)
	other, _, _ = FindProgramAddress([][]byte{[]byte("seed")}, tok)
	if bytes.Equal(other, addr) {
		t.Fatal("program change must change the address")
	}
	// No seeds at all is legal (used by the Anchor IDL derivation).
	if _, _, err := FindProgramAddress(nil, prog); err != nil {
		t.Fatal(err)
	}
}

func TestCreateWithSeed(t *testing.T) {
	base := bytes.Repeat([]byte{1}, 32)
	owner := bytes.Repeat([]byte{2}, 32)
	got := CreateWithSeed(base, "anchor:idl", owner)
	want := sha256.Sum256(append(append(append([]byte{}, base...), []byte("anchor:idl")...), owner...))
	if !bytes.Equal(got, want[:]) {
		t.Fatal("create_with_seed must be sha256(base||seed||owner)")
	}
	if bytes.Equal(CreateWithSeed(base, "other", owner), got) {
		t.Fatal("seed must affect the result")
	}
	if bytes.Equal(CreateWithSeed(owner, "anchor:idl", base), got) {
		t.Fatal("base and owner order must matter")
	}
}

func TestAnchorIDLAddress(t *testing.T) {
	a, err := AnchorIDLAddress(TokenProgram)
	if err != nil {
		t.Fatal(err)
	}
	if !ValidPubkey(a) {
		t.Fatalf("idl address %q is not a valid pubkey", a)
	}
	b, _ := AnchorIDLAddress(TokenProgram)
	if a != b {
		t.Fatal("idl address must be deterministic")
	}
	if c, _ := AnchorIDLAddress(Token2022Program); c == a {
		t.Fatal("different programs must have different idl addresses")
	}
	if a == TokenProgram {
		t.Fatal("idl address must differ from the program id")
	}
	if _, err := AnchorIDLAddress("nope"); err == nil {
		t.Fatal("invalid program id must fail")
	}
}

func TestAssociatedTokenAddress(t *testing.T) {
	owner := testKey("treasury")
	ata, err := AssociatedTokenAddress(owner, usdcMint)
	if err != nil {
		t.Fatal(err)
	}
	if !ValidPubkey(ata) {
		t.Fatalf("ata %q is not a valid pubkey", ata)
	}
	raw, _ := Decode(ata)
	if onCurve(raw) {
		t.Fatal("ata is a PDA and must be off curve")
	}
	again, _ := AssociatedTokenAddress(owner, usdcMint)
	if again != ata {
		t.Fatal("ata must be deterministic")
	}
	if ata == owner || ata == usdcMint {
		t.Fatal("ata must differ from owner and mint")
	}
	// Swapping owner and mint must not give the same account.
	swapped, _ := AssociatedTokenAddress(usdcMint, owner)
	if swapped == ata {
		t.Fatal("owner/mint order must matter")
	}
	other, _ := AssociatedTokenAddress(testKey("someone-else"), usdcMint)
	if other == ata {
		t.Fatal("different owners must have different atas")
	}
	// Matches the manual PDA derivation with the canonical seed order.
	o, _ := Decode(owner)
	m, _ := Decode(usdcMint)
	tok, _ := Decode(TokenProgram)
	prog, _ := Decode(AssociatedTokenProg)
	manual, _, _ := FindProgramAddress([][]byte{o, tok, m}, prog)
	if Encode(manual) != ata {
		t.Fatal("ata must be PDA([owner, token program, mint], ata program)")
	}
	for _, bad := range [][2]string{{"", usdcMint}, {owner, ""}, {"xyz", usdcMint}, {owner, "0000"}} {
		if _, err := AssociatedTokenAddress(bad[0], bad[1]); err == nil {
			t.Errorf("AssociatedTokenAddress(%q,%q) should fail", bad[0], bad[1])
		}
	}
}
