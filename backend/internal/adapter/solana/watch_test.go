package solana

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/forefy/vig/backend/internal/chain"
	"github.com/forefy/vig/backend/internal/chain/chaintest"
	"github.com/forefy/vig/backend/internal/core"
)

const (
	wPID   = "GauwsjaNa7fmSRwCg9APFhZMkgxSz31q5HfNuLtRR522"
	wVault = "VauLt11111111111111111111111111111111111111"
	wPData = "PDaTa11111111111111111111111111111111111111"
	wToken = "TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA"
)

// memSink records everything a session reports.
type memSink struct {
	mu       sync.Mutex
	rows     []core.SurfaceEvidence
	spans    map[string]core.CoverageSpan
	spanN    int
	cursors  map[string]core.Cursor
	accounts []core.WatchedAccount
	facts    map[string]map[string]string
	states   []core.WatchState
	nodes    []core.SurfaceNode
	edges    []core.SurfaceEdge
	tallies  map[string]int
}

func newMemSink() *memSink {
	return &memSink{spans: map[string]core.CoverageSpan{}, cursors: map[string]core.Cursor{}, facts: map[string]map[string]string{}}
}

func (m *memSink) Evidence(_ context.Context, rows []core.SurfaceEvidence) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rows = append(m.rows, rows...)
	return nil
}

func (m *memSink) Tally(_ context.Context, nodeID string, _ time.Time, calls, failed int, units int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.tallies == nil {
		m.tallies = map[string]int{}
	}
	m.tallies[nodeID] += calls
	return nil
}

func (m *memSink) Coverage(_ context.Context, span core.CoverageSpan) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if span.ID == "" {
		m.spanN++
		span.ID = "span" + string(rune('0'+m.spanN))
		m.spans[span.ID] = span
		return span.ID, nil
	}
	have := m.spans[span.ID]
	have.ToSlot, have.ToAt = span.ToSlot, span.ToAt
	m.spans[span.ID] = have
	return span.ID, nil
}

func (m *memSink) Cursor(_ context.Context, subject string, c core.Cursor) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cursors[subject] = c
	return nil
}

func (m *memSink) Accounts(_ context.Context, accounts []core.WatchedAccount) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.accounts = accounts
	return nil
}

func (m *memSink) Facts(_ context.Context, nodeID string, facts map[string]string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.facts[nodeID] = facts
	return nil
}

func (m *memSink) Extend(_ context.Context, nodes []core.SurfaceNode, edges []core.SurfaceEdge) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nodes = append(m.nodes, nodes...)
	m.edges = append(m.edges, edges...)
	return nil
}

func (m *memSink) State(_ context.Context, state core.WatchState, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.states = append(m.states, state)
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

// fakeRPC answers the HTTP methods a session reads: nothing exists on
// chain except a two-signature history and the transactions it names.
func fakeRPC(t *testing.T) *httptest.Server {
	t.Helper()
	tx := func(sig string, slot uint64) any {
		return map[string]any{"slot": slot, "blockTime": 1_700_000_000 + int64(slot),
			"meta": map[string]any{"err": nil, "computeUnitsConsumed": 5000, "logMessages": []string{}, "innerInstructions": []any{}},
			"transaction": map[string]any{"signatures": []string{sig}, "message": map[string]any{
				"accountKeys":  []any{map[string]any{"pubkey": "UserA", "signer": true, "writable": true}, map[string]any{"pubkey": wVault, "signer": false, "writable": true}, map[string]any{"pubkey": wPID, "signer": false, "writable": false}},
				"instructions": []any{map[string]any{"programId": wPID, "accounts": []string{wVault, "UserA"}, "data": chain.Encode([]byte{1, 2, 3})}}}}}
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     int               `json:"id"`
			Method string            `json:"method"`
			Params []json.RawMessage `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		var result any
		switch req.Method {
		case "getSignaturesForAddress":
			var opts map[string]any
			if len(req.Params) > 1 {
				_ = json.Unmarshal(req.Params[1], &opts)
			}
			if opts["until"] == "s12" {
				result = []any{map[string]any{"signature": "s14", "slot": 14, "err": nil, "blockTime": 1_700_000_014}, map[string]any{"signature": "s13", "slot": 13, "err": nil, "blockTime": 1_700_000_013}}
			} else {
				result = []any{}
			}
		case "getTransaction":
			var sig string
			_ = json.Unmarshal(req.Params[0], &sig)
			slot := uint64(20)
			if strings.HasPrefix(sig, "s1") {
				slot = uint64(sig[2]-'0') + 10
			}
			result = tx(sig, slot)
		default:
			result = map[string]any{"context": map[string]any{"slot": 1}, "value": nil}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
	}))
}

func watchedSurface() (*core.Surface, []core.SurfaceEvidence) {
	s := &core.Surface{Adapter: Key, Nodes: []core.SurfaceNode{
		{ID: "p:" + wPID, Kind: "program", Name: "prog", Source: "idl", Facts: map[string]string{"program": wPID}},
		{ID: "ix:" + wPID + ":deposit", Parent: "p:" + wPID, Kind: "instruction", Name: "deposit", Source: "idl", Facts: map[string]string{"program": wPID}},
		{ID: "acct:" + wPID + ":deposit:vault", Parent: "ix:" + wPID + ":deposit", Kind: "account", Name: "vault", Source: "idl", Facts: map[string]string{"writable": "true"}},
		{ID: "acct:" + wPID + ":deposit:user", Parent: "ix:" + wPID + ":deposit", Kind: "account", Name: "user", Source: "idl", Facts: map[string]string{"signer": "true", "writable": "true"}},
	}}
	var sample []core.SurfaceEvidence
	for i, slot := range []uint64{10, 11, 12} {
		at := time.Unix(1_700_000_000+int64(slot), 0).UTC()
		sample = append(sample, core.SurfaceEvidence{NodeID: "ix:" + wPID + ":deposit", Kind: "tx", Ref: "s1" + string(rune('0'+i)), At: &at,
			Summary: map[string]any{"slot": float64(slot), "keys": []any{wVault, "User" + string(rune('A'+i))}}})
	}
	return s, sample
}

func TestWatchSessionBackfillsStreamsAndWrites(t *testing.T) {
	rpc := fakeRPC(t)
	defer rpc.Close()
	ws := chaintest.NewFakeWS()
	defer ws.Close()
	doc, sample := watchedSurface()
	w := &Watcher{SurfaceBuilder: &SurfaceBuilder{Chain: chain.New([]string{rpc.URL})}, URLs: []string{ws.URL()}}
	sink := newMemSink()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	in := core.WatchInput{Subjects: []string{wPID}, Surface: doc, Sample: sample,
		Accounts: []core.WatchedAccount{{Address: wVault, NodeID: "acct:" + wPID + ":deposit:vault", Role: "state"}, {Address: wPData, NodeID: "p:" + wPID, Role: "program"}},
		Priority: []string{"ix:" + wPID + ":deposit"}, Budget: core.WatchBudget{Backfill: 1000, Details: 30, Pace: time.Millisecond, Accounts: 32}}
	done := make(chan error, 1)
	go func() { done <- w.Watch(ctx, in, sink) }()

	sink.wait(t, "live state", func() bool {
		for _, s := range sink.states {
			if s == core.WatchLive {
				return true
			}
		}
		return false
	})
	// The backfill read both signatures after the sample's newest and
	// covered their range.
	sink.wait(t, "backfill rows", func() bool { return len(sink.rows) >= 2 })
	sink.mu.Lock()
	var backfill, gap int
	for _, sp := range sink.spans {
		switch sp.Kind {
		case core.CoverageBackfill:
			backfill++
			if sp.FromSlot != 13 || sp.ToSlot != 14 {
				t.Errorf("backfill span %+v", sp)
			}
		case core.CoverageGap:
			gap++
		}
	}
	refs := map[string]bool{}
	for _, r := range sink.rows {
		refs[r.Ref] = true
		if r.Summary["live"] != true || r.Summary["source"] != "tx" {
			t.Errorf("backfill row summary %+v", r.Summary)
		}
	}
	sink.mu.Unlock()
	if backfill != 1 || gap != 0 || !refs["s13"] || !refs["s14"] {
		t.Fatalf("backfill=%d gap=%d refs=%v", backfill, gap, refs)
	}

	// A logs notification lands on the instruction the Anchor log names,
	// with its callee, and the priority node earns a full read that
	// supersedes the log-derived row.
	logsSub, ok := ws.Find("logsSubscribe", wPID)
	if !ok {
		t.Fatalf("no logs subscription: %+v", ws.Subscriptions())
	}
	ws.Logs(logsSub.ID, 20, "s20", nil, []string{
		"Program " + wPID + " invoke [1]",
		"Program log: Instruction: Deposit",
		"Program " + wToken + " invoke [2]",
		"Program " + wToken + " success",
		"Program " + wPID + " consumed 41212 of 200000 compute units",
		"Program " + wPID + " success",
	})
	sink.wait(t, "logs row and its full read", func() bool {
		n := 0
		for _, r := range sink.rows {
			if r.Ref == "s20" {
				n++
			}
		}
		return n >= 2
	})
	sink.mu.Lock()
	var logsRow, txRow *core.SurfaceEvidence
	for i := range sink.rows {
		r := &sink.rows[i]
		if r.Ref != "s20" {
			continue
		}
		if r.Summary["source"] == "logs" {
			logsRow = r
		} else {
			txRow = r
		}
	}
	sink.mu.Unlock()
	if logsRow == nil || logsRow.NodeID != "ix:"+wPID+":deposit" || logsRow.Summary["units"] != uint64(41212) {
		t.Fatalf("logs row: %+v", logsRow)
	}
	if cpi, _ := logsRow.Summary["cpi"].([]string); len(cpi) != 1 || cpi[0] != wToken {
		t.Errorf("cpi: %v", logsRow.Summary["cpi"])
	}
	if txRow == nil || txRow.Summary["source"] != "tx" {
		t.Errorf("full read: %+v", txRow)
	}

	// A write to the vault is a row on its account node; a write to the
	// program-data account rewrites the program's facts.
	vaultSub, _ := ws.Find("accountSubscribe", wVault)
	ws.Account(vaultSub.ID, 21, wPID, 1000, base64.StdEncoding.EncodeToString([]byte{9, 9, 9}))
	pdata := make([]byte, 45+8)
	binary.LittleEndian.PutUint32(pdata[:4], 3)
	binary.LittleEndian.PutUint64(pdata[4:12], 22)
	pdata[12] = 1
	copy(pdata[13:45], []byte("authority-key-authority-key-auth"))
	copy(pdata[45:], []byte{7, 7, 7, 7, 0, 0, 0, 0})
	pdSub, _ := ws.Find("accountSubscribe", wPData)
	ws.Account(pdSub.ID, 22, "BPFLoaderUpgradeab1e11111111111111111111111", 5, base64.StdEncoding.EncodeToString(pdata))
	sink.wait(t, "write rows", func() bool {
		n := 0
		for _, r := range sink.rows {
			if r.Kind == "write" {
				n++
			}
		}
		return n >= 2 && sink.facts["p:"+wPID] != nil
	})
	sink.mu.Lock()
	facts := sink.facts["p:"+wPID]
	var vaultWrite, upgrade *core.SurfaceEvidence
	for i := range sink.rows {
		r := &sink.rows[i]
		if r.Kind != "write" {
			continue
		}
		if r.NodeID == "p:"+wPID {
			upgrade = r
		} else {
			vaultWrite = r
		}
	}
	sink.mu.Unlock()
	if facts["bytecode_now"] != chain.HashBytecode(pdata[45:]) || facts["last_deploy_slot"] != "22" || facts["authority"] == "" || facts["immutable"] != "false" {
		t.Errorf("program facts: %+v", facts)
	}
	if upgrade == nil || upgrade.Ref != "upgrade@22" || upgrade.Summary["kind"] != "program_data" {
		t.Errorf("upgrade row: %+v", upgrade)
	}
	if vaultWrite == nil || vaultWrite.NodeID != "acct:"+wPID+":deposit:vault" || vaultWrite.Summary["size"] != 3 || vaultWrite.Ref != wVault+"@21" {
		t.Errorf("vault write: %+v", vaultWrite)
	}

	// The cursor and the live span follow the stream once flushed.
	sink.wait(t, "cursor and span flush", func() bool {
		if sink.cursors[wPID].Ref != "s20" {
			return false
		}
		for _, sp := range sink.spans {
			if sp.Kind == core.CoverageLive && sp.FromSlot == 20 && sp.ToSlot == 20 {
				return true
			}
		}
		return false
	})
	sink.wait(t, "edge from the logs", func() bool {
		for _, e := range sink.edges {
			if e.From == "ix:"+wPID+":deposit" && e.To == "p:"+wToken && e.Count == 1 {
				return true
			}
		}
		return false
	})

	// Dropping the socket ends the session with an error the supervisor
	// retries on.
	ws.Drop()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("session ended without an error after a drop")
		}
	case <-time.After(8 * time.Second):
		t.Fatal("session did not end after the drop")
	}
}

func TestWatchUnresolvedCallLandsOnTheProgram(t *testing.T) {
	rpc := fakeRPC(t)
	defer rpc.Close()
	ws := chaintest.NewFakeWS()
	defer ws.Close()
	doc, _ := watchedSurface()
	w := &Watcher{SurfaceBuilder: &SurfaceBuilder{Chain: chain.New([]string{rpc.URL})}, URLs: []string{ws.URL()}}
	sink := newMemSink()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// No sample and no cursor: the session starts at the head, and a
	// zero detail budget means nothing is read in full.
	in := core.WatchInput{Subjects: []string{wPID}, Surface: doc, Accounts: []core.WatchedAccount{{Address: wVault, NodeID: "acct:" + wPID + ":deposit:vault", Role: "state"}},
		Budget: core.WatchBudget{Backfill: 1000, Details: 1, Pace: time.Millisecond, Accounts: 32}}
	go func() { _ = w.Watch(ctx, in, sink) }()
	sink.wait(t, "live", func() bool { return len(sink.states) > 0 && sink.states[len(sink.states)-1] == core.WatchLive })
	sub, _ := ws.Find("logsSubscribe", wPID)
	// A native-style call logs no instruction name: the first is queued
	// for a read, the rest of the burst count on the program itself.
	for i := 0; i < 70; i++ {
		ws.Logs(sub.ID, 30+uint64(i), "n"+string(rune('a'+i%26))+string(rune('a'+i/26)), nil, []string{"Program " + wPID + " invoke [1]", "Program " + wPID + " success"})
	}
	sink.wait(t, "unresolved rows", func() bool {
		for _, r := range sink.rows {
			if r.NodeID == "p:"+wPID && r.Summary["unresolved"] == true {
				return true
			}
		}
		return false
	})
}

func TestPickAccountsChoosesSharedWritableState(t *testing.T) {
	rpc := fakeRPC(t)
	defer rpc.Close()
	doc, sample := watchedSurface()
	w := &Watcher{SurfaceBuilder: &SurfaceBuilder{Chain: chain.New([]string{rpc.URL})}}
	got := w.pickAccounts(context.Background(), core.WatchInput{Subjects: []string{wPID}, Surface: doc, Sample: sample}, 32)
	var vault, idl, user bool
	for _, a := range got {
		switch {
		case a.Address == wVault && a.NodeID == "acct:"+wPID+":deposit:vault" && a.Role == "state":
			vault = true
		case a.Role == "idl" && a.NodeID == "p:"+wPID:
			idl = true
		case strings.HasPrefix(a.Address, "User"):
			user = true
		}
	}
	if !vault || !idl || user {
		t.Errorf("picked %+v", got)
	}
}

func TestWatchRowBudgetCountsPastIt(t *testing.T) {
	rpc := fakeRPC(t)
	defer rpc.Close()
	ws := chaintest.NewFakeWS()
	defer ws.Close()
	doc, _ := watchedSurface()
	w := &Watcher{SurfaceBuilder: &SurfaceBuilder{Chain: chain.New([]string{rpc.URL})}, URLs: []string{ws.URL()}}
	sink := newMemSink()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	in := core.WatchInput{Subjects: []string{wPID}, Surface: doc, Accounts: []core.WatchedAccount{{Address: wVault, NodeID: "acct:" + wPID + ":deposit:vault", Role: "state"}},
		Budget: core.WatchBudget{Backfill: 1000, Details: 1, Pace: time.Millisecond, Accounts: 32, Rows: 60}}
	go func() { _ = w.Watch(ctx, in, sink) }()
	sink.wait(t, "live", func() bool { return len(sink.states) > 0 && sink.states[len(sink.states)-1] == core.WatchLive })
	sub, _ := ws.Find("logsSubscribe", wPID)
	for i := 0; i < 40; i++ {
		ws.Logs(sub.ID, 100+uint64(i), "c"+string(rune('a'+i%26))+string(rune('a'+i/26)), nil, []string{"Program " + wPID + " invoke [1]", "Program log: Instruction: Deposit", "Program " + wPID + " success"})
	}
	node := "ix:" + wPID + ":deposit"
	sink.wait(t, "tallies past the budget", func() bool { return sink.tallies[node] > 0 && sink.facts["p:"+wPID]["live_rows"] == "sampled" })
	sink.mu.Lock()
	defer sink.mu.Unlock()
	stored := 0
	for _, r := range sink.rows {
		if r.NodeID == node && r.Summary["source"] == "logs" {
			stored++
		}
	}
	// Sixty a minute is five per five-second window; a burst may straddle
	// one boundary.
	if stored < 5 || stored > 10 {
		t.Errorf("stored %d log rows, budget 5 per window", stored)
	}
	if stored+sink.tallies[node] < 40 {
		t.Errorf("rows %d + tallies %d < 40 calls", stored, sink.tallies[node])
	}
}
