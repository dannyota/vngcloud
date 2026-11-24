# Server root disk zone evidence

`QuoteCreateServer` and `CreateServer` do not validate root disk zone
compatibility before pricing. Keep the provider's decision until scope
and compatibility can be verified. The SDK must not reject an existing
valid configuration on catalog evidence alone.

## What the catalog proves

Read-only SDK requests to the official API in `hcm-3` found two enabled
network zones with disjoint scoped volume type catalogs. Each catalog
included that zone's default type. `GetVolumeType.zoneId` matched the
volume type zone ID, not the network zone ID. Two disabled network zones
listed types, but their default reads returned 404. The unfiltered parent
list returned only one disabled network zone. The SDK can fill
`VolumeTypeZoneID` for a scoped child, so that field alone is not wire
evidence.

The [provider's MCP discovery][vendor-catalog] recommends choosing a tier
from the selected zone. Its [create handler][vendor-create] submits the
order without checking root disk zone membership or calling a separate
validator. This is selection guidance, not an API compatibility rule.

The [pricing discovery sample][discovery] found an enabled zone with an
empty scoped catalog. Types with unfiltered parents in a disabled zone
still quoted for it. A list miss, a matching default, and a successful
quote do not establish whether the server create accepts a type. The
catalog probe did not replay an order, identify the historically refused
input, or check Hanoi.

## What would justify a guard

Find a provider rule or a non-ordering validation step that distinguishes
incompatible pairs from valid ones. Record the scope and response or the
exact provider restriction. Do not add an allowlist
or a peer lookup that treats catalog placement as exclusivity. Do not
order servers to obtain that evidence without separate approval.

If there is conclusive evidence, review the guard against
[paid writes](vserver-paid-writes.md) and the
[write conventions](../adr/0002-write-api-conventions.md). Standalone
quotes must not gain cross-product authorization, mutate shared project
or cookie state, or consume the time the caller allowed for pricing.
Keep input shape checks, the price cap, credential hygiene, and send-once
ordering intact. Treat unsupported response shapes separately from
malformed or credential-reflecting replies.

[discovery]: pricing-discovery-api.md#volume-catalog-reconciliation-and-prices
[vendor-catalog]: https://github.com/GreenNodeHub/greennode-mcp/blob/ecddafc32a54210e5711418c4bbaf2ccb00e37ad/src/vserver-mcp-server/greennode/vserver_mcp_server/volumetype_handler.py#L62-L74
[vendor-create]: https://github.com/GreenNodeHub/greennode-mcp/blob/ecddafc32a54210e5711418c4bbaf2ccb00e37ad/src/vserver-mcp-server/greennode/vserver_mcp_server/server_handler.py#L384-L433
