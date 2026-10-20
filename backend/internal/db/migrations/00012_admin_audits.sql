-- +goose Up
-- An admin can cancel any audit, refunding what it cost, and hide it from
-- its owner; a delivered report can be revised into a new version.
alter table audits drop constraint audits_state_check;
alter table audits add constraint audits_state_check check (state in ('queued','claimed','cloning','programs_verified','analysis','human_review','delivered','delayed','failed','cancelled'));
alter table audits add column hidden_at timestamptz;
alter table audits add column hidden_reason text;
drop index if exists audits_dedup_idx;
create index audits_dedup_idx on audits(user_id, head_sha, coalesce(base_sha,''), programs) where state not in ('failed','cancelled');
alter table reports drop constraint reports_audit_id_key;
create index reports_audit_idx on reports(audit_id, created_at desc);
alter table reports add column revision int not null default 1;
alter table reports add column supersedes uuid references reports(id);

-- +goose Down
alter table reports drop column if exists supersedes;
alter table reports drop column if exists revision;
drop index if exists reports_audit_idx;
alter table reports add constraint reports_audit_id_key unique (audit_id);
drop index if exists audits_dedup_idx;
create index audits_dedup_idx on audits(user_id, head_sha, coalesce(base_sha,''), programs) where state <> 'failed';
alter table audits drop column if exists hidden_reason;
alter table audits drop column if exists hidden_at;
alter table audits drop constraint audits_state_check;
alter table audits add constraint audits_state_check check (state in ('queued','claimed','cloning','programs_verified','analysis','human_review','delivered','delayed','failed'));
