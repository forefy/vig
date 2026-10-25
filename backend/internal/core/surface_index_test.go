package core

import "testing"

func TestIndexNodesCarriesAttentionAndFindings(t *testing.T) {
	s := Surface{Adapter: "webapp", Nodes: []SurfaceNode{
		{ID: "host:a", Kind: "host", Name: "a"},
		{ID: "route:a:/x", Parent: "host:a", Kind: "route", Name: "/x", Facts: map[string]string{"host": "a", "path": "/x", "methods": "GET"}},
		{ID: "route:a:/y", Parent: "host:a", Kind: "route", Name: "/y", Facts: map[string]string{"host": "a", "path": "/y"}},
	}}
	findings := []Finding{{Ordinal: 1, Severity: SeverityHigh, Status: FindingOpen, Title: "x is open", Location: Location{URL: "https://a/x"}}}
	s.Attend([]SurfaceEvidence{{NodeID: "route:a:/y", Kind: "request"}}, findings)
	rows := IndexNodes("audit-1", s, findings)
	if len(rows) != 3 {
		t.Fatalf("rows %d", len(rows))
	}
	x := rows[1]
	if x.AuditID != "audit-1" || x.Attention != AttentionFinding || len(x.Findings) != 1 || x.Findings[0].Title != "x is open" || x.Findings[0].Severity != SeverityHigh {
		t.Fatalf("x: %+v", x)
	}
	if y := rows[2]; y.Attention != AttentionObserved || y.Observed != 1 {
		t.Fatalf("y: %+v", y)
	}
	if h := rows[0]; h.Attention != AttentionFinding {
		t.Fatalf("host carries the strongest child state: %+v", h)
	}
}
