# vStorage: decisions

The release order and the owner's decisions for [vStorage](storage.md).

## Releases

Each release ships the SDK and CLI together, with its wiki pages.

| Release | Content |
|-|-|
| S1 | `storage` reads: `ListRegions`, `ListProjects`, `ListBuckets`, `GetBucket`; the `Storage` endpoint, region lookup, and envelope errors |
| S1.1 | Fix: every storage call except `ListRegions` sends `region` and `region_id`; a `ListProjects` fixture from the live shape; unit tests for both headers; a live `ListProjects` that finds the project |
| S2 | `CreateBucket` and `DeleteBucket` with `ErrBucketNotEmpty`, the delete wait and `NoWait`, and envelope code 112 as `ErrInvalidInput` |
| S3 | `storage` S3 keys on the console API: `ListS3Keys`, `CreateS3Key`, `DeleteS3Key`, `storage.ErrNoSecret`; `storage list-s3-keys`, `create-s3-key` with `--secret-file`, and `delete-s3-key` |
| S4 | Service account keys: `AttachS3Key`, `DetachS3Key`, `EnsureServiceAccountPrincipal`, and `S3Key.SubUserID` as the restriction state; `storage attach-s3-key`, `detach-s3-key`, `ensure-service-account-principal`, and `create-s3-key --service-account-id` |
| S5 | Bucket policy: `GetBucketPolicy`, `PutBucketPolicy` with its `Policy` and statement checks, and `DeleteBucketPolicy`; `storage get-bucket-policy`, `put-bucket-policy`, and `delete-bucket-policy`; the policy template and wiki rules; the live test runs the per-bucket key end to end |
| S6 | Bucket versioning and CORS: `GetBucketVersioning`, `PutBucketVersioning`, `GetBucketCORS`, `PutBucketCORS`, `DeleteBucketCORS`, with the CORS rule checks; the missing-bucket check on the settings and policy calls; `put-bucket-policy` needs `--yes` for a public principal; `storage get-bucket-versioning`, `put-bucket-versioning`, `get-bucket-cors`, `put-bucket-cors`, and `delete-bucket-cors`; the public-read template in the wiki |

S1.1 ships before S2, since no storage read finds data without it. S2 and
later use the test project. S2 to S5 change no existing method or command.
S3 ships before S4 because a project-wide key already unblocks aboutme. S4
ships before S5 because the policy needs the principal and an attached key
to check. The S5 scope probe passed, so S5 ships the policy calls. S6
follows S5: its live checks are answered and its shapes are in
[vStorage: settings](storage-settings.md). S6 changes one existing
command: `put-bucket-policy` gains the `--yes` rule for a public principal
(decision 44), so its release notes say so. Service accounts shipped in
[IAM writes](iam-writes.md) I2.

## Owner decisions

Decisions 1 to 50 are approved as recommended.

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

29. Approved: `GetBucketPolicy` returns `Policy ""` and no error when
    the server sends no `data`, since it answers success. The alternative,
    `ErrNotFound` as S3's `NoSuchBucketPolicy`, would make a normal state
    an error.
30. Approved: `PutBucketPolicy` requires a JSON object with a non-empty
    `Statement` array before any request. The alternative, valid JSON only,
    lets `""` reach the server's generic code 114 and lets a JSON array or
    string through.
31. Approved: the server's policy refusals, codes 400 and 114, stay
    `*APIError` with its message and no sentinel. The alternative maps code
    400 to `ErrInvalidInput`, exit 2.
32. Approved: `DeleteBucketPolicy` returns `{}` when no policy exists,
    as the server answers, so a repeat delete succeeds.
33. Approved: `GetBucketPolicy` returns the server's string unchanged.
    The server may change whitespace and key order, so the docs tell
    callers to compare decoded documents. The CLI prints it as a string,
    as the AWS CLI does.
34. Approved: the SDK does not check that a principal names a real
    sub-user (decision 46 only requires one to be present), and the
    docs say a policy that names no real sub-user grants nothing. A check
    would block valid principals such as an IAM user's.
35. Approved: the wiki template grants named object actions, not `s3:*`,
    so a leaked key cannot delete its bucket or rewrite its policy.
36. Approved: the S5 live test signs data plane calls with a small
    standard-library Signature V4 signer, not an external tool.
37. Approved: S6 is the next release after S5 and starts with its live
    checks.

38. Approved: `GetBucketVersioning` returns `{Enabled bool, Status
    string}`, `Status` being the server's `Off`, `Enabled`, or `Suspended`
    unchanged. `Enabled` alone would hide that a disabled bucket is
    `Suspended`, never `Off` again.
39. Approved: `PutBucketVersioning` takes `Enabled *bool`, required,
    and always sends `{"enable": <value>}`. A plain `bool` would let a bare
    `put-bucket-versioning` suspend versioning, since the CLI sets a field
    only when its flag is given.
40. Approved: `CORSRule` has `AllowedOrigins`, `AllowedMethods`,
    `AllowedHeaders`, and `MaxAgeSeconds`, plus a read-only
    `ExposedHeaders` that a get fills and a put never sends. The server
    never stores the `ExposeHeaders` value sent: when a put names it, the
    server copies `AllowedHeaders` into the exposed list, and when a put
    omits it, the list is null. A settable list would promise what the
    server does not do. Decision 50 covers sending `ExposeHeaders`.
41. Approved: `PutBucketCORS` refuses, before any request, an empty
    rule list, a rule without an origin or a method, an empty origin, an
    origin with more than one `*`, a method outside `GET`, `PUT`, `POST`,
    `DELETE`, `HEAD`, and a negative `MaxAgeSeconds`. This departs from ADR
    0002 rule 5 because the server answers these with a generic code 114
    or `MalformedXML` that names no rule. The alternative leaves them to
    the server.
42. Approved: `GetBucketCORS` returns an empty `Rules` list when the
    server sends no `data`; `DeleteBucketCORS` returns `{}` also when no
    rules exist; the server's codes 114 and 400 stay `*APIError` with no
    sentinel, as for the policy (decision 31).
43. Approved: S6 drops `GetBucketPublicAccess` and
    `PutBucketPublicAccess`. The `public_access` route answers 403 to the
    IAM user for every body, and an unmapped route answers the same, so
    nothing shows it works. Public reads go through a bucket policy, and
    the ACL route stays a non-goal: its `AllUsers` grant opens the bucket
    listing but no object.
44. Approved: `put-bucket-policy` needs `--yes` when an `Allow`
    statement names a public principal, which carries out decision 10.
    `storage.PolicyHasPublicPrincipal` holds the rule, so SDK callers can
    apply it too. The alternative, a separate public-read command, adds a
    call the server does not have.
45. Approved: on the settings and policy calls, a 2xx with an empty
    body triggers one `GetBucket`; its `ErrNotFound` is returned, and any
    other result returns the `EmptyResponse` error. The alternative, mapping
    the empty body straight to `ErrNotFound`, would call a bucket with a
    broken policy, or a transient empty answer, missing.
46. Approved: `PutBucketPolicy` refuses, before any request, a statement
    that is not an object or lacks a non-empty `Effect`, `Principal`,
    `Action`, or `Resource`. The server accepts a statement without a
    principal and then answers empty bodies to the console's policy reads,
    policy delete, and bucket delete until the policy is deleted through
    the data plane.
47. Approved: the `DeleteBucket` guard stays as it is. The server
    deletes a bucket with its objects and versions and never refuses, so
    the guard is the only protection.
48. Approved: live tests on the shared test project never run at the
    same time, and hand probes name buckets `vngcloud-probe-`, which no
    live test sweep deletes. A concurrent sweep deleted a probe's bucket
    mid-run.
49. Approved: S6 ships versioning, CORS, the missing-bucket check, and
    the `put-bucket-policy` `--yes` rule together, as one bucket settings
    release.
50. Approved: `CORSRule` gains `ExposeAllowedHeaders bool`. When it
    is true, `PutBucketCORS` sends `ExposeHeaders` equal to
    `AllowedHeaders`, and the server exposes the allowed headers; a get
    sets it when `exposedHeaders` is not empty. A rule that sets it with
    no `AllowedHeaders` is refused before any request. Without it, rules
    put through the SDK expose no headers, and a browser upload cannot
    read `ETag`. The name says what the server does. The alternative keeps
    the SDK from ever sending `ExposeHeaders` and documents the limit.

Open beyond the live checks: whether GreenNode will publish the console API
or accept IAM User tokens on the external API.
