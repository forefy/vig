package scan

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const advisoryPatched = "```toml\n" +
	"[advisory]\n" +
	"id = \"RUSTSEC-2022-0093\"\n" +
	"package = \"ed25519-dalek\"\n" +
	"date = \"2022-06-11\"\n" +
	"url = \"https://github.com/MystenLabs/ed25519-unsafe-libs\"\n" +
	"categories = [\"crypto-failure\"]\n" +
	"keywords = [\"signature\", \"oracle\"]\n" +
	"cvss = \"CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H\"\n" +
	"aliases = [\"CVE-2022-0000\"]\n" +
	"\n" +
	"[versions]\n" +
	"patched = [\">= 2.0.0\"]\n" +
	"unaffected = [\"< 1.0.0\"]\n" +
	"\n" +
	"[affected]\n" +
	"functions = { \"ed25519_dalek::Keypair::sign\" = [\"< 2.0.0\"] }\n" +
	"```\n" +
	"\n" +
	"# Double Public Key Signing Function Oracle Attack on `ed25519-dalek`\n" +
	"\n" +
	"Versions of `ed25519-dalek` prior to v2.0 model private and public keys as separate types.\n" +
	"\n" +
	"# Not the title\n"

const advisoryWithdrawn = "```toml\n" +
	"[advisory]\n" +
	"id = \"RUSTSEC-2021-0001\"\n" +
	"package = \"tokio-rustls\"\n" +
	"date = \"2021-01-01\"\n" +
	"informational = \"unmaintained\"\n" +
	"withdrawn = \"2021-03-01\"\n" +
	"\n" +
	"[versions]\n" +
	"patched = []\n" +
	"unaffected = [\n" +
	"    \">= 0.20.0\",\n" +
	"    \"< 0.10.0\",\n" +
	"]\n" +
	"```\n" +
	"\n" +
	"# tokio-rustls is unmaintained\n"

func TestParseAdvisory(t *testing.T) {
	a, ok := ParseAdvisory(advisoryPatched)
	if !ok {
		t.Fatal("advisory must parse")
	}
	want := Advisory{
		ID: "RUSTSEC-2022-0093", Package: "ed25519-dalek",
		Title:   "Double Public Key Signing Function Oracle Attack on `ed25519-dalek`",
		CVSS:    "CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H",
		Patched: []string{">= 2.0.0"}, Unaffected: []string{"< 1.0.0"},
	}
	if !reflect.DeepEqual(a, want) {
		t.Fatalf("\n got %+v\nwant %+v", a, want)
	}
	if a.Severity() != "critical" {
		t.Fatalf("severity %s", a.Severity())
	}
	for ver, affected := range map[string]bool{"1.0.0": true, "1.0.1": true, "1.9.9": true, "2.0.0-pre.0": true, "2.0.0": false, "3.1.0": false, "0.9.9": false, "0.0.1": false} {
		if got := a.Affects(v(ver)); got != affected {
			t.Errorf("Affects(%s)=%v want %v", ver, got, affected)
		}
	}

	w, ok := ParseAdvisory(advisoryWithdrawn)
	if !ok {
		t.Fatal("withdrawn advisory must still parse")
	}
	wantW := Advisory{
		ID: "RUSTSEC-2021-0001", Package: "tokio-rustls", Title: "tokio-rustls is unmaintained",
		Informational: "unmaintained", Withdrawn: true,
		Patched: []string{}, Unaffected: []string{">= 0.20.0", "< 0.10.0"},
	}
	if !reflect.DeepEqual(w, wantW) {
		t.Fatalf("\n got %+v\nwant %+v", w, wantW)
	}
	if w.Severity() != "info" {
		t.Fatalf("severity %s", w.Severity())
	}
	if !w.Affects(v("0.15.0")) || w.Affects(v("0.20.0")) || w.Affects(v("0.9.9")) {
		t.Fatal("unaffected ranges")
	}
	// Empty slices, not nil, so the JSON cache carries [] rather than null.
	b, _ := json.Marshal(w)
	if !strings.Contains(string(b), `"patched":[]`) {
		t.Fatalf("patched must marshal as []: %s", b)
	}
}

func TestParseAdvisoryRejects(t *testing.T) {
	cases := map[string]string{
		"no front matter":    "# Title\n\nbody\n",
		"unterminated toml":  "```toml\n[advisory]\nid = \"RUSTSEC-2020-0001\"\npackage = \"x\"\n",
		"missing id":         "```toml\n[advisory]\npackage = \"x\"\n```\n# t\n",
		"missing package":    "```toml\n[advisory]\nid = \"RUSTSEC-2020-0001\"\n```\n# t\n",
		"id outside section": "```toml\nid = \"RUSTSEC-2020-0001\"\npackage = \"x\"\n```\n# t\n",
	}
	for name, md := range cases {
		if a, ok := ParseAdvisory(md); ok {
			t.Errorf("%s: parsed %+v", name, a)
		}
	}
}

func TestParseAdvisoryNoVersionsSection(t *testing.T) {
	a, ok := ParseAdvisory("```toml\n[advisory]\nid = \"RUSTSEC-2020-0002\"\npackage = \"x\"\n```\n")
	if !ok || a.Title != "" || len(a.Patched) != 0 || len(a.Unaffected) != 0 || a.Patched == nil || a.Unaffected == nil {
		t.Fatalf("%+v %v", a, ok)
	}
	if !a.Affects(v("0.0.1")) || !a.Affects(v("99.0.0")) {
		t.Fatal("no patched range means every version is affected")
	}
}

func TestSplitSections(t *testing.T) {
	s := splitSections("[advisory]\nid = \"a\"\n\n[versions]\npatched = [\">= 1\"]\n[affected.functions]\nx = 1\n")
	if len(s) != 3 || !strings.Contains(s["advisory"], "id = \"a\"") || !strings.Contains(s["versions"], "patched") || !strings.Contains(s["affected.functions"], "x = 1") {
		t.Fatalf("%q", s)
	}
	if strings.Contains(s["advisory"], "patched") {
		t.Fatal("sections must not bleed")
	}
}

func advisoryArchive(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	tw.WriteHeader(&tar.Header{Name: "advisory-db-main/", Typeflag: tar.TypeDir, Mode: 0o755})
	tw.WriteHeader(&tar.Header{Name: "advisory-db-main/crates/", Typeflag: tar.TypeDir, Mode: 0o755})
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
		tw.Write([]byte(body))
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func TestParseArchive(t *testing.T) {
	archive := advisoryArchive(t, map[string]string{
		"advisory-db-main/crates/ed25519-dalek/RUSTSEC-2022-0093.md": advisoryPatched,
		"advisory-db-main/crates/tokio-rustls/RUSTSEC-2021-0001.md":  advisoryWithdrawn,
		"advisory-db-main/crates/broken/RUSTSEC-2020-0000.md":        "no front matter here\n",
		"advisory-db-main/crates/notes/README.md":                    "# not an advisory\n",
		"advisory-db-main/rust/RUSTSEC-2019-0001.md":                 advisoryPatched, // outside crates/
		"advisory-db-main/README.md":                                 advisoryPatched,
		"advisory-db-main/crates/ed25519-dalek/data.json":            "{}",
	})
	list, err := ParseArchive(bytes.NewReader(archive))
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, a := range list {
		ids[a.ID] = true
	}
	if len(list) != 2 || !ids["RUSTSEC-2022-0093"] || !ids["RUSTSEC-2021-0001"] {
		t.Fatalf("parsed %v", ids)
	}

	empty := advisoryArchive(t, map[string]string{"advisory-db-main/README.md": "x"})
	if _, err := ParseArchive(bytes.NewReader(empty)); err == nil {
		t.Fatal("archive without advisories must error")
	}
	if _, err := ParseArchive(strings.NewReader("not gzip")); err == nil {
		t.Fatal("non-gzip must error")
	}
	if _, err := ParseArchive(bytes.NewReader(archive[:len(archive)/2])); err == nil {
		t.Fatal("truncated archive must error")
	}
}

func TestMatch(t *testing.T) {
	patched, _ := ParseAdvisory(advisoryPatched)
	withdrawn, _ := ParseAdvisory(advisoryWithdrawn)
	info := Advisory{ID: "RUSTSEC-2023-0005", Package: "solana-program", Informational: "unsound", Patched: []string{">= 1.18.0"}, Unaffected: []string{}}
	advisories := []Advisory{patched, withdrawn, info}

	lock := `[[package]]
name = "ed25519-dalek"
version = "1.0.1"

[[package]]
name = "ed25519-dalek"
version = "2.1.0"

[[package]]
name = "tokio-rustls"
version = "0.15.0"

[[package]]
name = "solana-program"
version = "1.17.6"

[[package]]
name = "solana-program"
version = "1.18.0"

[[package]]
name = "anchor-lang"
version = "0.29.0"

[[package]]
name = "weird"
version = "not-a-version"
`
	got := Match(advisories, ParseCargoLock(lock))
	if len(got) != 2 {
		t.Fatalf("got %+v", got)
	}
	if got[0].ID != "RUSTSEC-2022-0093" || got[0].Package != "ed25519-dalek" || got[0].Version != "1.0.1" || got[0].Severity != "critical" || got[0].URL != patched.URL() {
		t.Fatalf("first match %+v", got[0])
	}
	if got[1].ID != "RUSTSEC-2023-0005" || got[1].Package != "solana-program" || got[1].Version != "1.17.6" || got[1].Severity != "info" {
		t.Fatalf("second match %+v", got[1])
	}
	if out := Match(advisories, nil); out == nil || len(out) != 0 {
		t.Fatal("no packages yields an empty, non-nil slice")
	}
	if out := Match(nil, ParseCargoLock(lock)); len(out) != 0 {
		t.Fatal("no advisories yields nothing")
	}
}

func TestDBAdvisoriesCachesAndServesStale(t *testing.T) {
	archive := advisoryArchive(t, map[string]string{"advisory-db-main/crates/x/RUSTSEC-2022-0093.md": advisoryPatched})
	var hits atomic.Int32
	var fail atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if fail.Load() {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.Write(archive)
	}))
	defer srv.Close()

	dir := t.TempDir()
	db := NewDB(dir)
	db.URL = srv.URL
	ctx := context.Background()

	list, err := db.Advisories(ctx)
	if err != nil || len(list) != 1 || list[0].ID != "RUSTSEC-2022-0093" {
		t.Fatalf("%v %+v", err, list)
	}
	if _, err := os.Stat(filepath.Join(dir, "advisories.json")); err != nil {
		t.Fatal("cache file must be written")
	}
	db.Advisories(ctx)
	if hits.Load() != 1 {
		t.Fatalf("in-memory copy must be served within TTL, hits=%d", hits.Load())
	}

	// A fresh DB reads the on-disk cache without downloading.
	db2 := NewDB(dir)
	db2.URL = srv.URL
	if l, err := db2.Advisories(ctx); err != nil || len(l) != 1 || hits.Load() != 1 {
		t.Fatalf("disk cache: err=%v len=%d hits=%d", err, len(l), hits.Load())
	}

	// Expired cache and a failing upstream: the stale copy is served.
	db3 := NewDB(dir)
	db3.URL = srv.URL
	db3.TTL = time.Nanosecond
	fail.Store(true)
	if l, err := db3.Advisories(ctx); err != nil || len(l) != 1 {
		t.Fatalf("stale fallback: err=%v len=%d", err, len(l))
	}
	if hits.Load() != 2 {
		t.Fatalf("an expired cache must attempt a download, hits=%d", hits.Load())
	}

	// No cache at all and a failing upstream is an error.
	db4 := NewDB(t.TempDir())
	db4.URL = srv.URL
	if _, err := db4.Advisories(ctx); err == nil {
		t.Fatal("no cache and no upstream must fail")
	}
}
