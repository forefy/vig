package solana

import (
	"regexp"
	"testing"

	"github.com/forefy/vig/backend/internal/core"
)

var _ core.Adapter = (*Adapter)(nil)

func TestStepsAndPanel(t *testing.T) {
	steps := New().IntakeSteps()
	if len(steps) != 5 {
		t.Fatalf("want 5 steps, got %d", len(steps))
	}
	if p := EmptyPanel("x"); len(p.Rows) != 11 {
		t.Fatalf("want 11 panel rows, got %d", len(p.Rows))
	}
}

func TestPatterns(t *testing.T) {
	pid := regexp.MustCompile(programIDPattern)
	if !pid.MatchString("TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA") {
		t.Fatal("token program id should match")
	}
	if pid.MatchString("0OIl") {
		t.Fatal("non base58 chars must not match")
	}
	repo := regexp.MustCompile(repoURLPattern)
	for _, ok := range []string{"https://github.com/vig-demo/vault-programs", "https://github.com/vig-demo/vault-programs/tree/main/programs/core"} {
		if !repo.MatchString(ok) {
			t.Fatalf("%s should match", ok)
		}
	}
	if repo.MatchString("https://gitlab.com/a/b") {
		t.Fatal("other hosts must not match")
	}
}
