package rustsrc

import "testing"

func TestFingerprintFollowsReach(t *testing.T) {
	head := load(t, "diff_head")
	base := load(t, "diff_base")
	hc, bc := &head.Crates[0], &base.Crates[0]
	fp := func(c *Crate, m *Model, name string) string { return c.Fingerprint(ix(t, c, name), m) }
	// A new comment above deposit is not a change.
	if fp(hc, head, "deposit") != fp(bc, base, "deposit") {
		t.Error("deposit: comments must not count")
	}
	// withdraw's own body is identical; utils::fee, which it reaches, is not.
	if fp(hc, head, "withdraw") == fp(bc, base, "withdraw") {
		t.Error("withdraw: a change in a reached function must count")
	}
	if fp(hc, head, "deposit") == fp(hc, head, "close") {
		t.Error("distinct handlers must differ")
	}
	if fp(hc, head, "withdraw") != fp(hc, head, "withdraw") {
		t.Error("not deterministic")
	}
	// The accounts struct is part of the hash.
	before := fp(hc, head, "withdraw")
	w := ix(t, hc, "withdraw")
	w.Accounts = append([]Account(nil), w.Accounts...)
	w.Accounts[0].Constraints++
	if hc.Fingerprint(w, head) == before {
		t.Error("a constraint change must count")
	}
}

func TestBumpConstraint(t *testing.T) {
	m := load(t, "diff_head")
	c := &m.Crates[0]
	if a := acct(t, ix(t, c, "deposit"), "book"); !a.Bump || a.Seeds == "" {
		t.Errorf("bump with seeds: %+v", a)
	}
	if a := acct(t, ix(t, c, "withdraw"), "book"); a.Bump {
		t.Errorf("no bump: %+v", a)
	}
	v := load(t, "anchor_vault")
	if a := acct(t, ix(t, &v.Crates[0], "initialize"), "vault"); !a.Bump {
		t.Errorf("multi-line bump: %+v", a)
	}
}
