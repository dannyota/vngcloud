# DNS

`dns` is `danny.vn/vngcloud/dns`, with its own `New(cfg)`. vDNS hosts
private zones only: a zone resolves inside the VPCs associated with it, and
nothing else. There is no public zone type, so a zone cannot take over a
public domain such as `example.com`; keep public DNS elsewhere and use vDNS
for private names inside a VPC.

`dns` reads and writes hosted zones (`ListHostedZones`, `GetHostedZone`,
`CreateHostedZone`, `UpdateHostedZone`, `DeleteHostedZone`) and their
records (`ListRecords`, `GetRecord`, `CreateRecord`, `UpdateRecord`,
`DeleteRecord`).

## Setup

```go
package main

import (
	"context"
	"log"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/dns"
)

func main() {
	ctx := context.Background()

	cfg, err := vngcloud.NewConfig(
		vngcloud.WithRegion("hcm-3"),
		vngcloud.WithIAMUser(&vngcloud.IAMUserAuth{
			RootEmail: "<root-email>",
			Username:  "<iam-username>",
			Password:  "<password>",
		}),
	)
	if err != nil {
		log.Fatal(err)
	}

	client := dns.New(cfg)
	_ = ctx
	_ = client
}
```

The rest of this page assumes `cfg` and `ctx` from this setup, plus
`client := dns.New(cfg)`.

## Reading zones and records

```go
zones, err := client.ListHostedZones(ctx, nil)      // Name, Page, Size
zone, err := client.GetHostedZone(ctx, in)          // HostedZoneID (required)
records, err := client.ListRecords(ctx, in)         // HostedZoneID (required), Name
record, err := client.GetRecord(ctx, in)             // HostedZoneID, RecordID (both required)
```

`HostedZone.Status` is `CREATING`, `ACTIVE`, `UPDATING`, or `ERROR`; the
constants `dns.StatusCreating`, `dns.StatusActive`, `dns.StatusUpdating`, and
`dns.StatusError` name them. `HostedZone.AssociatedVPCIDs` lists the zone's
VPCs, and `AssocVPCMapRegion` pairs each one with its region.

## Private DNS on the VPC

Every VPC a zone associates with needs Private DNS on. Check
`network.GetVPC`'s `VPC.DNSStatus`: it goes `DISABLED`, `ENABLING`,
`ENABLED`. Enabling it is a console step, not an SDK method; see the
[Network](Services.md#network) page for `GetVPC`.

- Creating a zone for a `DISABLED` VPC returns a 404 that
  `vngcloud.IsNotFound(err)` reports true for.
- Creating one for an `ENABLING` VPC succeeds, but the zone then reaches
  `StatusError` within a second: wait for `ENABLED` before creating a zone
  against a VPC, not just for `ENABLING` to have started.

## Creating, updating, and deleting zones

```go
created, err := client.CreateHostedZone(ctx, &dns.CreateHostedZoneInput{
	DomainName: "app.internal",
	VPCIDs:     []string{vpcID},
})
if err != nil {
	log.Fatal(err)
}
log.Println(created.HostedZone.ID)

updated, err := client.UpdateHostedZone(ctx, &dns.UpdateHostedZoneInput{
	HostedZoneID: created.HostedZone.ID,
	Description:  vngcloud.Ptr("app zone"),
})

if _, err := client.DeleteHostedZone(ctx, &dns.DeleteHostedZoneInput{
	HostedZoneID: created.HostedZone.ID,
}); err != nil {
	log.Fatal(err)
}
```

`CreateHostedZone` always sends type `PRIVATE`, the only type vDNS offers.
It is a `POST` and is never retried after a failure that may already have
reached the server: after any error that is not a 4xx `*vngcloud.APIError`
or `vngcloud.ErrInvalidInput`, the zone may exist. Call `ListHostedZones`
with `Name` and look for the domain before creating it again, rather than
retrying blind.

`UpdateHostedZone` changes a zone's `Description`, its `VPCIDs`, or both;
at least one must be set, or the call fails with `vngcloud.ErrInvalidInput`
and sends nothing. The API takes a full replacement body, so the SDK reads
the zone first and resends whatever field the caller left `nil` unchanged:
an unset `Description` keeps the current one, and an unset `VPCIDs` keeps
the current VPCs.

`VPCIDs` pointing at a non-nil, empty slice is different from `VPCIDs` left
`nil`: the empty slice sends an explicit `[]` and detaches every VPC from
the zone, which then resolves in none of them.

```go
// Detach every VPC on purpose.
client.UpdateHostedZone(ctx, &dns.UpdateHostedZoneInput{
	HostedZoneID: zoneID,
	VPCIDs:       vngcloud.Ptr([]string{}),
})
```

This is not destructive in the sense the CLI's `--yes` rule cares about,
because another update re-attaches VPCs, but it does take the zone
offline until one does.

`DeleteHostedZone` fails with the server's own 400 if the zone still holds
any record other than the server's own `NS` and `SOA`; the SDK does not
delete records first. `DELETE` is idempotent, and a retry that finds the
zone already gone returns `vngcloud.IsNotFound(err) == true`, not an error
worth treating specially.

## Creating, updating, and deleting records

```go
created, err := client.CreateRecord(ctx, &dns.CreateRecordInput{
	HostedZoneID: zoneID,
	Type:         "A",
	Values:       []dns.RecordValue{{Value: "10.0.0.1"}, {Value: "10.0.0.2"}},
})
if err != nil {
	log.Fatal(err)
}
log.Println(created.Record.ID)

updated, err := client.UpdateRecord(ctx, &dns.UpdateRecordInput{
	HostedZoneID: zoneID,
	RecordID:     created.Record.ID,
	TTL:          vngcloud.Ptr(120),
})

if _, err := client.DeleteRecord(ctx, &dns.DeleteRecordInput{
	HostedZoneID: zoneID,
	RecordID:     created.Record.ID,
}); err != nil {
	log.Fatal(err)
}
```

`SubDomain` left empty on `CreateRecord` creates the zone apex; the server
rejects `"@"` for the same purpose with a 400. `TTL` left zero sends `300`,
the console default, and `RoutingPolicy` left empty sends
`"simple-routing"`; the server also accepts `"weighted"` and
`"geolocation"`. `Record.SubDomain` always comes back as the full name
(`"www.app.internal"`, or the zone's own domain name for the apex), never
the label a caller sent.

Only one record of a given `Type` may exist per subdomain; a second create
fails with the server's own 400 (`"Type must be unique in one subdomain."`).
Several values, such as a weighted record's two targets or a `TXT`
record's two strings, go in one `Values` slice; an `MX` value is the whole
string `"10 mx1.example.com"`. The server's own `NS` and `SOA` records
cannot be deleted.

`CreateRecord` is a `POST` and is never retried after a failure that may
already have reached the server, for the same reason `CreateHostedZone` is
not; call `ListRecords` with `Name` and look for the record before creating
it again, rather than retrying blind.

`UpdateRecordInput`'s fields are all pointers, and `UpdateRecord` sends only
the ones set. Unlike `UpdateHostedZone`, the server itself applies a
partial body to a record, so the SDK does no read-merge first. At least one
field must be set, or the call fails with `vngcloud.ErrInvalidInput` and
sends nothing.

`DeleteRecord` is idempotent like `DeleteHostedZone`; a retry that finds the
record already gone returns `vngcloud.IsNotFound(err) == true`.

Every record create or update, like a zone update that changes its VPCs,
locks the zone for several seconds: any other record write started in that
window fails with the server's own 400. `CreateRecord`, `UpdateRecord`, and
`DeleteRecord` each wait for the zone to clear before sending anything (see
Waits below), so calling several of them back to back needs no sleep of
its own.

### Waits

vDNS writes are asynchronous. Every `UpdateHostedZone` and
`DeleteHostedZone` call, and every record write (`CreateRecord`,
`UpdateRecord`, `DeleteRecord`), first waits for its zone to reach
`StatusActive` or `StatusError`, the two states the server accepts a write
against; this always runs, since nothing has been sent yet, and it turns
the server's zone-lock 400 into a short wait instead. If the zone stays
busy (`CREATING` or `UPDATING`) past the wait's bound, the call returns
`dns.ErrZoneBusy` and sends nothing; running it again is safe.

Unless `NoWait` is set on the Input, a write also waits for its own result
after sending it:

| Call | Settled when | Failed when |
|-|-|-|
| `CreateHostedZone` | Zone `StatusActive` | Zone `StatusError` |
| `UpdateHostedZone` | Zone `StatusActive` with the sent fields | Zone `StatusError` |
| `DeleteHostedZone` | A `GetHostedZone` read returns not-found | |
| `CreateRecord` | Zone and record both `StatusActive` | Zone or record `StatusError` |
| `UpdateRecord` | Zone and record both `StatusActive`, record showing the sent fields | Zone or record `StatusError` |
| `DeleteRecord` | A `GetRecord` read returns not-found | |

For `UpdateRecord`, a sent `SubDomain` is checked against the zone's
`DomainName`, since the server returns `Record.SubDomain` as the full name
rather than the label sent.

A wait polls every 2 seconds for up to 60 seconds of elapsed time. If the
zone or the record reaches `StatusError`, the call returns an error
wrapping `dns.ErrFailed`. Once the write itself has succeeded, every later
failure, the bound running out, a read failing, or the wait's own sleep
ending early, such as from a canceled `ctx`, returns an error wrapping
`dns.ErrNotSettled`: the write may have landed and the SDK could not
confirm it, so do not send it again. Every one of these outcomes still
returns a non-nil Output: it holds the last resource a read returned, or,
when no read after the write ever succeeded, the resource the write's own
response or sent fields carried, so the caller keeps its id to check on it
later:

```go
created, err := client.CreateHostedZone(ctx, in)
switch {
case errors.Is(err, dns.ErrFailed):
	log.Printf("zone %s failed: %s", created.HostedZone.ID, created.HostedZone.Status)
case errors.Is(err, dns.ErrNotSettled):
	log.Printf("zone %s may exist; check it later, do not retry", created.HostedZone.ID)
case err != nil:
	log.Fatal(err)
}
```

With `NoWait` set, a create returns the `StatusCreating` resource from its
own create response, an update returns after one read (not a poll loop),
and a delete returns at once after the delete request succeeds; none of
the six writes ever returns `ErrFailed` or `ErrNotSettled` in that case.

Within one process, a `dns.Client` runs its writes, zone and record alike,
one at a time, covering a call's entire pre-write wait, write, and
post-write wait, so two goroutines sharing a `Client` never race the zone
lock against each other. Across processes, or across two `Client` values,
the caller serializes; a lost race there is the zone-lock 400 above, with
nothing written.

`UpdateHostedZone` reads the zone and resends every field as a full
replacement, so a race between two processes updating the same zone is not
merged: whichever write reaches the server last simply overwrites the
other's change, since the API takes no condition field to detect it.
`UpdateRecord` sends only the fields set, so it merges with a concurrent
change to a different field of the same record; two updates to the same
field still have the last one win.

### Errors

```go
var ErrZoneBusy   = errors.New("dns: zone busy")
var ErrNotSettled = errors.New("dns: write accepted but not settled")
var ErrFailed     = errors.New("dns: write failed on the server")
```

`ErrZoneBusy` means the pre-write wait never saw the zone leave `CREATING`
or `UPDATING`; nothing was sent, and calling the same method again is
always safe.

`ErrFailed` and `ErrNotSettled` mean the write itself was sent; see
[Waits](#waits) above for what each means and why the Output still holds
the zone or record.

## Endpoint

The vDNS API defaults to `https://vdns.console.greennode.ai/vdns-api/`.
Override it with `DNS` on `vngcloud.EndpointOverrides`, passed through
`vngcloud.WithEndpointOverrides`. Zone and record paths carry no project
ID; they are per account, not per project.
