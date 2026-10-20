package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// Impersonation is a timed "view as user" grant an admin opened from their
// own session. Live means not ended and not past expires_at.
type Impersonation struct {
	ID           string
	AdminID      string
	SessionID    string
	TargetUserID string
	AuditID      string
	StartedAt    time.Time
	ExpiresAt    time.Time
	EndedAt      *time.Time
}

const impersonationCols = `id, admin_id, session_id, target_user_id, coalesce(audit_id::text,''), started_at, expires_at, ended_at`

func scanImpersonation(row pgx.Row) (Impersonation, error) {
	var i Impersonation
	err := row.Scan(&i.ID, &i.AdminID, &i.SessionID, &i.TargetUserID, &i.AuditID, &i.StartedAt, &i.ExpiresAt, &i.EndedAt)
	return i, wrap(err)
}

// StartImpersonation opens a grant on the admin's session. Any live grant on
// that session ends first, so a session views as at most one user at a time.
func (s *Store) StartImpersonation(ctx context.Context, adminID, sessionID, targetID, auditID string, ttl time.Duration) (Impersonation, error) {
	id := NewID()
	var audit *string
	if auditID != "" {
		audit = &auditID
	}
	err := s.Tx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `update impersonations set ended_at=now() where session_id=$1 and ended_at is null`, sessionID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `insert into impersonations(id, admin_id, session_id, target_user_id, audit_id, expires_at) values($1,$2,$3,$4,$5,$6)`,
			id, adminID, sessionID, targetID, audit, time.Now().Add(ttl))
		return err
	})
	if err != nil {
		return Impersonation{}, wrap(err)
	}
	return s.GetImpersonation(ctx, id)
}

// GetImpersonation fetches one grant.
func (s *Store) GetImpersonation(ctx context.Context, id string) (Impersonation, error) {
	if !ValidID(id) {
		return Impersonation{}, ErrNotFound
	}
	return scanImpersonation(s.Pool.QueryRow(ctx, `select `+impersonationCols+` from impersonations where id=$1`, id))
}

// LiveImpersonation returns the grant currently active on a session, if any.
func (s *Store) LiveImpersonation(ctx context.Context, sessionID string) (Impersonation, bool) {
	i, err := scanImpersonation(s.Pool.QueryRow(ctx,
		`select `+impersonationCols+` from impersonations where session_id=$1 and ended_at is null and expires_at > now() order by started_at desc limit 1`, sessionID))
	return i, err == nil
}

// EndImpersonation closes a live grant. Only the admin who opened it can.
func (s *Store) EndImpersonation(ctx context.Context, id, adminID string) (bool, error) {
	tag, err := s.Pool.Exec(ctx, `update impersonations set ended_at=now() where id=$1 and admin_id=$2 and ended_at is null`, id, adminID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}
