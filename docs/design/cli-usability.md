# CLI Usability Design

Status: Accepted (2026-10-10).

This design fixes what the first external agent run found hard in the CLI:
finding the IDs a server quote needs, quote flags the price does not use,
and list output shapes. It builds on [CLI](cli.md) and
[vServer paid writes](vserver-paid-writes.md), whose reads and quotes it
changes. Every change here is free to verify: reads and quotes only.

## Goals

- An agent finds every ID `create-server` needs in one call per kind.
- A quote asks only for the values that change its price.
- Every list command prints one shape, so one `--query` works on all.

## Non-goals

- A command that picks IDs for the caller, such as a cheapest flavor.
- A placeholder ID on the wire. The quote leaves unpriced keys out.
- Changing any create Input. `CreateServerInput`, `CreateVolumeInput`, and
  `CreateLoadBalancerInput` keep their fields and required tags.

## Where the lookups live

The lookups live in the SDK, as new Input fields on the existing list
operations. The CLI then gets them through flag reflection with no
hand-built command.

- The CLI design maps each command to one SDK method of the same name
  ([Commands](cli.md#commands)). A CLI-only `list-flavors` with
  `--zone-id` would need a hand-built op whose Input is not the SDK's,
  which only `create-ssh-key` has today, for a secret file.
- The SDK already does client-side work on reads: `ListFlavorZones`
  filters by zone after the call, and `ListAllSnapshots` sends one request
  per volume. A fan-out read is the same kind of helper.
- Go callers face the same five-call discovery and get the fix too.
- The change is additive: no field is removed, and one required tag is
  dropped in favor of a one-of rule.

## Flavors by zone

`compute.ListFlavorsInput` gains two fields:

| Field | Flag | Rule |
|-|-|-|
| `FlavorZoneID` | `--flavor-zone-id` | No longer tagged required |
| `ZoneID` | `--zone-id` | Network zone; lists every flavor zone in it |
| `Name` | `--name` | Keep only flavors whose `Name` equals this exactly |

- Exactly one of `FlavorZoneID` and `ZoneID` is set. Neither, or both, is
  `ErrInvalidInput` naming both fields, before any request.
- With `FlavorZoneID`, the call is one request, as today.
- With `ZoneID`, the SDK calls `ListFlavorZones` with that `ZoneID`, then
  `ListFlavors` once per flavor zone it returned, one at a time. That is
  `1 + N` requests for `N` flavor zones in the zone. Each flavor zone ID
  passes `core.CheckPathID` before its request.
- Output stays `ListFlavorsOutput` (`Items []Flavor`). Rows keep the order
  of the flavor zone list, then the API's order inside each flavor zone.
- Each row names its flavor zone. When the API leaves a row's
  `FlavorZoneID` or `ZoneID` empty, the SDK fills it with the value it
  queried; a non-empty API value is kept.
- No dedup. A flavor zone that returns no flavors adds no rows, so the
  same-named empty flavor zones the agent hit drop out on their own. A
  flavor listed in two flavor zones stays as two rows, told apart by
  `FlavorZoneID`.
- Sold-out flavors stay in the list with `IsSoldOut` true. A caller drops
  them with `--query "Items[?!IsSoldOut]"`.
- The first failing request ends the call with its error. No partial list
  is returned, so a missing flavor zone never looks like an empty one.
- A zone with no flavor zones returns empty `Items` and exit 0, like any
  list. So does a `Name` that matches nothing.

```sh
vngcloud compute list-flavors --zone-id <zone> --name s2-general-1x2 \
  --query "Items[].[FlavorID,FlavorZoneID,IsSoldOut]"
```

## Volume types by zone

`volume.ListVolumeTypesInput` gains two fields:

| Field | Flag | Rule |
|-|-|-|
| `VolumeTypeZoneID` | `--volume-type-zone-id` | Unchanged |
| `ZoneID` | `--zone-id` | Network zone; lists every volume type zone in it |
| `IOPS` | `--iops` | When above 0, keep only types whose `IOPS` equals it |

- At most one of `VolumeTypeZoneID` and `ZoneID` is set; both is
  `ErrInvalidInput`. Neither keeps today's project-wide list.
- A negative `IOPS` is `ErrInvalidInput`. 0 means no filter.
- With `ZoneID`, the SDK calls `ListVolumeTypeZones` with that `ZoneID`,
  keeps only the zones whose `Zone.UUID` equals it (the API filter is not
  confirmed), then calls `ListVolumeTypes` once per volume type zone, one
  at a time: `1 + M` requests.
- Order, empty results, the first-error rule, and filling an empty
  `VolumeTypeZoneID` or `ZoneID` on a row follow
  [flavors by zone](#flavors-by-zone).
- `IOPS` applies in every mode, after the call.

```sh
vngcloud volume list-volume-types --zone-id <zone> --iops 3000 \
  --query "Items[].[ID,Name,MinSize,MaxSize]"
```

## IDs for create-server

A new CLI wiki page, `IDs-for-Create-Server.md`, lists each ID flag of
`create-server` and `quote-create-server` with the command that finds it.
It is linked from `CLI.md`, `CLI-Compute.md` (through the
`create-server` and `quote-create-server` notes), `Compute-Servers.md`,
and `_Sidebar.md`.

| Flag | Source command | Quote needs it |
|-|-|-|
| `--zone-id` | `portal list-zones` | Yes |
| `--flavor-id` | `compute list-flavors --zone-id <zone>` | Yes |
| `--image-id` | `compute list-os-images --zone-id <zone>` | Yes |
| `--root-disk-type-id`, `--data-disk-type-id` | `volume list-volume-types --zone-id <zone>` | Yes |
| `--vpc-id` | `network list-vpcs` | No |
| `--subnet-id` | `network list-subnets-by-vpc --vpc-id <vpc>` | No |
| `--security-group-id` | `network list-security-groups` | No |
| `--ssh-key-id` | `compute list-ssh-keys` | No |
| `--server-group-id` | `compute list-server-groups` | No |

The cli role confirms each row's command name and the output field that
holds the ID against the golden files and fixtures, and gives one
`--query` per row. The page also says that `IsEnabled` false on a zone is
a hint, not a gate, linking the `portal list-zones` note.

## Quotes ask only for priced values

Rule: a quote requires only the fields that change its price. A field the
price ignores is optional on the quote. When it is set, the SDK still
checks its shape (`core.CheckPathID` for an ID), so a bad value fails at
the quote as it will at the create. It is never sent to the billing
gateway.

The quote body holds only the priced keys, as the console sends them
([quote requests](vserver-paid-writes-api.md#quote-requests)). One quote
builder per write makes that body. The quote operation and the paid
write's own price guard both call it, so the price a caller sees is the
price the guard checks (ADR 0002 rule 8). The create body builder and its
required checks do not change.

| Quote | Required on the quote | Required on the create only | Body keys besides `period`, `isPoc` |
|-|-|-|-|
| `compute.QuoteCreateServer` | `ZoneID`, `FlavorID`, `ImageID`, `RootDiskSize`, `RootDiskTypeID` | `Name`, `VPCID`, `SubnetID`, `SecurityGroupIDs`, `SSHKeyID` | `zoneId`, `flavorId`, `imageId`, `rootDiskSize`, `rootDiskTypeId`, `encryptionVolume`; `dataDiskSize` and `dataDiskTypeId` when set |
| `volume.QuoteCreateVolume` | `ZoneID`, `Size`, `VolumeTypeID` | `Name` | `zoneId`, `size`, `volumeTypeId` |
| `loadbalancer.QuoteCreateLoadBalancer` | `PackageID`, `ZoneID` | `Name`, `Scheme`, `SubnetID`, `Type` | `packageId`, `zoneId`, `isBuyMorePoc` (unchanged) |

- Fields that are optional on the create, such as `ServerGroupID`,
  `DataDiskName`, and `AutoRenew`, are checked as today and not sent.
- The half-a-data-disk rule stays: `DataDiskSize` and `DataDiskTypeID`
  are set together or not at all, on the quote and the create.
- `QuoteResizeServer`, `QuoteResizeVolume`, and `QuoteResizeLoadBalancer`
  price every field they require, so they do not change.
  `monitor.QuoteCreateLogProject` does not change either: its body is the
  order body and its only required field, `Name`, is part of that body.
- `UserData`, `MaxPrice`, and `NoWait` stay hidden on quote commands.
- The vLB quote body is already priced-only; only its required set
  shrinks.

### Why not a placeholder

A placeholder ID such as `none` on the wire works today only because the
gateway ignores the key. A gateway change could start checking it, and a
placeholder would then fail in a way the caller cannot read. Leaving the
key out matches what the console sends.

### CLI

The CLI reads required fields from the `vngcloud:"required"` tag, which
the create and the quote share. A new read option,
`Optional(fields ...string)`, beside `NoFlag`, marks fields not required
for one op: the CLI's required check skips them and `gen-docs` prints
"no" in the Required column. The three quote commands set it to the
"Required on the create only" column above. A test runs each quote command with
only its required flags against a fake gateway and checks the body has no
unpriced key, so the CLI list and the SDK rule cannot drift.

```sh
vngcloud compute quote-create-server --zone-id <zone> \
  --flavor-id <flavor> --image-id <image> \
  --root-disk-size 20 --root-disk-type-id <type>
```

The `quote-create-server` note drops the sentence that the gateway
ignores unpriced keys and says instead which flags are optional and why.

## List output shape

Rule: every list command prints a JSON object whose rows are under
`Items`. Page metadata and other fields sit beside `Items` (`Page`,
`TotalItem`, `Summary`, `Source`). `table` and `text` render the rows of
`Items`.

The code already follows the rule. `list-flavor-zones` and
`list-os-images` return `core.List`, so their Output is
`{"Items": [...]}`, the same as `list-flavors` and `list-volume-types`;
the golden file `compute-list-flavor-zones.json.golden` shows it. No
Output type in any service package is a slice, and the generic `cli.Read`
encodes the method's `*Out` as is, so there is no bare-array path to fix.
The one list Output without `Items` is `iam list-policy-attachments`,
which returns three lists (`Groups`, `UserIDs`, `ServiceAccountIDs`) by
design and keeps that shape.

So no command's output changes and no release note is needed. The fix is
a guard: a CLI test walks every service's op table and fails when a read
whose name starts with `list-` has an Output that is not a struct with an
`Items` slice field, with `iam list-policy-attachments` as the one named
exception. A future slice Output then fails `make check`.

The run's report most likely compared `--output text` or `table` output,
which prints the rows of `Items` without the wrapper, with `json`. The
manager confirms that against the run's transcript; see
[open items](#open-items).

## Smaller fixes

These need no design beyond one sentence each.

- Quote disk text: the quote note says the gateway's `ROOT DISK` line
  shows the type ID where the size belongs; rendering the size from the
  Input instead would rewrite gateway text, so the note stays the fix.
- `IsEnabled` false: the `portal list-zones` note says the flag is a hint,
  not a gate.
- Configuration: the wiki page gives the read-only agent profile recipe,
  and `configure list` prints the file paths when every value is empty.
- `--version`: the root command accepts `--version` and `-v` beside
  `vngcloud version`.

The last three, and the quote note, are on `master` (unpushed when this
design was written).

## Security

- The lookups are reads with no new request type. Path IDs taken from a
  list response pass `core.CheckPathID` before they reach a path.
- The quote change sends fewer keys to the billing gateway, never more.
  `UserData` still never reaches it.
- A paid create still requires every field it required. Only the quote's
  required set shrinks, and the price guard quotes the same priced-only
  body the quote command prints.
- Read-only profiles run all of these, as before.

## Testing

- SDK: `httptest` tests for each fan-out: request count and order, the
  one-of rule, `Name` and `IOPS` filters, empty flavor zones, filling
  empty `FlavorZoneID` and `ZoneID`, a failing second request returning
  no rows, and a bad flavor zone ID from the API refused before its
  request.
- SDK: each quote with only its required fields sends exactly the body
  keys in the table; an unpriced field with a bad ID is refused; the
  create's required checks are unchanged; `CreateServer` and
  `CreateVolume` guards send the priced-only body.
- CLI: golden output for `list-flavors --zone-id`, the quote body test
  above, the list-shape guard, and generated wiki pages.
- Live, free: `list-flavors --zone-id` and `list-volume-types --zone-id`
  in `hcm-3`, and a `quote-create-server` with only priced flags that must
  quote 347,800 VND for `s2-general-1x2`, Ubuntu 24.04, 20 GB SSD root
  ([prices](vserver-paid-writes-api.md#prices)). The run also records
  whether `ListFlavors` rows carry `flavorZoneId` and whether
  `volume_type_zones` honors `zoneId`. Fixtures follow
  [live data](../../instructions/live-data.md).

## Releases

| Release | Content | Breaks |
|-|-|-|
| U1 | `ListFlavors` `ZoneID` and `Name`, `ListVolumeTypes` `ZoneID` and `IOPS`, the list-shape guard test, `IDs-for-Create-Server.md`; SDK then CLI notes and wiki | No |
| U2 | Priced-only quote bodies and required sets for server, volume, and load balancer quotes; the price guards use them; CLI `Optional`; wiki and notes | No |

Each is one minor tag, in this order, after its free live run. U1 is
sdk work plus cli notes and the wiki page; U2 is sdk work, then cli. U2
changes the body the paid create guards send, so it gets the adversarial
review that write paths get, limited to the guard and the quote body.

## Owner decisions

1. Where the lookups live. Options: SDK Input fields on `ListFlavors` and
   `ListVolumeTypes`; CLI-only hand-built ops. Recommended: SDK, for the
   reasons in [where the lookups live](#where-the-lookups-live).
2. Flavor name filter. Options: exact match; substring. Recommended:
   exact, since an ID lookup wants one flavor, and `--query` with
   `contains()` covers partial matches.
3. Quote unpriced fields. Options: optional and left out of the body; a
   documented placeholder; keep them required. Recommended: optional and
   left out, as the console does.
4. Price guard body. Options: the paid creates' guards quote the same
   priced-only body; they keep quoting the full create body.
   Recommended: the same body, so the quote command and the guard always
   price the same request.
5. List shape. Options: the `Items` rule with a guard test and no output
   change; also unwrap or re-wrap anything. Recommended: the rule and the
   guard test, since every list already follows it.
6. Release split. Options: U1 then U2; one release. Recommended: U1 then
   U2, one feature per tag.

## Open items

- The agent run's exact commands for the bare-array report, to confirm it
  came from `text` or `table` output.
- Whether live `ListFlavors` rows carry `flavorZoneId`, and whether
  `volume_type_zones` filters by `zoneId`. The design works either way.
- Whether the gateway prices the priced-only server body the same as the
  full one. The U2 live quote checks it before the tag.
