-- title: Node neighborhood
-- description: Every edge within N hops (at most 6) of one node id (e.g. host:api.acme.com, resource:arn:..., repo:owner/name), walked both ways over the edges table without revisiting a node.
-- view: graph
-- param: node
-- param: target
-- param: hops 1 int
WITH RECURSIVE g AS (
  SELECT src_id, src_kind, dst_id, dst_kind, rel, confidence, evidence
  FROM edges WHERE target = {{target}} AND src_id IS NOT NULL AND dst_id IS NOT NULL
), walk AS (
  SELECT {{node}} AS id, 0 AS hops, [{{node}}] AS seen
  UNION ALL
  SELECT CASE WHEN g.src_id = w.id THEN g.dst_id ELSE g.src_id END, w.hops + 1,
         list_append(w.seen, CASE WHEN g.src_id = w.id THEN g.dst_id ELSE g.src_id END)
  FROM walk w JOIN g ON g.src_id = w.id OR g.dst_id = w.id
  WHERE w.hops < least({{hops}}, 6)
    AND NOT list_contains(w.seen, CASE WHEN g.src_id = w.id THEN g.dst_id ELSE g.src_id END)
), reached AS (
  SELECT id, min(hops) AS hops FROM walk GROUP BY id
)
SELECT g.src_id, g.src_kind, g.rel, g.confidence, g.evidence, g.dst_id, g.dst_kind, least(a.hops, b.hops) AS hops
FROM g
JOIN reached a ON a.id = g.src_id
JOIN reached b ON b.id = g.dst_id
WHERE least(a.hops, b.hops) < least({{hops}}, 6)
ORDER BY hops, g.rel, g.src_id, g.dst_id;
