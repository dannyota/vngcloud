# vLB Writes: Checks

Status: Accepted (2026-09-28), with [vLB writes](lb-writes.md).

The unit tests, free probes, live checks, and security review for
[vLB writes](lb-writes.md). Terms and sentinels are defined there.

## Unit tests

Unit tests use `httptest` and an injected clock and sleep. They never
reach the real API.

- Sanitized fixtures and decode tests for each create response (the flat
  `uuid`), the resize response, and reads with `progressStatus` on a
  load balancer, listener, pool, member, and policy, in
  `testdata/loadbalancer/`. Until a live capture exists, fixtures follow
  VNG Cloud's SDK shapes and say so in the test name; the live checks
  replace them.
- Quotes: the create quote body is `resourceType` `load-balancer`,
  `action` `create`, and `resourceInfo` with `packageId`, `zoneId`,
  `period` 1, `isPoc` false, and `isBuyMorePoc` false; the resize quote
  body has `action` `resize`, `packageId`, and `loadBalancerId`.
  `pricing.GetQuote` with no `Action` still sends `create`.
- Price guard, each for create and resize: a quote above `MaxPrice` sends
  no write and returns `ErrPriceAboveMax` naming both amounts; a quote
  equal to `MaxPrice` sends one write; `MaxPrice` NaN, +Inf, -Inf, or -1
  sends nothing, not even the quote; a quote with no price sends no
  write; the quote and the write read the same Input fields (one test
  changes `PackageID` and checks both bodies change).
- Send once: a 502 on the create `POST` and on the resize `PUT` is not
  resent; a failed dial on the resize is not resent either (`Once`).
- Request bodies: load balancer create sends `zoneId`, `autoScalable`
  false, `isPoc` false, and no `listener` or `pool`; listener create
  sends default timeouts for 0, the joined CIDRs, and certificate fields
  only for HTTPS; pool create sends default algorithm and monitor values,
  HTTP check fields only for HTTP and HTTPS, and `stickiness` only when
  set; policy bodies for each action.
- Read-merge: an update that sets one field resends every other field as
  read, for listener, pool (with its monitor), and policy; member add,
  update, and remove bodies hold every member read except the one
  removed, plus the one added or changed; add of a present member is
  `Changed` false with no `PUT`; a conflicting add is `ErrInvalidInput`;
  update or remove of an absent member is `NotFound`; a confirm read that
  differs is `ErrNotSettled`.
- Shape refusals with no request: CIDR with host bits, IPv6, or no
  prefix; empty `AllowedCIDRs`; member address that is not IPv4; port 0
  or 70000; `HTTPS` without `DefaultCertificateID`; a certificate field
  on `TCP`; an HTTP check field on a `TCP` check; empty update; empty
  `Scheme`, `ZoneID`, or `PackageID`.
- Guards, each sending no write: pool named by a listener's
  `DefaultPoolID`; resize to the current package (`Changed` false, no
  quote either); delete of a load balancer already `DELETING` waits
  without a `DELETE`. The server's `is used in listener` message at 400
  and 409 wraps `ErrInUse`.
- Busy: the pre-write wait sends after the load balancer leaves
  `UPDATING`; past the bound it is `ErrBusy` with no write; a busy
  refusal of a free write is followed by a wait and one more send; a busy
  refusal of a resize returns `ErrBusy` with no second `PUT`; a refusal
  that does not match a busy message is never resent; two writes to one
  load balancer in one process do not overlap.
- Waits, per row of the waits table: settled, `ERROR`, 404 then
  `CREATED` during a create, `CREATING-BILLING` kept pending, delete by
  404, the bound, `NoWait`, poll spacing, an unknown status, and a
  cancelled context.
- Statuses 200, 202, 400, 404, 409, 500, and 502; a create response
  without `uuid` fails with a message naming the list.
- Path ID rejection for `..`, `.`, `/`, `?`, and empty on every
  operation, reads included, and on the body IDs the design names.
- CLI golden tests for every command; `--yes` refusals for delete
  commands, `remove-pool-member`, `create-load-balancer --scheme
  Internet`, and a listener whose `--allowed-cidrs` includes `0.0.0.0/0`;
  no `--yes` needed for `--scheme Internal` or a `/24`; `--max-price`
  NaN refused; read-only refusal of every write with no request, and the
  quotes still run under read-only; the `PriceAboveMax`, `ResourceBusy`,
  and `ResourceInUse` codes.

## Free probes

These run now on the test account, read-only, through a throwaway
program or `make live`, following [live data](../../instructions/live-data.md).
Results go in [vLB writes: API](lb-writes-api.md) without account data.

Done on 2026-09-28:

1. `ListPackages` with no zone and per `hcm-3` zone: 11 packages, 8 in the
   Bangkok zone.
2. A create quote for every package in every zone, and with no zone,
   `period` 3, extra body fields, an unknown package, and no package.
3. A resize quote for a missing load balancer: 400.
4. `GetLoadBalancer`, `GetListener`, `GetPool`, and `ListPools` on a
   missing load balancer: 404 with its message.

To run before L1 merges:

5. The raw body of a 404 and a 400 from the vLB gateway: field names only,
   to learn whether it carries `errorCode`.
6. `ListLoadBalancers --name` with a name that exists nowhere: an empty
   page, not an error.
7. `make live` gains `QuoteCreateLoadBalancer` for the smallest package,
   logging only the price.

Not free, so not run: any `POST`, `PUT`, or `DELETE` on the vLB gateway,
including a create meant to be refused for lack of credit
([decision 8](lb-writes.md#owner-decisions)).

## Live checks

Each paid release's checks run after credit exists and before its tag.
The manager runs them through the release's live write test, with the
owner's approval naming the account, region, resources, and the run's
budget cap. Before the first paid run, the owner sets a billing budget
with an alert at the cap and confirms the balance covers it.

Every paid write in a live test goes through the SDK's price guard with
`MaxPrice` set to the lowest amount that passes: 400,000 VND for a small
package. The test also sums the quotes it accepted and stops before any
write that would pass the run's cap. Runs log only statuses, field names
and types, timings, and prices; never addresses, IDs, or names.

Names are `vngcloud-live-<8 hex>`. Each run creates its own VPC and `/24`
subnet in an enabled zone, per
[vServer network writes](vserver-network-writes-checks.md#live-checks),
and at most one VPC at a time.

### L1 quotes

No write. `QuoteCreateLoadBalancer` for `NLB_Small` and `ALB_Small` in
the zone the account uses: 400,000 VND each, one `LOAD BALANCER` line.
`QuoteResizeLoadBalancer` on a missing load balancer: 400.

### L2 load balancers (cap 400,000 VND)

1. `billing get-balances` and `get-current-period-cost` before the run.
2. Create `ALB_Small`, `Internal`, in the test subnet, `MaxPrice`
   400,000: status, response shape, every `progressStatus` seen and its
   time, the time to `CREATED`, the read shape (`zone` or `zoneId`,
   `loadBalancerSchema` value).
3. Balances right after the create: whether 400,000 left at once.
4. Create again with the same name and `MaxPrice` 0: refused by the guard
   with no request (checks the guard live, costs nothing).
5. Delete: status, `DELETING` time, 404 time. Whether the VPC then
   deletes normally.
6. Balances after the delete, and the next day's
   `list-cost-resources`: the line for the load balancer and any refund.
   This answers [decision 7](lb-writes.md#owner-decisions).

An `Internet` create is checked in the L5 run, which needs one to test a
listener's exposure; the L2 run keeps the address count at zero.

### L3 resize (cap 1,200,000 VND)

On an `ALB_Small` load balancer, reused under decision 7 or created by the
run:

1. `QuoteResizeLoadBalancer` to `ALB_Medium`: the price, and whether it
   is the difference for the rest of the month.
2. Resize with `MaxPrice` set to that quote: status, `UPDATING` time, the
   new `packageId` on read.
3. Resize to the same package: `Changed` false, no request.
4. Quote and resize back to `ALB_Small`: the price (0, negative, or a
   refund), and the balance after.
5. A resize sent while the load balancer is `UPDATING`, through a raw
   `PUT` in the test: the busy refusal's status and body.

### L4 pools and members (cap 0 or 400,000 VND)

1. Create a `TCP` pool with a `TCP` check and an `HTTP` pool with an
   HTTP/1.1 check and a domain: status, times, the load balancer's
   `progressStatus` meanwhile. Create the same name again: message.
2. Update each pool's algorithm only: every other field unchanged on
   read, the monitor included.
3. Add two members with addresses in the test subnet that no interface
   holds, then update one's weight, then remove one: the `PUT` bodies,
   the member read shape, and the time to settle. Add the same member
   again: no request.
4. Send a second write while the first is settling: the busy refusal,
   and the SDK's resend.
5. Delete a pool: 202, then 404.

### L5 listeners (cap 400,000 VND with a new Internet load balancer)

1. On an `Internal` Layer 7 load balancer: an `HTTP` listener with the L4
   pool as default and `AllowedCIDRs` `10.0.0.0/8`; a `TCP` and a `UDP`
   listener (accepted or refused on Layer 7); the same port twice.
2. Import a throwaway self-signed certificate, as
   [vLB certificates](lb-certificates.md#live-checks) does, and create an
   `HTTPS` listener with it: status and read fields. The certificate's
   `inUse` is then true, and `DeleteCertificate` refuses it.
3. Update the HTTP listener's `TimeoutClient` only: every other field
   unchanged, including `insertHeaders`, and whether the read shows any
   of `blockedCidrs`, `defaultAction`, `alpnProtocols`, or
   `tlsSecurityPolicy`.
4. `DeletePool` on the default pool: `ErrInUse`, no request.
5. Delete each listener, then the certificate.
6. With the owner's approval for an address: create an `Internet`
   `NLB_Small` (400,000 VND), add a `TCP` listener with `AllowedCIDRs`
   the runner's own `/32`, and confirm the listener's read. Delete it at
   once. Skipped unless the run's approval names it.

### L6 policies (cap 0 or 400,000 VND)

On the Layer 7 load balancer with an HTTP listener and two pools:

1. Create a `REDIRECT_TO_POOL` policy with a `PATH` `STARTS_WITH` rule,
   and a `REDIRECT_TO_URL` policy with code 301: status, read shape,
   `position`.
2. Update the first policy's rules only: the rule list replaced, the
   rest unchanged.
3. `DeletePool` on the redirect pool: the server's refusal message.
4. Delete both policies.

### Cleanup

The live write test first deletes leftovers whose names start with
`vngcloud-live-`, children before parents: policies, listeners, members,
pools, load balancers (waiting for 404), certificates, subnets, then
VPCs. It registers `t.Cleanup` as soon as each ID is known and deletes
with its own context. A load balancer is the paid resource, so its
cleanup runs first among parents and is asserted: the test fails, naming
the leftover, when a `vngcloud-live-` load balancer still reads after the
delete bound. If a create fails after the `POST`, it lists by exact name
and deletes a match. It never touches a resource without the prefix.
Under decision 7 the kept load balancer is named `vngcloud-live-keep-<8
hex>`, and only the last run, or a run past its paid month, deletes it.

A VPC that held a load balancer may refuse its delete for minutes, as
after a subnet delete; the retry of
[vServer network writes](vserver-network-writes-checks.md#cleanup)
applies. The live target's timeout is at least 60 minutes.

## Security review

- One adversarial review per release, before its tag, covering the
  checks in [vLB writes](lb-writes.md#security).
- The reviewer confirms by test, not by reading: a quote above
  `MaxPrice` sends no write; no path sends a paid write twice; the busy
  resend never fires for a resize or for a refusal that is not a busy
  message; `--yes` gates `--scheme Internet` and a `/0` CIDR.
- Fixtures replace addresses, CIDRs, names, and IDs; the price fixtures
  keep the public package prices.
