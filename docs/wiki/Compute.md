# Compute

`compute` is `danny.vn/vngcloud/compute`, with its own `New(cfg)`. It reads
vServer instances and images; reads, creates, updates, and deletes server
groups; and reads, imports, creates, and deletes SSH keys. Server writes
(create, start, stop, reboot, rename, resize, and delete) are on
[Compute Servers](Compute-Servers.md).

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
flavors, err := client.ListFlavors(ctx, in)     // FlavorZoneID or ZoneID, Name
gpuImages, err := client.ListGPUImages(ctx, nil)
userImages, err := client.ListUserImages(ctx, in) // Page, Size
```

`ListServerSecurityGroups` and `ListServerGroupMembers` flatten nested data
already returned by `ListServers` and `ListServerGroups`; see
[Services](Services.md#compute) for the full read method list.
`ListServerGroups`' own `Name` filter matches by substring, not exactly: a
search for `"web"` also finds `"webhook"`. Any code that must find one
group by name lists and scans for an exact match itself.

## Listing flavors

Set exactly one of `FlavorZoneID` and `ZoneID`; neither, or both, fails with
`vngcloud.ErrInvalidInput` before any request. `Name` keeps only flavors
whose name equals it exactly.

```go
flavors, err := client.ListFlavors(ctx, &compute.ListFlavorsInput{
	ZoneID: "<zone-id>",
	Name:   "s2-general-1x2",
})
if err != nil {
	log.Fatal(err)
}
for _, f := range flavors.Items {
	log.Println(f.FlavorID, f.FlavorZoneID, f.IsSoldOut)
}
```

With `ZoneID`, the SDK lists the flavor zones in that network zone, then the
flavors of each one at a time: `1 + N` requests for `N` flavor zones. Rows
follow the flavor zone order, then the API's order inside each flavor zone.
Each row carries its `FlavorZoneID`; the SDK fills it when the API leaves it
empty. A flavor zone with no flavors adds no rows, a flavor listed in two
flavor zones stays two rows, and sold-out flavors stay in the list with
`IsSoldOut` true. The first failing request ends the call with its error
and no rows.

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

A malformed `SSHKeyID`, `ServerGroupID`, or `PolicyID`, an empty required
field, an empty `UpdateServerGroup` input, an `UpdateServerGroup` `Name` set
to the empty string, or an `ImportSSHKey` shape refusal fails with
`vngcloud.ErrInvalidInput` before any request. An unknown key or server
group fails with `vngcloud.IsNotFound(err) == true`. Server write errors
(`ErrFailed`, `ErrUnexpectedStatus`, and `ErrNotSettled` for `CreateServer`,
`StartServer`, `StopServer`, `RebootServer`, `ResizeServer`, and
`DeleteServer`) are on [Compute Servers](Compute-Servers.md#errors).

```go
var ErrServerGroupInUse = errors.New("compute: server group in use")
var ErrNotSettled       = errors.New("compute: write accepted but not settled")
```

`ErrServerGroupInUse` means `DeleteServerGroup` sent nothing because the
group has servers attached, or that its `DELETE` request was refused by
the server; see [Creating, updating, and deleting server
groups](#creating-updating-and-deleting-server-groups) above.
`ErrNotSettled` means `UpdateServerGroup`'s confirm read after a successful
`PUT` failed to come back; the write already reached the server, but the
update is safe to run again, since it is a read-merge `PUT` that resends
both fields either way.

A duplicate name, a quota, or a rejected key or policy shape from the
server itself comes back as the server's own `*vngcloud.APIError`; see
[Errors](Errors.md) for the general error model.
