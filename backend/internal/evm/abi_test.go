package evm

import (
	"encoding/hex"
	"math/big"
	"strings"
	"testing"
)

func word(hexs string) string { return strings.Repeat("0", 64-len(hexs)) + hexs }

func TestSelectorsAndTopics(t *testing.T) {
	abi, err := ParseABI([]byte(`[
	  {"type":"function","name":"transfer","inputs":[{"name":"to","type":"address"},{"name":"amount","type":"uint256"}]},
	  {"type":"function","name":"multicall","inputs":[{"name":"deadline","type":"uint256"},{"name":"data","type":"bytes[]"}]},
	  {"type":"function","name":"exactInputSingle","inputs":[{"name":"params","type":"tuple","components":[
	    {"name":"tokenIn","type":"address"},{"name":"tokenOut","type":"address"},{"name":"fee","type":"uint24"},{"name":"recipient","type":"address"},
	    {"name":"amountIn","type":"uint256"},{"name":"amountOutMinimum","type":"uint256"},{"name":"sqrtPriceLimitX96","type":"uint160"}]}]},
	  {"type":"event","name":"Transfer","inputs":[{"name":"from","type":"address","indexed":true},{"name":"to","type":"address","indexed":true},{"name":"value","type":"uint256"}]}
	]`))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"transfer": "a9059cbb", "multicall": "5ae401dc", "exactInputSingle": "04e45aaf"}
	for _, f := range abi.Functions {
		if got := hex.EncodeToString(f.Selector[:]); want[f.Name] != got {
			t.Errorf("%s selector %s want %s (sig %s)", f.Name, got, want[f.Name], f.Sig)
		}
	}
	for _, e := range abi.Events {
		if got := hex.EncodeToString(e.Topic[:]); got != "ddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef" {
			t.Errorf("Transfer topic %s", got)
		}
	}
}

func TestDecodeStaticAndDynamic(t *testing.T) {
	abi, _ := ParseABI([]byte(`[
	  {"type":"function","name":"transfer","inputs":[{"name":"to","type":"address"},{"name":"amount","type":"uint256"}]},
	  {"type":"function","name":"multicall","inputs":[{"name":"deadline","type":"uint256"},{"name":"data","type":"bytes[]"}]},
	  {"type":"function","name":"neg","inputs":[{"name":"x","type":"int256"},{"name":"ok","type":"bool"},{"name":"tag","type":"bytes4"}]}
	]`))
	to := "5aaeb6053f3e94c9b9a09f33669435e7ef1beaed"
	data, _ := hex.DecodeString("a9059cbb" + word(to) + word("3e8"))
	f, args, err := abi.DecodeCall(data)
	if err != nil || f.Name != "transfer" || args["to"] != "0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed" || args["amount"] != "1000" {
		t.Fatalf("transfer: %v %v %v", f.Name, args, err)
	}
	// multicall(deadline, [0xdeadbeef]): head = deadline, offset 0x40; tail = len 1, elem offset 0x20, bytes len 4, bytes.
	mc := "5ae401dc" + word("64") + word("40") + word("1") + word("20") + word("4") + "deadbeef" + strings.Repeat("0", 56)
	data, _ = hex.DecodeString(mc)
	f, args, err = abi.DecodeCall(data)
	if err != nil || f.Name != "multicall" || args["deadline"] != "100" || args["data.length"] != "1" || args["data[0]"] != "0xdeadbeef" {
		t.Fatalf("multicall: %v %v %v", f.Name, args, err)
	}
	neg := "" + strings.Repeat("f", 64) + word("1") + "cafebabe" + strings.Repeat("0", 56)
	sel := Keccak([]byte("neg(int256,bool,bytes4)"))
	data, _ = hex.DecodeString(hex.EncodeToString(sel[:4]) + neg)
	f, args, err = abi.DecodeCall(data)
	if err != nil || f.Name != "neg" || args["x"] != "-1" || args["ok"] != "true" || args["tag"] != "0xcafebabe" {
		t.Fatalf("neg: %v %v %v", f.Name, args, err)
	}
	if _, _, err := abi.DecodeCall([]byte{1, 2}); err == nil {
		t.Error("short calldata accepted")
	}
	if _, _, err := abi.DecodeCall([]byte{9, 9, 9, 9}); err == nil {
		t.Error("unknown selector accepted")
	}
}

func TestDecodeTupleAndLog(t *testing.T) {
	abi, _ := ParseABI([]byte(`[
	  {"type":"function","name":"exactInputSingle","inputs":[{"name":"params","type":"tuple","components":[
	    {"name":"tokenIn","type":"address"},{"name":"tokenOut","type":"address"},{"name":"fee","type":"uint24"},{"name":"recipient","type":"address"},
	    {"name":"amountIn","type":"uint256"},{"name":"amountOutMinimum","type":"uint256"},{"name":"sqrtPriceLimitX96","type":"uint160"}]}]},
	  {"type":"event","name":"Transfer","inputs":[{"name":"from","type":"address","indexed":true},{"name":"to","type":"address","indexed":true},{"name":"value","type":"uint256"}]}
	]`))
	a := "1111111111111111111111111111111111111111"
	b := "2222222222222222222222222222222222222222"
	body := word(a) + word(b) + word("bb8") + word(a) + word("de0b6b3a7640000") + word("0") + word("0")
	data, _ := hex.DecodeString("04e45aaf" + body)
	f, args, err := abi.DecodeCall(data)
	if err != nil || f.Name != "exactInputSingle" || args["params.fee"] != "3000" || args["params.amountIn"] != "1000000000000000000" || args["params.tokenOut"] != Checksum(mustHex(b)) {
		t.Fatalf("tuple: %v %v %v", f.Name, args, err)
	}
	topic0 := Keccak([]byte("Transfer(address,address,uint256)"))
	var t1, t2 [32]byte
	copy(t1[12:], mustHex(a))
	copy(t2[12:], mustHex(b))
	logData, _ := hex.DecodeString(word("2a"))
	e, ev, err := abi.DecodeLog([][32]byte{topic0, t1, t2}, logData)
	if err != nil || e.Name != "Transfer" || ev["from"] != Checksum(mustHex(a)) || ev["to"] != Checksum(mustHex(b)) || ev["value"] != "42" {
		t.Fatalf("log: %v %v %v", e.Name, ev, err)
	}
}

func TestChecksum(t *testing.T) {
	if got := Checksum(mustHex("5aaeb6053f3e94c9b9a09f33669435e7ef1beaed")); got != "0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed" {
		t.Errorf("checksum %s", got)
	}
}

func TestSignatureParsing(t *testing.T) {
	f, err := ParseSignature("swap(uint256,address[],(address,uint24)[],bytes)")
	if err != nil || f.Name != "swap" || len(f.Inputs) != 4 || f.Inputs[1].Type != "address[]" || f.Inputs[2].Type != "tuple[]" || len(f.Inputs[2].Components) != 2 || f.Inputs[3].Type != "bytes" {
		t.Fatalf("parse: %+v %v", f, err)
	}
	if hex.EncodeToString(f.Selector[:]) != hex.EncodeToString(func() []byte { h := Keccak([]byte("swap(uint256,address[],(address,uint24)[],bytes)")); return h[:4] }()) {
		t.Error("selector does not match the canonical signature")
	}
}

func mustHex(s string) []byte {
	b, err := hex.DecodeString(s)
	if err != nil {
		panic(err)
	}
	return b
}

func TestPushedSelectors(t *testing.T) {
	// PUSH4 a9059cbb EQ, PUSH3 04e45a (a selector with a leading zero byte
	// pushed short), PUSH32 of arbitrary data that contains a PUSH4 pattern
	// inside it and must be skipped, then STOP.
	code, _ := hex.DecodeString("63a9059cbb14" + "6204e45a" + "7f" + "63deadbeef" + strings.Repeat("00", 27) + "00")
	got := PushedSelectors(code)
	if !got[[4]byte{0xa9, 0x05, 0x9c, 0xbb}] || !got[[4]byte{0x00, 0x04, 0xe4, 0x5a}] {
		t.Errorf("missing pushed selectors: %v", got)
	}
	if got[[4]byte{0xde, 0xad, 0xbe, 0xef}] {
		t.Error("read push data as opcodes")
	}
}

func TestUnitsRendering(t *testing.T) {
	cases := map[string]string{"5130000000000000000": "5.13", "100000000": "100", "89": "<0.0001", "0": "0", "123456789012345678901": "123.4567"}
	for in, want := range cases {
		v, _ := new(big.Int).SetString(in, 10)
		d := 18
		if in == "100000000" {
			d = 6
		}
		if got := Units(v, d); got != want {
			t.Errorf("Units(%s, %d) = %q want %q", in, d, got, want)
		}
	}
}
