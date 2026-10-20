package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/forefy/vig/backend/internal/core"
)

// Scope is the files and lines an audit covers.
type Scope struct {
	Files []string `json:"files"`
	Lines int      `json:"lines"`
}

// Audit is an audits row joined with the target repo.
type Audit struct {
	ID            string
	UserID        string
	TargetID      string
	Repo          string
	Adapter       string
	Coverage      string
	BaseSHA       string
	HeadSHA       string
	ProgramIDs    []string
	Scope         Scope
	State         core.AuditState
	ResumeState   core.AuditState
	ETAAt         *time.Time
	DelayedReason string
	FailedReason  string
	CostKind      string
	LedgerEntryID string
	SlotPlanID    string
	SlotPrevRunAt *time.Time
	ReportID      string
	ClaimedAt     *time.Time
	CreatedAt     time.Time
	UpdatedAt     time.Time
	// HiddenAt is set when an admin took the audit off its owner's list.
	HiddenAt     *time.Time
	HiddenReason string
}

// AuditEvent is one timeline row.
type AuditEvent struct {
	ID        string
	AuditID   string
	Kind      string
	Actor     string
	ActorID   string
	State     core.AuditState
	Payload   map[string]any
	CreatedAt time.Time
}

// OperatorToken is a job-scoped bearer token row (hash only).
type OperatorToken struct {
	ID        string
	AuditID   string
	ExpiresAt time.Time
	UsedAt    *time.Time
	RevokedAt *time.Time
	CreatedBy string
}

// ErrNoSlot is returned when a Pro user's daily slot is not available yet.
type ErrNoSlot struct{ NextAt time.Time }

func (e ErrNoSlot) Error() string { return "next slot at " + e.NextAt.UTC().Format(time.RFC3339) }

// ErrBadTransition wraps a state machine refusal.
type ErrBadTransition struct{ Err error }

func (e ErrBadTransition) Error() string { return e.Err.Error() }

const auditCols = `a.id, a.user_id, a.target_id, t.repo_full_name, t.adapter, a.coverage, coalesce(a.base_sha,''), a.head_sha, a.programs, a.scope, a.state, coalesce(a.resume_state,''), a.eta_at,
	coalesce(a.delayed_reason,''), coalesce(a.failed_reason,''), a.cost_kind, coalesce(a.ledger_entry_id::text,''), coalesce(a.slot_plan_id::text,''), a.slot_prev_run_at, coalesce(a.report_id::text,''), a.claimed_at, a.created_at, a.updated_at, a.hidden_at, coalesce(a.hidden_reason,'')`

const auditFrom = ` from audits a join targets t on t.id=a.target_id `

func scanAudit(row pgx.Row) (Audit, error) {
	var a Audit
	var programs, scope []byte
	err := row.Scan(&a.ID, &a.UserID, &a.TargetID, &a.Repo, &a.Adapter, &a.Coverage, &a.BaseSHA, &a.HeadSHA, &programs, &scope, &a.State, &a.ResumeState, &a.ETAAt,
		&a.DelayedReason, &a.FailedReason, &a.CostKind, &a.LedgerEntryID, &a.SlotPlanID, &a.SlotPrevRunAt, &a.ReportID, &a.ClaimedAt, &a.CreatedAt, &a.UpdatedAt, &a.HiddenAt, &a.HiddenReason)
	if err != nil {
		return a, wrap(err)
	}
	_ = json.Unmarshal(programs, &a.ProgramIDs)
	_ = json.Unmarshal(scope, &a.Scope)
	if a.ProgramIDs == nil {
		a.ProgramIDs = []string{}
	}
	if a.Scope.Files == nil {
		a.Scope.Files = []string{}
	}
	return a, nil
}

func programsJSON(ids []string) []byte {
	sorted := append([]string(nil), ids...)
	sort.Strings(sorted)
	b, _ := json.Marshal(sorted)
	return b
}

// GetAudit fetches an audit.
func (s *Store) GetAudit(ctx context.Context, id string) (Audit, error) {
	if !ValidID(id) {
		return Audit{}, ErrNotFound
	}
	return scanAudit(s.Pool.QueryRow(ctx, `select `+auditCols+auditFrom+`where a.id=$1`, id))
}

func getAuditLocked(ctx context.Context, tx pgx.Tx, id string) (Audit, error) {
	if !ValidID(id) {
		return Audit{}, ErrNotFound
	}
	if _, err := tx.Exec(ctx, `select 1 from audits where id=$1 for update`, id); err != nil {
		return Audit{}, err
	}
	return scanAudit(tx.QueryRow(ctx, `select `+auditCols+auditFrom+`where a.id=$1`, id))
}

func (s *Store) listAudits(ctx context.Context, where string, args ...any) ([]Audit, error) {
	rows, err := s.Pool.Query(ctx, `select `+auditCols+auditFrom+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Audit{}
	for rows.Next() {
		a, err := scanAudit(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ListAudits returns a user's audits, newest first.
func (s *Store) ListAudits(ctx context.Context, userID string) ([]Audit, error) {
	return s.listAudits(ctx, `where a.user_id=$1 and a.hidden_at is null order by a.created_at desc`, userID)
}

// ListQueue returns every audit that is not terminal, oldest first.
func (s *Store) ListQueue(ctx context.Context) ([]Audit, error) {
	return s.listAudits(ctx, `where a.state not in ('delivered','failed','cancelled') order by a.created_at`)
}

// CountAudits counts a user's audits.
func (s *Store) CountAudits(ctx context.Context, userID string) (int, error) {
	var n int
	err := s.Pool.QueryRow(ctx, `select count(*) from audits where user_id=$1`, userID).Scan(&n)
	return n, err
}

// FindDuplicate returns a non-failed audit by the same user with the same
// commit, base and program set.
func (s *Store) FindDuplicate(ctx context.Context, userID, headSHA, baseSHA string, programIDs []string) (Audit, error) {
	return s.findDuplicate(ctx, s.Pool, userID, headSHA, baseSHA, programIDs)
}

func (s *Store) findDuplicate(ctx context.Context, db DB, userID, headSHA, baseSHA string, programIDs []string) (Audit, error) {
	return scanAudit(db.QueryRow(ctx, `select `+auditCols+auditFrom+
		`where a.user_id=$1 and a.head_sha=$2 and coalesce(a.base_sha,'')=$3 and a.programs=$4::jsonb and a.state not in ('failed','cancelled') order by a.created_at desc limit 1`,
		userID, headSHA, baseSHA, programsJSON(programIDs)))
}

// QueueAudit consumes today's Pro slot or a credit and inserts the audit in
// state queued with its first event, atomically. The duplicate check runs
// inside the transaction with the user row locked, so two identical requests
// cannot both pay: the second returns the first audit with created=false.
//
// Cost order: an active plan with an available slot pays with the slot; else
// a credit; else the caller gets ErrNoSlot (plan, slot used) or
// core.ErrInsufficientCredits (no plan, no credit).
func (s *Store) QueueAudit(ctx context.Context, a Audit) (Audit, bool, error) {
	id := NewID()
	created := false
	err := s.Tx(ctx, func(tx pgx.Tx) error {
		// Serialize per user so two concurrent requests cannot both consume.
		if _, err := tx.Exec(ctx, `select 1 from users where id=$1 for update`, a.UserID); err != nil {
			return err
		}
		if existing, err := s.findDuplicate(ctx, tx, a.UserID, a.HeadSHA, a.BaseSHA, a.ProgramIDs); err == nil {
			id = existing.ID
			return nil
		} else if !errors.Is(err, ErrNotFound) {
			return err
		}
		now := time.Now()
		costKind := ""
		var ledgerID, slotPlanID string
		var slotPrev *time.Time
		var noSlot error
		plan, err := s.ActivePlan(ctx, tx, a.UserID)
		switch {
		case err == nil:
			var last time.Time
			if plan.LastRunAt != nil {
				last = *plan.LastRunAt
			}
			if next := core.NextSlot(last, now); next.After(now) {
				noSlot = ErrNoSlot{NextAt: next}
			} else {
				costKind = "slot"
				slotPlanID = plan.ID
				slotPrev = plan.LastRunAt
				if _, err := tx.Exec(ctx, `update plans set last_run_at=$2 where id=$1`, plan.ID, now); err != nil {
					return err
				}
			}
		case errors.Is(err, ErrNotFound):
		default:
			return err
		}
		if costKind == "" {
			balance, err := s.Balance(ctx, tx, a.UserID)
			if err != nil {
				return err
			}
			if balance < 1 {
				if noSlot != nil {
					return noSlot
				}
				return core.ErrInsufficientCredits
			}
			row, err := s.AppendLedger(ctx, tx, LedgerRow{UserID: a.UserID, Kind: core.LedgerConsume, Delta: -1, RefKind: "audit", RefID: id})
			if err != nil {
				return err
			}
			costKind = "credit"
			ledgerID = row.ID
		}
		scope, _ := json.Marshal(a.Scope)
		eta := now.Add(24 * time.Hour)
		_, err = tx.Exec(ctx, `insert into audits(id, user_id, target_id, coverage, base_sha, head_sha, programs, scope, state, eta_at, cost_kind, ledger_entry_id, slot_plan_id, slot_prev_run_at)
			values($1,$2,$3,$4,$5,$6,$7,$8,'queued',$9,$10,$11,$12,$13)`,
			id, a.UserID, a.TargetID, a.Coverage, nullStr(a.BaseSHA), a.HeadSHA, programsJSON(a.ProgramIDs), scope, eta, costKind, nullStr(ledgerID), nullStr(slotPlanID), slotPrev)
		if err != nil {
			return err
		}
		created = true
		return s.AddEvent(ctx, tx, AuditEvent{AuditID: id, Kind: "queued", Actor: "user", ActorID: a.UserID, State: core.StateQueued,
			Payload: map[string]any{"cost_kind": costKind, "eta_at": eta.UTC().Format(time.RFC3339)}})
	})
	if err != nil {
		return Audit{}, false, err
	}
	out, err := s.GetAudit(ctx, id)
	return out, created, err
}

// CountActiveAuditsForClone counts non-terminal audits, other than exclude,
// that read the same repository checkout. The clone is shared between them,
// so it may only be removed when this returns zero.
func (s *Store) CountActiveAuditsForClone(ctx context.Context, repo, headSHA, excludeID string) (int, error) {
	var n int
	err := s.Pool.QueryRow(ctx, `select count(*) from audits a join targets t on t.id=a.target_id
		where t.repo_full_name=$1 and a.head_sha=$2 and a.id<>$3 and a.state not in ('delivered','failed','cancelled')`, repo, headSHA, excludeID).Scan(&n)
	return n, err
}

// ListOverdue returns non-terminal audits whose ETA passed more than grace
// ago and that have not yet been marked overdue for that ETA.
func (s *Store) ListOverdue(ctx context.Context, grace time.Duration) ([]Audit, error) {
	return s.listAudits(ctx, `where a.state not in ('delivered','failed','cancelled') and a.eta_at is not null and a.eta_at < now() - $1::interval
		and not exists (select 1 from audit_events e where e.audit_id=a.id and e.kind='overdue' and (e.payload->>'eta_at')::timestamptz = a.eta_at)
		order by a.eta_at`, grace)
}

// MarkOverdue records the overdue event for the audit's current ETA so the
// sweep fires once per ETA.
func (s *Store) MarkOverdue(ctx context.Context, a Audit) error {
	if a.ETAAt == nil {
		return nil
	}
	return s.AddEvent(ctx, s.Pool, AuditEvent{AuditID: a.ID, Kind: "overdue", Actor: "system", State: a.State,
		Payload: map[string]any{"eta_at": a.ETAAt.UTC().Format(time.RFC3339Nano)}})
}

// AddEvent appends a timeline row.
func (s *Store) AddEvent(ctx context.Context, db DB, ev AuditEvent) error {
	if ev.Payload == nil {
		ev.Payload = map[string]any{}
	}
	payload, _ := json.Marshal(ev.Payload)
	_, err := db.Exec(ctx, `insert into audit_events(id, audit_id, kind, actor, actor_id, state, payload) values($1,$2,$3,$4,$5,$6,$7)`,
		NewID(), ev.AuditID, ev.Kind, ev.Actor, nullStr(ev.ActorID), nullStr(string(ev.State)), payload)
	return err
}

// ListEvents returns an audit's timeline in order.
func (s *Store) ListEvents(ctx context.Context, auditID string) ([]AuditEvent, error) {
	rows, err := s.Pool.Query(ctx, `select id, audit_id, kind, actor, coalesce(actor_id::text,''), coalesce(state,''), payload, created_at from audit_events where audit_id=$1 order by created_at, id`, auditID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AuditEvent{}
	for rows.Next() {
		var ev AuditEvent
		var payload []byte
		if err := rows.Scan(&ev.ID, &ev.AuditID, &ev.Kind, &ev.Actor, &ev.ActorID, &ev.State, &payload, &ev.CreatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(payload, &ev.Payload)
		out = append(out, ev)
	}
	return out, rows.Err()
}

// Advance moves an audit one step along the pipeline (or back out of
// delayed) after checking the state machine. Delivered requires a report and
// revokes the job's operator tokens.
func (s *Store) Advance(ctx context.Context, id string, to core.AuditState, actor, actorID string, payload map[string]any) (Audit, error) {
	err := s.Tx(ctx, func(tx pgx.Tx) error {
		a, err := getAuditLocked(ctx, tx, id)
		if err != nil {
			return err
		}
		if err := core.CanTransition(core.Transition{From: a.State, To: to, Resume: a.ResumeState}); err != nil {
			return ErrBadTransition{Err: err}
		}
		if to == core.StateDelivered && a.ReportID == "" {
			return ErrBadTransition{Err: fmt.Errorf("delivered requires a report")}
		}
		set := `state=$2, resume_state=null, delayed_reason=null, updated_at=now()`
		if to == core.StateClaimed {
			set += `, claimed_at=now()`
		}
		if _, err := tx.Exec(ctx, `update audits set `+set+` where id=$1`, id, string(to)); err != nil {
			return err
		}
		if to == core.StateDelivered {
			if _, err := tx.Exec(ctx, `update operator_tokens set revoked_at=now() where audit_id=$1 and revoked_at is null`, id); err != nil {
				return err
			}
		}
		return s.AddEvent(ctx, tx, AuditEvent{AuditID: id, Kind: string(to), Actor: actor, ActorID: actorID, State: to, Payload: payload})
	})
	if err != nil {
		return Audit{}, err
	}
	return s.GetAudit(ctx, id)
}

// DelayAudit parks the audit with a new ETA and remembers where to resume.
func (s *Store) DelayAudit(ctx context.Context, id string, eta time.Time, reason, actor, actorID string) (Audit, error) {
	err := s.Tx(ctx, func(tx pgx.Tx) error {
		a, err := getAuditLocked(ctx, tx, id)
		if err != nil {
			return err
		}
		if err := core.CanTransition(core.Transition{From: a.State, To: core.StateDelayed}); err != nil {
			return ErrBadTransition{Err: err}
		}
		if _, err := tx.Exec(ctx, `update audits set state='delayed', resume_state=$2, eta_at=$3, delayed_reason=$4, updated_at=now() where id=$1`, id, string(a.State), eta, reason); err != nil {
			return err
		}
		return s.AddEvent(ctx, tx, AuditEvent{AuditID: id, Kind: "delayed", Actor: actor, ActorID: actorID, State: core.StateDelayed,
			Payload: map[string]any{"eta_at": eta.UTC().Format(time.RFC3339), "reason": reason, "resume_state": string(a.State)}})
	})
	if err != nil {
		return Audit{}, err
	}
	return s.GetAudit(ctx, id)
}

// FailAudit ends the audit, refunds the credit or restores the slot and
// revokes operator tokens.
func (s *Store) FailAudit(ctx context.Context, id, reason, actor, actorID string) (Audit, error) {
	err := s.Tx(ctx, func(tx pgx.Tx) error {
		a, err := getAuditLocked(ctx, tx, id)
		if err != nil {
			return err
		}
		if err := core.CanTransition(core.Transition{From: a.State, To: core.StateFailed}); err != nil {
			return ErrBadTransition{Err: err}
		}
		if _, err := tx.Exec(ctx, `select 1 from users where id=$1 for update`, a.UserID); err != nil {
			return err
		}
		payload := map[string]any{"reason": reason}
		if a.CostKind == "credit" && a.LedgerEntryID != "" {
			row, err := s.AppendLedger(ctx, tx, LedgerRow{UserID: a.UserID, Kind: core.LedgerRefund, Delta: 1, RefKind: "audit", RefID: id, Reason: "audit failed"})
			if err != nil {
				return err
			}
			payload["refund_ledger_id"] = row.ID
		} else if a.CostKind == "slot" && a.SlotPlanID != "" {
			if _, err := tx.Exec(ctx, `update plans set last_run_at=$2 where id=$1`, a.SlotPlanID, a.SlotPrevRunAt); err != nil {
				return err
			}
			payload["slot_restored"] = true
		}
		if _, err := tx.Exec(ctx, `update audits set state='failed', failed_reason=$2, resume_state=null, updated_at=now() where id=$1`, id, reason); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `update operator_tokens set revoked_at=now() where audit_id=$1 and revoked_at is null`, id); err != nil {
			return err
		}
		return s.AddEvent(ctx, tx, AuditEvent{AuditID: id, Kind: "failed", Actor: actor, ActorID: actorID, State: core.StateFailed, Payload: payload})
	})
	if err != nil {
		return Audit{}, err
	}
	return s.GetAudit(ctx, id)
}

// AuditFilter narrows the admin's list of every audit.
type AuditFilter struct {
	State   string
	UserID  string
	Adapter string
	Hidden  *bool  // nil: both
	Query   string // repo substring
	Limit   int
}

// ListAllAudits is the admin's view across users, newest first.
func (s *Store) ListAllAudits(ctx context.Context, f AuditFilter) ([]Audit, error) {
	where := []string{"true"}
	args := []any{}
	add := func(cond string, v any) {
		args = append(args, v)
		where = append(where, fmt.Sprintf(cond, len(args)))
	}
	if f.State != "" {
		add("a.state=$%d", f.State)
	}
	if f.UserID != "" && ValidID(f.UserID) {
		add("a.user_id=$%d", f.UserID)
	}
	if f.Adapter != "" {
		add("t.adapter=$%d", f.Adapter)
	}
	if f.Hidden != nil {
		if *f.Hidden {
			where = append(where, "a.hidden_at is not null")
		} else {
			where = append(where, "a.hidden_at is null")
		}
	}
	if q := strings.TrimSpace(f.Query); q != "" {
		add("t.repo_full_name ilike $%d", "%"+q+"%")
	}
	limit := f.Limit
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	args = append(args, limit)
	return s.listAudits(ctx, `where `+strings.Join(where, " and ")+fmt.Sprintf(` order by a.created_at desc limit $%d`, len(args)), args...)
}

// CancelAudit is an admin's decision from any state but failed or
// cancelled: the credit or slot comes back unless it already did, operator
// tokens are revoked, a public report goes private, and hide takes the
// audit off its owner's list in the same transaction.
func (s *Store) CancelAudit(ctx context.Context, id, reason, adminID string, hide bool) (Audit, error) {
	err := s.Tx(ctx, func(tx pgx.Tx) error {
		a, err := getAuditLocked(ctx, tx, id)
		if err != nil {
			return err
		}
		if a.State.Refunded() {
			return ErrBadTransition{Err: fmt.Errorf("audit is %s", a.State)}
		}
		if _, err := tx.Exec(ctx, `select 1 from users where id=$1 for update`, a.UserID); err != nil {
			return err
		}
		payload := map[string]any{"reason": reason, "from": string(a.State)}
		if a.CostKind == "credit" && a.LedgerEntryID != "" {
			row, err := s.AppendLedger(ctx, tx, LedgerRow{UserID: a.UserID, Kind: core.LedgerRefund, Delta: 1, RefKind: "audit", RefID: id, Reason: "audit cancelled"})
			if err != nil {
				return err
			}
			payload["refund_ledger_id"] = row.ID
		} else if a.CostKind == "slot" && a.SlotPlanID != "" {
			if _, err := tx.Exec(ctx, `update plans set last_run_at=$2 where id=$1`, a.SlotPlanID, a.SlotPrevRunAt); err != nil {
				return err
			}
			payload["slot_restored"] = true
		}
		if _, err := tx.Exec(ctx, `update audits set state='cancelled', failed_reason=$2, resume_state=null, updated_at=now() where id=$1`, id, reason); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `update operator_tokens set revoked_at=now() where audit_id=$1 and revoked_at is null`, id); err != nil {
			return err
		}
		if a.ReportID != "" {
			if _, err := tx.Exec(ctx, `update reports set published_at=null where id=$1`, a.ReportID); err != nil {
				return err
			}
		}
		if err := s.AddEvent(ctx, tx, AuditEvent{AuditID: id, Kind: "cancelled", Actor: "system", ActorID: adminID, State: core.StateCancelled, Payload: payload}); err != nil {
			return err
		}
		if hide {
			return s.setHidden(ctx, tx, id, true, reason, adminID, core.StateCancelled)
		}
		return nil
	})
	if err != nil {
		return Audit{}, err
	}
	return s.GetAudit(ctx, id)
}

// SetAuditHidden takes an audit off its owner's list, or puts it back.
func (s *Store) SetAuditHidden(ctx context.Context, id string, hidden bool, reason, adminID string) (Audit, error) {
	err := s.Tx(ctx, func(tx pgx.Tx) error {
		a, err := getAuditLocked(ctx, tx, id)
		if err != nil {
			return err
		}
		if (a.HiddenAt != nil) == hidden {
			return nil
		}
		return s.setHidden(ctx, tx, id, hidden, reason, adminID, a.State)
	})
	if err != nil {
		return Audit{}, err
	}
	return s.GetAudit(ctx, id)
}

func (s *Store) setHidden(ctx context.Context, tx pgx.Tx, id string, hidden bool, reason, adminID string, state core.AuditState) error {
	kind := "hidden"
	if hidden {
		if _, err := tx.Exec(ctx, `update audits set hidden_at=now(), hidden_reason=$2 where id=$1`, id, reason); err != nil {
			return err
		}
	} else {
		kind = "unhidden"
		if _, err := tx.Exec(ctx, `update audits set hidden_at=null, hidden_reason=null where id=$1`, id); err != nil {
			return err
		}
	}
	return s.AddEvent(ctx, tx, AuditEvent{AuditID: id, Kind: kind, Actor: "system", ActorID: adminID, State: state, Payload: map[string]any{"reason": reason}})
}

// Reassign releases a claimed or active job back to the queue (admin only)
// and revokes its operator tokens. This is an administrative reset, not an
// operator transition, so it bypasses the pipeline order.
func (s *Store) Reassign(ctx context.Context, id, adminID, reason string) (Audit, error) {
	err := s.Tx(ctx, func(tx pgx.Tx) error {
		a, err := getAuditLocked(ctx, tx, id)
		if err != nil {
			return err
		}
		if a.State.Terminal() || a.State == core.StateQueued {
			return ErrBadTransition{Err: fmt.Errorf("audit is %s", a.State)}
		}
		if _, err := tx.Exec(ctx, `update audits set state='queued', resume_state=null, delayed_reason=null, claimed_at=null, updated_at=now() where id=$1`, id); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `update operator_tokens set revoked_at=now() where audit_id=$1 and revoked_at is null`, id); err != nil {
			return err
		}
		return s.AddEvent(ctx, tx, AuditEvent{AuditID: id, Kind: "reassigned", Actor: "system", ActorID: adminID, State: core.StateQueued, Payload: map[string]any{"reason": reason, "from": string(a.State)}})
	})
	if err != nil {
		return Audit{}, err
	}
	return s.GetAudit(ctx, id)
}

// ---- operator tokens ----

// CreateOperatorToken stores a token hash. auditID may be empty for a claim
// token that binds to whatever job it claims.
func (s *Store) CreateOperatorToken(ctx context.Context, auditID string, hash []byte, expires time.Time, createdBy string) (OperatorToken, error) {
	id := NewID()
	_, err := s.Pool.Exec(ctx, `insert into operator_tokens(id, audit_id, hash, scope, expires_at, created_by) values($1,$2,$3,'{"claim":true}',$4,$5)`,
		id, nullStr(auditID), hash, expires, nullStr(createdBy))
	if err != nil {
		return OperatorToken{}, err
	}
	return OperatorToken{ID: id, AuditID: auditID, ExpiresAt: expires, CreatedBy: createdBy}, nil
}

// RenewOperatorToken replaces the token on a job that is already claimed:
// every live token for the audit is revoked and the new one is inserted
// already bound, so it skips claim and continues the job. The customer sees
// the swap on the timeline.
func (s *Store) RenewOperatorToken(ctx context.Context, auditID string, hash []byte, expires time.Time, adminID string) (OperatorToken, error) {
	id := NewID()
	err := s.Tx(ctx, func(tx pgx.Tx) error {
		a, err := getAuditLocked(ctx, tx, auditID)
		if err != nil {
			return err
		}
		if a.State.Terminal() || a.State == core.StateQueued {
			return ErrBadTransition{Err: fmt.Errorf("audit is %s", a.State)}
		}
		if _, err := tx.Exec(ctx, `update operator_tokens set revoked_at=now() where audit_id=$1 and revoked_at is null`, auditID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `insert into operator_tokens(id, audit_id, hash, scope, expires_at, used_at, created_by) values($1,$2,$3,'{"claim":false}',$4,now(),$5)`,
			id, auditID, hash, expires, nullStr(adminID)); err != nil {
			return err
		}
		return s.AddEvent(ctx, tx, AuditEvent{AuditID: auditID, Kind: "token_renewed", Actor: "system", ActorID: adminID, State: a.State, Payload: map[string]any{"token_id": id}})
	})
	if err != nil {
		return OperatorToken{}, err
	}
	now := time.Now()
	return OperatorToken{ID: id, AuditID: auditID, ExpiresAt: expires, UsedAt: &now, CreatedBy: adminID}, nil
}

// OperatorTokenByHash resolves a presented token. Expired or revoked tokens
// are not found.
func (s *Store) OperatorTokenByHash(ctx context.Context, hash []byte) (OperatorToken, error) {
	var t OperatorToken
	err := s.Pool.QueryRow(ctx, `select id, coalesce(audit_id::text,''), expires_at, used_at, revoked_at, coalesce(created_by::text,'') from operator_tokens where hash=$1 and revoked_at is null and expires_at > now()`, hash).
		Scan(&t.ID, &t.AuditID, &t.ExpiresAt, &t.UsedAt, &t.RevokedAt, &t.CreatedBy)
	return t, wrap(err)
}

// ClaimJob takes the token's bound audit if it has one, else the oldest
// queued audit, moves it to claimed and binds the token to it. A token can
// claim exactly once.
func (s *Store) ClaimJob(ctx context.Context, tokenID string) (Audit, error) {
	var auditID string
	err := s.Tx(ctx, func(tx pgx.Tx) error {
		var bound *string
		var usedAt *time.Time
		if err := tx.QueryRow(ctx, `select audit_id, used_at from operator_tokens where id=$1 for update`, tokenID).Scan(&bound, &usedAt); err != nil {
			return wrap(err)
		}
		if usedAt != nil {
			return ErrBadTransition{Err: fmt.Errorf("token already used to claim a job")}
		}
		var err error
		if bound != nil {
			err = tx.QueryRow(ctx, `select id from audits where id=$1 and state='queued' for update skip locked`, *bound).Scan(&auditID)
		} else {
			err = tx.QueryRow(ctx, `select id from audits where state='queued' order by created_at limit 1 for update skip locked`).Scan(&auditID)
		}
		if err != nil {
			return wrap(err)
		}
		if _, err := tx.Exec(ctx, `update audits set state='claimed', claimed_at=now(), updated_at=now() where id=$1`, auditID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `update operator_tokens set audit_id=$2, used_at=now() where id=$1`, tokenID, auditID); err != nil {
			return err
		}
		return s.AddEvent(ctx, tx, AuditEvent{AuditID: auditID, Kind: "claimed", Actor: "operator", State: core.StateClaimed, Payload: map[string]any{"token_id": tokenID}})
	})
	if err != nil {
		return Audit{}, err
	}
	return s.GetAudit(ctx, auditID)
}

// BackdateAudit shifts an audit, its timeline and its report into the past.
// Only the seed uses it, to make a dev dataset look lived-in.
func (s *Store) BackdateAudit(ctx context.Context, id string, by time.Duration) error {
	return s.Tx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `update audits set created_at=created_at-$2::interval, updated_at=updated_at-$2::interval, claimed_at=claimed_at-$2::interval, eta_at=eta_at-$2::interval where id=$1`, id, by); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `update audit_events set created_at=created_at-$2::interval where audit_id=$1`, id, by); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `update reports set created_at=created_at-$2::interval, published_at=published_at-$2::interval where audit_id=$1`, id, by)
		return err
	})
}

// CountRows returns the row count of every table the seed writes, for the
// idempotency check.
func (s *Store) CountRows(ctx context.Context) (map[string]int, error) {
	tables := []string{"users", "identities", "api_keys", "targets", "target_programs", "audits", "audit_events", "reports", "findings",
		"purchases", "plans", "ledger", "tweet_claims", "notifications", "operator_tokens", "admin_actions"}
	out := map[string]int{}
	for _, t := range tables {
		var n int
		if err := s.Pool.QueryRow(ctx, `select count(*) from `+t).Scan(&n); err != nil {
			return nil, err
		}
		out[t] = n
	}
	return out, nil
}

// SetAuditState is an admin's override of the pipeline position: the audit
// moves to any happy-path state from any state, the order and the terminal
// rule set aside. Delivered still requires a report. Leaving a refunded
// state pays for the audit again (a new consume row, or today's slot), so
// the ledger and the run agree; leaving delivered takes a public report
// private. Queued releases the claim and every operator token.
func (s *Store) SetAuditState(ctx context.Context, id string, to core.AuditState, reason, adminID string) (Audit, error) {
	if to.Next() == "" && to != core.StateDelivered {
		return Audit{}, ErrBadTransition{Err: fmt.Errorf("%s is not a pipeline state", to)}
	}
	err := s.Tx(ctx, func(tx pgx.Tx) error {
		a, err := getAuditLocked(ctx, tx, id)
		if err != nil {
			return err
		}
		if a.State == to {
			return ErrBadTransition{Err: fmt.Errorf("audit is already %s", to)}
		}
		if to == core.StateDelivered && a.ReportID == "" {
			return ErrBadTransition{Err: fmt.Errorf("delivered requires a report")}
		}
		payload := map[string]any{"reason": reason, "from": string(a.State)}
		set := `state=$2, resume_state=null, delayed_reason=null, failed_reason=null, updated_at=now()`
		if a.State.Refunded() {
			if _, err := tx.Exec(ctx, `select 1 from users where id=$1 for update`, a.UserID); err != nil {
				return err
			}
			switch a.CostKind {
			case "credit":
				balance, err := s.Balance(ctx, tx, a.UserID)
				if err != nil {
					return err
				}
				if balance < 1 {
					return ErrBadTransition{Err: fmt.Errorf("owner has no credit to pay for the audit again")}
				}
				row, err := s.AppendLedger(ctx, tx, LedgerRow{UserID: a.UserID, Kind: core.LedgerConsume, Delta: -1, RefKind: "audit", RefID: id, Reason: "audit reopened"})
				if err != nil {
					return err
				}
				payload["consume_ledger_id"] = row.ID
				if _, err := tx.Exec(ctx, `update audits set ledger_entry_id=$2 where id=$1`, id, row.ID); err != nil {
					return err
				}
			case "slot":
				if a.SlotPlanID != "" {
					if _, err := tx.Exec(ctx, `update plans set last_run_at=now() where id=$1`, a.SlotPlanID); err != nil {
						return err
					}
					payload["slot_taken"] = true
				}
			}
		}
		switch to {
		case core.StateQueued:
			set += `, claimed_at=null`
		case core.StateClaimed:
			set += `, claimed_at=coalesce(claimed_at, now())`
		}
		if _, err := tx.Exec(ctx, `update audits set `+set+` where id=$1`, id, string(to)); err != nil {
			return err
		}
		if to == core.StateQueued || to == core.StateDelivered {
			if _, err := tx.Exec(ctx, `update operator_tokens set revoked_at=now() where audit_id=$1 and revoked_at is null`, id); err != nil {
				return err
			}
		}
		if a.State == core.StateDelivered && a.ReportID != "" {
			if _, err := tx.Exec(ctx, `update reports set published_at=null where id=$1`, a.ReportID); err != nil {
				return err
			}
		}
		return s.AddEvent(ctx, tx, AuditEvent{AuditID: id, Kind: "state_set", Actor: "system", ActorID: adminID, State: to, Payload: payload})
	})
	if err != nil {
		return Audit{}, err
	}
	return s.GetAudit(ctx, id)
}

// Deleted is what a row delete leaves the caller: the audit as it was, how
// much of it went, and whether the cost came back on the way out.
type Deleted struct {
	Audit    Audit
	Events   int
	Reports  int
	Refunded bool
}

// DeleteAudit removes the audit row and everything hanging off it: events,
// reports and findings, operator tokens, the surface. The ledger is
// append-only, so its rows stay and keep naming the audit; a cost still held
// is refunded first so no credit disappears with the row.
func (s *Store) DeleteAudit(ctx context.Context, id, adminID string) (Deleted, error) {
	var out Deleted
	err := s.Tx(ctx, func(tx pgx.Tx) error {
		a, err := getAuditLocked(ctx, tx, id)
		if err != nil {
			return err
		}
		out.Audit = a
		if err := tx.QueryRow(ctx, `select count(*) from audit_events where audit_id=$1`, id).Scan(&out.Events); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `select count(*) from reports where audit_id=$1`, id).Scan(&out.Reports); err != nil {
			return err
		}
		if !a.State.Refunded() {
			if _, err := tx.Exec(ctx, `select 1 from users where id=$1 for update`, a.UserID); err != nil {
				return err
			}
			if a.CostKind == "credit" && a.LedgerEntryID != "" {
				if _, err := s.AppendLedger(ctx, tx, LedgerRow{UserID: a.UserID, Kind: core.LedgerRefund, Delta: 1, RefKind: "audit", RefID: id, Reason: "audit deleted", ActorID: adminID}); err != nil {
					return err
				}
				out.Refunded = true
			} else if a.CostKind == "slot" && a.SlotPlanID != "" {
				if _, err := tx.Exec(ctx, `update plans set last_run_at=$2 where id=$1`, a.SlotPlanID, a.SlotPrevRunAt); err != nil {
					return err
				}
				out.Refunded = true
			}
		}
		_, err = tx.Exec(ctx, `delete from audits where id=$1`, id)
		return err
	})
	if err != nil {
		return Deleted{}, err
	}
	return out, nil
}
