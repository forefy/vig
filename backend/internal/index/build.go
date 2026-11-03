package index

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/forefy/vig/backend/internal/core"
)

// RepoInput is one repository checkout the build reads.
type RepoInput struct {
	Name   string // owner/name
	Dir    string // the checkout
	Commit string
	Branch string
}

// Options is what a build takes.
type Options struct {
	Root      string // parts go under <Root>/index/<table>/target=<Slug>/
	Slug      string // the target: the audit id, or a name for a build off the job
	Repos     []RepoInput
	HARs      []string // capture files
	Names     string   // a DNS listing
	Resources string   // a cloud listing
	Now       time.Time
	Log       func(string)
}

// Summary is what a build reports.
type Summary struct {
	Files, Lines, Functions, Calls, Flows, Imports, Literals, Routes int
	Requests, Bodies, Hosts, Subdomains, Resources, Edges            int
	Parts                                                            []core.IndexPartFile
}

// Build reads every input, links them and writes the parts. It returns
// the parts the way IndexParts lists them, ready to push or import.
func Build(ctx context.Context, o Options) (Summary, error) {
	if o.Slug == "" {
		return Summary{}, fmt.Errorf("index build: a target is needed")
	}
	if !CanExtract && len(o.Repos) > 0 {
		return Summary{}, fmt.Errorf("index build: this build of vig-op carries no parsers")
	}
	now := o.Now
	if now.IsZero() {
		now = time.Now()
	}
	now = now.UTC().Truncate(time.Millisecond)
	logf := o.Log
	if logf == nil {
		logf = func(string) {}
	}
	b := &builder{target: o.Slug, now: now, log: logf}
	for _, r := range o.Repos {
		if err := ctx.Err(); err != nil {
			return Summary{}, err
		}
		if err := b.repo(ctx, r); err != nil {
			return Summary{}, err
		}
	}
	b.link()
	for _, h := range o.HARs {
		t, err := ReadHAR(h, o.Slug, now)
		if err != nil {
			return Summary{}, err
		}
		b.entries = append(b.entries, t.Entries...)
		b.bodies = append(b.bodies, t.Bodies...)
		b.hosts = append(b.hosts, t.Hosts...)
		b.edges = append(b.edges, t.Edges...)
		logf(fmt.Sprintf("capture %s: %d requests, %d bodies, %d hosts", filepath.Base(h), len(t.Entries), len(t.Bodies), len(t.Hosts)))
	}
	if o.Names != "" {
		subs, err := ReadNames(o.Names, o.Slug, now)
		if err != nil {
			return Summary{}, err
		}
		b.subs = subs
		logf(fmt.Sprintf("names %s: %d", filepath.Base(o.Names), len(subs)))
	}
	if o.Resources != "" {
		res, err := ReadResources(o.Resources, o.Slug, now)
		if err != nil {
			return Summary{}, err
		}
		b.resources = res
		logf(fmt.Sprintf("resources %s: %d", filepath.Base(o.Resources), len(res)))
	}
	b.crossEdges()
	if err := b.write(o.Root); err != nil {
		return Summary{}, err
	}
	parts, err := core.IndexParts(o.Root, o.Slug, func(note string) { logf(note) })
	if err != nil {
		return Summary{}, err
	}
	return Summary{Files: len(b.files), Lines: len(b.sources), Functions: len(b.functions), Calls: len(b.calls), Flows: len(b.flows), Imports: len(b.imports), Literals: len(b.literals), Routes: len(b.routes),
		Requests: len(b.entries), Bodies: len(b.bodies), Hosts: len(b.hosts), Subdomains: len(b.subs), Resources: len(b.resources), Edges: len(b.edges), Parts: parts}, nil
}

type builder struct {
	target string
	now    time.Time
	log    func(string)

	repos     []RepoRow
	bindings  []Binding
	files     []FileRow
	sources   []SourceRow
	functions []FunctionRow
	calls     []CallRow
	flows     []FlowRow
	imports   []ImportRow
	literals  []LiteralRow
	routes    []RouteRow
	entries   []HAREntryRow
	bodies    []BodyRow
	hosts     []HostRow
	subs      []SubdomainRow
	resources []CloudResourceRow
	edges     []EdgeRow
}

// repo walks one checkout and extracts every source file it parses.
func (b *builder) repo(ctx context.Context, r RepoInput) error {
	root := filepath.Clean(r.Dir)
	var bytes int64
	langCount := map[Lang]int{}
	n := 0
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p != root && SkipDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		lang := LangOf(d.Name())
		if lang == "" {
			return nil
		}
		info, err := d.Info()
		if err != nil || !info.Mode().IsRegular() || SkipFile(d.Name(), info.Size()) {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		src, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		sum := sha256.Sum256(src)
		b.files = append(b.files, FileRow{Target: b.target, IngestedAt: b.now, Repo: r.Name, Path: rel, Lang: string(lang), Bytes: int64(len(src)), SHA256: hex.EncodeToString(sum[:]), LOC: int64(countLines(src))})
		bytes += int64(len(src))
		langCount[lang]++
		for i, line := range strings.Split(strings.TrimSuffix(string(src), "\n"), "\n") {
			b.sources = append(b.sources, SourceRow{Target: b.target, IngestedAt: b.now, Repo: r.Name, Path: rel, Line: int64(i + 1), Text: clean(strings.TrimRight(line, "\r"), lineMax)})
		}
		ex, err := Extract(lang, r.Name, rel, src)
		if err != nil {
			b.log(fmt.Sprintf("%s/%s: %v", r.Name, rel, err))
			return nil
		}
		ex.stamp(b.target, b.now)
		b.functions = append(b.functions, ex.Functions...)
		b.calls = append(b.calls, ex.Calls...)
		b.flows = append(b.flows, ex.Flows...)
		b.imports = append(b.imports, ex.Imports...)
		b.literals = append(b.literals, ex.Literals...)
		b.routes = append(b.routes, ex.Routes...)
		b.bindings = append(b.bindings, ex.Bindings...)
		n++
		return nil
	})
	if err != nil {
		return err
	}
	main := ""
	for l, c := range langCount {
		if main == "" || c > langCount[Lang(main)] || (c == langCount[Lang(main)] && string(l) < main) {
			main = string(l)
		}
	}
	b.repos = append(b.repos, RepoRow{Target: b.target, IngestedAt: b.now, Repo: r.Name, Commit: r.Commit, DefaultBranch: r.Branch, Language: main, SizeKB: int32(bytes / 1024), FetchedAt: b.now})
	b.log(fmt.Sprintf("%s: %d files, %d functions so far", r.Name, n, len(b.functions)))
	return nil
}

// A source line longer than this is cut: a minified file slipped through.
const lineMax = 4000

func countLines(src []byte) int {
	n := 0
	for _, c := range src {
		if c == '\n' {
			n++
		}
	}
	if len(src) > 0 && src[len(src)-1] != '\n' {
		n++
	}
	return n
}

// link resolves calls and route handlers to functions of the same
// repository, marks registered handlers, and adds the flows across a
// resolved call: arguments into parameters, the return back out.
func (b *builder) link() {
	// name -> function ids, by repo, and by repo+file.
	byName := map[string][]int{}
	byFile := map[string][]int{}
	for i, f := range b.functions {
		byName[f.Repo+"\x00"+f.Name] = append(byName[f.Repo+"\x00"+f.Name], i)
		byFile[f.Repo+"\x00"+f.Path+"\x00"+f.Name] = append(byFile[f.Repo+"\x00"+f.Path+"\x00"+f.Name], i)
	}
	resolve := func(repo, path, name string) (int, string) {
		if name == "" || name == "<anon>" {
			return -1, "unresolved"
		}
		if c := byFile[repo+"\x00"+path+"\x00"+name]; len(c) == 1 {
			return c[0], "local"
		}
		if c := byName[repo+"\x00"+name]; len(c) == 1 {
			return c[0], "local"
		} else if len(c) > 1 {
			return c[0], "ambiguous"
		}
		return -1, "unresolved"
	}
	for i := range b.calls {
		c := &b.calls[i]
		j, how := resolve(c.Repo, c.Path, simpleName(c.CalleeName))
		if j < 0 {
			continue
		}
		c.CalleeFn, c.Resolution = b.functions[j].FnID, how
		if how == "ambiguous" {
			continue
		}
		// Into the callee: argument i into parameter i, the return back.
		names := paramNames(b.functions[j].Params)
		callID := CallPrefix + c.CallID
		for k := range c.Args {
			if k >= len(names) || names[k] == "" {
				break
			}
			b.flows = append(b.flows, FlowRow{Target: b.target, IngestedAt: b.now, Repo: c.Repo, Path: c.Path, FnID: c.CallerFn, Src: callID, Dst: VarPrefix + b.functions[j].FnID + "#" + names[k], Kind: "param", ArgIndex: int64(k), Line: c.Line})
		}
		b.flows = append(b.flows, FlowRow{Target: b.target, IngestedAt: b.now, Repo: c.Repo, Path: c.Path, FnID: c.CallerFn, Src: RetPrefix + b.functions[j].FnID, Dst: callID, Kind: "return", ArgIndex: -1, Line: c.Line})
	}
	for i := range b.routes {
		r := &b.routes[i]
		if r.HandlerFn != "" {
			continue
		}
		j, _ := resolve(r.Repo, r.Path, simpleName(r.Handler))
		if j < 0 {
			continue
		}
		r.HandlerFn = b.functions[j].FnID
		b.functions[j].IsHandler = true
	}
	b.routerPrefixes()
	sort.Slice(b.flows, func(i, j int) bool {
		if b.flows[i].Path != b.flows[j].Path {
			return b.flows[i].Path < b.flows[j].Path
		}
		return b.flows[i].Line < b.flows[j].Line
	})
}

// routerPrefixes follows routers handed to calls into the callee's
// parameters, so a registration on a parameter router gets the prefix
// the caller built: `mount(r.PathPrefix("/api").Subrouter())` makes
// `router.HandleFunc("/x", h)` inside mount the route `/api/x`. A router
// passed on through several calls settles in a few rounds.
func (b *builder) routerPrefixes() {
	byID := map[string]int{}
	for i, f := range b.functions {
		byID[f.FnID] = i
	}
	callByID := map[string]CallRow{}
	for _, c := range b.calls {
		if c.CalleeFn != "" && c.Resolution == "local" {
			callByID[c.CallID] = c
		}
	}
	// (function, parameter) -> the prefix the router carries there.
	known := map[string]string{}
	for round := 0; round < 4; round++ {
		changed := false
		for _, bd := range b.bindings {
			c, ok := callByID[bd.CallID]
			if !ok {
				continue
			}
			f, ok := byID[c.CalleeFn]
			if !ok {
				continue
			}
			names := paramNames(b.functions[f].Params)
			if bd.ArgIndex >= len(names) || names[bd.ArgIndex] == "" {
				continue
			}
			// A router rooted at a parameter nobody in the repository calls
			// with (the entry point) counts from where it is.
			prefix := bd.Prefix
			if bd.Param != "" {
				if from, ok := known[bd.FnID+"\x00"+bd.Param]; ok {
					prefix = from + prefix
				}
			}
			key := c.CalleeFn + "\x00" + names[bd.ArgIndex]
			if have, ok := known[key]; !ok || have != prefix {
				known[key] = prefix
				changed = true
			}
		}
		if !changed {
			break
		}
	}
	for i := range b.routes {
		r := &b.routes[i]
		if r.routerParam == "" || r.FnID == "" {
			continue
		}
		if prefix, ok := known[r.FnID+"\x00"+r.routerParam]; ok {
			r.Pattern = prefix + r.Pattern
		}
	}
}

// patternRe turns a route pattern into a regexp over a request path:
// `{id}`, `:id`, `<int:id>` and `*` match one segment or more.
func patternRe(pattern string) *regexp.Regexp {
	p := strings.TrimRight(pattern, "/")
	if p == "" {
		p = "/"
	}
	var sb strings.Builder
	sb.WriteString("(^|/)")
	i := 0
	for i < len(p) {
		c := p[i]
		switch {
		case c == '{':
			j := strings.IndexByte(p[i:], '}')
			if j < 0 {
				sb.WriteString(regexp.QuoteMeta(p[i:]))
				i = len(p)
				continue
			}
			sb.WriteString("[^/]+")
			i += j + 1
		case c == '<':
			j := strings.IndexByte(p[i:], '>')
			if j < 0 {
				sb.WriteString(regexp.QuoteMeta(p[i:]))
				i = len(p)
				continue
			}
			sb.WriteString("[^/]+")
			i += j + 1
		case c == ':' && i+1 < len(p) && (p[i+1] == '_' || (p[i+1] >= 'a' && p[i+1] <= 'z') || (p[i+1] >= 'A' && p[i+1] <= 'Z')):
			j := i + 1
			for j < len(p) && p[j] != '/' {
				j++
			}
			sb.WriteString("[^/]+")
			i = j
		case c == '*':
			sb.WriteString(".*")
			i++
		default:
			sb.WriteString(regexp.QuoteMeta(string(c)))
			i++
		}
	}
	sb.WriteString("/?$")
	return regexp.MustCompile(sb.String())
}

// crossEdges joins the pillars: calls between functions, flows between
// values, hosts to the handlers their requests route to, bodies to the
// files they are, literals to the hosts and resources they name, names
// to the resources they resolve to.
func (b *builder) crossEdges() {
	add := func(srcKind, src, dstKind, dst, rel string, conf float64, evidence string) {
		b.edges = append(b.edges, EdgeRow{Target: b.target, IngestedAt: b.now, SrcKind: srcKind, SrcID: src, DstKind: dstKind, DstID: dst, Rel: rel, Confidence: conf, Evidence: evidence})
	}
	seen := map[string]bool{}
	for _, c := range b.calls {
		if c.CallerFn == "" || c.CalleeFn == "" {
			continue
		}
		k := c.CallerFn + "\x00" + c.CalleeFn
		if seen[k] {
			continue
		}
		seen[k] = true
		conf := 1.0
		if c.Resolution == "ambiguous" {
			conf = 0.6
		}
		add("function", FnPrefix+c.CallerFn, "function", FnPrefix+c.CalleeFn, "calls", conf, c.CalleeName)
	}
	kindOf := func(id string) string {
		switch {
		case strings.HasPrefix(id, VarPrefix):
			return "var"
		case strings.HasPrefix(id, CallPrefix):
			return "call"
		case strings.HasPrefix(id, RetPrefix):
			return "ret"
		}
		return "node"
	}
	seen = map[string]bool{}
	for _, f := range b.flows {
		k := f.Src + "\x00" + f.Dst
		if seen[k] {
			continue
		}
		seen[k] = true
		add(kindOf(f.Src), f.Src, kindOf(f.Dst), f.Dst, "flows", 1, f.Kind)
	}
	// Requests to the handlers whose patterns their paths fit.
	type rt struct {
		re  *regexp.Regexp
		row RouteRow
	}
	var routes []rt
	for _, r := range b.routes {
		if r.HandlerFn == "" || r.Pattern == "" {
			continue
		}
		routes = append(routes, rt{patternRe(r.Pattern), r})
	}
	if len(routes) > 0 {
		paths := map[string]map[string]bool{}
		for _, e := range b.entries {
			if paths[e.Host] == nil {
				paths[e.Host] = map[string]bool{}
			}
			paths[e.Host][e.Method+" "+e.Path] = true
		}
		seen = map[string]bool{}
		for host, reqs := range paths {
			for mp := range reqs {
				method, p, _ := strings.Cut(mp, " ")
				for _, r := range routes {
					if !r.re.MatchString(p) {
						continue
					}
					if r.row.Method != "" && !strings.Contains(r.row.Method, method) {
						continue
					}
					k := host + "\x00" + r.row.HandlerFn
					if seen[k] {
						continue
					}
					seen[k] = true
					conf := 0.8
					if strings.Count(r.row.Pattern, "/") >= 2 {
						conf = 0.9
					}
					add("host", HostPrefix+host, "function", FnPrefix+r.row.HandlerFn, "routes", conf, strings.TrimSpace(r.row.Method+" "+r.row.Pattern))
				}
			}
		}
	}
	// A served body that is a file of a repository.
	fileBySHA := map[string]FileRow{}
	for _, f := range b.files {
		fileBySHA[f.SHA256] = f
	}
	seen = map[string]bool{}
	for _, body := range b.bodies {
		f, ok := fileBySHA[body.SHA256]
		if !ok || seen[body.SHA256] {
			continue
		}
		seen[body.SHA256] = true
		add("body", BodyPrefix+body.SHA256, "repo", RepoPrefix+f.Repo, "serves_repo", 1, f.Path)
	}
	// Literals that name a host the capture or the listing knows, or a
	// resource's hostname or id.
	known := map[string]bool{}
	for _, h := range b.hosts {
		known[h.Host] = true
	}
	for _, s := range b.subs {
		known[s.FQDN] = true
	}
	apexes := map[string]bool{}
	for h := range known {
		apexes[apexOf(h)] = true
	}
	type resHost struct {
		host string
		rid  string
	}
	var resHosts []resHost
	for _, r := range b.resources {
		for _, h := range r.Hostnames {
			if h != "" {
				resHosts = append(resHosts, resHost{h, r.RID})
			}
		}
	}
	for _, l := range b.literals {
		if l.Kind != "host" && l.Kind != "url" {
			continue
		}
		host := hostOfLiteral(l.Value)
		if host == "" {
			continue
		}
		id := LiteralPrefix + l.Repo + ":" + l.Path + ":" + fmt.Sprint(l.Line)
		switch {
		case known[host]:
			add("literal", id, "host", HostPrefix+host, "mentions_host", 1, l.Value)
		case apexes[apexOf(host)]:
			add("literal", id, "host", HostPrefix+host, "mentions_host", 0.7, l.Value)
		}
		for _, rh := range resHosts {
			if host == rh.host || strings.HasSuffix(host, "."+rh.host) {
				add("literal", id, "resource", ResourcePrefix+rh.rid, "mentions_resource", 1, l.Value)
			}
		}
	}
	b.edges = append(b.edges, resolveEdges(b.subs, b.resources, b.target, b.now)...)
}

func hostOfLiteral(v string) string {
	s := strings.ToLower(v)
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	if i := strings.IndexAny(s, "/?#:"); i >= 0 {
		s = s[:i]
	}
	if !strings.Contains(s, ".") {
		return ""
	}
	return s
}

// write lays every table out as parts.
func (b *builder) write(root string) error {
	if err := clearTarget(root, b.target); err != nil {
		return err
	}
	w := func(table string, err error) error {
		if err != nil {
			return fmt.Errorf("write %s: %w", table, err)
		}
		return nil
	}
	var err error
	_, err = writeTable(root, b.target, "repos", b.repos)
	if e := w("repos", err); e != nil {
		return e
	}
	_, err = writeTable(root, b.target, "files", b.files)
	if e := w("files", err); e != nil {
		return e
	}
	_, err = writeTable(root, b.target, "sources", b.sources)
	if e := w("sources", err); e != nil {
		return e
	}
	_, err = writeTable(root, b.target, "functions", b.functions)
	if e := w("functions", err); e != nil {
		return e
	}
	_, err = writeTable(root, b.target, "calls", b.calls)
	if e := w("calls", err); e != nil {
		return e
	}
	_, err = writeTable(root, b.target, "flows", b.flows)
	if e := w("flows", err); e != nil {
		return e
	}
	_, err = writeTable(root, b.target, "imports", b.imports)
	if e := w("imports", err); e != nil {
		return e
	}
	_, err = writeTable(root, b.target, "literals", b.literals)
	if e := w("literals", err); e != nil {
		return e
	}
	_, err = writeTable(root, b.target, "routes", b.routes)
	if e := w("routes", err); e != nil {
		return e
	}
	_, err = writeTable(root, b.target, "har_entries", b.entries)
	if e := w("har_entries", err); e != nil {
		return e
	}
	_, err = writeTable(root, b.target, "bodies", b.bodies)
	if e := w("bodies", err); e != nil {
		return e
	}
	_, err = writeTable(root, b.target, "hosts", b.hosts)
	if e := w("hosts", err); e != nil {
		return e
	}
	_, err = writeTable(root, b.target, "subdomains", b.subs)
	if e := w("subdomains", err); e != nil {
		return e
	}
	_, err = writeTable(root, b.target, "cloud_resources", b.resources)
	if e := w("cloud_resources", err); e != nil {
		return e
	}
	_, err = writeTable(root, b.target, "edges", b.edges)
	return w("edges", err)
}
