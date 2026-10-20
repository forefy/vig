package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/forefy/vig/backend/internal/core"
)

// LedgerRow is one credit movement.
type LedgerRow struct {
	ID           string
	UserID       string
	Kind         core.LedgerKind
	Delta        int
	BalanceAfter int
	RefKind      string
	RefID        string
	Reason       string
	ActorID      string
	ExpiresAt    *time.Time
	CreatedAt    time.Time
}

// Plan is a plans row.
type Plan struct {
	ID         string
	UserID     string
	Kind       string
	StartsAt   time.Time
	EndsAt     time.Time
	RunCap     int
	LastRunAt  *time.Time
	PurchaseID string
}

// Purchase is a purchases row.
type Purchase struct {
	ID            string
	UserID        string
	SKU           core.SKU
	AmountUSDC    int64
	MemoNonce     string
	Treasury      string
	USDCMint      string
	State         string
	TxSignature   string
	ConfirmedSlot uint64
	Promo         string
	ExpiresAt     time.Time
	CreatedAt     time.Time
}

// TweetClaim is a tweet_claims row.
type TweetClaim struct {
	ID            string
	UserID        string
	Nonce         string
	URL           string
	State         string
	ReviewedBy    string
	ReviewedAt    *time.Time
	Reason        string
	LedgerEntryID string
	CreatedAt     time.Time
}

// ---- ledger ----

// Balance derives the credit balance from the ledger.
func (s *Store) Balance(ctx context.Context, db DB, userID string) (int, error) {
	var n int
	err := db.QueryRow(ctx, `select coalesce(sum(delta),0) from ledger where user_id=$1`, userID).Scan(&n)
	return n, err
}

// AppendLedger validates the movement with core.ApplyLedger and appends it.
// Callers that must be atomic with other writes pass a transaction and lock
// the user row first.
func (s *Store) AppendLedger(ctx context.Context, db DB, row LedgerRow) (LedgerRow, error) {
	balance, err := s.Balance(ctx, db, row.UserID)
	if err != nil {
		return LedgerRow{}, err
	}
	after, err := core.ApplyLedger(balance, row.Kind, row.Delta)
	if err != nil {
		return LedgerRow{}, err
	}
	row.ID = NewID()
	row.BalanceAfter = after
	if row.Delta > 0 && row.ExpiresAt == nil {
		t := time.Now().Add(core.CreditTTL)
		row.ExpiresAt = &t
	}
	var refID *string
	if row.RefID != "" && ValidID(row.RefID) {
		refID = &row.RefID
	}
	err = db.QueryRow(ctx, `insert into ledger(id, user_id, kind, delta, balance_after, ref_kind, ref_id, reason, actor_id, expires_at) values($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) returning created_at`,
		row.ID, row.UserID, string(row.Kind), row.Delta, after, nullStr(row.RefKind), refID, nullStr(row.Reason), nullStr(row.ActorID), row.ExpiresAt).Scan(&row.CreatedAt)
	return row, err
}

// AppendLedgerLocked appends in its own transaction with the user row locked.
func (s *Store) AppendLedgerLocked(ctx context.Context, row LedgerRow) (LedgerRow, error) {
	var out LedgerRow
	err := s.Tx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `select 1 from users where id=$1 for update`, row.UserID); err != nil {
			return err
		}
		r, err := s.AppendLedger(ctx, tx, row)
		out = r
		return err
	})
	return out, err
}

// ListLedger returns a user's ledger, newest first.
func (s *Store) ListLedger(ctx context.Context, userID string) ([]LedgerRow, error) {
	rows, err := s.Pool.Query(ctx, `select id, user_id, kind, delta, balance_after, coalesce(ref_kind,''), coalesce(ref_id::text,''), coalesce(reason,''), coalesce(actor_id::text,''), expires_at, created_at from ledger where user_id=$1 order by created_at desc, id desc`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LedgerRow{}
	for rows.Next() {
		var r LedgerRow
		if err := rows.Scan(&r.ID, &r.UserID, &r.Kind, &r.Delta, &r.BalanceAfter, &r.RefKind, &r.RefID, &r.Reason, &r.ActorID, &r.ExpiresAt, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ExpireCredits writes expire rows for credits past their TTL. Credits are
// consumed oldest first, so the expired count is the expired positives minus
// everything already spent, revoked or expired.
func (s *Store) ExpireCredits(ctx context.Context) (int, error) {
	rows, err := s.Pool.Query(ctx, `select user_id from ledger where delta > 0 and expires_at < now() group by user_id`)
	if err != nil {
		return 0, err
	}
	var users []string
	for rows.Next() {
		var u string
		if err := rows.Scan(&u); err != nil {
			rows.Close()
			return 0, err
		}
		users = append(users, u)
	}
	rows.Close()
	written := 0
	for _, u := range users {
		err := s.Tx(ctx, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, `select 1 from users where id=$1 for update`, u); err != nil {
				return err
			}
			var expiredPos, spent, expiredAlready int
			if err := tx.QueryRow(ctx, `select
				coalesce(sum(case when delta > 0 and expires_at < now() then delta else 0 end),0),
				coalesce(sum(case when delta < 0 and kind <> 'expire' then -delta else 0 end),0),
				coalesce(sum(case when kind = 'expire' then -delta else 0 end),0)
				from ledger where user_id=$1`, u).Scan(&expiredPos, &spent, &expiredAlready); err != nil {
				return err
			}
			toExpire := expiredPos - spent - expiredAlready
			balance, err := s.Balance(ctx, tx, u)
			if err != nil {
				return err
			}
			if toExpire > balance {
				toExpire = balance
			}
			if toExpire <= 0 {
				return nil
			}
			_, err = s.AppendLedger(ctx, tx, LedgerRow{UserID: u, Kind: core.LedgerExpire, Delta: -toExpire, Reason: "credits past 12 months"})
			if err == nil {
				written++
			}
			return err
		})
		if err != nil {
			return written, err
		}
	}
	return written, nil
}

// ---- plans ----

const planCols = `id, user_id, kind, starts_at, ends_at, run_cap_per_day, last_run_at, coalesce(purchase_id::text,'')`

func scanPlan(row pgx.Row) (Plan, error) {
	var p Plan
	err := row.Scan(&p.ID, &p.UserID, &p.Kind, &p.StartsAt, &p.EndsAt, &p.RunCap, &p.LastRunAt, &p.PurchaseID)
	return p, wrap(err)
}

// ActivePlan returns the user's plan that covers now.
func (s *Store) ActivePlan(ctx context.Context, db DB, userID string) (Plan, error) {
	return scanPlan(db.QueryRow(ctx, `select `+planCols+` from plans where user_id=$1 and starts_at <= now() and ends_at > now() order by ends_at desc limit 1 for update`, userID))
}

// GetPlan reads the active plan without locking.
func (s *Store) GetPlan(ctx context.Context, userID string) (Plan, error) {
	return scanPlan(s.Pool.QueryRow(ctx, `select `+planCols+` from plans where user_id=$1 and starts_at <= now() and ends_at > now() order by ends_at desc limit 1`, userID))
}

// CreatePlan inserts a plan row.
func (s *Store) CreatePlan(ctx context.Context, db DB, p Plan) (Plan, error) {
	p.ID = NewID()
	if p.RunCap == 0 {
		p.RunCap = 1
	}
	_, err := db.Exec(ctx, `insert into plans(id, user_id, kind, starts_at, ends_at, run_cap_per_day, purchase_id) values($1,$2,$3,$4,$5,$6,$7)`,
		p.ID, p.UserID, p.Kind, p.StartsAt, p.EndsAt, p.RunCap, nullStr(p.PurchaseID))
	return p, err
}

// SetPlanLastRun sets the last run time (seed and tests).
func (s *Store) SetPlanLastRun(ctx context.Context, id string, t *time.Time) error {
	_, err := s.Pool.Exec(ctx, `update plans set last_run_at=$2 where id=$1`, id, t)
	return err
}

// ---- purchases ----

const purchaseCols = `id, user_id, sku, amount_usdc, memo_nonce, treasury, usdc_mint, state, coalesce(tx_signature,''), coalesce(confirmed_slot,0), coalesce(promo,''), expires_at, created_at`

func scanPurchase(row pgx.Row) (Purchase, error) {
	var p Purchase
	var slot int64
	err := row.Scan(&p.ID, &p.UserID, &p.SKU, &p.AmountUSDC, &p.MemoNonce, &p.Treasury, &p.USDCMint, &p.State, &p.TxSignature, &slot, &p.Promo, &p.ExpiresAt, &p.CreatedAt)
	p.ConfirmedSlot = uint64(slot)
	return p, wrap(err)
}

// CreatePurchase inserts a pending purchase.
func (s *Store) CreatePurchase(ctx context.Context, p Purchase) (Purchase, error) {
	p.ID = NewID()
	_, err := s.Pool.Exec(ctx, `insert into purchases(id, user_id, sku, amount_usdc, memo_nonce, treasury, usdc_mint, state, promo, expires_at) values($1,$2,$3,$4,$5,$6,$7,'pending',$8,$9)`,
		p.ID, p.UserID, string(p.SKU), p.AmountUSDC, p.MemoNonce, p.Treasury, p.USDCMint, nullStr(p.Promo), p.ExpiresAt)
	if err != nil {
		return Purchase{}, err
	}
	return s.GetPurchase(ctx, p.ID)
}

// GetPurchase fetches a purchase.
func (s *Store) GetPurchase(ctx context.Context, id string) (Purchase, error) {
	if !ValidID(id) {
		return Purchase{}, ErrNotFound
	}
	return scanPurchase(s.Pool.QueryRow(ctx, `select `+purchaseCols+` from purchases where id=$1`, id))
}

// ConfirmPurchase marks a purchase confirmed and grants what it bought:
// credits for mini, a plan row for pro, both with a ledger row.
func (s *Store) ConfirmPurchase(ctx context.Context, id, signature string, slot uint64) (Purchase, error) {
	err := s.Tx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `select 1 from purchases where id=$1 for update`, id); err != nil {
			return err
		}
		p, err := scanPurchase(tx.QueryRow(ctx, `select `+purchaseCols+` from purchases where id=$1`, id))
		if err != nil {
			return err
		}
		if p.State != "pending" {
			return ErrConflict
		}
		if _, err := tx.Exec(ctx, `select 1 from users where id=$1 for update`, p.UserID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `update purchases set state='confirmed', tx_signature=$2, confirmed_slot=$3 where id=$1`, id, signature, int64(slot)); err != nil {
			return wrap(err)
		}
		plan, ok := core.PlanBySKU(p.SKU)
		if !ok {
			return errors.New("unknown sku")
		}
		switch p.SKU {
		case core.SKUMini:
			_, err = s.AppendLedger(ctx, tx, LedgerRow{UserID: p.UserID, Kind: core.LedgerPurchase, Delta: plan.Credits, RefKind: "purchase", RefID: id, Reason: "mini audit"})
			return err
		case core.SKUPro:
			now := time.Now()
			_, err = s.CreatePlan(ctx, tx, Plan{UserID: p.UserID, Kind: "pro", StartsAt: now, EndsAt: now.Add(time.Duration(plan.TermDays) * 24 * time.Hour), RunCap: plan.RunsPerDay, PurchaseID: id})
			return err
		}
		return errors.New("unknown sku")
	})
	if err != nil {
		return Purchase{}, err
	}
	return s.GetPurchase(ctx, id)
}

// ExpirePurchases closes pending purchases past their deadline and returns
// them so the caller can tell each user.
func (s *Store) ExpirePurchases(ctx context.Context) ([]Purchase, error) {
	rows, err := s.Pool.Query(ctx, `update purchases set state='expired' where state='pending' and expires_at < now() returning `+purchaseCols)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Purchase{}
	for rows.Next() {
		p, err := scanPurchase(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ListPendingPurchases returns purchases still waiting for their transaction.
func (s *Store) ListPendingPurchases(ctx context.Context) ([]Purchase, error) {
	rows, err := s.Pool.Query(ctx, `select `+purchaseCols+` from purchases where state='pending' and expires_at > now() order by created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Purchase{}
	for rows.Next() {
		p, err := scanPurchase(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// AdminCredit grants or revokes credits on behalf of an admin. The ledger
// row is appended first with the user locked; the admin_actions row that
// references it is written in the same transaction, after the append
// succeeds, so a refused movement leaves no action behind.
func (s *Store) AdminCredit(ctx context.Context, adminID, userID string, kind core.LedgerKind, delta int, reason string) (LedgerRow, AdminAction, error) {
	var row LedgerRow
	action := AdminAction{ID: NewID(), AdminID: adminID, Kind: "credits." + string(kind), SubjectKind: "user", SubjectID: userID, Payload: map[string]any{"delta": delta, "reason": reason}}
	err := s.Tx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `select 1 from users where id=$1 for update`, userID); err != nil {
			return err
		}
		r, err := s.AppendLedger(ctx, tx, LedgerRow{UserID: userID, Kind: kind, Delta: delta, RefKind: "admin_action", RefID: action.ID, Reason: reason, ActorID: adminID})
		if err != nil {
			return ErrBadTransition{Err: err}
		}
		row = r
		action.Payload["ledger_id"] = r.ID
		a, err := s.logAdminAction(ctx, tx, action)
		action = a
		return err
	})
	return row, action, err
}

// ---- tweet claims ----

const claimCols = `id, user_id, nonce, url, state, coalesce(reviewed_by::text,''), reviewed_at, coalesce(reason,''), coalesce(ledger_entry_id::text,''), created_at`

func scanClaim(row pgx.Row) (TweetClaim, error) {
	var c TweetClaim
	err := row.Scan(&c.ID, &c.UserID, &c.Nonce, &c.URL, &c.State, &c.ReviewedBy, &c.ReviewedAt, &c.Reason, &c.LedgerEntryID, &c.CreatedAt)
	return c, wrap(err)
}

// CreateTweetClaim submits a claim.
func (s *Store) CreateTweetClaim(ctx context.Context, userID, nonce, url string) (TweetClaim, error) {
	id := NewID()
	_, err := s.Pool.Exec(ctx, `insert into tweet_claims(id, user_id, nonce, url, state) values($1,$2,$3,$4,'submitted')`, id, userID, nonce, url)
	if err != nil {
		return TweetClaim{}, err
	}
	return s.GetTweetClaim(ctx, id)
}

// GetTweetClaim fetches a claim.
func (s *Store) GetTweetClaim(ctx context.Context, id string) (TweetClaim, error) {
	if !ValidID(id) {
		return TweetClaim{}, ErrNotFound
	}
	return scanClaim(s.Pool.QueryRow(ctx, `select `+claimCols+` from tweet_claims where id=$1`, id))
}

func (s *Store) listClaims(ctx context.Context, where string, args ...any) ([]TweetClaim, error) {
	rows, err := s.Pool.Query(ctx, `select `+claimCols+` from tweet_claims `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TweetClaim{}
	for rows.Next() {
		c, err := scanClaim(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ListTweetClaims returns a user's claims, newest first.
func (s *Store) ListTweetClaims(ctx context.Context, userID string) ([]TweetClaim, error) {
	return s.listClaims(ctx, `where user_id=$1 order by created_at desc`, userID)
}

// ListAllTweetClaims returns claims for admin review, submitted first.
func (s *Store) ListAllTweetClaims(ctx context.Context) ([]TweetClaim, error) {
	return s.listClaims(ctx, `order by (state='submitted') desc, created_at desc limit 500`)
}

// ApproveClaim grants one credit and marks the claim approved.
func (s *Store) ApproveClaim(ctx context.Context, id, adminID, reason string) (TweetClaim, error) {
	err := s.Tx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `select 1 from tweet_claims where id=$1 for update`, id); err != nil {
			return err
		}
		c, err := scanClaim(tx.QueryRow(ctx, `select `+claimCols+` from tweet_claims where id=$1`, id))
		if err != nil {
			return err
		}
		if c.State != "submitted" {
			return ErrConflict
		}
		if _, err := tx.Exec(ctx, `select 1 from users where id=$1 for update`, c.UserID); err != nil {
			return err
		}
		row, err := s.AppendLedger(ctx, tx, LedgerRow{UserID: c.UserID, Kind: core.LedgerGrant, Delta: 1, RefKind: "tweet_claim", RefID: id, Reason: "free audit", ActorID: adminID})
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `update tweet_claims set state='approved', reviewed_by=$2, reviewed_at=now(), reason=$3, ledger_entry_id=$4 where id=$1`, id, adminID, nullStr(reason), row.ID)
		return err
	})
	if err != nil {
		return TweetClaim{}, err
	}
	return s.GetTweetClaim(ctx, id)
}

// RejectClaim marks a submitted claim rejected.
func (s *Store) RejectClaim(ctx context.Context, id, adminID, reason string) (TweetClaim, error) {
	tag, err := s.Pool.Exec(ctx, `update tweet_claims set state='rejected', reviewed_by=$2, reviewed_at=now(), reason=$3 where id=$1 and state='submitted'`, id, adminID, nullStr(reason))
	if err != nil {
		return TweetClaim{}, err
	}
	if tag.RowsAffected() == 0 {
		return TweetClaim{}, ErrConflict
	}
	return s.GetTweetClaim(ctx, id)
}

// RevokeClaim takes the granted credit back if the balance allows and marks
// the claim revoked.
func (s *Store) RevokeClaim(ctx context.Context, id, adminID, reason string) (TweetClaim, error) {
	err := s.Tx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `select 1 from tweet_claims where id=$1 for update`, id); err != nil {
			return err
		}
		c, err := scanClaim(tx.QueryRow(ctx, `select `+claimCols+` from tweet_claims where id=$1`, id))
		if err != nil {
			return err
		}
		if c.State != "approved" {
			return ErrConflict
		}
		if _, err := tx.Exec(ctx, `select 1 from users where id=$1 for update`, c.UserID); err != nil {
			return err
		}
		if _, err := s.AppendLedger(ctx, tx, LedgerRow{UserID: c.UserID, Kind: core.LedgerRevoke, Delta: -1, RefKind: "tweet_claim", RefID: id, Reason: reason, ActorID: adminID}); err != nil {
			return ErrBadTransition{Err: err}
		}
		_, err = tx.Exec(ctx, `update tweet_claims set state='revoked', reviewed_by=$2, reviewed_at=now(), reason=$3 where id=$1`, id, adminID, nullStr(reason))
		return err
	})
	if err != nil {
		return TweetClaim{}, err
	}
	return s.GetTweetClaim(ctx, id)
}
