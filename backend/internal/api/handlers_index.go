package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"

	"github.com/forefy/vig/backend/internal/core"
	"github.com/forefy/vig/backend/internal/store"
)

// An audit's index: Parquet tables an operator built off the job with zz
// and pushed part by part under the job's token, then sealed with a
// manifest. The files live under DATA_DIR/index/<audit_id>/<table>/<file>,
// staged under DATA_DIR/index/<audit_id>/.staging while an upload is in
// flight, and swapped in whole on seal. Atlas reads them in the browser
// with range requests; the catalogue says where each part is.

const (
	indexPartMax  = 64 << 20 // one part
	indexTotalMax = 2 << 30  // one audit's index
	indexMime     = "application/vnd.apache.parquet"
)

// IndexBuild is an audit's index as the owner reads it: the audit it
// belongs to and every part with the URL to fetch it from.
type IndexBuild struct {
	AuditID     string            `json:"audit_id"`
	TargetID    string            `json:"target_id"`
	Repo        string            `json:"repo"`
	Adapter     string            `json:"adapter" enum:"solana,ethereum,webapp,repo"`
	HeadSHA     string            `json:"head_sha"`
	State       core.AuditState   `json:"state"`
	Tool        string            `json:"tool"`
	ToolVersion string            `json:"tool_version,omitempty"`
	Bytes       int64             `json:"bytes"`
	BuiltAt     time.Time         `json:"built_at"`
	Tables      []IndexBuildTable `json:"tables"`
}

// IndexBuildTable is one table with its parts' URLs.
type IndexBuildTable struct {
	Name  string           `json:"name"`
	Rows  int64            `json:"rows,omitempty"`
	Bytes int64            `json:"bytes"`
	Parts []IndexBuildPart `json:"parts"`
}

// IndexBuildPart is one Parquet file and where to GET it. The URL answers
// range requests, so a reader can take a footer or a row group alone.
type IndexBuildPart struct {
	File   string `json:"file"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
	URL    string `json:"url" doc:"Path on this API; sends the Parquet bytes and honours Range"`
}

// AtlasIndex is every index across the user's audits, every user's for
// an admin.
type AtlasIndex struct {
	Audits []IndexBuild `json:"audits"`
}

type opIndexPartIn struct {
	ID   string `path:"id"`
	Body struct {
		Table      string `json:"table" doc:"Table the part belongs to" example:"calls"`
		File       string `json:"file" doc:"Part file name" example:"part-1761900000-ab12cd34.parquet"`
		SHA256     string `json:"sha256" doc:"Digest of the bytes; the upload is refused when it does not match"`
		DataBase64 string `json:"data_base64" doc:"The Parquet file, base64"`
	}
}

type opIndexPartOut struct {
	Body struct {
		Table  string `json:"table"`
		File   string `json:"file"`
		Bytes  int64  `json:"bytes"`
		Staged int    `json:"staged" doc:"Parts staged for this job so far"`
	}
}

type opIndexSealIn struct {
	ID   string `path:"id"`
	Body core.IndexManifest
}

func (d *Deps) registerIndex(api huma.API) {
	huma.Register(api, withAuth(op("getAtlasIndex", http.MethodGet, "/v1/atlas/index", "Every index across the user's audits (every user's for an admin), with where each Parquet part is", "audits"), userAuth), d.getAtlasIndex)
	huma.Register(api, withAuth(op("getAuditIndex", http.MethodGet, "/v1/audits/{id}/index", "The audit's index", "audits"), userAuth), d.getAuditIndex)
	huma.Register(api, withAuth(op("putJobIndexPart", http.MethodPut, "/v1/op/jobs/{id}/index/parts", "Stage one Parquet part of the job's index", "operator"), operatorAuth), d.putJobIndexPart)
	huma.Register(api, withAuth(op("sealJobIndex", http.MethodPost, "/v1/op/jobs/{id}/index", "Seal the staged parts as the job's index, replacing any earlier one", "operator"), operatorAuth), d.sealJobIndex)
	huma.Register(api, withAuth(op("dropJobIndexStaging", http.MethodDelete, "/v1/op/jobs/{id}/index/staging", "Discard parts staged and not sealed", "operator"), operatorAuth), d.dropJobIndexStaging)
}

// MountIndex serves the Parquet parts to the owner and admins, with
// ranges, outside huma so http.ServeContent can do the slicing. HEAD is
// what DuckDB asks first: a part that answers it with its length is read
// by Range, footer and row groups, instead of whole.
func (d *Deps) MountIndex(r chi.Router) {
	r.Get("/v1/audits/{id}/index/{table}/{file}", d.serveIndexPart)
	r.Head("/v1/audits/{id}/index/{table}/{file}", d.serveIndexPart)
}

// ---------- paths ----------

func (d *Deps) indexRoot() string { return filepath.Join(d.Config.DataDir, "index") }

func (d *Deps) indexDir(auditID string) string {
	return filepath.Join(d.indexRoot(), auditID)
}

func (d *Deps) indexStaging(auditID string) string {
	return filepath.Join(d.indexDir(auditID), ".staging")
}

// indexPartURL names a part by its audit, table, file and content: the
// query carries the digest, so a rebuilt part is a new URL and the browser
// may keep the old one as long as it likes.
func indexPartURL(auditID, table, file, sha string) string {
	u := "/v1/audits/" + auditID + "/index/" + table + "/" + file
	if len(sha) >= 16 {
		u += "?v=" + sha[:16]
	}
	return u
}

// removeIndexFiles drops everything stored for an audit's index. Called
// when the audit is deleted; the row goes with the audit by cascade.
func (d *Deps) removeIndexFiles(auditID string) {
	if store.ValidID(auditID) {
		_ = os.RemoveAll(d.indexDir(auditID))
	}
}

// ---------- read ----------

func (d *Deps) buildOf(a store.Audit, ix store.AuditIndex) IndexBuild {
	out := IndexBuild{AuditID: a.ID, TargetID: a.TargetID, Repo: a.Repo, Adapter: a.Adapter, HeadSHA: a.HeadSHA, State: a.State,
		Tool: ix.Tool, ToolVersion: ix.ToolVersion, Bytes: ix.Bytes, BuiltAt: ix.BuiltAt, Tables: []IndexBuildTable{}}
	for _, t := range ix.Tables {
		bt := IndexBuildTable{Name: t.Name, Rows: t.Rows, Bytes: t.Bytes, Parts: []IndexBuildPart{}}
		for _, p := range t.Parts {
			bt.Parts = append(bt.Parts, IndexBuildPart{File: p.File, Bytes: p.Bytes, SHA256: p.SHA256, URL: indexPartURL(a.ID, t.Name, p.File, p.SHA256)})
		}
		out.Tables = append(out.Tables, bt)
	}
	return out
}

func (d *Deps) getAtlasIndex(ctx context.Context, _ *struct{}) (*body[AtlasIndex], error) {
	u, err := d.user(ctx)
	if err != nil {
		return nil, err
	}
	// An admin reads every build, as the parts are served to one.
	owner := u.ID
	if u.Role == "admin" {
		owner = ""
	}
	audits, rows, err := d.Store.ListIndexedAudits(ctx, owner)
	if err != nil {
		return nil, fail(err)
	}
	out := AtlasIndex{Audits: []IndexBuild{}}
	for _, a := range audits {
		out.Audits = append(out.Audits, d.buildOf(a, rows[a.ID]))
	}
	return &body[AtlasIndex]{Body: out}, nil
}

func (d *Deps) getAuditIndex(ctx context.Context, in *idPath) (*body[IndexBuild], error) {
	u, err := d.user(ctx)
	if err != nil {
		return nil, err
	}
	a, err := d.Store.GetAudit(ctx, in.ID)
	if err != nil {
		return nil, fail(err)
	}
	if (a.UserID != u.ID && u.Role != "admin") || a.HiddenAt != nil {
		return nil, huma.Error404NotFound("not found")
	}
	ix, err := d.Store.GetIndex(ctx, a.ID)
	if err != nil {
		return nil, fail(err)
	}
	return &body[IndexBuild]{Body: d.buildOf(a, ix)}, nil
}

func (d *Deps) serveIndexPart(w http.ResponseWriter, r *http.Request) {
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
	if err != nil || (a.UserID != p.UserID && p.Role != "admin") || a.HiddenAt != nil {
		writeProblem(w, r, http.StatusNotFound, "not found")
		return
	}
	table, file := chi.URLParam(r, "table"), chi.URLParam(r, "file")
	if !core.ValidIndexTable(table) || !core.ValidIndexFile(file) {
		writeProblem(w, r, http.StatusNotFound, "not found")
		return
	}
	ix, err := d.Store.GetIndex(r.Context(), a.ID)
	if err != nil {
		writeProblem(w, r, http.StatusNotFound, "not found")
		return
	}
	part, ok := indexPart(ix, table, file)
	if !ok {
		writeProblem(w, r, http.StatusNotFound, "not found")
		return
	}
	// The digest in the URL has to be the part's, whole as the catalogue
	// writes it: a stale link from an earlier build is not this part, and
	// must not be kept as it.
	if v := r.URL.Query().Get("v"); v != "" && (len(part.SHA256) < 16 || v != part.SHA256[:16]) {
		writeProblem(w, r, http.StatusNotFound, "not found")
		return
	}
	f, err := os.Open(filepath.Join(d.indexDir(a.ID), table, file))
	if err != nil {
		writeProblem(w, r, http.StatusNotFound, "not found")
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		writeProblem(w, r, http.StatusNotFound, "not found")
		return
	}
	w.Header().Set("Content-Type", indexMime)
	// The URL names the content, so the bytes may be kept; the digest is
	// the tag a browser revalidates with.
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	w.Header().Set("ETag", `"`+part.SHA256+`"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, file, st.ModTime(), f)
}

// ---------- operator: stage, seal, drop ----------

func (d *Deps) putJobIndexPart(ctx context.Context, in *opIndexPartIn) (*opIndexPartOut, error) {
	a, _, err := d.job(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	b := in.Body
	if !core.ValidIndexTable(b.Table) {
		return nil, huma.Error422UnprocessableEntity("table: lowercase letters, digits and underscores")
	}
	if !core.ValidIndexFile(b.File) {
		return nil, huma.Error422UnprocessableEntity("file: a plain .parquet file name")
	}
	if !core.ValidSHA256(b.SHA256) {
		return nil, huma.Error422UnprocessableEntity("sha256: 64 lowercase hex characters")
	}
	raw, err := base64.StdEncoding.DecodeString(b.DataBase64)
	if err != nil {
		return nil, huma.Error400BadRequest("data_base64 is not valid base64")
	}
	if len(raw) == 0 {
		return nil, huma.Error422UnprocessableEntity("empty part")
	}
	if len(raw) > indexPartMax {
		return nil, huma.Error413RequestEntityTooLarge(fmt.Sprintf("part over %dMB", indexPartMax>>20))
	}
	if !isParquet(raw) {
		return nil, huma.Error422UnprocessableEntity("not a Parquet file")
	}
	sum := sha256.Sum256(raw)
	if hex.EncodeToString(sum[:]) != b.SHA256 {
		return nil, huma.Error422UnprocessableEntity("sha256 does not match the bytes")
	}
	staging := d.indexStaging(a.ID)
	have, err := dirBytes(staging)
	if err != nil {
		return nil, fail(err)
	}
	if have+int64(len(raw)) > indexTotalMax {
		return nil, huma.Error413RequestEntityTooLarge(fmt.Sprintf("index over %dGB", indexTotalMax>>30))
	}
	dir := filepath.Join(staging, b.Table)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fail(err)
	}
	if err := writeAtomic(filepath.Join(dir, b.File), raw); err != nil {
		return nil, fail(err)
	}
	n, _ := countFiles(staging)
	out := &opIndexPartOut{}
	out.Body.Table, out.Body.File, out.Body.Bytes, out.Body.Staged = b.Table, b.File, int64(len(raw)), n
	return out, nil
}

func (d *Deps) sealJobIndex(ctx context.Context, in *opIndexSealIn) (*body[IndexBuild], error) {
	a, _, err := d.job(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	m := in.Body
	if err := m.Validate(); err != nil {
		return nil, huma.Error422UnprocessableEntity(err.Error())
	}
	staging := d.indexStaging(a.ID)
	// Every part the manifest names must be staged with the bytes and the
	// digest it claims; anything staged and not named is dropped.
	named := map[string]bool{}
	for _, t := range m.Tables {
		for _, p := range t.Parts {
			rel := filepath.Join(t.Name, p.File)
			named[rel] = true
			st, err := os.Stat(filepath.Join(staging, rel))
			if err != nil {
				return nil, huma.Error422UnprocessableEntity(rel + ": not staged")
			}
			if st.Size() != p.Bytes {
				return nil, huma.Error422UnprocessableEntity(fmt.Sprintf("%s: staged %d bytes, manifest says %d", rel, st.Size(), p.Bytes))
			}
			sum, err := fileSHA256(filepath.Join(staging, rel))
			if err != nil {
				return nil, fail(err)
			}
			if sum != p.SHA256 {
				return nil, huma.Error422UnprocessableEntity(rel + ": staged digest differs from the manifest")
			}
		}
	}
	_ = filepath.WalkDir(staging, func(path string, de fs.DirEntry, err error) error {
		if err != nil || de.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(staging, path)
		if !named[rel] {
			_ = os.Remove(path)
		}
		return nil
	})
	// Swap: the live tables go, the staged ones take their place.
	dir := d.indexDir(a.ID)
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.Name() != ".staging" {
			_ = os.RemoveAll(filepath.Join(dir, e.Name()))
		}
	}
	for _, t := range m.Tables {
		if err := os.Rename(filepath.Join(staging, t.Name), filepath.Join(dir, t.Name)); err != nil {
			return nil, fail(err)
		}
	}
	_ = os.RemoveAll(staging)
	ix, err := d.Store.SaveIndex(ctx, store.AuditIndex{AuditID: a.ID, Tool: m.Tool, ToolVersion: m.ToolVersion, Bytes: m.TotalBytes(), Tables: m.Tables})
	if err != nil {
		return nil, fail(err)
	}
	// The map reads the index: build it again now that there is one.
	if d.Mapper != nil {
		if err := d.Mapper.Remap(ctx, a); err != nil {
			slog.Warn("index: remap", "audit", a.ID, "err", err)
		}
	}
	return &body[IndexBuild]{Body: d.buildOf(a, ix)}, nil
}

func (d *Deps) dropJobIndexStaging(ctx context.Context, in *idPath) (*struct{}, error) {
	a, _, err := d.job(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	if err := os.RemoveAll(d.indexStaging(a.ID)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fail(err)
	}
	return nil, nil
}

// ---------- files ----------

// indexHas reports whether the sealed index names the part.
func indexHas(ix store.AuditIndex, table, file string) bool {
	_, ok := indexPart(ix, table, file)
	return ok
}

// indexPart is the sealed index's record of a part.
func indexPart(ix store.AuditIndex, table, file string) (core.IndexPart, bool) {
	for _, t := range ix.Tables {
		if t.Name != table {
			continue
		}
		for _, p := range t.Parts {
			if p.File == file {
				return p, true
			}
		}
	}
	return core.IndexPart{}, false
}

// isParquet checks the magic at both ends of the file.
func isParquet(b []byte) bool {
	return len(b) >= 12 && bytes.Equal(b[:4], []byte("PAR1")) && bytes.Equal(b[len(b)-4:], []byte("PAR1"))
}

func writeAtomic(path string, b []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func fileSHA256(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

func dirBytes(dir string) (int64, error) {
	var n int64
	err := filepath.WalkDir(dir, func(_ string, de fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if de.IsDir() {
			return nil
		}
		if info, err := de.Info(); err == nil {
			n += info.Size()
		}
		return nil
	})
	if errors.Is(err, fs.ErrNotExist) {
		err = nil
	}
	return n, err
}

func countFiles(dir string) (int, error) {
	n := 0
	err := filepath.WalkDir(dir, func(_ string, de fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !de.IsDir() {
			n++
		}
		return nil
	})
	return n, err
}
