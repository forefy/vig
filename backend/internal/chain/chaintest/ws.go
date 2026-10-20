// Package chaintest fakes the RPC's websocket for tests: it answers
// subscription requests with ids and lets a test push notifications.
package chaintest

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"

	"github.com/coder/websocket"
)

// Subscription is one request the fake accepted.
type Subscription struct {
	ID     int
	Method string
	Params []json.RawMessage
}

// FakeWS is a websocket server that speaks the subscription protocol.
type FakeWS struct {
	Server *httptest.Server

	mu    sync.Mutex
	conns []*websocket.Conn
	subs  []Subscription
	next  int
	// Reject makes every subscription request answer an error.
	Reject bool
}

// NewFakeWS starts the server. Close it with Close.
func NewFakeWS() *FakeWS {
	f := &FakeWS{}
	f.Server = httptest.NewServer(http.HandlerFunc(f.serve))
	return f
}

// URL is the HTTP form, which chain.DialWS derives the websocket from.
func (f *FakeWS) URL() string { return f.Server.URL }

// Close ends every connection and the server.
func (f *FakeWS) Close() {
	f.mu.Lock()
	conns := f.conns
	f.conns = nil
	f.mu.Unlock()
	for _, c := range conns {
		_ = c.Close(websocket.StatusGoingAway, "closing")
	}
	f.Server.Close()
}

// Drop closes every open connection, as a flaky endpoint would.
func (f *FakeWS) Drop() {
	f.mu.Lock()
	conns := f.conns
	f.conns = nil
	f.mu.Unlock()
	for _, c := range conns {
		_ = c.Close(websocket.StatusGoingAway, "dropped")
	}
}

// Subscriptions lists what was subscribed, in order.
func (f *FakeWS) Subscriptions() []Subscription {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Subscription(nil), f.subs...)
}

// Find returns the subscription of a method whose first parameter
// contains needle, or zero.
func (f *FakeWS) Find(method, needle string) (Subscription, bool) {
	for _, s := range f.Subscriptions() {
		if s.Method != method || len(s.Params) == 0 {
			continue
		}
		if needle == "" || containsString(s.Params[0], needle) {
			return s, true
		}
	}
	return Subscription{}, false
}

func containsString(raw json.RawMessage, needle string) bool {
	return len(raw) > 0 && (string(raw) == `"`+needle+`"` || jsonContains(raw, needle))
}

func jsonContains(raw json.RawMessage, needle string) bool {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s == needle
	}
	var m map[string]any
	if json.Unmarshal(raw, &m) == nil {
		for _, v := range m {
			switch x := v.(type) {
			case string:
				if x == needle {
					return true
				}
			case []any:
				for _, e := range x {
					if e == needle {
						return true
					}
				}
			}
		}
	}
	return false
}

// Notify pushes one notification to every connection.
func (f *FakeWS) Notify(method string, subscription int, result any) {
	f.NotifyID(method, subscription, result)
}

// NotifyID pushes one notification with any subscription id shape.
func (f *FakeWS) NotifyID(method string, subscription any, result any) {
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": method, "params": map[string]any{"subscription": subscription, "result": result}})
	f.mu.Lock()
	conns := append([]*websocket.Conn(nil), f.conns...)
	f.mu.Unlock()
	for _, c := range conns {
		_ = c.Write(context.Background(), websocket.MessageText, body)
	}
}

// Logs pushes a logsNotification for a subscription.
func (f *FakeWS) Logs(subscription int, slot uint64, signature string, err any, logs []string) {
	f.Notify("logsNotification", subscription, map[string]any{"context": map[string]any{"slot": slot}, "value": map[string]any{"signature": signature, "err": err, "logs": logs}})
}

// Account pushes an accountNotification for a subscription.
func (f *FakeWS) Account(subscription int, slot uint64, owner string, lamports uint64, dataBase64 string) {
	f.Notify("accountNotification", subscription, map[string]any{"context": map[string]any{"slot": slot}, "value": map[string]any{"owner": owner, "lamports": lamports, "executable": false, "data": []string{dataBase64, "base64"}}})
}

func (f *FakeWS) serve(w http.ResponseWriter, r *http.Request) {
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	f.mu.Lock()
	f.conns = append(f.conns, c)
	f.mu.Unlock()
	ctx := r.Context()
	for {
		_, data, err := c.Read(ctx)
		if err != nil {
			return
		}
		var req struct {
			ID     int               `json:"id"`
			Method string            `json:"method"`
			Params []json.RawMessage `json:"params"`
		}
		if json.Unmarshal(data, &req) != nil {
			continue
		}
		f.mu.Lock()
		reject := f.Reject
		f.next++
		id := f.next
		f.subs = append(f.subs, Subscription{ID: id, Method: req.Method, Params: req.Params})
		f.mu.Unlock()
		var reply []byte
		if reject {
			reply, _ = json.Marshal(map[string]any{"jsonrpc": "2.0", "id": req.ID, "error": map[string]any{"code": -32602, "message": "rejected"}})
		} else {
			reply, _ = json.Marshal(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": id})
		}
		_ = c.Write(ctx, websocket.MessageText, reply)
	}
}
