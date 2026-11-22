# Private endpoint API evidence

Endpoint inventory reads ship in the SDK and CLI. Bounded manager-run
IAM-user GETs verify selected Hanoi catalogs and a VPC response shape.
In `hcm-3`, the console verifies the catalog reads, one monthly vStorage
Standard quote, and one create and delete with its billing effect. The
[hcm-3 write design](endpoint-create.md) uses that record; the
[broader lifecycle proposal](network-endpoints.md) remains gated.

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

The public SDK has [general VPC and subnet reads](../../network/vpcs.go)
and [regional discovery](../../network/network.go), but no endpoint-specific
catalog or eligibility picker methods. General network reads do not prove
endpoint service eligibility. The catalog GETs below are manager evidence,
not shipped SDK methods.

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
| Direct create | `POST` collection | Bundle passes `resourceInfo`; see captured body below |
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

The selected category's services GET succeeds in the
[recorded Hanoi catalog reads](#recorded-iam-user-catalog-reads). Stable
identifiers and a general category-to-service mapping remain gates.

Delete's frontend body has `endpointUuid`, `vpcUuid`,
`endpointServiceUuid`, `regionUuid`, and `projectUuid`. The service ID's
different name does not establish equivalence with create's `serviceUuid`.
Safe delete requires a verified mapping from the target's detail response.

Billing code builds `POST /vnetwork/billing/v1/{regionUuid}/{projectUuid}/price`
and `/create-order` with the console order request. Module 1452's endpoint
constructor defaults `resourceInfo.isEnableAutoRenew=true`. The payment
component's `createOrder()` adds `resourceInfo.period=1` and sets
`isEnableAutoRenew=false` when the payment method is `POSTPAID`. This does
not establish that direct create accepts the false flag or sets a one-month
prepaid term. The payment component can navigate to `redirectUrl`; the
observed endpoint flow below did not use the separate order route.

## Recorded hcm-3 console quote and create request

Source: manager-recorded IAM-user session at the [hcm-3 console][console],
page `/endpoint/create`, with writes blocked. The UI showed HCM-1A and
HCM-1B as `COMING SOON`; only HCM-1C was selectable. The pickers showed
only VPCs and subnets in the signed-in account. A disposable VPC and its
subnet in HCM03-1C were selectable. These observations do not establish
picker schemas, API eligibility semantics, or an SDK auth contract.

The quote POST was allowed, on the same origin at
`/vnetwork-gateway/vnetwork/billing/v1/{regionUuid}/{projectUuid}/price`.
The resolved region ID is 24 hex digits; `projectUuid` has `pro-<uuid>`
form. No availability-zone ID appears in the path or body.

```json
{
  "resourceType": "endpoint",
  "action": "create",
  "resourceInfo": {
    "isBuyMorePoc": false,
    "isPoc": false,
    "isEnableAutoRenew": true,
    "endpointName": "<name>",
    "categoryUuid": "<24-hex>",
    "serviceUuid": "<uuid>",
    "packageUuid": "<uuid>",
    "vpcUuid": "net-<uuid>",
    "subnetUuid": "sub-<uuid>",
    "regionUuid": "<24-hex>",
    "projectUuid": "pro-<uuid>",
    "enableAZ": true,
    "enableDnsName": false
  }
}
```

HTTP 200 carried this quote envelope:

```json
{
  "message": "Success",
  "code": 0,
  "success": true,
  "data": {
    "optimumPrice": 712400,
    "discountPercent": 0.0,
    "originalPrice": 712400,
    "discountPrice": 0,
    "propertiesPrice": [
      {
        "optimumPrice": 712400,
        "monthlyPrice": 712400,
        "currentPrice": null,
        "discountPercent": 0.0,
        "name": "Standard",
        "description": "STANDARD"
      }
    ]
  }
}
```

The console summary showed `vstorage.hcm03 / Standard / 712,400 VND`
with total 712,400 VND. This establishes VND from the console and the
monthly quote from `propertiesPrice`, not a debit, prepaid term, tax,
refund, or whether DNS and renewal flags take effect. The observed quote
has no binding token; whether pricing can be locked remains unknown.

Clicking CREATE ENDPOINT attempted a POST on the same origin at
`/vnetwork-gateway/vnetwork/v1/{regionUuid}/{projectUuid}/endpoints`.
The blocked request used the same envelope and `resourceInfo` as quote,
plus top-level `"tagDetails": []`. It included neither `period` nor
`paymentType`, and no separate `/create-order` request appeared.

## Recorded hcm-3 catalog reads

Source: the same `hcm-3` console session, read-only. Every catalog GET
takes a `params` JSON query with `search`, `sort`, `page`, and `size`
(the console sends size 1000) under
`/vnetwork-gateway/vnetwork/v1/{regionUuid}/{projectUuid}/endpoints`.

- **Zones.** `GET /zones` returns `message`, `code` (200), `success`, and
  `data` rows with `uuid`, `name`, `zoneType`, `isEnabled`, and
  `isDefault`. `HCM03-1C` has `isEnabled: true`.
- **Categories.** `GET /categories` returns one row, `VNGEndpoint`, with
  `isDefault: true`.
- **Services.** `GET /{categoryUuid}/services?zoneUuid=<zone>` returns
  seven rows, each with `uuid`, `name`, `endpointUrl`, `endpointType`
  (`Gateway`), and nested `packages`. `vstorage.hcm03` appears once per
  zone, and its `uuid` differs between zones.
- **Packages.** `GET /{serviceUuid}/packages` returns one row named
  `Standard` with `serviceName`, `monthlyPrice`, `currencyUnit`, `price`,
  and `uuid`.
- **VPCs.** `GET /vpcs` filtered by `status`, `endpointServiceId`, and
  `zoneType` returns `success`, `data`, `page`, `size`, `totalPage`, and
  `total`; rows carry `uuid`, `projectUuid`, `regionUuid`, and `status`.
- **Subnets.** `GET /vpcs/{vpcUuid}/subnets` filtered by
  `isEndpointUsable=true` and `zoneUuid` returns `message`, `code` (0),
  `success`, and `data`. A row carries `uuid` (`sub-<uuid>`), `name`,
  `status` (`ACTIVE`), `cidr`, `subnetType` (`USER`), `vpcUuid` (equal to
  the path VPC), a 24-hex `zoneId`, and a `zone` object whose `uuid` is the
  zone name, such as `HCM03-1C`. With no eligible subnet the reply omits
  `data`, which explains the Hanoi response above.

## Recorded hcm-3 create and delete probe

Source: one manager-run console create and delete in `hcm-3` under owner
decision 4 of the [create design](endpoint-create.md#owner-decisions), on
a disposable VPC and HCM03-1C subnet with no consumers. The browser
rewrote the outgoing create body to `isEnableAutoRenew: false`; every
other write except quote, that create, and its delete stayed blocked.

- **Create.** The POST above with renewal off returned HTTP 201 and
  `{"message":"Success","code":0,"success":true,"data":{"uuid":"enp-<uuid>","name":"<name>"}}`.
  The ID has `enp-<uuid>` form. Renewal off was accepted.
- **Debit.** Cash and available balance each fell by the quoted 712,400 VND
  at create.
- **List row.** `GET .../endpoints?params=...` returns `success`, `data`,
  `page`, `size`, `totalPage`, and `total`. An empty list omits `data` and
  reports `total: 0`, `totalPage: 0`. The row carries `uuid`,
  `endpointName`, `endpointServiceId`, `vpcId`, `status`,
  `billingStatus`, `endpointType` (`Gateway`), `zoneUuid` (`HCM03-1C`),
  `enableDnsName` (`false`), `endpointResource` (`null`),
  `endpointDomains` (empty), and objects `category`, `service`, `vpc`,
  `subnet`, `project`, and `packageId` (with `monthlyPrice` and
  `currencyUnit`). No field names an owner, manager, or consumer.
- **States.** `PROVISIONING` with `billingStatus: provisioning`, then
  `ACTIVE` after about 126 seconds while `billingStatus` stayed
  `provisioning`. Delete showed `DELETING`/`deleting`, then the row left
  the list within about 30 seconds.
- **Delete.** `DELETE .../endpoints/{endpointUuid}` with body
  `{"endpointUuid","vpcUuid","endpointServiceUuid","regionUuid","projectUuid"}`
  returned HTTP 200 and `{"message":"Success","code":0,"success":true}`.
  `endpointServiceUuid` equals create's `serviceUuid`; `vpcUuid`,
  `regionUuid`, and `projectUuid` equal create's values.
- **Refund.** After the delete, cash and available balance were back to
  their pre-create values: the full 712,400 VND was returned.

The exact detail GET after delete and a VKS-managed endpoint were not
observed.

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

The Hanoi reads establish no minimum permissions, service-account
authorization, interpreted error envelopes, stable catalog IDs, or public
sanitized fixtures. They do not verify the hcm-3 catalog joins or write
access. No authoritative endpoint ownership discriminator is established
by the inspected VKS documentation, provider, or API reference.

## Bounded evidence work

Only the manager performs live evidence runs with runtime credentials and
private captures under the [live-data rules](../../instructions/live-data.md).
Use the [hcm-3 probe gates](endpoint-create.md#probe-record-and-release-order)
for the vStorage Standard lifecycle. Verify the read-only gates before a
paid run; a captured request does not authorize replay. Record sanitized
schema and outcome evidence, not account values or raw captures.

The broader lifecycle still needs its own evidence: usable catalogs and
eligibility across supported scopes; authorization for each additional
auth mode or region; and DNS field mapping, readiness, and conflict rules
before any DNS mutation. Synthetic variants cannot close a live gate. An
unresolved payment or renewal model blocks create even if delete is known.

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
