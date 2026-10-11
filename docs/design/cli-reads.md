# CLI Reads Design

Status: Accepted; typed Zone contract requires independent review.

Read commands cover `project`, `portal`, `volume`, `loadbalancer`,
`globalloadbalancer`, and `containerregistry`. Portal types `Zone`; no new
SDK method or write. Follow [CLI](cli.md): one operation table per service,
Input flags, and Go output names; see [SDK](sdk-and-cli.md#service-packages).

## Goals

- Expose every SDK read in the CLI so account inspection needs no Go code.
- Check every decode live, or confirm its shape with a second source and
  check live when the account holds the resource.
- Never print credentials.

## Non-goals

- Writes. OpenTofu covers volume and load balancer writes for aboutme;
  each write set needs its own design under
  [ADR 0002](../adr/0002-write-api-conventions.md).
- New SDK reads, such as volume attachments or registry tags.
- Pagination helpers. Lists keep `DefaultPageSize`.

## Scope rules

`LoadConfig` requires a region from the profile, `VNGCLOUD_REGION`, or
`--region` for every command. Scope determines how the command uses it:

- Project: put the project ID in the path. The SDK discovers it by region;
  multiple projects cause exit 2 until `--project-id` selects one.
- Region: use the regional host without a project in the path.
- Global: ignore the required configured region; all regions give the same
  answer from a host without a region.

Use `project list-projects` to find `--project-id`.

## Verification

Live evidence compares raw bodies with SDK models in `hcm-3` and `han-1`.
This document records shapes, never account values. Read states mean:

- Live: a live response fills the decoded fields.
- Empty: the live call returns no items; the item model remains unchecked.
- Shape: no live row exists; the SDK envelope, bare or data-wrapped,
  matches GreenNode's official Go SDK.
- Broken: the live call fails or decodes wrong.

Hand-written fixtures do not prove live envelopes. See [portal](#portal)
for Zone provenance. Live `network.GetVPC` and `GetSubnet` return bare
objects, matching the official SDK, the second source for Shape reads.

- Every read returning a row fills its decoded fields.
- `volume.GetDefaultVolumeType` returns 404 `NotFound` with `{message}` in
  both regions. The official SDK uses the same path. Whether the account
  lacks a default type or the endpoint is gone remains unknown.
- `volume.ListVolumeTypeZones` decodes only the live item fields: `ID`,
  `Name`, `Description`, and `Zone`.
- Registry lists are Empty and map-backed; unseen keys reach output under
  [Secrets](#secrets).
- No volume, load balancer, certificate, global load balancer, registry
  repository, or registry user exists in the test account. Their Gets and
  child lists are Shape.

## Commands

SDK method names become kebab-case commands. Input fields supply flags
under [CLI](cli.md#operation-table); none has a type reflection skips.
"(r)" marks required flags. See [project](#project) for the global collision.
Every Get wraps one resource, such as `{LoadBalancer: {...}}`, rendered as
one compact JSON cell in table/text, as `compute get-server` does. Wiki Get
examples use `--query <Field>` to turn resource fields into columns.

### project

Short description: "Projects in the configured region".
`list-projects`: no flags, Region scope, Live.

`ListProjectsInput.Region` collides with global `--region`; `Service` panics
on that table. The field only filters results; the global flag picks the
regional host, and an empty field filters by configured region. Suppress
its flag while retaining JSON input:

```go
Read[project.Client, project.ListProjectsInput, project.ListProjectsOutput](
    kebab("ListProjects"), (*project.Client).ListProjects, NoFlag("Region")),
```

`Read` gains variadic options like `Write`, preserving existing tables.
`NoFlag(field)` skips flag reflection and collision checks in `validateOps`;
`--cli-input-json` still sets it, and gen-docs marks it JSON-only. A shared
rename-table entry would also rename vStorage's unrelated `Region` field.

### portal

Short description: "Account info, zones, and quotas".

| Command | Flags | Scope | State |
|-|-|-|-|
| `get-user-info` | none | Region | Live |
| `list-zones` | none | Project | Live |
| `list-quota-used` | none | Project | Live |
| `get-quota` | `--name` (r) | Project | Live |
| `get-tag-quota` | none | Project | Live |

`UserInfo`, `Quota`, and `TagQuota` remain map-backed and print API keys.
The zone fixtures provide no evidence for typing those models.
`get-quota` matches `--name` against `quotaName`, case-insensitive.
`get-user-info` prints account data (email, names, user ID, cash and billing
status); see [Secrets](#secrets).

#### Typed Zone contract

The owner approves typing `portal.Zone`; the implementation is map-backed.
[list_zones.json](../../testdata/portal/list_zones.json) is hand-written
compatibility evidence for `id` and `name` in a `zones` envelope, with no
capture provenance. [discovery_zones.json][zone-capture] is a sanitized raw
capture documented in [discovery provenance][zone-provenance]: GET
`/v1/{projectId}/zones`, status 200, in hcm-3. Its `data` rows contain the ten
detailed fields below, without `id`. Neither fixture contains explicit nulls.
Keep synthetic compatibility separate from observed API evidence. Neither
establishes a published schema or behavior in every region.

[zone-capture]: ../../testdata/portal/discovery_zones.json
[zone-provenance]: ../../testdata/pricing/discovery-provenance.md#files

`danny.vn/vngcloud/portal.Zone` is a struct with these fields in this order.
The API key is the exact `json` tag, without `omitempty`; the Go field name
is the CLI JSON key under `Items`, including for `--query` and table/text.

| Go field / CLI key | Go type | API key / SDK JSON key |
|-|-|-|
| `ID` | `*string` | `id` |
| `UUID` | `*string` | `uuid` |
| `Name` | `string` | `name` |
| `ZoneType` | `*string` | `zoneType` |
| `IsDefault` | `*bool` | `isDefault` |
| `Description` | `*string` | `description` |
| `IsEnabled` | `*bool` | `isEnabled` |
| `VolumeCount` | `*int` | `volumeCount` |
| `ServerCount` | `*int` | `serverCount` |
| `OpenStackZone` | `*string` | `openstackZone` |
| `IPRanges` | `*[]string` | `ipRanges` |

Only `Name` appears in every fixture row. Other fields use pointers because
one fixture omits them. Absent or null pointers decode to nil and print as
JSON null; present false, zero, empty string, and empty array remain distinct.
`ID` and `UUID` stay separate: no evidence establishes an alias or fallback.
`ZoneType` stays an open string. Counts use integers and ranges use strings;
the fixtures establish neither maximum counts nor an address format rule.

Ignore unknown fields during decoding; do not retain an extras map or print
them. New fields require evidence and a reviewed contract. A wrong type in
a known field fails decoding, returns no partial `ListZonesOutput`, and uses
the existing transport `APIError` path. Keep `ListZonesInput{}`, nil input,
`ListZonesOutput = core.List[Zone]`, the project-scoped GET, success 200,
flexible envelopes, metadata, cancellation, retries, and CLI exit rules.
Add no pagination, waits, dependencies, flags, or response validation rules.

The struct breaks SDK map indexing, literals, iteration, and assertions;
use fields and nil checks. CLI keys and queries change from `id` and
`isEnabled` to `ID` and `IsEnabled`. Unknown keys disappear; absent pointers
print null. SDK JSON retains API spellings with those same omission rules.
Treat this as breaking: add migration examples to `RELEASE_NOTES.md`, and
update wiki and generated docs with implementation. The owner selects the
release version. No dual-key compatibility mode is defined.

Pricing discovery retains `flavorDiscoveryZone` in
`compute/discovery_decode.go` and `discoveryZone` in
`volume/discovery_decode.go`, reading `uuid`, `name`, and `isEnabled` from
`data` without portal. Preserve [discovery](pricing-discovery.md) ID,
enabled-state, duplicate, budget, and disabled-zone quote rules.

Required checks decode both fixtures through `portal.Client.ListZones`
and assert every field, pointer presence, and both envelopes. Synthetic
variants cover null, absent, zero, false, empty ranges, unknown secret keys,
and wrong known-field types without partial output or body leaks. CLI
goldens cover JSON, table, text, and queries with Go names and nulls; unknown
keys must never reach output. Retain quota redaction tests and run compute
and volume discovery regressions against the unchanged discovery fixture.

#### Caller migration inventory

- `portal/portal.go` defines the struct; `portal/portal_test.go` replaces
  map indexing with nil-checked fields and covers both fixture sources.
- `internal/cli/svc_portal.go` updates its map-backed comment.
  `internal/cli/svc_portal_test.go` migrates the Zone literal and lowercase
  output assertion in `TestGoldenPortalListZones` and
  `TestPortalListZonesUsesTheProjectScopedPath`. Update
  `internal/cli/testdata/golden/portal-list-zones.{json,table}.golden`.
- `livetest/live_write_test.go` migrates `pickEnabledZoneID`: require
  `IsEnabled != nil && *IsEnabled` and `UUID != nil && *UUID != ""` before
  returning `*UUID`. Skip nil, false, or empty values; preserve the first
  eligible row, list errors, and no-enabled-zone error. Never fall back to
  `ID`. Its callers are `createLiveVPCAndSubnet`, `useLiveVPCAndSubnet`,
  `TestLiveWriteNetworkVPC`, and `TestLiveWriteLoadBalancer` in that file.
- `examples/basic/{resources,output}.go` retain `[]portal.Zone` recording
  and the `portal/zone` operation map. `livetest/{live_test,live_cli_test}.go`
  retain row counts and Items checks. `internal/core/list_shape_test.go`
  keeps Items reflection. `livetest/live_write_encryption_test.go` uses
  the VPC helper through `createLiveServerPrereqs`, without field access.
- `docs/wiki/IDs-for-Create-Server.md` updates its ID table, map claims,
  and lowercase query to `Items[].[UUID,Name]` for the captured shape;
  explain nil UUIDs and the separate synthetic `ID`, without a fallback.
  `docs/wiki/Services.md` updates its table and map-backed paragraph.
- `internal/cli/gendocs_notes.go` removes `portalMapRedactionNote` from
  `portalListZonesNote` and excludes Zone from its shared-map comment.
  Describe typed keys and ignored unknown fields; retain the enabled-flag
  hint. Regenerate `docs/wiki/CLI-Portal.md` with `make gen-docs`.
- In `internal/cli/gendocs_test.go`, change
  `TestGenDocsPortalOpsDocumentAccountDataAndRedaction` to require map
  redaction only for the four remaining portal commands and assert the
  typed zone note instead. Preserve
  `TestGenDocsNotesCoverZoneEnabledFlagAndRootDiskText`.
  `internal/cli/redact_maps_test.go` replaces its Zone map example comment;
  keep map-redaction coverage using Quota or the existing synthetic maps.
- Command-only references in `docs/wiki/{Configuration,Network}.md`,
  `CLI-{Compute,Volume,Network}.md`, and
  `internal/cli/gendocs_notes{,_vserver}.go` need no field migration.
  Pricing discovery uses the private decoders above; other services' Zone
  types are separate. Preserve versioned release facts; add the new break
  to `RELEASE_NOTES.md` and remove the completed Zone item from `TODO.md`.

Test the picker with nil, false, empty UUID, and valid rows. Require
`make check`, generated-doc freshness, `go vet -tags live ./...`, and
`go vet -tags livewrite ./...`. Both vet passes compile without live calls;
they do not authorize running the live tests.

Zone typing guards decoded output only. Unknown secret keys cannot reach
stdout; raw capture hooks and known string values keep existing behavior.
Malformed fields use `internal/transport/transport.go:247`; test sanitized
errors and no partial output (`portal/portal.go:52`). Hostile oversized
bodies remain a high-severity memory risk: portal sets no `MaxBody`
(`internal/transport/transport.go:124`). Body limits need a separate scope.
Auth, redirect, cache, paid-write, and cleanup paths do not change.

### volume

Short description: "Block volumes, volume types, and snapshots".

| Command | Flags | Scope | State |
|-|-|-|-|
| `list-volumes` | `--name`, `--page`, `--size` | Project | Empty |
| `get-volume` | `--volume-id` (r) | Project | Shape |
| `get-underlying-volume` | `--volume-id` (r) | Project | Shape |
| `list-volume-type-zones` | `--zone-id` | Project | Live |
| `list-volume-types` | `--volume-type-zone-id` | Project | Live |
| `get-volume-type` | `--volume-type-id` (r) | Project | Live |
| `get-default-volume-type` | none | Project | Broken |
| `list-encryption-types` | none | Project | Live |
| `list-snapshots` | `--volume-id` (r), `--page`, `--size` | Project | Shape |
| `list-all-snapshots` | none | Project | Empty |

- `get-default-volume-type` stays out of the table until a live account
  returns 200 (see [decision 3](#owner-decisions)).
- `list-all-snapshots` sends one request per volume after the volume list.
- `Volume.VolumeType`, `Volume.Throughput`, and several `Snapshot` fields
  are `any`. They print whatever the API sent, with the API's key names.

### loadbalancer

Short description: "Regional load balancers, listeners, pools, and
certificates". Project scope. ID flags are required except `--zone-id`;
all other flags are optional.

|Command|Flags|State|
|-|-|-|
|`list-load-balancers`|`--name`, `--page`, `--size`|Empty|
|`get-load-balancer`|`--load-balancer-id`|Shape|
|`list-packages`|`--zone-id`|Live|
|`list-certificates`|`--name`, `--page`, `--size`|Empty|
|`get-certificate`|`--certificate-id`|Shape|
|`list-listeners`|`--load-balancer-id`|Shape|
|`get-listener`|`--load-balancer-id`, `--listener-id`|Shape|
|`list-pools`|`--load-balancer-id`|Shape|
|`get-pool`|`--load-balancer-id`, `--pool-id`|Shape|
|`get-pool-health-monitor`|`--load-balancer-id`, `--pool-id`|Shape|
|`list-pool-members`|`--load-balancer-id`, `--pool-id`|Shape|
|`list-policies`|`--load-balancer-id`, `--listener-id`|Shape|
|`get-policy`|`--load-balancer-id`, `--listener-id`, `--policy-id`|Shape|
|`list-tags`|`--load-balancer-id`|Shape|

- A `LoadBalancer` row has 24 columns in `table`. The wiki example uses
  `--query 'Items[].{ID:UUID,Name:Name,Status:DisplayStatus}'`.
- `Nodes`, `Members`, `L7Rules`, and `InsertHeaders` are nested lists and
  render as compact JSON cells.
- The `Certificate` model has no key or PEM field, and typed models drop
  unknown fields, so a private key in a response could not reach output.

### globalloadbalancer

Short description: "Global load balancers, pools, and listeners".
Global scope; ID flags required, other flags optional.

|Command|Flags|State|
|-|-|-|
|`list-packages`|none|Live|
|`list-regions`|none|Live|
|`list-load-balancers`|`--name`, `--offset`, `--limit`|Empty|
|`get-load-balancer`|`--load-balancer-id`|Shape|
|`list-pools`|`--load-balancer-id`|Shape|
|`list-listeners`|`--load-balancer-id`|Shape|
|`get-listener`|`--load-balancer-id`, `--listener-id`|Shape|
|`list-pool-members`|`--load-balancer-id`, `--pool-id`|Shape|
|`get-pool-member`|`--load-balancer-id`, `--pool-id`, `--pool-member-id`|Shape|
|`list-usage-histories`|`--load-balancer-id`, `--from`, `--to`, `--type`|Shape|

- `list-load-balancers` pages by `--offset` and `--limit`, not `--page` and
  `--size`, because the API does.
- `--from`, `--to`, and `--type` pass through unchecked. Their formats and
  allowed values are unknown; the wiki says so until a live call shows
  them.
- `Package.Detail` is `any` and prints the API's object as-is.

### containerregistry

Short description: "Container registry repositories".

| Command | Flags | Scope | State |
|-|-|-|-|
| `list-repositories` | `--access-level` | Global | Empty |
| `list-users` | `--name`, `--page`, `--size` | Global | Empty |

`--access-level` defaults to `ALL`; other values are unknown. Both models
are map-backed without live rows. [Secrets](#secrets) gates `list-users`.

## Service names and pages

Use package group names without aliases under [CLI](cli.md#commands),
such as `vngcloud globalloadbalancer list-load-balancers`. In gen-docs,
`serviceTitle` maps `loadbalancer` to `LoadBalancer`, `globalloadbalancer`
to `GlobalLoadBalancer`, and `containerregistry` to `ContainerRegistry`.
Pages use `CLI-<Title>.md`; project, portal, and volume use the default rule.

## Secrets

Typed Outputs omit credential and unknown fields. Map-backed models pass
every key through, with these risks:

- `containerregistry list-users`: a user may carry a password or robot
  token; no live row identifies the keys. Hold until a live capture supports
  a typed `User` with no secret field ([decision 4](#owner-decisions)).
- `containerregistry list-repositories`: secrets are unlikely, but no live
  row exists. Ship with key redaction.
- `portal get-user-info`: print the caller's email, names, user ID, cash,
  and billing status, which are account data, not credentials. The wiki
  warns that agent transcripts retain them.
- Portal quotas: no secrets seen in live rows; print with map redaction.

No service here returns a kubeconfig; the SDK has no Kubernetes package.

Before encoding any map-backed Output, lowercase each `map[string]any`
key and remove nonletters and nondigits. At every depth, replace its value
with `<redacted>` if the result contains `password`, `passwd`, `passphrase`,
`secret`, `token`, `credential`, `privatekey`, `apikey`, `authorization`,
`accesskey`, or `dockerconfig`, or equals `auth` or `auths`. Leave typed
fields, such as monitor `map[string]string` headers, unchanged. This guards
unseen keys without replacing typed models. Test every key through JSON,
table, text, and queries; no secret value may appear.

## Live checks

Before release, require `make live` checks in `livetest/live_test.go` and
`livetest/live_cli_test.go`. Log only counts and field presence.

- Project: retain SDK coverage; CLI `project list-projects` returns an item.
- Portal: SDK `ListZones`, `ListQuotaUsed`, `GetQuota` for the first quota's
  name, `GetTagQuota`, and `GetUserInfo` key count; CLI `portal list-zones`.
- Volume: SDK `ListVolumeTypeZones`, `ListVolumeTypes`, `GetVolumeType` for
  the first type, and `ListEncryptionTypes`. With a volume, check
  `GetVolume`, `GetUnderlyingVolume`, and `ListSnapshots` on it.
  CLI: `volume list-volume-types`.
- Load balancer: SDK `ListPackages`, `ListCertificates`; with a load
  balancer, `GetLoadBalancer` and each child read on the first child;
  with a certificate, `GetCertificate`. CLI: `loadbalancer list-packages`.
- Global load balancer: SDK `ListPackages`, `ListRegions`; with a load
  balancer, `GetLoadBalancer`, child reads on the first child, and
  `ListUsageHistories`. CLI: `globalloadbalancer list-regions`.
- Registry: SDK `ListRepositories`, `ListUsers`; log key counts for rows.
  CLI: `containerregistry list-repositories`.

Gets must return the listed ID, as vMonitor checks, so empty decodes fail.
Absent resources log `skipped: none` and pass while commands remain Shape.
When resources exist, replace synthetic fixtures with sanitized raw captures
in `testdata/` under [live data](../../instructions/live-data.md).
The `portal-user` check logs only a key count, never `%+v` account data.

## Security

- All commands are reads. `--read-only` does not affect them.
- A missing required flag stops before any request, as today.
- Key redaction and the held `list-users` keep registry secrets out of
  stdout. No command writes a file.
- No adversarial review is needed: no write, auth, or credential code
  changes. The one final review per release still runs.

## Testing

Run each CLI command in-process against an `httptest` fixture server; check:

- The command list matches the table in this doc, with the right flags.
- Required flags stop before any request.
- Golden `json` output for one list and one Get, and `table` for one list.
- For `project`, `--region` is the global flag, `--cli-input-json
  '{"Region":"han-1"}'` sets the field, and gen-docs marks it JSON-only.
- For map-backed Outputs, the redaction test above.

## Releases

Release each service separately except account-level project and portal.
Assign versions when shipping, as for vMonitor Alerts and vStorage.

- R1: project and portal commands, `NoFlag`, map redaction, and
  `serviceTitle` cases for R3 to R5.
- R2: volume commands without `get-default-volume-type`.
- R3: loadbalancer commands.
- R4: globalloadbalancer commands.
- R5: `containerregistry list-repositories`; add `list-users` when the SDK
  types `User`.

R1 changes shared `op.go`, `flags.go`, `encode.go`, and `gendocs.go` first.
After R1 merges, R2 to R5 add service files `internal/cli/svc_<name>.go`,
tests, generated wiki pages, and live checks. Shared additions are
`root.AddCommand` in `root.go`, `buildDocService` in `gendocs.go`, the wiki
`_Sidebar.md` and generated `CLI.md` index, and subtests in
`livetest/live_test.go` and `livetest/live_cli_test.go`. Rerun `make gen-docs`
after merges instead of merging `CLI.md` by hand. Limit workers to three:
R2 to R4 run in parallel, then R5. The typed [Zone](#typed-zone-contract),
R5 User, and [decision 5](#owner-decisions) cleanup require SDK changes.

## Owner decisions

The owner approves these recommendations.

1. Ship Shape reads matching official SDK envelopes; mark them unverified
   in the wiki and check Gets when resources exist. Creating volumes or
   load balancers for checks costs money and needs paid-write approval.
2. Use `NoFlag` for project `Region`. A shared `--filter-region` rename
   would also affect vStorage's unrelated field.
3. Hold `get-default-volume-type` until a live 200. It returns `NotFound`
   in both regions; a command that always exits 4 misleads agents.
4. Hold registry `list-users` until a live capture supports typed `User`.
   Approve one user create/delete if free; otherwise wait.
5. In R2, drop seven never-sent envelope fields from `VolumeTypeZone`.
   This breaks SDK callers, permitted before `v1.0.0`, and needs a release
   note. Keeping the fields would print empty columns.
6. Keep package names without aliases under [CLI](cli.md#commands);
   `glb` and `vcr` would break the one-name rule.
7. Apply map key redaction to all map-backed Outputs in R1. It affects only
   keys that look secret and costs nothing for the observed rows.
