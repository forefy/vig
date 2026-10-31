-- title: Dangling CNAMEs to cloud providers
-- description: Subdomains whose CNAME points at a cloud provider suffix but matches none of our cloud resources (takeover candidates). Resolved A records are shown; an empty list is the strongest signal.
-- view: table
-- param: target
WITH latest AS (
  SELECT * FROM subdomains WHERE target = {{target}} AND cname IS NOT NULL
  QUALIFY row_number() OVER (PARTITION BY fqdn ORDER BY last_seen DESC NULLS LAST) = 1
)
SELECT s.fqdn, s.cname, s.a, s.http_status, s.cdn
FROM latest s
WHERE (s.cname LIKE '%.amazonaws.com' OR s.cname LIKE '%.cloudfront.net' OR s.cname LIKE '%.azurewebsites.net'
       OR s.cname LIKE '%.herokuapp.com' OR s.cname LIKE '%.github.io' OR s.cname LIKE '%.netlify.app'
       OR s.cname LIKE '%.vercel.app' OR s.cname LIKE '%.appspot.com' OR s.cname LIKE '%.run.app'
       OR s.cname LIKE '%.elasticbeanstalk.com' OR s.cname LIKE '%.trafficmanager.net' OR s.cname LIKE '%.azureedge.net')
  AND NOT EXISTS (SELECT 1 FROM edges e WHERE e.target = s.target AND e.rel = 'resolves_to' AND e.src_id = 'subdomain:' || s.fqdn)
ORDER BY (s.a IS NULL OR len(s.a) = 0) DESC, s.fqdn;
