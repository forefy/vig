package api

import (
	"context"
	"strings"

	"github.com/danielgtaylor/huma/v2"

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

// exportReportPDF is the owner's report on paper: the same document the
// page shows, exploit paths included when they are shown there.
func (d *Deps) exportReportPDF(ctx context.Context, in *idPath) (*pdfOut, error) {
	r, _, owner, err := d.reportFor(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	return d.pdfFor(ctx, r, owner)
}

// getPublishedReportPDF is the public page on paper: never exploit paths.
func (d *Deps) getPublishedReportPDF(ctx context.Context, in *slugPath) (*pdfOut, error) {
	if err := d.ready(); err != nil {
		return nil, err
	}
	r, err := d.Store.GetReportBySlug(ctx, in.Slug)
	if err != nil {
		return nil, fail(err)
	}
	return d.pdfFor(ctx, r, false)
}

func (d *Deps) pdfFor(ctx context.Context, r store.Report, owner bool) (*pdfOut, error) {
	rep := toReport(r, owner)
	out, err := pdf.Render(pdf.Input{
		Doc: rep.Document, Adapter: r.Adapter, Revision: r.Revision, CreatedAt: r.CreatedAt, PublishedAt: r.PublishedAt,
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
