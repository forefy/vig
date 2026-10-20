package api

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/forefy/vig/backend/internal/auth"
	"github.com/forefy/vig/backend/internal/core"
	"github.com/forefy/vig/backend/internal/github"
	"github.com/forefy/vig/backend/internal/store"
)

type createAuditIn struct {
	Body struct {
		TargetID   string   `json:"target_id"`
		Coverage   string   `json:"coverage" enum:"full,diff"`
		BaseSHA    string   `json:"base_sha,omitempty"`
		ProgramIDs []string `json:"program_ids" minItems:"1"`
		Files      []string `json:"files" minItems:"1"`
	}
}

type findingStatusIn struct {
	ID   string `path:"id"`
	FID  string `path:"fid"`
	Body struct {
		Status core.FindingStatus `json:"status" enum:"open,fixed,acknowledged,disputed"`
	}
}

type findingSeverityIn struct {
	ID   string `path:"id"`
	FID  string `path:"fid"`
	Body struct {
		Severity core.Severity `json:"severity" enum:"critical,high,medium,low,info"`
		Reason   string        `json:"reason" minLength:"3" maxLength:"140" doc:"Why, in a line; kept on the finding's history"`
	}
}

type publishOut struct {
	Body struct {
		Slug string `json:"slug"`
		URL  string `json:"url"`
	}
}

func (d *Deps) registerAudits(api huma.API) {
	huma.Register(api, withAuth(op("createAudit", http.MethodPost, "/v1/audits", "Queue an audit, consuming a credit or today's slot", "audits"), userAuth), d.createAudit)
	huma.Register(api, withAuth(op("listAudits", http.MethodGet, "/v1/audits", "My audits", "audits"), userAuth), d.listAudits)
	huma.Register(api, withAuth(op("getAudit", http.MethodGet, "/v1/audits/{id}", "Audit with timeline", "audits"), userAuth), d.getAudit)
	huma.Register(api, withAuth(op("getReport", http.MethodGet, "/v1/audits/{id}/report", "Report", "audits"), userAuth), d.getReport)
	huma.Register(api, withAuth(op("exportReport", http.MethodGet, "/v1/audits/{id}/report.json", "Findings JSON export", "audits"), userAuth), d.exportReport)
	huma.Register(api, withAuth(op("setFindingStatus", http.MethodPatch, "/v1/audits/{id}/report/findings/{fid}", "Set finding status", "audits"), userAuth), d.setFindingStatus)
	huma.Register(api, withAuth(op("setFindingSeverity", http.MethodPatch, "/v1/audits/{id}/report/findings/{fid}/severity", "Re-rate a finding up or down with a reason; the change joins its history and the summary follows", "audits"), userAuth), d.setFindingSeverity)
	huma.Register(api, withAuth(op("publishReport", http.MethodPost, "/v1/audits/{id}/report/publish", "Publish report page", "audits"), userAuth), d.publishReport)
	huma.Register(api, withAuth(op("unpublishReport", http.MethodDelete, "/v1/audits/{id}/report/publish", "Unpublish", "audits"), userAuth), d.unpublishReport)
}

// ownedAudit loads an audit the caller may see: only its owner.
func (d *Deps) ownedAudit(ctx context.Context, id string) (store.Audit, store.User, error) {
	u, err := d.user(ctx)
	if err != nil {
		return store.Audit{}, u, err
	}
	a, err := d.Store.GetAudit(ctx, id)
	if err != nil {
		return a, u, fail(err)
	}
	if a.UserID != u.ID || a.HiddenAt != nil {
		return a, u, huma.Error404NotFound("not found")
	}
	return a, u, nil
}

// isOwner reports whether every program of the audit has a proven upgrade
// authority on its target. Only then are exploit paths shown.
func (d *Deps) isOwner(ctx context.Context, a store.Audit) (bool, error) {
	programs, err := d.Store.ListPrograms(ctx, a.TargetID)
	if err != nil {
		return false, err
	}
	proven := map[string]bool{}
	for _, p := range programs {
		proven[p.ProgramID] = p.OwnershipProvenAt != nil
	}
	if len(a.ProgramIDs) == 0 {
		return false, nil
	}
	for _, id := range a.ProgramIDs {
		if !proven[id] {
			return false, nil
		}
	}
	return true, nil
}

func (d *Deps) createAudit(ctx context.Context, in *createAuditIn) (*body[Audit], error) {
	t, u, err := d.ownedTarget(ctx, in.Body.TargetID)
	if err != nil {
		return nil, err
	}
	base := ""
	if in.Body.Coverage == "diff" {
		base, err = d.resolveBase(ctx, t, in.Body.BaseSHA)
		if err != nil {
			return nil, err
		}
	}
	programs, err := d.Store.ListPrograms(ctx, t.ID)
	if err != nil {
		return nil, fail(err)
	}
	onTarget := map[string]bool{}
	for _, p := range programs {
		onTarget[p.ProgramID] = true
	}
	ids := uniqueSorted(in.Body.ProgramIDs)
	for _, id := range ids {
		if !onTarget[id] {
			return nil, huma.Error400BadRequest("program " + id + " is not on the target")
		}
	}
	tier := d.tier(ctx, u.ID)
	if len(ids) > tier.MaxPrograms {
		return nil, huma.Error409Conflict("your tier allows " + itoa(tier.MaxPrograms) + " program(s) per audit")
	}
	root, err := d.checkout(ctx, t)
	if err != nil {
		return nil, err
	}
	tree, err := github.BuildTree(root, t.Subpath)
	if err != nil {
		return nil, fail(err)
	}
	files := uniqueSorted(in.Body.Files)
	// The cap counts code lines: blanks and comments are not audited.
	lines := 0
	for _, f := range files {
		n, ok := tree.Code[f]
		if !ok {
			return nil, huma.Error400BadRequest("file not in the repository: " + f)
		}
		lines += n
	}
	if lines > tier.MaxLines {
		return nil, huma.Error409Conflict("scope of " + itoa(lines) + " code lines exceeds the " + itoa(tier.MaxLines) + " line cap of your tier")
	}
	a, _, err := d.Store.QueueAudit(ctx, store.Audit{UserID: u.ID, TargetID: t.ID, Coverage: in.Body.Coverage, BaseSHA: base, HeadSHA: t.CommitSHA, ProgramIDs: ids, Scope: store.Scope{Files: files, Lines: lines}})
	if err != nil {
		return nil, fail(err)
	}
	out, err := d.auditWithEvents(ctx, a)
	if err != nil {
		return nil, fail(err)
	}
	return &body[Audit]{Body: out}, nil
}

func (d *Deps) listAudits(ctx context.Context, _ *struct{}) (*body[list[Audit]], error) {
	u, err := d.user(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := d.Store.ListAudits(ctx, u.ID)
	if err != nil {
		return nil, fail(err)
	}
	out := make([]Audit, 0, len(rows))
	for _, a := range rows {
		out = append(out, toAudit(a))
	}
	return items(out), nil
}

func (d *Deps) getAudit(ctx context.Context, in *idPath) (*body[Audit], error) {
	a, _, err := d.ownedAudit(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	out, err := d.auditWithEvents(ctx, a)
	if err != nil {
		return nil, fail(err)
	}
	return &body[Audit]{Body: out}, nil
}

func (d *Deps) reportFor(ctx context.Context, id string) (store.Report, store.Audit, bool, error) {
	a, _, err := d.ownedAudit(ctx, id)
	if err != nil {
		return store.Report{}, a, false, err
	}
	r, err := d.Store.GetReportByAudit(ctx, a.ID)
	if err != nil {
		return r, a, false, fail(err)
	}
	owner, err := d.isOwner(ctx, a)
	if err != nil {
		return r, a, false, fail(err)
	}
	return r, a, owner, nil
}

func (d *Deps) getReport(ctx context.Context, in *idPath) (*body[Report], error) {
	r, _, owner, err := d.reportFor(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	return &body[Report]{Body: toReport(r, owner)}, nil
}

func (d *Deps) exportReport(ctx context.Context, in *idPath) (*body[core.FindingsDocument], error) {
	r, _, owner, err := d.reportFor(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	return &body[core.FindingsDocument]{Body: toReport(r, owner).Document}, nil
}

func (d *Deps) setFindingStatus(ctx context.Context, in *findingStatusIn) (*body[core.Finding], error) {
	r, _, owner, err := d.reportFor(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	f, err := d.Store.SetFindingStatus(ctx, r.ID, in.FID, in.Body.Status)
	if err != nil {
		return nil, fail(err)
	}
	out := f.Finding.ForViewer(owner)
	out.ID = f.ID
	out.CarriedFromID = f.CarriedFromID
	return &body[core.Finding]{Body: out}, nil
}

func (d *Deps) setFindingSeverity(ctx context.Context, in *findingSeverityIn) (*body[core.Finding], error) {
	r, _, owner, err := d.reportFor(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	f, err := d.Store.SetFindingSeverity(ctx, r.ID, in.FID, in.Body.Severity, strings.TrimSpace(in.Body.Reason), "owner", principal(ctx).UserID)
	if err != nil {
		if errors.Is(err, store.ErrConflict) {
			return nil, huma.Error409Conflict("finding is already " + string(in.Body.Severity))
		}
		return nil, fail(err)
	}
	out := f.Finding.ForViewer(owner)
	out.ID = f.ID
	out.CarriedFromID = f.CarriedFromID
	return &body[core.Finding]{Body: out}, nil
}

func (d *Deps) publishReport(ctx context.Context, in *idPath) (*publishOut, error) {
	r, _, owner, err := d.reportFor(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	if !owner {
		return nil, huma.Error403Forbidden("publishing requires proven ownership of every program in the report")
	}
	// A report keeps its address across an unpublish, so a link that was
	// shared before still resolves when it goes back up. Only a report that
	// has never been published gets a new slug.
	slug := r.PublishedSlug
	if slug == "" {
		slug = auth.RandomToken(6)
	}
	if r.PublishedAt == nil {
		if err := d.Store.Publish(ctx, r.ID, slug); err != nil {
			return nil, fail(err)
		}
	}
	out := &publishOut{}
	out.Body.Slug = slug
	out.Body.URL = d.Config.FrontendURL + "/r/" + slug
	return out, nil
}

func (d *Deps) unpublishReport(ctx context.Context, in *idPath) (*struct{}, error) {
	r, _, _, err := d.reportFor(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	if err := d.Store.Unpublish(ctx, r.ID); err != nil {
		return nil, fail(err)
	}
	return nil, nil
}

func uniqueSorted(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func itoa(n int) string { return strconv.Itoa(n) }
