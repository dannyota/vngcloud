# Network read parity inventory

Status: Inventory only. No work is planned; a future slice needs its own
evidence and design.

[Published vServer OpenAPI][vserver-schema] targets a service-account
base absent from this SDK. Its nearest product is `routes.ProductVServer`;
matching paths do not prove IAM support. The [GreenNode MCP client][mcp]
corroborates interconnect paths in `interconnect_handler.py` and fields in
`models/advanced_network.py` under `src/vserver-mcp-server/greennode/`.
The [upstream Go SDK][go-sdk] network interface adds no missing read.

### vServer circuit interconnect, not vNetwork Cross Connect

`ListInterconnects` already calls `GET I` (`name`, `page`, `size`), where
`I` is `/v2/{project}/interconnects`. The five missing `ProductVServer`
reads have **published schema** and **MCP client** evidence:

- **GET `I/{interconnect}`**, no query: `data` with `uuid`, `projectId`,
  `name`, `status`, `description`, `createdAt`, `typeId`, `typeName`,
  `packageId`, `circuitId`, `enableGw2`, `gw01Ip`, `gw02Ip`, `gwVip`,
  `remoteGw01Ip`, `remoteGw02Ip`.
- **GET `I/{interconnect}/connections`**, optional `name`, `page`, `size`
  (schema defaults 1/10 for paging): `listData`, `page`, `pageSize`,
  `totalItem`, `totalPage`. Rows have `uuid`, `projectId`, `interconnectId`,
  `networkId`, `name`, `status`, `description`, `createdAt`, `remoteSubnets`.
- **GET `I/{interconnect}/connections/{connection}`**, no query: `data`
  with the same connection fields. The MCP accepts a CSV `remoteSubnets`
  string or an array; the schema is not proof of deployed nullability.
- **GET `I/packages`** and **GET `I/circuit-types`**, no query: published
  arrays of `uuid`, `name`, `status`; packages also have `description`.
  The [regional vServer chunk][vserver-console] also defines the package
  GET. Qualify array vs envelope before shipping either catalog.

IAM access, nullability, paging and not-found behavior are unverified.

### Regional vNetwork console

GET paths use `routes.ProductVNet` with prefix `vnetwork/v1/` unless noted.
**Console code only**: IAM scope, types, and empty/error shapes are open.
`P` is `{project}`, `R` is `{vnetregion}`. Paged `params` is JSON with
`search`, `sort`, `page`, `size`.

- **`region-interconnects`** (`params`, `name` in `search`) and
  **`region-interconnects/{id}`** (no query): [entry][vnet-entry] and
  [Cross Connect page][cross-connect]. List reads `data` and `total`;
  detail reads `success` and `data`. Fields include `uuid`, `name`,
  `status`, `billingStatus`, `description`, `createdAt`, `deletable`,
  `requester`, `localRegion`, `remoteRegion`, `regionInterconnectPackage`,
  and list `vpcInterconnects`. No project path; account scope and its IAM
  equivalent are unresolved. Do not alias these to vServer circuits.
- **`region-interconnects/packages`** and **`region-interconnects/regions`**
  (no query): same sources. `data` arrays; package `uuid`, `name`,
  `default`, destination region `uuid` and `name`.
- **`vpc-interconnects`** (`params`, optional `regionInterconnectUuid` in
  `search`; console uses 1/1000), same sources: `success`, `data` rows with
  `uuid`, `status`, `createdAt`, `requestVpcDetail`, `acceptVpcDetail`,
  `remoteVpcUuid`; VPC objects have `uuid`, `name`, `cidr`. No detail GET
  proven. Full pagination and parent/client-side joins are unverified.
- **`vpc-interconnects/vpcs`** and
  **`/{regionCode}/vnetwork/v1/vpc-interconnects/vpcs`** (no query), same
  sources: `success` and `data[].{uuid,name,cidr}`. Remote picker prepends a
  region path segment on the same gateway, not a new host or product.
  Remote routing and project visibility need evidence; do not follow URLs
  supplied by a response.
- **`R/P/vpc-peering`** (`params`, `any` or `status=WAITING_FOR_APPROVAL`
  in `search`): [peering chunk][peering-page]. Reads `data`, `total`, and
  row `uuid`, `vpcPeeringName`, `status`, `createdAt`, `description`,
  `requesterVPC`, `receiverVPC`. Alternative to existing vServer
  `GET /v2/{project}/peering`, not a new generic list. Pending approvals
  and whether the two inventories agree are unknown. Neither source
  establishes peering detail GET.
- **`P/vpns/{vpn}`**, no query: [entry][vnet-entry] and [VPN page][vpn-page].
  `success`, `data` with `uuid`, `vpnName`, `status`, `billingStatus`,
  gateway IPs, `localNetworkCidr`, `zoneUuid`, `createdAt`, nested package,
  VPC, subnet and `vpnSites` with tunnels. UI reads plaintext `preShareKey`.
  Never expose that key, raw VPN logs, or console-style generated keys.
- **`P/vpns/{vpn}/tunnels/status`**, no query, same sources: `success` and
  `data`; site and `tunnels` entries use `uuid`, `status`, `ipsecStatus`.
  Status does not prove traffic flow. **`P/vpns/{vpn}/charon-log`** takes
  `line`; console reads `data` as text. Do not expose unbounded raw logs.
- **`P/vpns/packages`** (`params={}`, optional `zoneUuid`), same sources:
  `data` rows with `uuid`, `name`, `default`. **`P/vpns/tunnels/phase1`**
  and **`P/vpns/tunnels/phase2`** (no query): `data` maps algorithm settings
  to `values` with `configValue`, `isDefault`. See
  [VPN catalog evidence](network-writes-api.md); contract is gated there.
- **`P/vpns/zones`**, **`P/vpns/vpcs`**, and
  **`P/vpns/vpcs/{vpc}/subnets`** (`params`), [shared service][vnet-shared]
  and [VPN page][vpn-page]: same paging keys as the [NAT picker][nat-a].
  Zone rows use `uuid`, `zoneType`, `isDefault`, `isEnabled`; VPC and subnet
  rows use `uuid`, `name`, `cidr`; UI filters by `zoneType` or `zoneUuid` in
  `params.search`. These are form pickers, not VPN child inventory.
- **`regions/services`**, no query, [entry][vnet-entry]: `success`,
  `data[].type` for menu discovery. Existing region listing does not cover
  service-menu eligibility; feature flags and lifetime are undefined.
- **`P/tags`**, `resourceUuid`, [tag component][tag-component]: same route
  and row keys as [NAT tags][nat-a]. The Cross Connect UI reuses it; the
  SDK exposes only endpoint tags. Verify authority and ownership per resource.

Endpoint list/detail/tags are public. The private [HCM picker][hcm-catalog]
qualifies `R/P/endpoints/vpcs/{vpc}/subnets`. The endpoint's own subnet
list, VPCs by service and private DNS helpers have no observed consumer
or verified reply; they are not the HCM picker.

### Adjacent vServer VPC gaps

[Published schema][vserver-schema], `routes.ProductVServer`, GET under
`/v2/{project}/` unless stated. No query unless shown; IAM, live shapes,
permissions and paging are unverified.

- `network-acl/{acl}/rules`: `data[]` with `uuid`, `type`, `action`,
  `protocol`, `port`, `source`, `seqNumber`, `interfaceAclPolicyUuid`,
  `createdAt`. Schema only; writes GET the parent ACL and PUT `/rules`.
- `route-table/route/{table}`: `data[]` with `uuid`, `routeTableId`,
  `destinationCidrBlock`, `routingType`, `target`, `status`. Existing
  aggregate and embedded route views are not this per-table read.
- `network-interfaces-elastic/{id}`: `data` with `uuid`, `name`, `status`,
  `ip`, `gatewayIp`, `elasticIpId`, `serverId`, `vpcId`, `zone`, timestamps.
- `public-vips/externalNetworkInterfaces` and
  `virtualIpAddress/internalNetworkInterfaces` (latter `subnetId`,
  `zoneId`): `data[]` with `uuid`, `fixedIp`, `subnetId`, `zone`.
  `virtualIpAddress/{vip}/addressPairs/{pair}`: `data` with `uuid`,
  `networkInterfaceId`, `networkInterfaceIp`, `virtualIpAddressId`,
  `virtualSubnetId`, `cidr`, `createdAt`.
- `secgroups/{group}/secgroupRules/{rule}`: `data[]` with `id`, `protocol`,
  `direction`, `etherType`, `portRangeMin`, `portRangeMax`, `remoteGroupId`,
  `remoteIpPrefix`, `description`, `status`, `createdAt`.
  `secgroups/{group}/secgroupRules/samples`: `data[]` with `id`, `name`,
  `ipProtocol`, `direction`, `fromPort`, `toPort`. **`/v2/protocols`**
  (no project) has `data` without a typed catalog in the published schema.
- `networks/active` (`page`, `size`): paging metadata and `listData` with
  `id`, `displayName`, `cidr`, `status`, `zone`, `dhcpOptionId`,
  `routeTableId`, `dnsStatus`. `elastic-ips` (`name`, `page`, `size`):
  paging metadata and `listData` with `uuid`, `name`, `status`,
  `allocationType`, `usageType`, `elasticIpPackage`, VPC and target fields.
  Verify whether this overlaps the existing WAN IP inventory.

**Owner decision:** keep B as an inventory. Recommend one future slice for
vServer circuit detail (`GetInterconnect` / `network get-interconnect`)
if an existing circuit proves IAM routing. Decide separately whether the
account-scoped Cross Connect inventory has a consumer; no parity sweep or
paid fixtures for B. Prefer one typed operation per verified use case.

[cross-connect]:
  https://han-1-vnetwork.console.greennode.ai/398.a4628e6cab97047a.js
[go-sdk]: https://github.com/vngcloud/vngcloud-go-sdk
[hcm-catalog]: network-endpoints-api.md#recorded-hcm-3-catalog-reads
[mcp]: https://github.com/GreenNodeHub/greennode-mcp
[peering-page]:
  https://han-1-vnetwork.console.greennode.ai/273.277a04927803c2f0.js
[tag-component]:
  https://han-1-vnetwork.console.greennode.ai/887.277f3216f682dfa6.js
[vnet-entry]:
  https://han-1-vnetwork.console.greennode.ai/main.30ce34ec3e7349fd.js
[vnet-shared]:
  https://han-1-vnetwork.console.greennode.ai/177.61b1e60c1b063add.js
[vpn-page]: https://han-1-vnetwork.console.greennode.ai/336.154908e105e505d0.js
[vserver-console]:
  https://han-1.console.greennode.ai/vserver/91.fcdf8cbb5ccab638.js
[vserver-schema]: https://docs.api.greennode.ai/service-docs/vserver.html
[nat-a]: network-nat-reads.md#a-public-nat-detail-and-inbound-rules
