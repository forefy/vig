package chain

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// A throttled endpoint moves behind the others for a while but is never
// dropped: the second endpoint answers, and a lone throttled endpoint is
// still asked.
func TestThrottledEndpointRestsBehindOthers(t *testing.T) {
	hits := map[string]int{}
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits["first"]++
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer first.Close()
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits["second"]++
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","result":7,"id":1}`))
	}))
	defer second.Close()
	c := New([]string{first.URL, second.URL})
	for i := 0; i < 3; i++ {
		if _, err := c.GetSlot(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if hits["first"] != 1 || hits["second"] != 3 {
		t.Fatalf("first=%d second=%d; the throttled endpoint should rest after one 429", hits["first"], hits["second"])
	}
	if got := c.order(); got[0] != second.URL {
		t.Fatalf("order %v", got)
	}
}
