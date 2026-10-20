package chain

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// txBuilder assembles a jsonParsed getTransaction result by hand.
type txBuilder struct {
	slot    uint64
	err     any
	noMeta  bool
	outer   []any
	inner   []any
	blockTs int64
}

func memoIx(text string) map[string]any {
	return map[string]any{"program": "spl-memo", "programId": MemoProgram, "parsed": text}
}

func memoIxByID(text string) map[string]any {
	return map[string]any{"programId": MemoProgramV1, "parsed": text}
}

func transferCheckedIx(dest, mint, amount string) map[string]any {
	return map[string]any{
		"program": "spl-token", "programId": TokenProgram,
		"parsed": map[string]any{
			"type": "transferChecked",
			"info": map[string]any{
				"destination": dest, "mint": mint, "source": testKey("payer-ata"), "authority": testKey("payer"),
				"tokenAmount": map[string]any{"amount": amount, "decimals": 6, "uiAmountString": "x"},
			},
		},
	}
}

func transferIx(dest, amount string) map[string]any {
	return map[string]any{
		"programId": TokenProgram,
		"parsed": map[string]any{
			"type": "transfer",
			"info": map[string]any{"destination": dest, "amount": amount, "source": testKey("payer-ata"), "authority": testKey("payer")},
		},
	}
}

func (b txBuilder) json(t *testing.T) json.RawMessage {
	t.Helper()
	tx := map[string]any{
		"slot":      b.slot,
		"blockTime": b.blockTs,
		"transaction": map[string]any{
			"message":    map[string]any{"instructions": b.outer, "accountKeys": []any{}},
			"signatures": []string{"sig"},
		},
	}
	if !b.noMeta {
		meta := map[string]any{"err": b.err, "fee": 5000}
		if len(b.inner) > 0 {
			meta["innerInstructions"] = []any{map[string]any{"index": 1, "instructions": b.inner}}
		}
		tx["meta"] = meta
	}
	raw, err := json.Marshal(tx)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestCheckPayment(t *testing.T) {
	treasury := testKey("treasury")
	ata, err := AssociatedTokenAddress(treasury, usdcMint)
	if err != nil {
		t.Fatal(err)
	}
	wrongATA, _ := AssociatedTokenAddress(testKey("attacker"), usdcMint)
	pay := Payment{MemoNonce: "vig:7f3a9c", Mint: usdcMint, Treasury: treasury, AmountBase: 25_000_000}

	cases := []struct {
		name     string
		raw      json.RawMessage
		pay      Payment
		wantSlot uint64
		wantErr  string
	}{
		{
			name:     "happy path memo plus transferChecked",
			raw:      txBuilder{slot: 250_123_456, outer: []any{memoIx("vig:7f3a9c"), transferCheckedIx(ata, usdcMint, "25000000")}}.json(t),
			pay:      pay,
			wantSlot: 250_123_456,
		},
		{
			name:     "memo text may embed the nonce",
			raw:      txBuilder{slot: 9, outer: []any{memoIx("purchase vig:7f3a9c thanks"), transferCheckedIx(ata, usdcMint, "25000000")}}.json(t),
			pay:      pay,
			wantSlot: 9,
		},
		{
			name:     "memo v1 matched by program id only",
			raw:      txBuilder{slot: 10, outer: []any{memoIxByID("vig:7f3a9c"), transferCheckedIx(ata, usdcMint, "25000000")}}.json(t),
			pay:      pay,
			wantSlot: 10,
		},
		{
			name:     "plain transfer with amount field",
			raw:      txBuilder{slot: 11, outer: []any{memoIx("vig:7f3a9c"), transferIx(ata, "25000000")}}.json(t),
			pay:      pay,
			wantSlot: 11,
		},
		{
			name:     "transfer inside inner instructions",
			raw:      txBuilder{slot: 12, outer: []any{memoIx("vig:7f3a9c"), map[string]any{"programId": testKey("some-cpi-program"), "parsed": nil}}, inner: []any{transferCheckedIx(ata, usdcMint, "25000000")}}.json(t),
			pay:      pay,
			wantSlot: 12,
		},
		{
			name:     "memo inside inner instructions",
			raw:      txBuilder{slot: 13, outer: []any{transferCheckedIx(ata, usdcMint, "25000000")}, inner: []any{memoIx("vig:7f3a9c")}}.json(t),
			pay:      pay,
			wantSlot: 13,
		},
		{
			name:    "wrong amount too low",
			raw:     txBuilder{slot: 1, outer: []any{memoIx("vig:7f3a9c"), transferCheckedIx(ata, usdcMint, "24999999")}}.json(t),
			pay:     pay,
			wantErr: "no transfer of 25000000",
		},
		{
			name:    "wrong amount too high is not accepted either",
			raw:     txBuilder{slot: 1, outer: []any{memoIx("vig:7f3a9c"), transferCheckedIx(ata, usdcMint, "25000001")}}.json(t),
			pay:     pay,
			wantErr: "no transfer",
		},
		{
			name:    "two half transfers do not sum",
			raw:     txBuilder{slot: 1, outer: []any{memoIx("vig:7f3a9c"), transferCheckedIx(ata, usdcMint, "12500000"), transferCheckedIx(ata, usdcMint, "12500000")}}.json(t),
			pay:     pay,
			wantErr: "no transfer",
		},
		{
			name:    "wrong recipient",
			raw:     txBuilder{slot: 1, outer: []any{memoIx("vig:7f3a9c"), transferCheckedIx(wrongATA, usdcMint, "25000000")}}.json(t),
			pay:     pay,
			wantErr: "no transfer",
		},
		{
			name:    "recipient is the treasury wallet not its ata",
			raw:     txBuilder{slot: 1, outer: []any{memoIx("vig:7f3a9c"), transferCheckedIx(treasury, usdcMint, "25000000")}}.json(t),
			pay:     pay,
			wantErr: "no transfer",
		},
		{
			name:    "transferChecked with another mint",
			raw:     txBuilder{slot: 1, outer: []any{memoIx("vig:7f3a9c"), transferCheckedIx(ata, testKey("fake-usdc"), "25000000")}}.json(t),
			pay:     pay,
			wantErr: "no transfer",
		},
		{
			name:    "missing memo",
			raw:     txBuilder{slot: 1, outer: []any{transferCheckedIx(ata, usdcMint, "25000000")}}.json(t),
			pay:     pay,
			wantErr: "memo vig:7f3a9c not found",
		},
		{
			name:    "memo mismatch",
			raw:     txBuilder{slot: 1, outer: []any{memoIx("vig:000000"), transferCheckedIx(ata, usdcMint, "25000000")}}.json(t),
			pay:     pay,
			wantErr: "memo vig:7f3a9c not found",
		},
		{
			name:    "memo from an unrelated program is ignored",
			raw:     txBuilder{slot: 1, outer: []any{map[string]any{"programId": testKey("not-memo"), "parsed": "vig:7f3a9c"}, transferCheckedIx(ata, usdcMint, "25000000")}}.json(t),
			pay:     pay,
			wantErr: "memo vig:7f3a9c not found",
		},
		{
			name:    "meta.err set",
			raw:     txBuilder{slot: 1, err: map[string]any{"InstructionError": []any{0, "Custom"}}, outer: []any{memoIx("vig:7f3a9c"), transferCheckedIx(ata, usdcMint, "25000000")}}.json(t),
			pay:     pay,
			wantErr: "failed on chain",
		},
		{
			name:    "meta missing",
			raw:     txBuilder{slot: 1, noMeta: true, outer: []any{memoIx("vig:7f3a9c"), transferCheckedIx(ata, usdcMint, "25000000")}}.json(t),
			pay:     pay,
			wantErr: "no meta",
		},
		{
			name:    "null result",
			raw:     json.RawMessage("null"),
			pay:     pay,
			wantErr: "not found or not finalized",
		},
		{
			name:    "empty result",
			raw:     json.RawMessage(""),
			pay:     pay,
			wantErr: "not found or not finalized",
		},
		{
			name:    "malformed json",
			raw:     json.RawMessage("{not json"),
			pay:     pay,
			wantErr: "invalid character",
		},
		{
			name:    "invalid treasury",
			raw:     txBuilder{slot: 1, outer: []any{memoIx("vig:7f3a9c")}}.json(t),
			pay:     Payment{MemoNonce: "vig:7f3a9c", Mint: usdcMint, Treasury: "not-a-key", AmountBase: 1},
			wantErr: "not a 32-byte base58 key",
		},
		{
			name:    "non-numeric amount",
			raw:     txBuilder{slot: 1, outer: []any{memoIx("vig:7f3a9c"), transferCheckedIx(ata, usdcMint, "25e6")}}.json(t),
			pay:     pay,
			wantErr: "no transfer",
		},
		{
			name:    "token instruction that is not a transfer",
			raw:     txBuilder{slot: 1, outer: []any{memoIx("vig:7f3a9c"), map[string]any{"program": "spl-token", "programId": TokenProgram, "parsed": map[string]any{"type": "approve", "info": map[string]any{"destination": ata, "amount": "25000000"}}}}}.json(t),
			pay:     pay,
			wantErr: "no transfer",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			slot, err := CheckPayment(c.raw, c.pay)
			if c.wantErr != "" {
				if err == nil {
					t.Fatalf("want error containing %q, got slot %d", c.wantErr, slot)
				}
				if !strings.Contains(err.Error(), c.wantErr) {
					t.Fatalf("error %q does not contain %q", err, c.wantErr)
				}
				if slot != 0 {
					t.Fatalf("slot must be 0 on error, got %d", slot)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if slot != c.wantSlot {
				t.Fatalf("slot %d want %d", slot, c.wantSlot)
			}
		})
	}
}

func TestCheckPaymentToken2022(t *testing.T) {
	// Token-2022 transfers are matched by program id even without the
	// "program" label. The destination is still the classic-token ATA the
	// treasury expects.
	treasury := testKey("treasury")
	ata, _ := AssociatedTokenAddress(treasury, usdcMint)
	ix := transferCheckedIx(ata, usdcMint, "1")
	delete(ix, "program")
	ix["programId"] = Token2022Program
	raw := txBuilder{slot: 77, outer: []any{memoIx("n"), ix}}.json(t)
	slot, err := CheckPayment(raw, Payment{MemoNonce: "n", Mint: usdcMint, Treasury: treasury, AmountBase: 1})
	if err != nil || slot != 77 {
		t.Fatalf("slot=%d err=%v", slot, err)
	}
}

func TestCheckPaymentErrorMentionsATA(t *testing.T) {
	treasury := testKey("treasury")
	ata, _ := AssociatedTokenAddress(treasury, usdcMint)
	raw := txBuilder{slot: 1, outer: []any{memoIx("n")}}.json(t)
	_, err := CheckPayment(raw, Payment{MemoNonce: "n", Mint: usdcMint, Treasury: treasury, AmountBase: 42})
	if err == nil || err.Error() != fmt.Sprintf("no transfer of 42 base units to %s found", ata) {
		t.Fatalf("unexpected error %v", err)
	}
}
