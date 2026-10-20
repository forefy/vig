package solana

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/forefy/vig/backend/internal/chain"
	"github.com/forefy/vig/backend/internal/rustsrc"
)

// staticIDL builds the instruction spec a replay needs from the source
// when the program publishes no IDL: every Anchor handler with the
// discriminator its name implies, its accounts in the order the instruction
// receives them, its scalar arguments, and the PDA seeds the Accounts
// struct declares. Account layouts and error codes are not recoverable
// this way and stay empty.
// peers maps sibling crates to their program ids, for seeds::program.
// types is every struct and enum the workspace declares, so arguments of
// those shapes decode.
func staticIDL(c *rustsrc.Crate, ps peers, types map[string]rustsrc.TypeDef) *idlSpec {
	if c == nil || c.Framework != "anchor" || len(c.Instructions) == 0 {
		return nil
	}
	spec := &idlSpec{Accounts: map[[8]byte]accountLayout{}, types: map[string]json.RawMessage{}}
	for name, def := range types {
		spec.types[name] = idlType(def)
	}
	for _, ix := range c.Instructions {
		is := instructionSpec{Name: ix.Name, Discriminator: anchorDiscriminator("global:" + ix.Name)}
		argNames := map[string]bool{}
		for _, a := range ix.Args {
			argNames[a.Name] = true
			is.Args = append(is.Args, fieldSpec{Name: a.Name, Type: rustType(a.Type)})
		}
		acctNames := map[string]bool{}
		for _, a := range ix.Accounts {
			acctNames[a.Name] = true
			acctNames[a.Name[strings.LastIndex(a.Name, ".")+1:]] = true
		}
		for _, a := range ix.Accounts {
			as := accountSpec{Name: a.Name, Signer: a.Signer, Writable: a.Writable, Seeds: a.Seeds}
			if a.Seeds != "" {
				as.PDASeeds = parseSeeds(a.Seeds, acctNames, argNames, c.Consts)
				if a.SeedsProgram != "" {
					as.PDAProgram = seedsProgram(a.SeedsProgram, acctNames, ps)
				}
			}
			is.Accounts = append(is.Accounts, as)
		}
		spec.Instructions = append(spec.Instructions, is)
	}
	return spec
}

// rustType maps a Rust type onto the IDL's type shapes: the scalars,
// pubkey, string, bytes, option, vec, array, and a defined name for
// anything else (its path and generics dropped), which the decoder resolves
// through the spec's types or stops at.
func rustType(t string) json.RawMessage {
	t = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(t), "&"))
	switch t {
	case "bool", "u8", "i8", "u16", "i16", "u32", "i32", "u64", "i64", "u128", "i128", "f32", "f64":
		return json.RawMessage(`"` + t + `"`)
	case "Pubkey":
		return json.RawMessage(`"pubkey"`)
	case "String", "str":
		return json.RawMessage(`"string"`)
	case "Vec<u8>":
		return json.RawMessage(`"bytes"`)
	}
	if strings.HasPrefix(t, "[") && strings.HasSuffix(t, "]") {
		if elem, n, ok := strings.Cut(t[1:len(t)-1], ";"); ok {
			var size int
			if _, err := fmt.Sscanf(strings.TrimSpace(n), "%d", &size); err == nil {
				b, _ := json.Marshal(map[string]any{"array": []any{json.RawMessage(rustType(elem)), size}})
				return b
			}
		}
	}
	if i := strings.IndexByte(t, '<'); i > 0 && strings.HasSuffix(t, ">") {
		head, inner := t[:i], t[i+1:len(t)-1]
		if j := strings.LastIndex(head, "::"); j >= 0 {
			head = head[j+2:]
		}
		switch head {
		case "Option":
			b, _ := json.Marshal(map[string]json.RawMessage{"option": rustType(inner)})
			return b
		case "Vec":
			b, _ := json.Marshal(map[string]json.RawMessage{"vec": rustType(inner)})
			return b
		case "Box":
			return rustType(inner)
		}
		t = head // a generic user type: its name, generics dropped
	}
	if j := strings.LastIndex(t, "::"); j >= 0 {
		t = t[j+2:]
	}
	b, _ := json.Marshal(map[string]string{"defined": t})
	return b
}

// idlType renders a source type definition the way an IDL does.
func idlType(d rustsrc.TypeDef) json.RawMessage {
	field := func(f rustsrc.TypeField) map[string]any {
		return map[string]any{"name": f.Name, "type": rustType(f.Type)}
	}
	if d.Kind == "enum" {
		var vs []map[string]any
		for _, v := range d.Variants {
			m := map[string]any{"name": v.Name}
			if len(v.Fields) > 0 {
				var fs []map[string]any
				for _, f := range v.Fields {
					fs = append(fs, field(f))
				}
				m["fields"] = fs
			}
			vs = append(vs, m)
		}
		b, _ := json.Marshal(map[string]any{"kind": "enum", "variants": vs})
		return b
	}
	var fs []map[string]any
	for _, f := range d.Fields {
		fs = append(fs, field(f))
	}
	b, _ := json.Marshal(map[string]any{"kind": "struct", "fields": fs})
	return b
}

var (
	seedLitRE   = regexp.MustCompile(`^&?b?"((?:[^"\\]|\\.)*)"(?:\.as_ref\(\)|\.as_bytes\(\))?$`)
	seedAcctRE  = regexp.MustCompile(`^&?(?:ctx\.accounts\.)?([a-z_][a-z0-9_.]*?)(?:\.to_account_info\(\))?\.key(?:\(\))?(?:\.as_ref\(\)|\.to_bytes\(\)|\.as_bytes\(\))?$`)
	seedArgRE   = regexp.MustCompile(`^&?\[?([a-z_][a-z0-9_]*)\]?(?:\.to_le_bytes\(\)|\.to_be_bytes\(\)|\.as_bytes\(\)|\.as_ref\(\)|\.as_slice\(\))?$`)
	seedConstRE = regexp.MustCompile(`^&?(?:[A-Za-z_][A-Za-z0-9_]*::)*([A-Z_][A-Z0-9_]*)(?:\.as_ref\(\)|\.as_bytes\(\))?$`)
)

// parseSeeds reads the seeds clause of an #[account(...)] attribute as the
// replay's seed specs. A seed it cannot name (a constant, an expression)
// yields a spec of no kind, which the check treats as no verdict.
func parseSeeds(text string, accounts, args map[string]bool, consts map[string][]byte) []seedSpec {
	var out []seedSpec
	for _, part := range splitTopLevel(text) {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if m := seedLitRE.FindStringSubmatch(part); m != nil {
			out = append(out, seedSpec{Kind: "const", Value: unescape(m[1])})
			continue
		}
		if m := seedConstRE.FindStringSubmatch(part); m != nil {
			if v, ok := consts[m[1]]; ok && len(v) > 0 {
				out = append(out, seedSpec{Kind: "const", Value: v})
				continue
			}
		}
		if m := seedAcctRE.FindStringSubmatch(part); m != nil && accounts[m[1]] {
			out = append(out, seedSpec{Kind: "account", Path: m[1]})
			continue
		}
		if m := seedArgRE.FindStringSubmatch(part); m != nil && args[m[1]] {
			out = append(out, seedSpec{Kind: "arg", Path: m[1]})
			continue
		}
		out = append(out, seedSpec{})
	}
	return out
}

var seedsProgRE = regexp.MustCompile(`^&?(?:ctx\.accounts\.)?([a-z_][a-z0-9_]*)(?:\.key(?:\(\))?)?(?:\.as_ref\(\))?$`)

// seedsProgram reads a seeds::program clause: a program account named in
// the struct, or a sibling crate's ID (loom_fiber_program::ID) that the
// peers map resolves to that program's id. Anything else gives a spec of
// no kind, so the check yields no verdict rather than the wrong one.
func seedsProgram(text string, accounts map[string]bool, ps peers) *seedSpec {
	text = strings.TrimSpace(text)
	if m := seedsProgRE.FindStringSubmatch(text); m != nil && accounts[m[1]] {
		return &seedSpec{Kind: "account", Path: m[1]}
	}
	if i := strings.LastIndex(text, "::"); i > 0 && strings.HasSuffix(strings.TrimSuffix(text, ".key()"), "ID") {
		if pid, ok := ps.resolve(text[:i], ""); ok {
			if b, err := chain.Decode(pid); err == nil {
				return &seedSpec{Kind: "const", Value: b}
			}
		}
	}
	return &seedSpec{}
}

func splitTopLevel(text string) []string {
	var out []string
	depth, start := 0, 0
	inStr := false
	for i := 0; i < len(text); i++ {
		c := text[i]
		if inStr {
			if c == '\\' {
				i++
			} else if c == '"' {
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			depth--
		case ',':
			if depth == 0 {
				out = append(out, text[start:i])
				start = i + 1
			}
		}
	}
	if strings.TrimSpace(text[start:]) != "" {
		out = append(out, text[start:])
	}
	return out
}

func unescape(s string) []byte {
	if !strings.Contains(s, `\`) {
		return []byte(s)
	}
	var b []byte
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			i++
			switch s[i] {
			case 'n':
				b = append(b, '\n')
			case 't':
				b = append(b, '\t')
			case '0':
				b = append(b, 0)
			default:
				b = append(b, s[i])
			}
			continue
		}
		b = append(b, s[i])
	}
	return b
}
