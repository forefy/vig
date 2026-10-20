-- +goose Up
-- What the finding costs when it happens, rendered last on the report.
alter table findings add column impact text;

-- +goose Down
alter table findings drop column if exists impact;
