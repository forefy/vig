-- +goose Up
-- A replay is evidence too: a re-execution with state before and after.
alter table surface_evidence drop constraint surface_evidence_kind_check;
alter table surface_evidence add constraint surface_evidence_kind_check check (kind in ('tx','request','review','replay'));

-- +goose Down
alter table surface_evidence drop constraint surface_evidence_kind_check;
alter table surface_evidence add constraint surface_evidence_kind_check check (kind in ('tx','request','review'));
