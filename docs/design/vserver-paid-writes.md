# vServer Paid Writes Design

Status: Accepted, including decisions 19 and 20; the refusal probe is
declined. Floating IP decisions are accepted; implementation stays gated
under [their design](vserver-floating-ips.md).

This design covers the vServer writes that cost money: servers in
`compute` and volumes in `volume`. Every paid create and resize quotes
first and compares the quote with `MaxPrice`, which defaults to 0.
The API does not enforce that cap on the eventual charge. Base operations
are implemented. Live verification needs separate account, resource,
permission, and cost approval.

It follows [vServer free writes](vserver-writes.md) for the gateway,
create retries, and delete guards,
[ADR 0002](../adr/0002-write-api-conventions.md) for every write, and
[ADR 0003](../adr/0003-toggle-writes.md) for writes that read first and
send once. The price guard copies
[vMonitor log projects](monitor-alerts.md#log-projects). Calls, bodies,
statuses, the billing model, the quoted prices, and the open questions are
in [vServer paid writes: API](vserver-paid-writes-api.md). Tests, live
runs, and the security review are in
[vServer paid writes: checks](vserver-paid-writes-checks.md).

## Non-goals

- Floating IPs: [vServer floating IPs](vserver-floating-ips.md) owns them.
- Password login and Windows images: `userName`, `userPassword`,
  `osLicence`, and `expirePassword`.
- Multi-attach volumes, volume type change, and host groups. Encryption
  is in [encrypted volumes](encrypted-volumes.md).
- Volume and server snapshots, backups, and restores. The snapshot service
  is activated through a console billing order and a payment page.
- Changing a server's security groups, network interfaces, or tags.
- Renew, recover, and auto-renew changes after create.
- Server groups on create beyond passing an ID; server group writes are in
  [vServer network writes](vserver-network-writes.md).
- Writes to resources that OpenTofu manages. The wiki warns about drift.

## Cost

The test account is prepaid. Per the
[billing model](vserver-paid-writes-api.md#billing-model), a create pays
the period price from the wallet; refund eligibility follows the product
terms. Direct API billing details retain their evidence gates. The smallest
server this design can create (1 vCPU, 2 GB, 20 GB SSD root) quotes
347,800 VND a month; a 10 GB
SSD volume quotes 32,000 ([prices](vserver-paid-writes-api.md#prices)).
A zero-balance refusal is unverified. The SDK relies on its quote guard,
not on the wallet refusing an order.

## Quote before a paid write

Every write that can charge the account has a quote operation that takes
the write's own Input (ADR 0002 rule 8):

| Write | Quote | `resourceType`, `action` |
|-|-|-|
| `compute.CreateServer` | `compute.QuoteCreateServer` | `server`, `create` |
| `compute.ResizeServer` | `compute.QuoteResizeServer` | `server`, `resize` |
| `volume.CreateVolume` | `volume.QuoteCreateVolume` | `volume`, `create` |
| `volume.ResizeVolume` | `volume.QuoteResizeVolume` | `volume`, `resize` |

- One quote builder per write makes `resourceInfo` from the write's
  Input: only the priced keys in
  [quote requests](vserver-paid-writes-api.md#quote-requests), plus
  `period` 1 and `isPoc` false. It never holds `userData` or `osLicence`.
  The create's price guard calls the same builder. A quote requires only
  priced fields; see
  [CLI usability](cli-usability.md#quotes-ask-only-for-priced-values).
- The quote's Output is `pricing.GetQuoteOutput`. For a create,
  `OptimumPrice` is VND a month. For a resize it reports the signed quote
  for the remaining period. A negative value denotes an expected credit,
  not a confirmed refund
  ([resize credits](vserver-paid-writes-resize.md#resize-credits)).
- `pricing.GetQuoteInput.Action` defaults to `create` when empty, preserving
  existing callers. One internal quote helper serves `pricing`, `compute`,
  and `volume`.

The paid write runs these steps, in order, and stops at the first failure:

1. Check the Input shape and `MaxPrice`: NaN, an infinity, or a negative
   value is `ErrInvalidInput`, since each would disable the comparison.
2. Run the write's pre-reads and guards (below).
3. Quote. A quote error stops the write; a response without
   `optimumPrice` is an `*APIError`, never a price of 0. A negative price
   is an `*APIError` unless the action is exactly `resize`.
4. When `OptimumPrice` is above `MaxPrice`, return an error wrapping
   `vngcloud.ErrPriceAboveMax` that names both amounts. A quote of 0 is
   `ErrUnpriced`, as is a volume resize quote below 0. Nothing is sent.
5. Send the write once. A create is a `POST` and is not retried after a
   5xx or a network error (ADR 0002 rule 2). A resize is sent with `Once`
   (ADR 0003 rule 3): a resent resize could pay twice.
6. Wait, unless `NoWait` is set.

`MaxPrice` is VND for the quoted operation: monthly for creates, the
quoted adjustment for resizes. `MonthlyPrice` retains its existing name
and holds that quote, not the charged or refunded amount. The order has
no price condition; see [race
limits](vserver-paid-writes-resize.md#evidence).

`vngcloud.ErrPriceAboveMax` is in the root package (from
`internal/core`). `monitor.ErrPriceAboveMax` is the same value, so
`errors.Is` keeps working for monitor callers and the CLI maps one
sentinel to `PriceAboveMax`.

## Reads

Free reads a caller needs to build a create:

| Operation | Input | Output |
|-|-|-|
| `compute.ListFlavorZones` | `ZoneID` | `Items []FlavorZone` |
| `compute.ListFlavors` | `FlavorZoneID` or `ZoneID`, `Name` ([lookups](cli-usability.md#flavors-by-zone)) | `Items []Flavor` |
| `volume.ListVolumesByServer` | `ServerID` (r) | `Items []Volume` |
| `volume.GetDefaultVolumeType` | gains `ZoneID` | unchanged |

- `ListFlavorZones` filters by `ZoneID` in the SDK, since the API returns
  every zone. `FlavorZone` holds `ID`, `Name`, `Description`, and `ZoneID`.
- `ListFlavors` decodes into the existing `Flavor` model, which gains
  `RemainingVMs` and `IsSoldOut`. `GetDefaultVolumeType` sends `zoneId`
  when set; without it the API uses the region's first zone, which can be
  disabled.

## Servers

All methods live in `compute`. "(r)" marks `vngcloud:"required"`.

| Operation | Input | Output |
|-|-|-|
| `QuoteCreateServer` | `*CreateServerInput` | `pricing.GetQuoteOutput` |
| `CreateServer` | see below | `{Server; MonthlyPrice}` |
| `DeleteServer` | `ServerID` (r), `DeleteVolumes`, `NoWait` | `{DeletedVolumeIDs, KeptVolumeIDs}` |
| `StartServer` | `ServerID` (r), `NoWait` | `{Server; Changed}` |
| `StopServer` | `ServerID` (r), `NoWait` | `{Server; Changed}` |
| `RebootServer` | `ServerID` (r), `NoWait` | `{Server}` |
| `QuoteResizeServer` | `*ResizeServerInput` | `pricing.GetQuoteOutput` |
| `ResizeServer` | `ServerID` (r), `FlavorID` (r), `MaxPrice`, `NoWait` | `{Server; MonthlyPrice}` |
| `RenameServer` | `ServerID` (r), `Name` (r) | `{Server}` |

### Create

`CreateServerInput`:

| Field | Rule |
|-|-|
| `Name` (r) | Refused when a server with that exact name exists |
| `ZoneID` (r) | The subnet's zone |
| `FlavorID` (r), `ImageID` (r) | Path-safe IDs; the server checks they match |
| `VPCID` (r), `SubnetID` (r) | Sent as `networkId` and `subnetId` |
| `SecurityGroupIDs` (r) | At least one; no default |
| `SSHKeyID` | Exactly one of `SSHKeyID` or `UserData` that installs keys |
| `RootDiskSize` (r), `RootDiskTypeID` (r) | GB and a volume type ID |
| `DataDiskSize`, `DataDiskTypeID`, `DataDiskName` | One optional data disk; size and type together or neither |
| `ServerGroupID` | Optional |
| `UserData` | Cloud-init text; the SDK base64-encodes it and sets `userDataBase64Encoded` |
| `AutoRenew` | Sent as `isEnableAutoRenew`, false by default |
| `AttachFloatingIP`, `AllowOpenLogin` | Accepted additions; [floating IPs](vserver-floating-ips.md#r2-floating-ip-on-create) owns body, quote, and shared SDK exposure checks |
| `MaxPrice` | VND a month; default 0 |
| `NoWait` | Skip the wait |

- Encryption follows [encrypted volumes](encrypted-volumes.md). The SDK
  never sends the fields in [Non-goals](#non-goals).
- The duplicate-name check lists servers and matches the name exactly,
  since list filters match substrings. It runs before the quote. A rerun
  after an unclear failure then finds the first server by name instead of
  ordering a second one when that match is visible. Eventual visibility
  and concurrent callers can still permit duplicates.
- The project's default security group allows SSH, RDP, HTTP, HTTPS, and
  ICMP from anywhere
  ([free writes](vserver-writes.md#server-rules-from-the-product-docs)).
  The SDK never picks it; a caller who names it gets it, and the wiki says
  what it opens. Public create also follows the accepted
  [exposure check](vserver-floating-ips.md#login-exposure).
- `UserData` can hold secrets. When it is set, the create is `Sensitive`,
  so no response capture holds it, and no error or log quotes it. It is
  never sent to the quote.
- After the `202`, the SDK takes the ID from `data.uuid`. A `2xx` without
  it is an `*APIError` whose message says a server may exist and names
  `list-servers`. Then it waits.

### Delete

- `DeleteServer` reads the server and lists its volumes with
  `ListVolumesByServer` first. It sends `deleteAllVolume` equal to
  `DeleteVolumes`.
- With `DeleteVolumes` false, attached data volumes stay and keep being
  billed. The recorded run deletes the boot volume with the server.
  After the wait the SDK reads each volume it listed, and
  `KeptVolumeIDs` names those that still exist, so the caller sees what
  still costs money. With `NoWait` it names every listed data volume.
- With `DeleteVolumes` true, every attached volume is deleted with the
  server, data included. `DeletedVolumeIDs` names them.
- The server's refusals (`CREATING`, `CREATING-BILLING`, `DELETING`)
  reach the caller as `*APIError`.

### Start, stop, and reboot

Each reads the server first and sends its `PUT` with `Once`, then confirms
by reading, as ADR 0003 does for toggles:

- `StartServer` on an `ACTIVE` server and `StopServer` on a `STOPPED`
  server send nothing and return `Changed` false.
- `StartServer` needs `STOPPED` and `StopServer` needs `ACTIVE`;
  `RebootServer` needs `ACTIVE`. Any other status returns
  `ErrUnexpectedStatus` and sends nothing, so a start is never sent to a
  server mid-create.
- A `4xx` means the server did not act and returns that error. Any other
  failure after the send returns `ErrNotSettled`; the recovery is to run
  the same operation again, because it reads first.

Resize and rename rules, including signed quotes, are in
[resize](vserver-paid-writes-resize.md).

## Volumes

All methods live in `volume`.

| Operation | Input | Output |
|-|-|-|
| `QuoteCreateVolume` | `*CreateVolumeInput` | `pricing.GetQuoteOutput` |
| `CreateVolume` | `Name` (r), `ZoneID` (r), `Size` (r), `VolumeTypeID` (r), `AutoRenew`, `MaxPrice`, `NoWait` | `{Volume; MonthlyPrice}` |
| `DeleteVolume` | `VolumeID` (r), `NoWait` | `{}` |
| `QuoteResizeVolume` | `*ResizeVolumeInput` | `pricing.GetQuoteOutput` |
| `ResizeVolume` | `VolumeID` (r), `Size` (r), `MaxPrice`, `NoWait` | `{Volume; MonthlyPrice}` |
| `AttachVolume` | `VolumeID` (r), `ServerID` (r), `NoWait` | `{Volume; Changed}` |
| `DetachVolume` | `VolumeID` (r), `ServerID` (r), `AllowRunning`, `NoWait` | `{Volume; Changed}` |

- `CreateVolume` refuses a duplicate exact name, as `CreateServer` does,
  and takes the ID from `data.uuid`.
- `DeleteVolume` reads the volume first and returns `ErrVolumeInUse`,
  sending nothing, while it is `IN-USE` or lists a server. The server's
  own in-use refusal is the final guard. Delete destroys the data.
- `ResizeVolume` only grows. It reads the volume; a `Size` at or below the
  current size is `ErrInvalidInput` with nothing sent, because a shrink
  would cut off the end of the data. It sends the current
  `volumeTypeId` as `newVolumeTypeId`, which the API requires, so a type
  never changes by accident. It quotes, guards, sends with `Once`, and
  waits for the new size. The filesystem inside the server must still be
  grown; the wiki shows how.
- `AttachVolume` reads the volume first. Already attached to that server:
  `Changed` false, nothing sent. Attached elsewhere: the server's refusal.
- `DetachVolume` reads the volume and the server first:
  - Not attached to that server: `Changed` false, nothing sent.
  - The boot volume, a bootable volume, or no boot ID on the server read
    (always read): `ErrBootVolume`, nothing sent.
  - Server not `STOPPED`, `AllowRunning` false: `ErrServerRunning`, nothing
    sent; a mounted volume can lose unwritten data. Stop the server, or
    unmount and pass `AllowRunning`.
- Attach and detach keep the transport's `PUT` retries: a repeat is
  refused as already attached or already available, never a second
  charge.

## Waits

Per ADR 0002 rule 7, each write waits unless `NoWait` is set. The settled
states, poll spacing, bounds, and failure results are in the
[wait table](vserver-paid-writes-api.md#waits).

## Identifiers and retries

- Every operation that puts an ID in a path, including the reads above
  and `GetServer`, `GetVolume`, and `ListSnapshots`, checks it with
  `core.CheckPathID` before any request. Body IDs (flavor, image, VPC,
  subnet, security groups, key, volume type, server group) get the same
  check, so a bad ID fails before a quote.
- Creates are never resent. After a 5xx or a network error the error says
  the resource may exist and names `list-servers` or `list-volumes` with
  the exact name. The duplicate-name check reduces accidental repeat
  creates; it is not atomic against concurrent callers.
- Resize, start, stop, and reboot use `Once`. Rename, attach, detach, and
  deletes keep the transport's retries; a retried delete that finds the
  resource gone returns `NotFound`. The CLI never retries a write.

## CLI

| Command | Kind | `--yes` | Release |
|-|-|-|-|
| `compute list-flavor-zones`, `list-flavors` | Read | No | P1 |
| `compute quote-create-server`, `quote-resize-server` | Read | No | P1, P5 |
| `volume list-volumes-by-server`, `quote-create-volume`, `quote-resize-volume` | Read | No | P1, P5 |
| `volume create-volume` | Paid write | No | P2 |
| `volume delete-volume` | Write, destroys data | Yes | P2 |
| `compute create-server` | Paid write | No | P3 |
| `compute delete-server` | Write, destroys the server | Yes | P3 |
| `compute start-server`, `rename-server` | Write | No | P3 |
| `compute stop-server`, `reboot-server` | Write, interrupts | Yes | P3 |
| `volume attach-volume` | Write | No | P4 |
| `volume detach-volume` | Write, can lose data | Yes | P4 |
| `compute resize-server` | Paid write, restarts | Yes | P5 |
| `volume resize-volume` | Paid write | Yes | P5 |

- A paid create needs no `--yes`: `--max-price <vnd>` is its consent.
  Without the flag, `MaxPrice` is 0 and the command refuses with
  `PriceAboveMax` after the quote; a quote of 0 is refused as `ErrUnpriced`.
- Stop and reboot need `--yes` although start undoes them: they cut off
  what runs on the server and lose what it holds only in memory, as route
  writes need `--yes` for cutting traffic
  ([network writes](vserver-network-writes.md#cli)). Resize needs `--yes`
  because it restarts the server and may charge more. A credit passes
  without `--max-price`; the quote stays an estimate under the accepted
  race in decision 20.
- `delete-server --delete-volumes --yes` deletes attached volumes too.
  Without `--delete-volumes` they stay, and stdout lists them.
- `detach-volume --allow-running` maps to `AllowRunning`.
- `--security-group-id` repeats. The CLI flag reflection gains
  `[]string` fields as repeatable flags.
- `--user-data-file <path>` reads `UserData`, at most 64 KiB, from a
  file. `UserData` has no string flag and `--cli-input-json` refuses it,
  since argv and shell history keep it.
- A [read-only](cli.md#read-only) profile refuses every write with exit 2
  before any request. Quotes are reads and still run. Usage examples are
  on the generated `CLI-Compute` and `CLI-Volume` wiki pages.

## Errors

| Case | Result | CLI code and exit |
|-|-|-|
| Missing field, bad ID, bad `MaxPrice`, half a data disk, same flavor, shrink, duplicate name | `ErrInvalidInput`, nothing sent | `InvalidUsage`, 2 |
| Missing `--yes` | Nothing sent | `InvalidUsage`, 2 |
| Quote above `MaxPrice` | `vngcloud.ErrPriceAboveMax`, nothing ordered | `PriceAboveMax`, 1 |
| Quote of 0, or a volume resize quote below 0 | `vngcloud.ErrUnpriced`, nothing ordered | `Unpriced`, 1 |
| Status wrong for start, stop, reboot, or resize | `ErrUnexpectedStatus`, nothing sent | `UnexpectedStatus`, 1 |
| Delete of an attached volume | `ErrVolumeInUse` | `VolumeInUse`, 1 |
| Detach of the boot volume | `ErrBootVolume`, nothing sent | `BootVolume`, 1 |
| Detach from a server that is not `STOPPED`, without `AllowRunning` | `ErrServerRunning`, nothing sent | `ServerRunning`, 1 |
| `ERROR` in a wait | `ErrFailed`, with Output | `WriteFailed`, 1 |
| Bound reached, or a start, stop, or reboot not confirmed | `ErrNotSettled`, with Output | `NotSettled`, 1 |
| Unknown server or volume | `NotFound` | `NotFound`, 4 |
| Quota, billing refusal, flavor or image mismatch | The server's `*APIError` | 1 |
| 5xx or network error on a create | The error; the message names the list | 1 |

Service sentinels are `compute.ErrFailed`, `compute.ErrUnexpectedStatus`,
`compute.ErrNotSettled`, `volume.ErrNotSettled`, `volume.ErrFailed`,
`volume.ErrVolumeInUse`, `volume.ErrBootVolume`, `volume.ErrServerRunning`,
and `volume.ErrUnexpectedStatus`. They preserve `errors.Is` identity;
[CLI error mapping](cli.md#errors-and-exit-codes) preserves existing codes.

## Security

The guards above are the security design: a quote at or below `MaxPrice`
(default 0) before any paid write, a negative quote kept only for a
resize, no resent order, no password fields sent, no public IP unless
`AttachFloatingIP` asks and the summed quote covers it, no default
security group, auto-renew off, and `--yes` plus a guard on every write
that can lose data. Public create also requires the shared SDK
[exposure guard](vserver-floating-ips.md#login-exposure). The review that
checks them is in
[checks](vserver-paid-writes-checks.md#security-review). IDs, names, IPs,
and user data are account data; fixtures use `<id>`, `<name>`, `<ip>`, and
`<secret>`.

## Releases

| Release | Content | Tag |
|-|-|-|
| P1 | `ListFlavorZones`, `ListFlavors`, `ListVolumesByServer`, `GetDefaultVolumeType` zone, `QuoteCreateServer`, `QuoteCreateVolume`, `pricing.GetQuoteInput.Action`, `vngcloud.ErrPriceAboveMax`, path ID checks on the reads; CLI | Free read checks |
| P2 | `CreateVolume`, `DeleteVolume`, volume waits; CLI | After run L1 |
| P3 | `CreateServer`, `DeleteServer`, `StartServer`, `StopServer`, `RebootServer`, `RenameServer`, server waits, `[]string` flags, `--user-data-file`; CLI | After run L2 |
| P4 | `AttachVolume`, `DetachVolume` and their guards; CLI | After run L2 |
| P5 | `QuoteResizeServer`, `ResizeServer`, `QuoteResizeVolume`, `ResizeVolume`; CLI | After run L3 |
| P6 | Signed resize quote reads, then the credit write guard; CLI and wiki | Independent review, raw evidence |

The signed read change depends on the existing pricing and transport
packages. Require independent design and implementation review, the raw
fixture from free check 7, and deterministic regression tests before
release. These gates apply to reads and the accepted credit write guard.
This design approves no live run, resource creation, activation, purchase,
or cleanup.
Update `Compute-Servers`, `Volume`, `Billing-and-Pricing`, and generated
`CLI-Compute`, `CLI-Volume`, `CLI-Pricing`, and `CLI-LoadBalancer` pages
through `make gen-docs` from CLI sources. Release notes explain signed
results and the quote-to-charge race. No new dependency or CLI bypass is needed.

## Owner decisions

1. Release split: P1 to P5, with the evidence gates in [Releases](#releases).
2. Quote action: `GetQuoteInput.Action`, empty meaning `create`; no
   separate `GetResizeQuote`.
3. Price sentinel: one root `vngcloud.ErrPriceAboveMax`, reused by monitor.
4. `MaxPrice`: VND for the quote, default 0, not a whole-run budget.
   Creates quote a month; resizes quote an adjustment.
5. Login: require `SSHKeyID` or key-installing `UserData`, never both.
   Put login keys in cloud-config for user data. This does not prove
   the image disables password authentication.
6. Public IP on create: opt-in `AttachFloatingIP`, default off, priced
   and guarded under [floating IPs](vserver-floating-ips.md).
7. Security groups: at least one explicit ID, no project default.
8. Duplicate names: refuse exact matches before quoting servers or
   volumes. This reduces repeat creates; it is not an atomic uniqueness
   guarantee against concurrent callers.
9. Server delete: keep data volumes unless `DeleteVolumes`, and list
   those kept because billing continues. The boot volume is deleted.
10. Running-server detach: refuse unless `AllowRunning`; the caller must
    stop the server or unmount first.
11. Require `--yes` on delete-server, delete-volume, stop, reboot, both
    resizes, and detach; not on create, start, rename, or attach.
12. Volume resize: grow only and resend the current type.
13. Auto-renew: off by default, exposed as `AutoRenew` on create.
14. Snapshots: deferred pending paid service activation authority.
15. Data disk on create: one optional disk; attach others separately.
16. User data: `--user-data-file` only, marked sensitive.
17. Refusal probe: declined. No zero-balance paid create to test refusal;
    acceptance could bill later. A later run requires an owner decision.
18. Live budget: credit and per-run caps remain prerequisites under
    [live runs](vserver-paid-writes-checks.md#live-runs).
19. Negative resize quotes. Decided: accept a negative `optimumPrice`
    only for exact `Action` `resize`; refuse it for create, empty, and
    other actions. An action rule keeps the public shape and lets every
    resize quote reader report credits. A server-only opt-in would leave
    the generic and load balancer quote readers inconsistent.
20. Credit presentation and guard. Decided: keep signed `OptimumPrice`
    and `MonthlyPrice`, with no new credit field, and let a credit pass
    `ResizeServer` at every valid `MaxPrice`; zero stays `ErrUnpriced`.
    The residual race is accepted explicitly: a concurrent change can
    turn a quoted credit into a charge at `MaxPrice` 0, because the write
    carries no price or state condition. This is the same quote-to-send
    window every paid write already accepts. A magnitude cap, another
    pre-read, or an extra consent flag cannot enforce a charge ceiling.
    The existing vLB policy is not proof of server resize safety.
