# Storage

`storage` is a separate package, `danny.vn/vngcloud/storage`, with its own
`New(cfg)`. It manages vStorage object storage: it reads the vStorage regions
and the projects in a region, lists, creates, and deletes the buckets in a
project and the project's S3 keys, attaches a key to an IAM service account,
and reads, sets, and deletes a bucket's policy. It covers the management plane
only. To read or write objects, use an S3 client such as rclone with an S3 key.

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
and hyphens. That refusal is an `*vngcloud.APIError` with code `112` and the
server's message, and it matches `vngcloud.ErrInvalidInput`: the server
received the request and rejected the input.

`CreateBucket` is a `POST`, so the SDK retries it only after a 429 or a failed
dial. After a 5xx or a network error the bucket may exist: the error names
`GetBucket` as the check.

`DeleteBucket` reads the bucket first. It returns `storage.ErrBucketNotEmpty`
and sends no delete when the object count is above 0, when the count is null
or missing, or when the size or used capacity is above 0. There is no force
option: empty the bucket with an S3 client. A missing bucket returns
`vngcloud.ErrNotFound`. A failure of that first read is returned as it is,
named as the read before the delete. The server's own refusal of a bucket
that holds objects is unverified until a live check can put an object.

The server answers a delete before the bucket is gone. For about a second
after the `DELETE`, `GetBucket` and `ListBuckets` can still show the bucket,
and a read can fail with code `-1` or `EmptyResponse`. Unless `NoWait` is
set, `DeleteBucket` waits for this: it calls `GetBucket` every second for up
to 30 seconds.

- `vngcloud.ErrNotFound` ends the wait, and the call returns.
- The bucket still readable, code `-1`, or `EmptyResponse` means it is still
  deleting, so the wait reads again.
- Any other read error is returned at once, with a note that the delete was
  accepted.
- If the bucket outlasts 30 seconds, the error wraps `storage.ErrNotSettled`.
  The delete was accepted: do not send it again. Read the bucket later to
  confirm.

```go
_, err = client.DeleteBucket(ctx, &storage.DeleteBucketInput{
	ProjectID: projectID,
	Bucket:    "my-bucket",
	NoWait:    true,
})
```

With `NoWait`, `DeleteBucket` returns once the server accepts the `DELETE`
and does not read the bucket again. Poll `GetBucket` until it returns
`vngcloud.ErrNotFound` before you reuse the name, and treat code `-1` and
`EmptyResponse` as still deleting. A repeat `DeleteBucket` inside that window
can stop at its first read's error, sending no `DELETE`.

## S3 keys

An S3 key is an access key and a secret for one project. An S3 client signs
its requests with it. The key has the rights of the IAM user that made it, on
every bucket of the project, so make keys only with an IAM user that is
scoped to vStorage. The server takes no name for a key, and an account holds
at most 10. A key made this way is unrestricted. To scope a key, attach it to a
service account ([below](#service-account-keys)).

```go
created, err := client.CreateS3Key(ctx, &storage.CreateS3KeyInput{
	ProjectID: projectID,
})
if err != nil {
	log.Fatal(err)
}
access := created.AccessKey
secret := created.SecretKey.Reveal() // save this now; it is never shown again

keys, err := client.ListS3Keys(ctx, &storage.ListS3KeysInput{ProjectID: projectID})
if err != nil {
	log.Fatal(err)
}
log.Println(len(keys.Items))

_, err = client.DeleteS3Key(ctx, &storage.DeleteS3KeyInput{
	ProjectID: projectID,
	UserKeyID: created.UserKeyID,
})
```

`ProjectID` is required on all three calls, and `Region` works as above.
`UserKeyID` is the key's ID, not its access key. `S3Key` has `UserKeyID`,
`AccessKey`, `ProjectID`, `RegionID`, `UserID`, `SubUserID`, `CreatedDate`,
and `Status`. `CreatedDate` is the server's `dd/mm/yyyy hh:mm` text. The
list never holds a secret.

`SecretKey` is a `vngcloud.Secret`: printing, logging, or JSON-encoding it
gives `"[redacted]"`, and `Reveal()` is the only way to read the value. See
[Compute](Compute.md#the-private-key-is-a-secret) for the full contract. The
create and list responses never reach a response-capture hook, and a decode
failure never quotes the response.

If a create response holds a key but no secret, `CreateS3Key` returns the key
and an error wrapping `storage.ErrNoSecret`. The key exists but cannot be
used: delete it with `DeleteS3Key`.

`CreateS3Key` is sent once, with no retry, no resend after a 401, and no
redirect. After a 5xx, a network error, or a response that fails to decode,
the error says a key may exist. Call `ListS3Keys` and delete any `UserKeyID`
you do not know, since its secret is lost. The SDK lists nothing itself: a
key another client made at the same time would look the same. A 4xx, a 429,
a failed dial, and an envelope refusal made no key, and the error says
nothing about one. At the key limit the server answers HTTP 200 with
envelope code `114` and the message `Key number is reached to maximum value
10`.

`DeleteS3Key` ends the key at once and cannot be undone, and it works on an
attached key. It keeps the transport's retries. The server answers success for
a `UserKeyID` it does not know, and a repeat delete of a deleted key as
envelope code `114` (`Could not delete s3 keys. InvalidAccessKeyId`), not
`ErrNotFound`.

A key works on the data plane. With the AWS CLI, put the access key and
secret in a credentials file and point `AWS_SHARED_CREDENTIALS_FILE` at it,
then use the endpoint from `Region.S3Host` and the region name:

```sh
AWS_DEFAULT_REGION=HCM04 aws s3 ls --endpoint-url https://hcm04.vstorage.vngcloud.vn
```

## Service account keys

A key attached to a service account loses its creator's rights and acts as the
service account. The service account's rights on a bucket come only from bucket
policies that name its principal. The calls are `AttachS3Key`, `DetachS3Key`,
and `EnsureServiceAccountPrincipal`.

```go
principal, err := client.EnsureServiceAccountPrincipal(ctx,
	&storage.EnsureServiceAccountPrincipalInput{
		ProjectID:        projectID,
		ServiceAccountID: serviceAccountID, // as iam returns it, no "sa-" prefix
	})
if err != nil {
	log.Fatal(err)
}
log.Println(principal.PrincipalARN) // put this in the bucket policy

_, err = client.AttachS3Key(ctx, &storage.AttachS3KeyInput{
	ProjectID:        projectID,
	UserKeyID:        created.UserKeyID,
	ServiceAccountID: serviceAccountID,
})
```

`DetachS3Key` takes `ProjectID` and `UserKeyID` and makes the key unrestricted
again: it has its creator's rights on every bucket of the project at once.
Neither call returns data. Read the state back with `ListS3Keys`: `SubUserID`
is empty for an unrestricted key, and `<account user>:sa-<service account
name>` for a key attached to that service account. The data plane follows
within 3 seconds.

`EnsureServiceAccountPrincipal` returns `SubUserID` and `PrincipalARN`
(`arn:aws:iam:::user/` and the `SubUserID`). It is a write although it sends a
`GET`: the server makes the service account's storage sub-user on the first
call, and a repeat returns the same one. The sub-user cannot be deleted and has
no rights until a bucket policy names it. A response without a `subUserId`, or
with one that is not exactly `<user>:sa-<name>`, is an error with no Output, so
the IAM user's own principal never reaches a policy. A well-formed ID that
matches no service account gets a code `114` refusal and no Output.

A key is unrestricted from its create until its attach. If you create a key to
restrict it, attach it before you store the secret, and delete the key if the
attach fails for any reason.

An attached key can still list the project's buckets and create buckets, since
no bucket policy governs those. Make keys only with an IAM user scoped to
vStorage.

`AttachS3Key` and `DetachS3Key` are not idempotent, so each is sent once, with
no retry, no resend after a 401, and no redirect. A 429 or a failed dial
returns an `*vngcloud.APIError` with `Retryable` true, and you may rerun. After
a 5xx, a network error, or an unreadable response, the error says the change
may have happened: list the keys and read `SubUserID`. A rerun that answers
"already attached with this service account", or for a detach "not attached",
means the first try took effect.

The server refuses with HTTP 200, envelope code `114`, and a message. Each is
an `*vngcloud.APIError` with `Code` `"114"` and that message, and none matches
a sentinel such as `ErrNotFound`:

| Case | Message |
|---|---|
| Attach to the account the key is already attached to | `This S3 key is already attached with this service account` |
| Attach of a key attached to another account | `This S3 key is already attached with another service account` |
| Attach to an unknown service account | `StatusCode=404` |
| Attach or detach of an unknown key | `S3 key not found` |
| Detach of an unattached key | `This S3 key is not attached to any service account.` |

The server checks "attached elsewhere" before the service account, so any
attach of an attached key gives one of the first two messages.

The sub-user is named from the service account's name, not its ID. A new
service account with the name of a deleted one gets the same principal, so it
inherits any bucket policy that still names it. Remove a service account from
every bucket policy before you delete it. Detach its keys first too: a key
stays attached to a deleted service account, and `ListS3Keys` still shows its
`SubUserID` until you call `DetachS3Key` (which succeeds) or `DeleteS3Key`.

For one key per bucket, run the steps in this order:

1. Create the bucket.
2. Create the service account with `iam`.
3. Call `EnsureServiceAccountPrincipal`.
4. Write a bucket policy that allows the principal on the bucket, with
   `PutBucketPolicy` ([Storage Bucket Policy](Storage-Bucket-Policy.md)).
5. Create the key, then attach it to the service account, then store its
   secret.

The principal exists before the policy names it. The policy exists before the
key, since an attached key has no rights in a bucket until a policy names its
principal. The key is attached before its secret is stored, since a key is
unrestricted until its attach. [CLI-Storage](CLI-Storage.md) has the commands.

## Errors

The console API reports many failures as HTTP 200 with `"success": false`.
The SDK turns each into a `*vngcloud.APIError`: `StatusCode` is the HTTP
status, `Code` is the envelope code as text, and `Message` is the envelope
message cut to 256 bytes. An envelope code from 400 to 599 also matches that
status's sentinel, so code 404 matches `vngcloud.ErrNotFound`.

| Case | Result |
|---|---|
| Missing field, bad project ID or bucket name, unmapped or unknown region | `vngcloud.ErrInvalidInput`, found before the call, so no call to the bucket is sent |
| Envelope `success: false` | `*vngcloud.APIError` with the envelope code |
| Envelope code `112`, the server refused an input | `*vngcloud.APIError` that matches `vngcloud.ErrInvalidInput`; the request was sent |
| HTTP 200 with an empty or non-JSON body | `*vngcloud.APIError`, code `EmptyResponse` |
| HTTP 403 | `vngcloud.ErrPermission`, with the code the API names, such as `IAM_PERMISSION_DENIED` |
| `DeleteBucket` on a bucket with objects, or one whose count the read did not report | `storage.ErrBucketNotEmpty`, no delete sent |
| `DeleteBucket` accepted, bucket still readable after 30 seconds | Error wrapping `storage.ErrNotSettled`; do not repeat the delete |
| A write answered with an empty body | `*vngcloud.APIError`, code `EmptyResponse`; the change may have happened |
| `CreateS3Key` got a 5xx, a network error, or no decodable response | Error that says a key may exist: list the keys and delete any unknown `UserKeyID` |
| `CreateS3Key` response holds a key but no secret | The key and an error wrapping `storage.ErrNoSecret`; delete the key |
| `CreateS3Key` at the 10-key limit, or a repeat `DeleteS3Key` | `*vngcloud.APIError` with envelope code `114`, no sentinel |
| `AttachS3Key` or `DetachS3Key` refused | `*vngcloud.APIError` with envelope code `114` and the server's message, listed under [Service account keys](#service-account-keys) |
| `AttachS3Key` or `DetachS3Key` got a 5xx or a network error | `*vngcloud.APIError` that says the change may have happened; list the keys and read `SubUserID` |
| `EnsureServiceAccountPrincipal` response without a `:sa-` sub-user | `*vngcloud.APIError`, no Output |
| A bucket policy call refused, or a `Policy` that is not a JSON object with a non-empty `Statement` array | See [Storage Bucket Policy](Storage-Bucket-Policy.md#errors) |

See [Errors](Errors.md) for `APIError` itself.
