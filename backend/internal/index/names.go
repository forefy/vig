package index

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Names is what DNS and cloud listings add to the index.
type Names struct {
	Subdomains []SubdomainRow
	Resources  []CloudResourceRow
	Edges      []EdgeRow
}

// ReadNames reads a DNS listing: one name per line, followed by what it
// resolves to, addresses and CNAMEs, separated by spaces or commas, the
// way dnsx and similar tools print. Comments start with #.
func ReadNames(path, target string, now time.Time) ([]SubdomainRow, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	source := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	var out []SubdomainRow
	seen := map[string]int{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.FieldsFunc(line, func(r rune) bool { return r == ' ' || r == ',' || r == '\t' || r == ';' || r == '[' || r == ']' })
		fqdn := strings.ToLower(strings.TrimSuffix(fields[0], "."))
		if !strings.Contains(fqdn, ".") {
			continue
		}
		row := SubdomainRow{Target: target, IngestedAt: now, Apex: apexOf(fqdn), FQDN: fqdn, A: []string{}, Source: source, FirstSeen: now, LastSeen: now}
		for _, t := range fields[1:] {
			t = strings.ToLower(strings.TrimSuffix(t, "."))
			if net.ParseIP(t) != nil {
				row.A = append(row.A, t)
			} else if strings.Contains(t, ".") {
				row.CNAME = t
			}
		}
		if i, ok := seen[fqdn]; ok {
			out[i].A = uniq(append(out[i].A, row.A...))
			if out[i].CNAME == "" {
				out[i].CNAME = row.CNAME
			}
			continue
		}
		seen[fqdn] = len(out)
		out = append(out, row)
	}
	return out, sc.Err()
}

// apexOf is the registrable part of a name as two labels; enough to group
// a listing, not a public suffix lookup.
func apexOf(fqdn string) string {
	parts := strings.Split(fqdn, ".")
	if len(parts) <= 2 {
		return fqdn
	}
	return strings.Join(parts[len(parts)-2:], ".")
}

// A resource as a cloud listing names it: the fields every provider's
// inventory can give, in one JSON array.
type resourceJSON struct {
	Provider  string            `json:"provider"`
	AccountID string            `json:"account_id"`
	Region    string            `json:"region"`
	Service   string            `json:"service"`
	Type      string            `json:"type"`
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Public    bool              `json:"public"`
	Hostnames []string          `json:"hostnames"`
	Tags      map[string]string `json:"tags"`
}

// ReadResources reads a cloud listing: a JSON array of resources with
// provider, id, name, type, service, region, account_id, public, hostnames
// and tags.
func ReadResources(path, target string, now time.Time) ([]CloudResourceRow, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var list []resourceJSON
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	var out []CloudResourceRow
	for _, r := range list {
		if r.ID == "" {
			continue
		}
		hosts := make([]string, 0, len(r.Hostnames))
		for _, h := range r.Hostnames {
			hosts = append(hosts, strings.ToLower(strings.TrimSuffix(h, ".")))
		}
		tags := r.Tags
		if tags == nil {
			tags = map[string]string{}
		}
		out = append(out, CloudResourceRow{Target: target, IngestedAt: now, Provider: r.Provider, AccountID: r.AccountID, Region: r.Region, Service: r.Service, RType: r.Type, RID: r.ID, Name: r.Name, Public: r.Public, Hostnames: hosts, Tags: tags, Snapshot: now})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].RID < out[j].RID })
	return out, nil
}

// resolveEdges joins names to the resources they land on: a CNAME onto a
// resource hostname is certain, an address onto one is close.
func resolveEdges(subs []SubdomainRow, res []CloudResourceRow, target string, now time.Time) []EdgeRow {
	var out []EdgeRow
	for _, s := range subs {
		for _, r := range res {
			for _, h := range r.Hostnames {
				if h == "" {
					continue
				}
				switch {
				case s.CNAME != "" && (s.CNAME == h || strings.HasSuffix(s.CNAME, "."+h)):
					out = append(out, EdgeRow{Target: target, IngestedAt: now, SrcKind: "subdomain", SrcID: SubdomainPrefix + s.FQDN, DstKind: "resource", DstID: ResourcePrefix + r.RID, Rel: "resolves_to", Confidence: 1, Evidence: "cname " + s.CNAME})
				default:
					for _, a := range s.A {
						if a == h {
							out = append(out, EdgeRow{Target: target, IngestedAt: now, SrcKind: "subdomain", SrcID: SubdomainPrefix + s.FQDN, DstKind: "resource", DstID: ResourcePrefix + r.RID, Rel: "resolves_to", Confidence: 0.9, Evidence: "a " + a})
						}
					}
				}
			}
		}
	}
	return out
}
