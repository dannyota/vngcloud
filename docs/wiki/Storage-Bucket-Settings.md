# Storage Bucket Settings

Versioning and CORS calls for `danny.vn/vngcloud/storage`:
`GetBucketVersioning`, `PutBucketVersioning`, `GetBucketCORS`,
`PutBucketCORS`, and `DeleteBucketCORS`. See [Storage](Storage.md) for buckets
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
