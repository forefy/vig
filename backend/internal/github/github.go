// Package github resolves public repositories through the REST API and
// caches commit tarballs on disk so the tree and the pre-flight can read
// files without git.
package github

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Client talks to the GitHub REST API. Token is optional and only raises
// the rate limit.
type Client struct {
	API     string
	Token   string
	HTTP    *http.Client
	DataDir string
}

// New builds a client with tarballs cached under dataDir/clones.
func New(api, token, dataDir string) *Client {
	return &Client{API: strings.TrimRight(api, "/"), Token: token, HTTP: &http.Client{Timeout: 120 * time.Second}, DataDir: dataDir}
}

// WithToken returns a copy of the client that authenticates with token (an
// installation token for a private repository). An empty token returns the
// client itself.
func (c *Client) WithToken(token string) *Client {
	if token == "" {
		return c
	}
	cp := *c
	cp.Token = token
	return &cp
}

// Ref is a parsed repository reference.
type Ref struct {
	Owner string
	Name  string
	Rev   string // branch, tag or sha from /tree/<rev>; empty for the default branch
	Path  string // optional subdirectory from /tree/<rev>/<path>
}

// FullName is owner/name.
func (r Ref) FullName() string { return r.Owner + "/" + r.Name }

var repoURLRE = regexp.MustCompile(`^https?://(?:www\.)?github\.com/([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+?)(?:\.git)?(?:/tree/([^/\s]+)(?:/(.*))?)?/?$`)

// ParseURL accepts https://github.com/owner/name[/tree/rev[/path]].
func ParseURL(s string) (Ref, error) {
	m := repoURLRE.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return Ref{}, errors.New("not a public GitHub repository URL")
	}
	return Ref{Owner: m[1], Name: m[2], Rev: m[3], Path: strings.Trim(m[4], "/")}, nil
}

// ParseFullName accepts owner/name.
func ParseFullName(s string) (Ref, error) {
	owner, name, ok := strings.Cut(strings.TrimSpace(s), "/")
	if !ok || owner == "" || name == "" || strings.Contains(name, "/") {
		return Ref{}, errors.New("repo must be owner/name")
	}
	return Ref{Owner: owner, Name: name}, nil
}

// Commit is a resolved revision.
type Commit struct {
	SHA    string
	Author string
	Date   time.Time
}

func (c *Client) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.API+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "vig")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound, http.StatusUnprocessableEntity: // 422 is GitHub's answer for an unknown ref
		return ErrNotFound
	case http.StatusForbidden, http.StatusTooManyRequests:
		return ErrRateLimited
	default:
		return fmt.Errorf("github %s: %s", path, resp.Status)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(out)
}

// ErrNotFound means the repo or revision does not exist or is private.
var ErrNotFound = errors.New("repository or revision not found")

// ErrRateLimited means GitHub refused the unauthenticated call.
var ErrRateLimited = errors.New("github rate limit reached, try again later")

// DefaultBranch returns the repository's default branch.
func (c *Client) DefaultBranch(ctx context.Context, r Ref) (string, error) {
	var repo struct {
		DefaultBranch string `json:"default_branch"`
	}
	if err := c.get(ctx, "/repos/"+url.PathEscape(r.Owner)+"/"+url.PathEscape(r.Name), &repo); err != nil {
		return "", err
	}
	return repo.DefaultBranch, nil
}

// Resolve turns a branch, tag or (partial) sha into a full commit.
func (c *Client) Resolve(ctx context.Context, r Ref, rev string) (Commit, error) {
	if rev == "" {
		b, err := c.DefaultBranch(ctx, r)
		if err != nil {
			return Commit{}, err
		}
		rev = b
	}
	var out struct {
		SHA    string `json:"sha"`
		Commit struct {
			Author struct {
				Name string    `json:"name"`
				Date time.Time `json:"date"`
			} `json:"author"`
		} `json:"commit"`
	}
	if err := c.get(ctx, "/repos/"+url.PathEscape(r.Owner)+"/"+url.PathEscape(r.Name)+"/commits/"+url.PathEscape(rev), &out); err != nil {
		return Commit{}, err
	}
	if out.SHA == "" {
		return Commit{}, ErrNotFound
	}
	return Commit{SHA: out.SHA, Author: out.Commit.Author.Name, Date: out.Commit.Author.Date}, nil
}

// Compare lists the files that differ between base and head
// (GET /repos/{r}/compare/{base}...{head}), paging through the file list.
// Paths are relative to the repository root.
func (c *Client) Compare(ctx context.Context, r Ref, base, head string) ([]string, error) {
	var files []string
	for page := 1; page <= 30; page++ {
		var out struct {
			Files []struct {
				Filename         string `json:"filename"`
				PreviousFilename string `json:"previous_filename"`
			} `json:"files"`
		}
		path := "/repos/" + url.PathEscape(r.Owner) + "/" + url.PathEscape(r.Name) + "/compare/" + url.PathEscape(base) + "..." + url.PathEscape(head) + "?per_page=100&page=" + fmt.Sprint(page)
		if err := c.get(ctx, path, &out); err != nil {
			return nil, err
		}
		for _, f := range out.Files {
			files = append(files, f.Filename)
			if f.PreviousFilename != "" {
				files = append(files, f.PreviousFilename)
			}
		}
		if len(out.Files) < 100 {
			break
		}
	}
	return files, nil
}

// CloneDir is where a commit is extracted.
func (c *Client) CloneDir(fullName, sha string) string {
	return filepath.Join(c.DataDir, "clones", strings.ReplaceAll(fullName, "/", "__")+"__"+sha)
}

// Cached returns the clone dir when the commit is already extracted, and
// touches it so cleanup keeps it. No network is involved, so a private
// repository's cached checkout needs no token.
func (c *Client) Cached(fullName, sha string) (string, bool) {
	dir := c.CloneDir(fullName, sha)
	if st, err := os.Stat(filepath.Join(dir, ".ok")); err == nil && !st.IsDir() {
		_ = os.Chtimes(filepath.Join(dir, ".ok"), time.Now(), time.Now())
		return dir, true
	}
	return "", false
}

// Checkout downloads the commit tarball (once) and extracts it to the clone
// dir. It returns the directory holding the repository root. An optional
// token (an installation token for a private repository) authenticates the
// download; the cached path never needs one.
func (c *Client) Checkout(ctx context.Context, fullName, sha string, token ...string) (string, error) {
	if dir, ok := c.Cached(fullName, sha); ok {
		return dir, nil
	}
	if len(token) > 0 && token[0] != "" {
		c = c.WithToken(token[0])
	}
	dir := c.CloneDir(fullName, sha)
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return "", err
	}
	tmp := dir + ".tmp"
	_ = os.RemoveAll(tmp)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.API+"/repos/"+fullName+"/tarball/"+sha, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "vig")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return "", ErrNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("tarball: %s", resp.Status)
	}
	if err := extract(io.LimitReader(resp.Body, 512<<20), tmp); err != nil {
		_ = os.RemoveAll(tmp)
		return "", err
	}
	if err := os.WriteFile(filepath.Join(tmp, ".ok"), []byte(sha), 0o644); err != nil {
		return "", err
	}
	_ = os.RemoveAll(dir)
	if err := os.Rename(tmp, dir); err != nil {
		return "", err
	}
	return dir, nil
}

// extract unpacks a GitHub tarball, dropping the top-level directory.
func extract(r io.Reader, dest string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	var total int64
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		parts := strings.SplitN(h.Name, "/", 2)
		if len(parts) < 2 || parts[1] == "" {
			continue
		}
		rel := filepath.Clean(parts[1])
		if strings.HasPrefix(rel, "..") {
			continue
		}
		target := filepath.Join(dest, rel)
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if h.Size > 16<<20 {
				continue
			}
			total += h.Size
			if total > 512<<20 {
				return errors.New("repository too large")
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return err
			}
			f.Close()
		}
	}
}

// Cleanup removes clones not touched for longer than maxAge.
func (c *Client) Cleanup(maxAge time.Duration) int {
	entries, err := os.ReadDir(filepath.Join(c.DataDir, "clones"))
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		p := filepath.Join(c.DataDir, "clones", e.Name())
		st, err := os.Stat(filepath.Join(p, ".ok"))
		if (err == nil && time.Since(st.ModTime()) > maxAge) || (err != nil && strings.HasSuffix(e.Name(), ".tmp")) {
			if os.RemoveAll(p) == nil {
				n++
			}
		}
	}
	return n
}

// RemoveClone deletes one cached checkout.
func (c *Client) RemoveClone(fullName, sha string) error {
	return os.RemoveAll(c.CloneDir(fullName, sha))
}

// SearchCode finds public repositories whose Rust sources or Anchor.toml
// contain needle, most relevant first. GitHub only answers code searches for
// authenticated callers, so without a token this returns ErrRateLimited.
// Ten searches a minute is the ceiling; callers cache.
func (c *Client) SearchCode(ctx context.Context, needle string) ([]string, error) {
	if c.Token == "" {
		return nil, ErrRateLimited
	}
	var res struct {
		Items []struct {
			Path       string `json:"path"`
			Repository struct {
				FullName string `json:"full_name"`
				Fork     bool   `json:"fork"`
			} `json:"repository"`
		} `json:"items"`
	}
	q := url.QueryEscape("\""+needle+"\" in:file language:Rust") + "&per_page=10"
	if err := c.get(ctx, "/search/code?q="+q, &res); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []string
	for _, it := range res.Items {
		if it.Repository.Fork || seen[it.Repository.FullName] {
			continue
		}
		seen[it.Repository.FullName] = true
		out = append(out, it.Repository.FullName)
	}
	return out, nil
}
