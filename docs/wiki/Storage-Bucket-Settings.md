# Storage Bucket Settings

Versioning, encryption, and CORS calls for `danny.vn/vngcloud/storage`:
`GetBucketVersioning`, `PutBucketVersioning`, `GetBucketCORS`,
`PutBucketCORS`, `DeleteBucketCORS`, `GetBucketEncryption`, and
`PutBucketEncryption`. See [Storage](Storage.md) for buckets
and S3 keys, and [Storage Bucket Policy](Storage-Bucket-Policy.md) for the
bucket policy.

This page assumes `cfg`, `ctx`, and `client := storage.New(cfg)` from
[Storage's Setup](Storage.md#setup). For errors, see
[Storage's Errors](Storage.md#errors).

## Versioning

```go
v, err := client.GetBucketVersioning(ctx, &storage.GetBucketVersioningInput{
	ProjectID: projectID, Bucket: "my-bucket"})
log.Println(v.Enabled, v.Status) // false Off

on := true
_, err = client.PutBucketVersioning(ctx, &storage.PutBucketVersioningInput{
	ProjectID: projectID, Bucket: "my-bucket", Enabled: &on})
```

`Status` is the server's `Off`, `Enabled`, or `Suspended`, unchanged. A bucket
reads `Off` only until the first put: a put of `false` gives `Suspended`, also
on a bucket never versioned, and nothing returns it to `Off`. `Enabled` is a
`*bool` and required, so a call cannot suspend versioning by leaving it out; a
nil `Enabled` returns `vngcloud.ErrInvalidInput` and sends nothing. A repeat put
gives the same result, so the call keeps the transport's retries. With
versioning on, overwrites and deletes keep old versions, which use quota and
make `DeleteBucket` refuse the bucket. Suspending keeps the versions already
stored.

## Encryption

```go
state, err := client.GetBucketEncryption(ctx, &storage.GetBucketEncryptionInput{
	ProjectID: projectID, BucketName: "my-bucket"})

_, err = client.PutBucketEncryption(ctx, &storage.PutBucketEncryptionInput{
	ProjectID: projectID, BucketName: "my-bucket", Enabled: true})
```

Encryption is server-managed SSE-S3: S3 `GetBucketEncryption` reports `AES256`,
and uploads to an enabled bucket answer `x-amz-server-side-encryption: AES256`.
There is no key choice. `Enabled` reads `data.encryption`; missing, null, or
non-boolean state returns `*vngcloud.APIError` with code `InvalidResponse`. Both
inputs require `ProjectID` and `BucketName`. `BucketName` follows the bucket
name checks in [Storage](Storage.md#buckets). `Region` uses the same resolution
and headers as other storage calls.

`PutBucketEncryptionInput.Enabled` is a bool with a complete target state.
False, including its zero value, disables default encryption. The request
always sends `enable`. After an accepted PUT, the SDK reads
`GetBucketEncryption` once under the caller's context. A matching state
returns an empty Output. A failed or mismatching read returns a nil Output
and an error wrapping `storage.ErrNotSettled`, with any read error preserved.
The error names the bucket and `GetBucketEncryption` as the recovery check.
A refused PUT returns the server error, including `vngcloud.ErrPermission`
for envelope code 403. An uncertain response says the change may have
happened and never becomes success from a later read.

The PUT sends once, without automatic resends, until live checks confirm
repeat behavior. Neither this call nor creation retries the whole sequence.
Both encryption operations apply the missing-bucket fallback to an empty
2xx response: one `GetBucket` returns `vngcloud.ErrNotFound` if the bucket is
gone, otherwise the original `EmptyResponse` error. The fallback's read
error does not replace that error.

To create with encryption:

```go
created, err := client.CreateBucket(ctx, &storage.CreateBucketInput{
	ProjectID: projectID, Bucket: "my-bucket", Encryption: true})
```

`Encryption: true` creates the bucket, enables encryption and confirms true,
then reads the bucket for the usual `CreateBucketOutput`. An ambiguous
create failure returns the existing recovery error and sends no encryption
call. After a successful create, an enable failure, cancellation before
enable, or failed confirmation returns a nil Output and
`storage.ErrBucketEncryptionIncomplete`. The error preserves its cause for
`errors.Is` and `errors.As`. A refused enable or confirmation of false says
the bucket exists without encryption enabled by this call. A lost response
or failed read says encryption is unconfirmed. Read with
`get-bucket-encryption`, then use `put-bucket-encryption --enabled=true` if
needed after fixing the cause. Do not upload backups until a read confirms
true. A failed final bucket read says encryption was confirmed and preserves
that read error.

Create and enable are not atomic. Delay uploads until successful return.
The SDK never deletes the bucket after a failed setup. A repeated create
with `Encryption: true` also enables an existing bucket, which can hold data.
False sends no encryption call and preserves an existing bucket's setting.
Encryption applies to uploads only (checked live on 2026-10-10 in `HCM04`):
objects written before enabling stay unencrypted, and objects written while
enabled stay encrypted after disabling. Conditional `PutObject`
(`If-None-Match: *`), multipart uploads, and `DeleteObjects` work on an
encrypted bucket. S3 `CopyObject` of an encrypted object answers 501
`NotImplemented`, and a copy of an unencrypted object is not encrypted, so
copy, move, and rename do not work for encrypted data; download and upload
again instead.

## CORS

```go
rule := storage.CORSRule{
	AllowedOrigins: []string{"https://app.example.com"},
	AllowedMethods: []string{"GET", "PUT"},
	MaxAgeSeconds:  600,
}
_, err := client.PutBucketCORS(ctx, &storage.PutBucketCORSInput{
	ProjectID: projectID, Bucket: "my-bucket", Rules: []storage.CORSRule{rule}})
```

`PutBucketCORS` replaces every rule, and a failed put keeps the old ones. It
returns `vngcloud.ErrInvalidInput`, with no request sent, for an empty `Rules`,
a rule without an origin or a method, an empty origin, an origin with more than
one `*`, a method other than `GET`, `PUT`, `POST`, `DELETE`, or `HEAD` (upper
case), a negative `MaxAgeSeconds`, or `ExposeAllowedHeaders` without
`AllowedHeaders`; the message names the rule and field. To
remove every rule, call `DeleteBucketCORS`, which also succeeds when there are
none. The server's own refusals, code `114` or `400` `MalformedXML`, are an
`*vngcloud.APIError` with no sentinel. A put and a delete keep the transport's
retries and take effect on the next preflight.

`GetBucketCORS` returns an empty, non-nil `Rules` when the bucket has none. The
server returns `AllowedMethods` in its own order, so compare methods as a set.

By default a rule exposes no response headers, so a browser page reads only the
CORS-safelisted ones. To let a page read `ETag` after an upload, list `ETag` in
`AllowedHeaders` and set `ExposeAllowedHeaders`:

```go
rule.AllowedHeaders = []string{"ETag"}
rule.ExposeAllowedHeaders = true
```

The put then sends `ExposeHeaders` equal to `AllowedHeaders`, and the server
adds `Access-Control-Expose-Headers` to the preflight answer. The flag without
`AllowedHeaders` returns `vngcloud.ErrInvalidInput`, with no request sent.
`ExposedHeaders` is read-only: the server sets it from the allowed headers, a
put never sends it, and a get sets `ExposeAllowedHeaders` when it is not empty,
so a rule read and put back keeps its exposed headers.
