package og

import (
	"bytes"
	"image/png"
	"os"
	"testing"
)

func decode(t *testing.T, b []byte) {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	if got := img.Bounds().Size(); got.X != Width || got.Y != Height {
		t.Fatalf("size %v, want %dx%d", got, Width, Height)
	}
}

func TestDefault(t *testing.T) {
	b, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	decode(t, b)
	if dir := os.Getenv("OG_DUMP_DIR"); dir != "" {
		_ = os.WriteFile(dir+"/default.png", b, 0o644)
	}
}

func TestRender(t *testing.T) {
	b, err := Render(Report{
		Repo: "vig-demo/vault-programs", Commit: "3f2a9c1d7e8b4a6f0c2d1e9b8a7f6e5d4c3b2a10",
		Programs: 2, Match: "mismatch", Critical: 1, High: 2, Medium: 4, Low: 3, Info: 7,
	})
	if err != nil {
		t.Fatal(err)
	}
	decode(t, b)
	if dir := os.Getenv("OG_DUMP_DIR"); dir != "" {
		_ = os.WriteFile(dir+"/report.png", b, 0o644)
	}
}

func TestRenderLongRepoIsTruncated(t *testing.T) {
	b, err := Render(Report{Repo: "an-extremely-long-organisation-name/an-even-longer-repository-name-that-cannot-fit", Match: "verified"})
	if err != nil {
		t.Fatal(err)
	}
	decode(t, b)
}

func TestWorstMatch(t *testing.T) {
	cases := map[string][]string{
		"unverified": nil,
		"verified":   {"verified", "verified"},
		"self_built": {"verified", "self_built"},
		"mismatch":   {"verified", "mismatch", "unverified"},
	}
	for want, in := range cases {
		if got := WorstMatch(in); got != want {
			t.Errorf("WorstMatch(%v) = %s, want %s", in, got, want)
		}
	}
}
