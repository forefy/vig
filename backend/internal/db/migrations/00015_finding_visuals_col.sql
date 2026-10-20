-- +goose Up
-- Captures referenced from findings[].visuals; the bytes live in
-- finding_visuals keyed by the id the document carries.
alter table findings add column visuals jsonb;

-- +goose Down
alter table findings drop column if exists visuals;
