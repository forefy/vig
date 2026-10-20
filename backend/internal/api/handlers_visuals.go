package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"sort"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"

	"github.com/forefy/vig/backend/internal/core"
	"github.com/forefy/vig/backend/internal/store"
)

// Size caps by kind. A picture is a screenshot; a recording is a screen
// capture of the PoC running; a file is a script, a dump or a log.
const (
	visualMaxImage = 8 << 20
	visualMaxVideo = 64 << 20
	visualMaxFile  = 16 << 20
	// The JSON body carries the bytes base64, a third larger than the
	// largest attachment, with headroom for the rest of the document.
	visualMaxBody = 96 << 20
)

// ---------- upload ----------

type visualBody struct {
	Ordinal    int    `json:"ordinal" minimum:"1" doc:"The finding ordinal this attachment belongs to"`
	Caption    string `json:"caption" minLength:"3" maxLength:"140" doc:"What the attachment shows"`
	Mime       string `json:"mime" minLength:"3" maxLength:"100" doc:"image/png, image/jpeg, image/webp, image/gif and video/mp4, video/webm render inline; any other type is stored as a file the reader downloads"`
	Name       string `json:"name,omitempty" maxLength:"120" doc:"File name the reader saves a download as; required when the type is not an image or a video"`
	DataBase64 string `json:"data_base64" minLength:"32" doc:"The bytes, base64"`
}

type visualUploadIn struct {
	ID   string `path:"id"`
	Body visualBody
}

type visualUploadOut struct {
	Body struct {
		ID     string      `json:"id" doc:"The stored attachment id"`
		SHA256 string      `json:"sha256"`
		Visual core.Visual `json:"visual" doc:"The reference to put in findings[].visuals or findings[].sites[].visuals, as the report expects it"`
	}
}

func (d *Deps) registerVisuals(api huma.API) {
	up := func(id, path, summary, tag string, sec []map[string][]string) huma.Operation {
		o := withAuth(op(id, http.MethodPost, path, summary, tag), sec)
		o.MaxBodyBytes = visualMaxBody
		return o
	}
	huma.Register(api, up("adminUploadVisual", "/v1/admin/audits/{id}/visuals",
		"Store a capture, recording or file for a finding of an audit that has a report; reference the returned visual from the revised document", "admin", adminAuth), d.adminUploadVisual)
	huma.Register(api, up("uploadJobVisual", "/v1/op/jobs/{id}/visuals",
		"Store a capture, recording or file for a finding before the report is posted; reference the returned visual from the findings document", "operator", operatorAuth), d.uploadJobVisual)
}

// adminUploadVisual stores bytes for a report revision.
func (d *Deps) adminUploadVisual(ctx context.Context, in *visualUploadIn) (*visualUploadOut, error) {
	a, err := d.Store.GetAudit(ctx, in.ID)
	if err != nil {
		return nil, fail(err)
	}
	if _, err := d.Store.GetReportByAudit(ctx, a.ID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, huma.Error409Conflict("audit has no report yet")
		}
		return nil, fail(err)
	}
	return d.storeVisual(ctx, a, in.Body)
}

// uploadJobVisual stores bytes for the report the operator is about to post.
func (d *Deps) uploadJobVisual(ctx context.Context, in *visualUploadIn) (*visualUploadOut, error) {
	a, _, err := d.job(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	if a.State.Terminal() {
		return nil, huma.Error409Conflict("audit is " + string(a.State))
	}
	return d.storeVisual(ctx, a, in.Body)
}

// storeVisual decodes, checks the type and size for its kind, and stores.
// The findings document carries only the reference the response returns.
func (d *Deps) storeVisual(ctx context.Context, a store.Audit, b visualBody) (*visualUploadOut, error) {
	v := core.Visual{ID: strings.Repeat("0", 36), Caption: b.Caption, Mime: b.Mime, Name: b.Name}
	if err := v.Normalize(); err != nil {
		return nil, huma.Error422UnprocessableEntity(err.Error())
	}
	raw, err := base64.StdEncoding.DecodeString(b.DataBase64)
	if err != nil {
		return nil, huma.Error400BadRequest("data_base64 is not valid base64")
	}
	limit := visualMaxFile
	switch v.Kind {
	case core.VisualImage:
		limit = visualMaxImage
	case core.VisualVideo:
		limit = visualMaxVideo
	}
	if len(raw) > limit {
		return nil, huma.Error413RequestEntityTooLarge(fmt.Sprintf("%s over %dMB", v.Kind, limit>>20))
	}
	sum := sha256.Sum256(raw)
	if v.Kind != core.VisualFile {
		v.Name = ""
	}
	sv, err := d.Store.SaveVisual(ctx, store.Visual{
		AuditID: a.ID, Ordinal: b.Ordinal, Caption: v.Caption,
		Mime: v.Mime, Name: v.Name, SHA256: hex.EncodeToString(sum[:]), Bytes: raw,
	})
	if err != nil {
		return nil, fail(err)
	}
	v.ID = sv.ID
	v.Size = sv.Size
	out := &visualUploadOut{}
	out.Body.ID = sv.ID
	out.Body.SHA256 = sv.SHA256
	out.Body.Visual = v
	return out, nil
}

// bindVisuals checks that every attachment a document references is stored
// for this audit and rewrites each reference from the stored row, so the
// document cannot claim a type, name or size the bytes do not have. Runs
// before Validate, which then fills the kind.
func (d *Deps) bindVisuals(ctx context.Context, auditID string, doc *core.FindingsDocument) error {
	ids := map[string]bool{}
	for _, f := range doc.Findings {
		for _, v := range f.Visuals {
			ids[v.ID] = true
		}
		for _, s := range f.Sites {
			for _, v := range s.Visuals {
				ids[v.ID] = true
			}
		}
	}
	if len(ids) == 0 {
		return nil
	}
	list := make([]string, 0, len(ids))
	for id := range ids {
		if len(id) != 36 {
			return huma.Error422UnprocessableEntity(fmt.Sprintf("visual id %q is not a stored attachment id", id))
		}
		list = append(list, id)
	}
	metas, err := d.Store.VisualMetas(ctx, auditID, list)
	if err != nil {
		return fail(err)
	}
	var missing []string
	for _, id := range list {
		if _, ok := metas[id]; !ok {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return huma.Error422UnprocessableEntity("attachments not stored for this audit: " + strings.Join(missing, ", "))
	}
	bind := func(v *core.Visual) {
		m := metas[v.ID]
		v.Mime, v.Name, v.Size = m.Mime, m.Name, m.Size
		v.Kind = core.VisualKindOf(m.Mime)
	}
	for i := range doc.Findings {
		for j := range doc.Findings[i].Visuals {
			bind(&doc.Findings[i].Visuals[j])
		}
		for j := range doc.Findings[i].Sites {
			for k := range doc.Findings[i].Sites[j].Visuals {
				bind(&doc.Findings[i].Sites[j].Visuals[k])
			}
		}
	}
	return nil
}

// ---------- serving (raw chi: bytes, no JSON marshaling) ----------

// MountVisuals serves stored attachments. Owner and admin sessions reach
// them by audit id; a published report's page reaches them by its slug. The
// bytes are report evidence: private, never cached by intermediaries.
func (d *Deps) MountVisuals(r chi.Router) {
	r.Get("/v1/audits/{id}/visuals/{vid}", d.serveVisualForAudit)
	r.Get("/v1/reports/{slug}/visuals/{vid}", d.serveVisualForSlug)
}

func (d *Deps) serveVisualForAudit(w http.ResponseWriter, r *http.Request) {
	if d.Store == nil {
		writeProblem(w, r, http.StatusServiceUnavailable, "database not configured")
		return
	}
	p, ok := d.resolveRequest(r)
	if !ok || p.UserID == "" {
		writeProblem(w, r, http.StatusUnauthorized, "sign in required")
		return
	}
	a, err := d.Store.GetAudit(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeProblem(w, r, http.StatusNotFound, "not found")
		return
	}
	if (a.UserID != p.UserID && p.Role != "admin") || a.HiddenAt != nil {
		writeProblem(w, r, http.StatusNotFound, "not found")
		return
	}
	d.serveVisual(w, r, a.ID)
}

func (d *Deps) serveVisualForSlug(w http.ResponseWriter, r *http.Request) {
	if d.Store == nil {
		writeProblem(w, r, http.StatusServiceUnavailable, "database not configured")
		return
	}
	rep, err := d.Store.GetReportBySlug(r.Context(), chi.URLParam(r, "slug"))
	if err != nil {
		writeProblem(w, r, http.StatusNotFound, "not found")
		return
	}
	d.serveVisual(w, r, rep.AuditID)
}

// activeTypes are types a browser would execute or render if handed them
// inline. A file of one of these is served as opaque bytes; a picture or a
// recording is only ever one of the inline types core allows.
var activeTypes = map[string]bool{
	"text/html": true, "application/xhtml+xml": true, "image/svg+xml": true,
	"text/xml": true, "application/xml": true, "text/javascript": true, "application/javascript": true,
}

// serveVisual writes the bytes with the headers their kind needs. Pictures
// and recordings go inline; a recording is served through ServeContent so
// the player can seek with range requests. A file goes as a download under
// the name it was uploaded with.
func (d *Deps) serveVisual(w http.ResponseWriter, r *http.Request, auditID string) {
	v, err := d.Store.GetVisual(r.Context(), chi.URLParam(r, "vid"))
	if err != nil || v.AuditID != auditID || !core.ValidVisualMime(v.Mime) {
		writeProblem(w, r, http.StatusNotFound, "not found")
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	kind := core.VisualKindOf(v.Mime)
	ct := v.Mime
	if kind == core.VisualFile {
		if activeTypes[ct] || strings.HasSuffix(ct, "+xml") {
			ct = "application/octet-stream"
		}
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": v.Name}))
	} else {
		w.Header().Set("Content-Disposition", "inline")
	}
	w.Header().Set("Content-Type", ct)
	http.ServeContent(w, r, "", v.CreatedAt, bytes.NewReader(v.Bytes))
}
