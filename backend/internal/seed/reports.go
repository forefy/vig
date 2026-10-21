package seed

import (
	"fmt"
	"github.com/forefy/vig/backend/internal/core"
)

// capture fills each finding's code from the checked-out commit, through the
// same Segment/AttachSnippets path an operator's report goes through. The seed
// carries no code of its own: what a dev report shows is what the product
// captured from the real repository at the real commit.
var capture func(repo, commit string, fs []core.Finding)

func withSnippets(repo, commit string, fs []core.Finding) []core.Finding {
	if capture != nil {
		capture(repo, commit, fs)
	}
	return fs
}

// fixes are the recommended lines a finding carries, keyed by file:line. These
// are authored - a recommendation is not something the repository contains -
// and are attached to code the product captured.
type recommendation struct {
	Code string
	At   int
}

func applyFixes(fs []core.Finding, m map[string]recommendation) {
	put := func(sn *core.Snippet, file string, line int) {
		if sn == nil {
			return
		}
		if r, ok := m[key(file, line)]; ok {
			sn.Fix, sn.FixAt = r.Code, r.At
		}
	}
	for i := range fs {
		put(fs[i].Snippet, fs[i].Location.File, fs[i].Location.LineStart)
		for j := range fs[i].Sites {
			put(fs[i].Sites[j].Snippet, fs[i].Sites[j].Location.File, fs[i].Sites[j].Location.LineStart)
		}
		// The recommendation is the patch: it rides on the fix as a fence
		// placed at the located lines. A finding with no recommendation
		// is closed operationally.
		if len(core.FixLocations(fs[i].Fix)) == 0 {
			if r, ok := m[key(fs[i].Location.File, fs[i].Location.LineStart)]; ok {
				end := fs[i].Location.LineEnd
				if end == 0 {
					end = fs[i].Location.LineStart
				}
				fs[i].Fix += fmt.Sprintf("\n```rust %s:%d-%d\n%s\n```", fs[i].Location.File, fs[i].Location.LineStart, end, r.Code)
			} else if fs[i].FixKind == "" {
				fs[i].FixKind = core.FixOperational
			}
		}
	}
}

func key(file string, line int) string { return file + ":" + itoa(line) }

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

func pin(repo, commit, base string, programs ...core.ProgramPin) core.Pin {
	return core.Pin{Repo: repo, Commit: commit, BaseSHA: base, Programs: programs}
}

func doc(p core.Pin, findings []core.Finding) core.FindingsDocument {
	findings = withSnippets(p.Repo, p.Commit, findings)
	for i := range findings {
		findings[i].Ordinal = i + 1
		if findings[i].Status == "" {
			findings[i].Status = core.FindingOpen
		}
	}
	return core.FindingsDocument{Report: core.ReportHeader{Pin: p, Summary: core.Summarize(findings)}, Findings: findings}
}

// vaultFixes are the recommended lines for the vault findings, taken
// from the `secure` variant each insecure program ships alongside it. They are
// attached to code the product captured; only the recommendation is authored.
var vaultFixes = map[string]recommendation{
	key("programs/0-signer-authorization/insecure/src/lib.rs", 16):  {Code: "    authority: Signer<'info>,"},
	key("programs/0-signer-authorization/insecure/src/lib.rs", 9):   {Code: "        if !ctx.accounts.authority.is_signer {\n            return Err(ProgramError::MissingRequiredSignature);\n        }", At: 9},
	key("programs/2-owner-checks/insecure/src/lib.rs", 13):          {Code: "        if ctx.accounts.token.owner != &spl_token::ID {\n            return Err(ProgramError::InvalidAccountData);\n        }"},
	key("programs/1-account-data-matching/insecure/src/lib.rs", 12): {Code: "        if ctx.accounts.authority.key != &token.owner {\n            return Err(ProgramError::InvalidAccountData);\n        }"},
	key("programs/5-arbitrary-cpi/insecure/src/lib.rs", 13):         {Code: "        if &spl_token::ID != ctx.accounts.token_program.key {\n            return Err(ProgramError::IncorrectProgramId);\n        }", At: 11},
	key("programs/9-closing-accounts/insecure/src/lib.rs", 12):      {Code: "        let mut data = account.try_borrow_mut_data()?;\n        for byte in data.deref_mut().iter_mut() {\n            *byte = 0;\n        }\n\n        let dst: &mut [u8] = &mut data;\n        let mut cursor = Cursor::new(dst);\n        cursor.write_all(&CLOSED_ACCOUNT_DISCRIMINATOR).unwrap();"},
	key("programs/3-type-cosplay/insecure/src/lib.rs", 34):          {Code: "#[derive(BorshSerialize, BorshDeserialize)]\npub struct Metadata {\n    discriminant: AccountDiscriminant,\n    account: Pubkey,\n}"},
}

// vaultReport is the first full audit of the vault program.
func vaultReport(repo, commit, program string) core.FindingsDocument {
	d := doc(pin(repo, commit, "", core.ProgramPin{ID: program, Slot: 284_912_000, BytecodeSHA256: hash32("vault"), Match: core.MatchVerified}), []core.Finding{
		{Severity: core.SeverityCritical, Title: "Missing signer check on withdraw authority", ExploitableBy: core.ExploitableAnyone,
			Evidence:  &core.Evidence{Kind: core.EvidenceFuzz, Tool: "trident", Artifact: "withdraw_authority crash 3f9c1e"},
			Location:  core.Location{File: "programs/0-signer-authorization/insecure/src/lib.rs", Package: "0-signer-authorization", LineStart: 16, LineEnd: 16, ProgramID: program, Instruction: "log_message"},
			SitesKind: core.SitesFlow,
			Sites: []core.Site{
				{Location: core.Location{File: "programs/0-signer-authorization/insecure/src/lib.rs", Package: "0-signer-authorization", LineStart: 16, LineEnd: 16}, Note: "authority declared unsigned"},
				{Location: core.Location{File: "programs/0-signer-authorization/insecure/src/lib.rs", Package: "0-signer-authorization", LineStart: 9, LineEnd: 9}, Note: "used as if it had signed"},
			},
			Description: "The authority account is read but never required to sign. Any caller can pass the vault's authority pubkey and the instruction proceeds as if the authority approved it.",
			ExploitPath: "Build a transaction that passes the real authority pubkey as a non-signer account and calls the instruction; the runtime accepts it because the program never inspects is_signer.",
			Fix:         "- Declare the account as `Signer<'info>` in the accounts struct, or check `authority.is_signer` before acting on it.",
			References:  []string{"https://solana.com/developers/courses/program-security/signer-auth"}},
		{Severity: core.SeverityHigh, Title: "Owner check missing on token account", ExploitableBy: core.ExploitableAnyone,
			Evidence:  &core.Evidence{Kind: core.EvidenceStatic},
			Location:  core.Location{File: "programs/2-owner-checks/insecure/src/lib.rs", Package: "2-owner-checks", LineStart: 13, LineEnd: 13, ProgramID: program, Instruction: "log_message"},
			SitesKind: core.SitesOccurrences,
			Sites: []core.Site{
				{Location: core.Location{File: "programs/2-owner-checks/insecure/src/lib.rs", Package: "2-owner-checks", LineStart: 13, LineEnd: 13}, Note: "token account"},
				{Location: core.Location{File: "programs/1-account-data-matching/insecure/src/lib.rs", Package: "1-account-data-matching", LineStart: 12, LineEnd: 12}, Note: "account data"},
			},
			Description: "The token account is deserialized without checking that its owner is the SPL Token program. A fake account with the same layout passes the checks.",
			ExploitPath: "Create an account owned by an attacker program with SPL Token layout and the victim's pubkey as authority; pass it in place of the real token account.",
			Fix:         "- Use `Account<'info, TokenAccount>` so Anchor enforces the owner, or compare `token.owner` with `spl_token::ID` explicitly.",
			References:  nil},
		{Severity: core.SeverityMedium, Title: "Arbitrary CPI target", ExploitableBy: core.ExploitableAnyone,
			Location:    core.Location{File: "programs/5-arbitrary-cpi/insecure/src/lib.rs", Package: "5-arbitrary-cpi", LineStart: 13, LineEnd: 13, ProgramID: program, Instruction: "cpi"},
			Description: "The program invoked through CPI is taken from the accounts list without validation. A caller can substitute any program with a matching instruction layout.",
			ExploitPath: "Pass an attacker-controlled program id in the token_program slot; the CPI transfers control to it with the vault's signer seeds.",
			Fix:         "- Use `Program<'info, Token>` so Anchor checks the program id\n```rust\npub token_program: Program<'info, Token>,\n```\n\n- or assert `token_program.key() == spl_token::ID` before invoking",
			References:  nil},
		{Severity: core.SeverityLow, Title: "Closed account can be revived within the same transaction", ExploitableBy: core.ExploitableAnyone,
			Location:    core.Location{File: "programs/9-closing-accounts/insecure/src/lib.rs", Package: "9-closing-accounts", LineStart: 12, LineEnd: 15, ProgramID: program, Instruction: "close"},
			Description: "Lamports are drained but the account data is not zeroed and no closed discriminator is written, so a later instruction in the same transaction can refund rent and reuse the account.",
			Fix:         "- Zero the data and write the `CLOSED_ACCOUNT_DISCRIMINATOR`, or use Anchor's `close` constraint.",
			References:  nil},
		{Severity: core.SeverityInfo, Title: "Type cosplay possible between User and Metadata", ExploitableBy: core.ExploitableNone,
			Location:    core.Location{File: "programs/3-type-cosplay/insecure/src/lib.rs", Package: "3-type-cosplay", LineStart: 34, LineEnd: 37, ProgramID: program},
			Description: "Two account types share a layout and no discriminator. Not exploitable with the current instruction set, but a future instruction reading one type could be fed the other.",
			Fix:         "- Add a discriminator field or use `#[account]` so Anchor writes one.",
			References:  nil},
	})
	applyFixes(d.Findings, vaultFixes)
	d.Report.Methods = &core.Methods{Static: true, Bytecode: true, OnChain: true, Fuzzing: &core.Fuzzing{Tool: "trident", Harnesses: 3, Runs: 1_840_000, Crashes: 2, DurationS: 6 * 3600, Coverage: "71% edges"}}
	return d
}

// vaultDiffReport is the re-audit of the vault program after the fix.
// Two findings carry over by title and file; one is new.
func vaultDiffReport(repo, head, base, program string) core.FindingsDocument {
	d := doc(pin(repo, head, base, core.ProgramPin{ID: program, Slot: 285_200_000, BytecodeSHA256: hash32("vault-b"), Match: core.MatchSelfBuilt}), []core.Finding{
		{Severity: core.SeverityHigh, Title: "Owner check missing on token account", ExploitableBy: core.ExploitableAnyone,
			Location:    core.Location{File: "programs/2-owner-checks/insecure/src/lib.rs", Package: "2-owner-checks", LineStart: 13, LineEnd: 13, ProgramID: program, Instruction: "log_message"},
			Description: "Unchanged since the previous review: the token account owner is still not validated.",
			ExploitPath: "Same as the previous report: substitute an attacker-owned account with SPL Token layout.",
			Fix:         "- Use `Account<'info, TokenAccount>` so the owner is checked at deserialization\n```rust\npub token: Account<'info, TokenAccount>,\n```\n\n- or compare `token.owner` with `spl_token::ID` before reading it",
			References:  nil},
		{Severity: core.SeverityMedium, Title: "Arbitrary CPI target", ExploitableBy: core.ExploitableAnyone,
			Location:    core.Location{File: "programs/5-arbitrary-cpi/insecure/src/lib.rs", Package: "5-arbitrary-cpi", LineStart: 13, LineEnd: 13, ProgramID: program, Instruction: "cpi"},
			Description: "Unchanged since the previous review.",
			ExploitPath: "Pass an attacker-controlled program id in the token_program slot.",
			Fix:         "- Use `Program<'info, Token>`.",
			References:  nil},
		{Severity: core.SeverityMedium, Title: "Account data matching skipped after refactor", ExploitableBy: core.ExploitableAnyone,
			Location:    core.Location{File: "programs/1-account-data-matching/insecure/src/lib.rs", Package: "1-account-data-matching", LineStart: 12, LineEnd: 14, ProgramID: program, Instruction: "log_message"},
			Description: "The refactor between the base and head commits removed the comparison between the stored authority and the passed account, so any token account passes.",
			ExploitPath: "Pass a token account whose authority differs from the stored one; the instruction no longer compares them.",
			Fix:         "- Restore the `has_one = authority` constraint or the explicit key comparison.",
			References:  nil},
		{Severity: core.SeverityLow, Title: "Deserialization unwraps on attacker-supplied data", ExploitableBy: core.ExploitableAnyone,
			Location:    core.Location{File: "programs/3-type-cosplay/insecure/src/lib.rs", Package: "3-type-cosplay", LineStart: 11, LineEnd: 11, ProgramID: program, Instruction: "update_user"},
			Description: "try_from_slice is unwrapped, so an account whose data is shorter than the layout panics the instruction instead of returning an error.",
			Fix:         "- Propagate the error with `?` and a typed account error rather than unwrapping.",
			References:  nil},
	})
	applyFixes(d.Findings, vaultFixes)
	return d
}

// stakepoolReport is a full audit of a larger program with a mixed severity set.
func stakepoolReport(repo, commit, program string) core.FindingsDocument {
	// Every location below was read at the pinned commit. The code each one
	// shows is captured from that checkout, not written here.
	d := doc(pin(repo, commit, "", core.ProgramPin{ID: program, Slot: 281_770_000, BytecodeSHA256: hash32("stakepool"), Match: core.MatchVerified}), []core.Finding{
		{Severity: core.SeverityMedium, Title: "Liquid unstake fee rounds to zero on small amounts", ExploitableBy: core.ExploitableAnyone,
			Evidence:    &core.Evidence{Kind: core.EvidenceStatic},
			Location:    core.Location{File: "programs/stakepool/src/instructions/liq_pool/liquid_unstake.rs", Package: "stakepool", LineStart: 91, LineEnd: 91, ProgramID: program, Instruction: "liquid_unstake"},
			Description: "Fee::apply multiplies by basis points and divides, both in integers, so any msol_amount small enough that the product truncates to zero pays no fee at all. The treasury cut is then taken from that zero.",
			ExploitPath: "Split an unstake into amounts below the rounding threshold and repeat; each call takes pool liquidity and pays nothing.",
			Fix:         "- Round the fee up rather than down, or refuse amounts below the smallest that yields a non-zero fee.",
			References:  []string{"https://github.com/vig-demo/stake-pool/blob/main/docs/unstake.md"}},
		{Severity: core.SeverityLow, Title: "Admin configuration takes effect in the same transaction", ExploitableBy: core.ExploitableAdmin,
			Evidence:    &core.Evidence{Kind: core.EvidenceStatic},
			Location:    core.Location{File: "programs/stakepool/src/instructions/admin/config_pool.rs", Package: "stakepool", LineStart: 151, LineEnd: 151, ProgramID: program, Instruction: "config_pool"},
			Description: "Each parameter is written straight to state as it is read from the instruction, with no staging and no delay. Whoever holds the admin key can change a cap or a fee and have it apply to the very next instruction in the same block.",
			Fix:         "- Stage parameter changes and apply them after a delay a depositor can observe and react to.",
		},
		{Severity: core.SeverityInfo, Title: "Sixteen error variants are never constructed", ExploitableBy: core.ExploitableNone,
			Evidence:    &core.Evidence{Kind: core.EvidenceStatic},
			Location:    core.Location{File: "programs/stakepool/src/error.rs", Package: "stakepool", LineStart: 8, LineEnd: 8, ProgramID: program},
			Description: "StakepoolError declares 93 variants and 77 are referenced; the remaining 16, beginning with WrongReserveOwner, appear nowhere outside the enum. Each one still consumes a stable error code that clients may have mapped.",
			Fix:         "- Return them from the checks they were written for, or remove them and document the codes as retired.",
		},
	})
	// No captured recommendations on this report: every fix is operational.
	applyFixes(d.Findings, map[string]recommendation{})
	return d
}
