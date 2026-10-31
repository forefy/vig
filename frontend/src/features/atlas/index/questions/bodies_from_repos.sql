-- title: Served bodies matched to repos
-- description: Response bodies seen in HAR captures whose hash (or JS basename + size, low confidence) matches a file in an indexed repo: which host serves code from which repo.
-- view: table
-- param: target
SELECT sv.src_id AS host, sr.dst_id AS repo, sr.confidence, sr.evidence AS file_path, b.sha256, b.bytes, b.content_type
FROM edges sr
JOIN edges sv ON sv.target = sr.target AND sv.rel = 'serves' AND sv.dst_id = sr.src_id
JOIN (SELECT DISTINCT sha256, bytes, content_type FROM bodies WHERE target = {{target}}) b ON 'body:' || b.sha256 = sr.src_id
WHERE sr.target = {{target}} AND sr.rel = 'serves_repo'
ORDER BY sr.confidence DESC, host, repo;
