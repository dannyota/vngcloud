# Server Service Read APIs

Status: Accepted (2026-10-10) for snapshot policy reads and (2026-10-11) for
console log reads. Policy reads, page traversal, and cookie-free IAM bearer
authentication are verified.

This companion records the wire evidence for
[server service reads](server-services.md). It contains no live account
values. Snapshot evidence comes from a read-only HCM-03 console capture on
2026-10-10. Console log sources are named in their section.

## Request and envelope

The observed method is `GET`, with HTTP 200 success:

```text
https://hcm-3.console.greennode.ai/
  vserver/vbackup-gateway/v1/snapshot-policies
  ?backendId=<backend-id>&projectId=<project-id>&page=<page>&size=<size>
```

The line breaks are for display. The response is an object with these fields:

| JSON field | Observed JSON type | SDK output field |
|-|-|-|
| `items` | Array of policy objects | `Items []SnapshotPolicy` |
| `page` | Number | `Page int` |
| `pageSize` | Number | `PageSize int` |
| `totalPages` | Number | `TotalPage int` |
| `totalItems` | Number | `TotalItem int` |

The successful request used page 1 and size 10. Use those as SDK defaults;
server defaults and size limits remain unknown. Cookie-free reads at page 1
and page 2 with size 1 returned distinct policies. Each returned pageSize 1,
totalPages 2, and totalItems 2, with the requested page number. Counters are
integral. Do not assume a maximum or clamp to an invented cap.

The same policy request succeeded in a fresh isolated context with no cookies
and only the IAM bearer token. Reuse the existing shared SDK transport and
credential flow. No activation or purchase occurred. The minimum IAM policy
needed for this read is not established by a successful authorized request.

### Endpoint and region scope

Endpoint configuration uses `VServerBackup`; the route product is
`ProductVServerBackup`. The default endpoint map initially has exactly one
entry:

| Config region | Base URL |
|-|-|
| `hcm-3` | `https://hcm-3.console.greennode.ai/vserver/vbackup-gateway/` |

Both backend lookup and policy listing reject any unmapped region with
`ErrInvalidConfig` before authentication, project discovery, or HTTP access.
A `VServerBackup` override changes the destination only after this semantic
region check succeeds. It cannot enable `han-1` or an unknown region. No
host is derived from an arbitrary region string.

The release must test supported and unsupported regions with and without an
override, counting both credential-provider calls and HTTP requests. Test
that only `ProductVServerBackup` consumes the `VServerBackup` override.
HAN-01 stays unsupported until cookie-free IAM requests verify backend lookup
and policy listing there, followed by an explicit map change and review.

Backup Center has a separate `BackupCenter` endpoint and `ProductBackupCenter`
route product. This release does not add either. Snapshot reads never use a
Backup Center endpoint as a fallback.

## Backend lookup

`GET /vserver/vbackup-gateway/v1/backends?backend=HCM-03` succeeds with the
same cookie-free authentication. Its response has `items`, whose rows contain
string `id` and `name`; `page` and `pageSize` are null. The envelope also
contains `totalPages` and `totalItems`. The policy query's `backendId` matches
an ID returned by this lookup.

Expose `ListSnapshotBackendsInput{Name string}` with `Name` required. Build
the query using `url.Values`; no string interpolation into a URL. Return
`Items []SnapshotBackend`, whose fields are `ID` and `Name` with the matching
JSON tags. The captured totalItems equals the returned count, confirming a
complete unpaged list. Do not invent pagination inputs for it.

Require an explicit backend ID on policy reads. Selecting the first returned
backend or deriving a name from config could select the wrong scope. The
verified name `HCM-03` does not establish the name or endpoint in HAN-01.

Backend IDs are scoped to the endpoint and product that returned them. Pass
snapshot backend IDs only to the vServer snapshot gateway. Never pass those
IDs or snapshot resource IDs to Backup Center or assume that product IDs are
interchangeable. Product-isolation tests must ensure snapshot operations
cannot route their IDs through `ProductBackupCenter`.

## Policy model

Retain these fields in `SnapshotPolicy`:

| JSON field | Go field | Go type |
|-|-|-|
| `id` | `ID` | `string` |
| `name` | `Name` | `string` |
| `policyType` | `PolicyType` | `string` |
| `config` | `Config` | `SnapshotPolicyConfig` |
| `createdAt` | `CreatedAt` | `string` |
| `updatedAt` | `UpdatedAt` | `string` |
| `snapshotServerCount` | `SnapshotServerCount` | `int` |
| `snapshotVolumeCount` | `SnapshotVolumeCount` | `int` |

Keep timestamps as received; their timezone and grammar are not established
by a JSON string type. Observed policy types are `DEFAULT` and `ENHANCED`;
keep the field as a string so new values survive decoding. Snapshot counts
are verified integral. Do not infer active state from a type or count.

`SnapshotPolicyConfig` retains the fields whose shapes were populated:

| JSON field | Go field | Go type |
|-|-|-|
| `hour` | `Hour` | `int` |
| `minute` | `Minute` | `int` |
| `timeZone` | `TimeZone` | `string` |
| `hourlyEnabled` | `HourlyEnabled` | `bool` |
| `hourlyConfig` | `HourlyConfig` | `*SnapshotPolicyHourlyConfig` |
| `dailyEnabled` | `DailyEnabled` | `bool` |
| `dailyConfig` | `DailyConfig` | `*SnapshotPolicyDailyConfig` |
| `weeklyEnabled` | `WeeklyEnabled` | `bool` |
| `monthlyEnabled` | `MonthlyEnabled` | `bool` |
| `isProtectedServer` | `IsProtectedServer` | `bool` |
| `statusSendEmail` | `StatusSendEmail` | `[]string` |

Both policy rows establish different optional schedule shapes:

| Policy type | `hourlyConfig` | `dailyConfig` |
|-|-|-|
| `DEFAULT` | Empty object | Integral `retention` |
| `ENHANCED` | Integral `interval` and `retention` | Empty object |

`SnapshotPolicyHourlyConfig` contains `Interval *int` and `Retention *int`
with JSON tags `interval,omitempty` and `retention,omitempty`.
`SnapshotPolicyDailyConfig` contains `Retention *int` with JSON tag
`retention,omitempty`. The raw values for interval and both retention fields
are integral. Their units remain undocumented; do not infer units from the
field names or sample values.

The `HourlyConfig` and `DailyConfig` pointers use `omitempty`. A missing or
null config decodes to nil. An empty object decodes to a non-nil config whose
members are nil, preserving `{}` in output. An absent member stays omitted;
an explicit zero remains a non-nil pointer to zero. Do not synthesize disabled
schedules or infer member values from the enabled flags.

The hour and minute values are verified integral. Preserve timezone and
status strings; do not convert them into undocumented enums or calculate
execution times.

The observed `statusSendEmail` value is the status string `ERROR`. Expose
`[]string` so future statuses survive decoding. The field names do not
establish delivery guarantees or email recipient configuration.

### Deliberate omissions

- `userId` is a number. Omit this account identifier.
- `backendId` and `projectId` are strings. Omit duplicated scope metadata from
  resource output; request scope remains explicit in config/input.
- `isDefault` and `deletedAt` were null. Null alone establishes no underlying
  Go type. Omit both until a non-null response or official schema settles it.
- `weeklyConfig` and `monthlyConfig` were empty objects in both policy rows.
  Do not invent their fields, flatten them into hourly or daily retention, or
  expose them as `any`, `map[string]any`, or `json.RawMessage`.

The first read is a policy summary with verified hourly and daily fields.
It is not a complete configuration export for weekly or monthly schedules.
The wiki must state that limit. Enabled flags remain visible even when a
schedule's settings have no verified model.

Omitting fields is deliberate and must be checked during raw-to-SDK
comparison. Never use this summary as a write body or as input to a policy
replacement operation. No tokens, Service Account secrets, certificates, or
arbitrary upstream fields enter the model.

## History and errors

The following GET routes under the same gateway return HTTP 200 using
cookie-free IAM bearer authentication:

```text
/snapshot-histories/rollback
/snapshot-histories/restore
/snapshot-histories/snapshot
```

Each query carries `backendId`, `projectId`, `page=1`, and `size=10`. Each
response has empty `items` and the ordinary page envelope. These observations
establish read routes, authentication, and an empty-list response only. No
history row schema is verified. Do not expose history as an untyped map or
guess a model from policy rows. Populated evidence is required before adding
history methods, and must not be obtained by creating or restoring resources.

Only successful policy responses are verified. Preserve the shared
`*vngcloud.APIError` mapping for HTTP failures. Synthetic tests for 401, 403,
404, 429, and 5xx verify SDK behavior and must not be labeled observed service
errors. Missing or mistyped envelope fields fail as response-shape errors
without embedding response text.

The inspected official SDK [snapshot service][snapshot-source] lists, creates,
and deletes snapshots by volume. It supplies no policy or history contract
for this design. Public searches found no GreenNode policy schema that fills
the gaps above; similar APIs from other vendors are not evidence.

[snapshot-source]: https://github.com/vngcloud/vngcloud-go-sdk/blob/main/vngcloud/services/volume/v2/snapshot.go

## Server console log

Read boot and serial output when SSH is unavailable. The route is:

```text
GET <configured vServer gateway>/v2/{projectId}/servers/{serverId}/console-log
```

HTTP 200 carries an object with `data` as one string. The supplied read-only
evidence from 2026-10-10 reports systemd and cloud-init output.
No account values or log content are retained here. The official Terraform
provider confirms the route and string envelope. Its generated method takes
only project and server IDs and sends an empty query. No length or line-count
parameter is confirmed; expose neither and send no query parameters. This
does not claim that the service has no undocumented parameters.

First-party source checked on 2026-10-11: [Terraform provider][console-source]
at commit `230bd7d346853b2aba33a9ec06f8c544b06fa8a2`, under `client/vserver/`:

- `api_server_rest_controller_v2.go:847`: `GetConsoleLogUsingGET`; path at
  line 857, empty query at 862, passed unchanged at 882.
- `model_data_responsestring.go:12`: `Data string` with JSON key `data`.
- `docs/ServerRestControllerV2Api.md:255`: generated method documentation.

These sources establish no stopped-server or unknown-ID behavior. Do not
infer anonymous access from the generated documentation's auth annotation;
use the existing compute bearer authentication and configured region/project.
Never call `console-url`, including as a fallback or redirect target. Its
credential-bearing noVNC URL is outside this operation.

### SDK and transport

The public method is `(*compute.Client).GetServerConsoleLog(ctx, in)`, with
the usual `context.Context`, pointer input, pointer output, and error:

```go
type GetServerConsoleLogInput struct {
    ServerID string `vngcloud:"required"`
}

type GetServerConsoleLogOutput struct {
    Log vngcloud.Secret
}
```

Input and Output have no JSON tags. Reject nil input, empty ServerID, and
invalid path IDs with `ErrInvalidInput` before authentication or project
discovery, as `compute.GetServer` does. Use its gateway routing and project
resolution. Root `vngcloud` gains no service types or methods. No server-state
pre-read, polling, boot action, or streaming/follow mode is needed.

Console output can contain passwords and keys. `Log` uses the existing
`vngcloud.Secret` to suppress ordinary formatting, JSON encoding, and slog
output; SDK callers obtain the text explicitly with `Log.Reveal()`. This is
not content redaction or protection against reflection and explicit casts.

Set `Sensitive: true`, `NoRedirect: true`, `OK: []int{200}`, and
`MaxBody: 8 << 20` on every attempt. The 8 MiB cap covers the entire JSON body
read by the transport, including JSON escaping and HTTP decompression. It
bounds allocations while allowing a substantial boot log; it is a client
resource limit, not an observed service maximum. Read at most cap plus one
byte, fail on overflow, and never return a truncated log. No cap override.
Normal compute GET retries and cancellation apply; body overflow is not
retried. Refusing all redirects prevents a redirect to `console-url`.

`Sensitive` suppresses captures and decode details, but not HTTP error text.
Also set `WithholdMessage` to `console log request failed; body withheld`.
Use `ClassifyError` to replace every upstream code with
`core.ResolvedCode(status, "")`, or `RequestFailed` when that is empty. Never
classify by upstream text. Preserve status, retryability, and shared status
sentinels. No response body, message, code, or nested decode error is copied
into errors. `--debug` shows the usual method, URL path without query, status
when available, and duration per attempt, plus existing IAM login events.
The path includes resource IDs. Debug shows no bodies, headers, or credentials.

Disable routine example/live-suite calls and all raw or decoded captures for
this operation. Fixtures are synthetic only, including secret-like markers;
never sanitize a real console log into a fixture. This is a scoped exception
to the capture/fixture workflow in [live data][console-live-data]. An explicit
live check may assert status and shape in memory, but cannot print or persist
the log. Routine diagnostics must not call `Reveal()`.

### Results and errors

Only an object containing string `data` is success. Decode with presence and
type checks; ignore extra fields. Return nil output on every failure.

| Response or condition | Result |
|-|-|
| `200`, `data: ""` | Success with an empty Log |
| Missing/null/non-string `data`, malformed JSON, empty body | Decode failure |
| Non-object envelope, including JSON null | Decode failure |
| Stopped server | Send the same GET; return its string or mapped error |
| Unknown ID returning 404 | `APIError`, `NotFound`, `ErrNotFound` |
| Unknown ID returning another response | Apply the same shape/status rules |
| Body over 8 MiB, on any status | `APIError`, `ResponseTooLarge` |

Decode failures use `*vngcloud.APIError` with operation
`compute.GetServerConsoleLog`, code `RequestFailed`, message
`console log response invalid; body withheld`, status 200, and Retryable false.
Do not retain the underlying decode error. Wrap the internal
`transport.ErrBodyTooLarge` as a public `APIError` with code
`ResponseTooLarge`, message `console log response exceeds 8 MiB`, status 0,
and Retryable false; `DoJSONStatus` does not preserve status on overflow.
Add no public sentinel. Oversize errors take precedence over status mapping.

Other failures retain compute read semantics: 401 maps to `ErrAuth`, 403 to
`ErrPermission`, 404 to `ErrNotFound`, and 429 to `ErrRateLimited`. Use the
shared status-derived codes, including `Conflict` for 409 and `ServerError`
for 5xx. Stopped and unknown-ID service responses remain unverified; do not
invent `ErrUnexpectedStatus` or treat an empty string as not found.

### CLI and approval

Register `vngcloud compute get-server-console-log --server-id <id>` as a
`Read` using only the public SDK. It works in read-only profiles, accepts
`--cli-input-json`, and needs no `--yes`. Keep the usual output default
(profile setting, otherwise JSON). The operation explicitly reveals Log only
for requested stdout output; help and wiki text must say logs can hold secrets.

- `--output text` without a query writes the decoded Log with no label,
  quotes, or added newline. When stdout is not a terminal (a pipe or file),
  the bytes are verbatim, including control characters, so the log pipes
  unchanged. When stdout is a terminal, control characters other than
  newline and tab are escaped as the existing text renderer escapes them, so
  a log cannot drive the terminal. An empty log writes zero bytes.
- JSON uses the object `{"Log":"..."}` with normal JSON escaping; an empty
  log is `{"Log":""}`. Table output has one Log column and one row, with
  controls escaped by the existing renderer; an empty log is an empty cell.
- `--query` operates on that revealed object. A text query returning a string
  writes that string verbatim; other results use normal rendering. Keep query
  syntax checks before the call. The terminal rule above applies to a
  string written this way. Runtime query failures use `QueryFailed`
  with fixed text `console log query failed; result withheld`, without the
  underlying query error, which may quote the log. No log goes to stderr.
- Keep existing exits: success 0; usage/config 2; credentials/login/401 3;
  not found 4; permission, throttle, decode, size, query-runtime, network,
  cancellation, and other request failures 1. Errors remain JSON on stderr.

The owner approved on 2026-10-11 Secret-typed SDK output, the 8 MiB cap,
synthetic-only fixtures, and explicit secret-bearing stdout, verbatim only
when stdout is not a terminal. The stdout and raw-control rules are scoped
exceptions to [CLI reads][console-reads] "No command prints a credential"
and [CLI][console-cli] secret-file and text escaping rules. No other command
changes. Ship SDK and CLI together in one additive release, with no existing
API break.

Required checks use synthetic responses: exact GET/path/no query, validation
before auth, empty and invalid shapes, stopped/unknown-ID status handling,
cap and cap-plus-one bodies (also errors and decompressed bodies), no partial
output, status sentinels/exits, retries/cancellation, and refused redirects.
Assert no captures or secret markers in errors, debug, query failures, or
ordinary SDK formatting. Check raw text bytes with and without a final
newline, terminal escaping, JSON/table escaping, queries, input JSON, and
read-only mode. Update SDK and CLI wiki pages and run `make check` before
release.

[console-source]:
  https://github.com/vngcloud/terraform-provider-vngcloud/tree/230bd7d
[console-live-data]: ../../instructions/live-data.md
[console-reads]: cli-reads.md
[console-cli]: cli.md
