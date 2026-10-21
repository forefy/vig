package core

import (
	"context"
	"net/url"
	"sort"
	"strings"
)

// SiteScope is what the customer authorised on a live target: the hosts
// that may be probed and the path prefixes the review may walk on each of
// them. Hosts are lowercase, each one the origin's host or a subdomain of
// it; prefixes start with a slash and an empty list means the whole host.
// Nothing outside it is touched, which is the consent SPEC §12 and §20
// require before a web adapter ships.
type SiteScope struct {
	Hosts []string `json:"hosts" doc:"Hosts the review may probe; the origin's host is always among them"`
	Paths []string `json:"paths,omitempty" doc:"Path prefixes the review may walk on every scoped host; none means the whole host"`
}

// Normalize lowercases and dedupes the hosts, trims the prefixes, and makes
// sure the origin's host stands first.
func (s SiteScope) Normalize(originHost string) SiteScope {
	seen := map[string]bool{}
	out := SiteScope{}
	add := func(h string) {
		h = strings.ToLower(strings.TrimSpace(h))
		if h == "" || seen[h] {
			return
		}
		seen[h] = true
		out.Hosts = append(out.Hosts, h)
	}
	add(originHost)
	rest := make([]string, 0, len(s.Hosts))
	for _, h := range s.Hosts {
		if h = strings.ToLower(strings.TrimSpace(h)); h != "" && !seen[h] {
			rest = append(rest, h)
		}
	}
	sort.Strings(rest)
	for _, h := range rest {
		add(h)
	}
	pseen := map[string]bool{}
	for _, p := range s.Paths {
		p = strings.TrimSpace(p)
		if p == "" || p == "/" || pseen[p] {
			continue
		}
		pseen[p] = true
		out.Paths = append(out.Paths, p)
	}
	sort.Strings(out.Paths)
	return out
}

// URLs lists the scope as URL prefixes under the origin's scheme: one per
// host, or one per host and prefix when prefixes are set. These are the
// places an audit of the site is pinned to.
func (s SiteScope) URLs(origin string) []string {
	scheme := "https"
	if u, err := url.Parse(origin); err == nil && u.Scheme != "" {
		scheme = u.Scheme
	}
	var out []string
	for _, h := range s.Hosts {
		if len(s.Paths) == 0 {
			out = append(out, scheme+"://"+h+"/")
			continue
		}
		for _, p := range s.Paths {
			out = append(out, scheme+"://"+h+p)
		}
	}
	return out
}

// SiteProof is where a nonce must appear for an origin.
type SiteProof struct {
	DNSName string `json:"dns_name" doc:"TXT record name to create"`
	Value   string `json:"value" doc:"The TXT record's value, and the file's whole body"`
	FileURL string `json:"file_url" doc:"Where the file must answer"`
}

// SiteOwnership is the optional adapter capability behind a site's
// ownership proof: where a nonce goes, and whether it is there.
type SiteOwnership interface {
	OwnershipPlaces(origin, nonce string) (SiteProof, error)
	// ProveOwnership returns the method that held the nonce, or "" when
	// neither place did. Not found is not an error.
	ProveOwnership(ctx context.Context, origin, nonce string) (string, error)
}

// Allows says whether a URL falls inside the scope: its host is scoped and
// its path is under a scoped prefix (any path, when no prefix is set).
func (s SiteScope) Allows(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	ok := false
	for _, h := range s.Hosts {
		if h == host {
			ok = true
			break
		}
	}
	if !ok {
		return false
	}
	if len(s.Paths) == 0 {
		return true
	}
	path := u.Path
	if path == "" {
		path = "/"
	}
	for _, p := range s.Paths {
		if path == p || strings.HasPrefix(path, strings.TrimSuffix(p, "/")+"/") {
			return true
		}
	}
	return false
}
