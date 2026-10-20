-- +goose Up

-- Email preference and account anonymisation marker.
alter table users add column notify_email boolean not null default true;
alter table users add column deleted_at timestamptz;

-- External delivery is asynchronous: a row with next_attempt_at set is
-- waiting for the dispatcher; null means nothing is pending.
alter table notifications add column attempts int not null default 0;
alter table notifications add column next_attempt_at timestamptz;
create index notifications_due_idx on notifications(next_attempt_at) where next_attempt_at is not null;

-- TOTP recovery codes, stored hashed, consumed once.
create table recovery_codes (
  id uuid primary key,
  user_id uuid not null references users(id) on delete cascade,
  hash bytea not null,
  used_at timestamptz,
  created_at timestamptz not null default now(),
  unique (user_id, hash)
);
create index recovery_codes_user_idx on recovery_codes(user_id) where used_at is null;

-- A webhook may announce an installation before its user binds it through
-- the setup redirect, so the owner is filled in later.
alter table github_installations alter column user_id drop not null;

-- Ownership nonces expire; remember when one was issued.
alter table target_programs add column ownership_nonce_at timestamptz;

-- Hot query support.
create index scans_fp_day_idx on scans(fingerprint_hash, created_at) where fingerprint_hash is not null;
create index ledger_expiry_idx on ledger(expires_at) where delta > 0;
create index audits_eta_idx on audits(eta_at) where state not in ('delivered','failed');
create index audits_target_idx on audits(target_id, head_sha) where state not in ('delivered','failed');
create index purchases_pending_idx on purchases(expires_at) where state = 'pending';
create index sessions_live_idx on sessions(user_id, created_at desc) where revoked_at is null;
create index github_installations_user_idx on github_installations(user_id);

-- +goose Down
drop index if exists github_installations_user_idx, sessions_live_idx, purchases_pending_idx,
  audits_target_idx, audits_eta_idx, ledger_expiry_idx, scans_fp_day_idx, recovery_codes_user_idx, notifications_due_idx;
alter table target_programs drop column if exists ownership_nonce_at;
delete from github_installations where user_id is null;
alter table github_installations alter column user_id set not null;
drop table if exists recovery_codes;
alter table notifications drop column if exists next_attempt_at;
alter table notifications drop column if exists attempts;
alter table users drop column if exists deleted_at;
alter table users drop column if exists notify_email;
