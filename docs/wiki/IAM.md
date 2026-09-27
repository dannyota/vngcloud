# IAM

`iam` is `danny.vn/vngcloud/iam`, with its own `New(cfg)`. It reads caller
identity, users, actions, policies, groups, and service accounts; see the
[IAM section of Services](Services.md#iam) for that full read list. This
page covers service account writes, the IAM resources this SDK writes here.
Policies and groups stay read-only.

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
whatever the target:

| Sentinel | Meaning |
|-|-|
| `iam.ErrSelfChange` | The target service account is the caller, or the caller is a service account |
| `iam.ErrPrivilegedChange` | The target holds a policy that grants an IAM write right, or the caller's own type could not be classified |
| `iam.ErrNoSecret` | A create or reset response reported success but carried no client secret |
| `iam.ErrCreateUnconfirmed` | A create succeeded but the read-back that confirms it failed |

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
