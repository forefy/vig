package solana

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/forefy/vig/backend/internal/chain"
)

func TestDecodeFieldsReadsSizedAndSequencedFields(t *testing.T) {
	idl, err := parseIDL([]byte(`{"instructions":[],"accounts":[{"name":"State","discriminator":[1,2,3,4,5,6,7,8]}],
	  "types":[{"name":"State","type":{"kind":"struct","fields":[{"name":"fee_bps","type":"u16"},{"name":"paused","type":"bool"},{"name":"admin","type":"pubkey"},{"name":"cfg","type":{"defined":"Cfg"}},{"name":"names","type":{"vec":"string"}},{"name":"after","type":"u8"}]}},
	          {"name":"Cfg","type":{"kind":"struct","fields":[{"name":"cap","type":"u64"}]}}],
	  "errors":[{"code":6000,"name":"FeeTooLow","msg":"fee below floor"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	data := []byte{1, 2, 3, 4, 5, 6, 7, 8}
	data = binary.LittleEndian.AppendUint16(data, 30)
	data = append(data, 1)
	data = append(data, make([]byte, 32)...)
	data = binary.LittleEndian.AppendUint64(data, 1_000_000)
	data = append(data, 0, 0, 0, 0, 7)
	st, typ := decodeState(chain.RawAccount{Exists: true, Lamports: 5, Owner: "Prog", Data: data}, idl)
	if typ != "State" {
		t.Fatalf("type %q", typ)
	}
	// The vec<string> is empty, so the field after it decodes too.
	want := map[string]string{"fee_bps": "30", "paused": "true", "admin": "11111111111111111111111111111111", "cfg.cap": "1000000", "names": "[]", "after": "7"}
	for k, v := range want {
		if st.Fields[k] != v {
			t.Errorf("%s = %q want %q", k, st.Fields[k], v)
		}
	}
	e := decodeError(json.RawMessage(`{"InstructionError":[0,{"Custom":6000}]}`), idl)
	if e.Name != "FeeTooLow" || e.Code != 6000 || e.Raw != "FeeTooLow" {
		t.Errorf("error: %+v", e)
	}
	if e := decodeError(json.RawMessage(`{"InstructionError":[0,{"Custom":3012}]}`), idl); e.Name != "AccountNotInitialized" {
		t.Errorf("framework error: %+v", e)
	}
	var generic any
	_ = json.Unmarshal([]byte(`{"InstructionError":[0,{"Custom":6000}]}`), &generic)
	if decodeError(generic, idl).Name != "FeeTooLow" {
		t.Error("error from decoded json")
	}
	tok := make([]byte, 165)
	binary.LittleEndian.PutUint64(tok[64:], 42)
	st, typ = decodeState(chain.RawAccount{Exists: true, Owner: chain.TokenProgram, Data: tok}, idl)
	if typ != "token" || st.Fields["amount"] != "42" {
		t.Errorf("token: %s %+v", typ, st.Fields)
	}
	calls := callsFromLogs([]string{"Program MarB invoke [1]", "Program log: Instruction: LiquidUnstake", "Program Tok invoke [2]", "Program log: Instruction: Burn", "Program Tok success", "Program MarB success"})
	if len(calls) != 2 || calls[0].Name != "liquid_unstake" || calls[1].Depth != 2 || calls[1].Name != "burn" {
		t.Errorf("calls: %+v", calls)
	}
	if got := changed(&ReplayState{Lamports: 1, Fields: map[string]string{"a": "1"}}, &ReplayState{Lamports: 2, Fields: map[string]string{"a": "2"}}); len(got) != 2 {
		t.Errorf("changed: %v", got)
	}
}

func TestSeedsAndPDAFacts(t *testing.T) {
	idl, err := parseIDL([]byte(`{"instructions":[{"name":"claim","discriminator":[1,1,1,1,1,1,1,1],"accounts":[{"name":"state","writable":true},{"name":"reserve_pda","pda":{"seeds":[{"kind":"account","path":"state"},{"kind":"const","value":[114,101,115,101,114,118,101]}]}}],"args":[]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	a := idl.Instructions[0].Accounts
	if len(a) != 2 || a[1].Seeds != `state, "reserve"` {
		t.Fatalf("seeds: %+v", a)
	}
	nodes := programNodes("P", idl.Instructions, "idl")
	if nodes[3].Facts["seeds"] != `state, "reserve"` {
		t.Errorf("seed fact missing: %+v", nodes[3])
	}
}

func TestCallArgsFromIDLAndParsedForm(t *testing.T) {
	idl, _ := parseIDL([]byte(`{"instructions":[{"name":"deposit","discriminator":[9,9,9,9,9,9,9,9],"accounts":[],"args":[{"name":"lamports","type":"u64"}]}]}`))
	data := []byte{9, 9, 9, 9, 9, 9, 9, 9}
	data = binary.LittleEndian.AppendUint64(data, 111000000)
	if got := callArgs("P", chain.Call{Data: data}, idl); got["lamports"] != "111000000" {
		t.Errorf("idl args: %v", got)
	}
	parsed := chain.Call{ParsedType: "transferChecked", ParsedInfo: map[string]any{"source": "11111111111111111111111111111111", "tokenAmount": map[string]any{"amount": "9915000", "decimals": float64(6)}, "authority": "11111111111111111111111111111111"}}
	if got := callArgs(chain.TokenProgram, parsed, nil); got["token_amount"] != "9915000" || len(got) != 1 {
		t.Errorf("parsed args: %v", got)
	}
	if got := callArgs("P", chain.Call{Data: []byte{1, 2}}, idl); got != nil {
		t.Errorf("short data: %v", got)
	}
}

func TestWatchedPrefersWritableWithinLimit(t *testing.T) {
	accts := []chain.CallAccount{{Pubkey: "a"}, {Pubkey: "b", Writable: true}, {Pubkey: "c"}, {Pubkey: "b", Writable: true}, {Pubkey: "d", Writable: true}}
	got := watched(accts, 3)
	if len(got) != 3 || got[0] != 1 || got[1] != 4 || got[2] != 0 {
		t.Errorf("watched %v", got)
	}
	if all := watched(accts, 27); len(all) != 4 {
		t.Errorf("dedup %v", all)
	}
}

func TestKnownNameResolvesMapNamesThenHashes(t *testing.T) {
	create := anchorDiscriminator("global:create")
	fromBytecode := anchorDiscriminator("global:update")
	known := map[string]string{"create": "", "update": hex.EncodeToString(fromBytecode[:])}
	if got := knownName(create[:], known); got != "create" {
		t.Fatalf("source name: got %q", got)
	}
	if got := knownName(fromBytecode[:], known); got != "update" {
		t.Fatalf("bytecode name: got %q", got)
	}
	other := anchorDiscriminator("global:swap")
	if got := knownName(other[:], known); got != discriminatorName(other[:]) {
		t.Fatalf("unknown call: got %q", got)
	}
}

func TestPDACheckMatchesAssociatedTokenDerivation(t *testing.T) {
	owner := chain.Encode(bytes.Repeat([]byte{7}, 32))
	mint := "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v"
	ata, err := chain.AssociatedTokenAddress(owner, mint)
	if err != nil {
		t.Fatal(err)
	}
	prog, _ := chain.Decode(chain.AssociatedTokenProg)
	idl, err := parseIDL([]byte(`{"instructions":[{"name":"open","discriminator":[2,2,2,2,2,2,2,2],
	  "accounts":[{"name":"owner","signer":true},{"name":"token_program"},{"name":"mint"},
	    {"name":"vault","pda":{"seeds":[{"kind":"account","path":"owner"},{"kind":"account","path":"token_program"},{"kind":"account","path":"mint"}],
	      "program":{"kind":"const","value":[` + joinBytes(prog) + `]}}}],"args":[]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	spec := &idl.Instructions[0]
	call := chain.Call{ProgramID: "P", Data: []byte{2, 2, 2, 2, 2, 2, 2, 2}, Accounts: []chain.CallAccount{{Pubkey: owner}, {Pubkey: chain.TokenProgram}, {Pubkey: mint}, {Pubkey: ata}}}
	if got := pdaCheck(spec, 3, call, chain.TokenProgram); got != "ok" {
		t.Fatalf("ata via general derivation: %q", got)
	}
	if got := pdaCheck(spec, 0, call, chain.TokenProgram); got != "" {
		t.Fatalf("account without seeds: %q", got)
	}
	call.Accounts[3].Pubkey = owner
	if got := pdaCheck(spec, 3, call, chain.TokenProgram); got != "mismatch" {
		t.Fatalf("wrong address: %q", got)
	}
}

func TestPDACheckResolvesArgSeeds(t *testing.T) {
	pid := chain.Encode(bytes.Repeat([]byte{9}, 32))
	pidB, _ := chain.Decode(pid)
	state := chain.Encode(bytes.Repeat([]byte{3}, 32))
	stateB, _ := chain.Decode(state)
	idl, err := parseIDL([]byte(`{"instructions":[{"name":"claim","discriminator":[1,1,1,1,1,1,1,1],
	  "accounts":[{"name":"state"},{"name":"slot","pda":{"seeds":[{"kind":"const","value":[118,97,117,108,116]},{"kind":"account","path":"state"},{"kind":"arg","path":"id"},{"kind":"arg","path":"label"}]}}],
	  "args":[{"name":"amount","type":"u64"},{"name":"id","type":"u16"},{"name":"label","type":"string"},{"name":"tail","type":"u8"}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	spec := &idl.Instructions[0]
	if len(spec.Accounts[1].PDASeeds) != 4 || spec.Accounts[1].Seeds != `"vault", state, id, label` {
		t.Fatalf("seeds: %+v %q", spec.Accounts[1].PDASeeds, spec.Accounts[1].Seeds)
	}
	data := []byte{1, 1, 1, 1, 1, 1, 1, 1}
	data = binary.LittleEndian.AppendUint64(data, 5)
	data = binary.LittleEndian.AppendUint16(data, 42)
	data = binary.LittleEndian.AppendUint32(data, 2)
	data = append(data, 'h', 'i', 9)
	want, _, _ := chain.FindProgramAddress([][]byte{[]byte("vault"), stateB, {42, 0}, []byte("hi")}, pidB)
	call := chain.Call{ProgramID: pid, Data: data, Accounts: []chain.CallAccount{{Pubkey: state}, {Pubkey: chain.Encode(want)}}}
	if got := pdaCheck(spec, 1, call, pid); got != "ok" {
		t.Fatalf("arg seeds: %q", got)
	}
	// An argument after a variable-size field the walker cannot size, or
	// data too short for the seed, gives no verdict.
	spec.Accounts[1].PDASeeds[2].Path = "nope"
	if got := pdaCheck(spec, 1, call, pid); got != "" {
		t.Fatalf("unresolvable seed: %q", got)
	}
	spec.Accounts[1].PDASeeds[2].Path = "id"
	call.Data = data[:12]
	if got := pdaCheck(spec, 1, call, pid); got != "" {
		t.Fatalf("short data: %q", got)
	}
}

func TestLegacySeedShapeParses(t *testing.T) {
	idl, err := parseIDL([]byte(`{"instructions":[{"name":"init","accounts":[{"name":"cfg","isMut":true,"isSigner":false,"pda":{"seeds":[{"kind":"const","type":"string","value":"config"},{"kind":"const","type":"u16","value":7},{"kind":"account","type":"publicKey","path":"authority"}]}}],"args":[]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	seeds := idl.Instructions[0].Accounts[0].PDASeeds
	if len(seeds) != 3 || string(seeds[0].Value) != "config" || !bytes.Equal(seeds[1].Value, []byte{7, 0}) || seeds[2].Path != "authority" {
		t.Fatalf("legacy seeds: %+v", seeds)
	}
}

func TestTokenMovesNetPerMint(t *testing.T) {
	m1 := chain.Encode(bytes.Repeat([]byte{1}, 32))
	m2 := chain.Encode(bytes.Repeat([]byte{2}, 32))
	n := byte(10)
	tok := func(mint, before, after string) ReplayAccount {
		n++
		return ReplayAccount{Pubkey: chain.Encode(bytes.Repeat([]byte{n}, 32)), Type: "token",
			Before: &ReplayState{Fields: map[string]string{"mint": mint, "amount": before}},
			After:  &ReplayState{Fields: map[string]string{"mint": mint, "amount": after}}}
	}
	accounts := []ReplayAccount{
		tok(m1, "1000", "700"),
		tok(m1, "50", "350"),
		tok(m2, "7", "7"),
		tok(m2, "0", "12"),
		{Pubkey: m2, Type: "mint", Before: &ReplayState{Fields: map[string]string{"decimals": "9", "supply": "1"}}},
		{Pubkey: "x", Type: "State"},
	}
	got := tokenMoves(accounts)
	if len(got) != 1 || got[0].Mint != m2 || got[0].Delta != "12" || got[0].Decimals != 9 {
		t.Fatalf("moves: %+v", got)
	}
	// A repeated account counts once.
	if dup := tokenMoves(append(accounts, accounts[3])); len(dup) != 1 || dup[0].Delta != "12" {
		t.Fatalf("dedup: %+v", dup)
	}
	accounts[1] = tok(m1, "50", "300")
	got = tokenMoves(accounts)
	if len(got) != 2 || got[0].Mint != m1 || got[0].Delta != "-50" || got[0].Decimals != 0 {
		t.Fatalf("net move: %+v", got)
	}
	if tokenMoves([]ReplayAccount{{Type: "token", Before: &ReplayState{Fields: map[string]string{"mint": m1, "amount": "5"}}}}) != nil {
		t.Fatal("a failed replay has no after state and no moves")
	}
}

func joinBytes(b []byte) string {
	parts := make([]string, len(b))
	for i, c := range b {
		parts[i] = strconv.Itoa(int(c))
	}
	return strings.Join(parts, ",")
}

func TestDecodeScalarShapes(t *testing.T) {
	idl := &idlSpec{types: map[string]json.RawMessage{
		"Trigger": json.RawMessage(`{"kind":"enum","variants":[{"name":"Now"},{"name":"Cron","fields":[{"name":"schedule","type":"string"},{"name":"skippable","type":"bool"}]},{"name":"Slot","fields":[{"name":"0","type":"u64"}]}]}`),
	}}
	into := map[string]string{}
	if _, _, ok := decodeScalar([]byte{1}, json.RawMessage(`{"option":"u8"}`), idl, "x", into); ok {
		t.Error("some with no payload byte must fail")
	}
	v, n, ok := decodeScalar([]byte{0}, json.RawMessage(`{"option":"u8"}`), idl, "x", into)
	if !ok || v != "none" || n != 1 {
		t.Errorf("none: %q %d %v", v, n, ok)
	}
	v, n, ok = decodeScalar([]byte{1, 9}, json.RawMessage(`{"option":"u8"}`), idl, "x", into)
	if !ok || v != "9" || n != 2 {
		t.Errorf("some: %q %d %v", v, n, ok)
	}
	v, n, ok = decodeScalar([]byte{3, 0, 0, 0, 1, 0, 2, 0, 3, 0}, json.RawMessage(`{"vec":"u16"}`), idl, "x", into)
	if !ok || v != "[1, 2, 3]" || n != 10 {
		t.Errorf("vec: %q %d %v", v, n, ok)
	}
	v, n, ok = decodeScalar([]byte{2, 0, 0, 0, 'h', 'i'}, json.RawMessage(`"string"`), idl, "x", into)
	if !ok || v != "hi" || n != 6 {
		t.Errorf("string: %q %d %v", v, n, ok)
	}
	v, n, ok = decodeScalar(append([]byte{1, 3, 0, 0, 0, 'a', 'b', 'c'}, 1), json.RawMessage(`{"defined":"Trigger"}`), idl, "trigger", into)
	if !ok || v != "Cron" || n != 9 || into["trigger.schedule"] != "abc" || into["trigger.skippable"] != "true" {
		t.Errorf("enum: %q %d %v %v", v, n, ok, into)
	}
	if v, _, ok = decodeScalar([]byte{2, 5, 0, 0, 0, 0, 0, 0, 0}, json.RawMessage(`{"defined":"Trigger"}`), idl, "t", into); !ok || v != "Slot" || into["t.0"] != "5" {
		t.Errorf("tuple variant: %q %v %v", v, ok, into)
	}
	if _, _, ok = decodeScalar([]byte{7}, json.RawMessage(`{"defined":"Trigger"}`), idl, "t", into); ok {
		t.Error("unknown variant must fail")
	}
}
