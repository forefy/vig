-- +goose Up
-- A web target carries what the customer authorised and what the intake
-- read: scope is the hosts and path prefixes probing may touch (consent,
-- SPEC §12 and §20), panel the site's intake facts, and the ownership
-- columns the nonce proof, as target_programs carries it for a program.
alter table targets add column scope jsonb not null default '{}'::jsonb;
alter table targets add column panel jsonb;
alter table targets add column ownership_nonce text;
alter table targets add column ownership_nonce_at timestamptz;
alter table targets add column ownership_proven_at timestamptz;
alter table targets add column ownership_method text;

-- +goose Down
alter table targets drop column if exists ownership_method;
alter table targets drop column if exists ownership_proven_at;
alter table targets drop column if exists ownership_nonce_at;
alter table targets drop column if exists ownership_nonce;
alter table targets drop column if exists panel;
alter table targets drop column if exists scope;
