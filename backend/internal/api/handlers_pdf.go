package api

import (
	"context"
	"strconv"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/forefy/vig/backend/internal/core"
	"github.com/forefy/vig/backend/internal/pdf"
	"github.com/forefy/vig/backend/internal/store"
)

// pdfOut is a rendered report: bytes, named after the repository.
type pdfOut struct {
	ContentType        string `header:"Content-Type"`
	ContentDisposition string `header:"Content-Disposition"`
	Body               []byte
}

// pdfOp describes the response as a PDF rather than the opaque bytes huma
// assumes for a byte body.
func pdfOp(o huma.Operation) huma.Operation {
	o.Responses = map[string]*huma.Response{"200": {
		Description: "The report as a PDF",
		Content:     map[string]*huma.MediaType{"application/pdf": {Schema: &huma.Schema{Type: "string", Format: "binary"}}},
	}}
	return o
}

// A selection of findings to export, as the page's view of the report:
// the ordinals to keep, or every finding when absent.
type selection struct {
	Findings  string `query:"findings" doc:"Comma-separated ordinals to keep; absent keeps every finding" example:"1,3,4"`
	Without   string `query:"without" doc:"Comma-separated parts to leave out: methods, scope, ruled_out, history, code, attachments, references, known" example:"attachments,history"`
	Renumber  bool   `query:"renumber" doc:"Number the exported findings 1 upward in reading order (by severity) instead of keeping the report's ordinals"`
	FullScope bool   `query:"full_scope" doc:"Print every pinned component on the cover; otherwise the three with the most findings and a count of the others"`
}

func (s selection) ordinals() ([]int, bool, error) {
	if strings.TrimSpace(s.Findings) == "" {
		return nil, false, nil
	}
	var out []int
	for _, part := range strings.Split(s.Findings, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil || n < 1 {
			return nil, false, huma.Error422UnprocessableEntity("findings: ordinals are whole numbers from 1, separated by commas")
		}
		out = append(out, n)
	}
	return out, true, nil
}

// cut applies the selection to a report for the viewer; of is the count
// before the cut when one was made.
func (s selection) cut(r store.Report, owner bool) (Report, int, error) {
	rep := toReport(r, owner)
	if err := rep.Document.Without(strings.Split(s.Without, ",")); err != nil {
		return rep, 0, huma.Error422UnprocessableEntity(err.Error())
	}
	keep, some, err := s.ordinals()
	if err != nil {
		return rep, 0, err
	}
	of := 0
	if some {
		of = len(rep.Document.Findings)
		rep.Document.Keep(keep)
	}
	if s.Renumber {
		rep.Document.Renumber()
	}
	return rep, of, nil
}

type exportPath struct {
	ID        string `path:"id"`
	Findings  string `query:"findings" doc:"Comma-separated ordinals to keep; absent keeps every finding" example:"1,3,4"`
	Without   string `query:"without" doc:"Comma-separated parts to leave out: methods, scope, ruled_out, history, code, attachments, references, known" example:"attachments,history"`
	Renumber  bool   `query:"renumber" doc:"Number the exported findings 1 upward in reading order (by severity) instead of keeping the report's ordinals"`
	FullScope bool   `query:"full_scope" doc:"Print every pinned component on the cover; otherwise the three with the most findings and a count of the others"`
}

func (in exportPath) selection() selection {
	return selection{Findings: in.Findings, Without: in.Without, Renumber: in.Renumber, FullScope: in.FullScope}
}

type exportSlugPath struct {
	Slug      string `path:"slug"`
	Findings  string `query:"findings" doc:"Comma-separated ordinals to keep; absent keeps every finding" example:"1,3,4"`
	Without   string `query:"without" doc:"Comma-separated parts to leave out: methods, scope, ruled_out, history, code, attachments, references, known" example:"attachments,history"`
	Renumber  bool   `query:"renumber" doc:"Number the exported findings 1 upward in reading order (by severity) instead of keeping the report's ordinals"`
	FullScope bool   `query:"full_scope" doc:"Print every pinned component on the cover; otherwise the three with the most findings and a count of the others"`
}

func (in exportSlugPath) selection() selection {
	return selection{Findings: in.Findings, Without: in.Without, Renumber: in.Renumber, FullScope: in.FullScope}
}

// exportReportPDF is the owner's report on paper: the same document the
// page shows, exploit paths included when they are shown there.
func (d *Deps) exportReportPDF(ctx context.Context, in *exportPath) (*pdfOut, error) {
	r, _, owner, err := d.reportFor(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	return d.pdfFor(ctx, r, owner, in.selection())
}

// getPublishedReportPDF is the public page on paper: never exploit paths.
func (d *Deps) getPublishedReportPDF(ctx context.Context, in *exportSlugPath) (*pdfOut, error) {
	if err := d.ready(); err != nil {
		return nil, err
	}
	r, err := d.Store.GetReportBySlug(ctx, in.Slug)
	if err != nil {
		return nil, fail(err)
	}
	return d.pdfFor(ctx, r, false, in.selection())
}

// getPublishedReportJSON is the public page's findings document.
func (d *Deps) getPublishedReportJSON(ctx context.Context, in *exportSlugPath) (*body[core.FindingsDocument], error) {
	if err := d.ready(); err != nil {
		return nil, err
	}
	r, err := d.Store.GetReportBySlug(ctx, in.Slug)
	if err != nil {
		return nil, fail(err)
	}
	rep, _, err := in.selection().cut(r, false)
	if err != nil {
		return nil, err
	}
	return &body[core.FindingsDocument]{Body: rep.Document}, nil
}

func (d *Deps) pdfFor(ctx context.Context, r store.Report, owner bool, sel selection) (*pdfOut, error) {
	rep, of, err := sel.cut(r, owner)
	if err != nil {
		return nil, err
	}
	// The cover ranks components over the whole report, as the page does.
	ranked := toReport(r, owner).Document.Findings
	out, err := pdf.Render(pdf.Input{
		Doc: rep.Document, Adapter: r.Adapter, Revision: r.Revision, CreatedAt: r.CreatedAt, PublishedAt: r.PublishedAt, Of: of, FullScope: sel.FullScope, Ranked: ranked,
		// Attachments are report evidence, read by id and only for this audit.
		Visual: func(id string) (string, []byte, bool) {
			v, err := d.Store.GetVisual(ctx, id)
			if err != nil || v.AuditID != r.AuditID {
				return "", nil, false
			}
			return v.Mime, v.Bytes, true
		},
	})
	if err != nil {
		return nil, huma.Error500InternalServerError("render failed")
	}
	name := strings.ReplaceAll(r.Pin.Repo, "/", "-") + "-findings.pdf"
	return &pdfOut{ContentType: "application/pdf", ContentDisposition: `attachment; filename="` + name + `"`, Body: out}, nil
}
