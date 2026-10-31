-- title: Host to body to repo
-- description: Hosts that served a response body whose hash matches a file in an indexed repo, as edges: which repo backs which live host.
-- view: graph
-- param: target
WITH chain AS (
  SELECT sv.src_id AS host, sv.dst_id AS body, sr.dst_id AS repo, sv.confidence AS c1, sr.confidence AS c2, sr.evidence
  FROM edges sv
  JOIN edges sr ON sr.target = sv.target AND sr.rel = 'serves_repo' AND sr.src_id = sv.dst_id
  WHERE sv.target = {{target}} AND sv.rel = 'serves'
)
SELECT DISTINCT host AS src_id, 'host' AS src_kind, 'serves' AS rel, c1 AS confidence, NULL AS evidence, body AS dst_id, 'body' AS dst_kind FROM chain
UNION
SELECT DISTINCT body, 'body', 'serves_repo', c2, evidence, repo, 'repo' FROM chain
ORDER BY rel, src_id, dst_id;
