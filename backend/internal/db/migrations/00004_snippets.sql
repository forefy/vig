-- +goose Up
-- The code each finding points at, captured from the pinned commit when the
-- report is submitted. Optional; findings from before this carry null.
alter table findings add column snippet jsonb;

-- +goose Down
alter table findings drop column if exists snippet;
