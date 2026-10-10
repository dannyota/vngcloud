# vNetwork NAT and VPN Reads

Status: Accepted (2026-10-11) for the NAT and VPN list releases. Detail,
rule, and write surfaces remain separate designs.

Extend `network.Client` with read-only Public NAT and site-to-site VPN
inventory. Ship NAT list first, then VPN list with inline sites and tunnels.
The populated list schemas are verified. SDK authorization and routing
checks below remain release prerequisites.

This design follows [SDK and CLI](sdk-and-cli.md) and [CLI](cli.md).
It adds no authentication method, dependency, write API, or root-package
service re-export. Existing network operations keep their contracts.

## Scope and evidence

[API evidence](network-services-api.md) records the 2026-10-11 paid
root-console probes, including populated rows, nullability, statuses,
regional origins, package prices, and delete behavior. HCM and HAN use the
same route templates. Earlier HCM empty-list reads establish cookie-free
IAM bearer authorization, but do not prove SDK login-provider behavior.

| Surface | Evidence | Decision |
|---|---|---|
| NAT inventory | Populated list, including ACTIVE and ERROR | Typed list |
| NAT rules | No verified response schema | Defer |
| VPN inventory | Populated list, including ACTIVE | Typed sensitive list |
| VPN sites and tunnels | Embedded in list rows | Typed inline children |
| VPN phase configuration | Embedded configuration arrays | Safe typed fields |
| Detail reads | No verified response schema | Defer |

Exclude creation, deletion, rename, bandwidth changes, route changes,
NAT rule changes, VPN secret retrieval, key rotation, configuration
downloads, and connectivity tests. Delete routes are evidence for a future
write design, not additions to this read surface. A provisioning status
must not be presented as proof of traffic flow or tunnel health.

## Public surface

Add these operations to the existing `network` package and CLI operation
table. Each release includes SDK methods, CLI commands, fixtures, example
calls, and wiki documentation.

| SDK method | CLI command | Item model |
|---|---|---|
| `ListNATInstances` | `network list-nat-instances` | `NATInstance` |
| `ListVPNConnections` | `network list-vpn-connections` | `VPNConnection` |

Each uses `Method(ctx, *Input) (*Output, error)`. Both list inputs accept nil.
`ListNATInstancesInput` has `ZoneID string`, `Page int`, and `Size int`.
`ListVPNConnectionsInput` has only `Page int` and `Size int`. Inputs have
no JSON tags. Both outputs use `core.PagedList[T]` with the matching item
model. No list scans masquerade as Get operations. Inline VPN arrays do
not create `ListVPNSites` or `ListVPNTunnels` methods.

An explicit NAT `ZoneID` passes `core.CheckPathID` before project discovery
or HTTP. An omitted zone requires a unique verified mapping for the selected
region. Selected project IDs also pass path validation. Invalid IDs,
negative Page, or negative Size fail before any discovery or resource call.
Zero Page or Size uses the SDK defaults, page 1 and size 10.

Resource JSON tags retain wire names. Go fields use standard initialisms:
`UUID`, `ID`, `CIDR`, `IP`, `DNS`, and `CPU`. Use `Phase1IKELifetime` and
`Phase2IKELifetime` for the configuration lifetime fields. All verified
status and timestamp fields remain raw strings. Preserve unknown status
values; do not introduce status enums or infer operational health.

### Model allowlists

The wire-key lists below define the exported fields. Unless a type is
stated, an allowed scalar is a Go `string`. Nested objects use the named
public types, arrays use typed slices, and verified string-or-null fields
use `*string`. Keep null distinct from an empty string. Do not export
`any`, `map[string]any`, `json.RawMessage`, or a catch-all field.

A null-only observation does not prove a non-null type. Omit those fields
and arrays with no proven element type until evidence establishes a typed
contract. Unknown fields are discarded. Tests retain synthetic excluded
keys to prove they cannot reach public output.

`NATInstance` exposes:

- `uuid`, `natName`, `status`, `createdAt`, `updatedAt`, `projectUuid`,
  and `zoneUuid`.
- `natGatewayIp`, `publicIp`, `deletedAt`, and `billingStatus` as `*string`.
- `natPackage` as `NATPackage` and `vpc` as `NATVPC`.

`NATPackage` exposes `id`, `uuid`, `name`, `createdAt`, `packageId`,
`default` as `bool`, and `image` as `NATImage`. `NATImage` exposes `id`,
`uuid`, `imageType`, `imageVersion`, `licence`, `flavorZoneIds` as
`[]string`, and `packageLimit` as `NATPackageLimit`. The limit type exposes
`cpu`, `memory`, and `diskSize` as `int`, with no invented units.

`NATVPC` and `VPNVPC` each expose `uuid`, `name`, `cidr`, `status`,
`regionId`, `projectId`, `lastSyncTime`, and `dnsStatus`. Keep separate
public types so future NAT changes do not alter VPN's contract.

NAT omits `portalUserId`, `visible`, `message`, `subnet`, and every
null-only package, image, or VPC field. In particular, never model
`image.licenseKey`. Omit `elasticIps`, whose element type is unverified.
Omit `monthlyPrice`: the API's zero is not a usable package price.

`VPNConnection` exposes:

- `uuid`, `vpnName`, `packageUuid`, `localNetworkCidr`, `createdAt`,
  `status`, `billingStatus`, and `zoneUuid`.
- `localGatewayIp` and `vpnGatewayIp` as `*string`.
- `subnetDetailModel` as `VPNSubnet`, `vpcDetailModel` as `VPNVPC`,
  `projectDetailModel` as `VPNProject`, and `packageModel` as `VPNPackage`.
- `vpnSites` as `[]VPNSite`.

`VPNSubnet` exposes `uuid`, `name`, `status`, `cidr`, `subnetType`,
`updatedAt`, `lastSyncTime`, and `zoneId`. `VPNProject` exposes `id`,
`backendProjectId`, and `vserverProjectId`. It omits `portalUserId`.
`VPNPackage` exposes `uuid`, `name`, `packageId`, `tunnelLimit` as `int`,
and `default` as `bool`. Omit `monthlyPrice` and null-only package fields.

`VPNSite` exposes `remoteGatewayIp`, `uuid`, `status`, `siteName`,
`createdAt`, `phase1Configs` as `[]VPNPhase1Config`, and `tunnels` as
`[]VPNTunnel`. `VPNPhase1Config` exposes `phase1Algorithm`, `phase1Hash`,
`phase1DhGroup`, and `phase1IkeLifeTime`, all strings.

`VPNTunnel` exposes `siteUuid`, `tunnelName`, `remoteNetworkCidr`, `uuid`,
`status`, `createdAt`, and `phase2Configs` as `[]VPNPhase2Config`.
`VPNPhase2Config` exposes `phase2Algorithm`, `phase2Hash`, `phase2DhGroup`,
and `phase2IkeLifeTime`, all strings.

VPN omits `preShareKey` entirely, including from private wire models.
It also omits null-only outer `phase1IkeLifeTime`, `phase2IkeLifeTime`,
`phase2DhGroup`, `phase1Status`, and `phase2Status`. Omit all other
null-only fields and `elasticIps`. The null phase statuses do not justify
inventing a phase-health field. Lifetime strings in configuration arrays
must not be converted to numbers.

## Routing and pagination

Support HCM (`hcm-3`) and HAN (`han-1`) through their verified regional
vNetwork origins in the API evidence. Reuse `core.Client`,
`transport.Request`, and route construction. NAT needs zone and project
path segments; VPN needs only project. VPN must not perform zone discovery.

Use the selected project through the existing project mechanism. Prove the
project namespace against these routes before release. Do not borrow an
account-wide project or silently select another region. For NAT, use an
explicit validated ZoneID or resolve a unique verified region-to-zone
mapping. Return discovery errors; return `ErrInvalidConfig` for no unique
match. Never substitute a region label for a zone UUID.

Keep `Endpoints.VNetwork` overrides authoritative for discovery and
resource requests. No production fallback may replace an override or
receive its test token. Add a narrow helper for these new operations if
the existing endpoint helper cannot meet that contract. Shared-helper
changes require regression tests for existing endpoint reads.

For production, allow only the verified HCM and HAN HTTPS origins. A
region response cannot supply an arbitrary credential destination. Keep
TLS verification and cross-host redirect refusal. Explicit test overrides
retain their current local-test behavior. Do not probe another region or
host after an authorization failure.

Each SDK call requests one page. Encode the `params` object with
`encoding/json`, then encode the query with `url.Values`. NAT sends empty
`search` and `sort`; VPN sends the verified empty `any` search and empty
`sort`. The API evidence contains exact query shapes. Do not expose search
or sort flags in these releases.

Default to page 1 and size 10, the values accepted by both lists. Positive
Page and Size values are sent as supplied; no verified server cap exists.
Do not claim live multi-page verification or silently auto-page. Map `page`,
`size`, `totalPage`, and `total` to `Page`, `PageSize`, `TotalPage`, and
`TotalItem`. Preserve server totals rather than deriving them from one page.

## Envelopes and errors

A successful list requires HTTP 200, boolean `success: true`, positive
integer `page` and `size`, nonnegative integer `totalPage` and `total`,
and an array `data`. Require a nonempty string `uuid` on each row. Wrong
types, HTML, invalid JSON, missing required metadata, or an invalid row ID
return `InvalidResponse`, not an empty inventory.

NAT and VPN have verified empty envelopes without `data`. Accept omission
only when success is explicitly true, both totals are explicitly zero,
and positive page metadata is present. Return an empty `Items` slice with
that metadata. Use presence-aware decoding; zero values cannot distinguish
missing totals from explicit zero. Reject `data: null`, whose empty-list
meaning is unverified. Apply no global missing-data rule to other lists.

NAT uses existing `APIError` and sentinel behavior. Test 401, 403, 404, 429,
and server failures as shared transport behavior, not a documented endpoint
error inventory. A false success envelope is a failure. Preserve context
cancellation and the shared GET retry policy.

### VPN secrets and errors

VPN list responses contain plaintext `vpnSites[].preShareKey`. Every VPN
resource request is Sensitive even though its public model excludes keys.
Never capture, log, model, or return the key, or add a reveal operation.

- Set `transport.Request.Sensitive` on every VPN request. Raw success
  bodies must not reach capture hooks; decode errors withhold their cause.
- Withhold server error messages and codes. `WithholdMessage` alone leaves
  the server's Code intact and cannot enforce this boundary.
- Return a fresh `APIError` with fixed operation text and
  `core.ResolvedCode(status, "")`. Preserve operation, status, retryability,
  and safe shared sentinels. Never retain the original response-bearing
  error in an extra field or unwrap chain.
- Withhold message and code fields in HTTP 200 failure envelopes as well.
  Use fixed `InvalidResponse` for invalid success bodies.
- Decode only the field allowlists. Drop `portalUserId`, all secret fields,
  and unknown fields. Do not use `vngcloud.Secret` as a substitute for
  omitting a credential field.
- Test invented secrets in success, nested, unknown, error, and malformed
  fields. Inspect error fields, causes, formatted errors, capture callbacks,
  debug logs, and all CLI output modes.

VPN documentation states that server messages and codes are withheld.
NAT and unrelated services keep their current error behavior.

## CLI and compatibility

Register both operations through `Read` in the network operation table.
The CLI uses only public SDK methods and derives flags from Input structs.
Both commands support read-only profiles and existing project, region,
output, query, and debug settings. No credential or setup changes are needed.

Generate `CLI-Network.md` from the table. Add `Network-NAT.md` and
`Network-VPN.md`, linked from `Network.md`. Describe HCM and HAN routing,
pagination evidence limits, omitted fields, and the distinction between
provisioning state and working VPN connectivity. New types and operations
are additive; existing callers and endpoint operations retain behavior.

## Fixtures and verification

NAT fixtures may use fully synthetic responses matching the observed wire
schema. VPN fixtures must be fully synthetic, assembled from field names
and types with invented values. Never copy or sanitize a live VPN body into
a fixture. Include synthetic excluded fields and secret canaries to test
the boundary. Do not serialize SDK output to construct a wire fixture.

The ordinary example's VPN raw capture stays suppressed. Live VPN checks
report only safe metadata, counts, and field presence; they never persist
the raw response or key. Existing redacted discovery files establish schema
only and do not become fixture sources. No paid resource is needed to
establish the list model again.

SDK work owns new `network/nat.go`, `network/nat_types.go`,
`network/nat_test.go`, `network/vpn.go`, `network/vpn_types.go`, and
`network/vpn_test.go`. It also owns a narrow routing helper if needed,
`testdata/network/` fixtures, dedicated example calls, live read assertions,
and SDK wiki pages. Keep these models out of shared `network/models.go`.

CLI work owns registration in `internal/cli/svc_network.go`, command tests,
golden files, and `CLI-Network.md`. SDK signatures settle before CLI
integration. The manager owns indexes, release notes, Git, and releases.

Write fixture and request tests first. Check paths, headers, query encoding,
nil inputs, invalid IDs before discovery, pagination, errors, cancellation,
region resolution, and endpoint overrides. Assert every allowed nested
field and nullable field. Cover provisioning, ACTIVE, ERROR, future status
strings, omitted data, null data, malformed envelopes, and omitted secrets.

CLI tests cover flags, JSON input precedence, read-only acceptance, query,
JSON/table/text output, and no secret leakage under `--debug`. Use
`httptest` and synthetic data only. Run `make check` for implementation.
VPN error and capture paths require independent adversarial inspection.

Before release, verify SDK login-provider reads, IAM-denial behavior,
selected-project mapping, and NAT region-to-zone mapping. Verify both
supported regional origins and cookie-free HAN authorization. These are
execution checks, not evidence gaps in the populated row schemas. A safe
empty SDK result can verify routing without proving detail reads.

Multi-page behavior remains unverified until existing inventory permits
it. Do not buy resources for a page test. Record that limit in the wiki and
use deterministic tests for request and metadata handling.

## Release order and owner decisions

1. NAT list, including nested package and VPC metadata, with SDK and CLI.
2. VPN list with inline sites, tunnels, and safe phase configuration, with
   SDK and CLI.
3. Detail or NAT rule reads only after their routes and schemas are proven.
4. Audit endpoint, peering, and Cross Connect parity separately.

The owner approved this list surface, the field allowlists, and the release
order on 2026-10-11. No child-route decision blocks VPN inventory.
Unverified null-only fields stay excluded under the stated rule.

Writes require separate designs for cost bounds, retry safety, cleanup,
and per-run paid-operation approval. NAT creation changes VPC routing;
VPN writes change connectivity and exposure. Observed refunds do not imply
safe retry or guaranteed refunds. Follow
[ADR 0002](../adr/0002-write-api-conventions.md) for future write contracts.
