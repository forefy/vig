-- +goose Up
-- A finding can be archived: judged not relevant, with a reason, kept
-- visible and never counted. Every status move is kept, like re-ratings,
-- and follows the finding into its next version on a revision.
alter table findings drop constraint findings_status_check;
alter table findings add constraint findings_status_check
    check (status in ('open','fixed','acknowledged','disputed','archived'));
create table finding_status_changes (
    id uuid primary key,
    finding_id uuid not null references findings(id) on delete cascade,
    from_status text not null,
    to_status text not null,
    reason text not null default '',
    by_role text not null,
    actor_user_id uuid,
    created_at timestamptz not null default now()
);
create index finding_status_changes_finding_idx on finding_status_changes(finding_id, created_at);

-- +goose Down
drop table if exists finding_status_changes;
alter table findings drop constraint findings_status_check;
alter table findings add constraint findings_status_check
    check (status in ('open','fixed','acknowledged','disputed'));
