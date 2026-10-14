# IAM users: API and checks

The GreenNode IAM user calls, bodies, and responses behind
[IAM users](iam-users.md), the SDK surface built on them, and the tests,
probes, and live checks that gate each release. Hosts, endpoints, paging,
and the existing user reads are in [IAM writes: API](iam-writes-api.md).

## Sources

- The OpenAPI spec embedded in
  `https://docs.api.greennode.ai/service-docs/accounts-api.html` ("IAM
  Accounts API Docs", version 1.0.0, server
  `https://iamapis.vngcloud.vn/accounts-api`). It documents the user calls
  below with status codes, the create and reset-password request bodies,
  and the list response, and no other response body. This is the published
  contract.
- The public IAM console at `https://iam.console.greennode.ai/`: its index,
  `main.<hash>.js`, and the lazy chunks the runtime names, read as data. Its
  user-account service names every call the console makes; its create
  wizard, validators, and password generator give the request body and the
  username and password rules. These are inferred wire shapes.
- GreenNode product pages: "User accounts" under Identity and Access
  Management (`docs.greennode.ai/identity-and-access-management-iam/...`),
  "How to sign in", and the vStorage pages "Create an IAM user account" and
  "Delete an IAM user account". They state the username and password rules,
  that a new user holds no policy and is denied by default, that a username
  stays unique until the user is deleted, and that a deleted user cannot be
  recovered.
- No live call was made for this design. The [probes](#probes) establish
  the create response, the get shape, the list filter, and the delete codes
  before code starts. Nothing below upgrades an inferred shape to a
  verified one until a probe records it.

## Calls

Every call runs on the accounts API under the Dashboard endpoint, at
`accounts-api/v1/`, as the existing `ListUsers` does. "Spec" marks the
published contract; "console" marks an inferred shape.

| Call | Method and path | Success | Documented errors | Evidence |
|-|-|-|-|-|
| Create user | `POST iam-users` | 201 | 400, 401, 403, 409, 500 | Spec and console |
| Get user | `GET iam-users/{id}` | 200 | 401, 403, 404, 500 | Spec (no body); console model |
| Delete user | `DELETE iam-users/{id}` | 204 | 401, 403, 404, 409, 500 | Spec and console |
| List users | `GET iam-users?pageSize&pageNumber` | 200 | 401, 403, 500 | Spec; `name` and `from` filters console only |
| User quota | `GET quotas/iam-user` | 200 | none listed | Console only |

The console also calls these, and this design adopts none of them:
`PUT iam-users/{id}` with `{username}`; `POST iam-users/{id}/activate` and
`/deactivate`; `POST iam-users/{id}/reset-password` with `{password}` and
`POST iam-users/reset-password` for the caller's own password; the
`set-up-`, `send-verification-`, `verify-`, `activate-`, and `deactivate-`
calls for email, SMS, and Google authenticator two-factor; and
`iam-users/{id}/tags`. See the design's
[owner decisions](iam-users.md#owner-decisions).

## Bodies and responses

- Create body: exactly `username` and `password`, both strings. The spec
  and the console agree. There is no console-access flag, no
  change-password-at-first-sign-in flag, no group or policy field, and no
  server-side password generation: the console's "Auto-generate" button
  builds a 12-character password in the browser and sends it like a typed
  one. Neither the console nor the product pages mention a forced password
  change at first sign-in.
- Create response: the spec documents 201 with no body, and the console
  discards whatever comes back. Whether the body holds `id` is unknown.
  The SDK handles both cases; see [CreateUser](#writes).
- Get response, inferred from the console's user model and how it fills
  list rows from this call: `id`, `username`, `createdAt`, `enabled`
  (boolean), `lastUse`, `from`, `idpId`, and `twoFactorAuth` with `email`
  (`verified`, `enabled`, `address`), `sms` (`verified`, `enabled`, `tel`),
  and `googleAuthenticator` (`verified`, `enabled`). `enabled`, `lastUse`,
  and `from` are present: the console copies them from this call into
  every list row. `from` is `iam` for a user created in IAM and `idp` for
  one from an identity provider; the console shows the password section
  only when `from` is exactly `iam`. The type of `lastUse` (number or
  string) and the exact key set need the probe.
- List response: `data`, `pageNumber`, `pageSize`, `totalItems`, and
  `totalPages`; a row holds `id`, `username`, and `createdAt` as an RFC
  3339 string. The console adds `name` and an optional `from` query
  parameter. Whether `name` matches exactly or by substring is unknown.
- Username rule, from the console validator and the product pages: 5 to 50
  characters from ASCII letters, digits, `_`, `-`, and `.`
  (`^[\w\-.]{5,50}$`), unique within the account until deleted. The vStorage
  page says to keep sensitive data such as IP addresses or passwords out of
  a username.
- Password rule, from the console validator and the product pages: 8 to 50
  characters with at least one uppercase letter, one lowercase letter, one
  digit, and one character from
  `` !"#$%&'()*+,-./:;<=>?@[\]^_`{|}~`` (space included). The root user can
  replace the default policy with a custom one: minimum length 8 to 50,
  required character classes, expiry of 1 to 1095 days, and 1 to 24
  passwords that may not be reused. The server enforces the policy; the SDK
  checks only [shape](#writes).
- Delete response: 204 with no body. The spec lists 409 for delete without
  saying when; whether a user with attachments or a signed-in user
  conflicts is unknown.
- Quota response: `limit` and `usage`, from the console's quota mapper.
- Error bodies: the accounts API error wrapper that
  [IAM writes: API](iam-writes-api.md#bodies-and-responses) decodes. The
  console bundles name no user-specific error code; a 409 on create means
  the username is taken.
- Identifiers: user IDs are UUIDs in the existing reads. Usernames and
  user IDs are account data; fixtures use `<id>` and `<account>`.

## SDK

All operations live in `iam`. "(r)" marks `vngcloud:"required"`.

### Reads

| Operation | Input | Output |
|-|-|-|
| `GetUser` | `UserID` (r) | `{UserDetail}` |
| `ListUsers` | `Name`, `Page`, `Size` | L[User] |

- `ListUsers` gains an optional `Name`, sent as `name` only when
  non-empty. Existing callers pass no `Name` and see no change.
- `UserDetail` embeds `User` (`ID`, `Username`, `CreatedAt`) and adds
  `Enabled bool` (`enabled`) and `From string` (`from`). `LastUse` joins
  it once the probe fixes its type; `idpId` and `twoFactorAuth` are
  dropped, since identity providers and MFA stay out of scope and the
  email address and phone number are account data the SDK does not need.
- `GetUser` sets `MaxBody` to 256 KiB; `ListUsers` keeps its current
  shape and gains a 4 MiB cap. Every path ID passes `core.CheckPathID`.

### Writes

| Operation | Input | Output |
|-|-|-|
| `CreateUser` | `Username` (r), `Password vngcloud.Secret` | `{UserDetail; Password vngcloud.Secret}` |
| `DeleteUser` | `UserID` (r) | `{}` |

`CreateUser`:

1. Checks shape before any request: `Username` non-empty, at most 4096
   bytes, with no whitespace or control character; `Password`, when given,
   the same bounds and no control character. Length and character-class
   rules stay on the server (ADR 0002 rule 5).
2. When `Password` is empty, generates one: 32 characters from
   `crypto/rand`, at least one uppercase letter, one lowercase letter, one
   digit, and one of `!@#$%^&*()_+-=[]{}:;,.?/<>|~`, all inside the
   console's accepted set and none of space, quote, backslash, or backtick.
   The generated value is returned in `Output.Password` and nowhere else;
   `Output.Password` is empty when the caller supplied one.
3. Sends `POST iam-users` with `{"username", "password"}` and
   `transport.Request.Once`, `Sensitive`, `Redact` holding the password,
   `MaxBody` 64 KiB, `OK` 201, and a `NotSent` flag. The response is
   decoded for an optional `id` only.
4. After an error: when `NotSent` is true, the error is returned as is,
   since no bytes reached a connection and the call is safe to repeat;
   after a 4xx `*core.APIError`, the error is returned as is, since the
   server rejected the request; after anything else, the error says the
   create may have reached the server and names `list-users --name`.
5. After a 201: with an `id`, reads `GetUser`; without one, lists users
   with `Name` set to the username, page size 1000, and takes the row whose
   `Username` equals it exactly, refusing a page whose `data` length and
   `totalItems` differ or whose `totalPages` exceeds 1. A read-back that
   fails, finds nothing, or finds no exact match returns a non-nil Output
   holding the ID when known, the username, and the password, with
   `iam.ErrCreateUnconfirmed`, as `CreateServiceAccount` does.

`DeleteUser` runs the [delete guard](iam-users.md#guards) and then sends
`DELETE iam-users/{id}` with `OK` 204. `DELETE` is idempotent and keeps the
transport's retries; a retried delete that finds nothing returns
`NotFound`, as `DeleteServiceAccount` does.

## Unit tests

Unit tests use `httptest` with a fake API that records every request.

- Sanitized raw fixtures and decode tests in `testdata/iam/`: `get_user.json`
  and `list_users_filtered.json` from the probe's reads, and
  `create_user.json` holding the create response with no password, or an
  empty body when the server sends none. Fixture provenance names the
  probe step and the replacements.
- `CreateUser` sends exactly `username` and `password`; sets `Once`,
  `Sensitive`, and `Redact`; a 400 whose body echoes the password returns
  an error without it; the capture hook never sees the response; `fmt`,
  `slog`, and `json.Marshal` of the Input and Output give `[redacted]`.
- Generation: an injected reader makes the password deterministic; 32
  characters; every class present; only accepted characters; a fresh value
  per call; `Output.Password` empty when the caller supplied one.
- Shape refusals with no request: empty username, whitespace or control
  character in either field, 4097 bytes in either field.
- Statuses 201, 400, 401, 403, 409, and 5xx; no resend after a 502, 429,
  401, or 307; a failed dial returns an error marked not sent; a 201 with
  `{"id"}` reads back by ID; a 201 with no body lists by name and matches
  exactly, refusing a substring match, a short page, or two pages; a failed
  read-back returns `ErrCreateUnconfirmed` with the ID or username and the
  password still in Output.
- Delete guard, each with no `DELETE` sent: the caller as target in a
  different case; `from` of `idp`, missing, or unreadable; a privileged
  policy held directly and one held through a group; an unprivileged
  policy; group membership alone; a short attachment page; a null group
  list; an unclassified caller type. A 404 from `GetUser` is `NotFound`.
- `GetUser` and `ListUsers` paths, `name` only when `Name` is set, body
  caps, and `CheckPathID` rejection for `..`, `.`, `/`, `?`, and empty.
- CLI golden tests: `create-user` with `--password-file`, with `-` on a
  pipe, with `--secret-file`, with both (exit 2), with neither (exit 2),
  with a terminal on stdin (exit 2), with an empty file (exit 2), with a
  `Password` key in `--cli-input-json` (exit 2); `--secret-file` refusals
  and mode 0600; a failed file write deletes the new user; a no-ID
  unconfirmed create writes the file and exits 1; `delete-user` without
  `--yes` (exit 2); read-only refusal of both writes with no request; no
  password in stdout, stderr, `--debug` output, or any error.

## Probes

The manager runs these on the test account before code starts, under the
owner's standing approval for free, cleaned-up writes. IAM is global, so no
region applies. The disposable user is named `vngcloud-live-<8 hex>`; the
probe touches no other user. Runs log only statuses, error codes, key
names and types, booleans, counts, and timings; never a username past the
prefix, an ID, or a password. The password stays in memory, and no create
response is written anywhere. Raw bodies of reads go under
`examples/basic/output/raw/iam/`.

1. `GET auth/userinfo`: the caller's own `userId`. `GET iam-users/{own
   id}`: key names and types, the `from` value, and the `lastUse` type.
2. `GET iam-users?name=<own username>&pageSize=10&pageNumber=0`, then the
   same with the first three characters only: whether `name` filters
   exactly or by substring, and whether it is case-sensitive.
3. `GET quotas/iam-user`: `limit` and `usage`; there must be room for one.
4. Create the prefix user with a locally generated 32-character password of
   the four classes: status, response key names and types, and whether an
   `id` is present.
5. `GET iam-users?name=<prefix user>`: found by exact name. `GET
   iam-users/{id}`: keys match step 1. `GET
   user-attachments/iam-users/{id}/policies`: `totalItems` 0. `GET
   user-attachments/iam-users/{id}/groups`: empty array.
6. Create the same username again: status and code.
7. Create with a 4-character username, then with a password missing one
   class: status and code for each; nothing may be created.
8. Sign in as the new user through the SDK's IAM user login with the root
   email, username, and generated password, and call
   `GetCallerIdentity`: whether login succeeds and reports `iam-user`.
   Log booleans only.
9. `DELETE iam-users/{id}`: status. Repeat: status and code. `GET`: status.
10. Step 3's quota is back to its first usage, and no prefix user remains.

Not probed: deleting the caller, any user with attachments, or any user
the probe did not create; the 409 on delete stays unresolved unless step 9
returns it.

## Live checks

After the probes, the release adds to `make live` and the live write test.
Live write runs need the owner's standing approval for free, cleaned-up
writes on the test account.

- `make live` adds `GetUser` on the caller's own ID and `ListUsers` with
  `Name` set to the caller's username, logging counts and booleans only.
- `TestLiveWriteIAMUser`, gated by `VNGCLOUD_LIVE_IAM_USER=1`, sweeps
  leftover prefix users that hold no policy and no group, creates one with
  a generated password written to a temp file, reads it back, lists it by
  name, checks that `DeleteUser` on the caller returns `ErrSelfChange`
  with no request, and deletes it. Cleanup is registered by exact name
  before the create and by ID as soon as it is known, and asserts none
  remain. The test never logs an ID, a username past the prefix, or a
  password.
