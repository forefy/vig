package chain

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCallFallsBackWhenPrimaryIsDown(t *testing.T) {
	good := newFakeRPC()
	goodSrv := httptest.NewServer(good)
	defer goodSrv.Close()
	dead := httptest.NewServer(http.NotFoundHandler())
	deadURL := dead.URL
	dead.Close() // connection refused from now on

	c := New([]string{deadURL, goodSrv.URL})
	slot, err := c.GetSlot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if slot != good.slot {
		t.Fatalf("slot %d want %d", slot, good.slot)
	}
	if good.calls.Load() != 1 {
		t.Fatalf("fallback must be hit exactly once, got %d", good.calls.Load())
	}
}

func TestCallFallsBackOnHTTPError(t *testing.T) {
	broken := newFakeRPC()
	broken.status = http.StatusServiceUnavailable
	brokenSrv := httptest.NewServer(broken)
	defer brokenSrv.Close()
	good := newFakeRPC()
	goodSrv := httptest.NewServer(good)
	defer goodSrv.Close()

	c := New([]string{brokenSrv.URL, goodSrv.URL})
	if _, err := c.GetSlot(context.Background()); err != nil {
		t.Fatal(err)
	}
	if broken.calls.Load() != 1 || good.calls.Load() != 1 {
		t.Fatalf("calls broken=%d good=%d", broken.calls.Load(), good.calls.Load())
	}
}

func TestCallFallsBackOnGarbageBody(t *testing.T) {
	garbage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("<html>")) }))
	defer garbage.Close()
	good := newFakeRPC()
	goodSrv := httptest.NewServer(good)
	defer goodSrv.Close()
	if _, err := New([]string{garbage.URL, goodSrv.URL}).GetSlot(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestCallReturnsLastErrorWhenAllFail(t *testing.T) {
	a := newFakeRPC()
	a.status = http.StatusBadGateway
	aSrv := httptest.NewServer(a)
	defer aSrv.Close()
	b := newFakeRPC()
	b.status = http.StatusTooManyRequests
	bSrv := httptest.NewServer(b)
	defer bSrv.Close()
	_, err := New([]string{aSrv.URL, bSrv.URL}).GetSlot(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "429") {
		t.Fatalf("last endpoint's error should surface, got %v", err)
	}
}

func TestCallRPCErrorDoesNotFallBack(t *testing.T) {
	a := newFakeRPC()
	a.rpcError = "Invalid param: could not find account"
	aSrv := httptest.NewServer(a)
	defer aSrv.Close()
	b := newFakeRPC()
	bSrv := httptest.NewServer(b)
	defer bSrv.Close()
	_, err := New([]string{aSrv.URL, bSrv.URL}).GetSlot(context.Background())
	if err == nil || !strings.Contains(err.Error(), "rpc getSlot: Invalid param") {
		t.Fatalf("got %v", err)
	}
	if b.calls.Load() != 0 {
		t.Fatal("a well-formed rpc error is final; no fallback")
	}
}

func TestCallNoEndpoints(t *testing.T) {
	_, err := New(nil).GetSlot(context.Background())
	if err == nil || !strings.Contains(err.Error(), "no rpc endpoints") {
		t.Fatalf("got %v", err)
	}
}

func TestCallHonoursContext(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Drain the body so the server watches the connection; then hold.
		_, _ = io.Copy(io.Discard, r.Body)
		select {
		case <-r.Context().Done():
		case <-time.After(3 * time.Second):
		}
	}))
	defer slow.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := New([]string{slow.URL}).GetSlot(ctx); err == nil {
		t.Fatal("expected a timeout error")
	}
}

func TestCachedHonoursTTL(t *testing.T) {
	f := newFakeRPC()
	srv := httptest.NewServer(f)
	defer srv.Close()
	c := New([]string{srv.URL})
	c.TTL = time.Hour
	ctx := context.Background()

	a, err := c.Cached(ctx, "k1", "getSlot")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := c.Cached(ctx, "k1", "getSlot")
	if string(a) != string(b) || f.calls.Load() != 1 {
		t.Fatalf("second call within TTL must be served from cache (calls=%d)", f.calls.Load())
	}
	// A different key is a different entry even for the same method.
	if _, err := c.Cached(ctx, "k2", "getSlot"); err != nil || f.calls.Load() != 2 {
		t.Fatalf("distinct keys must not share entries (calls=%d, err=%v)", f.calls.Load(), err)
	}
	// The TTL is fixed when an entry is written: a short one expires.
	c.TTL = time.Nanosecond
	c.Cached(ctx, "k3", "getSlot")
	time.Sleep(time.Millisecond)
	if _, err := c.Cached(ctx, "k3", "getSlot"); err != nil || f.calls.Load() != 4 {
		t.Fatalf("expired entry must refetch (calls=%d, err=%v)", f.calls.Load(), err)
	}
	// TTL zero disables caching entirely.
	c.TTL = 0
	c.Cached(ctx, "k4", "getSlot")
	c.Cached(ctx, "k4", "getSlot")
	if f.calls.Load() != 6 {
		t.Fatalf("zero TTL must not cache (calls=%d)", f.calls.Load())
	}
}

func TestCachedDoesNotStoreErrors(t *testing.T) {
	f := newFakeRPC()
	f.status = http.StatusInternalServerError
	srv := httptest.NewServer(f)
	defer srv.Close()
	c := New([]string{srv.URL})
	ctx := context.Background()
	if _, err := c.Cached(ctx, "k", "getSlot"); err == nil {
		t.Fatal("expected failure")
	}
	f.status = 0
	raw, err := c.Cached(ctx, "k", "getSlot")
	if err != nil {
		t.Fatal(err)
	}
	var slot uint64
	if json.Unmarshal(raw, &slot) != nil || slot != f.slot {
		t.Fatalf("got %s", raw)
	}
	if f.calls.Load() != 2 {
		t.Fatalf("a failed call must not poison the cache (calls=%d)", f.calls.Load())
	}
}

func TestCachedEvictsStaleEntriesWhenLarge(t *testing.T) {
	f := newFakeRPC()
	srv := httptest.NewServer(f)
	defer srv.Close()
	c := New([]string{srv.URL})
	c.TTL = time.Hour
	ctx := context.Background()
	// Pre-load stale entries directly.
	c.mu.Lock()
	for i := 0; i < 2001; i++ {
		c.cache["stale"+string(rune('a'+i%26))+string(rune(i))] = cacheEntry{at: time.Now().Add(-2 * time.Hour), data: json.RawMessage("1")}
	}
	n := len(c.cache)
	c.mu.Unlock()
	if n <= 2000 {
		t.Fatalf("setup: want >2000 entries, got %d", n)
	}
	if _, err := c.Cached(ctx, "fresh", "getSlot"); err != nil {
		t.Fatal(err)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.cache) != 1 {
		t.Fatalf("stale entries must be evicted, %d remain", len(c.cache))
	}
	if _, ok := c.cache["fresh"]; !ok {
		t.Fatal("the fresh entry must survive eviction")
	}
}

func TestGetAccountDecodesAndCaches(t *testing.T) {
	f := newFakeRPC()
	srv := httptest.NewServer(f)
	defer srv.Close()
	id := testKey("acct")
	f.account(id, testKey("owner"), false, []byte{1, 2, 3})
	c := New([]string{srv.URL})
	a, err := c.GetAccount(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if !a.Exists || a.Owner != testKey("owner") || a.Lamports != 1_000_000 || string(a.Data) != "\x01\x02\x03" || a.Slot != f.slot {
		t.Fatalf("account %+v", a)
	}
	c.GetAccount(context.Background(), id)
	if f.calls.Load() != 1 {
		t.Fatal("GetAccount must be cached by pubkey")
	}
	missing, err := c.GetAccount(context.Background(), testKey("none"))
	if err != nil || missing.Exists || missing.Slot != f.slot {
		t.Fatalf("missing account: %+v %v", missing, err)
	}
}

func TestGetAccountBadBase64(t *testing.T) {
	f := newFakeRPC()
	srv := httptest.NewServer(f)
	defer srv.Close()
	id := testKey("bad64")
	f.accounts[id] = map[string]any{"owner": testKey("o"), "lamports": 1, "executable": false, "data": []string{"!!!not base64!!!", "base64"}}
	if _, err := New([]string{srv.URL}).GetAccount(context.Background(), id); err == nil {
		t.Fatal("expected a decode error")
	}
}

func TestGetBlockTime(t *testing.T) {
	f := newFakeRPC()
	srv := httptest.NewServer(f)
	defer srv.Close()
	f.blockTimes[10] = 1_600_000_000
	c := New([]string{srv.URL})
	ts, err := c.GetBlockTime(context.Background(), 10)
	if err != nil || !ts.Equal(time.Unix(1_600_000_000, 0)) || ts.Location() != time.UTC {
		t.Fatalf("ts=%v err=%v", ts, err)
	}
	unknown, err := c.GetBlockTime(context.Background(), 11)
	if err != nil || !unknown.IsZero() {
		t.Fatalf("unknown slot: %v %v", unknown, err)
	}
	c.GetBlockTime(context.Background(), 10)
	if f.seen("getBlockTime") != 2 {
		t.Fatalf("block times are cached per slot, saw %d calls", f.seen("getBlockTime"))
	}
}
