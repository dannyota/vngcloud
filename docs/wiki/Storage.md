# Storage

`storage` is a separate package, `danny.vn/vngcloud/storage`, with its own
`New(cfg)`. It manages vStorage object storage: it reads the vStorage regions
and the projects in a region, and it lists, creates, and deletes the buckets in
a project. It covers the management plane only. To read or write objects, use
an S3 client such as rclone.

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
strings. Live buckets send `null` for most of these fields, so they decode to
their zero values; `ObjectCount` is set. `ListBuckets` gives `CreatedDate` as
`dd/mm/yyyy hh:mm`, and `GetBucket` gives it as `null`.

### Create and delete

```go
created, err := client.CreateBucket(ctx, &storage.CreateBucketInput{
	ProjectID: projectID,
	Bucket:    "my-bucket",
})
if err != nil {
	log.Fatal(err)
}
log.Println(created.Name)

_, err = client.DeleteBucket(ctx, &storage.DeleteBucketInput{
	ProjectID: projectID,
	Bucket:    "my-bucket",
})
if errors.Is(err, storage.ErrBucketNotEmpty) {
	log.Println("empty the bucket with an S3 client first")
}
```

`CreateBucket` makes a bucket without object lock and returns it as
`GetBucket` reads it. If that read fails, the error says the bucket was
created. A repeated create of a name you already own succeeds with the same
answer. The server refuses a name that is not all lowercase letters, numbers,
and hyphens, as an `*vngcloud.APIError` with code `112`.

`CreateBucket` is a `POST`, so the SDK retries it only after a 429 or a failed
dial. After a 5xx or a network error the bucket may exist: the error names
`GetBucket` as the check.

`DeleteBucket` reads the bucket first. If `ObjectCount` is above 0 it returns
`storage.ErrBucketNotEmpty` and sends no delete. There is no force option:
empty the bucket with an S3 client. A missing bucket returns
`vngcloud.ErrNotFound`.

The server answers a delete before the bucket is gone. For a moment after
`DeleteBucket` returns, `GetBucket` and `ListBuckets` can still show the
bucket, and a read can fail with code `-1` or `EmptyResponse`. Poll
`GetBucket` until it returns `vngcloud.ErrNotFound` before you reuse the name.

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
| `DeleteBucket` on a bucket with objects | `storage.ErrBucketNotEmpty`, no delete sent |
| A write answered with an empty body | `*vngcloud.APIError`, code `EmptyResponse`; the change may have happened |

See [Errors](Errors.md) for `APIError` itself.
