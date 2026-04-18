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
