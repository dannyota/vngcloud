# Guides

## Authentication

Only IAM User auth is supported. The SDK caches access tokens, refreshes before
expiry, and retries once after an HTTP 401 by refreshing the token.

`TOTP` is optional. Omit it when the IAM User account does not enforce OTP.

Use a shared secret when the SDK should compute the code:

```go
IAMUser: &vngcloud.IAMUserAuth{
	RootEmail: "<root-email>",
	Username:  "<iam-username>",
	Password:  "<password>",
	TOTP:      &vngcloud.SecretTOTP{Secret: "<totp-secret>"},
}
```

Use `TOTPFunc` when the caller wants to provide codes from another source:

```go
IAMUser: &vngcloud.IAMUserAuth{
	RootEmail: "<root-email>",
	Username:  "<iam-username>",
	Password:  "<password>",
	TOTP: vngcloud.TOTPFunc(func(ctx context.Context) (string, error) {
		return promptForCode(ctx)
	}),
}
```

Root-user auth is not supported because the browser login flow requires a
reCAPTCHA challenge. Service account auth is also not supported.

### Static Token (Captcha Workaround)

When IAM User login is blocked by a captcha challenge, use `WithStaticToken`
to bypass the login flow entirely. Capture a bearer token from an
authenticated console session (browser DevTools) and pass it to the client:

```go
client, err := vngcloud.NewClient(ctx, vngcloud.Config{
	Region: "hcm-3",
}, vngcloud.WithStaticToken("<bearer-token>"))
```

The SDK skips IAM login and uses the provided token for all requests. No
IAM User credentials are needed. The token is not refreshed automatically;
when it expires, create a new client with a fresh token.

### Dashboard Endpoint Override

`EndpointOverrides.Dashboard` controls the OAuth `redirectUri` used during
IAM login. Overriding `Dashboard` automatically derives the `Token`
endpoint unless `Token` is also set explicitly:

```go
client, err := vngcloud.NewClient(ctx, vngcloud.Config{
	Region: "hcm-3",
	IAMUser: &vngcloud.IAMUserAuth{ /* ... */ },
}, vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{
	Dashboard: "https://custom-dashboard.example.com",
}))
```

This is useful when targeting a non-default environment where the dashboard
host differs from the production default.

## Project Discovery

`ProjectID` is optional in `Config`.

When `ProjectID` is omitted, the client can still be created. Service methods
that need a project lazily list projects visible to the IAM User for the
configured region. If exactly one project is found, that project ID is used.

Users can inspect projects explicitly:

```go
projects, err := client.ListProjects(ctx, nil)
```

To override the region for listing:

```go
projects, err := client.ListProjects(ctx, &vngcloud.ListProjectsOptions{
	Region: "hcm-3",
})
```

If VNG Cloud supports multiple projects per region for an account, pass
`ProjectID` explicitly.

## Basic Example

The basic example demonstrates IAM User auth, project discovery, read-only API
calls, and local JSON output capture.

```bash
cp examples/basic/config.example.json examples/basic/config.local.json
$EDITOR examples/basic/config.local.json
go run ./examples/basic -config examples/basic/config.local.json
```

The config file supports multiple regions:

```json
{
  "regions": ["hcm-3", "han-1"],
  "rootEmail": "<root-email>",
  "username": "<iam-username>",
  "password": "<password>",
  "totpSecret": "<totp-secret-or-empty>",
  "totpCode": ""
}
```

Do not put `ProjectID` in the example config. The SDK demonstrates project
discovery automatically.

For OTP-enabled IAM Users, set either:

- `totpSecret`: a base32 shared secret, so the example computes codes.
- `totpCode`: a current one-time code for one immediate run.

Leave both empty when the IAM User does not enforce OTP.

The example writes local output under:

```text
examples/basic/output/
```

That directory is ignored by git. It may contain sensitive data and should not be
shared. The example validates the raw/sdk output shape before it exits.
