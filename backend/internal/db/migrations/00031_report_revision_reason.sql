-- +goose Up
-- Why a report was revised, in a line; the first version carries none.
alter table reports add column revision_reason text not null default '';

-- +goose Down
alter table reports drop column if exists revision_reason;
