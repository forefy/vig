package api

import (
	"context"
	"net/http"
	"sort"

	"github.com/danielgtaylor/huma/v2"

	"github.com/forefy/vig/backend/internal/core"
)

// Security scheme names used in operation definitions.
const (
	secSession  = "session"  // cookie session for users
	secAPIKey   = "api_key"  // bearer vig_ key for users
	secOperator = "operator" // bearer job-scoped token
	secAdmin    = "admin"    // session + admin role + recent TOTP
)

var (
	userAuth     = []map[string][]string{{secSession: {}}, {secAPIKey: {}}}
	operatorAuth = []map[string][]string{{secOperator: {}}}
	adminAuth    = []map[string][]string{{secAdmin: {}}}
)

type idPath struct {
	ID string `path:"id"`
}

type body[T any] struct {
	Body T
}

type list[T any] struct {
	Items []T `json:"items"`
}

func items[T any](in []T) *body[list[T]] {
	if in == nil {
		in = []T{}
	}
	return &body[list[T]]{Body: list[T]{Items: in}}
}

func op(id, method, path, summary string, tags ...string) huma.Operation {
	return huma.Operation{OperationID: id, Method: method, Path: path, Summary: summary, Tags: tags}
}

func withAuth(o huma.Operation, sec []map[string][]string) huma.Operation {
	o.Security = sec
	return o
}

// Register wires every route group. The dev-only login route is added only
// when the process runs with VIG_ENV=dev.
func Register(api huma.API, d *Deps) {
	if d.api == nil {
		d.api = api
	}
	d.registerPublic(api)
	d.registerAuth(api)
	d.registerMe(api)
	d.registerTargets(api)
	d.registerAudits(api)
	d.registerBilling(api)
	d.registerOperator(api)
	d.registerSurface(api)
	d.registerLive(api)
	d.registerAdmin(api)
	d.registerVisuals(api)
	d.registerFindings(api)
	if d.Config.Env == "dev" {
		d.registerDevLogin(api)
	}
}

// ---------- public ----------

type adapterPath struct {
	Key string `path:"key"`
}

type adapterInfo struct {
	Key            string                  `json:"key"`
	Steps          []core.StepSpec         `json:"steps"`
	OwnershipProof core.OwnershipProofSpec `json:"ownership_proof"`
	LocationKinds  []core.LocationKind     `json:"location_kinds"`
}

// adapterKeys lists the adapters in card order: AdapterOrder first, the
// rest alphabetically.
func (d *Deps) adapterKeys() []string {
	seen := map[string]bool{}
	var out []string
	for _, k := range d.AdapterOrder {
		if _, ok := d.Adapters[k]; ok && !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	var rest []string
	for k := range d.Adapters {
		if !seen[k] {
			rest = append(rest, k)
		}
	}
	sort.Strings(rest)
	return append(out, rest...)
}

func adapterOf(a core.Adapter) adapterInfo {
	return adapterInfo{Key: a.Key(), Steps: a.IntakeSteps(), OwnershipProof: a.OwnershipProof(), LocationKinds: a.LocationKinds()}
}

type healthOut struct {
	Body struct {
		OK bool `json:"ok"`
	}
}

func (d *Deps) registerPublic(api huma.API) {
	huma.Register(api, op("healthz", http.MethodGet, "/healthz", "Liveness", "public"),
		func(ctx context.Context, _ *struct{}) (*healthOut, error) {
			out := &healthOut{}
			out.Body.OK = true
			return out, nil
		})

	huma.Register(api, op("getVersion", http.MethodGet, "/version", "Build version", "public"),
		func(ctx context.Context, _ *struct{}) (*body[VersionInfo], error) {
			return &body[VersionInfo]{Body: VersionInfo{Version: Version, Commit: Commit}}, nil
		})

	huma.Register(api, op("listPlans", http.MethodGet, "/v1/plans", "Pricing table", "public"),
		func(ctx context.Context, _ *struct{}) (*body[list[core.Plan]], error) {
			return items(core.Plans), nil
		})

	huma.Register(api, op("listAdapters", http.MethodGet, "/v1/adapters", "Target adapters and their intake steps", "public"),
		func(ctx context.Context, _ *struct{}) (*body[list[adapterInfo]], error) {
			out := []adapterInfo{}
			for _, k := range d.adapterKeys() {
				out = append(out, adapterOf(d.Adapters[k]))
			}
			return items(out), nil
		})

	huma.Register(api, op("getAdapter", http.MethodGet, "/v1/adapters/{key}", "One adapter", "public"),
		func(ctx context.Context, in *adapterPath) (*body[adapterInfo], error) {
			a, ok := d.Adapters[in.Key]
			if !ok {
				return nil, huma.Error404NotFound("no adapter " + in.Key)
			}
			return &body[adapterInfo]{Body: adapterOf(a)}, nil
		})

	huma.Register(api, op("createScan", http.MethodPost, "/v1/scans", "Anonymous pre-flight scan of a public repo", "public"), d.createScan)
	huma.Register(api, op("getScan", http.MethodGet, "/v1/scans/{token}", "Scan result", "public"), d.getScan)
	huma.Register(api, op("getPublishedReport", http.MethodGet, "/v1/reports/{slug}", "Published report", "public"), d.getPublishedReport)
	huma.Register(api, op("getPublishedReportJSON", http.MethodGet, "/v1/reports/{slug}/report.json", "Published report's findings JSON", "public"), d.getPublishedReportJSON)
	huma.Register(api, pdfOp(op("getPublishedReportPDF", http.MethodGet, "/v1/reports/{slug}/report.pdf", "Published report as a PDF", "public")), d.getPublishedReportPDF)
}
