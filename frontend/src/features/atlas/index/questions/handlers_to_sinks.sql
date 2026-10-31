-- title: Handlers reaching dangerous sinks
-- description: Request handlers (is_handler) whose call graph, over call edges at or above min_confidence (1 = resolved through the same file or an import, 0.6 adds matches by name alone), reaches, within N hops (0 = the handler itself, at most 6), a function that calls a sink such as exec, eval, query or spawn. A recursive walk over the calls edges that never revisits a function.
-- view: table
-- param: target
-- param: max_hops 4 int
-- param: min_confidence 1 raw
-- param: sink_pattern (exec|eval|spawn|system|popen|query|execute|deserialize|pickle|yaml\.load) str
WITH RECURSIVE sinks AS (
  SELECT DISTINCT caller_fn AS fn_id, callee_name
  FROM calls WHERE target = {{target}} AND caller_fn IS NOT NULL AND regexp_matches(callee_name, {{sink_pattern}})
), handlers AS (
  SELECT fn_id, qualname, repo, path FROM functions WHERE target = {{target}} AND is_handler
), call_edges AS (
  SELECT DISTINCT src_id, dst_id FROM edges
  WHERE target = {{target}} AND rel = 'calls' AND src_id IS NOT NULL AND dst_id IS NOT NULL AND confidence >= {{min_confidence}}
), walk AS (
  SELECT 'fn:' || fn_id AS src, 'fn:' || fn_id AS dst, 0 AS hops, ['fn:' || fn_id] AS seen FROM handlers
  UNION ALL
  SELECT w.src, g.dst_id, w.hops + 1, list_append(w.seen, g.dst_id)
  FROM walk w JOIN call_edges g ON g.src_id = w.dst
  WHERE w.hops < least({{max_hops}}, 6) AND NOT list_contains(w.seen, g.dst_id)
)
SELECT h.repo, h.path, h.qualname AS handler, min(w.hops) AS hops, s.callee_name AS sink, s.fn_id AS sink_fn
FROM walk w
JOIN handlers h ON w.src = 'fn:' || h.fn_id
JOIN sinks s ON w.dst = 'fn:' || s.fn_id
GROUP BY h.repo, h.path, h.qualname, s.callee_name, s.fn_id
ORDER BY hops, h.repo, h.path;
