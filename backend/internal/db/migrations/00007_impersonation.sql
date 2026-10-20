-- +goose Up
-- An admin viewing the app as one user for a bounded time. Bound to the
-- admin's own browser session: while a row is live, that session resolves to
-- the target user and is read-only. Begin and end are admin actions.
create table impersonations (
  id uuid primary key,
  admin_id uuid not null references users(id) on delete cascade,
  session_id uuid not null references sessions(id) on delete cascade,
  target_user_id uuid not null references users(id) on delete cascade,
  audit_id uuid references audits(id) on delete set null,
  started_at timestamptz not null default now(),
  expires_at timestamptz not null,
  ended_at timestamptz
);
create index impersonations_live_idx on impersonations(session_id) where ended_at is null;

-- +goose Down
drop table if exists impersonations;
