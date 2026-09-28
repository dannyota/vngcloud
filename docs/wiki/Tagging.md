# Tagging

`tagging` is `danny.vn/vngcloud/tagging`, with its own `New(cfg)`. One tag
API on the vServer gateway serves every resource type, so this package
holds `ListResourceTags`, `TagResource`, and `UntagResource` rather than a
tag method on each service package.

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

Every resource carries system tags the platform manages: `vng.zone`,
`vng.region`, and `vng.createdBy`, confirmed live on a virtual IP address.
`ListResourceTags` returns them alongside any tag a caller wrote.

## Writing a tag

```go
tagged, err := client.TagResource(ctx, &tagging.TagResourceInput{
	ResourceID:   resourceID,
	ResourceType: tagging.ResourceTypeVirtualIPAddress,
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
given. `tagging.ResourceTypeVirtualIPAddress` is confirmed live to accept a
tag write for free. VNG Cloud's own Go SDK also names `SERVER`, `VOLUME`, and
`LOAD-BALANCER` for this call, on paid resources this package has not
tried.

It refuses with `tagging.ErrSystemTag`, sending nothing, when `Key` starts
with `vng.` or names an existing system tag: those two checks cover every
system tag confirmed live, and a resource carrying one is otherwise no
reason to refuse, since every resource has one. Otherwise it reads every
tag on the resource; when `Key` is already set to `Value` among the user
tags, it returns at once with `Changed` false and sends nothing. The
server refuses a `Value` shorter than 3 or longer than 255 characters with
400; the SDK leaves that rule to the server.

Otherwise it sends every user tag it read, with `Key`'s value replaced or
added, in one `PUT`. System tags are never included: the `PUT` replaces
only the user tag list, confirmed live, so a resource's system tags are
never sent and never touched. `PUT` is idempotent, so the transport's
normal retries apply, and this read-merge-send shape means a `TagResource`
call never drops a user tag some other caller wrote. It then reads the
tags again to confirm the user tags equal what was sent. A mismatch, or a
failure of that confirming read, returns an error wrapping
`tagging.ErrNotSettled`; read the tags again before writing once more,
rather than repeating the same call blind.

`TagResourceOutput.Tags` is the resource's whole tag list, system tags
included. `Previous` is `Key`'s value before the write, or `nil` when the
resource had no such tag, so a caller can undo a `TagResource` call by
writing `Previous` back.

## Removing a tag

```go
untagged, err := client.UntagResource(ctx, &tagging.UntagResourceInput{
	ResourceID:   resourceID,
	ResourceType: tagging.ResourceTypeVirtualIPAddress,
	Key:          "env",
})
```

`UntagResource` removes `Key` from `ResourceID`'s tags, following the same
refuse-read-send-confirm shape as `TagResource`: it refuses with
`tagging.ErrSystemTag`, sending nothing, when `Key` starts with `vng.` or
names an existing system tag; it returns at once with `Changed` false when
`Key` is already absent from the user tags; and otherwise it sends every
other user tag it read and confirms the result with another read. The `PUT`
replaces only the user tag list, confirmed live, so a resource's system
tags are never sent and never touched. The tag quota (10 per resource,
confirmed live) stays on the server.

## Errors

```go
var ErrSystemTag  = errors.New("tagging: key is a system tag")
var ErrNotSettled = errors.New("tagging: write accepted but not settled")
```

`ErrSystemTag` means `Key` names a system tag, either by its `vng.` prefix
or by matching an existing system tag, and `TagResource` or `UntagResource`
sent nothing. `ErrNotSettled` means the `PUT` was sent, and may have
reached the server, but the confirming read did not come back matching it:
either that read itself failed, or another writer changed the tags in
between. On a mismatched read, `Output` holds that read's tags. On a failed
confirming read, `Output` holds the tags read before the write.
