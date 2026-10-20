package rustsrc

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strconv"
	"strings"
)

// Fingerprint hashes everything a handler's behaviour depends on in the
// source: the clean body of every function it reaches, its arguments and
// its accounts with their constraints. Two checkouts give the same hash
// only when nothing the handler can execute changed; comments and line
// positions do not count.
func (c *Crate) Fingerprint(ix Instruction, m *Model) string {
	h := sha256.New()
	w := func(parts ...string) {
		for _, p := range parts {
			h.Write([]byte(p))
			h.Write([]byte{0})
		}
		h.Write([]byte{'\n'})
	}
	w("ix", ix.Name)
	for _, a := range ix.Args {
		w("arg", a.Name, a.Type)
	}
	for _, a := range ix.Accounts {
		w("acct", a.Name, a.Wrapper, a.Inner, flags(a), a.Seeds, strings.Join(a.HasOne, ","), a.Owner, a.Address, strconv.Itoa(a.Constraints))
	}
	reach := append([]string(nil), ix.Reach...)
	sort.Strings(reach)
	for _, k := range reach {
		f := m.Functions[k]
		if f == nil {
			w("fn", k)
			continue
		}
		w("fn", k, f.text)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// flags spells an account's boolean constraints as one token.
func flags(a Account) string {
	var out []string
	for _, f := range []struct {
		on   bool
		name string
	}{{a.Signer, "signer"}, {a.Writable, "mut"}, {a.Raw, "raw"}, {a.Optional, "optional"}, {a.Init, "init"}, {a.Close, "close"}, {a.Realloc, "realloc"}, {a.ZeroCopy, "zero_copy"}, {a.Bump, "bump"}} {
		if f.on {
			out = append(out, f.name)
		}
	}
	return strings.Join(out, ",")
}
