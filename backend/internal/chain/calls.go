package chain

import (
	"context"
	"encoding/json"
	"math/big"
	"strings"
	"time"

	"github.com/mr-tron/base58"
)

// CallAccount is one account an instruction received, with its privileges.
type CallAccount struct {
	Pubkey   string
	Signer   bool
	Writable bool
}

// Call is one instruction as executed: the program, its accounts, raw data
// and the programs it invoked beneath it.
type Call struct {
	ProgramID  string
	Accounts   []CallAccount
	Data       []byte
	Inner      []string // programs invoked by CPI under this call; for an inner call, its direct callees
	InnerCalls []Call   // the CPI calls themselves, in order
	// From the RPC's stack heights, when it reports them: Direct is the
	// programs a top-level call invokes itself, Depth the height of an inner
	// call (2 is called by the top-level instruction) and Parent the index
	// in InnerCalls of the call that made it, or -1 for the top-level one.
	Direct []string
	Depth  int
	Parent int
	// The RPC pre-parses well-known programs and drops their bytes: the
	// instruction name and its named accounts arrive here instead.
	ParsedType string
	ParsedInfo map[string]any
}

// TokenDelta is one token account's balance change over a transaction,
// in base units, signed. Owner is the wallet the account belongs to.
type TokenDelta struct {
	Mint     string
	Owner    string
	Decimals int
	Delta    string
}

// TxCalls is a transaction reduced to its calls, plus the outcome facts the
// meta carries for free: units, error, logs, signers and token movements.
type TxCalls struct {
	Signature   string
	Slot        uint64
	BlockTime   *time.Time
	Failed      bool
	Units       uint64
	Err         any      // meta.err as the RPC returned it
	Logs        []string // meta.logMessages
	Signers     []string // distinct signer keys, fee payer first
	TokenDeltas []TokenDelta
	Calls       []Call
}

type rawCallTx struct {
	Slot      uint64 `json:"slot"`
	BlockTime *int64 `json:"blockTime"`
	Meta      *struct {
		Err                  any      `json:"err"`
		ComputeUnitsConsumed uint64   `json:"computeUnitsConsumed"`
		LogMessages          []string `json:"logMessages"`
		InnerInstructions    []struct {
			Index        int              `json:"index"`
			Instructions []rawInstruction `json:"instructions"`
		} `json:"innerInstructions"`
		PreTokenBalances  []rawTokenBalance `json:"preTokenBalances"`
		PostTokenBalances []rawTokenBalance `json:"postTokenBalances"`
	} `json:"meta"`
	Transaction struct {
		Signatures []string `json:"signatures"`
		Message    struct {
			AccountKeys []struct {
				Pubkey   string `json:"pubkey"`
				Signer   bool   `json:"signer"`
				Writable bool   `json:"writable"`
			} `json:"accountKeys"`
			Instructions []rawInstruction `json:"instructions"`
		} `json:"message"`
	} `json:"transaction"`
}

type rawTokenBalance struct {
	AccountIndex  int    `json:"accountIndex"`
	Mint          string `json:"mint"`
	Owner         string `json:"owner"`
	UITokenAmount struct {
		Amount   string `json:"amount"`
		Decimals int    `json:"decimals"`
	} `json:"uiTokenAmount"`
}

type rawInstruction struct {
	ProgramID string   `json:"programId"`
	Accounts  []string `json:"accounts"`
	Data      string   `json:"data"`
	// 2 for a call made by the top-level instruction, 3 for one made by that
	// call, and so on. Older RPC nodes omit it.
	StackHeight *int `json:"stackHeight"`
	Parsed      *struct {
		Type string         `json:"type"`
		Info map[string]any `json:"info"`
	} `json:"parsed"`
}

// ParseCalls reduces a jsonParsed getTransaction result to its calls.
// Instructions the RPC pre-parsed for well-known programs keep their program
// id and lose their bytes, which is all the surface needs from them.
func ParseCalls(raw json.RawMessage) (TxCalls, error) {
	var tx rawCallTx
	if err := json.Unmarshal(raw, &tx); err != nil {
		return TxCalls{}, err
	}
	out := TxCalls{Slot: tx.Slot}
	if len(tx.Transaction.Signatures) > 0 {
		out.Signature = tx.Transaction.Signatures[0]
	}
	if tx.BlockTime != nil {
		t := time.Unix(*tx.BlockTime, 0).UTC()
		out.BlockTime = &t
	}
	if tx.Meta != nil {
		out.Failed = tx.Meta.Err != nil
		out.Err = tx.Meta.Err
		out.Units = tx.Meta.ComputeUnitsConsumed
		out.Logs = tx.Meta.LogMessages
		out.TokenDeltas = tokenDeltas(tx.Meta.PreTokenBalances, tx.Meta.PostTokenBalances)
	}
	priv := map[string]CallAccount{}
	for _, k := range tx.Transaction.Message.AccountKeys {
		if _, seen := priv[k.Pubkey]; !seen && k.Signer {
			out.Signers = append(out.Signers, k.Pubkey)
		}
		priv[k.Pubkey] = CallAccount{Pubkey: k.Pubkey, Signer: k.Signer, Writable: k.Writable}
	}
	for _, ins := range tx.Transaction.Message.Instructions {
		out.Calls = append(out.Calls, toCall(ins, priv))
	}
	if tx.Meta != nil {
		for _, inner := range tx.Meta.InnerInstructions {
			if inner.Index < 0 || inner.Index >= len(out.Calls) {
				continue
			}
			type frame struct{ idx, height int }
			var stack []frame
			top := &out.Calls[inner.Index]
			for _, ins := range inner.Instructions {
				c := toCall(ins, priv)
				c.Depth, c.Parent = 2, -1
				if ins.StackHeight != nil && *ins.StackHeight >= 2 {
					c.Depth = *ins.StackHeight
				}
				for len(stack) > 0 && stack[len(stack)-1].height >= c.Depth {
					stack = stack[:len(stack)-1]
				}
				if len(stack) > 0 {
					c.Parent = stack[len(stack)-1].idx
					top.InnerCalls[c.Parent].Inner = append(top.InnerCalls[c.Parent].Inner, ins.ProgramID)
				} else {
					top.Direct = append(top.Direct, ins.ProgramID)
				}
				top.Inner = append(top.Inner, ins.ProgramID)
				top.InnerCalls = append(top.InnerCalls, c)
				stack = append(stack, frame{len(top.InnerCalls) - 1, c.Depth})
			}
		}
	}
	return out, nil
}

func toCall(ins rawInstruction, priv map[string]CallAccount) Call {
	c := Call{ProgramID: ins.ProgramID}
	for _, a := range ins.Accounts {
		if p, ok := priv[a]; ok {
			c.Accounts = append(c.Accounts, p)
		} else {
			c.Accounts = append(c.Accounts, CallAccount{Pubkey: a})
		}
	}
	if ins.Data != "" {
		if b, err := base58.Decode(ins.Data); err == nil {
			c.Data = b
		}
	}
	if ins.Parsed != nil {
		c.ParsedType = ins.Parsed.Type
		c.ParsedInfo = ins.Parsed.Info
	}
	return c
}

// tokenDeltas pairs pre and post token balances by account index and keeps
// the ones that moved. An account missing on one side held nothing there.
func tokenDeltas(pre, post []rawTokenBalance) []TokenDelta {
	type side struct {
		bal    rawTokenBalance
		amount *big.Int
	}
	before := map[int]side{}
	after := map[int]side{}
	var order []int
	add := func(into map[int]side, bals []rawTokenBalance) {
		for _, b := range bals {
			n, ok := new(big.Int).SetString(b.UITokenAmount.Amount, 10)
			if !ok {
				continue
			}
			if _, seen := before[b.AccountIndex]; !seen {
				if _, seen := after[b.AccountIndex]; !seen {
					order = append(order, b.AccountIndex)
				}
			}
			into[b.AccountIndex] = side{bal: b, amount: n}
		}
	}
	add(before, pre)
	add(after, post)
	var out []TokenDelta
	for _, idx := range order {
		b, hasB := before[idx]
		a, hasA := after[idx]
		delta := new(big.Int)
		if hasA {
			delta.Set(a.amount)
		}
		if hasB {
			delta.Sub(delta, b.amount)
		}
		if delta.Sign() == 0 {
			continue
		}
		bal := a.bal
		if !hasA {
			bal = b.bal
		}
		out = append(out, TokenDelta{Mint: bal.Mint, Owner: bal.Owner, Decimals: bal.UITokenAmount.Decimals, Delta: delta.String()})
	}
	return out
}

// UnitsFor reads the compute units one program's call consumed from the
// "Program <pid> consumed N of M compute units" log line, first match.
func (t TxCalls) UnitsFor(programID string) (uint64, bool) {
	prefix := "Program " + programID + " consumed "
	for _, l := range t.Logs {
		if !strings.HasPrefix(l, prefix) {
			continue
		}
		rest := strings.TrimPrefix(l, prefix)
		digits, _, _ := strings.Cut(rest, " ")
		var n uint64
		for _, c := range digits {
			if c < '0' || c > '9' {
				return 0, false
			}
			n = n*10 + uint64(c-'0')
		}
		return n, digits != ""
	}
	return 0, false
}

// FailedIn returns the program that reported failure in the logs, from the
// first "Program <pid> failed:" line; empty when none did.
func (t TxCalls) FailedIn() string {
	for _, l := range t.Logs {
		if !strings.HasPrefix(l, "Program ") {
			continue
		}
		parts := strings.Fields(l)
		if len(parts) >= 3 && parts[2] == "failed:" {
			return parts[1]
		}
	}
	return ""
}

// ParsedAccount returns a named account of a pre-parsed call, by either
// spelling of the name.
func (c Call) ParsedAccount(name string) string {
	if c.ParsedInfo == nil {
		return ""
	}
	for _, k := range []string{name, camel(name)} {
		if v, ok := c.ParsedInfo[k].(string); ok {
			return v
		}
	}
	return ""
}

func camel(s string) string {
	out := make([]byte, 0, len(s))
	up := false
	for i := 0; i < len(s); i++ {
		if s[i] == '_' {
			up = true
			continue
		}
		if up && s[i] >= 'a' && s[i] <= 'z' {
			out = append(out, s[i]-32)
		} else {
			out = append(out, s[i])
		}
		up = false
	}
	return string(out)
}

// SimulateReplay re-executes a finalized transaction against current state:
// signatures are skipped and the blockhash replaced, so an old transaction
// runs again without its signers. Returns the raw simulateTransaction value.
func (c *Client) SimulateReplay(ctx context.Context, signature string) (json.RawMessage, error) {
	raw, err := c.Call(ctx, "getTransaction", signature, map[string]any{"encoding": "base64", "commitment": "finalized", "maxSupportedTransactionVersion": 0})
	if err != nil {
		return nil, err
	}
	var tx struct {
		Transaction []string `json:"transaction"`
	}
	if err := json.Unmarshal(raw, &tx); err != nil || len(tx.Transaction) == 0 {
		return nil, &RPCError{Method: "getTransaction", Message: "transaction not found"}
	}
	return c.Call(ctx, "simulateTransaction", tx.Transaction[0], map[string]any{
		"encoding": "base64", "sigVerify": false, "replaceRecentBlockhash": true, "commitment": "confirmed",
	})
}
