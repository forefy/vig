package evm

import (
	"errors"
	"strings"
)

// ParseSignature turns a text signature such as
// swap(uint256,address[],(address,uint24)[],bytes) into a Function with
// unnamed parameters: what the 4byte directory knows about a selector
// when no verified ABI does.
func ParseSignature(sig string) (Function, error) {
	open := strings.Index(sig, "(")
	if open <= 0 || !strings.HasSuffix(sig, ")") {
		return Function{}, errors.New("not a signature")
	}
	name := sig[:open]
	params, rest, err := parseParams(sig[open+1 : len(sig)-1])
	if err != nil {
		return Function{}, err
	}
	if rest != "" {
		return Function{}, errors.New("trailing text in signature")
	}
	canon := name + "(" + typeList(params) + ")"
	h := Keccak([]byte(canon))
	var sel [4]byte
	copy(sel[:], h[:4])
	return Function{Name: name, Inputs: params, Selector: sel, Sig: canon}, nil
}

// parseParams reads a comma list of types, tuples in parentheses with
// array suffixes after them, up to an unmatched close.
func parseParams(s string) ([]Param, string, error) {
	var out []Param
	for {
		s = strings.TrimSpace(s)
		if s == "" {
			return out, "", nil
		}
		var p Param
		if strings.HasPrefix(s, "(") {
			comps, rest, err := parseParams(s[1:])
			if err != nil {
				return nil, "", err
			}
			if !strings.HasPrefix(rest, ")") {
				return nil, "", errors.New("unclosed tuple")
			}
			rest = rest[1:]
			suffix := ""
			for strings.HasPrefix(rest, "[") {
				end := strings.Index(rest, "]")
				if end < 0 {
					return nil, "", errors.New("unclosed array")
				}
				suffix += rest[:end+1]
				rest = rest[end+1:]
			}
			p = Param{Type: "tuple" + suffix, Components: comps}
			s = rest
		} else {
			end := strings.IndexAny(s, ",)")
			if end < 0 {
				end = len(s)
			}
			p = Param{Type: strings.TrimSpace(s[:end])}
			s = s[end:]
		}
		out = append(out, p)
		s = strings.TrimSpace(s)
		switch {
		case strings.HasPrefix(s, ","):
			s = s[1:]
		case strings.HasPrefix(s, ")"), s == "":
			return out, s, nil
		default:
			return nil, "", errors.New("unexpected text in signature")
		}
	}
}
