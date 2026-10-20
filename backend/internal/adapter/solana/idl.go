package solana

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"strings"
	"unicode"
)

// idlSpec is what the audit needs from an Anchor IDL: instructions with
// their accounts and args, account layouts by discriminator, error codes.
type idlSpec struct {
	Instructions []instructionSpec
	Accounts     map[[8]byte]accountLayout
	Errors       map[int]idlError
	types        map[string]json.RawMessage
}

type instructionSpec struct {
	Name          string
	Discriminator [8]byte
	Accounts      []accountSpec
	Args          []fieldSpec
}

type accountSpec struct {
	Name     string
	Signer   bool
	Writable bool
	Seeds    string // "seeds: state, "reserve"" when the IDL declares the PDA
	// The same seeds kept structured so a replay can derive the address:
	// PDASeeds in order, PDAProgram the deriving program when the IDL
	// names one other than the program itself.
	PDASeeds   []seedSpec
	PDAProgram *seedSpec
}

// seedSpec is one PDA seed: literal bytes, an account's key by name, or an
// instruction argument by name.
type seedSpec struct {
	Kind  string // const, account, arg
	Value []byte // const only
	Path  string // account or arg name, possibly dotted
}

type accountLayout struct {
	Name   string
	Fields []fieldSpec
}

type fieldSpec struct {
	Name string
	Type json.RawMessage
}

type idlError struct {
	Code int
	Name string
	Msg  string
}

// parseIDL reads both Anchor IDL shapes: the legacy one with camelCase
// names and isMut/isSigner, and the 0.30 one with explicit discriminators
// and writable/signer flags.
func parseIDL(raw []byte) (*idlSpec, error) {
	var idl struct {
		Instructions []struct {
			Name          string          `json:"name"`
			Discriminator []byte          `json:"discriminator"`
			Accounts      json.RawMessage `json:"accounts"`
			Args          []fieldSpec     `json:"args"`
		} `json:"instructions"`
		Accounts []struct {
			Name          string          `json:"name"`
			Discriminator []byte          `json:"discriminator"`
			Type          json.RawMessage `json:"type"`
		} `json:"accounts"`
		Types []struct {
			Name string          `json:"name"`
			Type json.RawMessage `json:"type"`
		} `json:"types"`
		Errors []idlError `json:"errors"`
	}
	if err := json.Unmarshal(raw, &idl); err != nil {
		return nil, err
	}
	spec := &idlSpec{Accounts: map[[8]byte]accountLayout{}, Errors: map[int]idlError{}}
	for _, ins := range idl.Instructions {
		if ins.Name == "" {
			continue
		}
		s := instructionSpec{Name: snake(ins.Name), Args: ins.Args}
		if len(ins.Discriminator) == 8 {
			copy(s.Discriminator[:], ins.Discriminator)
		} else {
			s.Discriminator = anchorDiscriminator("global:" + s.Name)
		}
		s.Accounts = flattenAccounts(ins.Accounts, "")
		spec.Instructions = append(spec.Instructions, s)
	}
	types := map[string]json.RawMessage{}
	for _, t := range idl.Types {
		types[t.Name] = t.Type
	}
	for _, a := range idl.Accounts {
		layout := accountLayout{Name: a.Name}
		typ := a.Type
		if len(typ) == 0 {
			typ = types[a.Name]
		}
		layout.Fields = structFields(typ)
		var d [8]byte
		if len(a.Discriminator) == 8 {
			copy(d[:], a.Discriminator)
		} else {
			d = anchorDiscriminator("account:" + a.Name)
		}
		spec.Accounts[d] = layout
	}
	for _, e := range idl.Errors {
		spec.Errors[e.Code] = e
	}
	spec.types = types
	return spec, nil
}

// structFields returns a struct type's fields, or nil for anything else.
func structFields(typ json.RawMessage) []fieldSpec {
	var t struct {
		Kind   string      `json:"kind"`
		Fields []fieldSpec `json:"fields"`
	}
	if json.Unmarshal(typ, &t) != nil || t.Kind != "struct" {
		return nil
	}
	return t.Fields
}

// flattenAccounts walks nested account groups into one ordered list and
// keeps the PDA seeds an IDL declares.
func flattenAccounts(raw json.RawMessage, prefix string) []accountSpec {
	var items []struct {
		Name     string          `json:"name"`
		IsMut    bool            `json:"isMut"`
		IsSigner bool            `json:"isSigner"`
		Writable bool            `json:"writable"`
		Signer   bool            `json:"signer"`
		Accounts json.RawMessage `json:"accounts"`
		PDA      *struct {
			Seeds   []rawSeed `json:"seeds"`
			Program *rawSeed  `json:"program"`
		} `json:"pda"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &items) != nil {
		return nil
	}
	var out []accountSpec
	for _, a := range items {
		name := prefix + snake(a.Name)
		if len(a.Accounts) > 0 {
			out = append(out, flattenAccounts(a.Accounts, name+".")...)
			continue
		}
		spec := accountSpec{Name: name, Signer: a.IsSigner || a.Signer, Writable: a.IsMut || a.Writable}
		if a.PDA != nil && len(a.PDA.Seeds) > 0 {
			var seeds []string
			for _, s := range a.PDA.Seeds {
				ss := s.spec()
				switch ss.Kind {
				case "const":
					seeds = append(seeds, `"`+printable(ss.Value)+`"`)
				default:
					seeds = append(seeds, ss.Path)
				}
				spec.PDASeeds = append(spec.PDASeeds, ss)
			}
			spec.Seeds = strings.Join(seeds, ", ")
			if a.PDA.Program != nil {
				ps := a.PDA.Program.spec()
				spec.PDAProgram = &ps
			}
		}
		out = append(out, spec)
	}
	return out
}

// rawSeed is a PDA seed as either IDL shape writes it: 0.30 gives const
// values as byte arrays; the legacy shape types them and writes a string
// or a number.
type rawSeed struct {
	Kind  string          `json:"kind"`
	Path  string          `json:"path"`
	Type  json.RawMessage `json:"type"`
	Value json.RawMessage `json:"value"`
}

func (r rawSeed) spec() seedSpec {
	s := seedSpec{Kind: r.Kind, Path: r.Path}
	if r.Kind != "const" || len(r.Value) == 0 {
		return s
	}
	var bytesVal []byte
	if json.Unmarshal(r.Value, &bytesVal) == nil && r.Value[0] == '[' {
		s.Value = bytesVal
		return s
	}
	var str string
	if json.Unmarshal(r.Value, &str) == nil {
		s.Value = []byte(str)
		return s
	}
	var num uint64
	if json.Unmarshal(r.Value, &num) == nil {
		width, ok := scalarWidth(r.Type)
		if !ok {
			width = 8
		}
		buf := make([]byte, 8)
		binary.LittleEndian.PutUint64(buf, num)
		s.Value = buf[:width]
	}
	return s
}

func printable(b []byte) string {
	for _, c := range b {
		if c < 0x20 || c > 0x7e {
			return "0x" + hexString(b)
		}
	}
	return string(b)
}

func hexString(b []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, 0, len(b)*2)
	for _, c := range b {
		out = append(out, digits[c>>4], digits[c&15])
	}
	return string(out)
}

// typeWord renders an IDL type tersely: u64, pubkey, vec<u8>, Foo.
func typeWord(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil {
		return "?"
	}
	for _, k := range []string{"vec", "option", "coption"} {
		if inner, ok := m[k]; ok {
			return k + "<" + typeWord(inner) + ">"
		}
	}
	if inner, ok := m["array"]; ok {
		var arr []json.RawMessage
		if json.Unmarshal(inner, &arr) == nil && len(arr) > 0 {
			return "[" + typeWord(arr[0]) + "]"
		}
	}
	if d, ok := m["defined"]; ok {
		var name string
		if json.Unmarshal(d, &name) == nil {
			return name
		}
		var obj struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(d, &obj) == nil {
			return obj.Name
		}
	}
	return "?"
}

// anchorDiscriminator is sha256(preimage)[:8], e.g. "global:deposit".
func anchorDiscriminator(preimage string) [8]byte {
	sum := sha256.Sum256([]byte(preimage))
	var d [8]byte
	copy(d[:], sum[:8])
	return d
}

// snake turns camelCase into snake_case; snake_case passes through.
func snake(s string) string {
	var b strings.Builder
	for i, r := range s {
		if unicode.IsUpper(r) {
			if i > 0 {
				b.WriteByte('_')
			}
			b.WriteRune(unicode.ToLower(r))
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
