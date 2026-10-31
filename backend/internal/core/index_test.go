package core

import (
	"strings"
	"testing"
)

func TestIndexManifestValidate(t *testing.T) {
	sum := strings.Repeat("ab", 32)
	part := func(file string, n int64) IndexPart { return IndexPart{File: file, Bytes: n, SHA256: sum} }
	for _, c := range []struct {
		name string
		m    IndexManifest
		want string // substring of the error; empty means valid
	}{
		{"ok", IndexManifest{Tool: "zz", Tables: []IndexTable{{Name: "calls", Parts: []IndexPart{part("a.parquet", 10), part("b.parquet", 32)}}}}, ""},
		{"no tables", IndexManifest{Tool: "zz"}, "at least one table"},
		{"bad table name", IndexManifest{Tables: []IndexTable{{Name: "Calls", Parts: []IndexPart{part("a.parquet", 1)}}}}, "tables[0].name"},
		{"table with a separator", IndexManifest{Tables: []IndexTable{{Name: "a/b", Parts: []IndexPart{part("a.parquet", 1)}}}}, "tables[0].name"},
		{"repeated table", IndexManifest{Tables: []IndexTable{{Name: "calls", Parts: []IndexPart{part("a.parquet", 1)}}, {Name: "calls", Parts: []IndexPart{part("b.parquet", 1)}}}}, "repeats"},
		{"no parts", IndexManifest{Tables: []IndexTable{{Name: "calls"}}}, "no parts"},
		{"bad file extension", IndexManifest{Tables: []IndexTable{{Name: "calls", Parts: []IndexPart{part("a.csv", 1)}}}}, "parts[0].file"},
		{"file with a path", IndexManifest{Tables: []IndexTable{{Name: "calls", Parts: []IndexPart{part("../a.parquet", 1)}}}}, "parts[0].file"},
		{"file with dot dot", IndexManifest{Tables: []IndexTable{{Name: "calls", Parts: []IndexPart{part("a..parquet", 1)}}}}, "parts[0].file"},
		{"duplicate part", IndexManifest{Tables: []IndexTable{{Name: "calls", Parts: []IndexPart{part("a.parquet", 1), part("a.parquet", 2)}}}}, "parts[1].file"},
		{"bad digest", IndexManifest{Tables: []IndexTable{{Name: "calls", Parts: []IndexPart{{File: "a.parquet", Bytes: 1, SHA256: strings.ToUpper(sum)}}}}}, "sha256"},
		{"short digest", IndexManifest{Tables: []IndexTable{{Name: "calls", Parts: []IndexPart{{File: "a.parquet", Bytes: 1, SHA256: "abcd"}}}}}, "sha256"},
		{"zero bytes", IndexManifest{Tables: []IndexTable{{Name: "calls", Parts: []IndexPart{part("a.parquet", 0)}}}}, "bytes must be positive"},
	} {
		err := c.m.Validate()
		switch {
		case c.want == "" && err != nil:
			t.Errorf("%s: unexpected %v", c.name, err)
		case c.want != "" && err == nil:
			t.Errorf("%s: want error containing %q, got nil", c.name, c.want)
		case c.want != "" && !strings.Contains(err.Error(), c.want):
			t.Errorf("%s: want error containing %q, got %v", c.name, c.want, err)
		}
	}
}

// Validate sums each table's bytes from its parts, whatever the caller
// wrote, and TotalBytes sums every table.
func TestIndexManifestBytesSum(t *testing.T) {
	sum := strings.Repeat("00", 32)
	m := IndexManifest{Tool: "zz", Tables: []IndexTable{
		{Name: "calls", Bytes: 999, Parts: []IndexPart{{File: "a.parquet", Bytes: 10, SHA256: sum}, {File: "b.parquet", Bytes: 32, SHA256: sum}}},
		{Name: "repos", Parts: []IndexPart{{File: "c.parquet", Bytes: 8, SHA256: sum}}},
	}}
	if err := m.Validate(); err != nil {
		t.Fatal(err)
	}
	if m.Tables[0].Bytes != 42 || m.Tables[1].Bytes != 8 {
		t.Fatalf("table bytes %d %d, want 42 8", m.Tables[0].Bytes, m.Tables[1].Bytes)
	}
	if m.TotalBytes() != 50 {
		t.Fatalf("total %d, want 50", m.TotalBytes())
	}
}

func TestIndexNames(t *testing.T) {
	for name, ok := range map[string]bool{"calls": true, "call_edges2": true, "Calls": false, "2x": false, "": false, "a-b": false} {
		if ValidIndexTable(name) != ok {
			t.Errorf("table %q: want %v", name, ok)
		}
	}
	for name, ok := range map[string]bool{"part-1.parquet": true, "A.b_c.parquet": true, ".hidden.parquet": false, "x.csv": false, "a/b.parquet": false, "a..parquet": false, "parquet": false} {
		if ValidIndexFile(name) != ok {
			t.Errorf("file %q: want %v", name, ok)
		}
	}
	if !ValidSHA256(strings.Repeat("a", 64)) || ValidSHA256(strings.Repeat("A", 64)) || ValidSHA256("abc") {
		t.Error("sha256 check")
	}
}
