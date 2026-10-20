// Package evm reads Ethereum contracts the way the Solana adapter reads
// programs: an ABI in place of an IDL, calldata in place of instruction
// data, logs in place of account writes. Nothing here needs a key.
package evm

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"

	"golang.org/x/crypto/sha3"
)

// Param is one input or output of a function or event.
type Param struct {
	Name       string  `json:"name"`
	Type       string  `json:"type"`
	Indexed    bool    `json:"indexed,omitempty"`
	Components []Param `json:"components,omitempty"`
}

// Function is one entry of the ABI that calldata can select.
type Function struct {
	Name     string
	Inputs   []Param
	Selector [4]byte
	Sig      string // name(type,...)
}

// Event is one entry of the ABI that a log can match.
type Event struct {
	Name   string
	Inputs []Param
	Topic  [32]byte
	Sig    string
}

// Error is one custom error the contract can revert with.
type Error struct {
	Name     string
	Inputs   []Param
	Selector [4]byte
	Sig      string
}

// ABI is a contract's interface, keyed for decoding.
type ABI struct {
	Functions map[[4]byte]Function
	Events    map[[32]byte]Event
	Errors    map[[4]byte]Error
}

type abiEntry struct {
	Type   string  `json:"type"`
	Name   string  `json:"name"`
	Inputs []Param `json:"inputs"`
}

// ParseABI reads the JSON ABI as Sourcify and compilers emit it.
func ParseABI(raw []byte) (*ABI, error) {
	var entries []abiEntry
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, err
	}
	a := &ABI{Functions: map[[4]byte]Function{}, Events: map[[32]byte]Event{}, Errors: map[[4]byte]Error{}}
	for _, e := range entries {
		switch e.Type {
		case "error":
			sig := e.Name + "(" + typeList(e.Inputs) + ")"
			h := Keccak([]byte(sig))
			var sel [4]byte
			copy(sel[:], h[:4])
			a.Errors[sel] = Error{Name: e.Name, Inputs: e.Inputs, Selector: sel, Sig: sig}
		case "function":
			sig := e.Name + "(" + typeList(e.Inputs) + ")"
			h := Keccak([]byte(sig))
			var sel [4]byte
			copy(sel[:], h[:4])
			a.Functions[sel] = Function{Name: e.Name, Inputs: e.Inputs, Selector: sel, Sig: sig}
		case "event":
			sig := e.Name + "(" + typeList(e.Inputs) + ")"
			a.Events[Keccak([]byte(sig))] = Event{Name: e.Name, Inputs: e.Inputs, Topic: Keccak([]byte(sig)), Sig: sig}
		}
	}
	return a, nil
}

// typeList renders the canonical types of a parameter list, tuples
// expanded, for a selector.
func typeList(ps []Param) string {
	parts := make([]string, len(ps))
	for i, p := range ps {
		parts[i] = canonical(p)
	}
	return strings.Join(parts, ",")
}

func canonical(p Param) string {
	if strings.HasPrefix(p.Type, "tuple") {
		return "(" + typeList(p.Components) + ")" + strings.TrimPrefix(p.Type, "tuple")
	}
	return p.Type
}

// Keccak is the hash Ethereum uses for selectors, topics and addresses.
func Keccak(b []byte) [32]byte {
	h := sha3.NewLegacyKeccak256()
	h.Write(b)
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

// Selector reads the four-byte function selector of calldata.
func Selector(data []byte) ([4]byte, bool) {
	var s [4]byte
	if len(data) < 4 {
		return s, false
	}
	copy(s[:], data[:4])
	return s, true
}

// Decoded is one argument rendered as a string, flat: nested fields
// carry a dotted path and array elements an index, the way the Solana
// decoder renders them, so the same UI reads both.
type Decoded map[string]string

// DecodeCall decodes calldata against the function its selector names.
func (a *ABI) DecodeCall(data []byte) (Function, Decoded, error) {
	sel, ok := Selector(data)
	if !ok {
		return Function{}, nil, errors.New("calldata shorter than a selector")
	}
	f, ok := a.Functions[sel]
	if !ok {
		return Function{}, nil, fmt.Errorf("unknown selector 0x%s", hex.EncodeToString(sel[:]))
	}
	out := Decoded{}
	if err := decodeTuple(data[4:], f.Inputs, "", out); err != nil {
		return f, out, err
	}
	return f, out, nil
}

// DecodeLog decodes a log's topics and data against the event its first
// topic names.
func (a *ABI) DecodeLog(topics [][32]byte, data []byte) (Event, Decoded, error) {
	if len(topics) == 0 {
		return Event{}, nil, errors.New("anonymous log")
	}
	e, ok := a.Events[topics[0]]
	if !ok {
		return Event{}, nil, fmt.Errorf("unknown topic 0x%s", hex.EncodeToString(topics[0][:4]))
	}
	out := Decoded{}
	var unindexed []Param
	ti := 1
	for _, p := range e.Inputs {
		if p.Indexed {
			if ti < len(topics) {
				if isDynamic(p) {
					out[p.Name] = "0x" + hex.EncodeToString(topics[ti][:])
				} else {
					out[p.Name] = renderStatic(p, topics[ti][:])
				}
			}
			ti++
			continue
		}
		unindexed = append(unindexed, p)
	}
	return e, out, decodeTuple(data, unindexed, "", out)
}

// decodeTuple reads ABI-encoded values for params from data, in order:
// static values inline, dynamic ones through their offset.
func decodeTuple(data []byte, params []Param, prefix string, out Decoded) error {
	head := 0
	for _, p := range params {
		name := prefix + p.Name
		if p.Name == "" {
			name = prefix + strconv.Itoa(head/32)
		}
		if isDynamic(p) {
			if head+32 > len(data) {
				return errors.New("calldata truncated")
			}
			off := new(big.Int).SetBytes(data[head : head+32])
			if !off.IsInt64() || off.Int64() > int64(len(data)) {
				return errors.New("offset out of range")
			}
			if err := decodeDynamic(data[off.Int64():], p, name, out); err != nil {
				return err
			}
			head += 32
			continue
		}
		size := staticSize(p)
		if head+size > len(data) {
			return errors.New("calldata truncated")
		}
		if err := decodeStatic(data[head:head+size], p, name, out); err != nil {
			return err
		}
		head += size
	}
	return nil
}

// decodeStatic renders a static value: a word, a fixed array of static
// elements, or a tuple of static components.
func decodeStatic(data []byte, p Param, name string, out Decoded) error {
	base, count, isArr := arrayOf(p.Type)
	if isArr && count >= 0 {
		elem := Param{Type: base, Components: p.Components}
		size := staticSize(elem)
		for i := 0; i < count; i++ {
			if err := decodeStatic(data[i*size:(i+1)*size], elem, name+"["+strconv.Itoa(i)+"]", out); err != nil {
				return err
			}
		}
		return nil
	}
	if p.Type == "tuple" {
		return decodeTuple(data, p.Components, name+".", out)
	}
	out[name] = renderStatic(p, data[:32])
	return nil
}

// decodeDynamic renders a value that lives behind an offset: bytes and
// strings with a length, arrays with a count, tuples that hold dynamic
// parts.
func decodeDynamic(data []byte, p Param, name string, out Decoded) error {
	switch {
	case p.Type == "bytes" || p.Type == "string":
		if len(data) < 32 {
			return errors.New("calldata truncated")
		}
		n := new(big.Int).SetBytes(data[:32])
		if !n.IsInt64() || 32+n.Int64() > int64(len(data)) {
			return errors.New("length out of range")
		}
		b := data[32 : 32+n.Int64()]
		if p.Type == "string" {
			out[name] = string(b)
		} else {
			out[name] = "0x" + hex.EncodeToString(b)
		}
		return nil
	case p.Type == "tuple":
		return decodeTuple(data, p.Components, name+".", out)
	}
	base, count, isArr := arrayOf(p.Type)
	if !isArr {
		return fmt.Errorf("unsupported type %s", p.Type)
	}
	elem := Param{Type: base, Components: p.Components}
	body := data
	if count < 0 {
		if len(data) < 32 {
			return errors.New("calldata truncated")
		}
		n := new(big.Int).SetBytes(data[:32])
		if !n.IsInt64() || n.Int64() > 4096 {
			return errors.New("array length out of range")
		}
		count = int(n.Int64())
		body = data[32:]
	}
	out[name+".length"] = strconv.Itoa(count)
	if isDynamic(elem) {
		for i := 0; i < count; i++ {
			if (i+1)*32 > len(body) {
				return errors.New("calldata truncated")
			}
			off := new(big.Int).SetBytes(body[i*32 : (i+1)*32])
			if !off.IsInt64() || off.Int64() > int64(len(body)) {
				return errors.New("offset out of range")
			}
			if err := decodeDynamic(body[off.Int64():], elem, name+"["+strconv.Itoa(i)+"]", out); err != nil {
				return err
			}
		}
		return nil
	}
	size := staticSize(elem)
	for i := 0; i < count; i++ {
		if (i+1)*size > len(body) {
			return errors.New("calldata truncated")
		}
		if err := decodeStatic(body[i*size:(i+1)*size], elem, name+"["+strconv.Itoa(i)+"]", out); err != nil {
			return err
		}
	}
	return nil
}

// renderStatic renders one 32-byte word by its type.
func renderStatic(p Param, word []byte) string {
	t := p.Type
	switch {
	case t == "address":
		return Checksum(word[12:32])
	case t == "bool":
		return strconv.FormatBool(word[31] != 0)
	case strings.HasPrefix(t, "uint"):
		return new(big.Int).SetBytes(word).String()
	case strings.HasPrefix(t, "int"):
		n := new(big.Int).SetBytes(word)
		if word[0]&0x80 != 0 {
			n.Sub(n, new(big.Int).Lsh(big.NewInt(1), 256))
		}
		return n.String()
	case strings.HasPrefix(t, "bytes"):
		n, err := strconv.Atoi(strings.TrimPrefix(t, "bytes"))
		if err != nil || n <= 0 || n > 32 {
			n = 32
		}
		return "0x" + hex.EncodeToString(word[:n])
	}
	return "0x" + hex.EncodeToString(word)
}

// arrayOf splits T[n] or T[] into its element type and count (-1 when
// dynamic). Only the outermost dimension.
func arrayOf(t string) (base string, count int, ok bool) {
	if !strings.HasSuffix(t, "]") {
		return t, 0, false
	}
	i := strings.LastIndex(t, "[")
	if i < 0 {
		return t, 0, false
	}
	inner := t[i+1 : len(t)-1]
	if inner == "" {
		return t[:i], -1, true
	}
	n, err := strconv.Atoi(inner)
	if err != nil {
		return t, 0, false
	}
	return t[:i], n, true
}

// isDynamic says whether a value lives behind an offset.
func isDynamic(p Param) bool {
	if p.Type == "bytes" || p.Type == "string" {
		return true
	}
	if base, count, ok := arrayOf(p.Type); ok {
		if count < 0 {
			return true
		}
		return isDynamic(Param{Type: base, Components: p.Components})
	}
	if p.Type == "tuple" {
		for _, c := range p.Components {
			if isDynamic(c) {
				return true
			}
		}
	}
	return false
}

// staticSize is the bytes a static value takes in the head.
func staticSize(p Param) int {
	if base, count, ok := arrayOf(p.Type); ok && count >= 0 {
		return count * staticSize(Param{Type: base, Components: p.Components})
	}
	if p.Type == "tuple" {
		n := 0
		for _, c := range p.Components {
			n += staticSize(c)
		}
		return n
	}
	return 32
}

// Checksum renders an address with EIP-55 casing.
func Checksum(addr []byte) string {
	lower := hex.EncodeToString(addr)
	h := Keccak([]byte(lower))
	out := make([]byte, len(lower))
	for i := 0; i < len(lower); i++ {
		c := lower[i]
		nibble := h[i/2]
		if i%2 == 0 {
			nibble >>= 4
		} else {
			nibble &= 0x0f
		}
		if c >= 'a' && c <= 'f' && nibble >= 8 {
			c -= 32
		}
		out[i] = c
	}
	return "0x" + string(out)
}

// Revert is a decoded revert reason: the solidity Error(string) message,
// a Panic code with its meaning, or a custom error by name when the ABI
// declares it, else by selector.
type Revert struct {
	Name   string
	Msg    string
	Fields Decoded
	Sel    [4]byte
	Raw    []byte
}

var panicWords = map[uint64]string{0x00: "generic", 0x01: "assert", 0x11: "arithmetic overflow", 0x12: "division by zero", 0x21: "enum out of range", 0x22: "storage encoding", 0x31: "pop on empty array", 0x32: "index out of bounds", 0x41: "out of memory", 0x51: "uninitialized function"}

// DecodeRevert reads revert data. abi may be nil.
func DecodeRevert(abi *ABI, data []byte) Revert {
	r := Revert{Raw: data}
	if len(data) < 4 {
		r.Name = "reverted"
		return r
	}
	copy(r.Sel[:], data[:4])
	switch hex.EncodeToString(data[:4]) {
	case "08c379a0": // Error(string)
		r.Name = "Error"
		out := Decoded{}
		if err := decodeTuple(data[4:], []Param{{Name: "message", Type: "string"}}, "", out); err == nil {
			r.Msg = out["message"]
		}
		return r
	case "4e487b71": // Panic(uint256)
		r.Name = "Panic"
		out := Decoded{}
		if err := decodeTuple(data[4:], []Param{{Name: "code", Type: "uint256"}}, "", out); err == nil {
			code, _ := strconv.ParseUint(out["code"], 10, 64)
			r.Msg = panicWords[code]
			if r.Msg == "" {
				r.Msg = "code " + out["code"]
			}
		}
		return r
	}
	if abi != nil {
		if e, ok := abi.Errors[r.Sel]; ok {
			r.Name = e.Name
			out := Decoded{}
			if err := decodeTuple(data[4:], e.Inputs, "", out); err == nil && len(out) > 0 {
				r.Fields = out
			}
			return r
		}
	}
	r.Name = "0x" + hex.EncodeToString(data[:4])
	return r
}
