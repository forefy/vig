package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/forefy/vig/backend/internal/core"
)

// Watch is where an audit's live surface stands.
type Watch struct {
	AuditID     string
	State       core.WatchState
	Error       string
	Cursors     map[string]core.Cursor
	Accounts    []core.WatchedAccount
	StartedAt   time.Time
	LastEventAt *time.Time
	LastSlot    uint64
	EndsAt      *time.Time
	UpdatedAt   time.Time
}

// FreeWatchAfterDelivery is how long a delivered audit stays watched
// without an active plan.
const FreeWatchAfterDelivery = 30 * 24 * time.Hour

// LiveEvidenceKept is how long live rows stay before housekeeping removes
// them; the rollups keep their counts.
const LiveEvidenceKept = 7 * 24 * time.Hour

// ListWatchable returns the audits whose surface should be live: mapped,
// not failed, and either not yet delivered, delivered within the free
// window, or owned by a user with an active plan. An ended watch is
// skipped until its end is cleared.
func (s *Store) ListWatchable(ctx context.Context) ([]Audit, error) {
	rows, err := s.Pool.Query(ctx, `select a.id from audits a
		join audit_surfaces sf on sf.audit_id=a.id
		left join surface_watch w on w.audit_id=a.id
		where a.state <> 'failed'
		  and (w.ends_at is null or w.ends_at > now())
		  and (a.state <> 'delivered'
		       or coalesce((select max(e.created_at) from audit_events e where e.audit_id=a.id and e.kind='delivered'), a.updated_at) > now() - $1::interval
		       or exists (select 1 from plans p where p.user_id=a.user_id and p.starts_at <= now() and p.ends_at > now()))
		order by a.created_at`, FreeWatchAfterDelivery)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]Audit, 0, len(ids))
	for _, id := range ids {
		a, err := s.GetAudit(ctx, id)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				continue
			}
			return nil, err
		}
		out = append(out, a)
	}
	return out, nil
}

// GetWatch loads an audit's watch row; ErrNotFound before the first session.
func (s *Store) GetWatch(ctx context.Context, auditID string) (Watch, error) {
	if !ValidID(auditID) {
		return Watch{}, ErrNotFound
	}
	var w Watch
	var errText *string
	var cursors, accounts []byte
	err := s.Pool.QueryRow(ctx, `select audit_id, state, error, cursors, accounts, started_at, last_event_at, last_slot, ends_at, updated_at from surface_watch where audit_id=$1`, auditID).
		Scan(&w.AuditID, &w.State, &errText, &cursors, &accounts, &w.StartedAt, &w.LastEventAt, &w.LastSlot, &w.EndsAt, &w.UpdatedAt)
	if err != nil {
		return Watch{}, wrap(err)
	}
	if errText != nil {
		w.Error = *errText
	}
	_ = json.Unmarshal(cursors, &w.Cursors)
	_ = json.Unmarshal(accounts, &w.Accounts)
	if w.Cursors == nil {
		w.Cursors = map[string]core.Cursor{}
	}
	return w, nil
}

// EnsureWatch creates the watch row of an audit if it has none.
func (s *Store) EnsureWatch(ctx context.Context, auditID string) error {
	_, err := s.Pool.Exec(ctx, `insert into surface_watch(audit_id) values($1) on conflict (audit_id) do nothing`, auditID)
	return err
}

// SetWatchState records a session's state; the error text is kept only
// for the error state.
func (s *Store) SetWatchState(ctx context.Context, auditID string, state core.WatchState, msg string) error {
	var errText *string
	if state == core.WatchError && msg != "" {
		errText = &msg
	}
	_, err := s.Pool.Exec(ctx, `update surface_watch set state=$2, error=$3, updated_at=now() where audit_id=$1`, auditID, string(state), errText)
	return err
}

// SaveWatchCursor records the newest observation of one subject.
func (s *Store) SaveWatchCursor(ctx context.Context, auditID, subject string, c core.Cursor) error {
	b, err := json.Marshal(c)
	if err != nil {
		return err
	}
	_, err = s.Pool.Exec(ctx, `update surface_watch set cursors = cursors || jsonb_build_object($2::text, $3::jsonb),
		last_slot = greatest(last_slot, $4), last_event_at = greatest(coalesce(last_event_at, 'epoch'::timestamptz), $5), updated_at=now() where audit_id=$1`,
		auditID, subject, b, int64(c.Slot), nullTime(c.At))
	return err
}

// SaveWatchAccounts records the addresses a session subscribes to.
func (s *Store) SaveWatchAccounts(ctx context.Context, auditID string, accounts []core.WatchedAccount) error {
	if accounts == nil {
		accounts = []core.WatchedAccount{}
	}
	b, err := json.Marshal(accounts)
	if err != nil {
		return err
	}
	_, err = s.Pool.Exec(ctx, `update surface_watch set accounts=$2, updated_at=now() where audit_id=$1`, auditID, b)
	return err
}

// EndWatch stops an audit's watch at a time; nil resumes it.
func (s *Store) EndWatch(ctx context.Context, auditID string, at *time.Time) error {
	_, err := s.Pool.Exec(ctx, `update surface_watch set ends_at=$2, updated_at=now() where audit_id=$1`, auditID, at)
	return err
}

// AddLiveEvidence adds rows the watch saw. A ref already on its node has
// its summary replaced, so a full read supersedes the log-derived row
// without counting twice; only a new row increments the rollups.
func (s *Store) AddLiveEvidence(ctx context.Context, auditID string, items []core.SurfaceEvidence) (int, error) {
	n := 0
	err := s.Tx(ctx, func(tx pgx.Tx) error {
		for _, ev := range items {
			if ev.NodeID == "" || ev.Ref == "" {
				continue
			}
			summary, _ := json.Marshal(ev.Summary)
			if ev.Summary == nil {
				summary = []byte("{}")
			}
			at := ev.At
			if at == nil {
				now := time.Now().UTC()
				at = &now
			}
			var inserted bool
			err := tx.QueryRow(ctx, `insert into surface_evidence(id, audit_id, node_id, kind, ref, actor, at, summary, live) values($1,$2,$3,$4,$5,'system',$6,$7,true)
				on conflict (audit_id, node_id, ref) where ref <> '' do update set summary=excluded.summary, at=coalesce(surface_evidence.at, excluded.at)
				returning (xmax = 0)`, NewID(), auditID, ev.NodeID, ev.Kind, ev.Ref, at, summary).Scan(&inserted)
			if err != nil {
				return err
			}
			if !inserted || ev.Kind != "tx" {
				continue
			}
			n++
			failed := 0
			if f, _ := ev.Summary["failed"].(bool); f {
				failed = 1
			}
			units := summaryInt(ev.Summary["units"])
			if _, err := tx.Exec(ctx, `insert into surface_rollups(audit_id, node_id, minute, calls, failed, units) values($1,$2,date_trunc('minute',$3::timestamptz),1,$4,$5)
				on conflict (audit_id, node_id, minute) do update set calls=surface_rollups.calls+1, failed=surface_rollups.failed+excluded.failed, units=surface_rollups.units+excluded.units`,
				auditID, ev.NodeID, at, failed, units); err != nil {
				return err
			}
		}
		return nil
	})
	return n, err
}

func summaryInt(v any) int64 {
	switch x := v.(type) {
	case float64:
		return int64(x)
	case int:
		return int64(x)
	case int64:
		return x
	case uint64:
		return int64(x)
	}
	return 0
}

// AddRollup counts calls on a node in its minute without a row.
func (s *Store) AddRollup(ctx context.Context, auditID, nodeID string, at time.Time, calls, failed int, units int64) error {
	_, err := s.Pool.Exec(ctx, `insert into surface_rollups(audit_id, node_id, minute, calls, failed, units) values($1,$2,date_trunc('minute',$3::timestamptz),$4,$5,$6)
		on conflict (audit_id, node_id, minute) do update set calls=surface_rollups.calls+excluded.calls, failed=surface_rollups.failed+excluded.failed, units=surface_rollups.units+excluded.units`,
		auditID, nodeID, at, calls, failed, units)
	return err
}

// ListLiveEvidence returns live rows in id order: those after a cursor,
// or the newest ones when the cursor is empty.
func (s *Store) ListLiveEvidence(ctx context.Context, auditID, after string, limit int) ([]core.SurfaceEvidence, error) {
	var rows pgx.Rows
	var err error
	if after != "" && ValidID(after) {
		rows, err = s.Pool.Query(ctx, `select id, node_id, kind, ref, actor, at, summary, created_at from surface_evidence where audit_id=$1 and live and id > $2 order by id limit $3`, auditID, after, limit)
	} else {
		rows, err = s.Pool.Query(ctx, `select * from (select id, node_id, kind, ref, actor, at, summary, created_at from surface_evidence where audit_id=$1 and live order by id desc limit $2) t order by id`, auditID, limit)
	}
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

// ListLiveEvidenceBefore returns the newest live rows at or before a time,
// oldest first: the window a reader opens when it travels back.
func (s *Store) ListLiveEvidenceBefore(ctx context.Context, auditID string, before time.Time, limit int) ([]core.SurfaceEvidence, error) {
	rows, err := s.Pool.Query(ctx, `select * from (select id, node_id, kind, ref, actor, at, summary, created_at from surface_evidence where audit_id=$1 and live and coalesce(at, created_at) <= $2 order by coalesce(at, created_at) desc, id desc limit $3) t order by coalesce(at, created_at), id`, auditID, before, limit)
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

// ListLiveEvidenceBetween returns live rows after one time and up to
// another, oldest first: what a player crosses as it moves forward.
func (s *Store) ListLiveEvidenceBetween(ctx context.Context, auditID string, since, before time.Time, limit int) ([]core.SurfaceEvidence, error) {
	rows, err := s.Pool.Query(ctx, `select id, node_id, kind, ref, actor, at, summary, created_at from surface_evidence where audit_id=$1 and live and coalesce(at, created_at) > $2 and coalesce(at, created_at) <= $3 order by coalesce(at, created_at), id limit $4`, auditID, since, before, limit)
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

// RollupBuckets sums every node's rollups into buckets of one width, in
// order, for the whole watch.
func (s *Store) RollupBuckets(ctx context.Context, auditID string, bucket time.Duration) ([]core.ActivityBucket, error) {
	rows, err := s.Pool.Query(ctx, `select date_bin($2::interval, minute, 'epoch'::timestamptz) as b, sum(calls)::int, sum(failed)::int from surface_rollups where audit_id=$1 group by b order by b`, auditID, bucket)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []core.ActivityBucket{}
	for rows.Next() {
		var b core.ActivityBucket
		if err := rows.Scan(&b.At, &b.Calls, &b.Failed); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// ListLiveEvidenceFull returns the newest live rows the watch read in
// full, oldest first: the ones that carry keys, signers and arguments.
func (s *Store) ListLiveEvidenceFull(ctx context.Context, auditID string, limit int) ([]core.SurfaceEvidence, error) {
	rows, err := s.Pool.Query(ctx, `select * from (select id, node_id, kind, ref, actor, at, summary, created_at from surface_evidence where audit_id=$1 and live and kind='tx' and summary ? 'keys' order by id desc limit $2) t order by id`, auditID, limit)
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

// PruneLiveEvidence removes live rows older than the keep window. The
// rollups keep their counts.
func (s *Store) PruneLiveEvidence(ctx context.Context, keep time.Duration) (int64, error) {
	tag, err := s.Pool.Exec(ctx, `delete from surface_evidence where live and created_at < now() - $1::interval`, keep)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// AddCoverage opens a span and returns its id.
func (s *Store) AddCoverage(ctx context.Context, auditID string, span core.CoverageSpan) (string, error) {
	id := NewID()
	_, err := s.Pool.Exec(ctx, `insert into surface_coverage(id, audit_id, subject, kind, from_slot, to_slot, from_at, to_at) values($1,$2,$3,$4,$5,$6,$7,$8)`,
		id, auditID, span.Subject, string(span.Kind), int64(span.FromSlot), int64(span.ToSlot), span.FromAt, span.ToAt)
	return id, err
}

// ExtendCoverage moves a span's end forward.
func (s *Store) ExtendCoverage(ctx context.Context, auditID, id string, toSlot uint64, toAt *time.Time) error {
	if !ValidID(id) {
		return ErrNotFound
	}
	_, err := s.Pool.Exec(ctx, `update surface_coverage set to_slot=greatest(to_slot,$3), to_at=coalesce($4, to_at) where audit_id=$1 and id=$2`, auditID, id, int64(toSlot), toAt)
	return err
}

// ListCoverage returns an audit's spans in slot order, the newest limit.
func (s *Store) ListCoverage(ctx context.Context, auditID string, limit int) ([]core.CoverageSpan, error) {
	rows, err := s.Pool.Query(ctx, `select * from (select id, subject, kind, from_slot, to_slot, from_at, to_at from surface_coverage where audit_id=$1 order by from_slot desc limit $2) t order by from_slot`, auditID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []core.CoverageSpan{}
	for rows.Next() {
		var sp core.CoverageSpan
		var from, to int64
		if err := rows.Scan(&sp.ID, &sp.Subject, &sp.Kind, &from, &to, &sp.FromAt, &sp.ToAt); err != nil {
			return nil, err
		}
		sp.FromSlot, sp.ToSlot = uint64(from), uint64(to)
		out = append(out, sp)
	}
	return out, rows.Err()
}

// RollupTotals sums the rollups per node since the watch began.
func (s *Store) RollupTotals(ctx context.Context, auditID string) ([]core.NodeTotal, error) {
	rows, err := s.Pool.Query(ctx, `select node_id, sum(calls), sum(failed), sum(units), max(minute) from surface_rollups where audit_id=$1 group by node_id order by node_id`, auditID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []core.NodeTotal{}
	for rows.Next() {
		var t core.NodeTotal
		var last *time.Time
		if err := rows.Scan(&t.NodeID, &t.Calls, &t.Failed, &t.Units, &last); err != nil {
			return nil, err
		}
		t.LastAt = last
		out = append(out, t)
	}
	return out, rows.Err()
}

// RollupMinutes returns per-node traffic per minute since a time.
func (s *Store) RollupMinutes(ctx context.Context, auditID string, since time.Time) ([]core.NodeMinute, error) {
	rows, err := s.Pool.Query(ctx, `select node_id, minute, calls, failed from surface_rollups where audit_id=$1 and minute >= $2 order by minute, node_id`, auditID, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []core.NodeMinute{}
	for rows.Next() {
		var m core.NodeMinute
		if err := rows.Scan(&m.NodeID, &m.Minute, &m.Calls, &m.Failed); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// UpdateSurface loads the map under a row lock, applies fn and saves it.
// The watch and the operator both grow the map; neither may lose the
// other's additions.
func (s *Store) UpdateSurface(ctx context.Context, auditID string, fn func(doc *core.Surface) bool) error {
	return s.Tx(ctx, func(tx pgx.Tx) error {
		var b []byte
		if err := tx.QueryRow(ctx, `select doc from audit_surfaces where audit_id=$1 for update`, auditID).Scan(&b); err != nil {
			return wrap(err)
		}
		var doc core.Surface
		if err := json.Unmarshal(b, &doc); err != nil {
			return err
		}
		if !fn(&doc) {
			return nil
		}
		out, err := json.Marshal(doc)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `update audit_surfaces set doc=$2, updated_at=now() where audit_id=$1`, auditID, out)
		return err
	})
}

func nullTime(t *time.Time) time.Time {
	if t == nil {
		return time.Now().UTC()
	}
	return *t
}
