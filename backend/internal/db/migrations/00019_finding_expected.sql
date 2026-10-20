-- +goose Up
-- The groundwork of a finding: what the code is supposed to guarantee at
-- the location, shown first on the reproduction chain. Impact is last.
alter table findings add column expected text;

-- +goose Down
alter table findings drop column if exists expected;
