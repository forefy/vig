package sbpf

import (
	"crypto/sha256"
	"encoding/binary"
	"strings"
)

// Anchor tags every instruction with sha256("global:<snake_name>")[:8] and
// every account with sha256("account:<TypeName>")[:8]. The tags are baked
// into the program as constants, so a dictionary of common names recovers
// the program's surface without an IDL. Exact by construction: a match is
// a preimage, not a guess.

var instructionDictionary = strings.Fields(`
initialize init initialize_pool initialize_config initialize_vault initialize_market initialize_user initialize_state initialize_mint initialize_account
create create_pool create_vault create_market create_account create_position create_order create_config create_user create_treasury create_stake create_token
update update_config update_pool update_state update_fees update_authority update_price update_oracle update_active update_deactivated update_metadata
close close_account close_position close_pool close_market close_vault close_order close_user close_config close_ticket
deposit deposit_sol deposit_stake_account deposit_stake deposit_liquidity deposit_collateral deposit_reserve_liquidity deposit_tokens deposit_and_stake
withdraw withdraw_sol withdraw_stake withdraw_stake_account withdraw_liquidity withdraw_collateral withdraw_fees withdraw_rewards withdraw_tokens withdraw_all
swap swap_exact_in swap_exact_out swap_base_in swap_base_out swap_v2 route
stake unstake liquid_unstake order_unstake delayed_unstake instant_unstake emergency_unstake partial_unstake restake redelegate delegate undelegate
claim claim_rewards claim_fees claim_ticket claim_tokens claim_airdrop harvest harvest_rewards compound
borrow repay repay_obligation liquidate liquidate_obligation liquidate_position refresh_reserve refresh_obligation flash_loan flash_borrow flash_repay
mint mint_to burn transfer transfer_authority transfer_ownership freeze thaw
lock unlock extend_lock relock vest revoke cliff release
set_authority change_authority set_admin set_fee set_fees set_params set_lp_params set_validator_score set_config set_oracle set_pause set_paused
pause unpause resume halt emergency_pause
add_liquidity remove_liquidity add_validator remove_validator add_pool remove_pool add_reward remove_reward add_collateral remove_collateral
buy sell bid ask cancel cancel_order cancel_bid cancel_all place_order place_limit_order place_market_order match_orders consume_events settle settle_funds crank
open_position close_position increase_liquidity decrease_liquidity collect_fees collect_rewards collect_protocol_fees rebalance
config_pool config_validator_system config_lp stake_reserve deactivate_stake merge_stakes realloc_validator_list realloc_stake_list migrate upgrade
verify verify_signature validate execute propose approve reject vote finalize activate deactivate register unregister whitelist blacklist
create_proposal execute_proposal cancel_proposal cast_vote add_member remove_member set_threshold
wrap unwrap sync bridge deposit_for_burn receive_message send_message
`)

var accountDictionary = strings.Fields(`
State GlobalState Config GlobalConfig Pool PoolState Vault VaultState Market MarketState User UserState UserAccount Position PositionState Order OrderState
Escrow Treasury Stake StakeAccount StakeList ValidatorList ValidatorRecord TicketAccountData LiqPool Reserve Obligation LendingMarket Oracle PriceFeed
Whitelist Proposal Vote Multisig Transaction Governance Realm TokenOwnerRecord Metadata Collection Auction Listing Bid Farm Staker Miner Quarry Rewarder
Amm AmmState TickArray PersonalPosition AmmConfig PoolInfo Registry Authority Admin Fee FeeTier Bridge Vesting VestingSchedule Lock Locker
`)

type discriminatorHit struct {
	Name string
	Kind string // instruction or account
}

var discriminatorIndex = func() map[[8]byte]discriminatorHit {
	m := map[[8]byte]discriminatorHit{}
	for _, n := range instructionDictionary {
		var k [8]byte
		s := sha256.Sum256([]byte("global:" + n))
		copy(k[:], s[:8])
		m[k] = discriminatorHit{Name: n, Kind: "instruction"}
	}
	for _, n := range accountDictionary {
		var k [8]byte
		s := sha256.Sum256([]byte("account:" + n))
		copy(k[:], s[:8])
		m[k] = discriminatorHit{Name: n, Kind: "account"}
	}
	return m
}()

// findDiscriminators scans read-only data (any 8-byte window) and the code
// (64-bit immediates loaded by lddw) for known Anchor tags.
func findDiscriminators(rodata, text []byte) (instructions, accounts []string) {
	seen := map[string]bool{}
	hit := func(k [8]byte) {
		if h, ok := discriminatorIndex[k]; ok && !seen[h.Kind+":"+h.Name] {
			seen[h.Kind+":"+h.Name] = true
			if h.Kind == "instruction" {
				instructions = append(instructions, h.Name)
			} else {
				accounts = append(accounts, h.Name)
			}
		}
	}
	var k [8]byte
	for i := 0; i+8 <= len(rodata); i++ {
		copy(k[:], rodata[i:i+8])
		hit(k)
	}
	// Code compares tags as one 64-bit immediate (lddw), as two 32-bit
	// immediates a few instructions apart, or, as rustc usually lowers an
	// eight-byte array match, one byte at a time with jump-if-equal
	// immediates interleaved with loads. Collect every immediate in order
	// and test all three shapes.
	var imms []uint32
	var cmp []bool // whether the immediate belongs to a jump-if-(not-)equal
	for off := 0; off+8 <= len(text); {
		op := text[off]
		imm := binary.LittleEndian.Uint32(text[off+4 : off+8])
		if op == opLddw && off+16 <= len(text) {
			hi := binary.LittleEndian.Uint32(text[off+12 : off+16])
			binary.LittleEndian.PutUint64(k[:], uint64(imm)|uint64(hi)<<32)
			hit(k)
			imms = append(imms, imm, hi)
			cmp = append(cmp, false, false)
			off += 16
			continue
		}
		imms = append(imms, imm)
		cmp = append(cmp, op == 0x15 || op == 0x55 || op == 0x16 || op == 0x56)
		off += 8
	}
	for i := range imms {
		if imms[i] == 0 {
			continue
		}
		for j := i + 1; j < len(imms) && j <= i+6; j++ {
			if imms[j] == 0 {
				continue
			}
			binary.LittleEndian.PutUint64(k[:], uint64(imms[i])|uint64(imms[j])<<32)
			hit(k)
			binary.LittleEndian.PutUint64(k[:], uint64(imms[j])|uint64(imms[i])<<32)
			hit(k)
		}
	}
	// Byte-wise: from every instruction whose immediate is a candidate first
	// byte, greedily match the remaining seven on later compare instructions. The
	// window is wide because rustc lowers the match as a decision tree that
	// interleaves the branches.
	for i, imm := range imms {
		if imm > 0xff || !cmp[i] {
			continue
		}
		for _, cand := range byFirstByte[byte(imm)] {
			n := 1
			for j := i + 1; j < len(imms) && j < i+400 && n < 8; j++ {
				if cmp[j] && imms[j] == uint32(cand[n]) {
					n++
				}
			}
			if n == 8 {
				hit(cand)
			}
		}
	}
	return instructions, accounts
}

// byFirstByte indexes the dictionary for the byte-wise scan.
var byFirstByte = func() map[byte][][8]byte {
	m := map[byte][][8]byte{}
	for k := range discriminatorIndex {
		m[k[0]] = append(m[k[0]], k)
	}
	return m
}()
