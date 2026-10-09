# vStorage: API

The vStorage calls, shapes, and costs behind [vStorage](storage.md). The SDK
surface built on them is in that design.

## Sources

Public docs (`docs.greennode.ai/vstorage`), the OpenAPI specs on
`docs.api.greennode.ai` (`vstorage-hcm04-api`, `accounts-api`), the public
vStorage console bundle and its `config.prod.json`, live reads on the test
account on 2026-09-26, the console's own calls with the test IAM user
on 2026-10-09, after the owner bought a project, and live bucket, S3 key,
service account key, bucket policy, versioning, CORS, and ACL writes on the
test project the same day; every bucket, key, and service account made was
deleted. VNG Cloud's
Go SDK and Terraform provider have no vStorage code.

## Model

- A vStorage project is a paid storage package in one vStorage region,
  `HCM04` or `HAN02`, each with a UUID region ID and an S3 host such as
  `https://hcm04.vstorage.vngcloud.vn`. The Swift farm `HCM03` is out of
  scope.
- An S3 key is an access key and secret for one project, with the rights of
  the user who made it. The secret is shown once. The console API and the
  IAM accounts API keep separate key stores; see [S3 keys](#s3-keys).
- An IAM service account starts with no rights. A console key attached to
  it acts as the service account, whose bucket rights come from a bucket
  policy that names its storage principal,
  `arn:aws:iam:::user/<subUserId>`. This is the per-bucket key: one service
  account, one bucket policy, one key. See [Attach](#attach).

## Management APIs

| API | Base | Auth | Result |
|-|-|-|-|
| vStorage console API | `https://vstorage.console.greennode.ai/internal/v1/` | IAM User token | Works |
| vStorage external API | `https://hcm04-api.vstorage.vngcloud.vn/api/v1/` | Service account token | IAM User token gets an empty 200 |
| IAM accounts API | `https://dashboard.console.greennode.ai/accounts-api/v1/` | IAM User token | Reads work; S3 key create answers 500; attach answers 404 for console keys |

The console API has the external API's paths and bodies
(`ceph/projects/{projectId}/buckets/{bucket}`) under another prefix. The
documented IAM accounts API covers service accounts, S3 keys, and attaching
a key to a service account, on its own key store. The console API has its
own S3 key and attach calls.

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
| S3 key, console list | `regionId`, `projectId`, `userId`, `subUserId` (null, or the attached service account's sub-user), `userKeyId`, `accessKey`, `secretKey` (null), `createdDate` (`dd/mm/yyyy hh:mm`), `status` (number, 1) |
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

The server ignores `iamAccountId` and `iamUserType`: every variant tried
returns the caller's own IAM user sub-user. The console bundle's enum is
`iam-user`, `root-user`, and `user-sa`, and the console calls it only with
`iam-user`.

`GET users/details` with `generated=true` answered code 114 `Error occurred
in adding storage user <root email>` while the account had no project.
With a project, both `generated=true` and `generated=false` answer the
account-level user `<root local part>-<account ID>`, with `subUserId`
`<that>:iam-<IAM user name>` and the same endpoint fields.

For a service account, `GET users/details` with `generated=true`,
`project_id=<p>`, and `iam_account_id=sa-<service account id>` answers
`subUserId` `<account user>:sa-<service account name>`. With
`generated=false`, or with the ID without `sa-`, `subUserId` is null. For a
well-formed ID that matches no service account, `generated=true` answers
200 with `success: false` and code 114. The console's own sub-user create
sends exactly this: `sa-` or `iam-` plus the ID, with `generated=true`.
The sub-user name comes from the service account's name, not its ID. No
console call deletes a sub-user.

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
| `DELETE` of a bucket holding objects, or versions and delete markers | `{"code":200,"success":true}`; the bucket and its contents are gone within a second |

The delete is asynchronous. For up to about a second after its 200,
`GetBucket` and `ListBuckets` still show the bucket, and a `GetBucket` can
answer `code: -1` with `errorMsg` `Unknown error`, or an empty body. Then
the bucket is gone and reads answer code 404.

The server never refuses a bucket delete for its contents. `details.count`
counts object versions: 2 for two versions of one key, unchanged by a
delete marker, and 0 once the versions are deleted by ID, even with a
marker left.

Not yet checked: an unknown project, and whether names are unique across
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

`POST users/s3_keys` ignores `iamAccountId` and `iamUserType` too: every
key it makes is an ordinary project key, with `subUserId` null and
`userId` the account-level user.

### Attach

Live on the test project, with a console key and a new service account:

| Call | Result |
|-|-|
| `PUT users/s3_keys/{userKeyId}/attach`, body `{"projectId":"<p>","serviceAccountId":"<sa>"}` | 200 `{"code":200,"success":true}`, no `data`; the list then shows the key's `subUserId` as `<account user>:sa-<service account name>` |
| `PUT users/s3_keys/{userKeyId}/detach`, body `{"projectId":"<p>"}` | 200, success; `subUserId` null again |
| Attach again to the same account | `success: false`, code 114, `This S3 key is already attached with this service account` |
| Attach of a key attached to another account | Code 114, `This S3 key is already attached with another service account` |
| Attach of an attached key without `serviceAccountId` | The same "another service account" message |
| Attach to an unknown service account | Code 114, `StatusCode=404` |
| Attach of an unknown key | Code 114, `S3 key not found` |
| Detach of an unattached key | Code 114, `This S3 key is not attached to any service account.` |

Every answer is HTTP 200. The "attached elsewhere" check runs before the
service account check. Neither call is idempotent. The console shows
`subUserId` in its "Restriction by IAM" column.

- An attach to a service account that has no sub-user yet makes the
  sub-user: a `generated=false` read shows it afterwards.
- A key attached to a service account that is then deleted keeps its
  `subUserId`. A detach of that key still succeeds.
- An attached key can be deleted.

### Accounts API

Live under `accounts-api/v1/` with the IAM user token:

| Call | Result |
|-|-|
| `GET s3-keys` | 200, an empty page |
| `GET s3-keys` with a bad `pageNumber` | 400 `{"errors":[null]}` |
| `POST s3-keys` with `name`, `regionId` (UUID), and `projectId` | 500 `{"errors":[]}` |
| `POST s3-keys` with `regionId` or `projectId` missing or empty | 400, code `S3_KEY_PROJECT_ID_INVALID` |
| `DELETE s3-keys/{id}`, unknown id | 404, code `NOT_FOUND_S3_KEY` |
| `POST` or `DELETE service-accounts/{id}/s3-keys/{keyId}` with a console key | 404, code `NOT_FOUND_S3_KEY` |
| `PATCH s3-keys/{id}` with `{"restricted": true}` and a console key | 404, code `NOT_FOUND_S3_KEY` |
| `GET service-accounts/{id}/s3-keys` without `pageNumber` and `pageSize` | 400 |

The 500 held on a retry, with a lower-case region name, and with extra
region and user headers. The IAM console's "Create a new S3 Key" dialog
shows an empty Region list for the IAM user and sends no request, so the
console cannot create one either.

Errors arrive as `{"errors":[{"code","message"}]}`. The transport takes
the code and message from the first entry; an empty or null list leaves
the HTTP status text.

The spec names the list search parameter `searchByNameOrAccessKey`. The
accounts API's attach and `restricted` calls work only on its own key
store, which it cannot create keys in.

## Data plane

The data plane is S3-compatible (Signature V4, path-style, regions `HCM04`
and `HAN02`, HTTPS only). This SDK covers the management plane; objects go
through any S3 client. The wiki recommends **rclone**: GreenNode documents
its setup, and it ships as one static binary with a plain S3 mode. Recent
AWS CLI v2 releases send checksum headers by default that S3-compatible
servers often refuse. With default settings, including checksums,
`aws s3 ls` with `--endpoint-url https://hcm04.vstorage.vngcloud.vn` and
`AWS_DEFAULT_REGION=HCM04` exited 0 with a console API key and 255 with a
bogus key.

With SigV4 against `hcm04.vstorage.vngcloud.vn`, one console key:

| Step | List buckets | Create bucket | List, put, delete objects in the owner's bucket |
|-|-|-|-|
| Before attach | Yes | Yes | Yes |
| 3 s and 30 s after attach | Yes | Yes | 403 `AccessDenied` |
| After detach | Yes | Yes | Yes |

With the key attached and a policy on bucket A that allows its principal
`s3:*` on `arn:aws:s3:::<A>` and `arn:aws:s3:::<A>/*`:

| Call | Bucket A, policy on | Bucket B, no policy | Bucket A, policy deleted |
|-|-|-|-|
| List, put, get, delete objects | Allowed | 403 `AccessDenied` | 403 `AccessDenied` |
| `GET` of a missing key | 404 `NoSuchKey` | 404 `NoSuchKey` | 404 `NoSuchKey` |

- The 404 on a bucket the key cannot read means the key can learn whether a
  key name exists.
- List buckets is allowed, and so is create bucket. In a bucket C that the
  key creates, put object, delete object, and delete bucket are 403. C's
  owner is the account-level user, not the sub-user; the IAM user deleted
  it.
- A policy put, a policy delete, and an attach take effect on the data
  plane within about a second.

## Bucket policy

Live on the test project, with both region headers. Paths are
`ceph/projects/{p}/buckets/{b}/policy` under `internal/v1/`. Every answer
was HTTP 200.

| Call | Result |
|-|-|
| `GET` before any put | `{"code":200,"success":true}`, no `data` |
| `PUT` `{"policy":"<document as a JSON string>"}` | `data: true` |
| `GET` after the put | `data`, the document as a JSON string |
| `DELETE` | `data: true`; a `GET` then has no `data` |
| `DELETE` again | `data: true` |
| `PUT` with a `policy` that is not valid JSON | `success: false`, code 400, Ceph's parser message, such as `At character offset 1, Missing a name for object member.` |
| `PUT` `{"policy":""}` | `success: false`, code 114, `Error occurred when updating bucket policy.` |
| `PUT` naming a sub-user that does not exist | `data: true`: the principal is not checked |

| `PUT` with a `Version` other than `2008-10-17` or `2012-10-17`, a bad `Effect`, or a statement that is not an object | `success: false`, code 400 |
| `PUT` with a statement that has no `Principal`, or `Principal: {}` | `data: true`; then policy `GET`, policy `DELETE`, and bucket `DELETE` answer HTTP 200 with an empty body |
| Any policy call on a missing bucket | HTTP 200, empty body |

The document a `GET` returns decodes equal to the one put. One run saw it
re-serialized with object keys sorted; another got it back byte for byte,
a pretty-printed document with a scalar `Action` and `Resource` included.
The server may change whitespace and key order; array order is kept.

A policy with a statement without a principal blocks the console's policy
and bucket calls. Only the data plane removes it: `DELETE /<bucket>?policy`
signed with a key of the project.

## Versioning

Paths are `ceph/projects/{p}/buckets/{b}/versioning`. Every answer was
HTTP 200 with the envelope unless the table says otherwise.

| Call | Result |
|-|-|
| `GET` before any put | `data {"versioning":false,"versioningStatus":"Off"}` |
| `PUT {"enable":true}` | `data: true`; a `GET` shows `Enabled` |
| `PUT {"enable":false}` | `data: true`; a `GET` shows `Suspended`, also on a bucket never versioned |
| `PUT {}` or `{"status":true}` | Accepted as `enable: false` |
| `PUT {"enable":"x"}` or an empty body | HTTP 400, a Spring JSON error, not the envelope |

No put returns a bucket to `Off`. `GET ceph/projects/{p}/{b}/details`
mirrors the state: `versioningStatus` `Off`, `Enabled`, or `Suspended`,
`enableVersioning` true or null, and `isVersioned` always null. A change
shows on the first read.

## CORS

Paths are `ceph/projects/{p}/buckets/{b}/cors`.

| Call | Result |
|-|-|
| `GET` with no rules | The envelope, no `data` |
| `PUT` a bare JSON array of rules with keys `AllowedOrigins`, `AllowedMethods`, `AllowedHeaders`, `ExposeHeaders`, `MaxAgeSeconds` | `data: true` |
| `GET` after the put | `data.rules[]`: `allowedHeaders`, `allowedMethods`, `allowedOrigins`, `exposedHeaders`, `id` (null), `maxAgeSeconds` |
| `DELETE`, and a second and third | `data: true` |
| `PUT {"rules":[...]}` or a body that is not JSON | HTTP 400, Spring JSON |
| `PUT` with lower-case keys | `success: false`, code 400, `MalformedXML` |
| Method `FOO`, `get`, or `OPTIONS` | Code 114, `Error occurred when updating bucket CORS.` |
| `AllowedOrigins` empty or missing, `[]`, or origin `https://*.*.example.com` | Code 400, `MalformedXML` |
| Empty `AllowedMethods`, `MaxAgeSeconds: -1`, an origin without a scheme, unknown fields | Accepted |

- `allowedMethods` comes back in the server's set order, not the order put.
- The server sets `exposedHeaders` only when the put names
  `ExposeHeaders`, and then copies the allowed headers into it, never the
  value sent. A put without `ExposeHeaders` reads back null. The
  data-plane `GET ?cors` agrees.
- A failed put keeps the previous rules.
- After a delete, the data-plane `GET ?cors` answers 404
  `NoSuchCORSConfiguration`.
- An anonymous `OPTIONS` with `Origin` and
  `Access-Control-Request-Method: GET` answers 200 with
  `Access-Control-Allow-Origin`, `Access-Control-Allow-Methods`,
  `Access-Control-Max-Age`, and `Vary: Origin` while a rule matches, and
  403 otherwise. It adds `Access-Control-Expose-Headers` only when the
  rule has exposed headers. A put and a
  delete take effect on the first request.

## Public access

- `GET` and `PUT ceph/projects/{p}/buckets/{b}/public_access` answer 403
  `IAM_PERMISSION_DENIED` for every body tried, with an IAM user holding
  `vstorage:*`. The console bundle defines `getPublicAccessBlock` and
  `updatePublicAccessBlock` on that route but never calls them. An unknown
  route, such as `.../buckets/{b}/nonexistent`, and `.../lifecycle` answer
  the same 403, so an unmapped route and a missing grant look alike.
- The console makes a bucket public through the ACL. `PUT .../acl` with
  this body answers `data: true`:

  ```json
  {"ownerCanonical":["FULL_CONTROL"],"accountCanonicals":[],
   "groups":[{"grantee":"ALL_USERS","permission":"READ"}]}
  ```

  `GET .../acl` returns `grants`, `grantsAsList` (the owner's
  `FullControl`, then `{"grantee":"AllUsers","permission":"Read"}`),
  `owner`, and `requesterCharged`. `groups: []` removes the grant.
- With the ACL grant, an anonymous bucket listing answers 200 and an
  anonymous object `GET` stays 403.
- A bucket policy with `Principal: "*"` allowing `s3:GetObject` on
  `arn:aws:s3:::<bucket>/*` makes an anonymous object `GET` answer 200.
  Deleting the policy returns 403 at once.
- `details.isPublic` stays null and `allowPublicAccess` false throughout.

## Missing bucket

Every `GET`, `PUT`, and `DELETE` on a bucket's versioning, CORS, and policy
routes answers HTTP 200 with an empty body once the bucket is gone.

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
