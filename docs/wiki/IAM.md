# IAM

`iam` is `danny.vn/vngcloud/iam`, with its own `New(cfg)`. It reads caller
identity, users, actions, policies, groups, and service accounts; see the
[IAM section of Services](Services.md#iam) for that full read list. This
page covers service account and policy writes, the IAM resources this SDK
writes here. Groups stay read-only.

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

`CreatePolicy`, `UpdatePolicy`, `DeletePolicy`, `AttachServiceAccountPolicy`,
and `DetachServiceAccountPolicy` each refuse to run, sending no request, per
the rules below. Every guard also refuses for a caller whose type cannot be
classified.

| Write | Refused when |
|-|-|
| `CreatePolicy` | The statements grant an IAM write action |
| `UpdatePolicy` | The policy is managed; its current or proposed statements grant an IAM write action; or it is attached to a protected principal or group |
| `DeletePolicy` | The policy is managed, or attached to a group, an IAM user, or a service account |
| `AttachServiceAccountPolicy`, `DetachServiceAccountPolicy` | The policy grants an IAM write action, the target service account already holds one, or the caller is a service account |

A protected principal is the caller, an IAM user or service account holding
a policy that grants an IAM write right (an IAM user counts as protected
through a group's policy too), and a protected group is one with such a
policy attached, or with a protected principal as a member.

| Sentinel | Meaning |
|-|-|
| `iam.ErrSelfChange` | The target service account is the caller, the caller is a service account, or `UpdatePolicy` targets a policy attached to the caller (or to any service account, when the caller is one) |
| `iam.ErrPrivilegedChange` | The target, or something it is attached to, holds a policy that grants an IAM write right, or the caller's own type could not be classified |
| `iam.ErrManagedPolicy` | `UpdatePolicy` or `DeletePolicy` targets a GreenNode-managed policy |
| `iam.ErrInUse` | `DeletePolicy` targets a policy still attached to a group, an IAM user, or a service account |
| `iam.ErrNoSecret` | A create or reset response reported success but carried no client secret |
| `iam.ErrCreateUnconfirmed` | A create succeeded but the read-back that confirms it failed |
| `iam.ErrNotSettled` | `CreatePolicy` or `UpdatePolicy` succeeded but the read-back that confirms it failed; Output keeps the policy's ID |

None of these sentinels name the policy, statement, or action involved, and
there is no way to turn any guard off; make such a change from the IAM
console instead.
