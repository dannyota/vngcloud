# vServer Paid Writes Design

Status: Accepted (2026-09-28), except owner decision 17.

This design adds the vServer writes that cost money: servers in `compute`
and volumes in `volume`. Every paid create and resize quotes first and
orders nothing above `MaxPrice`, which defaults to 0. The code is built
and unit-tested now; the paid releases are tagged after their live runs,
which wait until the owner adds credit to the test account.

It follows [vServer free writes](vserver-writes.md) for the gateway,
create retries, and delete guards,
[ADR 0002](../adr/0002-write-api-conventions.md) for every write, and
[ADR 0003](../adr/0003-toggle-writes.md) for writes that read first and
send once. The price guard copies
[vMonitor log projects](monitor-alerts.md#log-projects). Calls, bodies,
statuses, the billing model, and the quoted prices are in
[vServer paid writes: API](vserver-paid-writes-api.md). Tests, live runs,
and the security review are in
[vServer paid writes: checks](vserver-paid-writes-checks.md).

## Non-goals

- Public IPs: `attachFloating` on create, and floating IP writes. An
  elastic IP costs 120,000 VND a month and exposes the server.
- Password login and Windows images: `userName`, `userPassword`,
  `osLicence`, and `expirePassword`.
- Volume and root disk encryption, multi-attach volumes, volume type
  change, and host groups.
- Volume snapshots. Creating one needs the snapshot service, which the
  console activates through a billing order and a payment page. Server
  snapshots, backups, and restores likewise.
- Changing a server's security groups, network interfaces, or tags.
- Renew, recover, and auto-renew changes after create.
- Server groups on create beyond passing an ID; server group writes are in
  [vServer network writes](vserver-network-writes.md).
- Writes to resources that OpenTofu manages. The wiki warns about drift.

## Cost

The test account is prepaid. A prepaid create pays one month from the
credit wallet at once, and a delete refunds the unused minutes, per the
[billing model](vserver-paid-writes-api.md#billing-model). There is no
hourly rate: a quote is VND a month. The smallest server this design can
create (1 vCPU, 2 GB, 20 GB SSD root) quotes 347,800 VND a month; a 10 GB
SSD volume quotes 32,000 ([prices](vserver-paid-writes-api.md#prices)).

With a zero balance the server refuses a paid create. The SDK does not
rely on that: with the default `MaxPrice` of 0 it never sends one.

## Quote before a paid write

Every write that can charge the account has a quote operation that takes
the write's own Input (ADR 0002 rule 8):

| Write | Quote | `resourceType`, `action` |
|-|-|-|
| `compute.CreateServer` | `compute.QuoteCreateServer` | `server`, `create` |
| `compute.ResizeServer` | `compute.QuoteResizeServer` | `server`, `resize` |
| `volume.CreateVolume` | `volume.QuoteCreateVolume` | `volume`, `create` |
| `volume.ResizeVolume` | `volume.QuoteResizeVolume` | `volume`, `resize` |

- One builder per write makes the request body. The quote sends that body
  as `resourceInfo`, plus `period` 1 and `isPoc` false, and minus
  `userData`, which is not priced and must not reach the billing gateway.
  The server ignores keys it does not price
  ([quote requests](vserver-paid-writes-api.md#quote-requests)).
- The quote's Output is `pricing.GetQuoteOutput`. `OptimumPrice` is VND a
  month for a create. For a resize, what it prices is an open question;
  the guard compares it the same way.
- `pricing.GetQuoteInput` gains `Action`; empty sends `create`, so
  existing callers are unchanged. The quote request is built by one
  internal helper that `pricing`, `compute`, and `volume` share.

The paid write runs these steps, in order, and stops at the first failure:

1. Check the Input shape and `MaxPrice`: NaN, an infinity, or a negative
   value is `ErrInvalidInput`, since each would disable the comparison.
2. Run the write's pre-reads and guards (below).
3. Quote. A quote error stops the write; a response without
   `optimumPrice` is an `*APIError`, never a price of 0.
4. When `OptimumPrice` is above `MaxPrice`, return an error wrapping
   `vngcloud.ErrPriceAboveMax` that names both amounts. Nothing is sent.
5. Send the write once. A create is a `POST` and is not retried after a
   5xx or a network error (ADR 0002 rule 2). A resize is sent with `Once`
   (ADR 0003 rule 3): a resent resize could pay twice.
6. Wait, unless `NoWait` is set.

`MaxPrice` is VND a month, the unit of the quote. The Output carries the
quoted `MonthlyPrice`. The price can change in the one request between the
quote and the write; the order itself carries no price.

`vngcloud.ErrPriceAboveMax` is new in the root package (from
`internal/core`). `monitor.ErrPriceAboveMax` becomes the same value, so
`errors.Is` keeps working for monitor callers and the CLI maps one
sentinel to `PriceAboveMax`.

## Reads

Free reads a caller needs to build a create:

| Operation | Input | Output |
|-|-|-|
| `compute.ListFlavorZones` | `ZoneID` | `Items []FlavorZone` |
| `compute.ListFlavors` | `FlavorZoneID` (r) | `Items []Flavor` |
| `volume.ListVolumesByServer` | `ServerID` (r) | `Items []Volume` |
| `volume.GetDefaultVolumeType` | gains `ZoneID` | unchanged |

- `ListFlavorZones` filters by `ZoneID` in the SDK, since the API returns
  every zone. `FlavorZone` holds `ID`, `Name`, `Description`, and
  `ZoneID`.
- `ListFlavors` decodes into the existing `Flavor` model, which gains
  `RemainingVMs` and `IsSoldOut`.
- `GetDefaultVolumeType` sends `zoneId` when set; without it the API
  looks up the region's first zone, which can be disabled.

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
| `SSHKeyID` (r) | The only login the SDK sets up |
| `RootDiskSize` (r), `RootDiskTypeID` (r) | GB and a volume type ID |
| `DataDiskSize`, `DataDiskTypeID`, `DataDiskName` | One optional data disk; size and type together or neither |
| `ServerGroupID` | Optional |
| `UserData` | Cloud-init text; the SDK base64-encodes it and sets `userDataBase64Encoded` |
| `AutoRenew` | Sent as `isEnableAutoRenew`, false by default |
| `MaxPrice` | VND a month; default 0 |
| `NoWait` | Skip the wait |

- The SDK sends `encryptionVolume` false and never sends the fields in
  [Non-goals](#non-goals).
- The duplicate-name check lists servers and matches the name exactly,
  since list filters match substrings. It runs before the quote. A rerun
  after an unclear failure then finds the first server by name instead of
  ordering a second one.
- The project's default security group allows SSH, RDP, HTTP, HTTPS, and
  ICMP from anywhere
  ([free writes](vserver-writes.md#server-rules-from-the-product-docs)).
  The SDK never picks it; a caller who names it gets it, and the wiki says
  what it opens.
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
  billed. Whether the boot volume stays is an open question. After the
  wait the SDK reads each volume it listed before the delete, and
  `KeptVolumeIDs` names those that still exist, so the caller sees what
  still costs money. With `NoWait` it names every listed volume.
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

### Resize and rename

- `ResizeServer` reads the server. The same flavor is `ErrInvalidInput`;
  a status other than `ACTIVE` or `STOPPED` is `ErrUnexpectedStatus`. Both
  send nothing. Then it quotes and guards as above, sends `flavorId` and
  `serverId` with `Once`, and waits until the flavor read back is the new
  one.
- `RenameServer` sends `newName` and returns the server from the
  response. Rename is free and keeps the transport's `PUT` retries.
- The root disk grows through `volume.ResizeVolume` on the server's
  `BootVolumeID`.

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

Per ADR 0002 rule 7, each write below waits unless `NoWait` is set. Each
package gets a poll helper with the injected clock and sleep that
`network` uses, and honours `ctx`.

| Write | Settled | Poll | Bound |
|-|-|-|-|
| Server create | `ACTIVE` | 5 s | 15 min |
| Server start, stop | `ACTIVE`, `STOPPED` | 5 s | 5 min |
| Server reboot | `ACTIVE`, read at least 10 s after the `202` | 5 s | 5 min |
| Server resize | New flavor, and `ACTIVE` or `STOPPED` | 5 s | 15 min |
| Server delete | 404 or `DELETED` | 5 s | 10 min |
| Volume create | `AVAILABLE` | 2 s | 5 min |
| Volume resize | New size, and `AVAILABLE` or `IN-USE` | 2 s | 5 min |
| Attach, detach | `IN-USE` with the server listed; `AVAILABLE` | 2 s | 5 min |
| Volume delete | 404 or `DELETED` | 2 s | 5 min |

- A 404 during a create wait keeps polling. `ERROR` returns the Output and
  an error wrapping `ErrFailed`. The bound returns the Output and an error
  wrapping `ErrNotSettled`, whose message says a create must not be
  repeated; the other writes read first and can be rerun.
- Unknown and moving statuses keep polling. The live runs record the
  times; a bound changes only by amending this table.

## Identifiers and retries

- Every operation that puts an ID in a path, including the reads above
  and `GetServer`, `GetVolume`, and `ListSnapshots`, checks it with
  `core.CheckPathID` before any request. Body IDs (flavor, image, VPC,
  subnet, security groups, key, volume type, server group) get the same
  check, so a bad ID fails before a quote.
- Creates are never resent. After a 5xx or a network error the error says
  the resource may exist and names `list-servers` or `list-volumes` with
  the exact name. The duplicate-name check makes a rerun safe.
- Resize, start, stop, and reboot use `Once`. Rename, attach, detach, and
  deletes keep the transport's retries; a retried delete that finds the
  resource gone returns `NotFound`.
- The CLI never retries a write.

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
  because it restarts the server and may charge more.
- `delete-server --delete-volumes --yes` deletes attached volumes too.
  Without `--delete-volumes` they stay, and stdout lists them.
- `detach-volume --allow-running` maps to `AllowRunning`.
- `--security-group-id` repeats. The CLI flag reflection gains
  `[]string` fields as repeatable flags.
- `--user-data-file <path>` reads `UserData`, at most 64 KiB, from a
  file. `UserData` has no string flag and `--cli-input-json` refuses it,
  since argv and shell history keep it.
- A [read-only](cli.md#read-only) profile refuses every write with exit 2
  before any request. Quotes are reads and still run.

```sh
vngcloud compute quote-create-server --name web-1 --zone-id <zone> \
  --flavor-id <flavor> --image-id <image> --vpc-id <vpc> \
  --subnet-id <subnet> --security-group-id <sg> --ssh-key-id <key> \
  --root-disk-size 20 --root-disk-type-id <type>
vngcloud compute create-server ... --max-price 347800
```

## Errors

| Case | Result | CLI code and exit |
|-|-|-|
| Missing field, bad ID, bad `MaxPrice`, half a data disk, same flavor, shrink, duplicate name | `ErrInvalidInput`, nothing sent | `InvalidUsage`, 2 |
| Missing `--yes` | Nothing sent | `InvalidUsage`, 2 |
| Quote above `MaxPrice` | `vngcloud.ErrPriceAboveMax`, nothing ordered | `PriceAboveMax`, 1 |
| Status wrong for start, stop, reboot, or resize | `ErrUnexpectedStatus`, nothing sent | `UnexpectedStatus`, 1 |
| Delete of an attached volume | `ErrVolumeInUse` | `VolumeInUse`, 1 |
| Detach of the boot volume | `ErrBootVolume`, nothing sent | `BootVolume`, 1 |
| Detach from a server that is not `STOPPED`, without `AllowRunning` | `ErrServerRunning`, nothing sent | `ServerRunning`, 1 |
| `ERROR` in a wait | `ErrFailed`, with Output | `WriteFailed`, 1 |
| Bound reached, or a start, stop, or reboot not confirmed | `ErrNotSettled`, with Output | `NotSettled`, 1 |
| Unknown server or volume | `NotFound` | `NotFound`, 4 |
| Quota, billing refusal, flavor or image mismatch | The server's `*APIError` | 1 |
| 5xx or network error on a create | The error; the message names the list | 1 |

New sentinels: `compute.ErrFailed` and `compute.ErrUnexpectedStatus`
(`compute.ErrNotSettled` exists); `volume.ErrNotSettled`,
`volume.ErrFailed`, `volume.ErrVolumeInUse`, `volume.ErrBootVolume`,
`volume.ErrServerRunning`, and `volume.ErrUnexpectedStatus`. The CLI list in
[CLI](cli.md#errors-and-exit-codes) gains `VolumeInUse`, `BootVolume`, and
`ServerRunning`; the other codes keep their meaning.

## Security

The guards above are the security design: a quote at or below `MaxPrice`
(default 0) before any paid write, no resent order, key login only, no
public IP or default security group, auto-renew off, and `--yes` plus a
guard on every write that can lose data. The review that checks them is in
[checks](vserver-paid-writes-checks.md#security-review). IDs, names, IPs,
and user data are account data; fixtures use `<id>`, `<name>`, `<ip>`, and
`<secret>`.

## Releases

| Release | Content | Tag |
|-|-|-|
| P1 | `ListFlavorZones`, `ListFlavors`, `ListVolumesByServer`, `GetDefaultVolumeType` zone, `QuoteCreateServer`, `QuoteCreateVolume`, `pricing.GetQuoteInput.Action`, `vngcloud.ErrPriceAboveMax`, path ID checks on the reads; CLI | Now |
| P2 | `CreateVolume`, `DeleteVolume`, volume waits; CLI | After run L1 |
| P3 | `CreateServer`, `DeleteServer`, `StartServer`, `StopServer`, `RebootServer`, `RenameServer`, server waits, `[]string` flags, `--user-data-file`; CLI | After run L2 |
| P4 | `AttachVolume`, `DetachVolume` and their guards; CLI | After run L2 |
| P5 | `QuoteResizeServer`, `ResizeServer`, `QuoteResizeVolume`, `ResizeVolume`; CLI | After run L3 |

P1 is free and ships now. P2 to P5 are built and unit-tested now, in this
order on one branch, and each is tagged after its
[live run](vserver-paid-writes-checks.md#live-runs) passes. No release
breaks callers; the reads above only start rejecting a malformed ID, and
`monitor.ErrPriceAboveMax` changes only its message. The `Compute`,
`Volume`, and `Billing-and-Pricing` wiki pages gain the writes, the cost
table, the `--max-price` and `--yes` reasons, and the drift warning.

## Owner decisions

1. Release split. Options: P1 to P5 as above; one release. Recommend P1
   to P5: each is small, and only P1 can be verified before credit.
2. Quote action. Options: `GetQuoteInput.Action`, empty meaning `create`;
   a separate `GetResizeQuote`. Recommend `Action`: one call, no break.
3. Price sentinel. Options: one root `vngcloud.ErrPriceAboveMax` that
   `monitor` reuses; one sentinel per package. Recommend one root value.
4. `MaxPrice` unit. Options: VND a month from `OptimumPrice`, default 0;
   a total for the whole run. Recommend VND a month, as vMonitor does.
5. Login. Options: `SSHKeyID` required; optional, letting GreenNode
   create a password. Recommend required.
6. Public IP on create. Options: never; an `AttachFloatingIP` flag.
   Recommend never in this design: it costs 120,000 VND a month and opens
   the server to the internet.
7. Security groups. Options: at least one, no default; fall back to the
   project default group. Recommend no default: it is open to the world.
8. Duplicate names. Options: refuse an exact-name match before the quote
   for servers and volumes; allow duplicates. Recommend refuse: it makes a
   rerun after an unclear failure safe.
9. Server delete. Options: keep volumes unless `DeleteVolumes`
   (`--delete-volumes`); always delete them. Recommend keep, and list the
   kept volumes, because they keep costing money.
10. Detach from a running server. Options: refuse unless `AllowRunning`;
    `--yes` only. Recommend refuse: the API cannot tell whether the
    volume is mounted.
11. `--yes`. Recommend on delete-server, delete-volume, stop, reboot,
    resize-server, resize-volume, and detach; not on creates (guarded by
    `--max-price`), start, rename, or attach.
12. Volume resize. Options: grow only, resending the current type; also
    allow a type change. Recommend grow only.
13. Auto-renew. Options: off by default with an `AutoRenew` field; the
    API default. Recommend off: nothing renews from credit without a
    command.
14. Snapshots. Recommend defer: they need a paid service activation
    through a payment page.
15. Data disks on create. Options: one, as the API allows, and more
    through `CreateVolume` and `AttachVolume`; none. Recommend one.
16. User data. Options: `--user-data-file` only, marked sensitive; a
    string flag. Recommend the file.
17. Refusal probe. Options: approve one `CreateVolume` of 1 GB
    (3,200 VND a month) on the zero-balance account to record the
    server's refusal; skip it. Declined: no create is sent before the
    account has credit, since a create the server accepts may bill later.
18. Live budget. Recommend the owner adds at least 1,000,000 VND of
    credit and approves the caps in
    [live runs](vserver-paid-writes-checks.md#live-runs).

## Open questions

- Whether the direct create charges one month at once, and whether a
  delete refunds servers and volumes to the minute.
- What a resize quote prices: the new monthly rate or the prorated
  difference.
- Whether `deleteAllVolume` false keeps the boot volume.
- What happens when a period ends without renewal, and after how long an
  expired server is deleted.
- The status and message of a create refused for a zero balance.
- The create responses' shapes, and whether server and volume names must
  be unique on the server.
- Whether a stop is graceful, and whether a reboot shows `REBOOTING`.
- The smallest volume the SSD type accepts: 1 GB or 10 GB.
