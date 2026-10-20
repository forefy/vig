package solana

import (
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	"github.com/forefy/vig/backend/internal/chain"
	"github.com/forefy/vig/backend/internal/core"
	"github.com/forefy/vig/backend/internal/rustsrc"
)

func TestParseIDLLegacyAndNew(t *testing.T) {
	legacy := `{"version":"0.1.0","name":"vault","instructions":[
	  {"name":"initializeVault","accounts":[{"name":"vault","isMut":true,"isSigner":false},{"name":"authority","isMut":false,"isSigner":true},
	     {"name":"tokens","accounts":[{"name":"mint","isMut":false,"isSigner":false}]}],
	   "args":[{"name":"amount","type":"u64"},{"name":"cfg","type":{"defined":"Config"}},{"name":"ids","type":{"vec":"publicKey"}}]}]}`
	idl, err := parseIDL([]byte(legacy))
	if err != nil || len(idl.Instructions) != 1 {
		t.Fatalf("legacy: %v %v", idl, err)
	}
	s := idl.Instructions[0]
	if s.Name != "initialize_vault" {
		t.Errorf("name %q", s.Name)
	}
	if hex.EncodeToString(s.Discriminator[:]) != hex.EncodeToString(func() []byte { d := anchorDiscriminator("global:initialize_vault"); return d[:] }()) {
		t.Error("discriminator not derived from snake name")
	}
	if len(s.Accounts) != 3 || s.Accounts[1].Name != "authority" || !s.Accounts[1].Signer || !s.Accounts[0].Writable || s.Accounts[2].Name != "tokens.mint" {
		t.Errorf("accounts: %+v", s.Accounts)
	}
	if len(s.Args) != 3 || s.Args[0].Name != "amount" || typeWord(s.Args[1].Type) != "Config" || typeWord(s.Args[2].Type) != "vec<publicKey>" {
		t.Errorf("args: %v", s.Args)
	}

	modern := `{"address":"X","metadata":{"name":"vault","version":"0.2.0","spec":"0.1.0"},"instructions":[
	  {"name":"withdraw","discriminator":[1,2,3,4,5,6,7,8],"accounts":[{"name":"vault","writable":true},{"name":"owner","signer":true}],"args":[]}]}`
	idl, err = parseIDL([]byte(modern))
	if err != nil || len(idl.Instructions) != 1 {
		t.Fatalf("modern: %v %v", idl, err)
	}
	s = idl.Instructions[0]
	if hex.EncodeToString(s.Discriminator[:]) != "0102030405060708" || !s.Accounts[0].Writable || !s.Accounts[1].Signer {
		t.Errorf("modern spec: %+v", s)
	}
}

func TestProgramNodesLayout(t *testing.T) {
	specs := []instructionSpec{{Name: "deposit", Accounts: []accountSpec{{Name: "vault", Writable: true}, {Name: "user", Signer: true}}, Args: []fieldSpec{{Name: "amount", Type: json.RawMessage(`"u64"`)}}}}
	nodes := programNodes("Prog", specs, "idl")
	if len(nodes) != 4 || nodes[0].ID != "p:Prog" || nodes[1].ID != "ix:Prog:deposit" || nodes[1].Parent != "p:Prog" || nodes[2].Parent != "ix:Prog:deposit" {
		t.Fatalf("nodes: %+v", nodes)
	}
	if nodes[1].Facts["args"] != "amount:u64" || nodes[1].Facts["accounts"] != "2" || nodes[3].Facts["signer"] != "true" {
		t.Errorf("facts: %+v %+v", nodes[1].Facts, nodes[3].Facts)
	}
}

func TestNativeProgramsAreNamed(t *testing.T) {
	tok, ok := nativeSpec("TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA")
	if !ok {
		t.Fatal("token program unknown")
	}
	if spec, ok := tok.resolve([]byte{3, 1, 0, 0, 0, 0, 0, 0, 0}); !ok || spec.Name != "transfer" || len(spec.Accounts) != 3 || !spec.Accounts[2].Signer {
		t.Errorf("transfer: %+v %v", spec, ok)
	}
	if _, ok := tok.resolve(nil); ok {
		t.Error("empty token data resolved")
	}
	sys, _ := nativeSpec("11111111111111111111111111111111")
	if spec, ok := sys.resolve([]byte{2, 0, 0, 0, 1, 2, 3, 4, 5, 6, 7, 8}); !ok || spec.Name != "transfer" {
		t.Errorf("system transfer: %+v", spec)
	}
	ata, _ := nativeSpec("ATokenGPvbdGVxr1b2hvZbsiqW5xWH25efTNsLJA8knL")
	if spec, ok := ata.resolve(nil); !ok || spec.Name != "create" {
		t.Errorf("ata empty: %+v", spec)
	}
	nodes := programNodes("TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA", tok.specs(), "native")
	if len(nodes) < 25 || nodes[0].Kind != "program" {
		t.Errorf("nodes: %d", len(nodes))
	}
}

func TestStaticNodesMeetIDLNodes(t *testing.T) {
	m, err := rustsrc.Load("../../rustsrc/testdata/anchor_vault", "")
	if err != nil {
		t.Fatal(err)
	}
	pid := "Fg6PaFpoGXkYsidMpWTK6W2BeZ7FEfcYkg476zPFsLnS"
	c, ok := m.CrateFor(pid)
	if !ok {
		t.Fatal("crate by declared id")
	}
	// An IDL that knows two of the three handlers and one account each.
	idl := []instructionSpec{
		{Name: "initialize", Accounts: []accountSpec{{Name: "vault", Writable: true}, {Name: "owner", Signer: true}}},
		{Name: "withdraw", Accounts: []accountSpec{{Name: "vault", Writable: true}}},
	}
	s := core.Surface{Adapter: Key}
	s.Merge(programNodes(pid, idl, "idl"), nil)
	before := len(s.Nodes)
	nodes, edges := staticNodes(pid, c, nil)
	s.Enrich(nodes, edges)
	byID := map[string]core.SurfaceNode{}
	for _, n := range s.Nodes {
		byID[n.ID] = n
	}
	ix := byID[ixNodeID(pid, "initialize")]
	if ix.Source != "idl" || ix.Facts["file"] != "programs/vault/src/lib.rs" || ix.Facts["line"] != "17" || ix.Facts["cpi"] != "3" {
		t.Errorf("IDL node keeps its source and gains static facts: %+v", ix)
	}
	if cl := byID[ixNodeID(pid, "close")]; cl.Source != "static" || cl.Facts["unsafe"] != "1" {
		t.Errorf("handler the IDL lacks is added as static: %+v", cl)
	}
	if a := byID[acctNodeID(pid, "withdraw", "anyone")]; a.Source != "static" || a.Facts["raw"] != "true" {
		t.Errorf("raw account: %+v", a)
	}
	if a := byID[acctNodeID(pid, "initialize", "vault")]; a.Facts["init"] != "true" || a.Facts["seeds"] == "" || a.Facts["writable"] != "true" {
		t.Errorf("IDL account enriched: %+v", a)
	}
	if len(s.Nodes) <= before {
		t.Error("nothing added")
	}
	var token *core.SurfaceEdge
	for i := range s.Edges {
		e := &s.Edges[i]
		if e.From == ixNodeID(pid, "withdraw") && e.To == programNodeID("TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA") {
			token = e
		}
	}
	if token == nil || token.Source != "static" || token.Facts["signed"] != "true" || token.Facts["ops"] != "transfer" || token.Count != 0 {
		t.Errorf("static CPI edge to the Token program: %+v", token)
	}
	// Traffic on the same edge merges its count and keeps the static facts.
	s.Merge(nil, []core.SurfaceEdge{{From: token.From, To: token.To, Kind: "cpi", Count: 3}})
	for _, e := range s.Edges {
		if e.From == token.From && e.To == token.To && (e.Count != 3 || e.Facts["signed"] != "true") {
			t.Errorf("merge lost static facts: %+v", e)
		}
	}
}

func TestSourceIDLRow(t *testing.T) {
	if r := sourceIDLRow(Facts{}); r.State != core.BadgePending {
		t.Errorf("no checkout: %+v", r)
	}
	if r := sourceIDLRow(Facts{SourceIx: []string{}}); r.Value != "not found" {
		t.Errorf("no crate: %+v", r)
	}
	if r := sourceIDLRow(Facts{SourceIx: []string{"a"}, SourceFound: true}); r.Value != "no IDL" {
		t.Errorf("idl absent: %+v", r)
	}
	if r := sourceIDLRow(Facts{SourceIx: []string{"a", "b"}, SourceFound: true, IDLPresent: true, IDLRead: true, IDLIx: []string{"b", "a"}}); r.Value != "match" || r.State != core.BadgeOK {
		t.Errorf("match: %+v", r)
	}
	r := sourceIDLRow(Facts{SourceIx: []string{"a", "c"}, SourceFound: true, IDLPresent: true, IDLRead: true, IDLIx: []string{"a", "b"}})
	if r.Value != "mismatch" || r.Detail != "+1 idl +1 source" || r.State != core.BadgeBad {
		t.Errorf("mismatch: %+v", r)
	}
}

func TestWithStaticSpecsDerivesDiscriminators(t *testing.T) {
	m, err := rustsrc.Load("../../rustsrc/testdata/anchor_vault", "")
	if err != nil {
		t.Fatal(err)
	}
	c := &m.Crates[0]
	specs := withStaticSpecs([]instructionSpec{{Name: "initialize", Discriminator: anchorDiscriminator("global:initialize")}}, c)
	if len(specs) != 3 {
		t.Fatalf("specs %d", len(specs))
	}
	if specs[1].Discriminator != anchorDiscriminator("global:withdraw") && specs[2].Discriminator != anchorDiscriminator("global:withdraw") {
		t.Error("withdraw discriminator not derived from its name")
	}
}

func TestStaticEdgesLandOnSiblingInstructions(t *testing.T) {
	m, err := rustsrc.Load("../../rustsrc/testdata/anchor_pair", "")
	if err != nil {
		t.Fatal(err)
	}
	desk, ledger := "Fg6PaFpoGXkYsidMpWTK6W2BeZ7FEfcYkg476zPFsLnS", "HmbTLCmaGvZhKnn1Zfa1JVnp7vkMV4DYVxPLWBVoN65L"
	ps := peersOf(m, []string{desk, ledger})
	if pid, ok := ps.resolve("BookLedger", desk); !ok || pid != ledger {
		t.Fatalf("Program<BookLedger> resolves to the ledger: %q %v (%v)", pid, ok, ps)
	}
	if _, ok := ps.resolve("Token", desk); ok {
		t.Error("well-known programs never resolve to a sibling")
	}
	c, _ := m.CrateFor(desk)
	post := c.Instructions[0]
	if post.Counts.CPI != 1 || post.Counts.SignedCPI != 1 {
		t.Errorf("the named call is its CpiContext, not a second CPI: %+v", post.Counts)
	}
	nodes, edges := staticNodes(desk, c, ps)
	if len(edges) != 1 {
		t.Fatalf("one edge, to the instruction: %+v", edges)
	}
	e := edges[0]
	if e.From != ixNodeID(desk, "post") || e.To != ixNodeID(ledger, "record") || e.Facts["signed"] != "true" || e.Source != "static" {
		t.Errorf("edge %+v", e)
	}
	for _, n := range nodes {
		if n.External {
			t.Errorf("no anonymous external node for a sibling: %+v", n)
		}
	}
	// Without peers the same source still yields one edge, to an external name.
	_, plain := staticNodes(desk, c, nil)
	if len(plain) != 1 || plain[0].To != "p:static:bookledger" {
		t.Errorf("unresolved fallback: %+v", plain)
	}
}

func TestStaticIDLNamesAccountsAndSeeds(t *testing.T) {
	m, err := rustsrc.Load("../../rustsrc/testdata/anchor_vault", "")
	if err != nil {
		t.Fatal(err)
	}
	c, _ := m.CrateFor("Fg6PaFpoGXkYsidMpWTK6W2BeZ7FEfcYkg476zPFsLnS")
	idl := staticIDL(c, nil, m.Types)
	if idl == nil || len(idl.Instructions) != 3 {
		t.Fatalf("spec %+v", idl)
	}
	var init *instructionSpec
	for i := range idl.Instructions {
		if idl.Instructions[i].Name == "initialize" {
			init = &idl.Instructions[i]
		}
	}
	if init == nil || init.Discriminator != anchorDiscriminator("global:initialize") {
		t.Fatal("initialize with its derived discriminator")
	}
	if len(init.Args) != 2 || string(init.Args[0].Type) != `"u64"` || !strings.Contains(string(init.Args[1].Type), "defined") {
		t.Errorf("args %+v", init.Args)
	}
	if fields := structFields(idl.types["Config"]); len(fields) != 1 || fields[0].Name != "fee_bps" || string(fields[0].Type) != `"u16"` {
		t.Errorf("Config from source: %s", idl.types["Config"])
	}
	for in, want := range map[string]string{
		"Option<u64>": `{"option":"u64"}`, "Vec<Pubkey>": `{"vec":"pubkey"}`, "[u8; 32]": `{"array":["u8",32]}`, "Box<Config>": `{"defined":"Config"}`,
		"loom_fiber_program::state::Trailing<Option<Vec<Pubkey>>>": `{"defined":"Trailing"}`, "Vec<u8>": `"bytes"`,
	} {
		if got := string(rustType(in)); got != want {
			t.Errorf("rustType(%s) = %s, want %s", in, got, want)
		}
	}
	if init.Accounts[0].Name != "vault" || !init.Accounts[1].Signer || init.Accounts[3].Name != "tokens.escrow" {
		t.Errorf("accounts in order, flattened: %+v", init.Accounts)
	}
	seeds := init.Accounts[0].PDASeeds
	// seeds = [Vault::SEED, owner.key().as_ref(), token::ID.as_ref()]
	if len(seeds) != 3 || seeds[0].Kind != "const" || string(seeds[0].Value) != "vault" || seeds[1].Kind != "account" || seeds[1].Path != "owner" || seeds[2].Kind != "" {
		t.Errorf("seeds %+v", seeds)
	}
	got := parseSeeds(`SEED_THREAD, authority.key().as_ref(), id.as_ref(), &[fiber_index], thread.authority.as_ref()`, map[string]bool{"authority": true, "thread": true}, map[string]bool{"id": true, "fiber_index": true}, map[string][]byte{"SEED_THREAD": []byte("thread")})
	want := []seedSpec{{Kind: "const", Value: []byte("thread")}, {Kind: "account", Path: "authority"}, {Kind: "arg", Path: "id"}, {Kind: "arg", Path: "fiber_index"}, {}}
	if len(got) != len(want) {
		t.Fatalf("parse %+v", got)
	}
	for i := range want {
		if got[i].Kind != want[i].Kind || got[i].Path != want[i].Path || string(got[i].Value) != string(want[i].Value) {
			t.Errorf("seed %d: %+v", i, got[i])
		}
	}
}

func TestSeedsProgramResolvesSiblingID(t *testing.T) {
	m, err := rustsrc.Load("../../rustsrc/testdata/anchor_pair", "")
	if err != nil {
		t.Fatal(err)
	}
	desk, ledger := "Fg6PaFpoGXkYsidMpWTK6W2BeZ7FEfcYkg476zPFsLnS", "HmbTLCmaGvZhKnn1Zfa1JVnp7vkMV4DYVxPLWBVoN65L"
	ps := peersOf(m, []string{desk, ledger})
	want, _ := chain.Decode(ledger)
	if p := seedsProgram("book_ledger::ID", nil, ps); p == nil || p.Kind != "const" || string(p.Value) != string(want) {
		t.Errorf("sibling ID: %+v", p)
	}
	if p := seedsProgram("ledger_program.key()", map[string]bool{"ledger_program": true}, ps); p == nil || p.Kind != "account" || p.Path != "ledger_program" {
		t.Errorf("program account: %+v", p)
	}
	if p := seedsProgram("some::other::ID", nil, ps); p == nil || p.Kind != "" {
		t.Errorf("unknown program gives no verdict: %+v", p)
	}
}
