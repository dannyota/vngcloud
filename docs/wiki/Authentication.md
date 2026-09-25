# Authentication

The SDK logs in as an IAM User, or uses a static bearer token. It does not
support root-account or service-account login.

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

The SDK caches the access token, refreshes it before it expires, and retries a
request once after an HTTP 401.

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
client, err := vngcloud.NewClient(ctx, cfg)
```

The SDK does not refresh a static token. When it expires, build a new
`Config` with `vngcloud.NewConfig` and a fresh token, then build a new client
from it.

## Permissions

The SDK can do only what the IAM User's policies allow. For full access, grant
the IAM User broad policies in the console instead of using the root account.
The root sign-in page requires a Google reCAPTCHA, so it cannot be automated.
