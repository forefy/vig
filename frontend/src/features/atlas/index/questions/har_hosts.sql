-- title: HAR hosts overview
-- description: Per host seen in HAR captures: request count, methods, status mix, unique bodies, bytes and cookies observed.
-- view: table
-- param: target
SELECT host,
       count(*) AS requests,
       list_distinct(list(method)) AS methods,
       count(*) FILTER (WHERE status >= 400) AS errors,
       count(DISTINCT resp_body_sha256) AS unique_bodies,
       sum(coalesce(resp_body_bytes, 0)) AS bytes,
       list_distinct(flatten(list(cookie_names))) AS cookies,
       min(started_at) AS first_seen, max(started_at) AS last_seen
FROM har_entries
WHERE target = {{target}}
GROUP BY host
ORDER BY requests DESC;
