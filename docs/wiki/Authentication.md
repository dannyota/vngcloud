# Authentication

The SDK logs in as an IAM User, uses a static bearer token, or takes tokens
from a custom `CredentialsProvider`. It does not support root-account or
service-account login.

## IAM User

Pass the root account email, the IAM username, and the password to
`vngcloud.WithIAMUser` in `NewConfig`:

```go
cfg, err := vngcloud.NewConfig(
	vngcloud.WithRegion("hcm-3"),
	vngcloud.WithIAMUser(&vngcloud.IAMUserAuth{
		RootEmail: "<root-email>",
		Username:  "<iam-username>",
		Password:  "<password>",
	}),
)
if err != nil {
	log.Fatal(err)
}
```

The SDK caches the access token and refreshes it before it expires. After an
HTTP 401, it logs in again and retries the request once, instead of resending
the token the server just rejected: it invalidates exactly the token that was
sent, but only while that token is still at least 30 seconds old. A token
younger than that is left in place, so a 401 caused by something other than
an expired token (a bad credential, or a server-side revoke) fails the call
with `vngcloud.ErrAuth` instead of forcing a second login inside the same
30-second TOTP window. Concurrent requests that all get a 401 for the same
token cause at most one new login; the rest reuse it once it is ready.

A request that needs authentication, including the retry after a 401, is
never sent without a token: if the token source (an `IAMUserAuth`, a static
token, or a `CredentialsProvider`) has none to give, the call fails with
`vngcloud.ErrAuth` before anything goes out.

## Two-factor codes (TOTP)

Omit `TOTP` when the IAM User has no 2FA. Otherwise, set it on the
`IAMUserAuth` literal with the base32 shared secret so the SDK computes codes
itself:

```go
vngcloud.WithIAMUser(&vngcloud.IAMUserAuth{
	RootEmail: "<root-email>",
	Username:  "<iam-username>",
	Password:  "<password>",
	TOTP:      &vngcloud.SecretTOTP{Secret: "<totp-secret>"},
}),
```

Or supply codes from your own source:

```go
vngcloud.WithIAMUser(&vngcloud.IAMUserAuth{
	RootEmail: "<root-email>",
	Username:  "<iam-username>",
	Password:  "<password>",
	TOTP: vngcloud.TOTPFunc(func(ctx context.Context) (string, error) {
		return promptForCode(ctx)
	}),
}),
```

## Static token

When login hits a captcha, copy a bearer token from a signed-in console session
(browser DevTools) and skip login:

```go
cfg, err := vngcloud.NewConfig(
	vngcloud.WithRegion("hcm-3"),
	vngcloud.WithStaticToken("<bearer-token>"),
)
if err != nil {
	log.Fatal(err)
}
computeClient := compute.New(cfg)
```

The SDK does not refresh a static token. When it expires, build a new
`Config` with `vngcloud.NewConfig` and a fresh token, then build new service
clients from it.

## Custom credentials provider

Implement `vngcloud.CredentialsProvider` to supply tokens from your own
source, such as a secrets manager:

```go
type CredentialsProvider interface {
	Token(ctx context.Context) (vngcloud.Token, error)
	Invalidate(accessToken string)
}
```

`Token` must return a non-empty `AccessToken` with a nil error; an empty
token with a nil error is treated as an authentication failure
(`vngcloud.ErrAuth`). Leave `ExpiresAt` zero to have the SDK call `Token`
before every request, for a source with its own caching. `Invalidate` is
called after an HTTP 401 with the exact token that was rejected, so the
provider can drop it from its own cache; the SDK never calls it with an
empty string.

```go
cfg, err := vngcloud.NewConfig(
	vngcloud.WithRegion("hcm-3"),
	vngcloud.WithCredentialsProvider(myProvider),
)
```

A `CredentialsProvider` wins over `WithStaticToken`, which wins over
`WithIAMUser`. Setting more than one is not an error; only the
highest-precedence one is used.

## Permissions

The SDK can do only what the IAM User's policies allow. For full access, grant
the IAM User broad policies in the console instead of using the root account.
The root sign-in page requires a Google reCAPTCHA, so it cannot be automated.
