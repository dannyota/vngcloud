# vStorage: keys

S3 keys, service account keys, and the bucket policy that scopes them, for
[vStorage](storage.md). The operation table, envelope errors, secrets, and
releases are in that design; the calls and live results are in
[S3 keys](storage-api.md#s3-keys) and
[Bucket policy](storage-api.md#bucket-policy); the commands are in
[vStorage: CLI](storage-cli.md).

## S3 keys

The calls are the console's own ([S3 keys](storage-api.md#s3-keys)). They
live in `storage` because they use its host, region headers, envelope, and
`ProjectID`; `iam` does not import `storage`.

- `CreateS3Key` and `DeleteS3Key` send `{"projectId": "<ProjectID>"}`. The
  server takes no name, so the Input has none. The server ignores
  `iamAccountId` and `iamUserType` in the create body, so the SDK sends
  neither.
- `S3Key` has `UserKeyID` (`userKeyId`) and `AccessKey` (`accessKey`), plus
  the other list fields under their API tags. No field holds the secret.
  The list shape carries `secretKey`, null on every key seen, so
  `ListS3Keys` sets `Sensitive` and the model leaves the field out.
- `S3Key.SubUserID` is the key's restriction state. Empty means the key is
  unrestricted: it has its creator's rights on every bucket of the project.
  `<account user>:sa-<service account name>` means the key is attached to
  that service account and acts as it. The console shows this value as
  "Restriction by IAM".
- `CreateS3Key` sets `Sensitive` and `Once`. Its Output comes from the
  create response, with `ProjectID` from the Input. A response without
  `userKeyId` or `accessKey` is an error that says a key may exist. A
  response without `secretKey` returns the key with an error wrapping
  `storage.ErrNoSecret`: the key exists but is unusable.
- `DeleteS3Key` of a deleted key is an `*APIError` with code 114, not
  `ErrNotFound`. A well-formed unknown `UserKeyID` answers success.
- Keys made through the IAM accounts API are a separate store that these
  calls do not see, and its attach calls answer 404 for console keys
  ([accounts API](storage-api.md#accounts-api)). The SDK does not use it.

## Service account keys

A key attached to a service account loses its creator's rights and acts as
the service account, whose bucket rights come only from bucket policies
that name its [principal](#principal).

- `AttachS3Key` sends `PUT users/s3_keys/{UserKeyID}/attach` with
  `{"projectId": "<ProjectID>", "serviceAccountId": "<ServiceAccountID>"}`.
  `ServiceAccountID` is the IAM ID as `iam` returns it, with no prefix.
- `DetachS3Key` sends `PUT users/s3_keys/{UserKeyID}/detach` with
  `{"projectId": "<ProjectID>"}`. The key is unrestricted again.
- Both answer `{"code":200,"success":true}` with no `data`, so the Output is
  `{}`. The state is read back with `ListS3Keys`; there is no single-key
  read. The data plane follows within 3 seconds.
- Neither call is idempotent: a repeat answers code 114. Both send with
  `Once` ([Retries](#retries)).
- Every failure the server reports is HTTP 200, `success: false`, code 114,
  with an `errorMsg`. Each is an `*APIError` with `Code` `114` and the
  message; the SDK adds no sentinel, since matching message text would
  break on any server wording change. The doc comments and the wiki list
  the messages:

| Case | `errorMsg` |
|-|-|
| Attach to the account the key is already attached to | `This S3 key is already attached with this service account` |
| Attach of a key attached to another account | `This S3 key is already attached with another service account` |
| Attach to an unknown service account | `StatusCode=404` |
| Attach of an unknown key | `S3 key not found` |
| Detach of an unattached key | `This S3 key is not attached to any service account.` |

The server checks "attached elsewhere" before it checks the service
account, so any attach of an attached key gives that message. An unknown
service account or key is code 114, not `ErrNotFound`.

An attach to a service account with no sub-user makes the sub-user, so an
attach needs no `EnsureServiceAccountPrincipal` first. A key stays attached
to a deleted service account: `ListS3Keys` still shows its `SubUserID`,
and `DetachS3Key` still succeeds.

An attached key keeps two rights outside any policy: it lists the
project's buckets and creates buckets. It cannot use a bucket it creates:
the account-level user owns it, and object writes and the bucket delete
answer 403. The wiki says so.

## Principal

A bucket policy names a service account as
`arn:aws:iam:::user/<subUserId>`. A service account has no `subUserId`
until one is generated, so the SDK offers an explicit write:

- `EnsureServiceAccountPrincipal` sends `GET users/details` with
  `generated=true`, `project_id=<ProjectID>`, and
  `iam_account_id=sa-<ServiceAccountID>`, as the console's own sub-user
  create does. With `generated=false` or without the `sa-` prefix the
  server answers a null `subUserId`, so the SDK always sends both. A
  well-formed ID that matches no service account answers code 114, an
  `*APIError` with no Output.
- It is a write (ADR 0002 rule 1), although the method is `GET`: a
  read-only profile refuses it, and no read in the SDK sends
  `generated=true`.
- It is idempotent: a repeat returns the same sub-user, and the transport
  may retry it as any `GET`.
- The Output is `SubUserID` (`data.subUserId`) and `PrincipalARN`
  (`arn:aws:iam:::user/` and `SubUserID`). A null or empty `subUserId` is
  an error that says no principal was made. A `subUserId` that is not
  exactly `<user>:sa-<name>`, with a non-empty user, a non-empty name, and
  no further colon, is an error and returns nothing, so a caller never puts
  the IAM user's own `:iam-` principal in a policy.
- Callers need it only to get `PrincipalARN` for a policy: an attach makes
  the sub-user by itself.
- The sub-user cannot be deleted through the console API. It costs nothing
  and has no rights until a policy names it.
- The SDK does not call `POST users/ceph_sub_users`: the server ignores its
  `iamAccountId` and `iamUserType` and always returns the caller's own
  sub-user.

The principal is built from the service account's name, not its ID. A
service account cannot be renamed through `iam`, but a new one with a
deleted one's name may get the same principal and so inherit any bucket
policy that still names it. The wiki tells the reader to remove a service
account from its bucket policies before deleting it.

## Bucket policy

A bucket policy grants a service account's [principal](#principal) rights
on one bucket. The calls are `ceph/projects/{p}/buckets/{b}/policy` with
both region headers ([Bucket policy](storage-api.md#bucket-policy)).

- `GetBucketPolicy` returns `Policy`, the server's `data` string unchanged.
  A response with no `data`, or `data` null, means the bucket has no
  policy: `Policy` is `""` and the error is nil. A `data` that is not a
  JSON string is a decode error.
- The server re-serializes the document: object keys sorted, array order
  kept. The SDK does not reformat it. The doc comment and the wiki tell
  callers to compare decoded documents, not bytes. The CLI prints
  `{"Policy": "<document as a string>"}`, as `aws s3api get-bucket-policy`
  does.
- `PutBucketPolicy` checks `Policy` before any request: valid JSON, a
  top-level object, and a `Statement` member that is an array with at least
  one element. Anything else is `ErrInvalidInput` with no request. This
  catches the empty string, which the server refuses only with a generic
  code 114, and JSON that is not an object. To remove every statement, call
  `DeleteBucketPolicy`.
- `PutBucketPolicy` sends `{"policy": "<Policy>"}`, the caller's text
  unchanged as a JSON string. A put replaces the whole policy. Success is
  the envelope's `success: true`; the Output is `{}`.
- The server's refusals are `*APIError` with the envelope code and message
  and no sentinel: code 400 carries Ceph's parser message, and code 114 a
  generic one. The CLI prints the code and the message and exits 1.
- `DeleteBucketPolicy` returns `{}`. A delete of a bucket with no policy
  also succeeds, so a repeat delete returns `{}`.
- Put and delete give the same result when repeated, so both keep the
  transport's retries.
- The server does not check principals. A policy that names a sub-user that
  does not exist, a mistyped ARN, or a deleted service account is accepted
  and grants nothing, and the SDK cannot detect it. The doc comment and the
  wiki say so, and tell callers to copy `PrincipalARN` from
  `EnsureServiceAccountPrincipal` and to check access with the attached key.
- The data plane follows a put or delete within about a second.

### Template

The wiki and the S5 live test use this template, which grants object work
and nothing on the bucket's settings:

```json
{"Version": "2012-10-17", "Statement": [
  {"Sid": "Bucket", "Effect": "Allow",
   "Principal": {"AWS": ["<PrincipalARN>"]},
   "Action": ["s3:ListBucket", "s3:GetBucketLocation",
              "s3:ListBucketMultipartUploads"],
   "Resource": ["arn:aws:s3:::<bucket>"]},
  {"Sid": "Objects", "Effect": "Allow",
   "Principal": {"AWS": ["<PrincipalARN>"]},
   "Action": ["s3:GetObject", "s3:PutObject", "s3:DeleteObject",
              "s3:AbortMultipartUpload", "s3:ListMultipartUploadParts"],
   "Resource": ["arn:aws:s3:::<bucket>/*"]}]}
```

`s3:*` also grants the bucket delete and policy changes on that bucket in
S3, so a leaked key could delete the bucket or rewrite its policy. Whether
Ceph lets a non-owner do so is unchecked; the template avoids the question.

### Wiki rules

The SDK and CLI storage pages state:

- An attached key lists every bucket of the project and can create a
  bucket, but cannot put or delete objects in it, or delete it.
- A `GET` of a missing object answers 404 `NoSuchKey` even in a bucket the
  key cannot read, so a key learns which object names exist anywhere in
  the project. Do not put secrets in object names.
- An attach makes the service account's sub-user. Run
  `EnsureServiceAccountPrincipal` only to get `PrincipalARN` for a policy.
- A policy that names no real sub-user is accepted and grants nothing.
- `GetBucketPolicy` returns the document re-serialized; compare decoded
  documents.
- The template above, and the reason it avoids `s3:*`.

## Retries

- `CreateS3Key` sets `Once`: it is sent once, with no retry, no resend
  after a 401, and no redirect. After a 5xx, a network error, or a
  response that fails to decode, the error says a key may exist: list the
  keys and delete any `UserKeyID` the caller does not know, since it has
  lost its secret. A 4xx `*APIError` passes through unchanged. The SDK
  lists nothing itself: a key another client made at the same time would
  look the same.
- `AttachS3Key` and `DetachS3Key` set `Once`, because the transport treats
  every `PUT` as idempotent and a resent attach whose first try took effect
  would fail with code 114. A 429 or a failed dial returns an `*APIError`
  with `Retryable` true; the caller may rerun. After a 5xx or a network
  error, the error says the change may have happened and names
  `list-s3-keys`. A rerun that answers "already attached with this service
  account", or for a detach "not attached", means the first try took
  effect.
- `DeleteS3Key` keeps the transport's retries. A retried delete whose first
  attempt took effect returns code 114.

## Errors

| Case | Result | CLI code and exit |
|-|-|-|
| `--secret-file` exists or its directory is missing | No request | `InvalidUsage`, 2 |
| Key create got a 5xx, a network error, or no decodable response | Error says a key may exist | 1 |
| Key create response held no secret | `storage.ErrNoSecret`, key returned | `SecretFileFailed`, 1, key deleted |
| 11th key in the account, envelope code 114 | `*APIError` | `114`, 1 |
| Key delete of a deleted key, envelope code 114 | `*APIError`, not `ErrNotFound` | `114`, 1 |
| Secret file write failed after create | Key deleted | `SecretFileFailed`, 1 |
| Attach or detach refused, envelope code 114 | `*APIError`, message from the table above | `114` and the message, 1 |
| Attach or detach got a 5xx or a network error | `*APIError`, says it may have happened | 1 |
| Attach failed in `create-s3-key --service-account-id` | Key deleted, no file | The attach's code, 1 |
| Principal for an unknown service account, envelope code 114 | `*APIError`, no Output | `114`, 1 |
| `subUserId` null, empty, or not `<user>:sa-<name>` | Error, no Output | `NotServiceAccountPrincipal`, 1 |

## Security

- A key is unrestricted from its create until its attach. The CLI's
  `create-s3-key --service-account-id` attaches before it writes the secret
  file, so an unrestricted secret never reaches disk
  ([create-s3-key](storage-cli.md#create-s3-key)). If the attach fails for
  any reason, including an ambiguous 5xx, the CLI deletes the key; if that
  delete fails, the error names the `UserKeyID`. SDK callers get the same
  rule in the `AttachS3Key` doc comment.
- A detach widens a key to its creator's project-wide rights at once, and
  an attach can cut off a running app, so both commands need `--yes`.
- The adversarial review checks: `Once` on attach and detach; no
  `generated=true` outside `EnsureServiceAccountPrincipal`; the `:sa-`
  check; the key delete after a failed attach; no secret file before the
  attach succeeds; and path checks on `UserKeyID` and `ServiceAccountID`.
- Service account names, `subUserId` values, and principal ARNs are account
  data. Fixtures use `<account-user>:sa-<name>`.

## Testing

Unit tests use `httptest`:

- Attach and detach paths and bodies; `Once` (one `PUT` after a 502 and
  after a 429); each code 114 message reaches `*APIError.Message`.
- `EnsureServiceAccountPrincipal` sends `generated=true`, `project_id`, and
  `iam_account_id=sa-<id>`; builds `PrincipalARN`; refuses a null
  `subUserId`, an `:iam-` one, and one with a further colon; code 114 for
  an unknown service account; read-only refusal with no request.
- `create-s3-key --service-account-id`: the attach runs before the file is
  opened; a failed attach deletes the key and leaves no file; a failed
  cleanup names the `UserKeyID`.

The S4 live write test needs `VNGCLOUD_LIVE_STORAGE_PROJECT_ID` and the
owner's approval, as in [vStorage](storage.md#testing). It deletes a
leftover service account named `vngcloud-live-storage`, then:

1. Creates that service account and calls
   `EnsureServiceAccountPrincipal` twice: the same `SubUserID`, with `:sa-`.
2. Creates a key, attaches it, and checks that `ListS3Keys` shows the
   key's `SubUserID` equal to the principal's.
3. Repeats the attach: code 114. Detaches: `SubUserID` empty. Repeats the
   detach: code 114.
4. Creates a second key, attaches it, and deletes it while attached.
5. Cleanup, registered as each ID is known, deletes both keys and the
   service account and asserts none remains. It logs statuses and counts.

The fixed name keeps each run to one undeletable sub-user and checks that
a same-name recreate returns the same principal.

The S5 live write test runs the per-bucket key end to end, with the same
approval and service account name. Data plane calls use a small Signature
V4 signer in the live test, standard library only, against the region's
`s3Host` with path-style URLs. It deletes leftover `vngcloud-live-`
buckets, then:

1. Creates buckets A and B. `GetBucketPolicy` on A returns `""`.
   `GetBucketPolicy` on a missing bucket is expected to return
   `ErrNotFound` (code 404); the test logs the code it gets.
2. Creates the service account, calls `EnsureServiceAccountPrincipal`, and
   puts the [template](#template) on A. `GetBucketPolicy` returns a
   document that decodes equal to the one put.
3. Creates a key with the attach. Within 5 seconds the key puts, gets,
   lists, and deletes an object in A. A put in B answers 403.
4. Deletes A's policy. Within 5 seconds a put in A answers 403. A second
   delete returns `{}`, and `GetBucketPolicy` returns `""`.
5. Cleanup, registered as each ID is known, deletes the key, the buckets,
   and the service account, and asserts none remains. If a step failed
   with the test object still in A, cleanup first puts the template back
   and deletes the object with the key. It logs statuses and counts only.

Unit tests for the policy calls cover: the path and region headers; the put
body as a JSON string; each client-side refusal (empty, invalid JSON, an
array, an object without `Statement`, an empty `Statement`) with no request;
no `data` and null `data` as `""`; code 400 and code 114 reaching
`*APIError.Message`; and a second delete as `{}`.

## Live checks

Answered for S4 and S5, on the test project: the create and sub-user
bodies' ignored fields; the `users/details` sub-user and its code 114 for
an unknown service account; attach, detach, and each code 114; an attach
that makes the sub-user; a key attached to a deleted service account; the
accounts API's 404 for console keys; the policy calls and their errors;
and the data plane with no policy, with a policy on one bucket, and after
the policy delete. The results are in [S3 keys](storage-api.md#s3-keys),
[Bucket policy](storage-api.md#bucket-policy), and
[Data plane](storage-api.md#data-plane).

Open: the S5 live write test repeats the policy-with-key check with the
[template](#template) instead of `s3:*`.
