-- +goose Up
-- The code each fix fence applies to, captured from the checkout, so the
-- page shows the fence as a patch against the lines it replaces.
alter table findings add column fix_sites jsonb;

-- +goose Down
alter table findings drop column if exists fix_sites;
