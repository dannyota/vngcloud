# Compute

`compute` is `danny.vn/vngcloud/compute`, with its own `New(cfg)`. It reads
vServer instances and images; reads, creates, updates, and deletes server
groups; reads, imports, creates, and deletes SSH keys; and orders, deletes,
starts, stops, reboots, and renames servers.

## Setup

```go
package main

import (
	"context"
	"log"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/compute"
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

	client := compute.New(cfg)
	_ = ctx
	_ = client
}
```

The rest of this page assumes `cfg` and `ctx` from this setup, plus
`client := compute.New(cfg)`.

## Reading

```go
servers, err := client.ListServers(ctx, in)     // Page, Size
server, err := client.GetServer(ctx, in)        // ServerID (required)
keys, err := client.ListSSHKeys(ctx, in)        // Name, Page, Size
key, err := client.GetSSHKey(ctx, in)           // SSHKeyID (required)
groups, err := client.ListServerGroups(ctx, in) // Name, Page, Size
group, err := client.GetServerGroup(ctx, in)    // ServerGroupID (required)
osImages, err := client.ListOSImages(ctx, in)   // ZoneID
gpuImages, err := client.ListGPUImages(ctx, nil)
userImages, err := client.ListUserImages(ctx, in) // Page, Size
```

`ListServerSecurityGroups` and `ListServerGroupMembers` flatten nested data
already returned by `ListServers` and `ListServerGroups`; see
[Services](Services.md#compute) for the full read method list.
`ListServerGroups`' own `Name` filter matches by substring, not exactly: a
search for `"web"` also finds `"webhook"`. Any code that must find one
group by name lists and scans for an exact match itself.

## Creating, starting, stopping, rebooting, and deleting servers

Creating a server charges the account: a prepaid account pays one month's
price from its credit wallet when the server is created, and deleting one
refunds the unused value. The smallest server this SDK can create (1 vCPU,
2 GB, 20 GB SSD root) quotes about 347,800 VND a month; see [Billing and
Pricing](Billing-and-Pricing.md#vserver-prices).

```go
quote, err := client.QuoteCreateServer(ctx, &compute.CreateServerInput{
	Name: "web-1", ZoneID: "<zone-id>", FlavorID: "<flavor-id>", ImageID: "<image-id>",
	VPCID: "<vpc-id>", SubnetID: "<subnet-id>", SecurityGroupIDs: []string{"<security-group-id>"},
	SSHKeyID: "<ssh-key-id>", RootDiskSize: 20, RootDiskTypeID: "<volume-type-id>",
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
caller's explicit consent to pay that price, the same role the CLI's
`--max-price` flag plays for `vngcloud compute create-server`. Before any
request, `CreateServer` also rejects a `NaN`, `+Inf`, `-Inf`, or negative
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
response body; it is never sent to `QuoteCreateServer`'s quote, logged, or
echoed in any error, and the SDK base64-encodes it itself.

`CreateServer` builds one request body from `Input` and sends a copy of it,
with `UserData` cleared, to `QuoteCreateServer`'s own quote endpoint first,
so the quote always prices the exact server the create would make. The
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
`DeleteVolumes` false (the default), its volumes, including the boot
volume, stay and keep being billed: `KeptVolumeIDs` names every volume
still attached after the delete settles, since the CLI's `--yes` flag
consents only to deleting the server, not silently losing data on
volumes still costing money; pass `DeleteVolumes` (`--delete-volumes` on
the CLI) to delete them with the server. `DELETE` keeps the transport's
normal retries. Unless `NoWait` is set, `DeleteServer` then waits up to 10
minutes, polling every 5 seconds, for the server to be gone; `ERROR` wraps
`compute.ErrFailed`, and the bound running out wraps
`compute.ErrNotSettled`. A rerun is always safe, since `DeleteServer`
always reads first.

If a server is managed by OpenTofu or Terraform, a write made here drifts
from that state; keep such a server's writes in its own tool.

## Creating, updating, and deleting server groups

```go
policies, err := client.ListServerGroupPolicies(ctx, nil)
if err != nil {
	log.Fatal(err)
}

created, err := client.CreateServerGroup(ctx, &compute.CreateServerGroupInput{
	Name:     "web-tier",
	PolicyID: policies.Items[0].UUID,
})
if err != nil {
	log.Fatal(err)
}
log.Println(created.ServerGroup.UUID)

updated, err := client.UpdateServerGroup(ctx, &compute.UpdateServerGroupInput{
	ServerGroupID: created.ServerGroup.UUID,
	Description:   vngcloud.Ptr("web tier, edge servers"),
})
if err != nil {
	log.Fatal(err)
}
log.Println(updated.ServerGroup.Description)

if _, err := client.DeleteServerGroup(ctx, &compute.DeleteServerGroupInput{
	ServerGroupID: created.ServerGroup.UUID,
}); err != nil {
	log.Fatal(err)
}
```

A group's policy is set at create and cannot change; `ListServerGroupPolicies`
lists the choices. Server group names are unique per project; a repeat name
fails with the server's own message.

`CreateServerGroup` is a `POST` and is never retried after a failure that
may already have reached the server: after any error that is not a 4xx
`*vngcloud.APIError`, the group may exist. List server groups and match the
name exactly before creating it again, rather than retrying blind. A create
is synchronous: the response already carries the finished group, so there
is no wait.

`UpdateServerGroup` changes `Name`, `Description`, or both; at least one
must be set, and a `Name` that is set must not be the empty string, or the
call fails with `vngcloud.ErrInvalidInput` and sends nothing. The API takes
a full replacement body, so the SDK reads the group first and resends
whichever field the caller left `nil` unchanged, plus a `serverGroupId`
field set to the same id as the path (the `PUT` fails without it), then
reads the group once more after the `PUT` to build the Output from a shape
the SDK trusts rather than the `PUT` response itself. If that confirm read
fails, the write has already succeeded: the error wraps
`compute.ErrNotSettled`. Unlike `CreateServerGroup`'s `POST`, this update is
a read-merge `PUT` and is safe to run again after `ErrNotSettled`; it will
just resend the same Name and Description. The Output falls back to the
fields the `PUT` itself sent. There is no `PolicyID` field on the update; a
group's policy cannot change after create.

`DeleteServerGroup` lists server groups first, since `GetServerGroup` never
returns a group's members, and sends nothing when that list shows the group
has any server attached (`compute.ErrServerGroupInUse`). A group can be in
use by more than servers, so the server's own refusal is the final guard:
an error whose message contains `"server group is in use"` also wraps
`compute.ErrServerGroupInUse`, whatever its HTTP status. `DELETE` keeps the
transport's normal retries; a retry sent after the first response was lost
can find the group already gone and get back a 404, which
`vngcloud.IsNotFound(err) == true` reports the same as any other unknown
id. That 404 means the group is gone, not that the delete failed.

`GetServerGroup` on an unknown id gets a 200 with `"data"` null rather than
a 404; `GetServerGroup` treats that the same way, returning
`vngcloud.IsNotFound(err) == true` instead of an empty `ServerGroup`.

If a group is managed by OpenTofu or Terraform, a write made here drifts
from that state; keep such a group's writes in its own tool.

## SSH keys

An SSH key is either imported from a public key made elsewhere, or created
by having GreenNode generate the key pair and hand back the private key
once. **Prefer `ImportSSHKey`**: generate a key pair yourself, such as with
`ssh-keygen -t rsa -b 3072`, and import only the public half, so the
private key never leaves your machine. Use `CreateSSHKey` only when you
need GreenNode to generate the key pair itself; in that case GreenNode has
generated and seen the private key.

The server accepts RSA public keys only: an ED25519 key (`ssh-ed25519`,
such as from `ssh-keygen -t ed25519`) is rejected with a 400 "Invalid
public key".

```go
imported, err := client.ImportSSHKey(ctx, &compute.ImportSSHKeyInput{
	Name:      "laptop",
	PublicKey: "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABgQ... me@laptop",
})
if err != nil {
	log.Fatal(err)
}
log.Println(imported.SSHKey.ID)

created, err := client.CreateSSHKey(ctx, &compute.CreateSSHKeyInput{Name: "generated"})
if err != nil {
	log.Fatal(err)
}
log.Println(created.SSHKey.ID)
privateKeyPEM := created.PrivateKey.Reveal() // an OpenSSH private key, PEM-encoded

if _, err := client.DeleteSSHKey(ctx, &compute.DeleteSSHKeyInput{
	SSHKeyID: created.SSHKey.ID,
}); err != nil {
	log.Fatal(err)
}
```

`ImportSSHKey`'s `PublicKey` must be one line after trimming leading and
trailing whitespace, and must not contain the text `PRIVATE KEY`; both
checks run before any request, and neither one's error ever repeats the
value back. Key type and size are not checked by the SDK; the server
decides what it accepts, and currently accepts RSA keys only.

`SSHKey` has no field for a private key, on any call: a read can never
carry one back, matching what GreenNode's API itself returns. Only
`CreateSSHKeyOutput` carries one, as `PrivateKey`, separate from `SSHKey`.
It is an OpenSSH private key, PEM-encoded starting
`-----BEGIN OPENSSH PRIVATE KEY-----`, not a PKCS#1 or PKCS#8 key.

Both `ImportSSHKey` and `CreateSSHKey` are `POST` and are never retried
after a failure that may have already reached the server: after any error
that is not a 4xx `*vngcloud.APIError`, the key may exist. Call
`ListSSHKeys` with `Name` and look for it before calling either one again,
rather than retrying blind; a key found that way after a failed
`CreateSSHKey` has already lost its private key and should be deleted.

`DeleteSSHKey` is idempotent. A retry that finds the key already gone gets
a 400 "Cannot get ssh key with id \<id\>" from the server, not a 404;
`DeleteSSHKey` recognizes that message and returns
`vngcloud.IsNotFound(err) == true` for it, same as any other unknown-id
response. `GetSSHKey` on an unknown id gets a 200 with an empty object
rather than a 404 or 400; `GetSSHKey` treats that the same way, returning
`vngcloud.IsNotFound(err) == true` instead of an empty `SSHKey`.

### The private key is a Secret

`CreateSSHKeyOutput.PrivateKey` is a `vngcloud.Secret`, not a plain string.
Printing it with an ordinary `fmt` verb (`%v`, `%+v`, `%#v`, `%s`, and so
on), logging it through `log/slog`, or encoding it with `json.Marshal` all
give `"[redacted]"`; this holds even when the `Secret` is a field of a
larger struct, such as the whole `CreateSSHKeyOutput`. `Reveal()` is the
only way to read the actual value back out:

```go
fmt.Println(created)               // ...PrivateKey:[redacted]
fmt.Printf("%+v\n", created)        // same
log.Println(created.PrivateKey)     // [redacted]
data, _ := json.Marshal(created)    // {"SSHKey":{...},"PrivateKey":"[redacted]"}

key := created.PrivateKey.Reveal()  // the private key, in PEM form
```

Three things bypass the redaction, none of them through the methods above:
`fmt.Printf("%p", ...)` on a `Secret` prints the value itself, in fmt's own
error text for a verb it does not support (`%!p(vngcloud.Secret=...)`);
`encoding/gob` encodes the underlying string directly; and
`reflect.Value.String()` called on a `Secret` value returns the string
itself rather than calling `String()`. Never format a `Secret` with `%p`,
gob-encode one, or read one back with `reflect.Value.String()`.

Nothing in the SDK writes a `Secret`'s value to a file for you; save
`Reveal()`'s result yourself, with a mode that keeps it readable only by
you (`0600` on Unix), and never print or log it once read.

## Errors

A malformed `SSHKeyID`, `ServerGroupID`, `PolicyID`, or `ServerID`, an empty
required field, an invalid `MaxPrice`, an empty `UpdateServerGroup` input, an
`UpdateServerGroup` `Name` set to the empty string, or an `ImportSSHKey`
shape refusal fails with `vngcloud.ErrInvalidInput` before any request. An
unknown key, server group, or server fails with
`vngcloud.IsNotFound(err) == true`.

```go
var ErrServerGroupInUse   = errors.New("compute: server group in use")
var ErrNotSettled         = errors.New("compute: write accepted but not settled")
var ErrFailed             = errors.New("compute: resource reached ERROR")
var ErrUnexpectedStatus   = errors.New("compute: unexpected status")
```

`ErrServerGroupInUse` means `DeleteServerGroup` sent nothing because the
group has servers attached, or that its `DELETE` request was refused by
the server; see [Creating, updating, and deleting server
groups](#creating-updating-and-deleting-server-groups) above.
`ErrNotSettled` means a write's confirm read or wait after a successful
request failed to come back; `UpdateServerGroup`'s update is safe to run
again either way, since it is a read-merge `PUT` that resends both fields;
`CreateServer` must not be repeated, since the server already exists, while
`StartServer`, `StopServer`, `RebootServer`, and `DeleteServer` are safe to
run again, since each reads first. `ErrFailed` means a create, toggle, or
delete's wait observed the server reach `ERROR`. `ErrUnexpectedStatus`
means `StartServer`, `StopServer`, or `RebootServer` read a status that
call does not act on, such as a reboot of a `STOPPED` server; nothing was
sent.

A duplicate name, a quota, a billing refusal, or a rejected key, policy,
flavor, or image shape from the server itself comes back as the server's
own `*vngcloud.APIError`; see [Errors](Errors.md) for the general error
model.
