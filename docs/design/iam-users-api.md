# IAM users: API and checks

Wire evidence and proposed SDK contracts for [IAM users](iam-users.md).
[Existing IAM API evidence](iam-writes-api.md) owns endpoints and shared
attachment calls. No live user create, get, filtered list, or delete response
has been verified for this design.

## Sources

- [Accounts OpenAPI][spec], version 1.0.0: paths, request properties,
  statuses, and the user-list schema below. Its server is
  `https://iamapis.vngcloud.vn/accounts-api`. Use the SDK's existing
  Dashboard endpoint; this source does not verify that gateway's writes.
- [Console main bundle][main]: `getListUserAccounts`, `getUserById`,
  `createUser`, `deleteUAById`, user-model defaults, and validators.
  [User-account chunk][users]: `createUsers` sends username and password,
  discards the create response, and enriches list rows using get results.
  [Account-settings chunk][settings] defines custom password controls.
  [Quota chunk][quota] names `quotas/iam-user`. These are source-level
  inferences, not server response evidence. Bundles were read as text and
  never executed; runtime filenames are content-addressed evidence pins.
- The [product documentation index][index] links
  [User Accounts][product-users], [Create an IAM User Account][product-create],
  and [Delete an IAM User Account][product-delete]. Those page bodies were
  inaccessible during verification. Product-level claims about uniqueness,
  free use, default-deny, and deletion recovery remain unverified here.

[spec]: https://docs.api.greennode.ai/service-docs/accounts-api.html
[main]: https://iam.console.greennode.ai/main.a0cfd100731832eb.js
[users]: https://iam.console.greennode.ai/718.16d2630147e1e633.js
[settings]: https://iam.console.greennode.ai/953.d8123c2354e6d86e.js
[quota]: https://iam.console.greennode.ai/373.d55091d035f19fda.js
[index]: https://docs.greennode.ai/llms.txt
[product-users]: https://docs.greennode.ai/identity-and-access-management-iam/cac-loai-dinh-danh-iam/tai-khoan-user-accounts
[product-create]: https://docs.greennode.ai/vstorage/object-storage/vstorage-hcm03/identity-and-access-management/managing-vstorage-access-account/iam-user-account/create-an-iam-user-account
[product-delete]: https://docs.greennode.ai/vstorage/object-storage/vstorage-hcm03/identity-and-access-management/managing-vstorage-access-account/iam-user-account/delete-an-iam-user-account

## Calls

Paths below are relative to `accounts-api/v1/` on the configured Dashboard
endpoint. The spec documents the four operations, including every listed status.
It does not document user-specific error bodies or meanings of 409.

| Operation and path | Success | Documented errors |
|-|-|-|
| `POST iam-users` | 201 | 400, 401, 403, 409, 500 |
| `GET iam-users/{id}` | 200 | 401, 403, 404, 500 |
| `DELETE iam-users/{id}` | 204 | 401, 403, 404, 409, 500 |
| `GET iam-users` | 200 | 401, 403, 500 |

The list spec requires `pageSize` and zero-based `pageNumber`. Only the console
supplies `name` and optional `from`. Filtering semantics and case handling are
unknown. Expose only `Name`; do not use `from=iam` to hide unclassified targets
from guards. `GET quotas/iam-user` and its limit/usage mapping are console
evidence only, used by probes, not a new SDK method.

## Bodies and responses

Create sends exactly `{"username":"<account>","password":"<secret>"}`. The spec
requires the body but does not mark either property required; the console and
proposed SDK require both actual values. The console supports local password
generation, not a server-generation switch. No observed create field supplies
groups, policies, console access, or a forced-password-change option. Absence of
those fields does not prove server defaults or sign-in behavior.

The 201 response has no documented schema. The console ignores its body. Empty
response, optional `id`, and malformed response handling are proposed client
behavior, not evidence of what the server returns. DELETE's 204 has no body. A
create 409 may be a duplicate name; quota or another conflict is not ruled out.
A delete 409 may occur despite empty attachment reads. Never translate it into
`ErrInUse`, success, or automatic detach.

The list schema has `data`, `pageNumber`, `pageSize`, `totalItems`, and
`totalPages`. Rows have `id`, `username`, and RFC 3339 `createdAt`. Existing
sanitized reads support that minimal row shape. The spec does not make metadata
keys required, so strict completeness is a local confirmation rule, not a claim
about every valid list response.

The console's user model initializes `id`, `username`, `createdAt`, `enabled`,
`lastUse`, `from`, `idpId`, and `twoFactorAuth`. It also has UI-only fields,
including a blank `password`, group selections, and loading flags. The get
mapper assigns arbitrary response properties onto those defaults. Therefore
neither field presence nor wire types follow from the model alone. The list view
uses get results for `enabled`, `lastUse`, and `from`; the password UI checks
`from === "iam"`. Treat `iam` and `idp` as observed console discriminators, not
a complete server enum. MFA model fields include email, SMS, and Google
authenticator state, with contact values. Omit these and `idpId` from public
outputs; preserve their presence in sanitized raw fixture evidence.

The console username validator allows 5 to 50 ASCII letters, digits,
underscores, hyphens, or dots. Its password validator requires 8 to 50
JavaScript characters and at least one upper-case ASCII letter, lower-case ASCII
letter, digit, and special character. The special class includes ASCII space and
punctuation. Use the exact expression below; it requires a special character but
does not restrict all other characters.

```text
^(?=.*[A-Z])(?=.*[a-z])(?=.*[0-9])(?=.*[ !"#$%&'()*+,-./:;<=>?@\[\]\\^_`{|}~]).{8,50}$
```

The final dot is not a whitelist of password characters. The settings UI allows
minimum length 8 to 50, configurable required classes, expiration 1 to 1095
days, and reuse history 1 to 24. These UI rules do not establish server policy
for a particular account. The SDK validates shape only.

## SDK

All methods use `context.Context`, pointer Input, and `(*Output, error)` in
`danny.vn/vngcloud/iam`, with `iam.<Method>` operation names. Required fields
use `vngcloud:"required"`; nil required inputs fail locally.

### Reads

- `GetUserInput { UserID string }`, required and checked by
  `core.CheckPathID`; `GetUserOutput { User UserDetail }`.
- `ListUsersInput` retains `Page int` and `Size int` and adds optional
  `Name string`; `ListUsersOutput` remains `core.List[User]`.
- `UserDetail` embeds the existing `User` and adds `Enabled bool` and
  `From string` with API JSON tags. Fix `LastUse`'s type only after raw
  evidence and design review; never guess `int64` from a console default.
  This type gate blocks the model's implementation.

Public `ListUsers` still returns one page and no metadata. Page <= 0 sends 0;
Size <= 0 sends the existing default, 10000. Send `name` only when non-empty,
query-encoded without trimming or case normalization. Do not promise exact
filtering. Input shape bounds for `Name` match Username below. Existing keyed
literals and methods retain their behavior except the proposed body cap; adding
a field breaks unkeyed struct literals. Document that source-compatibility limit
in release notes.

A private list helper must retain presence-aware metadata for create
confirmation; `ListUsersOutput` cannot prove completeness. Public reads remain
tolerant of missing metadata. A guard target read requires usable, matching ID
and exact `from: "iam"`, never zero-value model defaults.

### Generation

When Password is empty, generate 32 ASCII characters with `crypto/rand`. Use
upper- and lower-case ASCII letters, decimal digits, and
`!@#$%^&*()_+-=[]{}:;,.?/<>|~`. All are in the console's special class; spaces,
quotes, backslash, and backtick are deliberately excluded.

Sample uniformly without modulo bias. Reject a whole candidate unless all four
classes appear; do not fix predictable class positions. Bound candidate attempts
at 256 and entropy consumption at 64 KiB, including rejected byte samples. Check
cancellation between draws and return a fixed local error on entropy failure or
exhaustion before any request. Production randomness is never caller-selectable;
inject a reader only through private test seams. This gives well over 190 bits
of entropy with a functioning uniform source. Do not copy the console's
`Math.random` generator or claim compatibility with all custom policies.

### Writes

- `CreateUserInput { Username string; Password vngcloud.Secret }`.
  Username is required. Empty Password selects generation; an empty
  supplied CLI file must fail before reaching this method.
- `CreateUserOutput { User UserDetail; Password vngcloud.Secret;
  CreatedID string }`. Password holds only a locally generated value.
  CreatedID holds only a valid ID from this invocation's 201 response,
  never an ID discovered by list or get. User may be partial.
- `DeleteUserInput { UserID string }`, required and path-validated;
  `DeleteUserOutput {}`. No partial update fields or guard override exist.

Validate before authentication or requests: Username is non-empty valid UTF-8,
at most 4096 bytes, with no whitespace or controls. Supplied Password is valid
UTF-8, at most 4096 bytes, with no controls; spaces are allowed and preserved.
Never quote invalid input. The server owns actual length, character-class,
uniqueness, and policy checks. Do not mutate the caller's Input while generating
or clearing internal values.

Create uses `DoJSONStatus`, `Once`, a per-call `NotSent`, `Sensitive`, `Redact`
with the submitted password, `WithholdMessage` with fixed text, and
`ClassifyError` returning fixed status-based codes. Never retain raw server
codes as a fallback. Serialize the password explicitly in a private wire struct;
marshaling Secret directly would send `[redacted]`. Use these privacy controls
on private confirmation requests too. Return no arbitrary server string from a
malformed response or raw unwrap cause.

| Create result | Output and error |
|-|-|
| Validation, entropy, or `NotSent` failure | Nil output; safe local cause |
| Definite 4xx rejection | Nil output; sanitized API error |
| 201 and confirmed matching user | Output and nil error |
| 201 but invalid body or failed confirmation | Partial; unconfirmed |
| Any other possibly sent failure | Partial; uncertain |

Use the observed status, not a nested provider error's status, to identify a
definite rejection. A 4xx with body overflow still has its status and is
rejected; lost status, unexpected 2xx/3xx, 5xx, decode/read failure without a
reliable 201, and cancellation after a possible send are uncertain. `NotSent`
wins before status classification. `Retryable` alone does not prove no send,
particularly with a custom RoundTripper.

A partial output always retains requested Username and the generated Password.
Set CreatedID only from a complete valid 201 object; retain it if confirmation
fails. Reject duplicate identity or paging keys in private create, get, and list
decoders before trusting any value. Use existing `ErrCreateUnconfirmed` only for
known 201 acceptance, and new `ErrUserCreateUncertain` for unknown outcomes.
Generalize the former's service-account-specific text without changing its
identity or existing behavior. Neither case triggers a second POST or an
automatic SDK delete.

After a valid 201 object with an ID, validate that ID and read it once. Require
returned ID to match case-insensitively, username to match exactly, and `from`
to equal `iam`. An invalid/non-string ID is unconfirmed, never a fallback to a
name search. An empty body or object with absent/empty ID permits one filtered
list request, page 0, size 1000. Require non-null data and all paging keys, page
0, positive pageSize <= 1000, totalItems equal to data length, length <=
pageSize, and totalPages consistent with a complete single page (0 or 1 for
empty, 1 otherwise). Reject duplicate IDs, more than one exact username,
substring-only matches, or an invalid match ID. Read that one match by ID and
verify its identity and `from: "iam"`. This confirms current state, not creation
ownership; CreatedID stays empty. No match, incomplete metadata, malformed body,
or failed get is unconfirmed.

Private confirms suppress capture and unsafe error text. Validate returned
string fields against their recorded schema and submitted identity before
publishing them. Refuse a response identity containing the submitted password;
never redact an ID and then use the altered ID for a request. Redact the
submitted password from other retained text. A successful read never supplies a
password. Preserve only trusted input and create identity in partial results,
not partially decoded server data.

Delete runs the [ordered guards](iam-users.md#guards), then sends DELETE with
`OK: 204`, a 64 KiB response cap, and `NoRedirect`. Keep the shared idempotent
retry policy. Return an empty Output only on 204. A target 404 returns nil and
`ErrNotFound`; 409 remains Conflict; every exhausted failure remains an error.
Wrap guard dependency errors with a fixed read label without losing error
identity. Cleanup must confirm target absence with a fresh GetUser after any
delete error classified as 404; do not infer it from IsNotFound alone.

Use `errors.Is` for `ErrInvalidInput`, guard sentinels, create outcome
sentinels, and context cancellation/deadline errors. Keep sanitized
`*vngcloud.APIError` reachable through `errors.As`, including status and
Retryable; preserve safe underlying causes. Map 401 to `ErrAuth`, 403 to
`ErrPermission`, 404 to `ErrNotFound`, and 429 to `ErrRateLimited` through the
existing core mapping. CLI create-outcome handling precedes underlying status
mapping, so an uncertain/accepted create always exits 1.

### Bounds and cancellation

New create/delete responses cap at 64 KiB, get at 256 KiB, and lists at 4 MiB.
Fail on overflow; never decode a truncated prefix. Confirmation performs at most
one list and one get, or one get with CreatedID. No wait or eventual-consistency
polling occurs. All SDK requests and guard reads use the caller's context;
cancellation stops further reads and writes. Only CLI file-failure cleanup
detaches, with its 10-second total limit.

Reads and deletes retain configured transport retries: retryCount + 1 attempts
per send, plus the existing single 401 refresh send, and at most 30 seconds per
backoff. The HTTP client timeout bounds each request; callers must set an
overall context deadline for a multi-request operation. No new retry loop wraps
the transport. Create remains one attempt.

Existing caller, action, policy, and guard readers omit `MaxBody`. Before this
feature's implementation, separately brief and review caps for
`GetCallerIdentity`, `ListActions`, `GetPolicy`, and the HTTP reads in
`iam/guard.go`: 256 KiB for single objects, 4 MiB for lists. Preserve their
fail-closed classification and error identities. Oversized existing reads will
begin failing; owner approval must cover that compatibility change. Do not
describe a capped user get as bounding the whole guard graph.

## Unit tests

Use synthetic canaries and `httptest`, never real credentials or live calls.
Each operation tests its exact path, body/query, success status, and every error
in the [calls table](#calls), plus unexpected responses.

- Decode manager-sanitized raw get/list/create fixtures through the SDK.
  Record no-body create as empty bytes with provenance, not invented `{}`.
  Test omitted, null, wrong-type, and duplicate JSON identity/metadata keys;
  reject ambiguous duplicate keys in security-relevant private decoders.
- Check generator alphabet, length, all classes, unbiased sampling logic,
  fresh values, entropy failure, cancellation, and bounded exhaustion.
  Test password spaces, UTF-8, controls, byte bounds, and no Input mutation.
- Count create POSTs for 401, 429, 500/502, all redirects, partial bodies,
  body overflow, dropped connections, and cancellation before/after send.
  Distinguish proven NotSent from custom-transport dial-shaped errors.
- Test every outcome-table branch with generated and supplied passwords,
  both sentinels via `errors.Is`, API errors via `errors.As`, and context
  identities. Test 201 with empty body, null/array/malformed JSON, missing,
  invalid, or mismatched ID, failed confirmation, and duplicate matches.
- Exercise missing/null/negative/inconsistent list metadata, multi-page
  results, substring and case-only names, wrong get identity, caps, and
  public ListUsers compatibility without Name. Verify CreatedID provenance.
- Test self before origin or privilege reads; unknown/root caller; missing
  caller ID; service-account caller; non-iam origin; direct and inherited
  privilege; deny-only attachment; any group; every guard read failure;
  empty action catalog; malformed policy patterns; null/short attachments.
  Assert zero mutations on refusal, not zero reads.
- Test DELETE 204, 404, 409, retries ending in 404, exhausted 5xx, no
  redirects, cancellation, and target versus dependency 404 cleanup rules.
- Canary-check stdout, stderr, debug, capture, every reachable error node,
  and supported fmt/slog/JSON paths. Include nested escaped password
  echoes in error codes/messages and success bodies; unsafe formatting is
  forbidden, not claimed safe. Confirm requests serialize the actual value.
- CLI tests cover exactly-one flag, empty file, private file mode, symlinks,
  FIFO/device/directory refusal, terminal stdin, CRLF/spaces, overflow,
  shared stdin, stalled input deadline, and cancellation. Read-only must
  perform no input read or request; delete without `--yes` sends nothing.
- Inject file open/write/sync/close/removal failures. Check exclusive 0600
  creation, existing-file preservation, saved passwords on partial output,
  cleanup only with CreatedID, no detach or name cleanup, bounded detached
  context, refusal/409/target-404/dependency-404 handling, and exit precedence.

## Probes

Only the manager may run probes, after owner approval naming the account,
resources, sign-in scope, cost assumption, and cleanup authority. IAM is global.
This design grants no standing approval. Serialize the probe with other IAM
writers in the test account. Use `vngcloud-probe-<8 hex>` names, precheck
absence, and keep a private per-run ledger of returned IDs. A prefix alone never
authorizes cleanup. Do not alter policy or account security settings to make the
probe pass.

1. Verify permissions and quota headroom using `quotas/iam-user`. Read the
   caller and its own user only if expressly approved; record get keys,
   types, origin, timestamp forms, and missing/null fields.
2. Create one disposable user with a generated password using the same
   alphabet. Hold response bytes only in memory; record status, key names,
   types, and whether an ID exists. Keep the password until cleanup.
3. List by full disposable username, a substring, a case variation, and
   a definitely absent random name. Record metadata and filter semantics.
   Get the disposable user and verify ID, origin, model fields, empty
   direct policies, and empty groups. Attachment calls use the policies
   endpoint, not the accounts endpoint.
4. Try the same name once more only if approved; record status and safe
   code. A 409 establishes this duplicate case only, not all conflicts.
   Avoid short-name or weak-password negative probes: custom policy may
   accept them and create extra identities. Use offline validation tests.
5. If separately approved, sign in as the disposable user through the
   existing IAM login, without a persistent token cache, then read caller
   identity and compare IDs in memory. Record booleans only. MFA or sign-in
   refusal is a gate, not permission to disable account controls.
6. Delete only the attributable disposable ID. Record status, repeat-delete
   status, target-get status, and quota restoration. If no create ID exists,
   obtain specific manager/owner reconciliation authority for the discovered
   ID before deletion. Stop and report leftovers privately on ambiguity.

No probe deletes the caller, an attached user, or a pre-existing user. Delete
409 meaning, custom policy breadth, concurrent races, and token revocation after
deletion remain unverified unless separately evidenced. A read failure or name
mismatch must never expand cleanup scope.

### Fixture provenance

Keep account-bearing raw reads only under the private output paths in [live
data](../../instructions/live-data.md). No password-bearing response may be
captured. Sanitize create bytes in memory before writing a fixture; if they
contain a password, omit that response fixture and preserve the schema gate
until an approved safe evidence method exists. Never substitute SDK output or a
hand-built response for raw evidence.

Fixtures are `get_user.json`, `list_users_filtered.json`, and, if safe,
`create_user.json` under `testdata/iam/`. Provenance records method, path with
placeholders, status, raw origin, removed sensitive values, and intentional
model omissions. Use `<id>`, `<account>`, and `<secret>` or short synthetic IDs
that pass path validation; retain structural keys and types. Validate JSON only
for nonempty bodies. Record an empty HTTP body as such. The manager checks
sanitation before workers receive fixtures. Re-review any contract changed by
the probes.

## Live checks

Implement and independently review live helpers before running them. Add GetUser
and filtered ListUsers to the authorized read suite. Gate `TestLiveWriteIAMUser`
on both `VNGCLOUD_LIVE_WRITE=1` and `VNGCLOUD_LIVE_IAM_USER=1`. Use a unique
`vngcloud-live-<8 hex>` name and only resources recorded by this run. Never
sweep all prefix users.

Exercise generation, create, private temporary secret-file persistence, get,
filtered list, self refusal with no DELETE, and guarded delete. Register cleanup
before create using a ledger, but do not authorize a name-based delete. On
uncertain ownership, report a leftover and stop. Remove temporary secrets on
completion and emit only fixed labels, statuses, counts, and booleans.
Compile-only checks are not live evidence.
