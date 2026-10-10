# Volume

`volume` is `danny.vn/vngcloud/volume`, with its own `New(cfg)`. It reads
vServer volumes, volume types, snapshots, and snapshot policies, and orders
and deletes volumes. See [Services](Services.md#volume) for other read methods.

## Setup

```go
package main

import (
	"context"
	"log"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/volume"
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

	client := volume.New(cfg)
	_ = ctx
	_ = client
}
```

The rest of this page assumes `cfg` and `ctx` from this setup, plus
`client := volume.New(cfg)`.

## Listing volume types

```go
types, err := client.ListVolumeTypes(ctx, &volume.ListVolumeTypesInput{
	ZoneID: "<zone-id>",
	IOPS:   3000,
})
if err != nil {
	log.Fatal(err)
}
for _, t := range types.Items {
	log.Println(t.ID, t.Name, t.MinSize, t.MaxSize)
}
```

`ListVolumeTypesInput` takes at most one of `VolumeTypeZoneID` and `ZoneID`;
both fails with `vngcloud.ErrInvalidInput`. With neither, the call lists the
project's volume types. With `ZoneID`, the SDK lists the volume type zones,
keeps those whose zone is `ZoneID`, then lists each one's types one at a
time: `1 + M` requests for `M` volume type zones. Rows follow the volume
type zone order, then the API's order inside each zone, and carry their
`VolumeTypeZoneID` (the SDK fills it when the API leaves it empty). A
zone with no types adds no rows. The first failing request ends the call
with its error and no rows.

`IOPS` keeps only types whose `IOPS` equals it, in every mode. 0 means no
filter, and a negative value fails with `vngcloud.ErrInvalidInput`.

## Creating and deleting volumes

Creating a volume charges the account: a prepaid account pays one month's
price from its credit wallet when the volume is created, and deleting one
refunds the unused value. There is no hourly rate; a quote is VND a month. A
10 GB SSD volume, the smallest this SDK creates in one call, quotes about
32,000 VND a month; see [Billing and
Pricing](Billing-and-Pricing.md#vserver-prices) for the fuller price table.

```go
quote, err := client.QuoteCreateVolume(ctx, &volume.CreateVolumeInput{
	ZoneID: "<zone-id>", Size: 10, VolumeTypeID: "<volume-type-id>",
})
if err != nil {
	log.Fatal(err)
}
log.Printf("%.0f VND/month", quote.OptimumPrice)

created, err := client.CreateVolume(ctx, &volume.CreateVolumeInput{
	Name: "vngcloud-my-data", ZoneID: "<zone-id>", Size: 10, VolumeTypeID: "<volume-type-id>",
	MaxPrice: quote.OptimumPrice,
})
switch {
case errors.Is(err, vngcloud.ErrPriceAboveMax):
	log.Fatal("quoted price exceeds MaxPrice; raise MaxPrice to order it anyway")
case err != nil:
	log.Fatal(err)
}
log.Println(created.Volume.UUID, created.MonthlyPrice)

if _, err := client.DeleteVolume(ctx, &volume.DeleteVolumeInput{
	VolumeID: created.Volume.UUID,
}); err != nil {
	log.Fatal(err)
}
```

`QuoteCreateVolume` requires only `ZoneID`, `Size`, and `VolumeTypeID`, the
fields that change the price; `Name` is optional there. `CreateVolume` quotes
the same priced-only body for its price guard and still requires `Name`.

`CreateVolume`'s `MaxPrice` is VND a month and defaults to 0, so
`CreateVolumeInput{Name: "x", ZoneID: z, Size: 10, VolumeTypeID: t}` alone
always refuses with `vngcloud.ErrPriceAboveMax` and orders nothing: raising
`MaxPrice` to the quote's own `OptimumPrice` is the caller's explicit
consent to pay that price, the same role the CLI command plays (see
[CLI-Volume](CLI-Volume.md)). A quote of 0 also orders nothing: it refuses
with `vngcloud.ErrUnpriced`, since no volume is free. Before any request,
`CreateVolume` also rejects a `NaN`, `+Inf`, `-Inf`, or negative `MaxPrice`
with `vngcloud.ErrInvalidInput`, since none of those compares safely
against a quote. It then lists volumes by `Name` and refuses, also with
`vngcloud.ErrInvalidInput`, when one already exists with that exact name
(the list's own `Name` filter matches by substring, so `CreateVolume` still
scans for an exact match itself), so a rerun after an unclear failure never
risks ordering a second volume under the same name; a name that is only a
substring match, such as ordering `"data-1"` when `"data-10"` already
exists, still creates.

`CreateVolume` builds one request body from `Input` and sends that same
body to `QuoteCreateVolume`'s own quote endpoint first, so the quote always
prices the exact volume the create would make; see [Billing and
Pricing](Billing-and-Pricing.md#quoting-a-paid-write). It refuses with
`vngcloud.ErrPriceAboveMax`, ordering nothing, when the quote's
`OptimumPrice` exceeds `Input.MaxPrice`. The order is a `POST` and is never
retried after a failure that may have already reached the server: after
any error that is not a 4xx `*vngcloud.APIError` or `vngcloud.ErrInvalidInput`,
the volume may exist, and the caller lists volumes by `Name` and matches it
exactly before ordering again, rather than retrying blind.

Unless `NoWait` is set, `CreateVolume` then waits up to 5 minutes, polling
every 2 seconds, for the new volume to reach `AVAILABLE`; a 404 read during
that wait keeps polling, since a volume just created may not be readable at
once. If the volume instead reaches `ERROR`, the returned error wraps
`volume.ErrFailed`; if the bound runs out, or a read or a sleep fails, such
as from a canceled `ctx`, it wraps `volume.ErrNotSettled` and says the
create must not be repeated. Either way `Output.Volume` still holds the
last volume a read returned, falling back to one with only the new UUID and
`Input.Name` when no read ever succeeded. `NoWait` skips that wait and
returns at once with only the UUID and Name filled in.

`DeleteVolume` destroys the volume's data; there is no undo. It reads the
volume first and, when that read shows it `IN-USE` or naming a server,
sends nothing and returns `volume.ErrVolumeInUse`: detach it first. The
server's own in-use refusal is the final guard for a race this pre-check
misses. `DELETE` is idempotent and keeps the transport's normal retries; a
retry that finds the volume already gone comes back as
`vngcloud.IsNotFound(err) == true`. Unless `NoWait` is set, `DeleteVolume`
then waits up to 5 minutes, polling every 2 seconds, for a 404 or a read
showing `Status` `DELETED`; `ERROR` wraps `volume.ErrFailed`, and the bound
running out, or a read or a sleep failing, wraps `volume.ErrNotSettled`. A
rerun after either is always safe, since `DeleteVolume` reads first.

## Encrypted volumes

`CreateVolumeInput.EncryptionTypeID` creates an encrypted volume. It is
optional; empty sends no encryption key. Take the ID from
`ListEncryptionTypes`, which returns `aes-xts-plain64_128` and
`aes-xts-plain64_256` on the test account. The SDK sends the ID as
`encryptionType` on the create and on the quote, and refuses an ID outside
`^[A-Za-z0-9_-]+$` with `vngcloud.ErrInvalidInput` before any request. It
does not check the ID against the list: the server refuses an unknown type.

```go
types, err := client.ListEncryptionTypes(ctx, nil)
if err != nil {
	log.Fatal(err)
}
input := &volume.CreateVolumeInput{
	Name: "data-1", ZoneID: zone, Size: 10, VolumeTypeID: typeID,
	EncryptionTypeID: types.Items[0].ID,
}
quote, err := client.QuoteCreateVolume(ctx, input)
```

Encryption does not change a volume's price: a 10 GB SSD volume quotes and
bills 32,000 VND a month with either type, as without one. `GetVolume`
returns `EncryptionType`; `GetUnderlyingVolume` does not carry it, so read
the type back with `GetVolume`. A delete refunds the price, but the refund
can post a few minutes after the delete settles.

An encrypted volume attaches to a server created with encrypted disks (see
[Compute-Servers](Compute-Servers.md#encrypted-disks)). A server with
plain disks refuses the attach with a 400 `BadRequest` whose message says
`cannot attach encryption volume`.

## Attaching and detaching

```go
attached, err := client.AttachVolume(ctx, &volume.AttachVolumeInput{
	VolumeID: created.Volume.UUID, ServerID: "<server-id>",
})
if err != nil {
	log.Fatal(err)
}
log.Println(attached.Changed, attached.Volume.Status)

detached, err := client.DetachVolume(ctx, &volume.DetachVolumeInput{
	VolumeID: created.Volume.UUID, ServerID: "<server-id>",
})
switch {
case errors.Is(err, volume.ErrServerRunning):
	log.Fatal("server is ACTIVE; stop it first, or pass AllowRunning")
case err != nil:
	log.Fatal(err)
}
log.Println(detached.Changed, detached.Volume.Status)
```

`AttachVolume` reads the volume first: already attached to `ServerID`
returns at once with `Changed` false, sending nothing; attached elsewhere,
the `PUT` reaches the server, which refuses it with its own error. The
`PUT` keeps the transport's normal retries: a repeat is refused as already
attached, never a second charge. Unless `NoWait` is set, it then waits up
to 5 minutes, polling every 2 seconds, for the volume to read `IN-USE`
with `ServerID` among its attached servers.

`DetachVolume` reads the volume first: not attached to `ServerID` returns
at once with `Changed` false, sending nothing. Attached, `DetachVolume`
always reads the server next, whether or not `AllowRunning` is set, and
refuses with `volume.ErrBootVolume`, sending nothing, when `VolumeID`
equals the server's own boot volume id, when `Volume.Bootable` says so, or
when the server's read carries no boot volume id at all: a missing id
cannot rule out this being the boot volume, so it fails closed. Unless
`AllowRunning` is set, that same read's status must be `STOPPED`; any
other status, including one this SDK does not recognize or an empty
string, refuses with `volume.ErrServerRunning`, sending nothing, since the
volume may be mounted on a server that is not fully stopped. Stop the
server first, or unmount it yourself and pass `AllowRunning`, the same
role the CLI command plays (see [CLI-Volume](CLI-Volume.md)). The `PUT`
keeps the transport's normal retries. Unless `NoWait` is set, it then
waits up to 5 minutes, polling every 2 seconds, for the volume to read
`AVAILABLE`.

`ListVolumesByServer` lists a server's volumes, boot volume included
(`GET /v2/{project}/volumes/servers/{serverId}`). The rows sit under
`volumes`; the decoder also reads a bare array, `data`, or `listData`. A
live read of a plain server returned its boot volume, with `serverIdList`
empty and `serverId` and `serverNameList` naming the server.

## Resizing

```go
quote, err := client.QuoteResizeVolume(ctx, &volume.ResizeVolumeInput{
	VolumeID: created.Volume.UUID, Size: 20,
})
if err != nil {
	log.Fatal(err)
}

resized, err := client.ResizeVolume(ctx, &volume.ResizeVolumeInput{
	VolumeID: created.Volume.UUID, Size: 20, MaxPrice: quote.OptimumPrice,
})
switch {
case errors.Is(err, vngcloud.ErrPriceAboveMax):
	log.Fatal("quoted price exceeds MaxPrice; raise MaxPrice to order it anyway")
case err != nil:
	log.Fatal(err)
}
log.Println(resized.Volume.Size, resized.MonthlyPrice)
```

`ResizeVolume` only grows: it reads the volume first, and `Size` at or
below the current size fails with `vngcloud.ErrInvalidInput`, sending
nothing, since shrinking would cut off the end of the data. It sends the
volume's current `VolumeTypeID` back as `newVolumeTypeId`, which the API
requires on every resize, so a type never changes by accident. It
otherwise follows `CreateVolume`'s own price guard: `MaxPrice` defaults to
0, and a `NaN`, `+Inf`, `-Inf`, or negative `MaxPrice` is
`vngcloud.ErrInvalidInput` before any request. `QuoteResizeVolume` reads
the volume fresh on every call, independently of `ResizeVolume`'s own
read, to learn its current size and type.

The resize `PUT` is sent at most once (`transport.Request.Once`): a resend
would act on the size read this call already took. A 4xx response proves
the server never acted and is returned as is; any other failure wraps
`volume.ErrNotSettled`, and the recovery is to run `ResizeVolume` again.
Unless `NoWait` is set, it then waits up to 5 minutes, polling every 2
seconds, for a read showing the new size with `Status` `AVAILABLE` or
`IN-USE`. `ERROR` wraps `volume.ErrFailed`.

`ResizeVolume` grows only the block device; the filesystem inside a
server that has the volume attached must still be grown separately, with
whatever tool the guest OS provides (`resize2fs`, `xfs_growfs`, and so
on).

If a volume is managed by OpenTofu or Terraform, a write made here drifts
from that state; keep such a volume's writes in its own tool.

## Snapshot policies

Snapshot backend and policy reads use the vServer backup gateway in `hcm-3`.
Other regions return `vngcloud.ErrInvalidConfig` before authentication,
project discovery, or HTTP access. `EndpointOverrides.VServerBackup` changes
only this gateway and cannot enable another region. Snapshot backend IDs
belong to this gateway and must not be used with Backup Center.

```go
backends, err := client.ListSnapshotBackends(ctx,
	&volume.ListSnapshotBackendsInput{Name: "HCM-03"})
if err != nil {
	log.Fatal(err)
}
for _, backend := range backends.Items {
	log.Println(backend.ID, backend.Name)
}

// Choose the backend explicitly from the lookup results.
policies, err := client.ListSnapshotPolicies(ctx,
	&volume.ListSnapshotPoliciesInput{
		BackendID: "<backend-id>", Page: 1, Size: 10,
	})
if err != nil {
	log.Fatal(err)
}
for _, policy := range policies.Items {
	log.Println(policy.Name, policy.PolicyType)
}
```

`ListSnapshotBackends` requires `Name` and returns an unpaged `Items` list
with backend `ID` and `Name`. It does not derive a backend name from the
region or select the first result. `ListSnapshotPolicies` requires
`BackendID` and uses the configured project. Each call reads one page and
returns `Items`, `Page`, `PageSize`, `TotalPage`, and `TotalItem`. Zero `Page`
and `Size` select 1 and 10. Negative values, malformed backend IDs, nil
inputs, and empty required fields return `vngcloud.ErrInvalidInput` before
access.

`SnapshotPolicy` retains identity, name, type, timestamps, snapshot counts,
and verified config fields. Unknown policy types and status strings survive
decoding. Timestamps and timezone strings remain as received. Hourly and
daily config objects use pointers: missing or null is nil, an empty object
has nil members, and an explicit zero has a non-nil pointer to zero. Interval
and retention units are undocumented.

Policies are summaries, not complete configuration exports. Weekly and
monthly settings are omitted because their field types are unverified;
the enabled flags remain visible. Account identifiers, duplicated backend
and project scope, and the untyped null fields `isDefault` and `deletedAt`
are also omitted. Do not use a summary as a policy write or replacement
body. Snapshot history and policy writes have no SDK methods.

## Errors

```go
var ErrNotSettled     = errors.New("volume: write accepted but not settled")
var ErrFailed         = errors.New("volume: resource reached ERROR")
var ErrVolumeInUse    = errors.New("volume: volume in use")
var ErrBootVolume     = errors.New("volume: cannot detach the boot volume")
var ErrServerRunning  = errors.New("volume: server is running")
```

A malformed `VolumeID`, `VolumeTypeID`, or `ServerID`, an empty required
field, or an invalid `MaxPrice` fails with `vngcloud.ErrInvalidInput`
before any request. An unknown volume fails with
`vngcloud.IsNotFound(err) == true`. A quota, billing refusal, or a
rejected shape from the server itself comes back as the server's own
`*vngcloud.APIError`; see [Errors](Errors.md) for the general error model.
