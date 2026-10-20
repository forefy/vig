package solana

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/forefy/vig/backend/internal/chain"
	"github.com/forefy/vig/backend/internal/core"
	"github.com/forefy/vig/backend/internal/github"
	"github.com/forefy/vig/backend/internal/sbpf"
)

// Facts is everything the panel renders for one program.
type Facts struct {
	ProgramID        string
	Exists           bool
	Loader           string
	UpgradeAuthority string
	LastDeploySlot   uint64
	DeployedAt       time.Time
	BytecodeSHA256   string
	IDLPresent       bool
	IDLVersion       string

	DeclaredMatch string // match, mismatch, not_found, unknown
	DeclaredFile  string

	VerifiedBuild  core.MatchStatus
	VerifiedCommit string
	VerifiedRepo   string

	OwnershipProven bool

	// What the source declares for this program, against the on-chain IDL.
	// SourceIx nil means the checkout was not read; SourceFound false means
	// no crate in it declares the id.
	SourceIx    []string
	SourceFound bool
	IDLIx       []string
	IDLRead     bool

	Profile    *core.CodeProfile // offline bytecode profile, nil when the image could not be read
	Disclosure *core.Disclosure  // security.txt from the image, nil when absent
}

// Inspector fetches chain facts and the registry answer for a program.
type Inspector struct {
	Chain *chain.Client
	Osec  *chain.Osec
}

// Inspect gathers the facts for a program. cloneRoot may be empty; then the
// declared id row is unknown. Registry failures degrade to unverified.
func (in *Inspector) Inspect(ctx context.Context, programID, cloneRoot string) (Facts, error) {
	f := Facts{ProgramID: programID, DeclaredMatch: "unknown", VerifiedBuild: core.MatchUnverified}
	pf, err := in.Chain.Program(ctx, programID)
	if err != nil {
		return f, err
	}
	f.Exists, f.Loader, f.UpgradeAuthority = pf.Exists, pf.Loader, pf.UpgradeAuthority
	f.LastDeploySlot, f.DeployedAt, f.BytecodeSHA256 = pf.LastDeploySlot, pf.DeployedAt, pf.BytecodeSHA256
	f.IDLPresent, f.IDLVersion = pf.IDLPresent, pf.IDLVersion
	if !f.Exists {
		return f, nil
	}
	if len(pf.Bytecode) > 0 {
		if prof, err := sbpf.Analyze(pf.Bytecode); err == nil {
			f.Profile = toProfile(prof)
		}
		f.Disclosure = Disclosure(SecurityTxt(pf.Bytecode))
	}
	if cloneRoot != "" {
		ids := github.DeclaredIDs(cloneRoot)
		if file, ok := ids[programID]; ok {
			f.DeclaredMatch, f.DeclaredFile = "match", file
		} else if len(ids) > 0 {
			f.DeclaredMatch = "mismatch"
		} else {
			f.DeclaredMatch = "not_found"
		}
	}
	if in.Osec != nil {
		if vb, err := in.Osec.Status(ctx, programID); err == nil && vb.Verified {
			f.VerifiedBuild = core.MatchVerified
			f.VerifiedCommit = vb.Commit
			f.VerifiedRepo = vb.RepoURL
			if vb.OnChainHash != "" && vb.ExecutableHash != "" && vb.OnChainHash != vb.ExecutableHash {
				f.VerifiedBuild = core.MatchMismatch
			}
		}
	}
	return f, nil
}

// idlInstructions lists the instruction names the on-chain IDL declares.
// ok is false when the IDL could not be read or parsed.
func (in *Inspector) idlInstructions(ctx context.Context, programID string) ([]string, bool) {
	raw, err := in.Chain.IDL(ctx, programID)
	if err != nil {
		return nil, false
	}
	idl, err := parseIDL(raw)
	if err != nil {
		return nil, false
	}
	names := make([]string, 0, len(idl.Instructions))
	for _, ix := range idl.Instructions {
		names = append(names, ix.Name)
	}
	return names, true
}

// toProfile maps the sBPF profile onto the chain-neutral shape.
func toProfile(p sbpf.Profile) *core.CodeProfile {
	out := &core.CodeProfile{Size: p.Size, CodeSize: p.TextSize, Units: p.Instructions, Functions: p.Functions, Framework: p.Framework,
		Capabilities: p.Capabilities, Imports: p.Syscalls, Sources: p.SourcePaths, Symbols: p.AnchorIxs, Types: p.AnchorAccts}
	if p.Rustc != "" {
		out.Toolchain = "rustc " + p.Rustc[:12]
	}
	for _, c := range p.Crates {
		out.Components = append(out.Components, core.Component{Name: c.Name, Version: c.Version})
	}
	if out.Capabilities == nil {
		out.Capabilities = []string{}
	}
	if out.Imports == nil {
		out.Imports = []string{}
	}
	return out
}

// Panel renders the ten SPEC section 7 rows from the facts, plus the
// bytecode profile when the image was readable.
func Panel(f Facts) core.VerificationPanel {
	p := panelRows(f)
	p.Profile = f.Profile
	p.Disclosure = f.Disclosure
	return p
}

func panelRows(f Facts) core.VerificationPanel {
	rows := map[string]core.PanelRow{}
	if !f.Exists {
		rows[RowExists] = core.PanelRow{Value: "not found", State: core.BadgeBad}
		for _, k := range RowOrder[1:] {
			rows[k] = core.PanelRow{State: core.BadgeNeutral}
		}
		return assemble(f.ProgramID, rows)
	}
	rows[RowExists] = core.PanelRow{Value: "found", State: core.BadgeOK}
	switch f.Loader {
	case "upgradeable":
		rows[RowLoader] = core.PanelRow{Value: "upgradeable", State: core.BadgeOK}
	case "non-upgradeable", "loader_v4":
		rows[RowLoader] = core.PanelRow{Value: f.Loader, State: core.BadgeNeutral}
	default:
		rows[RowLoader] = core.PanelRow{Value: "unknown", State: core.BadgeWarn}
	}
	if f.UpgradeAuthority != "" {
		rows[RowAuthority] = core.PanelRow{Value: f.UpgradeAuthority, State: core.BadgeOK}
	} else {
		rows[RowAuthority] = core.PanelRow{Value: "none", Detail: "immutable", State: core.BadgeNeutral}
	}
	if f.LastDeploySlot > 0 {
		r := core.PanelRow{Value: fmt.Sprintf("%d", f.LastDeploySlot), State: core.BadgeOK}
		if !f.DeployedAt.IsZero() {
			r.Detail = f.DeployedAt.UTC().Format("2006-01-02")
		}
		rows[RowLastDeployed] = r
	} else {
		rows[RowLastDeployed] = core.PanelRow{Value: "unknown", State: core.BadgeNeutral}
	}
	if f.BytecodeSHA256 != "" {
		rows[RowBytecodeHash] = core.PanelRow{Value: f.BytecodeSHA256, State: core.BadgeOK, Action: "copy"}
	} else {
		rows[RowBytecodeHash] = core.PanelRow{Value: "unknown", State: core.BadgeNeutral}
	}
	if f.IDLPresent {
		rows[RowIDL] = core.PanelRow{Value: "present", Detail: f.IDLVersion, State: core.BadgeOK}
	} else {
		rows[RowIDL] = core.PanelRow{Value: "absent", State: core.BadgeNeutral}
	}
	switch f.DeclaredMatch {
	case "match":
		rows[RowDeclaredID] = core.PanelRow{Value: "match", Detail: f.DeclaredFile, State: core.BadgeOK}
	case "mismatch":
		rows[RowDeclaredID] = core.PanelRow{Value: "mismatch", State: core.BadgeBad}
	case "not_found":
		rows[RowDeclaredID] = core.PanelRow{Value: "not found", State: core.BadgeWarn}
	default:
		rows[RowDeclaredID] = core.PanelRow{Value: "pending", State: core.BadgePending}
	}
	switch f.VerifiedBuild {
	case core.MatchVerified:
		rows[RowVerifiedBuild] = core.PanelRow{Value: "verified", Detail: short(f.VerifiedCommit), State: core.BadgeOK}
		rows[RowSourceMatch] = core.PanelRow{Value: "verified", Detail: short(f.VerifiedCommit), State: core.BadgeOK}
	case core.MatchMismatch:
		rows[RowVerifiedBuild] = core.PanelRow{Value: "mismatch", State: core.BadgeBad}
		rows[RowSourceMatch] = core.PanelRow{Value: "mismatch", State: core.BadgeBad}
	case core.MatchSelfBuilt:
		rows[RowVerifiedBuild] = core.PanelRow{Value: "unverified", State: core.BadgeNeutral}
		rows[RowSourceMatch] = core.PanelRow{Value: "self-built match", State: core.BadgeOK}
	default:
		rows[RowVerifiedBuild] = core.PanelRow{Value: "unverified", State: core.BadgeNeutral}
		rows[RowSourceMatch] = core.PanelRow{Value: "pending", State: core.BadgePending}
	}
	rows[RowSourceIDL] = sourceIDLRow(f)
	switch {
	case f.OwnershipProven:
		rows[RowOwnership] = core.PanelRow{Value: "proven", State: core.BadgeOK}
	case f.UpgradeAuthority == "":
		rows[RowOwnership] = core.PanelRow{Value: "immutable", State: core.BadgeNeutral}
	default:
		rows[RowOwnership] = core.PanelRow{Value: "not proven", State: core.BadgeNeutral, Action: "prove_ownership"}
	}
	return assemble(f.ProgramID, rows)
}

// MatchRows rebuilds the verified-build and source-match rows from an
// operator's match result so the panel agrees with the recorded columns.
// The other rows are left as they were at intake.
func MatchRows(panel core.VerificationPanel, pin core.ProgramPin) core.VerificationPanel {
	if len(panel.Rows) == 0 {
		panel = EmptyPanel(pin.ID)
	}
	f := Facts{VerifiedBuild: pin.Match}
	fresh := map[string]core.PanelRow{}
	switch f.VerifiedBuild {
	case core.MatchVerified:
		fresh[RowVerifiedBuild] = core.PanelRow{Value: "verified", State: core.BadgeOK}
		fresh[RowSourceMatch] = core.PanelRow{Value: "verified", State: core.BadgeOK}
	case core.MatchMismatch:
		fresh[RowVerifiedBuild] = core.PanelRow{Value: "mismatch", State: core.BadgeBad}
		fresh[RowSourceMatch] = core.PanelRow{Value: "mismatch", State: core.BadgeBad}
	case core.MatchSelfBuilt:
		fresh[RowVerifiedBuild] = core.PanelRow{Value: "unverified", State: core.BadgeNeutral}
		fresh[RowSourceMatch] = core.PanelRow{Value: "self-built match", State: core.BadgeOK}
	default:
		fresh[RowVerifiedBuild] = core.PanelRow{Value: "unverified", State: core.BadgeNeutral}
		fresh[RowSourceMatch] = core.PanelRow{Value: "unverified", State: core.BadgeNeutral}
	}
	for i := range panel.Rows {
		r := &panel.Rows[i]
		switch r.Key {
		case RowVerifiedBuild:
			// Keep the registry commit when the operator confirms a verified build.
			detail := ""
			if pin.Match == core.MatchVerified {
				detail = r.Detail
			}
			*r = core.PanelRow{Key: r.Key, Label: RowLabels[r.Key], Value: fresh[r.Key].Value, State: fresh[r.Key].State, Detail: detail}
		case RowSourceMatch:
			*r = core.PanelRow{Key: r.Key, Label: RowLabels[r.Key], Value: fresh[r.Key].Value, State: fresh[r.Key].State, Detail: short(pin.BytecodeSHA256)}
		case RowBytecodeHash:
			if pin.BytecodeSHA256 != "" {
				*r = core.PanelRow{Key: r.Key, Label: RowLabels[r.Key], Value: pin.BytecodeSHA256, State: core.BadgeOK, Action: "copy"}
			}
		case RowLastDeployed:
			if pin.Slot > 0 {
				r.Value = fmt.Sprintf("%d", pin.Slot)
				r.State = core.BadgeOK
			}
		}
	}
	return panel
}

func assemble(subject string, rows map[string]core.PanelRow) core.VerificationPanel {
	out := core.VerificationPanel{Subject: subject, Rows: make([]core.PanelRow, 0, len(RowOrder))}
	for _, k := range RowOrder {
		r := rows[k]
		r.Key, r.Label = k, RowLabels[k]
		if r.State == "" {
			r.State = core.BadgePending
		}
		out.Rows = append(out.Rows, r)
	}
	return out
}

func short(sha string) string {
	if len(sha) > 10 {
		return sha[:10]
	}
	return sha
}

// OwnershipMessage is the text the upgrade authority signs.
func OwnershipMessage(nonce, programID string, at time.Time) string {
	return strings.Join([]string{"vig ownership proof", nonce, programID, at.UTC().Format(time.RFC3339)}, " ")
}
