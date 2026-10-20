-- +goose Up
-- Findings that need insider standing - repo, cluster or telemetry access -
-- never a customer. Distinguishing them is significant to severity.
alter table findings drop constraint findings_exploitable_by_check;
alter table findings add constraint findings_exploitable_by_check
    check (exploitable_by = any (array['anyone','any_user','internal','any_signer','account_owner','admin','upgrade_authority','other_program','internal_network','api_key','none']));

-- +goose Down
alter table findings drop constraint findings_exploitable_by_check;
alter table findings add constraint findings_exploitable_by_check
    check (exploitable_by = any (array['anyone','any_user','any_signer','account_owner','admin','upgrade_authority','other_program','none']));