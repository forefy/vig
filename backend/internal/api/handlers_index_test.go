package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/forefy/vig/backend/internal/adapter/solana"
	"github.com/forefy/vig/backend/internal/auth"
	"github.com/forefy/vig/backend/internal/config"
	"github.com/forefy/vig/backend/internal/core"
	"github.com/forefy/vig/backend/internal/db/testdb"
	"github.com/forefy/vig/backend/internal/store"
)

func TestMain(m *testing.M) {
	code := m.Run()
	testdb.Stop()
	os.Exit(code)
}

// indexHarness is a router over a real store with two users, an admin, two
// audits of the owner and an operator token bound to each.
type indexHarness struct {
	t       *testing.T
	ctx     context.Context
	d       *Deps
	h       http.Handler
	s       *store.Store
	owner   store.User
	other   store.User
	admin   store.User
	a, b    store.Audit
	ownerK  string // API key of the owner
	otherK  string // API key of the other user
	tokA    string // operator token bound to a
	tokB    string // operator token bound to b
	dataDir string
}

func newIndexHarness(t *testing.T) *indexHarness {
	t.Helper()
	t.Setenv("RATE_LIMIT_DISABLED", "1")
	ctx := context.Background()
	s := testdb.New(t)
	h := &indexHarness{t: t, ctx: ctx, s: s, dataDir: t.TempDir()}
	h.owner = h.user("alice")
	h.other = h.user("bob")
	h.admin = h.user("root")
	if err := s.SetRole(ctx, h.admin.ID, "admin"); err != nil {
		t.Fatal(err)
	}
	tg, err := s.CreateTarget(ctx, store.Target{Adapter: "solana", OwnerUserID: h.owner.ID, SourceKind: "public", Repo: "acme/vault", Selector: "branch", Revision: "main", CommitSHA: "aaaa1111"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendLedgerLocked(ctx, store.LedgerRow{UserID: h.owner.ID, Kind: core.LedgerGrant, Delta: 2, Reason: "test"}); err != nil {
		t.Fatal(err)
	}
	queue := func(head string) store.Audit {
		a, created, err := s.QueueAudit(ctx, store.Audit{UserID: h.owner.ID, TargetID: tg.ID, Coverage: "full", HeadSHA: head, ProgramIDs: []string{"P1"}, Scope: store.Scope{Files: []string{"src/lib.rs"}, Lines: 10}})
		if err != nil || !created {
			t.Fatalf("queue %s: created=%v err=%v", head, created, err)
		}
		return a
	}
	h.a, h.b = queue("aaaa1111"), queue("bbbb2222")
	h.ownerK, h.otherK = h.apiKey(h.owner), h.apiKey(h.other)
	h.tokA, h.tokB = h.opToken(h.a), h.opToken(h.b)
	h.d = &Deps{Config: config.Config{DataDir: h.dataDir}, Store: s, Adapters: map[string]core.Adapter{solana.Key: solana.New()}}
	h.h = NewRouter(h.d)
	return h
}

func (h *indexHarness) user(name string) store.User {
	h.t.Helper()
	u, err := h.s.CreateUser(h.ctx, h.s.Pool, name, "")
	if err != nil {
		h.t.Fatal(err)
	}
	return u
}

func (h *indexHarness) apiKey(u store.User) string {
	h.t.Helper()
	key, prefix, hash := auth.MintAPIKey()
	if _, err := h.s.CreateAPIKey(h.ctx, u.ID, prefix, "test", hash); err != nil {
		h.t.Fatal(err)
	}
	return key
}

func (h *indexHarness) opToken(a store.Audit) string {
	h.t.Helper()
	tok := auth.RandomToken(24)
	if _, err := h.s.CreateOperatorToken(h.ctx, a.ID, auth.Hash(tok), time.Now().Add(time.Hour), ""); err != nil {
		h.t.Fatal(err)
	}
	return tok
}

// do sends one request with a bearer (or none) and a JSON body (or none).
func (h *indexHarness) do(method, path, bearer string, in any, hdr ...string) *httptest.ResponseRecorder {
	h.t.Helper()
	var body *bytes.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			h.t.Fatal(err)
		}
		body = bytes.NewReader(b)
	} else {
		body = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, body)
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	rr := httptest.NewRecorder()
	h.h.ServeHTTP(rr, req)
	return rr
}

func (h *indexHarness) put(tok string, auditID, table, file string, raw []byte) *httptest.ResponseRecorder {
	h.t.Helper()
	return h.do(http.MethodPut, "/v1/op/jobs/"+auditID+"/index/parts", tok, map[string]any{
		"table": table, "file": file, "sha256": digest(raw), "data_base64": base64.StdEncoding.EncodeToString(raw)})
}

func (h *indexHarness) seal(tok string, auditID string, m core.IndexManifest) *httptest.ResponseRecorder {
	h.t.Helper()
	return h.do(http.MethodPost, "/v1/op/jobs/"+auditID+"/index", tok, m)
}

func (h *indexHarness) indexPath(auditID string, rel ...string) string {
	return filepath.Join(append([]string{h.dataDir, "index", auditID}, rel...)...)
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// parquet makes a byte slice the server takes for a Parquet file: the
// magic at both ends, anything in between.
func parquet(payload string) []byte { return []byte("PAR1" + payload + "PAR1") }

func digest(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func part(file string, raw []byte) core.IndexPart {
	return core.IndexPart{File: file, Bytes: int64(len(raw)), SHA256: digest(raw)}
}

func decode[T any](t *testing.T, rr *httptest.ResponseRecorder) T {
	t.Helper()
	var out T
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode %s: %v", rr.Body, err)
	}
	return out
}

func TestIndexFlow(t *testing.T) {
	h := newIndexHarness(t)
	A := h.a.ID
	calls1, calls2, repos1 := parquet("calls one, row group"), parquet("calls two"), parquet("repos")

	// An operator token bound to another job cannot stage here.
	if rr := h.put(h.tokB, A, "calls", "part-1.parquet", calls1); rr.Code != http.StatusForbidden {
		t.Fatalf("other job's token: %d %s", rr.Code, rr.Body)
	}
	// No token at all: the operator scheme is required.
	if rr := h.put("", A, "calls", "part-1.parquet", calls1); rr.Code != http.StatusUnauthorized {
		t.Fatalf("no token: %d %s", rr.Code, rr.Body)
	}
	// A user's API key is not an operator token either.
	if rr := h.put(h.ownerK, A, "calls", "part-1.parquet", calls1); rr.Code != http.StatusUnauthorized {
		t.Fatalf("api key on op route: %d %s", rr.Code, rr.Body)
	}
	// Bytes that are not Parquet, and a digest that does not match.
	if rr := h.put(h.tokA, A, "calls", "part-1.parquet", []byte("just some bytes here")); rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("not parquet: %d %s", rr.Code, rr.Body)
	}
	if rr := h.do(http.MethodPut, "/v1/op/jobs/"+A+"/index/parts", h.tokA, map[string]any{
		"table": "calls", "file": "part-1.parquet", "sha256": digest([]byte("other")), "data_base64": base64.StdEncoding.EncodeToString(calls1)}); rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("wrong digest on put: %d %s", rr.Code, rr.Body)
	}

	// Stage two parts of calls and one of repos.
	for i, p := range []struct {
		table, file string
		raw         []byte
	}{{"calls", "part-1.parquet", calls1}, {"calls", "part-2.parquet", calls2}, {"repos", "part-1.parquet", repos1}} {
		rr := h.put(h.tokA, A, p.table, p.file, p.raw)
		if rr.Code != http.StatusOK {
			t.Fatalf("put %s/%s: %d %s", p.table, p.file, rr.Code, rr.Body)
		}
		out := decode[struct {
			Table  string `json:"table"`
			File   string `json:"file"`
			Bytes  int64  `json:"bytes"`
			Staged int    `json:"staged"`
		}](t, rr)
		if out.Table != p.table || out.File != p.file || out.Bytes != int64(len(p.raw)) || out.Staged != i+1 {
			t.Fatalf("put %s/%s: %+v", p.table, p.file, out)
		}
	}
	if !exists(h.indexPath(A, ".staging", "calls", "part-2.parquet")) {
		t.Fatal("part not staged on disk")
	}
	// Nothing is visible before the seal.
	if rr := h.do(http.MethodGet, "/v1/audits/"+A+"/index", h.ownerK, nil); rr.Code != http.StatusNotFound {
		t.Fatalf("index before seal: %d %s", rr.Code, rr.Body)
	}

	// A manifest naming a part that was never staged.
	m := core.IndexManifest{Tool: "zz", ToolVersion: "0.3", Tables: []core.IndexTable{
		{Name: "calls", Rows: 12, Parts: []core.IndexPart{part("part-1.parquet", calls1), part("part-2.parquet", calls2), part("part-3.parquet", parquet("ghost"))}},
		{Name: "repos", Parts: []core.IndexPart{part("part-1.parquet", repos1)}},
	}}
	if rr := h.seal(h.tokA, A, m); rr.Code != http.StatusUnprocessableEntity || !bytes.Contains(rr.Body.Bytes(), []byte("not staged")) {
		t.Fatalf("unstaged part: %d %s", rr.Code, rr.Body)
	}
	// A manifest whose digest differs from the staged bytes.
	m.Tables[0].Parts = m.Tables[0].Parts[:2]
	m.Tables[0].Parts[1].SHA256 = digest([]byte("something else"))
	if rr := h.seal(h.tokA, A, m); rr.Code != http.StatusUnprocessableEntity || !bytes.Contains(rr.Body.Bytes(), []byte("digest")) {
		t.Fatalf("wrong digest: %d %s", rr.Code, rr.Body)
	}
	// Still nothing live, and the staged files are untouched.
	if exists(h.indexPath(A, "calls")) || !exists(h.indexPath(A, ".staging", "repos", "part-1.parquet")) {
		t.Fatal("a refused seal moved files")
	}

	// The matching manifest seals.
	m.Tables[0].Parts[1] = part("part-2.parquet", calls2)
	rr := h.seal(h.tokA, A, m)
	if rr.Code != http.StatusOK {
		t.Fatalf("seal: %d %s", rr.Code, rr.Body)
	}
	build := decode[IndexBuild](t, rr)
	if build.AuditID != A || build.Tool != "zz" || build.ToolVersion != "0.3" || len(build.Tables) != 2 || build.Bytes != int64(len(calls1)+len(calls2)+len(repos1)) {
		t.Fatalf("build: %+v", build)
	}
	if build.Tables[0].Name != "calls" || build.Tables[0].Rows != 12 || build.Tables[0].Bytes != int64(len(calls1)+len(calls2)) || len(build.Tables[0].Parts) != 2 {
		t.Fatalf("calls table: %+v", build.Tables[0])
	}
	partURL := build.Tables[0].Parts[1].URL
	if partURL != "/v1/audits/"+A+"/index/calls/part-2.parquet" {
		t.Fatalf("part url %q", partURL)
	}
	if exists(h.indexPath(A, ".staging")) || !exists(h.indexPath(A, "calls", "part-1.parquet")) || !exists(h.indexPath(A, "repos", "part-1.parquet")) {
		t.Fatal("seal did not swap the tables in")
	}

	// The owner reads the catalogue, by audit and across audits.
	rr = h.do(http.MethodGet, "/v1/audits/"+A+"/index", h.ownerK, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("audit index: %d %s", rr.Code, rr.Body)
	}
	if got := decode[IndexBuild](t, rr); got.Repo != "acme/vault" || got.Adapter != "solana" || got.Tables[1].Parts[0].URL != "/v1/audits/"+A+"/index/repos/part-1.parquet" {
		t.Fatalf("audit index: %+v", got)
	}
	rr = h.do(http.MethodGet, "/v1/atlas/index", h.ownerK, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("atlas: %d %s", rr.Code, rr.Body)
	}
	atlas := decode[AtlasIndex](t, rr)
	if len(atlas.Audits) != 1 || atlas.Audits[0].AuditID != A || atlas.Audits[0].Tables[0].Parts[1].URL != partURL {
		t.Fatalf("atlas: %+v", atlas)
	}

	// The part comes back whole, as Parquet, and sliced on a Range.
	rr = h.do(http.MethodGet, partURL, h.ownerK, nil)
	if rr.Code != http.StatusOK || rr.Header().Get("Content-Type") != indexMime || !bytes.Equal(rr.Body.Bytes(), calls2) {
		t.Fatalf("get part: %d %s %q", rr.Code, rr.Header().Get("Content-Type"), rr.Body)
	}
	if rr.Header().Get("Accept-Ranges") != "bytes" || rr.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("part headers: %v", rr.Header())
	}
	rr = h.do(http.MethodGet, partURL, h.ownerK, nil, "Range", "bytes=4-8")
	if rr.Code != http.StatusPartialContent || !bytes.Equal(rr.Body.Bytes(), calls2[4:9]) || rr.Header().Get("Content-Type") != indexMime {
		t.Fatalf("range: %d %q %v", rr.Code, rr.Body, rr.Header())
	}
	if cr := rr.Header().Get("Content-Range"); cr != "bytes 4-8/"+itoa(len(calls2)) {
		t.Fatalf("content-range %q", cr)
	}
	// The footer alone, as a reader asks for it.
	rr = h.do(http.MethodGet, partURL, h.ownerK, nil, "Range", "bytes=-4")
	if rr.Code != http.StatusPartialContent || string(rr.Body.Bytes()) != "PAR1" {
		t.Fatalf("footer range: %d %q", rr.Code, rr.Body)
	}

	// Another user: no part, no catalogue, an empty atlas.
	if rr := h.do(http.MethodGet, partURL, h.otherK, nil); rr.Code != http.StatusNotFound {
		t.Fatalf("other user's part: %d", rr.Code)
	}
	if rr := h.do(http.MethodGet, "/v1/audits/"+A+"/index", h.otherK, nil); rr.Code != http.StatusNotFound {
		t.Fatalf("other user's index: %d", rr.Code)
	}
	rr = h.do(http.MethodGet, "/v1/atlas/index", h.otherK, nil)
	if rr.Code != http.StatusOK || len(decode[AtlasIndex](t, rr).Audits) != 0 {
		t.Fatalf("other user's atlas: %d %s", rr.Code, rr.Body)
	}
	// Anonymous: sign in required on the part route too.
	if rr := h.do(http.MethodGet, partURL, "", nil); rr.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous part: %d", rr.Code)
	}
	// A file that exists on disk but is not in the catalogue is not served,
	// and neither is a name that fails validation.
	if err := os.WriteFile(h.indexPath(A, "calls", "stray.parquet"), calls1, 0o600); err != nil {
		t.Fatal(err)
	}
	if rr := h.do(http.MethodGet, "/v1/audits/"+A+"/index/calls/stray.parquet", h.ownerK, nil); rr.Code != http.StatusNotFound {
		t.Fatalf("stray file served: %d", rr.Code)
	}
	if rr := h.do(http.MethodGet, "/v1/audits/"+A+"/index/calls/..%2Fpart-1.parquet", h.ownerK, nil); rr.Code != http.StatusNotFound {
		t.Fatalf("traversal: %d", rr.Code)
	}

	// Re-seal with a different set of tables: the old directories go.
	flows := parquet("flows, the whole table")
	if rr := h.do(http.MethodDelete, "/v1/op/jobs/"+A+"/index/staging", h.tokA, nil); rr.Code >= 300 {
		t.Fatalf("drop staging: %d %s", rr.Code, rr.Body)
	}
	if rr := h.put(h.tokA, A, "flows", "part-1.parquet", flows); rr.Code != http.StatusOK {
		t.Fatalf("stage flows: %d %s", rr.Code, rr.Body)
	}
	// A part staged and then left out of the manifest is dropped, not kept.
	if rr := h.put(h.tokA, A, "flows", "orphan.parquet", parquet("orphan")); rr.Code != http.StatusOK {
		t.Fatalf("stage orphan: %d %s", rr.Code, rr.Body)
	}
	rr = h.seal(h.tokA, A, core.IndexManifest{Tool: "zz", ToolVersion: "0.4", Tables: []core.IndexTable{{Name: "flows", Parts: []core.IndexPart{part("part-1.parquet", flows)}}}})
	if rr.Code != http.StatusOK {
		t.Fatalf("re-seal: %d %s", rr.Code, rr.Body)
	}
	if exists(h.indexPath(A, "calls")) || exists(h.indexPath(A, "repos")) || exists(h.indexPath(A, ".staging")) {
		t.Fatal("re-seal kept the old tables")
	}
	if !exists(h.indexPath(A, "flows", "part-1.parquet")) || exists(h.indexPath(A, "flows", "orphan.parquet")) {
		t.Fatal("re-seal did not place exactly the named parts")
	}
	if rr := h.do(http.MethodGet, partURL, h.ownerK, nil); rr.Code != http.StatusNotFound {
		t.Fatalf("old part after re-seal: %d", rr.Code)
	}
	rr = h.do(http.MethodGet, "/v1/audits/"+A+"/index/flows/part-1.parquet", h.ownerK, nil)
	if rr.Code != http.StatusOK || !bytes.Equal(rr.Body.Bytes(), flows) {
		t.Fatalf("new part: %d", rr.Code)
	}
	got, err := h.s.GetIndex(h.ctx, A)
	if err != nil || got.ToolVersion != "0.4" || len(got.Tables) != 1 || got.Tables[0].Name != "flows" {
		t.Fatalf("catalogue after re-seal: %+v %v", got, err)
	}

	// Staging over the stale catalogue does not change what is served.
	if rr := h.put(h.tokA, A, "flows", "part-2.parquet", parquet("unsealed")); rr.Code != http.StatusOK {
		t.Fatalf("stage after seal: %d %s", rr.Code, rr.Body)
	}
	if rr := h.do(http.MethodGet, "/v1/audits/"+A+"/index/flows/part-2.parquet", h.ownerK, nil); rr.Code != http.StatusNotFound {
		t.Fatalf("unsealed part served: %d", rr.Code)
	}

	// Admin delete of the audit removes the files with the row.
	actx := context.WithValue(h.ctx, principalKey{}, auth.Principal{UserID: h.admin.ID, Role: "admin", Via: auth.ViaSession})
	if _, err := h.d.deleteAudit(actx, A, "test"); err != nil {
		t.Fatalf("delete audit: %v", err)
	}
	if exists(h.indexPath(A)) {
		t.Fatal("index directory survived the audit")
	}
	if _, err := h.s.GetIndex(h.ctx, A); err == nil {
		t.Fatal("index row survived the audit")
	}
	rr = h.do(http.MethodGet, "/v1/atlas/index", h.ownerK, nil)
	if rr.Code != http.StatusOK || len(decode[AtlasIndex](t, rr).Audits) != 0 {
		t.Fatalf("atlas after delete: %d %s", rr.Code, rr.Body)
	}
}

// The seal refuses what the manifest validation refuses, before touching
// the staging area, and the staging drop is idempotent.
func TestIndexSealValidation(t *testing.T) {
	h := newIndexHarness(t)
	A := h.a.ID
	raw := parquet("one small part")
	if rr := h.put(h.tokA, A, "calls", "part-1.parquet", raw); rr.Code != http.StatusOK {
		t.Fatalf("put: %d %s", rr.Code, rr.Body)
	}
	for name, m := range map[string]core.IndexManifest{
		"bad table":      {Tool: "zz", Tables: []core.IndexTable{{Name: "Calls", Parts: []core.IndexPart{part("part-1.parquet", raw)}}}},
		"bad file":       {Tool: "zz", Tables: []core.IndexTable{{Name: "calls", Parts: []core.IndexPart{part("part-1.csv", raw)}}}},
		"duplicate part": {Tool: "zz", Tables: []core.IndexTable{{Name: "calls", Parts: []core.IndexPart{part("part-1.parquet", raw), part("part-1.parquet", raw)}}}},
		"bytes differ":   {Tool: "zz", Tables: []core.IndexTable{{Name: "calls", Parts: []core.IndexPart{{File: "part-1.parquet", Bytes: 3, SHA256: digest(raw)}}}}},
	} {
		if rr := h.seal(h.tokA, A, m); rr.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s: %d %s", name, rr.Code, rr.Body)
		}
	}
	// No tables at all is a schema error.
	if rr := h.seal(h.tokA, A, core.IndexManifest{Tool: "zz"}); rr.Code != http.StatusUnprocessableEntity {
		t.Errorf("no tables: %d %s", rr.Code, rr.Body)
	}
	if rr := h.seal(h.tokB, A, core.IndexManifest{Tool: "zz", Tables: []core.IndexTable{{Name: "calls", Parts: []core.IndexPart{part("part-1.parquet", raw)}}}}); rr.Code != http.StatusForbidden {
		t.Errorf("other job's token on seal: %d", rr.Code)
	}
	if !exists(h.indexPath(A, ".staging", "calls", "part-1.parquet")) {
		t.Fatal("a refused seal touched staging")
	}
	for range 2 {
		if rr := h.do(http.MethodDelete, "/v1/op/jobs/"+A+"/index/staging", h.tokA, nil); rr.Code >= 300 {
			t.Fatalf("drop staging: %d %s", rr.Code, rr.Body)
		}
	}
	if exists(h.indexPath(A, ".staging")) {
		t.Fatal("staging survived the drop")
	}
	if rr := h.seal(h.tokA, A, core.IndexManifest{Tool: "zz", Tables: []core.IndexTable{{Name: "calls", Parts: []core.IndexPart{part("part-1.parquet", raw)}}}}); rr.Code != http.StatusUnprocessableEntity {
		t.Errorf("seal after drop: %d %s", rr.Code, rr.Body)
	}
}
