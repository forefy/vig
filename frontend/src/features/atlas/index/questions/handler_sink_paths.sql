-- title: Handler to sink paths
-- description: The shortest call chain from each request handler to each function that calls a sink, as edges: handler, the functions in between, the sink function and the call it makes.
-- view: graph
-- param: target
-- param: max_hops 4 int
-- param: min_confidence 1 raw
-- param: sink_pattern (exec|eval|spawn|system|popen|query|execute|deserialize|pickle|yaml\.load) str
-- param: limit 15 int
WITH RECURSIVE sinks AS (
  SELECT caller_fn AS fn_id, arg_min(callee_name, length(callee_name)) AS callee
  FROM calls WHERE target = {{target}} AND caller_fn IS NOT NULL AND regexp_matches(callee_name, {{sink_pattern}})
  GROUP BY caller_fn
), handlers AS (
  SELECT fn_id FROM functions WHERE target = {{target}} AND is_handler
), call_edges AS (
  SELECT DISTINCT src_id, dst_id FROM edges
  WHERE target = {{target}} AND rel = 'calls' AND src_id IS NOT NULL AND dst_id IS NOT NULL AND confidence >= {{min_confidence}}
), walk AS (
  SELECT 'fn:' || fn_id AS src, 'fn:' || fn_id AS dst, 0 AS hops, ['fn:' || fn_id] AS seen FROM handlers
  UNION ALL
  SELECT w.src, g.dst_id, w.hops + 1, list_append(w.seen, g.dst_id)
  FROM walk w JOIN call_edges g ON g.src_id = w.dst
  WHERE w.hops < least({{max_hops}}, 6) AND NOT list_contains(w.seen, g.dst_id)
), hits AS (
  SELECT w.src, s.fn_id AS sink_fn, s.callee, w.seen, w.hops,
         row_number() OVER (PARTITION BY w.src, s.fn_id ORDER BY w.hops) AS rk
  FROM walk w JOIN sinks s ON w.dst = 'fn:' || s.fn_id
), paths AS (
  SELECT * FROM hits WHERE rk = 1 ORDER BY hops DESC, src, sink_fn LIMIT {{limit}}
), steps AS (
  SELECT seen, unnest(range(1, len(seen))) AS i FROM paths
)
SELECT DISTINCT seen[i] AS src_id, 'function' AS src_kind, 'calls' AS rel, seen[i + 1] AS dst_id, 'function' AS dst_kind
FROM steps
UNION
SELECT DISTINCT 'fn:' || sink_fn, 'function', 'sinks', 'sink:' || sink_fn || '#' || callee, 'sink'
FROM paths
ORDER BY rel, src_id, dst_id;
