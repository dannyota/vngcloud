# IAM users: API and checks

Wire evidence and proposed SDK contracts for [IAM users](iam-users.md).
[Existing IAM API evidence](iam-writes-api.md) owns endpoints and shared
attachment calls. No live user response is verified for this design.

## Sources

- [Accounts OpenAPI][spec], version 1.0.0: paths, request properties, statuses,
  and the user-list schema below. Its server is
  `https://iamapis.vngcloud.vn/accounts-api`; the SDK uses its existing
  Dashboard endpoint, whose writes this source does not verify.
- [Console main bundle][main]: `getListUserAccounts`, `getUserById`,
  `createUser`, `deleteUAById`, `editUsername` (a candidate PUT rename; see
  [administration evidence](iam-users-admin-api.md#sources)), model defaults,
  and validators. [User-account chunk][users]: `createUsers` sends username
  and password and discards the response. [Account-settings chunk][settings]:
  custom password controls. [Quota chunk][quota]: `quotas/iam-user`.
  Source-level inferences read as text, never executed; not server evidence.
- Product pages [User Accounts][product-users], [Create an IAM User
  Account][product-create], and [Delete an IAM User Account][product-delete]
  describe denied-by-default creation, password/name rules, name uniqueness,
  and irreversible deletion: claims inherited from pages outside this research
  scope, not server behavior.

[spec]: https://docs.api.greennode.ai/service-docs/accounts-api.html
[main]: https://iam.console.greennode.ai/main.a0cfd100731832eb.js
[users]: https://iam.console.greennode.ai/718.16d2630147e1e633.js
[settings]: https://iam.console.greennode.ai/953.d8123c2354e6d86e.js
[quota]: https://iam.console.greennode.ai/373.d55091d035f19fda.js
[product-users]: https://docs.greennode.ai/identity-and-access-management-iam/cac-loai-dinh-danh-iam/tai-khoan-user-accounts
[product-create]: https://docs.greennode.ai/vstorage/object-storage/vstorage-hcm03/identity-and-access-management/managing-vstorage-access-account/iam-user-account/create-an-iam-user-account
[product-delete]: https://docs.greennode.ai/vstorage/object-storage/vstorage-hcm03/identity-and-access-management/managing-vstorage-access-account/iam-user-account/delete-an-iam-user-account

## Calls

Paths are relative to `accounts-api/v1/` on the configured Dashboard endpoint.
The spec documents these statuses and no error bodies or 409 meanings.

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

Create sends `{"username":"<account>","password":"<secret>"}`. The spec requires
the body, not either property; the console and SDK require both values. The
console generates passwords locally; no server switch, group, policy,
console-access, or forced-change field was observed, and absence proves no
server default or sign-in behavior.

The 201 response has no documented schema and the console ignores its body;
empty, optional-`id`, and malformed handling are proposed client behavior.
DELETE's 204 has no body. A create 409 may be a duplicate name, a quota, or
another conflict. A delete 409 is the server's refusal, for attachments or
anything else; never translate it into success or automatic detach.

List fields are `data`, `pageNumber`, `pageSize`, `totalItems`, `totalPages`;
rows have `id`, `username`, and RFC 3339 `createdAt`. Metadata is not required
by the spec; completeness is a local confirmation rule.

The console's user model initializes `id`, `username`, `createdAt`, `enabled`,
`lastUse`, `from`, `idpId`, and `twoFactorAuth`, plus UI-only fields such as a
blank `password`; its get mapper copies arbitrary response properties onto those
defaults, so neither presence nor wire types follow from the model. The list
view takes `enabled`, `lastUse`, and `from` from get results, and the password
UI checks `from === "iam"`; treat `iam` and `idp` as observed discriminators,
not a complete enum. MFA fields carry email, SMS, and Google authenticator state
with contact values; omit contact values and `idpId` from public outputs, keep
their presence in sanitized raw fixtures, and see the [administration state
model](iam-users-admin-api.md#state-model) for the factor booleans. The console
username validator allows 5 to 50 ASCII letters, digits, underscores, hyphens,
or dots; its password validator is the regex below, whose special class
includes ASCII space and whose final dot restricts no other character.

```text
^(?=.*[A-Z])(?=.*[a-z])(?=.*[0-9])(?=.*[ !"#$%&'()*+,-./:;<=>?@\[\]\\^_`{|}~]).{8,50}$
```

## SDK

All methods use `context.Context`, pointer Input, and `(*Output, error)` in
`danny.vn/vngcloud/iam`, with `iam.<Method>` operation names. Required fields
use `vngcloud:"required"`; nil required inputs fail locally.

### Reads

- `GetUserInput { UserID string }`, required and checked by `core.CheckPathID`;
  `GetUserOutput { User UserDetail }`. The request sets `Sensitive` until the
  probe shows that a user object carries no password-like key.
- `ListUsersInput` retains `Page int` and `Size int` and adds optional `Name
  string`; `ListUsersOutput` remains `core.List[User]`.
- `UserDetail` embeds `User` and adds `Enabled bool` and `From string` with
  API JSON tags. `LastUse` waits for raw evidence and design review, never a
  console-default guess; that gate blocks only that field.

Public `ListUsers` still returns one page and no metadata. Page <= 0 sends 0;
Size <= 0 sends the existing default, 10000. Send `name` only when non-empty,
query-encoded without trimming or case normalization. Do not promise exact
filtering. Input shape bounds for `Name` match Username below. Existing keyed
literals and methods retain their behavior except the proposed body cap; adding
a field breaks unkeyed struct literals, a source-compatibility limit for the
release notes. A private list helper must retain presence-aware metadata for
create confirmation, since the tolerant `ListUsersOutput` cannot prove
completeness. A guard target read requires a usable, matching ID and exact
`from: "iam"`, never zero-value model defaults.

### Generation

When Password is empty, generate `PasswordLength` ASCII characters with
`crypto/rand` from 26 uppercase letters, 26 lowercase letters, 10 digits, and
`@` (63 characters). Zero means 32; outside 8 to 50 is `ErrInvalidInput` before
requests. `@` is the only special character under [decision
3](iam-users.md#owner-decisions); the console validator accepts every generated
candidate, but custom server policy can still reject one.

For unbiased draws, accept random bytes below 252, map each modulo 63, and
discard 252 to 255. Reject a whole candidate unless all four classes occur; do
not repair characters or fix class positions. Stop after 256 candidates or 64
KiB of random bytes consumed, whichever comes first, counting discarded bytes.
Check cancellation between bounded draws. Entropy errors, exhaustion, and
cancellation send no write; errors contain no candidate bytes. Inject randomness
only through private test seams; numeric bounds do not make a blocking reader
interruptible.

Count valid strings of length n by inclusion-exclusion over class sizes (26,
26, 10, 1): `sum((-1)^|S| * (63-sum(S))^n)`; entropy is `log2(count)`: 44.169
bits at 8, 189.947 at 32, 298.003 at 50, with acceptance 7.971%, 39.890%, and
55.057%. 256 length-8 rejections have probability about `5.82e-10` under
independent uniform draws. Create and reset share this generator, never the
console's `Math.random`; the minimum length lacks the default's entropy.

### Writes

- `CreateUserInput { Username string; Password vngcloud.Secret; PasswordLength
  int }`. Username is required. Empty Password selects generation with
  PasswordLength; a non-zero PasswordLength with a supplied Password is
  `ErrInvalidInput`. An empty supplied CLI file fails earlier.
- `CreateUserOutput { User UserDetail; Password vngcloud.Secret; CreatedID
  string; CandidateID string }`. Password holds only a locally generated value.
  CreatedID holds only a valid ID from this invocation's 201 response, never
  one found by list or get; CandidateID holds the single exact-name match found
  after a 201 without an ID and never proves ownership.
- `DeleteUserInput { UserID string }`, required and path-validated;
  `DeleteUserOutput {}`. No partial update fields or guard override exist.

Validate before authentication or requests: Username is non-empty valid UTF-8,
at most 4096 bytes, with no whitespace or controls; a supplied Password is valid
UTF-8, at most 4096 bytes, with no controls, spaces preserved. Never quote
invalid input; the server owns length, class, uniqueness, and policy checks;
never mutate the caller's Input while generating or clearing values.

Create uses `DoJSONStatus`, `Once`, a per-call `NotSent`, `Sensitive`, `Redact`
with the submitted password, `WithholdMessage` with fixed text, and a
`ClassifyError` returning fixed status-based codes, never raw server codes.
Serialize the password in a private wire struct; marshaling Secret directly
sends `[redacted]`. Private confirmation requests use the same controls.

| Create result | Output and error |
|-|-|
| Validation, entropy, or `NotSent` failure | Nil output; safe local cause |
| Definite 4xx rejection | Nil output; sanitized API error |
| 201 with a valid ID and confirmed matching user | Output and nil error |
| 201 with a valid ID but failed confirmation | Partial; unconfirmed |
| 201 without a valid ID: missing, empty, invalid, non-string, or credential-bearing | Partial; uncertain, with any CandidateID |
| Any other possibly sent failure | Partial; uncertain |

Use the observed status, not a nested provider error's status, to identify a
definite rejection. A 4xx with body overflow still has its status and is
rejected; lost status, unexpected 2xx/3xx, 5xx, decode/read failure without a
reliable 201, and cancellation after a possible send are uncertain. `NotSent`
wins before status classification. `Retryable` alone does not prove no send,
particularly with a custom RoundTripper. The transport today drops the
observed status when the body read or close fails, except on overflow
(`internal/transport/transport.go:526`), so such a 201 or 4xx is uncertain
until the separately briefed observed-status prerequisite lands; afterwards
such a 201 is one without a valid ID and such a 4xx stays rejected.

Partial output retains requested Username and generated Password; CreatedID
comes only from a complete valid 201 object and survives confirmation failure.
Use existing `ErrCreateUnconfirmed` only for a 201 with a valid ID whose
confirmation reads fail, generalizing its service-account text without changing
its identity or behavior, and new `ErrUserCreateUncertain` for every other
201 and every unknown outcome. Neither triggers a second POST or an SDK delete.

After a 201 with a valid string ID, validate that ID and read it once. Require
returned ID to match case-insensitively, username to match exactly, and `from`
to equal `iam`. An invalid, non-string, or credential-bearing ID is uncertain
with no CandidateID and no search fallback. Only an empty body, or an object
with an absent or empty ID, permits one filtered list request, page 0, size
1000. Require non-null data and all paging keys, page
0, positive pageSize <= 1000, totalItems equal to data length, length <=
pageSize, and totalPages consistent with a complete single page (0 or 1 for
empty, 1 otherwise). Reject duplicate IDs, more than one exact username,
substring-only matches, or an invalid match ID. Read that one match by ID and
verify its identity and `from: "iam"`. That match is current state, not creation
ownership: another actor can delete the new user and recreate the name
meanwhile. The result is always `ErrUserCreateUncertain` with CreatedID empty
and the match in CandidateID, never a nil error; the error says the password's
owner is unproven and that the caller must establish ownership with other
administrators before trusting the password, resetting the candidate, or
deleting it by that ID. No match, incomplete metadata, or a malformed body is
the same outcome without CandidateID.

Private confirms suppress capture and unsafe error text, and returned strings
are validated against their recorded schema and the submitted identity before
publication. Give the create and each confirmation request its own per-call
`SentCredentials` slice, fold each into an operation-local set after the call,
and clear the set when the operation returns. Refuse an identity, ID, username,
or other response string that contains the submitted password or any collected
credential: return the unconfirmed or uncertain outcome with a fixed cause,
retain nothing from it, and send no follow-up request with it. Never redact an
ID and then use the altered ID for a request. Sanitize every other retained
text against both sets with `transport.RedactValues`. A successful read never
supplies a password.

Delete runs the [ordered guards](iam-users.md#guards), then sends DELETE with
`OK: 204`, a 64 KiB response cap, and `NoRedirect`. Keep the shared idempotent
retry policy. Return an empty Output only on 204. A target 404 returns nil and
`ErrNotFound`; 409 remains Conflict; every exhausted failure remains an error.
Wrap guard dependency errors with a fixed read label without losing error
identity. The caller identity and target reads decode under the strict rule
below.

Use `errors.Is` for `ErrInvalidInput`, `ErrSelfChange`, `ErrPrivilegedChange`,
the new `ErrOriginNotIAM`, create outcome sentinels, and context
cancellation/deadline errors. Keep sanitized `*vngcloud.APIError` reachable
through `errors.As`, including status and Retryable; preserve safe underlying
causes. Map 401 to `ErrAuth`, 403 to `ErrPermission`, 404 to `ErrNotFound`, and
429 to `ErrRateLimited` through the existing core mapping.

### Bounds and strict decoding

New create/delete responses cap at 64 KiB, get at 256 KiB, and lists at 4 MiB;
fail on overflow and never decode a truncated prefix. Confirmation performs at
most one list and one get, or one get with CreatedID, with no polling. All
requests use the caller's context; cancellation stops further requests.

Strict rule: a request sets `transport.Request.StrictJSON`, and `doJSONStatus`
then runs `jsonresponse.ValidateStrict(body, out)` before its own
`json.Unmarshal`, inside the same error path, so a validation or typed decode
failure returns the existing sanitized decode error, or the withheld form under
`Sensitive`; typed decoding never moves out of the transport, so
`sanitizeDecodeError` keeps discarding every cause but fixed `encoding/json`
kinds. `ValidateStrict` walks the body beside the target's static type and
rejects malformed JSON, trailing data, an exact duplicate key in any object,
and, only for an object decoded into a struct, two keys that `encoding/json`
would assign to one field by its case-insensitive match. Maps, interfaces, raw
messages, and non-struct kinds get exact-duplicate rejection only, so distinct
case-sensitive `Condition` map keys still decode. `Validate` stays unchanged.

Reads and deletes retain configured transport retries: retryCount + 1 attempts
per send, the existing single 401 refresh send, and at most 30 seconds per
backoff. The HTTP client timeout bounds each request; a multi-request operation
needs a caller deadline. Create remains one attempt.

Set `StrictJSON` on the user get, the filtered list, the create response, and
`GetCallerIdentity`, the existing read every guarded user write needs, which
today omits `MaxBody` and the strict rule. Before this feature's SDK work, one
separately briefed and reviewed prerequisite adds `StrictJSON` to
`internal/transport`, `ValidateStrict` to `internal/jsonresponse`, and both
flags with a 256 KiB cap to `GetCallerIdentity` in `iam/caller.go`. If the
[administration privilege guard](iam-users-admin.md#owner-decisions) is
accepted, the same prerequisite extends to `ListActions`, `GetPolicy`,
`guardPagedPolicySummaries`, `guardFetchAttachmentArray`,
`guardGetGroupAttachments`, and `guardUserGroups` in `iam/actions.go`,
`iam/policies.go`, and `iam/guard.go`: 256 KiB for single objects, 4 MiB for
lists, preserving their fail-closed classification and error identities. An
oversized or ambiguous `userinfo` response will begin failing for every guarded
write; proposed decision 9 covers this change; a capped get bounds only itself.

## Unit tests

Use synthetic canaries and `httptest`, never real credentials or live calls.
Each operation tests its exact path, body/query, success status, and every error
in the [calls table](#calls), plus unexpected responses.

- Decode manager-sanitized raw get/list/create fixtures through the SDK,
  including the empty-bytes create. Test omitted, null, wrong-type, and
  duplicate JSON identity/metadata keys.
- Check generator alphabet, default and chosen lengths, the 8 and 50 bounds, all
  four classes, unbiased sampling, fresh values, entropy failure, cancellation,
  bounded exhaustion, and the supplied-plus-length refusal; supplied-password
  shape and immutable Input.
- Count create POSTs for 401, 429, 500/502, all redirects, partial bodies, body
  overflow, dropped connections, and cancellation before/after send. Distinguish
  proven NotSent from custom-transport dial-shaped errors.
- Test every outcome-table branch with generated and supplied passwords, both
  sentinels via `errors.Is`, API errors via `errors.As`, and context identities.
  Test 201 with empty body, null/array/malformed JSON, missing, invalid, or
  mismatched ID, failed confirmation, and duplicate matches: only a valid ID
  with failed confirmation is `ErrCreateUnconfirmed`; a missing, empty,
  invalid, non-string (`{"id":17}`), or credential-bearing ID and a name-only
  match are `ErrUserCreateUncertain`, CandidateID only for the last, never a
  nil error. A 201 `id`, or a list or get identity after a 401 refresh, that
  equals a sent bearer token is refused with no follow-up and no token in any
  error node, output, or CLI line.
- Exercise missing/null/negative/inconsistent list metadata, multi-page results,
  substring and case-only names, wrong get identity, caps, and public ListUsers
  compatibility without Name. Verify CreatedID provenance.
- Test self before the target read; unknown/root caller; missing caller ID;
  service-account caller; non-iam, missing, null, and wrong-type origin;
  mismatched target ID; failed target read. Assert zero mutations on refusal,
  not zero reads. An attached user is not refused; its 409 stays `Conflict`.
- Test DELETE 204, 404, 409, retries ending in 404, exhausted 5xx, no redirects,
  cancellation, and dependency 404 labels. Send zero DELETEs for a duplicate
  `userId` or `userType`, or a `USERID` alias after the real key, in the caller
  identity, and for a duplicate `id`, `username`, or `from` in the target user.
- Put a canary in a `$numberLong` timestamp, a `root`, and a malformed strict
  body on every strict read, and check that no SDK error node, `APIError` field,
  or CLI stderr line carries it. Canary-check stdout, stderr, debug, capture,
  every reachable error node, and supported fmt/slog/JSON paths, including
  nested escaped password echoes in error codes/messages and success bodies;
  unsafe formatting is forbidden, not claimed safe. Confirm requests serialize
  the actual value.
- CLI tests cover exactly-one flag, empty file, private file mode, symlinks,
  FIFO/device/directory refusal, terminal stdin, CRLF/spaces, overflow, stalled
  input deadline, and cancellation. `beforeInput` runs before any
  `--cli-input-json` read: `--password-file -` with `file://` JSON exits 2 while
  stdin is a never-closing pipe; a FIFO at `file://` exits 2 for any command;
  read-only refuses before any stdin read, proven by a stdin that fails the test
  on `Read`; cancellation during a stalled stdin exits within the deadline with
  no request. Delete without `--yes` sends nothing.
- Inject file open/write/sync/close/removal failures. Check exclusive 0600
  creation, existing-file preservation, saved passwords on partial output, no
  request of any kind after a file failure, the `CreatedID` or username and the
  `delete-user` command in the message, and exit precedence.

## Probes

Only the manager may run probes, after owner approval naming the account,
resources, sign-in scope, cost assumption, and cleanup authority, and after a
separate adversarial review with fix confirmation of the credential-free probe
source, which handles a password and creates and deletes a principal. IAM is
global. Serialize the probe with other IAM writers in the test account. Use
`vngcloud-probe-<8 hex>` names, precheck absence, and keep a private per-run
ledger of returned IDs. A prefix alone never authorizes cleanup. Do not alter
policy or account security settings to make the probe pass.

1. Verify permissions and quota headroom using `quotas/iam-user`. Read the
   caller and its own user only if expressly approved; record get keys, types,
   origin, timestamp forms, and missing/null fields.
2. Create one disposable user with a generated password using the same alphabet.
   Hold response bytes only in memory; record status, key names, types, and
   whether an ID exists. Keep the password until cleanup.
3. List by full disposable username, a substring, a case variation, and a
   definitely absent random name; record metadata and filter semantics. Get the
   disposable user and verify ID, origin, model fields, and, through the
   policies endpoint, empty direct policies and empty groups. Run only
   separately authorized [administration probes](iam-users-admin-api.md#probes)
   here; missing administration evidence does not block the base create/delete.
4. Try the same name once more only if approved; record status and safe code. A
   409 establishes this duplicate case only. Skip short-name and weak-password
   negatives, which custom policy may accept while creating extra identities;
   offline tests cover validation.
5. If separately approved, sign in as the disposable user through the existing
   IAM login, without a persistent token cache, then read caller identity and
   compare IDs in memory. Record booleans only. MFA or sign-in refusal is a
   gate, not permission to disable account controls.
6. Delete only the attributable disposable ID. Record status, repeat-delete
   status, target-get status, and quota restoration. If no create ID exists,
   obtain specific manager/owner reconciliation authority for the discovered ID
   before deletion. Stop and report leftovers privately on ambiguity.

No probe deletes the caller, an attached user, or a pre-existing user. Delete
409 meaning, custom policy breadth, concurrent races, and token revocation after
deletion stay unverified; a read failure or name mismatch never widens cleanup.

### Fixture provenance

Keep account-bearing raw reads only under the private output paths in [live
data](../../instructions/live-data.md). Sanitize create bytes in memory; if they
contain a password, omit that fixture and keep the schema gate. Fixtures are
`get_user.json`, `list_users_filtered.json`, and, if safe, `create_user.json`
under `testdata/iam/`, with method/path/status, raw provenance, replacements,
and model omissions. Use `<id>`, `<account>`, and `<secret>` or short synthetic
IDs that pass path validation; retain structural keys and types; record an
empty body as such. The manager checks sanitation before workers see fixtures.

## Live checks

Add GetUser and filtered ListUsers to the authorized read suite. Gate
`TestLiveWriteIAMUser` on both `VNGCLOUD_LIVE_WRITE=1` and
`VNGCLOUD_LIVE_IAM_USER=1`. Use a unique `vngcloud-live-<8 hex>` name and only
resources recorded by this run; never sweep all prefix users. Exercise
generation, create, private temporary secret-file persistence, get, filtered
list, self refusal with no DELETE, guarded delete, and only separately opted-in
[administration live checks](iam-users-admin-api.md#live-checks) on the same
user. Register cleanup before create using a ledger, never a name-based delete;
on uncertain ownership, report a leftover and stop. Remove temporary secrets on
completion and emit only fixed labels, statuses, counts, and booleans.
