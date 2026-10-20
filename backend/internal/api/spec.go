package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/forefy/vig/backend/internal/auth"
)

// specLevel is how much of the API a caller may see. The document served
// at /openapi.json is trimmed to the caller's level, so a user never learns
// the shape of the admin or operator surface. The committed spec and the
// generated client are always the full document.
type specLevel int

const (
	specPublic specLevel = iota
	specOperator
	specAdmin
)

var specSchemes = map[specLevel][]string{
	specPublic:   {secSession, secAPIKey},
	specOperator: {secSession, secAPIKey, secOperator},
	specAdmin:    {secSession, secAPIKey, secOperator, secAdmin},
}

// specCache holds the three trimmed documents; the spec is fixed per build.
type specCache struct {
	once sync.Once
	docs map[specLevel][]byte
	err  error
}

func (d *Deps) spec(level specLevel) ([]byte, error) {
	d.specs.once.Do(func() {
		raw, err := d.api.OpenAPI().MarshalJSON()
		if err != nil {
			d.specs.err = err
			return
		}
		d.specs.docs = map[specLevel][]byte{}
		for _, l := range []specLevel{specPublic, specOperator, specAdmin} {
			b, err := filterSpec(raw, l)
			if err != nil {
				d.specs.err = err
				return
			}
			d.specs.docs[l] = b
		}
	})
	return d.specs.docs[level], d.specs.err
}

// openAPIJSON serves the spec at the caller's level. Admin sessions and
// operator tokens see their own routes; everyone else sees the user surface.
func (d *Deps) openAPIJSON(w http.ResponseWriter, r *http.Request) {
	level := specPublic
	if p, ok := d.resolveRequest(r); ok {
		switch {
		case p.IsAdmin(d.now()):
			level = specAdmin
		case p.Via == auth.ViaOperator:
			level = specOperator
		}
	}
	b, err := d.spec(level)
	if err != nil {
		writeProblem(w, r, http.StatusInternalServerError, "spec unavailable")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "private, no-store")
	_, _ = w.Write(b)
}

// filterSpec drops every operation the level cannot call, then every schema
// nothing left refers to, then the security schemes the level cannot use.
func filterSpec(raw []byte, level specLevel) ([]byte, error) {
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	allowed := map[string]bool{}
	for _, s := range specSchemes[level] {
		allowed[s] = true
	}
	paths, _ := doc["paths"].(map[string]any)
	for p, item := range paths {
		ops, _ := item.(map[string]any)
		for m, op := range ops {
			o, ok := op.(map[string]any)
			if !ok {
				continue
			}
			if !secAllowed(o["security"], allowed) {
				delete(ops, m)
			}
		}
		if len(ops) == 0 {
			delete(paths, p)
		}
	}
	comps, _ := doc["components"].(map[string]any)
	if schemas, ok := comps["schemas"].(map[string]any); ok {
		keep := map[string]bool{}
		collectRefs(paths, keep)
		for changed := true; changed; {
			changed = false
			for name := range keep {
				before := len(keep)
				collectRefs(schemas[name], keep)
				if len(keep) != before {
					changed = true
				}
			}
		}
		for name := range schemas {
			if !keep[name] {
				delete(schemas, name)
			}
		}
	}
	if ss, ok := comps["securitySchemes"].(map[string]any); ok {
		for name := range ss {
			if !allowed[name] {
				delete(ss, name)
			}
		}
	}
	return json.Marshal(doc)
}

// secAllowed reports whether any alternative of an operation's security
// requirement is satisfiable at the level. No requirement means public.
func secAllowed(sec any, allowed map[string]bool) bool {
	alts, ok := sec.([]any)
	if !ok || len(alts) == 0 {
		return true
	}
	for _, alt := range alts {
		req, _ := alt.(map[string]any)
		ok := len(req) > 0
		for scheme := range req {
			if !allowed[scheme] {
				ok = false
			}
		}
		if ok {
			return true
		}
	}
	return false
}

// collectRefs records every schema name reached through a $ref under v.
func collectRefs(v any, out map[string]bool) {
	switch t := v.(type) {
	case map[string]any:
		for k, x := range t {
			if k == "$ref" {
				if s, ok := x.(string); ok && strings.HasPrefix(s, "#/components/schemas/") {
					out[strings.TrimPrefix(s, "#/components/schemas/")] = true
				}
				continue
			}
			collectRefs(x, out)
		}
	case []any:
		for _, x := range t {
			collectRefs(x, out)
		}
	}
}

// llms.txt follows the llms.txt shape: a title, a one-line summary, then
// short sections of links. It teaches where the reference is and that the
// reference is scoped to the credential. The file is scoped the same way:
// a role is described only to callers who hold it.
const llmsHead = `# vig

> Program audits sold as credits. One origin serves the app, the API and this file. Targets are chain adapters: %[1]s/v1/adapters lists them.

## Find the API

- [Reference](%[1]s/docs): interactive, scoped to the credential the browser holds
- [Spec](%[1]s/openapi.json): OpenAPI 3.1, scoped to the credential sent with the request

## Credentials

- User: session cookie, or ` + "`Authorization: Bearer vig_...`" + ` minted under settings, API keys
`

var llmsRole = map[specLevel]string{
	specOperator: "- Operator: `Authorization: Bearer vop_...`, bound to one job; work runs in the sandbox with `vig-op`, which prints its commands when run bare\n",
	specAdmin:    "- Admin: browser session with a TOTP check inside 12 hours\n",
}

const llmsTail = `
## Conventions

- IDs are UUIDv7, timestamps UTC, money in USDC base units
- Errors are RFC 9457 problem+json
`

func (d *Deps) llms(w http.ResponseWriter, r *http.Request) {
	level := specPublic
	if p, ok := d.resolveRequest(r); ok {
		switch {
		case p.IsAdmin(d.now()):
			level = specAdmin
		case p.Via == auth.ViaOperator:
			level = specOperator
		}
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "private, no-store")
	var b strings.Builder
	fmt.Fprintf(&b, llmsHead, origin(r))
	if level >= specOperator {
		b.WriteString(llmsRole[specOperator])
	}
	if level == specAdmin {
		b.WriteString(llmsRole[specAdmin])
	}
	b.WriteString(llmsTail)
	_, _ = w.Write([]byte(b.String()))
}

// origin is the public scheme and host of the request, as the caller saw it.
func origin(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}
