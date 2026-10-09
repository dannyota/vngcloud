# vLB Writes: API

Status: Accepted (2026-09-28), with [vLB writes](lb-writes.md).

The calls, bodies, server rules, and price evidence behind
[vLB writes](lb-writes.md). A shape marked "live" was seen on the test
account; the rest is inferred until its live check passes.

## Sources

- The vLB API reference on `docs.api.greennode.ai`
  (`service-docs/vlb-api.html`), tags `Load Balancers`,
  `Load Balancer Listeners`, and `Load Balancer Pools`.
- VNG Cloud's Go SDK (`vngcloud/vngcloud-go-sdk`,
  `services/loadbalancer/v2` and `sdk_error/loadbalancer.go`): success
  statuses, response shapes, defaults, and error messages.
- VNG Cloud's Terraform provider (`vngcloud/terraform-provider-vngcloud`,
  `resource/vloadbalancing`): waits, the statuses it waits on, and its
  retries while a load balancer is busy.
- The vServer console's public JavaScript
  (`hcm-3.console.greennode.ai/vserver/`): the price request it sends
  before a create or a resize.
- Read-only calls on the test account in `hcm-3` on 2026-09-28, through a
  throwaway program: packages, quotes, and not-found errors. The account
  has no load balancer. No write was sent.

The reference documents the public gateway. The SDK's reads already use
the IAM vLB gateway (`VLB` endpoint) with the same `v2/{projectId}` paths,
and certificate import works there; that the other writes do is a
[live check](lb-writes-checks.md#live-checks).

## Calls

Paths are under `v2/{projectId}/loadBalancers`. The reference says 200 for
every write; VNG Cloud's SDK accepts only 202 for all but certificate
calls. The SDK accepts both until the live check records one.

| Call | Method and path | Success |
|-|-|-|
| Create load balancer | `POST ` (the collection) | 202 |
| Resize (package change) | `PUT /{lbId}/resize` | 202 |
| Delete load balancer | `DELETE /{lbId}` | 202 |
| Create listener | `POST /{lbId}/listeners` | 202 |
| Update listener | `PUT /{lbId}/listeners/{listenerId}` | 202 |
| Delete listener | `DELETE /{lbId}/listeners/{listenerId}` | 202 |
| Create pool | `POST /{lbId}/pools` | 202 |
| Update pool | `PUT /{lbId}/pools/{poolId}` | 202 |
| Delete pool | `DELETE /{lbId}/pools/{poolId}` | 202 |
| Replace members | `PUT /{lbId}/pools/{poolId}/members` | 202 |
| Create policy | `POST /{lbId}/listeners/{listenerId}/l7policies` | 202 |
| Update policy | `PUT .../l7policies/{policyId}` | 202 |
| Delete policy | `DELETE .../l7policies/{policyId}` | 202 |

The API has no call to rename a load balancer, listener, or pool: no
update body carries a name, and Terraform replaces the resource when a
name changes. It has no separate health monitor call either: a pool's
create and update bodies carry it.

Not in this design: `PUT /{lbId}/bandwidth` (a bandwidth package),
`PUT /{lbId}/rebalancing` (autoscaling nodes), `POST /{lbId}/clone`,
`PUT .../reorderL7Policies`, and tags.

## Bodies

From the reference, with VNG Cloud's SDK defaults in brackets.

- Load balancer create: `name` (5 to 50 of `a-z A-Z 0-9 _ - .`),
  `packageId`, `scheme` (`Internet` or `Internal`), `subnetId`, `type`
  (`Layer 4` or `Layer 7`), and optional `zoneId`, `autoScalable`,
  `tags`, and an inline `listener` and `pool`. VNG Cloud's SDK also sends
  `isPoc` false. The console defaults the scheme to `Internet`.
- Resize: `packageId`.
- Listener create: `listenerName`, `listenerProtocol` (`HTTP`, `HTTPS`,
  `TCP`, `UDP`), `listenerProtocolPort`, `timeoutClient` [50],
  `timeoutMember` [50], `timeoutConnection` [5] (each 1 to 3600 s), and
  optional `defaultPoolId`, `allowedCidrs` [`0.0.0.0/0`] (one string,
  comma-separated), `certificateAuthorities`,
  `defaultCertificateAuthority`, `clientCertificate`, `insertHeaders`,
  `blockedCidrs`, `defaultAction`, `alpnProtocols`, and
  `tlsSecurityPolicy`.
- Listener update: the create body without name, protocol, and port. The
  three timeouts are required, so an update is a full replace.
- Pool create: `poolName`, `poolProtocol` (`HTTP`, `TCP`, `UDP`,
  `PROXY`), `algorithm` [`ROUND_ROBIN`] (or `LEAST_CONNECTIONS`,
  `SOURCE_IP`), `healthMonitor` (required), and optional `stickiness`,
  `tlsEncryption` (Layer 7 only), and `members`. A Layer 7 load balancer
  accepts only `HTTP` pools (a `TCP` pool is refused with 400 `Invalid
  pool's protocol for Application load balancer. Valid values: [HTTP]`),
  so `CreatePool` refuses any other protocol there before sending. The
  accepted set on a Network load balancer is unverified and not guarded.
- Health monitor: `healthCheckProtocol` (`TCP`, `HTTP`, `HTTPS`,
  `PING-UDP`), `healthyThreshold` [3] and `unhealthyThreshold` [3] (2 to
  10), `interval` [30] (5 to 3600 s), `timeout` [5] (2 to 120 s), and,
  for HTTP checks, `healthCheckPath`, `healthCheckMethod` (`GET`, `POST`,
  `PUT`), `successCode`, `httpVersion` (`1.0`, `1.1`), and `domainName`.
  VNG Cloud's SDK drops the HTTP fields for TCP and PING-UDP checks.
- Pool update: `algorithm` and `healthMonitor` (required), `stickiness`,
  `tlsEncryption`. The update monitor has no `healthCheckProtocol`, so a
  check's protocol is fixed at create.
- Replace members: `members`, the whole list, each `ipAddress` (IPv4),
  `port`, `backup` (required), and optional `weight` [1], `name` (5 to 50
  of `a-z A-Z 0-9 _ - .`), and `monitorPort`. No member ID is sent.
- Policy create: `name`, `action` (`REDIRECT_TO_POOL` or
  `REDIRECT_TO_URL`), `redirectPoolId` or `redirectUrl`,
  `redirectHttpCode` (301 or 302), `keepQueryString`, and `rules`, each
  `ruleType` (`PATH` or `HOST_NAME`), `compareType` (`CONTAINS`,
  `ENDS_WITH`, `EQUAL_TO`, `REGEX`, `STARTS_WITH`), and `ruleValue`.
  Policy update: the same without `name`; `action` is required and
  `rules` is the whole list.

## Responses

- Every create returns a flat `{"uuid": "..."}` (VNG Cloud's SDK). Resize
  returns the same with the load balancer's ID. Deletes return no body
  that anyone reads.
- Reads return `data` with a `progressStatus` on load balancers,
  listeners, pools, members, and policies. The `Pool` model lacks it
  today; VNG Cloud's SDK decodes it.
- The listener read has no `blockedCidrs`, `defaultAction`,
  `alpnProtocols`, or `tlsSecurityPolicy`, and names the client
  certificate `clientCertificateAuthentication`, not `clientCertificate`.
- Live: a load balancer read and list send its zone as an object under
  `zone` (`uuid`, `name`, `zoneType`, `isDefault`, `isEnabled`, and
  counts), with no flat `zoneId`. The model's `ZoneID` is the zone's
  `uuid`.
- Live: `GetLoadBalancer`, `GetListener`, `GetPool`, and `ListPools` on a
  missing load balancer return 404 with `Cannot get load balancer with id
  <id>`. The transport already maps it to `NotFound`.

## Statuses and waits

Terraform waits on `progressStatus`:

| Resource | Pending | Settled | Deleted |
|-|-|-|-|
| Load balancer | `CREATING`, `CREATING-BILLING`, `UPDATING`, `DELETING` | `CREATED` | 404 |
| Listener, pool, policy | `CREATING`, `UPDATING`, `DELETING` | `CREATED` | 404 |

It gives creates 10 s before the first read, polls every second after,
and allows 45 minutes for a resize to settle. No source names a failed
status; `ERROR` is assumed until the live check. No source gives times.

## Busy and error messages

VNG Cloud's SDK matches these messages, case-insensitive:

| Meaning | Message pattern |
|-|-|
| Load balancer busy | `load balancer id <id> is not ready`, `... is updating`, `... is creating`, `... is deleting` |
| Listener busy | `listener id <id> is not ready` |
| Pool busy | `pool id <id> is updating` |
| Pool in use | `is used in listener` |
| Duplicate name | `duplicated pool name`, `duplicated listener name` |
| Port taken | `duplicated listener protocol port` |
| Members unchanged | `the members provided are identical to the existing members in the pool` |
| Same package | `is the same as the current package` |
| Bad package | `invalid package id` |
| Quota | `exceeded load_balancer quota. current used` |
| Not found | `cannot get load balancer with id`, `cannot get listener with id`, `cannot get pool with id`, `could not find resource` |

Terraform reads an `errorCode` field (`LoadBalancerNotReady`,
`ListenerNotReady`) on these errors and retries the write every 30 to 90
seconds for up to 10 or 20 minutes. The shared error decoder here reads
`code`, not `errorCode`, so the SDK matches messages. Which status the
busy refusal carries is a live check.

## Price quotes

The console prices a vLB order on the regional billing gateway's
`POST /v1/price`, the call `pricing.GetQuote` already makes:

| Order | `resourceType` | `action` | `resourceInfo` |
|-|-|-|-|
| Create | `load-balancer` | `create` | `packageId`, `zoneId`, `period` (months), `isPoc` false, `isBuyMorePoc` false |
| Resize | `load-balancer` | `resize` | `packageId` (the new one), `loadBalancerId` |

The console then drops `period`, `isPoc`, `isBuyMorePoc`, and
`isEnableAutoRenew` and sends the rest as the create body. Scheme, type,
and name are not priced.

Live quotes for a create, `period` 1, on 2026-09-28, the same in all four
`hcm-3` zones and with no `zoneId`:

| Package | Layer | Connections | VND a month |
|-|-|-|-|
| `ALB_Small`, `NLB_Small` | 7, 4 | 2,000 | 400,000 |
| `ALB_Medium`, `NLB_Medium` | 7, 4 | 4,000 | 800,000 |
| `NLB_Large` | 4 | 10,000 | 1,600,000 |
| `NLB_x2Large` | 4 | 20,000 | 3,360,000 |
| `ALB_Large` | 7 | 10,000 | 4,000,000 |
| `NLB_x3Large` | 4 | 30,000 | 4,704,000 |
| `NLB_x4Large` | 4 | 40,000 | 6,115,200 |
| `ALB_x2Large`, `NLB_x5Large` | 7, 4 | 20,000, 50,000 | 8,000,000 |

- Each quote has one `propertiesPrice` line named `LOAD BALANCER` whose
  `description` is the package name. No line prices a public address.
- `optimumPrice` is `monthlyPrice` times `period`: `period` 3 gave
  1,200,000 for a small package.
- Every package is `ACTIVE/STANDBY`. All 11 are offered in zones 1A, 1B,
  and 1C; the Bangkok zone `HCM03-BKK-01` lists 8.
- Package IDs are zone-specific. `ListPackages` without a zone returns
  zone `HCM03-1A`'s IDs, and `HCM03-1B` and `HCM03-1C` each have their
  own, so the same package name has a different ID in each zone. A
  create must use a package from its own `zoneId`.
- A resize with another zone's package ID fails with 400 `Invalid
  package id (lbp-...)`, after the quote and before any charge. List
  packages with the load balancer's `zone` `uuid`.
- A quote with an unknown or missing `packageId` returns 500 `Internal
  Server Error`, so the SDK checks `PackageID` before quoting.
- A resize quote for a missing load balancer returns 400 `The resource is
  not found.`: the server accepts the shape and checks the ID. A resize
  quote on a real load balancer, and whether it prices the difference for
  the rest of the month, is a live check.

## Server rules

From the reference and the product pages on `docs.greennode.ai`:

- The package is the main factor in the price. The console's order
  carries a `period` in months and an auto-renew flag. Whether the create
  charges a whole month at once, and whether a delete refunds any of it,
  is a live check.
- An `Internet` load balancer gets a public address; an `Internal` one
  answers only inside the VPC. The scheme cannot change after create.
- The package's `lbType` (`L4` or `L7`) must match `type`. `Layer 7`
  offers HTTP and HTTPS listeners and policies; whether `Layer 4` offers
  only TCP and UDP is a live check.
- Listener, pool, member, and policy names are 5 to 50 characters.
  Listener and pool names are unique within a load balancer, and a
  listener's protocol and port are unique too.
- A pool that a listener uses as its default pool cannot be deleted.
- The console's delete sends one `DELETE` for the load balancer. Whether
  the server then deletes its listeners, pools, and policies, or refuses
  while they exist, is a live check.
