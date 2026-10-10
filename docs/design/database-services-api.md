# Database API contracts

Status: Draft (2026-10-10).

This companion to [database inventory](database-services.md) separates
verified IAM console list contracts from published resource schemas and
facts still needed. Every observed list was empty. No populated resource
model or detail route has live evidence.

The first releases are list-only. Each detail route below is a published
candidate held for a separate later release. Before that release, verify
the exact console route, bearer-only IAM replay without cookies, success
envelope, list-to-detail ID mapping, and safe error behavior. Owner acceptance
of published resource-field evidence cannot waive any of those gates.

## Sources

Read on 2026-10-10:

- [GreenNode vDB API reference][api], including its embedded OpenAPI 3.1
  document, API version 1.0.
- [Vendor Go SDK source][sdk] at commit
  `a76d5fc0fdbd31663c51a7ba5cb71c15b3604188`.

[api]: https://docs.api.greennode.ai/service-docs/vdb-api.html
[sdk]: https://github.com/vngcloud/vngcloud-go-sdk/tree/main/vngcloud

The vendor SDK files used are `gateway/vdb_kafka_gateway.go`,
`gateway/vdb_opensearch_gateway.go`, and each family's `services/<family>/v1/`
files named `url.go`, `<family>.go`, and `<family>_response.go`. These are
contract evidence, not a new dependency or code to copy into this SDK.

The reference embeds `https:/vdb-gateway.vngcloud.vn` as its server URL,
with one slash after `https:`. The vendor SDK names the valid host
`https://vdb-gateway.vngcloud.vn`. The current IAM console host and any
proxy prefixes are established by the separate live evidence below.

The fetched OpenAPI security scheme is HTTP bearer `IAMAccessToken`, with
description `IAM access token of the portal user`. The indexed rendered
reference names required `portal-user-id` headers, but the fetched embedded
schema omits them. The vendor SDK sends that header for Kafka and
OpenSearch. The verified IAM console lists do not need that header; do not
generalize that finding to the separate public service-account API.

Published read responses name 200, 202, 204, 400, 401, 403, 404, and 500.
Only 200 has a modeled body. Error bodies have no schema. HTTP 429 and 5xx
use the SDK's existing transport handling; no rate-limit headers are
documented by these sources.

## Verified IAM console lists

Read-only console discovery on 2026-10-10 established the origin
`https://vdb.console.greennode.ai` for all four families. Replaying each
observed list request with bearer auth in a fresh isolated browser context
without cookies returned HTTP 200. No `portal-user-id` header was needed.

The exact successful GET requests used these paths and queries:

```text
/vdb/vdb-kafka/clusters
/vdb/vdb-relational/v1/database-instances?pageNumber=1&pageSize=100
/vdb/vdb-memory/v1/database-instances?pageNumber=1&pageSize=500
/vdb/vdb-memory/v1/database-instances?pageNumber=1&pageSize=100
/vdb/open-search/v1/{projectId}/open-search
```

Kafka returned the bare empty array `[]`. Relational and MemoryStore
returned the envelope shown in the relational section, with `code: 200`,
string `message`, string `data.projectId`, empty `data.data`, and numeric
page metadata. OpenSearch returned empty `listData` and numeric `page`,
`pageSize`, `totalPage`, and `totalItem` without a query string.

The browser obtains project mapping through
`/iam-vserver-gateway/v1/projects`, whose `projects` rows contain
`projectId` and `userId`. The observed list routes need no portal user ID.
OpenSearch uses the project ID in its path.

Only the HCM/current-region session was verified. Empty lists do not prove
cross-region scope, resource fields, detail routes, or complete pagination.
No live account identifiers or raw captures belong in this document.

## Kafka

Published paths, relative to the vendor gateway origin:

| Operation | Method and path | Body |
|-|-|-|
| List | `GET /vdb-kafka/clusters` | Array of `KafkaCluster` |
| Detail | `GET /vdb-kafka/clusters/{clusterId}` | `KafkaCluster` object |

The verified console list is `GET /vdb/vdb-kafka/clusters`. A console
detail under the same prefix is a source-backed candidate, not a live fact.

Neither operation has a project, region, or pagination query. The vendor
SDK describes the list as every cluster owned by the authenticated portal
user. The cluster model carries `vserverProjectId`; that field does not
make the request project-scoped. The detail ID is the list row's `id`.

The proposed `KafkaCluster` fields retain these published wire keys and
types. None has been checked against a populated live row:

| Wire keys | Go type |
|-|-|
| `id`, `name`, `status`, `kafkaVersion` | `string` |
| `serverFlavorId`, `kafkaStorageType`, `vserverProjectId` | `string` |
| `networkId`, `subnetId`, `configGroupVersionId` | `string` |
| `volumeType`, `volumeTypeZoneId`, `instanceType`, `createdAt` | `string` |
| `kafkaBrokerCount`, `kafkaStorageSize`, `iops`, `ram`, `vcpus` | `int` |
| `kafkaStorageUsage` | `[]int64` |
| `fixedIps`, `floatingIps` | `[]string` |
| `publicAccess`, `mtlsAuthen`, `saslAuthen` | `bool` |
| `encryptionVolume` | `bool` |

Use standard Go initialisms, such as `ID`, `VServerProjectID`, `FixedIPs`,
`MTLSAuthen`, `SASLAuthen`, `IOPS`, `RAM`, and `VCPUs`. Keep timestamps as
strings until the actual timestamp format and null handling are verified.
Do not infer units for RAM or storage from field names; document units
only when the source or a probe establishes them.

Exclude `tags`, `portalUserId`, and `errorMessage` from the public model.
The first is an arbitrary value map, the second is account metadata not
needed for inventory, and the third can contain backend secrets.

Also exclude `securityGroupRules` from this release. The OpenAPI schema
uses `id`, `remoteIp`, `port`, `status`, and `createdAt`; the vendor SDK
uses `direction`, `protocol`, port ranges, and `remoteIpPrefix`. Resolve
that conflict with a raw live row before adding a rule model.

Still needed: populated list/Get bodies, the console detail route, and
actual account/project/region scope beyond the empty HCM session.

## Relational databases

Published prefix: `/vdb-relational/v1/database-instances`.

| Operation | Method and suffix | Success schema |
|-|-|-|
| List | `GET` on the prefix | `WrapContentDatabaseInstancesGatewayResponse` |
| Detail | `GET /id/{dbInstanceId}` | `WrapContentDbInstanceInfo` |

The verified console list adds `/vdb/` before the published prefix. The
successful unfiltered request sent only `pageNumber=1&pageSize=100`.

The list declares required query values `pageNumber` and `pageSize`, both
int32, and `filterRequest`, an object with optional `name` and string-array
`status`. The reference does not settle how `filterRequest` is serialized
on the wire. Do not guess a JSON string, `filterRequest[name]`, or flat
`name` and `status` parameters. The console accepts omission of that object
for an unfiltered list. Filter flags remain outside this inventory design.

The published list response below matches the observed empty envelope's
field names and types. `DatabaseInstancesGateway` remains source-backed:

```text
code: integer
message: string
data:
  projectId: string
  data: DatabaseInstancesGateway[]
  pageObject:
    totalPages: integer
    totalElements: integer
    size: integer
    number: integer
    maxSize: integer
```

The published detail response has `code`, `message`, and
`data: DbInstanceInfo`. The spec does not define the success-code value;
the observed list returned `code: 200`. Its `pageObject` was:

```json
{"totalPages":0,"totalElements":0,"size":100,"number":1,"maxSize":100}
```

The list's required page query has no default or page-base declaration.
Defaults of 1 and 10 on the history route do not prove list defaults.
The empty-list observation establishes the returned metadata, but not
multi-page behavior, omitted-query defaults, or an enforced size cap.

After verification, map `pageObject.number` to `Page`, `size` to `PageSize`,
`totalPages` to `TotalPage`, and `totalElements` to `TotalItem`. Preserve
`maxSize` as `MaxSize` and the envelope project as `ProjectID`.
Do not apply the repository's generic size 10000 until the API accepts it.

`RelationalDatabase` uses the common safe database fields below. Its detail
also retains `configId` as `ConfigID string`, `deleted` as `Deleted bool`,
and `numberOfReplicas` as `NumberOfReplicas int`. List rows omit those
detail-only fields; use separate private wire structs or documented
optional fields without treating absent detail data as a real value.

The relational schema includes PostgreSQL cluster keys such as
`deployType`, `numberOfNodes`, and `multiZoneInfos`. No standalone
PostgreSQL cluster inventory list is defined in the fetched reference.
Use the relational list for PostgreSQL inventory only when live rows or
further official evidence confirm that coverage.

Still needed: page base, defaults/cap, populated list/detail bodies,
console detail route, and scope beyond the current HCM
session. Inspect existing region and zone selectors without starting a
creation wizard.

## MemoryStore

Published prefix: `/vdb-memory/v1/database-instances`.

| Operation | Method and suffix | Success schema |
|-|-|-|
| List | `GET` on the prefix | `WrapContentDatabaseInstancesGatewayResponse` |
| Detail | `GET /{dbInstanceId}` | `WrapContentDbInstanceInfo` |

The verified console list adds `/vdb/` before the published prefix and
accepts `pageNumber=1&pageSize=500` without `filterRequest`. Its empty
response matches the relational envelope's field names and types.
The response has `code: 200` and this `pageObject`:

```json
{"totalPages":0,"totalElements":0,"size":500,"number":1,"maxSize":100}
```

The server echoes size 500 while reporting maxSize 100. Do not treat
`maxSize` as an enforced cap or assume a populated page can hold 500 rows.

A separate request with `pageNumber=1&pageSize=100`, IAM bearer auth, and
no cookies in a fresh context returned HTTP 200 and `code: 200`. Its data
array was empty and its `pageObject` was:

```json
{"totalPages":0,"totalElements":0,"size":100,"number":1,"maxSize":100}
```

Propose an SDK default size of 100. The two empty responses do not verify
populated paging, a second page, an enforced cap, the meaning of `maxSize`,
or cross-region scope.

The list parameters and envelopes match the relational declarations.
The detail suffix does not contain `/id/`. Verify MemoryStore separately;
a relational success does not prove MemoryStore paging defaults, limits,
or scope. Auth and list success code are independently verified for both.

`MemoryDatabase` keeps the safe common fields below and the detail-only
`configId` and `deleted` fields. Include `RedisPasswordEnabled bool` from
`redisPasswordEnabled` as a security setting. Do not model `redisPassword`
or another credential value, even if an upstream detail adds one.

Still needed: the same remaining list/detail checks as relational, using
MemoryStore's own responses. Do not create a database to obtain evidence.

## Common safe database fields

These wire keys exist in both published relational/MemoryStore row and
detail schemas. They are proposed model fields, not live-verified fields.
Each family has its own public resource type so later changes do not
silently join their contracts.

| Wire keys | Go type |
|-|-|
| `id`, `name`, `status`, `zoneId`, `region` | `string` |
| `datastoreType`, `datastoreVersion`, `volumeType` | `string` |
| `volumeTypeZoneId`, `projectId`, `subnetId` | `string` |
| `hostname`, `domainName`, `created`, `updated`, `role` | `string` |
| `replicaSourceId`, `replicaSourceName`, `deployType` | `string` |
| `privateRwIp`, `publicRwIp`, `privateRoIp`, `publicRoIp` | `string` |
| `ram`, `vcpus`, `volumeSize`, `port`, `portRo` | `int` |
| `numberOfNodes`, `poolMaxConnections`, `backupDuration` | `int` |
| `volumeUsed` | `float64` |
| `ip`, `replicas` | `[]string` |
| `publicAccess`, `backupAuto`, `enableProxies` | `bool` |
| `backupTime` | `string` |

Use `Configuration` with typed `ID` and `Name` fields for `configuration`.
For `address`, use `map[string][]Address`, where `Address` contains
`Addr`, `Type`, `Subnet`, and `ZoneID`, all strings. This map's values have
a fixed safe schema. Do not replace it with a generic map.

For `multiZoneInfos`, use a typed slice with string fields `zoneId`,
`subnetId`, `privateRwIp`, `publicRwIp`, `privateRoIp`, `publicRoIp`,
`rwPort`, `roPort`, and `status`. Its port fields are strings in the spec,
unlike the database's integer `port` and `portRo`. Preserve that distinction.

Omit billing fields, sharing metadata, arbitrary nested `securityGroup`
server objects, and unspecified values from these inventory models. The
spec's `InstanceEntity` schema is empty; it cannot justify a public model.
Sanitized raw fixtures retain omitted keys to test the intentional boundary.

## OpenSearch

The vendor SDK uses the console origin `https://vdb.console.vngcloud.vn`
and this prefix:

```text
/vdb/open-search/v1/{projectId}/open-search
```

List uses `GET` on the prefix. Detail uses `GET /{id}` beneath it. The SDK
sends `portal-user-id`, and its common client supplies bearer auth. The
detail ID is the full `id`, not the shortened display `clusterId`.
The list path is verified on the GreenNode console origin with bearer
auth alone. The detail suffix and ID mapping remain source-backed.

The vendor list decoder expects `listData`, `page`, `pageSize`, `totalPage`,
and `totalItem`. Detail expects an object under `data`. The vendor list
method sends no pagination query and discards the metadata when converting
its result. The observed empty list confirms those envelope fields, but
does not prove the first page contains every cluster.

The published vDB reference fetched for this design has no OpenSearch
section. The vendor source supports these candidate fields:

| Wire keys | Go type |
|-|-|
| `id`, `clusterId`, `projectId`, `name`, `status`, `version` | `string` |
| `region`, `vpcId`, `vpcCidr`, `subnetId` | `string` |
| `packageId`, `storageTypeId`, `configGroupId` | `string` |
| `createdAt`, `updatedAt`, `deletedAt` | `string` |
| `numberOfNodes`, `storageSize` | `int` |
| `enableTls`, `publicAccess`, `privateAccess` | `bool` |
| `encryptVolume`, `enableVpcDns` | `bool` |

Retain typed `PackageDetail` fields `id`, `name`, `type`, `ram`, `cpu`, and
`platform`; `StorageType` fields `id`, `name`, `min`, and `max`; and
`ConfigGroup` fields `name` and `version`. Number fields use `int`; other
listed fields use `string`. Omit description text, `portalUserId`, and
billing metadata from this inventory model.

Do not add `ListEndpoints` even though the vendor SDK has that route.
Connection URL inspection is outside this first inventory release.

Still needed: region behavior beyond the current HCM session, pagination
query names/base/default/cap, error semantics, populated list/detail bodies,
and the console detail route.

## Read-only probe contract

The manager opens only the four list pages and existing detail views:

```text
https://vdb.console.greennode.ai/relational/database
https://vdb.console.greennode.ai/memorystore/database
https://vdb.console.greennode.ai/kafka/cluster
https://vdb.console.greennode.ai/opensearch/cluster
```

Record only method, public origin, route templates, query names, header
names, status, field names/types, and counts in the report. Keep actual
IDs and bodies under ignored paths. Verify page changes with existing
navigation controls where available. Do not fetch secrets, backup exports,
certificates, credential bundles, or arbitrary guessed GET routes.

Follow-up findings per family:

1. Project and region selectors, including whether the list spans scopes.
2. Populated list shape, field nullability, and paging behavior.
3. Detail route and row ID mapping, if a resource already exists.
4. Detail envelope codes and populated-page metadata.
5. A safe error shape if an ordinary read produces an error; do not trigger
   writes, credential resets, or excessive failing requests to obtain one.

The design remains a draft. Verified empty-list contracts establish the
IAM console entry points; unresolved probes remain release prerequisites.
