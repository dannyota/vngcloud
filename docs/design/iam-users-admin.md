# IAM user administration design

Status: Accepted. The owner decided the follow-ups under
[Owner decisions](#owner-decisions). The [probe
evidence](iam-users-admin-api.md#probes) closed the reset acceptance and
target-shape gates; verified-factor MFA transitions stay gated. The approved
probe scope is recorded in decision 8; this document grants no broader live
call, sign-in, or cleanup authority.

Administrator password reset is implemented; MFA enable and disable for IAM
users remain proposed for the public `iam` package and CLI, beside [create and
delete](iam-users.md). The caller acts on another user under the same root
account; current-user credential changes stay out.

[API and checks](iam-users-admin-api.md) owns wire evidence, types, validation,
outcomes, tests, and probes. [IAM users](iam-users.md) owns password input,
generation, secret files, and the caller, self, and origin guards. Follow [write
conventions](../adr/0002-write-api-conventions.md) and the [IAM guard
model](iam-writes.md#guards). The SDK stays standard-library only; the CLI calls
only the public SDK. No dependency is added.

## Scope

- `ResetUserPassword`: published administrator password replacement.
- `DisableUserMFA` and `EnableUserMFA`: published activate/deactivate paths for
  `google` under [decision 8](#owner-decisions); `email` remains deferred.
  Each factor's state and transition evidence gates its release separately.

Exclude current-user reset and MFA, MFA set-up, verification and verification
mail, account activate/deactivate, tags, IP allow lists, identity providers, and
quota writes. Set-up can give an administrator control of the user's second
factor. No SDK method or CLI fallback may invoke an excluded path.

## Operations

### Password reset

`ResetUserPassword` submits a supplied or generated replacement password. The
observed success is 204 with an empty body, after which the old password was
refused and the new one signed in. Treat the old password as unusable after
acceptance and potentially unusable after an uncertain send. Session and token
revocation, forced password change, and MFA interaction remain unverified. No
user read proves which password works; this operation is not a
session-revocation mechanism.

Reuse [create's credential rules](iam-users.md#credentials): default length 32,
selectable 8 to 50, `Secret` inputs and outputs, private file or bounded stdin
input, and exclusive generated-secret persistence. Supplied passwords are never
returned. Retain generated passwords on acceptance and uncertainty. No password
may appear in argv, stdout, stderr, debug, errors, or captures. The [reset
privacy prerequisite](iam-users-admin-api.md#reset-privacy) covers the entire
guard request graph, not just the POST. Generate only after guards.

After the SDK call, save a generated password even with an outcome error. Report
separately whether the reset was accepted or remains uncertain and whether the
file was saved. A file failure returns `SecretFileFailed`, keeps any outcome
sentinel in the error chain, and names the validated user ID and manual
`reset-user-password` command with a fresh private destination and `--yes`.
Never say "password replaced" for an uncertain send. Do not retry, roll back to
the old password, delete the user, sign in, or send any request after the file
failure. Supplied mode creates no file. Process termination before persistence
can lose the only copy of a generated password.

A deliberate later reset can restore access but invalidates another password;
coordinate it with the user and other administrators. A fresh sign-in is an
optional manual diagnostic, never automatic recovery or proof of revocation of
previously issued tokens. Probe sign-in needs separate owner approval.

### MFA

The API separates set-up, verify, activate, and deactivate. A user get carries
`verified` and `enabled` for the email, Google Authenticator, and SMS factors;
every value was false on a user with no factor set up, the activate and
deactivate paths returned 400 for that user, and no verified or enabled factor
has been observed. Console defaults never prove a factor exists, is verified,
or is disabled. Unknown state is distinct from false under the [state
model](iam-users-admin-api.md#state-model).

Product scope is activate/deactivate of existing verified factors. The console
consumes
`secret` and `qrCodeUrl` from administrator Google set-up; an administrator
running it obtains material sufficient to copy the user's time-based one-time
password (TOTP) factor. Email set-up accepts an administrator-chosen mailbox,
which can redirect the second factor. Verification proves access to that mailbox
or secret, not that the intended user controls it. Both set-up flows stay
excluded from the product even if the caller has server permission. Decision 8
authorizes a Google enrolment probe only, not product set-up or verify commands.

Disabling MFA can complete an account takeover for someone who already knows the
password. Enabling a factor can lock out a user or activate an attacker-enrolled
factor. Recommend `--yes` for both, a protected-user guard, and enabling only
when the selected factor explicitly reports `verified: true`. Verification is
not an ownership guarantee; operators must establish the intended user's control
outside this tool. No consent flag bypasses a guard, and CLI consent is not a
new server security boundary.

These endpoints name a desired state. They are not evidenced as toggles even
though the console calls its helper `toggle2FA`. Adopt the read-first,
send-once, confirm-by-read discipline of [ADR
0003](../adr/0003-toggle-writes.md) conservatively, without claiming its
flip-twice race. Run all guards before an already-in-state no-op. Missing state
refuses the operation; enabling also requires verification before the no-op
check. Disable does not require verification, so a known enabled but unverified
factor can be disabled.

Send at most once, confirm once, and return `ErrNotSettled` after a 204 whose
confirmation fails. A deliberate repeat re-reads state and guards; it is not a
hidden retry. Concurrent opposite-state requests may overwrite each other, and a
confirmation proves only state at that instant. Unknown effects on account-wide
MFA requirements and sessions must be documented; neither enable nor disable
promises the user can sign in.

## Guards

Validate locally, then apply the [shared ordered guards](iam-users.md#guards)
with no mutation on refusal: classified caller, self, matching target ID and
`from: "iam"`. Root and unknown callers return `ErrPrivilegedChange`; an IAM
caller without a usable ID fails closed. "Before any request" here means before
the write, since guards perform reads. Self returns `ErrSelfChange` before
target or privilege reads. Target 404 remains `NotFound`; failed or ambiguous
reads never authorize a write. Service-account callers cannot be the target IAM
user but still need classification and target checks.

Recommend the same additional protection for reset, enable, and disable:
obtain `guardWriteActionNames`, call `userIsProtected` with a fresh
per-operation policy map, and map protection to `ErrPrivilegedChange`.
`iam/policy_guard.go:269` checks direct policies and every returned group's
policies. `iam/guard.go:186` refuses an empty action classification; its caller
and action caches are per client. `statementsArePrivileged` (`iam/guard.go:136`)
treats a null statement, an unknown effect, and an `allow` statement with a
missing, null, or empty action list as privileged under the [fail-closed
statement rule](iam-writes.md#rules), and every read in that graph decodes
strictly within its bound ([IAM writes: API](iam-writes-api.md#bodies)), so
this guard inherits both. Do not weaken unknown-action, malformed-statement,
incomplete-page, or failed-read refusals.

Resetting a protected user's password transfers its sign-in; disabling its MFA
weakens it; enabling MFA can lock it out. This matches the purpose
of `ResetServiceAccountSecret`'s guard (`iam/service_accounts_write.go:260`,
`iam/guard.go:497`). Delete retains the owner's lighter self/origin rule. No
override flag exists. Server permission is still necessary: checks and writes
are not atomic, cached identity/actions can age, and the guard does not evaluate
non-IAM privileges or prove absence of every escalation route. Recommend
dedicated least-privilege profiles and external coordination; neither is a new
claim about server behavior.

## CLI

All commands run under `vngcloud iam` and register as writes. Read-only refuses
before password input, generation, or file creation; flag/JSON shape checks may
run first under the shared `beforeInput` contract.

| Command | Flags | Consent |
|-|-|-|
| `reset-user-password` | User ID and password source/length | `--yes` |
| `disable-user-mfa` | `--user-id`, `--method google` | `--yes` |
| `enable-user-mfa` | `--user-id`, `--method google` | `--yes` |

Reset requires `--user-id` and exactly one of `--password-file` and
`--secret-file`; `--password-length` is only for generation. It reuses create's
`beforeInput`, JSON password exclusion, input deadline, and preflight rules.
This consent recommendation adds protection to reversible MFA changes because of
their credential impact; it does not redefine ADR 0002 globally.

```sh
vngcloud iam reset-user-password --user-id <user-id> \
  --secret-file <private-directory>/password --yes
vngcloud iam reset-user-password --user-id <user-id> --password-file - \
  --yes < <private-directory>/password
vngcloud iam disable-user-mfa --user-id <user-id> --method google --yes
```

Reset prints a redacted generated `Password` and `SecretFile` only when saved;
supplied mode prints neither. MFA prints
`Method`, nullable `Enabled`, and `Changed` under the API contract. Errors print
no partial object. Recovery may name the validated user ID and local path, both
account data for live-report sanitation.

Shape, flag, input-file, and read-only errors exit 2. Guards, unknown MFA state,
unverified-enable refusal, conflicts, and secret-file failures exit 1.
`ErrNotSettled` and `ErrUserWriteUncertain` always exit 1 and take precedence
over nested authentication or not-found errors. Ordinary authentication errors
exit 3, target 404 exits 4, and permission errors exit 1. Reuse existing
`SelfChange`, `PrivilegedChange`, `NotSettled`, `RequestFailed`, and
`SecretFileFailed` codes; add no CLI code.

## Threat analysis

These are design obligations, not an independent review. MFA paths remain
unimplemented; reset is implemented in `iam/users_admin.go`. [Shared
threats](iam-users.md#threat-analysis) cover generation,
input, and persistence; [checks](iam-users-admin-api.md#unit-tests) cover each
path below.

1. **Leaked credentials.** Invariant: reset secrets and bearer tokens reach no
   managed diagnostic or capture path. High severity: a guard failure or
   uncertain reset echoes a supplied password. Transport controls at
   `internal/transport/transport.go:170`, `:183`, `:202`, and `:596` protect
   individual requests, not a graph. Preserve the private reset reader controls;
   withhold server text and suppress captures on every dependent read and send,
   then make no recovery request. MFA carries no password but uses
   sensitive reads/sends and fixed errors against hostile secret-bearing
   responses. A peer can also return a sent bearer token as an `id` or
   `username`; `Sensitive` does not sanitize decoded output, so the
   [credential rule](iam-users-admin-api.md#error-identity-and-bounds) refuses
   such values before retention or reuse. Test all error nodes, debug,
   captures, partial output, and file-failure reporting. Product set-up stays
   excluded
   to avoid TOTP secret custody and attacker-mailbox enrolment; the approved
   probe keeps enrolment material in memory under decision 8.
2. **Hostile API peer.** Invariant: malformed identities, policy lists, or
   factor state cannot authorize a write or forge confirmation. High severity:
   missing factor booleans default to false, or a truncated group list hides
   privilege. `iam/guard.go:256`, `:427`, and `iam/policy_guard.go:269`
   establish the existing fail-closed, strictly decoded, bounded reads, and
   `iam/caller.go:39` sets the same flag and cap. The new target and
   factor-state reads must decode the same way; preserve unknown state and
   validate ID/origin again on confirmation. Test nulls, wrong types, aliases,
   duplicates, caps, and
   misleading 204 bodies. A peer lying about its own state or membership cannot
   be defeated by a second read from it.
3. **Redirected endpoint.** Invariant: reset and factor writes send
   once to the configured origin, with normal TLS verification. High severity:
   redirects steal a password or bearer token.
   `internal/transport/transport.go:440` refuses redirects under `Once`;
   `internal/transport/redirect.go:11` checks scheme and host around hooks. Test
   301/302/303/307/308 and foreign-host guard and confirmation reads. Endpoint
   overrides and custom HTTP transports remain trusted caller code
   (`internal/core/client.go:375`), not a provider-host allowlist.
4. **Local tampering.** Invariant: private reset files, bound caller identity,
   and no silent profile/cache edits on any operation. High severity: a writable
   parent swaps the destination, or an old cached token masks a failed fresh
   sign-in after reset/MFA changes. `internal/cli/secretfile.go:40`,
   `:80` protect exclusive creation, not hostile parents;
   `internal/core/load.go:175` checks credentials;
   `internal/tokencache/cache.go:43`, `:158` key and read cached tokens without
   proving server identity or revocation. Test collisions, file errors,
   cancellation, no configuration/cache mutation, and fresh-login isolation in
   any approved probe. Do not claim local cache invalidation revokes remote
   sessions or that the cache key binds a password.
5. **Concurrent or repeated writes.** Invariant: at most one mutation per
   administration invocation and no automatic cleanup or rollback. High
   severity: reset transfers sign-in or MFA disable assists takeover; medium
   severity: concurrent reset loses a password or MFA enable locks out a user.
   `internal/transport/transport.go:162` and `internal/transport/errors.go:420`
   supply `Once` and conservative `NotSent`; protection follows
   `iam/policy_guard.go:269`. Check 401, 429, 5xx, drops, cancellation,
   privilege changes between read and send, and opposing MFA writes. A
   successful reset may be overwritten immediately; neither client can
   necessarily detect it. No quote exists; free use remains a pre-run
   assumption, and no account or paid-resource change is authorized as recovery.
   All writes still require server permission.

## Wiki and release

Create/delete and reset are implemented. MFA ships separately after its own
evidence gates, with SDK, CLI, wiki, reviewed live helpers, and authorized
verification together. Apply the method gate in [decision 8](#owner-decisions).
No placeholder command or guessed method ships.

Update `docs/wiki/IAM.md`, `Services.md`, and `Configuration.md`; generate
`CLI-IAM.md` through source notes. Document passwords and partial outputs,
no-undo reset, manual recovery, guard permissions, MFA takeover/lockout risk,
and unknown token revocation. State that no factor is enrolled
and no cache is repaired automatically. Follow [release
gates](iam-users.md#wiki-and-release); no publication bypass is authorized.

## Owner decisions

The owner accepted each recommendation below, including refinements of the
first eight. None is covered by the create/delete decisions merely by
association.

1. **Privilege guard.** Recommend protected-user refusal for reset and
   both MFA operations, including direct and group IAM write rights.
   Alternative: self/origin only, accepting sign-in takeover and lockout
   exposure. Preserve delete's decided exception.
2. **Rename evidence.** Decided: drop IAM user rename from scope. The
   [recorded refusal and missing action](iam-users-admin-api.md#calls) leave no
   accepted contract for the SDK's supported callers. No rename SDK method,
   CLI command, live branch, probe step, or profile-username guidance remains
   proposed. Delete-and-create is never a fallback.
3. **MFA subset.** Recommend enable/disable of existing email and Google
   factors. Alternatives: disable only; console-only recovery; add email
   set-up/verification with mailbox-takeover risk; add Google set-up with TOTP
   secret custody. The latter two need a new security design.
4. **MFA state in output.** Recommend nullable factors and nullable booleans in
   `UserDetail.MFA`, no address, and nullable confirmed `Enabled` in operation
   outputs. Alternatives: keep state private; expose contact data with separate
   justification. Missing state must never become false.
5. **Consent.** Recommend `--yes` on reset, disable, and enable due to
   sign-in impact. Alternative: no consent on enable under the general
   reversible-write rule. Neither permits a guard override.
6. **Uncertain outcome sentinel.** Recommend `ErrUserWriteUncertain` for reset
   and MFA, beside the decided `ErrUserCreateUncertain`. Alternative:
   unify all user-write uncertainty before release. Keep `ErrNotSettled` for
   accepted writes with failed confirmation/response handling; never hide the
   outcome when persistence also fails.
7. **Own credentials.** Recommend excluding all current-user reset/MFA paths.
   Alternative: a separate self-service design including cache and credential
   replacement, never reuse the administrator bypass.
8. **Probe scope.** Decided: approve an in-memory Google Authenticator
   enrolment probe on the disposable user: `set-up-google`, `verify-google`
   with a TOTP code computed in memory, then activate and deactivate
   transitions. The secret stays in process memory; neither it nor the QR URL
   may be fetched into tooling, logged, or persisted. Never fetch the QR URL.
   Email stays deferred because no mailbox is available. MFA ships the Google
   method only once the [probe evidence](iam-users-admin-api.md#probes) is
   recorded and reviewed; the email method stays blocked. Decision 3's product
   scope is unchanged: enable and disable of existing verified factors.
   Set-up and verify are probe-only, never SDK methods or product commands.
   Probe source still needs independent review before the manager executes it
   within the approved scope; this decision is not evidence of a transition.
9. **Reset privacy prerequisite.** Recommend private request-local privacy
   controls throughout the reset guard graph before release. Alternative: defer
   reset. POST-only privacy does not meet the credential boundary.
10. **Verified enable.** Recommend refusing enable unless verification is
    explicitly true, even on a no-op. Alternative: delegate verification
    entirely to the server. Neither proves the intended user's possession.
11. **Release gates.** Recommend independent administration slices, with unknown
    token revocation and password-policy breadth documented rather than claimed.
    Alternative: wait for all administration evidence and behaviors. Target
    schemas, guards, privacy, and chosen transitions stay hard implementation
    gates in either case.
