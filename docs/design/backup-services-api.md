# Backup Center API evidence

Status: Accepted (2026-10-10) for the first release. HCM bearer reads
confirmed.

This file records evidence for [Backup Center reads](backup-services.md).
Public source establishes candidate routes and field meanings. Console
observations confirm the HCM IAM routes listed below. Other source routes
remain candidates until their own checks pass.

The first release exposes only `ListBackends` and `ListPolicies` in
`hcm-3`. Its policy input contains `Page` and `Size` only. Every detail,
server, destination, point, history, and additional filter remains held.

## Sources

The official [vBackup tool guide][guide] links to GreenNode's public
[MCP repository][repo]. Source references below use commit
`ca770f29e4b6eb2fe72248c6da6564a585ee4195`, inspected on 2026-10-10.
All source paths are under
`src/vbackup-mcp-server/greennode/vbackup_mcp_server/`.

- `config.py`: regional public gateways and region labels.
- `client.py`: token scope and separate vBackup, vServer, vDB, and metrics
  services.
- `paging.py`: query names, one-based paging, nullable unpaged metadata.
- `catalogue_handler.py`: backend listing.
- `backup_server_handler.py`: backup servers, volumes, points.
- `destination_handler.py`: destinations and detail reads.
- `policy_handler.py`: policy lists and details.
- `history_handler.py`: backup and restore history, filters, date window.
- `models/catalogue.py`, `models/backup_server.py`, `models/policy.py`,
  `models/points.py`, `models/history.py`: field extraction and units.

The official [restore point guide][points] confirms the console's backup
server, restore point, and history views. The [vBackup overview][overview]
distinguishes backups from block snapshots. Vendor source is evidence,
not code to copy or a dependency to install.

## Endpoints and authentication

Vendor `config.py` declares:

| Source region | Public gateway |
| --- | --- |
| `HCM-3` | `https://hcm-3.api.vngcloud.vn/vbackup-gateway` |
| `HAN` | `https://han-1.api.vngcloud.vn/vbackup-gateway` |

The source's local configuration uses service-account credentials. The
IAM console also calls the HCM public gateway above with an Authorization
header. No other auth, project, or region headers appeared on the observed
resource reads. The hostname does not imply service-account-only access.
Do not substitute `.console` for `.api` or insert `iam-`.

Fresh isolated requests with no cookies and only IAM bearer authorization
returned HTTP 200 for the HCM routes listed below. These checks establish
cookie-free IAM access and the HCM endpoint default. The SDK endpoint map
contains only `hcm-3`. `EndpointOverrides.BackupCenter` and
`routes.ProductBackupCenter` identify this service. Any other resolved
region fails with `ErrInvalidConfig` before authentication or network
access, including when an override is present. The source's `HAN` host is
unverified and held; no hostname is built by region interpolation.

The vServer snapshot proxy has separate names `VServerBackup` and
`routes.ProductVServerBackup`. Backend IDs are scoped to their product and
endpoint. Neither the backend IDs nor endpoint settings are interchangeable
without evidence; this design assumes no such interchangeability.

Public docs identify these read views on
`https://backupcenter.console.greennode.ai`:

- `/backup-server/list`
- `/backup-history/list`

### Confirmed console reads

On 2026-10-10, the backup server, destination, policy, and history views
produced the read requests below. Isolated cookie-free requests confirmed
the listed HCM collections. No activation, subscription, or resource write
was performed.

`GET https://backupcenter.console.greennode.ai/dr-manager/dr-regions`
returned HTTP 200 and a bare array of two region records. Each record has
string fields `VBackupGatewayUrl`, `VServerDashboardUrl`,
`VServerGatewayUrl`, `billingGatewayUrl`, and `name`. This lookup is
console routing evidence, not a proposed public SDK operation. The SDK
must not forward tokens to an arbitrary host returned by discovery.

The following reads used
`https://hcm-3.api.vngcloud.vn/vbackup-gateway/v1/`:

- `GET backends`: HTTP 200; `items` contains objects with string `id`
  and `name`. `page` and `pageSize` are null; `totalPages` and
  `totalItems` are numbers.
- `GET backup-instances?backendId=...&page=...&size=...`: HTTP 200;
  `items` is an empty array. `page`, `pageSize`, `totalPages`, and
  `totalItems` are numbers. The empty array verifies no server fields.
- `GET backup-statistic?projectId=...`: HTTP 200; numeric fields
  `totalBackupServers`, `totalProtectedServers`, `totalServers`,
  `totalBackupCompleted`, `totalBackupFailed`, `totalRestoreCompleted`,
  and `totalRestoreFailed`. These counters are not a quota API and are
  outside the first release.

The query names are observed. Query default values, filtering behavior,
and JSON integer range still require checks. No account identifiers or
returned account values are recorded here.

### Confirmed collection envelopes

Cookie-free HCM requests returned HTTP 200 for all these collections.
Each response contains `items`, `page`, `pageSize`, `totalPages`, and
`totalItems`. Requests that specify a page have numeric metadata. The
unpaged requests below have null `page` and `pageSize` and numeric totals.

- `backends`, unpaged, contains backend objects.
- `backup-instances` with `backendId`, `page=1`, `size=10` is empty.
- `backup-destinations` with `page=1`, empty `name`, `size=10` is empty.
- `backup-destinations` with `backendId`, `projectId`, unpaged, is empty.
- `backup-policies` with `backendId`, `projectId`, `page=1`, `size=10`
  contains one policy object.
- `histories/backup-instances` with `backendId`, `projectId`,
  `from_date`, `page=1`, `size=10` is empty.
- `histories/restoration` with `backendId`, `projectId`, `page=1`,
  `size=10` is empty.
- `histories/backup-destinations` with `page=1`, `size=10` is empty.

The destination history route is outside the proposed surface. Its empty
response does not confirm the source's history item model. The same limit
applies to every other empty collection. A sent query parameter is not
proof of required input or effective filtering.

### Policy requests without project or backend filters

Fresh zero-cookie contexts replayed the HCM policy list with only IAM
bearer authorization and these two query sets:

- `page=1&size=10`: HTTP 200, metadata `page=1`, `pageSize=10`,
  `totalPages=1`, `totalItems=1`.
- `page=1&size=200`: HTTP 200, metadata `page=1`, `pageSize=200`,
  `totalPages=1`, `totalItems=1`.

Both returned the same policy ID as the original request with project and
backend parameters, with the same row shape. This proves those parameters
can be omitted and size 200 is accepted. It does not prove filter effects,
account-wide scope, or cross-account isolation. No resource ID is recorded
here.

The first release sends only page and size for policies and no query for
backends. Both rely on token scope. Configured `ProjectID` and CLI
`--project-id` are ignored by these methods and must be documented as such.

### First-release output types

`ListBackendsOutput` preserves its nullable metadata with these exact
fields and wire mappings:

| Output field | Go type | Wire field |
| --- | --- | --- |
| `Items` | `[]Backend` | `items` |
| `Page` | `*int` | `page` |
| `PageSize` | `*int` | `pageSize` |
| `TotalPage` | `int` | `totalPages` |
| `TotalItem` | `int` | `totalItems` |

Do not decode backend metadata through `core.PagedList`, which uses
integer page fields. `ListPoliciesOutput` uses `core.PagedList[Policy]`:
the same wire keys map to `Items`, `Page`, `PageSize`, `TotalPage`, and
`TotalItem`, with ordinary integer metadata confirmed by both replays.

## Candidate GET routes

All paths below start with `/v1/`. Detail IDs are path segments. Only
`ListBackends` and `ListPolicies` enter the first release. All other rows
record held candidates, even where a console request confirmed an empty
envelope. Each held route needs its own complete evidence before release.

| SDK operation | Relative path |
| --- | --- |
| `ListBackends` | `backends` |
| `ListServers` | `backup-instances` |
| `GetServer` | `backup-instances/{id}` |
| `ListDestinations` | `backup-destinations` |
| `GetDestination` | `backup-destinations/{id}` |
| `ListPolicies` | `backup-policies` |
| `GetPolicy` | `backup-policies/{id}` |
| `ListServerVolumes` | `backup-instances/{id}/volumes` |
| `ListServerPoints` | `backup-instances/{id}/backup-instance-points` |
| `ListBackupHistory` | `histories/backup-instances` |
| `ListRestoreHistory` | `histories/restoration` |

Query fields established by handlers:

- Servers: `serverId`, `name`, `backendId`.
- Destinations: `name`, `type`, `backendId`.
- Policies: `name`, `backendId`, held until separately verified. The first
  release exposes neither filter.
- Backup history: `backupInstanceId`, `serverId`, `from_date`.
- Restore history: `backupInstanceId`, `serverId`.
- Paged collections: `page`, `size`. `pageSize` is a response field and
  is not a valid paging request key.

Collections use `items`, `page`, `pageSize`, `totalPages`, and
`totalItems`. Unpaged results can carry null page values. Volumes use a
bare array. Details return an object. Vendor helpers tolerate other
wrappers, but the Go SDK must not accept arbitrary shapes as empty lists.

Backup history defaults to 180 days. `from_date` is Unix milliseconds.
Vendor source reports that `projectId` and `backendId` are ignored by that
route. Restore history has no date filter in the inspected handler.

## Candidate resource fields

The following field names come from source extractors unless identified
as confirmed below. Confirm JSON number, string, null, and object types
before committing Go types. Model only the selected fields, including
when those objects are embedded in another resource.

### Backends and servers

`Backend`: `id`, `name`. Both are confirmed strings in the raw response.

`Server`: `id`, `name`, `serverId`, `serverDeleted`, `status`,
`backupEnabled`, `backupPolicyId`, `backupDestinationId`, `backendId`,
`projectId`, `latestRecord`, `createdAt`, `updatedAt`, `volumes`, `policy`,
and `destination`.

`ServerVolume`: `volumeId`, `backupEnabled`, `volumeSize`, `volumeUsage`,
and `latestRecord`. The vendor interprets volume sizes and usage as bytes.

Embedded policy references carry `id`, `name`, `isDefault`, and `config`.
Embedded destination references carry `id`, `name`, `status`, `type`, and
`isDefault`. Use the same safe nested decoders as full objects; never keep
the remaining object as raw data.

### Destinations

`Destination`: `id`, `name`, `status`, `type`, `isDefault`, `product`,
`numberOfBackupInstances`, `maxQuota`, `config`, `softDeleteConfig`,
`vaultLock`, `backendId`, `projectId`, `createdAt`, and `updatedAt`.

- `maxQuota`: `unlimited`, `maxQuota`. The numeric ceiling is in GB,
  separate from byte-valued storage usage.
- `config.vault` or `config.vstorage`: `regionId`, `regionName`,
  `containerName`, `projectName`, `storageService`, `skuUsage`, `used`,
  and `total`. `used` and `total` are bytes. Preserve both storage
  variants as separate optional typed fields.
- `softDeleteConfig`: `enable`, `retainDays`, `createdAt`.
- `vaultLock`: `enable`, `changeDuration`, `minRetention`, `maxRetention`,
  and `createdAt`. Duration and retention fields represent days.

The source accepts destination `config` as an object or JSON string.
Keep only the listed storage summary fields. Other configuration fields
can hold credentials and do not enter public output.

### Policies

`Policy`: `id`, `name`, `isDefault`, `product`, `backupInstanceCount`,
`config`, `backendId`, `projectId`, `createdAt`, and `updatedAt`.

The populated policy response confirms strings for `id`, `backendId`,
`projectId`, `product`, `name`, `createdAt`, and `updatedAt`; a boolean
`isDefault`; a numeric `backupInstanceCount`; and object `config`.
The response also contains numeric account field `userId`, which the
public model omits.

Policy `config` contains `hour`, `minute`, `timeZone`,
`isProtectedServer`, `hourlyEnabled`, `dailyEnabled`, `weeklyEnabled`,
`monthlyEnabled`, and matching cadence configuration objects.

Within `config`, the raw response confirms numeric `hour` and `minute`,
string `timeZone`, boolean cadence enable flags, boolean
`isProtectedServer`, and array-of-string `statusSendEmail`. The populated
`dailyConfig` contains numeric `retention` and `incrementalQuantity` and
string `backupType`. `hourlyConfig`, `weeklyConfig`, and `monthlyConfig`
are empty objects. Their populated field types remain source-only.

The first public `Policy` model allows only the confirmed string fields
listed above, `IsDefault bool`, `BackupInstanceCount int`, and typed
`Config PolicyConfig`. `PolicyConfig` contains `Hour int`, `Minute int`,
`TimeZone string`, the four cadence enable booleans,
`IsProtectedServer bool`, and `DailyConfig *DailyConfig`. Nested
`DailyConfig` contains `Retention *int`, `BackupType *string`, and
`IncrementalQuantity *int`. JSON tags match the confirmed camel-case keys.
These optional pointers preserve absence instead of inventing zeros.
Absent or null daily config is nil; an empty object has nil members.

Omit `hourlyConfig`, `weeklyConfig`, and `monthlyConfig` from the first
model. Empty objects prove no members, so their source-only `interval`,
`dayOfWeek`, `dayOfMonth`, `weekOfMonth`, or other cadence settings do not
become fields. Their enable flags remain available, and help states that
their configuration details are omitted. Later additions require populated
raw evidence. Do not use empty structs, maps, or raw JSON to expose them.

Omit `userId` and `statusSendEmail`. The latter's string-array type is
confirmed; the meaning of its values still needs verification.

### Restore points

`ServerPoint`: `id`, `backupInstanceId`, `serverId`, `status`,
`snapshotTime`, `finishTime`, `size`, `usage`, `destinationId`,
`destination`, `policySnapshot`, `backupVolumePoints`, `backendId`,
`projectId`, and `createdAt`.

`VolumePoint`: `backupVolumePointId` or `id`, `name`, `volumeId`,
`backupInstancePointId` or `parentId`, `status`, `bootable`, `bootIndex`,
`volumeTypeId`, `volumeSize` or `size`, `volumeUsage`, `snapshotTime`, and
`finishTime`. The alternative names come from the vendor's shared model
for generic and vServer projection routes. Implement only the names
confirmed for the generic route, not both by assumption.

Size and usage fields are bytes. The names `snapshotTime` and
`policySnapshot` do not make a restore point a `volume.Snapshot`.

### History

`BackupRun`: `id`, `backupInstanceId`, `backupInstanceName`, `serverId`,
`status`, `deletionStatus`, `snapshotTime`, `finishTime`, `size`, `usage`,
`policyId`, `destinationId`, `policySnapshot`, `destinationSnapshot`,
`backendId`, `projectId`, and `createdAt`.

Historical policy and destination snapshots can be JSON-encoded strings.
Decode only their ID and name into typed references. Keep those names
from the run; do not replace them with today's object names. Omit
`errorMessage` from public output.

`RestoreRun`: `id`, `type`, `status`, `backupInstanceId`,
`backupInstancePointId`, `backupVolumePointId`, `destinationServerId`,
`destinationVolumeId`, `finishAt`, `backendId`, `projectId`, `createdAt`,
and `updatedAt`. Omit the source field `config` entirely.

## Open console checks

The first release still needs live verification through the SDK's own IAM
provider and transport. Browser token replay does not verify that
integration. Compare both decoded SDK outputs with sanitized raw responses,
including metadata and the complete first-release policy allowlist.

The manager's authorized discovery must resolve these held contracts:

1. Hanoi IAM routing and cookie-free bearer access. Confirm whether that
   region needs any additional headers.
2. Paging and filter behavior for held reads. The first policy query and
   size 200 are verified; backend metadata is nullable and unpaged.
3. Populated raw field types for servers, destinations, points, volumes,
   and history; also populated hourly, weekly, and monthly policy
   configurations. Backend fields and the policy shape above are verified.
4. Detail and child-resource success envelopes. The listed collection
   envelopes are verified, but empty arrays do not verify resource models.
5. Safe disabled-service and permission-denied responses, without
   activating, subscribing, ordering, or creating backups.
6. Which generic reads are account-wide and which honor filters. Do not
   infer this from a one-project account or empty results.
7. Whether error bodies or nested config fields can contain credentials
   or execution text beyond the already identified fields.

Capture only sanitized structure in reports. Raw bodies stay in ignored
local output and are deleted when discovery ends. Paid or missing test
resources leave the corresponding operation unverified.

[guide]: https://github.com/vngcloud/docs/blob/main/English/backup-center/vbackup-mcp-server/vbackup-mcp-tools.md
[repo]: https://github.com/GreenNodeHub/greennode-mcp/tree/ca770f29e4b6eb2fe72248c6da6564a585ee4195/src/vbackup-mcp-server/greennode/vbackup_mcp_server
[points]: https://docs.greennode.ai/backup-center/cloud-backup/backup-server/backup-server-point-management
[overview]: https://github.com/vngcloud/docs/blob/main/English/backup-center/vbackup-mcp-server/README.md
