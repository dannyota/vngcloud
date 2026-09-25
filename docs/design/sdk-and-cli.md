# SDK and CLI Design

Status: Approved (2026-09-25).

This design restructures the Go SDK and adds the `vngcloud` command-line tool.
It covers the SDK layout, configuration and credentials, the CLI, errors,
testing, docs, and release order. Write APIs follow
[ADR 0002](../adr/0002-write-api-conventions.md), and each service's writes
get their own design; [billing](billing.md) has the first.

## Goals

- One tool that the owner and AI agents can use to inspect and, later, change
  VNG Cloud resources from a terminal or a script, in the style of the AWS CLI.
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
  the Cloudflare zone.
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
| CLI commands | One operation table per service; flags derived from Input structs |

## SDK

### Root package

`danny.vn/vngcloud` holds only what every service shares:

- `Config`: region, project ID, credentials provider, endpoint overrides,
  HTTP client, and retry settings.
- `LoadConfig(ctx, opts ...LoadOption) (Config, error)`: resolves a `Config`
  from options, environment variables, and profile files (see
  [Configuration](#configuration-and-credentials)).
- `NewConfig(opts ...LoadOption) (Config, error)`: builds a `Config` from
  options only, reading no environment variables or files.
- `CredentialsProvider`: an interface with `Token(ctx)`, which returns a
  valid access token, and `Invalidate(token)`, which drops that token from
  every cache. Built-in providers are IAM User login and static token.
- `*APIError` and `*LoginError` (see [Errors](#errors)).

`Config`, the credentials providers, and the shared session live in
`internal/core`; the root package aliases them. The root package never imports
a service package, and `internal/core` never imports the root package, so no
import cycle is possible.

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

An explicit profile (`--profile` or `WithProfile`) is the exception: its
credentials win over credentials in environment variables, as in the AWS
CLI. This stops a `.env` file for one account from sending `--profile prod`
calls to that account. `VNGCLOUD_PROFILE` is not explicit in this sense.

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
source, an access token wins over IAM User values.

A missing file is not an error. A missing region after all sources is.

### File safety

- `LoadConfig` opens the credentials file, then checks the open file's mode,
  and refuses it when group or others can read it. The error names the file
  and the fix (`chmod 600`). On Windows the check is skipped.
- The SDK never writes the credentials file. Only `vngcloud configure` writes
  it, with mode 0600, through a temp file and rename in the directory of the
  resolved path, so a symlinked file stays a symlink.
- `vngcloud configure` prompts for each value and does not echo the password
  or TOTP secret. When stdin is not a terminal it exits with code 2 instead of
  waiting, so it cannot hang an agent.
- `vngcloud configure set <key> <value>` serves scripts, and
  `configure get <key>` prints a value. `get` and `list` print `password` and
  `totp_secret` masked. Each key has one file:

| Key | File |
|-|-|
| `region`, `project_id`, `output` | config |
| `root_email`, `username`, `password`, `totp_secret` | credentials |

### Token cache

- The SDK caches tokens only when given `vngcloud.WithTokenCache(dir)`. The CLI
  always passes `~/.vngcloud/cache/`. Library users get no disk writes they did
  not ask for.
- The cache directory is created with mode 0700. Each credential set's token
  lives in `<dir>/<hash>.json`, mode 0600. The hash is SHA-256 hex over the
  length-prefixed profile name, root email, username, sign-in URL, and token
  URL. Changing credentials or endpoints therefore stops an old token from
  being used. Static tokens are never cached.
- The file holds the access token and its expiry. The SDK uses a token until
  30 seconds before expiry, as the code does today, then logs in again.
- Concurrent processes share the cache safely. The SDK holds an exclusive
  `flock` on `<hash>.lock` while it reads the cache, logs in if needed, and
  writes the result through a temp file and rename. A second process waits on
  the lock and then finds the fresh token, so two processes never log in with
  the same TOTP code. An unreadable or malformed cache file counts as a miss.
- After an HTTP 401, the transport calls `Invalidate` with the rejected token,
  which clears it from memory and disk. The next `Token` call logs in, and the
  request is retried once. Today's code re-sends the same cached token on that
  retry (`internal/core/auth.go`); `v0.5.0` fixes it.
- A refresh-token grant would avoid full logins. The token endpoint returns a
  refresh token, but the grant is unverified, so it is follow-up work.

## CLI

### Layout

`cmd/vngcloud/main.go` is a thin entry point. Everything else lives in
`internal/cli/`. The CLI adds two dependencies, `github.com/spf13/cobra` and a
JMESPath library; the SDK packages import neither.

### Commands

| Command | Purpose |
|-|-|
| `vngcloud configure` | Prompt for a profile's region and credentials |
| `vngcloud configure set\|get\|list` | Script-friendly profile edits and reads |
| `vngcloud version` | Print the version |
| `vngcloud <service> <operation>` | Call one SDK operation |

Service names match the SDK packages. Operation names are the SDK method names
in kebab case: `ListServers` becomes `list-servers`.

### Operation table

Each service registers its operations in one table:

```go
var computeOps = []cli.Op{
	cli.Read("list-servers", (*compute.Client).ListServers),
	cli.Read("get-server", (*compute.Client).GetServer),
}
```

`cli.Read` and `cli.Write` are generic over the client, Input, and Output
types, so the compiler checks each entry against the SDK method.
[Billing](billing.md#cliwrite-and-clidestructive) defines `cli.Write` and
`cli.Destructive`; the first asynchronous write defines `cli.WaitFor`.

Flags come from the Input struct by reflection. A field name becomes a
kebab-case flag: `ServerID` becomes `--server-id`, and an uppercase run stays
one word, so `VPCID` becomes `--vpcid`. `cli.Flag("VPCID", "vpc-id")` on a
table entry overrides a name. Supported field types are string, integer,
boolean, `[]string`, `time.Time`, and pointers to string, integer, and
boolean, which the CLI sets only when the flag is given. Other field types
are set through `--cli-input-json '<json>'` or
`--cli-input-json file://input.json`, whose keys are the Go field names.

The CLI builds the Input from `--cli-input-json` first, then applies every
flag the user set (cobra reports it `Changed`). It checks
`vngcloud:"required"` fields after that merge, so a required value may come
from either place. Cobra itself marks no flag required.

### Global flags

| Flag | Meaning |
|-|-|
| `--profile`, `--region`, `--project-id` | Override config |
| `--output json\|table\|text` | Output format; default from config, else `json` |
| `--query <jmespath>` | Filter output |
| `--yes` | Confirm a destructive operation |
| `--debug` | Log each request's method, URL, status, and timing to stderr |

`--debug` logs request paths without query strings, and logs the login flow
only as `login started` and `login finished` with the status. It never prints
bodies, headers, cookies, the root email, the authorization code, or any
credential.

### Output

- JSON keys are the SDK's Go field names (`Items[].Name`), not the raw API
  names. Go field names are the SDK's public contract and stay stable when the
  API renames a field. The CLI writes JSON with its own encoder, which uses Go
  field names and ignores JSON tags, because resource models keep their API
  tags.
- Map-backed models (Portal, Container Registry) pass their keys through
  unchanged.
- `--query` runs on that JSON. `table` and `text` render the query result:
  `text` prints tab-separated values, one row per list item, and `table` draws
  a bordered grid.
- Results go to stdout. Errors and debug logs go to stderr.

### Destructive commands

A command registered with `cli.Destructive` fails with exit code 2 and a
message naming `--yes` unless `--yes` is given. It never prompts, so it cannot
hang an agent. Create and update commands run without `--yes`.

## Errors

`*vngcloud.APIError` holds the operation, HTTP status, a stable code, the API
message, and whether the call can be retried. The code is the API's own error
code when the response has one. Otherwise it comes from the status:

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

Login failures, including a suspected captcha, return `*vngcloud.LoginError`.

The CLI prints errors to stderr as:

```json
{"error":{"code":"NotFound","message":"server not found","status":404,"operation":"compute.GetServer"}}
```

| Exit code | Meaning |
|-|-|
| 0 | Success |
| 1 | API or network error, or a cancelled command |
| 2 | Usage or config error: bad flags, a missing `--yes`, a missing region, or an ambiguous project |
| 3 | Missing credentials, bad credentials file, `LoginError`, or a 401 after the retry |
| 4 | `NotFound` |

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
- CLI tests run the root command in-process against an `httptest` fake API.
  They check golden output for `json`, `table`, and `text`, `--query`, every
  exit code, the `--yes` guard, required-field checks after a
  `--cli-input-json` merge, and that `--debug` output holds no token, password,
  TOTP secret, authorization code, root email, or cookie.
- `make live` also runs one CLI read command per service.

## Docs

- The SDK wiki pages in `docs/wiki/` are rewritten for the new layout in the
  release that ships it.
- A hidden `vngcloud gen-docs <dir>` command writes one CLI reference page per
  service from the operation tables. `make gen-docs` writes them into
  `docs/wiki/`, and CI fails when the committed pages differ from the output.

## Releases

Each release ships when CI is green on its commit.

| Version | Content |
|-|-|
| `v0.3.0` | Shared `Config` and the new `billing` and `pricing` packages. Other services stay behind a transitional `vngcloud.NewClient(ctx, cfg)`. Breaking |
| `v0.4.0` | The other services move to packages with the uniform method signature, and `NewClient` goes. Breaking. Built on a branch and merged when every service has moved |
| `v0.5.0` | `LoadConfig`, profile files, environment variables, and the token cache |
| `v0.6.0` | CLI foundation: `configure`, `version`, output, `--query`, errors, generated docs, `billing` and `pricing` commands, and `compute`, `network`, and `dns` read commands |

Budgets and price quotes come first so that spend can be capped and priced
before any paid write lands. New code uses the package layout from the
start, so `v0.3.0` ships the shared `Config` with them. The cost is two
breaking releases instead of one, and one release in which two API styles
coexist. Before `v1.0.0`, with the owner as the only SDK user, that cost is
small. Waiting for the whole restructure would give one break but delay
budgets by nine service moves. The CLI cannot ship earlier, because it needs
`LoadConfig`.

Install is `go install danny.vn/vngcloud/cmd/vngcloud@<version>`, pinned to a
tag, until prebuilt binaries get their own design.

After `v0.6.0`, designs follow aboutme's needs in this order, each covering
the SDK and CLI together:

1. vCDN: origin IP ranges first, then cache rules, the origin header, and the
   certificate.
2. vMonitor: pausing and resuming checks first, then alarms, log projects, and
   synthetic checks.
3. vDNS record writes.
4. vStorage buckets and service-account keys.

A discovery pass for vCDN and vMonitor can start now, because it needs no SDK
code. It confirms which console APIs an IAM User token can call, and it
answers aboutme's open provider questions about vCDN where the API shows them.

CLI read commands for the other services, and compute, volume, and network
writes, come after these. OpenTofu covers those writes for aboutme.
