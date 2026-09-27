# IAM Writes Design

Status: Accepted (2026-09-27). The owner approved every recommendation
under [Owner decisions](#owner-decisions), with guard option (d).

This design adds IAM service account, group, and policy writes to `iam`,
with the reads they need and CLI commands. It is picks 9 and 10 of the
[free writes survey](free-writes-survey.md). These calls change who can
do what in the whole account, so most of the design is about what the
tool refuses to do.

It follows [ADR 0002](../adr/0002-write-api-conventions.md) for every
write, and the `vngcloud.Secret` and `--secret-file` contract of
[vStorage](storage.md#secrets) and
[create-ssh-key](vserver-writes.md#create-ssh-key). Calls, bodies, and the
SDK surface are in [IAM writes: API](iam-writes-api.md); tests, probes,
and live checks are in [IAM writes: checks](iam-writes-checks.md).

## Ownership

[vStorage](storage.md#iam) already plans `iam` with S3 keys and service
accounts (releases S3 and S4). This design takes over the service
account calls that do not involve S3; vStorage keeps S3 keys and the key
attach.

| Call | Owner |
|-|-|
| `ListS3Keys`, `CreateS3Key`, `DeleteS3Key` | vStorage S3 |
| `ListServiceAccountS3Keys`, `AttachS3Key`, `DetachS3Key` | vStorage S4 |
| `GetServiceAccountPrincipal` | vStorage S5 |
| `ListServiceAccounts`, `GetServiceAccount`, `CreateServiceAccount`, `DeleteServiceAccount` | This design (moved from vStorage S4) |
| `UpdateServiceAccount`, `ResetServiceAccountSecret` | This design |
| Groups, policies, attachments, caller identity, user reads | This design |

vStorage S4 needs a paid vStorage project and waits for credit; service
accounts do not, so moving them lets them ship and be checked live now.
The storage design text changes listed in the report go with approval.

## Non-goals

- IAM user writes, identity providers, MFA, passwords, IP allow lists,
  and access settings, per the survey: they change who can sign in.
  `ListUsers` is a read, so callers can find a user ID.
- Service account activate and deactivate, trusted roots (cross-account
  trust), impersonation, and service account login.
- Tags on groups, policies, and service accounts.
- `compose-policy`, `decompose-policy`, and `parse-policy`: console
  editor helpers.
- Granting, revoking, or editing IAM write rights. That stays a console
  task; see [Guards](#guards).
- Groups in `idp` mode.

## Guards

### Threat

The CLI runs for the owner and for AI agents. An IAM write can lock the
owner out, for example by detaching the owner's admin policy, and can
escalate the caller, for example by attaching a full-access policy to
itself or to a service account whose secret it then holds. The root
account can always repair IAM in the console, so a lockout costs time,
not the account. An escalation can cost the account.

The server's IAM rights are the real boundary: a principal without IAM
write rights cannot call these writes at all. The guards below stop
mistakes and the two named harms in a caller that has IAM write rights.
Like [read-only](cli.md#read-only), they do not stop a process that can
call the API another way.

### Terms

- **IAM write action**: an action of product `iam` whose `label` from
  `ListActions` is not `List`, `Read`, or `Tagging`. Today that is 58
  actions, such as `CreatePolicy` and `AttachPolicyToIamUser`. An
  unknown label counts as a write.
- **Privileged policy**: a policy with an `allow` statement whose action
  pattern matches any IAM write action, compared case-insensitively with
  `*` as a wildcard: `*`, `iam:*`, `iam:Attach*`, and `iam:CreatePolicy`
  all match. Resources and conditions are ignored, so a narrowed grant
  still counts. Managed examples: `IAMFullAccess` and the `AgentBase`
  role policies.
- **Caller**: the principal from `GetCallerIdentity`: an IAM user or a
  service account. An unknown user type fails every guarded write.
- **Protected principal**: the caller; an IAM user with a privileged
  policy attached directly or through a group; a service account with a
  privileged policy attached.
- **Protected group**: a group with a privileged policy attached, or with
  a protected principal as a member.

### Rules

The SDK checks these with reads before any write request. A refusal
sends nothing and names the rule, never a policy document.

| Write | Refused when |
|-|-|
| `CreatePolicy` | The statements are privileged |
| `UpdatePolicy` | The policy is managed; the old or new statements are privileged; or the policy is attached to a protected principal or group |
| `DeletePolicy` | The policy is managed, or attached to anything |
| Attach or detach a policy | The policy is privileged, or the target is protected |
| `AddUserToGroup`, `RemoveUserFromGroup` | The user or the group is protected |
| `DeleteGroup` | The group has a member or a policy |
| `UpdateServiceAccount`, `DeleteServiceAccount`, `ResetServiceAccountSecret` | The service account is protected, or the caller is a service account |

`CreateServiceAccount`, `CreateGroup`, and `UpdateGroup` change no one's
rights and have no guard beyond read-only.

Taken together, the tool cannot create, grant, revoke, or edit IAM write
rights, cannot change the caller's own rights, and cannot take a
protected service account's secret. The owner's IAM user, which holds
IAM write rights, is a protected principal wherever the tool runs.

There is no flag or Input field that turns a guard off. The owner makes
such changes in the IAM console.

A guard read that comes back incomplete or unprovable refuses instead of
guessing: an account action list naming no write action, or an attachment
page whose item count does not match its own reported total, is treated as
a failed read, never as evidence that nothing is privileged. A policy with
no statements at all is privileged, and any statement effect other than
`deny` counts as a grant, including one the API has not defined yet.

A caller whose own type is a service account (`user-sa` or `service-sa`)
refuses every service-account-targeted write, not only one against its own
ID: see the [open question](#open-questions) on the caller identity form.

### Cost of the checks

A guard needs `GetCallerIdentity` and `ListActions` once per client, plus
reads of each principal's attached policies and a `GetPolicy` per policy
not yet read in that call. A user in a group with 30 policies costs about
35 requests. IAM writes are rare, so the design accepts that.

### What stays open

- The checks and the write are not atomic. A console change between them
  goes unseen, as with the other pre-read guards in this SDK.
- A caller with IAM write rights can still grant non-IAM rights it lacks,
  for example `vServerFullAccess` on a new service account whose secret
  it saves. Only the server's rights stop that; see decision 5.
- A `deny` statement attached to an unprotected principal can take its
  rights away. That is allowed, with `--yes`.
- A service-account caller cannot target any service account at all, itself
  included, until the [open question](#open-questions) on the caller
  identity form is settled with a live check.

## Policy documents

The SDK takes `Statements []Statement`, the API's own form. Before any
request it checks shape only:

- At least one statement.
- `Effect` is `allow` or `deny`.
- `Actions` and `Resources` are non-empty, with no empty string.
- `Condition` keys are not checked; the server checks them, as it checks
  action and resource names (ADR 0002 rule 5).

The CLI reads the document from `--document-file <path>`, a JSON object
`{"statements": [...]}` in the form the console's JSON editor shows. It
decodes with unknown fields refused, so an AWS-style document
(`Version`, `Statement`, `Effect`) fails with exit 2 instead of reaching
the server with no statements. Go's decoder matches keys without regard
to case, so `get-policy` output with Go field names also decodes. The
file is at most 64 KiB. `--cli-input-json` may set `Statements` too, since
a policy is not a secret.

The privileged check is the only part that reads action names, and it
uses the server's own action list.

## Secret file

`create-service-account` and `reset-service-account-secret` need
`--secret-file <path>` and cannot print the secret. They follow
[CLI secret files](cli.md#secret-files) and the steps of
[create-ssh-key](vserver-writes.md#create-ssh-key):

1. Before any request, the parent directory must exist and nothing may
   exist at the path, symlinks included; otherwise exit 2.
2. After the write, the CLI creates the path with
   `O_CREATE|O_EXCL|O_NOFOLLOW` and mode 0600 and writes the secret with
   one trailing newline.
3. If the file write fails after a create, the CLI removes any partial
   file, deletes the new service account, and exits 1 with
   `SecretFileFailed`. After a reset it cannot undo, so the error says to
   reset again. If the create's delete fails, the error names the
   service account ID.
4. If a create or reset response holds no secret, `iam.CreateServiceAccount`
   and `iam.ResetServiceAccountSecret` return `iam.ErrNoSecret` alongside
   their Output rather than succeeding silently. The CLI keeps the service
   account, writes no file, and exits 1 with `SecretFileFailed`; a create's
   message names `reset-service-account-secret`, and a reset's says the
   secret was probably rotated already and to reset again.
5. Stdout gets the service account without the secret: `ClientSecret`
   prints as `[redacted]`, and a `SecretFile` field names the path. The
   public `ClientID` prints as usual.
6. If the read-back `CreateServiceAccount` makes after the create request
   fails, it returns `iam.ErrCreateUnconfirmed` alongside an Output holding
   the create response's own ID and client secret (every other
   `ServiceAccount` field zero), instead of dropping them: the account and
   its secret are real either way. The CLI's use of this case is not yet
   decided; see [open questions](#open-questions).

## Errors

| Case | Result | CLI code and exit |
|-|-|-|
| Missing field, bad ID, statement shape, bad document file | `ErrInvalidInput`, no request | `InvalidUsage`, 2 |
| `--secret-file` exists or its directory is missing | No request | `InvalidUsage`, 2 |
| Change to the caller's own rights | `iam.ErrSelfChange`, no write sent | `SelfChange`, 1 |
| Privileged policy, or protected principal or group | `iam.ErrPrivilegedChange`, no write sent | `PrivilegedChange`, 1 |
| Update or delete of a managed policy | `iam.ErrManagedPolicy`, no write sent | `ManagedPolicy`, 1 |
| Delete of an attached policy or a non-empty group | `iam.ErrInUse`, no write sent | `ResourceInUse`, 1 |
| Create or reset response held no secret | `iam.ErrNoSecret`, write already applied | `SecretFileFailed`, 1 |
| Unknown ID | `NotFound` | `NotFound`, 4 |
| Name taken, repeated attach or add | `Conflict` | 1 |
| IAM policy denies the call | `ErrPermission` | 1 |
| Secret file write failed, or no secret returned | See [Secret file](#secret-file) | `SecretFileFailed`, 1 |

When a target is both the caller and protected, `ErrSelfChange` wins.
`ResourceInUse` keeps the meaning
[vServer network writes](vserver-network-writes.md#errors) gave it. The
[CLI error list](cli.md#errors-and-exit-codes) gains `SelfChange`,
`PrivilegedChange`, and `ManagedPolicy`.

## CLI

`svc_iam.go` registers the table. Flags follow the Input fields.

| Command | Kind | `--yes` |
|-|-|-|
| `get-caller-identity`, `list-users`, `list-actions` | Read | No |
| `list-service-accounts`, `get-service-account`, `list-service-account-policies` | Read | No |
| `list-policies`, `get-policy`, `list-policy-attachments` | Read | No |
| `list-groups`, `get-group`, `list-group-policies`, `list-user-groups`, `list-user-policies` | Read | No |
| `create-service-account`, `update-service-account` | Write | No |
| `reset-service-account-secret`, `delete-service-account` | Write | Yes |
| `create-policy`, `create-group`, `update-group` | Write | No |
| `update-policy`, `delete-policy`, `delete-group` | Write | Yes |
| `attach-group-policy`, `detach-group-policy` | Write | Yes |
| `attach-user-policy`, `detach-user-policy` | Write | Yes |
| `attach-service-account-policy`, `detach-service-account-policy` | Write | Yes |
| `add-user-to-group`, `remove-user-from-group` | Write | Yes |

- All commands are `vngcloud iam <command>`.
- Commands that need `--yes` register with `cli.Destructive`. Attach,
  detach, add, remove, and policy update can be undone by one more
  command, so ADR 0002 rule 6 does not require it; they need it anyway
  because a grant is usable at once and a removal can break a running
  app, as public bucket access needs it in [vStorage](storage.md#cli).
- A [read-only](cli.md#read-only) profile refuses every write with exit
  2 before any request.
- `create-policy` and `update-policy` take `--document-file`.

```sh
vngcloud iam create-policy --name app-bucket-read \
  --document-file policy.json
vngcloud iam create-service-account --name app \
  --secret-file ~/.config/app/client-secret
vngcloud iam attach-service-account-policy --policy-id <p> \
  --service-account-id <sa> --yes
```

## Security

- Every write gets an adversarial review before its release. The review
  checks each [guard rule](#rules) with a fake API that records requests,
  including a user protected only through a group, a pattern such as
  `IAM:attach*`, an unknown label, and an unknown caller type; that no
  guard can be turned off; the secret rules of
  [vStorage](storage.md#secrets); `mode: "iam"` and no members or
  policies on group create; `--yes` and read-only refusal; path checks on
  every call; and no create, attach, or reset resend after a 5xx.
- The wiki says: give agent profiles an IAM user without IAM write rights,
  and turn on `read_only` where they only read. Then the server refuses
  what the guards would, and the guards cover only mistakes by the owner.
- Policy names, group names, user names, service account names, client
  IDs, account numbers, and every ID are account data. Fixtures use
  `<id>`, `<name>`, `<account>`, and `<secret>`. Managed policy names and
  action names are product data and may stay.

## Releases

Each release ships the SDK and CLI together, with `Services.md` and a new
`CLI-IAM.md` in the wiki, after its live checks and an adversarial
review. Each is numbered when it ships.

| Release | Content |
|-|-|
| I1 | The `IAM` endpoint, `GetCallerIdentity`, `ListUsers`, `ListActions`, and every policy, group, and service account read |
| I2 | Service account create, update, delete, and reset, with `--secret-file` and the guards |
| I3 | Policy create, update, and delete, with document checks; attach and detach to service accounts |
| I4 | Group create, update, and delete; add and remove users; attach and detach to groups and users |

I1 is read-only. I2 carries the secret handling and the guard code. I3
adds the privileged check, and I4 the group and user paths. vStorage S4
then adds only the S3 key calls on top of I2. No release changes an
existing method or command.

## Owner decisions

1. **Ownership.** Options: this design takes service account list, get,
   create, and delete from vStorage S4; or vStorage keeps them and this
   design adds only update and reset. Recommend moving them: S4 waits
   for paid credit, and service accounts are free to check now.
2. **Client secret.** vStorage decision 8 says the CLI never shows or
   saves a service account's client secret. Options: require
   `--secret-file` on create and reset; keep decision 8 and discard the
   secret; make the file optional and discard without it. Recommend
   require the file: OpenTofu and the external vStorage API use the
   client ID and secret, and the file rules are already reviewed. This
   replaces decision 8.
3. **Reset secret.** Options: add `reset-service-account-secret` with
   `--yes` and `--secret-file`; leave it to the console. Recommend add
   it: it recovers a lost secret, and rotates one.
4. **Guards.** Options: (a) `--yes` only; (b) (a) plus the caller guard;
   (c) (b) plus refusing privileged policies; (d) (c) plus protected
   principals and groups, as in [Rules](#rules). Recommend (d) with no
   override: IAM write rights become a console task, and the owner's
   user is protected wherever the tool runs.
5. **Non-IAM escalation.** Options: accept it and document that the
   server's rights are the boundary; refuse attaching a policy the
   caller does not itself hold, compared by ID. Recommend accept: the ID
   rule would refuse every customer policy, including the per-bucket
   policies [vStorage](storage.md#per-bucket-key) needs.
6. **Protecting profiles and the owner.** Options: read-only as today,
   the protected-principal rule, and the wiki advice above; also refuse
   changes to any principal with a `protected` tag. Recommend the first:
   the owner's user is already protected by its IAM write rights, and a
   tag guard costs a read per principal and one more rule to review.
7. **Policy checks.** Options: valid JSON only; shape checks with
   unknown fields refused; also check action and resource names against
   `ListActions` and `GET resources`. Recommend shape checks: they catch
   the AWS-style mistake, and names stay a server rule (ADR 0002 rule 5).
8. **Policy input.** Options: `--document-file` plus
   `--cli-input-json`; also an inline `--document`; file only. Recommend
   `--document-file` plus `--cli-input-json`: JSON with `*` in argv is
   easy to break in a shell, and the JSON path already exists.
9. **Where `--yes` applies.** Recommend the table in [CLI](#cli): every
   delete, attach, detach, add, and remove, plus `update-policy` and
   `reset-service-account-secret`.
10. **Delete guards.** Options: refuse a non-empty group and an attached
    policy; let the server decide. Recommend refuse: a group delete
    silently takes rights from its members.
11. **Live attach and membership checks.** The survey's decision 3 kept
    attaches and membership out of live tests. Options: (a) attach only
    a read-only customer policy to a service account and a group the
    test creates, and check membership only through its refusals and a
    random user ID; (b) (a) plus a real add and remove of a spare IAM
    user the owner creates in the console, named by
    `VNGCLOUD_LIVE_IAM_USER_ID`; (c) no live attach. Recommend (a): the
    only IAM user on the test account is the caller, which the guard
    protects, and creating users is out of scope.
12. **Releases.** Recommend I1 to I4 in order, as above.

## Open questions

- Whether a service account create returns the client secret, and the ID
  form.
- Name rules and uniqueness for policies, groups, and service accounts,
  including whether `vngcloud-live-<8 hex>` is accepted.
- Whether `PUT policies/{id}` without `statements` clears them, and
  whether `PATCH groups/{id}` accepts a body without `name`.
- Statuses for a repeated attach or add, a detach of what is not
  attached, and a delete of an attached policy or a non-empty group.
- Whether the server checks action names on create.
- `userinfo` for a service account token, and the user type it reports: in
  particular, whether `userId` is the calling service account's own ID or
  its `clientId`. Until this is checked live, the guard refuses every
  service-account-targeted write from a service-account caller rather than
  compare `userId` against either form; this is the stricter of the two
  choices, chosen because a wrong comparison could miss a real self-change.
- Whether a service account can be a group member; the create spec says
  so, but no call adds one.
- Whether the CLI should still write `--secret-file` (or clean up the new
  account) from `CreateServiceAccount`'s Output when the read-back after
  create fails (`iam.ErrCreateUnconfirmed`), given the create response
  itself already carries the ID and secret.
