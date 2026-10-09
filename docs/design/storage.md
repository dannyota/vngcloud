# vStorage Design

Status: Accepted (2026-09-26).

This design adds vStorage object storage management to the SDK and CLI:
buckets, their settings, S3 keys, and the service accounts that scope a key
to one bucket. aboutme creates its buckets and per-bucket keys at setup
([First user](sdk-and-cli.md#first-user)).

It builds on [SDK and CLI](sdk-and-cli.md) and [CLI](cli.md). Writes follow
[ADR 0002](../adr/0002-write-api-conventions.md). S3 keys and service
account keys are in [vStorage: keys](storage-keys.md). The commands and the
per-bucket key setup are in [vStorage: CLI](storage-cli.md).

## Source

The calls, shapes, region headers, storage users, and costs this design
rests on are in [vStorage: API](storage-api.md), with their sources.

## Non-goals

- Creating, resizing, or deleting a project: a paid checkout, so a console
  step. The SDK would need a quote (ADR 0002 rule 8) and its own design.
- Objects, directories, presigned URLs, and uploads. Use an S3 client.
- Lifecycle, encryption, object lock, notifications, ACLs, IP range ACLs,
  usage alerts, reports, the Swift farm (`HCM03`), and Swift users.
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
| `GetBucketPolicy` | `GET .../buckets/{b}/policy` | `ProjectID` (r), `Bucket` (r) | `{Policy string}` |
| `PutBucketPolicy` | `PUT .../buckets/{b}/policy` | plus `Policy` (r) | `{}` |
| `DeleteBucketPolicy` | `DELETE .../buckets/{b}/policy` | `ProjectID` (r), `Bucket` (r) | `{}` |
| `GetBucketVersioning` | `GET .../buckets/{b}/versioning` | `ProjectID` (r), `Bucket` (r) | `{Enabled bool}` |
| `PutBucketVersioning` | `PUT .../buckets/{b}/versioning` | plus `Enabled bool` | `{}` |
| `GetBucketCORS` | `GET .../buckets/{b}/cors` | `ProjectID` (r), `Bucket` (r) | `{Rules []CORSRule}` |
| `PutBucketCORS` | `PUT .../buckets/{b}/cors` | plus `Rules` (r) | `{}` |
| `DeleteBucketCORS` | `DELETE .../buckets/{b}/cors` | `ProjectID` (r), `Bucket` (r) | `{}` |
| `GetBucketPublicAccess` | `GET .../buckets/{b}/public_access` | `ProjectID` (r), `Bucket` (r) | `{Public bool}` |
| `PutBucketPublicAccess` | `PUT .../buckets/{b}/public_access` | plus `Public bool` | `{}` |

- `CreateBucket` sends `{"status":"Disabled"}`, the console body for a
  bucket without object lock. The server answers 200 with only `name` and
  `count` set, so the Output comes from a `GetBucket` after the create; if
  that read fails, the error says the bucket was created. A create of a name
  the account already owns answers the same 200, so a rerun is safe.
- `PutBucketPolicy` sends `{"policy": "<Policy>"}`. `Policy` is the JSON
  document as a string; the SDK checks only that it is valid JSON. A put
  replaces the whole policy.
- `PutBucketCORS` replaces every rule. `CORSRule` has `AllowedOrigins`,
  `AllowedMethods`, `AllowedHeaders`, `ExposeHeaders`, and `MaxAgeSeconds`;
  the request uses capitalised keys and the response camel case.
- `PutBucketVersioning` always sends `{"enable": <Enabled>}`, since both
  values are meaningful. The public access body awaits the live checks.
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
wrapped as "reading the bucket before the delete". The server's refusal of
a non-empty bucket is unverified; it maps to the same sentinel once the
live checks name its code. No force flag: emptying is S3 client work.

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
| Envelope `success: false` | `*APIError`, envelope code | That code, 1 or 4 |
| Empty 2xx body | `*APIError` `EmptyResponse` | 1 |

Key, attach, and principal errors are in
[key errors](storage-keys.md#errors). The CLI error codes list in
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
  and counts. Later releases add their resources the same way; the S4
  test is in [key testing](storage-keys.md#testing).

## Live checks before code

The test IAM user has vStorage access, and the test account has a project
in `HCM04`. Writes need the owner's approval.

Answered: `ListProjects` returns the project once `region` is sent, so an
empty list means none; the headers the server needs; the empty bucket list;
bucket shapes and dates; an unknown bucket; create, duplicate, and invalid
name; and empty and repeated delete; console S3 key create, list, list
fields, repeat and unknown delete, the 11th key, and data-plane use; the
accounts API create's 500; and the S4 probes. The results are in
[bucket writes](storage-api.md#bucket-writes) and
[S3 keys](storage-api.md#s3-keys).

1. Once S3 can put an object: `DeleteBucket` on a bucket holding one
   object, and the server's refusal code when the count reads 0 but
   versions remain. Also open: an unknown project's code, and whether
   bucket names are unique across accounts.
2. The S4 live test's open points and the S5 scope claim, before any
   policy code: see [key live checks](storage-keys.md#live-checks).
3. Policy, versioning, CORS, and public access bodies and errors.
4. The next month's bill shows nothing beyond the project package.

## Releases

Each release ships the SDK and CLI together, with its wiki pages.

| Release | Content |
|-|-|
| S1 | `storage` reads: `ListRegions`, `ListProjects`, `ListBuckets`, `GetBucket`; the `Storage` endpoint, region lookup, and envelope errors |
| S1.1 | Fix: every storage call except `ListRegions` sends `region` and `region_id`; a `ListProjects` fixture from the live shape; unit tests for both headers; a live `ListProjects` that finds the project |
| S2 | `CreateBucket` and `DeleteBucket` with `ErrBucketNotEmpty`, the delete wait and `NoWait`, and envelope code 112 as `ErrInvalidInput` |
| S3 | `storage` S3 keys on the console API: `ListS3Keys`, `CreateS3Key`, `DeleteS3Key`, `storage.ErrNoSecret`; `storage list-s3-keys`, `create-s3-key` with `--secret-file`, and `delete-s3-key` |
| S4 | Service account keys: `AttachS3Key`, `DetachS3Key`, `EnsureServiceAccountPrincipal`, and `S3Key.SubUserID` as the restriction state; `storage attach-s3-key`, `detach-s3-key`, `ensure-service-account-principal`, and `create-s3-key --service-account-id` |
| S5 | The live scope check first, then bucket policy get, put, and delete: the per-bucket key works end to end |
| S6 | Bucket versioning, CORS, and public access |

S1.1 ships before S2, since no storage read finds data without it. S2 is
not tagged yet, so the delete wait and code 112 ship in it. S2 and later
use the test project. None changes an existing method or command. S3 ships
before S4 because a project-wide key already unblocks aboutme. S4 ships
before S5 because the policy needs the principal and an attached key to
check. If the S5 scope check fails, S5 stops and the design changes before
any policy code. Service accounts themselves shipped in
[IAM writes](iam-writes.md) I2.

## Owner decisions

Decisions 1 to 28 are approved as recommended.

1. Approved: buckets use the undocumented console API with the IAM User
   token; the documented external API needs service-account login.
2. Approved: two packages, `storage` and `iam`, with service accounts in
   `iam`. Decision 16 moves S3 keys to `storage`.
3. Approved: projects stay a console step; a project is a paid checkout.
4. Approved: the test IAM user has vStorage access. The owner bought the
   test project in the console (Gold, 30 GB, pay monthly, 30,000 VND a
   month), so S2 and later verify live.
5. Approved: `Region` Input, `hcm-3` defaults to `HCM04`, `han-1` to `HAN02`.
6. Approved: the secret goes only to `--secret-file`, an AWS credentials
   file; no stdout option, which would reach transcripts and CI logs.
7. Approved: `vngcloud.Secret` redacts even in `json.Marshal`.
8. Replaced by [IAM writes](iam-writes.md) decision 2: the client secret
   goes only to `--secret-file`.
9. Approved: `DeleteBucket` refuses a bucket with objects; no force option.
10. Approved: `--yes` when making a bucket public.
11. Approved: rclone as the S3 client; no object commands in the CLI.
12. Approved: the release order above.
13. Approved: every storage call except `ListRegions` sends both
    `region` and `region_id` with the region UUID, and S1.1 ships that fix
    before S2.
14. Approved: envelope code 112 matches `ErrInvalidInput` on every
    storage call, since the server uses it for each input check it
    reports.
15. Approved: `DeleteBucket` waits up to 30 seconds, polling every
    second, until `GetBucket` reports `NotFound`, with `NoWait` to skip it,
    so a following `ListBuckets` is accurate and a repeat delete reports
    `NotFound`.
16. Approved: S3 ships keys on the vStorage console API, in `storage`,
    with `ProjectID` required and no `Name`. The accounts API create
    answers 500 and its console dialog cannot create one either; the
    console API key works on the data plane.
17. Approved: the accounts API key code is removed, not shipped
    unreleased. Its facts and the 500 stay in
    [accounts API](storage-api.md#accounts-api); decision 22 closes the
    choice.
18. Approved: S4 starts with the probes, and S4 and S5 wait on them.
    The per-bucket key stays the target. The probes are answered.
19. Approved: after an ambiguous key create, the error tells the caller
    to list and delete unknown keys; the SDK does not list before and after
    the create, since another client's key would look like the orphan.
20. Approved: `ListS3Keys` stays sensitive, since its shape can carry a
    secret. The cost is that a capture hook never sees the list.
21. Approved: a repeat key delete is code 114, not `NotFound`, so the
    CLI rule that `NotFound` counts as done does not apply to keys.
    `delete-s3-key` on a deleted key exits 1 with code 114, and the
    `--secret-file` cleanup takes success or code 114 as "key gone".

22. Approved: S4 attaches console keys with the console's
    `users/s3_keys/{k}/attach` and `detach`. The accounts API is dropped:
    its attach answers 404 for console keys and its create answers 500.
23. Approved: attach and detach failures stay `*APIError` with code
    114 and the server's message, documented per case, with no sentinels;
    the CLI prints the message. Sentinels matched by message text would
    break silently on a wording change.
24. Approved: `EnsureServiceAccountPrincipal` replaces the unshipped
    `GetServiceAccountPrincipal`. It is an explicit, idempotent write that
    sends `generated=true`, since a read must not create state, and it
    refuses a `subUserId` without `:sa-`.
25. Approved: attach and detach set `Once`. The cost is no automatic
    retry after a 429 or a failed dial; `Retryable` tells the caller.
    The alternative is a transport flag that marks a `PUT` as not
    idempotent, which touches every service's transport.
26. Approved: `create-s3-key --service-account-id` creates, attaches,
    and only then writes the secret file; a failed attach deletes the key.
    The alternative, separate create and attach commands only, leaves an
    unrestricted secret on disk between them.
27. Approved: `attach-s3-key` and `detach-s3-key` both need `--yes`:
    a detach widens a key to the whole project and an attach can cut off a
    running app, as for IAM policy attach.
28. Approved: the S4 live test reuses one service account name,
    `vngcloud-live-storage`, so each run leaves at most one undeletable
    sub-user and the run checks that a same-name recreate gets the same
    principal.

Open beyond the live checks: whether GreenNode will publish the console API
or accept IAM User tokens on the external API.
