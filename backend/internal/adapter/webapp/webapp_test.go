package webapp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/forefy/vig/backend/internal/core"
)

func draft(base string, hosts, paths []string) core.IntakeDraft {
	return core.IntakeDraft{
		StepSite:  {"base_url": base},
		StepScope: {"hosts": hosts, "paths": paths},
	}
}

func fields(issues []core.Issue) string {
	var out []string
	for _, i := range issues {
		out = append(out, i.Step+"."+i.Field)
	}
	return strings.Join(out, ",")
}

func TestValidateOrigin(t *testing.T) {
	a := New()
	cases := []struct {
		base string
		bad  bool
	}{
		{"https://app.example.com", false},
		{"https://app.example.com/", false},
		{"http://staging.example.com:8080", false},
		{"https://App.Example.com", false},
		{"ftp://example.com", true},
		{"https://example.com/login", true},
		{"https://example.com/?x=1", true},
		{"https://example.com#top", true},
		{"https://user:pw@example.com", true},
		{"https://localhost", true},
		{"https://127.0.0.1", true},
		{"https://10.0.0.1:8080", true},
		{"https://[::1]", true},
		{"example.com", true},
		{"", false}, // nothing typed yet is not an issue
	}
	for _, c := range cases {
		issues, err := a.Validate(context.Background(), draft(c.base, nil, nil))
		if err != nil {
			t.Fatal(err)
		}
		if got := len(issues) > 0; got != c.bad {
			t.Errorf("%q: issues %v, want bad=%v", c.base, issues, c.bad)
		}
	}
}

func TestValidateScopeStaysUnderOrigin(t *testing.T) {
	a := New()
	base := "https://app.example.com"
	ok := []string{"app.example.com", "api.app.example.com", "APP.example.com"}
	for _, h := range ok {
		if issues, _ := a.Validate(context.Background(), draft(base, []string{h}, nil)); len(issues) > 0 {
			t.Errorf("%s should be in scope: %v", h, issues)
		}
	}
	bad := []string{"example.com", "evil-app.example.com", "app.example.com.evil.net", "other.net", "not a host"}
	for _, h := range bad {
		issues, _ := a.Validate(context.Background(), draft(base, []string{h}, nil))
		if fields(issues) != "scope.hosts" {
			t.Errorf("%s should fail on scope.hosts, got %v", h, issues)
		}
	}
	if issues, _ := a.Validate(context.Background(), draft(base, nil, []string{"/api/", "/v2"})); len(issues) > 0 {
		t.Errorf("prefixes: %v", issues)
	}
	if issues, _ := a.Validate(context.Background(), draft(base, nil, []string{"api"})); fields(issues) != "scope.paths" {
		t.Errorf("a prefix without a slash should fail, got %v", issues)
	}
	if issues, _ := a.Validate(context.Background(), draft("", []string{"app.example.com"}, nil)); fields(issues) != "scope.hosts" {
		t.Errorf("scope without an origin should fail, got %v", issues)
	}
	if issues, _ := a.Validate(context.Background(), core.IntakeDraft{StepSite: {"base_url": base, "repo_url": "https://gitlab.com/x/y"}}); fields(issues) != "site.repo_url" {
		t.Errorf("a non-GitHub snapshot should fail, got %v", issues)
	}
}

func TestScopeNormalizeAndURLs(t *testing.T) {
	s := core.SiteScope{Hosts: []string{"API.app.example.com", "app.example.com", "", "api.app.example.com"}, Paths: []string{"/v2", "/", "", "/api/"}}
	n := s.Normalize("app.example.com")
	if strings.Join(n.Hosts, ",") != "app.example.com,api.app.example.com" {
		t.Errorf("hosts %v", n.Hosts)
	}
	if strings.Join(n.Paths, ",") != "/api/,/v2" {
		t.Errorf("paths %v", n.Paths)
	}
	got := strings.Join(n.URLs("https://app.example.com"), " ")
	want := "https://app.example.com/api/ https://app.example.com/v2 https://api.app.example.com/api/ https://api.app.example.com/v2"
	if got != want {
		t.Errorf("urls\n got %s\nwant %s", got, want)
	}
	whole := core.SiteScope{}.Normalize("app.example.com")
	if got := strings.Join(whole.URLs("http://app.example.com"), " "); got != "http://app.example.com/" {
		t.Errorf("whole host: %s", got)
	}
}

func TestScopeAllows(t *testing.T) {
	s := core.SiteScope{Hosts: []string{"app.example.com", "api.app.example.com"}, Paths: []string{"/api/", "/v2"}}
	yes := []string{"https://app.example.com/api/users", "https://api.app.example.com/v2", "https://app.example.com/v2/x?y=1", "https://APP.example.com/api/"}
	for _, u := range yes {
		if !s.Allows(u) {
			t.Errorf("%s should be allowed", u)
		}
	}
	no := []string{"https://app.example.com/", "https://app.example.com/v22", "https://example.com/api/", "https://evil.net/api/", "not a url", "/api/"}
	for _, u := range no {
		if s.Allows(u) {
			t.Errorf("%s should be refused", u)
		}
	}
	whole := core.SiteScope{Hosts: []string{"app.example.com"}}
	if !whole.Allows("https://app.example.com/anything") || whole.Allows("https://x.app.example.com/") {
		t.Error("a scope without prefixes allows the whole host and nothing else")
	}
}

func TestProofPlaces(t *testing.T) {
	p, err := ProofFor("https://App.Example.com/", "n0nce")
	if err != nil {
		t.Fatal(err)
	}
	if p.DNSName != "_vig.app.example.com" || p.Value != "vig-verify=n0nce" || p.FileURL != "https://app.example.com/.well-known/vig-verify.txt" {
		t.Errorf("%+v", p)
	}
}

type fakeDNS map[string][]string

func (f fakeDNS) LookupTXT(ctx context.Context, name string) ([]string, error) {
	if v, ok := f[name]; ok {
		return v, nil
	}
	return nil, errors.New("no such host")
}

func TestProveOwnership(t *testing.T) {
	body := ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != WellKnownPath {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()
	// httptest listens on 127.0.0.1, which is not a public hostname; the
	// origin the adapter checks is spelled as one and the client is pointed
	// at the server by a transport that ignores the host.
	a := &Adapter{HTTP: &http.Client{Transport: rewriteTo(srv.URL)}, Resolver: fakeDNS{}}
	origin := "http://app.example.com"

	if m, err := a.ProveOwnership(context.Background(), origin, "abc"); err != nil || m != "" {
		t.Fatalf("nothing placed: %q %v", m, err)
	}
	body = "something else\n"
	if m, _ := a.ProveOwnership(context.Background(), origin, "abc"); m != "" {
		t.Fatalf("wrong body proved: %q", m)
	}
	body = "vig-verify=abc\n"
	if m, _ := a.ProveOwnership(context.Background(), origin, "abc"); m != ProofWellKnown {
		t.Fatalf("file: %q", m)
	}
	body = ""
	a.Resolver = fakeDNS{"_vig.app.example.com": {"other", " vig-verify=abc "}}
	if m, _ := a.ProveOwnership(context.Background(), origin, "abc"); m != ProofDNS {
		t.Fatalf("dns: %q", m)
	}
	if m, _ := a.ProveOwnership(context.Background(), origin, "xyz"); m != "" {
		t.Fatalf("another nonce proved: %q", m)
	}
	if _, err := New().ProveOwnership(context.Background(), origin, "abc"); !errors.Is(err, core.ErrNotWired) {
		t.Fatalf("unwired: %v", err)
	}
}

func TestInspectPanel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Server", "demo/1")
		w.Header().Set("Strict-Transport-Security", "max-age=1")
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()
	a := &Adapter{HTTP: &http.Client{Transport: rewriteTo(srv.URL)}}
	f, err := a.Inspect(context.Background(), "http://app.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if !f.Reachable || f.Status != 200 || f.Server != "demo/1" || !f.HSTS || f.FinalURL != "" {
		t.Errorf("%+v", f)
	}
	p := Panel(f, false)
	rows := map[string]core.PanelRow{}
	for _, r := range p.Rows {
		rows[r.Key] = r
	}
	if rows[RowSite].State != core.BadgeOK || rows[RowScheme].Value != "http" || rows[RowScheme].State != core.BadgeWarn || rows[RowHSTS].State != core.BadgeOK {
		t.Errorf("%+v", p.Rows)
	}
	if rows[RowOwnership].Action != "prove_ownership" || rows[RowOwnership].State != core.BadgeNeutral {
		t.Errorf("ownership row %+v", rows[RowOwnership])
	}
	if Panel(f, true).Rows[len(RowOrder)-1].Value != "proven" {
		t.Error("proven row")
	}
	down := Panel(Facts{Origin: "https://x.example.com", Err: "dial"}, false)
	if down.Rows[0].Value != "unreachable" || down.Rows[0].State != core.BadgeWarn {
		t.Errorf("unreachable: %+v", down.Rows[0])
	}
}

func TestRefuseInternal(t *testing.T) {
	refused := []string{"127.0.0.1:80", "10.1.2.3:443", "192.168.0.1:80", "172.16.5.5:80", "169.254.169.254:80", "100.64.0.1:80", "[::1]:443", "[fe80::1]:80", "0.0.0.0:80"}
	for _, a := range refused {
		if err := refuseInternal("tcp", a, nil); err == nil {
			t.Errorf("%s should be refused", a)
		}
	}
	allowed := []string{"93.184.216.34:443", "[2606:2800:21f:cb07:6820:80da:af6b:8b2c]:443", "1.1.1.1:80"}
	for _, a := range allowed {
		if err := refuseInternal("tcp", a, nil); err != nil {
			t.Errorf("%s should be allowed: %v", a, err)
		}
	}
}

// rewriteTo sends every request to one test server, whatever host it names.
type rewriteFn func(*http.Request) (*http.Response, error)

func (f rewriteFn) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func rewriteTo(base string) http.RoundTripper {
	return rewriteFn(func(r *http.Request) (*http.Response, error) {
		u := *r.URL
		u.Scheme = "http"
		u.Host = strings.TrimPrefix(base, "http://")
		r2 := r.Clone(r.Context())
		r2.URL = &u
		r2.Host = u.Host
		resp, err := http.DefaultTransport.RoundTrip(r2)
		if resp != nil {
			// The client reads where it landed from the response's request;
			// the rewrite is the transport's business, not a redirect.
			resp.Request = r
		}
		return resp, err
	})
}
