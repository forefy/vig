-- title: Code to resource to subdomain
-- description: Cloud resources named in code that a subdomain also resolves to, as edges: the repo literal, the resource and the public hostname in one picture.
-- view: graph
-- param: target
WITH chain AS (
  SELECT m.src_id AS lit, m.dst_id AS res, x.src_id AS sub, m.confidence AS c1, x.confidence AS c2, m.evidence AS e1, x.evidence AS e2
  FROM edges m
  JOIN edges x ON x.target = m.target AND x.rel = 'resolves_to' AND x.dst_id = m.dst_id
  WHERE m.target = {{target}} AND m.rel = 'mentions_resource'
)
SELECT DISTINCT lit AS src_id, 'literal' AS src_kind, 'mentions_resource' AS rel, c1 AS confidence, e1 AS evidence, res AS dst_id, 'resource' AS dst_kind FROM chain
UNION
SELECT DISTINCT sub, 'subdomain', 'resolves_to', c2, e2, res, 'resource' FROM chain
ORDER BY rel, src_id, dst_id;
