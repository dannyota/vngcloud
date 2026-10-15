# IAM

`iam` is `danny.vn/vngcloud/iam`, with its own `New(cfg)`. It reads caller
identity, users, actions, policies, groups, and service accounts; see the
[IAM section of Services](Services.md#iam) for that full read list. This
page covers IAM user, service account, policy, and group writes.

See [Storage Workload Access](Storage-Workload-Access.md) for a dedicated
service account and attached S3 key for an application. Its IAM client secret
authenticates management calls; the application S3 secret is separate.
Bucket policies grant the attached key's bucket access.

## IAM users

`GetUser` takes a required `UserID` and returns `UserDetail`, including ID,
username, origin (`From`), enabled state, and epoch-millisecond `CreatedAt`
and `LastUse` (0 before first use). It omits contact and MFA factor values.
`ListUsers` takes optional `Name`, `Page`, and `Size`, and returns one page
under `Items`. The observed name filter matches case-insensitive substrings;
compare exact usernames and IDs. A name match never proves creation ownership.

```sh
vngcloud iam get-user --user-id <user-id>
vngcloud iam list-users --name <username>
vngcloud iam create-user --username <username> \
  --secret-file <private-directory>/password
vngcloud iam create-user --username <username> --password-file - \
  < <private-directory>/password
vngcloud iam delete-user --user-id <user-id> --yes
```

Create requires exactly one password mode. Generation defaults to 32
characters; `--password-length` accepts 8 to 50, with at least one uppercase
letter, lowercase letter, digit, and `@`. Custom account password policy can
still reject it. Supplied passwords use a private regular file or stdin,
never argv or JSON. Files may be symlinks to trusted private files. Input
allows at most 4096 bytes, removes one final LF or CRLF, preserves spaces,
and rejects controls, invalid UTF-8, and empty values. Stdin refuses a
terminal, requires EOF within 30 seconds, and honors cancellation. Stdin
password input cannot share `file://` JSON input. All commands refuse a
non-regular `file://` JSON file before reading it.

The SDK generates when `CreateUserInput.Password` is empty and returns only
generated values in `CreateUserOutput.Password`, a `vngcloud.Secret`.
Supplied passwords are never returned. Inspect both output and error:
`iam.ErrCreateUnconfirmed` retains a valid `CreatedID` from the accepted
create, while `iam.ErrUserCreateUncertain` means ownership is unproven.
`CandidateID`, when present, comes only from a name lookup and never proves
ownership. Preserve any non-empty generated password through a private
persistence path even when the SDK returns an error. Do not log `Reveal()`.

The CLI saves generated passwords on success and on both partial outcomes,
then releases its references. Success prints a redacted `Password` and the
saved `SecretFile`; supplied mode prints no secret path. Errors print no
partial success object and say whether the password was saved. Both create
outcomes use `RequestFailed` and exit 1, including when a confirmation read
failed with 401 or 404. Reconcile an uncertain outcome with
`list-users --name` and other administrators. Do not retry, trust the password,
reset a candidate, or delete a name match until ownership is established.
Create sends at most one POST and never retries a password-policy rejection.

The destination must be new, including no existing symlink. Persistence uses
exclusive creation at mode 0600 and checks write, sync, and close failures.
Only a partial file created by the call is removed; removal failure warns
that secret bytes may remain. Use a trusted private directory: parent symlinks
are followed, and Unix mode bits do not guarantee Windows access controls.
`SecretFileFailed` sends no further cloud request. It names `CreatedID` when
available, otherwise the requested username, and the manual
`vngcloud iam delete-user --user-id <id> --yes` command after reconciliation.
Process termination between create and persistence can lose the password.
Saved passwords remain after confirmation or output-rendering failures.
Go may retain copies in memory; buffer clearing is not secure erasure.

Creation attaches no policies or groups. The caller needs server permission
to create users; grant access separately with guarded policy attachment or
group membership commands. Sign-in needs the root account email, new username,
and password, plus any account controls. The tool configures no MFA factor
and does not bypass MFA. See [Configuration](Configuration.md#iam-user-profile).
Read-only refuses writes before password input, generation, or file creation.

Delete requires `--yes` and a classified caller. It refuses the caller's own
IAM user before reading the target, then requires a matching target ID and
`From == "iam"`. Root and unknown callers fail closed. `ErrSelfChange` and
`ErrPrivilegedChange` keep their CLI codes; `ErrOriginNotIAM` maps to
`RequestFailed`. Delete has no privilege or attachment refusal; the server
must authorize deletion and its 409 stays `Conflict`, with no automatic
detach. Guard checks and deletion are not atomic. DELETE retains SDK retries;
a final 404 is `NotFound`, not success. After an uncertain delete inspect the
same ID with `get-user`, never a replacement username.

Adding `ListUsersInput.Name` breaks unkeyed struct literals. Responses are
bounded: get 256 KiB, list 4 MiB, and create/delete 64 KiB. Oversized bodies
fail instead of decoding a truncated prefix. Public list paging and output
remain unchanged. The [CLI reference](CLI-IAM.md#create-user) lists flags.

## Service account writes

```go
created, err := iamClient.CreateServiceAccount(ctx, &iam.CreateServiceAccountInput{
	Name:        "app",
	Description: "app service account",
})
if err != nil {
	log.Fatal(err)
}
secret := created.ClientSecret.Reveal() // save this now; it is never shown again

if _, err := iamClient.UpdateServiceAccount(ctx, &iam.UpdateServiceAccountInput{
	ServiceAccountID: created.ServiceAccount.ID,
	Description:      vngcloud.Ptr("renamed"),
}); err != nil {
	log.Fatal(err)
}

reset, err := iamClient.ResetServiceAccountSecret(ctx, &iam.ResetServiceAccountSecretInput{
	ServiceAccountID: created.ServiceAccount.ID,
})
if err != nil {
	log.Fatal(err)
}
newSecret := reset.ClientSecret.Reveal()

if _, err := iamClient.DeleteServiceAccount(ctx, &iam.DeleteServiceAccountInput{
	ServiceAccountID: created.ServiceAccount.ID,
}); err != nil {
	log.Fatal(err)
}
```

`ClientSecret` is a `vngcloud.Secret`, the same type `compute.CreateSSHKey`
returns for a private key: printing, logging, or JSON-encoding it gives
`"[redacted]"`, and `Reveal()` is the only way to read the value back out.
See [Compute](Compute.md#the-private-key-is-a-secret) for the full
redaction contract. When a create or reset response holds no secret,
`CreateServiceAccount` and `ResetServiceAccountSecret` return
`iam.ErrNoSecret` alongside their Output rather than succeeding silently;
`ClientSecret` stays empty in that case. If the read `CreateServiceAccount`
makes to confirm a new account fails, it returns `iam.ErrCreateUnconfirmed`
alongside an Output that still carries the create response's own ID and
client secret, with every other `ServiceAccount` field zero: the account and
its secret are real either way, even though the error is not nil.

`CreateServiceAccount` and `ResetServiceAccountSecret` are `POST` and are
sent at most once: a resend after a 401 or a followed redirect would create
a second account or rotate the secret a second time, so neither is ever
retried or resent, whatever the response. After any error that is not a
4xx `*vngcloud.APIError`, call `ListServiceAccounts` with `Name` and look
for the service account before creating it again, rather than retrying
blind. One found that way after a failed create has already lost its
client secret. A failed reset cannot be checked this way at all: there is
no read that shows whether the secret changed, so treat the previous one as
no longer trustworthy either way.

`UpdateServiceAccount`, `DeleteServiceAccount`, and
`ResetServiceAccountSecret` refuse to run, sending no request, when their
target is the caller itself, holds a policy that grants an IAM write
right, such as `CreatePolicy` or `AttachPolicyToIamUser`, found through the
account's own action list, or when the caller itself is a service account,
whatever the target.

A service-account caller (`user-sa` or `service-sa`) refuses every
service-account-targeted write, not only one against its own ID: GreenNode's
`userinfo` response is not confirmed to report a service-account caller's ID
in the same form as a target's ID or `ClientID`, so the SDK cannot safely
tell them apart and refuses every case rather than risk missing a real
self-change.

Neither `ErrSelfChange` nor `ErrPrivilegedChange` names the policy,
statement, or action involved, and there is no way to turn either guard
off; make such a change from the IAM console instead. `CreateServiceAccount`
has no guard: creating a service account changes no one's rights.

## Policy writes

```go
// sa is a service account from CreateServiceAccount, as shown above.
created, err := iamClient.CreatePolicy(ctx, &iam.CreatePolicyInput{
	Name: "app-read",
	Statements: []iam.Statement{
		{Effect: "allow", Actions: []string{"vserver:ListServers"}, Resources: []string{"*"}},
	},
})
if err != nil {
	log.Fatal(err)
}

if _, err := iamClient.UpdatePolicy(ctx, &iam.UpdatePolicyInput{
	PolicyID:    created.Policy.ID,
	Description: vngcloud.Ptr("read-only server access"),
}); err != nil {
	log.Fatal(err)
}

if _, err := iamClient.AttachServiceAccountPolicy(ctx, &iam.AttachServiceAccountPolicyInput{
	PolicyID:         created.Policy.ID,
	ServiceAccountID: sa.ServiceAccount.ID,
}); err != nil {
	log.Fatal(err)
}

if _, err := iamClient.DetachServiceAccountPolicy(ctx, &iam.DetachServiceAccountPolicyInput{
	PolicyID:         created.Policy.ID,
	ServiceAccountID: sa.ServiceAccount.ID,
}); err != nil {
	log.Fatal(err)
}

if _, err := iamClient.DeletePolicy(ctx, &iam.DeletePolicyInput{
	PolicyID: created.Policy.ID,
}); err != nil {
	log.Fatal(err)
}
```

`Statements` takes the API's own document shape: at least one statement,
`Effect` of `"allow"` or `"deny"`, and non-empty `Actions` and `Resources`
with no empty string. `CreatePolicy` and `UpdatePolicy` check this shape,
returning `vngcloud.ErrInvalidInput` before any request, so a document
shaped like AWS's (`Version`, `Statement`, capitalized `Effect`) fails
instead of reaching the server as an empty policy. Action, resource, and
condition values are not checked; the server checks those. `UpdatePolicy`
sends a full `PUT`, so it fills any field left nil (`Name`, `Description`,
`Statements`) from the policy's current state first.

`CreatePolicy` is `POST` and sent at most once, the same as
`CreateServiceAccount`: a resend after a 401 or a followed redirect would
create a second policy, so it is never retried or resent after any
response. After any error that is not a 4xx `*vngcloud.APIError`, call
`ListPolicies` with `Name` and look for the policy before creating it
again. `AttachServiceAccountPolicy` and `DetachServiceAccountPolicy` are
plain `POST` and `DELETE`: a repeat attach or detach is visible as the
server's own conflict or not-found response, so no such care is needed.

`CreatePolicy`, `UpdatePolicy`, `DeletePolicy`, every attach and detach,
`DeleteGroup`, `AddUserToGroup`, and `RemoveUserFromGroup` each refuse to
run, sending no request, per the rules below. Every guard also refuses for a
caller whose type cannot be classified.

| Write | Refused when |
|-|-|
| `CreatePolicy` | The statements grant an IAM write action |
| `UpdatePolicy` | The policy is managed; its current or proposed statements grant an IAM write action; or it is attached to a protected principal or group |
| `DeletePolicy` | The policy is managed, or attached to a group, an IAM user, or a service account |
| `DeleteGroup` | The group has a member or a policy attached |
| `AttachServiceAccountPolicy`, `DetachServiceAccountPolicy` | The policy grants an IAM write action, the target service account already holds one, or the caller is a service account |
| `AttachGroupPolicy`, `DetachGroupPolicy` | The policy grants an IAM write action, or the target group is protected |
| `AttachUserPolicy`, `DetachUserPolicy` | The policy grants an IAM write action, or the target user is protected |
| `AddUserToGroup`, `RemoveUserFromGroup` | The user or the group is protected |

A protected principal is the caller, an IAM user or service account holding
a policy that grants an IAM write right (an IAM user counts as protected
through a group's policy too), and a protected group is one with such a
policy attached, or with a protected principal as a member.

| Sentinel | Meaning |
|-|-|
| `iam.ErrSelfChange` | The target service account is the caller, the caller is a service account, `UpdatePolicy` targets a policy attached to the caller (or to any service account, when the caller is one), or a group membership or attach call would change the caller's own rights |
| `iam.ErrPrivilegedChange` | The target, or something it is attached to, holds a policy that grants an IAM write right, or the caller's own type could not be classified |
| `iam.ErrManagedPolicy` | `UpdatePolicy` or `DeletePolicy` targets a GreenNode-managed policy |
| `iam.ErrInUse` | `DeletePolicy` targets a policy still attached to a group, an IAM user, or a service account, or `DeleteGroup` targets a group with a member or a policy |
| `iam.ErrNoSecret` | A create or reset response reported success but carried no client secret |
| `iam.ErrCreateUnconfirmed` | A create succeeded but the read-back that confirms it failed |
| `iam.ErrNotSettled` | `CreatePolicy`, `UpdatePolicy`, `CreateGroup`, or `UpdateGroup` succeeded but the read-back that confirms it failed; Output keeps the resource's ID |

None of these sentinels name the policy, statement, or action involved, and
there is no way to turn any guard off; make such a change from the IAM
console instead.

## Group writes

```go
group, err := iamClient.CreateGroup(ctx, &iam.CreateGroupInput{Name: "app-readers"})
if err != nil {
	log.Fatal(err)
}
iamClient.AddUserToGroup(ctx, &iam.AddUserToGroupInput{GroupID: group.Group.ID, UserID: userID})
iamClient.AttachGroupPolicy(ctx, &iam.AttachGroupPolicyInput{PolicyID: policyID, GroupID: group.Group.ID})
iamClient.DetachGroupPolicy(ctx, &iam.DetachGroupPolicyInput{PolicyID: policyID, GroupID: group.Group.ID})
iamClient.RemoveUserFromGroup(ctx, &iam.RemoveUserFromGroupInput{GroupID: group.Group.ID, UserID: userID})
iamClient.DeleteGroup(ctx, &iam.DeleteGroupInput{GroupID: group.Group.ID})
```

`CreateGroup` always sends `mode: "iam"` with no initial members or
policies; like `CreateServiceAccount`, `CreateGroup` and `UpdateGroup` have
no guard. `AttachUserPolicy` and `DetachUserPolicy` attach a policy directly
to an IAM user, in the same shape as the group calls above.
