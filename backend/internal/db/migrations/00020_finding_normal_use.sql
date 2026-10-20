-- +goose Up
-- How the code is ordinarily used, shown before expected on the chain.
alter table findings add column normal_use text;

-- +goose Down
alter table findings drop column if exists normal_use;
