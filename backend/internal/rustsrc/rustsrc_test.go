package rustsrc

import (
	"testing"
)

func load(t *testing.T, dir string) *Model {
	t.Helper()
	m, err := Load("testdata/"+dir, "")
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func ix(t *testing.T, c *Crate, name string) Instruction {
	t.Helper()
	for _, i := range c.Instructions {
		if i.Name == name {
			return i
		}
	}
	t.Fatalf("instruction %s not found in %v", name, c.Instructions)
	return Instruction{}
}

func acct(t *testing.T, i Instruction, name string) Account {
	t.Helper()
	for _, a := range i.Accounts {
		if a.Name == name {
			return a
		}
	}
	t.Fatalf("account %s not found in %s", name, i.Name)
	return Account{}
}

func TestLexCountsLines(t *testing.T) {
	src := "// c\nfn a() { /* x\n y */ let s = \"}\"; }\n\n/// doc\nfn b() {}\n"
	s := lex("a.rs", []byte(src))
	if s.stats.Lines != 6 || s.stats.Code != 3 || s.stats.Comments != 2 || s.stats.Blank != 1 {
		t.Errorf("stats %+v", s.stats)
	}
	if string(s.masked[len("// c\nfn a() { /* x\n y */ let s = \""):][:1]) != " " {
		t.Error("string contents not masked")
	}
	if s.line(len("// c\nfn a")) != 2 {
		t.Errorf("line lookup %d", s.line(len("// c\nfn a")))
	}
	lines, code := CountLines("x.rs", []byte(src), true)
	if lines != 6 || code != 3 {
		t.Errorf("CountLines %d %d", lines, code)
	}
	lines, code = CountLines("x.toml", []byte("a=1\n\nb=2\n"), false)
	if lines != 3 || code != 2 {
		t.Errorf("CountLines toml %d %d", lines, code)
	}
	if l, c := CountLines("x.bin", []byte{1, 0, 2}, false); l != 0 || c != 0 {
		t.Error("binary should count zero")
	}
}

func TestLexCharAndLifetime(t *testing.T) {
	s := lex("a.rs", []byte("fn f<'info>(c: char) -> bool { c == '{' || c == '\\'' }"))
	if s.closer(len("fn f<'info>(c: char) -> bool ")) < 0 {
		t.Error("char literal brace broke matching")
	}
}

func TestAnchorModel(t *testing.T) {
	m := load(t, "anchor_vault")
	if len(m.Crates) != 1 {
		t.Fatalf("crates %d: shared must not be a program", len(m.Crates))
	}
	c := m.Crates[0]
	if c.Name != "vault" || c.Framework != "anchor" || c.Module != "vault" || len(c.DeclaredIDs) != 1 {
		t.Errorf("crate %+v", c)
	}
	if got, ok := m.CrateFor("Fg6PaFpoGXkYsidMpWTK6W2BeZ7FEfcYkg476zPFsLnS"); !ok || got.Name != "vault" {
		t.Error("CrateFor by declared id")
	}
	if len(c.Instructions) != 3 {
		t.Fatalf("handlers %d: not_a_handler must be skipped", len(c.Instructions))
	}

	init := ix(t, &c, "initialize")
	if init.File != "programs/vault/src/lib.rs" || init.Line != 17 || init.Context != "Initialize" {
		t.Errorf("handler location %+v", init)
	}
	if len(init.Args) != 2 || init.Args[0].Name != "amount" || init.Args[0].Type != "u64" || init.Args[1].Type != "Config" {
		t.Errorf("args %+v", init.Args)
	}
	v := acct(t, init, "vault")
	if !v.Init || !v.Writable || v.Seeds != `Vault::SEED, owner.key().as_ref(), token::ID.as_ref()` || v.Wrapper != "Account" || v.Inner != "Vault" {
		t.Errorf("multi-line attribute: %+v", v)
	}
	for _, a := range init.Accounts {
		if a.Name == "Vault" || a.Name == "token" || a.Name == "owner.key()" {
			t.Errorf("attribute line read as a field: %+v", a)
		}
	}
	if len(init.Accounts) != 6 {
		t.Errorf("initialize accounts %d: %+v", len(init.Accounts), init.Accounts)
	}
	if o := acct(t, init, "owner"); !o.Signer || !o.Writable {
		t.Errorf("owner %+v", o)
	}
	if e := acct(t, init, "tokens.escrow"); e.Wrapper != "Account" || e.Constraints != 1 || !e.Writable {
		t.Errorf("composite flattening: %+v", e)
	}
	if sp := acct(t, init, "system_program"); sp.Writable || sp.Inner != "System" {
		t.Errorf("attribute leaked to neighbour: %+v", sp)
	}
	// Reach: handler -> utils::deposit, utils::reward (-> its own transfer)
	// and self.into_transfer. Token ops: token::transfer and the bare
	// mint_to; the bare transfer into the System program is not one.
	if init.Counts.Functions != 5 || init.Counts.Token != 2 || init.Counts.CPI != 3 || init.Counts.SignedCPI != 0 {
		t.Errorf("initialize counts %+v reach %v", init.Counts, init.Reach)
	}
	if hasSite(init, "token", "transfer", "Token") && !hasSite(init, "token", "mint_to", "Token") {
		t.Errorf("bare mint_to missed: %+v", init.Sites)
	}
	for _, st := range init.Sites {
		if st.Kind == "token" && st.Function == "reward" && st.Op == "transfer" {
			t.Errorf("system transfer counted as a token op: %+v", st)
		}
	}
	if !hasSite(init, "cpi", "cpi_context", "Token") {
		t.Errorf("CpiContext target from Program<Token>: %+v", init.Sites)
	}

	w := ix(t, &c, "withdraw")
	if !w.Remaining {
		t.Error("remaining_accounts not seen")
	}
	// ctx.accounts.process() resolves to Withdraw's process, not Close's,
	// and self.check() inside it to the same impl: two PDA sites, no
	// ambiguity.
	if w.Counts.Raw != 2 || w.Counts.SignedCPI != 1 || w.Counts.PDA != 2 || w.Counts.Lamports != 1 || w.Unresolved != 0 || w.Counts.Functions != 4 {
		t.Errorf("withdraw counts %+v unresolved %d reach %v", w.Counts, w.Unresolved, w.Reach)
	}
	if a := acct(t, w, "anyone"); !a.Raw || a.Wrapper != "UncheckedAccount" {
		t.Errorf("unchecked %+v", a)
	}
	if a := acct(t, w, "maybe"); !a.Raw || !a.Optional || a.Wrapper != "AccountInfo" {
		t.Errorf("optional raw %+v", a)
	}
	if a := acct(t, w, "vault"); !a.Realloc || len(a.HasOne) != 1 || a.HasOne[0] != "owner" || a.Seeds == "" {
		t.Errorf("has_one and realloc %+v", a)
	}
	if a := acct(t, w, "token_program"); a.Address != "anchor_spl::token::ID" {
		t.Errorf("address %+v", a)
	}

	cl := ix(t, &c, "close")
	if a := acct(t, cl, "vault"); !a.Close || a.Owner != "crate::ID" {
		t.Errorf("close/owner %+v", a)
	}
	if a := acct(t, cl, "big"); !a.ZeroCopy || a.Inner != "Big" {
		t.Errorf("zero copy %+v", a)
	}
	if cl.Counts.Unsafe != 1 || cl.Counts.Serde != 1 || cl.Counts.Functions != 2 {
		t.Errorf("close reaches Vault::finish through an impl: %+v %v", cl.Counts, cl.Reach)
	}

	s := m.Summarize()
	if s.Programs != 1 || s.Instructions != 3 || s.Raw != 2 || s.SignedCPI != 1 || s.Unsafe != 1 || s.Code == 0 {
		t.Errorf("summary %+v", s)
	}
	todos := 0
	for _, f := range m.Files {
		todos += f.Todos
	}
	if todos != 3 {
		t.Errorf("todos %d", todos)
	}
}

func TestNativeModel(t *testing.T) {
	m := load(t, "native_counter")
	if len(m.Crates) != 1 || m.Crates[0].Framework != "native" {
		t.Fatalf("crates %+v", m.Crates)
	}
	c := m.Crates[0]
	if len(c.Instructions) != 3 {
		t.Fatalf("arms %d", len(c.Instructions))
	}
	cr := ix(t, &c, "create")
	if cr.File != "src/processor.rs" || cr.Line != 11 {
		t.Errorf("arm location %+v", cr)
	}
	if cr.Counts.Functions != 1 || cr.Counts.SignedCPI != 1 || cr.Counts.PDA != 1 || cr.Counts.Raw != 2 {
		t.Errorf("create counts %+v reach %v", cr.Counts, cr.Reach)
	}
	if !hasSite(cr, "cpi", "invoke_signed", "System") {
		t.Errorf("invoke target from the builder: %+v", cr.Sites)
	}
	if a := acct(t, cr, "payer"); !a.Signer || !a.Raw {
		t.Errorf("is_signer check %+v", a)
	}
	cc := ix(t, &c, "close_counter")
	if cc.Counts.Lamports != 2 {
		t.Errorf("lamport moves %+v", cc.Counts)
	}
	if a := acct(t, cc, "dest"); !a.Writable {
		t.Errorf("is_writable check %+v", a)
	}
	if _, ok := m.CrateFor("anything"); !ok {
		t.Error("single crate answers any id")
	}
}

func hasSite(i Instruction, kind, op, target string) bool {
	for _, s := range i.Sites {
		if s.Kind == kind && s.Op == op && s.Target == target {
			return true
		}
	}
	return false
}

func TestSnake(t *testing.T) {
	for in, want := range map[string]string{"InitializeVault": "initialize_vault", "close": "close", "SetURI": "set_uri", "CloseCounter": "close_counter"} {
		if got := snake(in); got != want {
			t.Errorf("snake(%s)=%s", in, got)
		}
	}
}

func TestConstsAreCollected(t *testing.T) {
	m := load(t, "anchor_vault")
	if string(m.Consts["SEED"]) != "vault" {
		t.Errorf("Vault::SEED from state.rs: %q", m.Consts["SEED"])
	}
	if string(m.Crates[0].Consts["SEED"]) != "vault" {
		t.Errorf("crate consts %v", m.Crates[0].Consts)
	}
	s := lex("c.rs", []byte("pub const A: &[u8] = b\"a\\\"b\";\nconst B: &'static str = \"bee\";\nconst N: u64 = 3;\n"))
	c := s.consts()
	if string(c["A"]) != `a"b` || string(c["B"]) != "bee" || len(c) != 2 {
		t.Errorf("consts %q", c)
	}
}

func TestTypeDefsAreCollected(t *testing.T) {
	m := load(t, "anchor_vault")
	cfg, ok := m.Types["Config"]
	if !ok || cfg.Kind != "struct" || len(cfg.Fields) != 1 || cfg.Fields[0].Name != "fee_bps" || cfg.Fields[0].Type != "u16" {
		t.Errorf("Config %+v", cfg)
	}
	if _, isType := m.Types["Initialize"]; isType {
		t.Error("Accounts structs are not data types")
	}
	s := lex("e.rs", []byte("#[derive(AnchorSerialize)]\npub enum Trigger {\n  /// doc\n  Now,\n  #[deprecated]\n  Cron { schedule: String, skippable: bool },\n  Slot(u64),\n}\npub struct Wrap<T> { pub value: T }\n"))
	d := s.typeDefs()
	tr := d["Trigger"]
	if tr.Kind != "enum" || len(tr.Variants) != 3 || tr.Variants[1].Name != "Cron" || len(tr.Variants[1].Fields) != 2 || tr.Variants[1].Fields[0].Type != "String" || tr.Variants[2].Fields[0].Type != "u64" || tr.Variants[2].Fields[0].Name != "0" {
		t.Errorf("enum %+v", tr)
	}
	if w := d["Wrap"]; w.Kind != "struct" || len(w.Fields) != 1 || w.Fields[0].Type != "T" {
		t.Errorf("generic struct %+v", w)
	}
}
