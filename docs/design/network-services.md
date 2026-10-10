# vNetwork NAT and VPN Reads

Status: Draft (2026-10-10), awaiting owner review. Resolve the discovery
gates for each release before implementation starts.

Extend `network.Client` with read-only Public NAT and site-to-site VPN
inventory. Ship NAT inventory first, then VPN inventory. Add child reads in
separate releases when their response contracts are verified. Each release
includes SDK methods, CLI commands, fixtures, and wiki documentation.

The owner uses the CLI and SDK to inspect services available from the main
GreenNode dashboard. Existing VPC, subnet, route, security group, network
ACL, DHCP option, private virtual IP, peering, interconnect, endpoint, and
WAN address coverage stays in `network`. A dashboard menu does not prove
that every operation behind that menu is already covered.

This design follows [SDK and CLI](sdk-and-cli.md), [CLI](cli.md), and the
existing network designs. It introduces no new authentication method,
dependency, write API, or root-package service re-export.

## Scope and evidence

The published [NAT guide][nat-create] describes a paid Public NAT instance
with a VPC and subnet. The [NAT rule guide][nat-rules] identifies inbound
rules and detail fields for the gateway and public IP. These pages establish
product concepts, not HTTP routes or JSON schemas.

The published [VPN creation guide][vpn-create] identifies a VPN connection,
sites for phase 1, and tunnels for phase 2. One site can contain multiple
tunnels. It allows a caller-supplied or service-generated pre-shared key.
Creation proceeds through checkout. The [VPN overview][vpn-overview]
describes an IPsec site-to-site service; it does not define separate public
gateway or policy API resources.

The [public API index][api-index] has no vNetwork entry. The linked
[vServer reference][vserver-api] has no NAT or VPN section. No NAT or VPN
HTTP route, response schema, pagination cap, or service-specific error code
has been verified from these public references. The public API index
describes service-account authentication; that does not establish the
authentication contract of the console's vNetwork gateway.

Authenticated console reads establish the NAT and VPN list routes and
their successful empty envelopes. [API evidence](network-services-api.md)
records the verified shapes and remaining gaps. Neither list contained a
resource, so neither response establishes a resource model.

| Surface | Current evidence | Design decision |
|---|---|---|
| NAT inventory | List route and empty envelope | Verify records first |
| NAT rules | Product detail section | Separate verified child read |
| VPN inventory | List route and empty envelope | Verify records first |
| VPN sites and tunnels | Product concepts | Verify child routes first |
| VPN policy metadata | Product creation fields | Model only verified data |
| Endpoint and peering | Existing SDK reads | Audit parity separately |
| Cross Connect | Existing interconnect reads | Audit parity separately |

Exclude resource creation, deletion, rename, bandwidth changes, route
changes, NAT rule changes, VPN secret retrieval, key rotation, configuration
downloads, and connectivity tests. Existing resources may be read without
creating a paid NAT or VPN to obtain sample data.

## Approach

Add methods to the existing `network` package and operation table. This
keeps one client for related resources and reuses shared configuration,
project discovery, transport, and CLI output. A separate `vnetwork` package
would split existing endpoint and region methods from their peers. A generic
raw-JSON command would expose unreviewed fields and make later typed models
harder to introduce.

The following names express the proposed SDK surface. They are not claims
that corresponding HTTP routes exist. Include a method only after its
operation contract is recorded under [Discovery](#discovery).

| SDK method | CLI command | Model |
|---|---|---|
| `ListNATInstances` | `network list-nat-instances` | `NATInstance` |
| `GetNATInstance` | `network get-nat-instance` | `NATInstanceDetail` |
| `ListNATRules` | `network list-nat-rules` | `NATRule` |
| `ListVPNConnections` | `network list-vpn-connections` | `VPNConnection` |
| `GetVPNConnection` | `network get-vpn-connection` | `VPNConnectionDetail` |
| `ListVPNSites` | `network list-vpn-sites` | `VPNSite` |
| `ListVPNTunnels` | `network list-vpn-tunnels` | `VPNTunnel` |

Do not add a `Get` method that scans a list, or split an embedded array into
a pretend child API. If detail includes rules, sites, or tunnels and no
separate read exists, expose the verified typed children on the detail
model. A list-only release is valid when its request, envelope, and typed
record schema are proven. An empty response alone does not meet that gate.

### SDK contract

Every method uses `Method(ctx, *Input) (*Output, error)`. A list input may be
nil when it has no required parent. List outputs use `core.PagedList[T]`
only when the API supplies page metadata, otherwise `core.List[T]`. Detail
outputs have a named resource field, `NATInstance` or `VPNConnection`.

Inputs have no JSON tags. Required parent or resource IDs carry
`vngcloud:"required"` and pass `core.CheckPathID` before any request. Use
`NATInstanceID`, `VPNConnectionID`, and `VPNSiteID` for proven relationships.
Add `ZoneID`, `Page`, `Size`, and filters only when the operation accepts
them. Keep API names in resource JSON tags and Go names in CLI output.

Models are typed structs built from sanitized raw responses or an
independently verified published typed schema. Frontend request builders
prove request construction; field references alone do not prove response
field types or nullability. Preserve verified nullable, numeric, timestamp,
enum, nested-array, and identifier types. List and detail models may differ.
Candidate metadata includes name, ID, status, VPC, subnet, gateway addresses,
package, and timestamps; none of
these has a verified NAT or VPN JSON spelling yet. No exported `map[string]any`,
raw JSON, secret field, or catch-all attributes field is permitted.

Preserve unknown status strings for forward compatibility. Do not infer
health from provisioning status, invent defaults for omitted fields, or
convert a denied or unsupported API into a successful empty inventory.

### Routing and pagination

The existing vNetwork gateway starts at
`https://{region}.console.greennode.ai/vserver/vnetwork-gateway/`.
`ListVNetworkRegions` also tries the regional console gateway at
`https://{region}-vnetwork.console.greennode.ai/vnetwork-gateway/`.
The existing endpoint methods use `vnetwork/v1` with a discovered zone UUID
and project ID. The verified NAT list also uses a zone and project in its
path. The verified VPN list uses the project alone. Do not add a zone path
segment or require zone discovery for VPN list calls. See the exact
[request templates](network-services-api.md#verified-requests).

Reuse `core.Client`, `transport.Request`, and the route builder. Prove the
remaining region-selection, project-namespace, authentication, and header
requirements for each operation. Both observed list requests also succeeded
with the captured IAM bearer token in a fresh context without cookies or
extra headers. That proves those list requests accept the IAM bearer;
verification with the SDK login provider remains open. Do not substitute
a region label for a required zone UUID when discovery fails. Return the
discovery error, or `ErrInvalidConfig` when no unique region match exists.
Do not borrow an account-wide project or silently pick another region.

Keep `Endpoints.VNetwork` overrides authoritative. Discovery must not
replace an explicit override or send a test token to a production fallback.
Use a narrowly scoped helper for new operations if the current endpoint
helper cannot preserve those rules. Any shared-helper change must have
regression tests for existing endpoint operations and a reviewed scope.

Accept only HTTPS production destinations from verified routing metadata.
An untrusted region response must not redirect credentials to another host.
Keep TLS verification and the transport's cross-host redirect refusal.
Record allowed gateway hosts from evidence before adding host validation.
Explicit test endpoint overrides retain their current local-test behavior.

Use one request page per SDK list call. If the API uses a JSON `params`
query, encode its object with `encoding/json` and the query with
`url.Values`; never concatenate user input. Record accepted page and size
values, sorting, filters, and envelope field mappings before coding. Do not
claim an unverified size cap.
The console sends page 1 and size 10 for both lists. Use those proven values
as the proposed SDK defaults; do not assume the shared default size of
10000 is supported. A maximum size and multi-page behavior remain
unverified. Map `page`, `size`, `totalPage`, and `total` to `Page`,
`PageSize`, `TotalPage`, and `TotalItem`. Return the server's totals without
inventing a total from the length of one page.

### Errors and secrets

NAT reads use existing `APIError` and sentinel behavior. Tests cover 401,
403, 404, 429, and server failures as transport behavior; those statuses
are not asserted to be the service's documented error inventory. Record
actual endpoint errors separately. Preserve context cancellation and the
shared read retry policy. Reject malformed success envelopes, including
HTML login pages, instead of returning no rows. NAT and VPN omit `data`
for the verified empty result. Accept that omission only when `success` is
explicitly true, `total` and `totalPage` are explicitly zero, and `page` and
`size` are present positive integers. Return an empty `Items` slice with
the supplied page metadata. Missing `data` with nonzero or missing totals,
missing success, wrong types, or invalid page metadata is malformed. Do
not treat `data: null` as equivalent to omission without evidence. Test
these distinctions with presence-aware decoding; Go zero values alone
cannot distinguish an omitted total from an explicit zero.

VPN reads are sensitive even when the public model omits secret fields.
Successful responses may contain pre-shared keys. Certificates, private
keys, passwords, authentication material, and downloaded configurations
must not reach capture hooks, SDK output, CLI output, logs, or errors.

- Set `transport.Request.Sensitive` on every VPN resource request so the
  raw response never reaches the configured capture hook and a decoding
  error withholds its cause.
- Withhold server error messages. The current `WithholdMessage` option
  leaves the server's `Code` intact; therefore it is insufficient alone.
- Before returning a VPN `APIError`, replace server-derived `Message` and
  `Code` with fixed operation text and `core.ResolvedCode(status, "")`.
  Preserve operation, status, retryability, and safe shared sentinels. Do
  not retain the original error through `Unwrap` or an extra field.
- Apply the same rule to failures inside HTTP 200 envelopes. Never expose
  an unchecked envelope message or code.
- Decode only approved metadata into exported models. Omit secret fields
  entirely, including `vngcloud.Secret` fields and reveal operations.
- Test fabricated secrets in success, error, nested, unknown, and malformed
  fields. Inspect formatted errors, error fields, capture callbacks, logs,
  and all CLI output modes.

Secret-safe errors trade service-provided diagnostics for predictable
metadata reads. VPN documentation must state that server message bodies and
codes are withheld. NAT and unrelated services keep their current behavior.

### CLI

Register each operation through `Read` in the network operation table. The
CLI imports only public SDK packages and derives flags from Input structs.
The new commands work with read-only profiles and the existing project,
region, output, query, and debug settings. No new credential file
or setup flow is needed.

Generate `CLI-Network.md` from the operation table. Add SDK pages
`Network-NAT.md` and `Network-VPN.md`, linked from `Network.md`. Describe
verified region support and pagination limits, and make the distinction
between inventory and operational tunnel health explicit.

## Discovery

The manager captures only requests made by list and detail views on the
authorized console. Return sanitized schemas and counts, never live values.
Public frontend source can establish request construction. A published
typed response declaration can establish modeled field types when its
operation mapping is verified; incidental field reads cannot. Neither
source proves successful IAM authorization. Keep evidence for request
construction, response schemas, and authorization separate.

For each proposed operation, record:

1. HTTP method, gateway origin, path template, API version, project and
   zone placement, query encoding, and accepted success status.
2. Auth mode and required header names. Never record tokens or cookies.
3. Sanitized raw success body with a nonempty record, or a published typed
   schema that independently establishes every modeled field. An empty
   list proves its envelope only.
4. Pagination and filter evidence. Exercise two small pages when existing
   records allow it; record when they do not.
5. Safe error outcomes and envelope shapes. A synthetic missing ID may be
   used only after the exact read route is established. Do not induce load
   to obtain a 429 or deliberately invalidate account credentials.
6. For VPN, the relationship between connection, site, and tunnel, whether
   children are embedded, and a list of excluded secret field paths.
7. Region and project behavior with an explicit zone and discovery, plus
   endpoint override behavior in deterministic tests.

NAT list, VPN list, and the regions request have successful console-read
evidence in [API evidence](network-services-api.md). Further probes close
only the listed gaps. Detail probes use an existing resource ID from a
list. Do not create a NAT, VPN, site, tunnel, or bandwidth package to fill a
fixture gap. A missing nonempty schema keeps that method unimplemented.

Keep account captures under the ignored example output paths required by
[live-data rules](../../instructions/live-data.md). VPN discovery must
sanitize secrets before persisting response bodies. A VPN fixture preserves
the raw envelope and metadata structure with sensitive values replaced;
it is not a serialization of the SDK model. Retain no original secret-bearing
capture. The ordinary example's raw capture remains suppressed for VPN.

## Verification and ownership

SDK work owns new `network/nat.go`, `network/nat_types.go`,
`network/nat_test.go`, `network/vpn.go`, `network/vpn_types.go`, and
`network/vpn_test.go`, plus a small shared routing helper if needed. SDK
work also owns fixtures under `testdata/network/`, example calls in
`examples/basic/network.go` or split network example files, live read
assertions, and the SDK wiki pages. Keep related models out of the already
large shared `network/models.go`.

CLI work owns `internal/cli/svc_network.go`, new NAT and VPN command tests,
any required generated-doc notes, CLI golden files, and `CLI-Network.md`.
SDK signatures must settle before CLI integration starts. The manager owns
documentation index changes, release notes, Git, and release checks.

The SDK implementer writes fixture and request tests first. Verify exact
path, headers, query encoding, nil inputs, required IDs, cancellation,
pagination, safe errors, region resolution, and endpoint override isolation.
Reject path traversal IDs before project discovery or HTTP calls. Exercise
empty lists, nulls permitted by the real contract, malformed bodies, and
nonempty fixtures with every approved field asserted.

CLI tests verify flags, JSON input precedence, read-only acceptance, query
results, JSON/table/text output, and no secret leakage under `--debug`.
Tests use `httptest` and synthetic data. Both implementers run `make check`.
An independent review covers the final diff; VPN error and capture paths
receive adversarial review because they handle credential-bearing responses.

Live verification is read-only. Compare NAT raw and decoded output locally.
For VPN, compare an explicitly sanitized raw shape with decoded metadata
through the approved discovery path; do not turn raw capture back on.
Report resource counts and checks performed, with no account values. Empty
inventory is a successful list check, not proof of detail or child models.
Run `make check` before commits and require green CI on each tagged commit.

## Release order and later writes

1. NAT list and any independently verified detail read, with SDK and CLI.
2. VPN list and any independently verified safe detail read, with SDK and
   CLI. This release does not wait for NAT child reads.
3. NAT rule reads when a separate endpoint or embedded schema is proven.
4. VPN site and tunnel reads when their routes and secret handling are
   proven. Split sites and tunnels if their evidence arrives separately.
5. Audit new-console endpoint, peering, and Cross Connect parity against
   existing methods, then design each actual gap separately.

Each item is an independently releasable feature. Do not choose tag numbers
until the manager integrates the release. A blocked read contract does not
block another verified read feature.

Paid NAT or VPN creation, bandwidth changes, and orders need separate
designs for quotes, cost bounds, retry safety, cleanup, and per-run owner
approval. NAT rules and VPN site or tunnel changes also need a separate
write design and adversarial review, even if a later price check finds them
free. Those operations can redirect traffic or change exposure. Use
[ADR 0002](../adr/0002-write-api-conventions.md) for their public contracts.

[nat-create]: https://docs.vngcloud.vn/vng-cloud-document/vnetwork/public-nat-instance/create-nat
[nat-rules]: https://docs.greennode.ai/vnetwork/public-nat-instance/add-remove-nat-port
[vpn-create]: https://docs.greennode.ai/vnetwork/vpn-virtual-private-network-site-to-site/create-vpn-site-to-site
[vpn-overview]: https://docs.greennode.ai/vnetwork/vpn-virtual-private-network-site-to-site
[api-index]: https://docs.api.greennode.ai/
[vserver-api]: https://docs.api.greennode.ai/service-docs/vserver.html
