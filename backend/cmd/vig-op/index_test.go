package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestIndexPartsAndManifest(t *testing.T) {
	root := t.TempDir()
	write := func(rel string, b []byte) {
		t.Helper()
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	calls2, calls1, repos := []byte("PAR1 calls two PAR1"), []byte("PAR1 calls one, longer PAR1"), []byte("PAR1 repos PAR1")
	write("index/calls/target=acme/part-2.parquet", calls2)
	write("index/calls/target=acme/part-1.parquet", calls1)
	write("index/calls/target=acme/_SUCCESS", []byte("x"))   // not a parquet file
	write("index/calls/target=acme/empty.parquet", nil)      // empty: skipped
	write("index/calls/target=other/part-9.parquet", calls1) // another target
	write("index/repos/target=acme/part-1.parquet", repos)
	write("index/flows/target=nope/part-1.parquet", repos) // table with no parts for the target
	write("index/Bad-Table/target=acme/part-1.parquet", repos)
	write("index/notes.txt", []byte("x")) // a file where tables live

	parts, err := indexParts(root, "acme")
	if err != nil {
		t.Fatal(err)
	}
	if len(parts) != 3 {
		t.Fatalf("parts: %+v", parts)
	}
	want := []struct {
		table, file string
		raw         []byte
	}{{"calls", "part-1.parquet", calls1}, {"calls", "part-2.parquet", calls2}, {"repos", "part-1.parquet", repos}}
	for i, w := range want {
		sum := sha256.Sum256(w.raw)
		p := parts[i]
		if p.Table != w.table || p.File != w.file || p.Bytes != int64(len(w.raw)) || p.SHA256 != hex.EncodeToString(sum[:]) || p.Path != filepath.Join(root, "index", w.table, "target=acme", w.file) {
			t.Errorf("part %d: %+v", i, p)
		}
	}

	m := indexManifest(parts, "vig", "0.3.1")
	if m.Tool != "vig" || m.ToolVersion != "0.3.1" || len(m.Tables) != 2 {
		t.Fatalf("manifest: %+v", m)
	}
	if m.Tables[0].Name != "calls" || len(m.Tables[0].Parts) != 2 || m.Tables[0].Bytes != int64(len(calls1)+len(calls2)) || m.Tables[0].Rows != 0 {
		t.Fatalf("calls: %+v", m.Tables[0])
	}
	if m.Tables[1].Name != "repos" || len(m.Tables[1].Parts) != 1 || m.Tables[1].Parts[0].File != "part-1.parquet" {
		t.Fatalf("repos: %+v", m.Tables[1])
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("manifest the server would refuse: %v", err)
	}
	if m.TotalBytes() != int64(len(calls1)+len(calls2)+len(repos)) {
		t.Fatalf("total %d", m.TotalBytes())
	}

	// No root, or a target nothing was built for: no parts, no error.
	for _, c := range [][2]string{{root, "missing"}, {filepath.Join(root, "nowhere"), "acme"}} {
		parts, err := indexParts(c[0], c[1])
		if err != nil || len(parts) != 0 {
			t.Errorf("%v: %d parts, %v", c, len(parts), err)
		}
	}
}
