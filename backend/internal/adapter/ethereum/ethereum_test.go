package ethereum

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/forefy/vig/backend/internal/chain"
	"github.com/forefy/vig/backend/internal/chain/chaintest"
	"github.com/forefy/vig/backend/internal/core"
	"github.com/forefy/vig/backend/internal/evm"
	"github.com/forefy/vig/backend/internal/solsrc"
)

const (
	tAddr   = "0x1111111111111111111111111111111111111111"
	tRouter = "0x2222222222222222222222222222222222222222"
	tUser   = "0x3333333333333333333333333333333333333333"
	tOther  = "0x4444444444444444444444444444444444444444"
)

const tABI = `[{"type":"function","name":"transfer","inputs":[{"name":"to","type":"address"},{"name":"amount","type":"uint256"}]},
{"type":"function","name":"approve","inputs":[{"name":"spender","type":"address"},{"name":"amount","type":"uint256"}]},
{"type":"event","name":"Transfer","inputs":[{"name":"from","type":"address","indexed":true},{"name":"to","type":"address","indexed":true},{"name":"value","type":"uint256"}]}]`

func word(h string) string { return strings.Repeat("0", 64-len(h)) + h }
func selector(sig string) string {
	h := evm.Keccak([]byte(sig))
	return hex.EncodeToString(h[:4])
}

// transferCall is transfer(tOther, 1000) as calldata.
var transferCall = "0x" + selector("transfer(address,uint256)") + word(tOther[2:]) + word("3e8")

// fakeChain answers the RPC methods the adapter reads: one contract with
// code, no proxy, a direct call in the head block, an event from a
// transaction sent to a router in the block before.
func fakeChain(t *testing.T) *httptest.Server {
	t.Helper()
	topic := evm.Keccak([]byte("Transfer(address,address,uint256)"))
	logs := []any{
		map[string]any{"address": tAddr, "topics": []string{"0x" + hex.EncodeToString(topic[:]), "0x" + word(tUser[2:]), "0x" + word(tOther[2:])}, "data": "0x" + word("3e8"), "transactionHash": "0xdirect", "blockNumber": "0x64"},
		map[string]any{"address": tAddr, "topics": []string{"0x" + hex.EncodeToString(topic[:]), "0x" + word(tRouter[2:]), "0x" + word(tUser[2:])}, "data": "0x" + word("7d0"), "transactionHash": "0xindirect", "blockNumber": "0x63"},
	}
	blocks := map[string]any{
		"0x64": map[string]any{"number": "0x64", "hash": "0xb100", "timestamp": "0x66000000", "transactions": []any{
			map[string]any{"hash": "0xdirect", "from": tUser, "to": tAddr, "input": transferCall, "value": "0x0"},
			map[string]any{"hash": "0xelse", "from": tUser, "to": tOther, "input": "0x", "value": "0x1"},
		}},
		"0x63": map[string]any{"number": "0x63", "hash": "0xb099", "timestamp": "0x65fffff4", "transactions": []any{
			map[string]any{"hash": "0xindirect", "from": tUser, "to": tRouter, "input": "0x" + selector("swap(uint256)") + word("1"), "value": "0x0"},
		}},
		"0x65": map[string]any{"number": "0x65", "hash": "0xb101", "timestamp": "0x6600000c", "transactions": []any{
			map[string]any{"hash": "0xlive", "from": tUser, "to": tAddr, "input": "0x" + selector("approve(address,uint256)") + word(tRouter[2:]) + word("5"), "value": "0x0"},
		}},
		"0x10": map[string]any{"number": "0x10", "hash": "0xb016", "timestamp": "0x6574e000", "transactions": []any{
			map[string]any{"hash": "0xold", "from": tOther, "to": tAddr, "input": "0x" + selector("approve(address,uint256)") + word(tUser[2:]) + word("9"), "value": "0x0"},
		}},
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/api/v2/addresses/") && strings.HasSuffix(r.URL.Path, "/token-balances") {
			// Blockscout: the contract holds 5 TKN.
			if strings.Contains(strings.ToLower(r.URL.Path), tAddr) {
				w.Write([]byte(`[{"token":{"address_hash":"` + tOther + `","symbol":"TKN","decimals":"18","type":"ERC-20"},"value":"5000000000000000000"}]`))
				return
			}
			w.Write([]byte(`[]`))
			return
		}
		if strings.Contains(r.URL.Path, "/api/v2/addresses/") {
			// Blockscout: one old transaction to the contract, whole history.
			if strings.Contains(strings.ToLower(r.URL.Path), tAddr) {
				w.Write([]byte(`{"items":[{"hash":"0xold","block_number":16,"timestamp":"2023-12-09T21:45:36.000000Z"}],"next_page_params":null}`))
				return
			}
			w.Write([]byte(`{"items":[],"next_page_params":null}`))
			return
		}
		if strings.Contains(r.URL.Path, "/contract/") {
			// Sourcify: verified for the contract, unknown for the rest.
			if strings.Contains(strings.ToLower(r.URL.Path), tAddr) {
				w.Write([]byte(`{"abi":` + tABI + `,"match":"exact_match","compilation":{"compilerVersion":"0.8.33+commit.64118f21","compilerSettings":{"viaIR":false,"evmVersion":"cancun"}}}`))
				return
			}
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var req struct {
			ID     int               `json:"id"`
			Method string            `json:"method"`
			Params []json.RawMessage `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		var result any
		str := func(i int) string {
			var s string
			if i < len(req.Params) {
				_ = json.Unmarshal(req.Params[i], &s)
			}
			return strings.ToLower(s)
		}
		switch req.Method {
		case "eth_getCode":
			if str(0) == tAddr || str(0) == tRouter {
				result = "0x6080604052"
			} else {
				result = "0x"
			}
		case "eth_getStorageAt":
			result = "0x" + word("")
		case "eth_call", "eth_estimateGas":
			// A call from tOther reverts with Error("nope"); any other runs.
			// symbol() on tOther answers "TKN".
			var call struct {
				From string `json:"from"`
				To   string `json:"to"`
				Data string `json:"data"`
			}
			_ = json.Unmarshal(req.Params[0], &call)
			if strings.HasPrefix(call.Data, "0x95d89b41") {
				if strings.EqualFold(call.To, tOther) {
					result = "0x" + word("20") + word("3") + "544b4e" + strings.Repeat("0", 58)
				} else {
					result = "0x"
				}
				break
			}
			if strings.EqualFold(call.From, tOther) {
				msg := "0x08c379a0" + word("20") + word("4") + "6e6f7065" + strings.Repeat("0", 56)
				_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "error": map[string]any{"code": 3, "message": "execution reverted", "data": msg}})
				return
			}
			if req.Method == "eth_estimateGas" {
				result = "0x5208"
			} else {
				result = "0x" + word("1")
			}
		case "eth_blockNumber":
			result = "0x64"
		case "eth_getBlockByNumber":
			result = blocks[str(0)]
			if result == nil {
				// Any other block exists and is empty, as on a real chain.
				result = map[string]any{"number": str(0), "hash": "0xb" + str(0)[2:], "timestamp": "0x65fff000", "transactions": []any{}}
			}
		case "eth_getLogs":
			// A public endpoint refuses wide windows: wider than 90 blocks
			// here, which the sampler must halve down to.
			var f struct{ From, To string }
			_ = json.Unmarshal(req.Params[0], &struct {
				From *string `json:"fromBlock"`
				To   *string `json:"toBlock"`
			}{&f.From, &f.To})
			from, _ := strconv.ParseUint(strings.TrimPrefix(f.From, "0x"), 16, 64)
			to, _ := strconv.ParseUint(strings.TrimPrefix(f.To, "0x"), 16, 64)
			if to-from > 90 {
				_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "error": map[string]any{"code": -32602, "message": "Archive requests require a personal token"}})
				return
			}
			result = logs
		case "eth_getTransactionReceipt":
			status := "0x1"
			if str(0) == "0xlive" {
				status = "0x0"
			}
			// The direct transfer made the token at tOther emit: an observed call out.
			var rlogs []any
			if str(0) == "0xdirect" {
				rlogs = []any{map[string]any{"address": tOther, "topics": []string{"0x" + hex.EncodeToString(topic[:])}, "data": "0x" + word("1"), "transactionHash": "0xdirect", "blockNumber": "0x64"}}
			}
			result = map[string]any{"status": status, "gasUsed": "0x5208", "logs": rlogs}
		case "eth_getTransactionByHash":
			for _, b := range blocks {
				for _, tx := range b.(map[string]any)["transactions"].([]any) {
					m := tx.(map[string]any)
					if m["hash"] == str(0) {
						m2 := map[string]any{}
						for k, v := range m {
							m2[k] = v
						}
						m2["blockNumber"] = b.(map[string]any)["number"]
						result = m2
					}
				}
			}
		default:
			result = nil
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
	}))
}

func wired(t *testing.T, srv *httptest.Server) *Adapter {
	t.Helper()
	lookup := evm.NewLookup()
	lookup.Sourcify = srv.URL + "/contract/"
	lookup.FourByte = srv.URL + "/4byte/?hex_signature=0x"
	client := evm.New([]string{srv.URL})
	explorer := evm.NewExplorer(srv.URL)
	return &Adapter{Lookup: lookup, Clients: func(Chain) *evm.Client { return client }, Explorers: func(Chain) *evm.Explorer { return explorer }}
}

func checkout(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	src := `pragma solidity ^0.8.20;
contract Token {
    event Transfer(address indexed from, address indexed to, uint256 value);
    function transfer(address to, uint256 amount) external returns (bool) { return true; }
    function approve(address spender, uint256 amount) external returns (bool) { return true; }
}
`
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "Token.sol"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestInspectPanelAndFacts(t *testing.T) {
	srv := fakeChain(t)
	defer srv.Close()
	a := wired(t, srv)
	ctx := context.Background()
	f, err := a.Inspect(ctx, tAddr, Chains[0], checkout(t))
	if err != nil || !f.Exists || !f.Verified || f.Implementation != "" || f.ABI == nil || len(f.ABI.Functions) != 2 {
		t.Fatalf("facts: %+v %v", f, err)
	}
	if f.SourceMatch != "match" || f.SourceContract != "Token" {
		t.Errorf("source: %s %s (+%d chain, +%d source)", f.SourceMatch, f.SourceContract, f.ChainOnly, f.SourceOnly)
	}
	panel := Panel(f)
	rows := map[string]core.PanelRow{}
	for _, r := range panel.Rows {
		rows[r.Key] = r
	}
	if rows[RowCompiler].Value != "0.8.33" || rows[RowCompiler].Detail != "legacy codegen · cancun" || rows[RowHoldings].Value != "1 tokens" || rows[RowHoldings].Detail != "TKN 5" || !strings.Contains(rows[RowVerified].Detail, "exact match") {
		t.Errorf("compiler/holdings rows: %+v %+v %+v", rows[RowCompiler], rows[RowHoldings], rows[RowVerified])
	}
	if rows[RowExists].State != core.BadgeOK || rows[RowVerified].Value != "verified" || rows[RowProxy].Value != "no proxy" || rows[RowAdmin].Value != "immutable" || rows[RowInterface].Value != "2 functions" || rows[RowSource].State != core.BadgeOK {
		t.Errorf("panel: %+v", panel.Rows)
	}
	pf, err := a.InspectProgram(ctx, tAddr, core.InspectOpts{Chain: "1"})
	if err != nil || pf.Chain != "1" || pf.Loader != "evm" || !pf.IDLPresent || pf.VerifiedBuild != core.MatchVerified || pf.BytecodeSHA256 == "" {
		t.Errorf("program facts: %+v %v", pf, err)
	}
	missing, err := a.Inspect(ctx, tOther, Chains[0], "")
	if err != nil || missing.Exists {
		t.Errorf("missing contract: %+v %v", missing, err)
	}
	if _, err := a.InspectProgram(ctx, tAddr, core.InspectOpts{Chain: "999"}); err == nil {
		t.Error("unknown chain accepted")
	}
	if !a.ValidProgramID(tAddr) || a.ValidProgramID("GauwsjaNa7fmSRwCg9APFhZMkgxSz31q5HfNuLtRR522") {
		t.Error("address validation")
	}
}

func TestBuildSurfacePlacesCallsEventsAndCallers(t *testing.T) {
	srv := fakeChain(t)
	defer srv.Close()
	a := wired(t, srv)
	root := checkout(t)
	s, evidence, err := a.BuildSurface(context.Background(), core.SurfaceInput{Subjects: []string{tAddr}, Chains: map[string]string{tAddr: "1"}, Clone: &core.Clone{Root: root}, Budget: core.SurfaceBudget{Signatures: 25, Transactions: 10, Profiles: 4, Pace: time.Millisecond, Deadline: time.Minute}})
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]core.SurfaceNode{}
	for _, n := range s.Nodes {
		byID[n.ID] = n
	}
	p, ok := byID["p:"+tAddr]
	if !ok || p.Facts["crate"] != "Token" || p.Facts["events"] != "Transfer" || p.Facts["immutable"] != "true" || p.Facts["bytecode_now"] == "" {
		t.Fatalf("contract node: %+v", p)
	}
	if p.Facts["compiler"] != "0.8.33" || p.Facts["via_ir"] != "false" || p.Facts["evm_version"] != "cancun" || p.Facts["match_quality"] != "exact" || p.Facts["holdings"] != "TKN 5" || p.Facts["holdings_count"] != "1" {
		t.Errorf("build and holdings facts: %+v", p.Facts)
	}
	fn, ok := byID["ix:"+tAddr+":transfer"]
	if !ok || fn.Source != "idl" || fn.Facts["args"] != "to:address, amount:uint256" || fn.Facts["discriminator"] != selector("transfer(address,uint256)") || fn.Facts["file"] != "src/Token.sol" {
		t.Fatalf("function node: %+v", fn)
	}
	if _, ok := byID["p:"+tRouter]; !ok {
		t.Error("router caller node missing")
	}
	var direct, indirect, events int
	for _, ev := range evidence {
		switch {
		case ev.Kind == "tx" && ev.NodeID == "ix:"+tAddr+":transfer":
			direct++
			args, _ := ev.Summary["args"].(map[string]string)
			if args["amount"] != "1000" || !strings.EqualFold(args["to"], tOther) || ev.Summary["units"] != uint64(0x5208) {
				t.Errorf("direct row: %+v", ev.Summary)
			}
		case ev.Kind == "tx" && ev.NodeID == "p:"+tAddr:
			indirect++
			if ev.Summary["via"] != tRouter || ev.Summary["unresolved"] != true {
				t.Errorf("indirect row: %+v", ev.Summary)
			}
		case ev.Kind == "write":
			events++
			fields, _ := ev.Summary["fields"].(map[string]string)
			if ev.Summary["event"] != "Transfer" || fields["value"] == "" {
				t.Errorf("event row: %+v", ev.Summary)
			}
		}
	}
	if direct != 1 || indirect != 1 || events != 2 {
		t.Errorf("rows: direct=%d indirect=%d events=%d", direct, indirect, events)
	}
	var edge bool
	for _, e := range s.Edges {
		if e.From == "p:"+tRouter && e.To == "p:"+tAddr && e.Count == 1 {
			edge = true
		}
	}
	if !edge {
		t.Errorf("caller edge missing: %+v", s.Edges)
	}
	var reached bool
	for _, e := range s.Edges {
		if e.From == "ix:"+tAddr+":transfer" && e.To == "p:"+tOther && e.Kind == "cpi" && e.Source == "traffic" && e.Count == 1 {
			reached = true
		}
	}
	if !reached {
		t.Errorf("observed call-out edge missing: %+v", s.Edges)
	}
	if n, ok := byID["p:"+tOther]; !ok || !n.External || n.Name != "TKN" {
		t.Errorf("reached token node: %+v", n)
	}
	for _, ev := range evidence {
		if ev.Kind == "tx" && ev.NodeID == "ix:"+tAddr+":transfer" {
			if cpi, _ := ev.Summary["cpi"].([]string); len(cpi) != 1 || cpi[0] != tOther {
				t.Errorf("row cpi: %+v", ev.Summary["cpi"])
			}
		}
	}
	if s.Sampling.Transactions < 3 || s.Sampling.Horizon == nil || !s.Sampling.Horizon.Equal(time.Date(2023, 12, 9, 21, 45, 36, 0, time.UTC)) || s.Sampling.Blocks != 100 {
		t.Errorf("sampling: %+v", s.Sampling)
	}
	var old bool
	for _, ev := range evidence {
		if keys, _ := ev.Summary["signer_keys"].([]string); ev.Kind == "tx" && ev.NodeID == "ix:"+tAddr+":approve" && len(keys) == 1 && keys[0] == tOther {
			old = true
		}
	}
	if !old {
		t.Error("the explorer's old transaction is not on the map")
	}
	replays := map[string]map[string]any{}
	for _, ev := range evidence {
		if ev.Kind == "replay" {
			replays[ev.NodeID] = ev.Summary
		}
	}
	tr, ap := replays["ix:"+tAddr+":transfer"], replays["ix:"+tAddr+":approve"]
	if tr == nil || tr["outcome"] != "ok" || tr["units"] != float64(0x5208) {
		t.Errorf("transfer replay: %+v", tr)
	}
	if ap == nil || ap["outcome"] != "reverted" {
		t.Fatalf("approve replay: %+v", ap)
	}
	if e, _ := ap["error"].(map[string]any); e["name"] != "Error" || e["msg"] != "nope" {
		t.Errorf("approve revert: %+v", ap["error"])
	}
}

type memSink struct {
	mu     sync.Mutex
	facts  map[string]map[string]string
	rows   []core.SurfaceEvidence
	spans  []core.CoverageSpan
	cursor map[string]core.Cursor
	states []core.WatchState
	nodes  []core.SurfaceNode
	edges  []core.SurfaceEdge
}

func (m *memSink) Evidence(_ context.Context, rows []core.SurfaceEvidence) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rows = append(m.rows, rows...)
	return nil
}
func (m *memSink) Tally(context.Context, string, time.Time, int, int, int64) error { return nil }
func (m *memSink) Coverage(_ context.Context, span core.CoverageSpan) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if span.ID == "" {
		span.ID = "s" + string(rune('a'+len(m.spans)))
		m.spans = append(m.spans, span)
		return span.ID, nil
	}
	for i := range m.spans {
		if m.spans[i].ID == span.ID {
			m.spans[i].ToSlot = span.ToSlot
		}
	}
	return span.ID, nil
}
func (m *memSink) Cursor(_ context.Context, subject string, c core.Cursor) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cursor == nil {
		m.cursor = map[string]core.Cursor{}
	}
	m.cursor[subject] = c
	return nil
}
func (m *memSink) Accounts(context.Context, []core.WatchedAccount) error { return nil }
func (m *memSink) Facts(_ context.Context, node string, facts map[string]string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.facts == nil {
		m.facts = map[string]map[string]string{}
	}
	if m.facts[node] == nil {
		m.facts[node] = map[string]string{}
	}
	for k, v := range facts {
		m.facts[node][k] = v
	}
	return nil
}
func (m *memSink) Extend(_ context.Context, nodes []core.SurfaceNode, edges []core.SurfaceEdge) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nodes = append(m.nodes, nodes...)
	m.edges = append(m.edges, edges...)
	return nil
}
func (m *memSink) State(_ context.Context, s core.WatchState, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.states = append(m.states, s)
	return nil
}
func (m *memSink) wait(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		m.mu.Lock()
		ok := cond()
		m.mu.Unlock()
		if ok {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestWatchSessionFollowsHeadsAndLogs(t *testing.T) {
	srv := fakeChain(t)
	defer srv.Close()
	ws := chaintest.NewFakeWS()
	defer ws.Close()
	orig := Dial
	Dial = func(ctx context.Context, _ []string) (*chain.WSConn, error) {
		return chain.DialWS(ctx, []string{ws.URL()})
	}
	defer func() { Dial = orig }()
	sockets = &wsConn{conns: map[string]*chain.WSConn{}}
	a := wired(t, srv)
	doc, _, err := a.BuildSurface(context.Background(), core.SurfaceInput{Subjects: []string{tAddr}, Chains: map[string]string{tAddr: "1"}, Budget: core.SurfaceBudget{Signatures: 25, Transactions: 10, Profiles: 4, Pace: time.Millisecond, Deadline: time.Minute}})
	if err != nil {
		t.Fatal(err)
	}
	sink := &memSink{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- a.Watch(ctx, core.WatchInput{Subjects: []string{tAddr}, Chains: map[string]string{tAddr: "1"}, Surface: &doc, Cursors: map[string]core.Cursor{tAddr: {Slot: 99}}, Budget: core.WatchBudget{Backfill: 1000, Details: 30, Pace: time.Millisecond, Accounts: 32, Rows: 600}}, sink)
	}()
	sink.wait(t, "live", func() bool {
		for _, s := range sink.states {
			if s == core.WatchLive {
				return true
			}
		}
		return false
	})
	// The backfill read block 100 from the cursor at 99.
	sink.wait(t, "backfill span", func() bool {
		for _, sp := range sink.spans {
			if sp.Kind == core.CoverageBackfill && sp.FromSlot == 100 && sp.ToSlot == 100 {
				return true
			}
		}
		return false
	})
	heads, ok := ws.Find("eth_subscribe", "newHeads")
	if !ok {
		t.Fatalf("no newHeads subscription: %+v", ws.Subscriptions())
	}
	// The logs filter is the second parameter: the contract's own logs carry
	// its address, the holdings filters carry topics.
	var logs, holds chaintest.Subscription
	var haveLogs, haveHolds bool
	for _, s := range ws.Subscriptions() {
		if s.Method != "eth_subscribe" || s.ID == heads.ID || len(s.Params) < 2 {
			continue
		}
		if strings.Contains(string(s.Params[1]), `"address"`) {
			logs, haveLogs = s, true
		} else if strings.Contains(string(s.Params[1]), `"topics"`) && !haveHolds {
			holds, haveHolds = s, true
		}
	}
	if !haveLogs || !haveHolds {
		t.Fatalf("logs or holdings subscription missing: %+v", ws.Subscriptions())
	}
	ws.Notify("eth_subscription", heads.ID, map[string]any{"number": "0x65", "hash": "0xb101", "timestamp": "0x6600000c"})
	sink.wait(t, "the new block's call", func() bool {
		for _, r := range sink.rows {
			if r.Ref == "0xlive" && r.NodeID == "ix:"+tAddr+":approve" && r.Summary["failed"] == true && r.Summary["live"] == true {
				return true
			}
		}
		return false
	})
	topic := evm.Keccak([]byte("Transfer(address,address,uint256)"))
	ws.Notify("eth_subscription", logs.ID, map[string]any{"address": tAddr, "topics": []string{"0x" + hex.EncodeToString(topic[:]), "0x" + word(tRouter[2:]), "0x" + word(tUser[2:])}, "data": "0x" + word("2a"), "transactionHash": "0xindirect", "blockNumber": "0x63"})
	sink.wait(t, "the event and its caller", func() bool {
		var write, via bool
		for _, r := range sink.rows {
			if r.Kind == "write" && strings.HasPrefix(r.Ref, "0xindirect#") {
				write = true
			}
			if r.Kind == "tx" && r.Ref == "0xindirect" && r.Summary["via"] == tRouter {
				via = true
			}
		}
		return write && via
	})
	// A Transfer of TKN into the contract: the holdings fact is rewritten
	// from a fresh balance read and the move is a write on the contract.
	ws.Notify("eth_subscription", holds.ID, map[string]any{"address": tOther, "topics": []string{"0x" + hex.EncodeToString(topic[:]), "0x" + word(tUser[2:]), "0x" + word(tAddr[2:])}, "data": "0x" + word("5"), "transactionHash": "0xdeposit", "blockNumber": "0x66"})
	sink.wait(t, "holdings fact", func() bool {
		f := sink.facts["p:"+tAddr]
		if f == nil || !strings.HasPrefix(f["holdings"], "TKN ") {
			return false
		}
		for _, r := range sink.rows {
			if r.Kind == "write" && r.Summary["kind"] == "holding" && r.Summary["symbol"] == "TKN" {
				return true
			}
		}
		return false
	})
	sink.wait(t, "cursor and live span", func() bool {
		if sink.cursor[tAddr].Slot != 101 {
			return false
		}
		for _, sp := range sink.spans {
			if sp.Kind == core.CoverageLive && sp.FromSlot == 101 {
				return true
			}
		}
		return false
	})
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("session did not end")
	}
}

func TestChainForIgnoresAddressCase(t *testing.T) {
	chains := map[string]string{"0xBe3D0000000000000000000000000000AbCd8BBa": "11155111"}
	if c := chainFor(chains, "0xbe3d0000000000000000000000000000abcd8bba"); c.ID != "11155111" {
		t.Errorf("lowercase lookup got chain %s", c.ID)
	}
	if c := chainFor(chains, "0x0000000000000000000000000000000000000001"); c.ID != Chains[0].ID {
		t.Errorf("unknown subject got chain %s", c.ID)
	}
}

// The scanner's reach becomes declared wires: one per outside contract a
// function's body reaches, on the function's node.
func TestStaticReachDrawsDeclaredEdges(t *testing.T) {
	root := t.TempDir()
	src := `pragma solidity ^0.8.20;
interface ISignatureTransfer { function permitTransferFrom(uint256 a) external; }
contract Board {
    ISignatureTransfer private constant _PERMIT2 = ISignatureTransfer(0x000000000022D473030F116dDEE9F6B43aC78BA3);
    function fill(uint256 id) external { _pay(id); }
    function fillPermit(uint256 a) external { _PERMIT2.permitTransferFrom(a); }
    function _pay(uint256 id) private { IERC20(address(uint160(id))).transferFrom(msg.sender, address(this), 1); }
}
`
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "Board.sol"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	model, err := solsrc.Load(root, "")
	if err != nil {
		t.Fatal(err)
	}
	c, _ := model.ByName("Board")
	in := &iface{abi: model.ExposedInterface(c), source: "static", model: model, name: "Board"}
	nodes, edges := nodesOf(tAddr, Chains[0], Facts{}, in)
	byID := map[string]core.SurfaceNode{}
	for _, n := range nodes {
		byID[n.ID] = n
	}
	if n, ok := byID["p:0x000000000022d473030f116ddee9f6b43ac78ba3"]; !ok || n.Name != "Permit2" || !n.External {
		t.Errorf("permit2 node: %+v", n)
	}
	if n, ok := byID["p:static:erc_20_token"]; !ok || n.Name != "ERC-20 token" {
		t.Errorf("token node: %+v", n)
	}
	want := map[string]bool{"ix:" + tAddr + ":fill>p:static:erc_20_token": false, "ix:" + tAddr + ":fillPermit>p:0x000000000022d473030f116ddee9f6b43ac78ba3": false}
	for _, e := range edges {
		if _, ok := want[e.From+">"+e.To]; ok && e.Source == "static" && e.Kind == "cpi" {
			want[e.From+">"+e.To] = true
		}
	}
	for k, ok := range want {
		if !ok {
			t.Errorf("declared edge missing: %s (have %+v)", k, edges)
		}
	}
}

// Overloads are labelled by the source's own argument types; without
// source, tuples fold; ids stay on the ABI signature.
func TestOverloadLabels(t *testing.T) {
	abi := &evm.ABI{Functions: map[[4]byte]evm.Function{}}
	var a, b, c, d [4]byte
	a[0], b[0], c[0], d[0] = 1, 2, 3, 4
	abi.Functions[a] = evm.Function{Name: "createOrder", Sig: "createOrder((address,uint128,address,uint128,bool))"}
	abi.Functions[b] = evm.Function{Name: "createOrder", Sig: "createOrder((address,uint128,address,uint128,bool),(uint256,uint256,uint256,bytes))"}
	abi.Functions[c] = evm.Function{Name: "createOrder", Sig: "createOrder((address,uint128,address,uint128,bool),(uint256,uint256,uint8,bytes32,bytes32))"}
	abi.Functions[d] = evm.Function{Name: "cancelOrder", Sig: "cancelOrder(uint256)"}
	in := &iface{abi: abi}
	src := map[[4]byte]solsrc.Function{
		a: {Name: "createOrder", Params: []solsrc.Param{{Raw: "Order", Type: "tuple"}}},
		b: {Name: "createOrder", Params: []solsrc.Param{{Raw: "Order", Type: "tuple"}, {Raw: "Permit2Data", Type: "tuple"}}},
	}
	got := in.labels(src)
	want := map[[4]byte]string{a: "createOrder(Order)", b: "createOrder(Order, Permit2Data)", c: "createOrder((…),(…))", d: "cancelOrder"}
	for sel, w := range want {
		if got[sel] != w {
			t.Errorf("%x: got %q want %q", sel, got[sel], w)
		}
	}
	// Two source-less overloads that fold alike fall back to the signature.
	delete(src, b)
	got = in.labels(src)
	if got[b] != abi.Functions[b].Sig || got[c] != abi.Functions[c].Sig {
		t.Errorf("collision: %q %q", got[b], got[c])
	}
	if ids := in.nodeNames(); ids[b] != abi.Functions[b].Sig {
		t.Errorf("ids moved: %q", ids[b])
	}
}

// An unverified deployment borrows names and errors from a verified
// sibling of the same repository, and says which one named the call.
func TestBorrowFromSiblingDeployment(t *testing.T) {
	srv := fakeChain(t)
	defer srv.Close()
	a := wired(t, srv)
	in := &iface{abi: &evm.ABI{Functions: map[[4]byte]evm.Function{}, Events: map[[32]byte]evm.Event{}, Errors: map[[4]byte]evm.Error{}}, source: "traffic"}
	a.borrow(context.Background(), in, tOther, []core.Sibling{{ID: tOther, Chain: "1"}, {ID: "So1anaProgram", Chain: "mainnet"}, {ID: tAddr, Chain: "1"}, {ID: tAddr, Chain: "999"}})
	var sel [4]byte
	h := evm.Keccak([]byte("transfer(address,uint256)"))
	copy(sel[:], h[:4])
	b, ok := in.borrowed[sel]
	if !ok || b.fn.Name != "transfer" || len(b.fn.Inputs) != 2 || b.fn.Inputs[0].Name != "to" || b.from != shortAddr(tAddr)+" on Ethereum" {
		t.Fatalf("borrowed: %+v ok=%v", b, ok)
	}
	acc := newRowAcc(tOther, in, a.Lookup, false)
	input := append([]byte{}, sel[:]...)
	input = append(input, make([]byte, 64)...)
	copy(input[4+12:4+32], []byte(strings.Repeat("\x11", 20)))
	input[4+63] = 7
	acc.add(context.Background(), evm.Tx{Hash: "0xb", From: tUser, To: tOther, Input: input}, 100, time.Unix(0x66000000, 0).UTC(), &evm.Receipt{Status: true})
	var node *core.SurfaceNode
	for i := range acc.nodes {
		if acc.nodes[i].Kind == "instruction" {
			node = &acc.nodes[i]
		}
	}
	if node == nil || node.Name != "transfer" || node.Facts["named_via"] != shortAddr(tAddr)+" on Ethereum" || node.Facts["args"] != "to:address, amount:uint256" {
		t.Fatalf("node: %+v", node)
	}
	if len(acc.evidence) != 1 || acc.evidence[0].Summary["args"].(map[string]string)["amount"] != "7" {
		t.Fatalf("row args: %+v", acc.evidence)
	}
}
