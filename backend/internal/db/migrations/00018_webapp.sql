-- +goose Up
-- Webapp engagements: a target can be a live website rather than an on-chain
-- program. base_url holds the site origin the audit speaks about; repo and
-- commit stay as the optional source-snapshot reference.
alter table targets add column base_url text not null default '';

-- +goose Down
alter table targets drop column if exists base_url;
