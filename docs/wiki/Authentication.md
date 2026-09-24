# Authentication

The SDK supports IAM User login and static bearer tokens. Root-account and
service-account login are not supported yet.

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
client, err := vngcloud.NewClient(ctx, vngcloud.Config{
	Region: "hcm-3",
}, vngcloud.WithStaticToken("<bearer-token>"))
```

The SDK does not refresh a static token. When it expires, create a new client
with a fresh token.

## Root account

Root login is not supported. The root sign-in page requires a Google reCAPTCHA
before it accepts the password, so it needs a browser step.
