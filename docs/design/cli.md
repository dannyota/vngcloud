# CLI Design

Status: Approved.

This design defines the `vngcloud` command-line tool: its dependencies,
commands, flags, output, safety guards, `configure`, and exit codes. It builds
on [SDK and CLI](sdk-and-cli.md), which owns the SDK surface, configuration
precedence, the token cache, the error types, and the release order. Write
commands follow [ADR 0002](../adr/0002-write-api-conventions.md).
The accepted [service-account design](service-account-auth.md) extends these
rules; its eight decisions are approved, implementation is pending.

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
| `vngcloud version`, `vngcloud --version` | Print the version |
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
call it. Service-account selection uses the SDK's accepted
[profile rules](service-account-auth.md#profiles-environment-and-cli), without
an auth flag or login command. Cache preflight waits for auth selection so
service accounts get the fixed cache error; other auth kinds keep their
existing preflight behavior. See [errors][sa-errors].

The CLI reads the profile's `output` and `read_only` keys through
`Config.ProfileSetting(key)`, which returns that key's value in the resolved
profile's config section, or an empty string. It reads nothing else from the
files itself, so profile selection has one implementation.

## Unattended CI

The supported first job is scheduled GitHub Actions renewal of the free
Synthetic quota with a renew-only service account. Supply
`VNGCLOUD_CLIENT_ID`, `VNGCLOUD_CLIENT_SECRET`, `VNGCLOUD_REGION`, and
`VNGCLOUD_PROJECT_ID` through the job environment. Use no --profile,
configure, stdin credentials, browser, or login command. Inject secrets at
execution, never in argv or shell tracing. Keep VNGCLOUD_ACCESS_TOKEN,
VNGCLOUD_PROFILE, config/credentials path overrides, and other credential
sources unset so precedence cannot select another identity.

Code inspection establishes the environment path: `internal/core/load.go:297`
selects service-account credentials and validates without echoing values;
missing default files are accepted at line 182. It still probes default
files, and existing invalid files can fail loading. Use a clean runner.
`internal/core/service_account_exchange.go:49` exchanges credentials lazily
with no user interaction; its private fixed-host client rejects redirects,
has no capture/browser hook, and retains no peer error text. Fixed exchange
logs contain only start/finish and success. Credential formatting is
redacted at `internal/core/service_account_auth.go:32`; token error fields
are fixed at `internal/core/errors.go:181`. Product logs omit headers,
body, and query at `internal/transport/transport.go:607`; quota projections
scrub sent tokens and suppress captures at `monitor/quotas.go:184`.

No pre-existing profile or credential file is required. This is not zero
filesystem I/O: `internal/cli/config.go:34` enables the automatic token
cache when a home exists, with 0600 files and a 0700 cache directory. Use
an ephemeral writable home and do not upload the cache as a job artifact.
Existing cache errors fail closed without interaction. A clean runner needs
no auth flow change. Test the environment-only CLI path with empty stdin,
no profiles, and synthetic auth/product endpoints. Check stdout, stderr,
debug, errors, and partial results for client ID, secret, Basic header,
and bearer canaries during failure and refresh.

Install a pinned release with the supported Go toolchain:

```sh
GOSUMDB=sum.golang.org GOPRIVATE= GONOSUMDB= \
  go install danny.vn/vngcloud/cmd/vngcloud@vX.Y.Z
```

Replace vX.Y.Z with the reviewed version, never latest. Keep checksum
verification enabled for the module and dependencies; installation or
checksum failure stops the job. The local `go help module-auth` describes
hash verification and its environment controls. This verifies downloaded
module content, not a release binary or build attestation. Release binaries
and provenance attestations remain a later installation option.

The [quota outcome contract][quota-outcomes] owns exact JSON, classes,
send certainty, and exits. Required CLI changes: `internal/cli/op.go:496`
currently prints only selected partial write errors; `internal/cli/main.go:15`
prints only stderr on earlier failures. Add scoped result handling for both
new writes, including recognized-command flag/input/config failures, and
for partial quota-status reads. Add QuotaChanged and QuotaStatusIncomplete
classification, preserve status for NotSettled, and keep other commands'
output contracts unchanged. Never infer send certainty in CLI code; use
SDK/transport evidence, except proven pre-SDK refusal. Refuse --query on
these writes. No valid JSON after process termination is an unknown result.

Require a short Monitor-Quotas wiki section containing the four variables,
the outcome/exit table, and this sequence using a fresh expiry:

```sh
quota_json=$(vngcloud monitor get-synthetic-quota --output json) || exit $?
expiry=$(printf '%s' "$quota_json" | jq -er '.Quota.EndTime') || exit $?
vngcloud monitor renew-synthetic-quota --expected-end-time "$expiry" \
  --max-days-left 7 --max-price 0 --output json
```

The wiki supplies JSON parsing and exit handling around the write, using
no --no-wait. Pre/post status reads use `quota-status --kind synthetic`;
this needs no Email/SMS permission or classes/settings request. Serialize
jobs per account/kind, persist the baseline and a pending-attempt marker
before invocation, and retain it on missing JSON or an unconfirmed result,
including NotSettled. New processes have no SDK ledger. Schedules reconcile
by reads or stop; they never blindly retry or purchase. Confirmed outcomes
or proven no-effect refusals/skips close the marker. The outcome table requires
no message parsing. This slice designs CI use, not a repository workflow.

[quota-outcomes]: monitor-quota-coverage.md#machine-readable-write-outcomes

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
kebab-case flag: `ServerID` becomes `--server-id`, and a run of capitals
followed by a lowercase letter gives its last capital to the next word, so
`VirtualIPAddressID` becomes `--virtual-ip-address-id`. One shared rename
table holds the exceptions: `VPCID` becomes `--vpc-id`, `CDNID` becomes
`--cdn-id`, and `Query` becomes `--search` so no Input flag shadows a
global flag. Supported field types are
string, integer, `float64`, boolean, and pointers to each; the CLI applies
a field only when its flag is given. Other field types are set
through `--cli-input-json '<json>'` or `--cli-input-json file://input.json`,
whose keys are the Go field names.

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
| `--yes` | Confirm a destructive or permission-changing operation |
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
- A list command prints an object with its rows under `Items`, and page
  metadata beside it. No command prints a bare array; see
  [CLI usability](cli-usability.md#list-output-shape).
- `--query` runs on that JSON. `table` and `text` render the query result:
  `text` prints tab-separated values, one row per list item, and `table` draws
  a bordered grid. Both escape C0, DEL, C1, and bidirectional format characters
  in strings; JSON writes these characters as `\u` escapes, except for its
  standard `\t`, `\n`, `\r`, `\b`, and `\f` escapes. They and
  `--query` work on JSON numbers as float64, so an integer above 2^53 may
  round there; plain `json` output keeps it exact.
- Results go to stdout. Errors and debug logs go to stderr.

## Destructive commands

A command registered with `cli.Destructive` fails with exit code 2 and a
message naming `--yes` unless `--yes` is given. It never prompts, so it cannot
hang an agent. Create and update commands run without `--yes`.

## Secret files

A command that would otherwise print a secret instead writes it to a file
named by a `--secret-file <path>` flag, refuses an existing path or a
symlink, and creates the file at mode 0600. `storage create-s3-key`,
`create-service-account`, and `reset-service-account-secret` are such
commands; see [vStorage](storage-cli.md#create-s3-key) and
[IAM writes](iam-writes.md).

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
- SDK calls generally ignore read-only. Quota lifecycle writes and the new
  synthetic recover/renew methods are exceptions: they check the loaded
  profile's read_only key before requests, as `monitor/quotas_lifecycle.go:82`
  does. True/1 refuses with ReadOnly; invalid values fail with InvalidConfig.
  They do not inspect the CLI flag or VNGCLOUD_READ_ONLY themselves.

Read-only guards against an agent's mistakes, not against a process that can
edit `~/.vngcloud`. Such a process can remove the key. `configure` has its own
[read-only rules](#configure-under-read-only). Server-enforced rights come
from IAM policies, as [auth scope](service-account-auth.md#scope-and-evidence)
defines.

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
a value; `get` and `list` mask `password`, `totp_secret`, and `client_secret`
in every format. Each key has one file:

| Key | File |
|-|-|
| `region`, `project_id`, `output`, `read_only` | config |
| `root_email`, `username`, `password`, `totp_secret` | credentials |
| `client_id`, `client_secret` | credentials |

Interactive IAM User prompts stay unchanged. Service-account setup uses
explicit commands; client ID display, fresh profiles, read-only ordering,
rotation, and create/reset secret recovery follow the accepted
[CLI profile rules](service-account-auth.md#profiles-environment-and-cli).

### Secrets stay out of argv

`configure set <key> -` reads the value from stdin, so it never appears in
argv, `ps` output, or shell history. It bounds stdin to 4096 bytes and drops
one trailing newline (`\n` or `\r\n`). Empty or oversized input is refused
with exit code 2.

`configure set` refuses literal values for `password`, `totp_secret`, and
`client_secret` with exit code 2. The message names the
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
`ReadOnly`, `InvalidConfig`, `NoCredentials`, `LoginFailed`,
`ServiceAccountLoginFailed`, `TokenCacheFailed`,
`RequestFailed`, `QueryFailed`, `PageFormat` (a public page such as the
CDN IP range FAQ changed format; see [CDN](cdn.md)), `UnexpectedStatus` (a
vMonitor check, or a vCDN CDN or certificate, has a status the SDK does
not toggle from, so nothing was sent), `StatusUnconfirmed` (a vMonitor
pause or resume, or a vCDN enable or disable, may have landed but a read
did not confirm it; see
[vMonitor](monitor.md#after-errstatusunconfirmed) and
[vCDN CLI](cdn-cli.md#errors)), `ZoneBusy` (a vDNS
zone stayed busy past the wait before a write, so nothing was sent),
`WriteFailed` (a vDNS write went to status `ERROR`, or, per
[vMonitor Alerts](monitor-alerts.md#errors), a vMonitor log project or
alarm wait failed), or `NotSettled` (a vDNS write, a vMonitor Alerts write,
or a [vStorage](storage.md#delete-bucket) bucket delete was accepted but did
not settle within the wait; do not repeat it). vMonitor
Alerts also adds `OTPRejected` (a channel OTP was wrong or expired, so no
create was sent) and `PriceAboveMax` (a log project quote exceeded
`--max-price`, so no order was sent); see
[vMonitor Alerts](monitor-alerts.md#errors). [vStorage](storage.md#errors)
adds `BucketNotEmpty` (`delete-bucket` refused a bucket holding objects,
so nothing was sent) and `SecretFileFailed` (`create-s3-key` could not
write `--secret-file` after the create, so it deleted the new key).
[IAM writes](iam-writes.md) adds `SelfChange`, `PrivilegedChange`, and
`ManagedPolicy`.
For `WriteFailed` and `NotSettled` the CLI also prints the Output on stdout;
see [vDNS](dns.md#after-a-write). `UnexpectedStatus`, `StatusUnconfirmed`,
`ZoneBusy`, `WriteFailed`, `NotSettled`, `OTPRejected`, `PriceAboveMax`,
`BucketNotEmpty`, `SecretFileFailed`, `SelfChange`, `PrivilegedChange`, and
`ManagedPolicy` exit 1. A not-found result that
is not an `*APIError`, such as a lookup by listing, also prints `NotFound`
and exits 4. `QueryFailed` means
`--query` failed after the operation succeeded; for a write, the message
says the write succeeded, so an agent does not retry it.

| Exit code | Meaning |
|-|-|
| 0 | Success |
| 1 | API or network error, or a cancelled command |
| 2 | Usage or config error: bad flags, a missing `--yes`, a read-only refusal, a literal secret in `configure set`, a missing region, an ambiguous project, or an input the SDK or server refuses (`ErrInvalidInput`; a server refusal keeps its `APIError.Code`, such as `112`) |
| 3 | `ErrNoCredentials` (including `cdn.ErrNoAPIKey`, which wraps it), `ErrCredentialsFile`, a login error matching `ErrAuth`, or a 401 after the retry (a vMonitor toggle write never retries, so its first 401 exits 3) |
| 4 | `NotFound` |

`ErrNoCredentials` and `ErrCredentialsFile` also match `ErrInvalidConfig`, so
the CLI checks them first. A cancelled context anywhere in the error chain,
including during login, is checked before all of them and exits 1. The
exceptions are `StatusUnconfirmed` and `NotSettled`, checked before the
cancelled context, because a write may have landed.

Service-account validation, login, cache, and cancellation codes follow
[the accepted mappings][sa-errors], including exit 1 for a canceled login
and `TokenCacheFailed`. Product 401/403 mappings remain unchanged.

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
  `password`, `totp_secret`, or `client_secret`. Service-account masking,
  reserved config keys, source selection, and cache failures follow its
  [deterministic checks](service-account-auth.md).
- `make live` also runs one CLI read command per service.

## Docs

A hidden `vngcloud gen-docs <dir>` command writes one CLI reference page per
service from the operation tables. `make gen-docs` writes them into
`docs/wiki/`, and CI fails when the committed pages differ from the output.
Service-account SDK, CLI, cache, and public docs ship together under the
[release contract](service-account-auth.md#owner-decisions-and-release-order).

## Configure under read-only

`configure` and `configure set` refuse writes with exit code 2 when the flag,
variable, or selected profile enables read-only. `configure get` and
`configure list` still work. Clearing the profile setting requires editing
the file by hand; no flag or configure command bypasses the latch. Apply the
accepted [rotation rules](service-account-auth.md#profiles-environment-and-cli).
This guards against mistakes, not against a process that can edit
`~/.vngcloud` directly.

[sa-errors]: service-account-auth.md#errors-and-secret-handling
