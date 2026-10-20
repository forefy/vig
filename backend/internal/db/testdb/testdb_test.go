package testdb

import (
	"context"
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	code := m.Run()
	Stop()
	os.Exit(code)
}

func TestMigratedStore(t *testing.T) {
	s := New(t)
	var n int
	if err := s.Pool.QueryRow(context.Background(), `select count(*) from users`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("fresh database has %d users", n)
	}
}
