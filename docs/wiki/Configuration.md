# Configuration

## LoadConfig

`vngcloud.LoadConfig(ctx, opts...)` resolves a `Config` from options,
environment variables, and AWS-style profile files, the way the AWS CLI
resolves its own configuration. `vngcloud.NewConfig` builds a `Config` from
options only, reading no environment variables or files; `LoadConfig` is the
default choice for a program or script that should pick up a profile:

```go
cfg, err := vngcloud.LoadConfig(ctx, vngcloud.WithRegion("hcm-3"))
if err != nil {
	log.Fatal(err)
}
```

### Precedence

Highest first:

1. Options passed to `LoadConfig`.
2. Environment variables.
3. The resolved profile's section in the config and credentials files.

An empty value from an option, an environment variable, or a file key counts
as unset. Credentials resolve as one set from the first source that sets any
credential value, so a profile's password is never mixed with another
source's username. Within one source, an access token wins over IAM User
values.

`WithProfile(name)` selects a profile **explicitly**; `VNGCLOUD_PROFILE`
never does. An explicit profile skips environment variables for credentials
and the project ID, so a `.env` file meant for one account can never send a
call made with another profile to that account. Region names no account, so
it may still come from the environment even with an explicit profile. An
explicit profile with no credentials, from options or its own file section,
fails with `vngcloud.ErrNoCredentials` naming the profile.

### Environment variables

| Variable | Meaning |
|-|-|
| `VNGCLOUD_PROFILE` | Profile name; default `default` |
| `VNGCLOUD_REGION` | Region |
| `VNGCLOUD_PROJECT_ID` | Project ID |
| `VNGCLOUD_ROOT_EMAIL`, `VNGCLOUD_USERNAME`, `VNGCLOUD_PASSWORD`, `VNGCLOUD_TOTP_SECRET` | IAM User credentials |
| `VNGCLOUD_ACCESS_TOKEN` | Static token; wins over IAM User values |
| `VNGCLOUD_CONFIG_FILE` | Config file path |
| `VNGCLOUD_SHARED_CREDENTIALS_FILE` | Credentials file path |

### Files

By default `LoadConfig` reads `~/.vngcloud/config` and
`~/.vngcloud/credentials`, or the paths from `WithConfigFile` /
`WithSharedCredentialsFile` or their environment variables. A missing
default file is fine; a missing explicit path is an error.

```ini
# ~/.vngcloud/config
[default]
region = hcm-3

[profile dev]
region = han-1
project_id = <project-id>
```

```ini
# ~/.vngcloud/credentials (mode 0600)
[default]
root_email = <root-email>
username = <iam-username>
password = <password>
totp_secret = <base32-secret>

[dev]
root_email = <root-email>
username = <iam-username>
password = <password>
```

Config sections other than `default` use the `profile` prefix; credentials
sections use the bare profile name either way. Unknown keys are ignored. A
named profile that appears in neither file is an error naming it. The
credentials file has no access token key: a static token comes only from
`WithStaticToken` or `VNGCLOUD_ACCESS_TOKEN`.

### File safety

`LoadConfig` opens the credentials file, checks the open file's mode, and
refuses it when group or others can read it, naming the file and the fix
(`chmod 600`); this check is skipped on Windows. A path that is not a
regular file, such as a directory or a FIFO, is an error. Every error from
`LoadConfig` matches `vngcloud.ErrInvalidConfig` via `errors.Is`; a missing
credential set also matches `vngcloud.ErrNoCredentials`, and a credentials
file problem (missing at an explicit path, unreadable, refused for unsafe
permissions, or malformed) also matches `vngcloud.ErrCredentialsFile`. No
error from `LoadConfig` names a credential value.

## Region

`Config.Region` selects the region, for example `hcm-3` or `han-1`. A client
serves one region.

## Project

`Config.ProjectID` is optional. When a method needs a project and none is set,
the SDK lists the projects visible to the IAM User in the region and uses the
only match. If more than one project matches, set `ProjectID`.

List projects yourself with the `project` package:

```go
projects, err := project.New(cfg).ListProjects(ctx, nil)
```

Or for another region:

```go
projects, err := project.New(cfg).ListProjects(ctx, &project.ListProjectsInput{
	Region: "han-1",
})
```

## Endpoints

The SDK targets the GreenNode domains (`*.console.greennode.ai`,
`signin.greennode.ai`) by default. The old `*.vngcloud.vn` hosts redirect to
them and drop the Authorization header on the way, so the SDK refuses
cross-host redirects and returns an error instead.

Point any product at another host with `WithEndpointOverrides`, passed to
`NewConfig` alongside the region and auth options:

```go
cfg, err := vngcloud.NewConfig(
	vngcloud.WithRegion("hcm-3"),
	vngcloud.WithIAMUser(iamUser),
	vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{
		Dashboard: "https://custom-dashboard.example.com",
	}),
)
if err != nil {
	log.Fatal(err)
}
computeClient := compute.New(cfg)
```

`Dashboard` sets the OAuth redirect URI used during IAM login. Setting it also
derives the `Token` endpoint, unless you set `Token` too.

## Retries

The SDK retries network errors and HTTP 429, 502, 503, and 504 with jittered
exponential backoff, and honors `Retry-After`. Change the count and base
interval with `WithRetry(count, interval)`. Cancel the context to stop
retrying. `vngcloud.IsRetryable(err)` reports whether a returned error was
retryable.

## Token cache

`vngcloud.WithTokenCache(dir)` turns on an on-disk token cache shared across
processes, so a program run repeatedly, or several programs sharing one
profile, log in only once per token lifetime instead of once per process:

```go
cfg, err := vngcloud.NewConfig(
	vngcloud.WithRegion("hcm-3"),
	vngcloud.WithIAMUser(iamUser),
	vngcloud.WithTokenCache("/home/user/.vngcloud/cache"),
)
```

Only IAM User credentials use the cache. A static token or a custom
`CredentialsProvider` is never written to disk, and without
`WithTokenCache` the SDK writes nothing.

`WithProfile(name)` adds a profile name to the cache key, so tokens for
different profiles sharing one cache directory never collide. Direct
`NewConfig` callers usually do not need it; `LoadConfig` sets it from the
resolved profile automatically.

Each credential set gets one file, `<dir>/<hash>.json`, named by a SHA-256
hash of the profile, root email, username, sign-in URL, and token URL, so
changed credentials or endpoints never reuse another entry's token. The
cache directory is created with mode 0700, and each token file with mode
0600; a pre-existing directory that group or others can access is refused.

One locked operation per credential set reads the cached token, logs in only
if it is missing, expiring within 30 seconds, or was rejected by the server
at least 30 seconds ago, and writes the result back, so two processes
sharing one profile never log in with the same TOTP code. The lock is tried
without blocking, then retried with backoff until the call's context ends;
a lock file is never deleted, and a failed cache write after a successful
login still returns the fresh token. On Linux, macOS, and the BSDs the lock
uses `flock`; on Windows it opens the lock file with an exclusive share
mode. A platform with neither locks within the process only, which still
avoids duplicate logins from one process but not from several.
