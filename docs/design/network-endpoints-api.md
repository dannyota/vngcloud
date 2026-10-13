# Private endpoint API evidence

Endpoint reads exist in the repository. Bounded manager-run IAM-user GETs
verify selected Hanoi catalogs and a VPC response shape. Public console
assets reveal candidate lifecycle and billing requests, but no endpoint
quote or mutation is verified. The [proposed contract](network-endpoints.md)
remains gated.

## Sources and confidence

The first-party [create guide][create] describes choosing a service, the
Standard package, region/zone, VPC, subnet, and optional Private DNS. Its
summary displays pricing. It lists vStorage among supported services. The
[list][list], [rename][rename], and [delete][delete] guides describe console
operations; delete warns that service connections are removed. These are
product descriptions, not server request/response contracts.

[create]: https://docs.greennode.ai/vnetwork/endpoint/create-endpoint.md
[list]: https://docs.greennode.ai/vnetwork/endpoint/view-list-of-endpoints.md
[rename]: https://docs.greennode.ai/vnetwork/endpoint/rename-endpoint.md
[delete]: https://docs.greennode.ai/vnetwork/endpoint/delete-endpoint.md

The unauthenticated [vNetwork console][console] loads
`main.36c7cb7f3452a722.js`. Its [runtime][runtime] maps endpoint module 516
to [the endpoint chunk][chunk], which imports service module 6171 from
[the service chunk][service]. These are first-party frontend artifacts,
obtained without credentials or cookies. Asset hashes identify inspected
content; deployed files can be removed or replaced.

[console]: https://hcm-3-vnetwork.console.greennode.ai/
[runtime]:
  https://hcm-3-vnetwork.console.greennode.ai/runtime.4cdfa1db2c82367b.js
[chunk]: https://hcm-3-vnetwork.console.greennode.ai/516.0cd16259d10a9963.js
[service]: https://hcm-3-vnetwork.console.greennode.ai/177.61b1e60c1b063add.js

The inspected [public API reference](https://docs.api.greennode.ai/) table
of contents has vServer and vStorage references but no endpoint lifecycle
reference. The inspected first-party Terraform provider has no endpoint
resource or data source in [its public source][provider] at commit
`230bd7d346853b2aba33a9ec06f8c544b06fa8a2`. Neither absence proves an API
is absent.

[provider]: https://github.com/vngcloud/terraform-provider-vngcloud

The [private-cluster guide][vks] says VKS creates four service endpoints,
including vStorage, and reuses them across private clusters sharing a VPC.
It warns against deleting them: VKS can recreate them after disruption,
potentially with different addresses. It also describes automatic cleanup
when no cluster reuses them and additional endpoint costs. This establishes
a managed/shared lifecycle hazard, not an API ownership discriminator.

[vks]:
  https://docs.greennode.ai/vks/getting-started/create-a-private-cluster.md

## Existing repository reads

[Network implementation](../../network/network.go),
[models](../../network/models.go), and
[tests](../../network/network_test.go) establish the current SDK behavior.
Routes are relative to the selected vNetwork base, which can include the
`/vnetwork-gateway` prefix. Do not add that prefix twice.

- `ListEndpoints`: `GET /vnetwork/v1/{zoneId}/{projectId}/endpoints`,
  with JSON `params` containing page, size, and optional `vpcId` and `uuid`
  search entries. It decodes `data`, `page`, `size`, `totalPage`, and `total`.
- `GetEndpoint`: the same collection plus `/{endpointId}`, HTTP 200 with
  a `data` object. It discovers its zone rather than accepting `ZoneID`.
- `ListEndpointTags`: `GET /vnetwork/v1/{projectId}/tags` with only the
  `resourceUuid` query parameter. It adds the `portal-user-id` header when
  the resolved project's `UserID` is positive.

Recorded sanitized fixtures are
[list](../../testdata/network/list_endpoints.json),
[detail](../../testdata/network/get_endpoint.json), and
[tags](../../testdata/network/list_endpoint_tags.json). List and detail
fixtures establish UUID, name, URL, status, and VPC fields; detail also
contains subnet ID. The model's other fields are not proven by these
fixtures. In particular, untyped package, price, domain, and nested fields
cannot be treated as verified billing or DNS evidence.

## Frontend request builders

The public service module constructs a base path
`/vnetwork/v1/{regionUuid}/{projectUuid}/endpoints` from the configured
`VNETWORK_GATEWAY` and local console selections. This `regionUuid` naming
differs from the repository's `ZoneID`; verify the mapping instead of
assuming an availability-zone identifier belongs in the path.

The following suffixes are relative to that base:

| Operation | Method and suffix | Frontend body or query |
|-|-|-|
| List | `GET` collection | JSON `params`; optional `zoneUuid` |
| Detail | `GET /{endpointUuid}` | None shown |
| Direct create | `POST` collection | Order's `resourceInfo` |
| Rename | `PUT /{endpointUuid}/name` | `newName` |
| Delete | `DELETE /{endpointUuid}` | Identity fields below |
| Categories | `GET /categories` | Catalog list |
| Services | `GET /{categoryUuid}/services` | Catalog list |
| Packages | `GET /{serviceUuid}/packages` | Catalog list |
| Zones | `GET /zones` | Catalog list |
| Eligible VPCs | `GET /vpcs` | Status and service filters |
| Eligible subnets | `GET /vpcs/{vpcUuid}/subnets` | Eligibility and zone |

List `params` carries `search`, `sort`, `page`, and `size`. VPC search uses
`status=ACTIVE` and `endpointServiceId`. Subnet search uses
`isEndpointUsable=true` and can include `zoneUuid`. The component's VPC DNS
check helper is empty; a VPC detail service method is present but does not
establish how to check DNS support.

The create component builds `resourceInfo` with `endpointName`,
`categoryUuid`, `serviceUuid`, `packageUuid`, `vpcUuid`, `subnetUuid`,
`regionUuid`, `projectUuid`, `enableAZ`, and `enableDnsName`. Its visible
path sets `enableDnsName=false`. This is not evidence that the server
supports private DNS creation, or that this field equals `enablePrivateDns`.
The selected category's services GET succeeds in the
[recorded catalog reads](#recorded-iam-user-catalog-reads). Stable identifiers
and a general category-to-service mapping remain gates.

Delete's frontend body has `endpointUuid`, `vpcUuid`,
`endpointServiceUuid`, `regionUuid`, and `projectUuid`. The service ID's
different name does not establish equivalence with create's `serviceUuid`.
Safe delete requires a verified mapping from the target's detail response.

Billing uses `POST /vnetwork/billing/v1/{regionUuid}/{projectUuid}/price`
and `/create-order` with the console order request. Module 1452's endpoint
request constructor defaults `resourceInfo.isEnableAutoRenew=true`.
The payment component's `createOrder()` adds `resourceInfo.period=1` and
sets `isEnableAutoRenew=false` when the payment method is `POSTPAID`.
Neither renewal off nor that period field is unconditional constructor
behavior, and neither is established for direct create by this payment
path. The component can navigate to `redirectUrl`.
No inspected response establishes amount, currency, unit, term, tax,
payment settlement, or renewal. A direct create method in the service
module does not prove that it bypasses billing or succeeds for SDK IAM auth.
The candidate one-month prepaid purchase is therefore still an open choice.

## Recorded IAM-user catalog reads

Source: manager-recorded bounded read-only discovery using IAM-user login
in `han-1`. Calls stayed on a fixed trusted vNetwork origin. The selected
region mapping was resolved through its regional `vnetworkDashboard`. This
establishes that route and auth combination for the recorded reads only.
No account identifiers, inventory values, or counts are reproduced here.
Authentication and the bounded project, region, zone, category, service,
package, and VPC reads passed the discovery helper's validation. The helper
is private discovery tooling; no public SDK or CLI runtime was added and
no workload environment was entered.

Zones, categories, an explicitly selected category's services, and an
explicitly selected vStorage service's packages GETs succeeded. Their
successful envelopes contain `success:boolean`, `code:number`, and
`data:array`, with no paging fields. Other envelope fields are withheld and
unmodelled. Known row fields are:

- Zone: `uuid`, `name`, `description`, and `zoneType` are strings; `id`
  is null; `isDefault` and `isEnabled` are booleans.
- Category: `uuid` and `name` are strings; `isDefault` is a boolean.
- Service: `uuid`, `name`, and `endpointUrl` are strings; `endpointIp`
  is null.
- Package: `uuid`, `name`, and `serviceName` are strings; `createdAt`,
  `description`, and `isDefault` are null. `price` is an object whose keys,
  values, units, and billing meaning are not interpreted.

Other row fields remain unmodelled. Null observations establish no non-null
type. The selected vStorage service's `endpointUrl` matches
`han02.vstorage.vngcloud.vn`, the native/public HAN02 hostname. This is a
catalog-to-region mapping, not proof of private DNS, routing, TLS, signing,
VKS compatibility, or application connectivity.

A VPC GET using the frontend status and service filters returned
`success=true`, object rows, and integer `page`, `size`, `total`, and
`totalPage`. The response size is positive and at most 100, but can differ
from the requested size of 100. In the captured response, pagination
indicated the first and only page, and `total` matched the decoded rows.
This checks that response's internal consistency, not general completeness.

Known VPC fields `uuid`, `name`, `cidr`, `projectId`, `projectUuid`,
`regionId`, `regionUuid`, and `status` are strings; `createdAt`, `updatedAt`,
and `region` are null. `zones` is an array of zone objects. Other fields
remain unmodelled. Returned rows are `ACTIVE`, but carry no
`endpointServiceId`; the response does not independently establish the
eligibility filter's semantics.

The bounded VPC capture passed validation that allows a positive response
size within the requested bound without requiring equality. It does not
establish general pagination behavior or the eligibility filter's meaning.

A subnet GET for a runtime-selected existing VPC returned HTTP 200 but
failed response validation. Its envelope has `success:boolean`,
`code:number`, and another withheld, unmodelled field; `data` and paging
fields are absent. Fixed comparisons establish `success=true` and an
integer code unequal to 200. The code value and unknown field are withheld.
This success-labelled response shape remains unresolved. It establishes no
subnet rows, usable subnet catalog, subnet schema, or empty inventory.

These partial observations establish no minimum permissions,
service-account authorization, interpreted error envelopes, stable catalog
IDs, public sanitized fixtures, quote schema, or write contract. No
authoritative endpoint ownership discriminator is established by the
inspected VKS documentation, provider, or API reference.

## Bounded evidence work

The owner's request covers bounded catalog GET discovery. Only the manager
performs those live reads with runtime credentials and private captures
under the [live-data rules](../../instructions/live-data.md). That authority
does not cover paid purchases or mutations. Public source inspection and
catalog success establish no additional write authority or API contract.

1. Resolve evidence beyond the recorded Hanoi subset: establish general
   VPC paging, explain the subnet response, and verify general
   project/region/zone mappings, catalog stability, category association,
   and network eligibility. Establish required headers, minimum permissions,
   and error envelopes. Service-account auth needs its own evidence; a
   successful token exchange is insufficient. Preserve other-region gates.
2. Read-only pricing: verify the exact quote body without creating an order.
   Record populated amounts, currency, tax, unit, term, metered charges,
   renewal meaning, and rejection envelopes. Establish which create fields
   affect pricing and whether a quote token binds the later debit.
3. Resolve DNS and lifecycle contracts from authoritative evidence. Identify
   the correct DNS input, conflict scope, VPC DNS prerequisite, ready/failure
   states, detail identity, deletion body, and charge termination condition.
   Do not perform a DNS mutation to discover its meaning in a shared VPC.
   Establish authoritative ownership and consumer fields that distinguish
   user endpoints from VKS-managed or reused endpoints. Unknown provenance
   blocks deletion; name prefixes and apparent idleness are insufficient.
4. If the first three steps close the contract gates, request a named paid
   run with account, region, disposable network resources, price cap, term,
   renewal constraint, cleanup method, and maximum unresolved obligation.
   No NAT-specific standing approval applies to endpoint purchases.
5. In that authorized run, send one create, establish identity, payment,
   readiness and renewal, then one authorized delete and confirm resource
   and future-charge removal. Stop on ambiguity; do not create again or
   follow a checkout URL. Record refunds separately from deletion success.

Sanitize raw populated responses only after evidence exists. Synthetic test
variants must be labelled synthetic and cannot close a schema or live gate.
An unresolved paid or renewal model blocks create even if delete is known.

## Private data-plane gate

The [create guide][create] lists `vStorage (HCM04)` with default hostname
`hcm04.vstorage.vngcloud.vn` and describes overriding the service domain
when Private DNS is enabled. This is published HCM04 hostname support,
not live proof of native private S3 connectivity. The same prose describes
a unique domain per endpoint and one Private DNS endpoint per service per
VPC. The inspected frontend fixes a different DNS flag to false. The
frontend/server DNS schema remains an unresolved source conflict.
The recorded Hanoi service catalog maps to the native/public HAN02 hostname;
it adds no private-connectivity evidence.

Verify the published hostname's live private address, DNS zone and scope,
TLS server name/certificate, Signature V4 region and path style, and
compatibility with the native client's fixed regional hostname. Verify the
endpoint's vStorage region mapping rather than inferring it from a path
identifier or the separate HCM03 example. Establish expected routing and
DNS behavior for VKS separately.

Application-network checks belong to the operator and require no SDK access
to a server or cluster. They must demonstrate intended object reads and
writes over the selected private path. Endpoint `ACTIVE` proves neither
DNS resolution nor that path. If a new hostname is required, a separate
reviewed native object-client endpoint contract is needed; arbitrary URLs,
redirects, and TLS exceptions remain excluded.
