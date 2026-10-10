# Server Service Reads

Status: Accepted (2026-10-10) for snapshot backend and policy reads.
Snapshot history and scheduled operations stay draft; their discovery
remains open.

Add missing reads from the vServer dashboard. Snapshot policies are the first
release candidate because a populated console response is verified.
Snapshot history is a separate candidate pending its contract. Scheduled
server operations follow only after their API is verified.

The SDK already lists snapshots through `volume.ListSnapshots` and
`volume.ListAllSnapshots`. It does not list snapshot policies or snapshot
history. History routes return empty lists, so their row models remain
unverified. This draft does not authorize guessed endpoints or models.

## Snapshot policy reads

The HCM-03 console capture on 2026-10-10 returned a populated policy list with
HTTP 200 without service activation or purchase. The verified route is:

```text
GET /vserver/vbackup-gateway/v1/snapshot-policies
    ?backendId=<backend-id>&projectId=<project-id>&page=<page>&size=<size>
```

The origin is `https://hcm-3.console.greennode.ai`. A fresh browser context
with no cookies returned HTTP 200 using only the IAM bearer token. Backend
lookup also succeeds: `GET /vserver/vbackup-gateway/v1/backends` with query
`backend=HCM-03` returns backend IDs and names. A returned ID matches the
policy request's `backendId`.
Exact wire fields and their safe model are in
[snapshot policy API](server-services-api.md).

### SDK and CLI

Add `volume.ListSnapshotPolicies(ctx, *ListSnapshotPoliciesInput)` returning
`*ListSnapshotPoliciesOutput`, whose items are `[]SnapshotPolicy`. Use the
`volume` package because it already owns snapshot reads. The service's gateway
name does not require a new public package.

`ListSnapshotPoliciesInput` carries required `BackendID`, plus `Page` and
`Size`; project scope uses existing config. Reject nil input and invalid
backend IDs before network access. Defaults are page 1 and size 10, matching
the successful console request. These are SDK defaults, not a claim about
server defaults. Zero selects those defaults; negative page or size is
`ErrInvalidInput` before network access. Pagination makes one request per
call and exposes returned metadata through `core.PagedList`. Reads at pages
1 and 2 with size 1 returned distinct policies and consistent totals.

Add `volume.ListSnapshotBackends` with required `Name`, mapped to the
`backend` query. Its output has `Items []SnapshotBackend` with string `ID`
and `Name`. The verified backend name is `HCM-03`. This lookup is unpaged;
the captured `page` and `pageSize` fields are null, and totalItems equals the
returned count. Do not derive backend names or IDs from a region, zone, or
project, or choose the first match silently.

Policy models retain the verified hourly and daily settings. Config objects
and their retention/interval fields use pointers so absent settings do not
become zero. Empty configs remain empty objects. Weekly and monthly settings
stay omitted until populated evidence exists; the enabled flags remain
visible. The API companion defines the exact types and omissions.

Add `vngcloud volume list-snapshot-backends --name HCM-03` and
`vngcloud volume list-snapshot-policies --backend-id <id>` as `Read`
operations using only the public SDK. Both work in read-only profiles and use
normal output and query handling. Add no create, edit, attach, delete,
activation, or restore operation. These would change schedules or paid
resources.

Add `VServerBackup` to endpoint configuration and `ProductVServerBackup` to
internal routes. Resolve its default base from a fixed map containing only
`hcm-3`:

```text
https://hcm-3.console.greennode.ai/vserver/vbackup-gateway/
```

Both snapshot reads reject an unmapped region with `ErrInvalidConfig` before
authentication, project discovery, or network access. An endpoint override
changes the transport destination for a supported region only; it never
bypasses the supported-region check. Do not interpolate arbitrary region
names into hosts or replace gateway prefixes through string manipulation.
HAN-01 support requires cookie-free IAM proof and an explicit map entry in a
later release. Reuse the current session and transport with the verified IAM
bearer flow. Preserve TLS verification and same-host redirect restrictions.

Backup Center uses the separate names `BackupCenter` and
`ProductBackupCenter`. Backend IDs belong to their endpoint and product.
Never pass snapshot backend or resource IDs to Backup Center or infer that
IDs from the two products are interchangeable. This release adds only the
`VServerBackup` endpoint and `ProductVServerBackup` route product.

Retain the existing API error mapping. Do not reinterpret a denied request as
an empty policy list or as a requirement to buy or activate a service. Unknown
envelopes and malformed JSON fail without echoing the response body. No raw
metadata, credentials, or request headers enter resource models or logs.

### Checks and ownership

SDK ownership covers new `volume/snapshot_policies*.go`,
`volume/snapshot_backends*.go`, matching tests,
`testdata/volume/`, `examples/basic/volume.go`, the volume SDK wiki, and shared
endpoint/route files assigned in the manager's brief. CLI ownership covers
the volume operation table, CLI tests, and `docs/wiki/CLI-Volume.md`.

Use the verified fields and explicit omissions in the API companion. Before
release:

1. Decode both sanitized raw policy rows through the SDK. Cover the populated
   daily config in `DEFAULT`, populated hourly config in `ENHANCED`, and the
   empty config in each row. Test missing/null configs, absent members, and
   explicit zero without conflating them. Also cover every retained field,
   empty results, unknown policy types, and ignored secret fields. Raw
   captures remain ignored and account values are replaced.
2. Test exact route, query encoding, project scope, bearer authentication,
   paging, context cancellation, malformed responses, and status errors.
   Test the fixed `hcm-3` map and reject `han-1` and unknown regions before
   credential-provider calls or HTTP requests, both with and without an
   endpoint override. Confirm that `VServerBackup` overrides apply only to
   `ProductVServerBackup`; reads never reach Backup Center, activation, or
   mutation routes.
3. Test the CLI in read-only mode, flags, input JSON, output, and debug/error
   redaction. Update the SDK wiki and regenerate the CLI wiki.
4. Run the basic example against existing policies with a manager brief.
   Compare raw and SDK values; explicitly document excluded fields. Do not
   create a policy or snapshot to obtain a fixture.
5. Run `make check`, independent review, and green CI on the release commit.

The release gate includes the supported-region and product-isolation tests.
No HAN-01 mapping ships without separate cookie-free IAM evidence for both
backend lookup and policy listing in that region.

The first release may contain policy reads alone. Add history in its own
release once a populated raw response or official schema verifies its model.
An empty history list can verify a route and envelope but cannot establish
row fields. Snapshot writes and Backup Center remain outside this design.

## Scheduled operations: evidence and scope

The current HCM-03 sidebar capture exposes no Scheduled O&M or Operations
Manager link. This observation does not establish that the API is unavailable
or that activation is required. Scheduled-operation API discovery remains
open independently of the verified snapshot policy read.

GreenNode's [Scheduled O&M guide][scheduled] describes a beta service with
daily START, STOP, and REBOOT actions. A task targets virtual machines (VMs)
directly or through tags and belongs to one region, HCM-03 or HAN-01. An
execution records aggregate and per-VM results. The scheduler resolves tag
targets when the task runs.

The [July 2026 digest][digest], published on August 3, states that activation
creates a dedicated Service Account and that the default account quota is 20
tasks. It also describes task updates, duplication, disabling, deletion, and
manual execution. These are product capabilities, not verified API contracts.

Source review date: 2026-10-10. The official
[Go SDK source tree][official-sdk] has no paths named for scheduled operations
or maintenance. That filename check does not prove the absence of an API.

Existing immediate server operations remain under
[vServer paid writes](vserver-paid-writes.md). Resource tags use the existing
`tagging` package and [resource tag design](vserver-network-writes-2.md).
This design adds no second implementation of either surface.

Not included:

- Activation, Service Account creation, or credential retrieval.
- Task creation, update, duplication, enable, disable, delete, or manual run.
- Scheduling in the client, waiting for future runs, or retrying VM actions.
- Network interface or security group changes after server creation.
- Snapshot writes, Backup Center, restores, or paid resource provisioning.

## Scheduled operations: package and release boundary

Add scheduled-operation reads to `compute`. These operations inspect server
automation and fit the existing `compute.Client` and CLI group. Keep the SDK
standard-library only. Root `vngcloud` does not import `compute` or re-export
its types, as required by [SDK and CLI design](sdk-and-cli.md).

A separate `operations` package would isolate a future cross-product
scheduler, but no verified contract establishes that scope. A generic API
passthrough would expose unverified payloads and credentials. Neither is
needed for this release.

Ship one additive release for task inspection and execution history. Do not
ship methods that always return an unsupported error, empty placeholder
models, or an empty list standing in for unavailable access. Quota alone is
not the scheduled-operation release. If task detail or execution data cannot be
verified, finish contract discovery before implementing this feature.

## Contract discovery

Use official API documentation or a read-only console capture to fill this
table before code starts. Capture only an already activated account. Do not
activate the service to unblock a read.

| Contract | Current evidence | Required evidence |
|-|-|-|
| Task list | Product capability | Method, origin, route, scope, envelope |
| Task detail | Product capability | Lookup key, route, populated response |
| Execution list | Product capability | Task scope, paging, populated response |
| Execution detail | Per-VM results exist | Separate read, or nesting in list |
| Quota | Published default is 20 | Read route, scope, used/limit fields |
| Caller authentication | Unknown | Supported existing credential flow |
| Inactive account reads | Unknown | Safe behavior and response shape |
| Region selection | One region per task | Request field, path, or host mapping |
| Errors | Unknown | Status, envelope, stable service codes |

For each operation record the exact HTTP method, origin, path, query keys,
headers by name, success status, JSON envelope, nullability, and field types.
Record required project, account, and region inputs. Header values and live
identifiers stay out of the design.

Confirm whether list pagination uses a page number, offset, or cursor, its
first value, default size, maximum size, ordering, and completion condition.
Do not apply the compute server list's page size or envelope by analogy.
Verify any name, status, time-range, or action filter before exposing it.

Capture task targets, schedule, timezone, optional validity bounds, enabled
state, and execution results. Preserve the distinction between configured
targets and VMs that actually ran. Establish timestamp formats and units from
raw responses. Display dates without silently assuming local time or UTC.

Read access before activation is unverified. If the console offers only an
activation screen, stop discovery and report that barrier. If a verified
side-effect-free read returns an activation-required response, preserve that
failure. Neither outcome permits automatic activation. A GET request is not
assumed safe solely because it uses GET.

The scheduler's dedicated Service Account does not establish how callers
authenticate to task reads. Reuse the existing IAM User or static bearer flow
only after verifying it works for this API. A new credential flow requires a
separate design. Do not obtain the scheduler's Service Account credentials.

## Proposed SDK surface

All methods follow `Method(ctx, *Input) (*Output, error)`. The names below
reserve a coherent surface; wire fields and paging fields remain blocked on
the contract evidence above.

| Method | Required input | Output |
|-|-|-|
| `ListScheduledTasks` | None if scope comes from config | `Items []ScheduledTask` |
| `GetScheduledTask` | `ScheduledTaskID` | `ScheduledTask ScheduledTask` |
| `ListScheduledTaskExecutions` | `ScheduledTaskID` | `Items []ScheduledTaskExecution` |
| `GetScheduledTaskExecution` | `ScheduledTaskID`, `ExecutionID` | `Execution ScheduledTaskExecution` |
| `GetScheduledTaskQuota` | None if scope comes from config | `Quota ScheduledTaskQuota` |

The last two methods are conditional. Omit an execution-detail method when
the execution list already returns the complete result and no detail API
exists. Omit quota when no independent read is verified. Do not calculate
account usage from a regional list or return 20 as a live quota.

Before accepting this design, replace the conditional input choices with
verified scope and pagination fields. Use `core.PagedList` only if the API's
metadata has the same meaning. An unpaged result has only `Items`. Lists make
one request per call; do not hide traversal behind a single method.

Required ID fields carry `vngcloud:"required"` and pass `core.CheckPathID`
before authentication or network access. If API IDs need a broader alphabet,
document that verified alphabet first. A nil input is valid only when every
field is optional. Region and project use existing config only where the
verified API scope supports them.

### Models

Models expose typed, verified resource fields and keep the API's JSON tags.
Input and Output structs follow existing SDK naming without JSON tags.

- `ScheduledTask` represents identity, name, action, enabled state, region,
  configured VM or tag targets, schedule, and optional validity bounds.
- `ScheduledTaskExecution` represents an execution identity, trigger, status,
  start and end times, and per-VM results when present.
- `ScheduledTaskQuota` represents only quota values actually returned by the
  service, with their observed scope.

These are required concepts to inspect, not invented JSON property names.
Do not write struct definitions until raw examples establish exact fields,
scalar types, array shapes, and nullable values. Unknown enum strings survive
decoding. An absent timestamp, counter, or boolean must not become a claimed
zero value when absence changes its meaning.

Exclude tokens, Service Account credentials, cookies, certificates, request
headers, raw request/response bodies, and arbitrary metadata maps. Unknown
fields stay discarded. Keep execution failure status and verified safe error
codes; do not expose free-form upstream error text until its redaction has
been designed and tested. A returned failed VM result is execution data, not
an SDK request failure.

### Routing and errors

Reuse `core.Client` and the existing transport. Use the existing vServer
endpoint only if discovery verifies that origin and prefix. If a distinct
origin or gateway is required, add a separate named endpoint and route
product through the existing configuration mechanism. Do not derive an
Operations Manager URL from a product name or send credentials to fallback
origins. TLS verification and same-host redirects remain mandatory.

Confirmed reads use existing retry and context behavior. Mark a POST read
idempotent only when its verified behavior permits repetition. No request
may trigger an execution, change task state, or provision a principal.

Use `*vngcloud.APIError` and existing status-based sentinels for request
failures. Do not translate every 403 into activation-required, or every 404
into an empty list. Add no activation sentinel until a stable service code
distinguishes that state from access denial. Decode failures and unexpected
success shapes fail instead of returning a zero-valued resource. Never place
the raw body or secret-bearing service text in an error or debug output.

## CLI

Register verified methods as `Read` operations in the `compute` group:

```text
vngcloud compute list-scheduled-tasks
vngcloud compute get-scheduled-task --scheduled-task-id <id>
vngcloud compute list-scheduled-task-executions --scheduled-task-id <id>
```

When verified, add `get-scheduled-task-execution` with the two ID flags and
`get-scheduled-task-quota`. Pagination flags follow the accepted SDK inputs.
All commands work in read-only profiles and use the public SDK only. Normal
JSON, table, text, and `--query` handling applies. Add no activation prompt,
`--yes` flag, credential export flag, or automatic manual-run fallback.

Document the beta service and activation prerequisite if verified. A read
failure can explain that the account needs separate setup only when the API
establishes that cause. Generated help must not claim access works before
activation.

## Verification and ownership

SDK ownership covers `compute/scheduled_tasks*.go`,
`compute/scheduled_task_executions*.go`, matching tests,
`testdata/compute/`, `examples/basic/`, `live_test.go`, and the compute SDK
wiki pages. The SDK worker owns any necessary shared route or endpoint
changes, including their tests, through an explicit manager brief.

CLI ownership covers the compute operation table, CLI tests, generated
`docs/wiki/CLI-Compute.md`, and compute-specific help notes. CLI work starts
after the manager accepts the SDK surface. The manager owns integration,
documentation indexes, checks, Git, and releases. The architect does not
implement this design; an independent reviewer reviews the release.

Required checks:

1. Sanitized raw fixtures for every shipped read, including populated task
   and execution responses. Decode through the SDK with `httptest`; fixture
   shapes must come from official examples or captured raw bodies, not SDK
   output. Label synthetic edge cases separately.
2. Exact request method, path, query, scope, and authentication tests. Cover
   ID validation before network access, empty lists, nullable fields, unknown
   enum values, malformed JSON, and unexpected success shapes.
3. Pagination tests using verified metadata. Cover a second page and the
   completion condition; never infer completion from the published quota.
4. Tests for captured/documented errors, plus shared handling of 401, 403,
   404, 429, 5xx, and context cancellation. Synthetic status tests establish
   SDK behavior, not a claim that the service returns every tested status.
5. Tests proving reads cannot activate or trigger tasks. Secret-bearing
   fixture fields and injected error text must not reach SDK formatting,
   CLI output, or debug logs.
6. CLI tests for read-only mode, required flags, input JSON, output, and
   errors. Regenerate the CLI wiki and update the SDK wiki in the same change.
7. `make check` before each commit. Green CI on the exact release commit.

The basic example gains an explicit scheduled-operations read opt-in. A live
read run requires a manager brief and an already activated account with
existing tasks and execution history. Reuse returned IDs only for reads.
Do not create a task or run an action to populate fixtures. Save raw and SDK
output under ignored example output paths and compare them before producing
sanitized fixtures, following [live data](../../instructions/live-data.md).
An empty account does not validate detail or execution models.

## Later writes

Activation is a separate IAM-affecting operation and needs its own contract,
explicit live approval, and adversarial review. Task mutations need a later
design covering schedule timezone, validity, tag selection, conflicts,
idempotency, and cleanup. Manual execution must define confirmation for
STOP/REBOOT and behavior after an uncertain response. Server actions run in
the provider; the SDK must not emulate them through immediate server calls.

Writes follow [ADR 0002](../adr/0002-write-api-conventions.md). Toggle APIs,
if discovered, also follow [ADR 0003](../adr/0003-toggle-writes.md). Paid
resources or uncleanable writes require separate owner approval. Snapshot
and Backup Center work also needs separate billing and restore designs.

[scheduled]: https://docs.greennode.ai/vserver/compute-hcm03-1a/operation-and-maintenance
[digest]: https://greennode.ai/blog/digest-july-2026
[official-sdk]: https://github.com/vngcloud/vngcloud-go-sdk
