# Storage Objects

Use `storage.NewObjectClient` for native S3 transfers, one-page listing, and
HEAD metadata, current-name deletion, and server-side copy. The existing
`storage.New(cfg)` client manages vStorage projects, buckets, keys, policies,
and settings through the console API. The object client uses a separate S3
data plane and never loads IAM credentials or a management client.

File transfers send one completed regular file as one object, with binary
content and no text conversion, compression, or format inspection. List and
HEAD return metadata without downloading content. The SDK accepts portable
in-memory credentials. All object CLI commands support Linux and macOS only.

## Credentials and regions

Use the existing [storage management setup](Storage.md#setup) and
[S3 key setup](Storage.md#s3-keys) to create a key for the selected project.
The `create-s3-key --secret-file <credentials-file>` command writes a
mode-0600 AWS shared credentials file containing `[default]`,
`aws_access_key_id`, and `aws_secret_access_key`. Keep the file private and
its parent directory trusted. The object CLI requires this explicit file and
checks that it is a regular file owned by the current user with mode 0600. Do
not pass secret values in command arguments.

The CLI and SDK require the exact S3 region `HCM04` or `HAN02`. They select
`https://hcm04.vstorage.vngcloud.vn` or
`https://han02.vstorage.vngcloud.vn`. The commands reject explicit global
`--region` and `--project-id`; environment or profile defaults for those
management settings do not select the S3 region. The S3 key scopes the project.

The S3 permission baseline is `s3:PutObject` for upload, `s3:GetObject` for
download or HEAD, `s3:ListBucket` on the bucket for listing, and
`s3:DeleteObject` for deletion. Recorded
provider tests use broader grants and do not prove every minimal-action
combination. No IAM login or ambient credential chain is used. The CLI
requires the explicit credential file; SDK callers pass both credentials in
memory:

```go
client, err := storage.NewObjectClient(storage.ObjectConfig{
	Region: "HCM04",
	Credentials: storage.ObjectCredentials{
		AccessKeyID:     vngcloud.Secret("<access-key-id>"),
		SecretAccessKey: vngcloud.Secret("<secret-access-key>"),
	},
})
```

The placeholders above are examples only. Never paste real credentials into
documentation, source control, or shell history.

## Listing and HEAD metadata

List one page or inspect one exact object key:

```sh
vngcloud storage list-objects --s3-region HCM04 \
  --s3-credentials-file ./storage.credentials \
  --bucket app-files --prefix archives/ --delimiter / --max-keys 100

vngcloud storage head-object --s3-region HAN02 \
  --s3-credentials-file ./storage.credentials \
  --bucket app-files --key archives/bundle.tar.gz
```

Both commands permit read-only mode from the flag, environment, or profile.
They use no IAM login, management request, token cache, ambient credential
chain, subprocess, body file, or local destination. `--cli-input-json`,
`--all`, and version selectors are unavailable. `--transfer-timeout` defaults
to five minutes and accepts one second through one hour. `--output` and
`--query` select metadata only. Keys and common prefixes retain literal UTF-8
bytes, including slash and percent sequences; the renderer escapes controls.

`list-objects` returns the page envelope `Items`, `CommonPrefixes`, `MaxKeys`,
`IsTruncated`, and `NextContinuationToken`. Each item has `Key`, `Size`,
`ETag`, `LastModified`, and `StorageClass`. `MaxKeys` is the effective page
bound, not a total. Its CLI default is 1,000; explicit values must be 1 through
1,000. The combined item and common-prefix count cannot exceed that bound.
Common prefixes are groups, not downloadable objects. The client preserves
server order without sorting or automatic pagination. JSON retains the full
page envelope; table and text show item rows. Query page fields explicitly
when using table or text.

An empty prefix matches all keys. A nonempty `--prefix` uses exact UTF-8 byte
matching and accepts at most 1,024 bytes. `--delimiter` is empty or `/`.
A continuation token is opaque UTF-8, at most 8 KiB, without control bytes.
Do not trim, parse, or URL-decode it. If `IsTruncated` is true, invoke the
command again with the returned token and unchanged bucket, prefix, and
delimiter. A short or empty page can still continue. A final page has an empty
next token. Pages do not form a snapshot; concurrent changes can cause
omissions or repeats.

`head-object` sends HEAD only, with no GET fallback. Its output adds input
`Bucket` and `Key` to `ContentLength`, `ETag`, `ContentType`, `LastModified`,
`Metadata`, `VersionID`, and `ServerSideEncryption`. Echoed inputs are
redacted against the credential pair. Length -1 means the server omitted it;
zero means an empty object. HEAD can report a size above download limits.
`Metadata` is a non-nil map of accepted user metadata, with lowercase names
and the `x-amz-meta-` prefix removed.

Unsafe, unsupported, duplicate, or oversize optional metadata is omitted.
Absent timestamps are JSON null; accepted timestamps are UTC. ETag, version,
and encryption fields are observations, not integrity attestations, locks,
or proof of a unique generation. Version selection is unavailable. A returned
403 remains failure and a returned 404 remains not found; permissions can
change which status the service returns for a missing key.

List and HEAD rely on published compatibility in both regions. HCM04 adds
recorded listing evidence. Detailed optional fields and HAN02 V2 pagination
remain standards-based assumptions, with strict local decoding on disagreement.
No fresh live verification is implied.

The SDK accepts the same filters with explicit in-memory credentials:

```go
input := &storage.ListObjectsInput{
    Bucket: "app-files", Prefix: "archives/", Delimiter: "/", MaxKeys: 100,
}
page, err := client.ListObjects(ctx, input)
if err != nil {
    return err
}
if page.IsTruncated {
    input.ContinuationToken = page.NextContinuationToken
    page, err = client.ListObjects(ctx, input)
    if err != nil {
        return err
    }
}
_ = page

metadata, err := client.HeadObject(ctx, &storage.HeadObjectInput{
    Bucket: "app-files", Key: "archives/bundle.tar.gz",
})
if err != nil {
    return err
}
_ = metadata
```

SDK `ListObjectsInput.MaxKeys` zero selects 1,000; positive values must be
1 through 1,000. Empty `Items` and `CommonPrefixes` are non-nil slices.
A failed list or HEAD returns nil output. Each method makes one application
request without retries or automatic confirmation reads.

Exit codes are 0 for success, 2 for usage/config/input refusal, 3 for
credential failures or HTTP 401, 4 for returned HTTP 404, and 1 for HTTP 403
or other failures. A query or stdout failure after success exits 1 and says
the operation completed. It never resends the operation.

## Current-name deletion

Delete one exact current object name with explicit destructive consent:

```sh
vngcloud storage delete-object --s3-region HCM04 \
  --s3-credentials-file ./storage.credentials \
  --bucket app-files --key archives/bundle.tar.gz --yes
```

`--yes` is required. Read-only from `--read-only`, `VNGCLOUD_READ_ONLY`, or
profile `read_only` blocks deletion before the credentials file opens. A false
value in one source cannot disable another source's read-only policy. Usage,
output, query syntax, and profile checks also run before credentials or client
construction. `--profile` selects local output and policy settings only.

Require explicit `--s3-region`, `--s3-credentials-file`, `--bucket`, and
`--key`.
The command uses the private credential reader described above, ignores
management and ambient S3 credentials, and makes no IAM, management, cache,
subprocess, HEAD, GET, or list call. Linux and macOS are supported. There is no
body, destination, replacement, version selector, precondition, MFA, retention
bypass, or `--cli-input-json` option. The exact UTF-8 key accepts 1 through
1,024 bytes, including literal slash and percent sequences. The whole-operation
`--transfer-timeout` defaults to five minutes and accepts one second through
one hour; an earlier caller deadline wins.

`--output` and `--query` select metadata only: `Bucket`, `Key`, `DeleteMarker`,
and `VersionID`. Echoed bucket and key values are redacted against both
credentials, and the renderer escapes key controls. An absent or filtered
marker is JSON null; a safe marker can be true or false. An absent or filtered
version is an empty string. No object content or progress goes to stdout.
Debug output contains only operation, elapsed time, status, byte count, and a
local error code. Confirmed deletion reports status 204 and zero content bytes.

DELETE targets the current name unconditionally. It has no protection against
concurrent writers and can remove a new object written at the same name. HTTP
204 confirms accepted deletion, even for a missing name; it does not prove
prior existence, permanent erasure, reclaimed quota, or an empty bucket.
Optional marker and version observations do not prove those facts either.

With enabled versioning, deletion can add a delete marker while retaining
older versions. With suspended versioning, it can remove the null version and
add a marker while retaining numbered versions. Repetition can delete newly
written data or add another marker. The command offers no exact-version
removal or permanent-erasure guarantee.

The SDK uses the same unconditional operation with in-memory credentials:

```go
result, err := client.DeleteObject(ctx, &storage.DeleteObjectInput{
    Bucket: "app-files", Key: "archives/bundle.tar.gz",
})
if errors.Is(err, storage.ErrObjectDeleteUnconfirmed) {
    // Inspect the current key and version history before another deletion.
    return err
}
if err != nil {
    return err
}
_ = result
```

The SDK returns only optional `DeleteMarker` and `VersionID`; every failure
returns nil output. Each invocation sends at most one DELETE with no retry,
rollback, or verification request. `s3:DeleteObject` is the action requirement.
The client needs no prior read or console grant, but recorded tests used broader
grants and do not prove a minimal permission combination. HCM04 has recorded
deletion behavior; HAN02 relies on published S3 compatibility. No fresh live
verification or conditional-delete guarantee is implied.

Exit codes are 0 for success, 2 for usage/config/input/read-only/consent
refusal, 3 for credentials or HTTP 401, 4 for returned HTTP 404, and 1 for HTTP
403 or other failures. Cancellation or deadline anywhere in an ordinary error
chain exits 1. `ObjectDeleteUnconfirmed` always exits 1 ahead of wrapped auth,
not-found, or context errors. Its fixed recovery advice is to inspect the
current key and version history before deciding whether to delete again.
Inspection is a separate decision using appropriate tools and permissions;
the CLI performs no recovery read automatically.

A query evaluation or stdout failure after confirmed deletion exits 1 and says
`delete completed; metadata output failed; do not retry the operation`. The
client sends no second DELETE. Keep the completed result and inspect before
any later deletion decision.

## Server-side copy

Use [Storage Object Copy](Storage-Objects-Copy.md) to copy one current source
within the same project and region, retaining the source. Native copy requires
`--replace --yes`; create-only copy and move remain unavailable. The copy guide
covers current-name races, diagnostic versions, provider metadata defaults,
recorded HCM04 encryption restrictions, unverified HAN02 encryption, and
unconfirmed-result recovery.

## CLI file transfers

Upload one file and download one exact key:

```sh
vngcloud storage put-object --s3-region HCM04 \
  --s3-credentials-file ./storage.credentials \
  --bucket app-files --key archives/bundle.tar.gz \
  --body ./bundle.tar.gz

vngcloud storage get-object --s3-region HCM04 \
  --s3-credentials-file ./storage.credentials \
  --bucket app-files --key archives/bundle.tar.gz \
  --destination ./downloaded.tar.gz
```

Both commands require regular file paths. They do not stream object bytes
through stdin or stdout and do not accept `-` or `file://` paths. Downloads
write only to the selected destination. `--cli-input-json` is unsupported.
Normal `--output` and `--query` handle metadata only: bucket, key, byte count,
local SHA256, optional ETag, and download destination.

The CLI PUT limit is decimal 5,000,000,000 bytes. GET defaults to the same
bound. `--max-download-bytes` selects a positive finite limit up to
9,223,372,036,854,775,806 bytes. A five-minute deadline covers the full
transfer by default. `--transfer-timeout` accepts one second through one hour.

HCM04 supports default create-only uploads. HAN02 create-only uploads are
unavailable; HAN02 downloads and explicit replacement remain available under
the published compatibility contract. This is not a new live verification.

## Replacement and local files

PUT creates only by default with a conditional request. If the key already
exists, the upload fails and preserves it. `--replace --yes` sends an
unconditional whole-object PUT. `--replace` without `--yes` fails before the
request. `--yes` by itself does not enable replacement. A CLI read-only policy
blocks PUT before the credentials file or source file opens. Read-only mode
still permits GET, which writes the chosen local file.

Finish writing the source before upload. The CLI copies it into a private
0600 snapshot inside a new 0700 `vngcloud-object-*` temporary directory, then
hashes and uploads that snapshot. The snapshot may use up to one upload's worth
of disk space. Changes to the source during copying can produce a mixed
snapshot. A process crash can leave `vngcloud-object-*` files for manual local
cleanup; the CLI does not sweep them automatically.

For GET, the destination parent must already exist and be trusted. By default,
the CLI refuses every occupied destination, including a dangling symlink.
`--replace --yes` permits replacing an existing regular local file; it does
not replace the remote object. The CLI writes a same-directory private
temporary file and publishes only after clean EOF, sync, and close. Default
publication uses a no-clobber link operation. Replacement uses an atomic
rename on supported Linux and macOS filesystems. A pre-publication failure
leaves the old destination intact. These operations do not promise power-loss
durability on every filesystem. Use trusted source, credential, and destination
parent directories.

If publication succeeds but temporary cleanup or directory sync fails, the
error says the download was published. Keep the complete file, do not repeat
GET, and remove any named local temporary file or sync the directory as
appropriate.

## SDK transfers

The SDK accepts in-memory credentials and does not read files. Set
`ObjectConfig.Region` to exactly `HCM04` or `HAN02`. A zero
`ObjectConfig.Timeout` selects five minutes; positive timeouts can be at most
one hour, and negative values are invalid. The requester's earlier deadline
wins.

PUT accepts an `io.ReadSeeker`. The SDK seeks the body to offset zero before
hashing, then rewinds it and sends one bounded request. `ContentLength` may be
zero through 5,000,000,000 bytes. The caller keeps the body immutable during
the call and owns it; `PutObject` does not close it. GET returns a streaming
`io.ReadCloser`. A zero `GetObjectInput.MaxBytes` selects the 5,000,000,000
byte default. A positive value sets a finite bound up to
9,223,372,036,854,775,806; negative values and `math.MaxInt64` are invalid.
Read the body through clean EOF to establish completion, then close it on every
path, including read errors:

```go
body, err := os.Open("bundle.tar.gz")
if err != nil {
	return err
}
defer body.Close()
info, err := body.Stat()
if err != nil {
	return err
}
put, err := client.PutObject(ctx, &storage.PutObjectInput{
	Bucket: "app-files", Key: "archives/bundle.tar.gz",
	Body: body, ContentLength: info.Size(),
})
if err != nil {
	return err
}
_ = put

got, err := client.GetObject(ctx, &storage.GetObjectInput{
	Bucket: "app-files", Key: "archives/bundle.tar.gz",
})
if err != nil {
	return err
}
defer got.Body.Close()
if _, err := io.Copy(destination, got.Body); err != nil {
	return err
}
```

The GET body must be consumed to clean EOF before treating the transfer as
complete. `ContentLength` is -1 when the server omits the length. A declared
length is checked against the bound before the body is returned. Unknown-length
bodies use one probe byte to enforce the limit. The request deadline stays
active until clean EOF or `Close`.

`PutObjectOutput.SHA256` is the locally computed upload digest. The CLI
computes and reports the SHA256 of bytes received during a download. `GetObject`
does not return a digest; SDK callers that need one must hash `Body` while
consuming it through clean EOF. A download digest describes received bytes and
does not verify them against a remote checksum. ETag is optional opaque
metadata, not a checksum contract.

## Unconfirmed uploads and other tools

A PUT may have reached the server even when the client cannot confirm its
response. The client does not retry, roll back, or send a cleanup DELETE. Fetch
the exact key to a fresh path, then compare its length and SHA256 with the
intended file before deciding whether to upload again. An HTTP 412 alone does
not prove that the existing bytes match your upload.

The native feature provides one-object PUT and GET, one-page listing,
HEAD metadata, current-name DELETE, and native server-side COPY. Create-only
copy and move remain unavailable. It does not append, sync, resume,
multipart-transfer, or create presigned URLs.
Use an external S3 tool such as rclone when you need sync or multipart
transfers. The native SDK and CLI remain separate from those tools.
