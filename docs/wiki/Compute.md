# Compute

`compute` is `danny.vn/vngcloud/compute`, with its own `New(cfg)`. It reads
vServer instances, server groups, images, and SSH keys, and reads, imports,
creates, and deletes SSH keys.

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
osImages, err := client.ListOSImages(ctx, in)   // ZoneID
gpuImages, err := client.ListGPUImages(ctx, nil)
userImages, err := client.ListUserImages(ctx, in) // Page, Size
```

`ListServerSecurityGroups` and `ListServerGroupMembers` flatten nested data
already returned by `ListServers` and `ListServerGroups`; see
[Services](Services.md#compute) for the full read method list.

## SSH keys

An SSH key is either imported from a public key made elsewhere, or created
by having GreenNode generate the key pair and hand back the private key
once. **Prefer `ImportSSHKey`**: generate a key pair yourself, such as with
`ssh-keygen -t ed25519`, and import only the public half, so the private
key never leaves your machine. Use `CreateSSHKey` only when you need
GreenNode to generate the key pair itself; in that case GreenNode has
generated and seen the private key.

```go
imported, err := client.ImportSSHKey(ctx, &compute.ImportSSHKeyInput{
	Name:      "laptop",
	PublicKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI... me@laptop",
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
privateKeyPEM := created.PrivateKey.Reveal() // the only way to read it

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
decides what it accepts.

`SSHKey` has no field for a private key, on any call: a read can never
carry one back, matching what GreenNode's API itself returns. Only
`CreateSSHKeyOutput` carries one, as `PrivateKey`, separate from `SSHKey`.

Both `ImportSSHKey` and `CreateSSHKey` are `POST` and are never retried
after a failure that may have already reached the server: after any error
that is not a 4xx `*vngcloud.APIError`, the key may exist. Call
`ListSSHKeys` with `Name` and look for it before calling either one again,
rather than retrying blind; a key found that way after a failed
`CreateSSHKey` has already lost its private key and should be deleted.

`DeleteSSHKey` is idempotent; a retry that finds the key already gone
returns `vngcloud.IsNotFound(err) == true`.

### The private key is a Secret

`CreateSSHKeyOutput.PrivateKey` is a `vngcloud.Secret`, not a plain string.
Printing it with any `fmt` verb (`%v`, `%+v`, `%#v`, `%s`, and so on),
logging it through `log/slog`, or encoding it with `json.Marshal` all give
`"[redacted]"`; this holds even when the `Secret` is a field of a larger
struct, such as the whole `CreateSSHKeyOutput`. `Reveal()` is the only way
to read the actual value back out:

```go
fmt.Println(created)               // ...PrivateKey:[redacted]
fmt.Printf("%+v\n", created)        // same
log.Println(created.PrivateKey)     // [redacted]
data, _ := json.Marshal(created)    // {"SSHKey":{...},"PrivateKey":"[redacted]"}

key := created.PrivateKey.Reveal()  // the private key, in PEM form
```

Nothing in the SDK writes a `Secret`'s value to a file for you; save
`Reveal()`'s result yourself, with a mode that keeps it readable only by
you (`0600` on Unix), and never print or log it once read.

## Errors

A malformed `SSHKeyID`, an empty required field, or an `ImportSSHKey` shape
refusal fails with `vngcloud.ErrInvalidInput` before any request. An
unknown key fails with `vngcloud.IsNotFound(err) == true`. A duplicate
name, a quota, or a rejected key shape from the server itself comes back as
the server's own `*vngcloud.APIError`; see [Errors](Errors.md) for the
general error model.
