package api

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/forefy/vig/backend/internal/core"
	"github.com/forefy/vig/backend/internal/store"
)

// SurfaceView is a map with attention filled in, its newest evidence, and
// the identity of each audited program.
type SurfaceView struct {
	State    string                 `json:"state" enum:"pending,ready,failed" doc:"pending: still being mapped; failed: gave up after retries"`
	Error    string                 `json:"error,omitempty"`
	Surface  *core.Surface          `json:"surface,omitempty"`
	Evidence []core.SurfaceEvidence `json:"evidence" doc:"Newest first, capped"`
	Programs []SurfaceProgram       `json:"programs,omitempty"`
	Watch    string                 `json:"watch" enum:"off,connecting,backfilling,live,error,ended" doc:"Where the live feed stands; the feed itself is at /surface/live"`
}

// SurfaceProgram is what the audit knows about one program's deployment.
type SurfaceProgram struct {
	ID               string           `json:"id"`
	Match            core.MatchStatus `json:"match" enum:"verified,self_built,mismatch,unverified" doc:"Whether the pinned source builds to the deployed bytecode"`
	VerifiedCommit   string           `json:"verified_commit,omitempty"`
	BytecodeSHA256   string           `json:"bytecode_sha256,omitempty"`
	UpgradeAuthority string           `json:"upgrade_authority,omitempty"`
	LastDeploySlot   uint64           `json:"last_deploy_slot,omitempty"`
	IDLPresent       bool             `json:"idl_present"`
	AnchorVersion    string           `json:"anchor_version,omitempty"`
	OwnershipProven  bool             `json:"ownership_proven"`

	// Read from the program node's facts on the surface map.
	BytecodeChanged bool   `json:"bytecode_changed" doc:"true when the deployed image differs from the one recorded at intake"`
	Upgrades        string `json:"upgrades,omitempty" doc:"Upgrade count, or 100+"`
	FirstDeployAt   string `json:"first_deploy_at,omitempty"`
	LastUpgradeAt   string `json:"last_upgrade_at,omitempty"`
	Immutable       bool   `json:"immutable,omitempty"`
	Loader          string `json:"loader,omitempty"`
}

type opEvidenceIn struct {
	ID   string `path:"id"`
	Body struct {
		Items []core.SurfaceEvidence `json:"items" minItems:"1" maxItems:"500"`
	}
}

type opNodesIn struct {
	ID   string `path:"id"`
	Body struct {
		Nodes []core.SurfaceNode `json:"nodes,omitempty" maxItems:"2000"`
		Edges []core.SurfaceEdge `json:"edges,omitempty" maxItems:"4000"`
	}
}

type addedOut struct {
	Body struct {
		Added int `json:"added"`
	}
}

const (
	evidenceReturned = 300
	evidenceRead     = 5000
	evidenceCap      = 20000
)

func (d *Deps) registerSurface(api huma.API) {
	huma.Register(api, withAuth(op("getSurfaceIndex", http.MethodGet, "/v1/surface", "Every node across the user's maps, with its audit, attention and findings", "audits"), userAuth), d.getSurfaceIndex)
	huma.Register(api, withAuth(op("adminGetSurfaceIndex", http.MethodGet, "/v1/admin/surface", "Every node across every audit's map", "admin"), adminAuth), d.adminGetSurfaceIndex)
	huma.Register(api, withAuth(op("getAuditSurface", http.MethodGet, "/v1/audits/{id}/surface", "Surface map with attention", "audits"), userAuth), d.getAuditSurface)
	huma.Register(api, withAuth(op("adminGetAuditSurface", http.MethodGet, "/v1/admin/audits/{id}/surface", "Any audit's surface map", "admin"), adminAuth), d.adminGetAuditSurface)
	huma.Register(api, withAuth(op("getJobSurface", http.MethodGet, "/v1/op/jobs/{id}/surface", "The job's surface map", "operator"), operatorAuth), d.getJobSurface)
	huma.Register(api, withAuth(op("postJobSurfaceEvidence", http.MethodPost, "/v1/op/jobs/{id}/surface/evidence", "Add observations to surface nodes", "operator"), operatorAuth), d.postJobSurfaceEvidence)
	huma.Register(api, withAuth(op("postJobSurfaceNodes", http.MethodPost, "/v1/op/jobs/{id}/surface/nodes", "Extend the surface with nodes and edges", "operator"), operatorAuth), d.postJobSurfaceNodes)
}

// surfaceView assembles the map: document, evidence, the report's findings,
// then attention derived from all three.
func (d *Deps) surfaceView(ctx context.Context, a store.Audit) (SurfaceView, error) {
	state, msg, err := d.Store.SurfaceStatus(ctx, a.ID)
	if err != nil {
		return SurfaceView{}, err
	}
	view := SurfaceView{State: string(state), Error: msg, Evidence: []core.SurfaceEvidence{}, Watch: "off"}
	if programs, err := d.Store.ListPrograms(ctx, a.TargetID); err == nil {
		for _, p := range programs {
			match := p.VerifiedBuild
			if match == "" {
				match = core.MatchUnverified
			}
			view.Programs = append(view.Programs, SurfaceProgram{ID: p.ProgramID, Match: match, VerifiedCommit: p.VerifiedCommit, BytecodeSHA256: p.BytecodeSHA256,
				UpgradeAuthority: p.UpgradeAuthority, LastDeploySlot: p.LastDeploySlot, IDLPresent: p.IDLPresent, AnchorVersion: p.AnchorVersion, OwnershipProven: p.OwnershipProvenAt != nil})
		}
	}
	if state != store.SurfaceReady {
		return view, nil
	}
	doc, _, evidence, err := d.attendedSurface(ctx, a)
	if err != nil {
		return SurfaceView{}, err
	}
	if wt, err := d.Store.GetWatch(ctx, a.ID); err == nil {
		view.Watch = string(wt.State)
	}
	for i := range view.Programs {
		view.Programs[i].fromNode(doc, "p:"+view.Programs[i].ID)
	}
	if len(evidence) > evidenceReturned {
		evidence = evidence[:evidenceReturned]
	}
	view.Surface = &doc
	view.Evidence = evidence
	return view, nil
}

// attendedSurface reads an audit's map as its page shows it: the stored
// document, enriched with the report's places for a site, with attention,
// counts and findings derived from the evidence and the report.
func (d *Deps) attendedSurface(ctx context.Context, a store.Audit) (core.Surface, []core.Finding, []core.SurfaceEvidence, error) {
	doc, err := d.Store.GetSurface(ctx, a.ID)
	if err != nil {
		return core.Surface{}, nil, nil, err
	}
	evidence, err := d.Store.ListSurfaceEvidence(ctx, a.ID, evidenceRead)
	if err != nil {
		return core.Surface{}, nil, nil, err
	}
	var findings []core.Finding
	if a.ReportID != "" {
		if r, err := d.Store.GetReport(ctx, a.ReportID); err == nil {
			findings = r.Document().Findings
		}
	}
	// A web map stands on the report before any capture: the host of the
	// target and every endpoint a finding names, read on every view so an
	// older report lights its routes too.
	if t, err := d.Store.GetTarget(ctx, a.TargetID); err == nil {
		// The target names the adapter; a document built under an older
		// spelling still reads in that adapter's words.
		doc.Adapter = t.Adapter
		if t.Adapter == "webapp" {
			doc.Enrich(webNodes(t.BaseURL, findings), nil)
		}
	}
	doc.Attend(evidence, findings)
	// What the watch saw since mapping counts as observed, from the
	// rollups rather than the rows.
	if totals, err := d.Store.RollupTotals(ctx, a.ID); err == nil && len(totals) > 0 {
		doc.AddObserved(totals)
	}
	return doc, findings, evidence, nil
}

// surfaceIndex flattens the maps of the given audits into one index. An
// audit whose map is not ready yet is listed with no nodes.
func (d *Deps) surfaceIndex(ctx context.Context, audits []store.Audit) (core.SurfaceIndex, error) {
	out := core.SurfaceIndex{Audits: []core.IndexAudit{}, Nodes: []core.IndexNode{}}
	for _, a := range audits {
		state, _, err := d.Store.SurfaceStatus(ctx, a.ID)
		if err != nil || state != store.SurfaceReady {
			continue
		}
		doc, findings, _, err := d.attendedSurface(ctx, a)
		if err != nil {
			return out, err
		}
		ia := core.IndexAudit{ID: a.ID, TargetID: a.TargetID, Repo: a.Repo, Adapter: doc.Adapter, State: a.State, HeadSHA: a.HeadSHA, Coverage: a.Coverage, BuiltAt: doc.BuiltAt, Map: doc.Coverage, Nodes: len(doc.Nodes)}
		if t, err := d.Store.GetTarget(ctx, a.TargetID); err == nil {
			ia.BaseURL = t.BaseURL
		}
		out.Audits = append(out.Audits, ia)
		out.Nodes = append(out.Nodes, core.IndexNodes(a.ID, doc, findings)...)
	}
	return out, nil
}

func (d *Deps) getSurfaceIndex(ctx context.Context, _ *struct{}) (*body[core.SurfaceIndex], error) {
	u, err := d.user(ctx)
	if err != nil {
		return nil, err
	}
	audits, err := d.Store.ListSurfaceAudits(ctx, u.ID)
	if err != nil {
		return nil, fail(err)
	}
	idx, err := d.surfaceIndex(ctx, audits)
	if err != nil {
		return nil, fail(err)
	}
	return &body[core.SurfaceIndex]{Body: idx}, nil
}

func (d *Deps) adminGetSurfaceIndex(ctx context.Context, _ *struct{}) (*body[core.SurfaceIndex], error) {
	audits, err := d.Store.ListSurfaceAudits(ctx, "")
	if err != nil {
		return nil, fail(err)
	}
	idx, err := d.surfaceIndex(ctx, audits)
	if err != nil {
		return nil, fail(err)
	}
	return &body[core.SurfaceIndex]{Body: idx}, nil
}

// fromNode reads the deployment facts the map carries for this program and
// flags a bytecode drift when both the intake hash and the live one are
// known and differ.
func (p *SurfaceProgram) fromNode(doc core.Surface, nodeID string) {
	for _, n := range doc.Nodes {
		if n.ID != nodeID {
			continue
		}
		now := n.Facts["bytecode_now"]
		p.BytecodeChanged = now != "" && p.BytecodeSHA256 != "" && now != p.BytecodeSHA256
		p.Upgrades = n.Facts["upgrades"]
		p.FirstDeployAt = n.Facts["first_deploy"]
		p.LastUpgradeAt = n.Facts["last_upgrade"]
		p.Immutable = n.Facts["immutable"] == "true"
		p.Loader = n.Facts["loader"]
		return
	}
}

func (d *Deps) getAuditSurface(ctx context.Context, in *idPath) (*body[SurfaceView], error) {
	a, _, err := d.ownedAudit(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	v, err := d.surfaceView(ctx, a)
	if err != nil {
		return nil, fail(err)
	}
	return &body[SurfaceView]{Body: v}, nil
}

func (d *Deps) adminGetAuditSurface(ctx context.Context, in *idPath) (*body[SurfaceView], error) {
	a, err := d.Store.GetAudit(ctx, in.ID)
	if err != nil {
		return nil, fail(err)
	}
	v, err := d.surfaceView(ctx, a)
	if err != nil {
		return nil, fail(err)
	}
	return &body[SurfaceView]{Body: v}, nil
}

func (d *Deps) getJobSurface(ctx context.Context, in *idPath) (*body[SurfaceView], error) {
	a, _, err := d.job(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	v, err := d.surfaceView(ctx, a)
	if err != nil {
		return nil, fail(err)
	}
	return &body[SurfaceView]{Body: v}, nil
}

// postJobSurfaceEvidence records what the operator observed or reviewed.
// Every item must name a node the map already has.
func (d *Deps) postJobSurfaceEvidence(ctx context.Context, in *opEvidenceIn) (*addedOut, error) {
	a, tokenID, err := d.job(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	if a.State.Terminal() {
		return nil, huma.Error409Conflict("audit is " + string(a.State))
	}
	// A review may name a route the report put on the map; make it a
	// node of record first.
	d.extendWebSurface(ctx, a, nil)
	doc, err := d.Store.GetSurface(ctx, a.ID)
	if err != nil {
		return nil, huma.Error409Conflict("surface not mapped yet; post nodes first")
	}
	if n, err := d.Store.CountSurfaceEvidence(ctx, a.ID); err != nil {
		return nil, fail(err)
	} else if n+len(in.Body.Items) > evidenceCap {
		return nil, huma.Error422UnprocessableEntity("evidence cap reached")
	}
	items := make([]core.SurfaceEvidence, 0, len(in.Body.Items))
	for i, ev := range in.Body.Items {
		if !doc.Has(ev.NodeID) {
			return nil, huma.Error422UnprocessableEntity("items[" + itoa(i) + "]: unknown node " + ev.NodeID)
		}
		ev.Actor = "operator"
		ev.ID = tokenID
		items = append(items, ev)
	}
	n, err := d.Store.AddSurfaceEvidence(ctx, a.ID, items)
	if err != nil {
		return nil, fail(err)
	}
	if n > 0 {
		if err := d.Store.AddEvent(ctx, d.Store.Pool, store.AuditEvent{AuditID: a.ID, Kind: "surface_enriched", Actor: "operator", ActorID: tokenID, State: a.State, Payload: map[string]any{"evidence": n}}); err != nil {
			return nil, fail(err)
		}
	}
	out := &addedOut{}
	out.Body.Added = n
	return out, nil
}

// postJobSurfaceNodes grows the map with what the operator found: routes
// from a crawl, instructions the IDL never declared. Existing nodes stay.
func (d *Deps) postJobSurfaceNodes(ctx context.Context, in *opNodesIn) (*addedOut, error) {
	a, tokenID, err := d.job(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	if a.State.Terminal() {
		return nil, huma.Error409Conflict("audit is " + string(a.State))
	}
	doc, err := d.Store.GetSurface(ctx, a.ID)
	if err != nil {
		t, terr := d.Store.GetTarget(ctx, a.TargetID)
		if terr != nil {
			return nil, fail(terr)
		}
		doc = core.Surface{Adapter: t.Adapter, Nodes: []core.SurfaceNode{}, BuiltAt: d.now()}
	}
	for i := range in.Body.Nodes {
		in.Body.Nodes[i].Source = "operator"
		in.Body.Nodes[i].Attention = ""
		in.Body.Nodes[i].Observed = 0
		in.Body.Nodes[i].LastSeen = nil
		in.Body.Nodes[i].Findings = nil
		in.Body.Nodes[i].Stats = nil
	}
	added := doc.Merge(in.Body.Nodes, in.Body.Edges)
	if len(doc.Nodes) > 5000 {
		return nil, huma.Error422UnprocessableEntity("surface cap reached")
	}
	if err := d.Store.SaveSurface(ctx, a.ID, doc); err != nil {
		return nil, fail(err)
	}
	if added > 0 {
		if err := d.Store.AddEvent(ctx, d.Store.Pool, store.AuditEvent{AuditID: a.ID, Kind: "surface_extended", Actor: "operator", ActorID: tokenID, State: a.State, Payload: map[string]any{"added": added}}); err != nil {
			return nil, fail(err)
		}
	}
	out := &addedOut{}
	out.Body.Added = added
	return out, nil
}

// webNodes is what a web target exposes before any capture: its host and
// the endpoint of every finding, as static nodes.
func webNodes(baseURL string, findings []core.Finding) []core.SurfaceNode {
	var nodes []core.SurfaceNode
	if h := core.WebHostNode(baseURL); h != nil {
		nodes = append(nodes, *h)
	}
	return append(nodes, core.WebNodes(findings)...)
}

// extendWebSurface writes the report's places onto a web audit's map, so
// a route a finding names can take reviews and requests like any node.
// findings nil reads the audit's current report. Other adapters, and a
// map that already has every place, are left alone; a failure is logged,
// never surfaced, since the view derives the same nodes on read.
func (d *Deps) extendWebSurface(ctx context.Context, a store.Audit, findings []core.Finding) {
	t, err := d.Store.GetTarget(ctx, a.TargetID)
	if err != nil || t.Adapter != "webapp" {
		return
	}
	if findings == nil && a.ReportID != "" {
		if r, err := d.Store.GetReport(ctx, a.ReportID); err == nil {
			findings = r.Document().Findings
		}
	}
	doc, err := d.Store.GetSurface(ctx, a.ID)
	if err != nil {
		doc = core.Surface{Adapter: t.Adapter, Nodes: []core.SurfaceNode{}, BuiltAt: d.now()}
	}
	if doc.Enrich(webNodes(t.BaseURL, findings), nil) == 0 {
		return
	}
	if err := d.Store.SaveSurface(ctx, a.ID, doc); err != nil {
		slog.Warn("surface: web nodes", "audit", a.ID, "err", err)
	}
}
