# VKS API evidence

Status: Accepted (2026-10-10) for the first release. HCM and HAN console reads
verify cluster-list, version, and quota envelopes using an IAM bearer
token without cookies. The SDK credential provider needs a later live check.

This document records evidence for [VKS reads](vks.md). Public API routes
do not establish that the repository's IAM-user tokens can use them.

## Sources

Checked on 2026-10-10:

- [Official VKS API reference][api], including the OpenAPI document in the
  page's `__redoc_state.spec.data` object. This is the current public wire
  reference for the routes and fields below.
- [Official CLI guide][guide], at docs repository revision
  `0c10ca7a4521d5348dab82937443ed27f784d73c`. The [source][guide-source]
  is readable even when the published GitBook page is unavailable.
- [Public API authorization][auth], which documents service-account
  client credentials and an OAuth2 bearer token.
- [Official Terraform VKS client][tf], at revision
  `230bd7d346853b2aba33a9ec06f8c544b06fa8a2`. Its Go models and generated
  request methods corroborate cluster and node group routes and envelopes.

The CLI guide links to `github.com/vngcloud/greennode-cli` and
`vngcloud.github.io/greennode-cli/`. Both returned HTTP 404 during this
check. No claim in this design relies on having read that CLI's source.

[api]: https://docs.api.greennode.ai/service-docs/vks-api.html
[guide]: https://docs.greennode.ai/vks/getting-started/manage-vks-with-the-greennode-cli
[guide-source]: https://github.com/vngcloud/docs/blob/0c10ca7a4521d5348dab82937443ed27f784d73c/English/vks/getting-started/manage-vks-with-the-greennode-cli.md
[auth]: https://docs.api.greennode.ai/#api-documentation
[tf]: https://github.com/vngcloud/terraform-provider-vngcloud/tree/230bd7d346853b2aba33a9ec06f8c544b06fa8a2/client/vks

## Authentication and scope

The public auth guide requires a service-account client ID and secret to
obtain a token using `client_credentials`. The VKS OpenAPI declares HTTP
bearer auth. Neither source proves IAM-user token acceptance.

The OpenAPI servers map HCM-3 to `https://vks.api.vngcloud.vn` and HAN to
`https://vks-han-1.api.vngcloud.vn`. It also lists a fleet-management host;
that host is excluded from inventory routing.

No listed read route has a project path segment or project query field.
The Terraform client's generated methods accept an optional
`portal-user-id` header, but the current public OpenAPI does not document
that header. Its generated "No authorization required" text is not
evidence that the API is unauthenticated.

Isolated browser contexts with no cookies confirm these IAM-console bases:

| SDK region | API prefix |
|-|-|
| `hcm-3` | `https://vks.console.greennode.ai/vks-api/v1/` |
| `han-1` | `https://vks-han-1.console.greennode.ai/vks-api/v1/` |

The three first-release reads succeed using the browser's captured IAM
bearer token, with no project header or query. These probes establish
cookie-free bearer acceptance. They do not exercise the SDK credential
provider, which still needs a live check during implementation.

The HCM console configuration names `HCM-3` as its current region and
`HAN-1` as the switch target. Its old HAN switch URL,
`https://vks-han-1.console.vngcloud.vn`, redirects to the verified
GreenNode host. The SDK uses that direct destination.

### Observed reads

The manager observed these reads on 2026-10-10. Only shape information is
recorded here. The three inventory reads succeeded in both regions.

| Relative GET path | Status | Shape |
|-|-|-|
| `clusters` | 200 | Object: empty `items` array; numbers `total`, `page`, `pageSize` |
| `cluster-versions` | 200 | Array: string `version`, bool `enable`, string `stage` |
| `quota` | 200 | Object: numbers `maxClusters`, `numClusters`, `maxNodeGroupsPerCluster`, `maxNodesPerNodeGroup` |

Both cluster-list probes sent `page=0&pageSize=10`, omitted `filter`, and
returned an empty list. The console UI also sends `filter`, but its
semantics are not established here. The empty lists verify envelopes,
not Cluster fields. Versions returned populated objects with the three
fields listed above; no `deprecatedAt` field was observed.

An HCM cookie-free probe sent `page=1&pageSize=1` and returned HTTP 200
with `page: 1`, `pageSize: 1`, `total: 0`, and `items: []`. This verifies
second-page acceptance and metadata, not traversal across populated pages.

The six initial regional responses are saved in ignored discovery output.
Sanitized tracked fixtures have not been added. Their creation remains
implementation work. Keep these captures only through fixture review and
handoff, then delete them under the live-data rules.

An HCM console `workspace` read also returned HTTP 200 with strings
`projectId`, `serviceAccountId`, and `status`. That read is discovery
evidence, not an added SDK method. It establishes no contract for inactive
accounts or activation behavior.

## Public read routes

All paths below are relative to the documented regional public API host.
`C` denotes a cluster ID and `G` a node group ID. The success status is the
OpenAPI declaration, not a live observation.

| Read | GET path | Success | Body |
|-|-|-|-|
| Clusters | `/v1/clusters` | 202 | Paged `Cluster` |
| Cluster detail | `/v1/clusters/C` | 202 | Bare `ClusterDetail` |
| Node groups | `/v1/clusters/C/node-groups` | 202 | Paged `NodeGroup` |
| Node group detail | `/v1/clusters/C/node-groups/G` | 202 | Bare `NodeGroupDetail` |
| Nodes | `/v1/clusters/C/node-groups/G/nodes` | 202 | Paged `Node` |
| Cluster versions | `/v1/cluster-versions` | 202 | Bare array |
| Cluster events | `/v1/clusters/C/events` | 200 | Paged `Event` |
| Quota | `/v1/quota` | 202 | Bare `Quota` |

Paged bodies have `items` as an array, `total` as int64, `page` as int32,
and `pageSize` as int32. They are not wrapped in `data`.

The public query fields are:

| Read | Query fields |
|-|-|
| Clusters | `id`, `name`, `status`, `version`, `page`, `pageSize` |
| Node groups | `search`, `status`, `page`, `pageSize` |
| Nodes | `page`, `pageSize` |
| Events | `action`, `type`, `page`, `pageSize` |

`page` starts at 0 and defaults to 0. `pageSize` is positive and defaults
to 10. No maximum size is documented. The older Terraform cluster list
instead sends `filter` as JSON. Console queries must be verified before
exposing any filter.

All listed operations document 400 and 500 responses with
`{"error":{"message":string}}`. The schema gives no VKS-specific error
code. A 401, 403, 404, or 429 remains possible at the gateway, but no live
error evidence is recorded here. The [error rules][error-rules] define the
safe projection.

[error-rules]: vks.md#errors-and-response-handling

## Models

These are typed wire-field allowlists from the current OpenAPI. Go field
names use normal initialisms: `id` becomes `ID`, `vpcId` becomes `VPCID`,
`cidr` becomes `CIDR`, `imageOS` becomes `ImageOS`, and
`enabledBlockStoreCsiPlugin` becomes `EnabledBlockStoreCSIPlugin`.
`numNodes` becomes `NumNodes`; `enable` becomes `Enable`.

Use `string` for text and enum fields, `bool` for booleans, `int64` for
node counts and totals, and `int` for other int32 fields after checked
conversion. Dates stay strings. Arrays preserve their element type.
Nested optional configuration objects use pointers.

The first implementation must compare observed fields with sanitized raw
console responses. Cluster fields are schema-backed because live lists
were empty. Detail and child models remain held. A disagreement stops
implementation of the affected model until the design records the
observed contract. Do not add an `any` field to conceal uncertainty.

### Cluster

| Wire fields | Type |
|-|-|
| `id`, `name`, `description`, `status`, `releaseChannel`, `version` | string |
| `azStrategy`, `createdAt`, `updatedAt` | string |
| `numNodes` | int64 |
| `enablePrivateCluster` | bool |

The OpenAPI incorrectly labels `releaseChannel` as an object while its
enum and example are strings. Current Terraform Go models also use a
string. Treat it as a string, subject to raw-console verification.

### ClusterDetail

Cluster detail repeats Cluster fields and adds:

| Wire fields | Type |
|-|-|
| `networkType`, `vpcId`, `subnetId`, `cidr`, `location` | string |
| `secondarySubnets`, `whitelistNodeCIDRs`, `listSubnetIds` | []string |
| `enabledLoadBalancerPlugin`, `enabledBlockStoreCsiPlugin` | bool |
| `enabledServiceEndpoint`, `poc`, `autoRenewal` | bool |
| `nodeNetmaskSize` | int |
| `numReadyNodes`, `numNotReadyNodes` | int64 |
| `serviceEndpoint` | *ServiceEndpoint |
| `autoUpgradeConfig` | *AutoUpgradeConfig |
| `autoHealingConfig` | *AutoHealingConfig |
| `fleet` | *Fleet |

`networkType` has the same object-versus-string documentation conflict as
`releaseChannel`; Terraform's enum type is string-backed. `subnetId` is
deprecated in the public API but retained in reads alongside
`listSubnetIds`. Do not fabricate one field from another.

Nested field definitions:

- `ServiceEndpoint`: strings `id`, `status`, `createdAt`; bool `poc`.
- `AutoUpgradeConfig`: strings `weekdays`, `time`; optional
  `futureTriggers` object with strings `planStartAt`, `planEndAt`,
  `newVersion`. Despite its plural name, the current schema says object;
  verify the console representation before exposing that nested field.
- `AutoHealingConfig`: bools `enableAutoHealing`,
  `remediationTimedOutEmitted`; strings `maxUnhealthy`, `unhealthyRange`;
  int `timeoutUnhealthy`.
- `Fleet`: strings `id`, `name`; bools `enableNorthSouthTraffic`,
  `enableEastWestTraffic`, `isHost`, `host`. Both host fields appear in
  the current public schema; retain only those confirmed on the console.

### NodeGroup

| Wire fields | Type |
|-|-|
| `id`, `clusterId`, `name`, `status`, `imageId` | string |
| `kubernetesVersion`, `createdAt`, `updatedAt` | string |
| `numNodes` | int64 |

### NodeGroupDetail

Node group detail repeats NodeGroup fields and adds:

| Wire fields | Type |
|-|-|
| `flavorId`, `diskType`, `sshKeyId`, `imageOS`, `subnetId` | string |
| `placementGroupId` | string |
| `diskSize` | int |
| `enablePrivateNodes`, `enabledEncryptionVolume` | bool |
| `securityGroups`, `secondarySubnets` | []string |
| `labels`, `tags` | map[string]string |
| `autoScaleConfig` | *AutoScaleConfig |
| `upgradeConfig` | *UpgradeConfig |
| `taints` | []Taint |
| `dnsServiceConfig` | *DNSServiceConfig |

Nested field definitions:

- `AutoScaleConfig`: ints `minSize`, `maxSize`.
- `UpgradeConfig`: string `strategy`; ints `maxSurge`, `maxUnavailable`.
- `Taint`: strings `key`, `value`, `effect`.
- `DNSServiceConfig`: strings `projectId`, `vpcId`, `subnetId`, `vpcCidr`,
  `virtualAddressIp`; optional `privateLinkConfig` with strings `projectId`,
  `vpcId`, `vpcCidr`, `subnetId`.

Labels and tags are caller-controlled account metadata. Do not place
credentials in fixture values. If live evidence shows secret-bearing
metadata keys, omit those maps from this release instead of assuming the
CLI's map-backed redactor covers typed `map[string]string` fields.

### Node, ClusterVersion, and Quota

- `Node`: strings `id`, `name`, `status`, `floatingIp`, `fixedIp`; bools
  `ready`, `poc`.
- `ClusterVersion`: strings `version`, `stage`, `deprecatedAt`; bool
  `enable`.
- `Quota`: ints `maxClusters`, `numClusters`, `maxNodeGroupsPerCluster`,
  `maxNodesPerNodeGroup`.

`deprecatedAt` may be absent or null and must decode without inventing a
timestamp. No certificate or kubeconfig is part of these models.
The [response rules][error-rules] require presence and type checks for
version and quota fields; ordinary zero-value struct decoding is not
sufficient to validate these bare responses.

### Events held for a later release

The public Event model has strings `type`, `action`, `message`, and
`createdAt`. The message is free text and can describe infrastructure
operations. The schema alone cannot prove that messages exclude
credentials. Do not expose a raw event body or add an event command as an
untyped passthrough.

## Remaining read evidence

The manager owns account access and captures. Report routes with sensitive
IDs replaced, field names and types, status, and counts. Never report
tokens, cookies, names, addresses, account IDs, or raw bodies in chat.

1. Exercise the three verified console reads in both regions through the
   SDK credential provider during implementation. The browser's bearer
   token already works without cookies; this check verifies the SDK login
   and routing end to end.
2. Keep deterministic tests for page 0 and page 1. Page 0 with size 10 is
   verified in both regions; page 1 with size 1 is verified in HCM. Verify
   populated traversal when existing resources make it possible. No
   larger-size cap is established; retain the documented default 10.
3. If a cluster already exists, read its detail, node groups, a node group
   detail, and nodes through observed or publicly corroborated routes.
   Record all wire fields and types, including nulls and nested objects.
   Check for kubeconfig, credential, or certificate fields without
   printing their values.
4. Record an ordinary safe read error if one occurs. A fabricated resource
   ID may establish a not-found shape, but a failed request cannot prove
   that a detail route works. Do not trigger auth failures deliberately
   or use mutations to obtain errors.
5. Sanitize the verified captures using the [live-data rules][live-data],
   preserve response structure, and add fixture decode tests. Replace all
   account-specific quota limits and usage numbers with synthetic integers
   before tracking fixtures, including every value in the four quota
   fields. Delete ignored captures after fixture review and handoff. The
   live gate compares models with raw fields. A separate Cluster item
   fixture may be schema-derived, but must be labeled as such and never
   described as a live raw capture.

An empty live list verifies the endpoint and envelope, not item fields.
Public documentation plus a matching official client can corroborate a
model where no resource exists, but they cannot replace missing IAM-user
endpoint evidence. Record each held operation explicitly in the wiki.

[live-data]: ../../instructions/live-data.md
