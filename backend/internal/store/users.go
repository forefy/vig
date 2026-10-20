package store

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// User is a users row.
type User struct {
	ID          string
	DisplayName string
	AvatarURL   string
	Role        string
	TOTPSecret  string
	TOTPEnabled bool
	NotifyEmail bool
	DeletedAt   *time.Time
	CreatedAt   time.Time
}

// Identity is one linked provider identity.
type Identity struct {
	ID         string
	UserID     string
	Provider   string
	ProviderID string
	Email      string
	Handle     string
	CreatedAt  time.Time
}

// Session is one login (a refresh-token family).
type Session struct {
	ID        string
	UserID    string
	ExpiresAt time.Time
	TOTPAt    *time.Time
	IP        string
	UA        string
	RevokedAt *time.Time
	CreatedAt time.Time
}

// APIKey is an api_keys row without the hash.
type APIKey struct {
	ID         string
	UserID     string
	Prefix     string
	Name       string
	LastUsedAt *time.Time
	RevokedAt  *time.Time
	CreatedAt  time.Time
}

const userCols = `id, display_name, avatar_url, role, coalesce(totp_secret,''), totp_enabled, notify_email, deleted_at, created_at`

func scanUser(row pgx.Row) (User, error) {
	var u User
	err := row.Scan(&u.ID, &u.DisplayName, &u.AvatarURL, &u.Role, &u.TOTPSecret, &u.TOTPEnabled, &u.NotifyEmail, &u.DeletedAt, &u.CreatedAt)
	return u, wrap(err)
}

// GetUser fetches one user.
func (s *Store) GetUser(ctx context.Context, id string) (User, error) {
	if !ValidID(id) {
		return User{}, ErrNotFound
	}
	return scanUser(s.Pool.QueryRow(ctx, `select `+userCols+` from users where id=$1`, id))
}

// FindUserByDisplayName finds the oldest user with the display name.
func (s *Store) FindUserByDisplayName(ctx context.Context, name string) (User, error) {
	return scanUser(s.Pool.QueryRow(ctx, `select `+userCols+` from users where display_name=$1 order by created_at limit 1`, name))
}

// CreateUser inserts a user with the default role.
func (s *Store) CreateUser(ctx context.Context, db DB, displayName, avatarURL string) (User, error) {
	id := NewID()
	_, err := db.Exec(ctx, `insert into users(id, display_name, avatar_url) values($1,$2,$3)`, id, displayName, avatarURL)
	if err != nil {
		return User{}, err
	}
	return scanUser(db.QueryRow(ctx, `select `+userCols+` from users where id=$1`, id))
}

// UpdateProfile sets the display name.
func (s *Store) UpdateProfile(ctx context.Context, id, displayName string) (User, error) {
	_, err := s.Pool.Exec(ctx, `update users set display_name=$2 where id=$1`, id, displayName)
	if err != nil {
		return User{}, err
	}
	return s.GetUser(ctx, id)
}

// SetRole changes a user's role and revokes every session, so the new
// privilege (or its loss) only applies to a fresh login.
func (s *Store) SetRole(ctx context.Context, id, role string) error {
	return s.Tx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `update users set role=$2 where id=$1`, id, role); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `update sessions set revoked_at=now() where user_id=$1 and revoked_at is null`, id)
		return err
	})
}

// SetNotifyEmail stores the email notification preference.
func (s *Store) SetNotifyEmail(ctx context.Context, id string, on bool) error {
	_, err := s.Pool.Exec(ctx, `update users set notify_email=$2 where id=$1`, id, on)
	return err
}

// DeleteAccount anonymises a user: identities, sessions, refresh tokens,
// API keys, installations, recovery codes and TOTP go; the profile is
// blanked. The ledger, purchases, audits and reports stay for accounting.
func (s *Store) DeleteAccount(ctx context.Context, id string) error {
	return s.Tx(ctx, func(tx pgx.Tx) error {
		for _, q := range []string{
			`delete from identities where user_id=$1`,
			`update sessions set revoked_at=now() where user_id=$1 and revoked_at is null`,
			`update refresh_tokens set revoked_at=now() where revoked_at is null and session_id in (select id from sessions where user_id=$1)`,
			`update api_keys set revoked_at=now() where user_id=$1 and revoked_at is null`,
			`delete from github_installations where user_id=$1`,
			`delete from recovery_codes where user_id=$1`,
			`update target_programs set ownership_nonce=null, ownership_nonce_at=null where target_id in (select id from targets where owner_user_id=$1)`,
			`update users set display_name='', avatar_url='', totp_secret=null, totp_enabled=false, notify_email=false, deleted_at=now() where id=$1`,
		} {
			if _, err := tx.Exec(ctx, q, id); err != nil {
				return err
			}
		}
		return nil
	})
}

// ---- recovery codes ----

// ReplaceRecoveryCodes drops the user's codes and stores a new set of hashes.
func (s *Store) ReplaceRecoveryCodes(ctx context.Context, userID string, hashes [][]byte) error {
	return s.Tx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `delete from recovery_codes where user_id=$1`, userID); err != nil {
			return err
		}
		for _, h := range hashes {
			if _, err := tx.Exec(ctx, `insert into recovery_codes(id, user_id, hash) values($1,$2,$3)`, NewID(), userID, h); err != nil {
				return err
			}
		}
		return nil
	})
}

// ConsumeRecoveryCode marks an unused code used and reports whether one
// matched.
func (s *Store) ConsumeRecoveryCode(ctx context.Context, userID string, hash []byte) (bool, error) {
	tag, err := s.Pool.Exec(ctx, `update recovery_codes set used_at=now() where user_id=$1 and hash=$2 and used_at is null`, userID, hash)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// CountRecoveryCodes counts the user's unused codes.
func (s *Store) CountRecoveryCodes(ctx context.Context, userID string) (int, error) {
	var n int
	err := s.Pool.QueryRow(ctx, `select count(*) from recovery_codes where user_id=$1 and used_at is null`, userID).Scan(&n)
	return n, err
}

// SetTOTPPending stores a fresh secret that is not yet enabled.
func (s *Store) SetTOTPPending(ctx context.Context, id, secret string) error {
	_, err := s.Pool.Exec(ctx, `update users set totp_secret=$2, totp_enabled=false where id=$1`, id, secret)
	return err
}

// EnableTOTP marks the pending secret as enabled.
func (s *Store) EnableTOTP(ctx context.Context, id string) error {
	_, err := s.Pool.Exec(ctx, `update users set totp_enabled=true where id=$1 and totp_secret is not null`, id)
	return err
}

// UserSummary is a user with the facts the admin list shows, assembled in
// one query.
type UserSummary struct {
	User       User
	Identities []Identity
	Credits    int
	Audits     int
	Plan       *Plan
}

// ListUserSummaries returns every user with identities, balance, audit count
// and active plan, newest first, in a single round trip.
func (s *Store) ListUserSummaries(ctx context.Context, limit int) ([]UserSummary, error) {
	// The user columns are qualified because the lateral plan join also has
	// id and created_at.
	qualified := "u." + strings.ReplaceAll(userCols, ", ", ", u.")
	qualified = strings.ReplaceAll(qualified, "u.coalesce(totp_secret,'')", "coalesce(u.totp_secret,'')")
	rows, err := s.Pool.Query(ctx, `select `+qualified+`,
		coalesce((select json_agg(json_build_object('id', i.id, 'provider', i.provider, 'provider_id', i.provider_id, 'email', coalesce(i.email,''), 'handle', coalesce(i.handle,''), 'created_at', i.created_at) order by i.created_at) from identities i where i.user_id=u.id), '[]'::json),
		coalesce((select sum(l.delta) from ledger l where l.user_id=u.id), 0)::bigint,
		(select count(*) from audits a where a.user_id=u.id)::bigint,
		p.id, p.kind, p.starts_at, p.ends_at, p.run_cap_per_day, p.last_run_at
		from users u
		left join lateral (select `+planCols+` from plans where user_id=u.id and starts_at <= now() and ends_at > now() order by ends_at desc limit 1) p on true
		order by u.created_at desc limit $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []UserSummary{}
	for rows.Next() {
		var us UserSummary
		var idents []byte
		var planID, planKind *string
		var starts, ends, lastRun *time.Time
		var runCap *int
		u := &us.User
		if err := rows.Scan(&u.ID, &u.DisplayName, &u.AvatarURL, &u.Role, &u.TOTPSecret, &u.TOTPEnabled, &u.NotifyEmail, &u.DeletedAt, &u.CreatedAt,
			&idents, &us.Credits, &us.Audits, &planID, &planKind, &starts, &ends, &runCap, &lastRun); err != nil {
			return nil, err
		}
		var raw []struct {
			ID         string    `json:"id"`
			Provider   string    `json:"provider"`
			ProviderID string    `json:"provider_id"`
			Email      string    `json:"email"`
			Handle     string    `json:"handle"`
			CreatedAt  time.Time `json:"created_at"`
		}
		_ = json.Unmarshal(idents, &raw)
		us.Identities = []Identity{}
		for _, r := range raw {
			us.Identities = append(us.Identities, Identity{ID: r.ID, UserID: u.ID, Provider: r.Provider, ProviderID: r.ProviderID, Email: r.Email, Handle: r.Handle, CreatedAt: r.CreatedAt})
		}
		if planID != nil {
			us.Plan = &Plan{ID: *planID, UserID: u.ID, Kind: *planKind, StartsAt: *starts, EndsAt: *ends, RunCap: *runCap, LastRunAt: lastRun}
		}
		out = append(out, us)
	}
	return out, rows.Err()
}

// ---- identities ----

const identityCols = `id, user_id, provider, provider_id, coalesce(email,''), coalesce(handle,''), created_at`

func scanIdentity(row pgx.Row) (Identity, error) {
	var i Identity
	err := row.Scan(&i.ID, &i.UserID, &i.Provider, &i.ProviderID, &i.Email, &i.Handle, &i.CreatedAt)
	return i, wrap(err)
}

// FindIdentity looks up a provider identity.
func (s *Store) FindIdentity(ctx context.Context, db DB, provider, providerID string) (Identity, error) {
	return scanIdentity(db.QueryRow(ctx, `select `+identityCols+` from identities where provider=$1 and provider_id=$2`, provider, providerID))
}

// AddIdentity links a provider identity to a user.
func (s *Store) AddIdentity(ctx context.Context, db DB, userID, provider, providerID, email, handle string) (Identity, error) {
	id := NewID()
	_, err := db.Exec(ctx, `insert into identities(id, user_id, provider, provider_id, email, handle) values($1,$2,$3,$4,$5,$6)`,
		id, userID, provider, providerID, nullStr(email), nullStr(handle))
	if err != nil {
		return Identity{}, wrap(err)
	}
	return scanIdentity(db.QueryRow(ctx, `select `+identityCols+` from identities where id=$1`, id))
}

// ListIdentities returns a user's identities.
func (s *Store) ListIdentities(ctx context.Context, userID string) ([]Identity, error) {
	rows, err := s.Pool.Query(ctx, `select `+identityCols+` from identities where user_id=$1 order by created_at`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Identity{}
	for rows.Next() {
		i, err := scanIdentity(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

// DeleteIdentity unlinks an identity. It refuses to remove the last one.
func (s *Store) DeleteIdentity(ctx context.Context, userID, id string) error {
	if !ValidID(id) {
		return ErrNotFound
	}
	return s.Tx(ctx, func(tx pgx.Tx) error {
		var n int
		if err := tx.QueryRow(ctx, `select count(*) from identities where user_id=$1`, userID).Scan(&n); err != nil {
			return err
		}
		if n <= 1 {
			return ErrConflict
		}
		tag, err := tx.Exec(ctx, `delete from identities where id=$1 and user_id=$2`, id, userID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return nil
	})
}

// FindOrCreateUser resolves an external identity to an account: provider id
// first, then email, then a fresh account. Returns the identity and whether
// the account was created now.
func (s *Store) FindOrCreateUser(ctx context.Context, provider, providerID, email, handle, displayName, avatarURL string) (User, bool, error) {
	var user User
	created := false
	err := s.Tx(ctx, func(tx pgx.Tx) error {
		if ident, err := s.FindIdentity(ctx, tx, provider, providerID); err == nil {
			u, err := scanUser(tx.QueryRow(ctx, `select `+userCols+` from users where id=$1`, ident.UserID))
			user = u
			return err
		} else if err != ErrNotFound {
			return err
		}
		// No linking by email. Matching a new provider to an existing account
		// on a shared address makes every account reachable by whoever can
		// prove that address at the weakest provider we ever support, and an
		// address can change hands. An unknown identity gets its own account;
		// joining two together is a deliberate act from settings, taken while
		// signed in to the account being joined.
		u, err := s.CreateUser(ctx, tx, displayName, avatarURL)
		if err != nil {
			return err
		}
		if _, err := s.AddIdentity(ctx, tx, u.ID, provider, providerID, email, handle); err != nil {
			return err
		}
		user = u
		created = true
		return nil
	})
	return user, created, err
}

// ---- sessions and refresh tokens ----

// CreateSession starts a login and returns the session plus the first
// refresh token hash to store.
func (s *Store) CreateSession(ctx context.Context, userID string, ttl time.Duration, ip, ua string, refreshHash []byte) (Session, error) {
	id := NewID()
	exp := time.Now().Add(ttl)
	err := s.Tx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `insert into sessions(id, user_id, expires_at, ip, ua) values($1,$2,$3,$4,$5)`, id, userID, exp, ip, ua); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `insert into refresh_tokens(id, session_id, hash, expires_at) values($1,$2,$3,$4)`, NewID(), id, refreshHash, exp)
		return err
	})
	if err != nil {
		return Session{}, err
	}
	return s.GetSession(ctx, id)
}

const sessionCols = `id, user_id, expires_at, totp_at, coalesce(ip,''), coalesce(ua,''), revoked_at, created_at`

func scanSession(row pgx.Row) (Session, error) {
	var se Session
	err := row.Scan(&se.ID, &se.UserID, &se.ExpiresAt, &se.TOTPAt, &se.IP, &se.UA, &se.RevokedAt, &se.CreatedAt)
	return se, wrap(err)
}

// GetSession fetches a session.
func (s *Store) GetSession(ctx context.Context, id string) (Session, error) {
	if !ValidID(id) {
		return Session{}, ErrNotFound
	}
	return scanSession(s.Pool.QueryRow(ctx, `select `+sessionCols+` from sessions where id=$1`, id))
}

// SessionLive reports whether a session exists, is not revoked and not expired.
func (s *Store) SessionLive(ctx context.Context, id string) (Session, bool) {
	se, err := s.GetSession(ctx, id)
	if err != nil || se.RevokedAt != nil || se.ExpiresAt.Before(time.Now()) {
		return Session{}, false
	}
	return se, true
}

// ErrRefreshReuse marks a replayed refresh token; the family is revoked.
type ErrRefreshReuse struct{}

func (ErrRefreshReuse) Error() string { return "refresh token reuse" }

// RotateRefresh validates a presented refresh token hash and replaces it
// within the same session. A spent token revokes the whole session.
func (s *Store) RotateRefresh(ctx context.Context, presentedHash, newHash []byte, ttl time.Duration) (Session, error) {
	var out Session
	err := s.Tx(ctx, func(tx pgx.Tx) error {
		var id, sessionID string
		var expires time.Time
		var replacedBy *string
		var revokedAt *time.Time
		err := tx.QueryRow(ctx, `select id, session_id, expires_at, replaced_by, revoked_at from refresh_tokens where hash=$1`, presentedHash).
			Scan(&id, &sessionID, &expires, &replacedBy, &revokedAt)
		if err != nil {
			return wrap(err)
		}
		if replacedBy != nil || revokedAt != nil {
			_, _ = tx.Exec(ctx, `update sessions set revoked_at=now() where id=$1`, sessionID)
			return ErrRefreshReuse{}
		}
		se, err := scanSession(tx.QueryRow(ctx, `select `+sessionCols+` from sessions where id=$1`, sessionID))
		if err != nil {
			return err
		}
		if expires.Before(time.Now()) || se.RevokedAt != nil || se.ExpiresAt.Before(time.Now()) {
			return ErrNotFound
		}
		newID := NewID()
		newExp := time.Now().Add(ttl)
		if _, err := tx.Exec(ctx, `insert into refresh_tokens(id, session_id, hash, expires_at) values($1,$2,$3,$4)`, newID, sessionID, newHash, newExp); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `update refresh_tokens set replaced_by=$2, revoked_at=now() where id=$1`, id, newID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `update sessions set expires_at=$2 where id=$1`, sessionID, newExp); err != nil {
			return err
		}
		se.ExpiresAt = newExp
		out = se
		return nil
	})
	return out, err
}

// RevokeSession ends a login and every refresh token in it.
func (s *Store) RevokeSession(ctx context.Context, id string) error {
	if !ValidID(id) {
		return nil
	}
	_, err := s.Pool.Exec(ctx, `update sessions set revoked_at=now() where id=$1 and revoked_at is null`, id)
	return err
}

// ListSessions returns a user's live sessions, newest first.
func (s *Store) ListSessions(ctx context.Context, userID string) ([]Session, error) {
	rows, err := s.Pool.Query(ctx, `select `+sessionCols+` from sessions where user_id=$1 and revoked_at is null and expires_at > now() order by created_at desc`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Session{}
	for rows.Next() {
		se, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, se)
	}
	return out, rows.Err()
}

// RevokeOtherSessions ends every session of the user except keep.
func (s *Store) RevokeOtherSessions(ctx context.Context, userID, keep string) (int64, error) {
	tag, err := s.Pool.Exec(ctx, `update sessions set revoked_at=now() where user_id=$1 and id<>$2 and revoked_at is null`, userID, keep)
	return tag.RowsAffected(), err
}

// SetSessionTOTP records a successful TOTP check on the session.
func (s *Store) SetSessionTOTP(ctx context.Context, id string) error {
	_, err := s.Pool.Exec(ctx, `update sessions set totp_at=now() where id=$1`, id)
	return err
}

// ---- SIWS nonces ----

// PutSIWSNonce stores a nonce to be consumed by one verify call.
func (s *Store) PutSIWSNonce(ctx context.Context, nonce string, ttl time.Duration) error {
	_, err := s.Pool.Exec(ctx, `insert into siws_nonces(nonce, expires_at) values($1,$2)`, nonce, time.Now().Add(ttl))
	return err
}

// ConsumeSIWSNonce deletes the nonce and reports whether it was valid.
func (s *Store) ConsumeSIWSNonce(ctx context.Context, nonce string) (bool, error) {
	tag, err := s.Pool.Exec(ctx, `delete from siws_nonces where nonce=$1 and expires_at > now()`, nonce)
	if err != nil {
		return false, err
	}
	_, _ = s.Pool.Exec(ctx, `delete from siws_nonces where expires_at <= now()`)
	return tag.RowsAffected() == 1, nil
}

// ---- API keys ----

const apiKeyCols = `id, user_id, prefix, name, last_used_at, revoked_at, created_at`

func scanAPIKey(row pgx.Row) (APIKey, error) {
	var k APIKey
	err := row.Scan(&k.ID, &k.UserID, &k.Prefix, &k.Name, &k.LastUsedAt, &k.RevokedAt, &k.CreatedAt)
	return k, wrap(err)
}

// CreateAPIKey stores a key hash.
func (s *Store) CreateAPIKey(ctx context.Context, userID, prefix, name string, hash []byte) (APIKey, error) {
	id := NewID()
	if _, err := s.Pool.Exec(ctx, `insert into api_keys(id, user_id, prefix, hash, name) values($1,$2,$3,$4,$5)`, id, userID, prefix, hash, name); err != nil {
		return APIKey{}, wrap(err)
	}
	return scanAPIKey(s.Pool.QueryRow(ctx, `select `+apiKeyCols+` from api_keys where id=$1`, id))
}

// ListAPIKeys returns a user's active keys.
func (s *Store) ListAPIKeys(ctx context.Context, userID string) ([]APIKey, error) {
	rows, err := s.Pool.Query(ctx, `select `+apiKeyCols+` from api_keys where user_id=$1 and revoked_at is null order by created_at desc`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []APIKey{}
	for rows.Next() {
		k, err := scanAPIKey(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// RevokeAPIKey revokes a user's key.
func (s *Store) RevokeAPIKey(ctx context.Context, userID, id string) error {
	if !ValidID(id) {
		return ErrNotFound
	}
	tag, err := s.Pool.Exec(ctx, `update api_keys set revoked_at=now() where id=$1 and user_id=$2 and revoked_at is null`, id, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// UserByAPIKeyHash resolves a key hash to its user and stamps last_used_at.
func (s *Store) UserByAPIKeyHash(ctx context.Context, hash []byte) (User, error) {
	var userID string
	err := s.Pool.QueryRow(ctx, `update api_keys set last_used_at=now() where hash=$1 and revoked_at is null returning user_id`, hash).Scan(&userID)
	if err != nil {
		return User{}, wrap(err)
	}
	return s.GetUser(ctx, userID)
}
