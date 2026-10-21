package store

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/forefy/vig/backend/internal/core"
)

// Target is a resolved source: repo plus commit.
type Target struct {
	ID             string
	Adapter        string
	OwnerUserID    string
	SourceKind     string
	Repo           string
	Subpath        string
	InstallationID string
	Selector       string
	Revision       string
	CommitSHA      string
	CommitAuthor   string
	CommitDate     time.Time
	BaseURL        string
	// A web target: what the customer authorised, what the intake read,
	// and the nonce proof, where a program keeps the same on its own row.
	Scope             core.SiteScope
	Panel             *core.VerificationPanel
	OwnershipNonce    string
	OwnershipNonceAt  *time.Time
	OwnershipProvenAt *time.Time
	OwnershipMethod   string
	CreatedAt         time.Time
}

// Program is a target_programs row.
type Program struct {
	ID                 string
	TargetID           string
	ProgramID          string
	Chain              string
	Loader             string
	UpgradeAuthority   string
	LastDeploySlot     uint64
	BytecodeSHA256     string
	IDLPresent         bool
	AnchorVersion      string
	VerifiedBuild      core.MatchStatus
	VerifiedCommit     string
	OwnershipNonce     string
	OwnershipNonceAt   *time.Time
	OwnershipProvenAt  *time.Time
	OwnershipSignature string
	Panel              core.VerificationPanel
	CreatedAt          time.Time
}

// Installation is a github_installations row.
type Installation struct {
	ID             string
	UserID         string
	InstallationID int64
	AccountLogin   string
	AccountType    string
	Status         string
}

const targetCols = `id, adapter, coalesce(owner_user_id::text,''), source_kind, repo_full_name, subpath, coalesce(installation_id::text,''), selector, revision, commit_sha, coalesce(commit_author,''), coalesce(commit_date, to_timestamp(0)), base_url, scope, panel, coalesce(ownership_nonce,''), ownership_nonce_at, ownership_proven_at, coalesce(ownership_method,''), created_at`

func scanTarget(row pgx.Row) (Target, error) {
	var t Target
	var scope, panel []byte
	err := row.Scan(&t.ID, &t.Adapter, &t.OwnerUserID, &t.SourceKind, &t.Repo, &t.Subpath, &t.InstallationID, &t.Selector, &t.Revision, &t.CommitSHA, &t.CommitAuthor, &t.CommitDate, &t.BaseURL,
		&scope, &panel, &t.OwnershipNonce, &t.OwnershipNonceAt, &t.OwnershipProvenAt, &t.OwnershipMethod, &t.CreatedAt)
	if err != nil {
		return t, wrap(err)
	}
	if len(scope) > 0 {
		_ = json.Unmarshal(scope, &t.Scope)
	}
	if len(panel) > 0 {
		var p core.VerificationPanel
		if json.Unmarshal(panel, &p) == nil {
			t.Panel = &p
		}
	}
	return t, nil
}

// CreateTarget stores a resolved target.
func (s *Store) CreateTarget(ctx context.Context, t Target) (Target, error) {
	id := NewID()
	scope, _ := json.Marshal(t.Scope)
	var panel []byte
	if t.Panel != nil {
		panel, _ = json.Marshal(t.Panel)
	}
	_, err := s.Pool.Exec(ctx, `insert into targets(id, adapter, owner_user_id, source_kind, repo_full_name, subpath, installation_id, selector, revision, commit_sha, commit_author, commit_date, base_url, scope, panel)
		values($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`,
		id, t.Adapter, nullStr(t.OwnerUserID), t.SourceKind, t.Repo, t.Subpath, nullStr(t.InstallationID), t.Selector, t.Revision, t.CommitSHA, nullStr(t.CommitAuthor), ptrTime(t.CommitDate), t.BaseURL, scope, panel)
	if err != nil {
		return Target{}, err
	}
	return s.GetTarget(ctx, id)
}

// SetTargetScope replaces what the customer authorised on a web target.
func (s *Store) SetTargetScope(ctx context.Context, id string, scope core.SiteScope) error {
	b, _ := json.Marshal(scope)
	_, err := s.Pool.Exec(ctx, `update targets set scope=$2 where id=$1`, id, b)
	return err
}

// SetTargetOwnershipNonce issues a nonce the site must carry.
func (s *Store) SetTargetOwnershipNonce(ctx context.Context, id, nonce string) error {
	_, err := s.Pool.Exec(ctx, `update targets set ownership_nonce=$2, ownership_nonce_at=now() where id=$1`, id, nonce)
	return err
}

// SetTargetOwnershipProven records where the nonce was found and the
// panel with its ownership row rewritten.
func (s *Store) SetTargetOwnershipProven(ctx context.Context, id, method string, panel core.VerificationPanel) error {
	b, _ := json.Marshal(panel)
	_, err := s.Pool.Exec(ctx, `update targets set ownership_proven_at=now(), ownership_method=$2, ownership_nonce=null, ownership_nonce_at=null, panel=$3 where id=$1`, id, method, b)
	return err
}

// GetTarget fetches a target.
func (s *Store) GetTarget(ctx context.Context, id string) (Target, error) {
	if !ValidID(id) {
		return Target{}, ErrNotFound
	}
	return scanTarget(s.Pool.QueryRow(ctx, `select `+targetCols+` from targets where id=$1`, id))
}

const programCols = `id, target_id, program_id, chain, coalesce(loader,''), coalesce(upgrade_authority,''), coalesce(last_deploy_slot,0), coalesce(bytecode_sha256,''), idl_present, coalesce(anchor_version,''), verified_build, coalesce(verified_commit,''), coalesce(ownership_nonce,''), ownership_nonce_at, ownership_proven_at, coalesce(ownership_signature,''), panel, created_at`

func scanProgram(row pgx.Row) (Program, error) {
	var p Program
	var slot int64
	var panel []byte
	err := row.Scan(&p.ID, &p.TargetID, &p.ProgramID, &p.Chain, &p.Loader, &p.UpgradeAuthority, &slot, &p.BytecodeSHA256, &p.IDLPresent, &p.AnchorVersion, &p.VerifiedBuild, &p.VerifiedCommit, &p.OwnershipNonce, &p.OwnershipNonceAt, &p.OwnershipProvenAt, &p.OwnershipSignature, &panel, &p.CreatedAt)
	if err != nil {
		return p, wrap(err)
	}
	p.LastDeploySlot = uint64(slot)
	if len(panel) > 0 {
		_ = json.Unmarshal(panel, &p.Panel)
	}
	return p, nil
}

// UpsertProgram inserts or refreshes a program on a target. Ownership proof
// survives a refresh only while the upgrade authority is the one that
// signed; a changed authority clears the proof so the owner must re-prove.
func (s *Store) UpsertProgram(ctx context.Context, p Program) (Program, error) {
	if p.VerifiedBuild == "" {
		p.VerifiedBuild = core.MatchUnverified
	}
	panel, _ := json.Marshal(p.Panel)
	id := NewID()
	_, err := s.Pool.Exec(ctx, `insert into target_programs(id, target_id, program_id, chain, loader, upgrade_authority, last_deploy_slot, bytecode_sha256, idl_present, anchor_version, verified_build, verified_commit, panel)
		values($1,$2,$3,$13,$4,$5,$6,$7,$8,$9,$10,$11,$12)
		on conflict (target_id, program_id) do update set chain=excluded.chain, loader=excluded.loader, upgrade_authority=excluded.upgrade_authority, last_deploy_slot=excluded.last_deploy_slot,
		  bytecode_sha256=excluded.bytecode_sha256, idl_present=excluded.idl_present, anchor_version=excluded.anchor_version, verified_build=excluded.verified_build,
		  verified_commit=excluded.verified_commit, panel=excluded.panel,
		  ownership_proven_at = case when target_programs.upgrade_authority is distinct from excluded.upgrade_authority then null else target_programs.ownership_proven_at end,
		  ownership_signature = case when target_programs.upgrade_authority is distinct from excluded.upgrade_authority then null else target_programs.ownership_signature end`,
		id, p.TargetID, p.ProgramID, nullStr(p.Loader), nullStr(p.UpgradeAuthority), int64(p.LastDeploySlot), nullStr(p.BytecodeSHA256), p.IDLPresent, nullStr(p.AnchorVersion), string(p.VerifiedBuild), nullStr(p.VerifiedCommit), panel, p.Chain)
	if err != nil {
		return Program{}, err
	}
	return s.GetProgram(ctx, p.TargetID, p.ProgramID)
}

// GetProgram fetches a program by target and program address.
func (s *Store) GetProgram(ctx context.Context, targetID, programID string) (Program, error) {
	if !ValidID(targetID) {
		return Program{}, ErrNotFound
	}
	return scanProgram(s.Pool.QueryRow(ctx, `select `+programCols+` from target_programs where target_id=$1 and program_id=$2`, targetID, programID))
}

// ListPrograms returns a target's programs in insertion order.
func (s *Store) ListPrograms(ctx context.Context, targetID string) ([]Program, error) {
	if !ValidID(targetID) {
		return []Program{}, nil
	}
	rows, err := s.Pool.Query(ctx, `select `+programCols+` from target_programs where target_id=$1 order by created_at`, targetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Program{}
	for rows.Next() {
		p, err := scanProgram(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ListRepoPrograms lists the programs of every other target on the same
// repository: other deployments of the code under audit, on any chain.
func (s *Store) ListRepoPrograms(ctx context.Context, repo, excludeTargetID string) ([]Program, error) {
	if repo == "" {
		return []Program{}, nil
	}
	rows, err := s.Pool.Query(ctx, `select distinct on (p.program_id, p.chain) `+programColsOf("p")+` from target_programs p join targets t on t.id=p.target_id
		where t.repo_full_name=$1 and p.target_id<>$2 order by p.program_id, p.chain, p.created_at`, repo, excludeTargetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Program{}
	for rows.Next() {
		p, err := scanProgram(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// programColsOf is programCols with every column qualified by an alias.
func programColsOf(alias string) string {
	parts := strings.Split(programCols, ", ")
	for i, p := range parts {
		if strings.HasPrefix(p, "coalesce(") {
			parts[i] = strings.Replace(p, "coalesce(", "coalesce("+alias+".", 1)
		} else {
			parts[i] = alias + "." + p
		}
	}
	return strings.Join(parts, ", ")
}

// DeleteProgram removes a program from a target.
func (s *Store) DeleteProgram(ctx context.Context, targetID, programID string) error {
	tag, err := s.Pool.Exec(ctx, `delete from target_programs where target_id=$1 and program_id=$2`, targetID, programID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SetOwnershipNonce stores the nonce the authority must sign and when it
// was issued.
func (s *Store) SetOwnershipNonce(ctx context.Context, targetID, programID, nonce string) error {
	_, err := s.Pool.Exec(ctx, `update target_programs set ownership_nonce=$3, ownership_nonce_at=now() where target_id=$1 and program_id=$2`, targetID, programID, nonce)
	return err
}

// SetOwnershipProven records a valid signature and updates the panel.
func (s *Store) SetOwnershipProven(ctx context.Context, targetID, programID, signature string, panel core.VerificationPanel) error {
	b, _ := json.Marshal(panel)
	_, err := s.Pool.Exec(ctx, `update target_programs set ownership_proven_at=now(), ownership_signature=$3, ownership_nonce=null, ownership_nonce_at=null, panel=$4 where target_id=$1 and program_id=$2`, targetID, programID, signature, b)
	return err
}

// PanelPatch rewrites the adapter's panel rows after the operator records a
// match result, so the badges and the columns never disagree. The adapter
// supplies it; nil leaves the panel untouched.
type PanelPatch func(panel core.VerificationPanel, pin core.ProgramPin) core.VerificationPanel

// SetProgramMatch records the operator's verified hash and match status and
// regenerates the dependent panel rows through s.PanelPatch.
func (s *Store) SetProgramMatch(ctx context.Context, db DB, targetID string, pin core.ProgramPin) error {
	if s.PanelPatch == nil {
		_, err := db.Exec(ctx, `update target_programs set bytecode_sha256=$3, last_deploy_slot=$4, verified_build=$5 where target_id=$1 and program_id=$2`,
			targetID, pin.ID, pin.BytecodeSHA256, int64(pin.Slot), string(pin.Match))
		return err
	}
	var raw []byte
	if err := db.QueryRow(ctx, `select panel from target_programs where target_id=$1 and program_id=$2 for update`, targetID, pin.ID).Scan(&raw); err != nil {
		return wrap(err)
	}
	var panel core.VerificationPanel
	_ = json.Unmarshal(raw, &panel)
	b, _ := json.Marshal(s.PanelPatch(panel, pin))
	_, err := db.Exec(ctx, `update target_programs set bytecode_sha256=$3, last_deploy_slot=$4, verified_build=$5, panel=$6 where target_id=$1 and program_id=$2`,
		targetID, pin.ID, pin.BytecodeSHA256, int64(pin.Slot), string(pin.Match), b)
	return err
}

// GetInstallationByID fetches an installation by its GitHub id.
func (s *Store) GetInstallationByID(ctx context.Context, installationID int64) (Installation, error) {
	var i Installation
	err := s.Pool.QueryRow(ctx, `select id, coalesce(user_id::text,''), installation_id, account_login, account_type, status from github_installations where installation_id=$1`, installationID).
		Scan(&i.ID, &i.UserID, &i.InstallationID, &i.AccountLogin, &i.AccountType, &i.Status)
	return i, wrap(err)
}

// GetInstallationRow fetches an installation by row id regardless of owner.
func (s *Store) GetInstallationRow(ctx context.Context, id string) (Installation, error) {
	if !ValidID(id) {
		return Installation{}, ErrNotFound
	}
	var i Installation
	err := s.Pool.QueryRow(ctx, `select id, coalesce(user_id::text,''), installation_id, account_login, account_type, status from github_installations where id=$1`, id).
		Scan(&i.ID, &i.UserID, &i.InstallationID, &i.AccountLogin, &i.AccountType, &i.Status)
	return i, wrap(err)
}

// SetInstallationStatus updates an installation's status by GitHub id. A
// webhook for an installation nobody has bound yet is not an error.
func (s *Store) SetInstallationStatus(ctx context.Context, installationID int64, status string) error {
	_, err := s.Pool.Exec(ctx, `update github_installations set status=$2 where installation_id=$1`, installationID, status)
	return err
}

// ---- installations ----

// ListInstallations returns a user's GitHub App installations.
func (s *Store) ListInstallations(ctx context.Context, userID string) ([]Installation, error) {
	rows, err := s.Pool.Query(ctx, `select id, user_id, installation_id, account_login, account_type, status from github_installations where user_id=$1 order by created_at`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Installation{}
	for rows.Next() {
		var i Installation
		if err := rows.Scan(&i.ID, &i.UserID, &i.InstallationID, &i.AccountLogin, &i.AccountType, &i.Status); err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

// GetInstallation fetches an installation owned by the user.
func (s *Store) GetInstallation(ctx context.Context, userID, id string) (Installation, error) {
	if !ValidID(id) {
		return Installation{}, ErrNotFound
	}
	var i Installation
	err := s.Pool.QueryRow(ctx, `select id, user_id, installation_id, account_login, account_type, status from github_installations where id=$1 and user_id=$2`, id, userID).
		Scan(&i.ID, &i.UserID, &i.InstallationID, &i.AccountLogin, &i.AccountType, &i.Status)
	return i, wrap(err)
}

// UpsertInstallation stores an installation (webhook, setup redirect or
// seed). An empty UserID leaves the existing owner in place; the webhook
// does not know the user, the setup redirect does.
func (s *Store) UpsertInstallation(ctx context.Context, i Installation) (Installation, error) {
	id := NewID()
	err := s.Pool.QueryRow(ctx, `insert into github_installations(id, user_id, installation_id, account_login, account_type, status) values($1,$2,$3,$4,$5,$6)
		on conflict (installation_id) do update set user_id=coalesce(excluded.user_id, github_installations.user_id),
		  account_login=case when excluded.account_login='' then github_installations.account_login else excluded.account_login end,
		  account_type=case when excluded.account_type='' then github_installations.account_type else excluded.account_type end,
		  status=excluded.status
		returning id, coalesce(user_id::text,''), account_login, account_type`,
		id, nullStr(i.UserID), i.InstallationID, i.AccountLogin, i.AccountType, i.Status).Scan(&i.ID, &i.UserID, &i.AccountLogin, &i.AccountType)
	return i, err
}
