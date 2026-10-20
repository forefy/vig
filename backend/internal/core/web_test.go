package core

import (
	"testing"
	"time"
)

func TestRouteShapeFoldsIDs(t *testing.T) {
	cases := map[string]string{
		"/":                                     "/",
		"":                                      "/",
		"/users/42/orders/9f1c2d3e4a5b6c7d8e9f": "/users/{id}/orders/{id}",
		"/api/v1/items/":                        "/api/v1/items",
		"/u/3f2504e0-4f89-11d3-9a0c-0305e82c3301": "/u/{id}",
		"/projects/{id}/mcp-servers":              "/projects/{id}/mcp-servers",
		"/agent-runtime/connectors/servicenow":    "/agent-runtime/connectors/servicenow",
		"/t/dGhpcy1pcy1hLXRva2VuMTIz":             "/t/{id}",
		"/some-very-long-static-segment-name":     "/some-very-long-static-segment-name",
	}
	for in, want := range cases {
		if got := RouteShape(in); got != want {
			t.Errorf("%q: got %s want %s", in, got, want)
		}
	}
}

func TestWebPlaceAndIDs(t *testing.T) {
	host, shape, ok := WebPlace("https://App.Example.com/users/7?x=1#frag")
	if !ok || host != "app.example.com" || shape != "/users/{id}" {
		t.Fatalf("place: %s %s %v", host, shape, ok)
	}
	if _, _, ok := WebPlace("not a url"); ok {
		t.Error("a bare word is not a place")
	}
	if WebRouteID(host, shape) != "route:app.example.com:/users/{id}" || WebHostID(host) != "host:app.example.com" {
		t.Error("ids")
	}
	if WebParamID("r", "query", "q") != "r?q" || WebParamID("r", "body", "b") != "r#b" {
		t.Error("param ids")
	}
	if n := WebHostNode("https://app.example.com"); n == nil || n.ID != "host:app.example.com" || n.Kind != "host" || n.Source != "static" {
		t.Errorf("host node: %+v", n)
	}
	if WebHostNode("") != nil {
		t.Error("no host from an empty base url")
	}
}

func TestWebNodesFromFindings(t *testing.T) {
	findings := []Finding{
		{Ordinal: 1, Location: Location{URL: "https://mcp.example.com/authorize"}},
		{Ordinal: 2, Location: Location{File: "a.py"}, Sites: []Site{{Location: Location{URL: "https://app.example.com/api/projects/42/servers"}}}},
		{Ordinal: 3, Location: Location{URL: "https://app.example.com/api/projects/7/servers"}},
		{Ordinal: 4, Location: Location{URL: "https://mcp.example.com/mcp"}, Sites: []Site{{Location: Location{URL: "http://attacker.oastify.com/cb"}}}},
	}
	nodes := WebNodes(findings)
	ids := make([]string, len(nodes))
	for i, n := range nodes {
		ids[i] = n.ID
	}
	want := []string{"host:mcp.example.com", "host:app.example.com", "route:mcp.example.com:/authorize", "route:app.example.com:/api/projects/{id}/servers", "route:mcp.example.com:/mcp"}
	if len(ids) != len(want) {
		t.Fatalf("got %v want %v", ids, want)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Errorf("node %d: got %s want %s", i, ids[i], want[i])
		}
	}
	for _, n := range nodes {
		if n.Source != "static" {
			t.Errorf("%s: source %s", n.ID, n.Source)
		}
		if n.Kind == "route" && n.Parent != WebHostID(n.Facts["host"]) {
			t.Errorf("%s: parent %s", n.ID, n.Parent)
		}
	}
}

// A finding with a URL lights the route its URL folds to, on the host it
// names, and nothing else; requests observe and reviews review; the host
// carries the strongest state beneath it.
func TestSurfaceAttendMatchesURL(t *testing.T) {
	findings := []Finding{
		{Ordinal: 1, Location: Location{URL: "https://app.example.com/api/projects/42/servers"}},
		{Ordinal: 2, Location: Location{File: "a.py"}, Sites: []Site{{Location: Location{URL: "https://mcp.example.com/mcp"}}}},
		{Ordinal: 3, Location: Location{File: "b.py", LineStart: 3}},
		{Ordinal: 4, Location: Location{URL: "https://mcp.example.com/register"}},
	}
	s := Surface{Adapter: "webapp"}
	s.Enrich(append([]SurfaceNode{*WebHostNode("https://app.example.com")}, WebNodes(findings)...), nil)
	s.Enrich([]SurfaceNode{
		{ID: "route:app.example.com:/login", Parent: "host:app.example.com", Kind: "route", Name: "/login", Source: "operator", Facts: map[string]string{"host": "app.example.com", "path": "/login", "methods": "GET, POST"}},
		{ID: "route:app.example.com:/login?next", Parent: "route:app.example.com:/login", Kind: "param", Name: "next", Source: "operator", Facts: map[string]string{"in": "query"}},
		{ID: "route:app.example.com:/health", Parent: "host:app.example.com", Kind: "route", Name: "/health", Source: "operator", Facts: map[string]string{"host": "app.example.com", "path": "/health"}},
	}, nil)
	now := time.Now()
	ev := []SurfaceEvidence{
		{NodeID: "route:app.example.com:/login", Kind: "request", Ref: "POST https://app.example.com/login", At: &now, Summary: map[string]any{"status": float64(200), "ms": float64(120)}},
		{NodeID: "route:app.example.com:/login", Kind: "request", Ref: "GET https://app.example.com/login?next=1", At: &now, Summary: map[string]any{"status": float64(403), "ms": float64(40)}},
		{NodeID: "route:app.example.com:/health", Kind: "review"},
	}
	s.Attend(ev, findings)
	get := func(id string) SurfaceNode {
		for _, n := range s.Nodes {
			if n.ID == id {
				return n
			}
		}
		t.Fatalf("no node %s", id)
		return SurfaceNode{}
	}
	if n := get("route:app.example.com:/api/projects/{id}/servers"); n.Attention != AttentionFinding || len(n.Findings) != 1 || n.Findings[0] != 1 {
		t.Errorf("projects route: %+v", n)
	}
	if n := get("route:mcp.example.com:/mcp"); n.Attention != AttentionFinding || len(n.Findings) != 1 || n.Findings[0] != 2 {
		t.Errorf("mcp route from a site: %+v", n)
	}
	if n := get("route:app.example.com:/login"); n.Attention != AttentionObserved || n.Observed != 2 || n.Stats == nil || n.Stats.Ok != 1 || n.Stats.Failed != 1 || n.Stats.Errors["403"] != 1 || n.Stats.UnitsMin != 40 || n.Stats.UnitsMax != 120 {
		t.Errorf("login: %+v stats %+v", n, n.Stats)
	}
	if n := get("route:app.example.com:/health"); n.Attention != AttentionReviewed {
		t.Errorf("health: %+v", n)
	}
	if n := get("host:app.example.com"); n.Attention != AttentionFinding || n.Stats == nil || n.Stats.Ok+n.Stats.Failed != 2 {
		t.Errorf("host should roll up to finding and aggregate requests: %+v %+v", n, n.Stats)
	}
	if n := get("host:mcp.example.com"); n.Attention != AttentionFinding {
		t.Errorf("mcp host: %+v", n)
	}
	if c := s.Coverage; c == nil || c.Instructions != 5 || c.InstructionsObserved != 2 {
		t.Errorf("coverage: %+v", c)
	}
}
