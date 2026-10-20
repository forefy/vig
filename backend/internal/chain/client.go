package chain

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Client is a JSON-RPC client with endpoint fallback and two caches: a short
// TTL cache for live account reads and a small LRU of program data bytes
// keyed by (programdata account, deploy slot), which never change once
// written.
type Client struct {
	URLs []string
	HTTP *http.Client
	// TTL bounds the live cache (account lookups).
	TTL time.Duration
	// BlockTimeTTL bounds cached block times; a slot's time is immutable so a
	// day is only a memory bound.
	BlockTimeTTL time.Duration
	// DataEntries bounds the program-data LRU.
	DataEntries int

	mu      sync.Mutex
	cache   map[string]cacheEntry
	data    *lru
	resting map[string]time.Time // endpoints that answered 429, until when
}

type cacheEntry struct {
	at   time.Time
	ttl  time.Duration
	data json.RawMessage
}

// New builds a client. The first URL is primary; later ones are fallbacks.
func New(urls []string) *Client {
	return &Client{URLs: urls, HTTP: &http.Client{Timeout: 20 * time.Second}, TTL: 60 * time.Second, BlockTimeTTL: 24 * time.Hour, DataEntries: 64,
		cache: map[string]cacheEntry{}, data: newLRU(64)}
}

// RPCError is a JSON-RPC error object returned by an endpoint.
type RPCError struct {
	Method  string
	Code    int
	Message string
}

func (e *RPCError) Error() string { return fmt.Sprintf("rpc %s: %s (%d)", e.Method, e.Message, e.Code) }

// NotFound reports whether the error is a well-formed "does not exist"
// answer rather than an endpoint problem: those are final and not retried
// on the fallback.
func (e *RPCError) NotFound() bool {
	switch e.Code {
	case -32004, -32007, -32009: // block not available, slot skipped, long-term storage slot missing
		return true
	}
	m := strings.ToLower(e.Message)
	return strings.Contains(m, "not found") || strings.Contains(m, "could not find") || strings.Contains(m, "was skipped") || strings.Contains(m, "does not exist")
}

type rpcResp struct {
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// Call performs one RPC method, trying each endpoint in order. Transport
// failures, non-200 answers and JSON-RPC error objects all fall through to
// the next endpoint, except a well-formed not-found which is returned as is.
func (c *Client) Call(ctx context.Context, method string, params ...any) (json.RawMessage, error) {
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	var last error
	for _, u := range c.order() {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := c.HTTP.Do(req)
		if err != nil {
			last = err
			continue
		}
		raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
		resp.Body.Close()
		if err != nil {
			last = err
			continue
		}
		if resp.StatusCode != http.StatusOK {
			last = fmt.Errorf("%s: %s", u, resp.Status)
			if resp.StatusCode == http.StatusTooManyRequests {
				c.rest(u)
			}
			continue
		}
		var r rpcResp
		if err := json.Unmarshal(raw, &r); err != nil {
			last = err
			continue
		}
		if r.Error != nil {
			rpcErr := &RPCError{Method: method, Code: r.Error.Code, Message: r.Error.Message}
			if rpcErr.NotFound() {
				return nil, rpcErr
			}
			last = rpcErr
			continue
		}
		return r.Result, nil
	}
	if last == nil {
		last = errors.New("no rpc endpoints configured")
	}
	return nil, last
}

// Cached runs Call through the live cache under key with the default TTL.
func (c *Client) Cached(ctx context.Context, key, method string, params ...any) (json.RawMessage, error) {
	return c.cachedFor(ctx, key, c.TTL, method, params...)
}

func (c *Client) cachedFor(ctx context.Context, key string, ttl time.Duration, method string, params ...any) (json.RawMessage, error) {
	c.mu.Lock()
	if e, ok := c.cache[key]; ok && time.Since(e.at) < e.ttl {
		c.mu.Unlock()
		return e.data, nil
	}
	c.mu.Unlock()
	data, err := c.Call(ctx, method, params...)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.cache[key] = cacheEntry{at: time.Now(), ttl: ttl, data: data}
	if len(c.cache) > 2000 {
		for k, e := range c.cache {
			if time.Since(e.at) >= e.ttl {
				delete(c.cache, k)
			}
		}
	}
	c.mu.Unlock()
	return data, nil
}

// Forget drops one live-cache entry, so a caller that recognised a bad
// answer (an empty history from a short-ledger fallback) can read again.
func (c *Client) Forget(key string) {
	c.mu.Lock()
	delete(c.cache, key)
	c.mu.Unlock()
}

// Account is a decoded getAccountInfo result.
type Account struct {
	Exists     bool
	Owner      string
	Lamports   uint64
	Executable bool
	Data       []byte
	Slot       uint64
}

type accountInfo struct {
	Context struct {
		Slot uint64 `json:"slot"`
	} `json:"context"`
	Value *struct {
		Owner      string   `json:"owner"`
		Lamports   uint64   `json:"lamports"`
		Executable bool     `json:"executable"`
		Data       []string `json:"data"`
	} `json:"value"`
}

func decodeAccount(raw json.RawMessage) (Account, error) {
	var r accountInfo
	if err := json.Unmarshal(raw, &r); err != nil {
		return Account{}, err
	}
	if r.Value == nil {
		return Account{Slot: r.Context.Slot}, nil
	}
	a := Account{Exists: true, Owner: r.Value.Owner, Lamports: r.Value.Lamports, Executable: r.Value.Executable, Slot: r.Context.Slot}
	if len(r.Value.Data) > 0 {
		var err error
		a.Data, err = decodeBase64(r.Value.Data[0])
		if err != nil {
			return Account{}, err
		}
	}
	return a, nil
}

// GetAccount fetches an account with base64 data through the live cache.
func (c *Client) GetAccount(ctx context.Context, pubkey string) (Account, error) {
	raw, err := c.Cached(ctx, "acct:"+pubkey, "getAccountInfo", pubkey, map[string]any{"encoding": "base64", "commitment": "confirmed"})
	if err != nil {
		return Account{}, err
	}
	return decodeAccount(raw)
}

// GetAccountSlice fetches only length bytes of an account's data from
// offset, through the live cache. Used to read a program data header
// without pulling the whole binary.
func (c *Client) GetAccountSlice(ctx context.Context, pubkey string, offset, length int) (Account, error) {
	raw, err := c.Cached(ctx, fmt.Sprintf("slice:%s:%d:%d", pubkey, offset, length), "getAccountInfo", pubkey,
		map[string]any{"encoding": "base64", "commitment": "confirmed", "dataSlice": map[string]int{"offset": offset, "length": length}})
	if err != nil {
		return Account{}, err
	}
	return decodeAccount(raw)
}

// GetAccountAtSlot fetches the full account through the immutable LRU keyed
// by (pubkey, slot). The caller has already established, from a header
// read, that the data was written at slot; the bytes cannot change without
// the slot changing too.
func (c *Client) GetAccountAtSlot(ctx context.Context, pubkey string, slot uint64) (Account, error) {
	key := fmt.Sprintf("%s@%d", pubkey, slot)
	c.mu.Lock()
	if a, ok := c.data.get(key); ok {
		c.mu.Unlock()
		return a, nil
	}
	c.mu.Unlock()
	raw, err := c.Call(ctx, "getAccountInfo", pubkey, map[string]any{"encoding": "base64", "commitment": "confirmed"})
	if err != nil {
		return Account{}, err
	}
	a, err := decodeAccount(raw)
	if err != nil {
		return Account{}, err
	}
	c.mu.Lock()
	c.data.put(key, a)
	c.mu.Unlock()
	return a, nil
}

// GetSlot returns the current confirmed slot.
func (c *Client) GetSlot(ctx context.Context) (uint64, error) {
	raw, err := c.Call(ctx, "getSlot", map[string]any{"commitment": "confirmed"})
	if err != nil {
		return 0, err
	}
	var slot uint64
	return slot, json.Unmarshal(raw, &slot)
}

// GetBlockTime returns the unix time of a slot, or zero when unknown. The
// answer is cached for BlockTimeTTL.
func (c *Client) GetBlockTime(ctx context.Context, slot uint64) (time.Time, error) {
	raw, err := c.cachedFor(ctx, fmt.Sprintf("bt:%d", slot), c.BlockTimeTTL, "getBlockTime", slot)
	if err != nil {
		var rpcErr *RPCError
		if errors.As(err, &rpcErr) && rpcErr.NotFound() {
			return time.Time{}, nil
		}
		return time.Time{}, err
	}
	var t *int64
	if err := json.Unmarshal(raw, &t); err != nil || t == nil {
		return time.Time{}, err
	}
	return time.Unix(*t, 0).UTC(), nil
}

// Signature is one entry of getSignaturesForAddress.
type Signature struct {
	Signature string `json:"signature"`
	Slot      uint64 `json:"slot"`
	Err       any    `json:"err"`
	BlockTime *int64 `json:"blockTime"`
}

// SignaturesFor lists the most recent transaction signatures touching an
// address, newest first, cached for 30 seconds.
func (c *Client) SignaturesFor(ctx context.Context, address string, limit int) ([]Signature, error) {
	raw, err := c.cachedFor(ctx, fmt.Sprintf("sigs:%s:%d", address, limit), 30*time.Second, "getSignaturesForAddress", address, map[string]any{"limit": limit, "commitment": "finalized"})
	if err != nil {
		return nil, err
	}
	var out []Signature
	return out, json.Unmarshal(raw, &out)
}

// lru is a tiny bounded map with insertion-order eviction, guarded by the
// client mutex.
type lru struct {
	max   int
	order []string
	items map[string]Account
}

func newLRU(max int) *lru { return &lru{max: max, items: map[string]Account{}} }

func (l *lru) get(k string) (Account, bool) {
	a, ok := l.items[k]
	if ok {
		l.touch(k)
	}
	return a, ok
}

func (l *lru) touch(k string) {
	for i, o := range l.order {
		if o == k {
			l.order = append(l.order[:i], l.order[i+1:]...)
			break
		}
	}
	l.order = append(l.order, k)
}

func (l *lru) put(k string, a Account) {
	if _, ok := l.items[k]; !ok && len(l.items) >= l.max && l.max > 0 {
		oldest := l.order[0]
		l.order = l.order[1:]
		delete(l.items, oldest)
	}
	l.items[k] = a
	l.touch(k)
}

// restFor is how long a throttled endpoint sits out before it is tried
// first again. Others are tried ahead of it meanwhile.
const restFor = 30 * time.Second

// rest records a 429 from an endpoint.
func (c *Client) rest(u string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.resting == nil {
		c.resting = map[string]time.Time{}
	}
	c.resting[u] = time.Now().Add(restFor)
}

// order returns the endpoints with resting ones moved to the back, so a
// throttled endpoint still serves when every other one fails.
func (c *Client) order() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.resting) == 0 {
		return c.URLs
	}
	now := time.Now()
	var live, tired []string
	for _, u := range c.URLs {
		if until, ok := c.resting[u]; ok && until.After(now) {
			tired = append(tired, u)
		} else {
			live = append(live, u)
		}
	}
	return append(live, tired...)
}
