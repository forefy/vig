package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/forefy/vig/backend/internal/adapter/solana"
	"github.com/forefy/vig/backend/internal/core"
)

// testRouter wires the router without a store: enough for the public
// catalogue routes, the spec, and validation, which all run before any query.
func testRouter() http.Handler {
	return NewRouter(&Deps{Adapters: map[string]core.Adapter{solana.Key: solana.New()}})
}

func TestPlansIsReal(t *testing.T) {
	rr := httptest.NewRecorder()
	testRouter().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/v1/plans", nil))
	if rr.Code != 200 {
		t.Fatalf("status %d: %s", rr.Code, rr.Body)
	}
	var out struct{ Items []core.Plan }
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Items) != 4 || out.Items[2].Key != "pro" {
		t.Fatalf("plans: %+v", out.Items)
	}
}

func TestAdaptersExposeSteps(t *testing.T) {
	rr := httptest.NewRecorder()
	testRouter().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/v1/adapters/solana", nil))
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), `"key":"programs"`) {
		t.Fatalf("status %d: %s", rr.Code, rr.Body)
	}
}

// Routes that need a dependency the process was not configured with answer
// 503, as problem+json like every other error.
func TestUnconfiguredDependencyIsProblemJSON(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/scans", strings.NewReader(`{"repo_url":"https://github.com/a/b"}`))
	req.Header.Set("Content-Type", "application/json")
	testRouter().ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("want 503, got %d: %s", rr.Code, rr.Body)
	}
	if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/problem+json") {
		t.Fatalf("want problem+json, got %s", ct)
	}
}

func TestOpenAPIAndDocs(t *testing.T) {
	for _, p := range []string{"/openapi.json", "/docs", "/healthz"} {
		rr := httptest.NewRecorder()
		testRouter().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, p, nil))
		if rr.Code != 200 {
			t.Fatalf("%s: %d", p, rr.Code)
		}
	}
}

func TestFindingsSchemaInSpec(t *testing.T) {
	rr := httptest.NewRecorder()
	testRouter().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/openapi.json", nil))
	var spec struct {
		Components struct {
			Schemas map[string]any `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &spec); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"FindingsDocument", "Finding", "VerificationPanel", "Plan", "Audit"} {
		if _, ok := spec.Components.Schemas[name]; !ok {
			t.Errorf("schema %s missing from spec", name)
		}
	}
}
