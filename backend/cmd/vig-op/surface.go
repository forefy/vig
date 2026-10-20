package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/forefy/vig/backend/internal/adapter/solana"
	"github.com/forefy/vig/backend/internal/api"
	"github.com/forefy/vig/backend/internal/chain"
	"github.com/forefy/vig/backend/internal/config"
	"github.com/forefy/vig/backend/internal/core"
)

// cmdSurface prints the job's map: every node with its attention, how often
// it was observed, and the findings on it.
func cmdSurface(w *workspace) error {
	job, err := w.job()
	if err != nil {
		return err
	}
	var view api.SurfaceView
	if err := w.call(http.MethodGet, "/v1/op/jobs/"+job.Audit.ID+"/surface", nil, &view); err != nil {
		return err
	}
	if view.State != "ready" || view.Surface == nil {
		fmt.Printf("surface    %s %s\n", view.State, view.Error)
		return nil
	}
	s := view.Surface
	children := map[string][]core.SurfaceNode{}
	var roots []core.SurfaceNode
	for _, n := range s.Nodes {
		if n.Parent == "" {
			roots = append(roots, n)
		} else {
			children[n.Parent] = append(children[n.Parent], n)
		}
	}
	var walk func(n core.SurfaceNode, depth int)
	walk = func(n core.SurfaceNode, depth int) {
		findings := ""
		if len(n.Findings) > 0 {
			findings = fmt.Sprintf(" findings %v", n.Findings)
		}
		fmt.Printf("%-9s %-4d %s%s  [%s]%s\n", n.Attention, n.Observed, strings.Repeat("  ", depth), n.Name, n.ID, findings)
		kids := children[n.ID]
		sort.Slice(kids, func(i, j int) bool { return kids[i].Observed > kids[j].Observed })
		for _, k := range kids {
			walk(k, depth+1)
		}
	}
	for _, r := range roots {
		walk(r, 0)
	}
	if s.Sampling != nil {
		fmt.Printf("sampled    %d transactions from %d signatures\n", s.Sampling.Transactions, s.Sampling.Signatures)
	}
	return nil
}

// cmdReview marks nodes as looked at, with an optional note.
func cmdReview(w *workspace, args []string) error {
	fs := flag.NewFlagSet("review", flag.ContinueOnError)
	note := fs.String("note", "", "what was checked")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return errors.New("usage: vig-op review [--note text] <node-id>...")
	}
	job, err := w.job()
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	var items []core.SurfaceEvidence
	for _, id := range fs.Args() {
		summary := map[string]any{}
		if *note != "" {
			summary["note"] = *note
		}
		items = append(items, core.SurfaceEvidence{NodeID: id, Kind: "review", At: &now, Summary: summary})
	}
	return postEvidence(w, job, items)
}

// cmdReplay re-executes a finalized transaction against current state with
// signatures skipped and the blockhash replaced, decodes accounts before and
// after through the IDL, and records it all on the instruction it hit.
// Point VIG_RPC at a surfnet to replay against curated state instead.
func cmdReplay(w *workspace, args []string) error {
	fs := flag.NewFlagSet("replay", flag.ContinueOnError)
	tag := fs.String("tag", "", "label for this replay, e.g. a finding ordinal")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: vig-op replay [--tag t] <signature>")
	}
	return replayAndPost(w, fs.Arg(0), *tag, nil, nil, "")
}

// cmdWhatIf replays with the world changed first: accounts overridden, the
// clock moved, an argument patched. Overrides need a surfnet on VIG_RPC;
// an argument patch works anywhere.
func cmdWhatIf(w *workspace, args []string) error {
	fs := flag.NewFlagSet("whatif", flag.ContinueOnError)
	tag := fs.String("tag", "", "label for this replay")
	var sets multiFlag
	fs.Var(&sets, "set-account", "pubkey=<json account update>, applied with surfnet_setAccount (repeatable)")
	travel := fs.String("time", "", "move the clock forward, e.g. 36h or 2d (surfnet_timeTravel)")
	arg := fs.String("arg", "", "patch a scalar instruction argument: name=value")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: vig-op whatif [--set-account k=json]... [--time 36h] [--arg name=value] [--tag t] <signature>")
	}
	rpc := rpcURL()
	var overrides []string
	if len(sets) > 0 || *travel != "" {
		info, err := surfnetInfo(rpc)
		if err != nil {
			return fmt.Errorf("overrides need a surfnet on VIG_RPC (%s): %w", rpc, err)
		}
		fmt.Printf("surfnet    %s\n", info)
		c := chain.New([]string{rpc})
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		for _, kv := range sets {
			k, v, ok := strings.Cut(kv, "=")
			if !ok {
				return fmt.Errorf("--set-account %q: want pubkey=json", kv)
			}
			var update map[string]any
			if err := json.Unmarshal([]byte(v), &update); err != nil {
				return fmt.Errorf("--set-account %s: %w", k, err)
			}
			if _, err := c.Call(ctx, "surfnet_setAccount", k, update); err != nil {
				return err
			}
			overrides = append(overrides, k)
			fmt.Printf("override   %s\n", k)
		}
		if *travel != "" {
			d, err := parseDuration(*travel)
			if err != nil {
				return err
			}
			if _, err := c.Call(ctx, "surfnet_timeTravel", map[string]any{"absoluteTimestamp": time.Now().Add(d).UnixMilli()}); err != nil {
				return err
			}
			fmt.Printf("clock      +%s\n", d)
		}
	}
	var patch *argPatch
	if *arg != "" {
		name, value, ok := strings.Cut(*arg, "=")
		if !ok {
			return errors.New("--arg wants name=value")
		}
		patch = &argPatch{Name: name, Value: value}
	}
	return replayAndPost(w, fs.Arg(0), *tag, patch, overrides, *travel)
}

type argPatch struct{ Name, Value string }

// replayAndPost replays sig and posts the result as evidence. Overrides and
// travel are what the what-if already applied on the surfnet; they are
// recorded on the replay so the row says what it was run against.
func replayAndPost(w *workspace, sig, tag string, patch *argPatch, overrides []string, travel string) error {
	job, err := w.job()
	if err != nil {
		return err
	}
	pid := firstProgram(job)
	if pid == "" {
		return errors.New("job has no program")
	}
	c := chain.New([]string{rpcURL()})
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	// The map's names for this program, so a replay lands on the node the
	// map already has even when the program publishes no IDL.
	known := map[string]string{}
	var view api.SurfaceView
	if err := w.call(http.MethodGet, "/v1/op/jobs/"+job.Audit.ID+"/surface", nil, &view); err == nil && view.Surface != nil {
		for _, n := range view.Surface.Nodes {
			if n.Kind == "instruction" && n.Facts["program"] == pid {
				known[n.Name] = n.Facts["discriminator"]
			}
		}
	}
	opts := []solana.ProfileOption{solana.WithKnown(known)}
	if patch != nil {
		opts = append(opts, solana.WithArg(patch.Name, patch.Value))
	}
	if len(overrides) > 0 {
		opts = append(opts, solana.WithOverrides(overrides))
	}
	if travel != "" {
		opts = append(opts, solana.WithTimeTravel(travel))
	}
	r, err := solana.ProfileWith(ctx, c, pid, sig, opts...)
	if err != nil {
		return err
	}
	r.Tag = tag
	fmt.Printf("replay     %s  %s  units %d  %s\n", sig, r.Instruction, r.Units, r.Outcome)
	if r.Patch != nil {
		fmt.Printf("  patch    %s = %s\n", r.Patch.Name, r.Patch.Value)
	}
	if len(r.Overrides) > 0 {
		fmt.Printf("  overrides %s\n", strings.Join(r.Overrides, " "))
	}
	if r.TimeTravel != "" {
		fmt.Printf("  time     %s\n", r.TimeTravel)
	}
	for k, v := range r.Args {
		fmt.Printf("  arg      %s = %s\n", k, v)
	}
	for _, a := range r.Accounts {
		mark := " "
		if len(a.Changed) > 0 {
			mark = "*"
		}
		name := a.Name
		if name == "" {
			name = short(a.Pubkey)
		}
		fmt.Printf("  %s %-28s %-8s %s\n", mark, name, a.Type, strings.Join(a.Changed, ","))
	}
	for _, l := range r.Logs {
		fmt.Println("  " + l)
	}
	now := time.Now().UTC()
	return postEvidence(w, job, []core.SurfaceEvidence{r.Evidence(pid, "operator", &now)})
}

func rpcURL() string {
	if v := os.Getenv("VIG_RPC"); v != "" {
		return v
	}
	return config.PublicRPC
}

// surfnetInfo reports whether VIG_RPC is a surfnet, by asking it.
func surfnetInfo(rpc string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	raw, err := chain.New([]string{rpc}).Call(ctx, "surfnet_getSurfnetInfo")
	if err != nil {
		return "", err
	}
	if len(raw) > 80 {
		raw = raw[:80]
	}
	return string(raw), nil
}

func parseDuration(s string) (time.Duration, error) {
	if strings.HasSuffix(s, "d") {
		var n int
		if _, err := fmt.Sscanf(s, "%dd", &n); err != nil {
			return 0, fmt.Errorf("bad duration %q", s)
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	return time.ParseDuration(s)
}

// cmdHAR turns a browser or proxy capture into routes and requests on the
// map: one node per path shape on a host, with the methods seen as a fact,
// one child per parameter, one evidence row per request, and a nav edge
// from the page that referred each request. This is how a web target
// gets its traffic.
func cmdHAR(w *workspace, args []string) error {
	fs := flag.NewFlagSet("har", flag.ContinueOnError)
	host := fs.String("host", "", "only requests to this host (default: every host)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: vig-op har [--host h] <capture.har>")
	}
	job, err := w.job()
	if err != nil {
		return err
	}
	b, err := os.ReadFile(fs.Arg(0))
	if err != nil {
		return err
	}
	var har struct {
		Log struct {
			Entries []struct {
				StartedDateTime string  `json:"startedDateTime"`
				Time            float64 `json:"time"`
				Request         struct {
					Method  string `json:"method"`
					URL     string `json:"url"`
					Headers []struct {
						Name  string `json:"name"`
						Value string `json:"value"`
					} `json:"headers"`
					QueryString []struct {
						Name string `json:"name"`
					} `json:"queryString"`
					PostData *struct {
						MimeType string `json:"mimeType"`
						Text     string `json:"text"`
					} `json:"postData"`
				} `json:"request"`
				Response struct {
					Status  int `json:"status"`
					Content struct {
						MimeType string `json:"mimeType"`
						Size     int    `json:"size"`
					} `json:"content"`
				} `json:"response"`
			} `json:"entries"`
		} `json:"log"`
	}
	if err := json.Unmarshal(b, &har); err != nil {
		return fmt.Errorf("%s: %w", fs.Arg(0), err)
	}
	nodes := map[string]core.SurfaceNode{}
	var order []string
	add := func(n core.SurfaceNode) {
		if _, ok := nodes[n.ID]; !ok {
			nodes[n.ID] = n
			order = append(order, n.ID)
		}
	}
	methods := map[string]map[string]bool{}
	nav := map[[2]string]int{}
	var navOrder [][2]string
	// place puts a URL's host and route on the map and returns the route id.
	place := func(u *url.URL) string {
		h := strings.ToLower(u.Host)
		add(core.SurfaceNode{ID: core.WebHostID(h), Kind: "host", Name: h, Facts: map[string]string{"host": h}})
		shape := core.RouteShape(u.Path)
		id := core.WebRouteID(h, shape)
		add(core.SurfaceNode{ID: id, Parent: core.WebHostID(h), Kind: "route", Name: shape, Facts: map[string]string{"host": h, "path": shape}})
		return id
	}
	var evidence []core.SurfaceEvidence
	for _, e := range har.Log.Entries {
		u, err := url.Parse(e.Request.URL)
		if err != nil || u.Host == "" {
			continue
		}
		if *host != "" && !strings.EqualFold(u.Host, *host) {
			continue
		}
		if skipAsset(u.Path, e.Response.Content.MimeType) {
			continue
		}
		routeID := place(u)
		if methods[routeID] == nil {
			methods[routeID] = map[string]bool{}
		}
		methods[routeID][e.Request.Method] = true
		for _, q := range e.Request.QueryString {
			add(core.SurfaceNode{ID: core.WebParamID(routeID, "query", q.Name), Parent: routeID, Kind: "param", Name: q.Name, Facts: map[string]string{"in": "query"}})
		}
		if e.Request.PostData != nil && strings.Contains(e.Request.PostData.MimeType, "json") {
			var body map[string]any
			if json.Unmarshal([]byte(e.Request.PostData.Text), &body) == nil {
				for k := range body {
					add(core.SurfaceNode{ID: core.WebParamID(routeID, "body", k), Parent: routeID, Kind: "param", Name: k, Facts: map[string]string{"in": "body"}})
				}
			}
		}
		// The page that led here is a route too, and the step between
		// them is how the site is walked.
		for _, h := range e.Request.Headers {
			if !strings.EqualFold(h.Name, "referer") {
				continue
			}
			ref, err := url.Parse(h.Value)
			if err != nil || ref.Host == "" || (*host != "" && !strings.EqualFold(ref.Host, *host)) {
				continue
			}
			if from := place(ref); from != routeID {
				k := [2]string{from, routeID}
				if nav[k] == 0 {
					navOrder = append(navOrder, k)
				}
				nav[k]++
			}
		}
		var at *time.Time
		if t, err := time.Parse(time.RFC3339Nano, e.StartedDateTime); err == nil {
			t = t.UTC()
			at = &t
		}
		ref := e.Request.Method + " " + u.String()
		evidence = append(evidence, core.SurfaceEvidence{NodeID: routeID, Kind: "request", Ref: ref, At: at,
			Summary: map[string]any{"method": e.Request.Method, "path": u.RequestURI(), "status": e.Response.Status, "ms": int(e.Time), "mime": e.Response.Content.MimeType, "bytes": e.Response.Content.Size}})
	}
	if len(order) == 0 {
		return errors.New("no requests to map")
	}
	var list []core.SurfaceNode
	for _, id := range order {
		n := nodes[id]
		// The server stamps the source; the document still has to validate.
		n.Source = "operator"
		n.Attention = core.AttentionUnseen
		if ms := methods[id]; len(ms) > 0 {
			names := make([]string, 0, len(ms))
			for m := range ms {
				names = append(names, m)
			}
			sort.Strings(names)
			n.Facts["methods"] = strings.Join(names, ", ")
		}
		list = append(list, n)
	}
	var edges []core.SurfaceEdge
	for _, k := range navOrder {
		edges = append(edges, core.SurfaceEdge{From: k[0], To: k[1], Kind: "nav", Count: nav[k], Source: "traffic"})
	}
	var out struct {
		Added int `json:"added"`
	}
	body := map[string]any{"nodes": list, "edges": edges}
	if err := w.call(http.MethodPost, "/v1/op/jobs/"+job.Audit.ID+"/surface/nodes", body, &out); err != nil {
		return err
	}
	fmt.Printf("nodes      +%d of %d routes and parameters, %d nav edges\n", out.Added, len(list), len(edges))
	return postEvidence(w, job, evidence)
}

func postEvidence(w *workspace, job api.Job, items []core.SurfaceEvidence) error {
	total := 0
	for len(items) > 0 {
		n := len(items)
		if n > 500 {
			n = 500
		}
		var out struct {
			Added int `json:"added"`
		}
		if err := w.call(http.MethodPost, "/v1/op/jobs/"+job.Audit.ID+"/surface/evidence", map[string]any{"items": items[:n]}, &out); err != nil {
			return err
		}
		total += out.Added
		items = items[n:]
	}
	fmt.Printf("evidence   +%d\n", total)
	return nil
}

var assetExt = regexp.MustCompile(`\.(js|css|png|jpg|jpeg|gif|svg|ico|woff2?|ttf|map|webp|mp4)$`)

func skipAsset(path, mime string) bool {
	if assetExt.MatchString(strings.ToLower(path)) {
		return true
	}
	return strings.HasPrefix(mime, "image/") || strings.HasPrefix(mime, "font/") || strings.HasPrefix(mime, "text/css")
}
