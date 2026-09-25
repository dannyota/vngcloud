# Authentication

The SDK logs in as an IAM User, or uses a static bearer token. It does not
support root-account or service-account login.

## IAM User

Pass the root account email, the IAM username, and the password:

```go
IAMUser: &vngcloud.IAMUserAuth{
	RootEmail: "<root-email>",
	Username:  "<iam-username>",
	Password:  "<password>",
}
```

The SDK caches the access token, refreshes it before it expires, and retries a
request once after an HTTP 401.

## Two-factor codes (TOTP)

Omit `TOTP` when the IAM User has no 2FA. Otherwise, give the SDK the base32
shared secret so it computes codes itself:

```go
TOTP: &vngcloud.SecretTOTP{Secret: "<totp-secret>"},
```

Or supply codes from your own source:

```go
TOTP: vngcloud.TOTPFunc(func(ctx context.Context) (string, error) {
	return promptForCode(ctx)
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
client, err := vngcloud.NewClient(ctx, cfg)
```

The SDK does not refresh a static token. When it expires, create a new client
with a fresh token.

## Permissions

The SDK can do only what the IAM User's policies allow. For full access, grant
the IAM User broad policies in the console instead of using the root account.
The root sign-in page requires a Google reCAPTCHA, so it cannot be automated.
