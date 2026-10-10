# vServer Paid Writes: Checks

Status: Accepted (2026-09-28), with [vServer paid writes](vserver-paid-writes.md).

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
  quote and the write bodies from one Input with every field set and
  checks every quote key except `period` and `isPoc` appears in the write
  body with an equal value.
- The price guard, per paid write: `MaxPrice` 0 with a quote of 347800
  sends no write and wraps `vngcloud.ErrPriceAboveMax`, naming both
  amounts; a quote equal to `MaxPrice` sends the write; NaN, `+Inf`,
  `-Inf`, and a negative `MaxPrice` send nothing at all, not even the
  quote; a quote without `optimumPrice`, or with it null, sends no write;
  a quote error sends no write. `errors.Is(err,
  monitor.ErrPriceAboveMax)` holds for the same value.
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
- Waits: each row of the [wait table](vserver-paid-writes.md#waits) for
  settled, `ERROR`, a 404 then settled, unknown status, the bound,
  `NoWait`, poll spacing, a cancelled context, a resize that reads
  `ACTIVE` with the old flavor first, and a reboot that reads `ACTIVE`
  before 10 s.
- Statuses 200, 202, 204, 400, 404, 409, and 5xx for every write; a
  create `2xx` without `data.uuid` fails and names the list.
- Path ID rejection for `..`, `.`, `/`, `?`, and empty on every ID, in a
  path or a body, before the quote.
- User data: the create is `Sensitive`; `--debug`, stdout, stderr, the
  error, and the quote body never hold the fixture's user data; the CLI
  refuses `UserData` in `--cli-input-json` and a file over 64 KiB.
- CLI golden tests for every command; `--yes` refusals for each command
  the [CLI table](vserver-paid-writes.md#cli) marks; `--max-price` absent
  gives `PriceAboveMax` after one quote and no write; repeatable
  `--security-group-id`; read-only refuses writes and runs quotes.

## Free checks

These run on the test account now, with no credit, and gate P1. They
follow [live data](../../instructions/live-data.md) and log only statuses,
counts, field names, types, and prices.

1. `ListFlavorZones` with and without `ZoneID`; `ListFlavors` on one
   flavor zone: raw rows against the models, `remainingVms` and
   `isSoldOut` types.
2. `GetDefaultVolumeType` with `ZoneID` set to the enabled zone, and
   without it: 200 and 404.
3. `ListVolumesByServer` on a malformed ID (refused before any request)
   and on a well-formed unknown ID: status and shape.
4. `QuoteCreateServer` for `s2-general-1x2`, Ubuntu 24.04, 20 GB SSD root:
   `OptimumPrice` 347800 at the time of writing, and the lines `INSTANCE
   TYPE` and `ROOT DISK`. With a 10 GB data disk: a `DATA DISK` line.
5. `QuoteCreateVolume` for 10 GB SSD: 32000 at the time of writing.
6. `CreateServer` and `CreateVolume` through the SDK with `MaxPrice` 0:
   exactly one quote request, then `ErrPriceAboveMax`, and no `POST` to
   `/servers` or `/volumes` (the test counts requests with a capturing
   transport).

Prices may change; the checks assert a positive price and log it, and the
design's [price table](vserver-paid-writes-api.md#prices) is updated when
it moves.

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
  resize first and stops, sending no write, when the sum exceeds the cap.
- Each paid write passes its own quote as `MaxPrice`, so a price change
  between the plan and the order stops the run.
- Before and after, the test writes `GetBalances` and
  `GetCurrentPeriodCost` to the ignored output directory and logs only
  whether the drop stayed within the cap.

| Run | Creates | Worst case if nothing refunds | Cap |
|-|-|-|-|
| L1 | One 10 GB SSD volume | 32,000 | 50,000 |
| L2 | One `s2-general-1x2` server with a 20 GB root, one 10 GB volume | 379,800 | 400,000 |
| L3 | L2's resources, then the server resized to `s2-general-2x4`, the volume to 20 GB, and the root to 30 GB | 1,011,387 VND of resize quotes plus L2's 379,800, since a resize quote prices the whole new configuration | 1,100,000 |

Deletes refund to the minute, so each run costs a few hundred VND at
most; all runs together cost about 120 VND
([billing model](vserver-paid-writes-api.md#billing-model)).

Results (2026-10-09, `hcm-3`, all resources deleted, every run passed):

- L1: the 10 GB SSD volume quoted 32,000 VND; create settled in 15 s at
  `AVAILABLE`, delete in 12 s, and the balance returned to its start.
- L2: `s2-general-1x2` with a 20 GB root quoted 347,800 VND; create 42 s to
  `ACTIVE` (1m13s on the first run), stop 25 s, start 15 s, reboot 20 s,
  rename at once, delete 20 s with the boot volume.
- Attach: 8 s to `IN-USE`; detach 6 s to `AVAILABLE`. The three detach
  guards refused as designed. Both `PUT`s need an empty JSON body.
- L3: the data volume grew 10 to 20 GB and settled `IN-USE`; the server
  resized to the larger flavor and ended `ACTIVE`; the root grew 20 to
  30 GB. The server resize quoted about 315,800 VND. The first cap, 800,000
  VND, stopped the run before the root resize; 1,100,000 VND covers the
  quotes.
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
6. Clean up as below.

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
owner is told the same day, because each one costs money.

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
write.
