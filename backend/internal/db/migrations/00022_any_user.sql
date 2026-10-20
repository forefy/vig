-- +goose Up
-- Webapp findings: any authenticated user of the site, nothing more.
alter table findings drop constraint findings_exploitable_by_check;
alter table findings add constraint findings_exploitable_by_check
    check (exploitable_by = any (array['anyone','any_user','any_signer','account_owner','admin','upgrade_authority','other_program','none']));

-- +goose Down
alter table findings drop constraint findings_exploitable_by_check;
alter table findings add constraint findings_exploitable_by_check
    check (exploitable_by = any (array['anyone','any_signer','account_owner','admin','upgrade_authority','other_program','none']));
