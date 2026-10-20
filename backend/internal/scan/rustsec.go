// Package scan implements the deterministic parts of the pre-flight that
// are not chain reads: dependency advisories from the RustSec database
// matched against Cargo.lock.
package scan

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/forefy/vig/backend/internal/core"
)

// AdvisoryDBURL is the RustSec advisory database archive.
const AdvisoryDBURL = "https://github.com/rustsec/advisory-db/archive/refs/heads/main.tar.gz"

// Advisory is one parsed RUSTSEC entry.
type Advisory struct {
	ID            string   `json:"id"`
	Package       string   `json:"package"`
	Title         string   `json:"title"`
	CVSS          string   `json:"cvss,omitempty"`
	Informational string   `json:"informational,omitempty"`
	Patched       []string `json:"patched"`
	Unaffected    []string `json:"unaffected"`
	Withdrawn     bool     `json:"withdrawn,omitempty"`
}

// URL is the advisory page.
func (a Advisory) URL() string { return "https://rustsec.org/advisories/" + a.ID + ".html" }

// Severity buckets the advisory: cvss score bands, or info for
// informational entries, or unknown.
func (a Advisory) Severity() string {
	if a.Informational != "" {
		return "info"
	}
	if a.CVSS == "" {
		return "unknown"
	}
	score, err := CVSSBase(a.CVSS)
	if err != nil {
		return "unknown"
	}
	switch {
	case score >= 9:
		return "critical"
	case score >= 7:
		return "high"
	case score >= 4:
		return "medium"
	case score > 0:
		return "low"
	}
	return "info"
}

// Affects reports whether the version falls in the vulnerable range.
func (a Advisory) Affects(v Version) bool {
	for _, req := range a.Unaffected {
		if MatchReq(req, v) {
			return false
		}
	}
	for _, req := range a.Patched {
		if MatchReq(req, v) {
			return false
		}
	}
	return true
}

// DB is the advisory set with an on-disk cache.
type DB struct {
	Dir  string
	URL  string
	TTL  time.Duration
	HTTP *http.Client

	mu   sync.Mutex
	list []Advisory
	at   time.Time
}

// NewDB caches advisories.json under dir.
func NewDB(dir string) *DB {
	return &DB{Dir: dir, URL: AdvisoryDBURL, TTL: 24 * time.Hour, HTTP: &http.Client{Timeout: 120 * time.Second}}
}

// Advisories returns the parsed database, refreshing when stale. A failed
// refresh keeps serving the last good copy.
func (d *DB) Advisories(ctx context.Context) ([]Advisory, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.list != nil && time.Since(d.at) < d.TTL {
		return d.list, nil
	}
	cache := filepath.Join(d.Dir, "advisories.json")
	if st, err := os.Stat(cache); err == nil && time.Since(st.ModTime()) < d.TTL {
		if b, err := os.ReadFile(cache); err == nil {
			var list []Advisory
			if json.Unmarshal(b, &list) == nil && len(list) > 0 {
				d.list, d.at = list, st.ModTime()
				return list, nil
			}
		}
	}
	list, err := d.download(ctx)
	if err != nil {
		if b, rerr := os.ReadFile(cache); rerr == nil {
			var stale []Advisory
			if json.Unmarshal(b, &stale) == nil && len(stale) > 0 {
				d.list, d.at = stale, time.Now()
				return stale, nil
			}
		}
		if d.list != nil {
			return d.list, nil
		}
		return nil, err
	}
	_ = os.MkdirAll(d.Dir, 0o755)
	if b, err := json.Marshal(list); err == nil {
		_ = os.WriteFile(cache, b, 0o644)
	}
	d.list, d.at = list, time.Now()
	return list, nil
}

func (d *DB) download(ctx context.Context) ([]Advisory, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, d.URL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "vig")
	resp, err := d.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("advisory db: %s", resp.Status)
	}
	return ParseArchive(io.LimitReader(resp.Body, 256<<20))
}

// ParseArchive reads every crates/<name>/RUSTSEC-*.md in the tarball.
func ParseArchive(r io.Reader) ([]Advisory, error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return nil, err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	var out []Advisory
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if h.Typeflag != tar.TypeReg || !strings.Contains(h.Name, "/crates/") || !strings.HasSuffix(h.Name, ".md") {
			continue
		}
		b, err := io.ReadAll(io.LimitReader(tr, 1<<20))
		if err != nil {
			return nil, err
		}
		if a, ok := ParseAdvisory(string(b)); ok {
			out = append(out, a)
		}
	}
	if len(out) == 0 {
		return nil, errors.New("advisory db archive had no advisories")
	}
	return out, nil
}

var (
	tomlStr   = regexp.MustCompile(`(?m)^\s*([a-z_]+)\s*=\s*"([^"]*)"`)
	tomlArr   = regexp.MustCompile(`(?ms)^\s*([a-z_]+)\s*=\s*\[(.*?)\]`)
	quotedRE  = regexp.MustCompile(`"([^"]*)"`)
	sectionRE = regexp.MustCompile(`(?m)^\s*\[([a-z_.]+)\]\s*$`)
	titleRE   = regexp.MustCompile(`(?m)^#\s+(.+)$`)
)

// ParseAdvisory reads the TOML front matter of one advisory file.
func ParseAdvisory(md string) (Advisory, bool) {
	start := strings.Index(md, "```toml")
	if start < 0 {
		return Advisory{}, false
	}
	rest := md[start+len("```toml"):]
	end := strings.Index(rest, "```")
	if end < 0 {
		return Advisory{}, false
	}
	front := rest[:end]
	sections := splitSections(front)
	a := Advisory{}
	adv := sections["advisory"]
	for _, m := range tomlStr.FindAllStringSubmatch(adv, -1) {
		switch m[1] {
		case "id":
			a.ID = m[2]
		case "package":
			a.Package = m[2]
		case "cvss":
			a.CVSS = m[2]
		case "informational":
			a.Informational = m[2]
		case "withdrawn":
			a.Withdrawn = true
		}
	}
	for _, m := range tomlArr.FindAllStringSubmatch(sections["versions"], -1) {
		vals := []string{}
		for _, q := range quotedRE.FindAllStringSubmatch(m[2], -1) {
			vals = append(vals, q[1])
		}
		switch m[1] {
		case "patched":
			a.Patched = vals
		case "unaffected":
			a.Unaffected = vals
		}
	}
	if m := titleRE.FindStringSubmatch(md[start+end:]); m != nil {
		a.Title = strings.TrimSpace(m[1])
	}
	if a.ID == "" || a.Package == "" {
		return Advisory{}, false
	}
	if a.Patched == nil {
		a.Patched = []string{}
	}
	if a.Unaffected == nil {
		a.Unaffected = []string{}
	}
	return a, true
}

func splitSections(s string) map[string]string {
	out := map[string]string{}
	locs := sectionRE.FindAllStringSubmatchIndex(s, -1)
	for i, loc := range locs {
		name := s[loc[2]:loc[3]]
		end := len(s)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		out[name] = s[loc[1]:end]
	}
	return out
}

// Match returns the advisories that affect the locked packages.
func Match(advisories []Advisory, packages []Package) []core.Advisory {
	byName := map[string][]Advisory{}
	for _, a := range advisories {
		if a.Withdrawn {
			continue
		}
		byName[a.Package] = append(byName[a.Package], a)
	}
	out := []core.Advisory{}
	for _, p := range packages {
		v, err := ParseVersion(p.Version)
		if err != nil {
			continue
		}
		for _, a := range byName[p.Name] {
			if a.Affects(v) {
				out = append(out, core.Advisory{ID: a.ID, Package: p.Name, Version: p.Version, Severity: a.Severity(), URL: a.URL()})
			}
		}
	}
	return out
}
