package notify_test

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/forefy/vig/backend/internal/db/testdb"
	"github.com/forefy/vig/backend/internal/notify"
)

func TestMain(m *testing.M) {
	code := m.Run()
	testdb.Stop()
	os.Exit(code)
}

// fakeSender records deliveries and fails while failing is set.
type fakeSender struct {
	name    string
	mu      sync.Mutex
	sent    []string
	failing bool
}

func (f *fakeSender) Name() string     { return f.name }
func (f *fakeSender) Configured() bool { return true }
func (f *fakeSender) Send(_ context.Context, to, _ string, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failing {
		return errors.New("down")
	}
	f.sent = append(f.sent, to)
	return nil
}

func TestNotifyQueuesAndDispatcherDelivers(t *testing.T) {
	ctx := context.Background()
	s := testdb.New(t)
	u, err := s.CreateUser(ctx, s.Pool, "bob", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddIdentity(ctx, s.Pool, u.ID, "discord", "discord-42", "", "bob"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddIdentity(ctx, s.Pool, u.ID, "google", "g-1", "bob@example.com", ""); err != nil {
		t.Fatal(err)
	}
	discord := &fakeSender{name: "discord", failing: true}
	email := &fakeSender{name: "email"}
	n := notify.New(s, discord, email)

	// Notify writes the row and returns without calling any sender.
	n.Notify(ctx, u.ID, "audit_delivered", map[string]any{"audit_id": "a1"})
	rows, err := s.ListNotifications(ctx, u.ID)
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows=%d err=%v", len(rows), err)
	}
	row := rows[0]
	if row.Delivered["inapp"] != "ok" || row.NextAttemptAt == nil || len(row.Pending()) != 2 {
		t.Fatalf("unexpected row: delivered=%v next=%v pending=%v", row.Delivered, row.NextAttemptAt, row.Pending())
	}
	if len(email.sent) != 0 {
		t.Fatal("Notify must not deliver inline")
	}

	d := &notify.Dispatcher{Notifier: n}
	if err := d.Once(ctx); err != nil {
		t.Fatal(err)
	}
	rows, _ = s.ListNotifications(ctx, u.ID)
	row = rows[0]
	if row.Delivered["email"] != "ok" || email.sent[0] != "bob@example.com" {
		t.Fatalf("email not delivered: %v", row.Delivered)
	}
	if _, done := row.Delivered["discord"]; done || row.Attempts != 1 || row.NextAttemptAt == nil || !row.NextAttemptAt.After(time.Now()) {
		t.Fatalf("failed channel should be scheduled for retry: delivered=%v attempts=%d next=%v", row.Delivered, row.Attempts, row.NextAttemptAt)
	}
	// Not due yet: a second pass leaves it alone.
	if err := d.Once(ctx); err != nil {
		t.Fatal(err)
	}
	rows, _ = s.ListNotifications(ctx, u.ID)
	if rows[0].Attempts != 1 {
		t.Fatalf("retried before the backoff elapsed: attempts=%d", rows[0].Attempts)
	}

	// Make it due, recover the channel, and the retry lands.
	if _, err := s.Pool.Exec(ctx, `update notifications set next_attempt_at=now() where id=$1`, row.ID); err != nil {
		t.Fatal(err)
	}
	discord.mu.Lock()
	discord.failing = false
	discord.mu.Unlock()
	if err := d.Once(ctx); err != nil {
		t.Fatal(err)
	}
	rows, _ = s.ListNotifications(ctx, u.ID)
	row = rows[0]
	if row.Delivered["discord"] != "ok" || row.NextAttemptAt != nil || discord.sent[0] != "discord-42" {
		t.Fatalf("retry did not deliver: delivered=%v next=%v", row.Delivered, row.NextAttemptAt)
	}
	if len(email.sent) != 1 {
		t.Fatalf("email delivered twice: %v", email.sent)
	}
}

func TestEmailSkippedWhenPreferenceOff(t *testing.T) {
	ctx := context.Background()
	s := testdb.New(t)
	u, err := s.CreateUser(ctx, s.Pool, "carol", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddIdentity(ctx, s.Pool, u.ID, "google", "g-2", "carol@example.com", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.SetNotifyEmail(ctx, u.ID, false); err != nil {
		t.Fatal(err)
	}
	email := &fakeSender{name: "email"}
	notify.New(s, email).Notify(ctx, u.ID, "credits_granted", map[string]any{"delta": 1})
	rows, _ := s.ListNotifications(ctx, u.ID)
	if len(rows) != 1 || len(rows[0].Channels) != 1 || rows[0].NextAttemptAt != nil {
		t.Fatalf("email channel should not be queued: %+v", rows)
	}
}

func TestGiveUpAfterMaxAttempts(t *testing.T) {
	ctx := context.Background()
	s := testdb.New(t)
	u, err := s.CreateUser(ctx, s.Pool, "dan", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddIdentity(ctx, s.Pool, u.ID, "discord", "discord-7", "", "dan"); err != nil {
		t.Fatal(err)
	}
	discord := &fakeSender{name: "discord", failing: true}
	n := notify.New(s, discord)
	n.Notify(ctx, u.ID, "audit_failed", map[string]any{"audit_id": "a2"})
	d := &notify.Dispatcher{Notifier: n}
	var id string
	for i := 1; i <= notify.MaxAttempts; i++ {
		if err := d.Once(ctx); err != nil {
			t.Fatal(err)
		}
		rows, _ := s.ListNotifications(ctx, u.ID)
		id = rows[0].ID
		if rows[0].Attempts != i {
			t.Fatalf("pass %d: attempts=%d", i, rows[0].Attempts)
		}
		if _, err := s.Pool.Exec(ctx, `update notifications set next_attempt_at=case when next_attempt_at is null then null else now() end where id=$1`, id); err != nil {
			t.Fatal(err)
		}
	}
	rows, _ := s.ListNotifications(ctx, u.ID)
	if rows[0].NextAttemptAt != nil || rows[0].Delivered["discord"] != "failed: down" {
		t.Fatalf("should have given up: next=%v delivered=%v", rows[0].NextAttemptAt, rows[0].Delivered)
	}
}
