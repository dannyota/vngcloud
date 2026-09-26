# vDNS Design

Status: Accepted (2026-09-26).

This design adds vDNS writes to the SDK and CLI: creating, updating, and
deleting private hosted zones and their records. vDNS hosts private zones
only: a zone resolves only inside the VPCs associated with it. So it cannot
take aboutme's public `aboutme.vn` zone, and aboutme keeps its public DNS
elsewhere. The writes ship anyway, for private names inside a VPC.

It builds on [SDK and CLI](sdk-and-cli.md): package per service,
`Method(ctx, *Input) (*Output, error)`, operation tables, and the error
model. Writes follow [ADR 0002](../adr/0002-write-api-conventions.md).

## Source

`docs.api.greennode.ai` does not list vDNS. The calls come from VNG Cloud's
own Go SDK (`vngcloud/vngcloud-go-sdk`, package `services/dns/v1`), and live
checks on the test account in `hcm-3` on 2026-09-26 confirmed them. The
product docs (`docs.greennode.ai/vdns/features`) say public hosted zones are
"currently not supported", and the console's create form offers no zone
type.

The base URL is the existing `DNS` endpoint,
`https://vdns.console.greennode.ai/vdns-api/`, with version `v1`. Paths
carry no project ID; zones are per account.

| Call | Method and path | Success | Async |
|-|-|-|-|
| Create zone | `POST /dns/hosted-zone` | 200, zone in `data`, `CREATING` | `ACTIVE` after about 7 s |
| Update zone | `PUT /dns/hosted-zone/{zoneId}` | 204 | `UPDATING` about 6 s when the VPCs change, then `ACTIVE` |
| Delete zone | `DELETE /dns/hosted-zone/{zoneId}` | 204 | GET 404 after about 5 s |
| Create record | `POST /dns/hosted-zone/{zoneId}/record` | 200, record in `data`, `CREATING` | Zone locked about 12 s |
| Update record | `PUT /dns/hosted-zone/{zoneId}/record/{recordId}` | 204 | Zone locked about 12 s |
| Delete record | `DELETE /dns/hosted-zone/{zoneId}/record/{recordId}` | 204 | GET still finds it at once |

A repeat delete returns 404. Lists return `listData`, `page`, `pageSize`,
`totalPage`, and `totalItem`; `page` and `pageSize` may be 0.

### Bodies

- Zone create: `domainName`, `assocVpcIds` (string array), `type`
  (`PRIVATE`), `description`.
- Zone update takes `assocVpcIds` and `description` and is a full replace:
  a field left out is cleared. A body of only `description` cleared
  `assocVpcIds` to `[]`, and `{}` cleared the description. A zone left with
  no VPCs stays `ACTIVE` but resolves in no VPC.
- Record create: `subDomain`, `ttl` (required; null is a 400), `type`,
  `routingPolicy`, `value` (array of `{value, location, weight}`), and
  optional `enableStickySession`.
- Record update is partial, unlike zone update: a body of only `ttl`
  changes the TTL and keeps the values. A full body also works.

### Rules the server enforces

- The apex is `subDomain: ""`; `"@"` is a 400. Responses return
  `subDomain` as the full name (`www.<zone>`, or the zone for the apex).
- `routingPolicy` is `simple-routing`, `weighted`, or `geolocation`;
  `simple` is a 400.
- Types are `A`, `CNAME`, `MX`, `SRV`, `TXT`, and `PTR`. The server makes
  `NS` and `SOA` records with each zone and refuses to delete them.
- One record per type per subdomain; a second is a 400. Several values go
  in one record's `value` array. An MX value is `10 mx1.example.com`. TXT
  values take spaces and most punctuation but not `"`.
- Deleting a zone that holds any record other than `NS` and `SOA` is a 400.
- The quota page lists 100 zones, 1000 records per zone, 100 values per
  record, and 50 VPCs per zone.

### Zone status and the zone lock

Zone statuses seen are `CREATING`, `ACTIVE`, `UPDATING`, and `ERROR`. A
zone update that changes the VPC list sets `UPDATING` before the 204
returns and holds it for about 6 seconds; a description-only update,
including a no-op, never leaves `ACTIVE`. So a read that is `ACTIVE` and
shows the sent fields is settled. Every record
create or update moves its zone out of `ACTIVE` for about 11 to 12 seconds.
Meanwhile any other record write to that zone fails with 400 `<zone> was
invalid status. Allowed in [ACTIVE, ERROR]`, and nothing is written. A record
delete left the zone `ACTIVE` within a second.

### Private DNS on the VPC

Every VPC in a zone needs Private DNS on. A VPC's `dnsStatus`, which
`network.GetVPC` already returns, goes `DISABLED`, `ENABLING`, `ENABLED`.
The console enables it with `PATCH /v2/{projectId}/networks/{vpcId}/enableDns`
on the vServer gateway; `ENABLED` follows about 5.5 minutes later.

- Creating a zone for a `DISABLED` VPC returns 404 `VPC: <id> is inactive
  DNS.`
- Creating one for an `ENABLING` VPC returns 200, and the zone goes to
  `ERROR` within a second.

### Cost

Nothing was billed for the live checks, on an account with no credit, where
a paid action would fail. vDNS appears on no pricing page. The design treats
zones and records as free within the quota, so ADR 0002 rule 8 (quote) does
not apply. One open check remains: a vDNS line on the next day's bill.

## Non-goals

- Public zones, DNSSEC, and nameserver delegation. vDNS lacks them.
- Enabling or disabling Private DNS on a VPC. It stays a console step; see
  [Private DNS stays in the console](#private-dns-stays-in-the-console).
- A zone file import or sync command. Per-record `create-record` in a
  script is enough.
- Finding a record by name in the SDK. `ListRecords` with `Name` filters.

## SDK

All methods live in the existing `dns` package, as `dns.<Method>`
operations.

```go
func (c *Client) CreateHostedZone(ctx context.Context, in *CreateHostedZoneInput) (*CreateHostedZoneOutput, error)
func (c *Client) UpdateHostedZone(ctx context.Context, in *UpdateHostedZoneInput) (*UpdateHostedZoneOutput, error)
func (c *Client) DeleteHostedZone(ctx context.Context, in *DeleteHostedZoneInput) (*DeleteHostedZoneOutput, error)
func (c *Client) CreateRecord(ctx context.Context, in *CreateRecordInput) (*CreateRecordOutput, error)
func (c *Client) UpdateRecord(ctx context.Context, in *UpdateRecordInput) (*UpdateRecordOutput, error)
func (c *Client) DeleteRecord(ctx context.Context, in *DeleteRecordInput) (*DeleteRecordOutput, error)

var (
	ErrZoneBusy   = errors.New("dns: zone busy")
	ErrNotSettled = errors.New("dns: write accepted but not settled")
	ErrFailed     = errors.New("dns: write failed on the server")
)

const (
	StatusCreating = "CREATING"
	StatusActive   = "ACTIVE"
	StatusUpdating = "UPDATING"
	StatusError    = "ERROR"
)
```

"(r)" marks `vngcloud:"required"`. Every write Input also has `NoWait bool`;
see [Waits](#waits).

| Operation | Input | Output |
|-|-|-|
| `CreateHostedZone` | `DomainName` (r), `VPCIDs` []string (r), `Description` | `{HostedZone}` |
| `UpdateHostedZone` | `HostedZoneID` (r), `VPCIDs` *[]string, `Description` *string | `{HostedZone}` |
| `DeleteHostedZone` | `HostedZoneID` (r) | `{}` |
| `CreateRecord` | `HostedZoneID` (r), `SubDomain`, `Type` (r), `TTL`, `RoutingPolicy`, `Values` []RecordValue (r), `StickySession` *bool | `{Record}` |
| `UpdateRecord` | `HostedZoneID` (r), `RecordID` (r), `SubDomain` *string, `Type` *string, `TTL` *int, `RoutingPolicy` *string, `Values` *[]RecordValue, `StickySession` *bool | `{Record}` |
| `DeleteRecord` | `HostedZoneID` (r), `RecordID` (r) | `{}` |

- `CreateHostedZone` always sends `type: "PRIVATE"`, the only type. A later
  public type adds a field, which breaks no caller.
- `SubDomain` is the label relative to the zone; empty is the apex. The
  returned `Record.SubDomain` is the full name, as the API returns it.
- `TTL` 0 sends 300, the console default. `RoutingPolicy` empty sends
  `simple-routing`. Zero is never valid for either, so ADR 0002 rule 3
  needs no pointer.
- `RecordValue` gains `omitempty` on `location` and `weight`. It stays the
  decode model.
- Update Inputs follow ADR 0002 rule 3: every optional field is a pointer,
  and nil means "keep". An update with no non-nil field is
  `ErrInvalidInput`, and nothing is sent: an empty body would lock the zone
  and, for a zone, clear it.
- `UpdateRecord` sends only the non-nil fields, because the API applies a
  partial record body.
- `UpdateHostedZone` reads the zone after the pre-write wait, sets each
  non-nil field on what it read, and sends the full body, because the API
  replaces the zone. An unset `Description` resends the current one, and an
  unset `VPCIDs` resends the current `assocVpcIds` unchanged. The race with
  a writer in another process remains: the last write wins, and the API has
  no condition field.
- Detaching every VPC is allowed only on purpose. `VPCIDs` nil keeps the
  VPCs; `VPCIDs` pointing at an empty list sends `[]` and detaches the zone,
  which then resolves nowhere. In the CLI that is
  `--cli-input-json '{"VPCIDs":[]}'`; `null` or leaving the key out keeps
  them. A detach is not destructive, because another update re-attaches,
  so it needs no `--yes`. The wiki page warns about it.
- Update responses are 204 with no body, so the Output comes from a read
  after the write.
- Per ADR 0002 rule 5, the SDK checks only required fields and shape. The
  record syntax, TXT character set, type uniqueness, and quota stay on the
  server.

### Identifiers

Every operation that puts `HostedZoneID` or `RecordID` in a path checks it
with `core.CheckPathID` (`^[A-Za-z0-9-]+$`) before any request, the four
reads included. IDs are `hosted-zone-<uuid>` and `record-<uuid>`.

### Retries

- Creates are `POST`: the transport retries only after a 429 or a failed
  dial (ADR 0002 rule 2). After a 5xx or a network error the resource may
  exist; the caller lists with `Name` before running it again, and the
  error says so.
- A create response without an ID is an `*APIError`. The SDK never finds a
  new resource by listing.
- Update and delete are idempotent and keep the transport's retries. A
  retried delete that finds the resource gone returns `NotFound`.
- The SDK does not retry the zone lock's 400. The pre-write wait makes it
  rare; when it happens, nothing was written, and the caller runs the same
  call again, which waits first.

## Waits

vDNS writes are asynchronous, so per ADR 0002 rule 7 this section defines
the waits. A wait polls with normal `GET` reads every 2 seconds, honours
`ctx`, and stops after 60 seconds. The clock is injected for tests, as in
`monitor`. A status the SDK does not know keeps the wait polling.

### Before a write

Every record write, and zone update and delete, first reads the zone and
waits until it is `ACTIVE` or `ERROR`, the states the server accepts;
`CREATING` and `UPDATING` count as busy. A zone update merges onto this
wait's last read. This
wait always runs, `NoWait` or not: nothing has been sent, so it is safe,
and it turns the zone lock into a short delay instead of a 400. When the
bound ends first, the call returns `ErrZoneBusy` and sends nothing; running
it again is safe.

Within one process, a `dns.Client` runs its writes one at a time under a
mutex, from the pre-write read to the end of the call, so two goroutines do
not both see `ACTIVE` and collide. Across processes, the caller serializes;
a lost race is the 400 above, with nothing written.

### After a write

Unless `NoWait` is set, the write waits for its result:

| Operation | Settled when | Failed when |
|-|-|-|
| `CreateHostedZone` | Zone `ACTIVE` | Zone `ERROR` |
| `UpdateHostedZone` | Zone `ACTIVE` with the sent description and VPCs | Zone `ERROR` |
| `DeleteHostedZone` | Zone `GET` is 404 | |
| `CreateRecord`, `UpdateRecord` | Zone and record both `ACTIVE`, and for an update the record shows the sent fields | Zone or record `ERROR` |
| `DeleteRecord` | Record `GET` is 404 | |

- Settled: the Output holds the last read. An update also checks the sent
  fields, so a read taken before the server leaves `ACTIVE` does not count
  as settled.
- Failed: the call returns the Output with the resource read and an error
  wrapping `ErrFailed` that names the status. For a zone, the message adds
  that every VPC needs Private DNS `ENABLED`, not `ENABLING`.
- Bound reached: the call returns the Output and an error wrapping
  `ErrNotSettled`. The message says the write was accepted and must not be
  repeated.

A non-nil Output with an error is unusual in this SDK, but here the write
happened, and the caller needs the new ID to clean up or check again. The
CLI prints that Output on stdout and the error on stderr.

With `NoWait`, a create returns the `CREATING` resource from its response,
an update returns one read, and a delete returns at once. `ErrNotSettled`
and `ErrFailed` never occur then.

## Private DNS stays in the console

Enabling Private DNS stays a documented console step, not an SDK method in
these releases:

- It is a one-time VPC setting. It changes the VPC's DHCP option set, and
  each VM must renew DHCP to pick up the new resolver, which the SDK cannot
  do.
- Disabling is not verified, so an SDK method could enable but not undo.
- The CLI can already check it: `vngcloud network get-vpc` shows
  `DNSStatus`. The DNS wiki page says to wait for `ENABLED` before creating
  a zone, because an `ENABLING` VPC gives a zone in `ERROR`.

`network.EnableVPCDNS` and `DisableVPCDNS` can follow with their own design
once disable is verified and someone needs to script it.

## CLI

`svc_dns.go` adds six entries to `dnsOps`:

| Command | Kind | Needs `--yes` | Release |
|-|-|-|-|
| `dns create-hosted-zone` | Write | No | `v0.10.0` |
| `dns update-hosted-zone` | Write | No | `v0.10.0` |
| `dns delete-hosted-zone` | Write, destructive | Yes | `v0.10.0` |
| `dns create-record` | Write | No | `v0.11.0` |
| `dns update-record` | Write | No | `v0.11.0` |
| `dns delete-record` | Write, destructive | Yes | `v0.11.0` |

- A [read-only](cli.md#read-only) profile refuses all six with exit 2
  before any request. Deletes need `--yes` (ADR 0002 rule 6).
- The waits live in the SDK, so the CLI needs no `cli.WaitFor`. Each
  command waits by default; `NoWait` becomes `--no-wait` by the usual flag
  reflection.
- Scalar fields are flags: `--domain-name`, `--description`,
  `--hosted-zone-id`, `--record-id`, `--sub-domain`, `--type`, `--ttl`,
  `--routing-policy`, `--sticky-session`, `--no-wait`.
- `VPCIDs` and `Values` are slices, so they come through `--cli-input-json`,
  as vMonitor's `Locations` do. An MX record:

```sh
vngcloud dns create-record --hosted-zone-id <id> --type MX \
  --cli-input-json '{"Values":[{"Value":"10 mx1.example.com"},
                              {"Value":"20 mx2.example.com"}]}'
```

A script that creates many records runs `create-record` once per record,
each from its own `file://record.json`. Each call waits for the zone, so the
loop needs no sleep.

## Errors

| Case | Result | CLI code and exit |
|-|-|-|
| Missing required field, bad ID shape, empty update | `ErrInvalidInput`, no request | `InvalidUsage`, 2 |
| Unknown zone or record, repeat delete | `NotFound` from the server's 404 | `NotFound`, 4 |
| VPC with Private DNS `DISABLED` on zone create | `NotFound` from the server's 404 `is inactive DNS` | `NotFound`, 4 |
| Zone not `ACTIVE` or `ERROR` within the pre-write bound | `ErrZoneBusy`, nothing sent | `ZoneBusy`, 1 |
| Zone lock 400 after the pre-write wait | The server's `*APIError`, nothing written | 1 |
| Resource `ERROR` after a write | `ErrFailed`, with Output | `WriteFailed`, 1 |
| Not settled within the post-write bound | `ErrNotSettled`, with Output | `NotSettled`, 1 |
| Bad record value, duplicate type, zone with records, `NS`/`SOA` delete | The server's `*APIError` | 1 |
| 5xx or network error on a create | The error; the message says to list before rerunning | 1 |

The CLI error codes list in [CLI](cli.md#errors-and-exit-codes) gains
`ZoneBusy`, `WriteFailed`, and `NotSettled`. The CLI checks
`ErrNotSettled` before its cancelled-context rule, so a Ctrl-C during a
post-write wait still reports that the write was accepted.

## Security

- Every write gets an adversarial review before its release. The review
  checks: no create retry after a 5xx or the lock 400; the pre-write wait
  sends nothing; path ID checks on every call; an empty update sends
  nothing; a zone update resends every field the caller did not set, and
  sends `[]` for VPCs only when `VPCIDs` is an explicit empty list; `--yes`
  on both deletes; read-only refusal of all six writes; and that a wait
  never repeats a write.
- Zone names, VPC IDs, record names, and values are account data. TXT
  values may hold verification tokens or DKIM keys. Fixtures replace them
  with `<hostname>`, `<id>`, `<ip>`, and `<secret>`. `--debug` logs no
  body.
- The SDK never enables Private DNS as a side effect.

## Testing

Unit tests use `httptest` and an injected clock:

- A sanitized raw fixture and a decode test for each create response in
  `testdata/dns/`, with `CREATING` status, `assocVpcMapRegion`, and a
  record with an MX value and two TXT values.
- Request bodies: zone create with and without a description; record
  create with defaults (TTL 300, `simple-routing`, apex `""`) and with
  every field; `location` and `weight` omitted when nil; an update with
  only `TTL` sends only `ttl`; an empty update sends nothing; a zone
  update of only `Description` resends the read VPC IDs, one of only
  `VPCIDs` resends the read description, and `VPCIDs` as an empty list
  sends `[]` while nil never does.
- Waits: pre-write wait through a locked zone to `ACTIVE`; `ERROR` accepted
  before a write; `ErrZoneBusy` at the bound with no write sent; each
  post-write outcome in [After a write](#after-a-write), including
  `ErrFailed` and `ErrNotSettled` with a non-nil Output; `NoWait` sends no
  post-write read; poll count and 2-second spacing; a cancelled context.
- Two goroutines writing through one `Client` never overlap.
- Statuses: 200 on create, 204 on update and delete, 400, 404, 409, and
  5xx; no create retry after a 502 or the lock 400; a create response
  without an ID fails.
- Path ID rejection for `..`, `.`, `/`, and empty on all ten operations.
- CLI golden tests for the six commands; `--yes` on both deletes;
  read-only refusal with no request sent; `--no-wait`; a
  `--cli-input-json` merge that supplies `Values`; the three new error
  codes, with the Output on stdout for `NotSettled` and `WriteFailed`.

Live tests follow [live data](../../instructions/live-data.md). Each write
run needs the owner's approval naming the account, region, and VPC.

- `make live` adds `ListRecords` on the first zone when one exists,
  logging counts only.
- The live write test skips unless `VNGCLOUD_LIVE_DNS_VPC_ID` names a VPC
  whose `dnsStatus` is `ENABLED`; it checks with `network.GetVPC` and never
  logs the ID. It deletes leftover zones named `vngcloud-live-*.internal`
  with their user records, then creates `vngcloud-live-<8 hex>.internal`.
  Its `t.Cleanup`, registered as soon as the zone ID is known, deletes the
  user records, then the zone, with its own context, and asserts none
  remain. If the create fails, it lists by name and deletes a match.
- The zone release's test creates, updates the description of, and
  deletes the zone. The record release's test adds an A record with two
  values, an MX with two priorities, and a TXT with two strings, updates
  the A record's TTL, and deletes all three, logging only statuses, counts,
  and wait times.

## Releases

| Version | Content |
|-|-|
| `v0.10.0` | `CreateHostedZone`, `UpdateHostedZone`, `DeleteHostedZone`, the zone waits and sentinels, and their CLI commands; path ID checks on the zone reads |
| `v0.11.0` | `CreateRecord`, `UpdateRecord`, `DeleteRecord`, the pre-write wait and mutex, and their CLI commands; path ID checks on the record reads |

Zones come first so the record live test can create its own zone. Neither
release changes an existing method or command, except that the reads now
reject a malformed ID before sending it. A `DNS` SDK wiki page covers
private-only zones, the Private DNS console step and `dnsStatus`, the
waits, `NoWait`, partial updates, and listing before rerunning a create.

## Owner decisions

1. Settled: `aboutme.vn` stays off vDNS, which has no public zone.
2. Approved: build private zone and record writes.
3. Approved: zones in `v0.10.0`, records in `v0.11.0`.
4. Approved, revised by the live checks: update Inputs use pointers. A
   record update sends only the non-nil fields, because the API applies a
   partial record body; a zone update is the read-merge in decision 11.
5. Approved: `Values` and `VPCIDs` go through `--cli-input-json`.
6. Approved: no bulk import or sync command.
7. Approved: no quote, pending the next-day bill check.
8. Approved: Private DNS stays a documented console step, and
   `network` gains no enable or disable method in these releases.
9. Approved: the SDK waits before every write (always) and after it
   (unless `NoWait`), polling every 2 seconds for up to 60 seconds; the CLI
   waits by default and takes `--no-wait`.
10. Approved: a post-write `ErrFailed` or `ErrNotSettled` returns the
    Output with the error, so the caller keeps the new ID.
11. Settled by the API, which replaces the zone: `UpdateHostedZone` reads
    the zone and sends a full body; unset fields keep their current values.
12. Settled by ADR 0002 rule 6, since another update re-attaches: detaching
    every VPC needs an explicit empty `VPCIDs` list, and no `--yes`.

## Open questions

- A vDNS line on the next day's bill.
- The `disableDns` call, needed only if Private DNS moves into the SDK.
- Whether GreenNode plans public zones.
