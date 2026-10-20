package chain

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mr-tron/base58"
)

func TestParseCallsReadsPrivilegesDataAndCPI(t *testing.T) {
	data := base58.Encode([]byte{1, 2, 3, 4, 5, 6, 7, 8, 9})
	raw := `{"slot": 10, "blockTime": 1700000000,
	 "meta": {"err": null, "innerInstructions": [{"index": 1, "instructions": [{"programId": "TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA", "accounts": ["A"], "data": ""}]}]},
	 "transaction": {"signatures": ["sig1"], "message": {
	   "accountKeys": [{"pubkey": "A", "signer": true, "writable": true}, {"pubkey": "B", "signer": false, "writable": false}, {"pubkey": "P", "signer": false, "writable": false}],
	   "instructions": [
	     {"program": "system", "programId": "11111111111111111111111111111111", "parsed": {"type": "transfer", "info": {"source": "A", "destination": "B", "lamports": 5}}},
	     {"programId": "P", "accounts": ["A", "B"], "data": "` + data + `"}
	   ]}}}`
	tx, err := ParseCalls(json.RawMessage(raw))
	if err != nil {
		t.Fatal(err)
	}
	if tx.Signature != "sig1" || tx.Slot != 10 || tx.BlockTime == nil || tx.Failed {
		t.Fatalf("header: %+v", tx)
	}
	if len(tx.Calls) != 2 || tx.Calls[0].ProgramID != "11111111111111111111111111111111" || len(tx.Calls[0].Data) != 0 {
		t.Fatalf("calls: %+v", tx.Calls)
	}
	if tx.Calls[0].ParsedType != "transfer" {
		t.Fatalf("parsed type: %+v", tx.Calls[0])
	}
	c := tx.Calls[1]
	if c.ProgramID != "P" || len(c.Accounts) != 2 || !c.Accounts[0].Signer || !c.Accounts[0].Writable || c.Accounts[1].Signer {
		t.Fatalf("privileges: %+v", c.Accounts)
	}
	if len(c.Data) != 9 || c.Data[0] != 1 {
		t.Fatalf("data: %v", c.Data)
	}
	if len(c.Inner) != 1 || c.Inner[0] != "TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA" {
		t.Fatalf("inner: %v", c.Inner)
	}
	if len(c.InnerCalls) != 1 || c.InnerCalls[0].ProgramID != c.Inner[0] || len(c.InnerCalls[0].Accounts) != 1 || !c.InnerCalls[0].Accounts[0].Signer {
		t.Fatalf("inner calls: %+v", c.InnerCalls)
	}
}

func TestParsedAccountByEitherSpelling(t *testing.T) {
	c := Call{ParsedInfo: map[string]any{"tokenAmount": "1", "destination": "D", "mintAuthority": "M"}}
	if c.ParsedAccount("destination") != "D" || c.ParsedAccount("mint_authority") != "M" || c.ParsedAccount("nope") != "" {
		t.Fatal("parsed account lookup")
	}
}

func TestParseCallsReadsMetaOutcomeFacts(t *testing.T) {
	raw := `{"slot": 11, "blockTime": 1700000000,
	 "meta": {"err": {"InstructionError": [0, {"Custom": 6001}]}, "computeUnitsConsumed": 12345,
	   "logMessages": ["Program P invoke [1]", "Program log: Instruction: Swap", "Program Tok invoke [2]", "Program Tok consumed 2000 of 190000 compute units", "Program Tok success", "Program P consumed 9000 of 200000 compute units", "Program P failed: custom program error: 0x1771"],
	   "preTokenBalances": [
	     {"accountIndex": 1, "mint": "M1", "owner": "A", "uiTokenAmount": {"amount": "1000", "decimals": 6}},
	     {"accountIndex": 2, "mint": "M1", "owner": "B", "uiTokenAmount": {"amount": "50", "decimals": 6}},
	     {"accountIndex": 3, "mint": "M2", "owner": "A", "uiTokenAmount": {"amount": "7", "decimals": 9}}],
	   "postTokenBalances": [
	     {"accountIndex": 1, "mint": "M1", "owner": "A", "uiTokenAmount": {"amount": "700", "decimals": 6}},
	     {"accountIndex": 2, "mint": "M1", "owner": "B", "uiTokenAmount": {"amount": "350", "decimals": 6}},
	     {"accountIndex": 3, "mint": "M2", "owner": "A", "uiTokenAmount": {"amount": "7", "decimals": 9}},
	     {"accountIndex": 4, "mint": "M2", "owner": "C", "uiTokenAmount": {"amount": "12", "decimals": 9}}],
	   "innerInstructions": []},
	 "transaction": {"signatures": ["sig2"], "message": {
	   "accountKeys": [{"pubkey": "A", "signer": true, "writable": true}, {"pubkey": "T1", "signer": false, "writable": true}, {"pubkey": "T2", "signer": false, "writable": true}, {"pubkey": "T3", "signer": false, "writable": true}, {"pubkey": "T4", "signer": false, "writable": true}, {"pubkey": "S", "signer": true, "writable": false}, {"pubkey": "A", "signer": true, "writable": true}],
	   "instructions": [{"programId": "P", "accounts": ["A", "T1"], "data": ""}]}}}`
	tx, err := ParseCalls(json.RawMessage(raw))
	if err != nil {
		t.Fatal(err)
	}
	if !tx.Failed || tx.Err == nil || tx.Units != 12345 || len(tx.Logs) != 7 {
		t.Fatalf("meta: failed=%v err=%v units=%d logs=%d", tx.Failed, tx.Err, tx.Units, len(tx.Logs))
	}
	if len(tx.Signers) != 2 || tx.Signers[0] != "A" || tx.Signers[1] != "S" {
		t.Fatalf("signers: %v", tx.Signers)
	}
	if n, ok := tx.UnitsFor("P"); !ok || n != 9000 {
		t.Fatalf("units for P: %d %v", n, ok)
	}
	if n, ok := tx.UnitsFor("Tok"); !ok || n != 2000 {
		t.Fatalf("units for Tok: %d %v", n, ok)
	}
	if _, ok := tx.UnitsFor("Nope"); ok {
		t.Fatal("units for an absent program")
	}
	if tx.FailedIn() != "P" {
		t.Fatalf("failed in %q", tx.FailedIn())
	}
	want := []TokenDelta{
		{Mint: "M1", Owner: "A", Decimals: 6, Delta: "-300"},
		{Mint: "M1", Owner: "B", Decimals: 6, Delta: "300"},
		{Mint: "M2", Owner: "C", Decimals: 9, Delta: "12"},
	}
	if len(tx.TokenDeltas) != len(want) {
		t.Fatalf("deltas: %+v", tx.TokenDeltas)
	}
	for i := range want {
		if tx.TokenDeltas[i] != want[i] {
			t.Errorf("delta %d = %+v want %+v", i, tx.TokenDeltas[i], want[i])
		}
	}
	ok, _ := ParseCalls(json.RawMessage(`{"meta": {"err": null, "logMessages": ["Program P invoke [1]", "Program P success"]}, "transaction": {"signatures": ["s"], "message": {"accountKeys": [], "instructions": []}}}`))
	if ok.Failed || ok.FailedIn() != "" || len(ok.TokenDeltas) != 0 {
		t.Fatalf("clean tx: %+v", ok)
	}
}

func TestParseCallsStackHeights(t *testing.T) {
	raw := []byte(`{"slot":1,"meta":{"err":null,"innerInstructions":[{"index":0,"instructions":[
	  {"programId":"Tok","accounts":[],"data":"","stackHeight":2},
	  {"programId":"Thread","accounts":[],"data":"","stackHeight":2},
	  {"programId":"Fiber","accounts":[],"data":"","stackHeight":3},
	  {"programId":"Sys","accounts":[],"data":"","stackHeight":4},
	  {"programId":"Memo","accounts":[],"data":"","stackHeight":2}]}]},
	 "transaction":{"signatures":["sig"],"message":{"accountKeys":[],"instructions":[{"programId":"Outer","accounts":[],"data":""}]}}}`)
	tx, err := ParseCalls(raw)
	if err != nil {
		t.Fatal(err)
	}
	top := tx.Calls[0]
	if got := strings.Join(top.Direct, ","); got != "Tok,Thread,Memo" {
		t.Errorf("direct callees of the top-level call: %s", got)
	}
	if len(top.Inner) != 5 {
		t.Errorf("Inner keeps every program beneath: %v", top.Inner)
	}
	thread, fiber, sys := top.InnerCalls[1], top.InnerCalls[2], top.InnerCalls[3]
	if thread.Parent != -1 || thread.Depth != 2 || strings.Join(thread.Inner, ",") != "Fiber" {
		t.Errorf("thread: %+v", thread)
	}
	if fiber.Parent != 1 || strings.Join(fiber.Inner, ",") != "Sys" {
		t.Errorf("fiber is called by thread and calls the system program: %+v", fiber)
	}
	if sys.Parent != 2 || sys.Depth != 4 {
		t.Errorf("sys: %+v", sys)
	}
	if top.InnerCalls[4].Parent != -1 {
		t.Errorf("memo returns to the top level: %+v", top.InnerCalls[4])
	}
}
