# vStorage Design

Status: Accepted (2026-09-26).

This design adds vStorage object storage management to the SDK and CLI:
buckets, their settings, S3 keys, and the service accounts that scope a key
to one bucket. aboutme creates its buckets and per-bucket keys at setup
([First user](sdk-and-cli.md#first-user)).

It builds on [SDK and CLI](sdk-and-cli.md) and [CLI](cli.md). Writes follow
[ADR 0002](../adr/0002-write-api-conventions.md).

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
`ProductStorage`; storage paths are `internal/v1/...` under it. `iam` uses
the existing `Dashboard` endpoint with `accounts-api/v1/...`, not the old
documented `iamapis.vngcloud.vn` host.

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
| `GetBucketPolicy` | `GET .../buckets/{b}/policy` | `ProjectID` (r), `Bucket` (r) | `{Policy string}` |
| `PutBucketPolicy` | `PUT .../buckets/{b}/policy` | plus `Policy` (r) | `{}` |
| `DeleteBucketPolicy` | `DELETE .../buckets/{b}/policy` | `ProjectID` (r), `Bucket` (r) | `{}` |
| `GetServiceAccountPrincipal` | See [Principal](#principal) | `ProjectID` (r), `ServiceAccountID` (r) | `{SubUserID, PrincipalARN string}` |
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

### iam

Operation names are `iam.<Method>`. Paths are under `accounts-api/v1/`.
List Inputs carry `Page` and `Size`, sent as `pageNumber` and `pageSize`;
`pageNumber` starts at 0. Service account calls without S3 are in
[IAM writes](iam-writes.md).

| Operation | Method and path | Input | Output |
|-|-|-|-|
| `ListS3Keys` | `GET s3-keys` | `Search` | L[S3Key] |
| `CreateS3Key` | `POST s3-keys` | `Name` (r), `ProjectID` (r), `Region` | `{S3Key; SecretKey vngcloud.Secret}` |
| `DeleteS3Key` | `DELETE s3-keys/{id}` | `S3KeyID` (r) | `{}` |
| `ListServiceAccountS3Keys` | `GET service-accounts/{id}/s3-keys` | `ServiceAccountID` (r) | L[S3Key] |
| `AttachS3Key` | `POST service-accounts/{id}/s3-keys/{keyId}` | `ServiceAccountID` (r), `S3KeyID` (r) | `{}` |
| `DetachS3Key` | `DELETE service-accounts/{id}/s3-keys/{keyId}` | `ServiceAccountID` (r), `S3KeyID` (r) | `{}` |

- `CreateS3Key` resolves `Region` through the `storage` region lookup, which
  `iam` imports, and sends `name`, `regionId`, and `projectId`. `Name` is
  required, unlike in the API, so a key whose response was lost can be found.
- `ClientSecret` is set only when the create response holds one.
- If the live checks show an attached key also needs `PATCH s3-keys/{id}`
  with `restricted: true`, `AttachS3Key` sends it.

### Identifiers

Every path ID is checked before any request, reads included:

- `ProjectID`, `S3KeyID`, and `ServiceAccountID`: `core.CheckPathID`
  (`^[A-Za-z0-9-]+$`). The live checks confirm their shape.
- `Bucket`: `^[A-Za-z0-9][A-Za-z0-9._-]{0,254}$`, a path-safety check only.
  It rejects `/`, `?`, `%`, `.`, and `..`. The S3 naming rules stay on the
  server (ADR 0002 rule 5): upper case and `_` pass this check, and the
  server refuses them with code 112 (see [Envelope errors](#envelope-errors)).
  The SDK escapes the name with `url.PathEscape`, as the console escapes it.

### Principal

A bucket policy names a service account as
`arn:aws:iam:::user/<subUserId>`. The console reads the sub-user from
`GET internal/v1/users/details` with `generated=true`, `project_id`, and
`iam_account_id=sa-<id>`, and creates one with
`POST internal/v1/users/ceph_sub_users`, which returns the same record when
repeated ([storage users](storage-api.md#storage-users)). With a project,
`generated=true` and `generated=false` answered alike for the IAM user, so
`GetServiceAccountPrincipal` sends `generated=false`. The SDK does not call
`ceph_sub_users`: S2 does not need it. If S4 or S5 finds a service account
has no sub-user until that POST, the design adds an explicit, idempotent
write rather than let a `GET` create state.

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
Output leaks nothing. `transport.Request` gains `Sensitive bool`, set by
both creates: the `WithResponseCapture` hook never sees such a response, and
a decode error never quotes its body. `--debug` already logs no body.

### Retries

- Creates and `AttachS3Key` are `POST`: retried only after a 429 or a
  failed dial (ADR 0002 rule 2). After a 5xx or a network error the
  resource may exist; the error names the check (`get-bucket`, or a list by
  name). A bucket create can then be rerun. A key found that way has lost
  its secret: delete it.
- A repeated attach returns 409 `Conflict`, which the SDK does not hide.
- Deletes and puts keep the transport's retries; a retried delete that
  finds nothing returns `NotFound`. A create response without an ID or
  name is an error.

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

## Per-bucket key

aboutme's setup, per bucket. `policy.json` allows the principal `s3:*` on
`arn:aws:s3:::<b>` and `arn:aws:s3:::<b>/*`; the wiki gives the template.

```sh
vngcloud storage create-bucket --project-id <p> --bucket <b>
vngcloud iam create-service-account --name <b>-app --secret-file <path>
vngcloud storage get-service-account-principal --project-id <p> \
  --service-account-id <sa>
vngcloud storage put-bucket-policy --project-id <p> --bucket <b> \
  --policy file://policy.json
vngcloud iam create-s3-key --name <b>-app --project-id <p> \
  --secret-file <path>
vngcloud iam attach-s3-key --service-account-id <sa> --s3-key-id <k>
```

Until the attach, the key has the creating user's rights, so the app starts
only after it.

## CLI

`svc_storage.go` and `svc_iam.go` register the tables. Flags follow the
Input fields; `Rules` comes through `--cli-input-json`, and `Policy` accepts
`file://` like `--cli-input-json`.

| Command | Kind | Needs `--yes` |
|-|-|-|
| `storage list-regions`, `list-projects`, `list-buckets`, `get-bucket` | Read | No |
| `storage create-bucket` | Write | No |
| `storage delete-bucket` | Write, destructive | Yes |
| `storage get-bucket-policy`, `get-service-account-principal` | Read | No |
| `storage put-bucket-policy`, `delete-bucket-policy` | Write | No |
| `storage get-bucket-versioning`, `get-bucket-cors`, `get-bucket-public-access` | Read | No |
| `storage put-bucket-versioning`, `put-bucket-cors`, `delete-bucket-cors` | Write | No |
| `storage put-bucket-public-access` | Write | Yes when `--public` |
| `iam list-s3-keys`, `list-service-account-s3-keys` | Read | No |
| `iam create-s3-key`, `attach-s3-key`, `detach-s3-key` | Write | No |
| `iam delete-s3-key` | Write, destructive | Yes |

- A [read-only](cli.md#read-only) profile refuses every write with exit 2
  before any request.
- A deleted bucket, key, or service account cannot be restored by one more
  command (ADR 0002 rule 6); a deleted policy or CORS set can, by a put.
- Making a bucket public needs `--yes`: exposure cannot be undone, because
  anyone may copy the objects while it lasts.

The global `--project-id` flag supplies a vStorage `ProjectID`. Only the flag
counts: the environment and profile project is the vServer project and is
never used here. The flag overrides a `ProjectID` in `--cli-input-json`, and a
missing one exits 2 before any request.

### create-s3-key

`iam create-s3-key` needs `--secret-file <path>` and cannot print the secret:

1. Before any request, the parent directory must exist and nothing may exist
   at the path, symlinks included; otherwise it exits 2.
2. After the create, it opens the path with `O_CREATE|O_EXCL|O_NOFOLLOW`
   and mode 0600, writes, syncs, and closes. The file is an AWS shared
   credentials file, which rclone and the AWS CLI read through
   `AWS_SHARED_CREDENTIALS_FILE`:

   ```ini
   [default]
   aws_access_key_id = <access key>
   aws_secret_access_key = <secret key>
   ```

3. If the write fails, the CLI removes any partial file, deletes the new key
   (the secret is lost anyway), and exits 1. If that delete fails, the
   error names the key ID so a person can delete it.
4. Stdout gets the key without the secret: `SecretKey` prints as
   `[redacted]`, and a `SecretFile` field names the path.

## Errors

| Case | Result | CLI code and exit |
|-|-|-|
| Missing field, bad ID or bucket shape, unmapped region | `ErrInvalidInput`, no request | `InvalidUsage`, 2 |
| Server refuses an input, envelope code 112 | `ErrInvalidInput` | `112`, 2 |
| Bucket delete not settled in 30 s | `storage.ErrNotSettled` | `NotSettled`, 1 |
| `--secret-file` exists or its directory is missing | No request | `InvalidUsage`, 2 |
| Bucket holds objects or data, or its count is not reported | `ErrBucketNotEmpty`, no delete sent | `BucketNotEmpty`, 1 |
| IAM policy denies the action | `ErrPermission` | `IAM_PERMISSION_DENIED`, 1 |
| Envelope `success: false` | `*APIError`, envelope code | That code, 1 or 4 |
| Empty 2xx body | `*APIError` `EmptyResponse` | 1 |
| Key attached twice | `Conflict` | 1 |
| Secret file write failed after create | Key deleted | `SecretFileFailed`, 1 |

The CLI error codes list in [CLI](cli.md#errors-and-exit-codes) gains
`BucketNotEmpty` and `SecretFileFailed`.

## Security

- Every write gets an adversarial review before its release. It checks: the
  secret never reaches stdout, stderr, `--debug`, an error, a response capture,
  or a fixture; `--secret-file` refuses existing paths and symlinks and creates
  mode 0600; the orphan key is deleted after a failed write; no create retry
  after a 5xx; path checks on every call; the `region` and `region_id` headers
  on every storage call; no `DELETE` for a non-empty bucket; `--yes` where the
  table says; read-only refusal; and no state created by a read.
- A key has its creator's rights. The wiki says to scope app keys through a
  service account and a bucket policy, never a broad user or the root.
- Bucket names, project IDs, access keys, service accounts, and policies are
  account data, and so is the `GetBucket` `owner` object (the account email
  in base64 and the storage user ID). Fixtures use `<id>`, `<account>`,
  `<access-key>`, and `<secret>`, and sanitize `owner`.
- The console API is undocumented and may change; fixed models make a changed
  field fail a fixture test.

## Testing

Unit tests use `httptest`:

- Sanitized fixtures and decode tests in `testdata/storage/` and
  `testdata/iam/` for every read and create, plus a `success: false`
  envelope, the 403 array, and an empty 200.
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

- `make live` adds `ListRegions`, `ListProjects` in both regions,
  `ListS3Keys`, and `ListServiceAccounts`, logging counts only.
- The live write test skips unless `VNGCLOUD_LIVE_STORAGE_PROJECT_ID` is
  set, and never logs it. It deletes leftover `vngcloud-live-` buckets,
  service accounts, and keys, never a project, then creates
  `vngcloud-live-<8 hex>` as a bucket, a service account, a policy, and a
  key written to a temp `--secret-file`, and attaches the key.
  `t.Cleanup`, registered as each ID is known, detaches and deletes the
  key, deletes the policy, the bucket, and the service account, and asserts
  none remain. It logs only statuses and counts.

## Live checks before code

The test IAM user has vStorage access, and the test account has a project
in `HCM04`. Writes need the owner's approval.

Answered: `ListProjects` returns the project once `region` is sent, so an
empty list means none; the headers the server needs; the empty bucket list;
bucket shapes and dates; an unknown bucket; create, duplicate, and invalid
name; and empty and repeated delete. The results are in
[bucket writes](storage-api.md#bucket-writes).

1. Pending until S3, which can put an object: `DeleteBucket` on a bucket
   holding one object, and the server's refusal code when the count reads
   0 but versions remain. Also open: an unknown project's code, and whether
   bucket names are unique across accounts.
2. `CreateS3Key`: 201 body, the `projectId` and `regionId` it wants, the
   11th-key error, delete and repeat, and `rclone lsd` with the key.
3. Attach a key to a service account, repeat the attach, and detach.
4. Principal: `users/details` with `generated=false` for a service account
   before and after attach, and whether its `ceph_sub_users` must run
   first. For the IAM user, the shapes are known and the POST is
   idempotent.
5. Scope, the core claim: with a policy for the principal on bucket A, the
   attached key reads and writes A and is denied on bucket B; whether
   `restricted: true` is needed.
6. Policy, versioning, CORS, and public access bodies and errors.
7. The next month's bill shows nothing beyond the project package.

## Releases

Each release ships the SDK and CLI together, with its wiki pages.

| Release | Content |
|-|-|
| S1 | `storage` reads: `ListRegions`, `ListProjects`, `ListBuckets`, `GetBucket`; the `Storage` endpoint, region lookup, and envelope errors |
| S1.1 | Fix: every storage call except `ListRegions` sends `region` and `region_id`; a `ListProjects` fixture from the live shape; unit tests for both headers; a live `ListProjects` that finds the project |
| S2 | `CreateBucket` and `DeleteBucket` with `ErrBucketNotEmpty`, the delete wait and `NoWait`, and envelope code 112 as `ErrInvalidInput` |
| S3 | `iam` S3 keys: `ListS3Keys`, `CreateS3Key`, `DeleteS3Key`; `vngcloud.Secret`, `transport.Request.Sensitive`, and `--secret-file` |
| S4 | `iam` S3 keys on service accounts: `ListServiceAccountS3Keys`, `AttachS3Key`, `DetachS3Key`; needs IAM writes I2 |
| S5 | Bucket policy get, put, and delete, and `GetServiceAccountPrincipal`: the per-bucket key works end to end |
| S6 | Bucket versioning, CORS, and public access |

S1.1 ships before S2, since no storage read finds data without it. S2 is
not tagged yet, so the delete wait and code 112 ship in it. S2 and later
use the test project. None changes an existing method or command. Keys ship
before key attach because a project-wide key already unblocks aboutme; service
accounts themselves ship in [IAM writes](iam-writes.md) I2.

## Owner decisions

Decisions 1 to 15 are approved as recommended.

1. Approved: buckets use the undocumented console API with the IAM User
   token; the documented external API needs service-account login.
2. Approved: two packages, `storage` and `iam`: keys and service accounts
   live in the IAM API, as in AWS.
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

Open beyond the live checks: whether GreenNode will publish the console API
or accept IAM User tokens on the external API.
