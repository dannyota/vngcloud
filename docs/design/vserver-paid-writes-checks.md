# vServer Paid Writes: Checks

Status follows [vServer paid writes](vserver-paid-writes.md).

The tests, free checks, live runs, and security review for
[vServer paid writes](vserver-paid-writes.md). Terms and sentinels are
defined there.

## Unit tests

Unit tests use `httptest` and an injected clock. No unit test reaches the
real API.

- Sanitized raw fixtures and decode tests in `testdata/compute/` and
  `testdata/volume/` for the flavor zone and flavor lists, the default
  volume type, each create response, and a server and volume in every
  status of the [status table](vserver-paid-writes-api.md#statuses). Until
  a live run captures a create response, its fixture is built from the
  reference and marked so in the test name.
- Quote bodies: each quote sends only the priced keys in
  [quote requests](vserver-paid-writes-api.md#quote-requests) as
  `resourceInfo`, plus `period` 1 and `isPoc` false, and never `userData`;
  `action` is `create` or `resize`; an empty
  `pricing.GetQuoteInput.Action` sends `create`. The drift test builds the
  quote and the write bodies from one Input with every field set, then
  clears `SSHKeyID` for user data and checks every quote key except
  `period` and `isPoc` appears in the write body with an equal value.
- The price guard, per paid write: `MaxPrice` 0 with a quote of 347800
  sends no write and wraps `vngcloud.ErrPriceAboveMax`, naming both
  amounts; a quote equal to `MaxPrice` sends the write; NaN, `+Inf`,
  `-Inf`, and a negative `MaxPrice` send nothing at all, not even the
  quote; a quote without `optimumPrice`, or with it null, sends no write;
  a quote error sends no write. `errors.Is(err,
  monitor.ErrPriceAboveMax)` holds for the same value.
- Signed-reader matrix: use synthetic `optimumPrice: -180797` for exact
  `resize`, `create`, empty, `Resize`, ` resize`, and an unknown action.
  Only exact `resize` succeeds; empty sends `create`, other actions keep
  their wire value. Run against server, volume, load balancer, and an
  arbitrary resource string. Refusals return nil Output and satisfy
  `errors.As` for `*vngcloud.APIError`. A success says nothing about
  whether the gateway supports the synthetic resource/action pair.
- Through `pricing.GetQuote` test missing/null prices, empty/error
  envelopes, wrong types, invalid JSON, bare NaN and infinities, and
  positive/negative overflow. All fail with nil Output. Test NaN and both
  infinities directly at the validator too. Both signed zeros decode;
  tiny and maximum finite negative numbers preserve their values only
  for resize. Test the body bound and cancellation in
  [threats](#resize-credit-threats).
- Service isolation: `QuoteResizeServer`, `QuoteResizeVolume`, and
  `QuoteResizeLoadBalancer` return signed values. Volume quote tests
  supply a valid current volume and larger target. Negative create quotes
  fail through `pricing.GetQuote`; `CreateServer`/`CreateVolume` send no
  write. `ResizeVolume` refuses a negative price with `ErrUnpriced`
  naming the amount. Preserve the vLB private guard's existing negative
  resize acceptance and nonpositive create refusal. Use `errors.Is` for
  sentinels. Do not change the shared positive-only guard into a signed one.
- Proposed server write tests require decision 20. With a credit,
  `MaxPrice` 0 and 1000000 send one PUT; invalid `MaxPrice` values send
  no request. Both signed zeros wrap `ErrUnpriced` and send no write.
  Positive equality passes, an excess wraps `ErrPriceAboveMax`. Missing,
  null, malformed, or nonfinite prices send no PUT. Assert signed
  `MonthlyPrice` on success, `NoWait`, failed wait, cancellation after
  acceptance, and ambiguous send; assert error identities and `GetServer`
  recovery advice. A 202 ignores the body and confirms by reading;
  401/400/404/409 return API errors, 500/502 and connection loss remain
  uncertain. Exercise 429, 401, 307/308, failed dial, and 5xx with no resend.
- Raw evidence is required before release: free check 7 supplies
  `testdata/pricing/GetQuoteServerResizeCredit.json`. Decode that raw
  body through `pricing.GetQuote` and `QuoteResizeServer`. Before capture,
  use inline synthetic bodies or a separate `Synthetic` fixture and test
  suffix; never label a synthetic body as captured. Synthetic tests cover
  policy but cannot satisfy the raw-fixture gate. Assert all modeled raw
  fields without inventing line-item signs or discount relationships.
- CLI goldens use the captured quote for JSON, table, and text and retain
  its minus sign. Generic `pricing get-quote --action resize` succeeds;
  create with that body exits 1, `RequestFailed`. Quote commands run in
  read-only mode. The proposed resize write requires `--yes`, defaults
  `--max-price` to 0, and exits 0 only on successful SDK completion;
  read-only or missing consent exits 2 before any request. Golden tests
  may use explicitly synthetic inputs before check 7; release decode
  evidence must use the captured body.
- No resend: a create answered 502, 503, and a dropped connection is sent
  once; a resize, start, stop, and reboot answered 502 or 429 are sent
  once; rename, attach, detach, and deletes keep the transport's retries.
- Guards send nothing: duplicate exact name (and a substring match that is
  not exact still creates), start on `ACTIVE`, stop on `STOPPED` (both
  `Changed` false), start on `CREATING`, reboot on `STOPPED`, resize to
  the same flavor, shrink and equal size, delete of an `IN-USE` volume,
  attach already done, detach of the boot volume, detach from an `ACTIVE`
  server without `AllowRunning`, detach of a volume not on that server.
- Create bodies: every field in the create table and nothing from the
  [non-goals](vserver-paid-writes.md#non-goals), `encryptionVolume` false
  with no encryption field set,
  `isEnableAutoRenew` false by default, `userData` base64 with
  `userDataBase64Encoded` true, half a data disk refused, an empty
  `SecurityGroupIDs` refused. Resize volume sends the read type as
  `newVolumeTypeId`. Delete server sends `deleteAllVolume` as set.
- Waits: each row of the [wait table](vserver-paid-writes-api.md#waits) for
  settled, `ERROR`, a 404 then settled, unknown status, the bound,
  `NoWait`, poll spacing, a cancelled context, a resize that reads
  `ACTIVE` with the old flavor first, and a reboot that reads `ACTIVE`
  before 10 s.
- Statuses 200, 202, 204, 400, 404, 409, and 5xx for every write; a
  create `2xx` without `data.uuid` fails and names the list.
- Path ID rejection for `..`, `.`, `/`, `?`, and empty on every ID, in a
  path or a body, before the quote. An empty `SSHKeyID` is allowed when
  `UserData` installs keys; both login fields or neither send no request.
- User data: the create is `Sensitive`; `--debug`, stdout, stderr, the
  error, and the quote body never hold the fixture's user data; the CLI
  refuses `UserData` in `--cli-input-json` and a file over 64 KiB.
- CLI golden tests for every command; `--yes` refusals for each command
  the [CLI table](vserver-paid-writes.md#cli) marks; `--max-price` absent
  gives `PriceAboveMax` for a positive quote and no write; repeatable
  `--security-group-id`; read-only refuses writes and runs quotes.

## Free checks

Only the manager may run these after explicit live-read authorization.
Checks 1 to 6 cover the baseline; check 7 gates signed-credit release
evidence and needs an existing server. Follow
[live data](../../instructions/live-data.md); log only statuses, counts,
field names, types, and prices. This document authorizes no run.

1. `ListFlavorZones` with and without `ZoneID`; `ListFlavors` on one
   flavor zone: raw rows against the models, `remainingVms` and
   `isSoldOut` types.
2. `GetDefaultVolumeType` with `ZoneID` set to the enabled zone, and
   without it: 200 and 404.
3. `ListVolumesByServer` on a malformed ID (refused before any request)
   and on a well-formed unknown ID: status and shape.
4. `QuoteCreateServer` for `s2-general-1x2`, Ubuntu 24.04, 20 GB SSD root:
   `OptimumPrice` 347800 in recorded evidence, and the lines `INSTANCE
   TYPE` and `ROOT DISK`. With a 10 GB data disk: a `DATA DISK` line.
5. `QuoteCreateVolume` for 10 GB SSD: 32000 in recorded evidence.
6. `CreateServer` and `CreateVolume` through the SDK with `MaxPrice` 0:
   exactly one quote request, then `ErrPriceAboveMax`, and no `POST` to
   `/servers` or `/volumes` (the test counts requests with a capturing
   transport).
7. Resize credit probe. Prepare a disposable, git-ignored helper in the
   manager's authorized live workspace, not a worker worktree. Obtain
   account, region, project, server ID, and target flavor ID privately
   from the run authorization. No defaults from example IDs. Stop if the
   manager has no existing prepaid server or approved read access; never
   create, start, stop, resize, or delete a resource to enable this probe.
   - Build one `vngcloud.Config` using the manager's normal credential
     loader, `WithProjectID`, `WithRetry(0, 0)`, and
     `WithResponseCapture`. Share it with `compute.New`. Use a 60-second
     context. Keep normal HTTPS endpoints and TLS verification. No debug
     logging or credentials, IDs, raw outputs, or raw errors on stdout.
   - `GetServer` verifies `ACTIVE` or `STOPPED` and the current flavor.
     `ListFlavorZones` for its zone and `ListFlavors` for that flavor zone
     verify target membership in the same zone and group. Match the
     current flavor ID to its list entry; compare CPU/memory and reject
     a sold-out target. These lists have no prices: the manager supplies
     the cheaper target using public price evidence. Use
     `s2-general-2x4` to `s2-general-1x2` only if those entries actually
     exist. Reject equal IDs, a different zone, or uncertain selection.
     These reads do not prove image compatibility or a credit; the quote
     may refuse the target, which leaves the evidence gate open.
   - Call `QuoteResizeServer(ctx, &compute.ResizeServerInput{ServerID:
     serverID, FlavorID: targetID})` once. No write method is called.
     Expect `POST v1/price`, `resourceType: server`, `action: resize`,
     and `resourceInfo` containing exactly `serverId`, `flavorId`,
     `period: 1`, `isPoc: false`. Normal authentication may refresh once
     after 401; only a final HTTP 200 negative response qualifies.
   - The hook selects `Operation == "pricing.GetQuote"`, `Method ==
     "POST"`, and `StatusCode == 200`; copy `Body` before returning. It
     runs before price validation and receives credential-redacted raw
     JSON, not decoded SDK output. Verify the local helper's request
     allowlist permits only the named reads, normal auth/discovery, and
     this pricing POST. Fail closed on any other request, especially a
     PUT, DELETE, or create POST.
   - Save the captured JSON under a `body` field in
     `examples/basic/output/raw/pricing/GetQuoteServerResizeCredit.json`,
     mode 0600 in a private directory. A numeric negative `optimumPrice`
     is required. SDK rejection of that value is expected before the
     fix; missing capture, 4xx, nonnegative price, or capture failure
     leaves the gate open. Do not retry with another resource or write.
   - Extract the raw `body` into the fixture path above only after
     [sanitation](../../instructions/live-data.md#sanitizing-fixtures).
     Replace all sensitive values, including nested descriptions, IDs,
     URLs, and names. Preserve keys, types, nulls, signs, and numeric
     prices; never reconstruct fields from SDK output. Record sanitized
     provenance beside the decode test: operation, HTTP status, region,
     request shape, and replacement rules, without account identifiers.
   - Compare raw fields with the SDK model, add the decode tests, and
     record only verified field signs in the API evidence section.
     Unknown fields require assessment, not silent deletion. Remove the
     helper afterward. Keep captures private; no resource cleanup is
     needed or authorized by a quote-only run.

Prices may change; checks assert positive create prices and a negative credit
quote without asserting a fixed amount. The
[price table](vserver-paid-writes-api.md#prices) records samples.

## Refusal probe

Declined ([decision 17](vserver-paid-writes.md#owner-decisions)): no
paid write is sent before the account has credit. The steps stay here for
a later owner decision. On the zero-balance test account in `hcm-3`:

1. Read `GetBalances`; stop unless every balance is 0 or null.
2. `CreateVolume` named `vngcloud-live-<8 hex>`, 1 GB SSD, zone
   `HCM03-1C`, `MaxPrice` 3200, `NoWait`.
3. Expected: a `4xx` refusal. Record the status and message, and whether
   any volume with that name appears in `ListVolumes`.
4. If a volume appears, delete it at once and tell the owner; the refusal
   premise is wrong and every live run waits for a decision.

The result sets the billing-refusal row in
[errors](vserver-paid-writes.md#errors) and its fixture.

## Live runs

These need credit and the owner's approval per run naming the account,
region `hcm-3`, and the resources below. The manager runs each through its
live write test, gated by `VNGCLOUD_LIVE_WRITE=1` and the run's own
variable, per [live data](../../instructions/live-data.md).

### Budget

- The owner adds at least 1,000,000 VND of credit before L1.
- Each test takes `VNGCLOUD_LIVE_MAX_VND`, the run's cap, and fails before
  any write when it is unset. It quotes everything it will create or
  resize first and stops before a write when the sum of positive quotes
  exceeds the cap. Never subtract credits from that sum.
- Each write passes `max(quote, 0)` as `MaxPrice`. Its fresh internal
  quote can stop a price increase, but the final quote-to-send race remains.
  A quote cap is not an enforced billing cap.
- Before and after, the test writes `GetBalances` and
  `GetCurrentPeriodCost` to the ignored output directory and logs only
  whether the drop stayed within the cap.

| Run | Creates | Worst case if nothing refunds | Cap |
|-|-|-|-|
| L1 | One 10 GB SSD volume | 32,000 | 50,000 |
| L2 | One `s2-general-1x2` server with a 20 GB root, one 10 GB volume | 379,800 | 400,000 |
| L3 | L2's resources, then the server resized to `s2-general-2x4`, the volume to 20 GB, and the root to 30 GB | 1,391,187 VND: resize quotes 1,011,387 plus create quotes 379,800 | 1,100,000 |

Refund timing and eligibility do not guarantee a run's net cost. Keep
positive quotes within the approved cap without subtracting expected
credits; stop if billed cost exceeds it.
[Billing evidence](vserver-paid-writes-api.md#billing-model) does not
authorize relying on a refund to fund another write.

Recorded live evidence (`hcm-3`; cleanup was reported complete):

- L1: the 10 GB SSD volume quoted 32,000 VND; create settled in 15 s at
  `AVAILABLE`, delete in 12 s, and the balance returned to its start.
- L2: `s2-general-1x2` with a 20 GB root quoted 347,800 VND; create 42 s to
  `ACTIVE` (1m13s on a recorded run), stop 25 s, start 15 s, reboot 20 s,
  rename at once, delete 20 s with the boot volume.
- Attach: 8 s to `IN-USE`; detach 6 s to `AVAILABLE`. The three detach
  guards refused as designed. Both `PUT`s need an empty JSON body.
- L3: the data volume grew 10 to 20 GB and settled `IN-USE`; the server
  resized to the larger flavor and ended `ACTIVE`; the root grew 20 to
  30 GB. The server resize quoted about 315,800 VND. A cap of 800,000
  VND stopped the run before the root resize. The recorded 1,100,000
  VND cap conflicts with the table's combined create and resize total;
  reconcile the evidence and approved budget before another L3 run.
  Never raise the cap or subtract an expected credit automatically.
- The VPC quota (2) was full, so the runs set `VNGCLOUD_LIVE_NETWORK_VPC_ID`
  and created only their subnet in that VPC.

### Parents

The test creates its own security group (no ingress rule), an imported
throwaway RSA key, and a `/24` subnet in the enabled zone, in a VPC it
creates when the VPC quota allows. When it does not, the owner names an
existing VPC for the run, and the test creates only its subnet there.
Every name starts with `vngcloud-live-`.

### L1 volumes (gates P2)

1. Quote a 10 GB SSD volume; record `OptimumPrice`.
2. `CreateVolume` with `MaxPrice` equal to the quote: status 202, the raw
   response, `data.uuid`, statuses and time to `AVAILABLE`.
3. Balance after the create: the drop, against the quote (logged as a
   boolean match and saved raw).
4. `CreateVolume` again with the same name: refused by the SDK. Send the
   raw `POST` with the same name once more only if the owner approved a
   second volume: does the server allow duplicate names?
5. `DeleteVolume`: status 202, statuses to 404 or `DELETED`, time. Repeat
   delete: status. Balance after: the refund, if any.
6. `ListVolumes` by the name: absent.

### L2 servers, attach, and detach (gates P3 and P4)

1. Quote the server; `CreateServer` with `MaxPrice` equal to the quote:
   status 202, the raw response, statuses and time to `ACTIVE`, the
   `BootVolumeID`, and `ListVolumesByServer` rows.
2. `StopServer`: `TURNING-OFF` to `STOPPED`, time. Stop again:
   `Changed` false, no request.
3. `StartServer`: to `ACTIVE`, time. `RebootServer`: whether `REBOOTING`
   shows, and time.
4. `RenameServer` to a new `vngcloud-live-` name: status and response.
5. Create a 10 GB volume as in L1. `AttachVolume`: `ATTACHING` to
   `IN-USE`, time, the volume's `serverId` and `serverIdList`. Attach
   again: `Changed` false.
6. `DetachVolume` without `AllowRunning`: `ErrServerRunning`, no request.
   `DetachVolume` of the boot volume: `ErrBootVolume`. `DeleteVolume` on
   the attached volume: `ErrVolumeInUse`.
7. Stop the server; `DetachVolume`: `DETACHING` to `AVAILABLE`, time.
   Attach it again for the next step.
8. `DeleteServer` with `DeleteVolumes` false: status 202, statuses to 404
   or `DELETED`, time; which volumes remain (`KeptVolumeIDs`), the boot
   volume in particular, and their status.
9. Delete every kept volume. Balance after: the refunds, if any.

### L3 resizes (gates P5)

On a server and a 10 GB volume created as in L2:

1. `QuoteResizeVolume` to 20 GB: `OptimumPrice` and its lines; compare
   with the 20 GB create quote and the difference, to learn what a resize
   quote prices.
2. `ResizeVolume` to 20 GB with `MaxPrice` equal to the quote, while
   attached: statuses and time, the size read back. `ResizeVolume` to
   10 GB: refused by the SDK.
3. `QuoteResizeServer` to `s2-general-2x4`, as in step 1. `ResizeServer`
   with that `MaxPrice`: `CHANGING-FLAVOR` and `VERIFYING-FLAVOR`, the
   status it ends in (`ACTIVE` or `STOPPED`), time, the flavor read back.
4. `ResizeVolume` on the boot volume from 20 GB to 30 GB: status and
   time.
5. Balance after each resize: the charge, against its quote.
6. Downsize, only after decision 20 and approval for this step: run the
   [credit probe](#free-checks) from `s2-general-2x4` back to
   `s2-general-1x2`, then `ResizeServer` to it with `MaxPrice` 0: the
   negative quote, the negative `MonthlyPrice`, the statuses, the flavor
   read back, and the balance after, against the credit. The run's spent
   total never subtracts a credit.
7. Clean up as below.

### Cleanup

The test first deletes leftovers whose names start with `vngcloud-live-`,
in this order: detach volumes from test servers (stopping each first),
delete servers with `DeleteVolumes` false, delete volumes, then security
groups, keys, subnets, and a VPC it created. It registers `t.Cleanup` as
soon as each ID is known and deletes in the same order with its own
context, then asserts that no `vngcloud-live-` server or volume remains,
logging only counts. If a create fails, it lists by exact name and adopts
a match for cleanup. It never touches a resource without the prefix.
Leftovers it cannot delete are named in the failure message, and the
owner is told promptly, because each one costs money.

The next day, `billing list-cost-resources` and `get-balances` show each
run's real cost. A cost above the run's cap stops the next release until
the owner decides.

## Security review

Each release P2 to P5 gets an adversarial review before its tag. It
checks: no paid write without a quote at or below `MaxPrice`; default 0;
NaN, infinite, and negative `MaxPrice` refused; the quote and the write
built from one body; `userData` absent from the quote, captures, logs,
errors, and argv; no create resend after any failure; `Once` on resize,
start, stop, and reboot; every guard sends nothing; no `attachFloating`,
password, or default security group; auto-renew off by default;
`--yes` on every command the CLI table marks; `--delete-volumes` required
to delete volumes with a server; path ID checks on every ID; read-only
refusal of every write; and the live tests' cap check runs before any
write. The resize credit release adds: a negative quote is kept only when
`Action` is `resize`; a create's negative quote stays an `*APIError`; the
credit's size drives no decision; and `ResizeVolume` refuses a quote at
or below 0.

### Resize credit threats

These are design obligations, not an independent review verdict. Proposed
paths are unimplemented. Existing protections must survive the change.

- Leaked tokens, high impact: an echoed credential can escape through an
  HTTP/decode error, debug output, capture, or quote property text. Existing
  `internal/transport/transport.go:593` redacts captured bodies and skips
  sensitive captures; `:612` logs method/path/status/time only;
  `internal/transport/body.go:19` sanitizes decode errors. The numeric
  refusal at `pricing/pricing.go:172` contains only the price. Invariant:
  add no credential-bearing output or raw-response error. Check echoed
  synthetic tokens in success metadata, errors, debug, and captures;
  retain nested-escape limits from security instructions. Typed success
  metadata is not proved redacted by error/capture tests. Any leak found
  needs a scoped fix before release. Sanitize account data separately.
- Hostile peer, high impact: `pricing/pricing.go:121` sets no `MaxBody`,
  so `internal/transport/transport.go:560` reads without a size bound.
  Invariant: bound the response before decoding or capture and return no
  partial quote. Implement the proposed
  [body limit](vserver-paid-writes-api.md#quote-decoding-and-bounds).
  Check cap and cap+1, HTTP error identity, no partial capture or write,
  malformed types, finite extremes, and cancellation. A forged finite
  credit can still pass
  the proposed guard: TLS authenticates the peer, not billing accuracy.
- Redirect or endpoint tampering, high impact: credentials attach at
  `internal/transport/transport.go:470`; `redirect.go:11` checks host and
  scheme before and after caller hooks; `transport.go:433` refuses all
  resize redirects under `Once`. Defaults use HTTPS at
  `internal/endpoints/endpoints.go:98`. Caller HTTP clients/transports
  and endpoint overrides remain trusted configuration
  (`internal/core/client.go:376`, `client_options.go:123`), not a host
  allowlist. Invariant: no new endpoint or TLS bypass; no redirect may
  forward credentials cross-origin. Check foreign host, downgrade,
  hook mutation, and 307/308 without a second send. Do not treat a
  malicious initial endpoint or caller transport as protected by redirects.
- Local tampering, high impact: CLI configuration selects the account and
  consent policy before SDK calls (`internal/cli/op.go:419`); credential
  loading checks private file modes (`internal/core/load.go:163`). Cache keys
  bind profile, user, and auth endpoints (`internal/tokencache/cache.go:30`);
  `:169` rejects stale/empty tokens, `:199` writes mode 0600, and `:242`
  requires a mode 0700 directory on Unix. Invariant: reuse only the
  selected identity's token and preserve credential-file permissions.
  A same-user attacker can edit a profile or token; file modes are not
  cryptographic integrity. Check malformed/wrong-profile/stale caches,
  insecure directories, and credential writes at 0600. The credit path
  adds no file access; quote probes must not copy profiles into fixtures.
- Concurrent or repeated writes, high impact: the concrete sign-reversal
  race is in [API evidence](vserver-paid-writes-api.md#resize-credits).
  `compute/resize_server.go:120` reads before quoting; `:157` sets `Once`;
  neither locks a resource against another actor. Invariant: at most one
  resize per call, explicit CLI consent, no automatic retry or cleanup
  after uncertainty. Decision 20 gates the proposed negative guard.
  Check a deterministic peer changing the old flavor after the quote,
  unchanged target body, one send, and read-only recovery. This test
  exposes the residual risk; it must not assert a guaranteed zero charge.
  Check partial signed Output and context/error identity after send;
  wait remains 5-second polls within 15 minutes, honoring cancellation.
  Live cleanup still requires the run's existing explicit authority.
