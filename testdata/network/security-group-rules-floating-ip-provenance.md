# Security-group rule encoding fixture

`security_group_rules_floating_ip.json` is synthetic. It represents the
field shapes reported by a manager-owned IAM security-group read in
`hcm-3`, not a sanitized copy of its raw response or its SDK projection.
The manager reported HTTP success through the public SDK; raw responses
remain private. The fixture proves no current account membership or policy.

All IDs and metadata are synthetic. Narrow IPv4 and IPv6 prefixes use
documentation address ranges. Anywhere prefixes remain `0.0.0.0/0` and
`::/0`. The fixture retains observed `any`, `TCP`, and `tcp` protocol
spellings, integer port bounds, 0-65535 all-port bounds, and non-null
`remoteGroupId` metadata alongside CIDR prefixes. The IPv6 narrow row
combines the observed IPv6 and narrow-CIDR shapes synthetically.

Null protocols, null/absent ports, null/empty prefixes, and an actual
remote-group source have no observed encoding. They cannot establish
restricted exposure. A populated `remoteGroupId` alongside a CIDR does
not narrow that CIDR's grant.
