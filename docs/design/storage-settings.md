# vStorage: settings

Bucket versioning, CORS, and public reads for [vStorage](storage.md). The
operation table, envelope errors, and testing rules are in that design; the
calls and live results are in [Versioning](storage-api.md#versioning),
[CORS](storage-api.md#cors), and [Public access](storage-api.md#public-access);
the commands are in [vStorage: CLI](storage-cli.md).

Paths are `ceph/projects/{p}/buckets/{b}/<setting>` under `internal/v1/`,
with both region headers. Every call checks `ProjectID` and `Bucket` as in
[Identifiers](storage.md#identifiers).

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

The S6 live write test has the S5 approval and variables and uses its
The live write test `TestLiveWriteStorageBucketSettings` has the S5
approval and variables and uses one bucket. It deletes leftover
`vngcloud-live-` buckets, then:

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

Public read through a bucket policy is covered by the S5 live write test.
Open, with no live test:

- Whether the `DeleteBucket` guard refuses a bucket that holds only
  object versions with `ErrBucketNotEmpty`.
- Deleting object versions by ID with an S3 key.
