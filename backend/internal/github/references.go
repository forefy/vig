package github

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/forefy/vig/backend/internal/core"
)

// FileAt reads one file of a repository at a commit through the contents
// API, raw. A private repository needs the client's token.
func (c *Client) FileAt(ctx context.Context, r Ref, sha, path string) ([]byte, error) {
	parts := strings.Split(path, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.API+"/repos/"+url.PathEscape(r.Owner)+"/"+url.PathEscape(r.Name)+"/contents/"+strings.Join(parts, "/")+"?ref="+url.QueryEscape(sha), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github.raw+json")
	req.Header.Set("User-Agent", "vig")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return nil, ErrNotFound
	case http.StatusForbidden, http.StatusTooManyRequests:
		return nil, ErrRateLimited
	default:
		return nil, fmt.Errorf("github contents %s: %s", path, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 4<<20))
}

// maxReferenceCaptures bounds one report's reads of other repositories.
const maxReferenceCaptures = 40

// CaptureReferences reads the lines every GitHub file link in the report
// points at: a finding's references and a known item's reference. A link on
// a branch or tag is pinned to the commit it resolved to. What cannot be
// read (a private repository, a path that is not there, the rate limit)
// stays a plain link; nothing fails the submission. Captured anew on every
// store, so a revision that changes a link changes its code.
func (c *Client) CaptureReferences(ctx context.Context, doc *core.FindingsDocument) {
	shas := map[string]string{}
	budget := maxReferenceCaptures
	capture := func(raw string) *core.ReferenceCode {
		link, ok := core.ParseBlobLink(raw)
		if !ok || budget == 0 {
			return nil
		}
		budget--
		ref := Ref{Owner: link.Owner, Name: link.Name}
		sha := link.Rev
		if !link.IsSHA() {
			key := ref.FullName() + "@" + link.Rev
			if s, seen := shas[key]; seen {
				sha = s
			} else {
				commit, err := c.Resolve(ctx, ref, link.Rev)
				if err != nil {
					slog.Info("reference: not pinned", "url", raw, "err", err)
					return nil
				}
				sha = commit.SHA
				shas[key] = sha
			}
		}
		b, err := c.FileAt(ctx, ref, sha, link.Path)
		if err != nil {
			slog.Info("reference: not captured", "url", raw, "err", err)
			return nil
		}
		from, code, ok := SegmentBytes(b, link.Start, link.End)
		if !ok {
			return nil
		}
		return &core.ReferenceCode{URL: raw, Pinned: link.Pinned(sha), Repo: ref.FullName(), Commit: sha, File: link.Path, Start: link.Start, End: link.End, Code: core.Snippet{StartLine: from, Code: code}}
	}
	for i := range doc.Findings {
		f := &doc.Findings[i]
		f.ReferenceCode = nil
		for _, raw := range f.References {
			if rc := capture(raw); rc != nil {
				f.ReferenceCode = append(f.ReferenceCode, *rc)
			}
		}
	}
	for i := range doc.Report.Known {
		k := &doc.Report.Known[i]
		k.Code = capture(k.Reference)
	}
}
