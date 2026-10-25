// Package api declares the HTTP contract. Every route is registered with real
// request and response types so the OpenAPI spec, Scalar docs and the
// generated TypeScript client are complete from phase 0. Handlers that are
// not implemented yet return 501 with a problem+json body.
//
// Since phase 1 every operation is implemented; the ones that depend on a
// deferred integration (GitHub App, Discord, USDC treasury) answer 503.
package api

import (
	"time"

	"github.com/forefy/vig/backend/internal/core"
)

// ---- identity ----

type User struct {
	ID          string    `json:"id"`
	DisplayName string    `json:"display_name"`
	AvatarURL   string    `json:"avatar_url,omitempty"`
	Role        string    `json:"role" enum:"user,admin"`
	CreatedAt   time.Time `json:"created_at"`
}

type Identity struct {
	ID         string    `json:"id"`
	Provider   string    `json:"provider" enum:"google,github,solana,discord"`
	ProviderID string    `json:"provider_id"`
	Email      string    `json:"email,omitempty"`
	Handle     string    `json:"handle,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

type Me struct {
	User               User                `json:"user"`
	Identities         []Identity          `json:"identities"`
	Credits            int                 `json:"credits"`
	Plan               *PlanState          `json:"plan,omitempty"`
	TOTPEnabled        bool                `json:"totp_enabled"`
	TOTPVerifiedAt     *time.Time          `json:"totp_verified_at,omitempty" doc:"When this session last passed TOTP; admin routes need it within 12h"`
	RecoveryCodesLeft  int                 `json:"recovery_codes_left"`
	NotifyEmail        bool                `json:"notify_email"`
	RetentionDays      int                 `json:"retention_days" doc:"Days a source checkout outlives delivery; reports stay while the account exists"`
	DiscordGuildMember *bool               `json:"discord_guild_member,omitempty" doc:"Whether the linked Discord account is in the guild; absent when no guild is configured"`
	DiscordInviteURL   string              `json:"discord_invite_url,omitempty"`
	Impersonation      *ImpersonationState `json:"impersonation,omitempty" doc:"Present while an admin is viewing as this user; the session is read-only until it ends"`
}

// ImpersonationState is a live "view as user" grant.
type ImpersonationState struct {
	ID        string    `json:"id"`
	AdminID   string    `json:"admin_id"`
	UserID    string    `json:"user_id" doc:"The user being viewed"`
	AuditID   string    `json:"audit_id,omitempty" doc:"The audit the admin came from"`
	ExpiresAt time.Time `json:"expires_at"`
}

type TOTPStatus struct {
	Enabled       bool       `json:"enabled"`
	VerifiedAt    *time.Time `json:"verified_at,omitempty"`
	RecoveryCodes []string   `json:"recovery_codes,omitempty" doc:"Shown once, when TOTP is enabled or the set is regenerated"`
}

type Session struct {
	ID        string     `json:"id"`
	Current   bool       `json:"current"`
	IP        string     `json:"ip,omitempty"`
	UserAgent string     `json:"user_agent,omitempty"`
	TOTPAt    *time.Time `json:"totp_at,omitempty"`
	ExpiresAt time.Time  `json:"expires_at"`
	CreatedAt time.Time  `json:"created_at"`
}

type NotificationPrefs struct {
	Email bool `json:"email"`
}

type TOTPSetup struct {
	Secret     string `json:"secret"`
	OTPAuthURL string `json:"otpauth_url"`
}

type VersionInfo struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
}

type PlanState struct {
	Kind       string    `json:"kind" enum:"pro,team"`
	StartsAt   time.Time `json:"starts_at"`
	EndsAt     time.Time `json:"ends_at"`
	RunsPerDay int       `json:"runs_per_day"`
	NextSlotAt time.Time `json:"next_slot_at"`
}

type APIKey struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
}

type APIKeyCreated struct {
	APIKey
	Secret string `json:"secret" doc:"Shown once"`
}

type Installation struct {
	ID             string `json:"id"`
	InstallationID int64  `json:"installation_id"`
	AccountLogin   string `json:"account_login"`
	AccountType    string `json:"account_type" enum:"User,Organization"`
	Status         string `json:"status" enum:"active,pending,removed"`
}

// ---- targets ----

type Target struct {
	ID             string       `json:"id"`
	Adapter        string       `json:"adapter"`
	BaseURL        string       `json:"base_url,omitempty" format:"uri" doc:"For a site, the origin the audit is about"`
	Site           *TargetSite  `json:"site,omitempty" doc:"Present on a live target: its scope, its intake panel and its ownership proof"`
	SourceKind     string       `json:"source_kind" enum:"public,installation"`
	Repo           string       `json:"repo" doc:"owner/name"`
	Path           string       `json:"path,omitempty" doc:"Subdirectory from a /tree/<rev>/<path> URL"`
	InstallationID string       `json:"installation_id,omitempty"`
	Revision       Revision     `json:"revision"`
	Programs       []Program    `json:"programs"`
	Declared       []DeclaredID `json:"declared" doc:"Program ids the source declares (declare_id!, Anchor.toml); a fact even before deployment"`
	Scanned        bool         `json:"scanned" doc:"The checkout has been read; declared is complete"`
	CreatedAt      time.Time    `json:"created_at"`
}

// TargetSite is a live target's half of Target: what the customer authorised
// and what the intake read. The origin stands where a program stands.
type TargetSite struct {
	Origin            string                 `json:"origin" format:"uri"`
	Scope             core.SiteScope         `json:"scope"`
	Panel             core.VerificationPanel `json:"panel"`
	OwnershipProven   bool                   `json:"ownership_proven"`
	OwnershipMethod   string                 `json:"ownership_method,omitempty" enum:"dns_txt,well_known_file"`
	OwnershipProvenAt *time.Time             `json:"ownership_proven_at,omitempty"`
}

// DeclaredID is a program id named by the source and where.
type DeclaredID struct {
	ID      string `json:"id"`
	File    string `json:"file"`
	OnChain string `json:"on_chain" enum:"live,account,absent,unknown" doc:"live: an executable program; account: exists but is not a program (a mint, a PDA); absent: nothing at the address"`
}

type Revision struct {
	Selector string    `json:"selector" enum:"branch,tag,commit"`
	Value    string    `json:"value"`
	Commit   string    `json:"commit" doc:"Resolved full sha"`
	Author   string    `json:"author,omitempty"`
	Date     time.Time `json:"date"`
}

type Program struct {
	ID               string                 `json:"id" doc:"Program address"`
	Chain            string                 `json:"chain,omitempty" doc:"Chain id for adapters that serve several; empty for Solana mainnet"`
	Loader           string                 `json:"loader,omitempty"`
	UpgradeAuthority string                 `json:"upgrade_authority,omitempty"`
	LastDeploySlot   uint64                 `json:"last_deploy_slot,omitempty"`
	BytecodeSHA256   string                 `json:"bytecode_sha256,omitempty"`
	IDLPresent       bool                   `json:"idl_present"`
	AnchorVersion    string                 `json:"anchor_version,omitempty"`
	VerifiedBuild    core.MatchStatus       `json:"verified_build" enum:"verified,self_built,mismatch,unverified"`
	VerifiedCommit   string                 `json:"verified_commit,omitempty"`
	OwnershipProven  bool                   `json:"ownership_proven"`
	Panel            core.VerificationPanel `json:"panel"`
}

type Tree struct {
	Nodes         []core.TreeNode `json:"nodes"`
	SelectedLines int             `json:"selected_lines"`
	SelectedCode  int             `json:"selected_code" doc:"Code lines in the selection; the cap applies to this"`
	SelectedFiles int             `json:"selected_files"`
	TotalFiles    int             `json:"total_files"`
	MaxLines      int             `json:"max_lines" doc:"Cap for the user's tier"`
}

// ---- audits ----

type Scope struct {
	Files []string `json:"files"`
	Lines int      `json:"lines"`
}

type AuditEvent struct {
	ID        string          `json:"id"`
	Kind      string          `json:"kind"`
	Actor     string          `json:"actor" enum:"user,operator,system"`
	State     core.AuditState `json:"state,omitempty"`
	Payload   map[string]any  `json:"payload,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
}

type Audit struct {
	ID            string          `json:"id"`
	TargetID      string          `json:"target_id"`
	Repo          string          `json:"repo"`
	Adapter       string          `json:"adapter" enum:"solana,ethereum,webapp,repo" doc:"The target's adapter: which chain's words the audit speaks, webapp for a live website, repo for a repository with no chain"`
	Coverage      string          `json:"coverage" enum:"full,diff"`
	BaseSHA       string          `json:"base_sha,omitempty"`
	HeadSHA       string          `json:"head_sha"`
	ProgramIDs    []string        `json:"program_ids"`
	Scope         Scope           `json:"scope"`
	State         core.AuditState `json:"state"`
	ResumeState   core.AuditState `json:"resume_state,omitempty"`
	ETAAt         *time.Time      `json:"eta_at,omitempty"`
	DelayedReason string          `json:"delayed_reason,omitempty"`
	FailedReason  string          `json:"failed_reason,omitempty"`
	CostKind      string          `json:"cost_kind" enum:"credit,slot"`
	ReportID      string          `json:"report_id,omitempty"`
	Events        []AuditEvent    `json:"events,omitempty"`
	CreatedAt     time.Time       `json:"created_at"`
	UpdatedAt     time.Time       `json:"updated_at"`
	HiddenAt      *time.Time      `json:"hidden_at,omitempty" doc:"Set when an admin took the audit off its owner's list; owners never see a hidden audit"`
	HiddenReason  string          `json:"hidden_reason,omitempty"`
}

type Report struct {
	ID            string                `json:"id"`
	AuditID       string                `json:"audit_id"`
	Adapter       string                `json:"adapter,omitempty" enum:"solana,ethereum,webapp,repo"`
	Document      core.FindingsDocument `json:"document"`
	IsOwner       bool                  `json:"is_owner" doc:"Whether exploit paths are included"`
	PublishedSlug string                `json:"published_slug,omitempty"`
	PublishedAt   *time.Time            `json:"published_at,omitempty"`
	CreatedAt     time.Time             `json:"created_at"`
	Revision      int                   `json:"revision" doc:"1 for the delivered report, one more per revision"`
	Supersedes    string                `json:"supersedes,omitempty" doc:"The report version this one replaced"`
}

// ---- scans ----

type Scan struct {
	Token     string          `json:"token"`
	Repo      string          `json:"repo"`
	Commit    string          `json:"commit"`
	ProgramID string          `json:"program_id,omitempty"`
	State     string          `json:"state" enum:"queued,running,done,failed"`
	Result    *core.Preflight `json:"result,omitempty"`
	ExpiresAt time.Time       `json:"expires_at"`
	Remaining int             `json:"remaining_today"`
}

// ---- billing ----

type Purchase struct {
	ID            string    `json:"id"`
	SKU           core.SKU  `json:"sku" enum:"mini,pro"`
	AmountUSDC    int64     `json:"amount_usdc" doc:"Base units, 6 decimals"`
	MemoNonce     string    `json:"memo_nonce"`
	Treasury      string    `json:"treasury" doc:"Recipient wallet"`
	USDCMint      string    `json:"usdc_mint"`
	State         string    `json:"state" enum:"pending,confirmed,failed,expired"`
	TxSignature   string    `json:"tx_signature,omitempty"`
	ConfirmedSlot uint64    `json:"confirmed_slot,omitempty"`
	ExpiresAt     time.Time `json:"expires_at"`
	CreatedAt     time.Time `json:"created_at"`
}

type LedgerRow struct {
	ID string `json:"id"`
	core.LedgerEntry
	CreatedAt time.Time `json:"created_at"`
}

type TweetClaim struct {
	ID         string     `json:"id"`
	UserID     string     `json:"user_id"`
	Nonce      string     `json:"nonce"`
	URL        string     `json:"url"`
	State      string     `json:"state" enum:"submitted,approved,rejected,revoked"`
	Reason     string     `json:"reason,omitempty"`
	ReviewedAt *time.Time `json:"reviewed_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
}

type Notification struct {
	ID        string         `json:"id"`
	Kind      string         `json:"kind"`
	Payload   map[string]any `json:"payload"`
	ReadAt    *time.Time     `json:"read_at,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
}

// ---- operator ----

type Job struct {
	Audit             Audit      `json:"audit"`
	Target            Target     `json:"target" doc:"Resolved source and the programs with their on-chain facts at intake"`
	Token             string     `json:"token" doc:"Job-scoped operator token"`
	TokenExpiresAt    time.Time  `json:"token_expires_at"`
	CloneURL          string     `json:"clone_url"`
	CloneToken        string     `json:"clone_token,omitempty" doc:"One-hour GitHub installation token for private repos"`
	CloneTokenExpires *time.Time `json:"clone_token_expires_at,omitempty"`
	Lessons           []Lesson   `json:"lessons,omitempty" doc:"What owners and admins corrected on earlier reports of this repository: findings archived as not relevant and re-ratings, each with its reason. Read before writing findings; do not raise what was archived unless the code changed"`
}

// Lesson is one correction a person made to a delivered finding, with its
// reason: what the next report on the same code should not repeat.
type Lesson struct {
	AuditID   string        `json:"audit_id"`
	Repo      string        `json:"repo"`
	FindingID string        `json:"finding_id"`
	Ordinal   int           `json:"ordinal"`
	Title     string        `json:"title"`
	Severity  core.Severity `json:"severity" doc:"The finding's current severity"`
	Kind      string        `json:"kind" enum:"severity,status" doc:"What moved"`
	From      string        `json:"from"`
	To        string        `json:"to" doc:"For status, archived means judged not relevant"`
	Reason    string        `json:"reason"`
	By        string        `json:"by" enum:"owner,admin,operator"`
	At        time.Time     `json:"at"`
}

// ---- admin ----

type AdminAction struct {
	ID          string         `json:"id"`
	AdminID     string         `json:"admin_id"`
	Kind        string         `json:"kind"`
	SubjectKind string         `json:"subject_kind"`
	SubjectID   string         `json:"subject_id"`
	Payload     map[string]any `json:"payload,omitempty"`
	CreatedAt   time.Time      `json:"created_at"`
}

type AdminUser struct {
	User       User       `json:"user"`
	Identities []Identity `json:"identities"`
	Credits    int        `json:"credits"`
	Plan       *PlanState `json:"plan,omitempty"`
	Audits     int        `json:"audits"`
}

type AdminUserDetail struct {
	AdminUser
	Ledger    []LedgerRow `json:"ledger"`
	AuditList []Audit     `json:"audit_list"`
}

type AdminAudit struct {
	Audit
	UserID   string        `json:"user_id"`
	UserName string        `json:"user_name,omitempty"`
	Summary  *core.Summary `json:"summary,omitempty" doc:"Report summary when a report exists"`
}

// ReportRevision is what a revision changed against the version before.
type ReportRevision struct {
	Report  Report `json:"report"`
	Added   int    `json:"added"`
	Removed int    `json:"removed"`
	Kept    int    `json:"kept" doc:"Findings matched to the version before by carried_from_id or by title and file; they keep their status"`
}
