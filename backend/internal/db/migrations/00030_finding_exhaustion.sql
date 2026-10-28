-- +goose Up
-- What the reproduction ruled out: one answer per item vig lists for the
-- finding, done or not, with a note when not; shown as a row of dots.
alter table findings add column exhaustion jsonb;

-- +goose Down
alter table findings drop column if exists exhaustion;
