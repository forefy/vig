-- +goose Up
-- Captures that show a finding happening. Bytes live here, keyed by the id
-- the findings document references; revisions keep the reference.
create table finding_visuals (
    id uuid primary key,
    audit_id uuid not null references audits(id) on delete cascade,
    ordinal integer not null,
    caption text not null,
    mime text not null,
    sha256 text not null,
    bytes bytea not null,
    created_at timestamptz not null default now()
);
create index finding_visuals_audit_idx on finding_visuals(audit_id, ordinal);

-- +goose Down
drop table if exists finding_visuals;
