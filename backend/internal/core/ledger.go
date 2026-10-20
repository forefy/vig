package core

import "fmt"

// LedgerKind classifies a credit movement. The ledger is append-only and the
// balance is always derived from it.
type LedgerKind string

const (
	LedgerGrant    LedgerKind = "grant"    // admin or promo, positive
	LedgerPurchase LedgerKind = "purchase" // confirmed on chain, positive
	LedgerConsume  LedgerKind = "consume"  // audit queued, negative
	LedgerRefund   LedgerKind = "refund"   // audit failed, positive, references the consume
	LedgerRevoke   LedgerKind = "revoke"   // admin, negative
	LedgerExpire   LedgerKind = "expire"   // credits past 12 months, negative
)

// LedgerEntry is one row. Delta is in whole credits.
type LedgerEntry struct {
	Kind         LedgerKind `json:"kind" enum:"grant,purchase,consume,refund,revoke,expire"`
	Delta        int        `json:"delta"`
	BalanceAfter int        `json:"balance_after"`
	RefKind      string     `json:"ref_kind,omitempty" doc:"audit, purchase, tweet_claim, admin_action"`
	RefID        string     `json:"ref_id,omitempty"`
	Reason       string     `json:"reason,omitempty"`
}

// ApplyLedger validates a movement against the current balance and returns
// the balance after. Rules:
//
//   - grant, purchase, refund are strictly positive
//   - consume is exactly -1
//   - revoke and expire are strictly negative and never take the balance below zero
//   - consume needs balance >= 1
func ApplyLedger(balance int, kind LedgerKind, delta int) (int, error) {
	switch kind {
	case LedgerGrant, LedgerPurchase, LedgerRefund:
		if delta <= 0 {
			return balance, fmt.Errorf("%s must be positive", kind)
		}
	case LedgerConsume:
		if delta != -1 {
			return balance, fmt.Errorf("consume must be exactly -1")
		}
		if balance < 1 {
			return balance, ErrInsufficientCredits
		}
	case LedgerRevoke, LedgerExpire:
		if delta >= 0 {
			return balance, fmt.Errorf("%s must be negative", kind)
		}
		if balance+delta < 0 {
			return balance, fmt.Errorf("%s of %d would take balance %d below zero", kind, -delta, balance)
		}
	default:
		return balance, fmt.Errorf("unknown ledger kind %q", kind)
	}
	return balance + delta, nil
}

// ErrInsufficientCredits is returned when a consume cannot be paid.
var ErrInsufficientCredits = fmt.Errorf("insufficient credits")
