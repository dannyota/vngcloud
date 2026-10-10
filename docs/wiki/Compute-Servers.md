# Compute Servers

Server writes for `danny.vn/vngcloud/compute`: creating, starting,
stopping, rebooting, renaming, resizing, and deleting servers. See
[Compute](Compute.md) for reads, server groups, and SSH keys.

This page assumes `cfg`, `ctx`, and `client := compute.New(cfg)` from
[Compute's Setup](Compute.md#setup). [IDs for Create
Server](IDs-for-Create-Server.md) shows how to find each ID below.

## Creating, starting, stopping, rebooting, and deleting servers

Creating a server charges the account: a prepaid account pays one month's
price from its credit wallet when the server is created, and deleting one
refunds the unused value. The smallest server this SDK can create (1 vCPU,
2 GB, 20 GB SSD root) quotes about 347,800 VND a month; see [Billing and
Pricing](Billing-and-Pricing.md#vserver-prices).

```go
quote, err := client.QuoteCreateServer(ctx, &compute.CreateServerInput{
	ZoneID: "<zone-id>", FlavorID: "<flavor-id>", ImageID: "<image-id>",
	RootDiskSize: 20, RootDiskTypeID: "<volume-type-id>",
})
if err != nil {
	log.Fatal(err)
}

created, err := client.CreateServer(ctx, &compute.CreateServerInput{
	Name: "web-1", ZoneID: "<zone-id>", FlavorID: "<flavor-id>", ImageID: "<image-id>",
	VPCID: "<vpc-id>", SubnetID: "<subnet-id>", SecurityGroupIDs: []string{"<security-group-id>"},
	SSHKeyID: "<ssh-key-id>", RootDiskSize: 20, RootDiskTypeID: "<volume-type-id>",
	MaxPrice: quote.OptimumPrice,
})
switch {
case errors.Is(err, vngcloud.ErrPriceAboveMax):
	log.Fatal("quoted price exceeds MaxPrice; raise MaxPrice to order it anyway")
case err != nil:
	log.Fatal(err)
}
log.Println(created.Server.UUID, created.MonthlyPrice)
```

`CreateServer`'s `MaxPrice` is VND a month and defaults to 0, so an Input
with no `MaxPrice` set always refuses with `vngcloud.ErrPriceAboveMax` and
orders nothing: raising `MaxPrice` to the quote's own `OptimumPrice` is the
caller's explicit consent to pay that price, the same role the CLI command
plays (see [CLI-Compute](CLI-Compute.md)). Before any request,
`CreateServer` also rejects a `NaN`, `+Inf`, `-Inf`, or negative
`MaxPrice` with `vngcloud.ErrInvalidInput`. It then lists every server and
refuses, also with `vngcloud.ErrInvalidInput`, when one already exists with
`Name` exactly (`ListServers` has no name filter of its own, so this scans
every server), so a rerun after an unclear failure never risks ordering a
second server under the same name.

`CreateServerInput` requires `SSHKeyID`: this SDK sets up key login only,
never a password. `SecurityGroupIDs` must hold at least one id, and the SDK
never picks a default; the project's default security group opens SSH,
RDP, HTTP, HTTPS, and ICMP from anywhere, so naming it is the caller's own
choice, not the SDK's. The SDK never sends `attachFloating`: a public IP
costs 120,000 VND a month and exposes the server, so getting one is not
part of this design. `AutoRenew` defaults to false, so nothing renews from
credit without a later command. `UserData`, when set, makes the create
`transport.Request.Sensitive`, so a decode failure never quotes the
response body; it is never sent to a quote, logged, or
echoed in any error, and the SDK base64-encodes it itself.

`QuoteCreateServer` requires and sends only the fields that change the
price: `ZoneID`, `FlavorID`, `ImageID`, `RootDiskSize`, `RootDiskTypeID`,
and the data disk pair when set. `Name`, `VPCID`, `SubnetID`,
`SecurityGroupIDs`, and `SSHKeyID` are optional on a quote. A quote still
checks the shape of any of these IDs that is set, so a bad ID fails there as
it will at the create. `Name` has no shape rule and is simply not sent. `CreateServer` quotes the same priced-only body before
it orders, so the quote you read and the guard it checks price one request.
The create itself still requires every field above. The
order is a `POST` and is never retried after a failure that may have
already reached the server: after any error that is not a 4xx
`*vngcloud.APIError` or `vngcloud.ErrInvalidInput`, the server may exist,
and the caller lists servers and matches `Name` exactly before ordering
again.

Unless `NoWait` is set, `CreateServer` then waits up to 15 minutes, polling
every 5 seconds, for the new server to reach `ACTIVE`. `ERROR` wraps
`compute.ErrFailed`; the bound running out, or a read or a sleep failing,
wraps `compute.ErrNotSettled` and says the create must not be repeated.
`NoWait` returns at once with only the new UUID and `Name` filled in.

```go
stopped, err := client.StopServer(ctx, &compute.StopServerInput{ServerID: created.Server.UUID})
if err != nil {
	log.Fatal(err)
}
log.Println(stopped.Changed, stopped.Server.Status)

started, err := client.StartServer(ctx, &compute.StartServerInput{ServerID: created.Server.UUID})
if err != nil {
	log.Fatal(err)
}

if _, err := client.RebootServer(ctx, &compute.RebootServerInput{ServerID: created.Server.UUID}); err != nil {
	log.Fatal(err)
}

if _, err := client.RenameServer(ctx, &compute.RenameServerInput{
	ServerID: created.Server.UUID, Name: "web-1-renamed",
}); err != nil {
	log.Fatal(err)
}
```

`StartServer` and `StopServer` read the server first: already at the target
status returns `Changed` false, sending nothing. Starting a server that is
not `STOPPED`, or stopping one that is not `ACTIVE`, fails closed with
`compute.ErrUnexpectedStatus`, sending nothing, so a start is never sent to
a server mid-create. `RebootServer` needs `ACTIVE`; any other status is the
same `ErrUnexpectedStatus` refusal. Each toggle is sent at most once
(`transport.Request.Once`): a resend would act on a status read that only
grows staler. A 4xx response after the send proves the server never acted
and is returned as is; any other failure wraps `compute.ErrNotSettled`,
and the recovery is to run the same call again, since it always reads
first. `RebootServer`'s wait needs a read showing `ACTIVE` at least 10
seconds after the send, since an immediate read can still show the
pre-reboot `ACTIVE` state before `REBOOTING` appears. `RenameServer` is
free and keeps the transport's normal `PUT` retries.

```go
deleted, err := client.DeleteServer(ctx, &compute.DeleteServerInput{
	ServerID: created.Server.UUID, DeleteVolumes: true,
})
if err != nil {
	log.Fatal(err)
}
log.Println(deleted.DeletedVolumeIDs)
```

`DeleteServer` reads the server, then lists its volumes with
`volume.ListVolumesByServer`, before sending anything. With
`DeleteVolumes` false (the default), its attached data volumes stay and
keep being billed: `KeptVolumeIDs` names every one still present after
the delete settles, since deleting a server should never silently lose
data on volumes still costing money; pass `DeleteVolumes` to delete them
with the server (see
[CLI-Compute](CLI-Compute.md) for the matching CLI command). `DELETE`
keeps the transport's normal retries. Unless `NoWait` is set,
`DeleteServer` then waits up to 10 minutes, polling every 5 seconds, for
the server to be gone; `ERROR` wraps
`compute.ErrFailed`, and the bound running out wraps
`compute.ErrNotSettled`. A rerun is always safe, since `DeleteServer`
always reads first.

## Resizing a server

```go
quote, err := client.QuoteResizeServer(ctx, &compute.ResizeServerInput{
	ServerID: created.Server.UUID, FlavorID: "<bigger-flavor-id>",
})
if err != nil {
	log.Fatal(err)
}

resized, err := client.ResizeServer(ctx, &compute.ResizeServerInput{
	ServerID: created.Server.UUID, FlavorID: "<bigger-flavor-id>",
	MaxPrice: quote.OptimumPrice,
})
switch {
case errors.Is(err, vngcloud.ErrPriceAboveMax):
	log.Fatal("quoted price exceeds MaxPrice; raise MaxPrice to order it anyway")
case err != nil:
	log.Fatal(err)
}
log.Println(resized.Server.Flavor.FlavorID, resized.MonthlyPrice)
```

`ResizeServer` reads the server first: `FlavorID` equal to the server's
current flavor fails with `vngcloud.ErrInvalidInput`, and a status other
than `ACTIVE` or `STOPPED` fails with `compute.ErrUnexpectedStatus`; both
send nothing. It otherwise follows `CreateServer`'s own price guard:
`MaxPrice` defaults to 0, so `vngcloud.ErrPriceAboveMax` is the result
without one, and a `NaN`, `+Inf`, `-Inf`, or negative `MaxPrice` is
`vngcloud.ErrInvalidInput` before any request. The PUT is sent at most
once (`transport.Request.Once`), the same as `StartServer`: a resend would
act on the flavor read this call already took. A 4xx response proves the
server never acted and is returned as is; any other failure wraps
`compute.ErrNotSettled`, and the recovery is to run `ResizeServer` again.

Unless `NoWait` is set, `ResizeServer` then waits up to 15 minutes,
polling every 5 seconds, for a read showing the new flavor with `Status`
`ACTIVE` or `STOPPED`. `ERROR` wraps `compute.ErrFailed`, and the bound
running out wraps `compute.ErrNotSettled`.

`ResizeServer` never grows the root disk; grow it with
`volume.ResizeVolume` on the server's own `BootVolumeID`, from
`Server.BootVolumeID`.

If a server is managed by OpenTofu or Terraform, a write made here drifts
from that state; keep such a server's writes in its own tool.

## Errors

A malformed `ServerID` or `FlavorID`, an empty required field, or an
invalid `MaxPrice` fails with `vngcloud.ErrInvalidInput` before any
request, as does a `ResizeServer` call naming the server's current flavor
or a `CreateServer` call naming an existing server's exact name. An
unknown server fails with `vngcloud.IsNotFound(err) == true`.

```go
var ErrNotSettled       = errors.New("compute: write accepted but not settled")
var ErrFailed           = errors.New("compute: resource reached ERROR")
var ErrUnexpectedStatus = errors.New("compute: unexpected status")
```

These are the same values [Compute](Compute.md#errors) defines.
`ErrNotSettled` means a write's confirm read or wait after a successful
request failed to come back: `CreateServer` must not be repeated, since
the server already exists, while `StartServer`, `StopServer`,
`RebootServer`, `ResizeServer`, and `DeleteServer` are safe to run again,
since each reads first. `ErrFailed` means a create, toggle, resize, or
delete's wait observed the server reach `ERROR`. `ErrUnexpectedStatus`
means `StartServer`, `StopServer`, or `RebootServer` read a status that
call does not act on, such as a reboot of a `STOPPED` server, or
`ResizeServer` read a status other than `ACTIVE` or `STOPPED`; nothing
was sent.

A quota, billing refusal, or a rejected flavor or image shape from the
server itself comes back as the server's own `*vngcloud.APIError`; see
[Errors](Errors.md) for the general error model.
