# Storage Object Copy

Copy one exact current object name with native server-side CopyObject. Content
stays on the service, and the source remains. Use the separate
`storage.NewObjectClient` S3 client and the explicit private credential file
from [Storage Objects](Storage-Objects.md#credentials-and-regions).

```sh
vngcloud storage copy-object --s3-region HCM04 \
  --s3-credentials-file ./storage.credentials \
  --source-bucket app-files --source-key archives/bundle.bin \
  --bucket app-backups --key archives/bundle.bin --replace --yes
```

## Consent and scope

Both `--replace` and `--yes` are required. Consent permits replacing any
current destination, including one created or changed concurrently. It also
permits creation at an empty destination name. Native create-only copy is
unavailable; neither `--yes` alone nor an earlier HEAD enables replacement.
Move and rename remain unavailable.

Require explicit source and destination bucket/key pairs, `--s3-region`, and
`--s3-credentials-file`. Both buckets must belong to the same project and
region, using one credential pair and exactly `HCM04` or `HAN02`. The service
owns authorization; the client adds no console lookup or second credential.
Copy normally requires `s3:GetObject` on the source and `s3:PutObject` on the
destination. Recorded provider tests used broader grants and do not prove
every minimal permission combination.

Identical bucket/key pairs are refused. Keys are literal UTF-8 strings of
1 through 1,024 bytes. Slashes, repeated slashes, dot segments, Unicode, and
percent sequences retain their meaning. A slash-ending key is one object,
not a directory. There is no wildcard or recursive copy.

The CLI supports Linux and macOS credential files. Read-only from the flag,
`VNGCLOUD_READ_ONLY`, or profile `read_only` blocks copy before credentials
open. A false value cannot disable another source's policy. Output, query
syntax, profile, timeout, and platform checks precede credentials and client
construction. Explicit global `--region` and `--project-id` are refused;
their environment/profile defaults do not select S3 resources. `--profile`
selects local policy and output settings only.

`--transfer-timeout` defaults to five minutes and accepts one second through
one hour. An earlier caller deadline wins. There is no IAM login, token cache,
ambient credential chain, body file, local spool, subprocess, fallback, or
`--cli-input-json` input.

## Metadata and compatibility

Copy selects the current source during service execution. An earlier HEAD
observation does not lock that source. Concurrent changes can alter what is
copied, and success does not freeze either name afterward. `VersionID` and
`SourceVersionID` are diagnostic observations, never version selectors or
permission to delete the source. Version history is not copied. Destination
versioning can create another version or replace a null version; storage and
version costs follow the user's service terms.

Provider default metadata behavior applies without directives or overrides.
The S3 reference expects ordinary content headers and user metadata to copy,
but GreenNode's guides do not enumerate every preserved field. This is no
lossless clone promise for metadata, ACLs, tags, retention, storage class,
redirects, or encryption. The client does not fetch and replay headers.

Both regions rely on published CopyObject compatibility. HCM04 adds recorded
plain-copy success. In recorded HCM04 behavior, encrypted sources return
501 NotImplemented, and an unencrypted-source copy can remain unencrypted
despite enabled destination bucket encryption. HAN02 encryption behavior is
unverified. No encryption inheritance guarantee or fresh live verification
is implied. See the [object operation evidence][evidence].

AWS documents a 5 GB single-request copy boundary. That reference is not a
measured GreenNode limit; GreenNode's exact copy size limit is unverified.
The service enforces its limit. Refusal never triggers multipart, a download
and upload, or a bucket setting change. There is no source preflight HEAD,
source deletion, cleanup, rollback, verification read, or retry.

`--output` and `--query` expose only accepted `ETag`, `LastModified`,
`VersionID`, `SourceVersionID`, and `ServerSideEncryption`, plus credential-
redacted `SourceBucket`, `SourceKey`, `Bucket`, and `Key`. Controls are escaped.
Unsafe or absent times remain JSON null; accepted times are UTC. Unsafe or
absent optional strings are empty. ETag is opaque, not a checksum. No content
length, local digest, source ETag, raw headers, or object content is invented.
Debug contains only operation, elapsed time, status, count, and local code.

## Outcome and recovery

HTTP 200 alone is insufficient: a complete bounded CopyObjectResult is
required. An embedded HTTP 200 Error, malformed/truncated result, response
loss, or timeout after possible dispatch can leave an unconfirmed copy.
`ObjectCopyUnconfirmed` always exits 1 ahead of wrapped auth, not-found, or
context errors. Its fixed advice is to inspect the destination before deciding
whether to repeat the copy. A rerun can replace another writer's object or
create another version. Inspection requires a separate decision with suitable
tools and permissions; the CLI never performs recovery reads automatically.

Ordinary exit codes are 0 for success, 2 for usage/config/input/read-only/
consent refusal, 3 for credentials or HTTP 401, 4 for HTTP 404, and 1 for
HTTP 403 or other failures. No failed copy prints a success output.

A query evaluation or stdout failure after confirmed success exits 1 and says
`copy completed; metadata output failed; do not retry the operation`.
The copy call runs once. Keep the completed result and inspect before any
later copy decision.

The SDK uses the same operation with explicit in-memory credentials:

```go
result, err := client.CopyObject(ctx, &storage.CopyObjectInput{
    SourceBucket: "app-files", SourceKey: "archives/bundle.bin",
    Bucket: "app-backups", Key: "archives/bundle.bin", Replace: true,
})
if errors.Is(err, storage.ErrObjectCopyUnconfirmed) {
    // Inspect the destination before deciding whether to repeat the copy.
    return err
}
if err != nil {
    return err
}
_ = result
```

This example assumes the client setup in the object guide. Every SDK method
failure returns nil output. The SDK accepts portable in-memory credentials
and makes at most one native copy dispatch without automatic recovery.

[evidence]: https://github.com/dannyota/vngcloud/blob/master/docs/design/storage-object-operations-api.md#evidence-gates
