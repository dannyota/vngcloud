# CLI Reads Design

Status: Accepted (2026-09-26).

This design adds `vngcloud` read commands for the six SDK packages that have
none: `project`, `portal`, `volume`, `loadbalancer`, `globalloadbalancer`,
and `containerregistry`. It adds no SDK method and no write. Commands follow
[CLI](cli.md): one operation table per service, flags reflected from the
Input struct, and Go field names in the output. The SDK surface is in
[SDK and CLI](sdk-and-cli.md#service-packages).

## Goals

- Every SDK read has a CLI command, so an agent never needs Go code to
  inspect the account.
- No command ships on a decode that nobody has checked. Each read is either
  checked live, or its response shape is confirmed by a second source and
  a live check covers it as soon as the account holds such a resource.
- No command prints a credential.

## Non-goals

- Writes for these services. OpenTofu covers volume and load balancer
  writes for aboutme; each write set gets its own design under
  [ADR 0002](../adr/0002-write-api-conventions.md).
- New SDK reads, such as volume attachments or registry tags.
- A pagination helper. Lists keep `DefaultPageSize`.

## Scope rules

`LoadConfig` always needs a region, so every command needs one from the
profile, `VNGCLOUD_REGION`, or `--region`. What the region and project do
differs by service:

| Scope | Meaning for the CLI |
|-|-|
| Project | The call puts the project ID in its path. The SDK discovers it for the region; with more than one project it fails with exit 2 until `--project-id` is given |
| Region | The host is regional; no project in the path |
| Global | The host has no region; the configured region is required by `LoadConfig` but ignored, so every region gives the same answer |

`project list-projects` is the way to find the value for `--project-id`.

## Verification

Each read has one of these states. The live check ran on 2026-09-26 against
the test account in `hcm-3` and `han-1`, and compared the raw body with the
decoded SDK model. This doc records shapes only, never values.

| State | Meaning |
|-|-|
| Live | A live response decoded with its fields filled |
| Empty | The live call worked but returned no items, so the item model is unchecked |
| Shape | No live row, because the test account has no such resource; the SDK's envelope (`data`-wrapped or bare) matches GreenNode's official Go SDK |
| Broken | The live call fails or decodes wrong |

The SDK fixtures for these packages were written by hand when the SDK
started; none is a sanitized live capture. `network.GetVPC` and `GetSubnet`
had `data`-wrapped fixtures while the live API returns bare objects, which
is why fixtures alone count for nothing here. The official SDK has those
two calls unwrapped, so it would have caught that bug; it is the second
source for the Shape state.

Findings:

- No read decodes empty. Every call that returned a row decoded its fields.
- `volume.GetDefaultVolumeType` is Broken: 404 `NotFound` in both regions,
  body `{message}`. The official SDK calls the same path. Either the account
  has no default type or the endpoint is gone; nothing tells which.
- `volume.ListVolumeTypeZones` decodes, but its item model holds envelope
  fields the items never carry (`Success`, `ErrorCode`, `ErrorMsg`,
  `Extra`, `PoolName`, `VolumeTypeZones`, `UUID`). They print empty in
  every row.
- `containerregistry` lists are Empty, and their models are map-backed, so
  every key the API returns reaches the output unchecked (see
  [Secrets](#secrets)).
- The test account holds no volume, load balancer, certificate, global load
  balancer, or registry repository or user, so every Get and child list for
  them is Shape.

## Commands

Command names are the SDK method names in kebab case, and flags come from
the Input fields, as [CLI](cli.md#operation-table) defines. "(r)" marks a
required flag. No Input in this design has a field type flag reflection
skips, so every field has a flag. One field collides with a global flag;
see [project](#project).

Every Get Output wraps one resource (`{LoadBalancer: {...}}`). In `table` and
`text` it renders as one cell of compact JSON, as `compute get-server` does
today. The wiki example for each Get uses `--query <Field>` so the fields
become columns.

### project

Short description: "Projects in the configured region".

| Command | Flags | Scope | State |
|-|-|-|-|
| `list-projects` | none | Region | Live |

`ListProjectsInput.Region` would derive `--region`, which collides with the
global flag, and `Service` panics on such a table. The field only filters
the result; the global `--region` already picks the regional host, and the
SDK filters by the configured region when the field is empty. The CLI
therefore gives the field no flag:

```go
Read[project.Client, project.ListProjectsInput, project.ListProjectsOutput](
	kebab("ListProjects"), (*project.Client).ListProjects, NoFlag("Region")),
```

`NoFlag(field)` is a new read option: flag reflection skips that field,
`--cli-input-json` still sets it, `validateOps` skips the collision check
for it, and gen-docs lists it as JSON-only. `Read` gains variadic options
the way `Write` has them; existing tables do not change. A rename-table
entry is the alternative, but that table is shared by every service, and
vStorage has its own `Region` field with another meaning.

### portal

Short description: "Account info, zones, and quotas".

| Command | Flags | Scope | State |
|-|-|-|-|
| `get-user-info` | none | Region | Live |
| `list-zones` | none | Project | Live |
| `list-quota-used` | none | Project | Live |
| `get-quota` | `--name` (r) | Project | Live |
| `get-tag-quota` | none | Project | Live |

All four models are map-backed, so output keys are the API's own names, as
[CLI output](cli.md#output) allows. `get-quota` matches `--name` against
the quota's `quotaName` key, case-insensitive; the live rows carry that key.
`get-user-info` prints account data (email, names, user ID, cash and billing
status); see [Secrets](#secrets).

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
- `list-volume-type-zones` prints the empty envelope columns listed under
  [Verification](#verification) until the SDK model drops them.

### loadbalancer

Short description: "Regional load balancers, listeners, pools, and
certificates".

| Command | Flags | Scope | State |
|-|-|-|-|
| `list-load-balancers` | `--name`, `--page`, `--size` | Project | Empty |
| `get-load-balancer` | `--load-balancer-id` (r) | Project | Shape |
| `list-packages` | `--zone-id` | Project | Live |
| `list-certificates` | `--name`, `--page`, `--size` | Project | Empty |
| `get-certificate` | `--certificate-id` (r) | Project | Shape |
| `list-listeners` | `--load-balancer-id` (r) | Project | Shape |
| `get-listener` | `--load-balancer-id` (r), `--listener-id` (r) | Project | Shape |
| `list-pools` | `--load-balancer-id` (r) | Project | Shape |
| `get-pool` | `--load-balancer-id` (r), `--pool-id` (r) | Project | Shape |
| `get-pool-health-monitor` | `--load-balancer-id` (r), `--pool-id` (r) | Project | Shape |
| `list-pool-members` | `--load-balancer-id` (r), `--pool-id` (r) | Project | Shape |
| `list-policies` | `--load-balancer-id` (r), `--listener-id` (r) | Project | Shape |
| `get-policy` | `--load-balancer-id` (r), `--listener-id` (r), `--policy-id` (r) | Project | Shape |
| `list-tags` | `--load-balancer-id` (r) | Project | Shape |

- A `LoadBalancer` row has 24 columns in `table`. The wiki example uses
  `--query 'Items[].{ID:UUID,Name:Name,Status:DisplayStatus}'`.
- `Nodes`, `Members`, `L7Rules`, and `InsertHeaders` are nested lists and
  render as compact JSON cells.
- The `Certificate` model has no key or PEM field, and typed models drop
  unknown fields, so a private key in a response could not reach output.

### globalloadbalancer

Short description: "Global load balancers, pools, and listeners".

| Command | Flags | Scope | State |
|-|-|-|-|
| `list-packages` | none | Global | Live |
| `list-regions` | none | Global | Live |
| `list-load-balancers` | `--name`, `--offset`, `--limit` | Global | Empty |
| `get-load-balancer` | `--load-balancer-id` (r) | Global | Shape |
| `list-pools` | `--load-balancer-id` (r) | Global | Shape |
| `list-listeners` | `--load-balancer-id` (r) | Global | Shape |
| `get-listener` | `--load-balancer-id` (r), `--listener-id` (r) | Global | Shape |
| `list-pool-members` | `--load-balancer-id` (r), `--pool-id` (r) | Global | Shape |
| `get-pool-member` | `--load-balancer-id` (r), `--pool-id` (r), `--pool-member-id` (r) | Global | Shape |
| `list-usage-histories` | `--load-balancer-id` (r), `--from`, `--to`, `--type` | Global | Shape |

- `list-load-balancers` pages by `--offset` and `--limit`, not `--page` and
  `--size`, because the API does.
- `--from`, `--to`, and `--type` pass through unchecked. Their formats and
  allowed values are unknown; the wiki says so until a live call shows
  them.
- `Package.Detail` is `any` and prints the API's object as-is.

### containerregistry

Short description: "Container registry repositories and users".

| Command | Flags | Scope | State |
|-|-|-|-|
| `list-repositories` | `--access-level` | Global | Empty |
| `list-users` | `--name`, `--page`, `--size` | Global | Empty |

`--access-level` defaults to `ALL` in the SDK; other values are unknown.
Both models are map-backed and have no live row. See [Secrets](#secrets)
for why `list-users` waits.

## Service names and pages

Group names are the package names, as [CLI](cli.md#commands) requires:
`vngcloud globalloadbalancer list-load-balancers`. No alias is added; an
alias would be a second name for one command.

`serviceTitle` in gen-docs gains three cases so the wiki pages read well:
`loadbalancer` becomes `LoadBalancer`, `globalloadbalancer` becomes
`GlobalLoadBalancer`, and `containerregistry` becomes `ContainerRegistry`.
The pages are `CLI-LoadBalancer.md` and so on. `project`, `portal`, and
`volume` use the default rule.

## Secrets

No typed Output in this design has a credential field, and typed models
drop unknown response fields, so a typed read cannot print one. The risk is
in map-backed models, which pass every key through:

| Read | Risk | CLI handling |
|-|-|-|
| `containerregistry list-users` | A registry user may carry a password or robot token; no live row exists to show the keys | Held until the SDK types `User` from a live capture with no secret field (see [decision 4](#owner-decisions)) |
| `containerregistry list-repositories` | Repository rows are unlikely to hold secrets, but no live row exists | Ships with the key redaction below |
| `portal get-user-info` | Account data: email, names, user ID, cash and billing status. Not a credential | Printed; it is the caller's own account. The wiki warns that agent transcripts keep it |
| `portal` zones and quotas | None seen in live rows | Printed |

No service here returns a kubeconfig; the SDK has no Kubernetes package.

Key redaction for map-backed Outputs: before encoding, the CLI replaces the
value of any map key whose lower-case form contains `password`, `secret`,
`token`, `credential`, or `privatekey` with `[redacted]`, at any depth. It
applies to every map-backed Output, including portal. This is a guard for
keys nobody has seen, not a substitute for a typed model. A test feeds a
map with each such key through `json`, `table`, `text`, and `--query` and
checks that no value appears.

## Live checks

Before a release ships, `make live` must pass with these additions, which
the sdk role writes in `live_test.go` and `live_cli_test.go`. Each logs
counts and field presence only, never values.

| Service | SDK check | CLI check |
|-|-|-|
| project | Covered today | `project list-projects`, one item |
| portal | `ListZones`, `ListQuotaUsed`, `GetQuota` on the first quota's name, `GetTagQuota`; `GetUserInfo` logs its key count only | `portal list-zones` |
| volume | `ListVolumeTypeZones`, `ListVolumeTypes`, `GetVolumeType` on the first type, `ListEncryptionTypes`; with a volume, `GetVolume`, `GetUnderlyingVolume`, and `ListSnapshots` on it | `volume list-volume-types` |
| loadbalancer | `ListPackages`, `ListCertificates`; with a load balancer, `GetLoadBalancer` and each child read on the first child found; with a certificate, `GetCertificate` | `loadbalancer list-packages` |
| globalloadbalancer | `ListPackages`, `ListRegions`; with a load balancer, `GetLoadBalancer`, each child read, and `ListUsageHistories` | `globalloadbalancer list-regions` |
| containerregistry | `ListRepositories` and `ListUsers`; with a row, the log lists its key names | `containerregistry list-repositories` |

Each Get check asserts the returned ID equals the ID the list gave, as the
vMonitor check does, so a Get that decodes empty fails. A check whose
resource is absent logs `skipped: none` and passes; its commands stay in
the Shape state. When a later run finds a resource, the sdk role copies a
sanitized raw capture to `testdata/` and replaces the hand-written fixture,
per [live data](../../instructions/live-data.md).

The existing `portal-user` live check logs the whole `UserInfo` with
`%+v`, which puts account data in test output. It changes to a key count in
the portal release.

## Security

- All commands are reads. `--read-only` does not affect them.
- A missing required flag stops before any request, as today.
- Key redaction and the held `list-users` keep registry secrets out of
  stdout. No command writes a file.
- No adversarial review is needed: no write, auth, or credential code
  changes. The one final review per release still runs.

## Testing

Per service, CLI tests run each command in-process against an `httptest`
server that serves the existing fixtures, and check:

- The command list matches the table in this doc, with the right flags.
- Required flags stop before any request.
- Golden `json` output for one list and one Get, and `table` for one list.
- For `project`, `--region` is the global flag, `--cli-input-json
  '{"Region":"han-1"}'` sets the field, and gen-docs marks it JSON-only.
- For map-backed Outputs, the redaction test above.

## Releases

One release per service, except `project` and `portal`, which are small and
share the account-level scope. Version numbers are assigned when each
ships, as for the vMonitor Alerts and vStorage releases.

| Release | Content |
|-|-|
| R1 | `project` and `portal` commands; the `NoFlag` read option; key redaction for map-backed Outputs; the `serviceTitle` cases for R3 to R5 |
| R2 | `volume` commands, without `get-default-volume-type` |
| R3 | `loadbalancer` commands |
| R4 | `globalloadbalancer` commands |
| R5 | `containerregistry list-repositories`; `list-users` joins when the SDK types `User` |

R1 goes first, because it changes shared CLI code (`op.go`, `flags.go`,
`encode.go`, `gendocs.go`). After R1 merges, R2 to R5 can be built in
parallel: each adds only `internal/cli/svc_<name>.go`, its test, its
generated wiki page, and its live checks. The shared edits are one
`root.AddCommand` line in `root.go`, one `buildDocService` line in
`gendocs.go`, one line in `docs/wiki/_Sidebar.md`, the generated
`docs/wiki/CLI.md` index, and a subtest each in `live_test.go` and
`live_cli_test.go`. Those are one-line merges; rerun `make gen-docs` after
each merge rather than merging `CLI.md` by hand. At most three workers run
at once, so R2 to R4 run first and R5 after.

No release needs an SDK change except R5's `list-users` and the optional
cleanup in [decision 5](#owner-decisions).

## Owner decisions

The owner approved each recommendation on 2026-09-26.

1. **Ship Shape reads, or hold them.** Most Gets and child lists for
   volume, load balancers, and global load balancers have no live row,
   because the test account has none of those resources. Recommendation:
   ship them. Their envelopes match GreenNode's official SDK, which was
   right where our fixtures were wrong for `GetVPC`, and the live Get checks
   start guarding them the day a resource exists. The wiki marks them
   unverified. The alternative, creating a volume and a load balancer in the
   test account for one live run, costs money and needs a paid-write
   approval.
2. **`NoFlag` for `project list-projects`.** Recommendation: add the
   `NoFlag` read option. The alternative is a rename-table entry such as
   `Region` to `--filter-region`, which would also rename vStorage's
   `Region` field.
3. **`get-default-volume-type`.** Recommendation: leave it out until a live
   call returns 200. It fails with `NotFound` in both regions today, and a
   command that always exits 4 misleads an agent.
4. **Registry users.** Recommendation: hold `containerregistry list-users`
   until the SDK types `User` from a live capture. That capture needs a
   registry user in the test account, which is a write; if vCR is free for
   that, approve one create and delete. Otherwise the command waits.
5. **`VolumeTypeZone` cleanup.** Recommendation: in R2, let the sdk role
   drop the seven envelope fields from `VolumeTypeZone`. That breaks SDK
   callers who read them, which is allowed before `v1.0.0` and needs a
   release note; the fields are always empty. The alternative keeps them
   and prints empty columns.
6. **Group names.** Recommendation: keep the package names
   (`globalloadbalancer`, `containerregistry`) with no aliases, as
   [CLI](cli.md#commands) requires. Short names such as `glb` and `vcr`
   would be easier to type, but would break the one-name rule.
7. **Key redaction.** Recommendation: add it in R1 for all map-backed
   Outputs. It hides a value only when a key name looks secret, so it
   costs nothing for today's rows.
