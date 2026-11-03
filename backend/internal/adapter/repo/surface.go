package repo

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/parquet-go/parquet-go"

	"github.com/forefy/vig/backend/internal/core"
)

// The map of a repository is read off its index: the packages that hold
// request handlers, the handlers, and the sinks each one reaches. A sink
// is reached two ways. Through the flows, when the request a handler
// receives travels, variable by variable and call by call, into a call
// that runs, evaluates, queries or deserialises: that is the input
// arriving somewhere. Through the call graph, when the handler can get
// there within a few calls whether or not the input does. In the tree
// every adapter shares, a package is the program, a handler the
// instruction, a reached sink the account under it. Until an index is
// sealed the map is empty, and the mapper builds it again the moment one
// arrives.

// The rows of the zz tables the map reads. Columns the map ignores are
// left out; parquet-go fills by name.
type repoRow struct {
	Repo     string `parquet:"repo"`
	Commit   string `parquet:"commit"`
	Language string `parquet:"language"`
	Stars    int32  `parquet:"stars"`
}

type functionRow struct {
	Repo      string   `parquet:"repo"`
	Path      string   `parquet:"path"`
	FnID      string   `parquet:"fn_id"`
	Name      string   `parquet:"name"`
	Qualname  string   `parquet:"qualname"`
	Params    []string `parquet:"params,list"`
	LineStart int64    `parquet:"line_start"`
	LineEnd   int64    `parquet:"line_end"`
	Exported  bool     `parquet:"is_exported"`
	Handler   bool     `parquet:"is_handler"`
}

type callRow struct {
	Path       string `parquet:"path"`
	CallID     string `parquet:"call_id"`
	CallerFn   string `parquet:"caller_fn"`
	CalleeName string `parquet:"callee_name"`
	Line       int64  `parquet:"line"`
}

type flowRow struct {
	Src  string `parquet:"src"`
	Dst  string `parquet:"dst"`
	Kind string `parquet:"kind"`
}

type edgeRow struct {
	SrcID      string  `parquet:"src_id"`
	DstID      string  `parquet:"dst_id"`
	Rel        string  `parquet:"rel"`
	Confidence float64 `parquet:"confidence"`
}

// sinkRE is the sink pattern the Atlas questions start from: a call whose
// last name, in any case, says it runs, evaluates, queries or
// deserialises, or a known sink by its full name. The word has to end
// the callee: `db.Query` and `exec.Command` are sinks, `query.Limit`,
// `r.URL.Query().Get`, `executeCommand` and `isSystemAdmin` are not.
var sinkRE = regexp.MustCompile(`(?i)(^|[._])(exec|execsync|execfile|execute|eval|spawn|spawnsync|system|popen|query|queryrow|querycontext|queryrowcontext|raw|rawquery|deserialize|unserialize|loads)$|(^|\.)(exec\.command(context)?|subprocess\.(run|call|check_output|popen)|os\.system|yaml\.load|pickle\.load)$`)

// notSinkRE takes back what the last-name rule catches by accident:
// `r.URL.Query`, `redirectURL.Query` and `u.Query` parse a query string,
// they run nothing.
var notSinkRE = regexp.MustCompile(`(?i)(url|uri|(^|[._])u)\.query$`)

func isSink(callee string) bool {
	return sinkRE.MatchString(callee) && !notSinkRE.MatchString(callee)
}

// testPathRE marks files that hold tests, whose handlers are not the
// target's surface.
var testPathRE = regexp.MustCompile(`(^|/)(test|tests|__tests__|testdata|e2e|cypress)(/|$)|_test\.go$|\.(test|spec)\.[cm]?[jt]sx?$|(^|/)test_[^/]*\.py$|_test\.py$`)

const (
	maxHops         = 4  // calls followed from a handler through the call graph
	maxFlowSteps    = 24 // steps followed from a request through the flows
	maxSinksPerNode = 12
	maxNodes        = 6000
	maxVisited      = 20000 // flow nodes one handler's walk may touch
)

// BuildSurface implements core.SurfaceBuilder.
func (a *Adapter) BuildSurface(ctx context.Context, in core.SurfaceInput) (core.Surface, []core.SurfaceEvidence, error) {
	s := core.Surface{Adapter: Key, Nodes: []core.SurfaceNode{}, BuiltAt: time.Now().UTC()}
	if in.Index == "" {
		return s, nil, nil
	}
	if _, err := os.Stat(in.Index); err != nil {
		return s, nil, nil
	}
	repos := map[string]repoRow{}
	if err := scan(ctx, in.Index, "repos", func(r repoRow) { repos[r.Repo] = r }); err != nil {
		return s, nil, err
	}
	fns := map[string]functionRow{}
	var handlers []functionRow
	if err := scan(ctx, in.Index, "functions", func(f functionRow) {
		if f.FnID == "" {
			return
		}
		fns[f.FnID] = f
		if f.Handler && !testPathRE.MatchString(f.Path) {
			handlers = append(handlers, f)
		}
	}); err != nil {
		return s, nil, err
	}
	// Sink calls by the function that makes them, and by their id. A call
	// in a test file is not one a request reaches.
	sinks := map[string][]callRow{}
	sinkByID := map[string]callRow{}
	if err := scan(ctx, in.Index, "calls", func(c callRow) {
		if !isSink(c.CalleeName) || testPathRE.MatchString(c.Path) {
			return
		}
		sinkByID["call:"+c.CallID] = c
		if c.CallerFn == "" {
			return
		}
		for _, have := range sinks[c.CallerFn] {
			if have.CalleeName == c.CalleeName {
				return
			}
		}
		sinks[c.CallerFn] = append(sinks[c.CallerFn], c)
	}); err != nil {
		return s, nil, err
	}
	calls := map[string][]string{}
	if err := scan(ctx, in.Index, "edges", func(e edgeRow) {
		if e.Rel != "calls" || e.Confidence < 1 {
			return
		}
		src, dst := strings.TrimPrefix(e.SrcID, "fn:"), strings.TrimPrefix(e.DstID, "fn:")
		if src == "" || dst == "" || src == dst || testPathRE.MatchString(fns[dst].Path) {
			return
		}
		calls[src] = append(calls[src], dst)
	}); err != nil {
		return s, nil, err
	}
	flows := map[string][]string{}
	if err := scan(ctx, in.Index, "flows", func(f flowRow) {
		if f.Src == "" || f.Dst == "" || f.Src == f.Dst {
			return
		}
		flows[f.Src] = append(flows[f.Src], f.Dst)
	}); err != nil {
		return s, nil, err
	}

	sort.Slice(handlers, func(i, j int) bool {
		if handlers[i].Repo != handlers[j].Repo {
			return handlers[i].Repo < handlers[j].Repo
		}
		if handlers[i].Path != handlers[j].Path {
			return handlers[i].Path < handlers[j].Path
		}
		return handlers[i].LineStart < handlers[j].LineStart
	})
	isHandler := map[string]bool{}
	for _, h := range handlers {
		isHandler[h.FnID] = true
	}
	packages := map[string]*core.SurfaceNode{}
	var order []string
	var nodes []core.SurfaceNode
	var edges []core.SurfaceEdge
	for _, h := range handlers {
		if len(nodes)+len(packages) >= maxNodes {
			break
		}
		pkgID := packageID(h.Repo, h.Path)
		pkg, ok := packages[pkgID]
		if !ok {
			r := repos[h.Repo]
			facts := map[string]string{"repo": h.Repo, "dir": path.Dir(h.Path), "handlers": "0"}
			if r.Language != "" {
				facts["language"] = r.Language
			}
			if r.Commit != "" {
				facts["commit"] = r.Commit
			}
			pkg = &core.SurfaceNode{ID: pkgID, Kind: "program", Name: packageName(h.Repo, h.Path), Source: "static", Facts: facts}
			packages[pkgID] = pkg
			order = append(order, pkgID)
		}
		pkg.Facts["handlers"] = strconv.Itoa(atoi(pkg.Facts["handlers"]) + 1)
		name := h.Qualname
		if name == "" {
			name = h.Name
		}
		facts := map[string]string{"file": h.Path, "line": strconv.FormatInt(h.LineStart, 10), "repo": h.Repo}
		if h.LineEnd > 0 {
			facts["line_end"] = strconv.FormatInt(h.LineEnd, 10)
		}
		if h.Exported {
			facts["exported"] = "true"
		}
		hid := "ix:" + h.FnID
		// The request's own travel first; the call graph where it tells
		// nothing.
		var reached []reach
		if req := requestParam(h.Params); req != "" {
			facts["request"] = req
			reached = taint("var:"+h.FnID+"#"+req, flows, sinkByID)
		}
		if len(reached) > 0 {
			facts["tainted"] = strconv.Itoa(len(reached))
		} else {
			reached = walk(h.FnID, calls, sinks)
		}
		if len(reached) > 0 {
			facts["sinks"] = strconv.Itoa(len(reached))
		}
		nodes = append(nodes, core.SurfaceNode{ID: hid, Parent: pkgID, Kind: "instruction", Name: name, Source: "static", Facts: facts})
		for _, r := range reached {
			if len(nodes) >= maxNodes {
				break
			}
			f := fns[r.fn]
			sf := map[string]string{"callee": r.call.CalleeName, "file": r.call.Path, "line": strconv.FormatInt(r.call.Line, 10), "hops": strconv.Itoa(r.hops), "via": r.via}
			if f.Qualname != "" {
				sf["in"] = f.Qualname
			}
			nodes = append(nodes, core.SurfaceNode{ID: "sink:" + h.FnID + "#" + r.fn + "#" + r.call.CalleeName, Parent: hid, Kind: "account", Name: r.call.CalleeName, Source: "static", Facts: sf})
		}
		for _, dst := range calls[h.FnID] {
			if isHandler[dst] && dst != h.FnID {
				edges = append(edges, core.SurfaceEdge{From: hid, To: "ix:" + dst, Kind: "calls", Source: "static"})
			}
		}
	}
	for _, id := range order {
		s.Nodes = append(s.Nodes, *packages[id])
	}
	s.Nodes = append(s.Nodes, nodes...)
	s.Edges = edges
	return s, nil, nil
}

// A sink reached from a handler: the function that makes the call, the
// call, how many calls away it is, and whether the request's data got
// there (taint) or the handler merely can (calls).
type reach struct {
	fn   string
	call callRow
	hops int
	via  string
}

// requestParam names the parameter a handler receives the request in,
// by the type or the name each framework gives it.
func requestParam(params []string) string {
	for _, p := range params {
		t := strings.TrimSpace(p)
		name := strings.TrimLeft(t, "*&")
		if i := strings.IndexAny(name, " :"); i >= 0 {
			name = name[:i]
		}
		lower := strings.ToLower(name)
		switch {
		case strings.Contains(t, "http.Request"), strings.Contains(t, "gin.Context"), strings.Contains(t, "echo.Context"), strings.Contains(t, "fiber.Ctx"), strings.Contains(t, "fasthttp.RequestCtx"):
			return name
		case lower == "req" || lower == "request", lower == "ctx" && !strings.Contains(t, " "):
			return name
		}
	}
	return ""
}

// taint follows the flows from one value until they reach sink calls:
// through assignments, into arguments and receivers, across resolved
// calls into parameters and back out of returns.
func taint(start string, flows map[string][]string, sinkByID map[string]callRow) []reach {
	type item struct {
		id   string
		hops int
		step int
	}
	seen := map[string]bool{start: true}
	queue := []item{{start, 0, 0}}
	var out []reach
	found := map[string]bool{}
	for len(queue) > 0 && len(seen) < maxVisited {
		cur := queue[0]
		queue = queue[1:]
		for _, next := range flows[cur.id] {
			if seen[next] {
				continue
			}
			seen[next] = true
			hops := cur.hops
			if strings.HasPrefix(next, "call:") {
				if c, ok := sinkByID[next]; ok && !found[next] {
					found[next] = true
					out = append(out, reach{fn: c.CallerFn, call: c, hops: hops, via: "taint"})
					if len(out) >= maxSinksPerNode {
						return out
					}
				}
				hops++
			}
			if cur.step+1 < maxFlowSteps {
				queue = append(queue, item{next, hops, cur.step + 1})
			}
		}
	}
	return out
}

// walk follows the call graph from a handler for a few hops and returns
// the sinks it reaches, nearest first.
func walk(start string, calls map[string][]string, sinks map[string][]callRow) []reach {
	seen := map[string]int{start: 0}
	frontier := []string{start}
	var out []reach
	for hops := 0; hops <= maxHops && len(frontier) > 0; hops++ {
		var next []string
		for _, fn := range frontier {
			for _, c := range sinks[fn] {
				if len(out) >= maxSinksPerNode {
					return out
				}
				out = append(out, reach{fn: fn, call: c, hops: hops, via: "calls"})
			}
			if hops == maxHops {
				continue
			}
			for _, dst := range calls[fn] {
				if _, ok := seen[dst]; ok {
					continue
				}
				seen[dst] = hops + 1
				next = append(next, dst)
			}
		}
		frontier = next
	}
	return out
}

func packageID(repo, file string) string { return "p:" + repo + "/" + path.Dir(file) }

// packageName is the repository's name with the directory of the file:
// `mattermost-plugin-github/server`.
func packageName(repo, file string) string {
	short := repo[strings.LastIndex(repo, "/")+1:]
	dir := path.Dir(file)
	if dir == "." || dir == "" {
		return short
	}
	return short + "/" + dir
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

// scan reads every part of one table under the index directory, in
// batches, and hands each row to fn.
func scan[T any](ctx context.Context, dir, table string, fn func(T)) error {
	parts, err := filepath.Glob(filepath.Join(dir, table, "*.parquet"))
	if err != nil {
		return err
	}
	for _, p := range parts {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := scanFile(p, fn); err != nil {
			return fmt.Errorf("index %s: %w", filepath.Base(p), err)
		}
	}
	return nil
}

func scanFile[T any](p string, fn func(T)) error {
	f, err := os.Open(p)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	pf, err := parquet.OpenFile(f, st.Size())
	if err != nil {
		return err
	}
	r := parquet.NewGenericReader[T](pf)
	defer r.Close()
	buf := make([]T, 1024)
	for {
		n, err := r.Read(buf)
		for _, row := range buf[:n] {
			fn(row)
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}
