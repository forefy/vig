package repo

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/parquet-go/parquet-go"

	"github.com/forefy/vig/backend/internal/core"
)

// writeTable writes one Parquet part of a table under the index directory,
// the way the indexer lays them out.
func writeTable[T any](t *testing.T, dir, table string, rows []T) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, table), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := parquet.WriteFile(filepath.Join(dir, table, "part-0.parquet"), rows); err != nil {
		t.Fatal(err)
	}
}

func TestBuildSurfaceReadsTheIndex(t *testing.T) {
	dir := t.TempDir()
	const (
		h1 = "acme/app:server/api.go:ServeHTTP:10"
		h2 = "acme/app:server/admin.go:Handle:5"
		h3 = "acme/app:server/hooks.go:OnEvent:40"
		g  = "acme/app:server/db.go:load:30"
		th = "acme/app:server/api_test.go:<anon>:14"
	)
	writeTable(t, dir, "repos", []repoRow{{Repo: "acme/app", Commit: "abc123", Language: "go"}})
	writeTable(t, dir, "functions", []functionRow{
		{Repo: "acme/app", Path: "server/api.go", FnID: h1, Name: "ServeHTTP", Qualname: "Plugin.ServeHTTP", Params: []string{"w http.ResponseWriter", "r *http.Request"}, LineStart: 10, LineEnd: 24, Exported: true, Handler: true},
		{Repo: "acme/app", Path: "server/admin.go", FnID: h2, Name: "Handle", Qualname: "Admin.Handle", Params: []string{"w http.ResponseWriter", "r *http.Request"}, LineStart: 5, LineEnd: 9, Exported: true, Handler: true},
		{Repo: "acme/app", Path: "server/hooks.go", FnID: h3, Name: "OnEvent", Qualname: "Plugin.OnEvent", Params: []string{"c *gin.Context"}, LineStart: 40, LineEnd: 48, Exported: true, Handler: true},
		{Repo: "acme/app", Path: "server/db.go", FnID: g, Name: "load", Qualname: "load", Params: []string{"id string"}, LineStart: 30, LineEnd: 40},
		{Repo: "acme/app", Path: "server/api_test.go", FnID: th, Name: "<anon>", Qualname: "TestServe.<anon>", LineStart: 14, LineEnd: 20, Handler: true},
	})
	writeTable(t, dir, "calls", []callRow{
		{Path: "server/db.go", CallID: "acme/app:server/db.go:35:20", CallerFn: g, CalleeName: "db.Query", Line: 35},
		{Path: "server/api.go", CallID: "acme/app:server/api.go:12:30", CallerFn: h1, CalleeName: "r.URL.Query().Get", Line: 12},
		{Path: "server/api.go", CallID: "acme/app:server/api.go:13:11", CallerFn: h1, CalleeName: "r.URL.Query", Line: 13},
		{Path: "server/api.go", CallID: "acme/app:server/api.go:13:40", CallerFn: h1, CalleeName: "redirectURL.Query", Line: 13},
		{Path: "server/api_test.go", CallID: "acme/app:server/api_test.go:16:8", CallerFn: th, CalleeName: "db.Query", Line: 16},
		{Path: "server/api.go", CallID: "acme/app:server/api.go:14:12", CallerFn: h1, CalleeName: "p.load", Line: 14},
		{Path: "server/api.go", CallID: "acme/app:server/api.go:20:9", CallerFn: h1, CalleeName: "w.Write", Line: 20},
		{Path: "server/hooks.go", CallID: "acme/app:server/hooks.go:44:12", CallerFn: h3, CalleeName: "exec.Command", Line: 44},
	})
	writeTable(t, dir, "edges", []edgeRow{
		{SrcID: "fn:" + h1, DstID: "fn:" + g, Rel: "calls", Confidence: 1},
		{SrcID: "fn:" + h1, DstID: "fn:" + h2, Rel: "calls", Confidence: 1},
		{SrcID: "fn:" + h1, DstID: "fn:" + th, Rel: "calls", Confidence: 1},
		{SrcID: "fn:" + h2, DstID: "fn:" + g, Rel: "calls", Confidence: 0.4},
		{SrcID: "fn:" + h3, DstID: "fn:" + g, Rel: "calls", Confidence: 1},
		{SrcID: "var:x", DstID: "var:y", Rel: "flows", Confidence: 1},
	})
	// The request's travel in ServeHTTP: r into the query call, its result
	// into id, id into load's argument, into load's parameter, into the
	// db.Query call.
	writeTable(t, dir, "flows", []flowRow{
		{Src: "var:" + h1 + "#r", Dst: "call:acme/app:server/api.go:12:30", Kind: "recv"},
		{Src: "call:acme/app:server/api.go:12:30", Dst: "var:" + h1 + "#id", Kind: "assign"},
		{Src: "var:" + h1 + "#id", Dst: "call:acme/app:server/api.go:14:12", Kind: "arg"},
		{Src: "call:acme/app:server/api.go:14:12", Dst: "var:" + g + "#id", Kind: "param"},
		{Src: "var:" + g + "#id", Dst: "call:acme/app:server/db.go:35:20", Kind: "arg"},
		// A value that never leaves the admin handler.
		{Src: "var:" + h2 + "#r", Dst: "var:" + h2 + "#x", Kind: "assign"},
	})

	s, ev, err := New().BuildSurface(context.Background(), core.SurfaceInput{Repo: "acme/app", Index: dir})
	if err != nil {
		t.Fatal(err)
	}
	if len(ev) != 0 || s.Adapter != Key {
		t.Fatalf("evidence %d adapter %q", len(ev), s.Adapter)
	}
	byID := map[string]core.SurfaceNode{}
	for _, n := range s.Nodes {
		byID[n.ID] = n
	}
	pkg, ok := byID["p:acme/app/server"]
	if !ok || pkg.Kind != "program" || pkg.Name != "app/server" || pkg.Facts["handlers"] != "3" || pkg.Facts["language"] != "go" {
		t.Fatalf("package: %+v", pkg)
	}
	hn, ok := byID["ix:"+h1]
	if !ok || hn.Kind != "instruction" || hn.Parent != pkg.ID || hn.Name != "Plugin.ServeHTTP" || hn.Facts["file"] != "server/api.go" || hn.Facts["line"] != "10" || hn.Facts["line_end"] != "24" || hn.Facts["request"] != "r" || hn.Facts["tainted"] != "1" || hn.Facts["sinks"] != "1" {
		t.Fatalf("handler: %+v", hn)
	}
	if _, ok := byID["ix:"+th]; ok {
		t.Fatal("a test file's handler is on the map")
	}
	// The request reached db.Query through load: a tainted sink, two calls
	// crossed on the way (the query read and load itself).
	sink, ok := byID["sink:"+h1+"#"+g+"#db.Query"]
	if !ok || sink.Kind != "account" || sink.Parent != hn.ID || sink.Name != "db.Query" || sink.Facts["via"] != "taint" || sink.Facts["hops"] != "2" || sink.Facts["line"] != "35" || sink.Facts["in"] != "load" {
		t.Fatalf("sink: %+v", sink)
	}
	// The query read on the request is where input comes from, not a sink.
	if _, ok := byID["sink:"+h1+"#"+h1+"#r.URL.Query().Get"]; ok {
		t.Fatal("the query read on the request is a sink")
	}
	if _, ok := byID["sink:"+h1+"#"+h1+"#r.URL.Query"]; ok {
		t.Fatal("the query parse on the request is a sink")
	}
	if _, ok := byID["sink:"+h1+"#"+h1+"#redirectURL.Query"]; ok {
		t.Fatal("a query parse on a named URL is a sink")
	}
	// A call resolved into a test file is not a path a request takes.
	if _, ok := byID["sink:"+h1+"#"+th+"#db.Query"]; ok {
		t.Fatal("a sink in a test file is on the map")
	}
	// The admin handler's request goes nowhere, and its guessed call is not
	// followed: no sinks.
	if h2n := byID["ix:"+h2]; h2n.Facts["sinks"] != "" || h2n.Facts["tainted"] != "" {
		t.Fatalf("admin handler reached a sink: %+v", h2n)
	}
	// The gin handler's context has no flows; the call graph says it can
	// reach exec.Command in its own body and db.Query through load.
	h3n := byID["ix:"+h3]
	if h3n.Facts["request"] != "c" || h3n.Facts["tainted"] != "" || h3n.Facts["sinks"] != "2" {
		t.Fatalf("gin handler: %+v", h3n)
	}
	if s3 := byID["sink:"+h3+"#"+h3+"#exec.Command"]; s3.Facts["via"] != "calls" || s3.Facts["hops"] != "0" {
		t.Fatalf("exec sink: %+v", s3)
	}
	if len(s.Edges) != 1 || s.Edges[0].From != "ix:"+h1 || s.Edges[0].To != "ix:"+h2 || s.Edges[0].Kind != "calls" {
		t.Fatalf("edges: %+v", s.Edges)
	}

	// A finding at a line of the handler lands on it.
	s.Attend(nil, []core.Finding{{Ordinal: 3, Location: core.Location{File: "server/api.go", LineStart: 15}}})
	for _, n := range s.Nodes {
		if n.ID == hn.ID && (len(n.Findings) != 1 || n.Findings[0] != 3) {
			t.Fatalf("finding not on the handler: %+v", n)
		}
		if n.ID == pkg.ID && n.Attention != core.AttentionFinding {
			t.Fatalf("package attention %s", n.Attention)
		}
	}
}

func TestBuildSurfaceWithoutIndexIsEmpty(t *testing.T) {
	s, _, err := New().BuildSurface(context.Background(), core.SurfaceInput{Repo: "acme/app"})
	if err != nil || len(s.Nodes) != 0 || s.Adapter != Key {
		t.Fatalf("%v %+v", err, s)
	}
	s, _, err = New().BuildSurface(context.Background(), core.SurfaceInput{Repo: "acme/app", Index: filepath.Join(t.TempDir(), "missing")})
	if err != nil || len(s.Nodes) != 0 {
		t.Fatalf("%v %+v", err, s)
	}
}

func TestRequestParam(t *testing.T) {
	for _, c := range []struct {
		params []string
		want   string
	}{
		{[]string{"w http.ResponseWriter", "r *http.Request"}, "r"},
		{[]string{"c *gin.Context"}, "c"},
		{[]string{"req: Request", "res: Response"}, "req"},
		{[]string{"self", "request"}, "request"},
		{[]string{"ctx context.Context", "id string"}, ""},
		{[]string{"ctx"}, "ctx"},
		{[]string{"id string"}, ""},
	} {
		if got := requestParam(c.params); got != c.want {
			t.Errorf("%v: got %q want %q", c.params, got, c.want)
		}
	}
}
