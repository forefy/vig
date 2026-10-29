package pdf

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/forefy/vig/backend/internal/core"
)

// The shape example is a whole report; a second finding adds what it does
// not carry: a re-rating, a status move, a link to the first finding, an
// attachment the renderer has to embed, and an attachment it only names.
func sample(t *testing.T) (Input, string) {
	t.Helper()
	doc := core.DescribeShape(core.Pin{Repo: "vig-demo/vault-programs", Commit: "3f2a9c1d7e8b4a6f0c2d1e9b8a7f6e5d4c3b2a10", Programs: []core.ProgramPin{{ID: "TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA", Slot: 31415926, Match: "verified"}}}).Example
	doc.Report.AuditID = "0197a100-0000-7000-8000-000000000002"
	doc.Report.Pin.Programs[0].BytecodeSHA256 = strings.Repeat("ab", 32)
	doc.Report.Methods.Fuzzing = &core.Fuzzing{Tool: "trident", Harnesses: 3, Runs: 12000000, Crashes: 1, DurationS: 7200, Coverage: "72% edges"}
	doc.Report.Known = []core.Known{{Title: "No order expiration", Severity: core.SeverityLow, Source: "Cyfrin 2026-03", Status: "acknowledged", Reference: "audits/2026-03-04-cyfrin.pdf"}}
	first := doc.Findings[0]
	first.Title = "Owner check missing on token account"
	first.Location = core.Location{File: "programs/vault/src/lib.rs", LineStart: 41, LineEnd: 45, Instruction: "withdraw", Package: "vault", ProgramID: doc.Report.Pin.Programs[0].ID}
	first.Snippet = &core.Snippet{StartLine: 38, Code: "pub fn withdraw(ctx: Context<Withdraw>, amount: u64) -> Result<()> {\n    let vault = &mut ctx.accounts.vault;\n    let dest = &ctx.accounts.destination;\n    // no owner check on dest\n    token::transfer(ctx.accounts.transfer_ctx(), amount)?;\n    vault.balance -= amount;\n    Ok(())\n}"}
	first.Fix = "- compare the destination's owner to the signer before the transfer\n```rust programs/vault/src/lib.rs:41-45\n    require_keys_eq!(dest.owner, ctx.accounts.authority.key(), VaultError::WrongOwner);\n    token::transfer(ctx.accounts.transfer_ctx(), amount)?;\n```\n\n- add a test that withdraws to an account the signer does not own"
	first.FixSites = []core.FixSite{{Location: core.Location{File: "programs/vault/src/lib.rs", LineStart: 41, LineEnd: 45}, Snippet: &core.Snippet{StartLine: 41, Code: "    // no owner check on dest\n    token::transfer(ctx.accounts.transfer_ctx(), amount)?;"}}}
	first.Sites = []core.Site{{Location: core.Location{File: "programs/vault/src/instructions/transfer.rs", LineStart: 12, LineEnd: 14}, Note: "the forged account reaches the CPI", Snippet: &core.Snippet{StartLine: 10, Code: "fn transfer_ctx(&self) -> CpiContext<'_, '_, '_, 'info, Transfer<'info>> {\n    CpiContext::new(\n        self.token_program.to_account_info(),\n        Transfer { from: self.vault_ata.to_account_info(), to: self.destination.to_account_info(), authority: self.vault.to_account_info() },\n    )\n}"}}}
	first.References = []string{"https://github.com/coral-xyz/anchor/blob/master/lang/src/accounts/account.rs"}
	core.AnswerAll(&first, true, "")
	doc.Findings[0] = first
	second := first
	second.Ordinal = first.Ordinal + 1
	second.Title = "Arbitrary CPI target"
	second.Severity = core.SeverityLow
	second.Status = core.FindingAcknowledged
	second.Description = "Reached after [[" + first.Title + "]] lets a caller forge the account, so the two compound.\n\n- the `invoke` target is read from `ctx.accounts.program`\n- nothing compares it to the expected id"
	second.SeverityChanges = []core.SeverityChange{{From: core.SeverityHigh, To: core.SeverityLow, Reason: "not reproducible on re-test; scope narrowed to the guard", By: "admin", At: time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC)}}
	second.StatusChanges = []core.StatusChange{{From: core.FindingOpen, To: core.FindingAcknowledged, By: "owner", At: time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC)}}
	second.Visuals = []core.Visual{
		{ID: "0199c000-0000-7000-8000-000000000001", Kind: core.VisualImage, Caption: "The guard's answer for an invented hostname", Mime: "image/png"},
		{ID: "0199c000-0000-7000-8000-000000000002", Kind: "file", Caption: "The script that ran it", Mime: "text/x-python", Name: "probe.py", Size: 2048},
	}
	second.Exhaustion = nil
	core.AnswerAll(&second, true, "")
	core.Answer(&second, "negative", false, "no fix deployed yet to re-test against")
	doc.Findings = append(doc.Findings, second)
	if err := doc.Validate(); err != nil {
		t.Fatal(err)
	}
	img := image.NewNRGBA(image.Rect(0, 0, 640, 200))
	for y := 0; y < 200; y++ {
		for x := 0; x < 640; x++ {
			img.Set(x, y, color.NRGBA{R: uint8(x / 3), G: 0x80, B: uint8(y), A: 0xff})
		}
	}
	var pngBuf bytes.Buffer
	if err := png.Encode(&pngBuf, img); err != nil {
		t.Fatal(err)
	}
	in := Input{Doc: doc, Adapter: "solana", Revision: 2, CreatedAt: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC),
		Visual: func(id string) (string, []byte, bool) {
			if id == "0199c000-0000-7000-8000-000000000001" {
				return "image/png", pngBuf.Bytes(), true
			}
			return "", nil, false
		}}
	return in, first.Title
}

func TestRender(t *testing.T) {
	in, _ := sample(t)
	b, err := Render(in)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(b, []byte("%PDF-")) {
		t.Fatalf("not a pdf: %q", b[:8])
	}
	if !bytes.Contains(b, []byte("/Subtype /Image")) {
		t.Error("the picture was not embedded")
	}
	if bytes.Count(b, []byte("/Type /Page\n")) < 2 && bytes.Count(b, []byte("/Type /Page")) < 3 {
		t.Errorf("expected a cover and a findings page")
	}
	if dir := os.Getenv("PDF_DUMP_DIR"); dir != "" {
		_ = os.WriteFile(dir+"/report.pdf", b, 0o644)
	}
}

// A report with nothing in it still renders: the cover says so.
func TestRenderEmpty(t *testing.T) {
	in, _ := sample(t)
	in.Doc.Findings = nil
	in.Doc.Report.Summary = core.Summary{}
	if _, err := Render(in); err != nil {
		t.Fatal(err)
	}
}
