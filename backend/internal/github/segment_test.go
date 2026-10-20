package github

import (
	"os"
	"path/filepath"
	"testing"
)

const rustSample = `use anchor_lang::prelude::*;

declare_id!("Fg6PaFpoGXkYsidMpWTK6W2BeZ7FEfcYkg476zPFsLnS");

#[program]
pub mod signer_authorization_insecure {
    use super::*;

    pub fn log_message(ctx: Context<LogMessage>) -> ProgramResult {
        msg!("GM {}", ctx.accounts.authority.key().to_string());
        Ok(())
    }
}

#[derive(Accounts)]
pub struct LogMessage<'info> {
    authority: AccountInfo<'info>,
}
`

func TestSegmentFindsEnclosingFunction(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "lib.rs"), []byte(rustSample), 0o644)
	from, code, ok := Segment(dir, "lib.rs", 10, 10)
	if !ok {
		t.Fatal("segment not found")
	}
	if from != 9 {
		t.Fatalf("segment starts at %d, want 9 (the fn line)", from)
	}
	if code[len(code)-1] != '}' {
		t.Fatalf("segment must end at the fn's closing brace, got %q", code)
	}
	// The struct: attributes are part of the item.
	from, code, ok = Segment(dir, "lib.rs", 18, 18)
	if !ok || from != 15 {
		t.Fatalf("struct segment from %d ok=%v, want 15 (the #[derive] line)", from, ok)
	}
	if code[len(code)-1] != '}' {
		t.Fatalf("struct segment must close, got %q", code)
	}
}
