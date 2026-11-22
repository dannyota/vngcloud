# Authentication

The SDK logs in as an IAM User or service account, uses a static bearer
token, or takes tokens from a custom `CredentialsProvider`. It does not
support root-account login.

## IAM User

Pass the root account email, the IAM username, and the password to
`vngcloud.WithIAMUser` in `NewConfig`:

```go
cfg, err := vngcloud.NewConfig(
	vngcloud.WithRegion("hcm-3"),
	vngcloud.WithIAMUser(&vngcloud.IAMUserAuth{
		RootEmail: "<root-email>",
		Username:  "<iam-username>",
		Password:  vngcloud.Secret("<password>"),
	}),
)
if err != nil {
	log.Fatal(err)
}
```

`IAMUserAuth.Password` and `SecretTOTP.Secret` use `vngcloud.Secret`. Convert
string variables with `vngcloud.Secret(password)` and
`vngcloud.Secret(seed)`. String literals remain valid. `Secret` redacts
ordinary formatting and JSON; call `Reveal()` only where the value is needed.
Do not use `%p`, gob, or reflection to print secrets.

`WithIAMUser` snapshots the credential fields and a `SecretTOTP` seed when
called. Later mutations do not change the configured identity. Services from
one config share tokens; separate configs have separate memory state and can
share issued tokens through the optional disk cache. Custom TOTP providers
retain their supplied implementation.

The SDK caches the access token and refreshes it before it expires. An HTTP
401 invalidates only the token sent, when at least 30 seconds old or obtained
in the future. Ordinary requests retry once; a younger token is reused, so a
second 401 returns `vngcloud.ErrAuth`. Requests marked to send once never
resend. Concurrent refreshes reuse the winning token. A failed IAM login is
held for 30 seconds within its session to avoid repeating a TOTP code.
Login pages and token replies have a 1 MiB decoded-body limit. Lifespans must
fit a duration and leave more than 30 seconds from exchange request start.

Canceled callers waiting for authentication, refresh, or project discovery
return promptly without changing the holder's token or canceling its work.

A request that needs authentication, including the retry after a 401, is
never sent without a token: if the token source (an `IAMUserAuth`, a static
token, a service account, or a `CredentialsProvider`) has none to give, the
call fails with
`vngcloud.ErrAuth` before anything goes out.

A failed IAM User login itself, from `cfg.Authenticate(ctx)` or the first
call that needs a token, returns `*vngcloud.LoginError` rather than a bare
`vngcloud.ErrAuth`; see [Errors](Errors.md#loginerror) for its fields and
what it never exposes.

## Service account

Pass machine credentials to `NewConfig`:

```go
cfg, err := vngcloud.NewConfig(
    vngcloud.WithRegion("hcm-3"),
    vngcloud.WithProjectID("<project-id>"),
    vngcloud.WithServiceAccount(&vngcloud.ServiceAccountAuth{
        ClientID: "<client-id>",
        ClientSecret: vngcloud.Secret("<client-secret>"),
    }),
)
if err != nil {
    log.Fatal(err)
}
if err := cfg.Authenticate(ctx); err != nil {
    log.Fatal(err)
}
```

`LoadConfig` also accepts `VNGCLOUD_CLIENT_ID`/`VNGCLOUD_CLIENT_SECRET`, or
`client_id`/`client_secret` in the protected credentials file. Whole-source
and method selection follow [Configuration](Configuration.md#precedence).
The CLI's stdin-only setup and rotation steps are on
[Service-account profile](Configuration.md#service-account-profile).

`NewConfig` stays offline. Login occurs on the first authenticated request,
unless `Authenticate` runs it first. The option copies both fields when you
build it. Changing the input struct later does not change the identity.
Nil is unset; a non-nil value requires both fields. Values must contain no
whitespace or control characters and must fit within 4096 bytes. A client
ID cannot contain a colon. The SDK does not trim values or require UUIDs.
Only the selected method is validated; login failures never select another
method.

Services built from one config share memory tokens. Separate configs keep
separate memory tokens, even when they reuse the same option. The SDK uses
the returned token lifespan, measured from request start, with a 30-second
refresh margin. Service-account tokens follow the same exact-token,
30-second rejection rule as IAM-user tokens. A future obtain time also
permits invalidation. A 403 never triggers a refresh. Requests marked to
send once never resend after a 401.

The token exchange uses a fixed HTTPS endpoint with Basic auth and a JSON
client-credentials grant. It sends once, follows no redirects, keeps TLS
verification enabled, and uses no cookie jar or response capture. Public
HTTP clients, transports, retries, and endpoint overrides affect product
requests but never receive service-account secrets. The exchange exposes
no public token endpoint, scope option, token getter, or test hook. A positive
`WithTimeout` applies; otherwise the exchange uses 120 seconds. Caller
cancellation and deadlines bound the exchange. A canceled caller waiting
for shared refresh work returns promptly without sending, changing token
state, or canceling the active exchange.

Failed exchanges return
[`ServiceAccountLoginError`](Errors.md#serviceaccountloginerror).
`Secret` redacts normal formatting and encoding; its documented `%p`, gob,
reflection, and explicit-conversion limits still apply. Authentication
never grants policies or changes product permission checks. NAT
create/delete still require IAM-user login in Hanoi.

### Disk cache and rotation

SDK disk caching is opt-in through `WithTokenCache(dir)`. The CLI enables
its default cache when a home directory exists. Static tokens and custom
providers remain outside the disk cache. Service-account
entries live under `service-account-v1/`, separate from IAM-user entries.
Each key binds the auth kind, profile, client ID, fixed token endpoint, and
client secret. Changing the secret forces a fresh exchange in a rebuilt
config. IAM-user cache hashes keep their existing fields and exclude the
password and TOTP secret. Static tokens and custom providers never use
this disk cache.

The SDK creates cache directories with mode 0700 and token and persistent
lock files with mode 0600. IAM-user and service-account reads refuse symlinks,
nonregular files, mismatched opened inodes, and group/other access. Token
reads stop at 64 KiB. Oversized IAM-user entries fail closed. Windows retains its mode-bit exception. Missing or
malformed content triggers an exchange; unsafe entries or directory,
read, and lock failures fail closed with
[`ErrTokenCache`](Errors.md#serviceaccountloginerror). A successful exchange
still succeeds if its subsequent cache write fails. Atomic writes clean up
failed temporary files.

Processes sharing the same key reuse the winning token under a disk lock.
Platforms without an OS file lock retain process-local locking only. The
cache trusts its parent directories; file modes do not protect against
another process running as the same OS user. A canceled lock wait retains
its pending rejected token for the next acquisition.

Rotate credentials by replacing the protected credentials, rebuilding
configs, and restarting consumers. Old processes and old cache entries can
keep issued tokens until they expire, then fail when they exchange the old
secret. A new secret selects a separate entry; the SDK never purges old
entries or another profile's cache. Rotation does not promise immediate
revocation of issued tokens. Deletion and activation are not verified
emergency revocation mechanisms.

## Two-factor codes (TOTP)

Omit `TOTP` when the IAM User has no 2FA. Otherwise, set it on the
`IAMUserAuth` literal with the base32 shared secret so the SDK computes codes
itself:

```go
vngcloud.WithIAMUser(&vngcloud.IAMUserAuth{
	RootEmail: "<root-email>",
	Username:  "<iam-username>",
	Password:  vngcloud.Secret("<password>"),
	TOTP:      &vngcloud.SecretTOTP{Secret: vngcloud.Secret("<totp-secret>")},
}),
```

Or supply codes from your own source:

```go
vngcloud.WithIAMUser(&vngcloud.IAMUserAuth{
	RootEmail: "<root-email>",
	Username:  "<iam-username>",
	Password:  vngcloud.Secret("<password>"),
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
`WithServiceAccount`, which wins over `WithIAMUser`. Setting more than one
is not an error; only the
highest-precedence one is used.

## Permissions

The SDK can do only what the selected identity's policies allow. For full
access, grant
the IAM User broad policies in the console instead of using the root account.
The root sign-in page requires a Google reCAPTCHA, so it cannot be automated.
