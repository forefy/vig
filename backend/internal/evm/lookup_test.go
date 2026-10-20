package evm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A selector the 4byte directory does not list is named by openchain; one
// neither knows stays nil, and both answers are remembered.
func TestNameFallsThroughToOpenchain(t *testing.T) {
	sel := Keccak([]byte("OrderNotActive(uint256)"))
	var want [4]byte
	copy(want[:], sel[:4])
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		switch {
		case strings.HasPrefix(r.URL.Path, "/4byte"):
			w.Write([]byte(`{"results":[]}`))
		case strings.HasPrefix(r.URL.Path, "/openchain"):
			if strings.Contains(r.URL.RawQuery, "d2c02610") {
				w.Write([]byte(`{"ok":true,"result":{"function":{"0xd2c02610":[{"name":"OrderNotActive(uint256)","filtered":false}]},"event":{}}}`))
				return
			}
			w.Write([]byte(`{"ok":true,"result":{"function":{},"event":{}}}`))
		}
	}))
	defer srv.Close()
	l := NewLookup()
	l.HTTP = srv.Client()
	l.FourByte = srv.URL + "/4byte/?hex_signature=0x"
	l.Openchain = srv.URL + "/openchain?function=0x"
	f := l.Name(context.Background(), want)
	if f == nil || f.Name != "OrderNotActive" || f.Selector != want {
		t.Fatalf("openchain name: %+v", f)
	}
	if l.Name(context.Background(), want) == nil || calls != 2 {
		t.Fatalf("not remembered: calls=%d", calls)
	}
	if l.Name(context.Background(), [4]byte{1, 2, 3, 4}) != nil {
		t.Fatal("unknown selector named")
	}
}
