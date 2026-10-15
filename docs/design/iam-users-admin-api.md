# IAM user administration: API and checks

Wire evidence and proposed contracts for [IAM user
administration](iam-users-admin.md). [IAM users: API](iam-users-api.md) owns
get, filtered list, generation, bounds, and strict decoding. The reviewed probe
on the owner's test account verified reset acceptance, the no-factor MFA
refusals, the rename refusal for an IAM-user caller, and the target's factor
shape ([fixture provenance](../../testdata/iam/user-provenance.md)).
Verified-factor MFA transitions and rename acceptance remain unverified.
Source evidence does not close a raw-response gate or authorize a probe.

## Sources

- [Accounts OpenAPI][spec], version 1.0.0: the saved and fetched embedded
  specifications match. It publishes reset/MFA paths, request schemas, statuses,
  and caller `auth/userinfo.twoFactorAuth`. It publishes no success schema for
  the administrator operations, error schema, or 409 meaning. Only GET and
  DELETE are published on `/v1/iam-users/{id}`.
- [Console main bundle][main]: `resetUserPassword` posts `{password}`;
  `editUsername` uses PUT on `/v1/iam-users/{id}` with `{username}`. `toggle2FA`
  chooses `activate-` or `deactivate-` plus the factor name and posts `{}`.
  No PATCH user rename is evidenced.
- The probe runs, recorded under [calls](#calls), [state model](#state-model),
  and [probes](#probes): statuses, body lengths, key names, JSON types, and
  booleans for one IAM-user caller on the test account, with every password
  and body held in memory only.
- [User-account chunk][users]: administrator `setupGoogleAuth` consumes `secret`
  and `qrCodeUrl` from `setupGoogleAuth2FA`. Treat both as secret material; do
  not fetch or render a QR URL in a probe. The console service also names
  `/v1/iam-users/verify-google` for current-user verification; the OpenAPI omits
  that path. Its `{id}/verify-google` summary incorrectly says "Current User".
  Preserve this discrepancy; neither variant ships.

Bundles were read as text, never executed. Their model defaults do not prove
wire presence. The spec's server is `https://iamapis.vngcloud.vn/accounts-api`;
the SDK uses its existing Dashboard endpoint, which the probe exercised.

[spec]: https://docs.api.greennode.ai/service-docs/accounts-api.html
[main]: https://iam.console.greennode.ai/main.a0cfd100731832eb.js
[users]: https://iam.console.greennode.ai/718.16d2630147e1e633.js

## Calls

All listed methods are POST relative to `accounts-api/v1/iam-users/{id}/` unless
stated otherwise. Bearer authentication is required. Every reset and MFA path
below documents errors **400, 401, 403, 404, 409, 500**, including bodyless
paths. The spec marks request bodies required where present but no body property
required. The SDK requires a non-empty replacement password.

| Suffix | Published body | Success | Observed | Use |
|-|-|-|-|-|
| `reset-password` | `{"password": string}` | 204 | 204, empty body | Reset |
| `activate-email` | None | 204 | 400 with no factor | Enable email |
| `deactivate-email` | None | 204 | 400 with no factor | Disable email |
| `activate-google` | None | 204 | 400 with no factor | Enable Google |
| `deactivate-google` | None | 204 | 400 with no factor | Disable Google |
| `set-up-email` | `{"email": string}` (email format) | 204 | Not probed | Excluded |
| `send-verification-email` | None | 204 | Not probed | Excluded |
| `verify-email` | `{"token": string}` (six digits) | 204 | Not probed | Excluded |
| `set-up-google` | None | 200; schema absent | Not probed | Excluded |
| `verify-google` | `{"token": string}` (six digits) | 204 | Not probed | Excluded |

The token pattern is `^\d{6}$`. Current-user reset and MFA paths remove the
`{id}/` segment and publish the same statuses/bodies, except `verify-google` is
absent as noted above. Account `activate` and `deactivate` are separate excluded
paths: 204, errors 401/403/404/409/500, no documented 400.

Reset returned 204 with a zero-byte body for passwords of 32, 8, and 50
characters; a fresh sign-in with the new password succeeded and one with the
old password failed. The four activate and deactivate paths each returned 400
for a user with no factor set up: an unenrolled factor is a definite 4xx
rejection, not a 204 no-op. MFA set-up, verification, and activation order is
still inferred from path names and console state changes. Set-up can replace
enrolment; do not treat it as a harmless read. Whether activation requires
verification, deactivation preserves it, or an already-in-state request
returns 204 or 409 is unknown. Conflict always remains `Conflict`. The proposed
SDK sends no MFA body, following the spec; the console sends `{}`, a
difference the no-factor 400 does not settle.

Rename: `PUT accounts-api/v1/iam-users/{id}` with exactly
`{"username":"<account>"}` from `editUsername` returned 403 to the IAM-user
caller, with a top-level array of one object holding one string field. The
account's IAM action list names no update or rename action ([fixture
provenance](../../testdata/iam/user-provenance.md) lists the user actions).
The probe caller, an IAM user, was refused; whether other callers can rename
is unknown. The success status, body handling, uniqueness behavior, and
same-ID preservation stay unevidenced.

## State model

A user get carries `twoFactorAuth` with three factor objects
(`testdata/iam/get_user.json` and `get_user_mfa.json`): `email` has `address`
(null), `enabled`, and `verified`; `googleAuthenticator` has `enabled` and
`verified`; `sms` has `enabled`, `verified`, and one more null-valued key that
the provenance withholds by name and the fixtures omit. Every boolean was
`false` on a new user with no factor set up; no `true` boolean or non-null
contact value has been observed. The caller `auth/userinfo` fixture holds
`twoFactorAuth` as a boolean, so the caller decoder is not reused for the
target. Strict decoding passes unknown keys, so the withheld key is harmless.

Proposed additions, shipped by the MFA slice beside base `UserDetail`:

- `MFAFactor { Verified *bool; Enabled *bool }`, with matching API tags.
- `UserMFA { Email *MFAFactor; GoogleAuthenticator *MFAFactor }`.
- `UserDetail.MFA *UserMFA` with `json:"twoFactorAuth,omitempty"`. Nil means
  absent or null, never "disabled" or "not enrolled". The user get's private
  wire struct carries the factors, under the [wire-struct
  rule](iam-users-api.md#reads), so case-alias detection covers them.
- Omit address, SMS, TOTP secrets, and QR data. Keep safe structural presence in
  sanitized raw fixtures; contact values become `<account>`.

Public output preserves missing/null booleans as nil, a defense against a
server change, since the observed get carried both. A private state reader
requires matching valid ID, `from: "iam"`, the selected factor, and explicit
booleans before a mutation or no-op. Wrong types or ambiguous keys fail closed.
Missing/null fields give a fixed `RequestFailed` state-unavailable error, with
no new SDK sentinel and no POST. Do not infer enrolment from absence. Enable
requires `Verified == true` before checking `Enabled`; false gives a fixed state
refusal, not `ErrInvalidInput`. Disable requires known state but not
verification. Both still run all caller and privilege guards first.

## SDK

All methods use `context.Context`, pointer Input, `(*Output, error)`, and
`iam.<Method>` operation names in `danny.vn/vngcloud/iam`. Nil Inputs fail
locally. UserID and required names/methods use `vngcloud:"required"`;
`core.CheckPathID` validates UserID before authentication or requests. There are
no partial update fields or public guard overrides. Public additions preserve
existing methods/import paths; `UserDetail` and MFA types are new.

### Reset

- `ResetUserPasswordInput { UserID string; Password vngcloud.Secret;
  PasswordLength int }`. Empty Password selects [generation][generation];
  non-zero length with a supplied Password is `ErrInvalidInput`. Supplied
  Password follows create's shape rules; never mutate the caller's Input.
- `ResetUserPasswordOutput { Password vngcloud.Secret }`. Only a generated
  password is returned, including on accepted or uncertain partial output.

Order: validate; guards with the privacy rules below; generate if needed;
serialize the actual value in a private wire struct; send exactly
`{"password":"<secret>"}`. Set `OK: 204`, `Once`, per-call `NotSent`,
`KeepObservedStatus`, and the privacy controls. The write caps unexpected body
bytes at 64 KiB and decodes no success object; the observed success is a
zero-byte body. No confirmation read or password sign-in follows.

| Result | Output and error |
|-|-|
| Local validation, guard, entropy, or proven `NotSent` failure | Nil; safe cause |
| Observed 4xx rejection, including oversized error body | Nil; safe API error |
| Observed 204 with no transport/body error | Output; nil error |
| Observed 204 with body overflow, read, or close failure | Partial; `ErrNotSettled` |
| Other possibly sent outcome, including a lost status | Partial; `ErrUserWriteUncertain` |

Use the observed HTTP status, never a nested provider status. `NotSent` wins
first. A 204 is acceptance even if response handling fails; preserve that fact
and the generated password. The transport keeps the status through a body read
or close failure only for a request that sets `KeepObservedStatus`
(`internal/transport/body.go:38`); the reset sets it, so that 204 is
`ErrNotSettled` and a 4xx with the same failure stays a rejection. Unexpected
2xx/3xx, 5xx, missing status, dropped connection, and cancellation after
possible send are uncertain. `Retryable` is not proof of no send. Neither the
SDK nor CLI repeats a reset. A success proves acceptance, not that a
concurrent actor has not reset again. The probe showed the old password
refused after acceptance; whether a token issued before the reset survives is
unknown, because that check did not run.

### Reset privacy

This is a separately briefed prerequisite under [decision
9](iam-users-admin.md#owner-decisions), not an existing guard guarantee. Thread
private per-operation request options through every reset-dependent reader:
caller identity, target get, action classification, direct policy summaries,
user groups, and each `GetPolicy` read. Preserve existing guard semantics and
caching; do not change client-global state or expose a bypass.

Every network read in that graph sets `Sensitive`, `WithholdMessage` and fixed
non-empty status-based `ClassifyError`; include supplied Password in `Redact`
before guards and generated Password only after generation. The POST uses the
same controls. No server-controlled error field or raw unwrap cause survives.
Fixed read labels retain safe status, context identity, and guard sentinels.
Captures must be suppressed at transport time, not discarded later by the CLI.
Auth/token providers keep their existing privacy contract; custom
providers/transports are trusted code and cannot be made safe by this wrapper.
No follow-up, cleanup, or diagnostic request follows the POST.

Reset reaches `iam/guard.go:471`, `:186`, `:256`, `:427`, and
`iam/policy_guard.go:269`; those reads currently lack these local options.
Cold-cache and warm-cache tests must prove that options neither leak across
concurrent calls nor weaken normal guards. If this graph cannot carry the
controls safely, reset stays blocked. Sensitive `GetUser` alone is not enough.

### Rename

Blocked: the [PUT](#calls) returned 403 to an IAM-user caller and no IAM
action covers it, so no accepted response contract exists and the [rename
follow-up](iam-users-admin.md#owner-decisions) decides whether rename stays in
scope. Proposed types, if a later root-login probe evidences acceptance:
`RenameUserInput { UserID string; Username string }`, both required, and
`RenameUserOutput { User UserDetail }`. Username follows create's shape rules.
Send the PUT once with `Once` despite PUT's normal retries, only after
evidence/re-review selects its exact `OK` status and response rules. Set the
privacy controls described under shared bounds below.

After evidenced acceptance, read once by ID. Matching ID, exact Username, and
IAM origin confirm. A failed/mismatched read returns an output holding only the
validated input ID and `ErrNotSettled`. A possibly sent failure returns nil and
`ErrUserWriteUncertain`; do not attribute an unconfirmed name to the user. A
definite 4xx retains its classification, including Conflict. Inspect the same ID
manually; no list-based replacement, delete/create, profile rewrite, or
token-cache mutation is allowed.

### MFA

- `DisableUserMFAInput { UserID string; Method string }` and
  `EnableUserMFAInput` with the same required fields. Method must be exactly
  `email` or `google`, checked before any request; never interpolate another
  string into a path. An unevidenced method must not ship as supported.
- `DisableUserMFAOutput { Method string; Enabled *bool; Changed bool }` and
  `EnableUserMFAOutput` with the same fields. Enabled is the last confirmed
  post-operation state, nil when unavailable. Changed means this invocation
  received 204 for a mutation, not exclusive authorship of the final state.

Validate; run all guards; inspect the selected factor in the guard target read.
Already in state returns known Enabled and `Changed: false`, without a send.
Otherwise send the matching path with no body, `OK: 204`, `Once`, per-call
`NotSent`, `KeepObservedStatus`, and privacy controls. Read once after 204;
require the same ID/origin and complete selected-factor state. Enable
confirmation also requires `Verified: true`. A matching state returns
`Changed: true`. The observed no-factor 400 is an ordinary 4xx rejection;
enable never reaches it, since an unverified factor is refused before a send.

Observed 204 with body overflow, a body read or close failure, or an
absent/mismatched confirmation returns `ErrNotSettled`, `Changed: true`, and
nil Enabled if no trustworthy confirmation exists (otherwise the freshly read
Enabled). Never use the pre-write value as a confirmed result. A proven
`NotSent` or observed 4xx returns nil with the safe cause; every other possibly
sent outcome returns nil with `ErrUserWriteUncertain`, without a confirmation
or second send. Manual repetition rechecks state and guards. A 409 is never
converted to success, even when a later operator read finds the requested
state.

### Error identity and bounds

Use `errors.Is` for input/guard/outcome sentinels and context errors; preserve
sanitized `*vngcloud.APIError` via `errors.As`, with status and Retryable. Map
401/403/404/429 through existing Auth/Permission/NotFound/RateLimited mapping;
Conflict keeps its API error code. Label dependency failures with fixed read
names. A failed confirm after acceptance never means the write was refused.
Outcome and file-failure CLI handling precedes nested status mapping and always
exits 1.

All administration sends and target/confirm reads set `Sensitive`, fixed
`WithholdMessage` and non-empty status-based `ClassifyError`. Reset adds its
whole-graph privacy above. Return no arbitrary server text from partial objects;
rename confirmation uses create's identity validation rules. Each guard read,
send, and confirm read gets its own per-call `SentCredentials` slice, folded
into an operation-local set under [create's credential
rule](iam-users-api.md#writes): an ID, username, or factor value that contains
a collected credential or the submitted password is refused before it is
retained, printed, or used in a follow-up request, and every other retained
text is sanitized against both sets. Strict JSON and
byte caps apply to every guard dependency: 256 KiB single objects, 4 MiB lists,
64 KiB write bodies. Do not decode a truncated prefix. A 204 body is ignored,
bounded, and never captured, including with a custom client.

Use the caller's context and configured HTTP timeout; a multi-request call needs
a caller deadline. GETs retain retryCount + 1 attempts, the existing single 401
refresh send, and at most 30 seconds per backoff. Writes send once, refuse
redirects, and perform at most one logical confirmation GET, which may use those
transport retries. No extra paging, polling, waiter, detached context, or
automatic cleanup exists. Guard lists retain fail-closed completeness checks;
byte caps do not prove list completeness.

[generation]: iam-users-api.md#generation

## Unit tests

Use synthetic canaries and `httptest`; no live calls. For each enabled path,
test exact method/path/body, documented success, all 400/401/403/404/409/500
errors, 429, unexpected statuses, and the outcome table. Rename tests wait for
its response evidence, never a guessed success code or fixture.

- Reset: generated/supplied values serialize exactly; nil/invalid Input and
  conflicting length fail before reads; generation runs after guards; test the
  observed empty 204, a 204 with an ignored body, and response failures, fixed
  errors, all error nodes and API fields, redacted output, supplied value never
  returned, no Input mutation, and saved generated passwords on both outcome
  errors.
- Privacy: password/secret canaries on each guard edge, POST, debug, capture,
  nested escaped error codes/messages, and unsafe decode causes. Test cold and
  warm caches, concurrent requests with different redaction values, no retained
  private options, and no request after persistence failure. A target or
  confirm read whose `id` or `username` equals a sent bearer token, including
  the token sent after a 401 refresh, is refused with no send, no follow-up,
  and no token in any output, error node, or CLI line.
- Guards: self before origin/privilege, root/unknown/missing caller ID,
  service-account caller with IAM target, direct/group privilege, unknown action
  labels, empty actions, incomplete attachments, unreadable policy,
  null/wrong-type/non-IAM origin, and mismatched IDs. Every refusal sends zero
  writes, including an already-in-state MFA operation.
- MFA: decode `get_user.json` and `get_user_mfa.json` through the SDK to the
  all-false factors, with an extra synthetic null key inside a factor; nil and
  false are distinct through SDK fixture decoding and JSON output;
  missing/null factor or boolean refuses; enable unverified refuses before
  no-op; disable known enabled/unverified succeeds; duplicates and case aliases
  at every state level refuse. Test stale, missing, wrong-ID, non-IAM, and
  unverified-enable confirmations, Changed/Enabled semantics, and the 400 from
  a send.
- Every send: count requests for 401, 429, 5xx, all redirect statuses, drops,
  body limits, cancellation before/after send, and dial-shaped errors from
  custom transports. No replay, blind rollback, or cloud cleanup occurs. Race
  tests cover competing resets, opposite MFA writes, and changed privilege
  between read/send without claiming atomicity.
- CLI: all four `--yes` rules, read-only before password consumption, JSON
  secret exclusion, one password source, length restrictions, secret-file
  preflight and exclusive write/sync/close/removal failures. Distinguish
  accepted/uncertain and saved/lost, retain outcome identity under a file error,
  and test exit precedence after nested 401/404. No automatic profile/cache
  edits, sign-in, rollback, or reset after file failure.

## Probes

The administration probe ran inside the [user
probe](iam-users-checks.md#probes) on the same disposable user, between its
create and delete, under owner approval naming each reset, MFA, and rename
action and the sign-in scope, after adversarial review and fix confirmation of
the credential-free source. For an IAM-user caller it established:

- Reset: 204 with a zero-byte body for a generated 32-character password and
  supplied 8- and 50-character values satisfying the four classes; a fresh
  sign-in with the new password succeeded and one with the old password
  failed, recorded as booleans with no persistent cache.
- Factor state: the [state model](#state-model) shape on the get before and
  after sign-in, with every boolean `false` and no factor set up.
- MFA without enrolment: `activate-email`, `deactivate-email`,
  `activate-google`, and `deactivate-google` each returned 400.
- Rename: the console PUT returned 403 with a one-object array body, and the
  action list names no update action.

Not run: the pre-reset token check against `auth/userinfo`, because the probe
stops its sign-in checks after the expected old-password refusal, so token
survival after a reset is unknown; email and Google enrolment, because no
mailbox or in-memory Google enrolment was approved, so no verified-factor
transition, verified-state retention, or `{}`-versus-no-body difference is
evidenced; and any rename collision or old-name reuse. The remaining probes
need their own approval under the [probe-scope and rename
follow-ups](iam-users-admin.md#owner-decisions):

1. Email cycle, only with an owner-controlled test mailbox: set up, send mail,
   verify, activate, get, deactivate, get, reactivate, get. Check
   verified-state retention and the no-body POST against the console's `{}`.
   Never print the address or code.
2. Google cycle, only with approved in-memory enrolment: the same transitions
   with the TOTP secret and QR data in memory only; never fetch a returned QR
   URL. If absent, defer that method rather than extrapolate from email.
3. Rename, only through an approved root login on a disposable user: record
   status and body schema, get by ID, and compare exact username and unchanged
   ID. A filtered list can show the old name absent, not prove it reusable.
   Collision, old-name reuse, and token checks need separate approval.

Every repeat keeps the [base probe rules](iam-users-checks.md#probes), holds
secrets in memory, records locally classified codes rather than raw server
text, bounds sign-in attempts and stops on lockout or an MFA challenge, and
changes no policy or account setting to make a step pass. A probe establishes
its account and case, not all server behavior.

Hard gates: scoped guard privacy, chosen MFA transitions and state retention,
and rename acceptance evidence. Target schemas and reset acceptance and body
handling are closed. Password-policy breadth, general 409 meanings, session
revocation, and account-wide MFA effects remain documented unknowns; do not
promise them or require risky policy changes to ship.

### Fixture provenance

`testdata/iam/get_user_mfa.json` rebuilds the get after the disposable user's
sign-in: the unenrolled factor state plus `lastUse`, with the withheld `sms`
key omitted, as [fixture provenance](../../testdata/iam/user-provenance.md)
records. No enrolled-factor or rename fixture exists, and the empty reset 204
needs none. A future fixture preserves structural keys/types, replaces contact
values with `<account>` and IDs with `<id>`, records omissions, and stays out
of `testdata/` if it holds a secret. Never use decoded SDK output as raw
evidence. Workers receive only sanitized fixtures.

## Live checks

Extend `TestLiveWriteIAMUser` with separately opt-in reset, email MFA, Google
MFA, and rename branches, in addition to the two base gates. Use
`VNGCLOUD_LIVE_IAM_USER_RESET=1`, `VNGCLOUD_LIVE_IAM_USER_EMAIL_MFA=1`,
`VNGCLOUD_LIVE_IAM_USER_GOOGLE_MFA=1`, and `VNGCLOUD_LIVE_IAM_USER_RENAME=1`,
respectively; a branch exists only once its operation ships. The manager's run
brief must authorize each selected branch; unset means skipped, never
implicitly approved. Each enabled branch operates only on its created user,
cleans up attributable resources, and records actual coverage. A mailbox
variable configures an approved email branch, not its authorization. Every
branch is reviewed before execution and ships with its operation's SDK/CLI/wiki
slice. Fixed labels, statuses, counts, and booleans only; compile-only checks
are not live evidence.
