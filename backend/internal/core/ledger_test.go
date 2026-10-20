package core

import "testing"

func TestLedgerRules(t *testing.T) {
	cases := []struct {
		name    string
		balance int
		kind    LedgerKind
		delta   int
		want    int
		wantErr bool
	}{
		{"grant", 0, LedgerGrant, 1, 1, false},
		{"grant negative", 0, LedgerGrant, -1, 0, true},
		{"purchase three", 1, LedgerPurchase, 3, 4, false},
		{"consume", 4, LedgerConsume, -1, 3, false},
		{"consume wrong delta", 4, LedgerConsume, -2, 4, true},
		{"consume empty", 0, LedgerConsume, -1, 0, true},
		{"refund", 0, LedgerRefund, 1, 1, false},
		{"revoke", 2, LedgerRevoke, -1, 1, false},
		{"revoke below zero", 1, LedgerRevoke, -2, 1, true},
		{"expire", 3, LedgerExpire, -3, 0, false},
		{"expire positive", 3, LedgerExpire, 1, 3, true},
		{"unknown", 3, LedgerKind("bonus"), 1, 3, true},
	}
	for _, c := range cases {
		got, err := ApplyLedger(c.balance, c.kind, c.delta)
		if (err != nil) != c.wantErr {
			t.Errorf("%s: err=%v wantErr=%v", c.name, err, c.wantErr)
		}
		if got != c.want {
			t.Errorf("%s: got %d want %d", c.name, got, c.want)
		}
	}
}

func TestConsumeEmptyIsTyped(t *testing.T) {
	if _, err := ApplyLedger(0, LedgerConsume, -1); err != ErrInsufficientCredits {
		t.Fatalf("want ErrInsufficientCredits, got %v", err)
	}
}
