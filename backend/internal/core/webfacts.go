package core

import (
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// What traffic says about a web place, folded into facts on its node so
// the surface can be asked for it: the auth a route takes, the statuses
// it answered, the shape of a parameter's values and whether the value
// came back in the response. Names are kept; values are kept only when
// they are plainly not a secret, and never for a parameter whose name says
// it holds one.

// WebRequest is one request and its response, as a mapper saw it.
type WebRequest struct {
	Method     string
	Status     int
	Mime       string            // response content type
	BodyMime   string            // request body content type
	Headers    map[string]string // request headers, any case
	SetCookies []string          // names of the cookies the response set
	Query      []KV
	Body       map[string]any // the request body when it was JSON
	Response   string         // response text when the capture holds it
}

// KV is a named value of a request.
type KV struct {
	Name  string
	Value string
}

type routeFacts struct {
	methods, statuses, mimes, auths, bodies, headers, cookies set
}

type paramFacts struct {
	in        string
	shapes    set
	example   string
	reflected bool
}

type set map[string]bool

func (s set) add(v string) {
	if v != "" {
		s[v] = true
	}
}

func (s set) words() string {
	out := make([]string, 0, len(s))
	for v := range s {
		out = append(out, v)
	}
	sort.Strings(out)
	return strings.Join(out, ", ")
}

// WebFacts folds requests per route and parameter.
type WebFacts struct {
	routes map[string]*routeFacts
	params map[string]*paramFacts
}

func NewWebFacts() *WebFacts {
	return &WebFacts{routes: map[string]*routeFacts{}, params: map[string]*paramFacts{}}
}

const maxHeaderNames = 8

// Observe folds one request of a route. Parameters are keyed the way the
// map keys them, so Apply finds the node.
func (w *WebFacts) Observe(routeID string, r WebRequest) {
	rf := w.routes[routeID]
	if rf == nil {
		rf = &routeFacts{methods: set{}, statuses: set{}, mimes: set{}, auths: set{}, bodies: set{}, headers: set{}, cookies: set{}}
		w.routes[routeID] = rf
	}
	rf.methods.add(strings.ToUpper(r.Method))
	if r.Status > 0 {
		rf.statuses.add(strconv.Itoa(r.Status))
	}
	rf.mimes.add(MimeWord(r.Mime))
	rf.bodies.add(MimeWord(r.BodyMime))
	rf.auths.add(AuthOf(r.Headers))
	for name := range r.Headers {
		n := strings.ToLower(name)
		if strings.HasPrefix(n, "x-") && len(rf.headers) < maxHeaderNames {
			rf.headers.add(n)
		}
	}
	for _, c := range r.SetCookies {
		rf.cookies.add(strings.ToLower(c))
	}
	for _, q := range r.Query {
		w.param(WebParamID(routeID, "query", q.Name), "query", q.Name, q.Value, ValueShape(q.Value), r.Response)
	}
	for k, v := range r.Body {
		w.param(WebParamID(routeID, "body", k), "body", k, bodyText(v), BodyShape(v), r.Response)
	}
}

func (w *WebFacts) param(id, in, name, value, shape, response string) {
	p := w.params[id]
	if p == nil {
		p = &paramFacts{in: in, shapes: set{}}
		w.params[id] = p
	}
	p.shapes.add(shape)
	if p.example == "" {
		p.example = Example(name, value, shape)
	}
	if !p.reflected && Reflected(value, shape, response) {
		p.reflected = true
	}
}

// Apply writes what was folded onto the node, by its id. A node the
// traffic did not touch is left as it is.
func (w *WebFacts) Apply(n *SurfaceNode) {
	put := func(k, v string) {
		if v == "" {
			return
		}
		if n.Facts == nil {
			n.Facts = map[string]string{}
		}
		n.Facts[k] = v
	}
	if rf := w.routes[n.ID]; rf != nil {
		put("methods", rf.methods.words())
		put("status", rf.statuses.words())
		put("mime", rf.mimes.words())
		put("auth", rf.auths.words())
		put("body", rf.bodies.words())
		put("headers", rf.headers.words())
		put("cookies", rf.cookies.words())
	}
	if p := w.params[n.ID]; p != nil {
		put("in", p.in)
		put("type", p.shapes.words())
		put("example", p.example)
		if p.reflected {
			put("reflected", "true")
		}
	}
}

// ApplyAll applies to every node of a list.
func (w *WebFacts) ApplyAll(nodes []SurfaceNode) {
	for i := range nodes {
		w.Apply(&nodes[i])
	}
}

var (
	uuidRE  = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	intRE   = regexp.MustCompile(`^-?\d+$`)
	decRE   = regexp.MustCompile(`^-?\d+\.\d+$`)
	emailRE = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)
	dateRE  = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}([T ]\d{2}:\d{2}|$)`)
	jwtRE   = regexp.MustCompile(`^eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.`)
	hexRE   = regexp.MustCompile(`^[0-9a-fA-F]{16,}$`)
	blobRE  = regexp.MustCompile(`^[A-Za-z0-9+/_=-]{24,}$`)
)

// ValueShape names the shape of a text value: int, decimal, bool, uuid,
// email, url, date, token (a JWT or an opaque blob), hex, empty or text.
func ValueShape(v string) string {
	v = strings.TrimSpace(v)
	switch {
	case v == "":
		return "empty"
	case v == "true" || v == "false":
		return "bool"
	case intRE.MatchString(v):
		return "int"
	case decRE.MatchString(v):
		return "decimal"
	case uuidRE.MatchString(v):
		return "uuid"
	case emailRE.MatchString(v):
		return "email"
	case strings.HasPrefix(v, "http://") || strings.HasPrefix(v, "https://"):
		return "url"
	case dateRE.MatchString(v):
		return "date"
	case jwtRE.MatchString(v):
		return "token"
	case hexRE.MatchString(v):
		return "hex"
	case blobRE.MatchString(v):
		return "token"
	}
	return "text"
}

// BodyShape names the shape of a JSON value.
func BodyShape(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case bool:
		return "bool"
	case float64:
		if x == math.Trunc(x) {
			return "int"
		}
		return "decimal"
	case string:
		return ValueShape(x)
	case []any:
		return "list"
	case map[string]any:
		return "object"
	}
	return "text"
}

func bodyText(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(x)
	}
	return ""
}

var secretWords = map[string]bool{
	"password": true, "passwd": true, "pwd": true, "pass": true, "secret": true, "token": true, "key": true, "apikey": true,
	"auth": true, "authorization": true, "session": true, "sessionid": true, "sid": true, "cookie": true, "csrf": true, "xsrf": true,
	"otp": true, "pin": true, "ssn": true, "card": true, "cvv": true, "cvc": true, "iban": true, "pan": true, "signature": true, "sig": true,
	"bearer": true, "jwt": true, "hash": true, "salt": true, "nonce": true, "credential": true, "credentials": true,
}

var wordSplit = regexp.MustCompile(`[^a-z0-9]+`)

// SecretName says whether a parameter's name says it carries a secret.
func SecretName(name string) bool {
	n := strings.ToLower(name)
	if strings.Contains(n, "password") || strings.Contains(n, "token") || strings.Contains(n, "secret") {
		return true
	}
	for _, w := range wordSplit.Split(n, -1) {
		if secretWords[w] {
			return true
		}
	}
	return false
}

const maxExample = 40

// Example is the value kept as a sample, or nothing: never for a secret
// name, never a token, a hex blob, an address or anything long.
func Example(name, value, shape string) string {
	if SecretName(name) {
		return ""
	}
	switch shape {
	case "int", "decimal", "bool", "uuid", "date":
		return value
	case "text":
		v := strings.TrimSpace(value)
		if len(v) <= maxExample && !strings.ContainsAny(v, "=\n") {
			return v
		}
	}
	return ""
}

// Reflected says whether a value came back in the response. Short and
// trivial values match by accident, so they never count.
func Reflected(value, shape, response string) bool {
	if response == "" || len(value) < 4 {
		return false
	}
	switch shape {
	case "bool", "empty", "null", "list", "object":
		return false
	}
	return strings.Contains(response, value)
}

// AuthOf names how a request authenticated: bearer, basic, api-key,
// cookie, header (an Authorization of another scheme) or none.
func AuthOf(headers map[string]string) string {
	out := set{}
	for name, value := range headers {
		switch n := strings.ToLower(name); n {
		case "authorization":
			v := strings.ToLower(strings.TrimSpace(value))
			switch {
			case strings.HasPrefix(v, "bearer "):
				out.add("bearer")
			case strings.HasPrefix(v, "basic "):
				out.add("basic")
			case v != "":
				out.add("header")
			}
		case "x-api-key", "api-key", "x-auth-token", "x-access-token":
			out.add("api-key")
		case "cookie":
			if strings.TrimSpace(value) != "" {
				out.add("cookie")
			}
		}
	}
	if len(out) == 0 {
		return "none"
	}
	return out.words()
}

// MimeWord is the short word of a content type: json, html, form,
// multipart, xml, pdf, csv, text, image, or the subtype as is.
func MimeWord(mime string) string {
	m := strings.ToLower(strings.TrimSpace(strings.SplitN(mime, ";", 2)[0]))
	if m == "" {
		return ""
	}
	switch {
	case strings.Contains(m, "json"):
		return "json"
	case m == "text/html":
		return "html"
	case m == "application/x-www-form-urlencoded":
		return "form"
	case strings.HasPrefix(m, "multipart/"):
		return "multipart"
	case strings.Contains(m, "xml"):
		return "xml"
	case m == "application/pdf":
		return "pdf"
	case m == "text/csv":
		return "csv"
	case strings.HasPrefix(m, "text/"):
		return "text"
	case strings.HasPrefix(m, "image/"):
		return "image"
	}
	if i := strings.Index(m, "/"); i >= 0 {
		return m[i+1:]
	}
	return m
}
