-- title: Target overview
-- description: Row counts per table for one target, a quick sanity check after ingest.
-- view: table
-- param: target
SELECT 'repos' AS tbl, count(*) AS rows FROM repos WHERE target = {{target}}
UNION ALL SELECT 'files', count(*) FROM files WHERE target = {{target}}
UNION ALL SELECT 'functions', count(*) FROM functions WHERE target = {{target}}
UNION ALL SELECT 'calls', count(*) FROM calls WHERE target = {{target}}
UNION ALL SELECT 'literals', count(*) FROM literals WHERE target = {{target}}
UNION ALL SELECT 'findings', count(*) FROM findings WHERE target = {{target}}
UNION ALL SELECT 'hosts', count(*) FROM hosts WHERE target = {{target}}
UNION ALL SELECT 'har_entries', count(*) FROM har_entries WHERE target = {{target}}
UNION ALL SELECT 'bodies', count(*) FROM bodies WHERE target = {{target}}
UNION ALL SELECT 'subdomains', count(*) FROM subdomains WHERE target = {{target}}
UNION ALL SELECT 'cloud_resources', count(*) FROM cloud_resources WHERE target = {{target}}
UNION ALL SELECT 'edges', count(*) FROM edges WHERE target = {{target}};
