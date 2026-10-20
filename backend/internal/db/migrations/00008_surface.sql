-- +goose Up
-- The surface map: what a target exposes, as the audit knows it, and every
-- observation on it. The document is the tree; attention is derived on read.
create table audit_surfaces (
  audit_id uuid primary key references audits(id) on delete cascade,
  doc jsonb not null,
  built_at timestamptz not null default now(),
  updated_at timestamptz not null default now()
);

create table surface_evidence (
  id uuid primary key,
  audit_id uuid not null references audits(id) on delete cascade,
  node_id text not null,
  kind text not null check (kind in ('tx','request','review')),
  ref text not null default '',
  actor text not null check (actor in ('system','operator')),
  actor_id text,
  at timestamptz,
  summary jsonb not null default '{}'::jsonb,
  created_at timestamptz not null default now()
);
create index surface_evidence_audit_idx on surface_evidence(audit_id, node_id);
create unique index surface_evidence_ref_idx on surface_evidence(audit_id, node_id, ref) where ref <> '';

alter table audits add column surface_attempts int not null default 0;
alter table audits add column surface_tried_at timestamptz;
alter table audits add column surface_error text;

-- +goose Down
alter table audits drop column if exists surface_error;
alter table audits drop column if exists surface_tried_at;
alter table audits drop column if exists surface_attempts;
drop table if exists surface_evidence;
drop table if exists audit_surfaces;
