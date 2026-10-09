# Storage

`storage` is a separate package, `danny.vn/vngcloud/storage`, with its own
`New(cfg)`. It reads vStorage object storage: the vStorage regions, the
projects in a region, and the buckets in a project. It reads the management
plane only. To read or write objects, use an S3 client such as rclone.

`storage` calls the vStorage console API, which GreenNode does not document.
It may change without notice.

## Setup

```go
package main

import (
	"context"
	"log"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/storage"
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

	client := storage.New(cfg)
	_ = ctx
	_ = client
}
```

The rest of this page assumes `cfg`, `ctx`, and `client := storage.New(cfg)`.
The IAM User needs a policy that allows vStorage actions; without one, every
call fails with `vngcloud.ErrPermission`.

## Regions

vStorage has its own regions, `HCM04` and `HAN02`, separate from the
`hcm-3` and `han-1` config regions. Every method except `ListRegions` takes
a `Region` field with the vStorage region name, matched without regard to
case. An empty `Region` maps the config region: `hcm-3` to `HCM04` and
`han-1` to `HAN02`. Any other config region with an empty `Region` returns
`vngcloud.ErrInvalidInput`, and nothing is sent. An unknown name returns
`ErrInvalidInput` too. The client looks up region IDs once and keeps them,
and sends the region ID in the `region` and `region_id` headers of every
other call. The server returns only the projects of that region.

```go
regions, err := client.ListRegions(ctx, nil)
if err != nil {
	log.Fatal(err)
}
for _, r := range regions.Items {
	log.Printf("%s %s", r.Name, r.S3Host)
}
```

`Region` has `ID`, `Name`, `DisplayingName`, `Description`, `BackendType`,
`S3Host`, `VOSAPIHost`, `AccountURL`, `AuthHost`, and `Status`. `S3Host` is
the endpoint for an S3 client.

## Projects

A vStorage project is a paid storage package in one region. The SDK cannot
create one: buy it in the console.

```go
projects, err := client.ListProjects(ctx, &storage.ListProjectsInput{Region: "HCM04"})
if err != nil {
	log.Fatal(err)
}
for _, p := range projects.Items {
	log.Printf("%s %s", p.ID, p.Name)
}
```

An account with no project gets an empty `Items`, not an error. Other
storage calls need a project ID, so they cannot run until the account has
one.

`Project` has `ID`, `Name`, `RegionID`, `RegionName`, `Status`,
`TotalQuota` (GB), `StartTime`, `EndTime`, and `Period`. `Period` is zero
when the API returns null.

## Buckets

```go
buckets, err := client.ListBuckets(ctx, &storage.ListBucketsInput{
	Region:    "HCM04",
	ProjectID: projects.Items[0].ID,
})
if err != nil {
	log.Fatal(err)
}
for _, b := range buckets.Items {
	log.Printf("%s: %d objects, %d bytes", b.Name, b.ObjectCount, b.SizeBytes)
}

detail, err := client.GetBucket(ctx, &storage.GetBucketInput{
	ProjectID: projects.Items[0].ID,
	Bucket:    buckets.Items[0].Name,
})
if err != nil {
	log.Fatal(err)
}
log.Println(detail.IsPublic, detail.IsVersioned)
```

`ListBuckets` asks for up to 1000 buckets, the per-project cap, so one call
returns every bucket. If the response says more exist, the call fails.
`ProjectID` and `Bucket` are required. `Bucket` must start with a letter or
digit and hold only letters, digits, `.`, `_`, and `-`, up to 255
characters; the server applies the full S3 naming rules.

`Bucket` has `Name`, `ObjectCount`, `SizeBytes`, `IsPublic`, `IsVersioned`,
`CreatedDate`, `LastModified`, `Type`, and `VersionLocation`. Dates stay
strings. The bucket fields follow the API specification and have not been
checked against a live bucket.

## Errors

The console API reports many failures as HTTP 200 with `"success": false`.
The SDK turns each into a `*vngcloud.APIError`: `StatusCode` is the HTTP
status, `Code` is the envelope code as text, and `Message` is the envelope
message cut to 256 bytes. An envelope code from 400 to 599 also matches that
status's sentinel, so code 404 matches `vngcloud.ErrNotFound`.

| Case | Result |
|---|---|
| Missing field, bad project ID or bucket name, unmapped or unknown region | `vngcloud.ErrInvalidInput`, no request |
| Envelope `success: false` | `*vngcloud.APIError` with the envelope code |
| HTTP 200 with an empty or non-JSON body | `*vngcloud.APIError`, code `EmptyResponse` |
| HTTP 403 | `vngcloud.ErrPermission`, with the code the API names, such as `IAM_PERMISSION_DENIED` |

See [Errors](Errors.md) for `APIError` itself.
