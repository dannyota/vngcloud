# vStorage Design

Status: Accepted (2026-09-26).

This design adds vStorage object storage management to the SDK and CLI:
buckets, their settings, S3 keys, and the service accounts that scope a key
to one bucket. aboutme creates its buckets and per-bucket keys at setup
([First user](sdk-and-cli.md#first-user)).

It builds on [SDK and CLI](sdk-and-cli.md) and [CLI](cli.md). Writes follow
[ADR 0002](../adr/0002-write-api-conventions.md).

## Source

Public docs (`docs.greennode.ai/vstorage`), the OpenAPI specs on
`docs.api.greennode.ai` (`vstorage-hcm04-api`, `accounts-api`), the public
vStorage console bundle and its `config.prod.json`, and live reads on the
test account on 2026-09-26. VNG Cloud's Go SDK and Terraform provider have
no vStorage code.

### Model

- A vStorage project is a paid storage package in one vStorage region,
  `HCM04` or `HAN02`, each with a UUID region ID and an S3 host such as
  `https://hcm04.vstorage.vngcloud.vn`. The Swift farm `HCM03` is out of
  scope.
- An S3 key is an access key and secret for one project, with the rights of
  the user who made it. The secret is shown once.
- An IAM service account starts with no rights. A key attached to it acts
  as the service account, whose bucket rights come from a bucket policy
  that names its storage principal, `arn:aws:iam:::user/<subUserId>`. This
  is the per-bucket key: one service account, one bucket policy, one key.

### Management APIs

| API | Base | Auth | Result |
|-|-|-|-|
| vStorage console API | `https://vstorage.console.greennode.ai/internal/v1/` | IAM User token | Works |
| vStorage external API | `https://hcm04-api.vstorage.vngcloud.vn/api/v1/` | Service account token | IAM User token gets an empty 200 |
| IAM accounts API | `https://dashboard.console.greennode.ai/accounts-api/v1/` | IAM User token | Works |

The console API has the external API's paths and bodies
(`ceph/projects/{projectId}/buckets/{bucket}`) under another prefix, and
takes the vStorage region in a `region_id` header; adding a `region` header
got an empty 200. The documented IAM accounts API covers service accounts,
S3 keys, and attaching a key to a service account.

### Live reads

| Call | Result |
|-|-|
| `GET internal/v1/regions` | 200, `HCM04` and `HAN02` |
| `GET internal/v1/projects` with `region_id` | 200, `datas: null`: no project in either region |
| `GET internal/v1/users/s3_keys` | 403 `[{"code":"IAM_PERMISSION_DENIED","message":"IAM denied action"}]` |
| `GET internal/v1/ceph/projects/external` | 200 with `success: false`, `code: 114`, `errorMsg` |
| `GET internal/v1/billing/project_types` | 200, the price table |
| `GET accounts-api/v1/s3-keys`, `service-accounts` | 200, empty pages |

Console API responses use one envelope: `success`, `code`, `errorMsg`,
`action`, and `data` (one item) or `datas` (a list). Errors arrive as HTTP
200 with `success: false`. Field names and types:

| Model | Fields |
|-|-|
| Region | `regionId`, `regionName`, `regionDisplayingName`, `backendType`, `s3Host`, `vosApiHost`, `accountUrl`, `authHost`, `status` (number) |
| Project (spec) | `projectId`, `projectName`, `regionId`, `regionName`, `status`, `totalQuota`, `startTime`, `endTime`, `period` |
| Bucket (spec) | `name`, `count`, `size`, `isPublic`, `isVersioned`, `createdDate`, `lastModified`, `type`, `versionLocation` |
| S3 key (spec) | `id`, `name`, `accessKey`, `projectId`, `regionId`, `createdAt`; create adds `secretKey` |
| Service account page | `data`, `pageNumber`, `pageSize`, `totalItems`, `totalPages` |

### Data plane

The data plane is S3-compatible (Signature V4, path-style, regions `HCM04`
and `HAN02`, HTTPS only). This SDK covers the management plane; objects go
through any S3 client. The wiki recommends **rclone**: GreenNode documents
its setup, and it ships as one static binary with a plain S3 mode. Recent
AWS CLI v2 releases send checksum headers by default that S3-compatible
servers often refuse; whether vStorage does is a live check.

### Cost

- A project is a paid package: Gold 1,000 VND per GB per month from 30 GB,
  or pay as you go at 1,400 VND per GB per month from 1 GB.
- Buckets, S3 keys, and service accounts cost nothing to create. Requests
  are free; download traffic is free up to ten times the stored size.
- Limits: 10 S3 keys per root account, 1000 buckets per project.
- The test account has no project and no credit, so no bucket write can run
  on it yet; see [Owner decisions](#owner-decisions).

## Non-goals

- Creating, resizing, or deleting a project: a paid checkout, so a console
  step. The SDK would need a quote (ADR 0002 rule 8) and its own design.
- Objects, directories, presigned URLs, and uploads. Use an S3 client.
- Lifecycle, encryption, object lock, notifications, ACLs, IP range ACLs,
  usage alerts, reports, the Swift farm (`HCM03`), and Swift users.
- Service-account login, the external vStorage API, and a service account's
  client secret, which the CLI never shows or saves.

## Endpoints

`endpoints.Set` and `Overrides` gain `Storage`, default
`https://vstorage.console.greennode.ai/`, and `internal/routes` gains
`ProductStorage`; storage paths are `internal/v1/...` under it. `iam` uses
the existing `Dashboard` endpoint with `accounts-api/v1/...`, not the old
documented `iamapis.vngcloud.vn` host.

Every storage request sends `region_id: <region UUID>` and never a `region`
header. The client resolves a region name to its UUID with `ListRegions`
once per `storage.Client` and caches the result. An empty `Region` maps
`hcm-3` to `HCM04` and `han-1` to `HAN02`; any other config region with an
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
| `DeleteBucket` | `DELETE ceph/projects/{p}/buckets/{b}` | `ProjectID` (r), `Bucket` (r) | `{}` |
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
  bucket without object lock. The Output comes from a read after the create;
  if that read fails, the error says the bucket was created.
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
  `ObjectCount` and `SizeBytes`. Dates stay strings until a fixture shows
  their format, and numeric `status` values reach the caller unchanged.

### iam

Operation names are `iam.<Method>`. Paths are under `accounts-api/v1/`.
List Inputs carry `Page` and `Size`, sent as `pageNumber` and `pageSize`.

| Operation | Method and path | Input | Output |
|-|-|-|-|
| `ListS3Keys` | `GET s3-keys` | `Search` | L[S3Key] |
| `CreateS3Key` | `POST s3-keys` | `Name` (r), `ProjectID` (r), `Region` | `{S3Key; SecretKey vngcloud.Secret}` |
| `DeleteS3Key` | `DELETE s3-keys/{id}` | `S3KeyID` (r) | `{}` |
| `ListServiceAccounts` | `GET service-accounts` | `Name` | L[ServiceAccount] |
| `GetServiceAccount` | `GET service-accounts/{id}` | `ServiceAccountID` (r) | `{ServiceAccount}` |
| `CreateServiceAccount` | `POST service-accounts` | `Name` (r), `Description` | `{ServiceAccount; ClientSecret vngcloud.Secret}` |
| `DeleteServiceAccount` | `DELETE service-accounts/{id}` | `ServiceAccountID` (r) | `{}` |
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
  server (ADR 0002 rule 5). The SDK escapes the name with
  `url.PathEscape`, as the console escapes it.

### Principal

A bucket policy names a service account as
`arn:aws:iam:::user/<subUserId>`. The console reads the sub-user from
`GET internal/v1/users/details` with `generated=true`, `project_id`, and
`iam_account_id=sa-<id>`, and creates one with
`POST internal/v1/users/ceph_sub_users`. What `generated=true` does is
unknown, so `GetServiceAccountPrincipal` sends `generated=false`. If the
sub-user exists only after a write, the design adds an explicit write
rather than let a `GET` create state.

### Envelope errors

- A 2xx envelope with `success: false` is an `*APIError` with the HTTP
  status, `Code` set to the envelope `code` as text, and `Message` set to
  `errorMsg` cut to 256 bytes. An envelope `code` from 400 to 599 also
  matches that status's sentinel, so `NotFound` exits 4.
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
  name). A key found that way has lost its secret: delete it.
- A repeated attach returns 409 `Conflict`, which the SDK does not hide.
- Deletes and puts keep the transport's retries; a retried delete that
  finds nothing returns `NotFound`. A create response without an ID or
  name is an error.

### Delete bucket

`DeleteBucket` reads the bucket first and returns `ErrBucketNotEmpty`,
sending nothing, when `ObjectCount` is above 0. The server's own refusal
maps to the same sentinel once the live checks name its code; it is the
final guard, since `count` may omit old versions. There is no force flag:
emptying a bucket is object work for an S3 client.

## Per-bucket key

aboutme's setup, per bucket. `policy.json` allows the principal `s3:*` on
`arn:aws:s3:::<b>` and `arn:aws:s3:::<b>/*`; the wiki gives the template.

```sh
vngcloud storage create-bucket --project-id <p> --bucket <b>
vngcloud iam create-service-account --name <b>-app
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
| `iam list-s3-keys`, `list-service-accounts`, `get-service-account`, `list-service-account-s3-keys` | Read | No |
| `iam create-s3-key`, `create-service-account`, `attach-s3-key`, `detach-s3-key` | Write | No |
| `iam delete-s3-key`, `delete-service-account` | Write, destructive | Yes |

- A [read-only](cli.md#read-only) profile refuses every write with exit 2
  before any request.
- A deleted bucket, key, or service account cannot be restored by one more
  command (ADR 0002 rule 6); a deleted policy or CORS set can, by a put.
- Making a bucket public needs `--yes`: exposure cannot be undone, because
  anyone may copy the objects while it lasts.

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

`iam create-service-account` prints the service account with
`ClientSecret` redacted and writes no file. The per-bucket key does not use
the client secret; a later design can add `reset-secret` with a file.

## Errors

| Case | Result | CLI code and exit |
|-|-|-|
| Missing field, bad ID or bucket shape, unmapped region | `ErrInvalidInput`, no request | `InvalidUsage`, 2 |
| `--secret-file` exists or its directory is missing | No request | `InvalidUsage`, 2 |
| Bucket holds objects | `ErrBucketNotEmpty`, no delete sent | `BucketNotEmpty`, 1 |
| IAM policy denies the action | `ErrPermission` | `IAM_PERMISSION_DENIED`, 1 |
| Envelope `success: false` | `*APIError`, envelope code | That code, 1 or 4 |
| Empty 2xx body | `*APIError` `EmptyResponse` | 1 |
| Key attached twice | `Conflict` | 1 |
| Secret file write failed after create | Key deleted | `SecretFileFailed`, 1 |

The CLI error codes list in [CLI](cli.md#errors-and-exit-codes) gains
`BucketNotEmpty` and `SecretFileFailed`.

## Security

- Every write gets an adversarial review before its release. It checks: the
  secret never reaches stdout, stderr, `--debug`, an error, a response
  capture, or a fixture; `--secret-file` refuses existing paths and
  symlinks and creates mode 0600; the orphan key is deleted after a failed
  write; no create retry after a 5xx; path checks on every call; no
  `region` header; no `DELETE` for a non-empty bucket; `--yes` where the
  table says; read-only refusal; and no state created by a read.
- A key has its creator's rights. The wiki says to scope app keys through a
  service account and a bucket policy, never a broad user or the root.
- Bucket names, project IDs, access keys, service accounts, and policies are
  account data. Fixtures use `<id>`, `<account>`, `<access-key>`, and
  `<secret>`.
- The console API is undocumented and may change; fixed models make a
  changed field fail a fixture test.

## Testing

Unit tests use `httptest`:

- Sanitized fixtures and decode tests in `testdata/storage/` and
  `testdata/iam/` for every read and create, plus a `success: false`
  envelope, the 403 array, and an empty 200.
- Request bodies for every write; `region_id` sent and `region` never; the
  region mapping and its unmapped case; `limit=1000` and `isNext`.
- `DeleteBucket` sends no `DELETE` when the count is above 0.
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
  set, and never logs it. It deletes leftover `vngcloud-live-` resources,
  then creates `vngcloud-live-<8 hex>` as a bucket, a service account, a
  policy, and a key written to a temp `--secret-file`, and attaches the key.
  `t.Cleanup`, registered as each ID is known, detaches and deletes the
  key, deletes the policy, the bucket, and the service account, and asserts
  none remain. It logs only statuses and counts.

## Live checks before code

Reads need a `vstorage` IAM policy on the test IAM user; the 403 above shows
it lacks one. Writes need a project and the owner's approval.

1. With the policy, `ListProjects` on an account with a project, so
   `datas: null` means none rather than denied.
2. `ListBuckets` and `GetBucket` shapes, dates, and paging; whether writes
   need the console's `user_id` or `portal-user-id` header.
3. Envelope codes for an unknown project and bucket.
4. `CreateBucket`: response, sync or async, duplicate and invalid names,
   and whether names are unique across accounts.
5. `DeleteBucket`: empty, holding one object, and repeated.
6. `CreateS3Key`: 201 body, the `projectId` and `regionId` it wants, the
   11th-key error, delete and repeat, and `rclone lsd` with the key.
7. `CreateServiceAccount`: 201 body and any client secret; delete; attach,
   repeat attach, and detach.
8. Principal: `users/details` with `generated=false` before and after
   attach, and whether `ceph_sub_users` must run first.
9. Scope, the core claim: with a policy for the principal on bucket A, the
   attached key reads and writes A and is denied on bucket B; whether
   `restricted: true` is needed.
10. Policy, versioning, CORS, and public access bodies and errors.
11. The next day's bill shows nothing beyond the project package.

## Releases

Each release ships the SDK and CLI together, with its wiki pages.

| Release | Content |
|-|-|
| S1 | `storage` reads: `ListRegions`, `ListProjects`, `ListBuckets`, `GetBucket`; the `Storage` endpoint, region lookup, and envelope errors |
| S2 | `CreateBucket` and `DeleteBucket` with `ErrBucketNotEmpty` |
| S3 | `iam` S3 keys: `ListS3Keys`, `CreateS3Key`, `DeleteS3Key`; `vngcloud.Secret`, `transport.Request.Sensitive`, and `--secret-file` |
| S4 | `iam` service accounts: list, get, create, delete, attach and detach a key, and list its keys |
| S5 | Bucket policy get, put, and delete, and `GetServiceAccountPrincipal`: the per-bucket key works end to end |
| S6 | Bucket versioning, CORS, and public access |

S1 needs only the policy grant; S2 and later need a project. None changes an
existing method or command. Keys ship before service accounts because a
project-wide key already unblocks aboutme, and secret handling is the
riskiest part.

## Owner decisions

All 12 are approved as recommended.

1. Approved: buckets use the undocumented console API with the IAM User
   token; the documented external API needs service-account login.
2. Approved: two packages, `storage` and `iam`: keys and service accounts
   live in the IAM API, as in AWS.
3. Approved: projects stay a console step; a project is a paid checkout.
4. Approved: the test IAM user has vStorage access. Live writes wait for
   credit to buy the smallest pay-as-you-go project, about 1,400 VND per
   month. Until then only S1 verifies live.
5. Approved: `Region` Input, `hcm-3` defaults to `HCM04`, `han-1` to `HAN02`.
6. Approved: the secret goes only to `--secret-file`, an AWS credentials
   file; no stdout option, which would reach transcripts and CI logs.
7. Approved: `vngcloud.Secret` redacts even in `json.Marshal`.
8. Approved: the CLI never shows or saves a service account's client secret.
9. Approved: `DeleteBucket` refuses a bucket with objects; no force option.
10. Approved: `--yes` when making a bucket public.
11. Approved: rclone as the S3 client; no object commands in the CLI.
12. Approved: the release order above.

Open beyond the live checks: whether GreenNode will publish the console API
or accept IAM User tokens on the external API.
