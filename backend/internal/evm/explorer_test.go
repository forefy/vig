package evm

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A pruned endpoint answers null for an old receipt; that must read as
// "not held", never as a reverted transaction.
func TestReceiptNullIsNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":null}`))
	}))
	defer srv.Close()
	c := &Client{HTTP: srv.Client(), URLs: []string{srv.URL}}
	_, err := c.TransactionReceipt(context.Background(), "0xabc")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

// The explorer gives status, gas and logs for what the endpoint dropped.
func TestExplorerReceipt(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") == "" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/logs"):
			w.Write([]byte(`{"items":[{"address":{"hash":"0x8bfeB11546145f730bdE82088A1f6890fB9fD3dc"},"topics":["0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef","0x000000000000000000000000239e292161f5e6acaafc92c94af847461d4b55ad",null],"data":"0x01"}]}`))
		case strings.Contains(r.URL.Path, "/transactions/0xok"):
			w.Write([]byte(`{"status":"ok","result":"success","gas_used":"187376"}`))
		case strings.Contains(r.URL.Path, "/transactions/0xbad"):
			w.Write([]byte(`{"status":"error","result":"Reverted","gas_used":"30000"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte(`{"message":"Not found"}`))
		}
	}))
	defer srv.Close()
	e := &Explorer{HTTP: srv.Client(), Base: srv.URL}
	rc, err := e.Receipt(context.Background(), "0xok")
	if err != nil || !rc.Status || rc.GasUsed != 187376 {
		t.Fatalf("ok receipt: %+v %v", rc, err)
	}
	if len(rc.Logs) != 1 || rc.Logs[0].Address != "0x8bfeb11546145f730bde82088a1f6890fb9fd3dc" || len(rc.Logs[0].Topics) != 2 {
		t.Fatalf("logs: %+v", rc.Logs)
	}
	if rc, err := e.Receipt(context.Background(), "0xbad"); err != nil || rc.Status {
		t.Fatalf("reverted receipt: %+v %v", rc, err)
	}
	if _, err := e.Receipt(context.Background(), "0xnone"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
}

// A verified contract that is no token is named by the explorer.
func TestExplorerName(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/0xcoins") {
			w.Write([]byte(`{"hash":"0xcoins","name":"Coins","is_verified":true}`))
			return
		}
		w.Write([]byte(`{"hash":"0xnone","name":null}`))
	}))
	defer srv.Close()
	e := &Explorer{HTTP: srv.Client(), Base: srv.URL}
	if got := e.Name(context.Background(), "0xcoins"); got != "Coins" {
		t.Fatalf("name: %q", got)
	}
	if got := e.Name(context.Background(), "0xnone"); got != "" {
		t.Fatalf("unnamed: %q", got)
	}
}
