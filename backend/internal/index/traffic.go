package index

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// What joins the entries of a capture, beyond the host that served them:
// a document triggers the requests that name it as their referer, a value
// a response returns travels into a later request's path, query or body,
// and an endpoint sends a parameter by name. The three are edges, so a
// statement over `edges` draws a page's tree, a value's journey, or the
// parameters endpoints share.

const (
	ValuePrefix = "value:"
	ParamPrefix = "param:"
)

// A value is worth following when it is not a word: it carries a digit or
// runs long, and it is made of the characters identifiers and tokens use.
var tokenRe = regexp.MustCompile(`^[A-Za-z0-9_\-.:@+=/]{6,256}$`)

func tokenish(v string) bool {
	if !tokenRe.MatchString(v) {
		return false
	}
	if len(v) >= 16 {
		return true
	}
	return strings.ContainsAny(v, "0123456789")
}

// A value sent by more entries than this is a constant of the site, not
// something that travelled.
const valueFanMax = 40

// valueID is the node of a value: its hash, never the value itself.
func valueID(v string) string {
	sum := sha256.Sum256([]byte(v))
	return ValuePrefix + hex.EncodeToString(sum[:8])
}

type at struct {
	entry int
	where string
}

// linkTraffic adds the edges between entries. Entries are read in the
// order the capture lists them, which is the order they started. A
// `yields` edge carries the value whole after its path, so a reader can
// find it on the wire; a `sent_in` edge carries where it sat.
func linkTraffic(entries []HAREntryRow, reqBodies, respBodies []string, target string, now time.Time) []EdgeRow {
	var edges []EdgeRow
	add := func(srcKind, src, dstKind, dst, rel, evidence string) {
		edges = append(edges, EdgeRow{Target: target, IngestedAt: now, SrcKind: srcKind, SrcID: src, DstKind: dstKind, DstID: dst, Rel: rel, Confidence: 1, Evidence: evidence})
	}
	// Documents by their URL without query, for referers to find.
	byURL := map[string][]int{}
	key := func(scheme, host, path string) string {
		if path == "" {
			path = "/"
		}
		return scheme + "://" + host + path
	}
	for i, e := range entries {
		byURL[key(e.Scheme, e.Host, e.Path)] = append(byURL[key(e.Scheme, e.Host, e.Path)], i)
	}
	// triggers: the latest earlier entry at the referer's URL.
	for i, e := range entries {
		ref := e.ReqHeaders["referer"]
		if ref == "" {
			continue
		}
		u, err := url.Parse(ref)
		if err != nil || u.Host == "" {
			continue
		}
		k := key(u.Scheme, strings.ToLower(u.Hostname()), u.Path)
		from := -1
		for _, j := range byURL[k] {
			if j != i && j < i {
				from = j
			}
		}
		if from < 0 {
			continue
		}
		add("entry", EntryPrefix+entries[from].EntryID, "entry", EntryPrefix+e.EntryID, "triggers", e.Method+" "+e.Path)
	}
	// sends: a parameter by name, from the query and a form or JSON body.
	for i, e := range entries {
		seen := map[string]bool{}
		send := func(name, value string) {
			if name == "" || seen[name] {
				return
			}
			seen[name] = true
			add("entry", EntryPrefix+e.EntryID, "param", ParamPrefix+name, "sends", cut(value, 40))
		}
		if e.Query != "" {
			if q, err := url.ParseQuery(e.Query); err == nil {
				names := make([]string, 0, len(q))
				for n := range q {
					names = append(names, n)
				}
				sort.Strings(names)
				for _, n := range names {
					send(n, q.Get(n))
				}
			}
		}
		for _, kv := range bodyFields(reqBodies[i], e.ReqContentType) {
			send(kv.name, kv.value)
		}
	}
	// yields and sent_in: a value a response returned, found in a later
	// request's path, query or body.
	sentBy := map[string][]at{}
	for i, e := range entries {
		for _, seg := range strings.Split(e.Path, "/") {
			if tokenish(seg) {
				sentBy[seg] = append(sentBy[seg], at{i, "path"})
			}
		}
		if e.Query != "" {
			if q, err := url.ParseQuery(e.Query); err == nil {
				for n, vs := range q {
					for _, v := range vs {
						if tokenish(v) {
							sentBy[v] = append(sentBy[v], at{i, "query " + n})
						}
					}
				}
			}
		}
		for _, kv := range bodyFields(reqBodies[i], e.ReqContentType) {
			if tokenish(kv.value) {
				sentBy[kv.value] = append(sentBy[kv.value], at{i, "body " + kv.name})
			}
		}
	}
	for i, e := range entries {
		yielded := map[string]bool{}
		for _, kv := range responseValues(respBodies[i], e.RespContentType, e.RespHeaders) {
			sent := sentBy[kv.value]
			if len(sent) == 0 || len(sent) > valueFanMax {
				continue
			}
			id := valueID(kv.value)
			for _, s := range sent {
				if s.entry <= i {
					continue
				}
				if !yielded[id] {
					yielded[id] = true
					add("entry", EntryPrefix+e.EntryID, "value", id, "yields", kv.name+": "+kv.value)
				}
				add("value", id, "entry", EntryPrefix+entries[s.entry].EntryID, "sent_in", s.where)
			}
		}
	}
	return edges
}

type field struct{ name, value string }

// bodyFields are the named values of a request body: form fields, or the
// leaves of a JSON document by their path.
func bodyFields(body, contentType string) []field {
	if body == "" {
		return nil
	}
	ct := strings.ToLower(contentType)
	switch {
	case strings.Contains(ct, "json"):
		return jsonLeaves(body, 400)
	case strings.Contains(ct, "x-www-form-urlencoded"):
		q, err := url.ParseQuery(body)
		if err != nil {
			return nil
		}
		var out []field
		for n, vs := range q {
			for _, v := range vs {
				out = append(out, field{n, v})
			}
		}
		sort.Slice(out, func(a, b int) bool { return out[a].name < out[b].name })
		return out
	}
	return nil
}

var hiddenInputRe = regexp.MustCompile(`<input[^>]*name="([^"]+)"[^>]*value="([^"]{6,})"`)

// responseValues are the values a response hands the client: the leaves
// of a JSON body, the hidden inputs of an HTML one, and a Location.
func responseValues(body, contentType string, headers map[string]string) []field {
	var out []field
	if loc := headers["location"]; loc != "" {
		out = append(out, field{"location", loc})
	}
	if body == "" {
		return out
	}
	ct := strings.ToLower(contentType)
	switch {
	case strings.Contains(ct, "json"):
		out = append(out, jsonLeaves(body, 2000)...)
	case strings.Contains(ct, "html"):
		for _, m := range hiddenInputRe.FindAllStringSubmatch(body, 200) {
			out = append(out, field{"input " + m[1], m[2]})
		}
	}
	return out
}

// jsonLeaves walks a JSON document to its strings and whole numbers, each
// named by its path (`data.items[0].id`), up to max of them.
func jsonLeaves(body string, max int) []field {
	var doc any
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		return nil
	}
	var out []field
	var walk func(path string, v any)
	walk = func(path string, v any) {
		if len(out) >= max {
			return
		}
		switch x := v.(type) {
		case map[string]any:
			keys := make([]string, 0, len(x))
			for k := range x {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				p := k
				if path != "" {
					p = path + "." + k
				}
				walk(p, x[k])
			}
		case []any:
			for i, it := range x {
				walk(path+"["+strconv.Itoa(i)+"]", it)
			}
		case string:
			out = append(out, field{path, x})
		case float64:
			if x == float64(int64(x)) && (x >= 10000 || x <= -10000) {
				out = append(out, field{path, strconv.FormatInt(int64(x), 10)})
			}
		}
	}
	walk("", doc)
	return out
}

func cut(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
