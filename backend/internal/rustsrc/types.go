package rustsrc

import (
	"regexp"
	"strings"
)

// TypeDef is one plain struct or enum the source declares: what an
// instruction argument or an account's data is shaped like. Field types
// are the Rust spelling, tidied; the adapter maps them onto its own.
type TypeDef struct {
	Name     string
	Kind     string // struct, enum
	Fields   []TypeField
	Variants []Variant
}

type TypeField struct {
	Name string
	Type string
}

// Variant is one enum arm: unit, tuple (unnamed fields) or struct.
type Variant struct {
	Name   string
	Fields []TypeField
}

var (
	enumRE     = regexp.MustCompile(`(?m)^[ \t]*(?:pub(?:\([^)]*\))?[ \t]+)?enum[ \t]+([A-Za-z_][A-Za-z0-9_]*)`)
	attrLineRE = regexp.MustCompile(`(?m)^[ \t]*#\[[^\]]*\][ \t]*\n?`)
	variantRE  = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)\s*(\{.*\}|\(.*\))?$`)
)

// plainFields reads name: Type pairs at depth 0 of a struct body.
func (s *source) plainFields(from, to int) []TypeField {
	var out []TypeField
	body := s.masked[from:to]
	for _, m := range fieldRE.FindAllSubmatchIndex(body, -1) {
		nameOff := from + m[2]
		if s.depthAll(from, nameOff) != 0 || (from+m[1] < to && s.masked[from+m[1]] == ':') {
			continue
		}
		typStart, typEnd, depth := from+m[1], from+m[1], 0
	scan:
		for typEnd < to {
			switch s.masked[typEnd] {
			case '<', '(', '[', '{':
				depth++
			case '>', ')', ']', '}':
				depth--
			case ',':
				if depth == 0 {
					break scan
				}
			}
			typEnd++
		}
		out = append(out, TypeField{Name: string(body[m[2]:m[3]]), Type: tidy(string(s.masked[typStart:typEnd]))})
	}
	return out
}

// typeDefs lists the structs (other than Accounts structs) and enums a
// file declares, by name. A struct with tuple fields or generics keeps its
// name and whatever fields could be read.
func (s *source) typeDefs() map[string]TypeDef {
	out := map[string]TypeDef{}
	for _, m := range structRE.FindAllSubmatchIndex(s.masked, -1) {
		attrs, _ := s.attributes(m[0])
		if strings.Contains(attrs, "derive(") && strings.Contains(attrs, "Accounts") && !strings.Contains(attrs, "AccountsClose") {
			continue
		}
		open, close, ok := s.body(m[1])
		if !ok {
			continue
		}
		name := string(s.masked[m[2]:m[3]])
		out[name] = TypeDef{Name: name, Kind: "struct", Fields: s.plainFields(open+1, close)}
	}
	for _, m := range enumRE.FindAllSubmatchIndex(s.masked, -1) {
		open, close, ok := s.body(m[1])
		if !ok {
			continue
		}
		name := string(s.masked[m[2]:m[3]])
		def := TypeDef{Name: name, Kind: "enum"}
		body := attrLineRE.ReplaceAllString(string(s.masked[open+1:close]), "")
		for _, part := range splitTop(body, ',') {
			part = tidy(part)
			if part == "" {
				continue
			}
			vm := variantRE.FindStringSubmatch(part)
			if vm == nil {
				continue
			}
			v := Variant{Name: vm[1]}
			if payload := vm[2]; payload != "" {
				inner := tidy(payload[1 : len(payload)-1])
				if payload[0] == '{' {
					for _, f := range splitTop(inner, ',') {
						if n, t, ok := strings.Cut(f, ":"); ok {
							v.Fields = append(v.Fields, TypeField{Name: tidy(strings.TrimPrefix(tidy(n), "pub ")), Type: tidy(t)})
						}
					}
				} else {
					for i, t := range splitTop(inner, ',') {
						v.Fields = append(v.Fields, TypeField{Name: string(rune('0' + i)), Type: tidy(t)})
					}
				}
			}
			def.Variants = append(def.Variants, v)
		}
		out[name] = def
	}
	return out
}
