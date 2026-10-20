-- +goose Up
-- Further places a finding points at (a flow of steps, or repeated
-- occurrences), each with its own captured code. Optional.
alter table findings add column sites_kind text check (sites_kind in ('flow','occurrences'));
alter table findings add column sites jsonb;

-- +goose Down
alter table findings drop column if exists sites;
alter table findings drop column if exists sites_kind;
