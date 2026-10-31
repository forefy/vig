-- title: Findings by repo
-- description: Semgrep findings per repo and severity, highest severity first.
-- view: table
-- param: target
-- param: severity % str
SELECT repo, severity, rule_id, count(*) AS findings, min(path || ':' || line_start) AS example
FROM findings
WHERE target = {{target}} AND coalesce(severity, '') LIKE {{severity}}
GROUP BY repo, severity, rule_id
ORDER BY CASE lower(severity) WHEN 'critical' THEN 0 WHEN 'error' THEN 1 WHEN 'high' THEN 1 WHEN 'warning' THEN 2 WHEN 'medium' THEN 2 ELSE 3 END, findings DESC;
