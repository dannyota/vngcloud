# vMonitor Design

Status: Accepted for `v0.8.0` (2026-09-26); create and delete proposed.

This design adds vMonitor synthetic checks, which GreenNode calls uptime
checks, to the SDK and CLI. aboutme pauses its app-down check at deploy
step 5 and resumes it at step 9, so reading, pausing, and resuming come
first. Creating and deleting checks come next. Pause and resume use a toggle
API; [ADR 0003](../adr/0003-toggle-writes.md) sets the rules for it.

It builds on [SDK and CLI](sdk-and-cli.md): package per service,
`Method(ctx, *Input) (*Output, error)`, operation tables, and the error
model. Writes follow [ADR 0002](../adr/0002-write-api-conventions.md).

## Source

The API is undocumented: `docs.api.greennode.ai` does not list it. Live
discovery on 2026-09-26, with an SDK-issued IAM User token and no extra
header, found the uptime manager at
`https://vmonitor.console.greennode.ai/vmonitor-uptime-manager/v1`:

| Call | Result |
|-|-|
| `GET /uptimes` | 200, a JSON array of checks |
| `GET /uptimes/{id}` | 200, one check |
| `POST /uptimes` | 201, the new check |
| `DELETE /uptimes/{id}` | 204 |
| `PUT /uptimes/status/{id}` | 204; empty body; flips `status` between `ENABLED` and `DISABLED` |
| `GET /locations` | 200, public probe locations such as `SYNTT-VN-HCM01` and `SYNTT-VN-HAN01` |

The console's create body has `type` `API`, `subtype` `HTTP`, `name`,
`config.request` (`url`, `method`, `headers`, `query`, `body`, `timeout`,
`verified_ssl`), `config.assertions` (by default one:
`status_code` `does_not_match_regex` `[4-5][0-9][0-9]`),
`options` (`test_frequency`, `tests`, `failed_locations`), `locations` (IDs),
and `notifications` (`In-alarm`, `Up`, and `Undetermined` lists).
`test_frequency` is in minutes. The server requires a name of 5 to 30
characters that starts with a letter. Checks draw on a prepaid package
quota; the test account has 10 API tests free until 2026-10-26.

[Fixture work](../../instructions/live-data.md#adding-or-fixing-an-api)
settles the rest; see [Open questions](#open-questions).

## Non-goals

These wait for their own discovery and design:

- Notification channels. They live under
  `/notification-gateway/api/v1/notification/...`, which is not mapped.
- Alarms and log projects. Not discovered.
- Updating a check. Not discovered. Delete and create replace one.
- Check types other than `API`/`HTTP`, private locations, and check
  results or history.
- Finding a check by name. Names may not be unique; callers pass the ID.

## Decisions

| Topic | Decision |
|-|-|
| Package | New public package `monitor` |
| Resource name | `Check`, with `CheckID` in Inputs and `--check-id` in the CLI |
| Scope | Per account: no region in the URL and no project ID sent (assumed) |
| Pause and resume | Target-state operations over the toggle, per ADR 0003 |
| Toggle transport | Sent once: `transport.Request.Once` |
| Create | No quote, `verified_ssl` always on, no notifications yet |

## Endpoint

`endpoints.Set` gains `Monitor`, default
`https://vmonitor.console.greennode.ai/`, and `endpoints.Overrides` gains a
matching `Monitor` override. `internal/routes` gains `ProductMonitor`.
`Monitor` is the host root, as `Billing` is, because notification channels
live under a second prefix. Uptime paths are
`vmonitor-uptime-manager/v1/...` under it.

The host carries no region, so the SDK assumes checks are per account and
ignores the configured region, as billing does. It sends no project ID.
`Config` still requires a region, because the rest of the SDK needs one.

## SDK

### Package

```go
package monitor

func New(cfg vngcloud.Config) *Client

func (c *Client) ListChecks(ctx context.Context, in *ListChecksInput) (*ListChecksOutput, error)
func (c *Client) GetCheck(ctx context.Context, in *GetCheckInput) (*GetCheckOutput, error)
func (c *Client) PauseCheck(ctx context.Context, in *PauseCheckInput) (*PauseCheckOutput, error)
func (c *Client) ResumeCheck(ctx context.Context, in *ResumeCheckInput) (*ResumeCheckOutput, error)
func (c *Client) CreateCheck(ctx context.Context, in *CreateCheckInput) (*CreateCheckOutput, error)
func (c *Client) DeleteCheck(ctx context.Context, in *DeleteCheckInput) (*DeleteCheckOutput, error)
func (c *Client) ListLocations(ctx context.Context, in *ListLocationsInput) (*ListLocationsOutput, error)

var (
	ErrUnexpectedStatus = errors.New("monitor: unexpected check status")
	ErrStatusUnconfirmed = errors.New("monitor: check status not confirmed")
)

const (
	StatusEnabled  = "ENABLED"
	StatusDisabled = "DISABLED"
)
```

Operation names are `monitor.<Method>`.

| Operation | Method and path | Input | Output |
|-|-|-|-|
| `ListChecks` | `GET /uptimes` | none | L[Check] |
| `GetCheck` | `GET /uptimes/{id}` | `CheckID` (r) | `{Check Check}` |
| `PauseCheck` | see [Pause and resume](#pause-and-resume) | `CheckID` (r) | `{Check Check; Changed bool}` |
| `ResumeCheck` | see [Pause and resume](#pause-and-resume) | `CheckID` (r) | `{Check Check; Changed bool}` |
| `CreateCheck` | `POST /uptimes` | see [Create](#create) | `{Check Check}` |
| `DeleteCheck` | `DELETE /uptimes/{id}` | `CheckID` (r) | `{}` |
| `ListLocations` | `GET /locations` | none | L[Location] |

"(r)" marks `vngcloud:"required"`, and "L[T]" is `core.List[T]`. A nil
Input is valid for the two lists. `ListChecksInput` and `ListLocationsInput`
have no fields today, so later filters break no caller. If the capture shows
that `GET /uptimes` pages, this design is amended before code.

### Identifiers

Every operation that puts `CheckID` in a path checks it with
`core.CheckPathID` (`^[A-Za-z0-9-]+$`) before any request, reads included,
as billing does. The ID format is not captured yet. If live IDs hold another
character, this design states the wider pattern before code.

### Models

`Check` and `Location` keep their API JSON tags. The models need these
fields; the capture fixes their JSON names and types:

| Model | Fields |
|-|-|
| `Check` | `ID`, `Name`, `Type`, `Subtype`, `Status`, `Config` (`Request`, `Assertions`), `Options`, `Locations`, and created and updated times when present |
| `CheckRequest` | `URL`, `Method`, `Headers`, `Query`, `Body`, `Timeout`, `VerifiedSSL` |
| `Assertion` | `Type`, `Operator`, `Target` |
| `CheckOptions` | `TestFrequency` (minutes), `Tests`, `FailedLocations` |
| `Location` | `ID`, `Name`, and region or country fields when present |

`Notifications` is left out of `Check` until channels are designed, rather
than kept as `json.RawMessage`; adding it later breaks no caller. `Status`
is a plain string, so an unknown value decodes and reaches the caller.

## Pause and resume

`PauseCheck` drives a check to `DISABLED` and `ResumeCheck` to `ENABLED`.
Both share one implementation with a target status `T`. They follow
[ADR 0003](../adr/0003-toggle-writes.md):

1. Check `CheckID` is set and matches the path pattern. On failure, return
   `ErrInvalidInput` and send nothing.
2. Read the check with `GET /uptimes/{id}`, a normal read with normal
   retries. An error, such as `NotFound`, returns at once; nothing was sent.
3. If `Status` equals `T`, return the check with `Changed: false`. Send
   nothing.
4. If `Status` is neither `ENABLED` nor `DISABLED`, return an error wrapping
   `ErrUnexpectedStatus` that names the status, cut to 64 bytes. Send
   nothing. The SDK never flips a state it does not understand.
5. Send `PUT /uptimes/status/{id}` with no body and `Once` set. Any 2xx is
   accepted.
6. If the response is a 4xx (including 401, 403, 404, 409, and 429), or the
   dial failed, the server did not act. Return that error.
7. Otherwise, whether the PUT returned 2xx, 5xx, or failed after
   connecting (timeout, reset, cancelled context), confirm by reading. Read
   the check at once, then after 1, 2, and 4 seconds, stopping at the first
   read whose `Status` is `T`. Each read is a normal read. The waits honour
   `ctx` and use an injected clock in tests.
8. When a confirm read shows `T`, return that check with `Changed: true`,
   even if the PUT itself failed: the check reached the target, and this
   call sent the only toggle it saw.
9. When no confirm read shows `T`, or the reads fail, return an error
   wrapping `ErrStatusUnconfirmed`. It also wraps the PUT error and the last
   read error, when there are ones. Output is nil.

The confirm reads bound the wait at about 7 seconds plus request time, per
ADR 0002 rule 7. They cover a server whose reads lag its writes; whether
the real API lags is an open question the live test answers.

`Changed` lets a caller restore the prior state. A deploy that pauses at
step 5 resumes at step 9 only when the pause reported `Changed: true`, so a
check a person paused for maintenance stays paused.

### No retry

Every resend of the `PUT`, even after a 429 or a failed dial, acts on the
status read in step 2, which gets older with each backoff. Running the whole
operation again is safe instead, because it reads first. So the SDK sends
at most one `PUT` per call, the CLI never retries, and the transport gains
`transport.Request.Once bool`. With `Once` set, the transport:

- sends the request once: no retry after any status or network error,
  including 429, 5xx, and a failed dial;
- on a 401, invalidates the sent token as usual and returns the error
  without resending;
- sets `APIError.Retryable` true only for a 429 or a failed dial, where the
  server did not act. A caller that retries on it reruns the operation.

`internal/core` passes the field through. Requests without `Once` keep
today's behavior.

### The race

Two callers that read `ENABLED` at the same moment and both pause leave the
check `ENABLED`. Each one's confirm reads then fail to see `DISABLED`, so
each gets `ErrStatusUnconfirmed`. The API has no conditional request, so the
SDK can detect this race but not prevent it. Within one process, a
`monitor.Client` runs its pause and resume calls one at a time under a
mutex. Across processes, the caller serializes; aboutme runs one deploy at
a time.

A toggle that a 5xx hid can land after the last confirm read. A caller
whose rerun also fails to confirm stops and alerts a person, never loops.

## Create

`CreateCheckInput` is flat, so most fields map to CLI flags:

| Field | Type | Rule |
|-|-|-|
| `Name` | string | (r). The server requires 5 to 30 characters, starting with a letter |
| `URL` | string | (r). Sent as `config.request.url` |
| `Locations` | []string | (r). Location IDs from `ListLocations` |
| `Method` | string | Empty sends `GET` |
| `Headers`, `Query` | type per capture | Nil sends what the console sends for none |
| `Body` | string | Empty sends what the console sends for none |
| `Timeout` | int | Unit per capture; 0 sends the console default |
| `TestFrequency` | int | Minutes; 0 sends the console default |
| `Tests` | int | 0 sends the console default |
| `FailedLocations` | int | 0 sends the console default |
| `Assertions` | []Assertion | Empty sends the console default shown in [Source](#source) |

The SDK always sends `type: "API"`, `subtype: "HTTP"`,
`verified_ssl: true`, and `notifications` with three empty lists. The
console defaults for the zero values come from the captured create body and
live as constants in the package. Zero is never a valid value for those
fields, so ADR 0002 rule 3 needs no pointer.

Per ADR 0002 rule 5, the SDK checks only that required fields are set. The
name rule, frequency range, and location validity stay on the server, whose
error reaches the caller as an `*APIError`.

Create semantics:

- `POST` is not idempotent, so the transport retries it only after a 429 or
  a failed dial (ADR 0002 rule 2).
- The 201 response holds the new check. A response without an `ID` is an
  `*APIError`; the SDK never finds a new check by listing names.
- After a 5xx or a network error, the check may exist. The caller lists
  checks and looks for its name before trying again.

### Why no quote

ADR 0002 rule 8 asks every paid create for a quote. A check is not billed
per create: it uses one slot of a package the account bought in the
console. Within the quota a create costs nothing more, and past it the
server is expected to refuse. So `CreateCheck` has no quote. If the capture
shows that a create past the quota bills the account instead of failing, it
becomes a paid create and needs a quote before release.

### Why no notifications yet

Channel IDs come from the notification gateway, which is not mapped. A
check created now alerts nobody, and the wiki page says so. aboutme's
app-down check keeps its console-set notifications; pause and resume do not
touch them. A `Notifications` field is added when channels are designed,
which breaks no caller.

## Delete

`DeleteCheck` sends `DELETE /uptimes/{id}` and returns an empty Output on
204. `DELETE` is idempotent, so the transport retries it as a read. A retry
that finds the check gone returns `NotFound`; the SDK does not hide it.

## Errors

| Case | Result | CLI code and exit |
|-|-|-|
| Missing `CheckID`, bad ID shape, missing create field | `ErrInvalidInput`, no request | `InvalidUsage`, 2 |
| Unknown check | `NotFound` if the server sends 404 | `NotFound`, 4 |
| Status other than `ENABLED` or `DISABLED` | `ErrUnexpectedStatus`, no toggle | `UnexpectedStatus`, 1 |
| Toggle sent or maybe sent, target not confirmed | `ErrStatusUnconfirmed` | `StatusUnconfirmed`, 1 |
| 4xx or failed dial on the toggle | That `*APIError` | As for any `*APIError` |
| Name rule, quota, or bad location on create | The server's `*APIError` | 1 |

If the capture shows an unknown check is not a 404, this design adds a
mapping like [billing's](billing.md#errors) before code.

`ErrStatusUnconfirmed` errors say what happened and the fix, for example
`monitor: check status not confirmed: toggle sent, check still ENABLED; run
PauseCheck again, which reads the status first`. The CLI checks
`ErrStatusUnconfirmed` before its cancelled-context rule, so a Ctrl-C during
the `PUT` still reports that the toggle may have landed. The CLI error codes
list in [CLI](cli.md#errors-and-exit-codes) gains `UnexpectedStatus` and
`StatusUnconfirmed`.

## CLI

`svc_monitor.go` registers the table:

| Command | Kind | Needs `--yes` | Release |
|-|-|-|-|
| `monitor list-checks` | Read | No | `v0.8.0` |
| `monitor get-check` | Read | No | `v0.8.0` |
| `monitor pause-check` | Write | No | `v0.8.0` |
| `monitor resume-check` | Write | No | `v0.8.0` |
| `monitor create-check` | Write | No | `v0.9.0` |
| `monitor delete-check` | Write, destructive | Yes | `v0.9.0` |
| `monitor list-locations` | Read | No | `v0.9.0` |

- Pause and resume are writes, so a [read-only](cli.md#read-only) profile
  refuses them with exit 2 before any request. They are not destructive,
  per ADR 0002 rule 6: `resume-check` undoes `pause-check`.
- `delete-check` needs `--yes`: a deleted check and its history cannot be
  restored with one command.
- `pause-check --check-id <id>` prints `{"Check": {...}, "Changed": true}`.
  A deploy script reads `Changed` with `--query Changed`.
- `create-check` takes `--name`, `--url`, `--method`, `--body`,
  `--timeout`, `--test-frequency`, `--tests`, and `--failed-locations` as
  flags. `Locations`, `Headers`, `Query`, and `Assertions` are not flag
  types today, so they come through `--cli-input-json`.

aboutme's deploy saves `pause-check --query Changed`, and runs
`resume-check` from a trap when it was `true`, so a failed deploy does not
leave monitoring off. The CLI cannot enforce the trap.

## Security

- Every write in this design gets an adversarial review before its release.
  The review checks: the toggle is sent at most once at every layer; no
  toggle when the status is already the target or unknown; the confirm
  reads never lead to a second `PUT`; `Retryable` on the toggle; path ID
  checks on every call; `--yes` on delete; read-only refusal of all four
  writes; and no create retry after a 5xx.
- A check's request headers and body may hold a credential for the
  monitored service. The SDK returns them as the API does, and `get-check`
  and `list-checks` print them. They never appear in `--debug` output,
  which logs no body, or in error messages. The wiki page warns against
  putting long-lived secrets in check headers.
- Monitored URLs, check names, and IDs are account data. Fixtures replace
  them with `<hostname>`, `<id>`, and synthetic names.
- The SDK sends `verified_ssl: true` on every create. A check that skips TLS
  verification on its target needs a design change and the owner's
  approval.

## Testing

Unit tests use `httptest` and an injected clock:

- A sanitized raw fixture and a decode test per operation, in
  `testdata/monitor/`.
- Pause and resume, each: already at target (no `PUT`); `ENABLED` or
  `DISABLED` to target with a confirming read; an unknown status (no
  `PUT`); a 404 on the first read; 401, 403, 409, and 429 on the `PUT`
  (one `PUT`, no confirm, error returned); a failed dial (one attempt); a
  502 and a dropped connection on the `PUT` followed by a read at target
  (`Changed: true`) and by reads never at target (`ErrStatusUnconfirmed`,
  four reads, waits of 1, 2, and 4 seconds); a cancelled context during
  the `PUT`. Every case asserts the exact number of `PUT` requests, and the
  fake server fails the test on a second one.
- Transport tests for `Once`: one attempt after 429, 502, a network error,
  and a failed dial; no resend after a 401 while the token is invalidated;
  `Retryable` values.
- Create: the request body with defaults and with every field set, the 201
  response, a response without an ID, and no retry after a 502. Delete:
  204, 404, and the ID check. Path ID rejection for `..`, `.`, `/`, and
  empty values on every operation.
- CLI golden tests for `json`, `table`, and `text`; `--yes` on delete;
  read-only refusal of all four writes with no request sent; and the two
  new error codes, including `StatusUnconfirmed` after a cancel.

Live tests follow [live data](../../instructions/live-data.md). Each write
run needs the owner's approval naming the account, region, and checks, and
touches only checks named `vngcloud-live-*`, before the free package ends
on 2026-10-26 or with a new package.

- `make live` gains `ListChecks` and, when a check exists, `GetCheck` on
  the first one, without pinning counts. From `v0.9.0` it adds
  `ListLocations`. The live CLI test adds `monitor list-checks`.
- `v0.8.0` live write test: the SDK cannot create a check yet, so the owner
  creates one `vngcloud-live-toggle` check in the console before the run and
  deletes it after. The test finds it by exact name, or skips. It records
  the start status, pauses twice (`Changed` true, then false), resumes
  twice, restores the start status in `t.Cleanup` with its own
  `context.WithTimeout(context.Background(), ...)`, and asserts it. It also
  logs, as a count only, how many confirm reads each toggle needed, which
  answers the read-lag question.
- `v0.9.0` live write test: it deletes leftover `vngcloud-live-` checks,
  skips when the list is at the quota the approval names, creates
  `vngcloud-live-<8 hex>` with one location and the longest test frequency
  against a URL the owner names in the approval, and runs the pause and
  resume steps above on it. If `CreateCheck` fails, it lists checks and
  deletes the one with its name. `t.Cleanup` deletes the check with its own
  context and asserts no `vngcloud-live-` check remains, logging only a
  count. The target URL never enters the repository.

## Releases

| Version | Content |
|-|-|
| `v0.8.0` | `monitor` package with `ListChecks`, `GetCheck`, `PauseCheck`, and `ResumeCheck`; the `Monitor` endpoint; `transport.Request.Once`; the four CLI commands |
| `v0.9.0` | `CreateCheck`, `DeleteCheck`, and `ListLocations`, with their CLI commands |

Neither release changes an existing method, field, or command. Pause and
resume ship first: aboutme runs them on every deploy but creates
its check once, in the console. The cost is that the `v0.8.0` live write
test needs a console-created check. Create first would avoid that but delay
aboutme's per-deploy need by one release.

A `Monitor` SDK wiki page covers per-account scope, `Changed`,
`ErrStatusUnconfirmed`, silent created checks, and the header warning.

Alarms, log projects, and notification channels follow after discovery, in
their own designs.

## Owner decisions

1. Approved: [ADR 0003](../adr/0003-toggle-writes.md). Toggles read first,
   send once with no retry at any layer, and confirm by reading.
2. Approved: pause and resume in `v0.8.0`, create in `v0.9.0`.
3. Approved: the `v0.8.0` live toggle test uses a `vngcloud-live-toggle`
   check the owner creates in the console and deletes after the run.
4. Open: `CreateCheck` has no quote, on the ground that checks use a prepaid
   quota.
5. Open: `CreateCheck` always sends `verified_ssl: true`, with no field to
   turn it off.
6. Open: `CreateCheck` ships without notifications, so a created check alerts
   nobody until channels are designed.
7. Open: check headers and bodies print unredacted in CLI output.
8. Open: `Locations`, `Headers`, `Query`, and `Assertions` go through
   `--cli-input-json`, with no new CLI flag types.
9. Approved: four confirm reads over about 7 seconds.

## Open questions

- Whether checks are per account, per region, or per project. The design
  assumes per account.
- Whether reads lag the toggle. The `v0.8.0` live test measures it; a lag
  longer than the confirm window changes step 7.
- The check ID format, the unknown-check error status, whether
  `GET /uptimes` pages, and the JSON types of `headers`, `query`, `body`,
  and `timeout`. The fixture capture answers these before code.
- The console defaults for `timeout`, `tests`, and `failed_locations`, and
  whether a create past the quota fails or bills.
- What happens to existing checks, and to pause and resume, when the free
  package ends on 2026-10-26.
