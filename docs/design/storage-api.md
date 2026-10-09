# vStorage: API

The vStorage calls, shapes, and costs behind [vStorage](storage.md). The SDK
surface built on them is in that design.

## Sources

Public docs (`docs.greennode.ai/vstorage`), the OpenAPI specs on
`docs.api.greennode.ai` (`vstorage-hcm04-api`, `accounts-api`), the public
vStorage console bundle and its `config.prod.json`, live reads on the test
account on 2026-09-26, the console's own calls with the test IAM user
on 2026-10-09, after the owner bought a project, and live bucket and S3 key
writes on the test project the same day. VNG Cloud's Go SDK and
Terraform provider have no vStorage code.

## Model

- A vStorage project is a paid storage package in one vStorage region,
  `HCM04` or `HAN02`, each with a UUID region ID and an S3 host such as
  `https://hcm04.vstorage.vngcloud.vn`. The Swift farm `HCM03` is out of
  scope.
- An S3 key is an access key and secret for one project, with the rights of
  the user who made it. The secret is shown once. The console API and the
  IAM accounts API keep separate key stores; see [S3 keys](#s3-keys).
- An IAM service account starts with no rights. A key attached to it acts
  as the service account, whose bucket rights come from a bucket policy
  that names its storage principal, `arn:aws:iam:::user/<subUserId>`. This
  is the per-bucket key: one service account, one bucket policy, one key.

## Management APIs

| API | Base | Auth | Result |
|-|-|-|-|
| vStorage console API | `https://vstorage.console.greennode.ai/internal/v1/` | IAM User token | Works |
| vStorage external API | `https://hcm04-api.vstorage.vngcloud.vn/api/v1/` | Service account token | IAM User token gets an empty 200 |
| IAM accounts API | `https://dashboard.console.greennode.ai/accounts-api/v1/` | IAM User token | Reads work; S3 key create answers 500 |

The console API has the external API's paths and bodies
(`ceph/projects/{projectId}/buckets/{bucket}`) under another prefix. The
documented IAM accounts API covers service accounts, S3 keys, and attaching
a key to a service account. The console API also has S3 key calls.

## Region headers

The console sends `region` and `region_id`, both the region UUID, plus
`user_id` and `portal-user-id` (the numeric account ID). The server scopes
results by `region`:

| Call, with a project in `HCM04` | `region_id` only | `region` only |
|-|-|-|
| `GET projects` | 200 `{"code":200,"success":true}`, no `datas` | 200, the project |
| `GET ceph/projects/{p}?limit=1000` | 200, `success: false`, `code: 114`, `Could not retrieve buckets. No project authentication found.` | 200, the buckets |

`user_id` and `portal-user-id` change nothing. A missing `region` therefore
reads as "no projects", not as an error. `GET regions` needs neither header.

## Live reads

| Call | Result |
|-|-|
| `GET regions` | 200, `HCM04` and `HAN02` |
| `GET projects` with `region` | 200, that region's projects only |
| `GET ceph/projects/{p}?limit=1000` with `region`, empty project | 200, `datas: []` |
| `GET users/s3_keys?projectId={p}` with `region` and `region_id` | 200, the project's keys |
| The same without the region headers | 403 `[{"code":"IAM_PERMISSION_DENIED","message":"IAM denied action"}]` |
| `GET ceph/projects/external` | 200 with `success: false`, `code: 114`, `errorMsg` |
| `GET billing/project_types` | 200, the price table |
| `GET accounts-api/v1/s3-keys`, `service-accounts` | 200, empty pages |

Paths without a host are under `internal/v1/`. The console adds
`project_id`, `region_id`, and an empty `roleName` query to the bucket list;
the list works without them.

## Shapes

Console API responses use one envelope: `success`, `code`, `errorMsg`,
`action`, and `data` (one item) or `datas` (a list). Errors arrive as HTTP
200 with `success: false`. Field names and types:

| Model | Fields |
|-|-|
| Region | `regionId`, `regionName`, `regionDisplayingName`, `backendType`, `s3Host`, `vosApiHost`, `accountUrl`, `authHost`, `status` (number) |
| Project | See [Project](#project) |
| Bucket (spec) | `name`, `count`, `size`, `isPublic`, `isVersioned`, `createdDate`, `lastModified`, `type`, `versionLocation` |
| S3 key, console list | `regionId`, `projectId`, `userId`, `subUserId`, `userKeyId`, `accessKey`, `secretKey` (null), `createdDate` (`dd/mm/yyyy hh:mm`), `status` (number, 1) |
| S3 key, console create | `data.userKeyId`, `data.accessKey`, `data.secretKey`; the other list fields null |
| S3 key, accounts API spec | `id`, `name`, `accessKey`, `projectId`, `regionId`, `createdAt` (RFC 3339 text); create adds `secretKey` |
| Service account page | `data`, `pageNumber`, `pageSize`, `totalItems`, `totalPages` |

Bucket fields as served:

- `ListBuckets` gives `createdDate` as `dd/mm/yyyy hh:mm`, with no time
  zone. `GetBucket` gives `createdDate` null.
- `GetBucket` adds an `owner` object holding the account email in base64
  and the storage user ID. Both are account data; fixtures replace them.

### Project

A live project, with account data replaced:

| Field | Value |
|-|-|
| `projectId`, `userId` | 32 hex characters |
| `projectName`, `description` | Text; `description` is null |
| `status`, `projectType`, `purchaseTypeId`, `paymentMethod` | Numbers: 1, 1, 4, 1 |
| `projectTypeName`, `purchaseTypeName` | `"Gold"`, `"Pay monthly"` |
| `startTime`, `endTime` | `2026-10-09T14:23:05.933+00:00` |
| `totalQuota`, `usage` | GB as numbers: `30.0`, `0.0` |
| `period`, `backendQuota`, `traffic`, `accountUrl` | null |
| `userName` | Base64 of the root account email |
| `userIdMapping` | The numeric account ID |
| `roleId`, `roleName` | A UUID, `"Admin"` |
| `regionId`, `regionName` | The region UUID and name |
| `ownerId`, `ownerName`, `disabledTime`, `disabledBy`, `deletedTime` | Present |
| `lifecycleRules`, `containers` | null |
| `actions` | Array of `{id, userIdMapping, regionId, projectId, projectName, action, status, stackTrace, description, createdTime, updatedTime}`; `action` is `"Create"` |
| `resourceTags` | `[{key, value}]`: `vng.region` is `hcm04`, `vng.createdBy` is `IAMUser:<IAM user ID>` |
| `enableAutoRenew`, `autoRenewPeriod` | `false`, `0` |

More fields follow that were not recorded. `userName`, `userIdMapping`,
`ownerName`, and `resourceTags` are account data; fixtures replace them.

### Storage users

Before the first bucket list of a project, the console sends
`POST users/ceph_sub_users` with `region` and this body:

```json
{"iamAccountId":"<IAM user ID>","iamUserType":"iam-user","projectId":"<project id>"}
```

It answers the project's storage user in `data`: `userId`
(`<root local part>-<account ID>-<first 8 of project id>`), `subUserId`
(`<userId>:iam-<IAM user name>`), `userIdMapping`, `userName`, `regionId`,
`regionName`, `storageEndPointUrl` (`https://hcm04.vstorage.vngcloud.vn`),
`s3StorageURl`, `authEndPointUrl` (`https://hcm04-vstorage.vngcloud.vn`),
`publicAuthEndPointUrl`, and `status` 1. A repeated POST returns the same
record. Whether a bucket list fails before this POST is unknown: the user
already existed when the headers were tested.

`GET users/details` with `generated=true` answered code 114 `Error occurred
in adding storage user <root email>` while the account had no project.
With a project, both `generated=true` and `generated=false` answer the
account-level user `<root local part>-<account ID>`, with `subUserId`
`<that>:iam-<IAM user name>` and the same endpoint fields.

## Bucket writes

Live on the test project, with `region` sent. Every answer was HTTP 200;
201, 400, 403, 404, and 409 did not occur.

| Call | Result |
|-|-|
| `POST ceph/projects/{p}/buckets/{b}`, new name | `data` with `name`, `count: 0`, and null elsewhere |
| The same `POST` again, a name the account owns | The same 200 and body: idempotent |
| `POST` with an upper-case name | `success: false`, `code: 112`, `Invalid input error (Bucket name must be all lowercase letters, numbers or hyphens)` |
| `GET ceph/projects/{p}/{b}/details` right after the create | The full bucket |
| `DELETE ceph/projects/{p}/buckets/{b}`, empty bucket | `{"code":200,"success":true}` |
| `GET .../details` or `DELETE` of a missing bucket | `success: false`, `code: 404` |

The delete is asynchronous. For up to about a second after its 200,
`GetBucket` and `ListBuckets` still show the bucket, and a `GetBucket` can
answer `code: -1` with `errorMsg` `Unknown error`, or an empty body. Then
the bucket is gone and reads answer code 404.

Not yet checked: a delete of a bucket holding objects, which needs an S3
key to put one; an unknown project; and whether names are unique across
accounts.

## S3 keys

### Console API

Live on the test project, with the IAM user token and both region headers.
Paths are under `internal/v1/`.

| Call | Result |
|-|-|
| `POST users/s3_keys`, body `{"projectId":"<p>"}` | 200, `data` with `userKeyId`, `accessKey`, and `secretKey`; no name |
| `GET users/s3_keys?projectId=<p>` | 200, the keys, the new one included |
| `DELETE users/s3_keys/{userKeyId}`, body `{"projectId":"<p>"}` | 200 |
| `POST users/s3_keys` for the 11th key | 200, `success: false`, `code: 114`, `Key number is reached to maximum value 10` |
| `DELETE` of a deleted key, at once and 5 s later | 200, `success: false`, `code: 114`, `Could not delete s3 keys. InvalidAccessKeyId` |
| `DELETE` of a well-formed unknown `userKeyId` | 200, success, the id echoed |

The secret appears only in the create response. The list carries a
`secretKey` field, null on every key seen. A key made this way did not
appear in the accounts API list. A key passed `aws s3 ls` on the data
plane; see [Data plane](#data-plane).

The console's `ceph_sub_users` body takes `iamAccountId` and `iamUserType`
([storage users](#storage-users)). Whether `users/s3_keys` takes them too,
and so can make a key for a service account, is unknown.

### Accounts API

Live under `accounts-api/v1/` with the IAM user token:

| Call | Result |
|-|-|
| `GET s3-keys` | 200, an empty page |
| `GET s3-keys` with a bad `pageNumber` | 400 `{"errors":[null]}` |
| `POST s3-keys` with `name`, `regionId` (UUID), and `projectId` | 500 `{"errors":[]}` |
| `POST s3-keys` with `regionId` or `projectId` missing or empty | 400, code `S3_KEY_PROJECT_ID_INVALID` |
| `DELETE s3-keys/{id}`, unknown id | 404, code `NOT_FOUND_S3_KEY` |

The 500 held on a retry, with a lower-case region name, and with extra
region and user headers. The IAM console's "Create a new S3 Key" dialog
shows an empty Region list for the IAM user and sends no request, so the
console cannot create one either.

Errors arrive as `{"errors":[{"code","message"}]}`. The transport takes
the code and message from the first entry; an empty or null list leaves
the HTTP status text.

The spec names the list search parameter `searchByNameOrAccessKey`, has
`PATCH s3-keys/{id}`, and lists the attach calls under
`service-accounts/{id}/s3-keys`. The console shows a "Restriction by IAM"
column for keys.

## Data plane

The data plane is S3-compatible (Signature V4, path-style, regions `HCM04`
and `HAN02`, HTTPS only). This SDK covers the management plane; objects go
through any S3 client. The wiki recommends **rclone**: GreenNode documents
its setup, and it ships as one static binary with a plain S3 mode. Recent
AWS CLI v2 releases send checksum headers by default that S3-compatible
servers often refuse. With default settings, including checksums,
`aws s3 ls` with `--endpoint-url https://hcm04.vstorage.vngcloud.vn` and
`AWS_DEFAULT_REGION=HCM04` exited 0 with a console API key and 255 with a
bogus key; uploads are not yet checked.

## Purchase

The console buys a project in two calls, then redirects to the payment
console checkout:

1. `POST billing-api/v2/price` for the quote.
2. `POST internal/v2/orders` with `resourceType: "object_storage"`,
   `action: "create"`, `paymentType: "manual"`, and `resourceInfo`:
   `projectName`, `purchaseTypeId` 4, `projectType` 1, `quota`,
   `archivePeriod` 0, and `billingTimeType: "block"`.

The SDK does not make these calls; see [Non-goals](storage.md#non-goals).

## Cost

- The test account is offered "Pay monthly" only, no pay as you go. Project
  types are Gold and Instant Archive, from 30 GB. Gold 30 GB costs 30,000
  VND a month.
- Buckets, S3 keys, and service accounts cost nothing to create. Requests
  are free; download traffic is free up to ten times the stored size.
- Limits: 10 S3 keys per root account, 1000 buckets per project. The
  console API refuses the 11th key with code 114; whether the limit also
  counts accounts API keys is unknown.
- The test account has one Gold 30 GB project in `HCM04`, bought monthly
  with auto-renew off, so bucket writes can run live.
