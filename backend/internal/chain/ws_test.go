package chain

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/forefy/vig/backend/internal/chain/chaintest"
)

func TestWSURL(t *testing.T) {
	for in, want := range map[string]string{"https://api.mainnet-beta.solana.com": "wss://api.mainnet-beta.solana.com", "http://127.0.0.1:8899": "ws://127.0.0.1:8899", "wss://x": "wss://x", "ftp://x": "", "": ""} {
		if got := WSURL(in); got != want {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
}

func TestSubscribeDeliversNotificationsAndDoneOnClose(t *testing.T) {
	fake := chaintest.NewFakeWS()
	defer fake.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, err := DialWS(ctx, []string{"https://127.0.0.1:1", fake.URL()})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	sub, err := conn.LogsFor(ctx, pid)
	if err != nil || sub.ID != "1" {
		t.Fatalf("subscribe: %+v err=%v", sub, err)
	}
	if s, ok := fake.Find("logsSubscribe", pid); !ok || s.ID != 1 {
		t.Fatalf("server saw %+v", fake.Subscriptions())
	}
	// A second listener for the same thing shares the stream: no second
	// request reaches the wire, and both hear the notification.
	twin, err := conn.LogsFor(ctx, pid)
	if err != nil || twin.ID != sub.ID || len(fake.Subscriptions()) != 1 {
		t.Fatalf("twin: %+v err=%v subs=%d", twin, err, len(fake.Subscriptions()))
	}
	ch := sub.C
	wire, _ := strconv.Atoi(sub.ID)
	fake.Logs(wire, 100, "sig1", nil, []string{"Program " + pid + " invoke [1]", "Program " + pid + " success"})
	select {
	case n := <-twin.C:
		if n.Subscription != sub.ID {
			t.Fatalf("twin got %+v", n)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("twin heard nothing")
	}
	select {
	case n := <-ch:
		ln, err := ParseLogsNotification(n.Result)
		if err != nil || ln.Slot != 100 || ln.Signature != "sig1" || len(ln.Logs) != 2 {
			t.Fatalf("notification: %+v %v", ln, err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no notification")
	}
	// Closing one listener keeps the stream; closing the last one ends it
	// on the wire.
	twin.Close()
	if _, ok := <-twin.C; ok {
		t.Error("closed twin channel still open")
	}
	if n := len(fake.Subscriptions()); n != 1 {
		t.Errorf("unsubscribe sent with a listener left: %d requests", n)
	}
	sub.Close()
	deadline := time.Now().Add(3 * time.Second)
	for len(fake.Subscriptions()) < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if subs := fake.Subscriptions(); len(subs) != 2 || subs[1].Method != "logsUnsubscribe" {
		t.Errorf("last close did not unsubscribe: %+v", subs)
	}
	again, err := conn.LogsFor(ctx, pid)
	if err != nil || again.ID == sub.ID {
		t.Fatalf("resubscribe after close: %+v err=%v", again, err)
	}
	ch = again.C
	fake.Drop()
	select {
	case <-conn.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("not done after drop")
	}
	if conn.Err() == nil {
		t.Error("no error recorded")
	}
	if _, ok := <-ch; ok {
		t.Error("subscription channel still open")
	}
}

func TestSubscribeRejected(t *testing.T) {
	fake := chaintest.NewFakeWS()
	defer fake.Close()
	fake.Reject = true
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, err := DialWS(ctx, []string{fake.URL()})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.AccountFor(ctx, pid); err == nil {
		t.Fatal("expected an error")
	}
}

func TestParseAccountNotification(t *testing.T) {
	raw := []byte(`{"context":{"slot":7},"value":{"owner":"o","lamports":5,"executable":false,"data":["AQID","base64"]}}`)
	an, err := ParseAccountNotification(raw)
	if err != nil || an.Slot != 7 || !an.Account.Exists || an.Account.Owner != "o" || an.Account.Lamports != 5 || len(an.Account.Data) != 3 {
		t.Fatalf("%+v %v", an, err)
	}
	an, err = ParseAccountNotification([]byte(`{"context":{"slot":8},"value":null}`))
	if err != nil || an.Account.Exists {
		t.Fatalf("closed account: %+v %v", an, err)
	}
}
