-- +goose Up
-- An audit's index: the Parquet tables an operator built from the
-- checkout, the captures and the dumps of the job, read by Atlas in the
-- browser. The bytes live on disk under DATA_DIR/index/<audit_id>; this
-- row is the catalogue and the proof of what was built.
create table audit_indexes (
  audit_id     uuid primary key references audits(id) on delete cascade,
  tool         text not null,
  tool_version text not null default '',
  bytes        bigint not null default 0,
  tables       jsonb not null default '[]'::jsonb,
  built_at     timestamptz not null default now()
);

-- +goose Down
drop table if exists audit_indexes;
