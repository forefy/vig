-- title: Public cloud resources
-- description: Cloud resources whose public heuristic fired (open SG, public IP, PubliclyAccessible, AuthType NONE, internet-facing, public bucket), with the hostnames they expose.
-- view: table
-- param: target
-- param: limit 500 int
SELECT provider, account_id, region, service, rtype, name, rid, hostnames, tags
FROM cloud_resources
WHERE target = {{target}} AND public
ORDER BY service, rtype, name
LIMIT {{limit}};
