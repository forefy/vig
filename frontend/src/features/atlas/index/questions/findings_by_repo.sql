-- title: Sink matches by repo
-- description: Pattern matches from the pre-filter (ripgrep rules, semgrep when it ran) per repo and rule, highest severity first. Leads to read, not findings.
-- view: table
-- param: target
-- param: severity % str
SELECT repo, severity, rule_id, count(*) AS matches, min(path || ':' || line_start) AS example
FROM findings
WHERE target = {{target}} AND coalesce(severity, '') LIKE {{severity}}
GROUP BY repo, severity, rule_id
ORDER BY CASE lower(severity) WHEN 'critical' THEN 0 WHEN 'error' THEN 1 WHEN 'high' THEN 1 WHEN 'warning' THEN 2 WHEN 'medium' THEN 2 ELSE 3 END, matches DESC;
