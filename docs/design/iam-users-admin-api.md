# IAM user administration: API and checks

Wire evidence and proposed SDK contracts for [IAM user
administration](iam-users-admin.md). [IAM users: API](iam-users-api.md) owns
the user get, filtered list, generation, shared bounds, and the strict
decoding rule these operations reuse. No live reset, rename, or MFA response
has been verified for this design.

## Sources

- [Accounts OpenAPI][spec], version 1.0.0, read from the saved page and
  compared with the live page: every path, body property, and status below.
  The spec documents no response schema for any `iam-users/{id}` operation
  except the list, no error body, and no 409 meaning. Its server is
  `https://iamapis.vngcloud.vn/accounts-api`; the SDK uses its existing
  Dashboard endpoint.
- The `auth/userinfo` schema in the same spec documents the caller's
  `twoFactorAuth` object. Applying that shape to another user's get is
  inference from the console user model recorded in [IAM users:
  API](iam-users-api.md#bodies-and-responses).

[spec]: https://docs.api.greennode.ai/service-docs/accounts-api.html

## Calls

Paths are relative to `accounts-api/v1/` on the configured Dashboard endpoint.
Every call requires the bearer token. Documented errors are `401`, `403`,
`404`, `409`, and `500` on every path below, plus `400` where a body or
state applies; the table lists only the success status and body.

| Path | Body | Success | Design use |
|-|-|-|-|
| `POST iam-users/{id}/reset-password` | `{"password": string}` | 204 | `ResetUserPassword` |
| `POST iam-users/{id}/activate-email` | None | 204 | `EnableUserMFA` email |
| `POST iam-users/{id}/deactivate-email` | None | 204 | `DisableUserMFA` email |
| `POST iam-users/{id}/activate-google` | None | 204 | `EnableUserMFA` google |
| `POST iam-users/{id}/deactivate-google` | None | 204 | `DisableUserMFA` google |
| `POST iam-users/{id}/set-up-email` | `{"email": string}` | 204 | Excluded |
| `POST iam-users/{id}/send-verification-email` | None | 204 | Excluded |
| `POST iam-users/{id}/verify-email` | `{"token": "^\d{6}$"}` | 204 | Excluded |
| `POST iam-users/{id}/set-up-google` | None | 200, body undocumented | Excluded |
| `POST iam-users/{id}/verify-google` | `{"token": "^\d{6}$"}` | 204 | Excluded |
| `POST iam-users/{id}/activate`, `deactivate` | None | 204 | Excluded |
| `PUT` or `PATCH iam-users/{id}` | Not published | None | `RenameUser` blocked |

The spec marks no body property required. The reset path documents `400`,
so the server validates the password; which rules it applies is unknown. The
`verify-google` path exists only in the `{id}` form although its summary says
"Current User", a documentation inconsistency this design does not resolve.
Current-user variants exist for every MFA path except `verify-google` and
for reset; the design excludes them all.

The published MFA flow, by path names and the userinfo fields, is set-up
(address or secret), verify (six-digit code), then activate or deactivate.
Whether `activate-*` requires `verified: true`, whether `deactivate-*` on a
factor that is already off returns 204 or 409, and whether either path
exists for a user who never set the factor up are unverified. Treat 409 as
`Conflict` in every case.

## State model

`auth/userinfo` documents, for the caller:

```json
{"twoFactorAuth": {"email": {"verified": true, "enabled": true,
  "address": "<account>"}, "googleAuthenticator": {"verified": true,
  "enabled": true}}}
```

The proposed public model, gated on the user get carrying the same object:

- `MFAFactor { Verified bool; Enabled bool }`.
- `UserMFA { Email MFAFactor; GoogleAuthenticator MFAFactor }`.
- `UserDetail` adds `MFA *UserMFA`, nil when the get carries no
  `twoFactorAuth` key. `Address` is omitted under [decision
  4](iam-users-admin.md#owner-decisions); the sanitized raw fixture keeps
  its presence as `<account>`.

A private state reader decodes the get with presence-aware pointers for
`twoFactorAuth`, each factor, and each boolean. A missing or null factor is
"not set up", which is a distinct state from `enabled: false`; a missing
boolean inside a present factor fails closed. The MFA operations use this
reader, never the zero values of the public model.

## SDK

All methods use `context.Context`, pointer Input, and `(*Output, error)` in
`danny.vn/vngcloud/iam`. Required fields use `vngcloud:"required"`. Every
operation validates locally, runs the [guards](iam-users-admin.md#guards),
and only then sends.

### Reset

- `ResetUserPasswordInput { UserID string; Password vngcloud.Secret;
  PasswordLength int }`. UserID is required and checked by
  `core.CheckPathID`. Empty Password selects
  [generation](iam-users-api.md#generation) with PasswordLength; a
  non-zero PasswordLength with a supplied Password is `ErrInvalidInput`.
  Supplied Password follows create's shape rules.
- `ResetUserPasswordOutput { Password vngcloud.Secret }`. Password holds
  only a generated value.

The request sends exactly `{"password":"<secret>"}` through a private wire
struct, with `OK: 204`, `Once`, a per-call `NotSent`, `Sensitive`, `Redact`
with the submitted value, `WithholdMessage` with fixed text, a fixed
`ClassifyError`, and a 64 KiB cap on any unexpected body.

| Reset result | Output and error |
|-|-|
| Validation, guard, entropy, or `NotSent` failure | Nil output; safe local cause or guard sentinel |
| Definite 4xx rejection | Nil output; sanitized API error |
| 204 | Output with the generated Password or empty |
| Any other possibly sent outcome | Partial output with the generated Password; `ErrUserWriteUncertain` |

No read shows whether a password changed, so there is no confirmation step.
A 2xx other than 204, a 5xx, a lost status, a dropped connection, and
cancellation after a possible send are uncertain. `NotSent` wins before
status classification. The caller treats the previous password as possibly
invalid, keeps a generated value, and checks by signing in or resets again.

### Rename

Blocked until the path is evidenced. The conditional contract:

- `RenameUserInput { UserID string; Username string }`, both required;
  Username follows create's shape rules.
- `RenameUserOutput { User UserDetail }`.

Send once with `Once`, `NoRedirect`, and a 64 KiB cap, accepting the
evidenced success status. Then read the user by ID with the strict, capped
get. Matching ID and exact Username confirm. A failed read or a mismatch
returns `RenameUserOutput` with only `User.ID` set and `ErrNotSettled`. A
possibly sent failure returns nil and `ErrUserWriteUncertain`. A 409 stays
`Conflict`. The wiki tells the caller to inspect with `get-user`.

### MFA

- `DisableUserMFAInput { UserID string; Method string }` and
  `EnableUserMFAInput` with the same fields. Method must be `email` or
  `google`; anything else is `ErrInvalidInput` before any request, since
  Method selects the path.
- `DisableUserMFAOutput { Method string; Enabled bool; Changed bool }` and
  `EnableUserMFAOutput` with the same fields.

Flow: validate; guards; read the target state through the private reader.
A missing `twoFactorAuth` or factor fails with a fixed error naming the
missing state, sending nothing. When `Enabled` already equals the target,
return `Changed: false` without sending. Otherwise send the matching path
with no body, `OK: 204`, `Once`, `NoRedirect`, and a 64 KiB cap, then read
once more. A read showing the target returns `Changed: true`; a 204 followed
by a failed read or the old state returns the Output with `Enabled` from the
last successful read and `ErrNotSettled`; a possibly sent failure returns
nil and `ErrUserWriteUncertain`. Neither path sends twice. The server
decides whether an unverified factor can be enabled.

### Error identity

Use `errors.Is` for `ErrInvalidInput`, `ErrSelfChange`,
`ErrPrivilegedChange`, `ErrOriginNotIAM`, `ErrNotSettled`,
`ErrUserWriteUncertain`, and context errors. Keep sanitized
`*vngcloud.APIError` reachable through `errors.As` with status and
Retryable. Map 401 to `ErrAuth`, 403 to `ErrPermission`, 404 to
`ErrNotFound`, and 429 to `ErrRateLimited` through the existing core
mapping. A 404 from a guard or confirm read carries a fixed read label so it
never reads as "target absent" after a write. CLI handling of
`ErrNotSettled` and `ErrUserWriteUncertain` precedes status mapping, so both
exit 1.

### Bounds

Every request uses the caller's context and the configured HTTP client
timeout; a multi-request operation needs a caller deadline. Reset and the
MFA sends are one attempt each. The target and confirm reads use the
existing transport retries for GET. Reads cap at 256 KiB, writes at 64 KiB,
and the strict rule applies to every read a guard or confirmation depends
on. No polling loop and no detached context exist.

## Unit tests

Use synthetic canaries and `httptest`, never real credentials or live calls.
Each operation tests its exact path, body, success status, every documented
error status, and unexpected responses.

- Reset: body equals the generated or supplied value exactly; 204 with and
  without a body; 400, 404, 409 rejected with fixed text and no password in
  any error node, `APIError` field, debug line, or CLI stderr; nested
  escaped echoes; one POST for 401, 429, 500, 307, 308, dropped connection,
  and cancellation before and after send; partial output retains the
  generated Password only; supplied mode returns an empty Password;
  PasswordLength bounds and the supplied-plus-length refusal.
- Guards: self before origin and privilege; root and unknown caller types;
  missing caller ID; service-account caller with an IAM target; non-iam,
  missing, null, and wrong-type `from`; mismatched target ID; failed target
  read; zero writes on every refusal; privilege refusal only when the
  decision applies.
- MFA: already-in-state sends nothing and reports `Changed: false`; a
  missing factor sends nothing; `Method` values other than the two refuse
  locally; one POST then one read on success; 204 with a stale read gives
  `ErrNotSettled`; duplicate and case-alias `twoFactorAuth`, `email`,
  `googleAuthenticator`, `enabled`, and `verified` keys refuse with no
  POST; 409 stays `Conflict`.
- Rename: skipped until the path is evidenced; keep a failing test
  placeholder out of the suite rather than a guessed body.
- CLI: `--yes` required where the table says; read-only refuses before any
  stdin read; exactly one password flag; `--password-length` without
  `--secret-file` exits 2; secret-file preflight before requests; a write
  failure after 204 reports `SecretFileFailed` naming the user ID and the
  command to run again, with no request after the failure; output prints
  the redacted Password, `SecretFile`, `Method`, `Enabled`, and `Changed`
  as specified; exit precedence for both sentinels after a 401 or 404
  read.

## Probes

Only the manager may run probes, after owner approval naming the account,
the disposable user, sign-in scope, the test mailbox if any, cost assumption,
and cleanup authority, and after a separate adversarial review with fix
confirmation of the credential-free probe source. Run them inside the [IAM
users probe](iam-users-api.md#probes) on the same disposable user, after its
create and before its delete. No probe touches the caller's own password or
MFA, a pre-existing user, or account security settings.

1. Reset the disposable user's password with a generated value of the
   default length. Record status, body presence and length, and the safe
   error code on failure. If sign-in is approved, sign in once with the
   old password and once with the new, without a persistent cache, and
   record booleans; also record whether a token issued before the reset
   still answers `auth/userinfo`.
2. Reset with a supplied 8-character value that meets the four classes,
   then with length 50. Record statuses. Skip values that break the
   console rules: a custom policy may accept them, and nothing is learned.
3. Get the disposable user and record whether `twoFactorAuth` exists, each
   factor's key names, types, and values before any set-up, and null or
   missing cases.
4. Call `deactivate-email` and `deactivate-google` on the user with no
   factor set up. Record statuses and safe codes; a 409 or 400 here proves
   only the not-set-up case. Call `activate-email` the same way.
5. If the owner names a test mailbox, run `set-up-email`,
   `send-verification-email`, `verify-email` with the received code,
   `activate-email`, a get, `deactivate-email`, and a get. Record statuses
   and the factor fields after each step. Hold no code or address in
   output. Google set-up runs only under decision 8's alternative, with
   the response held in memory and discarded.
6. Rename runs only after a published path or a captured console request
   exists: rename the disposable user once, record status and body
   presence, get it, and record whether the old name is free by a filtered
   list.
7. Delete the disposable user under the IAM users probe. Record whether a
   token issued to it before deletion still answers `auth/userinfo` only
   if sign-in was approved.

Missing evidence after the probes blocks the dependent contract: the reset
400 rules, factor-state presence on the get, 409 meanings, token survival
after reset or rename, the rename path, and the interaction with an
account-level MFA requirement.

### Fixture provenance

Fixtures are `get_user_mfa.json` for a get carrying `twoFactorAuth`, and,
if the probe reaches them, `reset_user_password_204.txt` recording an empty
body with status, and `rename_user.json` under `testdata/iam/`. Replace the
address with `<account>` and IDs with `<id>`. Keep every structural key and
type. Record method, placeholder path, status, raw origin, removed values,
and intentional model omissions. Never substitute SDK output or a hand-built
response for raw evidence; an empty body stays empty.

## Live checks

Extend `TestLiveWriteIAMUser` with reset and the MFA state read on the user
it created, behind the same gates, and with the MFA writes only when the
owner-named mailbox variable is set. Rename joins only after its evidence.
Emit fixed labels, statuses, counts, and booleans; never a password, code,
address, or token. Compile-only checks are not live evidence.
