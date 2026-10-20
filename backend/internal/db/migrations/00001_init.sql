-- +goose Up
create table users (
  id uuid primary key,
  display_name text not null default '',
  avatar_url text not null default '',
  role text not null default 'user' check (role in ('user','admin')),
  totp_secret text,
  totp_enabled boolean not null default false,
  created_at timestamptz not null default now()
);

create table identities (
  id uuid primary key,
  user_id uuid not null references users(id) on delete cascade,
  provider text not null check (provider in ('google','github','solana','discord')),
  provider_id text not null,
  email text,
  handle text,
  created_at timestamptz not null default now(),
  unique (provider, provider_id)
);
create index identities_user_idx on identities(user_id);

-- A session is one login: the refresh-token family. Access tokens are
-- stateless JWTs that carry the session id, so revoking the session kills
-- every token minted for it.
create table sessions (
  id uuid primary key,
  user_id uuid not null references users(id) on delete cascade,
  expires_at timestamptz not null,
  totp_at timestamptz,
  ip text,
  ua text,
  revoked_at timestamptz,
  created_at timestamptz not null default now()
);
create index sessions_user_idx on sessions(user_id);

create table refresh_tokens (
  id uuid primary key,
  session_id uuid not null references sessions(id) on delete cascade,
  hash bytea not null unique,
  expires_at timestamptz not null,
  replaced_by uuid,
  revoked_at timestamptz,
  created_at timestamptz not null default now()
);

create table siws_nonces (
  nonce text primary key,
  expires_at timestamptz not null
);

create table api_keys (
  id uuid primary key,
  user_id uuid not null references users(id) on delete cascade,
  prefix text not null,
  hash bytea not null unique,
  name text not null,
  last_used_at timestamptz,
  revoked_at timestamptz,
  created_at timestamptz not null default now()
);

create table github_installations (
  id uuid primary key,
  user_id uuid not null references users(id) on delete cascade,
  installation_id bigint not null unique,
  account_login text not null,
  account_type text not null,
  status text not null check (status in ('active','pending','removed')),
  created_at timestamptz not null default now()
);

create table targets (
  id uuid primary key,
  adapter text not null,
  owner_user_id uuid references users(id) on delete set null,
  source_kind text not null check (source_kind in ('public','installation')),
  repo_full_name text not null,
  subpath text not null default '',
  installation_id uuid references github_installations(id) on delete set null,
  selector text not null check (selector in ('branch','tag','commit')),
  revision text not null,
  commit_sha text not null,
  commit_author text,
  commit_date timestamptz,
  created_at timestamptz not null default now()
);
create index targets_owner_idx on targets(owner_user_id);

create table target_programs (
  id uuid primary key,
  target_id uuid not null references targets(id) on delete cascade,
  program_id text not null,
  loader text,
  upgrade_authority text,
  last_deploy_slot bigint,
  bytecode_sha256 text,
  idl_present boolean not null default false,
  anchor_version text,
  verified_build text not null default 'unverified' check (verified_build in ('verified','self_built','mismatch','unverified')),
  verified_commit text,
  ownership_nonce text,
  ownership_proven_at timestamptz,
  ownership_signature text,
  panel jsonb not null default '{}'::jsonb,
  created_at timestamptz not null default now(),
  unique (target_id, program_id)
);

create table scans (
  id uuid primary key,
  token text not null unique,
  target_id uuid references targets(id) on delete set null,
  repo_full_name text not null,
  commit_sha text not null,
  program_id text,
  ip_hash bytea not null,
  fingerprint_hash bytea,
  state text not null check (state in ('queued','running','done','failed')),
  result jsonb,
  error text,
  expires_at timestamptz not null,
  created_at timestamptz not null default now()
);
create index scans_ip_day_idx on scans(ip_hash, created_at);

create table audits (
  id uuid primary key,
  user_id uuid not null references users(id) on delete cascade,
  target_id uuid not null references targets(id),
  coverage text not null check (coverage in ('full','diff')),
  base_sha text,
  head_sha text not null,
  programs jsonb not null,
  scope jsonb not null,
  state text not null check (state in ('queued','claimed','cloning','programs_verified','analysis','human_review','delivered','delayed','failed')),
  resume_state text,
  eta_at timestamptz,
  delayed_reason text,
  failed_reason text,
  cost_kind text not null check (cost_kind in ('credit','slot')),
  ledger_entry_id uuid,
  slot_plan_id uuid,
  slot_prev_run_at timestamptz,
  report_id uuid,
  claimed_at timestamptz,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now()
);
create index audits_user_idx on audits(user_id, created_at desc);
create index audits_state_idx on audits(state) where state in ('queued','delayed');
create index audits_dedup_idx on audits(user_id, head_sha, coalesce(base_sha,''), programs) where state <> 'failed';

create table audit_events (
  id uuid primary key,
  audit_id uuid not null references audits(id) on delete cascade,
  kind text not null,
  actor text not null check (actor in ('user','operator','system')),
  actor_id uuid,
  state text,
  payload jsonb not null default '{}'::jsonb,
  created_at timestamptz not null default now()
);
create index audit_events_audit_idx on audit_events(audit_id, created_at);

create table reports (
  id uuid primary key,
  audit_id uuid not null unique references audits(id) on delete cascade,
  pin jsonb not null,
  summary jsonb not null,
  published_slug text unique,
  published_at timestamptz,
  created_at timestamptz not null default now()
);

create table findings (
  id uuid primary key,
  report_id uuid not null references reports(id) on delete cascade,
  ordinal int not null,
  severity text not null check (severity in ('critical','high','medium','low','info')),
  title text not null,
  location jsonb not null,
  description text not null,
  exploit_path text,
  fix text not null,
  refs jsonb not null default '[]'::jsonb,
  status text not null default 'open' check (status in ('open','fixed','acknowledged','disputed')),
  carried_from_finding_id uuid references findings(id),
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  unique (report_id, ordinal)
);

create table purchases (
  id uuid primary key,
  user_id uuid not null references users(id) on delete cascade,
  sku text not null check (sku in ('mini','pro')),
  amount_usdc bigint not null,
  memo_nonce text not null unique,
  treasury text not null,
  usdc_mint text not null,
  state text not null check (state in ('pending','confirmed','failed','expired')),
  tx_signature text unique,
  confirmed_slot bigint,
  promo text,
  expires_at timestamptz not null,
  created_at timestamptz not null default now()
);
create index purchases_user_idx on purchases(user_id, created_at desc);

create table plans (
  id uuid primary key,
  user_id uuid not null references users(id) on delete cascade,
  kind text not null check (kind in ('pro','team')),
  starts_at timestamptz not null,
  ends_at timestamptz not null,
  run_cap_per_day int not null default 1,
  last_run_at timestamptz,
  purchase_id uuid references purchases(id),
  created_at timestamptz not null default now()
);
create index plans_user_active_idx on plans(user_id, ends_at desc);

create table ledger (
  id uuid primary key,
  user_id uuid not null references users(id) on delete cascade,
  kind text not null check (kind in ('grant','purchase','consume','refund','revoke','expire')),
  delta int not null,
  balance_after int not null check (balance_after >= 0),
  ref_kind text,
  ref_id uuid,
  reason text,
  actor_id uuid,
  expires_at timestamptz,
  created_at timestamptz not null default now()
);
create index ledger_user_idx on ledger(user_id, created_at desc);

create table tweet_claims (
  id uuid primary key,
  user_id uuid not null references users(id) on delete cascade,
  nonce text not null,
  url text not null,
  state text not null check (state in ('submitted','approved','rejected','revoked')),
  reviewed_by uuid references users(id),
  reviewed_at timestamptz,
  reason text,
  ledger_entry_id uuid references ledger(id),
  created_at timestamptz not null default now()
);
create index tweet_claims_state_idx on tweet_claims(state, created_at);

create table notifications (
  id uuid primary key,
  user_id uuid not null references users(id) on delete cascade,
  kind text not null,
  payload jsonb not null default '{}'::jsonb,
  channels jsonb not null default '[]'::jsonb,
  delivered jsonb not null default '{}'::jsonb,
  read_at timestamptz,
  created_at timestamptz not null default now()
);
create index notifications_user_idx on notifications(user_id, created_at desc);

-- audit_id is null until a claim token is used; the claim binds it to the
-- audit it took, so no token ever reads more than one customer's repo.
create table operator_tokens (
  id uuid primary key,
  audit_id uuid references audits(id) on delete cascade,
  hash bytea not null unique,
  scope jsonb not null default '{}'::jsonb,
  expires_at timestamptz not null,
  used_at timestamptz,
  revoked_at timestamptz,
  created_by uuid references users(id),
  created_at timestamptz not null default now()
);

create table admin_actions (
  id uuid primary key,
  admin_id uuid not null references users(id),
  kind text not null,
  subject_kind text not null,
  subject_id uuid,
  payload jsonb not null default '{}'::jsonb,
  created_at timestamptz not null default now()
);

-- +goose Down
drop table if exists admin_actions, operator_tokens, notifications, tweet_claims, ledger, plans, purchases,
  findings, reports, audit_events, audits, scans, target_programs, targets, github_installations,
  api_keys, siws_nonces, refresh_tokens, sessions, identities, users cascade;
