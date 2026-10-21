-- +goose Up
-- What closes a finding: code (a patch against the located lines, the
-- default) or operational (rotate, revoke, configure; nothing to patch).
alter table findings add column fix_kind text;

-- +goose Down
alter table findings drop column if exists fix_kind;
