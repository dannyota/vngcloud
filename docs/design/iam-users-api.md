# IAM users: API and checks

Wire evidence and proposed SDK contracts for [IAM users](iam-users.md).
[Existing IAM API evidence](iam-writes-api.md) owns endpoints and shared
attachment calls; [IAM users: checks](iam-users-checks.md) owns unit tests,
probe evidence, fixtures, and live checks. The reviewed probe on the owner's
test account recorded the create, get, list, duplicate, delete, and quota
shapes: [fixture provenance](../../testdata/iam/user-provenance.md) summarizes
the raw evidence and [probes](iam-users-checks.md#probes) records what the
runs closed and left open.

## Sources

- The probe runs: statuses, key names, JSON types, booleans, and body lengths
  from two runs that each created, exercised, and deleted one disposable user,
  rebuilt as synthetic fixtures under `testdata/iam/`. Server evidence for the
  Dashboard endpoint, the test account, and an IAM-user caller only.
- [Accounts OpenAPI][spec], version 1.0.0: paths, request properties, and the
  documented statuses below. Its server is
  `https://iamapis.vngcloud.vn/accounts-api`; the SDK uses its existing
  Dashboard endpoint, which the probe exercised.
- [Console main bundle][main]: `getListUserAccounts`, `getUserById`,
  `createUser`, `deleteUAById`, `editUsername` (the PUT rename; see
  [administration evidence](iam-users-admin-api.md#sources)), model defaults,
  and validators. [User-account chunk][users]: `createUsers` sends username
  and password and discards the response. [Account-settings chunk][settings]:
  custom password controls. [Quota chunk][quota]: `quotas/iam-user`. Read as
  text, never executed; not server evidence.
- Product pages [User Accounts][product-users], [Create an IAM User
  Account][product-create], and [Delete an IAM User Account][product-delete]:
  denied-by-default creation, password/name rules, name uniqueness, and
  irreversible deletion, as product claims, not server behavior.

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
The spec documents the statuses below and no error bodies or 409 meanings; the
last column holds what the probe observed.

| Operation and path | Success | Documented errors | Observed |
|-|-|-|-|
| `POST iam-users` | 201 | 400, 401, 403, 409, 500 | 201; 400 for a duplicate username |
| `GET iam-users/{id}` | 200 | 401, 403, 404, 500 | 200; 404 after delete |
| `DELETE iam-users/{id}` | 204 | 401, 403, 404, 409, 500 | 204; 404 on repeat |
| `GET iam-users` | 200 | 401, 403, 500 | 200 with and without `name` |

The list spec requires `pageSize` and zero-based `pageNumber`. Only the console
supplies `name` and optional `from`. The probe showed `name` matching a
case-insensitive substring and an absent name returning `data: []` with
`totalItems` 0. Expose only `Name`; do not use `from=iam` to hide unclassified
targets from guards. `GET quotas/iam-user` returns `{"usage": <number>,
"limit": <number>}` (limit 20 on the probe account, usage restored after each
run) and serves probes and live checks, not a new SDK method.

## Bodies and responses

Create sends `{"username":"<account>","password":"<secret>"}`. The spec requires
the body, not either property; the console and SDK require both values. The
console generates passwords locally; the request carries no switch, group,
policy, console-access, or forced-change field, and a new user had an empty
direct-policy page (`totalItems` 0) and a bare `[]` group list.

The spec publishes no 201 schema. Both probe runs returned a 45-byte object
with one key, `id`, holding a 36-character UUID (`create_user.json`); the
empty, absent-ID, and malformed branches below defend against a server change.
DELETE's 204 has no body; a repeat DELETE and a GET of the deleted ID return
404. A second create with an existing username returned 400, not 409: a
definite rejection whose fixed status-based code under [decision
10](iam-users.md#owner-decisions) cannot be told from any other 400;
`list-users --name` shows the holder. 409 stays documented with no observed
meaning: a create 409 may be a quota or another conflict, and a delete 409 is
the server's refusal, for attachments or anything else; never translate it
into success or an automatic detach.

A get returns `createdAt` (epoch-millisecond number), `enabled` (boolean,
`true` on a new user), `from` (`iam`), `id`, `username`, and `twoFactorAuth`,
whose factors the [administration state
model](iam-users-admin-api.md#state-model) records. `lastUse` is absent until
the user's first sign-in and then an epoch-millisecond number
(`get_user.json`, then `get_user_mfa.json`). No key holds a password. The
console model also names `idpId`, absent from an `iam` user; `iam` and `idp`
are console-observed discriminators, not a complete enum. Omit contact values
and `idpId` from public outputs.

List fields are `data`, `pageNumber`, `pageSize`, `totalItems`, `totalPages`;
rows have `id`, `username`, and `createdAt` as an RFC 3339 string with
milliseconds and `Z` (`list_users_filtered.json`), so one timestamp has two
wire forms across the two reads. Metadata is not required by the spec;
completeness is a local confirmation rule. The console username validator
allows 5 to 50 ASCII letters, digits, underscores, hyphens, or dots; its
password validator is the regex below, whose special class includes ASCII
space and whose final dot restricts no other character.

```text
^(?=.*[A-Z])(?=.*[a-z])(?=.*[0-9])(?=.*[ !"#$%&'()*+,-./:;<=>?@\[\]\\^_`{|}~]).{8,50}$
```

## SDK

All methods use `context.Context`, pointer Input, and `(*Output, error)` in
`danny.vn/vngcloud/iam`, with `iam.<Method>` operation names. Required fields
use `vngcloud:"required"`; nil required inputs fail locally.

### Reads

- `GetUserInput { UserID string }`, required and checked by `core.CheckPathID`;
  `GetUserOutput { User UserDetail }`. The request sets `Sensitive`: the probe
  found no password-like key, but an enrolled user's factors carry contact
  values, and guard and confirmation reads share this request.
- `ListUsersInput` retains `Page int` and `Size int` and adds optional `Name
  string`; `ListUsersOutput` remains `core.List[User]`, and `User.CreatedAt`
  stays the evidenced RFC 3339 string.
- `UserDetail { ID string; Username string; CreatedAt int64; Enabled bool;
  From string; LastUse int64 }` with API JSON tags and no custom
  `UnmarshalJSON`. It does not embed `User`, whose string `CreatedAt` would
  shadow the number the get returns. The get decodes a private wire struct
  whose `CreatedAt` and `LastUse` are `epochMillis` (`iam/timestamp.go`), like
  `guardSummary` in `iam/guard.go`: each accepts the number or an RFC 3339
  string, and `LastUse` is 0 when absent, as for `ServiceAccount`.
  `ValidateStrict` skips case-alias detection inside any type with a custom
  unmarshaler, so the wire struct keeps that check for `id` and `from`. The
  [MFA field](iam-users-admin-api.md#state-model) is added by its own slice.

Public `ListUsers` still returns one page and no metadata. Page <= 0 sends 0;
Size <= 0 sends the existing default, 10000. Send `name` only when non-empty,
query-encoded without trimming or case normalization; document the observed
substring match without promising it. Input shape bounds for `Name` match
Username below. Existing keyed literals and methods retain their behavior
except the proposed body cap; adding a field breaks unkeyed struct literals, a
source-compatibility limit for the release notes. A private list helper must
retain presence-aware metadata for create confirmation, since the tolerant
`ListUsersOutput` cannot prove completeness. A guard target read requires a
usable, matching ID and exact `from: "iam"`, never zero-value model defaults.

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

Create uses `DoJSONStatus`, `Once`, a per-call `NotSent`, `StrictJSON` with
`AllowEmptyBody` and a per-call `BodyWasEmpty`, `KeepObservedStatus`,
`Sensitive`, `Redact` with the submitted password, `WithholdMessage` with
fixed text, and a `ClassifyError` returning fixed status-based codes, never raw
server codes. A private wire struct serializes the password; marshaling Secret
directly sends `[redacted]`. Private confirmations use the same controls.

| Create result | Output and error |
|-|-|
| Validation, entropy, or `NotSent` failure | Nil output; safe local cause |
| Definite 4xx rejection, including the duplicate 400 | Nil output; sanitized API error |
| 201 with a valid ID and confirmed matching user | Output and nil error |
| 201 with a valid ID but failed confirmation | Partial; unconfirmed |
| 201 without a valid ID: missing, empty, invalid, non-string, or credential-bearing | Partial; uncertain, with any CandidateID |
| Any other possibly sent failure | Partial; uncertain |

Use the observed status, not a nested provider error's status, to identify a
definite rejection. A 4xx with body overflow still has its status and is
rejected; lost status, unexpected 2xx/3xx, 5xx, decode/read failure without a
reliable 201, and cancellation after a possible send are uncertain. `NotSent`
wins before status classification. `Retryable` alone does not prove no send,
particularly with a custom RoundTripper. The transport keeps the observed
status through a body read or close failure only for a request that sets
`KeepObservedStatus` (`internal/transport/body.go:38`); the create sets it, so
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
with no CandidateID and no search fallback. Only a zero-length body, reported
through the [empty-body option](#bounds-and-strict-decoding), or a valid
object with an absent or empty ID permits one filtered list request, page 0,
size 1000; a strict or decode failure permits none. Require non-null data and
all paging keys, page 0, positive pageSize <= 1000, totalItems equal to data
length, length <= pageSize, and totalPages consistent with a complete single
page (0 or 1 for empty, 1 otherwise). Reject duplicate IDs, more than one exact
username, substring-only matches, which the observed filter returns, or an
invalid match ID. Read that one match by ID and verify its identity and `from:
"iam"`. That match is current state, not creation ownership: another actor can
delete the new user and recreate the name meanwhile. The result is always
`ErrUserCreateUncertain` with CreatedID empty and the match in CandidateID,
never a nil error; the error says the password's owner is unproven and the
caller must establish ownership with other administrators before trusting the
password, resetting the candidate, or deleting it by that ID. No match,
incomplete metadata, or a malformed body is the same outcome without
CandidateID.

Private confirms suppress capture and unsafe error text, and returned strings
are validated against their recorded schema and the submitted identity before
publication. Give the create and each confirmation request its own per-call
`SentCredentials` slice, fold each into an operation-local set after the call,
and clear the set when the operation returns. Refuse an identity, ID, username,
or other response string that contains the submitted password or any collected
credential: return the unconfirmed or uncertain outcome with a fixed cause,
retain nothing from it, and send no follow-up request with it. Never redact an
ID and then use the altered ID for a request. Sanitize every other retained
text against both sets with `transport.RedactValues`.

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

Strict rule: the user get, the filtered list, and the create response set the
existing `transport.Request.StrictJSON`, so `doJSONStatus` runs
`jsonresponse.ValidateStrict(body, out)` before its own `json.Unmarshal`
inside the same sanitized error path, including the withheld form under
`Sensitive`. [IAM writes: API](iam-writes-api.md#bodies) records what
`ValidateStrict` rejects (malformed JSON, trailing data, an empty body, exact
duplicate keys at any depth, and struct-field case aliases, with map keys
exact-duplicate only) and that every read a guard decision depends on,
including `GetCallerIdentity` with its 256 KiB cap, already decodes this way.
Unknown keys pass, so the withheld factor key does not disturb decoding. A
capped get bounds only itself; the private confirmation decoders add their
presence and identity checks on top.

Empty-body option: under `Sensitive`, `ValidateStrict`'s rejection of a
zero-length body is the same withheld error as malformed JSON, so the create
request also sets `transport.Request.AllowEmptyBody` with a per-call
`BodyWasEmpty *bool` (`internal/transport/body.go:9`): with `StrictJSON` set,
a success status whose body is zero bytes skips validation and decoding,
leaves the target untouched, and sets the flag; any other body, including
whitespace only, `null`, or an array, follows the strict sanitized path.
Guard and confirmation reads never set the option, so they keep rejecting
empty bodies; `{}` stays a decoded object with an absent ID.

Reads and deletes retain configured transport retries: retryCount + 1 attempts
per send, the existing single 401 refresh send, and at most 30 seconds per
backoff. The HTTP client timeout bounds each request; a multi-request operation
needs a caller deadline. Create remains one attempt.
