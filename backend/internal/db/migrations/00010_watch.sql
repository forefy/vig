-- +goose Up
-- The watch: the surface kept live after mapping. Live rows share the
-- evidence table so a live row and a sampled row are the same row; they
-- are flagged so the sample the report cites is never displaced, and they
-- age out while the per-minute rollups keep the count forever.
alter table surface_evidence add column live boolean not null default false;
alter table surface_evidence drop constraint surface_evidence_kind_check;
alter table surface_evidence add constraint surface_evidence_kind_check check (kind in ('tx','request','review','replay','write'));
create index surface_evidence_live_idx on surface_evidence(audit_id, id) where live;

-- One row per audit: where every session stopped and what it subscribes to.
create table surface_watch (
  audit_id uuid primary key references audits(id) on delete cascade,
  state text not null default 'connecting' check (state in ('connecting','backfilling','live','error','ended')),
  error text,
  cursors jsonb not null default '{}'::jsonb,
  accounts jsonb not null default '[]'::jsonb,
  started_at timestamptz not null default now(),
  last_event_at timestamptz,
  last_slot bigint not null default 0,
  ends_at timestamptz,
  updated_at timestamptz not null default now()
);

-- Slot ranges of one subject, with how they were seen. Live spans are
-- extended in place; a gap is a fact like any other.
create table surface_coverage (
  id uuid primary key,
  audit_id uuid not null references audits(id) on delete cascade,
  subject text not null,
  kind text not null check (kind in ('live','backfill','gap')),
  from_slot bigint not null,
  to_slot bigint not null,
  from_at timestamptz,
  to_at timestamptz
);
create index surface_coverage_audit_idx on surface_coverage(audit_id, subject, from_slot);

-- Traffic per node per minute, incremented as rows land. Never pruned.
create table surface_rollups (
  audit_id uuid not null references audits(id) on delete cascade,
  node_id text not null,
  minute timestamptz not null,
  calls int not null default 0,
  failed int not null default 0,
  units bigint not null default 0,
  primary key (audit_id, node_id, minute)
);

-- +goose Down
drop table if exists surface_rollups;
drop table if exists surface_coverage;
drop table if exists surface_watch;
drop index if exists surface_evidence_live_idx;
alter table surface_evidence drop constraint surface_evidence_kind_check;
alter table surface_evidence add constraint surface_evidence_kind_check check (kind in ('tx','request','review','replay'));
alter table surface_evidence drop column if exists live;
