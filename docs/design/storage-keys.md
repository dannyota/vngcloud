# vStorage: keys

S3 keys and service account keys for [vStorage](storage.md). The
operation table, envelope errors, secrets, and releases are in that
design; the calls and live results are in
[S3 keys](storage-api.md#s3-keys); the commands are in
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

An attached key keeps two rights outside any policy: it lists the
project's buckets and creates buckets. The wiki says so.

## Principal

A bucket policy names a service account as
`arn:aws:iam:::user/<subUserId>`. A service account has no `subUserId`
until one is generated, so the SDK offers an explicit write:

- `EnsureServiceAccountPrincipal` sends `GET users/details` with
  `generated=true`, `project_id=<ProjectID>`, and
  `iam_account_id=sa-<ServiceAccountID>`, as the console's own sub-user
  create does. With `generated=false` or without the `sa-` prefix the
  server answers a null `subUserId`, so the SDK always sends both.
- It is a write (ADR 0002 rule 1), although the method is `GET`: a
  read-only profile refuses it, and no read in the SDK sends
  `generated=true`.
- It is idempotent: a repeat returns the same sub-user, and the transport
  may retry it as any `GET`.
- The Output is `SubUserID` (`data.subUserId`) and `PrincipalARN`
  (`arn:aws:iam:::user/` and `SubUserID`). A null or empty `subUserId` is
  an error that says no principal was made. A `subUserId` without a `:sa-`
  segment is an error and returns nothing, so a caller never puts the IAM
  user's own `:iam-` principal in a policy.
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
| `subUserId` null, empty, or not `:sa-` | Error, no Output | 1 |

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
  `subUserId` and an `:iam-` one; read-only refusal with no request.
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

## Live checks

Answered for S4, on the test project: the create and sub-user bodies'
ignored fields; the `users/details` sub-user; attach, detach, and each
code 114; the accounts API's 404 for console keys; and the data plane
before attach, after attach, and after detach. The results are in
[S3 keys](storage-api.md#s3-keys).

Open, checked by the S4 live test: the attach body takes the unprefixed
IAM ID; a repeat ensure and a same-name recreate return the same
principal; a key can be deleted while attached.

Open for S5, its core claim, before any policy code ships. With the key
attached to a service account and a policy on bucket A that allows its
`PrincipalARN` `s3:*` on `arn:aws:s3:::<A>` and `arn:aws:s3:::<A>/*`:

1. The key lists, puts, gets, and deletes objects in A.
2. The key gets 403 on list, put, and delete in bucket B, which has no
   policy for the principal.
3. After the policy delete, the key gets 403 in A again.
4. Who owns a bucket the attached key creates, and whether the key can
   delete it.
5. Whether an attach before any ensure makes the sub-user.
