package core

import (
	"context"
	"errors"
)

// FieldKind tells the wizard engine how to render an intake field.
type FieldKind string

const (
	FieldCards   FieldKind = "cards"   // pick one of several options
	FieldText    FieldKind = "text"    // single line input with validation
	FieldSelect  FieldKind = "select"  // dropdown
	FieldTree    FieldKind = "tree"    // file tree with checkboxes and a meter
	FieldList    FieldKind = "list"    // repeatable entries (e.g. program ids), each with a panel
	FieldSummary FieldKind = "summary" // read-only pin line
)

// Option is one card or select entry.
type Option struct {
	Value    string `json:"value"`
	Label    string `json:"label"`
	Hint     string `json:"hint,omitempty" doc:"One short fact under the label, e.g. the chains served"`
	Disabled bool   `json:"disabled,omitempty"`
	Soon     bool   `json:"soon,omitempty"`
}

// FieldSpec is one input on a step.
type FieldSpec struct {
	Key      string    `json:"key"`
	Kind     FieldKind `json:"kind" enum:"cards,text,select,tree,list,summary"`
	Label    string    `json:"label"`
	Options  []Option  `json:"options,omitempty"`
	Pattern  string    `json:"pattern,omitempty" doc:"Regex the value must match"`
	Required bool      `json:"required"`
	Panel    bool      `json:"panel,omitempty" doc:"Each list entry renders a verification panel"`
}

// StepSpec is one wizard step. Steps are declared by the adapter and rendered
// by the shared wizard engine.
type StepSpec struct {
	Key    string      `json:"key"`
	Title  string      `json:"title"`
	Fields []FieldSpec `json:"fields"`
}

// BadgeState is how a verification row renders.
type BadgeState string

const (
	BadgeOK      BadgeState = "ok"
	BadgeNeutral BadgeState = "neutral"
	BadgeWarn    BadgeState = "warn"
	BadgeBad     BadgeState = "bad"
	BadgePending BadgeState = "pending"
)

// PanelRow is one verified fact.
type PanelRow struct {
	Key    string     `json:"key"`
	Label  string     `json:"label"`
	Value  string     `json:"value,omitempty"`
	State  BadgeState `json:"state" enum:"ok,neutral,warn,bad,pending"`
	Detail string     `json:"detail,omitempty" doc:"Secondary value such as a commit or a version"`
	Action string     `json:"action,omitempty" doc:"Client action key, e.g. prove_ownership"`
}

// VerificationPanel replaces help text: facts as badges.
type VerificationPanel struct {
	Subject    string       `json:"subject" doc:"What was verified, e.g. a program id"`
	Rows       []PanelRow   `json:"rows"`
	Profile    *CodeProfile `json:"profile,omitempty" doc:"What the deployed code reveals offline; present when the target's bytes could be read"`
	Disclosure *Disclosure  `json:"disclosure,omitempty" doc:"What the deployer wrote into the deployed code about it; a claim, not a verification"`
}

// Disclosure is the chain-neutral shape of a self-description embedded in
// deployed code (for Solana, security.txt). Every field is the deployer's
// word; the panel rows say what was checked.
type Disclosure struct {
	Name          string   `json:"name,omitempty"`
	ProjectURL    string   `json:"project_url,omitempty"`
	SourceURL     string   `json:"source_url,omitempty"`
	SourceRelease string   `json:"source_release,omitempty"`
	Policy        string   `json:"policy,omitempty" doc:"Where the disclosure policy lives"`
	Contacts      []string `json:"contacts,omitempty"`
	Auditors      []string `json:"auditors,omitempty"`
	Expiry        string   `json:"expiry,omitempty"`
}

// CodeProfile is the chain-neutral shape of an offline bytecode profile. An
// adapter fills it from its own analysis (for Solana, internal/sbpf). It is
// the reverse-engineering baseline for programs with no verified source.
type CodeProfile struct {
	Size         int         `json:"size" doc:"Image size in bytes"`
	CodeSize     int         `json:"code_size" doc:"Executable bytes"`
	Units        int         `json:"units" doc:"Instructions"`
	Functions    int         `json:"functions"`
	Framework    string      `json:"framework"`
	Capabilities []string    `json:"capabilities" doc:"What the runtime imports imply: cpi, pda, logging, crypto, sysvars, return-data, introspection, heap, compute-meter, panics"`
	Imports      []string    `json:"imports" doc:"Runtime calls the code imports"`
	Components   []Component `json:"components,omitempty" doc:"Linked libraries with versions"`
	Sources      []string    `json:"sources,omitempty" doc:"Source files named inside the image"`
	Symbols      []string    `json:"symbols,omitempty" doc:"Instruction names recovered from the image"`
	Types        []string    `json:"types,omitempty" doc:"Account or data type names recovered from the image"`
	Toolchain    string      `json:"toolchain,omitempty" doc:"Compiler identity when the image names it"`
}

// Component is one linked dependency.
type Component struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// Issue is a validation problem tied to a field.
type Issue struct {
	Step  string `json:"step"`
	Field string `json:"field"`
	Msg   string `json:"msg"`
}

// IntakeDraft is the wizard's accumulated answers, keyed by step then field.
type IntakeDraft map[string]map[string]any

// Clone is a checked-out source tree the adapter may inspect.
type Clone struct {
	Root   string
	Repo   string
	Commit string
	Path   string // subdirectory the target lives in, repo-relative
}

// TreeNode is a file or directory with its auto-selection.
type TreeNode struct {
	Path     string     `json:"path"`
	Dir      bool       `json:"dir,omitempty"`
	Bytes    int64      `json:"bytes,omitempty"`
	Lines    int        `json:"lines,omitempty"`
	Code     int        `json:"code,omitempty" doc:"Lines with code: no blanks, no comments"`
	Selected bool       `json:"selected"`
	Changed  bool       `json:"changed,omitempty" doc:"Differs from the base commit (diff coverage only)"`
	Children []TreeNode `json:"children,omitempty"`
}

// Preflight is the deterministic scan: panel facts, tree, lines, advisories
// and what the source declares.
type Preflight struct {
	Adapter    string              `json:"adapter,omitempty" enum:"solana,ethereum,webapp,repo" doc:"The adapter that read the checkout"`
	Panels     []VerificationPanel `json:"panels"`
	Tree       []TreeNode          `json:"tree"`
	Lines      int                 `json:"lines" doc:"Lines in the auto-selected scope"`
	Code       int                 `json:"code" doc:"Code lines in the auto-selected scope; the scope cap applies to this"`
	Advisories []Advisory          `json:"advisories,omitempty"`
	Source     *SourceSummary      `json:"source,omitempty" doc:"What the checked-out source declares, when the adapter can read it"`
}

// SourceSummary is the source in numbers: the entrypoints it declares and
// what the code they reach does. Chain-neutral; the adapter fills it.
type SourceSummary struct {
	Programs     int `json:"programs"`
	Instructions int `json:"instructions" doc:"Entrypoints found in source"`
	Accounts     int `json:"accounts"`
	Raw          int `json:"raw" doc:"Accounts the framework does not type-check"`
	CPI          int `json:"cpi" doc:"Outbound calls to other programs in reachable code"`
	SignedCPI    int `json:"signed_cpi"`
	PDA          int `json:"pda" doc:"Program-derived address derivations"`
	Token        int `json:"token" doc:"Token operations"`
	Unsafe       int `json:"unsafe" doc:"unsafe blocks"`
	Code         int `json:"code" doc:"Code lines across program crates"`
}

// Advisory is a known dependency issue from a free advisory database.
type Advisory struct {
	ID       string `json:"id"`
	Package  string `json:"package"`
	Version  string `json:"version"`
	Severity string `json:"severity"`
	URL      string `json:"url"`
}

// OwnershipProofSpec tells the client how to prove ownership of a subject.
type OwnershipProofSpec struct {
	Method string `json:"method" doc:"e.g. sign_nonce_with_upgrade_authority"`
}

// FileSelector is implemented by an adapter whose default scope is not the
// chain adapters' (Rust programs and Solidity contracts): the tree endpoint
// asks it which files start selected.
type FileSelector interface {
	AutoSelect(path string) bool
}

// LocationKind is a way a finding can point at code for this target.
type LocationKind string

// ProgramFacts is what an adapter learned about one deployed program or
// contract, in the shape the target store keeps: chain-neutral words for
// chain-specific things. Loader is the deploy mechanism (a Solana loader,
// an EVM chain), the authority whoever can change the code, the slot the
// height it was last deployed at, the hash its normalised bytes.
type ProgramFacts struct {
	ProgramID        string
	Chain            string // empty for Solana mainnet; an EVM chain id otherwise
	Exists           bool
	Loader           string
	UpgradeAuthority string
	LastDeploySlot   uint64
	BytecodeSHA256   string
	IDLPresent       bool   // an interface is on record: an IDL, a verified ABI
	IDLVersion       string // its kind or version
	VerifiedBuild    MatchStatus
	VerifiedCommit   string
	OwnershipProven  bool
	Panel            VerificationPanel
}

// InspectOpts qualifies a program read.
type InspectOpts struct {
	Chain string // which chain, for adapters that serve several
	Root  string // a checkout to compare the source against, when there is one
}

// ErrNotWired is returned by an adapter without chain access.
var ErrNotWired = errors.New("adapter: chain access not wired")

// ProgramInspector is the optional adapter capability behind the wizard's
// program step: one program id in, its facts and panel out.
type ProgramInspector interface {
	InspectProgram(ctx context.Context, id string, opts InspectOpts) (ProgramFacts, error)
	// ValidProgramID says whether a string is an address on this adapter's chain.
	ValidProgramID(id string) bool
}

// Adapter is what a target type implements. Solana is adapter one. The core
// packages never import a chain SDK; they call this interface.
type Adapter interface {
	Key() string
	IntakeSteps() []StepSpec
	Validate(ctx context.Context, draft IntakeDraft) ([]Issue, error)
	Verify(ctx context.Context, draft IntakeDraft) ([]VerificationPanel, error)
	Preflight(ctx context.Context, clone Clone, draft IntakeDraft) (Preflight, error)
	LocationKinds() []LocationKind
	OwnershipProof() OwnershipProofSpec
}
