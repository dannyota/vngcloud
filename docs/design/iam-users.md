# IAM users design

Status: Proposed. [Owner decisions](#owner-decisions), independent design and
plan review, and the [evidence probes](iam-users-api.md#probes) gate
implementation. This document authorizes no live call or cleanup.

Add IAM user create and delete, `GetUser`, and `ListUsers.Name` to the public
`iam` package and CLI. Service accounts already have `create-service-account
--secret-file`. An IAM user is a separate sign-in identity under an existing
root account, not a new root account.

[API and checks](iam-users-api.md) owns wire evidence, public types, validation,
retries, partial outputs, and tests. Follow [write
conventions](../adr/0002-write-api-conventions.md), the [IAM guard
model](iam-writes.md#guards), and [CLI secret files](cli.md#secret-files). The
SDK stays standard-library only; the CLI calls only the public SDK. No
dependency is added.

## Scope

Create and delete are the only proposed exceptions to the [IAM user write
exclusion](iam-writes.md#non-goals). Password reset, rename, activate,
deactivate, MFA enrolment, IP allow lists, tags, identity-provider management,
and public quota operations stay out. Creation accepts no policies, groups, or
access settings. Existing guarded attachment commands grant access separately.
Do not promise that a new user can sign in until the password and account
sign-in requirements are verified. This feature does not provision MFA or bypass
account controls.

## Credentials

A password must not enter argv, stdout, stderr, debug logs, errors, captures, or
public fixtures. Inputs and outputs use `vngcloud.Secret`. Use `Reveal` only at
request serialization and the secret-file write. The API sibling defines
request-local redaction and error withholding; `Secret` alone does not protect a
raw request body or a server echo.

The SDK generates a password when `Password` is empty. Supplied passwords are
never returned; generated passwords remain in the partial output even when the
create outcome is uncertain. Generation uses cryptographic randomness and the
[specified alphabet](iam-users-api.md#generation). Custom policy rejection must
not trigger regeneration or an automatic second create.

The CLI requires exactly one of `--password-file <path>` and `--secret-file
<path>`. `--password-file -` reads stdin. No password flag, environment-variable
shortcut, or `Password` in `--cli-input-json` is accepted. Flag validation and
secret-path preflight run before requests. Read-only refusal runs before
password input, generation, or file creation. Read the password in the
operation's call hook, after the existing read-only checks, rather than in its
early guard hook.

Password input follows these proposed rules:

- Open a named file without blocking on a FIFO; inspect the opened file,
  require a regular file, and on Unix refuse group or other permissions.
  A symlink to a regular private file is allowed, matching credential input
  conventions. The resolved file and its directories must be trusted.
- Bound file and stdin input to 4096 bytes plus one overflow-detection
  byte. Remove exactly one final LF or CRLF. Preserve spaces and reject an
  empty result, embedded controls, invalid UTF-8, and overflow locally.
- Refuse a terminal for stdin. Require EOF and honor command cancellation.
  A pipe can stall despite a byte cap: impose a 30-second input deadline
  and return a fixed usage error without content. Test a pipe that never
  closes.
- Run a `beforeInput` hook, a new `Op` field, as the first step of `runOp`,
  before `--cli-input-json` or any other input is consumed. For
  `create-user` it requires exactly one of the two password flags and,
  with `--password-file -`, refuses any `--cli-input-json` value that
  starts with `file://`, so stdin can never be shared with the JSON input.
  The shared JSON file reader opens its path without blocking and refuses
  a non-regular file, closing the stalled-pipe hang for every command.
  Both checks run before read-only, which runs before any stdin read.
- Clear owned byte buffers after use and release secret references early.
  Go strings, JSON serialization, HTTP buffers, and `Secret` can retain
  copies. Do not claim secure erasure or immunity to process-memory reads.

### Secret file and cleanup

Preflight the destination under the [existing secret-file
contract](iam-writes.md#secret-file). After the SDK call, save a non-empty
generated password even with an accepted-but- unconfirmed or uncertain outcome.
The error must distinguish those outcomes and state whether the file was saved;
it must not claim the password belongs to a user found only by name.

Create exclusively with `O_CREATE|O_EXCL`, mode 0600, and `O_NOFOLLOW` where
supported. Write the password and one newline, check write, sync, and close
errors, and remove only a partial file created by this call. Never remove an
existing destination after an exclusive-open failure. Cleanup failure must
report that a partial file may remain. Parent paths are trusted configuration:
the shared helper follows parent symlinks and does not secure an
attacker-writable directory. Use a private directory; Unix mode bits do not
establish a Windows access-control guarantee.

After a file failure, `create-user` runs no automatic cloud cleanup, unlike
`create-service-account`. A cleanup would call `GetUser` and `DeleteUser`,
whose guard reads share `iam/guard.go` and carry no request-local password
redaction, `Sensitive` flag, or fixed error text; the transport captures their
responses (`internal/transport/transport.go:587`) before the CLI could discard
a cleanup error. Extending that privacy boundary across the whole guard graph
is a shared change this slice does not make. Instead the CLI reports
`SecretFileFailed` with `CreatedID` when the create returned one, otherwise
the requested username, and the exact `delete-user --user-id <id> --yes`
command. The orphan holds no policy or group, and its generated password
exists nowhere once the process exits.

A list match, prefix, or supplied password does not prove this invocation
created the user; never delete such a match automatically. Process termination
between creation and file persistence remains an unrecoverable-password risk;
document it.

## Guards

`CreateUser` has no caller privilege guard, like `CreateServiceAccount` and
`CreateGroup`; CLI read-only still applies. This relies on creation adding no
permissions or memberships, which the probes must establish. The server must
authorize creation. The SDK has no profile read-only boundary; read-only is a
CLI policy.

`DeleteUser` validates the input ID, then performs these checks before sending
DELETE. Refusals may perform reads but never a mutation.

1. Require a classified caller through `requireClassifiedCaller`:
   `iam-user`, `user-sa`, or `service-sa`. Root and unknown types return
   `ErrPrivilegedChange`. An IAM caller with no usable ID fails closed.
2. For an IAM caller, compare target and caller IDs case-insensitively.
   Return `ErrSelfChange` before any target, origin, or privilege read.
3. Read the target. Require the returned ID to match the requested ID and
   `from` to equal `iam`. Missing, null, unreadable, or unknown origin
   fails closed with a fixed guard error. A target 404 stays `NotFound`.
4. Apply `userIsProtected` using the existing action classification and
   direct and inherited policy checks. Return `ErrPrivilegedChange` for
   privilege and propagate failed reads without sending DELETE.
5. Read policy and group attachments through the fail-closed guard
   readers. Any remaining policy, including a deny-only policy, or any
   group membership returns `ErrInUse`.

Self outranks every other applicable refusal. Privilege outranks in-use when the
privilege reads can prove it. Missing policy data, invalid policy IDs,
incomplete attachment pages, null group lists, and missing group `policies`
never count as empty. Every read a delete depends on, including the target
read, caller identity, the action list, each policy, and every attachment
reader in `iam/guard.go`, validates the raw body with the
[strict JSON rule](iam-users-api.md#bounds-and-strict-decoding) before
decoding: a duplicate key, or two keys equal under case folding, refuses the
delete. Go's decoder otherwise lets a later `data` or `DATA` member replace an
earlier attachment list with an empty one. Reuse the existing classification;
do not replace it with managed-policy names. The user-group reader does not
check group mode or membership fields like the full group reader does, but any
membership still prevents this delete.

A service-account caller cannot be the target IAM user. It still needs
classified identity and all target checks; this does not relax the existing
blanket refusal of service-account-targeted writes by such a caller. No input or
flag bypasses guards. The owner's IAM user remains protected through IAM write
rights. The tool does not protect every nonprivileged user from an authorized
delete.

Checks and deletion are not atomic. Cached caller identity and action names are
per client; target attachments are read per operation. A policy or membership
can change after the reads, including during a transport retry. Server
authorization is the security boundary. A server 409 remains possible after
empty attachment reads and must remain an error.

## Write outcomes

The [API outcome contract](iam-users-api.md#writes) distinguishes local refusal,
definite server rejection, accepted creation, and an uncertain send. `Once`
forbids every create replay. `NotSent` is private transport evidence, not an
exported SDK error marker or a synonym for `Retryable`.

After an uncertain create, retain the generated password and reconcile with
`list-users --name <username>`, comparing exact usernames and IDs. Neither a
match nor an empty result proves which concurrent call won or whether a delayed
create will appear. Do not retry, delete, reset, or claim a supplied password
works merely because a name matches. Resolve ownership and current state before
a deliberate new create.

Delete uses existing idempotent DELETE retries, not `Once`. A repeated call
re-runs guards; retries within one call do not. A retry ending in 404 returns
`NotFound`, not success. After an uncertain delete, inspect the same ID with
`get-user`; do not target a replacement by username. There is no asynchronous
wait or polling loop.

The inspected API has no quote or price field. Free IAM user management is an
assumption to confirm before any live write, not proven by absence of pricing
fields. Quota, permissions, cleanup authority, and account controls remain
gates. No paid resource is created by this feature.

## CLI

All commands run under `vngcloud iam`. Register `get-user` as a read,
`create-user` as a write, and `delete-user` as a destructive write.

| Command | Flags | Consent |
|-|-|-|
| `get-user` | `--user-id` | None |
| `list-users` | Adds `--name`; paging unchanged | None |
| `create-user` | `--username`, one password source flag | None |
| `delete-user` | `--user-id` | `--yes` |

Success prints the user with `Password` redacted and `SecretFile` only when
written. Supplied-password mode prints no secret path. Errors print no partial
success object; recovery text may name the requested username, a validated ID,
and the destination path. These are account data, so live run reports must
sanitize them.

```sh
vngcloud iam create-user --username <username> \
  --secret-file <private-directory>/password
vngcloud iam create-user --username <username> --password-file - \
  < <private-directory>/password
vngcloud iam delete-user --user-id <user-id> --yes
```

Use file redirection in examples, never a secret in a shell command or `printf`
argument. Shape and file errors and read-only refusal exit 2. Guard refusals,
conflicts, and file failures exit 1. Ordinary authentication errors exit 3,
target not-found exits 4, and permission failures exit 1. Both create outcome
sentinels always exit 1, even when a confirmation read failed with 401 or 404.
Test this precedence in CLI error mapping. Reuse `RequestFailed` and
`SecretFileFailed`; no new CLI code is required.

## Threat analysis

All user operations below are unimplemented. These are design constraints for
independent adversarial review, not a review verdict. Evidence refers to
existing paths; required adversarial cases live in [unit
tests](iam-users-api.md#unit-tests).

1. **An attacker reads leaked credentials.** Invariant: managed output
   paths never disclose passwords or tokens. Severity: high if a rejection
   echoes a password into an error code, message, capture, or confirmation
   result. `internal/transport/transport.go:160` suppresses sensitive
   captures and decode text; `:174` withholds messages; `:196` can replace
   server codes; `:606` logs method, redacted path, status, and duration.
   Proposed create and its private confirmation requests must use those
   controls and redact the submitted password, not just bearer tokens;
   `GetUser` sets `Sensitive`, and no cleanup request follows a create.
   Check nested escaped echoes, all error-chain nodes, and partial outputs.
   `secret.go:19` explicitly excludes `%p`, gob, and reflection from its
   guarantees. Never use those paths; raw request structs are not safe to
   format. The shared security policy's crafted-encoding limit remains
   relevant outside the fixed-text create error boundary.
2. **A hostile API peer sends malformed data.** Invariant: malformed
   identity or incomplete lists cannot authorize deletion or attribute a
   password to a stranger. Severity: high for a wrong delete target;
   medium for memory exhaustion. `iam/caller.go:88` drops list totals;
   a private presence-aware decoder must check confirmation completeness.
   `iam/guard.go:240` and `:397` fail closed on incomplete attachments.
   These shared reads currently omit byte caps and decode with
   `json.Unmarshal` (`internal/transport/transport.go:262`), where a later
   duplicate or case-alias member replaces an earlier one. The
   [bounds and strict-decoding prerequisite](iam-users-api.md#bounds-and-strict-decoding)
   must land before reuse. Check caps, wrong IDs, duplicate exact names,
   missing keys, duplicate and case-alias keys, partial JSON, and failure
   propagation. Never let an inferred console default satisfy a guard.
3. **An attacker redirects a request or changes an endpoint.** Invariant:
   no credentials cross the configured origin on redirect and TLS verifies
   the peer with the standard transport. Severity: high for password and
   token theft. `internal/transport/redirect.go:11` checks scheme and host
   before and after caller redirect hooks; `transport.go:429` refuses
   all redirects on `Once`. `internal/core/client.go:374` accepts supplied
   HTTP clients and transports. Endpoint overrides, custom transports,
   credential providers, and TLS settings are trusted caller code, not a
   provider-host allowlist. Test foreign hosts, scheme changes, hook URL
   mutation, and no create replay. This slice adds no endpoint or TLS
   weakening switch and does not claim to secure malicious caller hooks.
4. **A local attacker tampers with files or cached identity.** Invariant:
   use private secret files and the configured caller's identity; never
   cache the new user's password or sign in as it during normal create.
   Severity: high when tampering redirects credentials or impersonates a
   different caller. `internal/core/load.go:175` checks opened credential
   files and Unix permissions; `internal/tokencache/cache.go:30` binds
   cache names to profile, root email, username, and login endpoints.
   `:238` checks directory mode, but `:157` reads cache files without an
   identity proof or per-file permission check. A writable profile or
   cache and same-user process compromise remain outside the guarantee.
   `internal/cli/secretfile.go:40` and `:80` protect exclusive creation,
   not hostile parent replacement. `internal/cli/op.go:399` consumes
   `--cli-input-json` before any guard, and `internal/cli/input.go:117`
   opens its `file://` path with a byte cap but no regular-file check, so
   a never-closing pipe stalls the command; the `beforeInput` hook and the
   regular-file rule above close that. Test input permissions, FIFO swaps,
   destination collisions, write/sync/close/removal failures, cancellation,
   a shared stdin, and saved-password reporting.
5. **A concurrent actor or repeated caller triggers writes.** Invariant:
   one create invocation sends at most one POST and no delete the caller
   did not request. Severity: high for deleting another actor's user;
   medium for orphan identities or lost passwords.
   `internal/transport/transport.go:147` enforces `Once`;
   `internal/transport/errors.go:418` conservatively tracks `NotSent`,
   including custom transports. Check 401, 429, 5xx, redirects, dropped
   connections, and cancellation before and after send. DELETE retries
   use earlier guard reads, so concurrent attachments remain a race.
   `internal/cli/svc_iam.go:233` supplies the bounded detached cleanup
   precedent that `create-user` deliberately does not reuse. No prefix
   sweep, automatic cleanup, hidden detach, or repeated create is allowed.
   There is no quote to race; creation still consumes quota and creates a
   sign-in principal.

## Wiki and release

Ship SDK reads and writes, CLI, and user docs together after evidence, owner
approval, implementation of the live tests, independent adversarial review, and
authorized live verification. Review all code before any live run, including the
live helpers and the pre-code probe source, each by a separate reviewer with fix
confirmation. The manager owns Git and release gates. A read-only release first
is an alternative if write evidence stays gated.

Update `docs/wiki/IAM.md`, `Services.md`, and `Configuration.md`; generate
`CLI-IAM.md` through its source notes. Include the two password modes,
partial-output checks, uncertain recovery, guards, permission needs, policy
attachment, root email and username configuration, password input through stdin,
and setting `read_only` last. Do not imply MFA is configured. Record
optional-field and body-cap compatibility limits from the API sibling. No wiki
publication bypass is authorized.

## Owner decisions

All recommendations are proposals, not approvals.

1. **Scope.** Recommend create and delete only, plus their supporting
   reads. Keep reset, activation, rename, MFA, tags, quota, and identity
   provider writes separate. Alternative: service accounts only.
2. **Password source.** Recommend either supplied file/stdin or generated
   secret file, exactly one. Alternative: generated only for less CLI
   surface. Require private input files on Unix and bounded stdin waits.
3. **Generation.** Recommend SDK generation at 32 characters. Alternative:
   CLI-only generation or 50 characters. A custom minimum above 32 may
   reject the default; supply a compliant value without automatic retry.
4. **Delete rules.** Recommend self, origin, privilege, then all
   attachments refused as specified. Alternative: refuse only protected
   users and let the server handle attachments. Empty reads cannot rule
   out a server 409; retain it as a conflict.
5. **Create guard.** Recommend no classified-caller guard, consistent with
   other empty-principal creates, conditional on evidence of no initial
   rights. Alternative: require a classified caller as for guarded writes.
6. **Input validation.** Recommend shape only, allowing password spaces;
   keep console length and class rules informational. Alternative: mirror
   the console's mutable validation and risk rejecting valid API values.
7. **Partial outcomes.** Recommend a new `ErrUserCreateUncertain`, retained
   generated passwords, and explicit `CreatedID` provenance. Keep
   `ErrCreateUnconfirmed` for 201 followed by failed confirmation.
   Alternative: discard an uncertain password and force manual recovery.
8. **Cleanup authority.** Recommend no automatic cloud cleanup after a
   secret-file failure: report `CreatedID` and the manual `delete-user`
   command, as in [Secret file and cleanup](#secret-file-and-cleanup). The
   orphan holds no rights and an unknown password. Alternative: automatic
   cleanup by `CreatedID` behind an SDK-internal privacy boundary that
   applies password redaction, capture suppression, and fixed errors to
   every guard read, as a separately briefed shared change with
   password-echo tests across the whole cleanup graph.
9. **Shared bounds and strict decoding.** Recommend one separately briefed
   prerequisite that adds byte caps and the strict JSON rule to every
   existing guard dependency this feature uses, since oversized or
   ambiguous responses will begin failing for existing guarded writes.
   Alternative: accept the inherited reads explicitly; do not silently
   claim bounds or strictness, or widen this feature's brief.
10. **Error privacy.** Recommend fixed create error text and status-based
    codes, sacrificing server detail to prevent encoded password echoes.
    Preserve status, sentinels, and safe causes for programmatic handling.
11. **Probe sign-in and authority.** Recommend a separate explicit approval
    to sign in as the disposable user without persistent cache changes.
    Confirm cost, quota, account controls, and cleanup authority per run;
    do not infer standing approval from this document.
12. **Release shape.** Recommend one release after all gates. Alternative:
    ship verified reads first with their wiki, then writes separately.
