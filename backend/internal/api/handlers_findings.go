package api

import (
	"context"
	"fmt"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/forefy/vig/backend/internal/core"
	"github.com/forefy/vig/backend/internal/github"
)

// ReportCheck is what a dry run of a findings document returns: whether it
// would be accepted, what would refuse it, and what it leaves out.
type ReportCheck struct {
	Valid  bool     `json:"valid"`
	Errors []string `json:"errors" doc:"What validation refuses; empty when valid"`
	Advice []string `json:"advice" doc:"Recommended fields and shapes the document leaves out; never blocks"`
}

type checkIn struct {
	ID   string `path:"id"`
	Body core.FindingsDocument
}

func (d *Deps) registerFindings(api huma.API) {
	huma.Register(api, op("findingsShape", http.MethodGet, "/v1/findings/shape",
		"What a findings document needs: required, recommended and optional fields, the rules validation applies, and a filled example", "public"), d.findingsShape)
	huma.Register(api, withAuth(op("checkJobReport", http.MethodPost, "/v1/op/jobs/{id}/report/check",
		"Dry run of a findings document against this job: validation errors and advice, nothing stored", "operator"), operatorAuth), d.checkJobReport)
	huma.Register(api, withAuth(op("adminCheckReport", http.MethodPost, "/v1/admin/audits/{id}/report/check",
		"Dry run of a revision against this audit: validation errors and advice, nothing stored", "admin"), adminAuth), d.adminCheckReport)
}

func (d *Deps) findingsShape(_ context.Context, _ *struct{}) (*body[core.Shape], error) {
	return &body[core.Shape]{Body: core.DescribeShape(core.Pin{Repo: "owner/name", Commit: "<full commit sha>"})}, nil
}

func (d *Deps) checkJobReport(ctx context.Context, in *checkIn) (*body[ReportCheck], error) {
	a, _, err := d.job(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	return d.checkReport(ctx, a.ID, a.Repo, a.HeadSHA, in.Body)
}

func (d *Deps) adminCheckReport(ctx context.Context, in *checkIn) (*body[ReportCheck], error) {
	a, err := d.Store.GetAudit(ctx, in.ID)
	if err != nil {
		return nil, fail(err)
	}
	return d.checkReport(ctx, a.ID, a.Repo, a.HeadSHA, in.Body)
}

// checkReport runs what a submission runs, in order, and reports instead
// of refusing: visuals bound to this audit, the document validated, the
// pin matched, then advice on what is missing.
func (d *Deps) checkReport(ctx context.Context, auditID, repo, head string, doc core.FindingsDocument) (*body[ReportCheck], error) {
	out := ReportCheck{Errors: []string{}, Advice: []string{}}
	if doc.Report.AuditID == "" {
		doc.Report.AuditID = auditID
	}
	if doc.Report.AuditID != auditID {
		out.Errors = append(out.Errors, "report.audit_id does not match the audit")
	}
	if err := d.bindVisuals(ctx, auditID, &doc); err != nil {
		if se, ok := err.(huma.StatusError); ok {
			out.Errors = append(out.Errors, se.Error())
		} else {
			return nil, err
		}
	}
	if err := doc.Validate(); err != nil {
		out.Errors = append(out.Errors, err.Error())
	}
	out.Errors = append(out.Errors, doc.Unexhausted(false)...)
	if doc.Report.Pin.Repo != repo || doc.Report.Pin.Commit != head {
		out.Errors = append(out.Errors, "report.pin must match the audited repo and commit")
	}
	// A fence that names a place the checkout does not have is a typo the
	// page would render as a bare block.
	if d.GitHub != nil {
		if root, ok := d.GitHub.Cached(repo, head); ok {
			for _, f := range doc.Findings {
				for _, loc := range core.FixLocations(f.Fix) {
					if _, _, ok := github.Segment(root, loc.File, loc.LineStart, loc.LineEnd); !ok {
						out.Errors = append(out.Errors, "finding "+itoa(f.Ordinal)+": fix fence names "+loc.File+":"+itoa(loc.LineStart)+", which the checkout does not have")
					}
				}
			}
		}
	}
	out.Valid = len(out.Errors) == 0
	out.Advice = doc.Advise()
	// On a revision, a finding that matches nothing on the current report
	// by id or by title and file starts over: its statuses, re-ratings
	// and archive reasons stay behind. Say so before the post.
	if cur, err := d.Store.GetReportByAudit(ctx, auditID); err == nil {
		byID := map[string]bool{}
		byKey := map[string]bool{}
		for _, f := range cur.Findings {
			byID[f.ID] = true
			byKey[f.Title+"\x00"+f.Location.File] = true
		}
		for _, f := range doc.Findings {
			id := f.CarriedFromID
			if id == "" {
				id = f.ID
			}
			if byID[id] || byKey[f.Title+"\x00"+f.Location.File] {
				continue
			}
			out.Advice = append(out.Advice, fmt.Sprintf("finding %d: matches nothing on the current report by id or by title and file, so it would start with no history; carry its id (the id field from GET report.json) if it continues an existing finding", f.Ordinal))
		}
	}
	return &body[ReportCheck]{Body: out}, nil
}
