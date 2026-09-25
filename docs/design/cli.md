# CLI Design

Status: Approved (2026-09-25).

This design defines the `vngcloud` command-line tool: its dependencies,
commands, flags, output, safety guards, `configure`, and exit codes. It builds
on [SDK and CLI](sdk-and-cli.md), which owns the SDK surface, configuration
precedence, the token cache, the error types, and the release order. Write
commands follow [ADR 0002](../adr/0002-write-api-conventions.md).

## Layout

`cmd/vngcloud/main.go` is a thin entry point. Everything else lives in
`internal/cli/`. The CLI calls the API only through the public SDK packages.

## Dependencies

Only `cmd/vngcloud` and `internal/cli` import these. The SDK packages stay
standard-library only.

| Module | Version | Reason |
|-|-|-|
| `github.com/spf13/cobra` | v1.10.x | Nested commands, flags, and help in the AWS CLI style |
| `github.com/jmespath/go-jmespath` | v0.4.0 | `--query` with the standard JMESPath the AWS CLI uses; no further dependencies |
| `golang.org/x/term` | latest | `configure` reads secrets from a terminal without echo |

Cobra brings `github.com/spf13/pflag`, and
`github.com/inconshreveable/mousetrap` on Windows. `golang.org/x/term` brings
`golang.org/x/sys`. Any other dependency needs the owner's approval and a
reason.

## Commands

| Command | Purpose |
|-|-|
| `vngcloud configure` | Prompt for a profile's region and credentials |
| `vngcloud configure set\|get\|list` | Script-friendly profile edits and reads |
| `vngcloud version` | Print the version |
| `vngcloud <service> <operation>` | Call one SDK operation |

Service names match the SDK packages. Operation names are the SDK method names
in kebab case: `ListServers` becomes `list-servers`.

## Building the Config

Every `<service> <operation>` command builds its `Config` with
`vngcloud.LoadConfig`, so the CLI resolves profiles, environment variables,
and files exactly as the SDK does. It passes:

- `WithProfile(name)` only when `--profile` is given. `VNGCLOUD_PROFILE` stays
  a non-explicit profile, as [precedence](sdk-and-cli.md#precedence) defines.
- `WithRegion` and `WithProjectID` only when `--region` and `--project-id` are
  given.
- `WithTokenCache("~/.vngcloud/cache")`, always, with `~` resolved to the home
  directory. When the home directory cannot be found, the CLI passes no cache.
- `WithLogger` only when `--debug` is given (see [Debug](#debug)).

`LoadConfig` sends no request. `configure`, `version`, and `gen-docs` do not
call it.

The CLI reads the profile's `output` and `read_only` keys through
`Config.ProfileSetting(key)`, which returns that key's value in the resolved
profile's config section, or an empty string. It reads nothing else from the
files itself, so profile selection has one implementation.

## Operation table

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

## Global flags

| Flag | Meaning |
|-|-|
| `--profile`, `--region`, `--project-id` | Override config |
| `--output json\|table\|text` | Output format; default from the profile's `output`, else `json` |
| `--query <jmespath>` | Filter output |
| `--yes` | Confirm a destructive operation |
| `--read-only` | Refuse every write command (see [Read-only](#read-only)) |
| `--debug` | Log requests to stderr (see [Debug](#debug)) |

## Output

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

## Destructive commands

A command registered with `cli.Destructive` fails with exit code 2 and a
message naming `--yes` unless `--yes` is given. It never prompts, so it cannot
hang an agent. Create and update commands run without `--yes`.

## Read-only

Read-only lets the owner hand an agent a profile that cannot change anything.
Any of these turns it on:

| Source | On when |
|-|-|
| `read_only` in the profile's config section | `true` |
| `VNGCLOUD_READ_ONLY` | `1` or `true` |
| `--read-only` | Given |

While read-only is on, every command registered with `cli.Write` fails with
exit code 2 before any request, whether or not it is destructive. The message
names the setting that turned it on, for example
`read-only: refused by read_only in profile "agent"`. Read commands, including
`pricing get-quote`, run as usual.

- Each source can only turn read-only on. `--read-only=false` does not turn
  off a profile or environment setting, and no flag or variable does.
- `VNGCLOUD_READ_ONLY` applies even with an explicit `--profile`, because it
  only restricts.
- An empty value, `false`, or `0` leaves that source off. Any other value is a
  config error with exit code 2 that names the key or variable, so a typo
  such as `read_only = ture` cannot leave writes open.
- The CLI checks the flag and variable before `LoadConfig`, and the profile
  key right after it. A `LoadConfig` error still wins, and no request has
  been sent either way.
- SDK library calls ignore read-only. `Config.ProfileSetting` exposes the key,
  but the SDK never acts on it.

Read-only guards against an agent's mistakes, not against a process that can
edit `~/.vngcloud`. Such a process can remove the key. `configure` is not a
`cli.Write` command, so it still runs.

## Debug

`--debug` gives `LoadConfig` a `slog` text logger on stderr at Debug level.
Without the flag the CLI passes no logger. What the SDK logs through it is in
[SDK and CLI](sdk-and-cli.md#logging): one line per request, and
`login started` and `login finished` for IAM login. `cli.Write` commands add
`write started` and `write finished` around the call, as
[billing](billing.md#cliwrite-and-clidestructive) defines.

Nothing in debug output holds a body, header, cookie, token, root email,
authorization code, or credential.

## configure

`vngcloud configure` prompts for each value. It reads the password and TOTP
secret with `golang.org/x/term`, without echo. When stdin is not a terminal it
exits with code 2 at once.

`configure set <key> <value>` serves scripts, and `configure get <key>` prints
a value; `get` and `list` mask `password` and `totp_secret`. Each key has one
file:

| Key | File |
|-|-|
| `region`, `project_id`, `output`, `read_only` | config |
| `root_email`, `username`, `password`, `totp_secret` | credentials |

### Secrets stay out of argv

`configure set <key> -` reads the value from stdin, so it never appears in
argv, `ps` output, or shell history. It reads all of stdin and drops one
trailing newline (`\n` or `\r\n`). An empty value is refused with exit code 2.

`configure set password <value>` and `configure set totp_secret <value>` with
a literal value are refused with exit code 2. The message names the
`configure set <key> -` form and does not echo the value. Other keys accept a
literal value or `-`.

### File writes

`configure` resolves the config and credentials paths with the same rules as
`LoadConfig`: the default under `~/.vngcloud/`, or `VNGCLOUD_CONFIG_FILE` and
`VNGCLOUD_SHARED_CREDENTIALS_FILE`. Each write:

1. Creates `~/.vngcloud` with mode 0700 when a default path is used and the
   directory is missing. A missing parent of a path from an environment
   variable is an error.
2. Resolves symlinks in the path. A target that exists and is not a regular
   file is an error.
3. Writes the new content to a temp file with mode 0600 in the directory of
   the resolved path, syncs it, and renames it over the resolved path. A
   symlinked file stays a symlink, and a crash leaves either the old or the
   new file, never a partial one.
4. Removes the temp file on any failure.

Both files end with mode 0600, whatever mode the old file had. `configure`
changes only the lines for the keys it sets, adding the section or key when
missing, and keeps every other line, comments included. Errors name the file
and never a value.

## Errors and exit codes

The CLI prints errors to stderr as one JSON line:

```json
{"error":{"code":"NotFound","message":"server not found","status":404,"operation":"compute.GetServer"}}
```

For an `*APIError`, `code` is `APIError.Code`, which falls back to the
status-derived code (see [Errors](sdk-and-cli.md#errors)). Other errors omit
`status` and `operation`, and `code` names the class: `InvalidUsage`,
`ReadOnly`, `InvalidConfig`, `NoCredentials`, `LoginFailed`, or
`RequestFailed`.

| Exit code | Meaning |
|-|-|
| 0 | Success |
| 1 | API or network error, or a cancelled command |
| 2 | Usage or config error: bad flags, a missing `--yes`, a read-only refusal, a literal secret in `configure set`, a missing region, or an ambiguous project |
| 3 | `ErrNoCredentials`, `ErrCredentialsFile`, a `*LoginError`, or a 401 after the retry |
| 4 | `NotFound` |

`ErrNoCredentials` and `ErrCredentialsFile` also match `ErrInvalidConfig`, so
the CLI checks them first.

## Security

- A read-only refusal, a missing `--yes`, and a missing required field all
  stop before any request.
- Secrets never come from argv. `configure` reads them without echo or from
  stdin.
- Debug output and error messages hold no secret. Tests check both.
- Every `cli.Write` command, and `configure`'s file writes, get an adversarial
  review before release.

## Testing

- CLI tests run the root command in-process against an `httptest` fake API.
- They check golden output for `json`, `table`, and `text`, `--query`, every
  exit code, the `--yes` guard, and required-field checks after a
  `--cli-input-json` merge.
- Read-only tests cover each source, `--read-only=false` against a profile
  and variable that set it, a bad value, and that the fake API receives no
  request.
- `--debug` tests assert that output holds no token, password, TOTP secret,
  authorization code, root email, cookie, or query string.
- `configure` tests use a temporary home directory and cover the 0700
  directory, 0600 files, a symlinked file staying a symlink, kept comments and
  other sections, `set <key> -` from stdin, and the refusal of a literal
  `password` or `totp_secret`.
- `make live` also runs one CLI read command per service.

## Docs

A hidden `vngcloud gen-docs <dir>` command writes one CLI reference page per
service from the operation tables. `make gen-docs` writes them into
`docs/wiki/`, and CI fails when the committed pages differ from the output.

## Configure under read-only

`configure` and `configure set` refuse to run, with exit code 2, while
`VNGCLOUD_READ_ONLY` or `--read-only` is on, so an agent cannot clear a
profile's `read_only` through the CLI. `configure get` and `configure list`
still work. This guards against mistakes, not against a process that can edit
`~/.vngcloud` directly.
