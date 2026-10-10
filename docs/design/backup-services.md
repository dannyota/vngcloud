# Backup Center reads

Status: Accepted (2026-10-10) for the first release: backend and policy
lists in `hcm-3`. Later releases stay draft until their raw shape checks
pass.

Add a `backup` SDK package and CLI group. The first release lists backup
backends and policies in `hcm-3`. Later small releases add server and
destination inventory, policy details, restore points, and run history.
Each release needs the confirmed IAM contract and raw
fixtures described in [API evidence](backup-services-api.md).

This design follows [SDK and CLI](sdk-and-cli.md). It does not change the
paid snapshot, backup, or restore exclusions in
[vServer paid writes](vserver-paid-writes.md#non-goals).

## Product boundary

Backup Center is a separate service from vServer volume snapshots. Its
backup server represents a protection configuration for a source server.
A backup destination is the console's Backup Location. A backup policy
sets the schedule; a backup server point holds a restore point. History
records backup and restore runs.

Keep `volume.ListSnapshots`, `volume.ListAllSnapshots`, their types, and
their CLI commands unchanged. Do not alias backup server points to
`volume.Snapshot` or route snapshots through the new package. Destination
quota and storage usage belong to destination reads. The inspected vendor
source establishes no separate jobs or account quota endpoint.

Three approaches were considered:

- Extend `volume`: fewer packages, but mixes two distinct products and
  cannot describe destinations or server protection clearly.
- Add every vendor backup read: covers databases, metrics, and migration
  history, but introduces several gateways and unrelated scope at once.
- Add a `backup` package in small read slices: keeps the product boundary
  clear and lets each response shape be checked before release. Selected.

## Scope

The first release contains only `ListBackends` and `ListPolicies`.
`ListPolicies` accepts only `Page` and `Size`. No resource, name, backend,
or project filters are exposed. Policy detail and every other operation
below remain held for their own evidence and release.

The proposed operations appear in vendor source. Cookie-free reads confirm
HCM IAM bearer access, populated backend and policy shapes, and empty
server, destination, and history envelopes. Other wire shapes still need
the evidence checks in the API companion before code starts. An operation
blocked by missing evidence stays out of the release; do not ship an empty
stub or guess its response shape.

No release in this design adds activation, billing orders, backup creation,
schedule changes, restore, deletion, replication, failover, downloads, or
signed download URLs. Database backups, vServer projection reads, metrics,
destination audit history, and configuration catalogs are separate work.
The SDK never activates a product to make a read succeed.

## SDK and CLI surface

Use `backup.New(cfg vngcloud.Config) *Client` and the existing
`Method(ctx, *Input) (*Output, error)` convention. `backup` depends on the
standard library, the root package, and shared internal helpers. The root
package re-exports no backup types. Every operation name is
`backup.<Method>`.

The CLI group is `backup`, described as "Backup backends and policies".
Each SDK method maps to its kebab-case CLI name.
The CLI uses public SDK methods and registers every operation with `Read`.
There are no mutation flags, background polling, or interactive prompts.

### First release

- `ListBackends`: empty `ListBackendsInput`; nil input is accepted. Sends
  `GET /v1/backends` without query parameters.
- `ListPolicies`: `ListPoliciesInput` has `Page int` and `Size int` only;
  nil input selects both defaults. Sends `GET /v1/backup-policies` with
  only `page` and `size`.

`ListBackendsOutput` has exactly `Items []Backend`, `Page *int`,
`PageSize *int`, `TotalPage int`, and `TotalItem int`. Map the wire keys
`items`, `page`, `pageSize`, `totalPages`, and `totalItems` in that order.
The two pointers preserve the observed nulls. Do not use
`core.PagedList[Backend]`, whose integer page fields would erase null.

`ListPoliciesOutput` is `core.PagedList[Policy]`. The same wire keys map
to `Items`, `Page`, `PageSize`, `TotalPage`, and `TotalItem`; all policy
page metadata is numeric in the confirmed paged responses.

Return stored cadence enable flags and the checked daily settings. Help
states that hourly, weekly, and monthly configuration details are not
included. An enable flag does not imply the schedule details are known.
Do not compute next-run times or change timezones.

### Held inventory

- `ListServers`: optional `ServerID`, `Name`, `BackendID`; paged
  `Items []Server`.
- `GetServer`: required `BackupServerID`; output field `Server`.
- `ListDestinations`: optional `Name`, `Type`, `BackendID`; paged
  `Items []Destination`.
- `GetDestination`: required `DestinationID`; output field `Destination`.

The word `Server` inside `backup` means a backup configuration. Its
`ServerID` identifies the source compute instance. CLI help spells out that
distinction for both flags. Do not follow IDs into compute or storage
automatically, and do not cache list or detail results.

### Held policy detail and filters

- `GetPolicy`: required `PolicyID`; output field `Policy`.
- `ListPolicies` filters such as `Name` and `BackendID` require separate
  checks before they can be added.

Return the stored schedule, including each enable flag. An empty cadence
configuration does not imply an enabled schedule. Return timezone and
retention values without computing next-run times or changing timezones.

### Held restore points

- `ListServerVolumes`: required `BackupServerID`;
  `Items []ServerVolume`, without page flags for the bare-array route.
- `ListServerPoints`: required `BackupServerID`; paged
  `Items []ServerPoint`.

Return nested disk metadata present in the point response. Do not fetch
disk images, download URLs, or other endpoints to enrich the result.
An empty point list is not proof that the server has never been backed up;
retention or deletion can remove points.

### Held history

- `ListBackupHistory`: optional `BackupServerID`, `ServerID`, `FromDate`;
  paged `Items []BackupRun`.
- `ListRestoreHistory`: optional `BackupServerID`, `ServerID`; paged
  `Items []RestoreRun`.

`FromDate string` accepts `YYYY-MM-DD` or RFC 3339 with an explicit
timezone. A date means midnight UTC. Encode the result as Unix
milliseconds in `from_date`. Reject invalid dates before authentication.
Do not add an unsupported end-date filter.

When `FromDate` is absent, document the vendor's 180-day backup history
window. Restore history has no date filter in the inspected source. Keep
server order and return page metadata. Do not silently sort, truncate, or
pretend these routes return current job state.

## Scope and routing

Use the existing IAM user or static bearer provider. The console sends
IAM authorization to `https://hcm-3.api.vngcloud.vn/vbackup-gateway/`.
The public API hostname is intentional; do not invent a console gateway.
Fresh isolated requests without cookies confirm IAM bearer access. No
additional auth, project, or region header appeared in those requests.
Service-account authentication is outside this design.

Add `EndpointOverrides.BackupCenter`, the matching internal endpoint
field, and `routes.ProductBackupCenter`. Resolve the default from a fixed
map containing only `hcm-3` and the URL above. Do not interpolate a region
into a hostname. Every backup operation rejects any other resolved region
with `ErrInvalidConfig` before authentication or network access, even when
`BackupCenter` is overridden. An override replaces only the verified
region's endpoint; it does not enable a region. Cross-host redirects remain
forbidden. The source's Hanoi URL is held until IAM verification.

The separate vServer snapshot proxy uses `VServerBackup` and
`routes.ProductVServerBackup`; these are not aliases for Backup Center.
Backend IDs belong to their product and endpoint. Do not infer that an ID
from one gateway is valid at the other, or infer a backend from a region
name. This package uses only its own backend results.

The first two reads use token scope. Neither sends a project ID. The
policy list succeeds with only page and size in isolated IAM requests.
Do not call `RequireProjectID` or attach project or tenant headers.
Configuration `ProjectID` and CLI `--project-id` do not scope these reads;
SDK and CLI docs state that fact. Returned project IDs identify resources,
but do not prove filtering or cross-account isolation.

The console sends `projectId` and `backendId` on several list reads.
Observed successful requests do not prove that these parameters filter
results. In particular, vendor source reports that backup history ignores
both. Neither history filter is exposed. IAM permissions remain the
authority for which resources a caller may read.

## Pagination and validation

`ListPolicies` has `Page int` and `Size int`. Zero selects page 1 and size
200, both confirmed by an isolated IAM read. Negative values fail
locally. The request keys are `page` and `size`, never `pageSize`. A size
of 200 is a client default, not a claimed server limit.

Each policy call reads one page. Preserve the metadata types defined in
[First release](#first-release). Do not infer totals from item counts or
claim complete results when more pages exist. `ListBackends` sends no
paging query. Held list operations need their own paging contract before
implementation.

Held required inputs use `vngcloud:"required"`. Validate every supplied path
ID with `core.CheckPathID` before authentication or requests. Validate
ID-valued query filters by the same rule; encode names with `url.Values`.
Do not enforce status, product, or destination type allowlists from the
few enum values seen in fixtures.

## Models and safe output

### First-release allowlist

`Backend` contains only `ID string` and `Name string`, tagged `id` and
`name`.

`Policy` contains string fields `ID`, `BackendID`, `ProjectID`, `Product`,
`Name`, `CreatedAt`, and `UpdatedAt`; `IsDefault bool`;
`BackupInstanceCount int`; and `Config PolicyConfig`. Tags map to the
confirmed camel-case wire names in the API companion.

`PolicyConfig` contains `Hour int`, `Minute int`, `TimeZone string`,
`HourlyEnabled bool`, `DailyEnabled bool`, `WeeklyEnabled bool`,
`MonthlyEnabled bool`, `IsProtectedServer bool`, and
`DailyConfig *DailyConfig`. `DailyConfig` contains only `Retention *int`,
`BackupType *string`, and `IncrementalQuantity *int`, with their matching
camel-case tags. Pointers keep absent daily values distinct from zero;
absent or null `dailyConfig` stays nil. An empty object leaves the nested
pointers nil. No inferred defaults are added.

The empty `hourlyConfig`, `weeklyConfig`, and `monthlyConfig` objects
establish no member fields. Omit these objects from the first public model;
do not invent structs from source-only fields or return misleading zero
settings. Their enable flags remain visible. Omit `statusSendEmail` and
account `userId`. Neither arbitrary configuration nor unknown fields get a
raw-data escape hatch.

### Later models

The API companion records candidate field names and units. Resource models
use API JSON tags. Input and output wrappers use Go field names. Use
typed nested structs for policy schedules, destination storage summaries,
quota, soft delete, locks, points, and volumes. Keep states as strings.
Keep timestamp strings exactly as received. Byte counts use `int64`;
quota limits preserve their separate documented GB unit.

Use pointers when null differs from zero or false, especially quota and
retention settings. Unknown numeric usage is not zero usage. A missing
quota setting is not proof of unlimited capacity. Do not copy the
vendor's lossy defaulting and coercion helpers into the Go SDK.

Destination `config` and historical snapshots may contain either objects
or JSON-encoded strings. Decode those forms privately, then expose only
the typed fields selected in the API companion. Reject malformed nested
JSON with fixed text. Never expose `any`, arbitrary maps, raw JSON, or the
original nested string as an escape hatch.

Backup responses can contain storage credentials or execution details.
Omit signed URLs, credentials, account `userId`, restore `config`, execution
`errorMessage`, and arbitrary descriptions from every public model. The
same exclusion applies to nested snapshots. Keep names, IDs, schedules,
statuses, sizes, and times needed for inventory. Document the omission of
execution details
in history help so a failed status does not imply full diagnostics.

Every backup request sets `Sensitive: true` and a fixed
`WithholdMessage`. Raw response bodies must not reach response-capture
hooks, decode errors, logs, or CLI errors. The package also replaces
server-provided error codes with `core.ResolvedCode(status, "")`; a code
field can contain secret text too. Preserve status, operation,
retryability, and standard error sentinels. `Sensitive` alone does not
withhold error codes or messages.

Use existing JSON, table, text, and JMESPath output. The first release shows
backend IDs and names, policy IDs and names, default state, product, and
cadence enable flags. Later inventory tables show status, backup-enabled
state, source server ID, destination ID, and policy ID where present.
Destination output keeps product, quota, usage, soft
delete, and lock fields separate. It does not estimate cost or label a
destination full from values with different units.

## Errors and compatibility

Keep existing status mapping and GET retries, including token refresh.
An HTTP 401, 403, or 404 is an error, never an empty inventory or an
activation instruction. An unrecognized body, HTML login page, missing
list envelope, or empty detail body fails with fixed text. A confirmed
empty list succeeds. Both first-release routes accept the confirmed HTTP
200 and collection envelope. Held operations need their own success check.

Product-disabled and permission-denied responses need separate fixtures
if the console exposes both. Do not invent a service-disabled sentinel
from a generic 403. If a 2xx body contains an application error, record its
contract before implementing a mapping.

The package and CLI group are additive. Existing volume snapshots,
configuration precedence, read-only profiles, and error exits keep their
current behavior.

## Verification and ownership

Before implementing a release, confirm its IAM host, region selection,
authorization mode, paths, success envelope, errors, and raw field types.
One region's evidence does not establish the other region's host. A
populated response is needed to verify a resource model; an empty response
only verifies the route and envelope. Missing resources do not authorize
paid activation or backup creation.

The first release still needs a read-only live check through the SDK's
built-in IAM provider, using the fixed HCM endpoint and both operations.
Browser bearer replay proves route access, not the SDK login and transport
integration. Compare SDK output with sanitized raw evidence before release.

Obtain raw evidence through the authorized console workflow and store it
only under ignored `examples/basic/output/raw/backup/`. Production SDK
capture stays suppressed. Compare that evidence with decoded example
output under `examples/basic/output/sdk/backup/`. Sanitize fixtures before
adding them to `testdata/backup/`; never construct a raw fixture from SDK
output. Keep the field names and shapes of omitted secret fields in
fixtures with synthetic values so tests prove those fields cannot escape.

First-release deterministic tests cover exact routes and queries, nil
inputs, page defaults and negative values, unsupported-region rejection
before auth or network even with an override, every allowlisted fixture
field, nullable backend metadata, numeric policy metadata, page 2, empty
results, optional daily values, omitted unverified cadence details,
unrecognized bodies, 401/403/404/429, retry behavior, and cancellation.
Secret marker tests cover normal formatting, JSON, table, text, debug
output, capture hooks, malformed bodies, and server errors including
`code`. No test sends a write. Later releases add ID, date, and nested-JSON
tests when those inputs and decoders enter their scope.

SDK ownership: `backup/`, backup fixtures, endpoint and route plumbing,
`examples/basic/`, read-only live checks, and SDK wiki pages. CLI ownership:
`internal/cli/svc_backup*.go`, CLI registration, generated help notes,
and CLI wiki pages. The manager assigns shared integration paths before
parallel work and owns the design index, README, plan, Git, and release.

Run `make check` for implementation. Run live reads only under an explicit
brief. Review independently before release, including adversarial checks
for credentials, execution text, and token routing. A tag requires green
CI on its exact commit.

## Release order

1. HCM backend and policy lists only. This establishes the client and the
   confirmed typed models with no speculative filters.
2. Policy detail and additional cadence settings, after their raw checks.
3. Server and destination inventory and details, including quota and usage,
   after populated models and filter behavior are verified.
4. Server volume and restore point reads after their own evidence.
5. Backup and restore history reads with execution text withheld.

Each is one feature release after its evidence and checks pass. Stop at a
missing contract and report that operation as unverified. Paid writes
need a separate design covering quotes, price limits, approval, cleanup,
and retention locks.
