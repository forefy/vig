-- title: Hosts mentioned in code
-- description: String literals in repos that name a known host or a subdomain of a known apex, joined back to the literal and its repo.
-- view: table
-- param: target
-- param: host_like % str
SELECT e.dst_id AS host, e.confidence, l.repo, l.path, l.line, l.kind, l.value
FROM edges e
JOIN literals l
  ON l.target = e.target AND e.src_id = 'literal:' || l.repo || ':' || l.path || ':' || l.line
WHERE e.target = {{target}} AND e.rel = 'mentions_host' AND e.dst_id LIKE 'host:' || {{host_like}}
ORDER BY e.confidence DESC, host, l.repo, l.path, l.line;
