# Global Load Balancer create

Status: Accepted with the owner decisions below. Implement now; the manager
runs the live create and delete on disposable resources before landing.

## Owner decisions

1. Build `CreateLoadBalancer` and `DeleteLoadBalancer` now. The owner
   approves live create and delete of disposable test resources.
2. Both writes accept HTTP 200 or 202, since the OpenAPI and the upstream
   SDK disagree. The live run records which one the API returns.
3. `paymentFlow` is always `automated`, charging existing credits; never
   `gateway`. No top-up or payment-method operation.
4. GLB has no quote API. As an exception to ADR 0002 rule 8, create needs
   `--yes` without a quote. Help states the prepaid three-day credit hold,
   later usage charges, and that delete does not promise a refund. Add a
   quote command if the live run finds a price endpoint.
5. Bare create takes `Name` and `PackageID` and creates a Layer 4 GLB.
   The [TCP child design][children] governs listener and pool writes on an
   existing GLB, including region and package eligibility per account.
6. Delete reads the exact ID first and sends only for a Layer 4 load
   balancer in a stable state. The create response's ID is attributable to
   the caller and may be cleaned up.
7. Create polls only documented transitional states; `ERROR` is a terminal
   failure; any other state is not settled. Errors use status-derived codes
   and a sanitized provider message.
8. A caller-supplied HTTP client or transport is the caller's own trusted
   code and is outside the endpoint guard; the wiki says so.
9. The sections below describe the stricter long-term design; where they
   conflict with these decisions, these decisions win.

## Scope and alternatives

The bundled proposal covers one Network GLB (Layer 4) with one TCP listener,
one TCP pool, one TCP health check, and one existing private vServer backend
in one verified region.
Require an explicit restricted listener CIDR. Use prepaid GreenNode credits
only. A usable create returns a confirmed GLB ID, not just an accepted order.
Ship quote, create, and delete together so users can price and remove what
create provisions. Reuse the existing catalog, list, detail, and child reads.

- **Recommend one bundled create.** The console creates a listener and pool at
  the same time, and the documented collection POST can carry both. Prove the
  bundled result and its cleanup before supporting either write.
- **Bare GLB, then child writes.** The top-level schema does not require the
  children. That does not prove an empty GLB is accepted, usable, or cheaper.
  Separate writes multiply failure, exposure, and cleanup cases. Defer them.
- **Catalog estimate or console checkout.** A package price cannot substitute
  for a quote under [ADR 0002][writes]. Checkout, gateway fallback, and a manual
  credit top-up are not ways to unblock this lifecycle. Keep it read-only if a
  matching quote or a credits-only payment path cannot be established.

No Application GLB (Layer 7), HTTP/TLS/UDP, public or arbitrary-IP backend,
additional regions or pools, attach/update, bulk delete, or DNS/certificate
writes. Do not create or change a server, VPC, subnet, or firewall. No order
or payment gateway, top-up, card, coupon, payment-method, postpaid, or renewal
operation. Account billing budgets are not transaction limits.

## Public and upstream evidence

The [public OpenAPI][openapi] uses `https://glb.api.vngcloud.vn/glb-controller`
and an `Authorization` header described as a bearer token. It has no account,
project, or region path or query parameter for the operations below. The SDK's
[`ProductGLB` route][sdk] uses the same `/glb-controller/v1` prefix on
`https://glb.console.greennode.ai`, through shared SDK IAM-user bearer auth.
[Existing evidence][reads] covers packages and regions live, an empty GLB
list, and shape-only detail and child reads. Do not infer mutation permission
from a successful read or send a bearer to the old host as fallback.

A Playwright session signed in as an IAM user received `200` for GLB
inventory and opened the Network TCP create form. The form displayed
package, listener, pool, backend, and Summary sections. No backend or
complete configuration was selected, so this does not establish quote
availability, wallet-only funding, or write permission. No order was sent.

| Path under `/v1` | OpenAPI status | Upstream status and body |
|-|-|-|
| `GET /global-load-balancers` | 200 | 200, offset list |
| `GET /global-load-balancers/{glbid}` | 200 | 200, direct object |
| `POST /global-load-balancers` | 200 | 202, envelope |
| `DELETE /global-load-balancers/{glbid}` | 200 | 202, no decode |
| `GET /packages`, `GET /regions` | 200 | 200, direct arrays |

OpenAPI supplies no response body schemas for these operations.
[Upstream operations][up-ops] and [response types][up-response] corroborate
routes but disagree with OpenAPI on write acceptance. The create envelope
has `globalLoadBalancer`, `globalListener`, and `globalPool`; the first
contains `id`, `name`, `package`, `type`, `status`, and `userId` among other
fields. Neither source proves the actual status, success/error envelope,
ID semantics, state transitions, or deletion cascade on the SDK host.
[Upstream client auth][up-auth] obtains a token with client credentials;
that is not evidence that the SDK's IAM-user login can create or delete.

OpenAPI's `CreateGlobalLoadBalancerRequest` requires a JSON body. The
listener and pool are optional at the top level. Each supplied object has
its own required properties:

- Root: `name`, `package`, `paymentFlow`, `type` (strings).
- `globalListener`: `allowedCidrs`, `name`, `protocol` (strings); `port`,
  `timeoutClient`, `timeoutConnection`, `timeoutMember` (integers).
- `globalPool`: `algorithm`, `name`, `protocol` (strings); `health` (object);
  `globalPoolMembers` (array).
  - `health`: `protocol` (string); `healthyThreshold`, `unhealthyThreshold`,
    `interval`, `timeout` (integers).
  - Each `globalPoolMembers` item: `name`, `region`, `type` (strings);
    `members` (array).
  - Each `members` item: `address` (string), `backupRole` (boolean), `port`
    (integer).

OpenAPI also allows top-level `description`, listener `globalPoolId` and
`headers`, pool `stickySession` and `tlsEnabled`, the backend group's `vpcId`,
and the individual member's `subnetId`. The proposed bundle needs the VPC
and subnet. OpenAPI does not define protocol or GLB type enums.
[Upstream create][up-request], [listener][up-listener], and [pool][up-pool]
suggest `Layer 4`, `TCP`, `ROUND_ROBIN`, `PRIVATE`, and
`paymentFlow: automated`. They do not prove the initial listener-to-pool
binding, timeout units/defaults, `monitorPort`, `weight`, `trafficDial`, or
whether omitted optional fields must be sent in this bundle. Do not copy the
upstream listener default `0.0.0.0/0`. The [console walkthrough][start] shows
one initial listener/pool, region/VPC/subnet backend selection, and Creating
then Active. [Managing NGLB][manage] documents irreversible deletion, not
what the API deletes, what is charged, or what credits are released.

## Billing and consent

[GLB pricing][pricing] says prepaid GLBs hold forecast credits for the next
three days and add connection/data charges to later holds; postpaid GLBs
bill at the start of the next month. It does not specify the actual amount
held at create or released at delete. The [general credit-hold guide][hold]
does not list GLB, and [general deletion terms][refund] only refund eligible
resources. Do not apply another service's policy to GLB. `paymentFlow` has
OpenAPI values `automated` and `gateway`; the upstream `automated` default
is not proof of prepaid eligibility, credit-only charging, or no fallback.

`ListPackages` exposes SKUs, allowances, and an untyped `Detail`. A package
rate or a price shown beside a plan is catalog information, not a matching
create quote, actual balance delta, debit, or refund. Neither reviewed GLB
API source defines a quote or an atomic price cap. Do not guess a pricing
product ID, endpoint, request body, currency, or a wallet-to-VND conversion.

Require a proven, side-effect-free quote for this exact normalized create
configuration and account. Confirm a prepaid credit-only mode with no
external funding fallback, including when credits are insufficient or the
account type changes. A balance read alone cannot guarantee that. The
quote must give the full initial credit hold/debit required to create,
including any tax and child allocations, with its unit and basis identified.
Require an explicit, positive validated amount for this paid slice. Missing,
zero, negative, malformed, partial, or wrong-unit pricing cannot authorize
create. The quote does not cover future usage; tell callers that creation can
cause continuing holds and charges and that deletion is not a refund promise.

Resolve inputs once. Use the same private normalized request builder for
quote and create; derive only documented pricing fields for the quote. After
preflights, fetch a fresh quote immediately before the single mutation. The
SDK compares it to the required exact `MaxPrice`, in the quote's documented
unit, without floating-point conversion, rounding, or a stale-quote fallback.
Equality is allowed. Use a server-bound quote/cap if verified. Otherwise the
local cap only limits the observed quote: price and credit availability can
change before the server acts, and the cap never bounds later usage. Require
owner acceptance of that race before shipping a local-only guard. If the
requested scope requires a hard spend ceiling and the API cannot enforce it,
do not release create.

## Proposed SDK and CLI

Use the normal `context.Context`, pointer Input and Output method shape in
`globalloadbalancer.Client`. Keep all existing read signatures, names, flags,
paging, and output shapes. Use strict private projections for guard reads
rather than making permissive inventory decoding a source of consent. No new
external dependency or generic payment/ownership subsystem.

- `QuoteCreateLoadBalancer(*CreateLoadBalancerInput)` and
  `globalloadbalancer quote-create-load-balancer` are reads, allowed in a
  read-only profile. Return the verified initial credit requirement, unit, and
  basis. No purchase, prompt, or use of `MaxPrice` in quote.
- `CreateLoadBalancer(*CreateLoadBalancerInput)` and
  `globalloadbalancer create-load-balancer` are writes. Require `Name`,
  `PackageID`, `ListenerName`, `ListenerPort`, `AllowedCIDR`, `PoolName`,
  `PoolMemberName`, `BackendServerID`, `BackendPort`, and `MaxPrice`; allow
  `Description`. Propose `MaxPrice *int64` only if the evidenced unit is whole
  VND; otherwise settle the exact type and flag unit before implementation.
  Return the confirmed `LoadBalancer` and the applied quote, never a claim of
  measured debit. CLI create requires `--yes` and an explicit cap from either
  `--max-price` or `MaxPrice` in `--cli-input-json`. No `--no-wait`,
  `--payment-flow`, raw wire body, or skip-quote flag.
- `DeleteLoadBalancer(*DeleteLoadBalancerInput)` and
  `globalloadbalancer delete-load-balancer` take one `LoadBalancerID`.
  Require CLI `--yes`; return the ID, setting `Deleted` true only after
  confirmation. The command warns about listener/pool effects and possible
  retained usage charges. No name selector, forced delete, bulk mode, or
  automatic cleanup.

Use the normal CLI flag/JSON merge, help, output/query and [exit rules][cli].
Before any request, reject nil or missing required inputs, IDs outside
`^[A-Za-z0-9-]+$`, ports outside 1 through 65535, negative caps, and a missing
cap on create. Accept one canonical IPv4 CIDR with no host bits and no `/0`;
verify its exact wire encoding and enforcement. Leave mutable provider name
limits to the API. Refuse unsupported auth or endpoint provenance before
requesting a token. Use SDK-owned IAM-user login in the verified prepaid
account mode, not root, service account, static bearer, or custom provider.

Select one enabled exact package ID and require supported backend placement.
The GLB remains global; config region and project select only the backend
lookup, never a GLB URL or account. Resolve the caller's existing server in
that verified region and project. Require an unambiguous private IPv4 on one
internal interface, with matching region, VPC and subnet, proven to belong to
that same account/project. Reuse verified Compute and Network reads if they
prove the join; don't add user-provided backend IPs, identity, or membership
assertions. Use only the captured GLB region mapping and package eligibility.
Do not pick a default row or region on missing or ambiguous data. Use verified
fixed protocol, algorithm, and health defaults, not guesswork or console
placeholder values. Fail closed if these data or permissions cannot be read.

## Send, confirm, and recover

Use `Once` and `NoRedirect` on both mutations. No resend after 401, 429, 5xx,
redirect, or transport failure. Keep TLS verification; allow only the proven
first-party bases for this workflow, with no untrusted endpoint override,
route fallback, or URL from a response. Reuse the transport's 8 MiB limit on
uncompressed bodies. Reject malformed fields, duplicate keys, and aliases
of a guard field. Suppress raw captures and peer error text on new guard
and write paths; expose only safe classified errors and validated output.
Once is per invocation, not cross-process idempotency. Separate invocations
can each spend credits; names do not deduplicate orders.

On proven create acceptance, take the path-safe resource ID from the
validated reply. Confirm by that ID in the same account, matching type, package, name, and
owner. Confirm the exact GLB → listener → pool → member ID chain and no
extra children. Match listener protocol, port, restricted CIDR, TCP health
check and the resolved backend to the quoted configuration. A missing or
mismatched field returns an unconfirmed outcome with the proven ID for
manual inspection. Before connecting any backend, require first-party
evidence that the restricted CIDR applies from listener activation.
Read-back proves configuration only; it cannot prove traffic enforcement.
Do not initiate data-plane traffic or interpret `Active` as reachability. Never identify a
created GLB by name, list order, or account balance. On ambiguous acceptance
without a valid ID, return recovery instructions for read-only inventory and
billing checks, not a replacement POST or guessed delete. Use only evidenced
transitional, success, and terminal failure states. Propose an immediate
poll then every 5 seconds, at most 15 minutes after create and 10 after
delete. Shorter caller deadlines win; existing read defaults stay unchanged.

Before DELETE, strictly read the exact ID and verify the returned ID, known
supported type/state and authenticated account ownership. Prove which field
joins to independently resolved GreenNode account identity; `userId` is a
candidate, not a caller-supplied ID or an assumed portal-user mapping.
Preflight must enumerate all child IDs and check their scope. Confirm that
every cascading child is in scope for deletion; uncertain or changed child
inventory authorizes no DELETE. A preflight 404 is `NotFound`, never
permission to send. Confirm deletion by the
proven authoritative terminal detail/absence response on the same scope,
not just 2xx or a transient 404. A 403, malformed body, or changed identity
is not absence. If child cleanup needs another write, keep the slice gated
rather than implementing an unproven cascade. Deletion confirmation never
means a credit release, refund, or zero later charges.

Use `ErrInvalidInput`/`InvalidUsage`, `InvalidConfig`, and `ReadOnly` for
validation and preflight refusal as applicable (exit 2). Use `ErrUnpriced`
or `ErrPriceAboveMax` for the corresponding quote refusal (exit 1);
malformed guard replies are `APIError{Code: "InvalidResponse"}` (exit 1).
Preserve typed API status/cause for definitive rejections, including 401
(exit 3), 403 (exit 1), and preflight `NotFound` (exit 4). Only map a specific
credit rejection once its status and code are established. For a request
that may have landed, malformed acceptance, unknown state, or unconfirmed
wait, return service `ErrNotSettled` (exit 1); use `ErrFailed`/`WriteFailed`
(exit 1) for proven terminal failure. Both preserve safe partial output on
stdout when available: validated ID, last confirmed state and applied quote,
not raw replies, owner identifiers, or claims of settlement. Advise the
caller to inspect detail/list and billing before any new mutation. Errors
and recovery text go to stderr; never prompt or resend.

## Evidence gates and release

1. **Read-only console discovery.** With approved access to a prepaid test
   account, open [GLB inventory][console] and confirm the IAM role and empty
   or existing disposable inventory. If there is an independently approved
   existing GLB, inspect its detail, listener/pool/member relations, and
   account ID join without modifying it. Open the create form, select
   Network TCP, the cheapest eligible package, one existing owned backend
   (region, VPC, subnet), restricted CIDR, and the Summary tab. Record
   sanitized catalog, price labels, required form fields, defaults, exact
   host and read-only preview request/response if present. Stop before any
   Create, Confirm, Pay, or action that might create an order, reserve funds
   or provision. In Billing, inspect prepaid account mode and Credit History,
   Payment History, and Billing History, retaining only safe shape, units,
   transaction kind, and join fields. Never capture auth or account values
   publicly. A displayed price or absent preview does not close the gate.
2. **Contract and resources.** Obtain first-party confirmation or sanitized
   read-only evidence of the quote and credits-only funding rules, including
   insufficient-credit rejection, permissions and account/region scope.
   Verify the candidate region's exact backend identifiers, enabled package,
   ownership join, permitted health-check traffic, input wire shape, and
   CIDR enforcement from activation. Establish whether a single DELETE
   removes the bundle and what charges, holds or release can remain. If no
   safe exact quote or upper cost bound for the probe, no existing eligible
   backend, no verified cleanup route, or uncertain wallet-only funding, do
   not submit a paid probe.
3. **Separately approved lifecycle evidence.** The owner must name the test
   account, region/project, existing backend, package, maximum credit at risk
   (including usage), lifetime, and cleanup authority. Record a ledger
   baseline. In one serialized run with no generated client traffic, quote
   the same payload, then submit one initial create. Capture status, safe
   response shape, resource and child identity, state transitions, and the
   independent credit hold/debit and any invoice rows. Keep the test bundle
   exclusive to the probe for its lifetime. Re-read the complete child
   inventory immediately before DELETE. Stop if it differs from the recorded
   owned IDs or if exclusion of other writers cannot be maintained. Delete
   only the proven run-created parent ID once and confirm disappearance and
   child cleanup.
   Reconcile release, retained charge, or delay using resource-linked ledger
   evidence, not a global before/after balance alone. Report leftovers and
   unresolved billing; do not retry, delete a name match, or deliberately
   deplete credit to force a failure. Apply the [live-data rules][live].
4. **Review, then implement and release.** Record observed routes, payloads,
   statuses and failure mappings, owner/cap decisions, and sanitized examples
   before independent design review and code. Test on loopback: catalog and
   ownership/placement joins; shared quote/create normalization; cap, absent
   price, wrong unit, ambiguous eligibility and no-write refusals; exact body,
   path ID and auth guards; failed/delayed and ambiguous writes, all retry and
   redirect cases, bounded waits, and delete confirmation. Test hostile
   responses, token/redaction canaries, and CLI help, JSON/flag precedence,
   read-only and `--yes`, output, query failure, and exits. Keep existing
   reads unchanged. Land SDK, CLI and updated generated wiki together after
   `make check`, independent adversarial review, and release gates.

Owner decisions before code: approve the one-private-backend scope and
restricted CIDR, the first verified region, the quote's unit, and whether a
local-only price guard suffices. A design approval does not authorize a live
probe, spend, or cleanup.

[children]: glb-pool.md#scope-guards
[writes]: ../adr/0002-write-api-conventions.md
[sdk]: ../../internal/endpoints/endpoints.go
[reads]: cli-reads.md#globalloadbalancer
[cli]: cli.md#errors-and-exit-codes
[live]: ../../instructions/live-data.md
[openapi]: https://docs.api.greennode.ai/service-docs/glb-api.html
[start]: https://docs.greennode.ai/glb/network-glb/getting-started-with-network-glb
[manage]: https://docs.greennode.ai/glb/network-glb/managing-nglb
[pricing]: https://docs.greennode.ai/glb/pricing
[console]: https://glb.console.greennode.ai/glb/list
[hold]: https://docs.greennode.ai/billing-management/experience-with-billing-and-payment/payment/credit-hold
[refund]: https://docs.greennode.ai/billing-management/experience-with-billing-and-payment/resource-lifecycle-management/delete-resource
[up-ops]: https://github.com/GreenNodeHub/vngcloud-go-sdk/blob/54700ac4189008f1d0b8aa0270aa9cad79dad4c3/vngcloud/services/glb/v1/glb.go#L340-L435
[up-response]: https://github.com/GreenNodeHub/vngcloud-go-sdk/blob/54700ac4189008f1d0b8aa0270aa9cad79dad4c3/vngcloud/services/glb/v1/glb_response.go#L126-L166
[up-auth]: https://github.com/GreenNodeHub/vngcloud-go-sdk/blob/54700ac4189008f1d0b8aa0270aa9cad79dad4c3/client/client.go#L244-L267
[up-request]: https://github.com/GreenNodeHub/vngcloud-go-sdk/blob/54700ac4189008f1d0b8aa0270aa9cad79dad4c3/vngcloud/services/glb/v1/glb_request.go
[up-listener]: https://github.com/GreenNodeHub/vngcloud-go-sdk/blob/54700ac4189008f1d0b8aa0270aa9cad79dad4c3/vngcloud/services/glb/v1/glb_listener_request.go
[up-pool]: https://github.com/GreenNodeHub/vngcloud-go-sdk/blob/54700ac4189008f1d0b8aa0270aa9cad79dad4c3/vngcloud/services/glb/v1/glb_pool_requests.go
