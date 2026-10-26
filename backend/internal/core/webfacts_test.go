package core

import "testing"

func TestValueShape(t *testing.T) {
	cases := map[string]string{
		"":                                     "empty",
		"true":                                 "bool",
		"42":                                   "int",
		"-3.5":                                 "decimal",
		"0197a100-0000-7000-8000-000000000002": "uuid",
		"a@b.io":                               "email",
		"https://x.y/z":                        "url",
		"2026-10-25":                           "date",
		"2026-10-25T10:00:00Z":                 "date",
		"eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.abcdefghij": "token",
		"deadbeefdeadbeef00":                              "hex",
		"QXJlIHlvdSBzdXJlIHlvdSB3YW50IHRoaXM=":            "token",
		"open":                                            "text",
		"invoice 10203":                                   "text",
	}
	for v, want := range cases {
		if got := ValueShape(v); got != want {
			t.Errorf("%q: %s, want %s", v, got, want)
		}
	}
	if BodyShape(float64(3)) != "int" || BodyShape(2.5) != "decimal" || BodyShape([]any{}) != "list" || BodyShape(map[string]any{}) != "object" || BodyShape(nil) != "null" {
		t.Fatal("body shapes")
	}
}

func TestSecretNameAndExample(t *testing.T) {
	for _, n := range []string{"password", "new_password", "accessToken", "x-api-key", "session_id", "csrf", "card_number", "otp"} {
		if !SecretName(n) {
			t.Errorf("%s must read as a secret", n)
		}
	}
	for _, n := range []string{"customer_id", "page", "status", "monkey", "email", "due_at"} {
		if SecretName(n) {
			t.Errorf("%s must not read as a secret", n)
		}
	}
	if Example("page", "2", "int") != "2" || Example("customer_id", "0197a100-0000-7000-8000-000000000002", "uuid") == "" {
		t.Fatal("plain values are kept")
	}
	if Example("password", "hunter2", "text") != "" || Example("email", "a@b.io", "email") != "" || Example("q", "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.abcdefghij", "token") != "" {
		t.Fatal("secrets, addresses and tokens are never kept")
	}
}

func TestWebFactsFold(t *testing.T) {
	w := NewWebFacts()
	route := WebRouteID("api.x", "/invoices/{id}")
	w.Observe(route, WebRequest{Method: "get", Status: 200, Mime: "application/json; charset=utf-8",
		Headers:  map[string]string{"Authorization": "Bearer eyJ.abc.def", "X-Request-Id": "1"},
		Query:    []KV{{"status", "open"}, {"customer_id", "0197a100-0000-7000-8000-000000000002"}},
		Response: `{"customer_id":"0197a100-0000-7000-8000-000000000002"}`})
	w.Observe(route, WebRequest{Method: "PATCH", Status: 401, Mime: "application/json", BodyMime: "application/json",
		Headers: map[string]string{"Cookie": "s=1"}, SetCookies: []string{"Session"},
		Body: map[string]any{"lines": []any{}, "due_at": "2026-11-01", "password": "x"}})
	nodes := []SurfaceNode{
		{ID: route, Kind: "route"},
		{ID: WebParamID(route, "query", "status"), Kind: "param"},
		{ID: WebParamID(route, "query", "customer_id"), Kind: "param"},
		{ID: WebParamID(route, "body", "password"), Kind: "param"},
		{ID: WebParamID(route, "body", "due_at"), Kind: "param"},
		{ID: "untouched", Kind: "host"},
	}
	w.ApplyAll(nodes)
	r := nodes[0].Facts
	want := map[string]string{"methods": "GET, PATCH", "status": "200, 401", "mime": "json", "auth": "bearer, cookie", "body": "json", "headers": "x-request-id", "cookies": "session"}
	for k, v := range want {
		if r[k] != v {
			t.Errorf("route %s = %q, want %q", k, r[k], v)
		}
	}
	if f := nodes[1].Facts; f["in"] != "query" || f["type"] != "text" || f["example"] != "open" || f["reflected"] != "" {
		t.Errorf("status param %v", f)
	}
	if f := nodes[2].Facts; f["type"] != "uuid" || f["reflected"] != "true" || f["example"] == "" {
		t.Errorf("customer_id param %v", f)
	}
	if f := nodes[3].Facts; f["in"] != "body" || f["example"] != "" {
		t.Errorf("password param %v", f)
	}
	if f := nodes[4].Facts; f["type"] != "date" || f["example"] != "2026-11-01" {
		t.Errorf("due_at param %v", f)
	}
	if nodes[5].Facts != nil {
		t.Fatal("a node the traffic did not touch stays as it is")
	}
}
