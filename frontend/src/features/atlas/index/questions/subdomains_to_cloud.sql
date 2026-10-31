-- title: Subdomains resolving to cloud resources
-- description: Subdomains whose CNAME or A records land on a known cloud resource hostname (resolves_to edges), with the resource and its account.
-- view: table
-- param: target
SELECT s.fqdn, s.cname, s.a, r.provider, r.account_id, r.service, r.rtype, r.name, r.public, e.confidence, e.evidence
FROM edges e
JOIN subdomains s ON s.target = e.target AND e.src_id = 'subdomain:' || s.fqdn
JOIN cloud_resources r ON r.target = e.target AND e.dst_id = 'resource:' || r.rid
WHERE e.target = {{target}} AND e.rel = 'resolves_to'
QUALIFY row_number() OVER (PARTITION BY s.fqdn, r.rid ORDER BY s.last_seen DESC NULLS LAST) = 1
ORDER BY e.confidence DESC, s.fqdn;
