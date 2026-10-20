-- +goose Up
-- Every re-rating of a finding, kept forever and copied to the finding's
-- next version on a revision. findings.severity is the current rating.
create table finding_severity_changes (
    id uuid primary key,
    finding_id uuid not null references findings(id) on delete cascade,
    from_severity text not null,
    to_severity text not null,
    reason text not null,
    by_role text not null,
    actor_user_id uuid,
    created_at timestamptz not null default now()
);
create index finding_severity_changes_finding_idx on finding_severity_changes(finding_id, created_at);

-- +goose Down
drop table if exists finding_severity_changes;
