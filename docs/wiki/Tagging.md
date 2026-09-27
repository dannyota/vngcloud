# Tagging

`tagging` is `danny.vn/vngcloud/tagging`, with its own `New(cfg)`. One tag
API on the vServer gateway serves every resource type, so this package
holds `ListResourceTags` and `TagResource` rather than a tag method on each
service package.

## Setup

```go
package main

import (
	"context"
	"log"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/tagging"
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

	client := tagging.New(cfg)
	_ = ctx
	_ = client
}
```

The rest of this page assumes `cfg` and `ctx` from this setup, plus
`client := tagging.New(cfg)`.

## Reading tags

```go
tags, err := client.ListResourceTags(ctx, &tagging.ListResourceTagsInput{
	ResourceID: resourceID,
})
```

`ListResourceTags` reads every tag on `ResourceID`, whatever its type: the
read accepts any resource id, which does not itself confirm that
`ResourceID` names a type the tag write also accepts. Each `tagging.Tag`
holds `Key`, `Value`, `SystemTag`, and `CreatedAt`.

## Writing a tag

```go
tagged, err := client.TagResource(ctx, &tagging.TagResourceInput{
	ResourceID:   resourceID,
	ResourceType: "SERVER",
	Key:          "env",
	Value:        "prod",
})
if err != nil {
	log.Fatal(err)
}
log.Println(tagged.Changed, tagged.Previous)
```

`TagResource` sets `Key` to `Value` on `ResourceID`, leaving every other tag
on the resource unchanged. `ResourceType` is sent to the server exactly as
given; this package exports no resource type constant yet, since none has
been confirmed live to accept a tag write for free.

It reads every tag on the resource first. If any of them is a system tag,
it returns `tagging.ErrSystemTag` and sends nothing: whether the tag write
would resend a system tag unchanged or drop it is not yet confirmed live.
Otherwise, when `Key` is already set to `Value`, it returns at once with
`Changed` false and sends nothing.

Otherwise it sends every tag it read, with `Key`'s value replaced or added,
in one `PUT`: `PUT` is idempotent, so the transport's normal retries apply,
and this read-merge-send shape means a `TagResource` call never drops a tag
some other caller wrote. It then reads the tags again to confirm they equal
what was sent. A mismatch, or a failure of that confirming read, returns an
error wrapping `tagging.ErrNotSettled`; read the tags again before writing
once more, rather than repeating the same call blind.

`TagResourceOutput.Previous` is `Key`'s value before the write, or `nil`
when the resource had no such tag, so a caller can undo a `TagResource` call
by writing `Previous` back.

There is no `UntagResource` yet: whether the tag `PUT` drops a key left off
the list, or leaves it in place because the server upserts by key, is not
yet confirmed live. Until that live check runs, removing a tag through this
SDK is not possible; the tag quota (10 per resource, confirmed live) stays
on the server.

## Errors

```go
var ErrSystemTag  = errors.New("tagging: resource has a system tag")
var ErrNotSettled = errors.New("tagging: write accepted but not settled")
```

`ErrSystemTag` means `TagResource` found a system tag on the resource and
sent nothing. `ErrNotSettled` means the `PUT` was sent, and may have
reached the server, but the confirming read did not come back matching it:
either that read itself failed, or another writer changed the tags in
between. The returned `Output` still holds the last tags a read returned.
