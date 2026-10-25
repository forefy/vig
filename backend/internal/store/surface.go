package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/forefy/vig/backend/internal/core"
)

// SurfaceState is where the map of an audit stands.
type SurfaceState string

const (
	SurfacePending SurfaceState = "pending"
	SurfaceReady   SurfaceState = "ready"
	SurfaceFailed  SurfaceState = "failed"
)

// Attempts are spaced ten minutes times the attempt count, so a chain that
// is unreachable for an hour costs a handful of tries, not the audit's map.
const surfaceMaxAttempts = 12

// SaveSurface stores or replaces an audit's map.
func (s *Store) SaveSurface(ctx context.Context, auditID string, doc core.Surface) error {
	b, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	_, err = s.Pool.Exec(ctx, `insert into audit_surfaces(audit_id, doc, built_at, updated_at) values($1,$2,$3,now())
		on conflict (audit_id) do update set doc=excluded.doc, updated_at=now()`, auditID, b, doc.BuiltAt)
	return err
}

// GetSurface loads an audit's map.
func (s *Store) GetSurface(ctx context.Context, auditID string) (core.Surface, error) {
	if !ValidID(auditID) {
		return core.Surface{}, ErrNotFound
	}
	var b []byte
	if err := s.Pool.QueryRow(ctx, `select doc from audit_surfaces where audit_id=$1`, auditID).Scan(&b); err != nil {
		return core.Surface{}, wrap(err)
	}
	var doc core.Surface
	return doc, json.Unmarshal(b, &doc)
}

// SurfaceStatus says whether a map exists, is still coming, or gave up.
func (s *Store) SurfaceStatus(ctx context.Context, auditID string) (SurfaceState, string, error) {
	var attempts int
	var errText *string
	var built *time.Time
	err := s.Pool.QueryRow(ctx, `select a.surface_attempts, a.surface_error, s.built_at from audits a left join audit_surfaces s on s.audit_id=a.id where a.id=$1`, auditID).
		Scan(&attempts, &errText, &built)
	if err != nil {
		return "", "", wrap(err)
	}
	if built != nil {
		return SurfaceReady, "", nil
	}
	if attempts >= surfaceMaxAttempts {
		msg := ""
		if errText != nil {
			msg = *errText
		}
		return SurfaceFailed, msg, nil
	}
	return SurfacePending, "", nil
}

// AddSurfaceEvidence appends observations. A signature or URL already
// recorded on the same node is not duplicated: a system row (a sampled
// transaction) takes the newer summary, since a rebuilt sample knows
// better than the last one; a person's row is never overwritten.
func (s *Store) AddSurfaceEvidence(ctx context.Context, auditID string, items []core.SurfaceEvidence) (int, error) {
	n := 0
	err := s.Tx(ctx, func(tx pgx.Tx) error {
		for _, ev := range items {
			if ev.NodeID == "" {
				continue
			}
			summary, _ := json.Marshal(ev.Summary)
			if ev.Summary == nil {
				summary = []byte("{}")
			}
			actor := ev.Actor
			if actor == "" {
				actor = "system"
			}
			var inserted bool
			err := tx.QueryRow(ctx, `insert into surface_evidence(id, audit_id, node_id, kind, ref, actor, actor_id, at, summary) values($1,$2,$3,$4,$5,$6,$7,$8,$9)
				on conflict (audit_id, node_id, ref) where ref <> '' do update set summary=excluded.summary, at=coalesce(excluded.at, surface_evidence.at)
				where surface_evidence.actor = 'system' and surface_evidence.kind = 'tx' and excluded.actor = 'system'
				returning (xmax = 0)`,
				NewID(), auditID, ev.NodeID, ev.Kind, ev.Ref, actor, nullStr(ev.ID), ev.At, summary).Scan(&inserted)
			if errors.Is(err, pgx.ErrNoRows) {
				continue // a row a person wrote, or one nothing changes
			}
			if err != nil {
				return err
			}
			if inserted {
				n++
			}
		}
		return nil
	})
	return n, err
}

// ListSurfaceEvidence returns an audit's sampled and operator observations,
// newest first. Live rows are listed by ListLiveEvidence.
func (s *Store) ListSurfaceEvidence(ctx context.Context, auditID string, limit int) ([]core.SurfaceEvidence, error) {
	rows, err := s.Pool.Query(ctx, `select id, node_id, kind, ref, actor, at, summary, created_at from surface_evidence where audit_id=$1 and not live order by coalesce(at, created_at) desc, id desc limit $2`, auditID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []core.SurfaceEvidence{}
	for rows.Next() {
		var ev core.SurfaceEvidence
		var summary []byte
		if err := rows.Scan(&ev.ID, &ev.NodeID, &ev.Kind, &ev.Ref, &ev.Actor, &ev.At, &summary, &ev.CreatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(summary, &ev.Summary)
		out = append(out, ev)
	}
	return out, rows.Err()
}

// CountSurfaceEvidence returns how many observations an audit holds.
func (s *Store) CountSurfaceEvidence(ctx context.Context, auditID string) (int, error) {
	var n int
	err := s.Pool.QueryRow(ctx, `select count(*) from surface_evidence where audit_id=$1 and not live`, auditID).Scan(&n)
	return n, err
}

// ClaimSurfaceWork takes the oldest audit that has no map yet, is due for
// another try and has not exhausted its attempts, counting this one.
// Failed audits are never mapped.
func (s *Store) ClaimSurfaceWork(ctx context.Context) (Audit, bool, error) {
	var id string
	err := s.Tx(ctx, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `select a.id from audits a
			where a.state <> 'failed' and a.surface_attempts < $1
			  and (a.surface_tried_at is null or a.surface_tried_at < now() - interval '10 minutes' * a.surface_attempts)
			  and not exists (select 1 from audit_surfaces s where s.audit_id=a.id)
			order by a.created_at limit 1 for update skip locked`, surfaceMaxAttempts).Scan(&id)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `update audits set surface_attempts=surface_attempts+1, surface_tried_at=now() where id=$1`, id)
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Audit{}, false, nil
	}
	if err != nil {
		return Audit{}, false, err
	}
	a, err := s.GetAudit(ctx, id)
	return a, err == nil, err
}

// SetSurfaceError records why the last mapping attempt failed.
func (s *Store) SetSurfaceError(ctx context.Context, auditID, msg string) error {
	_, err := s.Pool.Exec(ctx, `update audits set surface_error=$2 where id=$1`, auditID, msg)
	return err
}

// ListSurfaceAudits returns the audits that have a map, newest first; a
// user id limits them to that owner's, an empty one takes every audit.
func (s *Store) ListSurfaceAudits(ctx context.Context, userID string) ([]Audit, error) {
	where := `where a.hidden_at is null and exists (select 1 from audit_surfaces s where s.audit_id=a.id) order by a.created_at desc`
	if userID != "" {
		return s.listAudits(ctx, `where a.user_id=$1 and a.hidden_at is null and exists (select 1 from audit_surfaces s where s.audit_id=a.id) order by a.created_at desc`, userID)
	}
	return s.listAudits(ctx, where)
}
