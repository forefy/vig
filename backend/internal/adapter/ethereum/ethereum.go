// Package ethereum is target adapter two: EVM contracts on the chains a
// keyless public endpoint serves. It declares the intake steps, reads a
// contract's facts into the panel, scans Solidity for what the source
// declares, and maps and watches the contract's surface. Chain reads live
// in internal/evm, source reads in internal/solsrc.
package ethereum

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/forefy/vig/backend/internal/core"
	"github.com/forefy/vig/backend/internal/evm"
	"github.com/forefy/vig/backend/internal/github"
	"github.com/forefy/vig/backend/internal/solsrc"
)

// Key is the adapter identifier used in targets.adapter.
const Key = "ethereum"

// Chain is one EVM chain the adapter can read from a public endpoint.
type Chain struct {
	ID       string
	Name     string
	RPC      string
	Sourcify string // chain id Sourcify uses
	Explorer string // where a person reads a transaction
	API      string // the Blockscout instance that lists an address's history
}

// Chains are the chains served, by id. The first is the default.
var Chains = []Chain{
	{ID: "1", Name: "Ethereum", RPC: "https://ethereum-rpc.publicnode.com", Sourcify: "1", Explorer: "https://etherscan.io", API: "https://eth.blockscout.com"},
	{ID: "8453", Name: "Base", RPC: "https://base-rpc.publicnode.com", Sourcify: "8453", Explorer: "https://basescan.org", API: "https://base.blockscout.com"},
	{ID: "11155111", Name: "Sepolia", RPC: "https://ethereum-sepolia-rpc.publicnode.com", Sourcify: "11155111", Explorer: "https://sepolia.etherscan.io", API: "https://eth-sepolia.blockscout.com"},
}

// ChainByID finds a chain; empty means the default.
func ChainByID(id string) (Chain, bool) {
	if id == "" {
		return Chains[0], true
	}
	for _, c := range Chains {
		if c.ID == id {
			return c, true
		}
	}
	return Chain{}, false
}

// chainFor reads a subject's chain out of the map the jobs build from the
// store. The store keeps addresses as they were entered, checksummed
// usually, while the adapter works in lowercase; the lookup must not
// care.
func chainFor(chains map[string]string, addr string) Chain {
	if c, ok := ChainByID(chains[addr]); ok && chains[addr] != "" {
		return c
	}
	for k, v := range chains {
		if strings.EqualFold(k, addr) {
			if c, ok := ChainByID(v); ok {
				return c
			}
		}
	}
	return Chains[0]
}

// Verification panel row keys. The order here is the render order.
const (
	RowExists    = "exists"
	RowChain     = "chain"
	RowCode      = "bytecode_keccak"
	RowVerified  = "verified_source"
	RowProxy     = "proxy"
	RowAdmin     = "upgrade_admin"
	RowInterface = "interface"
	RowSource    = "source_interface"
	RowCompiler  = "compiler"
	RowHoldings  = "holdings"
)

// RowOrder is the panel order.
var RowOrder = []string{RowExists, RowChain, RowCode, RowVerified, RowCompiler, RowProxy, RowAdmin, RowInterface, RowSource, RowHoldings}

// RowLabels are the short labels the panel shows. No sentences.
var RowLabels = map[string]string{
	RowExists:    "Contract",
	RowChain:     "Chain",
	RowCode:      "Bytecode",
	RowVerified:  "Verified source",
	RowProxy:     "Proxy",
	RowAdmin:     "Upgrade admin",
	RowInterface: "Interface",
	RowSource:    "Interface matches source",
	RowCompiler:  "Compiler",
	RowHoldings:  "Holds",
}

const addressPattern = `^0x[0-9a-fA-F]{40}$`

// Public GitHub repo URL, optionally with /tree/<rev>/<path>.
const repoURLPattern = `^https://github\.com/[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+(/tree/[^\s]+)?/?$`

var (
	addressRE = regexp.MustCompile(addressPattern)
	repoURLRE = regexp.MustCompile(repoURLPattern)
)

// Adapter implements core.Adapter, core.ProgramInspector,
// core.SurfaceBuilder and core.Watcher. Clients is nil in the
// contract-only configuration (spec generation, tests without network).
type Adapter struct {
	// Clients returns the RPC client of a chain; nil means not wired.
	Clients func(chain Chain) *evm.Client
	// Explorers returns the history index of a chain; nil means none.
	Explorers func(chain Chain) *evm.Explorer
	Lookup    *evm.Lookup
	// RPCOverride replaces a chain's public endpoint (configuration).
	RPCOverride map[string]string

	mu        sync.Mutex
	clients   map[string]*evm.Client
	explorers map[string]*evm.Explorer
}

func (a *Adapter) explorer(c Chain) *evm.Explorer {
	if c.API == "" {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if e, ok := a.explorers[c.ID]; ok {
		return e
	}
	e := evm.NewExplorer(c.API)
	a.explorers[c.ID] = e
	return e
}

// New returns the adapter without chain access.
func New() *Adapter { return &Adapter{} }

// NewWired returns the adapter with chain and verification access.
func NewWired(overrides map[string]string) *Adapter {
	a := &Adapter{Lookup: evm.NewLookup(), RPCOverride: overrides, clients: map[string]*evm.Client{}, explorers: map[string]*evm.Explorer{}}
	a.Clients = a.client
	a.Explorers = a.explorer
	return a
}

func (a *Adapter) client(c Chain) *evm.Client {
	a.mu.Lock()
	defer a.mu.Unlock()
	if cl, ok := a.clients[c.ID]; ok {
		return cl
	}
	url := c.RPC
	if o := a.RPCOverride[c.ID]; o != "" {
		url = o
	}
	cl := evm.New([]string{url})
	a.clients[c.ID] = cl
	return cl
}

func (a *Adapter) Key() string { return Key }

// IntakeSteps declares the wizard steps: the same five as Solana, with
// contracts and a chain where programs were.
func (a *Adapter) IntakeSteps() []core.StepSpec {
	chains := make([]core.Option, 0, len(Chains))
	for _, c := range Chains {
		chains = append(chains, core.Option{Value: c.ID, Label: c.Name})
	}
	return []core.StepSpec{
		{Key: "target", Title: "Target", Fields: []core.FieldSpec{
			{Key: "kind", Kind: core.FieldCards, Label: "Target", Required: true, Options: []core.Option{
				{Value: "evm_contract", Label: "EVM contract"},
			}},
			{Key: "coverage", Kind: core.FieldCards, Label: "Coverage", Required: true, Options: []core.Option{
				{Value: "full", Label: "Full audit"},
				{Value: "diff", Label: "Diff review"},
			}},
		}},
		{Key: "source", Title: "Source", Fields: []core.FieldSpec{
			{Key: "kind", Kind: core.FieldCards, Label: "Source", Required: true, Options: []core.Option{
				{Value: "public", Label: "Public GitHub repository"},
				{Value: "installation", Label: "Connect GitHub"},
			}},
			{Key: "repo_url", Kind: core.FieldText, Label: "Repository", Required: true, Pattern: repoURLPattern},
			{Key: "selector", Kind: core.FieldSelect, Label: "Revision", Required: true, Options: []core.Option{
				{Value: "branch", Label: "Branch"}, {Value: "tag", Label: "Tag"}, {Value: "commit", Label: "Commit"},
			}},
			{Key: "revision", Kind: core.FieldText, Label: "Value", Required: true},
			{Key: "base", Kind: core.FieldText, Label: "Base", Required: false},
		}},
		{Key: "programs", Title: "Contracts", Fields: []core.FieldSpec{
			{Key: "chain", Kind: core.FieldSelect, Label: "Chain", Required: true, Options: chains},
			{Key: "program_ids", Kind: core.FieldList, Label: "Contract address", Required: true, Pattern: addressPattern, Panel: true},
		}},
		{Key: "scope", Title: "Scope", Fields: []core.FieldSpec{
			{Key: "files", Kind: core.FieldTree, Label: "Files", Required: true},
		}},
		{Key: "review", Title: "Review", Fields: []core.FieldSpec{
			{Key: "pin", Kind: core.FieldSummary, Label: "Pin", Required: false},
		}},
	}
}

func draftString(draft core.IntakeDraft, step, field string) string {
	s, _ := draft[step][field].(string)
	return s
}

func draftList(draft core.IntakeDraft, step, field string) []string {
	var out []string
	switch v := draft[step][field].(type) {
	case []string:
		out = v
	case []any:
		for _, x := range v {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
	case string:
		if v != "" {
			out = []string{v}
		}
	}
	return out
}

// Validate checks the shapes the wizard collects.
func (a *Adapter) Validate(ctx context.Context, draft core.IntakeDraft) ([]core.Issue, error) {
	var issues []core.Issue
	if kind := draftString(draft, "source", "kind"); kind == "public" || kind == "" {
		if u := draftString(draft, "source", "repo_url"); u != "" && !repoURLRE.MatchString(u) {
			issues = append(issues, core.Issue{Step: "source", Field: "repo_url", Msg: "not a public GitHub URL"})
		}
	}
	if c := draftString(draft, "programs", "chain"); c != "" {
		if _, ok := ChainByID(c); !ok {
			issues = append(issues, core.Issue{Step: "programs", Field: "chain", Msg: "unknown chain " + c})
		}
	}
	for _, id := range draftList(draft, "programs", "program_ids") {
		if !addressRE.MatchString(id) {
			issues = append(issues, core.Issue{Step: "programs", Field: "program_ids", Msg: "not a contract address: " + id})
		}
	}
	if cov := draftString(draft, "target", "coverage"); cov == "diff" && draftString(draft, "source", "base") == "" {
		issues = append(issues, core.Issue{Step: "source", Field: "base", Msg: "diff coverage needs a base"})
	}
	return issues, nil
}

// Facts is what one contract read told us.
type Facts struct {
	Address        string
	Chain          Chain
	Exists         bool
	CodeKeccak     string
	CodeSize       int
	Verified       bool
	ABI            *evm.ABI // verified, from Sourcify, of the implementation when a proxy
	Implementation string   // EIP-1967, when a proxy
	Admin          string   // EIP-1967 admin, when set
	// Against the source, when a checkout was read.
	SourceRead     bool
	SourceContract string
	SourceMatch    string // match, mismatch, not_found, or empty when not read
	SourceOnly     int    // selectors in source not on chain
	ChainOnly      int    // selectors on chain not in source
	// Without a verified interface, the bytecode itself is asked: how
	// many of the source contract's selectors it pushes.
	InCode      int
	SourceTotal int
	// How the verified build was made and how well it matches the chain.
	Compiler   string
	ViaIR      bool
	EVMVersion string
	Match      string // exact, partial
	// What the contract holds, when an explorer lists it.
	Holdings []evm.Holding
}

// Inspect reads one contract's facts on one chain.
func (a *Adapter) Inspect(ctx context.Context, address string, chain Chain, root string) (Facts, error) {
	if a.Clients == nil {
		return Facts{}, core.ErrNotWired
	}
	c := a.Clients(chain)
	f := Facts{Address: strings.ToLower(address), Chain: chain}
	code, err := c.Code(ctx, address)
	if err != nil {
		return f, err
	}
	f.Exists = len(code) > 0
	if !f.Exists {
		return f, nil
	}
	f.CodeSize = len(code)
	h := evm.Keccak(code)
	f.CodeKeccak = hex.EncodeToString(h[:])
	if w, err := c.StorageAt(ctx, address, evm.SlotImplementation); err == nil {
		f.Implementation = evm.AddressOf(w)
	}
	if w, err := c.StorageAt(ctx, address, evm.SlotAdmin); err == nil {
		f.Admin = evm.AddressOf(w)
	}
	if a.Lookup != nil {
		subject := address
		if f.Implementation != "" {
			subject = f.Implementation
		}
		if v, err := a.Lookup.Verified(ctx, chain.Sourcify, subject); err == nil {
			f.Verified = true
			f.ABI = v.ABI
			f.Compiler, f.ViaIR, f.EVMVersion, f.Match = v.Compiler, v.ViaIR, v.EVMVersion, v.Match
		}
	}
	if a.Explorers != nil {
		if ex := a.Explorers(chain); ex != nil {
			if hs, err := ex.TokenBalances(ctx, address); err == nil {
				f.Holdings = hs
			}
		}
	}
	if root != "" {
		model, err := solsrc.Load(root, "")
		if err == nil && model != nil {
			f.SourceRead = true
			var pushed map[[4]byte]bool
			if f.ABI == nil {
				// Unverified: the dispatcher's constants stand in for the
				// interface. A proxy's own code only forwards; read the
				// implementation's.
				scan := code
				if f.Implementation != "" {
					if ic, err := c.Code(ctx, f.Implementation); err == nil && len(ic) > 0 {
						scan = ic
					}
				}
				pushed = evm.PushedSelectors(scan)
			}
			a.matchSource(&f, model, pushed)
		}
	}
	return f, nil
}

// matchSource finds the deployable contract whose exposed selectors best
// cover the interface on chain, and records the difference. Without a
// verified interface the selectors the bytecode pushes take its place.
func (a *Adapter) matchSource(f *Facts, model *solsrc.Model, pushed map[[4]byte]bool) {
	if f.ABI == nil {
		// Nothing verified to compare with: the source alone names the
		// interface. The deployable contract with the most selectors in
		// the bytecode stands for it, the busiest one when none is.
		f.SourceMatch = ""
		list := model.Deployable()
		if len(list) == 0 {
			return
		}
		hits := map[string]int{}
		total := map[string]int{}
		for _, c := range list {
			for _, fn := range model.Exposed(&c) {
				total[c.Name]++
				if pushed[fn.Selector] {
					hits[c.Name]++
				}
			}
		}
		sort.Slice(list, func(i, j int) bool {
			if hits[list[i].Name] != hits[list[j].Name] {
				return hits[list[i].Name] > hits[list[j].Name]
			}
			return list[i].Code > list[j].Code
		})
		f.SourceContract = list[0].Name
		if pushed != nil {
			f.InCode, f.SourceTotal = hits[list[0].Name], total[list[0].Name]
		}
		return
	}
	best, bestHits := "", -1
	bestOnly, bestChainOnly := 0, 0
	for _, c := range model.Deployable() {
		have := map[[4]byte]bool{}
		for _, fn := range model.Exposed(&c) {
			have[fn.Selector] = true
		}
		hits, chainOnly := 0, 0
		for sel := range f.ABI.Functions {
			if have[sel] {
				hits++
			} else {
				chainOnly++
			}
		}
		sourceOnly := len(have) - hits
		if hits > bestHits {
			best, bestHits, bestOnly, bestChainOnly = c.Name, hits, sourceOnly, chainOnly
		}
	}
	if bestHits <= 0 {
		f.SourceMatch = "not_found"
		return
	}
	f.SourceContract, f.SourceOnly, f.ChainOnly = best, bestOnly, bestChainOnly
	if bestChainOnly == 0 {
		f.SourceMatch = "match"
	} else {
		f.SourceMatch = "mismatch"
	}
}

// Panel renders the facts as rows.
func Panel(f Facts) core.VerificationPanel {
	rows := make([]core.PanelRow, 0, len(RowOrder))
	add := func(key, value string, state core.BadgeState, detail string) {
		rows = append(rows, core.PanelRow{Key: key, Label: RowLabels[key], Value: value, State: state, Detail: detail})
	}
	if !f.Exists {
		add(RowExists, "not found", core.BadgeBad, "")
		add(RowChain, f.Chain.Name, core.BadgeNeutral, f.Chain.ID)
		for _, k := range RowOrder[2:] {
			rows = append(rows, core.PanelRow{Key: k, Label: RowLabels[k], State: core.BadgePending})
		}
		return core.VerificationPanel{Subject: f.Address, Rows: rows}
	}
	add(RowExists, "found", core.BadgeOK, strconv.Itoa(f.CodeSize)+" bytes")
	add(RowChain, f.Chain.Name, core.BadgeNeutral, f.Chain.ID)
	add(RowCode, f.CodeKeccak, core.BadgeNeutral, "")
	if f.Verified {
		add(RowVerified, "verified", core.BadgeOK, "sourcify"+map[bool]string{true: " · " + f.Match + " match", false: ""}[f.Match != ""])
	} else {
		add(RowVerified, "unverified", core.BadgeNeutral, "")
	}
	if f.Compiler != "" {
		add(RowCompiler, f.Compiler, core.BadgeNeutral, compilerDetail(f))
	} else {
		add(RowCompiler, "unknown", core.BadgePending, "")
	}
	if f.Implementation != "" {
		add(RowProxy, "proxy", core.BadgeWarn, f.Implementation)
	} else {
		add(RowProxy, "no proxy", core.BadgeOK, "")
	}
	if f.Admin != "" {
		add(RowAdmin, f.Admin, core.BadgeNeutral, "")
	} else if f.Implementation != "" {
		add(RowAdmin, "none", core.BadgeNeutral, "")
	} else {
		add(RowAdmin, "immutable", core.BadgeOK, "")
	}
	if f.ABI != nil {
		add(RowInterface, fmt.Sprintf("%d functions", len(f.ABI.Functions)), core.BadgeNeutral, fmt.Sprintf("%d events", len(f.ABI.Events)))
	} else {
		add(RowInterface, "unknown", core.BadgePending, "")
	}
	switch {
	case !f.SourceRead:
		add(RowSource, "pending", core.BadgePending, "")
	case f.SourceMatch == "match":
		add(RowSource, "match", core.BadgeOK, f.SourceContract)
	case f.SourceMatch == "mismatch":
		add(RowSource, "mismatch", core.BadgeBad, fmt.Sprintf("%s · +%d chain · +%d source", f.SourceContract, f.ChainOnly, f.SourceOnly))
	case f.SourceMatch == "not_found":
		add(RowSource, "not found", core.BadgeBad, "")
	case f.SourceTotal > 0 && f.InCode == f.SourceTotal:
		add(RowSource, "selectors in bytecode", core.BadgeOK, fmt.Sprintf("%s · %d/%d", f.SourceContract, f.InCode, f.SourceTotal))
	case f.SourceTotal > 0 && f.InCode > 0:
		add(RowSource, "partial", core.BadgeWarn, fmt.Sprintf("%s · %d/%d selectors in bytecode", f.SourceContract, f.InCode, f.SourceTotal))
	case f.SourceTotal > 0:
		add(RowSource, "not in bytecode", core.BadgeBad, fmt.Sprintf("%s · 0/%d selectors", f.SourceContract, f.SourceTotal))
	default:
		add(RowSource, "no verified interface", core.BadgeNeutral, f.SourceContract)
	}
	if len(f.Holdings) > 0 {
		add(RowHoldings, fmt.Sprintf("%d tokens", len(f.Holdings)), core.BadgeNeutral, evm.FormatHoldings(f.Holdings))
	} else {
		add(RowHoldings, "nothing", core.BadgeNeutral, "")
	}
	return core.VerificationPanel{Subject: f.Address, Rows: rows}
}

// compilerDetail says how the build was made: the pipeline and the EVM target.
func compilerDetail(f Facts) string {
	parts := []string{}
	if f.ViaIR {
		parts = append(parts, "via_ir")
	} else {
		parts = append(parts, "legacy codegen")
	}
	if f.EVMVersion != "" {
		parts = append(parts, f.EVMVersion)
	}
	return strings.Join(parts, " · ")
}

// EmptyPanel is the panel shape before any read.
func EmptyPanel(address string) core.VerificationPanel {
	rows := make([]core.PanelRow, 0, len(RowOrder))
	for _, k := range RowOrder {
		rows = append(rows, core.PanelRow{Key: k, Label: RowLabels[k], State: core.BadgePending})
	}
	return core.VerificationPanel{Subject: address, Rows: rows}
}

// ValidProgramID implements core.ProgramInspector.
func (a *Adapter) ValidProgramID(id string) bool { return addressRE.MatchString(id) }

// InspectProgram implements core.ProgramInspector.
func (a *Adapter) InspectProgram(ctx context.Context, id string, opts core.InspectOpts) (core.ProgramFacts, error) {
	chain, ok := ChainByID(opts.Chain)
	if !ok {
		return core.ProgramFacts{}, errors.New("unknown chain " + opts.Chain)
	}
	f, err := a.Inspect(ctx, id, chain, opts.Root)
	if err != nil {
		return core.ProgramFacts{}, err
	}
	pf := core.ProgramFacts{ProgramID: strings.ToLower(id), Chain: chain.ID, Exists: f.Exists, Loader: "evm", UpgradeAuthority: f.Admin,
		BytecodeSHA256: f.CodeKeccak, IDLPresent: f.Verified, VerifiedBuild: core.MatchUnverified, Panel: Panel(f)}
	if f.Verified {
		pf.IDLVersion = "abi"
		pf.VerifiedBuild = core.MatchVerified
	}
	if f.Implementation != "" {
		pf.Loader = "evm proxy"
	}
	return pf, nil
}

// Verify renders one panel per contract without a source checkout.
func (a *Adapter) Verify(ctx context.Context, draft core.IntakeDraft) ([]core.VerificationPanel, error) {
	chain, ok := ChainByID(draftString(draft, "programs", "chain"))
	if !ok {
		return nil, errors.New("unknown chain")
	}
	var out []core.VerificationPanel
	for _, id := range draftList(draft, "programs", "program_ids") {
		f, err := a.Inspect(ctx, id, chain, "")
		if err != nil {
			return nil, err
		}
		out = append(out, Panel(f))
	}
	return out, nil
}

// Preflight is the deterministic scan over a checkout: the tree, lines in
// the auto scope, what the Solidity declares, and one panel per contract.
func (a *Adapter) Preflight(ctx context.Context, clone core.Clone, draft core.IntakeDraft) (core.Preflight, error) {
	sub := draftString(draft, "source", "path")
	if sub == "" {
		sub = clone.Path
	}
	tree, err := github.BuildTree(clone.Root, sub)
	if err != nil {
		return core.Preflight{}, err
	}
	pf := core.Preflight{Panels: []core.VerificationPanel{}, Tree: tree.Nodes, Lines: tree.SelectedLines, Code: tree.SelectedCode, Advisories: []core.Advisory{}}
	model, _ := solsrc.Load(clone.Root, sub)
	if model != nil {
		pf.Source = Summary(model)
	}
	chain, ok := ChainByID(draftString(draft, "programs", "chain"))
	if !ok {
		chain = Chains[0]
	}
	for _, id := range draftList(draft, "programs", "program_ids") {
		if a.Clients == nil {
			pf.Panels = append(pf.Panels, EmptyPanel(id))
			continue
		}
		f, err := a.Inspect(ctx, id, chain, clone.Root)
		if err != nil {
			return core.Preflight{}, err
		}
		pf.Panels = append(pf.Panels, Panel(f))
	}
	return pf, nil
}

// Summary renders what the Solidity declares in the chain-neutral shape:
// contracts as programs, exposed functions as instructions, state
// variables as accounts, calls out as CPIs and delegatecalls as the
// signed kind, assembly as unsafe.
func Summary(m *solsrc.Model) *core.SourceSummary {
	s := &core.SourceSummary{Code: m.Code}
	for _, c := range m.Deployable() {
		s.Programs++
		s.Instructions += len(m.Exposed(&c))
		s.CPI += c.Sites.Calls + c.Sites.StaticCall
		s.SignedCPI += c.Sites.DelegateCall
		s.Token += c.Sites.Transfers
		s.Unsafe += c.Sites.Assembly + c.Sites.SelfDestruct
		s.Raw += c.Sites.Unchecked
	}
	return s
}

func (a *Adapter) LocationKinds() []core.LocationKind {
	return []core.LocationKind{"file_line", "contract_function"}
}

func (a *Adapter) OwnershipProof() core.OwnershipProofSpec {
	return core.OwnershipProofSpec{Method: "none"}
}
