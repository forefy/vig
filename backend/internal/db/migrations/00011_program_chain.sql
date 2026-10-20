-- +goose Up
-- A program's chain, for adapters that serve several (EVM chains). Empty
-- for Solana mainnet, which is the only chain the Solana adapter reads.
alter table target_programs add column chain text not null default '';

-- +goose Down
alter table target_programs drop column if exists chain;
