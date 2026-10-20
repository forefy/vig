package rustsrc

import (
	"regexp"
	"strings"
)

var (
	invokeRE      = regexp.MustCompile(`\b(invoke|invoke_signed|invoke_signed_unchecked)\s*\(`)
	cpiCtxRE      = regexp.MustCompile(`CpiContext\s*::\s*(new|new_with_signer)\s*\(`)
	anchorCpiRE   = regexp.MustCompile(`\b([a-z_][a-z0-9_]*)\s*::\s*cpi\s*::\s*([a-z_][a-z0-9_]*)\s*\(`)
	methodInvRE   = regexp.MustCompile(`\.\s*(invoke|invoke_signed)\s*\(\s*\)`)
	tokenOpRE     = regexp.MustCompile(`\b(?:token|token_2022|token_interface|spl_token|spl_token_2022|instruction)\s*::\s*(transfer|transfer_checked|mint_to|mint_to_checked|burn|burn_checked|close_account|approve|approve_checked|revoke|freeze_account|thaw_account|set_authority|initialize_account|initialize_account3|initialize_mint|initialize_mint2|sync_native)\s*\(`)
	sysProgramRE  = regexp.MustCompile(`\bsystem_program\s*::\s*(transfer|create_account|assign|allocate)\s*\(`)
	withSignerRE  = regexp.MustCompile(`\.\s*with_signer\s*\(`)
	ataRE         = regexp.MustCompile(`\b(?:associated_token|spl_associated_token_account::instruction)\s*::\s*(create|create_idempotent)\s*\(`)
	bareTokenRE   = regexp.MustCompile(`(^|[^.:\w])(transfer|transfer_checked|mint_to|mint_to_checked|burn|burn_checked|close_account|approve|approve_checked|revoke|freeze_account|thaw_account|set_authority|sync_native)\s*\(`)
	tokenImportRE = regexp.MustCompile(`\b(?:anchor_spl::token|anchor_spl::token_interface|anchor_spl::token_2022|spl_token::instruction|spl_token_2022::instruction)\b`)
	fnDeclRE      = regexp.MustCompile(`fn\s*$`)
	pdaRE         = regexp.MustCompile(`\b(find_program_address|try_find_program_address|create_program_address)\s*\(`)
	lamportsRE    = regexp.MustCompile(`try_borrow_mut_lamports\s*\(|lamports\s*\.\s*borrow_mut\s*\(|\bsub_lamports\s*\(|\badd_lamports\s*\(|\bset_lamports\s*\(|\.lamports\s*\(\s*\)\s*[-+]=`)
	unsafeRE      = regexp.MustCompile(`\bunsafe\s*(\{|fn\b)`)
	reallocRE     = regexp.MustCompile(`\.\s*(realloc)\s*\(`)
	serdeRE       = regexp.MustCompile(`\b(try_from_slice|try_to_vec|deserialize|serialize|from_bytes|from_bytes_mut|try_from_bytes|try_from_bytes_mut|pod_read_unaligned|try_borrow_mut_data|borrow_mut_data)\s*\(`)
	remainingRE   = regexp.MustCompile(`\bremaining_accounts\b`)
	firstArgRE    = regexp.MustCompile(`^\s*(?:ctx\.accounts\.|self\.|accounts\.)?([A-Za-z_][A-Za-z0-9_]*)`)
	aliasRE       = regexp.MustCompile(`let\s+(?:mut\s+)?([A-Za-z_][A-Za-z0-9_]*)\s*(?::[^=;]*)?=\s*(?:ctx\.accounts\.|self\.|accounts\.)([A-Za-z_][A-Za-z0-9_]*)`)
	ixBuilderRE   = regexp.MustCompile(`\b(spl_token|spl_token_2022|system_instruction|spl_associated_token_account|mpl_token_metadata|spl_stake_pool|spl_governance|bpf_loader_upgradeable)\b`)
)

// programNames maps the type inside Program<'info, T> to a display name.
var programNames = map[string]string{
	"System": "System", "Token": "Token", "Token2022": "Token-2022", "TokenInterface": "Token", "AssociatedToken": "Associated Token",
	"Metadata": "Token Metadata", "Rent": "Rent", "Memo": "Memo", "Stake": "Stake",
}

// nameOf maps a program account's field name to a display name when its
// type is unknown: token_program is the Token program by convention.
var nameOf = map[string]string{"token": "Token", "system": "System", "associated_token": "Associated Token", "token_2022": "Token-2022", "metadata": "Token Metadata", "token_metadata": "Token Metadata", "rent": "Rent"}

var builderNames = map[string]string{
	"spl_token": "Token", "spl_token_2022": "Token-2022", "system_instruction": "System", "spl_associated_token_account": "Associated Token",
	"mpl_token_metadata": "Token Metadata", "spl_stake_pool": "Stake Pool", "spl_governance": "Governance", "bpf_loader_upgradeable": "BPF Loader",
}

// sites finds what matters in one function body. accounts resolves a
// program account's field name to its type, for CpiContext targets.
func sites(f *Function, s *source, open int, accounts map[string]Account) []Site {
	body := f.body
	at := func(off int) Site { return Site{Function: f.Name, File: f.File, Line: s.line(open + off)} }
	var out []Site
	// Locals bound to an account: let cpi_program = ctx.accounts.token_program...
	aliases := map[string]string{}
	for _, m := range aliasRE.FindAllStringSubmatch(body, -1) {
		aliases[m[1]] = m[2]
	}
	// What this body builds instructions with, for invoke targets.
	builder := ""
	for _, m := range ixBuilderRE.FindAllStringSubmatch(body, -1) {
		n := builderNames[m[1]]
		if builder == "" {
			builder = n
		} else if builder != n {
			builder = "?"
		}
	}
	if builder == "?" {
		builder = ""
	}
	for _, m := range invokeRE.FindAllStringSubmatchIndex(body, -1) {
		if m[2] > 0 && body[m[2]-1] == '.' {
			continue
		}
		st := at(m[2])
		st.Kind, st.Op = "cpi", body[m[2]:m[3]]
		st.Signed = st.Op != "invoke"
		st.Target = builder
		out = append(out, st)
	}
	for _, m := range cpiCtxRE.FindAllStringSubmatchIndex(body, -1) {
		st := at(m[2])
		st.Kind = "cpi"
		st.Op = "cpi_context"
		if body[m[2]:m[3]] == "new_with_signer" {
			st.Op, st.Signed = "cpi_signer", true
		}
		// The first argument names the program account.
		rest := body[m[1]:]
		if am := firstArgRE.FindStringSubmatch(rest); am != nil {
			name := am[1]
			if _, ok := accounts[name]; !ok {
				if a, ok := aliases[name]; ok {
					name = a
				}
			}
			// CpiContext::new(anchor_lang::system_program::..., ..) names a
			// path, not an account.
			if name == "anchor_lang" || name == "solana_program" {
				name = ""
				if strings.Contains(rest[:min(len(rest), 80)], "system_program") {
					name = "system_program"
				}
			}
			short := strings.TrimSuffix(name, "_program")
			if a, ok := accounts[name]; ok && a.Inner != "" {
				if n, ok := programNames[a.Inner]; ok {
					st.Target = n
				} else {
					st.Target = a.Inner
				}
			} else if n, ok := nameOf[short]; ok {
				st.Target = n
			} else if name != "" {
				st.Target = short
			}
		}
		out = append(out, st)
	}
	// other_program::cpi::instruction(ctx, ..): Anchor's generated client
	// names both the callee crate and the instruction. It is the same call
	// as the CpiContext built for it, so it is a link, not a second CPI.
	for _, m := range anchorCpiRE.FindAllStringSubmatchIndex(body, -1) {
		st := at(m[2])
		st.Kind, st.Op, st.Target, st.Ix = "cpi", "anchor_cpi", body[m[2]:m[3]], body[m[4]:m[5]]
		out = append(out, st)
	}
	for _, m := range methodInvRE.FindAllStringSubmatchIndex(body, -1) {
		st := at(m[2])
		st.Kind, st.Op, st.Signed = "cpi", "method", body[m[2]:m[3]] == "invoke_signed"
		out = append(out, st)
	}
	for _, m := range tokenOpRE.FindAllStringSubmatchIndex(body, -1) {
		st := at(m[2])
		st.Kind, st.Op, st.Target = "token", body[m[2]:m[3]], "Token"
		out = append(out, st)
	}
	// Bare transfer(...) and friends, imported from anchor_spl::token. A
	// bare transfer whose context names the System program is a lamport
	// move, not a token op; the CPI site already counts it.
	if tokenImportRE.Match(s.clean) {
		for _, m := range bareTokenRE.FindAllStringSubmatchIndex(body, -1) {
			if fnDeclRE.MatchString(body[:m[4]]) {
				continue
			}
			rest := body[m[5]:]
			if len(rest) > 240 {
				rest = rest[:240]
			}
			if cm := cpiCtxRE.FindStringIndex(rest); cm != nil {
				if am := firstArgRE.FindStringSubmatch(rest[cm[1]:]); am != nil {
					name := am[1]
					if a, ok := aliases[name]; ok {
						name = a
					}
					if acc, ok := accounts[name]; ok && acc.Inner == "System" || strings.TrimSuffix(name, "_program") == "system" {
						continue
					}
				}
			}
			st := at(m[4])
			st.Kind, st.Op, st.Target = "token", body[m[4]:m[5]], "Token"
			out = append(out, st)
		}
	}
	for _, m := range ataRE.FindAllStringSubmatchIndex(body, -1) {
		st := at(m[2])
		st.Kind, st.Op, st.Target = "token", "create_ata", "Associated Token"
		out = append(out, st)
	}
	for _, m := range sysProgramRE.FindAllStringSubmatchIndex(body, -1) {
		st := at(m[2])
		st.Kind, st.Op, st.Target = "cpi", body[m[2]:m[3]], "System"
		out = append(out, st)
	}
	for _, m := range pdaRE.FindAllStringSubmatchIndex(body, -1) {
		st := at(m[2])
		st.Kind = "pda"
		st.Op = "find"
		if body[m[2]:m[3]] == "create_program_address" {
			st.Op = "create"
		}
		out = append(out, st)
	}
	for _, m := range lamportsRE.FindAllStringIndex(body, -1) {
		st := at(m[0])
		st.Kind, st.Op = "lamports", "direct"
		out = append(out, st)
	}
	for _, m := range unsafeRE.FindAllStringIndex(body, -1) {
		st := at(m[0])
		st.Kind = "unsafe"
		out = append(out, st)
	}
	for _, m := range reallocRE.FindAllStringSubmatchIndex(body, -1) {
		st := at(m[2])
		st.Kind, st.Op = "realloc", body[m[2]:m[3]]
		out = append(out, st)
	}
	for _, m := range serdeRE.FindAllStringSubmatchIndex(body, -1) {
		st := at(m[2])
		st.Kind, st.Op = "serde", body[m[2]:m[3]]
		out = append(out, st)
	}
	return out
}
