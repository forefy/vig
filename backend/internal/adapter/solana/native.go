package solana

import (
	"encoding/binary"

	"github.com/forefy/vig/backend/internal/chain"
)

// nativeProgram describes a program with a fixed instruction set and no
// IDL: instructions are keyed by a leading tag of TagWidth bytes.
type nativeProgram struct {
	Name         string
	TagWidth     int
	Instructions map[uint32]instructionSpec
	Empty        string // instruction name when the data is empty
}

func acct(name string, signer, writable bool) accountSpec {
	return accountSpec{Name: name, Signer: signer, Writable: writable}
}

func ix(name string, accounts ...accountSpec) instructionSpec {
	return instructionSpec{Name: name, Accounts: accounts}
}

var (
	w  = func(n string) accountSpec { return acct(n, false, true) }
	s  = func(n string) accountSpec { return acct(n, true, false) }
	sw = func(n string) accountSpec { return acct(n, true, true) }
	r  = func(n string) accountSpec { return acct(n, false, false) }
)

var tokenInstructions = map[uint32]instructionSpec{
	0:  ix("initialize_mint", w("mint"), r("rent")),
	1:  ix("initialize_account", w("account"), r("mint"), r("owner"), r("rent")),
	2:  ix("initialize_multisig", w("multisig"), r("rent")),
	3:  ix("transfer", w("source"), w("destination"), s("authority")),
	4:  ix("approve", w("source"), r("delegate"), s("owner")),
	5:  ix("revoke", w("source"), s("owner")),
	6:  ix("set_authority", w("account"), s("authority")),
	7:  ix("mint_to", w("mint"), w("account"), s("authority")),
	8:  ix("burn", w("account"), w("mint"), s("authority")),
	9:  ix("close_account", w("account"), w("destination"), s("owner")),
	10: ix("freeze_account", w("account"), r("mint"), s("authority")),
	11: ix("thaw_account", w("account"), r("mint"), s("authority")),
	12: ix("transfer_checked", w("source"), r("mint"), w("destination"), s("authority")),
	13: ix("approve_checked", w("source"), r("mint"), r("delegate"), s("owner")),
	14: ix("mint_to_checked", w("mint"), w("account"), s("authority")),
	15: ix("burn_checked", w("account"), w("mint"), s("authority")),
	16: ix("initialize_account2", w("account"), r("mint"), r("rent")),
	17: ix("sync_native", w("account")),
	18: ix("initialize_account3", w("account"), r("mint")),
	19: ix("initialize_multisig2", w("multisig")),
	20: ix("initialize_mint2", w("mint")),
	21: ix("get_account_data_size", r("mint")),
	22: ix("initialize_immutable_owner", w("account")),
	23: ix("amount_to_ui_amount", r("mint")),
	24: ix("ui_amount_to_amount", r("mint")),
}

// token2022Instructions extend the token set with the extension entry points.
var token2022Instructions = func() map[uint32]instructionSpec {
	m := map[uint32]instructionSpec{}
	for k, v := range tokenInstructions {
		m[k] = v
	}
	for k, name := range map[uint32]string{
		25: "initialize_mint_close_authority", 26: "transfer_fee_extension", 27: "confidential_transfer_extension", 28: "default_account_state_extension",
		29: "reallocate", 30: "memo_transfer_extension", 31: "create_native_mint", 32: "initialize_non_transferable_mint", 33: "interest_bearing_mint_extension",
		34: "cpi_guard_extension", 35: "initialize_permanent_delegate", 36: "transfer_hook_extension", 37: "confidential_transfer_fee_extension",
		38: "withdraw_excess_lamports", 39: "metadata_pointer_extension", 40: "group_pointer_extension", 41: "group_member_pointer_extension",
	} {
		m[k] = ix(name)
	}
	return m
}()

var systemInstructions = map[uint32]instructionSpec{
	0:  ix("create_account", sw("from"), sw("new_account")),
	1:  ix("assign", sw("account")),
	2:  ix("transfer", sw("from"), w("to")),
	3:  ix("create_account_with_seed", sw("from"), w("new_account"), s("base")),
	4:  ix("advance_nonce_account", w("nonce"), r("recent_blockhashes"), s("authority")),
	5:  ix("withdraw_nonce_account", w("nonce"), w("to"), r("recent_blockhashes"), r("rent"), s("authority")),
	6:  ix("initialize_nonce_account", w("nonce"), r("recent_blockhashes"), r("rent")),
	7:  ix("authorize_nonce_account", w("nonce"), s("authority")),
	8:  ix("allocate", sw("account")),
	9:  ix("allocate_with_seed", w("account"), s("base")),
	10: ix("assign_with_seed", w("account"), s("base")),
	11: ix("transfer_with_seed", w("from"), s("base"), w("to")),
	12: ix("upgrade_nonce_account", w("nonce")),
}

var stakeInstructions = map[uint32]instructionSpec{
	0:  ix("initialize", w("stake"), r("rent")),
	1:  ix("authorize", w("stake"), r("clock"), s("authority")),
	2:  ix("delegate_stake", w("stake"), r("vote"), r("clock"), r("stake_history"), r("config"), s("authority")),
	3:  ix("split", w("stake"), w("split_stake"), s("authority")),
	4:  ix("withdraw", w("stake"), w("to"), r("clock"), r("stake_history"), s("withdrawer")),
	5:  ix("deactivate", w("stake"), r("clock"), s("authority")),
	6:  ix("set_lockup", w("stake"), s("authority")),
	7:  ix("merge", w("destination"), w("source"), r("clock"), r("stake_history"), s("authority")),
	8:  ix("authorize_with_seed", w("stake"), s("base"), r("clock")),
	9:  ix("initialize_checked", w("stake"), r("rent"), r("staker"), s("withdrawer")),
	10: ix("authorize_checked", w("stake"), r("clock"), s("authority"), s("new_authority")),
	11: ix("authorize_checked_with_seed", w("stake"), s("base"), r("clock"), s("new_authority")),
	12: ix("set_lockup_checked", w("stake"), s("authority")),
	13: ix("get_minimum_delegation"),
	14: ix("deactivate_delinquent", w("stake"), r("vote"), r("reference_vote")),
	15: ix("redelegate", w("stake"), w("uninitialized_stake"), r("vote"), r("config"), s("authority")),
}

var associatedTokenInstructions = map[uint32]instructionSpec{
	0: ix("create", sw("payer"), w("associated_token"), r("wallet"), r("mint"), r("system_program"), r("token_program")),
	1: ix("create_idempotent", sw("payer"), w("associated_token"), r("wallet"), r("mint"), r("system_program"), r("token_program")),
	2: ix("recover_nested", w("nested"), r("nested_mint"), w("destination"), r("owner_associated_token"), r("owner_mint"), sw("wallet"), r("token_program")),
}

var nativePrograms = map[string]nativeProgram{
	chain.TokenProgram:                            {Name: "Token", TagWidth: 1, Instructions: tokenInstructions},
	chain.Token2022Program:                        {Name: "Token-2022", TagWidth: 1, Instructions: token2022Instructions},
	"11111111111111111111111111111111":            {Name: "System", TagWidth: 4, Instructions: systemInstructions},
	"Stake11111111111111111111111111111111111111": {Name: "Stake", TagWidth: 4, Instructions: stakeInstructions},
	chain.AssociatedTokenProg:                     {Name: "Associated Token", TagWidth: 1, Instructions: associatedTokenInstructions, Empty: "create"},
}

// nativeSpec returns the fixed instruction set of a native program.
func nativeSpec(pid string) (nativeProgram, bool) {
	p, ok := nativePrograms[pid]
	return p, ok
}

// nativeName names a native call from its data, or "" when unknown.
func (p nativeProgram) resolve(data []byte) (instructionSpec, bool) {
	if len(data) == 0 {
		if p.Empty != "" {
			if spec, ok := p.Instructions[0]; ok {
				return spec, true
			}
			return ix(p.Empty), true
		}
		return instructionSpec{}, false
	}
	if len(data) < p.TagWidth {
		return instructionSpec{}, false
	}
	var tag uint32
	switch p.TagWidth {
	case 1:
		tag = uint32(data[0])
	case 4:
		tag = binary.LittleEndian.Uint32(data[:4])
	}
	spec, ok := p.Instructions[tag]
	return spec, ok
}

// specs lists the instructions in tag order for the map.
func (p nativeProgram) specs() []instructionSpec {
	out := make([]instructionSpec, 0, len(p.Instructions))
	for tag := uint32(0); int(tag) < 64 && len(out) < len(p.Instructions); tag++ {
		if spec, ok := p.Instructions[tag]; ok {
			out = append(out, spec)
		}
	}
	return out
}

// byName finds a native instruction by its parsed name, e.g. transferChecked.
func (p nativeProgram) byName(parsed string) (instructionSpec, bool) {
	want := snake(parsed)
	for _, spec := range p.Instructions {
		if spec.Name == want {
			return spec, true
		}
	}
	return instructionSpec{}, false
}

// resolveCall names a native call from the RPC's parsed form first, then
// from its bytes.
func (p nativeProgram) resolveCall(c chain.Call) (instructionSpec, bool) {
	if c.ParsedType != "" {
		if spec, ok := p.byName(c.ParsedType); ok {
			return spec, true
		}
		return ix(snake(c.ParsedType)), true
	}
	return p.resolve(c.Data)
}
