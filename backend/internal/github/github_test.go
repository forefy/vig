package github

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestParseURL(t *testing.T) {
	cases := []struct {
		in      string
		want    Ref
		wantErr bool
	}{
		{"https://github.com/someorg/somerepo", Ref{Owner: "someorg", Name: "somerepo"}, false},
		{"https://github.com/someorg/somerepo/", Ref{Owner: "someorg", Name: "somerepo"}, false},
		{"https://github.com/someorg/somerepo.git", Ref{Owner: "someorg", Name: "somerepo"}, false},
		{"http://github.com/someorg/somerepo", Ref{Owner: "someorg", Name: "somerepo"}, false},
		{"https://www.github.com/someorg/somerepo", Ref{Owner: "someorg", Name: "somerepo"}, false},
		{"  https://github.com/someorg/somerepo \n", Ref{Owner: "someorg", Name: "somerepo"}, false},
		{"https://github.com/o/dotted.name", Ref{Owner: "o", Name: "dotted.name"}, false},
		{"https://github.com/o/name.git.git", Ref{Owner: "o", Name: "name.git"}, false},
		{"https://github.com/someorg/somerepo/tree/master", Ref{Owner: "someorg", Name: "somerepo", Rev: "master"}, false},
		{"https://github.com/someorg/somerepo/tree/v0.30.1/", Ref{Owner: "someorg", Name: "somerepo", Rev: "v0.30.1"}, false},
		{"https://github.com/someorg/somerepo/tree/master/lang/syn", Ref{Owner: "someorg", Name: "somerepo", Rev: "master", Path: "lang/syn"}, false},
		{"https://github.com/someorg/somerepo/tree/master/lang/syn/", Ref{Owner: "someorg", Name: "somerepo", Rev: "master", Path: "lang/syn"}, false},
		{"https://github.com/o/n/tree/9c1e4d7a2b5f8e0c3a6d1b4f7e2a5c8d0b3f6e9a/programs/x", Ref{Owner: "o", Name: "n", Rev: "9c1e4d7a2b5f8e0c3a6d1b4f7e2a5c8d0b3f6e9a", Path: "programs/x"}, false},
		{"", Ref{}, true},
		{"github.com/o/n", Ref{}, true},
		{"https://gitlab.com/o/n", Ref{}, true},
		{"https://github.com/o", Ref{}, true},
		{"https://github.com/o/n/blob/main/src/lib.rs", Ref{}, true},
		{"https://github.com/o/n/pull/12", Ref{}, true},
		{"https://github.com/o/n/tree", Ref{}, true},
		{"https://github.com/o/n/tree/", Ref{}, true},
		{"https://github.com/o/n?tab=readme", Ref{}, true},
		{"https://github.com/o/n#readme", Ref{}, true},
		{"https://github.com/o/n a", Ref{}, true},
		{"https://evil.com/https://github.com/o/n", Ref{}, true},
		{"https://github.com.evil.com/o/n", Ref{}, true},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			got, err := ParseURL(c.in)
			if (err != nil) != c.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, c.wantErr)
			}
			if got != c.want {
				t.Fatalf("got %+v want %+v", got, c.want)
			}
		})
	}
	if r, _ := ParseURL("https://github.com/o/n"); r.FullName() != "o/n" {
		t.Fatal("FullName")
	}
}

func TestParseFullName(t *testing.T) {
	cases := []struct {
		in      string
		want    Ref
		wantErr bool
	}{
		{"o/n", Ref{Owner: "o", Name: "n"}, false},
		{" o/n ", Ref{Owner: "o", Name: "n"}, false},
		{"o", Ref{}, true},
		{"/n", Ref{}, true},
		{"o/", Ref{}, true},
		{"o/n/x", Ref{}, true},
		{"", Ref{}, true},
	}
	for _, c := range cases {
		got, err := ParseFullName(c.in)
		if (err != nil) != c.wantErr || got != c.want {
			t.Errorf("ParseFullName(%q) = %+v, %v; want %+v wantErr=%v", c.in, got, err, c.want, c.wantErr)
		}
	}
}

// tarEntry describes one entry of a synthetic GitHub tarball.
type tarEntry struct {
	name string
	typ  byte
	body []byte
	link string
	size int64 // when non-zero and body is nil, that many zero bytes are written
}

func makeTarball(t *testing.T, entries []tarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		h := &tar.Header{Name: e.name, Typeflag: e.typ, Mode: 0o644, ModTime: time.Unix(0, 0)}
		switch e.typ {
		case tar.TypeDir:
			h.Mode = 0o755
		case tar.TypeSymlink:
			h.Linkname = e.link
		case tar.TypeReg:
			h.Size = int64(len(e.body))
			if e.body == nil && e.size > 0 {
				h.Size = e.size
			}
		case tar.TypeXGlobalHeader:
			h.Mode = 0
			h.ModTime = time.Time{}
			h.PAXRecords = map[string]string{"comment": "abc123"}
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if e.typ == tar.TypeReg {
			if e.body != nil {
				tw.Write(e.body)
			} else if e.size > 0 {
				if _, err := io.CopyN(tw, zeroReader{}, e.size); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 0
	}
	return len(p), nil
}

func TestExtract(t *testing.T) {
	root := t.TempDir()
	dest := filepath.Join(root, "dest")
	tb := makeTarball(t, []tarEntry{
		{name: "pax_global_header", typ: tar.TypeXGlobalHeader},
		{name: "owner-repo-abc123/", typ: tar.TypeDir},
		{name: "owner-repo-abc123/src/", typ: tar.TypeDir},
		{name: "owner-repo-abc123/src/lib.rs", typ: tar.TypeReg, body: []byte("fn main() {}\n")},
		{name: "owner-repo-abc123/deep/nested/no-dir-entry.txt", typ: tar.TypeReg, body: []byte("x")},
		{name: "owner-repo-abc123/../escape1.txt", typ: tar.TypeReg, body: []byte("bad")},
		{name: "owner-repo-abc123/src/../../../escape2.txt", typ: tar.TypeReg, body: []byte("bad")},
		{name: "owner-repo-abc123/../../escape3.txt", typ: tar.TypeReg, body: []byte("bad")},
		{name: "owner-repo-abc123//abs/escape4.txt", typ: tar.TypeReg, body: []byte("abs is joined under dest")},
		{name: "owner-repo-abc123/link", typ: tar.TypeSymlink, link: "/etc/passwd"},
		{name: "owner-repo-abc123/dirlink", typ: tar.TypeSymlink, link: ".."},
		{name: "owner-repo-abc123/hard", typ: tar.TypeLink, link: "owner-repo-abc123/src/lib.rs"},
		{name: "owner-repo-abc123/big.bin", typ: tar.TypeReg, size: 16<<20 + 1},
		{name: "owner-repo-abc123/after-big.txt", typ: tar.TypeReg, body: []byte("still extracted")},
		{name: "owner-repo-abc123", typ: tar.TypeDir}, // bare top-level, no slash
	})
	if err := extract(bytes.NewReader(tb), dest); err != nil {
		t.Fatal(err)
	}

	mustFile := func(rel, want string) {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(dest, rel))
		if err != nil {
			t.Fatalf("%s: %v", rel, err)
		}
		if string(b) != want {
			t.Fatalf("%s = %q want %q", rel, b, want)
		}
	}
	mustAbsent := func(p string) {
		t.Helper()
		if _, err := os.Lstat(p); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s must not exist (err=%v)", p, err)
		}
	}
	mustFile("src/lib.rs", "fn main() {}\n")
	mustFile("deep/nested/no-dir-entry.txt", "x")
	mustFile("after-big.txt", "still extracted")
	mustFile("abs/escape4.txt", "abs is joined under dest")
	mustAbsent(filepath.Join(dest, "big.bin"))
	mustAbsent(filepath.Join(dest, "link"))
	mustAbsent(filepath.Join(dest, "dirlink"))
	mustAbsent(filepath.Join(dest, "hard"))
	mustAbsent(filepath.Join(dest, "..", "escape1.txt"))
	for _, name := range []string{"escape1.txt", "escape2.txt", "escape3.txt"} {
		mustAbsent(filepath.Join(root, name))
		mustAbsent(filepath.Join(root, "..", name))
		mustAbsent(filepath.Join(dest, name))
	}
	// Nothing outside dest was written at all.
	entries, _ := os.ReadDir(root)
	if len(entries) != 1 || entries[0].Name() != "dest" {
		t.Fatalf("temp root polluted: %v", entries)
	}
}

func TestExtractRejectsNonGzip(t *testing.T) {
	if err := extract(strings.NewReader("definitely not gzip"), t.TempDir()); err == nil {
		t.Fatal("expected gzip error")
	}
}

func TestExtractTruncatedArchive(t *testing.T) {
	tb := makeTarball(t, []tarEntry{{name: "r/a.txt", typ: tar.TypeReg, body: bytes.Repeat([]byte("a"), 4096)}})
	// Cut the gzip stream so the tar reader hits an unexpected EOF.
	if err := extract(bytes.NewReader(tb[:len(tb)/2]), t.TempDir()); err == nil {
		t.Fatal("expected an error for a truncated archive")
	}
}

// ghServer fakes the parts of the REST API the client uses.
type ghServer struct {
	tarball   []byte
	tarCalls  atomic.Int32
	lastAuth  atomic.Value
	lastAgent atomic.Value
}

func (g *ghServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	g.lastAuth.Store(r.Header.Get("Authorization"))
	g.lastAgent.Store(r.Header.Get("User-Agent"))
	write := func(v any) { w.Header().Set("Content-Type", "application/json"); json.NewEncoder(w).Encode(v) }
	switch r.URL.Path {
	case "/repos/o/n":
		write(map[string]any{"default_branch": "trunk", "full_name": "o/n"})
	case "/repos/o/n/commits/trunk", "/repos/o/n/commits/v1.0.0", "/repos/o/n/commits/abc1234":
		write(map[string]any{"sha": "abc1234abc1234abc1234abc1234abc1234abc12", "commit": map[string]any{"author": map[string]any{"name": "Ada", "date": "2026-01-02T03:04:05Z"}}})
	case "/repos/o/n/commits/nope":
		w.WriteHeader(http.StatusUnprocessableEntity)
		write(map[string]any{"message": "No commit found for SHA: nope"})
	case "/repos/o/n/commits/empty":
		write(map[string]any{})
	case "/repos/o/missing", "/repos/o/missing/commits/main":
		w.WriteHeader(http.StatusNotFound)
	case "/repos/o/limited":
		w.WriteHeader(http.StatusForbidden)
		write(map[string]any{"message": "API rate limit exceeded"})
	case "/repos/o/toomany":
		w.WriteHeader(http.StatusTooManyRequests)
	case "/repos/o/broken":
		w.WriteHeader(http.StatusInternalServerError)
	case "/repos/o/garbage":
		w.Write([]byte("{not json"))
	case "/repos/o/n/tarball/abc1234abc1234abc1234abc1234abc1234abc12":
		g.tarCalls.Add(1)
		w.Header().Set("Content-Type", "application/gzip")
		w.Write(g.tarball)
	case "/repos/o/n/tarball/missing":
		w.WriteHeader(http.StatusNotFound)
	case "/repos/o/n/tarball/broken":
		w.WriteHeader(http.StatusBadGateway)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func TestDefaultBranchAndResolve(t *testing.T) {
	g := &ghServer{}
	srv := httptest.NewServer(g)
	defer srv.Close()
	c := New(srv.URL+"/", "tok", t.TempDir())
	ctx := context.Background()

	b, err := c.DefaultBranch(ctx, Ref{Owner: "o", Name: "n"})
	if err != nil || b != "trunk" {
		t.Fatalf("branch %q err %v", b, err)
	}
	if g.lastAuth.Load() != "Bearer tok" || g.lastAgent.Load() != "vig" {
		t.Fatalf("headers auth=%v agent=%v", g.lastAuth.Load(), g.lastAgent.Load())
	}

	commit, err := c.Resolve(ctx, Ref{Owner: "o", Name: "n"}, "")
	if err != nil {
		t.Fatal(err)
	}
	want := Commit{SHA: "abc1234abc1234abc1234abc1234abc1234abc12", Author: "Ada", Date: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)}
	if commit.SHA != want.SHA || commit.Author != want.Author || !commit.Date.Equal(want.Date) {
		t.Fatalf("resolve default: %+v", commit)
	}
	for _, rev := range []string{"v1.0.0", "abc1234"} {
		if got, err := c.Resolve(ctx, Ref{Owner: "o", Name: "n"}, rev); err != nil || got.SHA != want.SHA {
			t.Fatalf("resolve %s: %+v %v", rev, got, err)
		}
	}

	errCases := []struct {
		name string
		ref  Ref
		rev  string
		want error
	}{
		{"unknown ref 422", Ref{Owner: "o", Name: "n"}, "nope", ErrNotFound},
		{"missing repo 404", Ref{Owner: "o", Name: "missing"}, "main", ErrNotFound},
		{"missing repo default branch", Ref{Owner: "o", Name: "missing"}, "", ErrNotFound},
		{"empty sha in body", Ref{Owner: "o", Name: "n"}, "empty", ErrNotFound},
		{"rate limited 403", Ref{Owner: "o", Name: "limited"}, "", ErrRateLimited},
		{"rate limited 429", Ref{Owner: "o", Name: "toomany"}, "", ErrRateLimited},
	}
	for _, c2 := range errCases {
		t.Run(c2.name, func(t *testing.T) {
			_, err := c.Resolve(ctx, c2.ref, c2.rev)
			if !errors.Is(err, c2.want) {
				t.Fatalf("got %v want %v", err, c2.want)
			}
		})
	}
	if _, err := c.DefaultBranch(ctx, Ref{Owner: "o", Name: "broken"}); err == nil || errors.Is(err, ErrNotFound) || errors.Is(err, ErrRateLimited) || !strings.Contains(err.Error(), "500") {
		t.Fatalf("500 must be a generic error, got %v", err)
	}
	if _, err := c.DefaultBranch(ctx, Ref{Owner: "o", Name: "garbage"}); err == nil {
		t.Fatal("bad json must error")
	}

	// No token means no Authorization header.
	c.Token = ""
	c.DefaultBranch(ctx, Ref{Owner: "o", Name: "n"})
	if g.lastAuth.Load() != "" {
		t.Fatalf("no token must send no auth header, got %v", g.lastAuth.Load())
	}
}

func TestCheckoutCachesTarball(t *testing.T) {
	g := &ghServer{tarball: makeTarball(t, []tarEntry{
		{name: "o-n-abc1234/", typ: tar.TypeDir},
		{name: "o-n-abc1234/Cargo.toml", typ: tar.TypeReg, body: []byte("[package]\n")},
		{name: "o-n-abc1234/programs/x/src/lib.rs", typ: tar.TypeReg, body: []byte("declare_id!(\"x\");\n")},
	})}
	srv := httptest.NewServer(g)
	defer srv.Close()
	data := t.TempDir()
	c := New(srv.URL, "", data)
	ctx := context.Background()
	sha := "abc1234abc1234abc1234abc1234abc1234abc12"

	dir, err := c.Checkout(ctx, "o/n", sha)
	if err != nil {
		t.Fatal(err)
	}
	if dir != c.CloneDir("o/n", sha) || dir != filepath.Join(data, "clones", "o__n__"+sha) {
		t.Fatalf("clone dir %s", dir)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "programs", "x", "src", "lib.rs")); err != nil || !strings.HasPrefix(string(b), "declare_id!") {
		t.Fatalf("extracted file: %q %v", b, err)
	}
	if b, err := os.ReadFile(filepath.Join(dir, ".ok")); err != nil || string(b) != sha {
		t.Fatalf(".ok marker: %q %v", b, err)
	}
	if _, err := os.Stat(dir + ".tmp"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("tmp dir must be renamed away")
	}
	if _, err := c.Checkout(ctx, "o/n", sha); err != nil {
		t.Fatal(err)
	}
	if g.tarCalls.Load() != 1 {
		t.Fatalf("second checkout must reuse the cache, tarball fetched %d times", g.tarCalls.Load())
	}

	if _, err := c.Checkout(ctx, "o/n", "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("404 tarball: %v", err)
	}
	if _, err := c.Checkout(ctx, "o/n", "broken"); err == nil || errors.Is(err, ErrNotFound) {
		t.Fatalf("502 tarball: %v", err)
	}
	if _, err := os.Stat(c.CloneDir("o/n", "broken") + ".tmp"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("failed checkout must not leave a tmp dir")
	}

	if err := c.RemoveClone("o/n", sha); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("RemoveClone must delete the checkout")
	}
}

func TestCleanup(t *testing.T) {
	data := t.TempDir()
	c := New("http://unused", "", data)
	clones := filepath.Join(data, "clones")
	mk := func(name string, ok bool, age time.Duration) string {
		p := filepath.Join(clones, name)
		os.MkdirAll(p, 0o755)
		if ok {
			marker := filepath.Join(p, ".ok")
			os.WriteFile(marker, []byte("sha"), 0o644)
			old := time.Now().Add(-age)
			os.Chtimes(marker, old, old)
		}
		return p
	}
	old := mk("o__n__old", true, 48*time.Hour)
	fresh := mk("o__n__fresh", true, time.Hour)
	tmp := mk("o__n__half.tmp", false, 0)
	noMarker := mk("o__n__nomarker", false, 0)

	if n := c.Cleanup(24 * time.Hour); n != 2 {
		t.Fatalf("removed %d want 2", n)
	}
	for _, p := range []string{old, tmp} {
		if _, err := os.Stat(p); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s should be removed", p)
		}
	}
	for _, p := range []string{fresh, noMarker} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("%s should survive", p)
		}
	}
	if n := New("http://unused", "", filepath.Join(data, "nope")).Cleanup(time.Hour); n != 0 {
		t.Fatal("missing clones dir is not an error")
	}
}
