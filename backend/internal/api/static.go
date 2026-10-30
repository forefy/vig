package api

import (
	"bytes"
	"fmt"
	"html"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/forefy/vig/backend/internal/og"
	"github.com/forefy/vig/backend/internal/store"
)

// apiPrefixes are the paths the SPA fallback never claims.
var apiPrefixes = []string{"/v1", "/openapi.json", "/llms.txt", "/docs", "/healthz", "/readyz", "/version", "/metrics", "/og"}

// noindexPrefixes are served with X-Robots-Tag: noindex.
var noindexPrefixes = []string{"/s/", "/app", "/admin", "/login"}

// isAPIPath reports whether p belongs to the API surface rather than the app.
func isAPIPath(p string) bool {
	for _, pre := range apiPrefixes {
		if p == pre || strings.HasPrefix(p, pre+"/") {
			return true
		}
	}
	return false
}

// isNoindex reports whether p is an app or scan page crawlers must skip.
func isNoindex(p string) bool {
	for _, pre := range noindexPrefixes {
		if strings.HasPrefix(p, pre) {
			return true
		}
	}
	return false
}

// static serves the built frontend from Config.StaticDir.
type static struct {
	dir   string
	index []byte
	d     *Deps

	mu    sync.Mutex
	cache map[string]cachedPage
}

type cachedPage struct {
	body    []byte
	status  int
	expires time.Time
}

func newStatic(d *Deps) (*static, error) {
	index, err := os.ReadFile(filepath.Join(d.Config.StaticDir, "index.html"))
	if err != nil {
		return nil, fmt.Errorf("static: %w", err)
	}
	return &static{dir: d.Config.StaticDir, index: index, d: d, cache: map[string]cachedPage{}}, nil
}

// mount registers the static routes on r. The catch-all is chi's NotFound so
// every huma route keeps precedence.
func (s *static) mount(r chi.Router) {
	r.Get("/r/{slug}", s.report)
	r.NotFound(s.serve)
}

func (s *static) serve(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeProblem(w, r, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	p := path.Clean("/" + r.URL.Path)
	if isAPIPath(p) {
		writeProblem(w, r, http.StatusNotFound, "no such route")
		return
	}
	if isNoindex(p) {
		w.Header().Set("X-Robots-Tag", "noindex")
	}
	full := filepath.Join(s.dir, filepath.FromSlash(p))
	if fi, err := os.Stat(full); err == nil && !fi.IsDir() {
		if strings.HasPrefix(p, "/assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else if strings.HasSuffix(p, ".html") {
			w.Header().Set("Cache-Control", "no-cache")
		} else {
			w.Header().Set("Cache-Control", "public, max-age=3600")
		}
		http.ServeFile(w, r, full)
		return
	}
	// Prerendered route: dist/<route>/index.html.
	if p != "/" {
		pre := filepath.Join(full, "index.html")
		if fi, err := os.Stat(pre); err == nil && !fi.IsDir() {
			w.Header().Set("Cache-Control", "no-cache")
			http.ServeFile(w, r, pre)
			return
		}
	}
	s.html(w, http.StatusOK, s.index)
}

func (s *static) html(w http.ResponseWriter, status int, body []byte) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if w.Header().Get("Cache-Control") == "" {
		w.Header().Set("Cache-Control", "no-cache")
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// ---------- /r/{slug} ----------

// headTag matches the landing head tags the prerender wrote, which the
// report page replaces with its own.
var headTag = regexp.MustCompile(`(?is)\s*<title>.*?</title>|\s*<meta\s+name="description"[^>]*>|\s*<meta\s+property="og:(?:type|title|description|url|image)"[^>]*>|\s*<meta\s+name="twitter:[^"]*"[^>]*>|\s*<link\s+rel="canonical"[^>]*>`)

const reportPageTTL = 60 * time.Second

// report serves the SPA shell for a published report with its own title,
// description, canonical and OpenGraph tags, so unfurlers see the pin and
// the counts without executing the app.
func (s *static) report(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	if body, status, ok := s.cached(slug); ok {
		s.reportHeaders(w, status)
		s.html(w, status, body)
		return
	}
	status := http.StatusOK
	var body []byte
	rep, err := s.loadReport(r, slug)
	if err != nil {
		status = http.StatusNotFound
		body = s.injectHead(reportHead{noindex: true, title: "Program audits", description: "Audits of deployed Solana programs, pinned to the bytecode that runs.", image: s.d.Config.SiteURL + "/og/default.png"})
	} else {
		body = s.injectHead(reportHeadOf(rep, s.d.Config.SiteURL))
	}
	s.store(slug, body, status)
	s.reportHeaders(w, status)
	s.html(w, status, body)
}

func (s *static) reportHeaders(w http.ResponseWriter, status int) {
	w.Header().Set("Cache-Control", "public, max-age=60")
	if status != http.StatusOK {
		w.Header().Set("X-Robots-Tag", "noindex")
	}
}

func (s *static) loadReport(r *http.Request, slug string) (store.Report, error) {
	if s.d.Store == nil {
		return store.Report{}, store.ErrNotFound
	}
	return s.d.Store.GetReportBySlug(r.Context(), slug)
}

func (s *static) cached(slug string) ([]byte, int, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.cache[slug]
	if !ok || s.d.now().After(c.expires) {
		return nil, 0, false
	}
	return c.body, c.status, true
}

func (s *static) store(slug string, body []byte, status int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.cache) > 4096 {
		s.cache = map[string]cachedPage{}
	}
	s.cache[slug] = cachedPage{body: body, status: status, expires: s.d.now().Add(reportPageTTL)}
}

type reportHead struct {
	title, description, canonical, image string
	noindex                              bool
}

func reportHeadOf(rep store.Report, site string) reportHead {
	sum := rep.Summary
	n := sum.Critical + sum.High + sum.Medium + sum.Low + sum.Info
	sha := rep.Pin.Commit
	if len(sha) > 7 {
		sha = sha[:7]
	}
	return reportHead{
		title:       rep.Pin.Repo + " audit",
		description: fmt.Sprintf("%d findings at %s: %d critical, %d high, %d medium, %d low, %d info", n, sha, sum.Critical, sum.High, sum.Medium, sum.Low, sum.Info),
		canonical:   site + "/r/" + rep.PublishedSlug,
		image:       site + "/og/r/" + rep.PublishedSlug + ".png",
	}
}

// injectHead strips the prerendered landing tags from index.html and adds
// the report's.
func (s *static) injectHead(h reportHead) []byte {
	out := headTag.ReplaceAll(s.index, nil)
	var b strings.Builder
	esc := html.EscapeString
	fmt.Fprintf(&b, "\n    <title>%s</title>", esc(h.title))
	fmt.Fprintf(&b, "\n    <meta name=\"description\" content=\"%s\"/>", esc(h.description))
	if h.noindex {
		b.WriteString("\n    <meta name=\"robots\" content=\"noindex\"/>")
	} else {
		fmt.Fprintf(&b, "\n    <link rel=\"canonical\" href=\"%s\"/>", esc(h.canonical))
		fmt.Fprintf(&b, "\n    <meta property=\"og:url\" content=\"%s\"/>", esc(h.canonical))
	}
	b.WriteString("\n    <meta property=\"og:type\" content=\"article\"/>")
	fmt.Fprintf(&b, "\n    <meta property=\"og:title\" content=\"%s\"/>", esc(h.title))
	fmt.Fprintf(&b, "\n    <meta property=\"og:description\" content=\"%s\"/>", esc(h.description))
	fmt.Fprintf(&b, "\n    <meta property=\"og:image\" content=\"%s\"/>", esc(h.image))
	b.WriteString("\n    <meta property=\"og:image:width\" content=\"1200\"/>\n    <meta property=\"og:image:height\" content=\"630\"/>")
	b.WriteString("\n    <meta name=\"twitter:card\" content=\"summary_large_image\"/>")
	fmt.Fprintf(&b, "\n    <meta name=\"twitter:title\" content=\"%s\"/>", esc(h.title))
	fmt.Fprintf(&b, "\n    <meta name=\"twitter:description\" content=\"%s\"/>", esc(h.description))
	fmt.Fprintf(&b, "\n    <meta name=\"twitter:image\" content=\"%s\"/>", esc(h.image))
	b.WriteString("\n  </head>")
	return bytes.Replace(out, []byte("</head>"), []byte(b.String()), 1)
}

// ---------- /og ----------

// mountOG registers the preview image routes. They do not need StaticDir:
// the prerendered pages reference /og/default.png from any deployment.
func (d *Deps) mountOG(r chi.Router) {
	var (
		once sync.Once
		def  []byte
		derr error
	)
	r.Get("/og/default.png", func(w http.ResponseWriter, r *http.Request) {
		once.Do(func() { def, derr = og.Default() })
		if derr != nil {
			writeProblem(w, r, http.StatusInternalServerError, "render failed")
			return
		}
		servePNG(w, def)
	})
	r.Get("/og/r/{slug}.png", func(w http.ResponseWriter, r *http.Request) {
		if d.Store == nil {
			writeProblem(w, r, http.StatusServiceUnavailable, "database not configured")
			return
		}
		rep, err := d.Store.GetReportBySlug(r.Context(), chi.URLParam(r, "slug"))
		if err != nil {
			writeProblem(w, r, http.StatusNotFound, "not found")
			return
		}
		states := make([]string, 0, len(rep.Pin.Programs))
		for _, p := range rep.Pin.Programs {
			states = append(states, string(p.Match))
		}
		in := og.Report{
			Repo: rep.Pin.Repo, Commit: rep.Pin.Commit, Programs: len(rep.Pin.Programs), Adapter: rep.Adapter, Match: og.WorstMatch(states),
			Critical: rep.Summary.Critical, High: rep.Summary.High, Medium: rep.Summary.Medium, Low: rep.Summary.Low, Info: rep.Summary.Info,
		}
		// A site has no commit of its own: its line is its repositories, live.
		if rep.Adapter == "webapp" {
			in.Commit, in.Programs, in.Match = "", len(rep.Pin.Repos), "live"
		}
		img, err := og.Render(in)
		if err != nil {
			writeProblem(w, r, http.StatusInternalServerError, "render failed")
			return
		}
		servePNG(w, img)
	})
}

func servePNG(w http.ResponseWriter, b []byte) {
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.Header().Set("Content-Length", strconv.Itoa(len(b)))
	_, _ = w.Write(b)
}
