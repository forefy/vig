// Package repo is target adapter four: a repository at a commit, with no
// chain and no site. Any codebase reviews the same way: the source is the
// whole target, the scope is a file selection, and a finding points at a
// file and a line. Nothing here reads a network.
package repo

import (
	"context"
	"regexp"

	"github.com/forefy/vig/backend/internal/core"
	"github.com/forefy/vig/backend/internal/github"
)

// Key is the adapter identifier used in targets.adapter.
const Key = "repo"

// Kind is the target card's value.
const Kind = "repository"

const repoURLPattern = `^https://github\.com/[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+(/tree/[^\s]+)?/?$`

var repoURLRE = regexp.MustCompile(repoURLPattern)

// Adapter implements core.Adapter.
type Adapter struct{}

// New returns the adapter; it needs nothing wired.
func New() *Adapter { return &Adapter{} }

func (a *Adapter) Key() string { return Key }

// IntakeSteps declares the wizard steps: the target card, the source
// (repository and revision), the scope as a file tree, and the review. No
// programs step, so an audit of a repository carries no deployed subject.
func (a *Adapter) IntakeSteps() []core.StepSpec {
	return []core.StepSpec{
		{Key: "target", Title: "Target", Fields: []core.FieldSpec{
			{Key: "kind", Kind: core.FieldCards, Label: "Target", Required: true, Options: []core.Option{
				{Value: Kind, Label: "Repository", Hint: "any codebase"},
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

// Validate checks the shapes the wizard collects: a public GitHub URL when
// the source is public.
func (a *Adapter) Validate(ctx context.Context, draft core.IntakeDraft) ([]core.Issue, error) {
	var issues []core.Issue
	if draftString(draft, "source", "kind") != "installation" {
		if u := draftString(draft, "source", "repo_url"); u != "" && !repoURLRE.MatchString(u) {
			issues = append(issues, core.Issue{Step: "source", Field: "repo_url", Msg: "not a public GitHub URL"})
		}
	}
	return issues, nil
}

// Verify has nothing to look at: a repository has no deployed subject.
func (a *Adapter) Verify(ctx context.Context, draft core.IntakeDraft) ([]core.VerificationPanel, error) {
	return nil, nil
}

// Preflight reads the checkout into the scope tree: code files of any
// language selected, tests, docs, vendored code and tooling left out.
func (a *Adapter) Preflight(ctx context.Context, clone core.Clone, draft core.IntakeDraft) (core.Preflight, error) {
	sub := draftString(draft, "source", "path")
	if sub == "" {
		sub = clone.Path
	}
	tree, err := github.BuildTreeWith(clone.Root, sub, github.AutoSelectCode)
	if err != nil {
		return core.Preflight{}, err
	}
	return core.Preflight{Adapter: Key, Panels: []core.VerificationPanel{}, Tree: tree.Nodes, Lines: tree.SelectedLines, Code: tree.SelectedCode, Advisories: []core.Advisory{}}, nil
}

// AutoSelect is the repository's default scope, for the tree endpoint.
func (a *Adapter) AutoSelect(path string) bool { return github.AutoSelectCode(path) }

func (a *Adapter) LocationKinds() []core.LocationKind {
	return []core.LocationKind{"file"}
}

// OwnershipProof: a repository is proven by the GitHub installation that
// grants it, so a public one has nothing to prove here.
func (a *Adapter) OwnershipProof() core.OwnershipProofSpec {
	return core.OwnershipProofSpec{Method: "github_installation"}
}
