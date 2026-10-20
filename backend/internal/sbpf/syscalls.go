package sbpf

// Known runtime syscalls. Older toolchains call them by the murmur3 hash of
// the name; newer ones import them as undefined dynamic symbols. Both are
// recognised.
var syscallNames = []string{
	"abort", "sol_panic_", "sol_log_", "sol_log_64_", "sol_log_compute_units_", "sol_log_pubkey", "sol_log_data",
	"sol_create_program_address", "sol_try_find_program_address",
	"sol_sha256", "sol_keccak256", "sol_blake3", "sol_poseidon", "sol_secp256k1_recover",
	"sol_curve_validate_point", "sol_curve_group_op", "sol_curve_multiscalar_mul",
	"sol_alt_bn128_group_op", "sol_alt_bn128_compression", "sol_big_mod_exp",
	"sol_get_clock_sysvar", "sol_get_epoch_schedule_sysvar", "sol_get_fees_sysvar", "sol_get_rent_sysvar",
	"sol_get_last_restart_slot", "sol_get_epoch_rewards_sysvar", "sol_get_sysvar", "sol_get_epoch_stake",
	"sol_memcpy_", "sol_memmove_", "sol_memcmp_", "sol_memset_",
	"sol_invoke_signed_c", "sol_invoke_signed_rust", "sol_alloc_free_",
	"sol_set_return_data", "sol_get_return_data", "sol_get_stack_height",
	"sol_get_processed_sibling_instruction", "sol_remaining_compute_units",
}

var syscallHashes = func() map[string]uint32 {
	m := make(map[string]uint32, len(syscallNames))
	for _, n := range syscallNames {
		m[n] = murmur3([]byte(n), 0)
	}
	return m
}()

// murmur3 is MurmurHash3 x86_32, the hash the loader used for syscall names.
func murmur3(key []byte, seed uint32) uint32 {
	const c1, c2 = 0xcc9e2d51, 0x1b873593
	h := seed
	n := len(key)
	for i := 0; i+4 <= n; i += 4 {
		k := uint32(key[i]) | uint32(key[i+1])<<8 | uint32(key[i+2])<<16 | uint32(key[i+3])<<24
		k *= c1
		k = k<<15 | k>>17
		k *= c2
		h ^= k
		h = h<<13 | h>>19
		h = h*5 + 0xe6546b64
	}
	var k uint32
	switch n & 3 {
	case 3:
		k ^= uint32(key[n&^3+2]) << 16
		fallthrough
	case 2:
		k ^= uint32(key[n&^3+1]) << 8
		fallthrough
	case 1:
		k ^= uint32(key[n&^3])
		k *= c1
		k = k<<15 | k>>17
		k *= c2
		h ^= k
	}
	h ^= uint32(n)
	h ^= h >> 16
	h *= 0x85ebca6b
	h ^= h >> 13
	h *= 0xc2b2ae35
	h ^= h >> 16
	return h
}
