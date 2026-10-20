package chain

import (
	"context"
	"encoding/base64"
	"encoding/json"
)

// RawAccount is an account as getMultipleAccounts or a simulation returns it.
type RawAccount struct {
	Exists     bool
	Executable bool
	Lamports   uint64
	Owner      string
	Data       []byte
}

type rawAccountJSON struct {
	Lamports   uint64   `json:"lamports"`
	Owner      string   `json:"owner"`
	Executable bool     `json:"executable"`
	Data       []string `json:"data"`
}

func (r *rawAccountJSON) account() RawAccount {
	a := RawAccount{Exists: true, Executable: r.Executable, Lamports: r.Lamports, Owner: r.Owner}
	if len(r.Data) > 0 {
		a.Data, _ = base64.StdEncoding.DecodeString(r.Data[0])
	}
	return a
}

// GetMultipleAccounts reads up to 100 accounts in one call.
func (c *Client) GetMultipleAccounts(ctx context.Context, pubkeys []string) ([]RawAccount, error) {
	raw, err := c.Call(ctx, "getMultipleAccounts", pubkeys, map[string]any{"encoding": "base64", "commitment": "confirmed"})
	if err != nil {
		return nil, err
	}
	var res struct {
		Value []*rawAccountJSON `json:"value"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, err
	}
	out := make([]RawAccount, len(res.Value))
	for i, v := range res.Value {
		if v != nil {
			out[i] = v.account()
		}
	}
	return out, nil
}

// TransactionBase64 fetches a finalized transaction's wire bytes.
func (c *Client) TransactionBase64(ctx context.Context, signature string) (string, error) {
	raw, err := c.Call(ctx, "getTransaction", signature, map[string]any{"encoding": "base64", "commitment": "finalized", "maxSupportedTransactionVersion": 0})
	if err != nil {
		return "", err
	}
	var tx struct {
		Transaction []string `json:"transaction"`
	}
	if err := json.Unmarshal(raw, &tx); err != nil || len(tx.Transaction) == 0 {
		return "", &RPCError{Method: "getTransaction", Message: "transaction not found"}
	}
	return tx.Transaction[0], nil
}

// Simulation is what a replay produced.
type Simulation struct {
	Err           any
	UnitsConsumed uint64
	Logs          []string
	ReturnData    string
	Inner         []InnerCall
	Accounts      []RawAccount // in the order requested
}

// InnerCall is one CPI a simulation recorded; ProgramIndex points into the
// transaction's account keys.
type InnerCall struct {
	Index        int
	ProgramIndex int
	StackHeight  int
}

// Simulate re-executes a base64 transaction against current state with
// signatures skipped and the blockhash replaced, reading the post-state of
// the given accounts and the inner instructions it made.
func (c *Client) Simulate(ctx context.Context, txBase64 string, accounts []string) (Simulation, error) {
	cfg := map[string]any{"encoding": "base64", "sigVerify": false, "replaceRecentBlockhash": true, "commitment": "confirmed", "innerInstructions": true}
	if len(accounts) > 0 {
		cfg["accounts"] = map[string]any{"encoding": "base64", "addresses": accounts}
	}
	raw, err := c.Call(ctx, "simulateTransaction", txBase64, cfg)
	if err != nil {
		return Simulation{}, err
	}
	var res struct {
		Value struct {
			Err           any               `json:"err"`
			UnitsConsumed uint64            `json:"unitsConsumed"`
			Logs          []string          `json:"logs"`
			Accounts      []*rawAccountJSON `json:"accounts"`
			ReturnData    *struct {
				Data []string `json:"data"`
			} `json:"returnData"`
			InnerInstructions []struct {
				Index        int `json:"index"`
				Instructions []struct {
					ProgramIDIndex int `json:"programIdIndex"`
					StackHeight    int `json:"stackHeight"`
				} `json:"instructions"`
			} `json:"innerInstructions"`
		} `json:"value"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return Simulation{}, err
	}
	s := Simulation{Err: res.Value.Err, UnitsConsumed: res.Value.UnitsConsumed, Logs: res.Value.Logs}
	if res.Value.ReturnData != nil && len(res.Value.ReturnData.Data) > 0 {
		s.ReturnData = res.Value.ReturnData.Data[0]
	}
	s.Accounts = make([]RawAccount, len(res.Value.Accounts))
	for i, a := range res.Value.Accounts {
		if a != nil {
			s.Accounts[i] = a.account()
		}
	}
	for _, ii := range res.Value.InnerInstructions {
		for _, ins := range ii.Instructions {
			s.Inner = append(s.Inner, InnerCall{Index: ii.Index, ProgramIndex: ins.ProgramIDIndex, StackHeight: ins.StackHeight})
		}
	}
	return s, nil
}
