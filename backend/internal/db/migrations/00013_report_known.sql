-- +goose Up
-- What earlier reports raised on the same code, carried on the report.
alter table reports add column known jsonb;

-- +goose Down
alter table reports drop column if exists known;
