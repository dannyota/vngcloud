# vLB Writes Design

Status: Accepted (2026-09-28). The owner approved every recommendation
under [Owner decisions](#owner-decisions).

This design adds paid vLB writes to `loadbalancer`: load balancer create,
package change, and delete; listeners (HTTP, HTTPS, TCP, UDP); pools with
their health monitors; pool members; and Layer 7 policies. A create or a
package change costs money, so each one prices itself first and orders
nothing above a limit the caller sets, default 0 VND.

The test account has no credit. The code is built and unit-tested with
`httptest` now; each paid release's live checks and tag wait for credit.
Quotes are reads and ship first.

It follows [ADR 0002](../adr/0002-write-api-conventions.md) for every
write (rule 8: every paid create has a quote built by the create's own
code), the price guard of [vMonitor Alerts](monitor-alerts.md#log-projects),
and the waits and busy handling of
[vServer network writes](vserver-network-writes.md#waits). Certificates
already ship ([vLB certificates](lb-certificates.md)); an HTTPS listener
names one. The calls, bodies, statuses, and live quotes are in
[vLB writes: API](lb-writes-api.md). Tests, probes, live checks, and the
security review are in [vLB writes: checks](lb-writes-checks.md).

## Non-goals

- Renaming a load balancer, listener, or pool: the API has no call for
  it. Rename means create, move traffic, and delete.
- Inline listener and pool in the load balancer create body: separate
  calls cover them with one code path each.
- Bandwidth packages, autoscaling (`rebalancing`), clone, policy reorder,
  tags, and auto-renew.
- Listener fields the listener read does not return (`blockedCidrs`,
  `defaultAction`, `alpnProtocols`, `tlsSecurityPolicy`): an update is a
  full replace, and a field the SDK cannot read back would be wiped by
  the next update. A field joins when a live read shows it.
- Members at pool create: `AddPoolMember` covers them.
- A whole-list member replace from the caller, as in vServer network
  writes: an empty list from a script would drain a pool.

## Price guard

`CreateLoadBalancer` and `ResizeLoadBalancer` each have a quote that
takes the same Input, and each quotes before it sends:

1. Check the Input's shape, and that `MaxPrice` is finite and not
   negative (`ErrInvalidInput`).
2. Quote with the same builder the write uses, reading the response
   itself rather than through `pricing.GetQuote`: `GetQuoteOutput`'s
   `OptimumPrice` is a plain `float64`, so a JSON `null` there silently
   becomes 0, indistinguishable from a genuine zero price. The guard
   reads the raw quote body and refuses a missing `optimumPrice` key, a
   null one, or a NaN or infinite number, whatever pricing.GetQuote would
   have done with the same response.
3. A create's quote of 0 or less, and a resize's quote of exactly 0,
   returns `ErrUnpriced`, sending nothing, whatever `MaxPrice` is: nothing
   in vLB is free, so the gateway could not price the input. A resize's
   negative quote is a downsize refund and is allowed.
4. When the price is above `MaxPrice`, return `ErrPriceAboveMax` naming
   both amounts, sending nothing.
5. Send the write once. Never send it again after a failure that may have
   reached the server: the create `POST` sets `Once`, and the resize
   `PUT` already did.

`MaxPrice` is VND and defaults to 0, so a bare create orders nothing: the
cheapest package quotes 400,000 VND a month. A create quote uses
`period` 1, so it is the first month's price. What a resize quote prices
(the difference for the rest of the period, or a full month) is a live
check; the guard compares whatever the quote says. The price can change
between the quote and the write; the gap is one request.

`pricing.GetQuoteInput` gains `Action` (empty sends `create`, as today)
and constants `ActionCreate`, `ActionResize`, and `ResourceLoadBalancer`
(`load-balancer`). The change adds fields and breaks no caller.

## Load balancers

"(r)" marks `vngcloud:"required"`, and "*" a pointer sent only when set
(ADR 0002 rule 3).

| Operation | Input | Output |
|-|-|-|
| `QuoteCreateLoadBalancer` | `*CreateLoadBalancerInput` | `pricing.GetQuoteOutput` |
| `CreateLoadBalancer` | `Name` (r), `PackageID` (r), `Type` (r), `Scheme` (r), `SubnetID` (r), `ZoneID` (r), `MaxPrice`, `NoWait` | `{LoadBalancer; QuotedPrice}` |
| `QuoteResizeLoadBalancer` | `*ResizeLoadBalancerInput` | `pricing.GetQuoteOutput` |
| `ResizeLoadBalancer` | `LoadBalancerID` (r), `PackageID` (r), `MaxPrice`, `NoWait` | `{LoadBalancer; QuotedPrice; Changed}` |
| `DeleteLoadBalancer` | `LoadBalancerID` (r), `NoWait` | `{}` |

Constants: `TypeLayer4` (`Layer 4`), `TypeLayer7` (`Layer 7`),
`SchemeInternet` (`Internet`), and `SchemeInternal` (`Internal`). The SDK
sends them as given (ADR 0002 rule 5).

- `Scheme` has no default, unlike the console's `Internet`. An internet
  address is an exposure the caller names; see [Security](#security). It
  must be exactly `SchemeInternet` or `SchemeInternal`: any other value,
  including padding, a different case, or an invented value such as
  `Public`, is `ErrInvalidInput` before any request, quote included.
- `ZoneID` is required and goes to both the quote and the create. The
  SDK picks no default, since a vServer VPC create showed the server's
  default zone can be one the account cannot use. Whether it must match
  the subnet's zone is a live check.
- The create body is `name`, `packageId`, `scheme`, `subnetId`, `type`,
  `zoneId`, `autoScalable` false, and `isPoc` false. The quote body is
  `packageId`, `zoneId`, `period` 1, `isPoc` false, and `isBuyMorePoc`
  false, built from the same Input by one function.
- `QuotedPrice` is the `OptimumPrice` the guard accepted.
- Resize reads the load balancer first. The same `PackageID` returns
  `Changed` false with nothing quoted or sent. Otherwise it waits until
  the load balancer is not busy (see [Busy](#busy)), quotes, and sends
  the `PUT` with `Once`: a paid write is never resent, whatever the
  failure. A busy refusal after the send returns `ErrBusy`; the server
  did not act, and a rerun is safe because it reads first.
- Delete reads first: 404 is `NotFound`, and a load balancer already
  `DELETING` is waited on without a second `DELETE`. The `DELETE` keeps
  the transport's retries; a retry that finds it gone returns `NotFound`.
  What the server does with listeners and pools is a live check; the
  design follows [decision 6](#owner-decisions).

## Listeners

| Operation | Input | Output |
|-|-|-|
| `CreateListener` | `LoadBalancerID` (r), `Name` (r), `Protocol` (r), `Port` (r), `AllowedCIDRs []string` (r), `DefaultPoolID`, `TimeoutClient`, `TimeoutMember`, `TimeoutConnection`, `CertificateIDs []string`, `DefaultCertificateID`, `ClientCertificateID`, `InsertHeaders`, `NoWait` | `{Listener}` |
| `UpdateListener` | `LoadBalancerID` (r), `ListenerID` (r), and each create field after `Port` as * | `{Listener}` |
| `DeleteListener` | `LoadBalancerID` (r), `ListenerID` (r), `NoWait` | `{}` |

Constants: `ProtocolHTTP`, `ProtocolHTTPS`, `ProtocolTCP`, `ProtocolUDP`.

- `AllowedCIDRs` is required with no default, unlike VNG Cloud's SDK,
  which sends `0.0.0.0/0`. Each entry must parse as an IPv4 prefix with no
  host bits; the SDK joins them with commas.
- A timeout of 0 sends the upstream default: 50, 50, and 5 seconds.
- `Protocol` `HTTPS` needs `DefaultCertificateID`. Any other protocol
  refuses all three certificate fields, so a certificate never attaches
  where the server has no use for it. The SDK does not read the
  certificate; the server checks it exists.
- Update is a read-merge: read the listener, apply the set fields, and
  send the full body with the read values for the rest. An update with no
  field set is `ErrInvalidInput` with nothing sent. The `PUT` keeps the
  transport's retries: resending a full body is safe. The Output is a
  read after the wait.
- Two updates from different processes can lose one; the API has no
  version field.

## Pools and health monitors

| Operation | Input | Output |
|-|-|-|
| `CreatePool` | `LoadBalancerID` (r), `Name` (r), `Protocol` (r), `Algorithm`, `Stickiness` *, `TLSEncryption` *, `HealthCheckProtocol` (r), `HealthCheckPath`, `HealthCheckMethod`, `HealthCheckHTTPVersion`, `HealthCheckDomainName`, `HealthCheckSuccessCode`, `HealthyThreshold`, `UnhealthyThreshold`, `HealthCheckInterval`, `HealthCheckTimeout`, `NoWait` | `{Pool}` |
| `UpdatePool` | `LoadBalancerID` (r), `PoolID` (r), and each create field after `Protocol`, except `HealthCheckProtocol`, as * | `{Pool}` |
| `DeletePool` | `LoadBalancerID` (r), `PoolID` (r), `NoWait` | `{}` |

- The health monitor is flat in the Input, so each field is a CLI flag.
  Its reads stay `GetPoolHealthMonitor`.
- Empty `Algorithm` sends `ROUND_ROBIN`; 0 thresholds, interval, and
  timeout send 3, 3, 30, and 5. An `HTTP` pool always sends `stickiness` and
  `tlsEncryption` (`false` when nil); other pools send each only when set.
- The HTTP fields are sent only for `HTTP` and `HTTPS` checks; with any
  other protocol, setting one is `ErrInvalidInput`. An `HTTP` check sends
  `/`, `GET`, `200`, and `1.1` for an empty path, method, success code, and
  version, in `UpdatePool` too; `domainName` only when set.
- Update reads the pool and its health monitor, merges, and sends the full
  body. The check protocol cannot change after create; the update body has
  no field for it.
- `Pool` gains `ProgressStatus`; `HealthMonitor` already has it.

### Pool delete guard

`DeletePool` lists the load balancer's listeners and returns `ErrInUse`,
sending nothing, when one names the pool as `DefaultPoolID`. A policy
that redirects to the pool is left to the server's refusal: a scan costs
a request per listener. An API error whose message contains
`is used in listener` also wraps `ErrInUse`, whatever its status.

## Pool members

| Operation | Input | Output |
|-|-|-|
| `AddPoolMember` | `LoadBalancerID` (r), `PoolID` (r), `Address` (r), `Port` (r), `Name`, `Weight`, `MonitorPort`, `Backup`, `NoWait` | `{Pool; Changed}` |
| `UpdatePoolMember` | `LoadBalancerID` (r), `PoolID` (r), `Address` (r), `Port` (r), `Name` *, `Weight` *, `MonitorPort` *, `Backup` *, `NoWait` | `{Pool; Changed}` |
| `RemovePoolMember` | `LoadBalancerID` (r), `PoolID` (r), `Address` (r), `Port` (r), `NoWait` | `{Pool; Changed}` |

The members `PUT` replaces the whole list, and the request carries no
member ID, so a member is keyed by `Address` and `Port`. Each operation
is a read-merge, as [routes](vserver-network-writes.md#routes-replace)
are:

1. Wait until the load balancer and the pool are not busy
   ([Busy](#busy)).
2. Read the members and build the list from `address`, `protocolPort`,
   `weight`, `monitorPort`, `backup`, and `name`, then apply the change.
3. Compare, then send or stop. Add of a present key with the same fields:
   `Changed` false, nothing sent; with other fields: `ErrInvalidInput`
   naming `update-pool-member`. Update or remove of an absent key:
   `NotFound`. Update that changes nothing: `Changed` false.
4. Send the list, wait, and confirm that the members read equal the list
   sent. A mismatch is `ErrNotSettled`, whose message says another writer
   may have changed the pool.

`Address` must parse as IPv4 (the reference's pattern); `Port` and
`MonitorPort` are 1 to 65535; `MonitorPort` 0 sends `Port`. Weight 0
sends 1. The server's `identical to the existing members` refusal maps
to `Changed` false, since step 3 already stops that case.

## Policies

| Operation | Input | Output |
|-|-|-|
| `CreatePolicy` | `LoadBalancerID` (r), `ListenerID` (r), `Name` (r), `Action` (r), `RedirectPoolID`, `RedirectURL`, `RedirectHTTPCode`, `KeepQueryString`, `Rules []PolicyRuleInput`, `NoWait` | `{Policy}` |
| `UpdatePolicy` | `LoadBalancerID` (r), `ListenerID` (r), `PolicyID` (r), `Action` *, `RedirectPoolID` *, `RedirectURL` *, `RedirectHTTPCode` *, `KeepQueryString` *, `Rules` *, `NoWait` | `{Policy}` |
| `DeletePolicy` | `LoadBalancerID` (r), `ListenerID` (r), `PolicyID` (r), `NoWait` | `{}` |

- `PolicyRuleInput` has `Type`, `CompareType`, and `Value`, all required.
  Rules come through `--cli-input-json`, as check locations do.
- `REDIRECT_TO_POOL` needs `RedirectPoolID` and refuses the URL fields;
  `REDIRECT_TO_URL` needs `RedirectURL` and refuses `RedirectPoolID`.
  Other values go to the server as given.
- Update reads, merges, and sends the full body; a set `Rules` replaces
  the rule list, and an unset one resends the rules read.

## Busy

A load balancer is busy while its `progressStatus` is `CREATING`,
`CREATING-BILLING`, `UPDATING`, or `DELETING`, and a child while its own
is `CREATING`, `UPDATING`, or `DELETING`. The server refuses a write to
a busy load balancer or child with a `not ready` or `is updating` message
([API](lb-writes-api.md#busy-and-error-messages)).

- Before every write except create and delete of a load balancer, the SDK
  reads the load balancer (and the child being changed) and waits within
  the pre-write bound until neither is busy. Past it: `ErrBusy`, nothing
  sent.
- A busy refusal of a free write means the server did not act. The SDK
  goes back to the pre-write wait and sends again, within the same bound.
  This is the only resend, and only a refusal that matches a busy message
  triggers it.
- A busy refusal of a resize returns `ErrBusy` at once: a paid write is
  sent once.
- In one process, writes to one load balancer hold a per-ID lock, so they
  queue instead of racing into refusals. Across processes the server's
  refusal and the resend above cover it.

## Waits

Per ADR 0002 rule 7, each write waits unless `NoWait` is set. The poll
uses normal reads, the injected clock and sleep, and `ctx`, and keeps
polling on a status it does not know.

| Write | Settled | Failed | Poll | Bound |
|-|-|-|-|-|
| Load balancer create | `CREATED` | `ERROR` | 10 s | 20 min |
| Resize | `CREATED` and the new `packageId` | `ERROR` | 10 s | 45 min |
| Load balancer delete | 404 | `ERROR` | 10 s | 15 min |
| Child create, update, members replace | Child `CREATED` and load balancer not busy | `ERROR` | 5 s | 10 min |
| Child delete | Child 404 and load balancer not busy | `ERROR` | 5 s | 10 min |
| Pre-write | Neither busy | none | 5 s | 10 min |

A 404 during a create wait keeps polling. `ERROR` returns the Output and
`ErrFailed`. The bound returns the Output and `ErrNotSettled`, whose
message says the write was accepted and must not be repeated (a create or
a resize) or can be rerun (the read-first writes). The bounds follow
Terraform's; the live checks record real times, and a bound changes only
by amending this table.

## Identifiers and retries

- Every operation checks each path ID with `core.CheckPathID` before any
  request, including the existing reads, which send any value today.
  `PackageID`, `SubnetID`, `DefaultPoolID`, `RedirectPoolID`, and the
  certificate IDs go in bodies but get the same check, since each names a
  resource.
- Creates are `POST` and are not resent after a 5xx or a network error
  (ADR 0002 rule 2). The error names the list to check, matching the name
  exactly: `list-load-balancers --name`, `list-listeners`, `list-pools`,
  or `list-policies`. Listener and pool names are unique in a load
  balancer, so a rerun with the same name is refused, not doubled.
  Whether load balancer names are unique is a live check; until then a
  rerun of a create after a 5xx can buy a second one, and the wiki says
  to list first.
- A create response without `uuid` is an `*APIError` that says the
  resource may exist and names the list.
- Updates and deletes keep the transport's retries. The resize `PUT` uses
  `Once`: after a 5xx, a network error, or a timeout, the error advises
  reading the load balancer (`GetLoadBalancer`) and comparing its package to
  the one requested before any rerun, since a rerun could send a second paid
  resize. The CLI never retries a write.

## Security

- Every write gets an adversarial review before its release (ADR 0002
  rule 9). The review checks: the quote runs before each paid write on
  the write's own Input and builder; no paid write is sent above
  `MaxPrice` or sent twice; `MaxPrice` NaN, infinite, or negative is
  refused; no create resend after a 5xx; the busy resend happens only
  after a busy refusal; read-merge never drops a member or a field the
  caller did not name; path ID checks on every call; the guards send
  nothing; `--yes` where the CLI table says; read-only refusal of every
  write, quotes excepted.
- Exposure. An `Internet` load balancer gets a public address, and a
  listener on it with `0.0.0.0/0` serves the whole internet. The SDK
  makes the caller name both (`Scheme` and `AllowedCIDRs` have no
  default); the CLI asks for `--yes` for each, per decisions 2 and 3.
  The wiki says an HTTP listener on an internet load balancer is
  cleartext.
- Spend. `--max-price` defaults to 0, and the wiki shows
  `quote-create-load-balancer` first and a billing budget with an alert
  before any paid create.
- Addresses, IDs, CIDRs, names, and certificate IDs are account data.
  Fixtures use `<id>`, `<name>`, `<ip>`, and `<cidr>`.

## CLI

| Command | Kind | `--yes` | Release |
|-|-|-|-|
| `loadbalancer quote-create-load-balancer`, `quote-resize-load-balancer` | Read | No | L1 |
| `loadbalancer create-load-balancer` | Write, paid | Unless `--scheme Internal` | L2 |
| `loadbalancer delete-load-balancer` | Write, destructive | Yes | L2 |
| `loadbalancer resize-load-balancer` | Write, paid | No | L3 |
| `loadbalancer create-pool`, `update-pool` | Write | No | L4 |
| `loadbalancer delete-pool` | Write, destructive | Yes | L4 |
| `loadbalancer add-pool-member`, `update-pool-member` | Write | No | L4 |
| `loadbalancer remove-pool-member` | Write, changes traffic | Yes | L4 |
| `loadbalancer create-listener`, `update-listener` | Write | Unless every `--allowed-cidrs` entry is inside a private range | L5 |
| `loadbalancer delete-listener` | Write, destructive | Yes | L5 |
| `loadbalancer create-policy`, `update-policy` | Write | No | L6 |
| `loadbalancer delete-policy` | Write, destructive | Yes | L6 |

- Paid writes take `--max-price <vnd>`; without it every order is
  refused, since a real quote is above 0. `NaN`, `Inf`, or a negative
  value exits 2 before any request.
- A [read-only](cli.md#read-only) profile refuses every write with exit 2
  before any request. The quotes are reads and run.
- `--allowed-cidrs` is a comma-separated list, as the API takes it. `--yes`
  is required unless every entry lies entirely inside a private range
  (`10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`, `100.64.0.0/10`); several
  prefixes that together cover a public address cannot avoid it.
- Nested fields (`InsertHeaders`, `CertificateIDs`, policy `Rules`) come
  through `--cli-input-json`.
- A deleted load balancer loses its address and its prepaid time, and a
  removed member stops taking traffic at once; neither comes back with one
  command (ADR 0002 rule 6).

## Errors

| Case | Result | CLI code and exit |
|-|-|-|
| Missing field, bad ID, CIDR, address, or port, bad `MaxPrice`, empty update, conflicting member, protocol and certificate mismatch, HTTP check fields on a TCP check | `ErrInvalidInput`, no request | `InvalidUsage`, 2 |
| Missing `--yes` | No request | `InvalidUsage`, 2 |
| Quote above `MaxPrice` | `ErrPriceAboveMax`, no write | `PriceAboveMax`, 1 |
| Unknown load balancer, child, or member | `NotFound` | `NotFound`, 4 |
| Pool used by a listener | `ErrInUse` | `ResourceInUse`, 1 |
| Busy past the pre-write bound; busy refusal of a resize | `ErrBusy` | `ResourceBusy`, 1 |
| `ERROR` after a write | `ErrFailed`, with Output | `WriteFailed`, 1 |
| Bound reached, or confirm read differs | `ErrNotSettled`, with Output | `NotSettled`, 1 |
| Quota, duplicate name or port, bad package, no credit | The server's `*APIError` | 1 |
| 5xx or network error on a create or a resize | The error; the message names the list or says to read the load balancer | 1 |

New sentinels in `loadbalancer`: `ErrPriceAboveMax`, `ErrBusy`,
`ErrInUse`, `ErrFailed`, and `ErrNotSettled`. `ErrCertificateInUse`
keeps its meaning. The CLI codes all exist; the CLI maps the new
sentinels to them. How the server refuses a paid create for lack of
credit is unknown ([decision 8](#owner-decisions)); until a check
records it, the error passes through as an `*APIError`.

## Releases

| Release | Content | Live checks |
|-|-|-|
| L1 | `pricing` `Action` and constants; `QuoteCreateLoadBalancer`, `QuoteResizeLoadBalancer`; `Pool.ProgressStatus`; path ID checks on the existing reads; CLI quotes and `pricing get-quote --action` | Now, free |
| L2 | `CreateLoadBalancer`, `DeleteLoadBalancer`, the price guard, waits, busy wait, sentinels; CLI commands | Credit |
| L3 | `ResizeLoadBalancer`; CLI command | Credit |
| L4 | Pool create, update, delete with health monitors; member add, update, remove; CLI commands | Credit |
| L5 | Listener create, update, delete (HTTP, HTTPS, TCP, UDP); CLI commands | Credit |
| L6 | Policy create, update, delete; CLI commands | Credit |

Each is numbered when it ships, in this order: L4 and L5 need L2's load
balancer, a listener's default pool needs L4, and policies need a Layer 7
listener. No release breaks callers; the existing reads only start
rejecting a malformed ID. `loadbalancer.go` is near 600 lines, so each
release adds its own files. `Services.md` and `CLI-LoadBalancer.md` gain
the writes, the price guard, the exposure warnings, and the retry
advice. A paid release merges its code when its unit tests pass, and is
tagged only after its live checks pass and the next day's bill matches
the quotes.

## Owner decisions

1. Release split. Options: six releases L1 to L6; fewer, larger releases
   that share paid runs. Recommend six: small releases, and decision 7
   keeps the paid runs few.
2. Internet load balancer guard. Options: `Scheme` required in the SDK
   and `--yes` in the CLI for `Internet`; default `Internal`; no guard
   beyond the price. Recommend the first: the caller names the exposure,
   and an agent cannot add a public address by leaving a flag out.
3. Open listener guard. Options: `AllowedCIDRs` required in the SDK and
   `--yes` in the CLI unless every entry lies inside a private range (see
   [CLI](#cli)); `--yes` only when an entry has a `/0` prefix; `--yes` on
   every listener create; none. Recommend the private-range check: unlike
   the `/0` check, several prefixes that together cover a public address
   cannot pass without it.
4. `MaxPrice` default. Options: 0, so every paid write needs
   `--max-price`; no default, a required flag. Recommend 0, as
   `create-log-project` does.
5. Resize retries. Options: `Once`, never resent; the transport's normal
   `PUT` retries. Recommend `Once`: a resize is paid, and a resend after a
   5xx could race a resize already in progress.
6. Load balancer delete with children. Options: delete with its
   listeners and pools behind `--yes`; refuse while any exist. Recommend
   delete with children if the live check shows the server cascades:
   they are free configuration, and the paid resource is the point of
   the `--yes`.
7. Live runs. Options: one load balancer created by the L2 run and kept
   for the L3 to L6 runs within its paid month, then deleted; one per
   release, created and deleted in its run. Recommend keeping one if the
   L2 run shows a delete refunds nothing, else one per release. Keeping
   one needs an exception to the live-data rule that a test creates its
   own parents.
8. Zero-credit probe. Options: send one `CreateLoadBalancer` now, with
   `MaxPrice` 400,000 and the smallest package, to record how the server
   refuses an unfunded order; wait for credit. Recommend wait: if the
   server accepts and bills later, the account owes money and holds a
   load balancer.
9. Busy resend. Options: after a busy refusal of a free write, wait and
   send again within the pre-write bound; return `ErrBusy` at once.
   Recommend resend: the server did not act, and callers otherwise loop.
10. Policies. Options: ship them in L6; defer. Recommend L6: three calls
    on one listener, and HTTPS host routing needs them.
11. Per-run budget caps. Recommend 400,000 VND for L2 (one small
    package, one month), 1,200,000 VND for L3 (the create plus one
    resize up and one down), and 0 for L4 to L6 under decision 7, else
    400,000 each. The L5 run's optional internet check adds 400,000. The
    test stops before any write whose quote would pass the run's cap.

## Open questions

- Billing: whether a create charges a whole month at once, whether a
  delete refunds, what a resize quote prices, and whether the public
  address costs extra (no quote line names one).
- How the server refuses an order without credit, and what state it
  leaves.
- `progressStatus` values, the failed status, and real times.
- The busy refusal's status and body, including `errorCode`.
- Whether `zoneId` must match the subnet's zone, and whether load
  balancer names are unique.
- Whether a Layer 7 package takes TCP and UDP listeners, and a Layer 4
  one HTTP.
- Whether the listener `PUT` clears a field it does not carry, and what
  the full listener read returns.
- Whether a load balancer delete cascades or refuses.
