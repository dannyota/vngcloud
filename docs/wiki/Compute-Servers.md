# Compute Servers

Server console logs and writes for `danny.vn/vngcloud/compute`: creating,
starting, stopping, rebooting, renaming, resizing, and deleting servers. See
[Compute](Compute.md) for reads, server groups, and SSH keys.

This page assumes `cfg`, `ctx`, and `client := compute.New(cfg)` from
[Compute's Setup](Compute.md#setup). [IDs for Create
Server](IDs-for-Create-Server.md) shows how to find each ID below.

## Public address

Read one server's floating IP in one command:

```sh
vngcloud compute get-server --server-id <server-id> \
  --query Server.PublicIP --output text
vngcloud compute list-servers --query 'Items[].{ID:UUID,IP:PublicIP}'
```

The SDK exposes the same value as `out.Server.PublicIP` on `GetServer` and
`out.Items[i].PublicIP` on `ListServers`. Server write results and their
waits derive it by the same rule.

`PublicIP` uses only `InternalInterfaces[].FloatingIP`. Every non-empty
value must parse as a global unicast, non-private address without a zone,
whitespace, or control characters. IPv4-mapped IPv6 is unmapped first.
If those values identify one distinct address, the field holds its
canonical spelling. Invalid or distinct addresses leave it empty.
External Interfaces and an incoming top-level `publicIp` never supply it.
The original interface fields stay unchanged. An empty `PublicIP` does
not prove that no IP is allocated or attached.

Server read and rename responses have an 8 MiB limit before capture or
decoding. Oversized, malformed, or credential-reflecting responses return
an error with no unsafe partial server. Operations track at most 256
credentials across retries, refresh, and nested requests; exhaustion
withholds output. Retained server results are rechecked against later
credentials. Guarded operations suppress response captures, including
nested quote calls. Existing safe partial results and error causes remain
available when a wait fails. Reads retain finite retries; rename retains
normal PUT retries, and actions and resize retain their single-send rules.

CLI JSON, table, text, and queries use the Go field name `PublicIP`.
Standard Go `encoding/json` marshaling omits it through `json:"-"`.
Keyed struct literals remain compatible; strict CLI JSON consumers and
external unkeyed `compute.Server` literals may need updates. The positive
address fixture is schema-built, with documentation addresses, and is not
a populated live capture.

## Server console logs

`GetServerConsoleLog` reads boot and serial output without checking the
server's state first. It requires `ServerID` and sends no query parameters.
An empty log is a successful result.

```go
out, err := client.GetServerConsoleLog(ctx, &compute.GetServerConsoleLogInput{
    ServerID: "<server-id>",
})
if err != nil {
    log.Fatal(err)
}
```

Logs can hold secrets, including passwords and keys. `out.Log` is a
`vngcloud.Secret`: formatting, JSON encoding, and slog redact the value.
`out.Log.Reveal()` returns the text explicitly. The SDK suppresses response
captures and withholds response bodies and upstream messages from errors.

The entire JSON response has an 8 MiB limit, including JSON escaping and
HTTP decompression. A larger body returns `*vngcloud.APIError` with code
`ResponseTooLarge` and no partial output. Invalid response shapes return
`RequestFailed`. The SDK refuses redirects and uses normal GET retries and
cancellation. See [Errors](Errors.md) for status errors.

`ListFlavorPrices` compares only the instance component of a supported Ubuntu
reference server. It excludes the root disk and all other server costs. See
[monthly flavor prices](Compute.md#monthly-flavor-prices) for discovery inputs,
reference selection, unknown RAM units, and partial output. `QuoteCreateServer`
prices the actual configuration; discovery does not replace the paid create's
fresh price guard.

## Creating, starting, stopping, rebooting, and deleting servers

Prices are VND totals that include VAT, with no API VAT breakdown; see
[Billing and Pricing](Billing-and-Pricing.md#price-quotes).

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

`CreateServerInput` requires exactly one of `SSHKeyID` or `UserData` that
installs authorized keys. Both set or neither set returns
`vngcloud.ErrInvalidInput` before any request. GreenNode refuses user data
with an SSH key, username, or password. For user data, leave `SSHKeyID`
empty and put the public key in cloud-config:

```yaml
#cloud-config
ssh_authorized_keys:
  - <public-key>
```

`ssh_authorized_keys` installs the key for the image's default user. The
SDK base64-encodes the cloud-config and sends `userDataBase64Encoded: true`;
pass plain text as `UserData`. The SDK never sets up password login.

`SecurityGroupIDs` must hold at least one id, and the SDK
never picks a default; the project's default security group opens SSH,
RDP, HTTP, HTTPS, and ICMP from anywhere, so naming it is the caller's own
choice, not the SDK's. The SDK never sends `attachFloating`: a public IP
costs 120,000 VND a month and exposes the server, so getting one is not
part of this design. `AutoRenew` defaults to false, so nothing renews from
credit without a later command. `UserData`, when set, makes the create
`transport.Request.Sensitive`, so a decode failure never quotes the
response body; it is never sent to a quote, logged, or
echoed in any error.

`QuoteCreateServer` requires and sends only the fields that change the
price: `ZoneID`, `FlavorID`, `ImageID`, `RootDiskSize`, `RootDiskTypeID`,
the data disk pair when set, and `encryptionVolume` when a disk is encrypted.
`Name`, `VPCID`, `SubnetID`,
`SecurityGroupIDs`, and both login fields are optional on a quote. Neither
`SSHKeyID` nor `UserData` reaches the quote body. A quote still
checks the shape of any of these IDs that is set, so a bad ID fails there as
it will at the create. `Name` has no shape rule and is simply not sent.
`CreateServer` quotes the same priced-only body before it orders, so the
quote you read and the guard it checks price one request.
The create requires its network fields and exactly one login choice. The
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

## Encrypted disks

`RootDiskEncryptionTypeID` and `DataDiskEncryptionTypeID` encrypt the root and
data disks. Both are optional IDs from `volume.ListEncryptionTypes`
(`aes-xts-plain64_128` or `aes-xts-plain64_256`). The SDK sends them as
`rootDiskEncryptionType` and `dataDiskEncryptionType`, and sets
`encryptionVolume` true when either is set. A data disk type without
`DataDiskSize` and `DataDiskTypeID` is `vngcloud.ErrInvalidInput`, and an ID
outside `^[A-Za-z0-9_-]+$` is too, before any request. The server refuses an
unknown type.

```go
in := &compute.CreateServerInput{
	// ... the fields above ...
	RootDiskSize: 20, RootDiskTypeID: diskType,
	RootDiskEncryptionTypeID: "aes-xts-plain64_256",
	DataDiskSize: 20, DataDiskTypeID: diskType,
	DataDiskEncryptionTypeID: "aes-xts-plain64_128",
}
```

The quote sends only `encryptionVolume` true, as the console's price request
does; the type IDs do not change the price. Encryption adds a `CES` line of
30% of the flavor price: `s2-general-1x2` with a 20 GB root quotes 347,800
VND without it and 432,940 VND with it, and the data disk adds its own disk
price on top. Encrypting only the data disk, which needs a data disk, still
sets `encryptionVolume` and adds the same `CES` line. A server created with
both disks encrypted shows an `encryptionType` on the boot volume and on the
data volume in `volume.ListVolumes`. A separately created encrypted volume
attaches to such a server; a server with plain disks refuses it with a 400
`BadRequest` (`cannot attach encryption volume`).

## Resizing a server

```go
quote, err := client.QuoteResizeServer(ctx, &compute.ResizeServerInput{
	ServerID: created.Server.UUID, FlavorID: "<target-flavor-id>",
})
if err != nil {
	log.Fatal(err)
}

resized, err := client.ResizeServer(ctx, &compute.ResizeServerInput{
	ServerID: created.Server.UUID, FlavorID: "<target-flavor-id>",
	MaxPrice: max(quote.OptimumPrice, 0),
})
switch {
case errors.Is(err, vngcloud.ErrPriceAboveMax):
	log.Fatal("quoted price exceeds MaxPrice; raise MaxPrice to order it anyway")
case err != nil:
	log.Fatal(err)
}
log.Println(resized.Server.Flavor.FlavorID, resized.MonthlyPrice)
```

`QuoteResizeServer` is a read and works in CLI read-only mode. Its signed
`OptimumPrice` is the quoted adjustment in VND for the remaining period.
A negative amount is an expected credit, not a confirmed refund. JSON,
table, and text keep the minus sign.

`ResizeServer` reads the server first. The same flavor fails with
`vngcloud.ErrInvalidInput`; a status outside `ACTIVE` or `STOPPED` fails
with `compute.ErrUnexpectedStatus`. Both send nothing. A NaN, infinity,
or negative `MaxPrice` fails before any request. Positive quotes above
`MaxPrice` (default 0) wrap `vngcloud.ErrPriceAboveMax`. Either signed zero
wraps `vngcloud.ErrUnpriced`. A credit passes every valid `MaxPrice`, so a
CLI downsize needs `--yes` but can omit `--max-price`.

`MaxPrice` compares the quote; the API cannot enforce a ceiling on the
final charge. A concurrent flavor, price, or discount change can turn a
quoted credit into a charge even with `MaxPrice` 0. Do not subtract
expected credits from a spending budget or use them to fund another write.
The gateway's exact adjustment formula remains unverified.

The resize is sent once. After an ambiguous failure or failed wait, read
`GetServer` to reconcile before considering another write. Partial Output
retains the signed quote in `MonthlyPrice` and the last server. The quote
is neither the final charge nor proof of a refund.

Unless `NoWait` is set, the SDK polls every 5 seconds for up to 15 minutes
for the new flavor and `ACTIVE` or `STOPPED`. `ERROR` wraps
`compute.ErrFailed`; a timeout wraps `compute.ErrNotSettled`. `NoWait`
returns the pre-resize server with the quote, without confirming the change.

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
