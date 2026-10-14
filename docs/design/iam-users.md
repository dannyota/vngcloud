# IAM Users Design

Status: Proposed. The [owner decisions](#owner-decisions) are open, and
every write is gated on the [probes](iam-users-api.md#probes).

This design adds IAM user create and delete to `iam`, with the `GetUser`
read and the `ListUsers` name filter they need, and the CLI commands
`create-user`, `delete-user`, and `get-user`. It reverses one bullet of the
[IAM writes non-goals](iam-writes.md#non-goals), which kept every IAM user
write out because such writes change who can sign in. The owner asked for
the CLI to create other IAM accounts, so create and delete come in; every
other user write stays out. Calls, bodies, the SDK surface, tests, probes,
and live checks are in [IAM users: API and checks](iam-users-api.md).

It follows [ADR 0002](../adr/0002-write-api-conventions.md) for every
write, the [guard model](iam-writes.md#guards), and the `vngcloud.Secret`
and `--secret-file` contract of [IAM writes](iam-writes.md#secret-file) and
[CLI secret files](cli.md#secret-files).

## Scope

In scope: `CreateUser`, `DeleteUser`, `GetUser`, `ListUsers.Name`, their
CLI commands, their wiki pages, and the guards below. Out of scope, each
named under [owner decisions](#owner-decisions): password reset, username
change, activate and deactivate, two-factor enrolment, IP allow lists,
identity-provider users, user tags, and the user quota read. IAM users have
no access keys: programmatic access uses a service account, which
[IAM writes](iam-writes.md) already covers.

## Credentials

A password is a secret from the moment it exists. These rules hold in the
SDK and the CLI:

- A password never reaches stdout, stderr, `--debug` output, an error
  message, a capture, a fixture, argv, or shell history. `Password` fields
  are `vngcloud.Secret`, so `fmt`, `slog`, and `json.Marshal` print
  `[redacted]`; the create request sets `Sensitive` and `Redact`, so a
  failing response's text cannot echo the password back.
- The caller either supplies a password or lets the SDK generate one. The
  server has no generated-password option, so generation is local:
  `CreateUser` fills an empty `Password` with 32 characters from
  `crypto/rand` covering the four classes the console requires, and returns
  the value once in `Output.Password`. A supplied password is sent as given
  and never returned.
- The CLI takes a supplied password only from `--password-file <path>`, or
  from stdin with `--password-file -`, never from a flag value or
  `--cli-input-json`, which already refuses every `vngcloud.Secret` field.
  The file must be a regular file of at most 64 KiB, opened without
  following a FIFO; stdin is refused when it is a terminal and capped at
  4096 bytes; one trailing newline is dropped; an empty value exits 2.
- A generated password is written only to `--secret-file`, following the
  [secret file steps](iam-writes.md#secret-file): the path is checked before
  any request, created with `O_CREATE|O_EXCL|O_NOFOLLOW` at mode 0600, and
  written with one trailing newline. If the write fails after the create,
  the CLI removes any partial file, deletes the new user through
  `DeleteUser`, and exits 1 with `SecretFileFailed`; if that delete fails,
  the message names the user by ID, or by username when the create response
  gave no ID.
- Exactly one of `--password-file` and `--secret-file` is given; both or
  neither exits 2 before any request.
- Stdout gets the user without the password: `Password` prints as
  `[redacted]`, and `SecretFile` names the path when one was written.
- A supplied password is the caller's to keep: the CLI writes no file and
  prints no path.

## Guards

`CreateUser` has no guard beyond read-only. A new user holds no policy and
no group, so it changes no one's rights, exactly as `CreateServiceAccount`
and `CreateGroup` do; granting it rights goes through `attach-user-policy`
and `add-user-to-group`, whose guards refuse a privileged policy or a
protected group. The tool therefore still cannot create IAM write rights.

`DeleteUser` runs these checks with reads before any request, in this
order, and sends nothing when one refuses:

| Rule | Refused when | Error |
|-|-|-|
| Caller | The caller's user type is not `iam-user`, `user-sa`, or `service-sa` | `ErrPrivilegedChange` |
| Self | The target ID equals the caller's `UserID`, compared case-insensitively, when the caller is an IAM user | `ErrSelfChange` |
| Origin | `GetUser` shows a `from` other than exactly `iam`, missing included | A guard error naming the rule |
| Privileged | The user holds a privileged policy directly or through a group | `ErrPrivilegedChange` |
| In use | The user holds any policy or belongs to any group | `ErrInUse` |

- The caller's own user is never deletable here, and neither is any user
  with IAM write rights: the owner's user stays protected wherever the tool
  runs.
- The in-use rule makes a delete explicit. The caller detaches and removes
  with the existing guarded commands first, so a privileged user can never
  reach the delete through those commands either, and the unknown meaning
  of the server's 409 on delete never matters for a user this tool
  deletes.
- The origin rule fails closed on a user this design cannot classify: an
  identity-provider user is out of scope, as idp groups are.
- Guard reads use the fail-closed readers of `iam/guard.go`: a short
  attachment page, a null group list, or a group with no `policies` field
  refuses instead of being read as empty.
- A service-account caller cannot be an IAM user, so the self rule does
  not apply to it; the server decides whether it may delete users.
- There is no flag or Input field that turns a guard off. What stays open
  is the same as for every pre-read guard: the checks and the delete are
  not atomic, and a console change between them goes unseen.

## Write rules

- `CreateUser` sends at most once (`transport.Request.Once`): a resend
  after a 401, a 429, a 5xx, a failed stream, or a followed redirect could
  create a second user. It also sets `NotSent`, so an error raised before
  any byte reached a connection is returned as is and the call may be
  repeated, while a 5xx or a network error after sending is reported as
  uncertain. The CLI never retries a write.
- After an uncertain outcome the caller runs `list-users --name
  <username>` and matches the username exactly. A user found that way with
  a generated password is unusable, since no one holds the password:
  delete it and create again. A user found with a supplied password is
  complete.
- After a 201 the SDK reads the user back, by ID when the response holds
  one and otherwise by exact username through `ListUsers`. If that read
  fails, `CreateUser` returns `iam.ErrCreateUnconfirmed` with a non-nil
  Output holding the ID or username and the password, so a generated
  password is never lost to a failed confirm; the CLI still writes
  `--secret-file` from it and exits 1 naming `list-users`.
- `DeleteUser` keeps the transport's retries for `DELETE`; a retried delete
  that finds nothing returns `NotFound`, which the CLI prints with exit 4.
- No wait: both calls are synchronous.
- IAM user writes are free; no quote, price, or consent beyond `--yes`
  applies.

## Errors

| Case | Result | CLI code and exit |
|-|-|-|
| Missing or malformed username or password, bad path ID | `ErrInvalidInput`, no request | `InvalidUsage`, 2 |
| Both or neither of `--password-file` and `--secret-file`, unreadable or empty password file, terminal on stdin, `--secret-file` exists | No request | `InvalidUsage`, 2 |
| Delete of the caller | `iam.ErrSelfChange`, no request | `SelfChange`, 1 |
| Delete of a privileged user, or an unclassified caller | `iam.ErrPrivilegedChange`, no request | `PrivilegedChange`, 1 |
| Delete of a user with a policy or a group | `iam.ErrInUse`, no request | `ResourceInUse`, 1 |
| Delete of a user whose origin is not `iam` | Guard error, no request | `RequestFailed`, 1 |
| Create landed but the read-back failed | `iam.ErrCreateUnconfirmed`, Output kept | Exit 1, secret file still written |
| Secret file write failed after a create | User deleted, or named | `SecretFileFailed`, 1 |
| Error before any byte was sent | The error, marked not sent | Its own class, repeat allowed |
| Username taken | `Conflict` from the server | 1 |
| Password or username rejected by the server | `BadRequest` from the server | 1 |
| Unknown user ID | `NotFound` | `NotFound`, 4 |
| IAM policy denies the call | `ErrPermission` | 1 |

No new CLI error class is needed; the classes come from
[CLI errors](cli.md#errors-and-exit-codes) and
[IAM writes](iam-writes.md#errors).

## CLI

`svc_iam.go` registers the commands. Flags follow the Input fields; the
two file flags are registered by hand, as `--secret-file` and
`--document-file` are today.

| Command | Kind | `--yes` | Flags |
|-|-|-|-|
| `get-user` | Read | No | `--user-id` |
| `create-user` | Write | No | `--username`, `--password-file`, `--secret-file` |
| `delete-user` | Write | Yes | `--user-id` |

- All commands are `vngcloud iam <command>`.
- `delete-user` registers with `cli.Destructive`: a deleted user cannot be
  recovered (ADR 0002 rule 6).
- `create-user` is not destructive, like `create-service-account`.
- A [read-only](cli.md#read-only) profile refuses both writes with exit 2
  before any request. `--password-file` is read before that check, since it
  is a local file, exactly as `import-certificate` reads its files.
- `list-users` gains `--name`.

```sh
vngcloud iam create-user --username agent-reader \
  --secret-file ~/.config/agent/password
printf '%s' "$PASSWORD" | vngcloud iam create-user --username ops-tool \
  --password-file -
vngcloud iam delete-user --user-id <user-id> --yes
```

The usual next steps are `create-policy`, `attach-user-policy --yes`, and
`vngcloud configure` for a profile that reads the password from the secret
file through stdin, with `read_only` set last, as
[Configuration](../wiki/Configuration.md) describes for a service account.

## Threat analysis

Each item traces one attacker model through the proposed paths. Existing
code is cited by `path:line`; proposed paths are marked as unimplemented.
These are obligations for the independent adversarial review, not a
verdict.

1. **Leaked credentials in logs or errors.** Invariant: the password never
   appears outside the request body, `Output.Password`, and the secret
   file. The transport's debug record logs only method, path, status, and
   duration (`internal/transport/transport.go:606-616`), and the capture
   hook sees responses only, never request bodies
   (`internal/transport/transport.go:587`); `Sensitive` withholds the
   create response from it and from decode errors
   (`internal/transport/transport.go:160-166`), and `Redact` scrubs the
   password from a failing response's text
   (`internal/transport/transport.go:168-172`). `vngcloud.Secret` redacts every
   formatting path (`secret.go:38-66`); `--cli-input-json` refuses Secret
   fields (`internal/cli/input.go:48-60`); flag reflection never derives a
   flag for one (`internal/cli/flags.go:20-36`). Failure scenario: a 400
   body echoing the password reaches stderr. Severity: high. Constraint
   (unimplemented): set `Sensitive` and `Redact` on the create, keep the
   generated value only in a `Secret`, and test stdout, stderr, debug, and
   error text with a canary password. The uncertain-create error names the
   username, which is account data but not a secret, as
   `iam/service_accounts_write.go:119` names a service account name.
2. **Hostile or malformed responses.** Invariant: a response can fail the
   call but cannot make it delete the wrong user or report a user it did
   not confirm. The new calls set `MaxBody`; existing IAM calls leave it at
   the unlimited default (`internal/transport/transport.go:136-140`), a
   known limit this design does not widen. The create decodes only `id`,
   which passes `core.CheckPathID` before the read-back; a hostile ID fails
   that check and the create reports unconfirmed. The name lookup refuses a
   short or multi-page result. The delete guard reads through
   `guardPagedPolicySummaries` (`iam/guard.go:240-264`) and
   `guardUserGroups` (`iam/guard.go:397-412`), which refuse a null, short,
   or ambiguous page, and the origin rule refuses an unreadable or missing
   `from`. Severity: medium. Required checks: the decode and guard tests in
   [unit tests](iam-users-api.md#unit-tests).
3. **Redirects or a foreign endpoint.** Invariant: the password goes only
   to the configured Dashboard host over verified TLS. `Once` refuses a
   307 or 308 replay (`internal/transport/transport.go:147-158,426-429`);
   the transport follows no cross-host redirect and attaches the token only
   to configured hosts, per [security](../../instructions/security.md).
   `accountsURL` builds every user path under the Dashboard endpoint
   (`iam/iam.go:43-45`); an endpoint override is owner configuration,
   as for every call. Severity: medium. Check: a redirect test for the
   create, and a same-host 307 test for `GetUser`.
4. **Local tampering.** Invariant: the secret file is created fresh at
   0600, and a password file is read as a regular file. `checkSecretFilePath`
   refuses an existing path or symlink before any request
   (`internal/cli/secretfile.go:40-60`), and `openSecretFileExclusive`
   closes the race with `O_EXCL|O_NOFOLLOW`
   (`internal/cli/secretfile_unix.go:15-16`); a swapped symlink fails the
   open, and the create-then-delete path removes the user. `readInputFile`
   opens the password file without blocking on a FIFO and refuses a
   non-regular file (`internal/cli/inputfile.go:26-52`); stdin is bounded by
   `readStdinValue` (`internal/cli/configure.go:323-339`). Profiles, the
   token cache, and login are unchanged. Severity: low. Check: the
   secret-file and password-file golden tests.
5. **Concurrent or repeated writes.** Invariant: one `create-user` makes at
   most one user, and a delete never resends after an uncertain outcome.
   `Once` sends once whatever the response, and `NotSent` records a send
   that never reached a connection
   (`internal/transport/transport.go:104-109,147-158` and
   `internal/transport/errors.go:420-427`); the CLI never retries. Two
   callers creating the same username race to the server's uniqueness
   rule, and the loser gets a 409. The cleanup delete after a secret-file
   failure runs on a detached context with its own timeout, as
   `internal/cli/svc_iam.go:233-238` does, and passes the delete guard,
   which a fresh user satisfies. Nothing here is paid, so there is no quote
   or price race. Severity: low. Check: no-resend tests for 401, 429, 5xx,
   and 307, and the not-sent dial test.

## Security review

The adversarial review before release checks the items above against the
code, and also: that every guard read happens in the same call as the
delete; that a guard read failure refuses rather than sends; that no path
turns a guard off; that `ListUsers` existing behavior is unchanged without
`Name`; that `ErrSelfChange` wins over every other refusal; and that no
fixture, test name, or golden file holds a real username, ID, or password.

## Wiki

The release updates, in the same change as the code:

- `CLI-IAM.md`, generated, with notes for `create-user` (the two file flags,
  the generated password, the secret file, the unconfirmed case, and the
  retry advice), `delete-user` (the guard rules), and `get-user`.
- `IAM.md` with an SDK example for `CreateUser`, `GetUser`, and
  `DeleteUser`, the `Password` redaction contract, and the uncertain-create
  rule.
- `Services.md`, the IAM row and read list, for `GetUser` and `Name`.
- `Configuration.md`, a short IAM-user profile recipe: create the user,
  attach a read-only policy, pipe the secret file into `configure set
  password -`, and set `read_only` last.
- `Errors.md` needs no new code.

## Releases

One release, U1, ships the SDK reads and writes, the CLI, and the wiki
together after the probes and an adversarial review. It changes no existing
method or command: `ListUsers` gains an optional field, `User` keeps its
fields, and `UserDetail` is new. Alternative: ship `GetUser` and the name
filter first as a read-only release and the writes second; the probes
already verify the read shapes, so the split buys little.

## Owner decisions

1. **Reversal.** Confirm that IAM user create and delete come in and the
   other user writes stay out. Recommend yes: it is the owner's request,
   and the guards keep IAM write rights a console task.
2. **Password source.** Options: generated only, written to
   `--secret-file`; supplied only, from a file or stdin; either, with
   exactly one flag. Recommend either: a generated password suits an agent
   profile, and a supplied one suits a person who already has a password
   manager. Both paths keep the password off argv.
3. **Generation place and length.** Options: the SDK generates when
   `Password` is empty; only the CLI generates. Length 32 or 50. Recommend
   the SDK at 32 characters: SDK callers get the same safety, one generator
   is tested once, and 32 clears the default policy and any custom minimum
   up to 32. A custom minimum above 32 refuses the create with a 400 and
   nothing is created; the caller then supplies a password. 50, the
   console's maximum, would clear every custom minimum but is the longest
   value the console accepts and the server's own maximum is unknown.
4. **Delete rule.** Options: refuse any user that holds a policy or a
   group, as in [Guards](#guards); refuse only the caller and a privileged
   user and let the server decide the rest. Recommend the first: the
   caller sees exactly what rights disappear, every removal goes through a
   guarded command, and the server's undocumented 409 never applies.
5. **Identity-provider users.** Options: refuse a delete unless `from` is
   exactly `iam`; delete any user. Recommend refuse: identity providers
   are out of scope, and a user this design cannot classify is unprovable.
6. **Follow-up writes.** `reset-password`, `activate`, `deactivate`,
   username change, two-factor calls, and IP allow lists stay out.
   Recommend out: none is needed for a usable create, and each changes how
   an existing person signs in. If the owner wants rotation later,
   `reset-password` is the natural next slice and reuses the password rules
   here.
7. **Create guard.** Options: none beyond read-only; require a classified
   caller type. Recommend none: a new user holds nothing, and
   `create-service-account` and `create-group` set the precedent.
8. **Username checks.** Options: shape only, as in
   [CreateUser](iam-users-api.md#writes); the console's 5 to 50 character
   rule. Recommend shape only, per ADR 0002 rule 5; the wiki states the
   console rule so a caller is not surprised by a 400.
9. **Probe sign-in.** Options: the probe signs in as the new user and
   calls `GetCallerIdentity`; it does not. Recommend sign in: it is free,
   and it proves a generated password is accepted end to end before the
   feature ships.
10. **Release shape.** Recommend one release, U1.

## Open questions

- Whether the create response holds `id`, and its other keys.
- Whether `name` on `iam-users` matches exactly or by substring, and
  whether it is case-sensitive.
- The type of `lastUse` and the exact key set of `GET iam-users/{id}`.
- When `DELETE iam-users/{id}` returns 409.
- Whether a 32-character generated password passes the server's default
  policy and the sign-in gateway.
- The account's IAM user quota.
