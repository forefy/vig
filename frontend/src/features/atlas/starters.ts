// A few statements to start from, as plain SQL over the tables. They run
// over every build the views hold; a picked build narrows the views, so a
// statement never names a target. A join between two tables keeps to one
// build by joining on `target` too, since two builds can index the same
// repository.

export type Starter = { label: string; sql: string }

// A sink by its last name (`db.Query`, `query.Exec`, `eval`) or a known
// full name (`exec.Command`, `os.system`); `query.Limit`, `r.URL.Query()`
// and `executeCommand` are not.
const SINK = "'(?i)(^|[._])(exec|execsync|execfile|execute|eval|spawn|spawnsync|system|popen|query|queryrow|querycontext|queryrowcontext|raw|rawquery|deserialize|unserialize|loads)$|(^|\\.)(exec\\.command(context)?|subprocess\\.(run|call|check_output|popen)|os\\.system|yaml\\.load|pickle\\.load)$'"
// What the last-name rule catches by accident: `r.URL.Query`,
// `redirectURL.Query` and `u.Query` parse a query string, they run nothing.
const NOT_SINK = "'(?i)(url|uri|(^|[._])u)\\.query$'"
const sink = (col: string) => `regexp_matches(${col}, ${SINK}) AND NOT regexp_matches(${col}, ${NOT_SINK})`

export const STARTERS: Starter[] = [
  {
    label: 'functions',
    sql: 'SELECT repo, path, qualname, line_start, is_handler\nFROM functions\nLIMIT 100;',
  },
  {
    label: 'handlers',
    sql: 'SELECT repo, path, qualname, line_start\nFROM functions\nWHERE is_handler\nORDER BY repo, path, line_start\nLIMIT 300;',
  },
  {
    label: 'names calls are given',
    sql: "SELECT arg, count(*) AS n\nFROM (SELECT unnest(args) AS arg FROM calls)\nWHERE arg LIKE '\"%\"'\nGROUP BY arg\nORDER BY n DESC\nLIMIT 300;",
  },
  {
    label: 'callers of a name',
    sql: "SELECT c.repo, c.path, c.line, c.callee_name, f.qualname AS caller, c.call_id\nFROM calls c\nLEFT JOIN functions f ON f.target = c.target AND f.fn_id = c.caller_fn\nWHERE c.callee_name LIKE '%Exec%'\nORDER BY c.repo, c.path, c.line\nLIMIT 200;",
  },
  {
    label: 'where an input goes',
    sql: "WITH RECURSIVE walk AS (\n  SELECT target, 'call:' || call_id AS id, CAST(NULL AS VARCHAR) AS parent, 0 AS step, ['call:' || call_id] AS seen\n  FROM calls WHERE list_contains(args, '\"user_id\"')\n  UNION ALL\n  SELECT w.target, f.dst, w.id, w.step + 1, list_append(w.seen, f.dst)\n  FROM walk w JOIN flows f ON f.target = w.target AND f.src = w.id\n  WHERE w.step < 8 AND NOT list_contains(w.seen, f.dst)\n)\nSELECT DISTINCT parent AS src_id, id AS dst_id, 'flows' AS rel, step\nFROM walk WHERE parent IS NOT NULL\nORDER BY step LIMIT 300;",
  },
  {
    label: 'a handler and what it reaches',
    sql:
      "WITH RECURSIVE start AS (\n  SELECT target, fn_id FROM functions WHERE is_handler AND qualname LIKE '%getConnected%'\n), reach AS (\n  SELECT target, 'fn:' || fn_id AS id, 0 AS hops FROM start\n  UNION ALL\n  SELECT r.target, e.dst_id, r.hops + 1 FROM reach r\n  JOIN edges e ON e.target = r.target AND e.rel = 'calls' AND e.src_id = r.id\n  WHERE r.hops < 2\n), fns AS (SELECT DISTINCT target, id FROM reach)\n" +
      "SELECT e.src_id, e.src_kind, e.dst_id, e.dst_kind, e.rel, e.confidence, e.evidence\nFROM edges e\nJOIN fns a ON a.target = e.target AND a.id = e.src_id\nJOIN fns b ON b.target = e.target AND b.id = e.dst_id\nWHERE e.rel = 'calls'\nUNION ALL\n" +
      `SELECT 'fn:' || c.caller_fn, 'function', 'sink:' || c.callee_name || '@' || c.repo || ':' || c.path || ':' || c.line, 'sink', 'sinks', 1.0, c.callee_name\nFROM calls c JOIN fns ON fns.target = c.target AND fns.id = 'fn:' || c.caller_fn\nWHERE ${sink('c.callee_name')}\nLIMIT 300;`,
  },
  {
    label: 'an input reaching a sink',
    sql:
      `WITH RECURSIVE reads AS (\n  SELECT target, 'call:' || call_id AS id FROM calls\n  WHERE list_contains(args, '"user_id"')\n), sinks AS (\n  SELECT target, 'call:' || call_id AS id FROM calls\n  WHERE ${sink('callee_name')}\n), walk AS (\n  SELECT target, id, 0 AS step, [id] AS seen FROM reads\n  UNION ALL\n  SELECT w.target, f.dst, w.step + 1, list_append(w.seen, f.dst)\n  FROM walk w JOIN flows f ON f.target = w.target AND f.src = w.id\n  WHERE w.step < 10 AND NOT list_contains(w.seen, f.dst) AND w.target || w.id NOT IN (SELECT target || id FROM sinks)\n), hits AS (\n  SELECT w.target, w.seen FROM walk w JOIN sinks s ON s.target = w.target AND s.id = w.id LIMIT 40\n), named AS (\n  SELECT target, 'call:' || call_id AS id,\n    CASE WHEN ${sink('callee_name')} THEN 'sink:' ELSE 'call:' END || callee_name || '@' || repo || ':' || path || ':' || line AS place\n  FROM calls\n)\n` +
      'SELECT DISTINCT coalesce(a.place, seen[i]) AS src_id, coalesce(b.place, seen[i + 1]) AS dst_id, \'flows\' AS rel\nFROM (SELECT target, seen, unnest(range(1, len(seen))) AS i FROM hits) h\nLEFT JOIN named a ON a.target = h.target AND a.id = seen[i]\nLEFT JOIN named b ON b.target = h.target AND b.id = seen[i + 1]\nLIMIT 400;',
  },
  {
    label: 'shortest way to a sink',
    sql:
      "-- One search from every read of the input at once, each node reached\n-- once: every sink's path back to its nearest read is its shortest.\n" +
      `WITH RECURSIVE reads AS (\n  SELECT target, 'call:' || call_id AS id FROM calls\n  WHERE list_contains(args, '"user_id"')\n), sinks AS (\n  SELECT target, 'call:' || call_id AS id FROM calls\n  WHERE ${sink('callee_name')}\n), reach USING KEY (target, id) AS (\n  SELECT target, id, 0 AS step, CAST(NULL AS VARCHAR) AS parent FROM reads\n  UNION\n  SELECT w.target, f.dst, w.step + 1, w.id\n  FROM reach w JOIN flows f ON f.target = w.target AND f.src = w.id\n  WHERE w.step < 6\n    AND NOT EXISTS (SELECT 1 FROM sinks s WHERE s.target = w.target AND s.id = w.id)\n    AND NOT EXISTS (SELECT 1 FROM recurring.reach r WHERE r.target = w.target AND r.id = f.dst)\n), found AS (\n  SELECT r.target, r.id FROM reach r JOIN sinks s ON s.target = r.target AND s.id = r.id\n  ORDER BY r.step, r.id LIMIT 40\n), back AS (\n  SELECT f.target, f.id, r.parent FROM found f JOIN reach r ON r.target = f.target AND r.id = f.id\n  UNION ALL\n  SELECT b.target, r.id, r.parent FROM back b JOIN reach r ON r.target = b.target AND r.id = b.parent\n), named AS (\n  SELECT target, 'call:' || call_id AS id,\n    CASE WHEN ${sink('callee_name')} THEN 'sink:' ELSE 'call:' END || callee_name || '@' || repo || ':' || path || ':' || line AS place\n  FROM calls\n)\n` +
      "SELECT DISTINCT coalesce(a.place, b.parent) AS src_id, coalesce(c.place, b.id) AS dst_id, 'flows' AS rel\nFROM back b\nLEFT JOIN named a ON a.target = b.target AND a.id = b.parent\nLEFT JOIN named c ON c.target = b.target AND c.id = b.id\nWHERE b.parent IS NOT NULL\nLIMIT 400;",
  },
  {
    label: 'calls around a name',
    sql: "SELECT src_id, src_kind, dst_id, dst_kind, rel, confidence, evidence\nFROM edges\nWHERE rel = 'calls'\n  AND (src_id LIKE 'fn:%ServeHTTP%' OR dst_id LIKE 'fn:%ServeHTTP%')\nLIMIT 200;",
  },
  {
    label: 'routes and handlers',
    sql: "SELECT repo, framework, method, pattern, handler, path, line,\n  coalesce(nullif(fn_id, ''), handler_fn) AS fn_id,\n  coalesce(method || ' ', '') || pattern || ' ' || coalesce(handler, '') AS label\nFROM routes\nORDER BY pattern\nLIMIT 300;",
  },
  {
    label: 'requests on the wire',
    sql: 'SELECT session, method, host, path, query, status, resp_content_type, resp_body_bytes, time_ms, entry_id\nFROM har_entries\nORDER BY started_at\nLIMIT 300;',
  },
  {
    label: 'a page and what it triggers',
    sql:
      "SELECT e.src_id, e.src_kind, e.dst_id, e.dst_kind, e.rel, e.confidence, e.evidence,\n  s.method || ' ' || s.path AS src_label, d.method || ' ' || d.path AS dst_label\nFROM edges e\nJOIN har_entries s ON s.target = e.target AND 'entry:' || s.entry_id = e.src_id\nJOIN har_entries d ON d.target = e.target AND 'entry:' || d.entry_id = e.dst_id\nWHERE e.rel = 'triggers'\n  AND regexp_matches(coalesce(d.resp_content_type, ''), 'json|html|xml|text/plain')\n  AND NOT regexp_matches(d.path, '^/_next/')\nLIMIT 400;",
  },
  {
    label: 'a value that travels',
    sql:
      "SELECT e.src_id, e.src_kind, e.dst_id, e.dst_kind, e.rel, e.confidence, e.evidence,\n  s.method || ' ' || s.path AS src_label,\n  coalesce(d.method || ' ' || d.path, CASE WHEN e.rel = 'yields' THEN e.evidence END) AS dst_label\nFROM edges e\nLEFT JOIN har_entries s ON s.target = e.target AND 'entry:' || s.entry_id = e.src_id\nLEFT JOIN har_entries d ON d.target = e.target AND 'entry:' || d.entry_id = e.dst_id\nWHERE e.rel IN ('yields', 'sent_in')\nLIMIT 400;",
  },
  {
    label: 'a value through time',
    sql:
      "-- Where a value goes, in the order it went: the response that first\n-- handed it out, then every request that sent it on or response that\n-- returned it again, each to the next.\n" +
      "WITH legs AS (\n  SELECT ye.target, ye.dst_id AS value, ye.src_id AS entry, 0 AS side, y.started_at AS seen_at, regexp_extract(ye.evidence, '^([^:]+):', 1) AS sat\n  FROM edges ye JOIN har_entries y ON y.target = ye.target AND 'entry:' || y.entry_id = ye.src_id\n  WHERE ye.rel = 'yields'\n  UNION ALL\n  SELECT se.target, se.src_id, se.dst_id, 1, c.started_at, se.evidence\n  FROM edges se JOIN har_entries c ON c.target = se.target AND 'entry:' || c.entry_id = se.dst_id\n  WHERE se.rel = 'sent_in'\n), ordered AS (\n  SELECT *, row_number() OVER (PARTITION BY target, value ORDER BY seen_at, side) AS hop,\n    count(*) OVER (PARTITION BY target, value) AS hops\n  FROM legs\n)\n" +
      "SELECT a.entry AS src_id, b.entry AS dst_id, CASE b.side WHEN 1 THEN 'sent' ELSE 'seen' END AS rel, a.value, b.sat,\n  strftime(CAST(a.seen_at AS TIMESTAMP), '%H:%M:%S') || ' ' || ha.method || ' ' || ha.host || ha.path AS src_label,\n  strftime(CAST(b.seen_at AS TIMESTAMP), '%H:%M:%S') || ' ' || hb.method || ' ' || hb.host || hb.path AS dst_label\nFROM ordered a\nJOIN ordered b ON b.target = a.target AND b.value = a.value AND b.hop = a.hop + 1\nJOIN har_entries ha ON ha.target = a.target AND 'entry:' || ha.entry_id = a.entry\nJOIN har_entries hb ON hb.target = b.target AND 'entry:' || hb.entry_id = b.entry\nWHERE a.hops >= 3 AND a.entry <> b.entry\nORDER BY a.value, a.hop\nLIMIT 300;",
  },
  {
    label: 'a parameter through time',
    sql:
      "-- Every request that sends a parameter, in the order they went, each\n-- to the next, with the value it carried.\n" +
      "WITH legs AS (\n  SELECT e.target, e.src_id AS entry, h.started_at AS seen_at,\n    regexp_extract(h.query, '(?:^|&)id=([^&]*)', 1) AS sent\n  FROM edges e JOIN har_entries h ON h.target = e.target AND 'entry:' || h.entry_id = e.src_id\n  WHERE e.rel = 'sends' AND e.dst_id = 'param:id'\n), ordered AS (\n  SELECT *, row_number() OVER (PARTITION BY target ORDER BY seen_at, entry) AS hop FROM legs\n)\n" +
      "SELECT a.entry AS src_id, b.entry AS dst_id, CASE WHEN a.sent = b.sent THEN 'same' ELSE 'changed' END AS rel, a.sent, b.sent AS next_sent,\n  strftime(CAST(a.seen_at AS TIMESTAMP), '%H:%M:%S') || ' ' || ha.method || ' ' || ha.host || ha.path AS src_label,\n  strftime(CAST(b.seen_at AS TIMESTAMP), '%H:%M:%S') || ' ' || hb.method || ' ' || hb.host || hb.path AS dst_label\nFROM ordered a\nJOIN ordered b ON b.target = a.target AND b.hop = a.hop + 1\nJOIN har_entries ha ON ha.target = a.target AND 'entry:' || ha.entry_id = a.entry\nJOIN har_entries hb ON hb.target = b.target AND 'entry:' || hb.entry_id = b.entry\nORDER BY a.hop\nLIMIT 300;",
  },
  {
    label: 'parameters endpoints share',
    sql:
      "SELECT e.src_id, e.src_kind, e.dst_id, e.dst_kind, e.rel, e.confidence, e.evidence,\n  strftime(CAST(s.started_at AS TIMESTAMP), '%H:%M:%S') || ' ' || s.method || ' ' || s.path AS src_label\nFROM edges e\nJOIN har_entries s ON s.target = e.target AND 'entry:' || s.entry_id = e.src_id\nWHERE e.rel = 'sends'\n  AND e.dst_id IN (SELECT dst_id FROM edges WHERE rel = 'sends' GROUP BY dst_id HAVING count(DISTINCT src_id) > 1)\nLIMIT 400;",
  },
  {
    label: 'parameters on the wire',
    sql: "SELECT host, path, split_part(kv, '=', 1) AS param, count(*) AS n\nFROM (SELECT host, path, unnest(string_split(query, '&')) AS kv FROM har_entries WHERE query <> '')\nGROUP BY ALL\nORDER BY n DESC, host, path\nLIMIT 300;",
  },
  {
    label: 'hosts the code names',
    sql: "SELECT value, kind, count(*) AS places, count(DISTINCT repo) AS repos\nFROM literals\nWHERE kind IN ('url', 'host')\nGROUP BY value, kind\nORDER BY places DESC, value\nLIMIT 300;",
  },
]

// defaultSql is what the box opens on.
export const defaultSql = STARTERS[0]!.sql
