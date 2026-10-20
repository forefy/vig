package store

import (
	"context"
	"time"
)

// Visual is a stored attachment: a capture, a recording or a file that shows
// a finding happening. The findings document carries only the reference;
// the bytes live here.
type Visual struct {
	ID        string
	AuditID   string
	Ordinal   int
	Caption   string
	Mime      string
	Name      string
	SHA256    string
	Bytes     []byte
	Size      int64
	CreatedAt time.Time
}

// SaveVisual stores an attachment and returns it with its id.
func (s *Store) SaveVisual(ctx context.Context, v Visual) (Visual, error) {
	v.ID = NewID()
	v.Size = int64(len(v.Bytes))
	err := s.Pool.QueryRow(ctx,
		`insert into finding_visuals(id, audit_id, ordinal, caption, mime, name, sha256, bytes)
		 values($1,$2,$3,$4,$5,$6,$7,$8) returning created_at`,
		v.ID, v.AuditID, v.Ordinal, v.Caption, v.Mime, v.Name, v.SHA256, v.Bytes).
		Scan(&v.CreatedAt)
	return v, wrap(err)
}

// GetVisual loads an attachment with its bytes.
func (s *Store) GetVisual(ctx context.Context, id string) (Visual, error) {
	var v Visual
	err := s.Pool.QueryRow(ctx,
		`select id, audit_id, ordinal, caption, mime, name, sha256, bytes, length(bytes), created_at
		 from finding_visuals where id=$1`, id).
		Scan(&v.ID, &v.AuditID, &v.Ordinal, &v.Caption, &v.Mime, &v.Name, &v.SHA256, &v.Bytes, &v.Size, &v.CreatedAt)
	return v, wrap(err)
}

// VisualMetas returns what the findings document carries about each stored
// attachment of an audit, without the bytes, keyed by id. An id that is not
// among the keys is not stored for this audit.
func (s *Store) VisualMetas(ctx context.Context, auditID string, ids []string) (map[string]Visual, error) {
	out := map[string]Visual{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.Pool.Query(ctx,
		`select id, ordinal, caption, mime, name, sha256, length(bytes), created_at
		 from finding_visuals where audit_id=$1 and id = any($2::uuid[])`, auditID, ids)
	if err != nil {
		return nil, wrap(err)
	}
	defer rows.Close()
	for rows.Next() {
		v := Visual{AuditID: auditID}
		if err := rows.Scan(&v.ID, &v.Ordinal, &v.Caption, &v.Mime, &v.Name, &v.SHA256, &v.Size, &v.CreatedAt); err != nil {
			return nil, wrap(err)
		}
		out[v.ID] = v
	}
	return out, wrap(rows.Err())
}
