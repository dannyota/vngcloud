# VKS reads

Status: Accepted (2026-10-10) for the first release: cluster list, cluster
versions, and quota. Held reads stay draft until their evidence passes.
Console read routes work in both regions with an IAM bearer token and no
cookies. The SDK's own credential provider still needs a live check during
implementation.

VKS reads let SDK and CLI callers inspect Kubernetes clusters and their
workers through their existing GreenNode profile. The SDK package and CLI
group are `vks`. The first release provides inventory reads whose endpoint,
authentication, and response shape have been verified. The wire contracts
and evidence gaps are in [VKS API](vks-api.md).

This design follows [SDK and CLI](sdk-and-cli.md), [CLI](cli.md), and
[CLI reads](cli-reads.md). No existing public method changes.

## Scope

The first release includes cluster listing, supported cluster versions,
and account quota. Both regions have verified routes and response
envelopes for those reads. Cluster item fields remain public-schema-backed
because both live lists were empty.

Cluster detail, node group list and detail, and nodes remain held for a
later inventory release. Their public schemas are documented, but no
populated response or successful IAM-console route is verified yet. The
candidate surface below keeps their names and types consistent.

Cluster events are a separate read feature. Their free-form messages need
a separate assessment of credential exposure and verified console routing.
The public contract is recorded, but no event method or command is included
in the inventory release.

Kubeconfig generation, retrieval, refresh, and merging into local files are
excluded, including operations described as GETs. They return credentials
or change access and require their own design. Cluster and node group
creation, updates, deletion, scaling, upgrades, auto-healing changes,
workspace activation, fleet operations, and paid probes are excluded.

## Approach

Use the existing IAM-user session against verified VKS console routes. Keep
the standard-library SDK and the CLI operation-table pattern. This avoids
adding another credential source to perform inventory reads.

The public VKS API uses service-account tokens. Adding built-in
service-account authentication is a separate auth design, not a fallback
when an IAM-user request fails. Direct public API routing is acceptable
only if a read probe proves that the current IAM-user token works there.
Never retry a token against guessed hosts or exchange an IAM-user token
for a service-account token implicitly.

Importing the official Terraform client would add dependencies, expose a
larger write surface, and retain outdated model declarations. Use that
client only as corroborating evidence.

## Region and authentication

The SDK keeps the region names `hcm-3` and `han-1`. The official guide calls
these `HCM-3` and `HAN`. Its documented public API hosts are:

| SDK region | Public API host |
|-|-|
| `hcm-3` | `https://vks.api.vngcloud.vn` |
| `han-1` | `https://vks-han-1.api.vngcloud.vn` |

The SDK uses these verified console bases with `v1` routes:

| SDK region | Console base |
|-|-|
| `hcm-3` | `https://vks.console.greennode.ai/vks-api/` |
| `han-1` | `https://vks-han-1.console.greennode.ai/vks-api/` |

Cluster listing, versions, and quota return HTTP 200 at both bases with
an IAM bearer token in fresh browser contexts containing no cookies.
No project query or header is required. The actual SDK credential provider
must pass the later live gate; these probes used the browser's IAM token.
The old HAN console hostname redirects to the verified GreenNode host.
Target the direct GreenNode host; cross-host redirects remain forbidden.

Add `VKS` to `EndpointOverrides`, internal endpoint sets and overrides,
normalization, and core endpoint lookup, plus `routes.ProductVKS`. One
verified endpoint per supported region is sufficient. Do not add automatic
fallbacks or a separate endpoint CLI flag. Tests use the SDK override.

Reject an unsupported region before login or any VKS request. The endpoint
override does not change the region's semantic meaning. A zero `Config`
must still return `ErrInvalidConfig` without a panic.

The released reads are scoped by account and region. They contain no
project ID, so do not call `RequireProjectID` or add a project header.
Document that `--project-id` does not select a VKS workspace. The API
describes quota as scoped to the user and workspace as carrying a project
ID. Never accept a caller's `portal-user-id` header or discover another
account's user ID.

No credential parsing, token-cache format, credential provider, or profile
key changes belong in this release. Static bearer tokens retain their
existing meaning and use the same verified endpoint as IAM-user tokens.

## Public SDK surface

Add `danny.vn/vngcloud/vks` with `New(cfg vngcloud.Config) *Client`. It uses
the shared core session. Root `vngcloud` imports no service package and
re-exports no VKS service types.

Every method has the usual `Method(ctx, *Input) (*Output, error)` signature.
Input and Output fields have no JSON tags. Resource fields carry API JSON
tags. Required IDs carry `vngcloud:"required"`.

| Method | Input fields | Output | Release |
|-|-|-|-|
| `ListClusters` | `Page`, `Size` | Paged `Items []Cluster` | First |
| `ListClusterVersions` | none | `Items []ClusterVersion` | First |
| `GetQuota` | none | `Quota Quota` | First |
| `GetCluster` | `ClusterID` required | `Cluster ClusterDetail` | Held |
| `ListNodeGroups` | `ClusterID` required, `Page`, `Size` | Paged `Items []NodeGroup` | Held |
| `GetNodeGroup` | `ClusterID`, `NodeGroupID` required | `NodeGroup NodeGroupDetail` | Held |
| `ListNodes` | `ClusterID`, `NodeGroupID` required, `Page`, `Size` | Paged `Items []Node` | Held |

Omit filters in the first release; both regions accept the cluster list
without `filter`. The console uses a `filter` field while the public API
documents individual query fields. Verify filtering semantics before
exposing them.

Before any held ID method ships, required IDs must pass
`core.CheckRequired` and a VKS-local validator before authentication or
any request. The local validator accepts only `^[A-Za-z0-9-]+$` and returns
a new error wrapping `ErrInvalidInput` that names the operation, field,
and rule without the supplied value. Do not return or wrap
`core.CheckPathID` errors: that helper echoes rejected input. Reject
traversal, slashes, query delimiters, fragments, control characters, and
percent escapes. The public examples use both `cls-` and `k8s-` prefixes,
so do not require one prefix or a UUID layout. ID methods remain held.

### Pagination

VKS uses zero-based pages. Input `Page` and `Size` are `int`. Nil input for
an all-optional operation means page 0 and size 10. `Page == 0` is the
first page; `Size == 0` chooses 10. Reject negative values and values above
the documented signed 32-bit range before any request. A positive size is
sent as `pageSize`; a page is sent as `page`.

Do not reuse `core.PageQuery`, whose first page is 1 and whose size query
key differs. Do not send the shared size default of 10000 without evidence
that VKS accepts it. The public documentation gives no upper page-size cap.

Paged outputs use the existing `PagedList[T]` shape. Map response `total`
to `TotalItem`, preserve `Page` and `PageSize`, and compute `TotalPage`
from the returned total and positive page size. Zero items means zero
total pages. Check integer conversion and division before producing output.
Reject negative totals, negative pages, and a nonpositive returned page
size as a fixed malformed-response error. Lists send one request and do
not auto-page. The wiki must say how to request page 1 after page 0.

### Models

[VKS API](vks-api.md#models) defines the typed field allowlist. Do not use
`map[string]any`, `any`, `json.RawMessage`, or an extra-fields container in
public models. Separate list models from detail models so omitted detail
fields do not look like reported false or zero values in list output.

Keep timestamps as strings until live evidence establishes one format.
Keep enum values as strings; reads must preserve unfamiliar server values.
Use pointer types for nullable nested configuration objects. No model
contains kubeconfig, tokens, certificates, keys, or client secrets.

## Errors and response handling

Require HTTP 200 for the three first-release console reads. The public
specification uses 202 for those reads on its public hosts; that does not
change the console contract. Held methods need their own success-status
evidence. Do not treat every 2xx response as a valid model.

Use the existing transport's TLS, redirect, bearer authentication, token
invalidation, cancellation, timeout, and GET retry behavior. Preserve the
existing public error classes and CLI exit codes. No waiter is needed.

The public error body is `{"error":{"message":"..."}}`, which differs
from the transport's string `error` field. VKS does not expose upstream
error text. Set `Sensitive: true` and a fixed `WithholdMessage` on every
VKS request. Project every request error into a new safe error before
returning it, including HTTP, network, redirect, and decode failures.
Copy only safe status, operation, and retryability fields. Derive an HTTP
code from status and use fixed operation-specific text. Never copy an
upstream message or code, call an unknown cause's `Error()` method, or
retain the original error as a cause. Network and URL errors can contain
request URLs or caller-supplied IDs even without an HTTP response.

Preserve cancellation and deadline semantics by matching
`context.Canceled` and `context.DeadlineExceeded` and wrapping only those
known sentinels in the new error. Preserve auth, permission, not-found,
and rate-limit classes through the existing safe sentinels derived from
status. Do not retain an original URL-bearing, body-bearing, or arbitrary
value-bearing cause. This boundary applies to the first release and must
be tested again before any held ID method ships.

Preserve known authentication failure classes with a newly constructed
safe error containing fixed reason text and only known status or sentinel
fields. Configuration failures detected before a VKS request keep the
existing configuration error contract. Local input validation names the
field and rule. It must not echo an arbitrary supplied value, since
callers can paste a token into an ID field by mistake. Do not weaken
shared validation to accommodate VKS.

Successful detail responses must carry a nonempty ID matching the requested
ID. A node group detail must also identify the requested cluster. A bare
`{}`, `null`, or wrong envelope must fail rather than decode into a false
success. Paged responses must carry `items` and usable metadata. Empty
arrays are valid; absent fields are not evidence of an empty account.

`ListClusterVersions` requires a non-null bare array. Every item must be a
non-null object with present string `version`, boolean `enable`, and
string `stage` fields. A present `enable: false` is valid; an absent or
null `enable` is not. `GetQuota` requires a non-null bare object with all
four keys: `maxClusters`, `numClusters`, `maxNodeGroupsPerCluster`, and
`maxNodesPerNodeGroup`. Each must be a present, non-null JSON integer that
fits the model type. Zero is valid. Detect presence separately from Go
zero values. Reject null, `{}`, wrong types, missing keys, and wrapped
envelopes with a fixed malformed-response error.

## Secret and capture boundary

`Sensitive: true` disables the SDK response-capture hook, including error
responses, and withholds decode errors. VKS exposes only typed inventory
fields. Unknown fields are dropped before SDK output or CLI encoding.
Tests inject fake kubeconfig, certificate, private key, and token fields
at top level and inside nested objects to confirm that boundary.

The basic example writes decoded inventory to its existing ignored SDK
output directory. Its VKS raw-capture file is absent by design. Fixture
evidence comes from isolated discovery captures, sanitized before entering
`testdata/vks/`; do not manufacture a raw fixture from SDK output.

Live IDs, names, addresses, and descriptions remain account data. Examples
and CLI commands may return those fields to the caller, but test logs and
design documents report only counts, types, and field presence. The wiki
states that CLI output contains account data.

No operation follows a returned Kubernetes API endpoint, fetches a
kubeconfig URL, calls `kubectl`, reads `~/.kube/config`, or writes a
credential file.

## CLI

Register only released methods with `Read` in `internal/cli/svc_vks.go`.
The service description is "Kubernetes clusters and node groups".

The first commands are `list-clusters`, `list-cluster-versions`, and
`get-quota`. Held commands are `get-cluster`, `list-node-groups`,
`get-node-group`, and `list-nodes`. Names follow SDK kebab casing, not the
official CLI's `nodegroups` spelling. Flags follow Input fields, including
`--page` and `--size`; held reads later add `--cluster-id` and
`--node-group-id`. No aliases are added.

All commands work under `--read-only`. Existing JSON, table, text,
`--query`, required-field checks, and `--cli-input-json` apply. No new
global flag or dependency is needed. Add `VKS` to `serviceTitle` so the
generated reference is `CLI-VKS.md`.

The wiki examples project a short inventory, such as
`Items[].{ID:ID,Name:Name,Status:Status,Version:Version}`. Detail examples
select `Cluster` or `NodeGroup` before table rendering.

## Evidence and release gate

Read-only console discovery establishes both regional bases, the three
first-release routes, cookie-free bearer authentication, HTTP 200, and
their envelopes. It verifies page 0 with size 10 in both regions and page
1 with size 1 in HCM, without filters. The HCM second-page response echoes
page 1 and size 1 with an empty list and total 0. Empty accounts cannot
prove traversal across populated pages. A page-size cap, cluster item
fields, and the SDK's token provider remain unverified. No service
activation or paid creation is part of discovery.

The six initial regional responses are persisted in ignored discovery
output. Sanitizing and adding tracked test fixtures remain implementation
work; persisted discovery captures are not committed fixtures. Replace
every account-specific quota limit and usage number with a synthetic
integer before tracking a fixture, in addition to the normal live-data
sanitization. Keep ignored captures only through fixture review and
handoff, then delete them under the live-data rules.

Before release, use the SDK's own credential provider against both regions
and compare decoded output with sanitized raw captures. Add raw fixtures
for the verified empty cluster lists, versions, and quota. Test Cluster
item decoding separately against the public schema and official Terraform
model, label that fixture as schema-derived, and mark live item decoding
as unverified in the wiki. Do not call a hand-built fixture a raw capture.

The first release ships the three verified read routes. Detail and child
reads need successful console routing evidence before a later release.
When no resource exists, public OpenAPI and the official Terraform client
can corroborate their models, but cannot prove a console route works.
Never infer a working endpoint from a 404 or empty model. A held operation
does not justify paid resource creation. Events, credentials, and mutations
each get a separate later design and release.

## Owned implementation files

The manager assigns disjoint file sets and keeps Git and release work.
The architect does not implement these changes. The first release adds
only its three operations and their models. `vks/node_groups.go` and
detail-specific fixtures, models, examples, and tests wait with the held
reads.

| Role | Files |
|-|-|
| SDK | `vks/vks.go`, `vks/clusters.go`, `vks/models.go`, `vks/types.go`, matching `vks/*_test.go`; first-release models only |
| SDK | `internal/endpoints/endpoints.go`, `internal/endpoints/endpoints_test.go`, `internal/routes/routes.go`, `internal/routes/routes_test.go`, `internal/core/config.go`, `internal/core/client.go`, `internal/core/client_test.go` |
| SDK | First-release `testdata/vks/*.json`, `examples/basic/vks.go`, `examples/basic/main.go`, `livetest/live_vks_test.go`, `docs/wiki/VKS.md`, `docs/wiki/Services.md` |
| CLI | `internal/cli/svc_vks.go`, `internal/cli/svc_vks_test.go`, `internal/cli/root.go`, `internal/cli/gendocs.go`, `docs/wiki/CLI-VKS.md`, generated `docs/wiki/CLI.md`, `livetest/live_cli_vks_test.go` |
| Manager | `docs/wiki/_Sidebar.md`, `README.md`, release notes if needed |

`livetest/live_cli_vks_test.go` belongs to the CLI brief inside `livetest/`.
The SDK owner updates the exhaustive service index in `Services.md`.
The CLI owner generates `CLI-VKS.md` and `CLI.md` from its operation table.
No root service type re-export or module change is expected.

## Checks

Write failing deterministic tests before implementation. Tests cover:

- Exact host resolution for HCM and HAN, endpoint overrides, unsupported
  regions, nil inputs, zero config, and invalid IDs before any request.
- Exact GET paths and query keys; page 0, page 1, size defaults, invalid
  page values, empty lists, and metadata conversion.
- Verified envelopes, version objects, and quota through sanitized raw
  fixtures. Cluster item tests use clearly labeled schema-derived data
  until a populated raw capture exists. Held detail tests later cover
  nested values, null configuration, and identity checks.
- Missing envelopes, wrong JSON types, empty bodies, and malformed JSON.
- Version and quota responses containing `null`, `{}`, missing required
  keys, null fields, wrong types, and wrapped envelopes. Test valid false
  version enablement and zero quota values; reject fractional quota values.
- Documented error envelopes, 400, 401, 403, 404, 429, and 500, with normal
  retry and cancellation behavior. These status tests do not claim that a
  live server returned each status.
- No fake credential in SDK models, errors, error chains, capture hooks,
  debug output, CLI JSON, table, text, or query output.
- Network and redirect errors with fake secrets in URLs or cause text,
  preserving only known cancellation, deadline, and status sentinels.
  Before held ID reads ship, invalid-ID tests check that neither the error
  nor its chain contains the supplied value or a `CheckPathID` error.
- Every released CLI command, flag requirements after JSON merging,
  output shape, `--read-only`, and generated references.

The read-only live tests use existing resources. List clusters, versions,
and quota where released; for the first existing cluster, check its detail,
node groups, each released child read, and matching IDs. An absent resource
logs a count or a fixed skip reason. Never print raw or decoded objects.
Run one CLI inventory command per region.

Run `make check` before each commit and `make gen-docs` for CLI reference
changes. The manager runs the authorized live gate. An independent reviewer
checks endpoint authentication and the secret boundary before release.
The tag requires green CI on the exact signed release commit.
