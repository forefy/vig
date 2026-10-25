-- +goose Up
-- The lines each GitHub file link in a finding's references points at,
-- read when the report was stored and pinned to their commit, so the page
-- shows the referenced code and the report never depends on a branch moving.
alter table findings add column reference_code jsonb;

-- +goose Down
alter table findings drop column if exists reference_code;
