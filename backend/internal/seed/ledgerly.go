package seed

import (
	"strings"
	"time"

	"github.com/forefy/vig/backend/internal/core"
)

// The web demo: Ledgerly, an invented invoicing app with an invented
// repository, audited as a site with a source snapshot. The findings are the
// ones a web audit finds week after week, in the shape the web adapter
// gives them: a live endpoint, a route, a method, and who can reach it.

const (
	ledgerlyRepo = "vig-demo/ledgerly"
	ledgerlyApp  = "https://app.ledgerly.example"
	ledgerlyAPI  = "https://api.ledgerly.example"
	// LedgerlySlug is the public page of the delivered web report.
	LedgerlySlug = "ledgerly-invoice-idor"
)

var ledgerlyHosts = []string{"app.ledgerly.example", "api.ledgerly.example"}

func init() {
	for file, code := range ledgerlyCode {
		demoCode[file] = code
	}
}

// ledgerlyCode is the code at each located line, in the shape a capture
// would have had; the fixes in ledgerlyFixes patch against it.
var ledgerlyCode = map[string]core.Snippet{
	"src/routes/invoices.ts": {StartLine: 37, Code: `router.get('/api/v1/invoices/:id', requireSession, async (req, res) => {
  const id = Number(req.params.id)
  if (!Number.isInteger(id)) return res.status(400).json({ error: 'bad id' })

  const invoice = await prisma.invoice.findUnique({ where: { id } })
  if (!invoice) return res.status(404).json({ error: 'not found' })

  await audit.record(req.user.id, 'invoice.read', { id })
  res.json(invoice)
})`},
	"src/routes/me.ts": {StartLine: 24, Code: `router.patch('/api/v1/me', requireSession, async (req, res) => {
  const data = req.body as Partial<User>

  // the profile form sends name and timezone
  const user = await prisma.user.update({ where: { id: req.user.id }, data })
  res.json(user)
})`},
	"src/routes/search.ts": {StartLine: 14, Code: `router.get('/status/search', async (req, res) => {
  const q = String(req.query.q ?? '')
  if (q.length < 3) return res.json({ rows: [] })

  // public status page: no session
  const sql = ` + "`SELECT number, paid_at FROM invoices WHERE number LIKE '%${q}%'`" + `

  const rows = await prisma.$queryRawUnsafe(sql)
  res.json({ rows })
})`},
	"src/routes/auth.ts": {StartLine: 18, Code: `router.post('/api/v1/auth/login', async (req, res) => {
  const { email, password } = req.body
  const user = await prisma.user.findUnique({ where: { email } })
  if (!user) return res.status(401).end()
  const ok = await argon2.verify(user.hash, password)
  if (!ok) return res.status(401).end()
  res.json({ token: await session.issue(user) })
})`},
	"src/middleware/errors.ts": {StartLine: 7, Code: `export function errors(err: Error, req: Request, res: Response, _next: NextFunction) {
  const status = (err as HttpError).status ?? 500
  log.error({ err, path: req.path })

  res.status(status).json({ error: err.message, stack: err.stack })
}`},
}

var ledgerlyFixes = map[string]recommendation{
	key("src/routes/invoices.ts", 41):   {Code: "  const invoice = await prisma.invoice.findFirst({ where: { id, orgId: req.user.orgId } })"},
	key("src/routes/me.ts", 28):         {Code: "  const { name, timezone } = req.body\n  const user = await prisma.user.update({ where: { id: req.user.id }, data: { name, timezone } })"},
	key("src/routes/search.ts", 19):     {Code: "  const rows = await prisma.$queryRaw`SELECT number, paid_at FROM invoices WHERE number LIKE ${'%' + q + '%'}`"},
	key("src/routes/auth.ts", 18):       {Code: "const loginLimit = rateLimit({ windowMs: 15 * 60_000, limit: 10, keyGenerator: (req) => req.body.email })\nrouter.post('/api/v1/auth/login', loginLimit, async (req, res) => {"},
	key("src/middleware/errors.ts", 11): {Code: "  res.status(status).json({ error: status < 500 ? err.message : 'internal error', id: req.id })"},
}

// ledgerlyReport is the full audit of the site with its source snapshot.
func ledgerlyReport(commit string) core.FindingsDocument {
	d := doc(pin(ledgerlyRepo, commit, ""), []core.Finding{
		{Severity: core.SeverityCritical, Title: "Invoice lookup skips the organisation check", ExploitableBy: core.ExploitableAnyUser,
			Evidence:  &core.Evidence{Kind: core.EvidenceLive, Tool: "burp", Artifact: "repeater: id 10234 → 10233 answered 200 with another organisation's invoice"},
			Location:  core.Location{File: "src/routes/invoices.ts", LineStart: 41, LineEnd: 41, Instruction: "GET /api/v1/invoices/{id}", URL: ledgerlyAPI + "/api/v1/invoices/10234"},
			SitesKind: core.SitesFlow,
			Sites: []core.Site{
				{Location: core.Location{File: "src/routes/invoices.ts", LineStart: 41, LineEnd: 41, URL: ledgerlyAPI + "/api/v1/invoices/10234"}, Note: "looked up by id alone"},
				{Location: core.Location{File: "src/routes/invoices.ts", LineStart: 45, LineEnd: 45, URL: ledgerlyAPI + "/api/v1/invoices/10234"}, Note: "returned in full"},
			},
			Description: "The handler loads the invoice by the id in the path and never compares its organisation with the caller's. Any signed-in user who changes the id reads another customer's invoice.",
			NormalUse:   "A member opens an invoice from their own list. The id in the link came from the list the API gave them, so it is always one of theirs.",
			Expected:    "The lookup is scoped to the caller's organisation: an id that belongs to someone else answers 404, exactly like an id that does not exist.",
			Impact:      "Every invoice on the platform is readable with a free account.\n\n- ids are sequential, so a loop over them reads every customer's billing\n- amounts, addresses, line items and VAT numbers leave the platform",
			ExploitPath: "Sign in with any account, open an invoice, and replace the id in the request with a neighbouring number; the response carries the other organisation's invoice.",
			Fix:         "- Scope the lookup to the caller's organisation with `findFirst` on `id` and `orgId`, and let a miss answer 404.",
			References:  []string{"https://owasp.org/API-Security/editions/2023/en/0xa1-broken-object-level-authorization/"}},
		{Severity: core.SeverityHigh, Title: "Role is writable through the profile update", ExploitableBy: core.ExploitableAnyUser,
			Evidence:  &core.Evidence{Kind: core.EvidenceStatic},
			Location:  core.Location{File: "src/routes/me.ts", LineStart: 28, LineEnd: 28, Instruction: "PATCH /api/v1/me", URL: ledgerlyAPI + "/api/v1/me"},
			SitesKind: core.SitesOccurrences,
			Sites: []core.Site{
				{Location: core.Location{File: "src/routes/me.ts", LineStart: 28, LineEnd: 28, URL: ledgerlyAPI + "/api/v1/me"}, Note: "body spread into the update"},
			},
			Description: "The request body is spread straight into the Prisma update. A member who adds `role: \"owner\"` to the JSON becomes an owner of the organisation.",
			NormalUse:   "A member edits their display name and timezone from the profile page; the form sends only those two fields.",
			Expected:    "The update writes the fields the profile form owns and nothing else; a member's role changes only through the members page, by an owner.",
			Impact:      "Any member can make themselves an owner.\n\n- owners export every invoice, change billing and remove other members\n- the audit log records the change as a routine profile edit",
			ExploitPath: "Send `PATCH /api/v1/me` with `{\"name\":\"x\",\"role\":\"owner\"}` from a member session; the next request carries owner permissions.",
			Fix:         "- Pick the writable fields (`name`, `timezone`) out of the body and ignore the rest.",
			References:  []string{"https://owasp.org/API-Security/editions/2023/en/0xa3-broken-object-property-level-authorization/"}},
		{Severity: core.SeverityCritical, Title: "Search term concatenated into SQL on the public status page", ExploitableBy: core.ExploitableAnyone,
			Evidence:  &core.Evidence{Kind: core.EvidenceLive, Tool: "sqlmap", Artifact: "boolean-based blind on q, confirmed on 2 of 2 runs"},
			Location:  core.Location{File: "src/routes/search.ts", LineStart: 19, LineEnd: 21, Instruction: "GET /status/search", URL: ledgerlyApp + "/status/search?q=acme"},
			SitesKind: core.SitesFlow,
			Sites: []core.Site{
				{Location: core.Location{File: "src/routes/search.ts", LineStart: 19, LineEnd: 19, URL: ledgerlyApp + "/status/search?q=acme"}, Note: "term joined into the statement"},
				{Location: core.Location{File: "src/routes/search.ts", LineStart: 21, LineEnd: 21, URL: ledgerlyApp + "/status/search?q=acme"}, Note: "sent raw"},
			},
			Description: "The `q` parameter is joined into a raw SQL string. The page needs no account, so anyone on the internet can run statements against the production database.",
			NormalUse:   "A customer's customer opens the public status page and searches for an invoice number to see whether it was paid.",
			Expected:    "The term travels as a bound parameter; the database only ever sees it as a value, never as part of the statement.",
			Impact:      "The whole database is readable from the public internet.\n\n- every table, password hashes and API keys included, through a blind injection\n- a write is one statement away where the database role allows it",
			ExploitPath: "Request `/status/search?q=x' OR 1=1--` and compare the response with `q=x' AND 1=2--`; the difference shows the term reaches the statement.",
			Fix:         "- Use the tagged template `prisma.$queryRaw` so the term is bound as a parameter.",
			References:  []string{"https://owasp.org/Top10/A03_2021-Injection/"}},
		{Severity: core.SeverityMedium, Title: "Login accepts unlimited attempts", ExploitableBy: core.ExploitableAnyone,
			Evidence:  &core.Evidence{Kind: core.EvidenceLive, Tool: "ffuf", Artifact: "5,000 passwords in 92 s, none refused"},
			Location:  core.Location{File: "src/routes/auth.ts", LineStart: 18, LineEnd: 18, Instruction: "POST /api/v1/auth/login", URL: ledgerlyAPI + "/api/v1/auth/login"},
			SitesKind: core.SitesFlow,
			Sites: []core.Site{
				{Location: core.Location{File: "src/routes/auth.ts", LineStart: 18, LineEnd: 18, URL: ledgerlyAPI + "/api/v1/auth/login"}, Note: "answers every attempt"},
			},
			Description: "The login route answers as fast as it can, as many times as asked. A list of leaked passwords can be tried against every account in minutes.",
			NormalUse:   "A staff member signs in with email and password a few times a day.",
			Expected:    "After a handful of failures for one account the route slows down or refuses, and the attempt is logged.",
			Impact:      "Credential stuffing works against every account that is not behind SSO.\n\n- one reused password is one account taken over",
			ExploitPath: "Replay the login request with a password list and count the 200s.",
			Fix:         "- Put `express-rate-limit` in front of the route: ten attempts per account in fifteen minutes, then 429.\n- Alert on bursts of failures from one source address.",
			References:  []string{"https://owasp.org/API-Security/editions/2023/en/0xa4-unrestricted-resource-consumption/"}},
		{Severity: core.SeverityInfo, Title: "Error responses carry the stack trace", ExploitableBy: core.ExploitableNone,
			Evidence:    &core.Evidence{Kind: core.EvidenceStatic},
			Location:    core.Location{File: "src/middleware/errors.ts", LineStart: 11, LineEnd: 11, Instruction: "GET /api/v1/invoices/{id}", URL: ledgerlyAPI + "/api/v1/invoices/abc"},
			Description: "The error handler serialises `err.stack` into the JSON body. Paths, package versions and query fragments leave with every unhandled error.",
			Fix:         "- Answer with a generic message and an error id; keep the stack in the server log.",
			References:  nil},
	})
	applyFixes(d.Findings, ledgerlyFixes)
	d.Report.Methods = &core.Methods{Static: true, Live: true}
	return d
}

// ledgerlySurface is the site's map as a capture would have left it: both
// hosts, the routes a week of traffic reached, the parameters each takes,
// and the requests that lit them. Deterministic, so two seeds agree.
func ledgerlySurface(findings []core.Finding, now time.Time) (core.Surface, []core.SurfaceEvidence) {
	app, api := ledgerlyHosts[0], ledgerlyHosts[1]
	type route struct {
		host, path string
		methods    []string
		query      []string
		body       []string
		requests   int
		failed     int
		ms         int
	}
	routes := []route{
		{app, "/", []string{"GET"}, nil, nil, 1240, 0, 38},
		{app, "/login", []string{"GET"}, nil, nil, 610, 0, 41},
		{app, "/signup", []string{"GET"}, nil, nil, 96, 0, 44},
		{app, "/reset", []string{"GET"}, nil, nil, 31, 0, 40},
		{app, "/reset/{id}", []string{"GET"}, nil, nil, 27, 3, 52},
		{app, "/status", []string{"GET"}, nil, nil, 388, 0, 35},
		{app, "/status/search", []string{"GET"}, []string{"q"}, nil, 214, 12, 120},
		{app, "/app", []string{"GET"}, nil, nil, 980, 0, 61},
		{app, "/app/invoices", []string{"GET"}, []string{"status", "page"}, nil, 740, 0, 73},
		{app, "/app/invoices/{id}", []string{"GET"}, nil, nil, 702, 1, 70},
		{app, "/app/settings", []string{"GET"}, nil, nil, 118, 0, 58},
		{app, "/app/members", []string{"GET"}, nil, nil, 64, 0, 66},
		{api, "/api/v1/auth/login", []string{"POST"}, nil, []string{"email", "password"}, 5612, 5390, 210},
		{api, "/api/v1/auth/logout", []string{"POST"}, nil, nil, 402, 0, 12},
		{api, "/api/v1/me", []string{"GET", "PATCH"}, nil, []string{"name", "timezone"}, 1130, 0, 18},
		{api, "/api/v1/invoices", []string{"GET", "POST"}, []string{"status", "page"}, []string{"customer_id", "lines", "due_at"}, 1402, 4, 64},
		{api, "/api/v1/invoices/{id}", []string{"GET", "PATCH"}, nil, []string{"lines", "due_at"}, 2211, 9, 31},
		{api, "/api/v1/invoices/{id}/pdf", []string{"GET"}, nil, nil, 318, 0, 410},
		{api, "/api/v1/customers", []string{"GET", "POST"}, []string{"page"}, []string{"name", "email", "vat"}, 522, 0, 44},
		{api, "/api/v1/customers/{id}", []string{"GET", "PATCH"}, nil, []string{"name", "email", "vat"}, 377, 2, 29},
		{api, "/api/v1/members", []string{"GET", "POST"}, nil, []string{"email", "role"}, 143, 0, 26},
		{api, "/api/v1/members/{id}", []string{"PATCH", "DELETE"}, nil, []string{"role"}, 38, 0, 24},
		{api, "/api/v1/webhooks", []string{"GET", "POST"}, nil, []string{"url", "events"}, 57, 0, 33},
		{api, "/api/v1/export", []string{"GET"}, []string{"from", "to", "format"}, nil, 21, 0, 1900},
	}
	s := core.Surface{Adapter: "webapp", BuiltAt: now, Sampling: &core.Sampling{Transactions: 0}}
	facts := core.NewWebFacts()
	for _, h := range ledgerlyHosts {
		s.Nodes = append(s.Nodes, core.SurfaceNode{ID: core.WebHostID(h), Kind: "host", Name: h, Source: "traffic", Facts: map[string]string{"host": h}})
	}
	var ev []core.SurfaceEvidence
	for ri, r := range routes {
		id := core.WebRouteID(r.host, r.path)
		s.Nodes = append(s.Nodes, core.SurfaceNode{ID: id, Parent: core.WebHostID(r.host), Kind: "route", Name: r.path, Source: "traffic",
			Facts: map[string]string{"host": r.host, "path": r.path, "methods": strings.Join(r.methods, " ")}})
		for _, q := range r.query {
			s.Nodes = append(s.Nodes, core.SurfaceNode{ID: core.WebParamID(id, "query", q), Parent: id, Kind: "param", Name: q, Source: "traffic", Facts: map[string]string{"in": "query"}})
		}
		for _, b := range r.body {
			s.Nodes = append(s.Nodes, core.SurfaceNode{ID: core.WebParamID(id, "body", b), Parent: id, Kind: "param", Name: b, Source: "traffic", Facts: map[string]string{"in": "body"}})
		}
		// A capture holds every request; the map needs the counts, so a
		// bounded sample per route carries the method, status and timing.
		n := r.requests
		if n > 60 {
			n = 60
		}
		for i := 0; i < n; i++ {
			at := now.Add(-time.Duration((ri*7919+i*1301)%(6*24*3600)+3600) * time.Second)
			status := 200
			if r.failed > 0 && i*r.requests/n < r.failed {
				status = 401
				if r.path == "/status/search" {
					status = 500
				}
				if r.path == "/reset/{id}" || r.path == "/api/v1/invoices/{id}" || r.path == "/api/v1/customers/{id}" {
					status = 404
				}
			}
			method := r.methods[i%len(r.methods)]
			facts.Observe(id, ledgerlyRequest(r.host, r.path, method, status, r.query, r.body, i))
			ref := "https://" + r.host + strings.ReplaceAll(r.path, "{id}", itoa(10200+i))
			ev = append(ev, core.SurfaceEvidence{NodeID: id, Kind: "request", Ref: ref, Actor: "operator", At: &at,
				Summary: map[string]any{"method": method, "path": r.path, "status": status, "ms": r.ms + (i*37)%40, "count": r.requests}})
		}
	}
	facts.ApplyAll(s.Nodes)
	// The operator looked at the routes the findings name, and a few more.
	for _, path := range []string{"/api/v1/invoices/{id}", "/api/v1/me", "/api/v1/auth/login", "/api/v1/members/{id}", "/api/v1/export"} {
		at := now.Add(-26 * time.Hour)
		ev = append(ev, core.SurfaceEvidence{NodeID: core.WebRouteID(api, path), Kind: "review", Actor: "operator", At: &at, Summary: map[string]any{"note": "read with the handler open"}})
	}
	for _, path := range []string{"/status/search", "/reset/{id}"} {
		at := now.Add(-25 * time.Hour)
		ev = append(ev, core.SurfaceEvidence{NodeID: core.WebRouteID(app, path), Kind: "review", Actor: "operator", At: &at, Summary: map[string]any{"note": "read with the handler open"}})
	}
	// Where the browser went next, as the capture saw it.
	nav := [][2]string{{"/", "/login"}, {"/login", "/app"}, {"/app", "/app/invoices"}, {"/app/invoices", "/app/invoices/{id}"}, {"/app", "/app/settings"}, {"/app", "/app/members"}, {"/status", "/status/search"}, {"/login", "/reset"}, {"/reset", "/reset/{id}"}}
	for i, n := range nav {
		s.Edges = append(s.Edges, core.SurfaceEdge{From: core.WebRouteID(app, n[0]), To: core.WebRouteID(app, n[1]), Kind: "nav", Count: 400 - i*37, Source: "traffic"})
	}
	// The report's own places, in case a finding names a route the traffic
	// did not reach.
	s.Enrich(core.WebNodes(findings), nil)
	return s, ev
}

// ledgerlyRequest is one sampled request of the demo site, shaped the way
// a capture would show it: the app takes a session cookie, the API a
// bearer, the public routes nothing; invoice and search responses echo
// what was asked.
func ledgerlyRequest(host, path, method string, status int, query, body []string, i int) core.WebRequest {
	r := core.WebRequest{Method: method, Status: status, Mime: "text/html; charset=utf-8", Headers: map[string]string{"Accept-Language": "en"}}
	api := strings.HasPrefix(path, "/api/")
	public := path == "/" || path == "/login" || path == "/signup" || path == "/status" || path == "/status/search" || strings.HasPrefix(path, "/reset") || path == "/api/v1/auth/login"
	if api {
		r.Mime = "application/json"
		r.Headers["X-Request-Id"] = "req-" + itoa(1000+i)
	}
	switch {
	case public:
	case api:
		r.Headers["Authorization"] = "Bearer eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMDIwMSJ9.c2lnbmF0dXJl"
	default:
		r.Headers["Cookie"] = "ledgerly_session=s3ss10n"
	}
	if path == "/api/v1/auth/login" && status == 200 {
		r.SetCookies = []string{"ledgerly_session"}
	}
	if path == "/api/v1/invoices/{id}/pdf" {
		r.Mime = "application/pdf"
	}
	for _, q := range query {
		r.Query = append(r.Query, core.KV{Name: q, Value: ledgerlyValue(q, i)})
	}
	if len(body) > 0 && method != "GET" && method != "DELETE" {
		r.BodyMime = "application/json"
		r.Body = map[string]any{}
		for _, b := range body {
			r.Body[b] = ledgerlyBody(b, i)
		}
	}
	// What the response echoes: the invoice carries its customer, the
	// status search its query.
	switch path {
	case "/api/v1/invoices/{id}", "/api/v1/invoices":
		r.Response = `{"id":` + itoa(10200+i) + `,"customer_id":"` + ledgerlyValue("customer_id", i) + `","status":"` + ledgerlyValue("status", i) + `"}`
	case "/status/search":
		r.Response = "<h1>Results for " + ledgerlyValue("q", i) + "</h1>"
	}
	return r
}

func ledgerlyValue(name string, i int) string {
	switch name {
	case "status":
		return []string{"open", "paid", "overdue"}[i%3]
	case "page":
		return itoa(1 + i%9)
	case "q":
		return "invoice " + itoa(10200+i)
	case "customer_id":
		return "0199c0de-0000-7000-8000-0000000" + itoa(100000 + i%40)[1:]
	case "from", "to", "due_at":
		return "2026-" + []string{"09", "10", "11"}[i%3] + "-" + itoa(10+i%18)
	case "format":
		return []string{"csv", "json"}[i%2]
	case "email":
		return "member" + itoa(i) + "@example.test"
	case "name":
		return []string{"Acme", "Northwind", "Globex"}[i%3]
	case "timezone":
		return "Europe/Berlin"
	case "role":
		return []string{"member", "admin"}[i%2]
	case "vat":
		return "DE" + itoa(100000000+i)
	case "url":
		return "https://hooks.example.test/" + itoa(i)
	}
	return "x"
}

func ledgerlyBody(name string, i int) any {
	switch name {
	case "lines", "events":
		return []any{}
	case "password":
		return "hunter2"
	}
	return ledgerlyValue(name, i)
}
