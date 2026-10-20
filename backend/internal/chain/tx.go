package chain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Payment is what a purchase confirmation must find on chain.
type Payment struct {
	MemoNonce  string
	Mint       string
	Treasury   string // wallet; the transfer must land in its associated token account
	AmountBase int64  // USDC base units
}

type parsedInstruction struct {
	Program   string          `json:"program"`
	ProgramID string          `json:"programId"`
	Parsed    json.RawMessage `json:"parsed"`
}

type parsedTx struct {
	Slot      uint64 `json:"slot"`
	BlockTime *int64 `json:"blockTime"`
	Meta      *struct {
		Err               any `json:"err"`
		InnerInstructions []struct {
			Instructions []parsedInstruction `json:"instructions"`
		} `json:"innerInstructions"`
	} `json:"meta"`
	Transaction struct {
		Message struct {
			Instructions []parsedInstruction `json:"instructions"`
		} `json:"message"`
	} `json:"transaction"`
}

// GetTransaction fetches a finalized transaction in jsonParsed encoding.
func (c *Client) GetTransaction(ctx context.Context, signature string) (json.RawMessage, error) {
	return c.Call(ctx, "getTransaction", signature, map[string]any{"encoding": "jsonParsed", "commitment": "finalized", "maxSupportedTransactionVersion": 0})
}

// CheckPayment inspects a getTransaction result (finalized commitment) and
// returns the slot when it carries the memo nonce and an SPL token transfer
// of exactly the amount to the treasury's USDC account.
func CheckPayment(raw json.RawMessage, p Payment) (uint64, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return 0, errors.New("transaction not found or not finalized")
	}
	var tx parsedTx
	if err := json.Unmarshal(raw, &tx); err != nil {
		return 0, err
	}
	if tx.Meta == nil {
		return 0, errors.New("transaction has no meta")
	}
	if tx.Meta.Err != nil {
		return 0, errors.New("transaction failed on chain")
	}
	ata, err := AssociatedTokenAddress(p.Treasury, p.Mint)
	if err != nil {
		return 0, err
	}
	var ins []parsedInstruction
	ins = append(ins, tx.Transaction.Message.Instructions...)
	for _, inner := range tx.Meta.InnerInstructions {
		ins = append(ins, inner.Instructions...)
	}
	memoOK, transferOK := false, false
	for _, in := range ins {
		switch {
		case in.Program == "spl-memo" || in.ProgramID == MemoProgram || in.ProgramID == MemoProgramV1:
			var memo string
			if json.Unmarshal(in.Parsed, &memo) == nil && strings.Contains(memo, p.MemoNonce) {
				memoOK = true
			}
		case in.Program == "spl-token" || in.ProgramID == TokenProgram || in.ProgramID == Token2022Program:
			var parsed struct {
				Type string `json:"type"`
				Info struct {
					Destination string `json:"destination"`
					Mint        string `json:"mint"`
					Amount      string `json:"amount"`
					TokenAmount struct {
						Amount string `json:"amount"`
					} `json:"tokenAmount"`
				} `json:"info"`
			}
			if json.Unmarshal(in.Parsed, &parsed) != nil {
				continue
			}
			if parsed.Type != "transfer" && parsed.Type != "transferChecked" {
				continue
			}
			if parsed.Info.Destination != ata {
				continue
			}
			if parsed.Type == "transferChecked" && parsed.Info.Mint != "" && parsed.Info.Mint != p.Mint {
				continue
			}
			amt := parsed.Info.Amount
			if amt == "" {
				amt = parsed.Info.TokenAmount.Amount
			}
			n, err := strconv.ParseInt(amt, 10, 64)
			if err == nil && n == p.AmountBase {
				transferOK = true
			}
		}
	}
	if !memoOK {
		return 0, fmt.Errorf("memo %s not found in transaction", p.MemoNonce)
	}
	if !transferOK {
		return 0, fmt.Errorf("no transfer of %d base units to %s found", p.AmountBase, ata)
	}
	return tx.Slot, nil
}
