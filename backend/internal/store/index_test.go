package store_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/forefy/vig/backend/internal/core"
	"github.com/forefy/vig/backend/internal/store"
)

func TestIndexRoundTrip(t *testing.T) {
	f := newFixture(t)
	f.grant(2)
	a, _, err := f.s.QueueAudit(f.ctx, f.audit("aaaa1111", "P1"))
	if err != nil {
		t.Fatal(err)
	}
	// A second audit of the owner without an index must stay off the list.
	if _, _, err := f.s.QueueAudit(f.ctx, f.audit("bbbb2222", "P1")); err != nil {
		t.Fatal(err)
	}
	sum := strings.Repeat("ab", 32)
	tables := []core.IndexTable{{Name: "calls", Rows: 7, Bytes: 42, Parts: []core.IndexPart{{File: "a.parquet", Bytes: 10, SHA256: sum}, {File: "b.parquet", Bytes: 32, SHA256: sum}}}}

	if _, err := f.s.GetIndex(f.ctx, a.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("before save: %v, want not found", err)
	}
	saved, err := f.s.SaveIndex(f.ctx, store.AuditIndex{AuditID: a.ID, Tool: "zz", ToolVersion: "0.3", Bytes: 42, Tables: tables})
	if err != nil {
		t.Fatal(err)
	}
	if saved.BuiltAt.IsZero() {
		t.Fatal("built_at not set")
	}
	got, err := f.s.GetIndex(f.ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.AuditID != a.ID || got.Tool != "zz" || got.ToolVersion != "0.3" || got.Bytes != 42 || len(got.Tables) != 1 || got.Tables[0].Rows != 7 || len(got.Tables[0].Parts) != 2 || got.Tables[0].Parts[1].File != "b.parquet" {
		t.Fatalf("round trip: %+v", got)
	}

	// Second save replaces the row.
	replaced, err := f.s.SaveIndex(f.ctx, store.AuditIndex{AuditID: a.ID, Tool: "zz", ToolVersion: "0.4", Bytes: 8, Tables: []core.IndexTable{{Name: "repos", Bytes: 8, Parts: []core.IndexPart{{File: "c.parquet", Bytes: 8, SHA256: sum}}}}})
	if err != nil {
		t.Fatal(err)
	}
	if replaced.BuiltAt.Before(saved.BuiltAt) {
		t.Fatal("built_at went backwards")
	}
	got, err = f.s.GetIndex(f.ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ToolVersion != "0.4" || got.Bytes != 8 || len(got.Tables) != 1 || got.Tables[0].Name != "repos" {
		t.Fatalf("replace: %+v", got)
	}
	var n int
	if err := f.s.Pool.QueryRow(f.ctx, `select count(*) from audit_indexes where audit_id=$1`, a.ID).Scan(&n); err != nil || n != 1 {
		t.Fatalf("rows %d err %v, want 1", n, err)
	}

	// The owner's list carries the indexed audit alone; another user sees none.
	audits, rows, err := f.s.ListIndexedAudits(f.ctx, f.user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(audits) != 1 || audits[0].ID != a.ID || rows[a.ID].ToolVersion != "0.4" {
		t.Fatalf("owner list: %d audits, rows %+v", len(audits), rows)
	}
	bob, err := f.s.CreateUser(f.ctx, f.s.Pool, "bob", "")
	if err != nil {
		t.Fatal(err)
	}
	audits, rows, err = f.s.ListIndexedAudits(f.ctx, bob.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(audits) != 0 || len(rows) != 0 {
		t.Fatalf("other user's list: %d audits", len(audits))
	}

	// Not found: an unknown id, a malformed id, and after delete.
	if _, err := f.s.GetIndex(f.ctx, store.NewID()); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown id: %v", err)
	}
	if _, err := f.s.GetIndex(f.ctx, "not-a-uuid"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("bad id: %v", err)
	}
	if _, err := f.s.SaveIndex(f.ctx, store.AuditIndex{AuditID: "not-a-uuid", Tool: "zz"}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("save with bad id: %v", err)
	}
	if err := f.s.DeleteIndex(f.ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.GetIndex(f.ctx, a.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("after delete: %v", err)
	}
	audits, _, err = f.s.ListIndexedAudits(f.ctx, f.user.ID)
	if err != nil || len(audits) != 0 {
		t.Fatalf("list after delete: %d audits, %v", len(audits), err)
	}
}
