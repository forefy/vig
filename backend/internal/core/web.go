package core

import (
	"net/url"
	"strings"
)

// The web adapter's places on the surface. A host stands where a program
// does, a route where an instruction, a parameter where an account. Core
// knows the shape of those ids and how a URL folds into one; it never
// reads a site.

// IDSegment is what an identifier in a path folds to, so every request to
// one feature lands on one route: /users/42 and /users/7 are /users/{id}.
const IDSegment = "{id}"

// RouteShape folds identifiers out of a path: numbers, hex and UUIDs,
// and long opaque tokens that carry a digit. A segment already folded
// stays. Trailing slashes are dropped; the root is "/".
func RouteShape(path string) string {
	parts := strings.Split(path, "/")
	out := make([]string, 0, len(parts))
	for _, s := range parts {
		if s == "" {
			continue
		}
		if isIDSegment(s) {
			s = IDSegment
		}
		out = append(out, s)
	}
	return "/" + strings.Join(out, "/")
}

func isIDSegment(s string) bool {
	if s == IDSegment {
		return true
	}
	digits, hex, opaque := true, true, true
	hasDigit := false
	for _, r := range s {
		isDigit := r >= '0' && r <= '9'
		hasDigit = hasDigit || isDigit
		digits = digits && isDigit
		hex = hex && (isDigit || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F') || r == '-')
		opaque = opaque && (isDigit || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '-' || r == '_')
	}
	if !hasDigit {
		return false
	}
	return digits || (hex && len(s) >= 16) || (opaque && len(s) >= 22)
}

// WebHostID is the node id of a host.
func WebHostID(host string) string { return "host:" + host }

// WebRouteID is the node id of a route on a host: the folded path, with no
// method, so a finding that names a URL and a capture that saw it meet on
// one node. The methods seen are a fact on the node.
func WebRouteID(host, shape string) string { return "route:" + host + ":" + shape }

// WebParamID is the node id of one parameter of a route, by where it
// travels: "?" for the query, "#" for the body.
func WebParamID(routeID, in, name string) string {
	if in == "body" {
		return routeID + "#" + name
	}
	return routeID + "?" + name
}

// WebPlace reads the host and the folded path of a URL.
func WebPlace(raw string) (host, shape string, ok bool) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return "", "", false
	}
	return strings.ToLower(u.Host), RouteShape(u.Path), true
}

// WebHostNode is the host of a base URL as a node, or nil when the URL
// names none.
func WebHostNode(baseURL string) *SurfaceNode {
	host, _, ok := WebPlace(baseURL)
	if !ok {
		return nil
	}
	n := webHost(host, "static")
	return &n
}

func webHost(host, source string) SurfaceNode {
	return SurfaceNode{ID: WebHostID(host), Kind: "host", Name: host, Source: source, Facts: map[string]string{"host": host}}
}

func webRoute(host, shape, source string) SurfaceNode {
	return SurfaceNode{ID: WebRouteID(host, shape), Parent: WebHostID(host), Kind: "route", Name: shape, Source: source, Facts: map[string]string{"host": host, "path": shape}}
}

// WebNodes is what a report alone says a web target exposes: a host for
// every URL a finding is located at, and a route for every URL a finding
// or one of its sites names on those hosts, in first-named order, so the
// map stands before any capture is posted. A site on another host is a
// place a reproduction reaches (an attacker's listener, a resolver), not
// a place the target exposes, and stays off the map. Static: read from
// the report, not seen in traffic.
func WebNodes(findings []Finding) []SurfaceNode {
	var hosts, routes []SurfaceNode
	have := map[string]bool{}
	for _, f := range findings {
		host, _, ok := WebPlace(f.Location.URL)
		if !ok {
			continue
		}
		if h := WebHostID(host); !have[h] {
			have[h] = true
			hosts = append(hosts, webHost(host, "static"))
		}
	}
	for _, f := range findings {
		locs := []Location{f.Location}
		for _, site := range f.Sites {
			locs = append(locs, site.Location)
		}
		for _, loc := range locs {
			host, shape, ok := WebPlace(loc.URL)
			if !ok || !have[WebHostID(host)] {
				continue
			}
			if r := WebRouteID(host, shape); !have[r] {
				have[r] = true
				routes = append(routes, webRoute(host, shape, "static"))
			}
		}
	}
	return append(hosts, routes...)
}
