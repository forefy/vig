package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/forefy/vig/backend/internal/core"
)

// openAPI is the full contract, as `vig openapi` writes it.
func openAPI(t *testing.T) map[string]any {
	t.Helper()
	var buf bytes.Buffer
	if err := WriteOpenAPI(&buf, nil); err != nil {
		t.Fatal(err)
	}
	var spec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &spec); err != nil {
		t.Fatal(err)
	}
	return spec
}

// The served spec is scoped: an anonymous or user caller never sees the
// admin or operator surface, an operator token sees its own, an admin all.
func TestServedSpecIsScoped(t *testing.T) {
	rr := httptest.NewRecorder()
	testRouter().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/openapi.json", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d", rr.Code)
	}
	var pub map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &pub); err != nil {
		t.Fatal(err)
	}
	paths := pub["paths"].(map[string]any)
	for p := range paths {
		if strings.HasPrefix(p, "/v1/admin") || strings.HasPrefix(p, "/v1/op") {
			t.Errorf("public spec exposes %s", p)
		}
	}
	if _, ok := paths["/v1/audits"]; !ok {
		t.Error("public spec lost /v1/audits")
	}
	schemas := pub["components"].(map[string]any)["schemas"].(map[string]any)
	for _, name := range []string{"AdminAudit", "AdminUser", "OperatorToken"} {
		if _, ok := schemas[name]; ok {
			t.Errorf("public spec exposes schema %s", name)
		}
	}
	if _, ok := schemas["Audit"]; !ok {
		t.Error("public spec lost schema Audit")
	}
	ss := pub["components"].(map[string]any)["securitySchemes"].(map[string]any)
	if _, ok := ss["admin"]; ok {
		t.Error("public spec exposes the admin scheme")
	}

	var buf bytes.Buffer
	if err := WriteOpenAPI(&buf, nil); err != nil {
		t.Fatal(err)
	}
	for level, want := range map[specLevel]map[string]bool{
		specOperator: {"/v1/op/jobs/claim": true, "/v1/admin/queue": false},
		specAdmin:    {"/v1/op/jobs/claim": true, "/v1/admin/queue": true, "/v1/admin/impersonate": true},
	} {
		b, err := filterSpec(buf.Bytes(), level)
		if err != nil {
			t.Fatal(err)
		}
		var doc map[string]any
		_ = json.Unmarshal(b, &doc)
		got := doc["paths"].(map[string]any)
		for p, present := range want {
			if _, ok := got[p]; ok != present {
				t.Errorf("level %d: %s present=%v want %v", level, p, ok, present)
			}
		}
	}
}

// Every route the frontend and the operator CLI rely on is in the contract.
func TestRoutesInContract(t *testing.T) {
	paths := openAPI(t)["paths"].(map[string]any)
	want := map[string][]string{
		"/v1/me":                             {"get", "patch", "delete"},
		"/v1/me/sessions":                    {"get", "delete"},
		"/v1/me/notifications/prefs":         {"patch"},
		"/v1/me/totp/enable":                 {"post"},
		"/v1/me/totp/verify":                 {"post"},
		"/v1/me/totp/recovery":               {"post"},
		"/v1/github/webhook":                 {"post"},
		"/v1/github/setup":                   {"get"},
		"/v1/admin/audits/{id}":              {"get", "delete"},
		"/v1/admin/audits/{id}/state":        {"post"},
		"/v1/admin/audits/{id}/delay":        {"post"},
		"/v1/admin/audits":                   {"get"},
		"/v1/admin/audits/{id}/fail":         {"post"},
		"/v1/admin/audits/{id}/cancel":       {"post"},
		"/v1/admin/audits/{id}/hide":         {"post"},
		"/v1/admin/audits/{id}/report":       {"post"},
		"/v1/admin/audits/{id}/report.json":  {"get"},
		"/v1/audits/{id}/report.pdf":         {"get"},
		"/v1/reports/{slug}/report.pdf":      {"get"},
		"/v1/reports/{slug}/report.json":     {"get"},
		"/v1/admin/users/{id}":               {"get"},
		"/v1/targets/{id}/tree":              {"get"},
		"/v1/scans/{token}":                  {"get"},
		"/v1/op/jobs/{id}/events":            {"post"},
		"/v1/admin/credits/grant":            {"post"},
		"/v1/admin/impersonate":              {"post"},
		"/v1/audits/{id}/surface":            {"get"},
		"/v1/op/jobs/{id}/surface":           {"get"},
		"/v1/audits/{id}/surface/live":       {"get"},
		"/v1/admin/audits/{id}/surface/live": {"get"},
		"/v1/op/jobs/{id}/surface/live":      {"get"},
		"/v1/op/jobs/{id}/surface/evidence":  {"post"},
		"/v1/op/jobs/{id}/surface/nodes":     {"post"},
		"/v1/me/impersonation/end":           {"post"},
	}
	for p, methods := range want {
		item, ok := paths[p].(map[string]any)
		if !ok {
			t.Errorf("path %s missing", p)
			continue
		}
		for _, m := range methods {
			if _, ok := item[m]; !ok {
				t.Errorf("%s %s missing", strings.ToUpper(m), p)
			}
		}
	}
}

// The operator cannot post "claimed": claiming happens through the claim
// route only.
func TestOperatorEventEnumHasNoClaimed(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteOpenAPI(&buf, nil); err != nil {
		t.Fatal(err)
	}
	spec := buf.String()
	// The operator's enum began with claimed once; the admin's audit filter
	// legitimately lists every state, so the check is for an enum that
	// starts at claimed, not for the word.
	if strings.Contains(spec, `"enum":["claimed"`) {
		t.Fatal("operator event enum still lists claimed")
	}
	if !strings.Contains(spec, `"cloning","programs_verified","analysis","human_review","delivered"`) {
		t.Fatal("operator event enum missing")
	}
}

// Scan results are reachable by link only, never by search.
func TestScanResultCarriesNoindex(t *testing.T) {
	out := scanResult(Scan{Token: "x"})
	if out.Robots != "noindex" {
		t.Fatalf("X-Robots-Tag %q", out.Robots)
	}
}

func TestOwnershipMessageFreshness(t *testing.T) {
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	fresh := "vig ownership proof abc PROG " + now.Add(-5*time.Minute).Format(time.RFC3339)
	stale := "vig ownership proof abc PROG " + now.Add(-11*time.Minute).Format(time.RFC3339)
	future := "vig ownership proof abc PROG " + now.Add(5*time.Minute).Format(time.RFC3339)
	if !ownershipMessageFresh(fresh, now) {
		t.Error("fresh message rejected")
	}
	if ownershipMessageFresh(stale, now) {
		t.Error("stale message accepted")
	}
	if ownershipMessageFresh(future, now) {
		t.Error("future-dated message accepted")
	}
	if ownershipMessageFresh("garbage", now) {
		t.Error("garbage accepted")
	}
}

func TestBackgroundIsBounded(t *testing.T) {
	d := &Deps{}
	release := make(chan struct{})
	started := 0
	for i := 0; i < BackgroundSlots; i++ {
		if !d.background(func(ctx context.Context) { <-release }) {
			t.Fatalf("slot %d refused", i)
		}
		started++
	}
	if d.background(func(ctx context.Context) {}) {
		t.Fatal("accepted work beyond the slot count")
	}
	close(release)
	deadline := time.Now().Add(2 * time.Second)
	for !d.background(func(ctx context.Context) {}) {
		if time.Now().After(deadline) {
			t.Fatal("slots never freed")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// llms.txt describes a role only to a caller who holds it.
func TestLLMSTxtIsScoped(t *testing.T) {
	rr := httptest.NewRecorder()
	testRouter().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/llms.txt", nil))
	body := rr.Body.String()
	if !strings.Contains(body, "Bearer vig_") {
		t.Error("user credential missing")
	}
	for _, leak := range []string{"vop_", "vig-op", "Admin:", "TOTP"} {
		if strings.Contains(body, leak) {
			t.Errorf("anonymous llms.txt mentions %q", leak)
		}
	}
}

func TestCarryPostedIDsPrefersTheCurrentID(t *testing.T) {
	findings := []core.Finding{
		{Ordinal: 1, ID: "cur-1", CarriedFromID: "prev-1"},
		{Ordinal: 2, ID: "cur-2"},
		{Ordinal: 3, CarriedFromID: "prev-3"},
	}
	carryPostedIDs(findings)
	want := []string{"cur-1", "cur-2", "prev-3"}
	for i, f := range findings {
		if f.ID != "" || f.CarriedFromID != want[i] {
			t.Fatalf("finding %d: id=%q carried=%q, want carried=%q", f.Ordinal, f.ID, f.CarriedFromID, want[i])
		}
	}
}
