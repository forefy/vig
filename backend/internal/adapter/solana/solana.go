// Package solana is target adapter one. It declares the intake steps, the
// verification panel rows and the ownership proof for deployed Solana
// programs. Chain reads live in internal/chain.
package solana

import (
	"context"
	"errors"
	"path/filepath"
	"regexp"

	"github.com/forefy/vig/backend/internal/core"
	"github.com/forefy/vig/backend/internal/github"
	"github.com/forefy/vig/backend/internal/rustsrc"
	"github.com/forefy/vig/backend/internal/scan"
)

// Key is the adapter identifier used in targets.adapter.
const Key = "solana"

// Verification panel row keys (SPEC §7). The order here is the render order.
const (
	RowExists        = "exists"
	RowLoader        = "loader"
	RowAuthority     = "upgrade_authority"
	RowLastDeployed  = "last_deployed"
	RowBytecodeHash  = "bytecode_sha256"
	RowIDL           = "idl"
	RowDeclaredID    = "declared_id"
	RowVerifiedBuild = "verified_build"
	RowSourceMatch   = "source_match"
	RowSourceIDL     = "source_idl"
	RowOwnership     = "ownership"
)

// RowOrder is the panel order.
var RowOrder = []string{RowExists, RowLoader, RowAuthority, RowLastDeployed, RowBytecodeHash, RowIDL, RowDeclaredID, RowVerifiedBuild, RowSourceMatch, RowSourceIDL, RowOwnership}

// RowLabels are the short labels the panel shows. No sentences.
var RowLabels = map[string]string{
	RowExists:        "Program",
	RowLoader:        "Loader",
	RowAuthority:     "Upgrade authority",
	RowLastDeployed:  "Last deployed",
	RowBytecodeHash:  "Bytecode",
	RowIDL:           "IDL",
	RowDeclaredID:    "Declared ID",
	RowVerifiedBuild: "Verified build",
	RowSourceMatch:   "Source matches chain",
	RowSourceIDL:     "IDL matches source",
	RowOwnership:     "Ownership",
}

// Base58 program address, 32 to 44 chars.
const programIDPattern = `^[1-9A-HJ-NP-Za-km-z]{32,44}$`

// Public GitHub repo URL, optionally with /tree/<rev>/<path>.
const repoURLPattern = `^https://github\.com/[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+(/tree/[^\s]+)?/?$`

// Adapter implements core.Adapter. Inspector and Advisories are nil in the
// contract-only configuration (spec generation, tests without network).
type Adapter struct {
	Inspector  *Inspector
	Advisories *scan.DB
}

// New returns the Solana adapter without chain access.
func New() *Adapter { return &Adapter{} }

// NewWired returns the adapter with chain, registry and advisory access.
func NewWired(in *Inspector, adv *scan.DB) *Adapter { return &Adapter{Inspector: in, Advisories: adv} }

func (a *Adapter) Key() string { return Key }

// IntakeSteps declares the five wizard steps (SPEC §4.4).
func (a *Adapter) IntakeSteps() []core.StepSpec {
	return []core.StepSpec{
		{Key: "target", Title: "Target", Fields: []core.FieldSpec{
			{Key: "kind", Kind: core.FieldCards, Label: "Target", Required: true, Options: []core.Option{
				{Value: "solana_program", Label: "Solana program", Hint: "mainnet"},
				{Value: "framework", Label: "Framework or SDK", Soon: true, Disabled: true},
				{Value: "other", Label: "Something else", Soon: true, Disabled: true},
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
		{Key: "programs", Title: "Programs", Fields: []core.FieldSpec{
			{Key: "program_ids", Kind: core.FieldList, Label: "Program ID", Required: true, Pattern: programIDPattern, Panel: true},
		}},
		{Key: "scope", Title: "Scope", Fields: []core.FieldSpec{
			{Key: "files", Kind: core.FieldTree, Label: "Files", Required: true},
		}},
		{Key: "review", Title: "Review", Fields: []core.FieldSpec{
			{Key: "pin", Kind: core.FieldSummary, Label: "Pin", Required: false},
		}},
	}
}

// ErrNotWired is returned when the adapter has no chain client.
var ErrNotWired = errors.New("solana adapter: chain client not wired")

var (
	programIDRE = regexp.MustCompile(programIDPattern)
	repoURLRE   = regexp.MustCompile(repoURLPattern)
)

// draftList reads a list field as strings.
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

func draftString(draft core.IntakeDraft, step, field string) string {
	s, _ := draft[step][field].(string)
	return s
}

// Validate checks the shapes the wizard collects.
func (a *Adapter) Validate(ctx context.Context, draft core.IntakeDraft) ([]core.Issue, error) {
	var issues []core.Issue
	if kind := draftString(draft, "source", "kind"); kind == "public" || kind == "" {
		if u := draftString(draft, "source", "repo_url"); u != "" && !repoURLRE.MatchString(u) {
			issues = append(issues, core.Issue{Step: "source", Field: "repo_url", Msg: "not a public GitHub URL"})
		}
	}
	for _, id := range draftList(draft, "programs", "program_ids") {
		if !programIDRE.MatchString(id) {
			issues = append(issues, core.Issue{Step: "programs", Field: "program_ids", Msg: "not a program address: " + id})
		}
	}
	if cov := draftString(draft, "target", "coverage"); cov == "diff" && draftString(draft, "source", "base") == "" {
		issues = append(issues, core.Issue{Step: "source", Field: "base", Msg: "diff coverage needs a base"})
	}
	return issues, nil
}

// Verify renders one panel per program id without a source checkout.
func (a *Adapter) Verify(ctx context.Context, draft core.IntakeDraft) ([]core.VerificationPanel, error) {
	if a.Inspector == nil {
		return nil, ErrNotWired
	}
	var out []core.VerificationPanel
	for _, id := range draftList(draft, "programs", "program_ids") {
		f, err := a.Inspector.Inspect(ctx, id, "")
		if err != nil {
			return nil, err
		}
		out = append(out, Panel(f))
	}
	return out, nil
}

// Preflight is the deterministic scan over a checkout: panels, tree, lines
// in the auto scope, what the source declares, and Cargo.lock advisories.
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
	model, _ := rustsrc.Load(clone.Root, sub)
	if model != nil {
		pf.Source = summary(model)
	}
	for _, id := range draftList(draft, "programs", "program_ids") {
		if a.Inspector == nil {
			pf.Panels = append(pf.Panels, EmptyPanel(id))
			continue
		}
		f, err := a.Inspector.Inspect(ctx, id, clone.Root)
		if err != nil {
			return core.Preflight{}, err
		}
		if model != nil {
			f.SourceIx = []string{}
			if c, ok := model.CrateFor(id); ok {
				f.SourceFound = true
				f.SourceIx = instructionNames(c)
				if f.IDLPresent {
					f.IDLIx, f.IDLRead = a.Inspector.idlInstructions(ctx, id)
				}
			}
		}
		pf.Panels = append(pf.Panels, Panel(f))
	}
	if a.Advisories != nil {
		lock := filepath.Join(clone.Root, filepath.FromSlash(sub), "Cargo.lock")
		if pkgs := scan.ReadCargoLock(lock); len(pkgs) == 0 {
			pkgs = scan.ReadCargoLock(filepath.Join(clone.Root, "Cargo.lock"))
			if len(pkgs) > 0 {
				pf.Advisories = a.advisories(ctx, pkgs)
			}
		} else {
			pf.Advisories = a.advisories(ctx, pkgs)
		}
	}
	return pf, nil
}

func (a *Adapter) advisories(ctx context.Context, pkgs []scan.Package) []core.Advisory {
	list, err := a.Advisories.Advisories(ctx)
	if err != nil {
		return []core.Advisory{}
	}
	return scan.Match(list, pkgs)
}

// ValidProgramID implements core.ProgramInspector.
func (a *Adapter) ValidProgramID(id string) bool { return programIDRE.MatchString(id) }

// InspectProgram implements core.ProgramInspector: the facts of one
// deployed program in the store's shape.
func (a *Adapter) InspectProgram(ctx context.Context, id string, opts core.InspectOpts) (core.ProgramFacts, error) {
	if a.Inspector == nil {
		return core.ProgramFacts{}, ErrNotWired
	}
	f, err := a.Inspector.Inspect(ctx, id, opts.Root)
	if err != nil {
		return core.ProgramFacts{}, err
	}
	return core.ProgramFacts{ProgramID: id, Exists: f.Exists, Loader: f.Loader, UpgradeAuthority: f.UpgradeAuthority, LastDeploySlot: f.LastDeploySlot,
		BytecodeSHA256: f.BytecodeSHA256, IDLPresent: f.IDLPresent, IDLVersion: f.IDLVersion, VerifiedBuild: f.VerifiedBuild, VerifiedCommit: f.VerifiedCommit,
		OwnershipProven: f.OwnershipProven, Panel: Panel(f)}, nil
}

func (a *Adapter) LocationKinds() []core.LocationKind {
	return []core.LocationKind{"file_line", "program_instruction"}
}

func (a *Adapter) OwnershipProof() core.OwnershipProofSpec {
	return core.OwnershipProofSpec{Method: "sign_nonce_with_upgrade_authority"}
}

// EmptyPanel returns the ten rows in pending state for a program id, so the
// UI can render the panel shape before any chain read completes.
func EmptyPanel(programID string) core.VerificationPanel {
	rows := make([]core.PanelRow, 0, len(RowOrder))
	for _, k := range RowOrder {
		rows = append(rows, core.PanelRow{Key: k, Label: RowLabels[k], State: core.BadgePending})
	}
	return core.VerificationPanel{Subject: programID, Rows: rows}
}
