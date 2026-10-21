package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/forefy/vig/backend/internal/adapter/solana"
	"github.com/forefy/vig/backend/internal/auth"
	"github.com/forefy/vig/backend/internal/chain"
	"github.com/forefy/vig/backend/internal/core"
	"github.com/forefy/vig/backend/internal/github"
	"github.com/forefy/vig/backend/internal/store"
)

type createTargetIn struct {
	Body struct {
		Adapter        string   `json:"adapter" doc:"An adapter key from /v1/adapters"`
		BaseURL        string   `json:"base_url,omitempty" format:"uri" doc:"For a site, the origin the audit is about: scheme and host"`
		Hosts          []string `json:"hosts,omitempty" doc:"For a site, the hosts the review may probe; the origin's host is always among them"`
		Paths          []string `json:"paths,omitempty" doc:"For a site, the path prefixes the review may walk on every scoped host"`
		SourceKind     string   `json:"source_kind,omitempty" enum:"public,installation" doc:"Where the repository comes from; a site without one leaves it out"`
		RepoURL        string   `json:"repo_url,omitempty"`
		InstallationID string   `json:"installation_id,omitempty"`
		Repo           string   `json:"repo,omitempty" doc:"owner/name when using an installation"`
		Selector       string   `json:"selector,omitempty" enum:"branch,tag,commit"`
		Revision       string   `json:"revision,omitempty"`
	}
}

type addProgramIn struct {
	ID   string `path:"id"`
	Body struct {
		ProgramID string `json:"program_id"`
		Chain     string `json:"chain,omitempty" doc:"Chain id, for adapters that serve several"`
	}
}

type programPath struct {
	ID  string `path:"id"`
	PID string `path:"pid"`
}

type pidPath struct {
	PID string `path:"pid"`
}

// ProgramSource is where a program was built from. Sources are asked in
// order of trust: the verified-build registry, the security.txt the deployer
// embedded in the image, the repository its on-chain IDL names, and last a
// public code search for the address. Only the registry can verify.
type ProgramSource struct {
	ID       string `json:"id" doc:"Program address"`
	Origin   string `json:"origin,omitempty" enum:"registry,security_txt,idl,search" doc:"Where the repo came from; empty when nothing names one"`
	Verified bool   `json:"verified" doc:"On-chain hash matches the registry build of repo_url at revision"`
	RepoURL  string `json:"repo_url,omitempty" doc:"https://github.com/owner/name, no revision"`
	Selector string `json:"selector,omitempty" enum:"branch,tag,commit"`
	Revision string `json:"revision,omitempty"`
}

type ownershipNonceOut struct {
	Body struct {
		Nonce   string `json:"nonce"`
		Message string `json:"message"`
	}
}

type ownershipVerifyIn struct {
	ID   string `path:"id"`
	PID  string `path:"pid"`
	Body struct {
		Signature string `json:"signature" doc:"base58 signature over the message by the upgrade authority"`
	}
}

type treeIn struct {
	ID   string `path:"id"`
	Base string `query:"base" doc:"Diff base (branch, tag or sha); marks the files changed since it"`
}

// OwnershipNonceTTL is how long a nonce may be signed after it was issued.
const OwnershipNonceTTL = 10 * time.Minute

func (d *Deps) registerTargets(api huma.API) {
	huma.Register(api, withAuth(op("createTarget", http.MethodPost, "/v1/targets", "Resolve a source into a target", "targets"), userAuth), d.createTarget)
	huma.Register(api, withAuth(op("getTarget", http.MethodGet, "/v1/targets/{id}", "Target", "targets"), userAuth), d.getTarget)
	huma.Register(api, withAuth(op("addProgram", http.MethodPost, "/v1/targets/{id}/programs", "Fetch a program from chain and verify it", "targets"), userAuth), d.addProgram)
	huma.Register(api, withAuth(op("programSource", http.MethodGet, "/v1/programs/{pid}/source", "Repository and revision a program was built from", "targets"), userAuth), d.programSource)
	huma.Register(api, withAuth(op("removeProgram", http.MethodDelete, "/v1/targets/{id}/programs/{pid}", "Remove program", "targets"), userAuth), d.removeProgram)
	huma.Register(api, withAuth(op("ownershipNonce", http.MethodPost, "/v1/targets/{id}/programs/{pid}/ownership/nonce", "Nonce to sign with the upgrade authority", "targets"), userAuth), d.ownershipNonce)
	huma.Register(api, withAuth(op("ownershipVerify", http.MethodPost, "/v1/targets/{id}/programs/{pid}/ownership/verify", "Prove ownership", "targets"), userAuth), d.ownershipVerify)
	huma.Register(api, withAuth(op("getTree", http.MethodGet, "/v1/targets/{id}/tree", "File tree with auto selection", "targets"), userAuth), d.getTree)
	d.registerSites(api)
}

// tier returns the caps that apply to the user: Pro while a plan is active,
// otherwise the single-program Mini caps that also cover the free credit.
func (d *Deps) tier(ctx context.Context, userID string) core.Plan {
	if _, err := d.Store.GetPlan(ctx, userID); err == nil {
		if p, ok := core.PlanBySKU(core.SKUPro); ok {
			return p
		}
	}
	p, _ := core.PlanBySKU(core.SKUMini)
	return p
}

// ownedTarget loads a target the caller may use.
func (d *Deps) ownedTarget(ctx context.Context, id string) (store.Target, store.User, error) {
	u, err := d.user(ctx)
	if err != nil {
		return store.Target{}, u, err
	}
	t, err := d.Store.GetTarget(ctx, id)
	if err != nil {
		return t, u, fail(err)
	}
	if t.OwnerUserID != u.ID && u.Role != "admin" {
		return t, u, huma.Error404NotFound("not found")
	}
	return t, u, nil
}

func (d *Deps) sourceReady() error {
	if d.GitHub == nil {
		return huma.Error503ServiceUnavailable("source access not configured")
	}
	return nil
}

func (d *Deps) createTarget(ctx context.Context, in *createTargetIn) (*body[Target], error) {
	u, err := d.user(ctx)
	if err != nil {
		return nil, err
	}
	adapter, ok := d.Adapters[in.Body.Adapter]
	if !ok {
		return nil, huma.Error400BadRequest("unknown adapter " + in.Body.Adapter)
	}
	// The adapter's steps say what a target is: a site step makes it a
	// live origin (handlers_sites.go), a source step a repository.
	if hasStep(adapter, StepSite) {
		return d.createSiteTarget(ctx, u, adapter, in)
	}
	if in.Body.SourceKind == "" {
		return nil, huma.Error400BadRequest("source_kind is required")
	}
	if err := d.sourceReady(); err != nil {
		return nil, err
	}
	var ref github.Ref
	gh := d.GitHub
	var token, installationID string
	if in.Body.SourceKind == "installation" {
		if err := d.appReady(); err != nil {
			return nil, err
		}
		inst, err := d.Store.GetInstallation(ctx, u.ID, in.Body.InstallationID)
		if err != nil {
			return nil, fail(err)
		}
		if inst.Status != "active" {
			return nil, huma.Error409Conflict("installation is " + inst.Status)
		}
		owner, name, ok := strings.Cut(strings.TrimSpace(in.Body.Repo), "/")
		if !ok || owner == "" || name == "" {
			return nil, huma.Error400BadRequest("repo must be owner/name")
		}
		ref = github.Ref{Owner: owner, Name: name}
		tok, err := d.App.MintToken(ctx, inst.InstallationID, ref.FullName())
		if err != nil {
			return nil, huma.Error502BadGateway("GitHub App token: " + err.Error())
		}
		token = tok.Token
		gh = gh.WithToken(token)
		installationID = inst.ID
	} else {
		var err error
		ref, err = github.ParseURL(in.Body.RepoURL)
		if err != nil {
			return nil, huma.Error400BadRequest(err.Error())
		}
	}
	rev := strings.TrimSpace(in.Body.Revision)
	if rev == "" {
		rev = ref.Rev
	}
	selector := in.Body.Selector
	if rev == "" {
		rev, err = gh.DefaultBranch(ctx, ref)
		if err != nil {
			return nil, fail(err)
		}
		selector = "branch"
	}
	commit, err := gh.Resolve(ctx, ref, rev)
	if err != nil {
		return nil, fail(err)
	}
	t, err := d.Store.CreateTarget(ctx, store.Target{
		Adapter: in.Body.Adapter, OwnerUserID: u.ID, SourceKind: in.Body.SourceKind, Repo: ref.FullName(), Subpath: ref.Path, InstallationID: installationID,
		Selector: selector, Revision: rev, CommitSHA: commit.SHA, CommitAuthor: commit.Author, CommitDate: commit.Date,
	})
	if err != nil {
		return nil, fail(err)
	}
	// Warm the tarball cache so the tree and the declared-id check are
	// quick. Bounded; when no slot is free the first tree request fetches.
	d.background(func(ctx context.Context) { _, _ = d.GitHub.Checkout(ctx, t.Repo, t.CommitSHA, token) })
	out, err := d.toTarget(ctx, t)
	if err != nil {
		return nil, fail(err)
	}
	return &body[Target]{Body: out}, nil
}

func (d *Deps) getTarget(ctx context.Context, in *idPath) (*body[Target], error) {
	t, _, err := d.ownedTarget(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	out, err := d.toTarget(ctx, t)
	if err != nil {
		return nil, fail(err)
	}
	return &body[Target]{Body: out}, nil
}

func (d *Deps) addProgram(ctx context.Context, in *addProgramIn) (*body[Program], error) {
	t, u, err := d.ownedTarget(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	// The target's adapter reads the program: each chain has its own
	// address shape and its own facts.
	insp, ok := d.Adapters[t.Adapter].(core.ProgramInspector)
	if !ok {
		return nil, huma.Error503ServiceUnavailable("adapter cannot read programs")
	}
	if !insp.ValidProgramID(in.Body.ProgramID) {
		return nil, huma.Error400BadRequest("program_id is not an address on " + t.Adapter)
	}
	existing, err := d.Store.ListPrograms(ctx, t.ID)
	if err != nil {
		return nil, fail(err)
	}
	var prev *store.Program
	for i := range existing {
		if strings.EqualFold(existing[i].ProgramID, in.Body.ProgramID) {
			prev = &existing[i]
		}
	}
	if prev == nil {
		if cap := d.tier(ctx, u.ID).MaxPrograms; len(existing) >= cap {
			return nil, huma.Error409Conflict("program cap for your tier reached")
		}
	}
	root := ""
	if d.GitHub != nil {
		if dir, err := d.checkout(ctx, t); err == nil {
			root = dir
		}
	}
	facts, err := insp.InspectProgram(ctx, in.Body.ProgramID, core.InspectOpts{Chain: in.Body.Chain, Root: root})
	if err != nil {
		if errors.Is(err, solana.ErrNotWired) || errors.Is(err, core.ErrNotWired) {
			return nil, huma.Error503ServiceUnavailable("chain access not configured")
		}
		return nil, huma.Error502BadGateway("chain read failed: " + err.Error())
	}
	if prev != nil && prev.OwnershipProvenAt != nil {
		facts.OwnershipProven = true
	}
	// The store keeps the adapter's canonical spelling of the id, the one
	// its surface nodes and evidence use, not the one that was typed.
	programID := facts.ProgramID
	if programID == "" {
		programID = in.Body.ProgramID
	}
	p, err := d.Store.UpsertProgram(ctx, store.Program{
		TargetID: t.ID, ProgramID: programID, Chain: facts.Chain, Loader: facts.Loader, UpgradeAuthority: facts.UpgradeAuthority,
		LastDeploySlot: facts.LastDeploySlot, BytecodeSHA256: facts.BytecodeSHA256, IDLPresent: facts.IDLPresent, AnchorVersion: facts.IDLVersion,
		VerifiedBuild: facts.VerifiedBuild, VerifiedCommit: facts.VerifiedCommit, Panel: facts.Panel,
	})
	if err != nil {
		return nil, fail(err)
	}
	return &body[Program]{Body: toProgram(p)}, nil
}

// programSource finds the repo a program was built from, trying each source
// in ProgramSource's order and stopping at the first that names one.
func (d *Deps) programSource(ctx context.Context, in *pidPath) (*body[ProgramSource], error) {
	if _, err := d.user(ctx); err != nil {
		return nil, err
	}
	if !chain.ValidPubkey(in.PID) {
		return nil, huma.Error400BadRequest("pid is not a Solana address")
	}
	if d.Inspector == nil || d.Inspector.Osec == nil {
		return nil, huma.Error503ServiceUnavailable("registry access not configured")
	}
	out := ProgramSource{ID: in.PID}
	if vb, err := d.Inspector.Osec.Status(ctx, in.PID); err == nil {
		if ref, err := github.ParseURL(vb.RepoURL); err == nil {
			out.Origin, out.Verified = "registry", vb.Verified
			out.RepoURL = "https://github.com/" + ref.FullName()
			if vb.Commit != "" {
				out.Selector, out.Revision = "commit", vb.Commit
			} else if ref.Rev != "" {
				out.Selector, out.Revision = "commit", ref.Rev
			}
			return &body[ProgramSource]{Body: out}, nil
		}
	}
	if d.Inspector.Chain == nil {
		return &body[ProgramSource]{Body: out}, nil
	}
	pf, err := d.Inspector.Chain.Program(ctx, in.PID)
	if err != nil || !pf.Exists {
		return &body[ProgramSource]{Body: out}, nil
	}
	if st := solana.SecurityTxt(pf.Bytecode); st != nil {
		if ref, err := github.ParseURL(st["source_code"]); err == nil {
			out.Origin = "security_txt"
			out.RepoURL = "https://github.com/" + ref.FullName()
			if rel := strings.TrimSpace(st["source_release"]); rel != "" && d.GitHub != nil {
				if _, err := d.GitHub.Resolve(ctx, ref, rel); err == nil {
					out.Selector, out.Revision = "tag", rel
					return &body[ProgramSource]{Body: out}, nil
				}
			}
			if ref.Rev != "" {
				out.Selector, out.Revision = "branch", ref.Rev
			}
			return &body[ProgramSource]{Body: out}, nil
		}
	}
	if ref, err := github.ParseURL(pf.IDLRepository); err == nil {
		out.Origin, out.RepoURL = "idl", "https://github.com/"+ref.FullName()
		if ref.Rev != "" {
			out.Selector, out.Revision = "branch", ref.Rev
		}
		return &body[ProgramSource]{Body: out}, nil
	}
	if d.GitHub != nil {
		if repos, err := d.GitHub.SearchCode(ctx, in.PID); err == nil && len(repos) > 0 {
			out.Origin, out.RepoURL = "search", "https://github.com/"+repos[0]
		}
	}
	return &body[ProgramSource]{Body: out}, nil
}

func (d *Deps) removeProgram(ctx context.Context, in *programPath) (*struct{}, error) {
	t, _, err := d.ownedTarget(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	if err := d.Store.DeleteProgram(ctx, t.ID, in.PID); err != nil {
		return nil, fail(err)
	}
	return nil, nil
}

func (d *Deps) ownershipNonce(ctx context.Context, in *programPath) (*ownershipNonceOut, error) {
	t, _, err := d.ownedTarget(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	p, err := d.Store.GetProgram(ctx, t.ID, in.PID)
	if err != nil {
		return nil, fail(err)
	}
	if p.UpgradeAuthority == "" {
		return nil, huma.Error409Conflict("program is immutable; there is no authority to sign")
	}
	nonce := auth.RandomToken(12)
	msg := solana.OwnershipMessage(nonce, p.ProgramID, d.now())
	if err := d.Store.SetOwnershipNonce(ctx, t.ID, p.ProgramID, msg); err != nil {
		return nil, fail(err)
	}
	out := &ownershipNonceOut{}
	out.Body.Nonce = nonce
	out.Body.Message = msg
	return out, nil
}

func (d *Deps) ownershipVerify(ctx context.Context, in *ownershipVerifyIn) (*body[Program], error) {
	t, _, err := d.ownedTarget(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	p, err := d.Store.GetProgram(ctx, t.ID, in.PID)
	if err != nil {
		return nil, fail(err)
	}
	if p.UpgradeAuthority == "" {
		return nil, huma.Error409Conflict("program is immutable")
	}
	if p.OwnershipNonce == "" {
		return nil, huma.Error409Conflict("request a nonce first")
	}
	if p.OwnershipNonceAt == nil || d.now().Sub(*p.OwnershipNonceAt) > OwnershipNonceTTL || !ownershipMessageFresh(p.OwnershipNonce, d.now()) {
		return nil, huma.Error409Conflict("nonce expired; request a new one")
	}
	sig, err := auth.DecodeSignature(in.Body.Signature)
	if err != nil {
		return nil, huma.Error400BadRequest(err.Error())
	}
	if err := auth.VerifyEd25519(p.UpgradeAuthority, []byte(p.OwnershipNonce), sig); err != nil {
		return nil, huma.Error400BadRequest("signature is not by the upgrade authority")
	}
	panel := p.Panel
	for i := range panel.Rows {
		if panel.Rows[i].Key == solana.RowOwnership {
			panel.Rows[i] = core.PanelRow{Key: solana.RowOwnership, Label: solana.RowLabels[solana.RowOwnership], Value: "proven", State: core.BadgeOK}
		}
	}
	if err := d.Store.SetOwnershipProven(ctx, t.ID, p.ProgramID, in.Body.Signature, panel); err != nil {
		return nil, fail(err)
	}
	p, err = d.Store.GetProgram(ctx, t.ID, p.ProgramID)
	if err != nil {
		return nil, fail(err)
	}
	return &body[Program]{Body: toProgram(p)}, nil
}

// ownershipMessageFresh reports whether the timestamp the message carries
// (its last field, RFC 3339) is within the nonce window of now.
func ownershipMessageFresh(msg string, now time.Time) bool {
	fields := strings.Fields(msg)
	if len(fields) == 0 {
		return false
	}
	at, err := time.Parse(time.RFC3339, fields[len(fields)-1])
	if err != nil {
		return false
	}
	age := now.Sub(at)
	return age >= -time.Minute && age <= OwnershipNonceTTL
}

// checkout returns the cached clone of a target, fetching it when needed.
// A private source mints an installation token only on a cache miss.
func (d *Deps) checkout(ctx context.Context, t store.Target) (string, error) {
	if err := d.sourceReady(); err != nil {
		return "", err
	}
	if dir, ok := d.GitHub.Cached(t.Repo, t.CommitSHA); ok {
		return dir, nil
	}
	token, err := d.sourceToken(ctx, t)
	if err != nil {
		return "", err
	}
	root, err := d.GitHub.Checkout(ctx, t.Repo, t.CommitSHA, token)
	if err != nil {
		if errors.Is(err, github.ErrNotFound) || errors.Is(err, github.ErrRateLimited) {
			return "", fail(err)
		}
		return "", huma.Error502BadGateway("source fetch failed: " + err.Error())
	}
	return root, nil
}

// resolveBase checks a diff base against the target's repository: it must
// resolve to a commit and differ from the head. The full sha is returned.
func (d *Deps) resolveBase(ctx context.Context, t store.Target, base string) (string, error) {
	base = strings.TrimSpace(base)
	if base == "" {
		return "", huma.Error400BadRequest("diff coverage needs base_sha")
	}
	if err := d.sourceReady(); err != nil {
		return "", err
	}
	ref, err := github.ParseFullName(t.Repo)
	if err != nil {
		return "", fail(err)
	}
	token, err := d.sourceToken(ctx, t)
	if err != nil {
		return "", err
	}
	commit, err := d.GitHub.WithToken(token).Resolve(ctx, ref, base)
	if err != nil {
		if errors.Is(err, github.ErrNotFound) {
			return "", huma.Error400BadRequest("base_sha does not resolve in " + t.Repo)
		}
		return "", fail(err)
	}
	if commit.SHA == t.CommitSHA {
		return "", huma.Error400BadRequest("base_sha is the audited commit; a diff needs an earlier one")
	}
	return commit.SHA, nil
}

// changedSince lists repository-relative paths that differ between base and
// the target's commit.
func (d *Deps) changedSince(ctx context.Context, t store.Target, base string) ([]string, error) {
	ref, err := github.ParseFullName(t.Repo)
	if err != nil {
		return nil, fail(err)
	}
	token, err := d.sourceToken(ctx, t)
	if err != nil {
		return nil, err
	}
	files, err := d.GitHub.WithToken(token).Compare(ctx, ref, base, t.CommitSHA)
	if err != nil {
		return nil, fail(err)
	}
	return files, nil
}

func (d *Deps) getTree(ctx context.Context, in *treeIn) (*body[Tree], error) {
	t, u, err := d.ownedTarget(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	base := ""
	if strings.TrimSpace(in.Base) != "" {
		base, err = d.resolveBase(ctx, t, in.Base)
		if err != nil {
			return nil, err
		}
	}
	root, err := d.checkout(ctx, t)
	if err != nil {
		return nil, err
	}
	tree, err := github.BuildTree(root, t.Subpath)
	if err != nil {
		return nil, fail(err)
	}
	if base != "" {
		changed, err := d.changedSince(ctx, t, base)
		if err != nil {
			return nil, err
		}
		github.MarkChanged(&tree, changed, t.Subpath)
	}
	nodes := tree.Nodes
	if nodes == nil {
		nodes = []core.TreeNode{}
	}
	return &body[Tree]{Body: Tree{Nodes: nodes, SelectedLines: tree.SelectedLines, SelectedCode: tree.SelectedCode, SelectedFiles: tree.SelectedFiles, TotalFiles: tree.TotalFiles, MaxLines: d.tier(ctx, u.ID).MaxLines}}, nil
}
