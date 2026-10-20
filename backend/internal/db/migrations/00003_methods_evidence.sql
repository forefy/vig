-- +goose Up
-- How a report was produced (static, bytecode, on-chain, fuzzing campaign)
-- and what established each finding. Both are optional; older rows are null.
alter table reports add column methods jsonb;
alter table findings add column evidence jsonb;

-- +goose Down
alter table findings drop column if exists evidence;
alter table reports drop column if exists methods;
