// Package webapp is target adapter three: a live web application. There is
// no chain and no bytecode; the subject is an origin, the scope is the set
// of hosts and path prefixes the customer authorises probing on, and
// ownership is proven by a nonce the customer places where only the site's
// operator can: a DNS TXT record or a file under /.well-known. Every read
// is a plain fetch of public things; nothing is keyed and nothing is paid.
package webapp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/forefy/vig/backend/internal/core"
)

// Key is the adapter identifier used in targets.adapter.
const Key = "webapp"

// Kind is the target card's value.
const Kind = "web_app"

// Step keys. The wizard engine renders what the adapter declares; the
// site step stands where a chain adapter's source step stands.
const (
	StepTarget = "target"
	StepSite   = "site"
	StepScope  = "scope"
	StepReview = "review"
)

// Panel row keys, in render order.
const (
	RowSite      = "site"
	RowScheme    = "scheme"
	RowRedirect  = "redirect"
	RowServer    = "server"
	RowHSTS      = "hsts"
	RowOwnership = "ownership"
)

// RowOrder is the panel order.
var RowOrder = []string{RowSite, RowScheme, RowRedirect, RowServer, RowHSTS, RowOwnership}

// RowLabels are the short labels the panel shows. No sentences.
var RowLabels = map[string]string{
	RowSite:      "Site",
	RowScheme:    "Scheme",
	RowRedirect:  "Redirects to",
	RowServer:    "Server",
	RowHSTS:      "HSTS",
	RowOwnership: "Ownership",
}

// Where the ownership nonce goes. Either place proves it.
const (
	// DNSLabel is prefixed to the origin's host: _vig.example.com TXT "vig-verify=<nonce>".
	DNSLabel = "_vig"
	// WellKnownPath is fetched on the origin; its body is the nonce value.
	WellKnownPath = "/.well-known/vig-verify.txt"
	// ValuePrefix precedes the nonce in both places.
	ValuePrefix = "vig-verify="
)

// ProofMethod is how a site's ownership was proven.
const (
	ProofDNS       = "dns_txt"
	ProofWellKnown = "well_known_file"
)

// Shapes the wizard validates on the client before the adapter does.
const (
	OriginPattern = `^https?://[A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?)+(:[0-9]{1,5})?/?$`
	HostPattern   = `^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$`
	PathPattern   = `^/[^\s?#]*$`
	// Public GitHub repo URL, optionally with /tree/<rev>/<path>; a source snapshot.
	RepoURLPattern = `^https://github\.com/[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+(/tree/[^\s]+)?/?$`
)

var (
	originRE  = regexp.MustCompile(OriginPattern)
	hostRE    = regexp.MustCompile(HostPattern)
	pathRE    = regexp.MustCompile(PathPattern)
	repoURLRE = regexp.MustCompile(RepoURLPattern)
)

// Resolver answers the DNS read the ownership proof makes.
type Resolver interface {
	LookupTXT(ctx context.Context, name string) ([]string, error)
}

// Adapter implements core.Adapter. HTTP nil is the contract-only
// configuration (spec generation, tests without network).
type Adapter struct {
	HTTP     *http.Client
	Resolver Resolver
	// Now is the clock; tests override it.
	Now func() time.Time
}

// New returns the adapter without network access.
func New() *Adapter { return &Adapter{} }

// NewWired returns the adapter with a bounded client for the fetches it
// makes: the origin at intake, the well-known file and the TXT record at
// proof time.
func NewWired() *Adapter {
	dialer := &net.Dialer{Timeout: 5 * time.Second, Control: refuseInternal}
	return &Adapter{
		HTTP: &http.Client{
			Timeout: 10 * time.Second,
			Transport: &http.Transport{
				DialContext:         dialer.DialContext,
				MaxIdleConns:        4,
				TLSHandshakeTimeout: 5 * time.Second,
			},
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 5 {
					return errors.New("too many redirects")
				}
				return nil
			},
		},
		Resolver: net.DefaultResolver,
	}
}

// refuseInternal is the dial control on every fetch the adapter makes: the
// origin is the customer's word, so an address that resolves to a loopback,
// private, link-local or unspecified range is never connected to, whatever
// the name or a redirect said. The check runs on the resolved address, so
// a public name pointing inside is refused too.
func refuseInternal(network, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	ip := net.ParseIP(host)
	if ip == nil || !publicIP(ip) {
		return fmt.Errorf("refusing to connect to %s", host)
	}
	return nil
}

// publicIP says whether an address is routable on the public internet.
func publicIP(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return false
	}
	if v4 := ip.To4(); v4 != nil {
		// Carrier-grade NAT and the benchmarking range are not public either.
		if v4[0] == 100 && v4[1]&0xc0 == 64 {
			return false
		}
		if v4[0] == 198 && v4[1]&0xfe == 18 {
			return false
		}
	}
	return true
}

func (a *Adapter) Key() string { return Key }

// IntakeSteps declares the wizard steps: the target card, the site (its
// origin, and an optional repository snapshot), the scope the customer
// authorises, and the review.
func (a *Adapter) IntakeSteps() []core.StepSpec {
	return []core.StepSpec{
		{Key: StepTarget, Title: "Target", Fields: []core.FieldSpec{
			{Key: "kind", Kind: core.FieldCards, Label: "Target", Required: true, Options: []core.Option{
				{Value: Kind, Label: "Web app", Hint: "live site"},
			}},
		}},
		{Key: StepSite, Title: "Site", Fields: []core.FieldSpec{
			{Key: "base_url", Kind: core.FieldText, Label: "Origin", Required: true, Pattern: OriginPattern},
			{Key: "repo_url", Kind: core.FieldText, Label: "Repository", Required: false, Pattern: RepoURLPattern},
			{Key: "selector", Kind: core.FieldSelect, Label: "Revision", Required: false, Options: []core.Option{
				{Value: "branch", Label: "Branch"}, {Value: "tag", Label: "Tag"}, {Value: "commit", Label: "Commit"},
			}},
			{Key: "revision", Kind: core.FieldText, Label: "Value", Required: false},
		}},
		{Key: StepScope, Title: "Scope", Fields: []core.FieldSpec{
			{Key: "hosts", Kind: core.FieldList, Label: "Hosts", Required: true, Pattern: HostPattern},
			{Key: "paths", Kind: core.FieldList, Label: "Path prefixes", Required: false, Pattern: PathPattern},
		}},
		{Key: StepReview, Title: "Review", Fields: []core.FieldSpec{
			{Key: "pin", Kind: core.FieldSummary, Label: "Pin", Required: false},
		}},
	}
}

// ParseOrigin reads an http(s) origin: scheme and host, an optional port,
// nothing else. The result has no trailing slash and a lowercase host.
func ParseOrigin(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errors.New("origin is empty")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("not a URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, errors.New("origin must be http or https")
	}
	if u.Hostname() == "" || u.User != nil {
		return nil, errors.New("origin needs a host")
	}
	if (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("origin is scheme and host only")
	}
	if net.ParseIP(u.Hostname()) != nil || !hostRE.MatchString(strings.ToLower(u.Hostname())) {
		return nil, errors.New("origin host is not a public hostname")
	}
	out := &url.URL{Scheme: u.Scheme, Host: strings.ToLower(u.Host)}
	return out, nil
}

// InScope says whether a host is the origin's host or a subdomain of it.
func InScope(originHost, host string) bool {
	originHost = strings.ToLower(strings.TrimSpace(originHost))
	host = strings.ToLower(strings.TrimSpace(host))
	if originHost == "" || host == "" {
		return false
	}
	return host == originHost || strings.HasSuffix(host, "."+originHost)
}

// ScopeIssues checks a scope against its origin: every host is the origin's
// host or a subdomain of it, every prefix starts with a slash.
func ScopeIssues(originHost string, scope core.SiteScope) []core.Issue {
	var issues []core.Issue
	for _, h := range scope.Hosts {
		h = strings.ToLower(strings.TrimSpace(h))
		switch {
		case !hostRE.MatchString(h):
			issues = append(issues, core.Issue{Step: StepScope, Field: "hosts", Msg: "not a hostname: " + h})
		case !InScope(originHost, h):
			issues = append(issues, core.Issue{Step: StepScope, Field: "hosts", Msg: h + " is not " + originHost + " or under it"})
		}
	}
	for _, p := range scope.Paths {
		if !pathRE.MatchString(strings.TrimSpace(p)) {
			issues = append(issues, core.Issue{Step: StepScope, Field: "paths", Msg: "not a path prefix: " + p})
		}
	}
	return issues
}

func draftString(draft core.IntakeDraft, step, field string) string {
	s, _ := draft[step][field].(string)
	return s
}

func draftList(draft core.IntakeDraft, step, field string) []string {
	var out []string
	switch v := draft[step][field].(type) {
	case []string:
		out = v
	case []any:
		for _, x := range v {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
	case string:
		if v != "" {
			out = []string{v}
		}
	}
	return out
}

// Validate checks the shapes the wizard collects: an http(s) origin, an
// optional public repository, and a scope that stays under the origin.
func (a *Adapter) Validate(ctx context.Context, draft core.IntakeDraft) ([]core.Issue, error) {
	var issues []core.Issue
	originHost := ""
	if raw := draftString(draft, StepSite, "base_url"); raw != "" {
		if u, err := ParseOrigin(raw); err != nil {
			issues = append(issues, core.Issue{Step: StepSite, Field: "base_url", Msg: err.Error()})
		} else {
			originHost = u.Hostname()
		}
	}
	if r := draftString(draft, StepSite, "repo_url"); r != "" && !repoURLRE.MatchString(r) {
		issues = append(issues, core.Issue{Step: StepSite, Field: "repo_url", Msg: "not a public GitHub URL"})
	}
	scope := core.SiteScope{Hosts: draftList(draft, StepScope, "hosts"), Paths: draftList(draft, StepScope, "paths")}
	if len(scope.Hosts) > 0 || len(scope.Paths) > 0 {
		if originHost == "" {
			issues = append(issues, core.Issue{Step: StepScope, Field: "hosts", Msg: "scope needs an origin"})
		} else {
			issues = append(issues, ScopeIssues(originHost, scope)...)
		}
	}
	return issues, nil
}

// Facts is what one read of the origin told us.
type Facts struct {
	Origin    string
	Reachable bool
	Status    int
	FinalURL  string // where the origin landed after redirects, when elsewhere
	Server    string
	HSTS      bool
	Err       string
}

// Inspect fetches the origin once and records what a reader would see
// first: whether it answers, where it lands, what serves it.
func (a *Adapter) Inspect(ctx context.Context, origin string) (Facts, error) {
	if a.HTTP == nil {
		return Facts{}, core.ErrNotWired
	}
	u, err := ParseOrigin(origin)
	if err != nil {
		return Facts{}, err
	}
	f := Facts{Origin: u.String()}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String()+"/", nil)
	if err != nil {
		return f, err
	}
	req.Header.Set("User-Agent", "vig-intake/1 (+site check)")
	resp, err := a.HTTP.Do(req)
	if err != nil {
		f.Err = err.Error()
		return f, nil
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	f.Reachable = true
	f.Status = resp.StatusCode
	f.Server = resp.Header.Get("Server")
	f.HSTS = resp.Header.Get("Strict-Transport-Security") != ""
	if resp.Request != nil && resp.Request.URL != nil {
		final := resp.Request.URL
		if !strings.EqualFold(final.Host, u.Host) || final.Scheme != u.Scheme {
			f.FinalURL = (&url.URL{Scheme: final.Scheme, Host: final.Host}).String()
		}
	}
	return f, nil
}

// Panel renders facts as rows. Unreachable is a warning, plain http is a
// warning, a redirect off the origin is a warning; ownership starts not
// proven with the prove action on it.
func Panel(f Facts, proven bool) core.VerificationPanel {
	rows := make([]core.PanelRow, 0, len(RowOrder))
	for _, k := range RowOrder {
		row := core.PanelRow{Key: k, Label: RowLabels[k], State: core.BadgeNeutral}
		switch k {
		case RowSite:
			switch {
			case f.Reachable && f.Status < 400:
				row.Value, row.State = fmt.Sprintf("%d", f.Status), core.BadgeOK
			case f.Reachable:
				row.Value, row.State = fmt.Sprintf("%d", f.Status), core.BadgeWarn
			default:
				row.Value, row.State, row.Detail = "unreachable", core.BadgeWarn, f.Err
			}
		case RowScheme:
			if strings.HasPrefix(f.Origin, "https://") {
				row.Value, row.State = "https", core.BadgeOK
			} else {
				row.Value, row.State = "http", core.BadgeWarn
			}
		case RowRedirect:
			if f.FinalURL != "" {
				row.Value, row.State = f.FinalURL, core.BadgeWarn
			} else if f.Reachable {
				row.Value, row.State = "none", core.BadgeOK
			} else {
				row.State = core.BadgePending
			}
		case RowServer:
			if f.Server != "" {
				row.Value = f.Server
			} else if !f.Reachable {
				row.State = core.BadgePending
			} else {
				row.Value = "unnamed"
			}
		case RowHSTS:
			switch {
			case !f.Reachable:
				row.State = core.BadgePending
			case f.HSTS:
				row.Value, row.State = "present", core.BadgeOK
			default:
				row.Value, row.State = "absent", core.BadgeWarn
			}
		case RowOwnership:
			if proven {
				row.Value, row.State = "proven", core.BadgeOK
			} else {
				row.Value, row.State, row.Action = "not proven", core.BadgeNeutral, "prove_ownership"
			}
		}
		rows = append(rows, row)
	}
	return core.VerificationPanel{Subject: f.Origin, Rows: rows}
}

// EmptyPanel is the panel shape before any fetch, every row pending.
func EmptyPanel(origin string) core.VerificationPanel {
	rows := make([]core.PanelRow, 0, len(RowOrder))
	for _, k := range RowOrder {
		rows = append(rows, core.PanelRow{Key: k, Label: RowLabels[k], State: core.BadgePending})
	}
	return core.VerificationPanel{Subject: origin, Rows: rows}
}

// Verify reads the origin the draft names and renders its panel.
func (a *Adapter) Verify(ctx context.Context, draft core.IntakeDraft) ([]core.VerificationPanel, error) {
	origin := draftString(draft, StepSite, "base_url")
	if origin == "" {
		return nil, nil
	}
	f, err := a.Inspect(ctx, origin)
	if err != nil {
		return nil, err
	}
	return []core.VerificationPanel{Panel(f, false)}, nil
}

// Preflight on a web target is the site panel; a source snapshot, when one
// was given, is a reference the operator reads, not a tree to scope.
func (a *Adapter) Preflight(ctx context.Context, clone core.Clone, draft core.IntakeDraft) (core.Preflight, error) {
	panels, err := a.Verify(ctx, draft)
	if err != nil && !errors.Is(err, core.ErrNotWired) {
		return core.Preflight{}, err
	}
	if panels == nil {
		panels = []core.VerificationPanel{}
	}
	return core.Preflight{Adapter: Key, Panels: panels, Tree: []core.TreeNode{}}, nil
}

func (a *Adapter) LocationKinds() []core.LocationKind {
	return []core.LocationKind{"url", "file"}
}

func (a *Adapter) OwnershipProof() core.OwnershipProofSpec {
	return core.OwnershipProofSpec{Method: "dns_txt_or_well_known_file"}
}

// Proof is where a nonce must appear for an origin.
type Proof struct {
	Nonce   string
	DNSName string // _vig.<host>
	Value   string // vig-verify=<nonce>, the TXT record and the file body
	FileURL string // <origin>/.well-known/vig-verify.txt
}

// ProofFor says where a nonce goes for an origin.
func ProofFor(origin, nonce string) (Proof, error) {
	u, err := ParseOrigin(origin)
	if err != nil {
		return Proof{}, err
	}
	return Proof{
		Nonce:   nonce,
		DNSName: DNSLabel + "." + u.Hostname(),
		Value:   ValuePrefix + nonce,
		FileURL: u.String() + WellKnownPath,
	}, nil
}

// OwnershipPlaces implements core.SiteOwnership.
func (a *Adapter) OwnershipPlaces(origin, nonce string) (core.SiteProof, error) {
	p, err := ProofFor(origin, nonce)
	if err != nil {
		return core.SiteProof{}, err
	}
	return core.SiteProof{DNSName: p.DNSName, Value: p.Value, FileURL: p.FileURL}, nil
}

// ProveOwnership looks for the nonce in either place and says which one
// held it. Not found is not an error; the methods are tried in order and
// the first hit wins.
func (a *Adapter) ProveOwnership(ctx context.Context, origin, nonce string) (string, error) {
	if a.HTTP == nil || a.Resolver == nil {
		return "", core.ErrNotWired
	}
	p, err := ProofFor(origin, nonce)
	if err != nil {
		return "", err
	}
	if txts, err := a.Resolver.LookupTXT(ctx, p.DNSName); err == nil {
		for _, t := range txts {
			if strings.TrimSpace(t) == p.Value {
				return ProofDNS, nil
			}
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.FileURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "vig-intake/1 (+ownership)")
	resp, err := a.HTTP.Do(req)
	if err != nil {
		return "", nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", nil
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
	for _, line := range strings.Split(string(body), "\n") {
		if strings.TrimSpace(line) == p.Value {
			return ProofWellKnown, nil
		}
	}
	return "", nil
}
