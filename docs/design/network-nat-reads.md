# NAT reads and endpoint renewal

Status: Accepted for the NAT detail and inbound rule reads in section A.
Sections B and C stay inventory and held.

Paths are relative to the product endpoints in
[`internal/routes`](../../internal/routes/routes.go) and
[`internal/endpoints`](../../internal/endpoints/endpoints.go). The
`routes.ProductVNet` (`vnetwork`) base already includes `/vnetwork-gateway/`.
It has a regional dashboard variant. `routes.ProductVServer` (`vserver`)
uses the regional IAM vServer gateway. `routes.ProductBilling` (`billing`)
uses the central Billing base, not the regional `routes.ProductPortal`.

## A. Public NAT detail and Inbound Rules

The [NAT port guide][nat-guide] calls these **Inbound Rules**, not SNAT,
DNAT, or port-forward mappings. Both regional indexes expose the same
public bundles; the bare vNetwork host was unreachable (proxy 502).

Routes use `routes.ProductVNet`. Let `N` be
`vnetwork/v1/{vnetregion}/{project}/nats`. Console detail uses the config's
region UUID; rules and samples use the current dashboard's menu UUID.
Both must match the harness's region-level `ZoneID`, not an AZ or code.

- **GET `N/{nat}`**, no query, **console code only**. `detailNat` in
  [shared service][vnet-shared] returns the wrapper. The [NAT page][nat-page]
  checks `data.uuid` and `status` and displays `natName`, `natGatewayIp`,
  `publicIp`, `createdAt`, `zoneUuid`, `vpc.uuid`, `subnet.uuid` and
  `natPackage.name`. It also uses `server` internals excluded below. List
  and DELETE prove neither this GET nor its wrapper, scope or nullability.
- **GET `N/{nat}/sec-groups/rules`**, no query, **console code only**.
  `getListSecGroup` in [entry][vnet-entry] returns `success` and `data`
  rows with `uuid`, `protocol`, `etherType`, `portRangeMin`, `portRangeMax`,
  `createdAt`, `description`, `isSystem`. Empty shape and types are unknown.
- **GET `N/sec-groups/rules/configuration`**, no query, **console code
  only**. `getSecGroupRuleSamples` in [entry][vnet-entry] returns `success`
  and `data` rows with `name`, `ipProtocol`, `fromPort`, `toPort`. The
  dialog handles null and `"-1"`; exact kinds and role grants are unverified.
- **GET `N/vpcs/{vpc}/subnets`**, **console code only**. The legacy picker
  uses `params` (`search` on `zoneUuid`, empty `sort`, page 1, size 1000).
  [Shared service][vnet-shared] reads paging keys `page`, `size`, `total`,
  `totalPage` and `data[].{uuid,name,cidr}`. The no-query
  **GET `N/vpcs/{vpc}`** helper reads `data` with no observed consumer or
  schema. Neither is NAT child inventory; defer both.
- **GET `vnetwork/v1/{project}/tags`**, `resourceUuid={nat}`, **console
  code only**. NAT binds `selectedNat` to [tag component][tag-component],
  which reads `success` and `data[].{uuid,tagKey,tagValue,systemTag}`.
  Same route as endpoint tags; NAT grants and empty form are unknown.

Other discovery stays in the [NAT write evidence][nat-discovery]. No
translation, port-forward, server-volume or per-rule GET is shown. If a
live NAT shows another tab, observe only its sanitized GET traffic.

### Minimal surface and release gate

Propose `network.GetNATInstance` (`network get-nat-instance`) and
`network.ListNATInboundRules` (`network list-nat-inbound-rules`). Require
`NATID`, use selected project and optional region-scope `ZoneID`. No changes
to existing `NATInstance`, flags, query keys or exit codes. Keep detail
and rule reads separate. Catalog and tags are evidence only.

After probes, fix each route's wrapper and detail/row keys, required fields,
nullable types, identity, scope and allowed empty form before implementation.
Preserve null ports and string `"-1"` protocol if verified. Validate input
IDs before discovery and compare returned identities to the pinned scope.
Reject a matching NAT UUID with conflicting project, region or VPC, duplicate
IDs/keys, evidence of further pages and failure envelopes. Pass through
`ERROR`, `DELETING` and unknown statuses; do not copy the UI's status gate.
Use fixtures for these cases, credential canaries and CLI behavior.

Apply one fail-closed response rule. Probes print only allowlisted static
keys, kinds, bounded counts and comparisons, plus `reveal` values restricted
to booleans, small integers and fixed uppercase enums. Never descend under
`server` or into dynamic maps, even if keys match. The SDK returns only
approved typed fields. Reject the entire result if any retained decoded
string contains a credential sent on any attempt of the operation, as in
[`backup.GetPolicy`][backup-policy]. Never capture raw bodies. Errors and
causes keep only safe status and sentinels, not bodies, server codes or
original error text.

### Pinned read-only probes

Default HAN NAT list and delete pin this base with `fixedVNetEndpoint`:
`https://han-1-vnetwork.console.greennode.ai/vnetwork-gateway/`.
[`natRegionalOrigin`][nat-routing] selects a hard-coded HAN origin, not a
region projection. Generic `ProductVNet` resolves instead to the regional
vServer console with `/vserver/vnetwork-gateway/`. The `vnetEndpoints`
resolver projects the dashboard base for ordinary network reads; NAT uses
its separate fixed resolver. No override for this probe. The runner must
resolve `vnetwork-nat` to the fixed HAN base and append `vnetwork/v1` once.
No redirects, host changes or fallback.

Use [the live NAT harness][nat-harness] with an opt-in bounded hold only
once it confirms ACTIVE and renewal off. Use the harness's exact project,
region UUID (never an AZ ID), NAT and VPC IDs for targeting/comparison.
The harness always owns cleanup. The probes create no rule, tag or NAT.

```json
[
  {"label":"nat-detail","product":"vnetwork-nat","version":"vnetwork/v1",
   "parts":["{vnetregion}","{project}","nats","{nat}"],"query":{},
   "allowKeys":["success","data","uuid","status","natName","natGatewayIp",
                "publicIp","createdAt","zoneUuid","vpc","subnet",
                "natPackage","name"],
   "reveal":{"success":"bool","status":"enum"}},
  {"label":"nat-inbound-rules","product":"vnetwork-nat",
   "version":"vnetwork/v1",
   "parts":["{vnetregion}","{project}","nats","{nat}","sec-groups","rules"],
   "query":{},"allowKeys":["success","data","uuid","protocol","etherType",
                          "portRangeMin","portRangeMax","createdAt",
                          "description","isSystem","direction"],
   "reveal":{"success":"bool","isSystem":"bool","protocol":"enum",
             "etherType":"enum","portRangeMin":"int","portRangeMax":"int"}},
  {"label":"nat-rule-catalog","product":"vnetwork-nat","version":"vnetwork/v1",
   "parts":["{vnetregion}","{project}","nats","sec-groups","rules",
            "configuration"],"query":{},
   "allowKeys":["success","data","name","ipProtocol","fromPort","toPort"],
   "reveal":{"success":"bool","fromPort":"int","toPort":"int"}},
  {"label":"nat-tags","product":"vnetwork-nat","version":"vnetwork/v1",
   "parts":["{project}","tags"],"query":{"resourceUuid":"{nat}"},
   "allowKeys":["success","data","uuid","tagKey","tagValue","systemTag"],
   "reveal":{"success":"bool","systemTag":"bool"}}
]
```

Require valid HTTP 200 JSON per route. 401/403/404, HTML, redirects and
`success:false` are failures, not empty results. Record null vs absent.

These are the criteria set before the run, not results; the evidence below
says which were met.

|Probe|Pass criteria|
|-|-|
|`nat-detail`|One object; exact NAT UUID; no project, region or VPC conflict.|
|`nat-inbound-rules`|Complete array; unique IDs. Empty proves only empty form.|
|`nat-rule-catalog`|Sample array and kinds; can probe before any purchase.|
|`nat-tags`|NAT-scoped array or proven empty form; never create tags.|

An empty rule probe leaves populated row types unqualified. If scope is
missing or a value cannot be classified safely, leave the gate open.

### Observed live evidence

The owner-approved run of the NAT live test in `han-1` held one ACTIVE NAT
with renewal off while the reviewed read-only probe sent the four GETs above
with the IAM bearer, using the harness's own NAT, project, and region UUID
in the pinned path. The executed manifest matched the one above,
including `direction` for rules. The probe printed only allowlisted keys,
kinds, and reviewed values; it compared no values. All four returned
HTTP 200 with `success: true`.

- `nat-detail`: a `data` object with string `uuid`, `natName`,
  `natGatewayIp`, `publicIp`, `status` (`ACTIVE`), `zoneUuid`, and
  `createdAt`; a `natPackage` object with string `uuid`, `name`,
  `createdAt` and null `status`; a `vpc` object with string `uuid`, `name`,
  `status` and null `createdAt`; `subnet` null. Seven further `data` keys
  hold strings or nulls and stay unprojected. No other top-level key.
- `nat-inbound-rules`: a `data` array of a few rows, all `isSystem: true`,
  with string `uuid`, `protocol`, `etherType`, `direction`, `description`,
  `createdAt` and integer `portRangeMin` and `portRangeMax`. Eight further
  row keys and two further top-level keys (a number and a string) stay
  unprojected. User rules and null rule ports were not observed.
- `nat-rule-catalog`: a `data` array of more than ten rows with string
  `name` and `ipProtocol`; `fromPort` and `toPort` are integers or null.
- `nat-tags`: one row with string `uuid`, `tagKey`, `tagValue` and
  `systemTag: true`.

The URL pinned the scope; the response did not confirm it, and the probe
did not check identity. The SDK therefore checks identity at run time.
`zoneUuid` is not shown to be the region UUID. These results qualify the
two reads for IAM users in `han-1` only. They do not establish user rules,
other statuses' fields, `hcm-3`, failure envelopes, or paging.

### Accepted surface

`network.GetNATInstance` (`network get-nat-instance --nat-id`) returns
`NAT` with `ID`, `Name`, `Status`, `GatewayIP`, `PublicIP`, `CreatedAt`,
`PackageID`, `PackageName`, `VPCID`, and `VPCName`. Required keys:
`data.uuid` (equal to the requested ID), `data.status`, `data.vpc.uuid`
(path safe), and `data.natPackage.uuid`; the other strings may be absent
or null on non-ACTIVE NATs and are returned empty then. Statuses pass
through unchanged, including `ERROR`, `DELETING`, and unknown values.

`network.ListNATInboundRules` (`network list-nat-inbound-rules --nat-id`)
returns `Items` of `ID`, `Protocol`, `EtherType`, `Direction`,
`PortRangeMin`, `PortRangeMax`, `Description`, `CreatedAt`, and `System`.
Required row keys: `uuid` (unique, path safe), `protocol`, `etherType`,
`direction`, `isSystem`, `portRangeMin`, and `portRangeMax`. Ports are
`*int` for compatibility: JSON null becomes nil, an absent key or a
non-integer is rejected, and strings are never coerced. The result holds
exactly the rows of one response; unknown keys are ignored and never
decoded into output. Missing or null `data`, or an error status, is an
error, never an empty list.

Both reads use the NAT list's pinned regional base, the selected project,
and the region-level ID the list discovers, and require `success: true`.
They support only IAM users in `han-1`; any other region returns
`ErrInvalidConfig` before a request, and the list keeps its current
regions. A failure envelope, missing required key, wrong kind, duplicate
key, identity mismatch, or reflected credential returns a fixed
`InvalidResponse` with no output.

## B. Network read parity inventory

The inventory of other vNetwork and vServer reads moved to
[network read parity inventory](network-parity-inventory.md). It plans no
work.

## C. Private endpoint billing and auto-renew

The [endpoint page][endpoint-page] opens Dashboard `/resource`, with no
endpoint-specific renewal route. The [dashboard][billing-entry] uses
`/gateway/api` from [config][billing-config]. All use `routes.ProductBilling`
with no query and **console code only** evidence for endpoint use:

- **GET `gateway/api/v1/resources`**: `data.data`, summary counts, local
  filters. Row keys: `product`, `artifactType`, `artifactId`, `channel`,
  `renewType`, `renewPeriod`, `billingType`, `isRenewing`, `status`,
  `endBillingTime`. See the [existing billing read][renew-read]; no artifact
  filter is established. `cost` is not a quote.
- **GET `gateway/api/v1/home/user-info`**: [existing lookup][renew-account]
  reads `data.accountId` or `data.userId` fresh for the `portal-user-id`
  header. The dashboard uses a cached account ID; do not copy that behavior.
- **PUT `gateway/api/v1/resources/autoRenew`**: array of the row's four
  identity fields plus `autoRenewInfo: {isEnable, period}`. Enable sends
  `43200 * months`; disable sends `43200`. UI reads `data.successAll` and
  `errorAutoRenewResources`. [vStorage][renew-wire] and [NAT][nat-renewal]
  verify this wire form for their products, not endpoint eligibility,
  accepted periods or successful updates.

Endpoint create uses `resourceType: endpoint` and returns `enp-<uuid>`.
[Live evidence][endpoint-probe] shows PREPAID AUTO-RENEW despite renewal
false on create, not the tuple or ID mapping. Do not borrow vStorage's
product, NAT's artifact type or channel 1. Use only a present numeric
channel from the exact row once the join is proven.

### Read-only probe list

Cheap option: run only `endpoint-billing-resources` to record generic
presence and kinds, without any endpoint or `user-info`. It cannot prove
an endpoint join. For the full check use a disposable endpoint in the
approved HCM offer. Pin the HCM dashboard gateway and exact region UUID
for `endpoint-scope`, not the generic `ProductVNet` base or an AZ.

```json
[
  {"label":"endpoint-scope","product":"vnetwork","version":"vnetwork/v1",
   "parts":["{vnetregion}","{project}","endpoints","{endpoint}"],"query":{},
   "allowKeys":["data","uuid","endpointName","status","billingStatus","vpc",
                "service","project"],"reveal":{}},
  {"label":"endpoint-billing-resources","product":"billing",
   "version":"gateway/api/v1","parts":["resources"],"query":{},
   "allowKeys":["data","product","artifactType","artifactId","channel",
                "billingType","status","renewType","renewPeriod",
                "isRenewing","endBillingTime"],"reveal":{}}
]
```

[`GetEndpoint`][network-client] returns `data` without matching the UUID
and can substitute the region code when discovery fails. One 200 proves
neither ownership nor billing scope. Prove exact ID, project, VPC and
region independently before matching one billing row. Record safely the
`product`, `artifactType`, ID relationship (full ID, suffix or unrelated),
channel type/value/presence, billing state and end-time validity. Record
null vs absent, candidate counts and UI eligibility without clicking a
toggle. Do not expose raw IDs or account data; name/time matches are not
identity proof. Stop on ambiguity or denial.

### Surface and release order

**Owner decision:** hold both endpoint renewal methods. Once the scoped
join is proven, propose `network.GetEndpointAutoRenew` and
`network get-endpoint-auto-renew`: `EndpointID` in, typed `EndpointID`,
`RenewType`, nullable `Enabled`, `PeriodMonths`, and verified end time out.
Missing or ambiguous billing evidence is an error, not renewal off.

`network.PutEndpointAutoRenew` (`network put-endpoint-auto-renew`) also
needs separate approval and live evidence for each direction. A later
enable needs a fresh renewal quote and `MaxPrice`; disable needs no price.
Require explicit bool, writable profile, `--yes`, exact scope, active
PREPAID state, present channel, consistent period and no renewal underway.
Follow the [toggle ADR](../adr/0003-toggle-writes.md): no-op if already
correct; fresh identity, one no-redirect PUT and bounded same-row readback.
An uncertain outcome keeps safe observed state and returns `ErrNotSettled`;
never resend. No bulk write, implicit create fix, order or funding change.

Qualify A, then ship its read pair with fixtures and wiki. B is inventory
only. C stays held until its evidence closes; require adversarial review
and exact-commit checks for any toggle. Existing commands stay unchanged.

[nat-guide]:
  https://docs.greennode.ai/vnetwork/public-nat-instance/add-remove-nat-port
[vnet-entry]:
  https://han-1-vnetwork.console.greennode.ai/main.30ce34ec3e7349fd.js
[vnet-shared]:
  https://han-1-vnetwork.console.greennode.ai/177.61b1e60c1b063add.js
[nat-page]:
  https://han-1-vnetwork.console.greennode.ai/905.22f0aa955f61bd8c.js
[endpoint-page]:
  https://han-1-vnetwork.console.greennode.ai/516.0cd16259d10a9963.js
[cross-connect]:
  https://han-1-vnetwork.console.greennode.ai/398.a4628e6cab97047a.js
[vpn-page]:
  https://han-1-vnetwork.console.greennode.ai/336.154908e105e505d0.js
[peering-page]:
  https://han-1-vnetwork.console.greennode.ai/273.277a04927803c2f0.js
[tag-component]:
  https://han-1-vnetwork.console.greennode.ai/887.277f3216f682dfa6.js
[vserver-console]:
  https://han-1.console.greennode.ai/vserver/91.fcdf8cbb5ccab638.js
[vserver-schema]: https://docs.api.greennode.ai/service-docs/vserver.html
[mcp]: https://github.com/GreenNodeHub/greennode-mcp
[go-sdk]: https://github.com/vngcloud/vngcloud-go-sdk
[billing-entry]:
  https://dashboard.console.greennode.ai/main.9a3eb3adf6ee85e4.js
[nat-discovery]: network-writes-api.md#nat-discovery
[nat-renewal]: network-writes-api.md#nat-billing-and-renewal
[nat-routing]: ../../network/nat_routing.go
[nat-harness]: ../../livetest/live_write_nat_test.go
[network-client]: ../../network/network.go
[backup-policy]: ../../backup/backup.go
[hcm-catalog]: network-endpoints-api.md#recorded-hcm-3-catalog-reads
[renew-read]: auto-renew.md#billing-resource-read
[renew-wire]: auto-renew.md#wire-evidence-and-boundaries
[renew-account]: auto-renew.md#account-identity-and-privacy
[endpoint-probe]:
  network-endpoints-api.md#recorded-hcm-3-create-and-delete-probe
[billing-config]:
  https://dashboard.console.greennode.ai/assets/config/vbilling-portal.json
