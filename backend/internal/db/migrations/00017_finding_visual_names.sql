-- +goose Up
-- Attachments widen from captures to recordings and files: a file is saved
-- under the name it was uploaded with.
alter table finding_visuals add column name text not null default '';

-- +goose Down
alter table finding_visuals drop column if exists name;
