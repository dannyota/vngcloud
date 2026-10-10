# vStorage Design

Status: Accepted (2026-09-26).

This design adds vStorage object storage management to the SDK and CLI:
buckets, their settings, S3 keys, and the service accounts that scope a key
to one bucket. aboutme creates its buckets and per-bucket keys at setup
([First user](sdk-and-cli.md#first-user)).

It builds on [SDK and CLI](sdk-and-cli.md) and [CLI](cli.md). Writes follow
[ADR 0002](../adr/0002-write-api-conventions.md). S3 keys and service
account keys are in [vStorage: keys](storage-keys.md). Versioning, CORS,
and public reads are in [vStorage: settings](storage-settings.md). The
commands and the per-bucket key setup are in [vStorage: CLI](storage-cli.md).
The release order and the owner's decisions are in
[vStorage: decisions](storage-decisions.md).

## Source

The calls, shapes, region headers, storage users, and costs this design
rests on are in [vStorage: API](storage-api.md), with their sources.

## Non-goals

- Resizing a project. Project prices, purchase, and deletion have their
  own [vStorage projects design](storage-projects.md).
- Objects, directories, presigned URLs, and uploads. Use an S3 client.
- Lifecycle, encryption, object lock, notifications, ACLs, IP range ACLs,
  usage alerts, reports, the Swift farm (`HCM03`), and Swift users.
- The console's public access route, which answers 403 to the IAM user
  ([Public read](storage-settings.md#public-read)).
- Service-account login and the external vStorage API.

## Endpoints

`endpoints.Set` and `Overrides` gain `Storage`, default
`https://vstorage.console.greennode.ai/`, and `internal/routes` gains
`ProductStorage`; storage paths are `internal/v1/...` under it. S3 keys and
service account keys use this endpoint too. The IAM accounts API on the
`Dashboard` endpoint holds no call of this design.

Every storage request except `ListRegions` sends `region: <region UUID>` and
`region_id: <region UUID>`, the same UUID in both, as the console does. The
server scopes results by `region`; without it a project list is empty and a
bucket list fails with code 114; see
[region headers](storage-api.md#region-headers). The SDK sends no `user_id`
or `portal-user-id`. The client resolves a region name to its UUID with
`ListRegions` once per `storage.Client` and caches the result. An empty `Region`
maps `hcm-3` to `HCM04` and `han-1` to `HAN02`; any other config region with an
empty `Region` is `ErrInvalidInput`, and nothing is sent.

## SDK

### storage

Operation names are `storage.<Method>`. "(r)" marks `vngcloud:"required"`,
and "L[T]" is `core.List[T]`. Every Input except `ListRegionsInput` has
`Region string`. Paths are under `internal/v1/`.

| Operation | Method and path | Input | Output |
|-|-|-|-|
| `ListRegions` | `GET regions` | none | L[Region] |
| `ListProjects` | `GET projects` | `Region` | L[Project] |
| `ListBuckets` | `GET ceph/projects/{p}?limit=1000` | `ProjectID` (r) | L[Bucket] |
| `GetBucket` | `GET ceph/projects/{p}/{b}/details` | `ProjectID` (r), `Bucket` (r) | `{Bucket}` |
| `CreateBucket` | `POST ceph/projects/{p}/buckets/{b}` | `ProjectID` (r), `Bucket` (r) | `{Bucket}` |
| `DeleteBucket` | `DELETE ceph/projects/{p}/buckets/{b}` | `ProjectID` (r), `Bucket` (r), `NoWait` | `{}` |
| `ListS3Keys` | `GET users/s3_keys?projectId={p}` | `ProjectID` (r) | L[S3Key] |
| `CreateS3Key` | `POST users/s3_keys` | `ProjectID` (r) | `{S3Key; SecretKey vngcloud.Secret}` |
| `DeleteS3Key` | `DELETE users/s3_keys/{k}` | `ProjectID` (r), `UserKeyID` (r) | `{}` |
| `AttachS3Key` | `PUT users/s3_keys/{k}/attach` | `ProjectID` (r), `UserKeyID` (r), `ServiceAccountID` (r) | `{}` |
| `DetachS3Key` | `PUT users/s3_keys/{k}/detach` | `ProjectID` (r), `UserKeyID` (r) | `{}` |
| `EnsureServiceAccountPrincipal` | `GET users/details?generated=true&project_id={p}&iam_account_id=sa-{sa}` | `ProjectID` (r), `ServiceAccountID` (r) | `{SubUserID, PrincipalARN string}` |
| `GetBucketPolicy` | `GET ceph/projects/{p}/buckets/{b}/policy` | `ProjectID` (r), `Bucket` (r) | `{Policy string}`, `""` for none |
| `PutBucketPolicy` | `PUT ceph/projects/{p}/buckets/{b}/policy` | `ProjectID` (r), `Bucket` (r), `Policy` (r) | `{}` |
| `DeleteBucketPolicy` | `DELETE ceph/projects/{p}/buckets/{b}/policy` | `ProjectID` (r), `Bucket` (r) | `{}`, also when none |
| `GetBucketVersioning` | `GET .../buckets/{b}/versioning` | `ProjectID` (r), `Bucket` (r) | `{Enabled bool, Status string}` |
| `PutBucketVersioning` | `PUT .../buckets/{b}/versioning` | plus `Enabled *bool` (r) | `{}` |
| `GetBucketCORS` | `GET .../buckets/{b}/cors` | `ProjectID` (r), `Bucket` (r) | `{Rules []CORSRule}`, empty for none |
| `PutBucketCORS` | `PUT .../buckets/{b}/cors` | plus `Rules []CORSRule` (r) | `{}` |
| `DeleteBucketCORS` | `DELETE .../buckets/{b}/cors` | `ProjectID` (r), `Bucket` (r) | `{}`, also when none |

- `CreateBucket` sends `{"status":"Disabled"}`, the console body for a
  bucket without object lock. The server answers 200 with only `name` and
  `count` set, so the Output comes from a `GetBucket` after the create; if
  that read fails, the error says the bucket was created. A create of a name
  the account already owns answers the same 200, so a rerun is safe.
- The policy calls, the `Policy` check, and the template are in
  [Bucket policy](storage-keys.md#bucket-policy).
- Versioning and CORS bodies, the CORS rule checks, and
  `PolicyHasPublicPrincipal` are in [vStorage: settings](storage-settings.md).
- On the versioning, CORS, and policy calls, an empty 2xx body leads to one
  `GetBucket` before the call reports `ErrNotFound`
  ([Missing bucket](storage-settings.md#missing-bucket)).
- `ListBuckets` sends `limit=1000`, the per-project cap, so one call returns
  every bucket. A response with `isNext: true` fails the call.
- Models keep their API JSON tags. `Bucket` maps `count` and `size` to
  `ObjectCount` and `SizeBytes`. `CreatedDate` stays the server's string:
  `ListBuckets` gives `dd/mm/yyyy hh:mm` with no time zone, and `GetBucket`
  gives null, so it is empty there. Numeric `status` values reach the caller
  unchanged.

### Keys

The S3 key, attach, and principal calls, their retries, errors, and tests
are in [vStorage: keys](storage-keys.md). `EnsureServiceAccountPrincipal`
is a write, although its method is `GET`
([Principal](storage-keys.md#principal)).

### Identifiers

Every path ID is checked before any request, reads included:

- `ProjectID`, `UserKeyID`, and `ServiceAccountID`: `core.CheckPathID`
  (`^[A-Za-z0-9-]+$`). The live checks confirm their shape.
- `Bucket`: `^[A-Za-z0-9][A-Za-z0-9._-]{0,254}$`, a path-safety check only.
  It rejects `/`, `?`, `%`, `.`, and `..`. The S3 naming rules stay on the
  server (ADR 0002 rule 5): upper case and `_` pass this check, and the
  server refuses them with code 112 (see [Envelope errors](#envelope-errors)).
  The SDK escapes the name with `url.PathEscape`, as the console escapes it.

### Envelope errors

- A 2xx envelope with `success: false` is an `*APIError` with the HTTP
  status, `Code` set to the envelope `code` as text, and `Message` set to
  `errorMsg` cut to 256 bytes. An envelope `code` from 400 to 599 also
  matches that status's sentinel, so `NotFound` exits 4.
- Envelope code 112 matches `ErrInvalidInput` on every storage call: the
  server uses it for each input check it reports, and `errorMsg` names the
  rule. The CLI prints code `112` and exits 2.
- Envelope code `-1` (`Unknown error`) has no sentinel. Outside the delete
  wait it is an ordinary `*APIError`.
- A 2xx with an empty or non-JSON body is an `*APIError` with Code
  `EmptyResponse`; for a write, the message says it may have happened.
- A 403 with a JSON array of `{code, message}` takes `Code` from the first
  element, such as `IAM_PERMISSION_DENIED`, and matches `ErrPermission`.

### Secrets

`vngcloud.Secret` is a string type in the root package. Its `String`,
`GoString`, `Format`, `LogValue`, `MarshalJSON`, and `MarshalText` all give
`[redacted]`; only `Reveal()` returns the value, so printing or encoding an
Output leaks nothing. `transport.Request.Sensitive` is set by
`CreateS3Key` and `ListS3Keys`: the `WithResponseCapture` hook never sees
such a response,
and a decode error never quotes its body. `--debug` already logs no body.

### Retries

- `CreateBucket` is a `POST`, retried only after a 429 or a failed dial
  (ADR 0002 rule 2). After a 5xx or a network error the bucket may exist;
  the error names `get-bucket`, and the create can be rerun.
- `CreateS3Key`, `AttachS3Key`, and `DetachS3Key` set `Once`; see
  [key retries](storage-keys.md#retries).
- Other deletes and puts keep the transport's retries. A retried bucket
  delete that finds nothing returns `NotFound`.

### Delete bucket

`DeleteBucket` reads the bucket first and returns `ErrBucketNotEmpty`,
sending no `DELETE`, unless the bucket is provably empty. The read decodes
`count`, `size`, and `usedCapacity` as nullable, and refuses when:

- `count` is null or absent, since some answers null most bucket fields;
- `count` is above 0;
- `size` or `usedCapacity` is above 0, since old versions can leave
  `count` at 0.

A null `size` or `usedCapacity` with `count` 0 passes. A failed read is
wrapped as "reading the bucket before the delete". The server never refuses:
its `DELETE` removes a bucket with its objects, versions, and delete
markers within a second, so this guard is the only protection. `count`
counts object versions, and a delete marker adds nothing, so a bucket left
with only delete markers reads as empty and deletes cleanly. No force flag:
emptying is S3 client work.

The server deletes asynchronously: the `DELETE` answers 200 at once, and
for about a second reads still show the bucket or fail. `DeleteBucket`
accepts 200 and 204; a 204 has no body and counts as success. Unless
`NoWait` is set, `DeleteBucket` then calls `GetBucket` every second for up
to 30 seconds:

- `ErrNotFound`: settled; the call returns `{}`.
- The bucket, envelope code `-1`, or `EmptyResponse`: still deleting; poll
  again.
- Any other error: returned at once, with a message that the delete was
  accepted.
- Bound reached: an error wrapping `storage.ErrNotSettled`, which says the
  delete was accepted and must not be repeated. The CLI prints
  `NotSettled`, exit 1, as for the other packages' `ErrNotSettled`.

After a settled delete, `ListBuckets` omits the bucket and a repeat
`DeleteBucket` returns `NotFound`. With `NoWait`, a repeat inside the window
can stop at the first read's error, sending no `DELETE`. The CLI
`delete-bucket` waits; `--no-wait` sets `NoWait`.

## Errors

| Case | Result | CLI code and exit |
|-|-|-|
| Missing field, bad ID or bucket shape, unmapped region | `ErrInvalidInput`, no request | `InvalidUsage`, 2 |
| Server refuses an input, envelope code 112 | `ErrInvalidInput` | `112`, 2 |
| Bucket delete not settled in 30 s | `storage.ErrNotSettled` | `NotSettled`, 1 |
| Bucket holds objects or data, or its count is not reported | `ErrBucketNotEmpty`, no delete sent | `BucketNotEmpty`, 1 |
| IAM policy denies the action | `ErrPermission` | `IAM_PERMISSION_DENIED`, 1 |
| `Policy` is not a JSON object with a non-empty `Statement` array | `ErrInvalidInput`, no request | `InvalidUsage`, 2 |
| Server refuses a policy, envelope code 400 or 114 | `*APIError` with the server's message | That code, 1 |
| Empty 2xx body on a settings or policy call, bucket missing | `ErrNotFound` after one `GetBucket` | `NotFound`, 4 |
| Envelope `success: false` | `*APIError`, envelope code | That code, 1 or 4 |
| Empty 2xx body | `*APIError` `EmptyResponse` | 1 |

Key, attach, and principal errors are in
[key errors](storage-keys.md#errors); versioning and CORS errors are in
[settings errors](storage-settings.md#errors). The CLI error codes list in
[CLI](cli.md#errors-and-exit-codes) gains `BucketNotEmpty` and
`SecretFileFailed`.

## Security

- Every write gets an adversarial review before its release. It checks: the
  secret never reaches stdout, stderr, `--debug`, an error, a response capture,
  or a fixture; `--secret-file` refuses existing paths and symlinks and
  creates mode 0600; the orphan key is deleted after a failed write or a
  missing secret; no key create resend of any kind; path checks on every
  call; the `region` and `region_id` headers on every storage call; no
  `DELETE` for a non-empty bucket; `--yes` where the table says; read-only
  refusal; and no state created by a read.
- An unattached key has its creator's rights on the whole project. The
  wiki says so, gives the
  [per-bucket key](storage-cli.md#per-bucket-key) as the target, and never
  advises a key made by a broad user or the root. Key and attach rules are
  in [key security](storage-keys.md#security).
- Bucket names, project IDs, access keys, service accounts, and policies are
  account data, and so is the `GetBucket` `owner` object (the account email
  in base64 and the storage user ID). Fixtures use `<id>`, `<account>`,
  `<access-key>`, and `<secret>`, and sanitize `owner`.
- The console API is undocumented and may change; fixed models make a changed
  field fail a fixture test.

## Testing

Unit tests use `httptest`:

- Sanitized fixtures and decode tests in `testdata/storage/` for every
  read and create, plus a `success: false` envelope, the 403 array, and an
  empty 200.
- Request bodies for every write; `region` and `region_id` both sent with
  the same UUID on every call except `ListRegions`, which sends neither; the
  region mapping and its unmapped case; `limit=1000` and `isNext`.
- `DeleteBucket` sends no `DELETE` when `count` is above 0, null, or
  absent, or when `size` or `usedCapacity` is above 0; a null `size` or
  `usedCapacity` with `count` 0 passes; a 204 `DELETE` succeeds. The wait
  polls through the bucket, code `-1`, and an empty body, settles on code
  404, returns another error at once, and reaches `ErrNotSettled` on an
  injected clock; `NoWait` sends one `DELETE` and no poll.
- Code 112 matches `ErrInvalidInput`; a duplicate create returns the
  bucket.
- S3 keys: the `projectId` query and bodies; the list fixture's
  `secretKey` never reaches the model or a capture hook; the create
  fixture is synthesized from the recorded field shape, since a sensitive
  response is never captured; a create response without
  `userKeyId` or `accessKey` is an error; one without `secretKey` returns
  the key and `ErrNoSecret`; one `POST` after a 502, a network error, and
  a failed dial; the CLI deletes the key after `ErrNoSecret` and after a
  failed file write.
- Secrets: `fmt` verbs, `slog`, and `json.Marshal` give `[redacted]`; no
  capture hook sees a sensitive response; `--debug`, errors, stdout, and
  stderr never hold the fixture secret.
- `--secret-file`: an existing file or symlink and a missing directory exit
  2 with no request; mode 0600 and content; a failed write deletes the key.
- Statuses 200, 201, 204, 400, 403, 404, 409, and 5xx; no create retry after
  a 502; path rejection for `..`, `.`, `/`, `?`, and empty values.
- CLI golden tests, `--yes`, and read-only refusal with no request sent.

Live tests follow [live data](../../instructions/live-data.md); each write
run needs the owner's approval naming the account, region, and project.

- `make live` adds `ListRegions`, `ListProjects` in both regions, and,
  when the project is set, `ListS3Keys`, logging counts only.
- The live write test skips unless `VNGCLOUD_LIVE_STORAGE_PROJECT_ID` is
  set, and never logs it. It deletes leftover `vngcloud-live-` buckets,
  never a project, then creates `vngcloud-live-<8 hex>` as a bucket and
  a key written to a temp `--secret-file`, and checks that the list holds
  the key's access key. Keys have no name, so the test deletes only the
  keys it made: `t.Cleanup`, registered as each ID is known, deletes the
  key and the bucket and asserts neither remains. It logs only statuses
  and counts. Later releases add their resources the same way; the S4 and
  S5 tests are in [key testing](storage-keys.md#testing), and the S6 tests
  in [settings testing](storage-settings.md#testing).
- Live tests on the shared test project never run at the same time: each
  sweeps leftover `vngcloud-live-` buckets, which deletes another run's
  buckets mid-test. Hand probes name buckets `vngcloud-probe-<8 hex>`,
  which no sweep matches, and delete them themselves.

## Live checks before code

The test IAM user has vStorage access, and the test account has a project
in `HCM04`. Writes need the owner's approval.

Answered: `ListProjects` returns the project once `region` is sent, so an
empty list means none; the headers the server needs; the empty bucket list;
bucket shapes and dates; an unknown bucket; create, duplicate, and invalid
name; empty and repeated delete; delete of a bucket holding objects or
versions; console S3 key create, list, list fields, repeat and unknown
delete, the 11th key, and data-plane use; the accounts API create's 500; the
S4 probes; the S5 scope probe; and the S6 probes of versioning, CORS,
public access, ACLs, and a missing bucket. The results are in
[vStorage: API](storage-api.md) and
[key live checks](storage-keys.md#live-checks).

1. S5, by hand, once, before the tag: an rclone copy of a file large enough
   for a multipart upload, with a key under the
   [template](storage-keys.md#template).
2. Open for S6, none blocking: whether the root user can use the
   `public_access` route, or the route is unmapped; `data.versioning` on a
   `Suspended` bucket (the live test asserts `false`); the server's answer
   to a zero or absent `MaxAgeSeconds`; and whether `details` and the bucket
   `DELETE` of a missing bucket still answer code 404, as recorded, or the
   empty body the settings routes give.
3. Also open: an unknown project's code, and whether bucket names are
   unique across accounts.
4. The next month's bill shows nothing beyond the project package.
