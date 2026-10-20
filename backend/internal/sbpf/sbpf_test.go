package sbpf

import (
	"os"
	"testing"
)

func TestMurmur3KnownVectors(t *testing.T) {
	// Reference values from the MurmurHash3 x86_32 test suite.
	cases := map[string]uint32{"": 0, "a": 0x3c2569b2, "abc": 0xb3dd93fa, "Hello, world!": 0xc0363e43}
	for in, want := range cases {
		if got := murmur3([]byte(in), 0); got != want {
			t.Errorf("murmur3(%q) = %#x, want %#x", in, got, want)
		}
	}
}

func TestRejectsNonELF(t *testing.T) {
	if _, err := Analyze([]byte("hello")); err != ErrNotELF {
		t.Fatalf("want ErrNotELF, got %v", err)
	}
}

func TestSPLToken(t *testing.T) {
	b, err := os.ReadFile("testdata/spl_token.so")
	if err != nil {
		t.Skip("fixture missing")
	}
	p, err := Analyze(b)
	if err != nil {
		t.Fatal(err)
	}
	if p.Instructions < 1000 || p.Functions < 10 {
		t.Errorf("implausible code stats: %+v", p)
	}
	if len(p.Syscalls) == 0 {
		t.Error("no syscalls recognised")
	}
	has := func(list []string, want string) bool {
		for _, s := range list {
			if s == want {
				return true
			}
		}
		return false
	}
	if !has(p.Capabilities, "logging") {
		t.Errorf("token program logs; capabilities %v syscalls %v", p.Capabilities, p.Syscalls)
	}
	if p.Framework != "native" {
		t.Errorf("framework %q, want native", p.Framework)
	}
	t.Logf("spl_token: %d ix, %d fns, syscalls %v, caps %v, crates %v, paths %v", p.Instructions, p.Functions, p.Syscalls, p.Capabilities, p.Crates, p.SourcePaths)
}

func TestAnchorProgram(t *testing.T) {
	b, err := os.ReadFile("testdata/anchor_large.so")
	if err != nil {
		t.Skip("large fixture not present locally")
	}
	p, err := Analyze(b)
	if err != nil {
		t.Fatal(err)
	}
	if p.Framework != "anchor" {
		t.Errorf("framework %q, want anchor (crates %v, ixs %v)", p.Framework, p.Crates, p.AnchorIxs)
	}
	if len(p.AnchorIxs) == 0 {
		t.Error("no Anchor instruction names recovered")
	}
	t.Logf("anchor_large: %d ix, %d fns, syscalls %v, caps %v, crates %v, anchor ixs %v, accounts %v, rustc %s, paths %v", p.Instructions, p.Functions, p.Syscalls, p.Capabilities, p.Crates, p.AnchorIxs, p.AnchorAccts, p.Rustc, p.SourcePaths)
}
