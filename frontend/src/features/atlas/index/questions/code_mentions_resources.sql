-- title: Cloud resources mentioned in code
-- description: ARN and bucket literals in repos that match a cloud resource id or name, with the resource's public flag.
-- view: table
-- param: target
SELECT r.service, r.rtype, r.name, r.public, e.confidence, l.repo, l.path, l.line, l.value
FROM edges e
JOIN cloud_resources r ON r.target = e.target AND e.dst_id = 'resource:' || r.rid
JOIN literals l ON l.target = e.target AND e.src_id = 'literal:' || l.repo || ':' || l.path || ':' || l.line
WHERE e.target = {{target}} AND e.rel = 'mentions_resource'
ORDER BY r.public DESC, e.confidence DESC, r.service, l.repo;
