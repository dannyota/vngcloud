# IAM user administration: API and checks

Wire evidence and proposed contracts for [IAM user
administration](iam-users-admin.md). [IAM users: API](iam-users-api.md) owns
get, filtered list, generation, bounds, and strict decoding. No live reset,
rename, or MFA response has been verified. Source evidence does not close a
raw-response gate or authorize a probe.

## Sources

- [Accounts OpenAPI][spec], version 1.0.0: the saved and fetched embedded
  specifications match. It publishes reset/MFA paths, request schemas, statuses,
  and caller `auth/userinfo.twoFactorAuth`. It publishes no success schema for
  the administrator operations, error schema, or 409 meaning. Only GET and
  DELETE are published on `/v1/iam-users/{id}`.
- [Console main bundle][main]: `resetUserPassword` posts `{password}`;
  `editUsername` uses PUT on `/v1/iam-users/{id}` with `{username}`. `toggle2FA`
  chooses `activate-` or `deactivate-` plus the factor name and posts `{}`.
  These are source-level candidate requests, not observed server behavior. No
  PATCH user rename is evidenced.
- [User-account chunk][users]: administrator `setupGoogleAuth` consumes `secret`
  and `qrCodeUrl` from `setupGoogleAuth2FA`. Treat both as secret material; do
  not fetch or render a QR URL in a probe. The console service also names
  `/v1/iam-users/verify-google` for current-user verification; the OpenAPI omits
  that path. Its `{id}/verify-google` summary incorrectly says "Current User".
  Preserve this discrepancy; neither variant ships.

Bundles were read as text, never executed. Their model defaults do not prove
wire presence. The spec's server is `https://iamapis.vngcloud.vn/accounts-api`;
the SDK uses its existing Dashboard endpoint, whose behavior still needs
verification.

[spec]: https://docs.api.greennode.ai/service-docs/accounts-api.html
[main]: https://iam.console.greennode.ai/main.a0cfd100731832eb.js
[users]: https://iam.console.greennode.ai/718.16d2630147e1e633.js

## Calls

All listed methods are POST relative to `accounts-api/v1/iam-users/{id}/` unless
stated otherwise. Bearer authentication is required. Every reset and MFA path
below documents errors **400, 401, 403, 404, 409, 500**, including bodyless
paths. The spec marks request bodies required where present but no body property
required. The SDK requires a non-empty replacement password.

| Suffix | Published body | Success | Use |
|-|-|-|-|
| `reset-password` | `{"password": string}` | 204 | Reset |
| `activate-email` | None | 204 | Enable email |
| `deactivate-email` | None | 204 | Disable email |
| `activate-google` | None | 204 | Enable Google |
| `deactivate-google` | None | 204 | Disable Google |
| `set-up-email` | `{"email": string}` (email format) | 204 | Excluded |
| `send-verification-email` | None | 204 | Excluded |
| `verify-email` | `{"token": string}` (six digits) | 204 | Excluded |
| `set-up-google` | None | 200; schema absent | Excluded |
| `verify-google` | `{"token": string}` (six digits) | 204 | Excluded |

The token pattern is `^\d{6}$`. Current-user reset and MFA paths remove the
`{id}/` segment and publish the same statuses/bodies, except `verify-google` is
absent as noted above. Account `activate` and `deactivate` are separate excluded
paths: 204, errors 401/403/404/409/500, no documented 400.

MFA set-up, verification, and activation order is inferred from path names and
console state changes. Set-up can replace enrolment; do not treat it as a
harmless read. Whether activation requires verification, deactivation preserves
it, or already-in-state requests return 204 or 409 is unknown. Conflict always
remains `Conflict`. The proposed SDK sends no MFA body, following the spec; the
console sends `{}`, a difference to verify in probes.

Rename candidate: `PUT accounts-api/v1/iam-users/{id}` with exactly
`{"username":"<account>"}` from `editUsername`. Statuses, body handling, error
rules, uniqueness behavior, and same-ID preservation await evidence. Do not
accept arbitrary 2xx responses or infer 204 from nearby operations.

## State model

Caller userinfo publishes `twoFactorAuth.email.{verified,enabled,address}` and
`twoFactorAuth.googleAuthenticator.{verified,enabled}`. The booleans are not
required by that schema. Applying it to another user's get is inference until a
sanitized raw response establishes presence and types.

Proposed additions, gated separately from base `UserDetail`:

- `MFAFactor { Verified *bool; Enabled *bool }`, with matching API tags.
- `UserMFA { Email *MFAFactor; GoogleAuthenticator *MFAFactor }`.
- `UserDetail.MFA *UserMFA` with `json:"twoFactorAuth,omitempty"`. Nil means
  absent or null, never "disabled" or "not enrolled".
- Omit address, SMS, TOTP secrets, and QR data. Keep safe structural presence in
  sanitized raw fixtures; contact values become `<account>`.

Public output preserves missing/null booleans as nil. A private state reader
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
`{"password":"<secret>"}`. Set `OK: 204`, `Once`, per-call `NotSent`, and the
privacy controls. The write caps unexpected body bytes at 64 KiB and decodes no
success object. No confirmation read or password sign-in follows.

| Result | Output and error |
|-|-|
| Local validation, guard, entropy, or proven `NotSent` failure | Nil; safe cause |
| Observed 4xx rejection, including oversized error body | Nil; safe API error |
| Observed 204 with no transport/body error | Output; nil error |
| Observed 204 with response-read/limit error | Partial; `ErrNotSettled` |
| Other possibly sent outcome | Partial; `ErrUserWriteUncertain` |

Use the observed HTTP status, never a nested provider status. `NotSent` wins
first. A 204 is acceptance even if response handling fails; preserve that fact
and the generated password. Unexpected 2xx/3xx, 5xx, missing status, dropped
connection, and cancellation after possible send are uncertain. `Retryable` is
not proof of no send. Neither the SDK nor CLI repeats a reset. A success proves
acceptance, not that a concurrent actor has not reset again.

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

Reset reaches `iam/guard.go:441`, `:181`, `:240`, `:397`, and
`iam/policy_guard.go:269`; those reads currently lack these local options.
Cold-cache and warm-cache tests must prove that options neither leak across
concurrent calls nor weaken normal guards. If this graph cannot carry the
controls safely, reset stays blocked. Sensitive `GetUser` alone is not enough.

### Rename

Blocked pending the [candidate's response contract](#calls). Proposed types:
`RenameUserInput { UserID string; Username string }`, both required, and
`RenameUserOutput { User UserDetail }`. Username follows create's shape rules.
Send the candidate PUT once with `Once` despite PUT's normal retries, only after
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
`NotSent`, and privacy controls. Read once after 204; require the same ID/origin
and complete selected-factor state. Enable confirmation also requires `Verified:
true`. A matching state returns `Changed: true`.

Observed 204 with failed response handling or absent/mismatched confirmation
returns `ErrNotSettled`, `Changed: true`, and nil Enabled if no trustworthy
confirmation exists (otherwise the freshly read Enabled). Never use the
pre-write value as a confirmed result. A proven `NotSent` or observed 4xx
returns nil with the safe cause; every other possibly sent outcome returns nil
with `ErrUserWriteUncertain`, without a confirmation or second send. Manual
repetition rechecks state and guards. A 409 is never converted to success, even
when a later operator read finds the requested state.

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
rename confirmation uses create's identity validation rules. Strict JSON and
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
  conflicting length fail before reads; generation runs after guards; test 204
  with ignored body and response failures, fixed errors, all error nodes and API
  fields, redacted output, supplied value never returned, no Input mutation, and
  saved generated passwords on both outcome errors.
- Privacy: password/secret canaries on each guard edge, POST, debug, capture,
  nested escaped error codes/messages, and unsafe decode causes. Test cold and
  warm caches, concurrent requests with different redaction values, no retained
  private options, and no request after persistence failure.
- Guards: self before origin/privilege, root/unknown/missing caller ID,
  service-account caller with IAM target, direct/group privilege, unknown action
  labels, empty actions, incomplete attachments, unreadable policy,
  null/wrong-type/non-IAM origin, and mismatched IDs. Every refusal sends zero
  writes, including an already-in-state MFA operation.
- MFA: nil and false are distinct through SDK fixture decoding and JSON output;
  missing/null factor or boolean refuses; enable unverified refuses before
  no-op; disable known enabled/unverified succeeds; duplicates and case aliases
  at every state level refuse. Test stale, missing, wrong-ID, non-IAM, and
  unverified-enable confirmations and Changed/Enabled semantics.
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

Only the manager runs probes after explicit approval naming account, disposable
user, cost assumption, cleanup, and each reset/MFA/rename action. Sign-in and
factor enrolment need their own express scope. A mailbox variable or
public-source research authorization grants none. Independent adversarial review
and fix confirmation of credential-free probe code precedes execution. Run
inside the [user probe](iam-users-api.md#probes), after create and before
delete, serialized with other IAM writers. No pre-existing user, caller
credential, or account setting may change.

1. Reset with a generated default password, then approved supplied values of
   length 8 and 50 satisfying the four classes. Keep secrets in memory; record
   status, body presence/length, and locally classified error codes, never raw
   server codes/text. A rejection does not authorize weakening policy. Test
   invalid-policy responses offline, not by changing settings.
2. If sign-in is approved, compare fresh old/new-password sign-ins in memory
   without persistent cache, and separately check a pre-reset token against
   userinfo. Bound attempts; a lockout/MFA challenge stops the probe. Record
   booleans only. Successful cached auth does not prove a password works.
3. Get the disposable user before enrolment and after each approved factor
   transition. Record keys, types, absent/null states, verification and enabled
   booleans. Missing state blocks MFA operations, not reset.
4. If approved, probe activate/deactivate of both factors before set-up. Those
   results prove only that case, not a verified-factor transition.
5. With explicit email enrolment approval and an owner-controlled test mailbox,
   set up, send mail, verify, activate, get, deactivate, get, then reactivate
   and get. Check verified-state retention and no-body POSTs against the
   console's `{}` variant. Never print address or code. Google enable/disable
   needs an equivalent authorized transition cycle with the TOTP secret and QR
   data in memory only; never fetch a returned QR URL. If absent, defer that
   method rather than extrapolate from email.
6. With explicit rename approval, probe the console PUT on the disposable ID,
   record status/body schema, get by ID, and compare exact username and
   unchanged ID. A filtered list can show the old name absent, not prove it is
   reusable. Collision or old-name reuse needs separately approved extra writes.
   Sign-in/token checks need the same separate approval.
7. Delete only the attributable disposable ID under the base cleanup rules. No
   reset/MFA failure expands cleanup to a pre-existing principal. Check a
   pre-delete token only within approved sign-in scope.

Hard gates: target schemas, reset acceptance/body handling, scoped guard
privacy, chosen MFA transitions/state retention, and rename method/status/
response evidence. Password-policy breadth, general 409 meanings, session
revocation, and account-wide MFA effects remain documented unknowns unless
observed; do not promise them or require risky policy changes to ship. A probe
establishes its account/case only, not all server behavior.

### Fixture provenance

Under `testdata/iam/`, use manager-sanitized raw `get_user_mfa.json` and any
safe `rename_user.json` get fixture. Empty 204 responses remain empty bytes with
separate method/path/status provenance, never invented JSON. Preserve structural
keys/types, replace contact values with `<account>` and IDs with `<id>`, and
record omissions. If a response contains secrets, keep it out of fixtures and
return the safe evidence method to review. Never use decoded SDK output as raw
evidence. Workers receive only sanitized fixtures.

## Live checks

Extend `TestLiveWriteIAMUser` with separately opt-in reset, email MFA, Google
MFA, and rename branches, in addition to the two base gates. Use
`VNGCLOUD_LIVE_IAM_USER_RESET=1`, `VNGCLOUD_LIVE_IAM_USER_EMAIL_MFA=1`,
`VNGCLOUD_LIVE_IAM_USER_GOOGLE_MFA=1`, and `VNGCLOUD_LIVE_IAM_USER_RENAME=1`,
respectively. The manager's run brief must authorize each selected branch; unset
means skipped, never implicitly approved. Each enabled branch operates only on
its created user, cleans up attributable resources, and records actual coverage.
A mailbox variable configures an approved email branch, not its authorization.
Every branch is reviewed before execution and ships with its operation's
SDK/CLI/ wiki slice. Fixed labels, statuses, counts, and booleans only;
compile-only checks are not live evidence.
