# Elastic IP quote fixture provenance

`quote_elastic_ip.json` and `quote_elastic_ip_zone.json` are the raw
response bodies of two `pricing.GetQuote` calls (`POST v1/price`,
`resourceType` `elastic-ip`, `action` `create`) made by the owner's test
account in region `hcm-3`. The first sent `resourceInfo`
`{"period":1,"isPoc":false}`; the second added `"zoneId":"HCM03-1C"`, the
zone code `portal.ListZones` returns for zone `HCM-1C`. Both returned HTTP
200 with identical bodies. The bodies hold no identifiers or account data,
so they are unchanged.

The capture method and what the quote does and does not establish are in
[vServer floating IPs: API](../../docs/design/vserver-floating-ips-api.md#elastic-ip-quote).
