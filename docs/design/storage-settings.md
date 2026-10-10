# vStorage: settings

Bucket versioning, encryption, CORS, and public reads for
[vStorage](storage.md). Encryption's contract and pending live checks are
below. The
operation table, envelope errors, and testing rules are in that design; the
calls and live results are in [Versioning](storage-api.md#versioning),
[CORS](storage-api.md#cors), and [Public access](storage-api.md#public-access);
the commands are in [vStorage: CLI](storage-cli.md).

Paths are `ceph/projects/{p}/buckets/{b}/<setting>` under `internal/v1/`,
with both region headers. Every call checks the project and bucket as in
[Identifiers](storage.md#identifiers); encryption inputs name the bucket
field `BucketName`.

## Versioning

- `GetBucketVersioning` returns `Enabled` (`data.versioning`) and `Status`
  (`data.versioningStatus`), the server's `Off`, `Enabled`, or `Suspended`
  unchanged; an unknown value passes through. A success with no `data` is
  a decode error.
- `Off` holds only until the first put. A put with `false` gives
  `Suspended`, also on a bucket never versioned, and nothing returns a
  bucket to `Off`. The doc comment and the wiki say so.
- `PutBucketVersioning` takes `Enabled *bool`, required, and sends
  `{"enable": <value>}`. A nil `Enabled` is `ErrInvalidInput` with no
  request, so the CLI needs `--enabled=true` or `--enabled=false`. The
  server reads a body without `enable` as `false`, so the SDK never omits
  it. Success is `success: true`; the Output is `{}`.
- The put names the target state, so a repeat gives the same result and
  the transport's retries stay. The change shows on the next read.
- With versioning on, overwrites and deletes keep old versions, which use
  quota and keep `DeleteBucket` refusing the bucket
  ([Delete bucket](storage.md#delete-bucket)). Suspending keeps the
  versions already stored. The wiki says so.

## Encryption

Status: Accepted (2026-10-10). The live check passed on 2026-10-10.

This section adds per-bucket default encryption for backup workloads and
supersedes the encryption non-goal in [vStorage](storage.md#non-goals).
Treat encryption as server-managed. The console offers no bucket KMS key
choice; KMS icons belong to other products. Do not name an S3 algorithm
until the live check establishes one.

### Console evidence

Read-only console code inspection on 2026-10-10 found:

- `GET internal/v1/ceph/projects/{project}/buckets/{bucket}/encryption`
  reads `data.encryption`, a boolean. The console rejects `success: false`
  and maps envelope code 403 to a permission error.
- `PUT` on that path sends only `{"enable": true}` or `{"enable": false}`
  and uses the same envelope. Enable and disable have confirmation modals.
- Bucket creation uses `POST` on the bucket path with `bucketName` and
  `objectLockEnabled` only. Encryption needs a separate PUT. The SDK's
  existing create body is `{"status":"Disabled"}`; keep that tested body
  for this feature and verify it live, rather than infer a replacement.
- Bucket details carry `enableEncryption`. When true, the console disables
  object rename, move, and copy. This is not evidence of an S3 restriction.
- `vstorage_encrypt_document_url` exists but has an empty value.

These are console contracts, not live API results.

### SDK contract

- `GetBucketEncryptionInput` has `Region`, `ProjectID`, and `BucketName`
  strings. `ProjectID` and `BucketName` are required. Use the existing
  region resolution, both region headers, and project and bucket path
  checks. `BucketName` follows the `Bucket` validation rule.
- `GetBucketEncryptionOutput` has `Enabled bool`, from `data.encryption`.
  Absent or null `data`, an absent or null `encryption`, and a non-boolean
  value give `*APIError` code `InvalidResponse`. Never decode missing
  encryption state as false.
- `PutBucketEncryptionInput` has the same identifiers plus `Enabled bool`.
  This is a complete target state, not a partial update: false means
  disable. Send `enable` even when false. Unlike versioning's `*bool`, the
  SDK cannot distinguish an omitted field from false; document that rule.
- `PutBucketEncryptionOutput` is `{}`. After a successful PUT envelope,
  call `GetBucketEncryption` once under the caller's context. Return
  success only when `Enabled` matches the requested state. No polling or
  `NoWait` option is specified without evidence of asynchronous behavior.
- A refused PUT returns the existing envelope error, including
  `ErrPermission` for code 403. A failed or mismatching read after an
  accepted PUT wraps `storage.ErrNotSettled`, preserves any read error,
  and names the bucket and `GetBucketEncryption` as the recovery check.
  An uncertain PUT response says the change may have happened; it must
  not become success merely because a later read matches.
- Apply the [missing-bucket fallback](#missing-bucket) to an empty 2xx
  response on either encryption operation. Confirm the actual missing
  bucket response live; do not assume these routes match versioning.
- PUT specifies a target state and is expected to be idempotent. Until
  repeat PUT behavior is confirmed live, suppress automatic resends.
  Keep the normal PUT retries only after that confirmation. Neither the
  CLI nor the create sequence retries the whole operation.

Existing inputs keep `Bucket`; only the two new inputs use `BucketName`.
The new fields do not rename or remove any existing field.

### Create with encryption

`CreateBucketInput.Encryption bool` defaults to false. False preserves the
current create and read behavior and sends no encryption call, including
when the bucket already exists. True requests these ordered steps:

1. Create the bucket with the existing POST and retry rules. On an
   ambiguous POST failure, return the existing recovery error; do not
   proceed to enable encryption on a bucket whose creation is unconfirmed.
2. Call `PutBucketEncryption` with `Enabled: true`. Its confirmation read
   must report true. Map the existing create input's `Bucket` to
   `BucketName` without changing the name or project.
3. Read `GetBucket` for the existing `CreateBucketOutput`. Return success
   only after encryption is confirmed and this read succeeds. A failure
   of this final read says encryption was confirmed but reading the bucket
   failed, and preserves the read error.

After a successful POST, an enable failure, cancellation before enable,
or failed confirmation returns a new `storage.ErrBucketEncryptionIncomplete`
with a nil Output. Preserve the underlying error for `errors.Is` and
`errors.As`. The CLI reports `BucketEncryptionIncomplete`, exit 1, ahead
of any wrapped permission, cancellation, or `NotSettled` classification,
and prints no success Output.

Use a new sentinel because the bucket exists but the requested setup is
incomplete. `ErrNotSettled` alone would describe a refused enable as an
accepted write awaiting confirmation. Keep `ErrNotSettled` for the
standalone PUT's accepted but unconfirmed state.

The partial-create error names the bucket. When enable was not sent, was
refused, or a confirmation reads false, say the bucket exists without
encryption enabled by this call. For a lost response or failed read, say
the bucket exists but encryption is unconfirmed; do not assert false when
the server may have enabled it. Recommend `get-bucket-encryption`, then
`put-bucket-encryption --enabled=true` if needed after fixing the cause.
Do not upload backups until a read confirms true. Never delete the bucket
automatically: a repeated create can refer to an existing bucket with data.

Create and enable are not atomic. A concurrent client can upload before
encryption is enabled; callers must delay uploads until successful return.
A repeated create with `Encryption: true` also enables an existing bucket.
The feature neither rewrites objects nor claims existing data is encrypted.

### CLI and security

- `storage get-bucket-encryption` is a read and prints `Enabled`.
- `storage put-bucket-encryption` requires `--enabled=true` or
  `--enabled=false`. Use a CLI-only presence check, including for
  `--cli-input-json`; omission or null exits 2 without a request. Pass the
  resulting bool to the public SDK. Help says "encryption", not
  "versioning".
- Both commands expose `BucketName` as `--bucket` to match existing bucket
  commands. JSON inputs use `BucketName`. Keep the existing project flag
  and region handling.
- `storage create-bucket --encryption` sets the new create field. Omission
  or `--encryption=false` does not disable encryption on an existing bucket.
- Both writes obey read-only mode. No prompt or `--yes` is added for the
  reversible toggle, following [ADR 0002](../adr/0002-write-api-conventions.md).
  Help states that disabling changes the default for future uploads and
  that effects on existing objects remain unverified.
- No key material enters these requests. Keep credentials and live
  account data out of errors, debug output, and fixtures. An error may name
  the caller-supplied bucket for recovery; public artifacts use fake names.

### Verification and release

Unit tests cover true and false bodies, strict boolean decoding, identifier
checks, region headers, envelope errors, the empty-body fallback, and
confirmation reads that match, differ, fail, or are canceled. Test create
ordering, the unchanged false path, an existing bucket, ambiguous POST
failure, each failure after POST, preserved causes, nil failure Output,
and no automatic DELETE. CLI tests cover flag and JSON presence, help text,
read-only refusal, error precedence, and no success output on failure.

The manager obtains approval for buying and deleting a throwaway test
project, naming the account, region, and resources under
[live-data rules](../../instructions/live-data.md). Run only in that new
project, never a shared test project or a production project:

1. Create a `vngcloud-live-<8 hex>` bucket with encryption, then read it
   back. Record GET and PUT HTTP statuses and envelope shapes, including
   false and true states. Verify the existing create body still works.
2. Toggle off and on, reading after each write. Repeat the same PUT and
   read again to check idempotence and immediate visibility. Call both
   encryption operations on a missing bucket and record their responses.
3. Create a temporary S3 key, kept only in the runtime that uses it. Run
   S3 `GetBucketEncryption`; record the returned algorithm or error. Upload
   without an explicit encryption header and record whether the response
   carries `x-amz-server-side-encryption`, including its value. On the
   encrypted bucket, confirm that `PutObject` with `If-None-Match: *`
   answers 200 for a new key and 412 for an existing one, that a multipart
   upload completes, and that `DeleteObjects` removes objects.
4. Compare objects written before enable, while enabled, and after disable.
   Check reads and available S3 metadata after each toggle. Try S3 copy
   while encryption is on; test rename or move as copy followed by delete,
   recording each result. Do not infer re-encryption from readability alone.
5. Delete every object, version, delete marker, and multipart upload made
   by the run, then its bucket and key, then the project. Verify cleanup;
   report leftovers instead of deleting a parent that still holds data.

The live check on 2026-10-10 in `HCM04` found: `GET` answers
`data.encryption` and `PUT` answers `data: true`, visible at once; S3
`GetBucketEncryption` reports `AES256` (SSE-S3); a plain `PutObject` on an
enabled bucket answers `x-amz-server-side-encryption: AES256`; objects
written before enabling stay unencrypted and objects written while enabled
stay `AES256` after disabling; `If-None-Match: *` answers 200 then 412, a
two-part multipart upload completes as `AES256`, and `DeleteObjects` works;
`CopyObject` of an encrypted source answers 501 `NotImplemented`, and a
copy of an unencrypted source succeeds but is not encrypted. Effects
invisible through S3 stay unresolved. Raw responses stay in ignored output paths; only
sanitized response shapes become fixtures. Never capture key secrets.

Release this as one encryption feature after the live contract check,
SDK and CLI implementation, matching wiki updates, and an adversarial
write review. The review covers false defaults, partial creation, retries,
and failure reporting. Follow the repository's local checks and exact-commit
CI gate. Any live result that changes this contract needs a design update
before release.

### S3 compatibility notes (HCM04, observed 2026-10-10)

These observations help S3-client users; the SDK does not cover objects.
They do not establish behavior in other regions or verify encryption.

- `PutObject` with `If-None-Match: *` returns 200 for a new key and 412 for
  an existing key.
- `PutObject` with a matching quoted ETag in `If-Match`, the RFC form SDKs
  send, returns 412. The matching unquoted ETag returns 200.
- `DELETE` with a wrong `If-Match` returns 204 and deletes the object:
  the condition is ignored. Do not rely on conditional deletion for safety.
- OpenTofu 1.12.6's S3 backend with `use_lockfile = true` and path-style
  addressing locks correctly. A second plan gets 412 and
  "Error acquiring the state lock".
- Path-style addressing, 12 MB multipart uploads, `ListObjectsV2` with
  prefix and delimiter, `DeleteObjects`, and AWS CLI 2.37's default CRC32
  checksums work.
- `ListObjectsV2` omits `KeyCount` when the count is 0.
- On a failed conditional PUT, AWS CLI 2.37 prints
  "argument of type 'NoneType' is not a container or iterable" instead of
  the 412 response.

## CORS

`CORSRule` has `AllowedOrigins`, `AllowedMethods`, and `AllowedHeaders`
(`[]string`), `MaxAgeSeconds` (`int`), `ExposeAllowedHeaders` (`bool`),
and `ExposedHeaders` (`[]string`, read-only), as set by
[decisions 40 and 50](storage-decisions.md#owner-decisions).

- `PutBucketCORS` takes `Rules []CORSRule`, required, and replaces every
  rule. The body is a bare JSON array, built from a request type with the
  server's capitalised keys: `AllowedOrigins`, `AllowedMethods`,
  `AllowedHeaders`, `ExposeHeaders`, and `MaxAgeSeconds`. Empty
  `AllowedHeaders` and a zero `MaxAgeSeconds` are omitted.
  `ExposeHeaders` is sent, equal to `AllowedHeaders`, only when
  `ExposeAllowedHeaders` is true. `ExposedHeaders` is never sent.
- `GetBucketCORS` decodes `data.rules[]` with the response's camel-case
  keys (`allowedOrigins`, `allowedMethods`, `allowedHeaders`,
  `exposedHeaders`, `maxAgeSeconds`); `id` is always null and is dropped.
  It sets `ExposeAllowedHeaders` when `exposedHeaders` is not empty, so a
  rule read and put back keeps its exposed headers. A success with no
  `data` returns an empty, non-nil `Rules`, so the CLI prints
  `{"Rules": []}`.
- The server stores exposed headers only when the put names
  `ExposeHeaders`, and then copies the allowed headers into them, never
  the value sent. Without `ExposeHeaders`, `exposedHeaders` reads null and
  the preflight carries no `Access-Control-Expose-Headers`, so a browser
  reads only the CORS-safelisted response headers. To let a browser read
  `ETag`, for example after an upload, a rule lists `ETag` in
  `AllowedHeaders` and sets `ExposeAllowedHeaders`. The doc comment and
  the wiki say so and call `ExposedHeaders` server-derived.
- The server returns `allowedMethods` in its own order. The wiki tells
  callers to compare methods as sets.
- `DeleteBucketCORS` returns `{}`, also when no rules exist. Put and delete
  give the same result when repeated, so both keep the transport's retries.
- A failed put keeps the previous rules. Rules and a delete take effect on
  the next data-plane preflight.

### Rule checks

`PutBucketCORS` returns `ErrInvalidInput`, with no request, when:

- `Rules` is empty: the server refuses `[]` with `MalformedXML`. To remove
  every rule, call `DeleteBucketCORS`.
- A rule has no `AllowedOrigins`, or an empty origin, or an origin with
  more than one `*`.
- A rule sets `ExposeAllowedHeaders` with no `AllowedHeaders`: there is
  nothing to expose.
- A rule has no `AllowedMethods`, or a method other than `GET`, `PUT`,
  `POST`, `DELETE`, or `HEAD`, compared exactly. The server refuses
  lower case and `OPTIONS` with a generic code 114, and accepts an empty
  method list that would match nothing.
- `MaxAgeSeconds` is below 0.

The message names the rule index and the field. The server still owns the
rest: it accepts an origin without a scheme and unknown fields, and the SDK
does not refuse them.

## Public read

The console's public access route is unusable: `GET` and `PUT` answer 403
`IAM_PERMISSION_DENIED` to the test IAM user, which holds `vstorage:*`, for
every body, as an unmapped route does. The SDK has no public access call.
The ACL route can grant `AllUsers` read, which opens the anonymous bucket
listing but no object; it stays a non-goal.

A bucket policy is the one way to serve objects anonymously. The wiki gives
this public-read template, for `put-bucket-policy`:

```json
{"Version": "2012-10-17", "Statement": [
  {"Sid": "PublicRead", "Effect": "Allow", "Principal": "*",
   "Action": ["s3:GetObject"],
   "Resource": ["arn:aws:s3:::<bucket>/*"]}]}
```

It grants anonymous object reads and no listing. A bucket policy holds one
document, so a bucket that also has a per-bucket key keeps the key's
statements in the same document. Deleting the policy ends public reads at
once. `GetBucket`'s `isPublic` stays null and `allowPublicAccess` false
whatever the policy says, so the SDK reports no public state.

### Public principal

`put-bucket-policy` needs `--yes` when the document has a public principal,
and exits 2 with no request otherwise. A statement has one when its
`Effect` is not `Deny` and its `Principal` is a string containing `*`, or an
object whose `AWS` value is such a string or an array holding one. Any `*`
counts, not only `"*"`, since Ceph's wildcard matching in a principal is
unverified. The SDK exports `storage.PolicyHasPublicPrincipal(policy string)
(bool, error)` so the CLI and SDK callers share one rule; it returns an
error for a document `PutBucketPolicy` would refuse. `PutBucketPolicy`
itself does not ask for consent; the doc comment points to the function.

## Missing bucket

On a bucket that no longer exists, `GET`, `PUT`, and `DELETE` on the
versioning, CORS, and policy routes answer HTTP 200 with an empty body. A
bucket whose policy has a statement without a principal gives the same
answer while it exists ([Bucket policy](storage-keys.md#bucket-policy)).

So for `GetBucketVersioning`, `PutBucketVersioning`, `GetBucketCORS`,
`PutBucketCORS`, `DeleteBucketCORS`, `GetBucketPolicy`, `PutBucketPolicy`,
and `DeleteBucketPolicy`, a 2xx with an empty body leads to one `GetBucket`
with the same Input fields:

- `ErrNotFound`: the call returns that error, which the CLI prints as
  `NotFound`, exit 4.
- Any other result: the call returns the `EmptyResponse` `*APIError`, as
  before; for a write, it says the change may have happened. The read's
  own error is not returned.

The read is never retried by this rule and runs under the caller's context.
A read-only profile allows it, since it changes nothing.

## Errors

| Case | Result | CLI code and exit |
|-|-|-|
| `Enabled` nil, a CORS rule check fails | `ErrInvalidInput`, no request | `InvalidUsage`, 2 |
| Public principal without `--yes` | No request | `InvalidUsage`, 2 |
| Server refuses CORS, code 114 or 400 `MalformedXML` | `*APIError` with the server's message | That code, 1 |
| Empty body, then `GetBucket` reports `ErrNotFound` | `ErrNotFound` | `NotFound`, 4 |
| Empty body, bucket exists or the read fails | `*APIError` `EmptyResponse` | 1 |
| Not JSON or a wrong body shape, HTTP 400 | `*APIError` | 1 |

A wrong body shape cannot leave the SDK; the 400 row covers a server change.

## Testing

Unit tests use `httptest`:

- Versioning: the `enable` body for true and false; nil `Enabled` sends
  nothing; fixtures for `Off`, `Enabled`, and `Suspended`; no `data` is an
  error.
- CORS: the put body is a bare array with capitalised keys;
  `ExposeHeaders` equals `AllowedHeaders` when `ExposeAllowedHeaders` is
  true and is absent otherwise; zero `MaxAgeSeconds` and empty headers are
  omitted; each rule check refuses with no request; the get fixture fills
  `ExposedHeaders` and `ExposeAllowedHeaders`; no `data` gives an empty,
  non-nil list; code 114 and `MalformedXML` reach `*APIError.Message`; a
  second delete is `{}`.
- Missing bucket, for each of the eight calls: an empty body then a
  `GetBucket` code 404 gives `ErrNotFound`; an empty body then the bucket
  gives `EmptyResponse`; exactly one `GetBucket`.
- `PolicyHasPublicPrincipal`: `"*"`, `{"AWS":"*"}`, `{"AWS":["*"]}`, and an
  ARN with `*` are public; a `Deny` with `*` and a named ARN are not; the
  CLI refuses without `--yes` and sends with it.

The live write test `TestLiveWriteStorageBucketSettings` needs approval
under the live-data rules and `VNGCLOUD_LIVE_STORAGE_PROJECT_ID`, and uses
one bucket. It deletes leftover `vngcloud-live-` buckets, then:

1. Creates the bucket. Versioning reads `Off`; a put of true reads
   `Enabled`; a put of false reads `Suspended`. A put without `Enabled`
   returns `ErrInvalidInput`.
2. Calls `GetBucketVersioning` and `GetBucketCORS` on a missing bucket:
   each returns `ErrNotFound`.
3. CORS reads `[]`. A put of one rule without `ExposeAllowedHeaders`,
   then a get: equal origins, headers, and max age, methods equal as a
   set. A put of the rule with `ExposeAllowedHeaders`, then a get:
   `ExposedHeaders` equal to `AllowedHeaders` and the flag set. The rule
   read back and put again keeps its exposed headers.
4. The client refuses a lower-case method and `ExposeAllowedHeaders` with
   no `AllowedHeaders` with `ErrInvalidInput`, and the rule stays. Raw
   puts of an unknown method, an empty list, lower-case keys, and
   `ExposeHeaders` with no `AllowedHeaders` log status and code only.
5. An anonymous `OPTIONS` preflight answers 200 with
   `Access-Control-Allow-Origin` and `Access-Control-Expose-Headers`
   naming the allowed headers. A delete, a second delete, an empty get,
   and a preflight answering 403 without `Access-Control-Allow-Origin`.
6. Deletes the bucket, then calls `GetBucketVersioning`, `GetBucketCORS`,
   and `GetBucketPolicy` on it: each returns `ErrNotFound`.
7. Cleanup deletes the CORS rules and the bucket and asserts no
   `vngcloud-live-` bucket remains. It logs statuses and counts only.

### Live checks

Public read through a bucket policy is covered by
`TestLiveWriteStorageBucketPolicy`.
Open, with no live test:

- Whether the `DeleteBucket` guard refuses a bucket that holds only
  object versions with `ErrBucketNotEmpty`.
- Deleting object versions by ID with an S3 key.
