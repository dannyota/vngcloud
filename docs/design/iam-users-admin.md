# IAM user administration design

Status: Proposed. [Owner decisions](#owner-decisions), independent design and
plan review, and the [evidence probes](iam-users-admin-api.md#probes) gate
implementation. This document authorizes no live call, sign-in, or cleanup.

Add administrator password reset, rename, and MFA enable and disable for IAM
users to the public `iam` package and CLI, beside [create and
delete](iam-users.md). The caller acts on another user under the same root
account; nothing here changes the caller's own password or MFA.

[API and checks](iam-users-admin-api.md) owns wire evidence, public types,
validation, outcomes, tests, and probes. The [IAM users design](iam-users.md)
owns password sources, generation, input rules, secret files, and the shared
caller, self, and origin guards. Follow [write
conventions](../adr/0002-write-api-conventions.md), [toggle
writes](../adr/0003-toggle-writes.md) for the MFA state operations, the [IAM
guard model](iam-writes.md#guards), and [CLI secret
files](cli.md#secret-files). The SDK stays standard-library only; the CLI
calls only the public SDK. No dependency is added.

## Scope

| Operation | Published path | State |
|-|-|-|
| `ResetUserPassword` | `POST iam-users/{id}/reset-password` | Published; live evidence gated |
| `RenameUser` | None | No published or captured path; blocked |
| `DisableUserMFA` | `POST iam-users/{id}/deactivate-email` and `deactivate-google` | Published; state read gated |
| `EnableUserMFA` | `POST iam-users/{id}/activate-email` and `activate-google` | Published; state read gated |

Out of scope: the current-user reset and MFA paths, because the caller's own
credentials stay a console task; MFA set-up, verification, and verification
mail, because they need the user's own device, inbox, or code, or would hand
the administrator the user's secret; account activate and deactivate; tags;
IP allow lists; identity providers; and quota writes. The [MFA
subset](#mfa) names the alternatives the owner may choose instead.

Rename has no path in the published Accounts API, which exposes only `GET`
and `DELETE` on `iam-users/{id}` while publishing `PATCH` for service
accounts, identity providers, S3 keys, and Swift users. The console bundle
sources read for the create design named no user update call either. The
rename contract below is conditional: implementation waits for a published
path or a manager-captured console request under [live
data](../../instructions/live-data.md), followed by design re-review.

## Operations

### Password reset

`ResetUserPassword` replaces a user's password with a supplied or generated
value. The server returns 204 and no body, so no read can show whether a
password changed. The old password stops working when the request lands;
whether existing sessions or tokens of that user survive is unverified and
the wiki must not claim either way.

The password sources, generator, input rules, secret file, and privacy
controls are exactly those of [create](iam-users.md#credentials): one of a
supplied file or stdin and a generated secret file, default length 32,
caller-chosen 8 to 50, `Secret` everywhere, request-local redaction, fixed
error text, and capture suppression. Supplied passwords are never returned;
a generated password is returned on 204 and retained in the partial output
when the outcome is uncertain, because it may be the live one.

After a secret-file failure the CLI reports `SecretFileFailed`, states that
the password was already replaced, names the user ID, and tells the caller to
reset again. There is no undo and no cloud cleanup, as for
`reset-service-account-secret`.

### Rename

`RenameUser` changes the sign-in username. The contract assumes one request
carrying the new username and a 2xx with or without a body; the method,
body key, and status are unverified. After the request the SDK reads the user
by ID once: a matching username confirms; a failed or mismatched read returns
`ErrNotSettled` with the ID, never a second request. Username shape rules
match create. A duplicate name is the server's 409 and stays `Conflict`.

A CLI profile signs in with `username`, and the token cache binds its entry
to profile, root email, and username (`internal/tokencache/cache.go:43`).
Renaming a user that a profile uses breaks that profile's next sign-in until
its `username` is updated; whether tokens issued under the old name remain
valid is unverified. The wiki says this; the tool does not edit profiles.

### MFA

The published API models a factor per user for email and for Google
Authenticator. `auth/userinfo` documents `twoFactorAuth.email` with
`verified`, `enabled`, and `address`, and `googleAuthenticator` with
`verified` and `enabled`, for the caller. The console's user model carries
`twoFactorAuth` for any user, so the user get probably carries the same
shape; that is inference until the probe records it. The paths split into
set-up, verify, activate, and deactivate per factor.

An administrator can meaningfully turn a factor off for a user who lost a
device or inbox, and turn a verified factor back on. Set-up and verify need
the user: `set-up-google` answers 200 with an undocumented body that is
presumably the TOTP secret, so an administrator who runs it holds the user's
second factor; `set-up-email` lets the administrator choose the address, so
the administrator could enrol a mailbox it controls; `verify-*` needs the
six-digit code from the user's device or inbox. The recommended subset is
therefore `DisableUserMFA` and `EnableUserMFA` with `Method` equal to
`email` or `google`.

Both operations read first, send once, and confirm by reading, under [ADR
0003](../adr/0003-toggle-writes.md), even though each names its target
state: a resend after a 5xx could meet a 409 for a change that already
landed. When the target read already shows the requested state, the
operation sends nothing and reports `Changed: false`. A 204 followed by a
read that fails or still shows the old state returns `ErrNotSettled`; the
recovery is to run the same operation again. The server decides whether an
unverified factor can be enabled; the SDK does not pre-judge that.

Whether disabling a user's factor overrides an account-level MFA
requirement, and what the user sees at the next sign-in, is unverified. The
state read depends on the user get carrying the factor fields; if it does
not, both operations stay blocked rather than send blind.

## Guards

Every operation validates its input, then runs the shared [caller, self, and
origin checks](iam-users.md#guards) before any request: a classified caller
(`iam-user`, `user-sa`, or `service-sa`; root and unknown types return
`ErrPrivilegedChange` because the self check cannot be evaluated), the
caller's own ID refused as `ErrSelfChange`, and a target read whose ID must
match and whose `from` must equal `iam`, otherwise `ErrOriginNotIAM`. All
three apply to all three operations:

- Self: a caller must not reset its own password, rename itself, or change
  its own MFA through an administrator path; the API has current-user paths
  for that, which this design excludes. A self reset would also invalidate
  the credential the CLI profile holds.
- Origin: a provider-managed user has no IAM password, its name belongs to
  the provider, and the console shows the password controls only for
  `from === "iam"`. Refusing other origins avoids sending credentials to a
  path whose behavior for such users is unknown.
- Classification: the self check needs a usable caller ID and type.

The privilege guard is a [decision](#owner-decisions). Resetting the
password of a user that holds IAM write rights hands the caller that user's
sign-in, which is the escalation the [IAM guard
model](iam-writes.md#threat) exists to stop and the reason
`ResetServiceAccountSecret` refuses a protected service account. Disabling
such a user's MFA weakens the same sign-in; renaming it can lock the owner
out. The recommendation applies `userIsProtected` after the origin check and
returns `ErrPrivilegedChange`, with self outranking privilege. Delete keeps
its lighter rule because deleting a user removes rights rather than
transferring them.

A service-account caller cannot be the target user, but it still needs a
classified identity and the target checks. No input or flag bypasses a guard.
Checks and the write are not atomic; server authorization remains the
security boundary.

## CLI

All commands run under `vngcloud iam` and register as writes, so read-only
refuses them before any input or request.

| Command | Flags | Consent |
|-|-|-|
| `reset-user-password` | `--user-id`, one password source flag, `--password-length` | `--yes` |
| `rename-user` | `--user-id`, `--username` | `--yes` |
| `disable-user-mfa` | `--user-id`, `--method email\|google` | `--yes` |
| `enable-user-mfa` | `--user-id`, `--method email\|google` | None |

`reset-user-password` reuses create's `beforeInput` rule: exactly one of
`--password-file` and `--secret-file`, stdin never shared with
`--cli-input-json`, and `--password-length` only with `--secret-file`. Reset
and rename change a sign-in credential or identity and so need `--yes`;
disabling a factor weakens a sign-in and needs `--yes`; enabling is undone by
one more command and matches [ADR 0002](../adr/0002-write-api-conventions.md)
rule 6. The consent split is a [decision](#owner-decisions).

```sh
vngcloud iam reset-user-password --user-id <user-id> \
  --secret-file <private-directory>/password --yes
vngcloud iam reset-user-password --user-id <user-id> --password-file - \
  --yes < <private-directory>/password
vngcloud iam disable-user-mfa --user-id <user-id> --method google --yes
```

Reset prints `Password` redacted and `SecretFile` only when written; the
supplied mode prints neither. Rename prints the confirmed user. The MFA
commands print `Method`, `Enabled`, and `Changed`. Errors print no partial
object; recovery text may name the validated user ID and the destination
path, which are account data in live reports.

Shape, flag, file, and read-only errors exit 2. `ErrSelfChange`,
`ErrPrivilegedChange`, `ErrOriginNotIAM`, `ErrNotSettled`,
`ErrUserWriteUncertain`, 409, and secret-file failures exit 1; the two write
outcome sentinels exit 1 even when a later read failed with 401 or 404.
Authentication errors exit 3 and a target 404 exits 4. `ErrOriginNotIAM` maps
to `RequestFailed`; no new CLI code is added.

## Threat analysis

All operations below are unimplemented. These are constraints for independent
adversarial review, not a review verdict. The [IAM users threat
analysis](iam-users.md#threat-analysis) covers the shared input, secret-file,
generation, transport, and cache paths; this section adds the new requests.

1. **An attacker reads leaked credentials.** Invariant: a reset password
   reaches only the request body and the secret file. Severity: high if a
   400 or 409 echoes the submitted password into an error, capture, or the
   `ErrNotSettled` text. The reset request must set `Sensitive`, `Redact`
   with the submitted value, `WithholdMessage`, and a fixed `ClassifyError`
   (`internal/transport/transport.go:160`, `:174`, `:196`), and its 204 has
   no body to capture. MFA and rename requests carry no secret; the excluded
   `set-up-google` response would, which is why it stays out. Reset Output
   holds a password only when generated. Check every error node, the
   uncertain partial output, debug lines, and the CLI message that names the
   user ID but never the password.
2. **A hostile API peer sends malformed data.** Invariant: a malformed
   state or identity read cannot select a different user or forge a
   confirmation. Severity: high for a wrong target; medium for a skipped
   enable. The target read requires a matching ID and `from`; the MFA state
   read decodes under the [strict rule][strict] with a 256 KiB cap. A peer
   that falsely reports the requested state makes
   the operation a no-op; that is the server lying about its own state and
   is accepted, since the same server would also lie to a confirm read. A
   204 with a body is ignored under a 64 KiB cap. Check missing, null,
   wrong-type, duplicate, and case-alias factor keys, canaries in rejected
   bodies, and that a rename confirm never accepts a name match on another
   ID.
3. **An attacker redirects a request or changes an endpoint.** Invariant:
   the password body is sent once to the configured origin. Severity: high.
   `Once` refuses every redirect (`internal/transport/transport.go:429`),
   and `internal/transport/redirect.go:11` checks scheme and host around
   caller hooks. Test a 307 and 308 on the reset path with no replay, and
   foreign-host reads before a write.
4. **A local attacker tampers with files or cached identity.** Invariant:
   input and output files follow create's private-file rules, and a rename
   never edits a profile or cache. Severity: high for a redirected secret
   file. `internal/core/load.go:175` and `internal/cli/secretfile.go:40`
   provide the checked paths. After a rename the old cache entry becomes
   unreachable by name, and the profile fails to sign in rather than reuse
   a stale identity; test that no code path rewrites configuration.
5. **A concurrent actor or repeated caller triggers writes.** Invariant:
   one invocation sends at most one reset, rename, or factor request.
   Severity: medium. Two concurrent resets leave the loser holding an
   invalid password with no way to tell; the CLI reports the uncertain
   outcome and the manual check rather than retry. The read-then-send race
   of [ADR 0003](../adr/0003-toggle-writes.md) remains for MFA; the confirm
   read detects it per caller. Check `Once` on every write for 401, 429,
   5xx, redirects, dropped connections, and cancellation before and after
   send, and that the CLI never retries. No paid write exists; a reset
   still revokes a working credential.

[strict]: iam-users-api.md#bounds-and-strict-decoding

## Wiki and release

Ship with the IAM user create and delete release, or after it as a second
release if its evidence lands later; both follow the [IAM users release
order](iam-users.md#wiki-and-release). Document the three operations in
`docs/wiki/IAM.md` and `CLI-IAM.md` through source notes: the two password
modes and length flag, the no-undo reset, uncertain and unsettled recovery,
the guards and permission needs, the profile `username` update after a
rename, and that the tool enrols no factor and cannot see a user's secret.
Rename ships only after its path is evidenced and re-reviewed.

## Owner decisions

All recommendations are proposals, not approvals.

1. **Privilege guard.** Recommend `ErrPrivilegedChange` for reset, rename,
   and both MFA operations when the target holds IAM write rights directly
   or through a group, as for `ResetServiceAccountSecret`. Alternative: self
   and origin only, as for delete. Accepting the recommendation keeps the
   attachment readers in the [bounds and strict-decoding
   prerequisite](iam-users-api.md#bounds-and-strict-decoding) for these
   operations only.
2. **Rename evidence.** Recommend keeping rename blocked until a published
   path or a manager-captured console request exists, then re-reviewing the
   contract. Alternative: drop rename. Delete followed by create is not a
   rename: it changes the ID and loses policies, groups, and the password.
3. **MFA subset.** Recommend disable and enable for `email` and `google`.
   Alternatives: disable only; also `set-up-email` and
   `send-verification-email` for administrator-assisted enrolment, which
   lets the administrator choose the address; also `set-up-google`, which
   returns the user's TOTP secret to the administrator and is not
   recommended.
4. **MFA state in output.** Recommend `UserDetail.MFA` with `Verified` and
   `Enabled` per factor and no `Address`, since the address is contact data
   the operations do not need. Alternative: include `Address` for
   operators who manage mailboxes.
5. **Consent.** Recommend `--yes` on reset, rename, and disable, and none
   on enable. Alternative: `--yes` on all four.
6. **Uncertain outcome sentinel.** Recommend one new `ErrUserWriteUncertain`
   for reset, rename, and MFA sends whose outcome is unknown, beside the
   decided `ErrUserCreateUncertain`. Alternative: one shared sentinel for
   every IAM user write, renamed before anything ships.
7. **Own credentials.** Recommend excluding the current-user reset and MFA
   paths. Alternative: add `reset-my-password` with its own cache
   invalidation design.
8. **Probe scope.** Recommend probing reset and the MFA state read on the
   disposable user only, with email set-up against an owner-controlled test
   mailbox when the owner names one, and no Google set-up. Alternative:
   also set up Google on the disposable user, holding the returned secret
   only in memory, to evidence `deactivate-google` on an enabled factor.
