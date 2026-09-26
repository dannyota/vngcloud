# SDK and CLI Design

Status: Approved (2026-09-25).

This design restructures the Go SDK and adds the `vngcloud` command-line tool.
It covers the SDK layout, configuration and credentials, the CLI, errors,
testing, docs, and release order. Write APIs follow
[ADR 0002](../adr/0002-write-api-conventions.md), and each service's writes
get their own design; [billing](billing.md) has the first.

## Goals

- One tool that the owner and AI agents can use to inspect and, later, change
  GreenNode resources from a terminal or a script, in the style of the AWS CLI.
- An SDK whose public surface stays small and predictable as write APIs are
  added.
- Credentials handled like the AWS CLI: profiles on disk, environment
  overrides, and a cached access token so most commands skip login.

## First user

The first user is [aboutme](https://github.com/dannyota/aboutme), whose
production moves to GreenNode (aboutme ADR 0051). Its OpenTofu provider covers
servers, volumes, security groups, and floating IPs, but not vStorage buckets,
vCDN, vDNS, or vMonitor. aboutme's plan applies those with API scripts or
console steps. This CLI replaces those scripts and console steps, so services
the OpenTofu provider lacks come before write APIs it already has.

aboutme needs, from the CLI, most frequent first:

- Every deploy: read vCDN's origin IP ranges, so the deploy stops when they
  change, and pause and resume the vMonitor app-down check (deploy steps 5
  and 9).
- Setup and changes: vCDN cache rules, origin request header, and
  certificate; vMonitor log projects, alarms, and synthetic HTTPS checks.
- Once, at the nameserver move: vDNS records for `aboutme.vn`, created to match
  the Cloudflare zone. vDNS hosts private zones only, so it cannot serve this
  need, and aboutme keeps its public DNS elsewhere (see [vDNS](dns.md)).
- Setup: vStorage buckets and per-bucket service-account keys.
- Checks: reads of servers and security groups on the OpenTofu-managed host.

## Non-goals

- Root-account login. The root sign-in page requires a Google reCAPTCHA, so it
  cannot be automated. Users who need full access grant an IAM User broad
  policies.
- Service-account login.
- Prebuilt binaries and OS keyring storage. Each gets its own design when
  needed.
- A generated API model format like AWS botocore.

## Decisions

| Topic | Decision |
|-|-|
| Auth | IAM User login (password, optional TOTP) or a static bearer token |
| Credential storage | AWS-style `~/.vngcloud/config` and `~/.vngcloud/credentials` files, plus environment variables |
| SDK layout | One public package per service, like the AWS SDK for Go v2 |
| Method shape | `Method(ctx, *Input) (*Output, error)` for every operation |
| CLI output | `--output json\|table\|text`, JSON by default, JMESPath `--query` |
| Destructive commands | Fail unless `--yes` is given; never prompt |
| Read-only profiles | A profile key, variable, or flag makes the CLI refuse every write |
| CLI commands | One operation table per service; flags derived from Input structs |

## SDK

### Root package

`danny.vn/vngcloud` holds only what every service shares:

- `Config`: region, project ID, credentials provider, endpoint overrides,
  HTTP client, retry settings, and logger.
- `Config.ProfileSetting(key) string`: the value of `key` in the resolved
  profile's config section, or an empty string. `NewConfig` configs have no
  profile and always return an empty string. The CLI reads `output` and
  `read_only` through it; the SDK never acts on either.
- `LoadConfig(ctx, opts ...LoadOption) (Config, error)`: resolves a `Config`
  from options, environment variables, and profile files (see
  [Configuration](#configuration-and-credentials)).
- `NewConfig(opts ...LoadOption) (Config, error)`: builds a `Config` from
  options only, reading no environment variables or files.
- `CredentialsProvider`: an interface with `Token(ctx)`, which returns a
  valid access token, and `Invalidate(token)`, which drops that token from
  every cache. Built-in providers are IAM User login and static token. For a
  custom provider, an empty token with a nil error is an error, and a zero
  expiry makes the SDK call `Token` before every request.
- `*APIError` and `*LoginError` (see [Errors](#errors)).

`Config`, the credentials providers, and the shared session live in
`internal/core`; the root package aliases them. The root package never imports
a service package, and `internal/core` never imports the root package, so no
import cycle is possible.

### Logging

`WithLogger(*slog.Logger)` is the only way to get logs; without it the SDK
logs nothing. With it:

- The transport logs each HTTP attempt at Debug level as `request` with the
  method, the URL path without the query string, the status, and the
  duration. An attempt with no response has no status.
- IAM User login logs only `login started` and `login finished` with the
  outcome, `ok` or `failed`. Its own HTTP requests are not logged.

Nothing else is logged: no body, header, cookie, token, root email,
authorization code, or credential. Tests assert this for a full login and a
request with a query string.

### Shared session

`LoadConfig` and `NewConfig` attach one session to the `Config`. The session
holds the credentials provider with its in-memory token and the discovered
project ID. Every service client built from the same `Config` shares it, so a
program with a compute and a network client logs in once and discovers the
project once. Two logins inside one 30-second TOTP window would fail, because
the server rejects a reused code.

### Service packages

Each service is a public package with `New(cfg vngcloud.Config) *Client`.
Service packages may import each other for shared models; `network` imports
`compute` for `compute.Server`.

| Package | Covers |
|-|-|
| `compute` | Servers, SSH keys, server groups, images |
| `volume` | Volumes, volume types, snapshots |
| `network` | VPCs, subnets, security groups, routes, endpoints |
| `loadbalancer` | Regional load balancers and certificates |
| `globalloadbalancer` | Global load balancers |
| `dns` | vDNS hosted zones and records |
| `containerregistry` | Container registry repositories and users |
| `portal` | Account info, zones, and quotas |
| `project` | The project listing that `internal/core` keeps for discovery |
| `billing`, `pricing` | Budgets, cost, balances, and quotes; see [billing](billing.md) |
| `cdn` | CDN IP ranges; see [CDN](cdn.md) |
| `monitor` | vMonitor synthetic checks, notification channels, log projects, and alarms; see [vMonitor](monitor.md) |
| `storage` | vStorage regions, projects, and buckets; see [vStorage](storage.md) |
| `iam` | Service accounts and S3 keys; see [vStorage](storage.md) |

Every operation has one signature:

```go
func (c *Client) GetServer(ctx context.Context, in *GetServerInput) (*GetServerOutput, error)
```

Methods that take bare arguments today, such as `GetServer(ctx, id)` and
`ListSubnets(ctx)`, move to Input structs. A nil Input is valid when every
field is optional. Output structs name their fields. List outputs hold `Items`,
plus the page metadata the code has today (`Page`, `PageSize`, `TotalPage`,
`TotalItem`) for APIs that return it.

Input and Output fields use Go names only; they carry no JSON tags. A required
Input field has the tag `vngcloud:"required"`, and the SDK returns an error
before any request when it is empty. Resource models such as
`compute.Server` keep their API JSON tags, because they decode responses
directly.

List inputs carry `Page` and `Size`. The default size is 10000
(`DefaultPageSize`), so one call returns every item for every API the SDK
covers today. An API that caps page size states its default and cap in its
design; the SDK has no pagination helper until a design adds one.

Service code lives in its public package. `internal/core`,
`internal/transport`, `internal/endpoints`, `internal/routes`, and
`internal/iamuser` stay internal, and `vngcloud.go` re-exports no service
types. The move to packages changed no API coverage: every method kept its
behavior under the new signature.

## Configuration and credentials

### Files

```ini
# ~/.vngcloud/config
[default]
region = hcm-3
output = json

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
```

As in the AWS CLI, config sections other than `default` use the `profile`
prefix, and credentials sections use the bare profile name.

A small INI parser in `internal/` reads both files, so the SDK adds no
dependency. It supports sections, `key = value` lines, and full-line `#` and
`;` comments. Anything else is a parse error that names the file and line.

### Precedence

Highest first:

1. Options passed to `LoadConfig`, which the CLI fills from `--profile`,
   `--region`, and `--project-id`.
2. Environment variables.
3. The selected profile in the files.

An empty value from an option, a variable, or a file key counts as unset.
An explicit profile (`--profile` or `WithProfile`) skips step 2 for
credentials and the project ID, so a `.env` file for one account cannot send
`--profile prod` calls to that account. Region names no account, so it may
still come from the environment. An explicit profile with no credential in its
credentials section, and none from options, is an error naming it.
`VNGCLOUD_PROFILE` is never explicit.

| Variable | Meaning |
|-|-|
| `VNGCLOUD_PROFILE` | Profile name; default `default` |
| `VNGCLOUD_REGION` | Region |
| `VNGCLOUD_PROJECT_ID` | Project ID |
| `VNGCLOUD_ROOT_EMAIL`, `VNGCLOUD_USERNAME`, `VNGCLOUD_PASSWORD`, `VNGCLOUD_TOTP_SECRET` | IAM User credentials |
| `VNGCLOUD_ACCESS_TOKEN` | Static token; wins over password login |
| `VNGCLOUD_CONFIG_FILE` | Config file path |
| `VNGCLOUD_SHARED_CREDENTIALS_FILE` | Credentials file path |

Credentials resolve as a set: `LoadConfig` takes the credentials from the
first source, in the order above, that sets any credential value, so a
profile's password is never mixed with another source's username. Within one
source, an access token wins over IAM User values. A credentials provider
option wins over both. The credentials file has no access token key.

`LoadConfig` errors match `ErrInvalidConfig` under `errors.Is`. Two also match
their own sentinel: `ErrNoCredentials` when no source sets credentials, and
`ErrCredentialsFile` when the credentials file is missing at an explicit path,
unreadable, or refused. The CLI
[exit codes](cli.md#errors-and-exit-codes) follow these sentinels.

### File safety

- A missing file is an error only when an option, `VNGCLOUD_CONFIG_FILE`, or
  `VNGCLOUD_SHARED_CREDENTIALS_FILE` named its path. A path that cannot be read
  or is not a regular file is an error. Unknown keys are ignored. Errors name
  the file and the line or the fix, never a value.
- `LoadConfig` opens the credentials file, then checks the open file's mode,
  and refuses it when group or others can read it. The error names the file
  and the fix (`chmod 600`). On Windows the check is skipped.
- The SDK never writes the config or credentials file. Only
  [`vngcloud configure`](cli.md#configure) writes them.

### Token cache

- Only `vngcloud.WithTokenCache(dir)` turns on caching, so library users get
  no disk writes they did not ask for. The CLI passes `~/.vngcloud/cache/`.
- The cache directory has mode 0700. Each credential set's token lives in
  `<dir>/<hash>.json`, mode 0600. The hash is SHA-256 hex over the
  length-prefixed profile name, root email, username, sign-in URL, and token
  URL, so changed credentials or endpoints never reuse an old token. Static
  tokens are never cached.
- The file holds the access token, its expiry, and its login time. The SDK
  uses a token until 30 seconds before expiry, then logs in again. An
  unreadable or malformed cache file counts as a miss.
- One locked operation on `<hash>.lock` reads the cache, applies any pending
  invalidation, logs in if needed, and writes the result through a temp file
  and rename. A second process then finds the fresh token, so two processes
  never log in with the same TOTP code.
- The lock is tried without blocking and retried with backoff until the
  call's context ends, so a stuck process cannot hang another. Unix uses
  `flock`; Windows opens the lock file with share mode 0 through
  `syscall.CreateFile`. Either lock ends with its process. Lock files are kept.
- A failed cache write after a successful login is not a call error: the SDK
  returns the fresh token and removes its temp file.
- After an HTTP 401, the transport invalidates exactly the token it sent:
  memory and disk are cleared only while they still hold it. The request is
  retried once with a new token. A token whose login time is under 30 seconds
  old is not invalidated; the call returns `ErrAuth`. So a 401 that a new login
  cannot fix never causes repeated logins or two logins in one TOTP window.
  A request that needs auth is never sent without a token.
- The refresh-token grant is unverified, so every renewal is a full login.

## CLI

The CLI design is in [CLI](cli.md): dependencies, commands, flags, output,
read-only profiles, `configure`, and exit codes.

## Errors

`*vngcloud.APIError` holds the operation, HTTP status, a stable code, the API
message, and whether the call can be retried. `APIError.Code` is the API's own
error code when the response has one. When the API gives none, including a
null envelope `code` or an envelope `code` equal to the HTTP status, it falls
back to the code for the status:

| Status | Code |
|-|-|
| 400 | `BadRequest` |
| 401 | `Unauthorized` |
| 403 | `Forbidden` |
| 404 | `NotFound` |
| 409 | `Conflict` |
| 429 | `Throttled` |
| 5xx | `ServerError` |
| Other 4xx | `ClientError` |

Every login failure returns `*vngcloud.LoginError`. It wraps `ErrAuth`, so
`errors.Is(err, vngcloud.ErrAuth)` is true, except when the context ended
during login: then it wraps `ctx.Err()` instead. It holds the HTTP status of the
failed step, when there is one, and `CaptchaSuspected`, which is true when the
sign-in page shows the form again after a submit. Its message is fixed text
plus the status and, when suspected, a captcha hint. It never holds a
password, TOTP secret or code, token, cookie, authorization code, root email,
or username, and neither does any error it wraps.

The CLI output and exit codes for these errors are in
[CLI](cli.md#errors-and-exit-codes).

## Testing

- Fixture tests move with each service unchanged, except for the new
  signatures.
- `LoadConfig` tests use a temporary home directory and cover every precedence
  level, credential-set resolution, and the file permission check.
- The INI parser has table tests, including malformed lines.
- Token cache tests use a fake clock, injected into the login path, and cover
  reuse, expiry, invalidation when credentials or endpoints change, a malformed
  cache file, two concurrent processes sharing one login, and a 401 retry that
  sends a new token.
- A test builds two service clients from one `Config` and checks a single
  login.
- Error tests cover each status-to-code fallback and a `*LoginError` that
  matches `ErrAuth`, with and without a suspected captcha.
- CLI tests are in [CLI](cli.md#testing).

## Docs

- The SDK wiki pages in `docs/wiki/` are rewritten for the new layout in the
  release that ships it.
- CLI reference pages are generated from the operation tables, as
  [CLI](cli.md#docs) defines.

## Releases

Each release ships when CI is green on its commit.

| Version | Content |
|-|-|
| `v0.3.0` | Shared `Config` and the new `billing` and `pricing` packages. Other services stay behind a transitional `vngcloud.NewClient(ctx, cfg)`. Breaking |
| `v0.4.0` | The other services move to packages with the uniform method signature, and `NewClient` goes. Breaking. Built on a branch and merged when every service has moved |
| `v0.5.0` | `LoadConfig`, profile files, environment variables, and the token cache |
| `v0.6.0` | CLI foundation: `configure`, `version`, output, `--query`, read-only profiles, `--debug` logging, generated docs, `billing` and `pricing` commands, and `compute`, `network`, and `dns` read commands. SDK: `APIError.Code` fallback, `*LoginError`, `WithLogger` logging, and `Config.ProfileSetting` |
| `v0.7.0` | `cdn.ListIPRanges` and `vngcloud cdn list-ip-ranges`: the CDN IP ranges read from GreenNode's public FAQ page; see [CDN](cdn.md) |
| `v0.8.0` | vMonitor checks: list, get, pause, and resume, SDK and CLI; see [vMonitor](monitor.md) |
| `v0.9.0` | vMonitor checks: create and delete, and probe locations; see [vMonitor](monitor.md) |
| `v0.10.0` | vDNS private hosted zones: create, update, and delete, with waits; see [vDNS](dns.md) |
| `v0.11.0` | vDNS records: create, update, and delete, with the zone-lock wait; see [vDNS](dns.md) |

Budgets and price quotes come first so that spend can be capped and priced
before any paid write lands. New code uses the package layout from the
start, so `v0.3.0` ships the shared `Config` with them. The cost is two
breaking releases instead of one, and one release in which two API styles
coexist. Before `v1.0.0`, with the owner as the only SDK user, that cost is
small. Waiting for the whole restructure would give one break but delay
budgets by nine service moves. The CLI cannot ship earlier, because it needs
`LoadConfig`.

Install is `go install danny.vn/vngcloud/cmd/vngcloud@<version>`.

After `v0.6.0`, designs follow aboutme's needs in this order, each covering
the SDK and CLI together:

1. vCDN origin IP ranges, in `v0.7.0` ([CDN](cdn.md)).
2. vMonitor synthetic checks ([vMonitor](monitor.md)): read, pause, and
   resume in `v0.8.0`, then create, delete, and probe locations in `v0.9.0`.
   Channels, check alerting, log projects, and alarms follow in
   [vMonitor Alerts](monitor-alerts.md).
3. vDNS private zone and record writes ([vDNS](dns.md)): zones in
   `v0.10.0`, records in `v0.11.0`. vDNS has no public zone, so these serve
   private names inside a VPC, not aboutme's nameserver move.
4. vStorage buckets and service-account keys ([vStorage](storage.md)):
   reads, then bucket writes, S3 keys, service accounts, bucket policy, and
   bucket settings.

vCDN cache rules, the origin header, and the certificate stay console steps
for aboutme: vCDN has no public API, and its console accepts only root login
(see [CDN non-goals](cdn.md#non-goals)).

Discovery found that an IAM User token can call the vMonitor uptime API and
the vStorage console API. [vMonitor Alerts](monitor-alerts.md) and
[vStorage](storage.md) cover channels, check alerting, log projects, alarms,
buckets, S3 keys, and service accounts.

vMonitor Alerts releases M1 to M8 and vStorage releases S1 to S6 are built in
parallel, outside the `v0.x.y` sequence above; each gets its version number
when it ships, so no version rows are reserved for them here.

CLI read commands for the other services, and compute, volume, and network
writes, come after these. OpenTofu covers those writes for aboutme.
