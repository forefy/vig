package evm

// PushedSelectors walks the bytecode by opcode and returns every constant
// it pushes that fits in four bytes, left-padded. A Solidity dispatcher
// compares the calldata selector against each function's selector with a
// PUSH4 (a shorter push when the selector has leading zero bytes), so the
// set names the functions the bytecode answers to, verified or not. Push
// data is skipped, never read as opcodes; the metadata trailer at the end
// may still add a few stray constants, which is harmless for a lookup.
func PushedSelectors(code []byte) map[[4]byte]bool {
	out := map[[4]byte]bool{}
	for i := 0; i < len(code); i++ {
		op := code[i]
		if op < 0x60 || op > 0x7f {
			continue
		}
		n := int(op) - 0x5f
		if i+n >= len(code) {
			break
		}
		if n <= 4 {
			var sel [4]byte
			copy(sel[4-n:], code[i+1:i+1+n])
			out[sel] = true
		}
		i += n
	}
	return out
}
