package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/forefy/vig/backend/internal/core"
)

// Scan is an anonymous pre-flight.
type Scan struct {
	ID              string
	Token           string
	TargetID        string
	Repo            string
	CommitSHA       string
	ProgramID       string
	IPHash          []byte
	FingerprintHash []byte
	State           string
	Result          *core.Preflight
	Error           string
	ExpiresAt       time.Time
	CreatedAt       time.Time
}

const scanCols = `id, token, coalesce(target_id::text,''), repo_full_name, commit_sha, coalesce(program_id,''), ip_hash, fingerprint_hash, state, result, coalesce(error,''), expires_at, created_at`

func scanScan(row pgx.Row) (Scan, error) {
	var sc Scan
	var result []byte
	err := row.Scan(&sc.ID, &sc.Token, &sc.TargetID, &sc.Repo, &sc.CommitSHA, &sc.ProgramID, &sc.IPHash, &sc.FingerprintHash, &sc.State, &result, &sc.Error, &sc.ExpiresAt, &sc.CreatedAt)
	if err != nil {
		return sc, wrap(err)
	}
	if len(result) > 0 {
		var p core.Preflight
		if json.Unmarshal(result, &p) == nil {
			sc.Result = &p
		}
	}
	return sc, nil
}

// CreateScan inserts a queued scan.
func (s *Store) CreateScan(ctx context.Context, sc Scan) (Scan, error) {
	id := NewID()
	_, err := s.Pool.Exec(ctx, `insert into scans(id, token, target_id, repo_full_name, commit_sha, program_id, ip_hash, fingerprint_hash, state, expires_at)
		values($1,$2,$3,$4,$5,$6,$7,$8,'queued',$9)`,
		id, sc.Token, nullStr(sc.TargetID), sc.Repo, sc.CommitSHA, nullStr(sc.ProgramID), sc.IPHash, sc.FingerprintHash, sc.ExpiresAt)
	if err != nil {
		return Scan{}, err
	}
	return s.GetScanByToken(ctx, sc.Token)
}

// GetScanByToken fetches a scan that has not expired.
func (s *Store) GetScanByToken(ctx context.Context, token string) (Scan, error) {
	return scanScan(s.Pool.QueryRow(ctx, `select `+scanCols+` from scans where token=$1 and expires_at > now()`, token))
}

// CountScansSince counts scans from an ip hash (and optionally a fingerprint)
// created after t.
func (s *Store) CountScansSince(ctx context.Context, ipHash, fpHash []byte, t time.Time) (int, error) {
	var n int
	err := s.Pool.QueryRow(ctx, `select count(*) from scans where created_at > $3 and (ip_hash=$1 or ($2::bytea is not null and fingerprint_hash=$2))`, ipHash, fpHash, t).Scan(&n)
	return n, err
}

// ErrRateLimited is returned when the anonymous scan allowance is spent.
var ErrRateLimited = errors.New("scan limit reached for today")

// CreateScanLimited counts the caller's scans since t and inserts the new
// one in the same transaction, serialized per ip hash (and per fingerprint)
// with advisory locks so concurrent requests cannot exceed limit. It returns
// the scan and the allowance left after it.
func (s *Store) CreateScanLimited(ctx context.Context, sc Scan, t time.Time, limit int) (Scan, int, error) {
	id := NewID()
	remaining := 0
	err := s.Tx(ctx, func(tx pgx.Tx) error {
		for _, key := range [][]byte{sc.IPHash, sc.FingerprintHash} {
			if len(key) >= 8 {
				if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock(('x'||encode(substring($1::bytea from 1 for 8),'hex'))::bit(64)::bigint)`, key); err != nil {
					return err
				}
			}
		}
		var n int
		if err := tx.QueryRow(ctx, `select count(*) from scans where created_at > $3 and (ip_hash=$1 or ($2::bytea is not null and fingerprint_hash=$2))`, sc.IPHash, sc.FingerprintHash, t).Scan(&n); err != nil {
			return err
		}
		if n >= limit {
			return ErrRateLimited
		}
		remaining = limit - n - 1
		_, err := tx.Exec(ctx, `insert into scans(id, token, target_id, repo_full_name, commit_sha, program_id, ip_hash, fingerprint_hash, state, expires_at)
			values($1,$2,$3,$4,$5,$6,$7,$8,'queued',$9)`,
			id, sc.Token, nullStr(sc.TargetID), sc.Repo, sc.CommitSHA, nullStr(sc.ProgramID), sc.IPHash, sc.FingerprintHash, sc.ExpiresAt)
		return err
	})
	if err != nil {
		return Scan{}, 0, err
	}
	out, err := s.GetScanByToken(ctx, sc.Token)
	return out, remaining, err
}

// ClaimQueuedScan takes the oldest queued scan and marks it running.
func (s *Store) ClaimQueuedScan(ctx context.Context) (Scan, error) {
	var sc Scan
	err := s.Tx(ctx, func(tx pgx.Tx) error {
		var id string
		err := tx.QueryRow(ctx, `select id from scans where state='queued' order by created_at limit 1 for update skip locked`).Scan(&id)
		if err != nil {
			return wrap(err)
		}
		if _, err := tx.Exec(ctx, `update scans set state='running' where id=$1`, id); err != nil {
			return err
		}
		sc, err = scanScan(tx.QueryRow(ctx, `select `+scanCols+` from scans where id=$1`, id))
		return err
	})
	return sc, err
}

// SetScanRunning marks a scan as running (synchronous path).
func (s *Store) SetScanRunning(ctx context.Context, id string) error {
	_, err := s.Pool.Exec(ctx, `update scans set state='running' where id=$1 and state='queued'`, id)
	return err
}

// RequeueScan puts a running scan back in the queue (no worker slot was
// free for it).
func (s *Store) RequeueScan(ctx context.Context, id string) error {
	_, err := s.Pool.Exec(ctx, `update scans set state='queued' where id=$1 and state='running' and result is null`, id)
	return err
}

// FinishScan stores the result or the error.
func (s *Store) FinishScan(ctx context.Context, id string, result *core.Preflight, runErr error) error {
	if runErr != nil {
		_, err := s.Pool.Exec(ctx, `update scans set state='failed', error=$2 where id=$1`, id, runErr.Error())
		return err
	}
	b, _ := json.Marshal(result)
	_, err := s.Pool.Exec(ctx, `update scans set state='done', result=$2 where id=$1`, id, b)
	return err
}
