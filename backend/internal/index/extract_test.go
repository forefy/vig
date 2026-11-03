//go:build cgo

package index

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/parquet-go/parquet-go"
)

const goSrc = `package plugin

import (
	"net/http"
	gh "github.com/google/go-github/v41/github"
)

type Plugin struct{}

func (p *Plugin) initRouter(r *mux.Router) {
	api := r.PathPrefix("/api/v1").Subrouter()
	api.HandleFunc("/connected", p.getConnected).Methods(http.MethodGet)
	r.HandleFunc("/webhook", p.handleWebhook).Methods("POST")
	mountRuns(api.PathPrefix("/runs").Subrouter(), p)
}

func (p *Plugin) getConnected(w http.ResponseWriter, r *http.Request) {
	userID := r.URL.Query().Get("user_id")
	info := p.load(userID)
	db.Exec("select " + info)
	w.Write([]byte(info))
}

func (p *Plugin) handleWebhook(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(204)
}

func (p *Plugin) load(id string) string {
	return "https://api.github.com/users/" + id
}

func mountRuns(router *mux.Router, p *Plugin) {
	router.HandleFunc("/{id}", p.getRun).Methods(http.MethodGet)
}

func (p *Plugin) getRun(w http.ResponseWriter, r *http.Request) {}
`

func fn(t *testing.T, ex Extracted, name string) FunctionRow {
	t.Helper()
	for _, f := range ex.Functions {
		if f.Name == name {
			return f
		}
	}
	t.Fatalf("no function %q in %+v", name, ex.Functions)
	return FunctionRow{}
}

func TestExtractGo(t *testing.T) {
	ex, err := Extract(LangGo, "acme/app", "server/plugin/api.go", []byte(goSrc))
	if err != nil {
		t.Fatal(err)
	}
	got := fn(t, ex, "getConnected")
	if got.Qualname != "Plugin.getConnected" || !got.IsHandler || got.IsExported || got.LineStart != 17 || got.LineEnd != 22 || len(got.Params) != 2 {
		t.Fatalf("getConnected: %+v", got)
	}
	if got.FnID != "acme/app:server/plugin/api.go:getConnected:17" {
		t.Fatalf("fn id %s", got.FnID)
	}
	if l := fn(t, ex, "load"); l.IsHandler {
		t.Fatalf("load is a handler: %+v", l)
	}
	if ir := fn(t, ex, "initRouter"); ir.IsHandler {
		t.Fatalf("initRouter is a handler: %+v", ir)
	}
	// Routes: gorilla patterns with their chained methods and handlers; the
	// one registered on a parameter router knows only its own part here.
	if len(ex.Routes) != 3 {
		t.Fatalf("routes: %+v", ex.Routes)
	}
	if r := ex.Routes[2]; r.Pattern != "/{id}" || r.Router != "router" || r.routerParam != "router" || r.FnID != "acme/app:server/plugin/api.go:mountRuns:32" {
		t.Fatalf("parameter route: %+v", r)
	}
	if len(ex.Bindings) != 1 || ex.Bindings[0].ArgIndex != 0 || ex.Bindings[0].Prefix != "/api/v1/runs" || ex.Bindings[0].Param != "r" {
		t.Fatalf("bindings: %+v", ex.Bindings)
	}
	if r := ex.Routes[0]; r.Pattern != "/api/v1/connected" || r.Method != "GET" || r.Handler != "p.getConnected" || r.Framework != "gorilla" || r.Line != 12 {
		t.Fatalf("route: %+v", r)
	}
	if r := ex.Routes[1]; r.Method != "POST" || r.Handler != "p.handleWebhook" {
		t.Fatalf("route: %+v", r)
	}
	// Calls carry their callee and the caller's id.
	var query, exec CallRow
	for _, c := range ex.Calls {
		switch c.CalleeName {
		case "r.URL.Query().Get":
			query = c
		case "db.Exec":
			exec = c
		}
	}
	if query.CallerFn != got.FnID || query.Line != 18 || len(query.Args) != 1 || query.ArgKinds[0] != "string" {
		t.Fatalf("query call: %+v", query)
	}
	if exec.CallerFn != got.FnID || exec.ArgKinds[0] != "expression" {
		t.Fatalf("exec call: %+v", exec)
	}
	// Flows: r into the Query call, the call into userID, userID into
	// load's argument, info into Exec's argument and into Write.
	want := map[string]bool{
		"recv " + VarPrefix + got.FnID + "#r -> " + CallPrefix + query.CallID:             false,
		"assign " + CallPrefix + query.CallID + " -> " + VarPrefix + got.FnID + "#userID": false,
		"assign " + VarPrefix + got.FnID + "#info -> " + VarPrefix + got.FnID + "#info":   true, // never: a value does not flow into itself
		"arg " + VarPrefix + got.FnID + "#info -> " + CallPrefix + exec.CallID:            false,
	}
	for _, f := range ex.Flows {
		k := f.Kind + " " + f.Src + " -> " + f.Dst
		if v, ok := want[k]; ok {
			if v {
				t.Fatalf("unwanted flow %s", k)
			}
			want[k] = true
		}
	}
	for k, v := range want {
		if !v && !strings.HasPrefix(k, "assign "+VarPrefix+got.FnID+"#info") {
			t.Fatalf("missing flow %s", k)
		}
	}
	// Imports and literals.
	mods := map[string]string{}
	for _, i := range ex.Imports {
		mods[i.Module] = i.Alias
	}
	if mods["net/http"] != "" || mods["github.com/google/go-github/v41/github"] != "gh" {
		t.Fatalf("imports: %+v", ex.Imports)
	}
	kinds := map[string]string{}
	for _, l := range ex.Literals {
		kinds[l.Value] = l.Kind
	}
	if kinds["https://api.github.com/users/"] != "url" || kinds["/api/v1"] != "path" || kinds["/connected"] != "path" || kinds["user_id"] != "string" {
		t.Fatalf("literals: %+v", kinds)
	}
}

const tsSrc = `import express from 'express'
import { readFile as rf } from 'fs/promises'
const router = express.Router()

router.get('/items/:id', async (req, res) => {
  const id = req.params.id
  const row = await db.query('select ' + id)
  res.json(row)
})

export function health(req: Request, res: Response) {
  res.send('ok')
}
router.post('/health', health)

class Store {
  async load(key: string) {
    return cache.get(key)
  }
}
`

func TestExtractTypeScript(t *testing.T) {
	ex, err := Extract(LangTypeScript, "acme/web", "src/api.ts", []byte(tsSrc))
	if err != nil {
		t.Fatal(err)
	}
	anon := fn(t, ex, "<anon>")
	if !anon.IsHandler || anon.LineStart != 5 {
		t.Fatalf("inline handler: %+v", anon)
	}
	h := fn(t, ex, "health")
	if !h.IsHandler || !h.IsExported {
		t.Fatalf("health: %+v", h)
	}
	if l := fn(t, ex, "load"); l.Qualname != "Store.load" || l.IsHandler {
		t.Fatalf("load: %+v", l)
	}
	if len(ex.Routes) != 2 || ex.Routes[0].Pattern != "/items/:id" || ex.Routes[0].Method != "GET" || ex.Routes[0].HandlerFn != anon.FnID || ex.Routes[1].Handler != "health" {
		t.Fatalf("routes: %+v", ex.Routes)
	}
	mods := map[string]string{}
	for _, i := range ex.Imports {
		mods[i.Module+"/"+i.Name] = i.Alias
	}
	if mods["express/default"] != "express" || mods["fs/promises/readFile"] != "rf" {
		t.Fatalf("imports: %+v", ex.Imports)
	}
	var query CallRow
	for _, c := range ex.Calls {
		if c.CalleeName == "db.query" {
			query = c
		}
	}
	found := false
	for _, f := range ex.Flows {
		if f.Kind == "arg" && f.Src == VarPrefix+anon.FnID+"#id" && f.Dst == CallPrefix+query.CallID {
			found = true
		}
	}
	if !found {
		t.Fatalf("id does not flow into db.query: %+v", ex.Flows)
	}
}

const pySrc = `from flask import Flask, request
import os.path as osp

app = Flask(__name__)

@app.route("/search", methods=["GET", "POST"])
def search():
    q = request.args.get("q")
    rows = db.execute("select " + q)
    return rows

class Admin:
    def get(self, request):
        return request.GET
`

func TestExtractPython(t *testing.T) {
	ex, err := Extract(LangPython, "acme/py", "app/views.py", []byte(pySrc))
	if err != nil {
		t.Fatal(err)
	}
	s := fn(t, ex, "search")
	if !s.IsHandler {
		t.Fatalf("search: %+v", s)
	}
	if len(ex.Routes) != 1 || ex.Routes[0].Pattern != "/search" || ex.Routes[0].Method != "GET,POST" || ex.Routes[0].HandlerFn != s.FnID || ex.Routes[0].Framework != "flask" {
		t.Fatalf("routes: %+v", ex.Routes)
	}
	if g := fn(t, ex, "get"); g.Qualname != "Admin.get" || !g.IsHandler {
		t.Fatalf("Admin.get: %+v", g)
	}
	mods := map[string]string{}
	for _, i := range ex.Imports {
		mods[i.Module+"/"+i.Name] = i.Alias
	}
	if _, ok := mods["flask/request"]; !ok || mods["os.path/"] != "osp" {
		t.Fatalf("imports: %+v", ex.Imports)
	}
	// request.args.get reads request, not args or get.
	for _, f := range ex.Flows {
		if strings.HasSuffix(f.Src, "#args") || strings.HasSuffix(f.Src, "#get") {
			t.Fatalf("attribute taken for a variable: %+v", f)
		}
	}
	found := false
	for _, f := range ex.Flows {
		if f.Kind == "recv" && f.Src == VarPrefix+s.FnID+"#request" {
			found = true
		}
	}
	if !found {
		t.Fatal("request does not flow into its call")
	}
}

const rsSrc = `use axum::{routing::get, Router};

#[get("/ping")]
async fn ping() -> &'static str { "pong" }

pub async fn list(Query(q): Query<Params>) -> String { q.name }

fn app() -> Router {
    Router::new().route("/list", get(list))
}
`

func TestExtractRust(t *testing.T) {
	ex, err := Extract(LangRust, "acme/rs", "src/main.rs", []byte(rsSrc))
	if err != nil {
		t.Fatal(err)
	}
	if p := fn(t, ex, "ping"); !p.IsHandler {
		t.Fatalf("ping: %+v", p)
	}
	l := fn(t, ex, "list")
	if !l.IsExported {
		t.Fatalf("list: %+v", l)
	}
	if len(ex.Routes) != 2 || ex.Routes[0].Pattern != "/ping" || ex.Routes[0].Method != "GET" || ex.Routes[1].Pattern != "/list" || ex.Routes[1].Handler != "list" || ex.Routes[1].Method != "GET" {
		t.Fatalf("routes: %+v", ex.Routes)
	}
	if len(ex.Imports) != 1 || !strings.HasPrefix(ex.Imports[0].Module, "axum::") {
		t.Fatalf("imports: %+v", ex.Imports)
	}
}

const harSrc = `{"log":{"entries":[
 {"startedDateTime":"2026-10-30T10:00:00.000Z","time":12.5,"request":{"method":"GET","url":"https://app.acme.test/api/v1/connected?user_id=7","headers":[{"name":"Cookie","value":"MMAUTHTOKEN=secret"},{"name":"Accept","value":"*/*"}],"queryString":[{"name":"user_id","value":"7"}],"cookies":[{"name":"MMAUTHTOKEN"}]},
  "response":{"status":200,"headers":[{"name":"Content-Type","value":"application/json"},{"name":"Set-Cookie","value":"sid=1; Path=/"}],"content":{"mimeType":"application/json","size":2,"text":"{}"}}},
 {"startedDateTime":"2026-10-30T10:00:01.000Z","time":3,"request":{"method":"GET","url":"https://app.acme.test/static/api.go","headers":[],"queryString":[]},
  "response":{"status":200,"headers":[],"content":{"mimeType":"text/plain","size":10,"text":"BODY"}}},
 {"startedDateTime":"2026-10-30T10:00:02.000Z","time":3,"request":{"method":"GET","url":"https://app.acme.test/","headers":[],"queryString":[]},
  "response":{"status":200,"headers":[{"name":"Content-Type","value":"text/html"}],"content":{"mimeType":"text/html","size":6,"text":"<html>"}}},
 {"startedDateTime":"2026-10-30T10:00:03.000Z","time":3,"request":{"method":"GET","url":"https://app.acme.test/api/v1/me","headers":[{"name":"Referer","value":"https://app.acme.test/"}],"queryString":[]},
  "response":{"status":200,"headers":[{"name":"Content-Type","value":"application/json"}],"content":{"mimeType":"application/json","size":30,"text":"{\"data\":{\"id\":\"usr_12345678\",\"name\":\"Ann\"}}"}}},
 {"startedDateTime":"2026-10-30T10:00:04.000Z","time":3,"request":{"method":"GET","url":"https://app.acme.test/api/v1/users/usr_12345678","headers":[{"name":"Referer","value":"https://app.acme.test/"}],"queryString":[]},
  "response":{"status":200,"headers":[{"name":"Content-Type","value":"application/json"}],"content":{"mimeType":"application/json","size":2,"text":"{}"}}}
]}}`

func TestBuildLinksThePillars(t *testing.T) {
	dir := t.TempDir()
	repo := filepath.Join(dir, "src")
	if err := os.MkdirAll(filepath.Join(repo, "server", "plugin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "server", "plugin", "api.go"), []byte(goSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "node_modules", "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "node_modules", "x", "index.js"), []byte("module.exports = 1"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A served body that is a file of the repository: the capture's second
	// response is the bytes of static/api.go.
	if err := os.WriteFile(filepath.Join(repo, "static.go"), []byte("BODY"), 0o644); err != nil {
		t.Fatal(err)
	}
	har := filepath.Join(dir, "session-1.har")
	if err := os.WriteFile(har, []byte(harSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	names := filepath.Join(dir, "names.txt")
	if err := os.WriteFile(names, []byte("# dnsx\napp.acme.test 203.0.113.9\ncdn.acme.test, d1.cloudfront.example\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res := filepath.Join(dir, "resources.json")
	if err := os.WriteFile(res, []byte(`[{"provider":"aws","id":"arn:aws:cloudfront::1:distribution/D1","type":"distribution","service":"cloudfront","public":true,"hostnames":["d1.cloudfront.example"]},{"provider":"aws","id":"arn:aws:s3:::acme-assets","type":"bucket","service":"s3","public":false,"hostnames":["acme-assets.s3.example"]}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out")
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	var notes []string
	sum, err := Build(context.Background(), Options{Root: out, Slug: "t1", Repos: []RepoInput{{Name: "acme/app", Dir: repo, Commit: "abc", Branch: "main"}}, HARs: []string{har}, Names: names, Resources: res, Now: now, Log: func(s string) { notes = append(notes, s) }})
	if err != nil {
		t.Fatal(err)
	}
	if sum.Files != 2 || sum.Functions != 6 || sum.Routes != 3 || sum.Requests != 5 || sum.Bodies != 4 || sum.Hosts != 1 || sum.Subdomains != 2 || sum.Resources != 2 {
		t.Fatalf("summary: %+v\n%s", sum, strings.Join(notes, "\n"))
	}
	tables := map[string]bool{}
	for _, p := range sum.Parts {
		tables[p.Table] = true
		if p.Table == "findings" {
			t.Fatal("an empty table was written")
		}
	}
	for _, want := range []string{"repos", "files", "sources", "functions", "calls", "flows", "imports", "literals", "routes", "har_entries", "bodies", "hosts", "subdomains", "cloud_resources", "edges"} {
		if !tables[want] {
			t.Fatalf("no parts for %s: %v", want, tables)
		}
	}
	// The sources are there line by line, and a text body keeps its text.
	if sum.Lines != countLines([]byte(goSrc))+1 {
		t.Fatalf("lines: %d", sum.Lines)
	}
	sources, err := parquet.ReadFile[SourceRow](filepath.Join(out, "index", "sources", "target=t1", "part-0001.parquet"))
	if err != nil {
		t.Fatal(err)
	}
	if sources[0].Repo != "acme/app" || sources[0].Path != "server/plugin/api.go" || sources[0].Line != 1 || sources[0].Text != "package plugin" {
		t.Fatalf("first source line: %+v", sources[0])
	}
	bodies, err := parquet.ReadFile[BodyRow](filepath.Join(out, "index", "bodies", "target=t1", "part-0001.parquet"))
	if err != nil {
		t.Fatal(err)
	}
	texts := map[string]bool{}
	for _, b := range bodies {
		texts[b.Text] = true
	}
	if !texts["{}"] || !texts["BODY"] || !texts["<html>"] {
		t.Fatalf("body text: %v", texts)
	}
	// Read the edges back and check each pillar is joined.
	edges, err := parquet.ReadFile[EdgeRow](filepath.Join(out, "index", "edges", "target=t1", "part-0001.parquet"))
	if err != nil {
		t.Fatal(err)
	}
	rels := map[string][]EdgeRow{}
	for _, e := range edges {
		if e.Target != "t1" {
			t.Fatalf("target on edge: %+v", e)
		}
		rels[e.Rel] = append(rels[e.Rel], e)
	}
	find := func(rel, src, dst string) *EdgeRow {
		for _, e := range rels[rel] {
			if e.SrcID == src && e.DstID == dst {
				return &e
			}
		}
		return nil
	}
	fnGet := FnPrefix + "acme/app:server/plugin/api.go:getConnected:17"
	if e := find("calls", fnGet, FnPrefix+"acme/app:server/plugin/api.go:load:28"); e == nil || e.Confidence != 1 {
		t.Fatalf("calls edge: %+v", rels["calls"])
	}
	if e := find("routes", HostPrefix+"app.acme.test", fnGet); e == nil || e.Evidence != "GET /api/v1/connected" {
		t.Fatalf("routes edge: %+v", rels["routes"])
	}
	// The route registered through the parameter router carries the
	// caller's prefix once the build linked them.
	routes, err := parquet.ReadFile[RouteRow](filepath.Join(out, "index", "routes", "target=t1", "part-0001.parquet"))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, r := range routes {
		got[r.Handler] = r.Pattern
	}
	if got["p.getRun"] != "/api/v1/runs/{id}" || got["p.getConnected"] != "/api/v1/connected" {
		t.Fatalf("patterns: %v", got)
	}
	if len(rels["serves"]) != 4 {
		t.Fatalf("serves: %+v", rels["serves"])
	}
	// Every entry is a node its host requests, returning its body.
	if len(rels["requests"]) != 5 || len(rels["returns"]) != 5 {
		t.Fatalf("requests/returns: %+v %+v", rels["requests"], rels["returns"])
	}
	// The document triggers the two requests that name it as referer, the
	// id the first returns travels into the second's path, and the first
	// entry sends user_id by name.
	if len(rels["triggers"]) != 2 {
		t.Fatalf("triggers: %+v", rels["triggers"])
	}
	for _, e := range rels["triggers"] {
		if !strings.HasPrefix(e.Evidence, "GET /api/v1/") {
			t.Fatalf("triggers edge: %+v", e)
		}
	}
	if len(rels["yields"]) != 1 || rels["yields"][0].Evidence != "data.id: usr_12345678" || rels["yields"][0].DstID != valueID("usr_12345678") {
		t.Fatalf("yields: %+v", rels["yields"])
	}
	if len(rels["sent_in"]) != 1 || rels["sent_in"][0].SrcID != valueID("usr_12345678") || rels["sent_in"][0].Evidence != "path" {
		t.Fatalf("sent_in: %+v", rels["sent_in"])
	}
	if len(rels["sends"]) != 1 || rels["sends"][0].DstID != ParamPrefix+"user_id" || rels["sends"][0].Evidence != "7" {
		t.Fatalf("sends: %+v", rels["sends"])
	}
	for _, e := range rels["requests"] {
		if e.SrcID != HostPrefix+"app.acme.test" || !strings.HasPrefix(e.DstID, EntryPrefix) || !strings.HasPrefix(e.Evidence, "GET /") {
			t.Fatalf("requests edge: %+v", e)
		}
		if find("returns", e.DstID, "") == nil && len(rels["returns"]) > 0 {
			found := false
			for _, r := range rels["returns"] {
				if r.SrcID == e.DstID && strings.HasPrefix(r.DstID, BodyPrefix) && r.Evidence == "200" {
					found = true
				}
			}
			if !found {
				t.Fatalf("no returns edge for %s: %+v", e.DstID, rels["returns"])
			}
		}
	}
	bodySHA := ""
	for _, e := range rels["serves_repo"] {
		if e.DstID == RepoPrefix+"acme/app" && e.Evidence == "static.go" {
			bodySHA = e.SrcID
		}
	}
	if bodySHA == "" || find("serves", HostPrefix+"app.acme.test", bodySHA) == nil {
		t.Fatalf("serves_repo: %+v", rels["serves_repo"])
	}
	if e := find("mentions_host", LiteralPrefix+"acme/app:server/plugin/api.go:29", HostPrefix+"api.github.com"); e != nil {
		t.Fatalf("an unknown host was taken as known: %+v", e)
	}
	if e := find("resolves_to", SubdomainPrefix+"cdn.acme.test", ResourcePrefix+"arn:aws:cloudfront::1:distribution/D1"); e == nil || e.Confidence != 1 {
		t.Fatalf("resolves_to: %+v", rels["resolves_to"])
	}
	// The flow across the resolved call: the argument into load's
	// parameter, and load's return back into the call.
	flows, err := parquet.ReadFile[FlowRow](filepath.Join(out, "index", "flows", "target=t1", "part-0001.parquet"))
	if err != nil {
		t.Fatal(err)
	}
	param, ret := false, false
	for _, f := range flows {
		if f.Kind == "param" && f.Dst == VarPrefix+"acme/app:server/plugin/api.go:load:28#id" {
			param = true
		}
		if f.Kind == "return" && f.Src == RetPrefix+"acme/app:server/plugin/api.go:load:28" {
			ret = true
		}
	}
	if !param || !ret {
		t.Fatalf("flows across the call: param %v return %v", param, ret)
	}
	// The capture's secrets stay out of the index.
	entries, err := parquet.ReadFile[HAREntryRow](filepath.Join(out, "index", "har_entries", "target=t1", "part-0001.parquet"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 5 || entries[0].ReqHeaders["cookie"] != "" || entries[0].ReqHeaders["accept"] != "*/*" || entries[0].Query != "user_id=7" || len(entries[0].CookieNames) != 2 {
		t.Fatalf("entries: %+v", entries)
	}
	// A second build replaces the first.
	sum2, err := Build(context.Background(), Options{Root: out, Slug: "t1", Repos: []RepoInput{{Name: "acme/app", Dir: repo}}, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range sum2.Parts {
		if p.Table == "har_entries" {
			t.Fatal("the earlier build's capture lingered")
		}
	}
}

func TestLiteralKind(t *testing.T) {
	cases := map[string]string{
		"api.github.com":   "host",
		"github.com":       "host",
		"app.acme.test":    "host",
		"s3.amazonaws.com": "host",
		"https://x.io/a":   "url",
		"/api/v1/users":    "path",
		"privateKeyPassphraseHandler.options.password": "string",
		"config.db.host": "string",
		"Foo.Bar.Com":    "string",
		"hello world":    "string",
	}
	for v, want := range cases {
		if got := literalKind(v); got != want {
			t.Errorf("%q: got %s, want %s", v, got, want)
		}
	}
}
