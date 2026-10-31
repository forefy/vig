package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/forefy/vig/backend/internal/core"
)

// AuditIndex is the catalogue row of an audit's index; the Parquet bytes
// live on disk and the api package serves them.
type AuditIndex struct {
	AuditID     string
	Tool        string
	ToolVersion string
	Bytes       int64
	Tables      []core.IndexTable
	BuiltAt     time.Time
}

// SaveIndex records a sealed index, replacing any earlier one for the audit.
func (s *Store) SaveIndex(ctx context.Context, ix AuditIndex) (AuditIndex, error) {
	if !ValidID(ix.AuditID) {
		return AuditIndex{}, ErrNotFound
	}
	tables, err := json.Marshal(ix.Tables)
	if err != nil {
		return AuditIndex{}, err
	}
	err = s.Pool.QueryRow(ctx, `insert into audit_indexes (audit_id, tool, tool_version, bytes, tables, built_at)
		values ($1,$2,$3,$4,$5,now())
		on conflict (audit_id) do update set tool=excluded.tool, tool_version=excluded.tool_version, bytes=excluded.bytes, tables=excluded.tables, built_at=now()
		returning built_at`, ix.AuditID, ix.Tool, ix.ToolVersion, ix.Bytes, tables).Scan(&ix.BuiltAt)
	return ix, err
}

// GetIndex returns an audit's index row, ErrNotFound when none was sealed.
func (s *Store) GetIndex(ctx context.Context, auditID string) (AuditIndex, error) {
	if !ValidID(auditID) {
		return AuditIndex{}, ErrNotFound
	}
	var ix AuditIndex
	var tables []byte
	err := s.Pool.QueryRow(ctx, `select audit_id, tool, tool_version, bytes, tables, built_at from audit_indexes where audit_id=$1`, auditID).
		Scan(&ix.AuditID, &ix.Tool, &ix.ToolVersion, &ix.Bytes, &tables, &ix.BuiltAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return AuditIndex{}, ErrNotFound
	}
	if err != nil {
		return AuditIndex{}, err
	}
	if err := json.Unmarshal(tables, &ix.Tables); err != nil {
		return AuditIndex{}, err
	}
	return ix, nil
}

// DeleteIndex drops the row; the caller removes the files.
func (s *Store) DeleteIndex(ctx context.Context, auditID string) error {
	if !ValidID(auditID) {
		return ErrNotFound
	}
	_, err := s.Pool.Exec(ctx, `delete from audit_indexes where audit_id=$1`, auditID)
	return err
}

// ListIndexedAudits returns the owner's audits that carry an index, newest
// first, with the index row of each.
func (s *Store) ListIndexedAudits(ctx context.Context, userID string) ([]Audit, map[string]AuditIndex, error) {
	audits, err := s.listAudits(ctx, `where a.user_id=$1 and a.hidden_at is null and exists (select 1 from audit_indexes i where i.audit_id=a.id) order by a.created_at desc`, userID)
	if err != nil {
		return nil, nil, err
	}
	out := map[string]AuditIndex{}
	for _, a := range audits {
		ix, err := s.GetIndex(ctx, a.ID)
		if err != nil {
			return nil, nil, err
		}
		out[a.ID] = ix
	}
	return audits, out, nil
}
