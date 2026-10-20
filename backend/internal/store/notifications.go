package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"
)

// Notification is a notifications row.
type Notification struct {
	ID        string
	UserID    string
	Kind      string
	Payload   map[string]any
	Channels  []string
	Delivered map[string]string
	ReadAt    *time.Time
	Attempts  int
	// NextAttemptAt is set while an external channel still waits for the
	// dispatcher; nil once every channel has a result.
	NextAttemptAt *time.Time
	CreatedAt     time.Time
}

// Pending returns the channels that have no delivery result yet.
func (n Notification) Pending() []string {
	var out []string
	for _, c := range n.Channels {
		if _, done := n.Delivered[c]; !done {
			out = append(out, c)
		}
	}
	return out
}

// AdminAction is an admin_actions row.
type AdminAction struct {
	ID          string
	AdminID     string
	Kind        string
	SubjectKind string
	SubjectID   string
	Payload     map[string]any
	CreatedAt   time.Time
}

const notificationCols = `id, user_id, kind, payload, channels, delivered, read_at, attempts, next_attempt_at, created_at`

func scanNotification(row pgx.Row) (Notification, error) {
	var n Notification
	var payload, channels, delivered []byte
	if err := row.Scan(&n.ID, &n.UserID, &n.Kind, &payload, &channels, &delivered, &n.ReadAt, &n.Attempts, &n.NextAttemptAt, &n.CreatedAt); err != nil {
		return n, wrap(err)
	}
	_ = json.Unmarshal(payload, &n.Payload)
	_ = json.Unmarshal(channels, &n.Channels)
	_ = json.Unmarshal(delivered, &n.Delivered)
	if n.Payload == nil {
		n.Payload = map[string]any{}
	}
	if n.Delivered == nil {
		n.Delivered = map[string]string{}
	}
	return n, nil
}

// CreateNotification inserts a notification. In-app delivery is immediate;
// any other channel without a result is queued for the dispatcher.
func (s *Store) CreateNotification(ctx context.Context, n Notification) (Notification, error) {
	n.ID = NewID()
	if n.Payload == nil {
		n.Payload = map[string]any{}
	}
	if n.Channels == nil {
		n.Channels = []string{"inapp"}
	}
	if n.Delivered == nil {
		n.Delivered = map[string]string{}
	}
	for _, c := range n.Channels {
		if c == "inapp" {
			n.Delivered["inapp"] = "ok"
		}
	}
	var next *time.Time
	if len(n.Pending()) > 0 {
		t := time.Now()
		next = &t
	}
	payload, _ := json.Marshal(n.Payload)
	channels, _ := json.Marshal(n.Channels)
	delivered, _ := json.Marshal(n.Delivered)
	err := s.Pool.QueryRow(ctx, `insert into notifications(id, user_id, kind, payload, channels, delivered, next_attempt_at) values($1,$2,$3,$4,$5,$6,$7) returning created_at`,
		n.ID, n.UserID, n.Kind, payload, channels, delivered, next).Scan(&n.CreatedAt)
	n.NextAttemptAt = next
	return n, err
}

// SetDelivered records the outcome for a channel.
func (s *Store) SetDelivered(ctx context.Context, id, channel, result string) error {
	patch, _ := json.Marshal(map[string]string{channel: result})
	_, err := s.Pool.Exec(ctx, `update notifications set delivered = delivered || $2::jsonb where id=$1`, id, patch)
	return err
}

// ClaimDueNotifications takes up to limit notifications whose external
// delivery is due, bumping attempts and pushing next_attempt_at out by hold
// so a crashed dispatcher does not lose them and a second dispatcher does
// not double-send. The caller finishes each with RecordDelivery.
func (s *Store) ClaimDueNotifications(ctx context.Context, limit int, hold time.Duration) ([]Notification, error) {
	rows, err := s.Pool.Query(ctx, `update notifications set attempts=attempts+1, next_attempt_at=now()+$2::interval
		where id in (select id from notifications where next_attempt_at is not null and next_attempt_at <= now() order by next_attempt_at limit $1 for update skip locked)
		returning `+notificationCols, limit, hold)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Notification{}
	for rows.Next() {
		n, err := scanNotification(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// RecordDelivery stores per-channel results and schedules the next attempt,
// or clears the queue marker when next is nil.
func (s *Store) RecordDelivery(ctx context.Context, id string, results map[string]string, next *time.Time) error {
	patch, _ := json.Marshal(results)
	_, err := s.Pool.Exec(ctx, `update notifications set delivered = delivered || $2::jsonb, next_attempt_at=$3 where id=$1`, id, patch, next)
	return err
}

// ListNotifications returns a user's notifications, newest first.
func (s *Store) ListNotifications(ctx context.Context, userID string) ([]Notification, error) {
	rows, err := s.Pool.Query(ctx, `select `+notificationCols+` from notifications where user_id=$1 order by created_at desc limit 200`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Notification{}
	for rows.Next() {
		n, err := scanNotification(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// MarkRead sets read_at on a user's notification.
func (s *Store) MarkRead(ctx context.Context, userID, id string) error {
	if !ValidID(id) {
		return ErrNotFound
	}
	tag, err := s.Pool.Exec(ctx, `update notifications set read_at=coalesce(read_at, now()) where id=$1 and user_id=$2`, id, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// LogAdminAction appends to the admin log.
func (s *Store) LogAdminAction(ctx context.Context, a AdminAction) (AdminAction, error) {
	return s.logAdminAction(ctx, s.Pool, a)
}

func (s *Store) logAdminAction(ctx context.Context, db DB, a AdminAction) (AdminAction, error) {
	if a.ID == "" {
		a.ID = NewID()
	}
	if a.Payload == nil {
		a.Payload = map[string]any{}
	}
	payload, _ := json.Marshal(a.Payload)
	var subject *string
	if ValidID(a.SubjectID) {
		subject = &a.SubjectID
	}
	err := db.QueryRow(ctx, `insert into admin_actions(id, admin_id, kind, subject_kind, subject_id, payload) values($1,$2,$3,$4,$5,$6) returning created_at`,
		a.ID, a.AdminID, a.Kind, a.SubjectKind, subject, payload).Scan(&a.CreatedAt)
	return a, err
}

// ListAdminActions returns the newest admin actions.
func (s *Store) ListAdminActions(ctx context.Context, limit int) ([]AdminAction, error) {
	rows, err := s.Pool.Query(ctx, `select id, admin_id, kind, subject_kind, coalesce(subject_id::text,''), payload, created_at from admin_actions order by created_at desc limit $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AdminAction{}
	for rows.Next() {
		var a AdminAction
		var payload []byte
		if err := rows.Scan(&a.ID, &a.AdminID, &a.Kind, &a.SubjectKind, &a.SubjectID, &payload, &a.CreatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(payload, &a.Payload)
		out = append(out, a)
	}
	return out, rows.Err()
}
