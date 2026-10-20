package store_test

import (
	"context"
	"testing"

	"github.com/forefy/vig/backend/internal/db/testdb"
)

func TestListUserSummariesRuns(t *testing.T) {
	s := testdb.New(t)
	ctx := context.Background()
	u, err := s.CreateUser(ctx, s.Pool, "x", "")
	if err != nil {
		t.Fatal(err)
	}
	rows, err := s.ListUserSummaries(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].User.ID != u.ID || rows[0].Credits != 0 || rows[0].Audits != 0 {
		t.Fatalf("rows %+v", rows)
	}
}
