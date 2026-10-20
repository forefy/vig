package chain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// Subscriptions on the public RPC's websocket cost nothing per
// notification, which is what makes a complete watch affordable. One
// connection multiplexes every subscription, and two subscribers asking
// for the same thing share one subscription on the wire, so ten audits of
// one program cost the endpoint one stream. The caller owns reconnection,
// because a reconnect is where coverage has to be reasoned about.

// WSURL derives the websocket endpoint of an HTTP RPC endpoint.
func WSURL(httpURL string) string {
	u, err := url.Parse(httpURL)
	if err != nil {
		return ""
	}
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	case "http":
		u.Scheme = "ws"
	case "ws", "wss":
	default:
		return ""
	}
	return u.String()
}

// Notification is one subscription message.
type Notification struct {
	Subscription string
	Result       json.RawMessage
}

// WSConn is one open websocket with its subscriptions.
type WSConn struct {
	URL  string
	conn *websocket.Conn

	mu      sync.Mutex
	nextID  int
	pending map[int]chan wsReply
	streams map[string]*stream // by subscription id
	byKey   map[string]*stream // by method and params, for sharing
	done    chan struct{}
	err     error
}

// stream is one subscription on the wire and everyone listening to it.
type stream struct {
	id     string
	key    string
	method string
	subs   []*Subscription
}

// Subscription is one listener on a stream.
type Subscription struct {
	ID string
	C  <-chan Notification

	conn    *WSConn
	ch      chan Notification
	st      *stream
	dropped int
	closed  bool
}

type wsReply struct {
	Result json.RawMessage
	Err    *RPCError
}

type wsMessage struct {
	ID     *int            `json:"id"`
	Method string          `json:"method"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
	Params *struct {
		Subscription json.RawMessage `json:"subscription"`
		Result       json.RawMessage `json:"result"`
	} `json:"params"`
}

// subID renders a subscription id the way both chains send it: Solana an
// integer, Ethereum a hex string.
func subID(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return strings.TrimSpace(string(raw))
}

// readLimit fits an account notification carrying a whole program image.
const readLimit = 32 << 20

// subscriberBuffer is how many notifications a listener may fall behind
// before the oldest is dropped and counted.
const subscriberBuffer = 4096

// DialWS opens the first endpoint that answers, in order. Endpoints are
// HTTP URLs; their websocket form is derived.
func DialWS(ctx context.Context, urls []string) (*WSConn, error) {
	var last error
	for _, u := range urls {
		ws := WSURL(u)
		if ws == "" {
			continue
		}
		dctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		c, _, err := websocket.Dial(dctx, ws, nil)
		cancel()
		if err != nil {
			last = err
			continue
		}
		c.SetReadLimit(readLimit)
		w := &WSConn{URL: ws, conn: c, pending: map[int]chan wsReply{}, streams: map[string]*stream{}, byKey: map[string]*stream{}, done: make(chan struct{})}
		go w.read(ctx)
		return w, nil
	}
	if last == nil {
		last = errors.New("no websocket endpoints configured")
	}
	return nil, last
}

// Done closes when the connection is gone; Err then says why.
func (w *WSConn) Done() <-chan struct{} { return w.done }

// Err is the reason the connection ended, once Done is closed.
func (w *WSConn) Err() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.err
}

// Alive reports whether the connection is still open.
func (w *WSConn) Alive() bool {
	select {
	case <-w.done:
		return false
	default:
		return true
	}
}

// Close ends the connection.
func (w *WSConn) Close() {
	_ = w.conn.Close(websocket.StatusNormalClosure, "")
}

func (w *WSConn) read(ctx context.Context) {
	var err error
	defer func() {
		w.mu.Lock()
		w.err = err
		for id, ch := range w.pending {
			close(ch)
			delete(w.pending, id)
		}
		for id, st := range w.streams {
			for _, s := range st.subs {
				if !s.closed {
					s.closed = true
					close(s.ch)
				}
			}
			delete(w.streams, id)
		}
		w.byKey = map[string]*stream{}
		w.mu.Unlock()
		close(w.done)
	}()
	for {
		_, data, rerr := w.conn.Read(ctx)
		if rerr != nil {
			err = rerr
			return
		}
		var m wsMessage
		if json.Unmarshal(data, &m) != nil {
			continue
		}
		switch {
		case m.ID != nil:
			w.mu.Lock()
			ch := w.pending[*m.ID]
			delete(w.pending, *m.ID)
			w.mu.Unlock()
			if ch == nil {
				continue
			}
			r := wsReply{Result: m.Result}
			if m.Error != nil {
				r.Err = &RPCError{Method: "ws", Code: m.Error.Code, Message: m.Error.Message}
			}
			ch <- r
		case m.Params != nil:
			id := subID(m.Params.Subscription)
			n := Notification{Subscription: id, Result: m.Params.Result}
			w.mu.Lock()
			st := w.streams[id]
			if st == nil {
				w.mu.Unlock()
				continue
			}
			for _, s := range st.subs {
				// A slow listener drops its oldest notification rather than
				// stalling the socket for everyone; the drop is counted so
				// its session can end and let the backfill say what was missed.
				select {
				case s.ch <- n:
				default:
					s.dropped++
					select {
					case <-s.ch:
					default:
					}
					select {
					case s.ch <- n:
					default:
					}
				}
			}
			w.mu.Unlock()
		}
	}
}

// call sends one request and waits for its reply.
func (w *WSConn) call(ctx context.Context, method string, params ...any) (json.RawMessage, error) {
	w.mu.Lock()
	w.nextID++
	id := w.nextID
	ch := make(chan wsReply, 1)
	w.pending[id] = ch
	w.mu.Unlock()
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	wctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := w.conn.Write(wctx, websocket.MessageText, body); err != nil {
		w.mu.Lock()
		delete(w.pending, id)
		w.mu.Unlock()
		return nil, err
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-w.done:
		return nil, fmt.Errorf("websocket closed: %v", w.Err())
	case r, ok := <-ch:
		if !ok {
			return nil, fmt.Errorf("websocket closed: %v", w.Err())
		}
		if r.Err != nil {
			return nil, r.Err
		}
		return r.Result, nil
	}
}

// Subscribe joins the stream for a method and its parameters, opening it
// on the wire when nobody listens to it yet. The channel closes when the
// connection ends or the subscription is closed.
func (w *WSConn) Subscribe(ctx context.Context, method string, params ...any) (*Subscription, error) {
	pb, _ := json.Marshal(params)
	key := method + " " + string(pb)
	w.mu.Lock()
	st := w.byKey[key]
	w.mu.Unlock()
	if st == nil {
		raw, err := w.call(ctx, method, params...)
		if err != nil {
			return nil, err
		}
		id := subID(raw)
		if id == "" || id == "null" {
			return nil, fmt.Errorf("%s: bad subscription id %s", method, string(raw))
		}
		w.mu.Lock()
		// Two callers may have raced to open the same stream: keep the
		// first and let the second one's wire subscription idle.
		if have := w.byKey[key]; have != nil {
			st = have
		} else {
			st = &stream{id: id, key: key, method: method}
			w.streams[id] = st
			w.byKey[key] = st
		}
		w.mu.Unlock()
	}
	ch := make(chan Notification, subscriberBuffer)
	s := &Subscription{ID: st.id, C: ch, conn: w, ch: ch, st: st}
	w.mu.Lock()
	if _, open := w.streams[st.id]; !open {
		w.mu.Unlock()
		return nil, fmt.Errorf("websocket closed: %v", w.Err())
	}
	st.subs = append(st.subs, s)
	w.mu.Unlock()
	return s, nil
}

// Dropped is how many notifications this listener did not take in time.
func (s *Subscription) Dropped() int {
	s.conn.mu.Lock()
	defer s.conn.mu.Unlock()
	return s.dropped
}

// Close stops listening. The last listener of a stream ends it on the
// wire too.
func (s *Subscription) Close() {
	s.conn.mu.Lock()
	if s.closed {
		s.conn.mu.Unlock()
		return
	}
	s.closed = true
	close(s.ch)
	st := s.st
	for i, x := range st.subs {
		if x == s {
			st.subs = append(st.subs[:i], st.subs[i+1:]...)
			break
		}
	}
	last := len(st.subs) == 0
	if last {
		delete(s.conn.streams, st.id)
		delete(s.conn.byKey, st.key)
	}
	s.conn.mu.Unlock()
	if last && s.conn.Alive() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = s.conn.call(ctx, unsubscribeMethod(st.method), st.id)
	}
}

// LogsFor subscribes to every confirmed transaction that mentions an
// address, with its logs. Confirmed rather than finalized: one public
// endpoint never delivers finalized subscriptions, and optimistic
// confirmation is the same guarantee wallets settle on.
func (w *WSConn) LogsFor(ctx context.Context, address string) (*Subscription, error) {
	return w.Subscribe(ctx, "logsSubscribe", map[string]any{"mentions": []string{address}}, map[string]any{"commitment": "confirmed"})
}

// AccountFor subscribes to every confirmed write of an account, with its
// new data.
func (w *WSConn) AccountFor(ctx context.Context, address string) (*Subscription, error) {
	return w.Subscribe(ctx, "accountSubscribe", address, map[string]any{"encoding": "base64", "commitment": "confirmed"})
}

// Ping keeps an idle connection open through proxies that drop quiet
// sockets.
func (w *WSConn) Ping(ctx context.Context) error {
	pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return w.conn.Ping(pctx)
}

// LogsNotification is the payload of a logsSubscribe message.
type LogsNotification struct {
	Slot      uint64
	Signature string
	Err       any
	Logs      []string
}

// ParseLogsNotification decodes a logsSubscribe result.
func ParseLogsNotification(raw json.RawMessage) (LogsNotification, error) {
	var r struct {
		Context struct {
			Slot uint64 `json:"slot"`
		} `json:"context"`
		Value struct {
			Signature string   `json:"signature"`
			Err       any      `json:"err"`
			Logs      []string `json:"logs"`
		} `json:"value"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return LogsNotification{}, err
	}
	return LogsNotification{Slot: r.Context.Slot, Signature: r.Value.Signature, Err: r.Value.Err, Logs: r.Value.Logs}, nil
}

// AccountNotification is the payload of an accountSubscribe message.
type AccountNotification struct {
	Slot    uint64
	Account Account
}

// ParseAccountNotification decodes an accountSubscribe result.
func ParseAccountNotification(raw json.RawMessage) (AccountNotification, error) {
	var r struct {
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
	if err := json.Unmarshal(raw, &r); err != nil {
		return AccountNotification{}, err
	}
	out := AccountNotification{Slot: r.Context.Slot, Account: Account{Slot: r.Context.Slot}}
	if r.Value == nil {
		return out, nil
	}
	out.Account.Exists = true
	out.Account.Owner = r.Value.Owner
	out.Account.Lamports = r.Value.Lamports
	out.Account.Executable = r.Value.Executable
	if len(r.Value.Data) > 0 && !strings.EqualFold(r.Value.Data[len(r.Value.Data)-1], "base58") {
		b, err := decodeBase64(r.Value.Data[0])
		if err != nil {
			return out, err
		}
		out.Account.Data = b
	}
	return out, nil
}

// SignaturesPage lists signatures touching an address newest first, one
// page, stopping at until when it is reached, at the commitment the
// subscriptions use so a cursor from the stream is always found. Never
// cached: the cursor walk needs every page fresh.
func (c *Client) SignaturesPage(ctx context.Context, address, before, until string, limit int) ([]Signature, error) {
	opts := map[string]any{"limit": limit, "commitment": "confirmed"}
	if before != "" {
		opts["before"] = before
	}
	if until != "" {
		opts["until"] = until
	}
	raw, err := c.Call(ctx, "getSignaturesForAddress", address, opts)
	if err != nil {
		return nil, err
	}
	var out []Signature
	return out, json.Unmarshal(raw, &out)
}

// unsubscribeMethod names the call that ends a stream: Solana pairs
// logsSubscribe with logsUnsubscribe, Ethereum eth_subscribe with
// eth_unsubscribe.
func unsubscribeMethod(method string) string {
	if method == "eth_subscribe" {
		return "eth_unsubscribe"
	}
	return strings.TrimSuffix(method, "Subscribe") + "Unsubscribe"
}
