# Public NAT and Site-to-Site VPN Writes

Status: Accepted (2026-10-11). The accepted
[NAT contract](network-writes-nat.md) amends the NAT parts.

Add paid create and delete to `network.Client` and the CLI. Public NAT
ships first, with its quote and delete in the same release. VPN follows
only after its create body and secret handling are proven. No create is
ready for implementation until the applicable evidence gaps below close.

Follow [ADR 0002](../adr/0002-write-api-conventions.md), the send-once
transport in [ADR 0003](../adr/0003-toggle-writes.md),
[vServer paid writes](vserver-paid-writes.md), and
[vStorage projects](storage-projects.md). [API evidence](network-writes-api.md)
separates observed requests from proposed fields. The
[read design](network-services.md) owns inventory models and routing.
Accepted choices remain in force except where the
[NAT contract](network-writes-nat.md) replaces them.

## Scope and compatibility

Support one-month prepaid purchases from credit, one resource per call.
Exclude standalone renewal settings, resize, rename, tags, NAT rules,
extra VPN sites or tunnels, key rotation, configuration downloads, and
connectivity tests. NAT create includes one renewal-off write
as described in the [NAT contract](network-writes-nat.md#renewal-off).
Exclude trial, proof-of-concept, postpaid, and external payment methods.

New methods and types live in `network`; no root service re-exports or
SDK dependencies are added. Existing read methods remain paged and keep
their contracts. Write guards use private complete-list scans. No detail
endpoint is assumed. New outputs reuse the read design's safe models.

## SDK surface

Methods use `Method(ctx, *Input) (*Output, error)` on `network.Client`.
Operation names have the `network.` prefix.

| Method | Input | Output |
|---|---|---|
| `QuoteCreateNATInstance` | `CreateNATInstanceInput` | `NetworkQuoteOutput` |
| `CreateNATInstance` | `CreateNATInstanceInput` | `CreateNATInstanceOutput` |
| `DeleteNATInstance` | `DeleteNATInstanceInput` | Empty output |
|`QuoteCreateVPNConnection`|`CreateVPNConnectionInput`|`NetworkQuoteOutput`|
|`CreateVPNConnection`|`CreateVPNConnectionInput`|`CreateVPNConnectionOutput`|
| `DeleteVPNConnection` | `DeleteVPNConnectionInput` | Empty output |

Both create inputs have required `Name`, `ZoneID`, `PackageID`, and
`VPCID` strings and `MaxPrice float64`. VPN retains `NoWait bool`. The
[NAT input amendment](network-writes-nat.md#inputs-and-discovery) adds
required `AvailabilityZoneID` and removes create `NoWait`. `ZoneID` keeps the
read design's region-level path meaning, distinct from an availability
zone. Region and project come from the configured selection. There is no
implicit VPC, package, or zone. No `Period` or `AutoRenew` input is exposed:
one month and confirmed renewal off remain the intended result.

VPN adds required `SubnetID`, `SiteName`, `RemoteGatewayIP`, `TunnelName`,
and `RemoteNetworkCIDR` strings, plus `PreSharedKey vngcloud.Secret`.
Proposed phase fields are `Phase1Configs []VPNPhase1Config` and
`Phase2Configs []VPNPhase2Config`, using the safe read types. Require a
nonempty configuration for each phase. These names are a proposed public
contract, not a claim that the read schema is the create wire schema.
Algorithm selectors, required fields, and lifetime units must be confirmed
before accepting the VPN contract. Never choose weaker algorithms silently.

Each delete input has required `VPCID` and resource ID (`NATID` or `VPNID`),
plus `NoWait`. NAT delete also requires `ZoneID`; VPN delete has no zone
path segment or zone discovery.

`NetworkQuoteOutput` holds `OptimumPrice`, `OriginalPrice`, `DiscountPrice`,
nullable `DiscountPercent`, and typed `Properties` carrying the observed
price fields. It also holds `MonthlyPrice`, `TotalPrice`, and `Currency`.
Prices use float64; currency is VND. `MonthlyPrice` and `TotalPrice` both
equal `OptimumPrice` for one month. Do not use inventory `monthlyPrice`,
which is observed as zero. Preserve property `currentPrice` separately.

Create outputs hold `NATInstance *NATInstance` or
`VPNConnection *VPNConnection`, `OrderID string`, `MonthlyPrice`,
`TotalPrice`, `Currency`, and `AutoRenew *bool`. Nil resource or renewal
state means unconfirmed. `OrderID` comes only from a verified response
field, never a URL. Return safe partial output on post-order errors.
NAT's direct response has no ID, so its `OrderID` stays empty. Populate
`AutoRenew` only from a valid billing observation, never the order flag.
Never return checkout URLs, payment metadata, or the VPN key.

## Validation and VPC consent

Validate required fields and all path and body IDs with `core.CheckPathID`
before discovery. Check IP and CIDR syntax without inventing service
limits. The server owns names, package limits, and algorithm support.
Reject negative, NaN, or infinite `MaxPrice` before any request.

Every create and delete requires the caller's explicit `VPCID`. Read that
VPC in the selected project and region. VPN create also reads the named
subnet and proves that it belongs to that VPC and zone. Resolve the
vNetwork project and region namespaces without guessing. Missing or
inconsistent membership prevents the write. Never select a default VPC,
create one implicitly, or infer consent from the resource's VPC field.

Before create, take a complete inventory baseline in the selected scope.
Reject an exact duplicate resource name. For NAT also refuse an existing
NAT in the named VPC; replacement is outside scope. Before delete, find
the exact resource ID and require its VPC to equal the caller's VPCID.
Never delete a VPC, subnet, route, site, or tunnel through a separate call
as an implicit side effect of these methods.

Private scans follow all pages and require consistent totals, unique IDs,
valid envelopes, and forward progress. Incomplete or changing inventories
fail closed. An empty first page cannot prove absence when totals disagree.
The read design's live multi-page evidence gap remains a prerequisite for
claims about multi-page guards. Duplicate checks do not prevent concurrent
clients from ordering the same name. Serialize writes to the same VPC;
there is no proven server idempotency key or conditional write.

## Price and purchase

Each quote takes its create's Input and is an idempotent read. Quote and
create share one resolved specification and the same price serializer.
Separate order serialization adds only verified order fields. VPN keys
never enter pricing. NAT initially uses the captured priced fields,
including name and VPC, until omission probes establish a smaller body.
Quotes ignore `MaxPrice` and VPN `NoWait` and need no pre-shared key.
VPN quote-only required fields depend on the verified price schema.

A create performs these steps in order:

1. Validate input, resolve scope, and complete the VPC and inventory guards.
2. Fetch a fresh quote. Require `success: true` and presence-aware prices.
   Missing, null, malformed, non-finite, or duplicate-key replies fail with
   `InvalidResponse`. Zero or negative totals return `ErrUnpriced`.
3. Compare positive `TotalPrice` to `MaxPrice`; equality passes. Default
   zero refuses a purchase. Above-cap returns `ErrPriceAboveMax`, naming
   both amounts, before any order. Prices in the evidence are examples,
   never defaults or hard-coded caps.
4. Submit one order. NAT uses the verified direct collection POST with
   explicit renewal false, no `period`, and no `paymentType`. VPN's direct
   order has console-code evidence only and still requires a live probe.
5. Confirm identity, payment, readiness, and renewal under the applicable
   service contract. NAT waits for ACTIVE and billing before the
   renewal-off write. VPN retains its readiness-only `NoWait` option.

Every order, renewal PUT, and DELETE uses `transport.Request.Once` and
`NoRedirect`: no resend after 401, 429, 5xx, failed dial, redirect, or lost
response. The SDK and CLI
have no outer write retry. Reads retain their retry policy within the
operation's context and time bound.

The captured order has no quote token or server-enforced price cap.
`MaxPrice` bounds the preceding quote, not an atomic debit. Quote
immediately before ordering. A different debit fails live verification;
reconcile and clean up the identified resource without ordering again.

## Payment and auto-renew

IAM-user SDK login in HAN verified direct NAT credit purchase through the
resource collection, without checkout, cookies, `paymentType`, or `period`.
The server ignores the order's renewal-off field. The
[NAT amendment](network-writes-nat.md) defines the auth restrictions,
billing confirmation, and the explicit second write that disables renewal.
Root checkout is evidence only and stays outside the SDK contract.

VPN direct purchase and renewal-off behavior remain unverified. Console
AUTO routing is not proof of either. A successful envelope, resource row,
or checkout URL alone proves neither paid status nor renewal policy.
Do not silently correct VPN renewal with another write.

No fallback submits a second order, follows a redirect, opens a browser,
or calls `payment-api`. A proven clean refusal returns its safe error.
If verified reads prove checkout is required and no payment is in flight,
return `network.ErrPaymentRequired`. An empty list or redirect alone cannot
prove that condition; unresolved payment returns `network.ErrNotSettled`.
A payment extension requires a separate approved amendment proving IAM
authorization, same-order binding, amount, term, renewal, and cleanup.

Keep the selected vNetwork origin and `Endpoints.VNetwork` override
rules from the read design. Never forward credentials to a redirect or
callback destination, including a different GreenNode hostname. There is
no new payment endpoint configuration in these releases.

## Confirmation and recovery

Prefer a resource ID from a proven response field. Otherwise require one
new exact-name match absent from the baseline, with matching project,
region, VPC, package, and the verified zone mapping; NAT uses its
[identity rules](network-writes-nat.md#identity-and-payment). Match the
VPN subnet too. Check the VPN site's and tunnel's safe configuration as
well; never compare or return a read key.
Do not adopt or delete an ambiguous match. A matching resource without
confirmed payment and renewal is still unsettled.

| Operation | Confirmed result | Poll | Bound |
|---|---|---|---|
| NAT create | Matching `ACTIVE`, paid, renewal off | 5 s | 15 min |
| VPN create | Matching `ACTIVE`, paid, renewal off | 5 s | 15 min |
| Either delete | ID absent from complete scoped inventory | 5 s | 10 min |

Create bounds start after the order attempt; delete bounds start after
the DELETE attempt. NAT billing confirmation uses the shorter poll and
bound in the amendment, within the overall create bound. Honor a shorter
context deadline; use injected clocks in tests.
NAT's observed 5 to 6 minutes rules out a short generic create timeout.
VPN `ACTIVE` establishes provisioning, not a working peer or tunnel.

Missing resources and unknown or moving states keep polling on create.
Observed NAT `ERROR` returns `network.ErrFailed` with partial output.
VPN failure states need evidence before classification. A failed read,
malformed write reply, cancellation after send, timeout, or unresolved
payment returns `ErrNotSettled`. No resource on a list is not proof that
an order failed. Proven rejection before a side effect returns a safe
API error; contradictory payment evidence changes it to `NotSettled`.

VPN create `NoWait` skips readiness polling only. It still requires paid
purchase, matched identity, and renewal off. Those confirmations can wait
within the create bound. It never turns a redirect or ambiguous response
into success. Delete `NoWait` returns only after validated acceptance;
it does not claim absence or refund.

NAT recovery follows the [NAT contract](network-writes-nat.md#recovery).
VPN recovery names `network list-vpn-connections` in the same scope and
directs the caller to pending orders and payment history. It says not to
repeat create while payment or provisioning is unresolved. Keep known safe
IDs in the partial output. Do not retain sensitive response-bearing errors
as causes.

## Delete

Require the CLI's `--yes` and writable profile before any request. The SDK
has no interactive consent. Apply the VPC and identity guards, then send
one DELETE using the observed routes in the API evidence. Require HTTP
200 and explicit `success: true`; NAT requires numeric `code: 0` and
sends JSON `{}`. VPN delete sends no body.
A bare 2xx, empty body, or malformed envelope is not acceptance.

Confirmed absence before send returns `NotFound`, with no DELETE. After
send, confirm absence by complete list reads even when the reply is lost.
A 404 from a resource list endpoint is not proof of deletion. If reads
cannot confirm absence, return `NotSettled`. A later independent call
repeats all guards; no call resends its DELETE to force settlement.

NAT deletion can interrupt VPC egress. VPN deletion removes connectivity
and its embedded site and tunnel configuration. There is no force or
cascade flag. Success confirms removal from inventory, not refund or
restoration of the previous route table. The IAM NAT runs observed route
table and billing row removal; one run deleted its empty VPC on the first
attempt after NAT absence. Prior-route behavior and detach timing are not
guaranteed. The SDK does not rewrite routes to repair cleanup.

Observed full refunds are not a refund guarantee. Provisioning failure
can refund automatically while leaving a resource to inspect. Ordinary
delete does not poll the wallet; the paid live runs reconcile refunds
separately and report outstanding obligations.

## VPN secret boundary

CLI input accepts only `--pre-shared-key-file <path>` or
`--pre-shared-key-file -` for noninteractive stdin. There is no string
flag, environment variable, prompt, or JSON input path for the key.
Reject key fields in `--cli-input-json`, including file-backed JSON.
Reject terminal stdin so typing cannot echo the key. Read-only and usage
guards run before opening the file or reading stdin.

Read at most 64 KiB, reject empty input, and remove at most one terminal
LF or CRLF. Preserve all other bytes; server key rules remain server-owned.
The size cap is a local input bound, not a claimed VPN limit. Never print
contents in validation or I/O errors. SDK callers supply `vngcloud.Secret`
from their own secret source. Reveal it only to the private create-body
serializer. Never format inputs, use reflection to inspect the underlying
string, or serialize them for diagnostics. No key appears in examples.

Every VPN request, including quote, order, delete, and confirmation, is
Sensitive. All order replies are also Sensitive because they can hold
checkout tokens. Apply the read design's fixed-error contract to VPN and
order responses: withhold server messages, codes, decode causes, and raw
bodies, including HTTP 200 failures. Preserve only safe status, operation,
retryability, and shared sentinels. A secret wrapper alone is insufficient.

Omit `preShareKey` from every response model, including private decoders.
Drop unknown fields; no raw JSON or catch-all output. No reveal command
exists. Test keys in nested and unknown fields, malformed bodies, errors,
capture hooks, debug logs, queries, and JSON, text, and table output.
VPN fixtures are synthetic only. Do not persist a live key-bearing body.

## CLI and errors

Commands under `network` are `quote-create-nat-instance`,
`create-nat-instance`, `delete-nat-instance`, `quote-create-vpn-connection`,
`create-vpn-connection`, and `delete-vpn-connection`. Fields map to kebab-case
flags; global project and region flags retain their meanings. Nested VPN
phase settings use key-free `--cli-input-json`; no raw request-body escape.
Quotes are reads and work in read-only profiles. Creates use `--max-price`
as cost consent. Both NAT writes also require `--yes` because they change
VPC egress; VPN delete requires `--yes`. VPN create needs explicit VPC,
subnet, peer, and phase configuration, without an additional `--yes`.

NAT create help must say: "Creates a 0.0.0.0/0 route in the named VPC.
Every VM in that VPC uses this NAT for egress. Use a disposable VPC for
testing; never test in a production VPC." NAT help must also explain the
[renewal step and wait](network-writes-nat.md#cli). Delete help warns that
egress can stop and prior routes are not restored by the CLI. VPN help states
that `ACTIVE` does not prove tunnel connectivity and keys are never shown.

| Condition | Error code | Exit |
|---|---|---|
| Invalid input, VPC mismatch, missing consent, read-only | `InvalidUsage` | 2 |
| Unsupported NAT auth, region, or V3 mode | `InvalidConfig` | 2 |
| Nonpositive quote | `Unpriced` | 1 |
| Quote over cap | `PriceAboveMax` | 1 |
| Confirmed missing target | `NotFound` | 4 |
| Proven terminal provisioning failure | `WriteFailed` | 1 |
| Uncertain write, renewal, or payment | `NotSettled` | 1 |
| Proven checkout requirement | `PaymentRequired` | 1 |
| Invalid response or safe server refusal | Safe API error code | 1 |

Reuse `network.ErrNotSettled`, `network.ErrFailed`, and shared input,
not-found, unpriced, and price-cap sentinels. Add `network.ErrPaymentRequired`.
Preserve sentinel identity for existing callers. Quotes and reads that
fail stop before ordering; no error prints the input key or checkout URL.

## Evidence gates and release order

The [probe list](network-writes-api.md#probes-before-implementation) records
closed and open evidence gates. The IAM NAT lifecycle and renewal-off write
are proven in HAN. Owner approval of the NAT amendment remains required;
multi-page guard evidence remains a gate. Do not implement an inferred VPN
body or a guessed payment read.

1. Public NAT create, quote, delete, guards, waits, SDK and CLI docs.
   Depends on NAT inventory and proven direct credit payment. Start with
   HAN, where successful provisioning is observed. Other regional write
   support requires its own successful run; no automatic region fallback.
2. Site-to-site VPN create, quote, delete, secret boundary, SDK and CLI docs.
   Depends on VPN inventory, proven VPN body and pricing, and the same
   service-specific payment and renewal evidence.

Each feature gets a separate release with green CI on its exact commit.
If direct purchase is unsupported, defer that create release. A delete-only
release is a separate owner decision; do not silently change this order.

Use `httptest`, synthetic VPN fixtures, and injected clocks. Cover quote
and order field parity, explicit renewal false, the NAT billing write,
its confirmation and no-resend failures, all price failures,
complete-list guards, VPC mismatches, scope routing, no resend on every
failure class, payment ambiguity, partial outputs, waits, and secret
withholding. Verify consent and read-only gates before any request.
Run `make check` for implementation and the required live-tag compile and
vet checks from [verification](../../instructions/verification.md).
Apply the write security checks required by ADR 0002 before release.

## Owner decisions

The owner approved the base choices below on 2026-10-11. The
[NAT decisions](network-writes-nat.md#owner-decisions), approved the same
day, replace them for NAT where they differ:

1. Public methods, names, inputs, outputs, and first-release scope above,
   including one default VPN site and tunnel and explicit phase choices.
2. One-month prepaid credit purchases only; auto-renew fixed off with no
   opt-in flag, and confirmation required even under `NoWait`.
3. `MaxPrice` compares the immediate quote's total; accept the documented
   quote-to-debit race rather than claim a server-enforced cap.
4. One proven auto-payment attempt; defer create on refusal or checkout.
   Direct `payment-api` support needs a separate approved amendment.
5. Explicit VPC and scope guards on both writes, duplicate-name refusal,
   and refusal to create a second NAT in a VPC.
6. NAT create requires `--yes` as well as `--max-price`; both deletes need
   `--yes`. VPN create uses the price cap and explicit configuration.
7. The wait bounds, `NoWait` limits, read-only recovery, and separation of
   deletion from refund or route restoration guarantees.
8. The file/stdin key input, 64 KiB bound and newline handling, typed
   secret input, omitted response keys, and fixed VPN/order errors.
9. NAT before VPN, HAN first, and a separate paid run for each additional
   region. Auto failure defers create rather than enabling checkout.
10. Refundable paid runs have standing owner approval in a disposable
    Hanoi VPC: one order at a time, cap 1,000,000 VND per order, auto-renew
    off, delete right away, and report the net cost. Refund amount and
    timing remain uncertain. A payment fallback probe additionally needs
    approval for its exact payment action.
