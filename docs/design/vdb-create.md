# vDB relational instance create

Status: Accepted with the owner decisions below.

Ship one lifecycle for a private, single-node MySQL 8.0 instance in `hcm-3`,
funded only with existing regular GreenNode credits. Create one named logical
database and one caller-supplied master user on an existing, eligible VPC
and subnet. Quote, create, list, get, and delete are one release. No other
engine, cluster, replica, public IP, access-rule write, backup write, renewal,
provisioning of network resources, or database data-plane operation.

## Owner decisions

1. Start with community MySQL 8.0 in HCM, private access and automatic backup
   off. MySQL has a matching upstream relational example; PostgreSQL cluster
   uses a separate create route, and the observed HCM PostgreSQL console
   price did not distinguish single-node from clustered service. Require the
   HCM catalog to confirm a MySQL offering rather than switch engines silently.
2. Use one `database` package and CLI group. Use instance names for the
   relational operations below and ship them together instead of waiting for
   the unrelated Kafka inventory. This supersedes only the proposed relational
   names and order in the [inventory draft][inventory] if accepted.
3. Prefer a proven quote with a required cap and `--yes`. If the pricing
   candidate cannot quote the same create, use `--yes` without a quote only
   under an explicit, slice-specific [ADR 0002][writes] exception like
   [GLB][glb]. Do not make it a runtime fallback or present a catalog rate as
   a quote. Keep create blocked without the exception, and keep funding and
   initial-charge evidence gates in either case.
4. Use IAM Auto Payment with explicit `poc: false`. Do not expose a funding
   selector. Delete only one owned, standalone, backup-free instance, with no
   final backup or backup purge. Prefer an explicit terminal detail result to
   inferring deletion from an empty body.
5. Accepted by the owner's delegate: implement now; the manager's live
   create-then-delete run in `hcm-3` is the release gate, and its debit and
   refund evidence settle the funding questions below. No per-call account
   payment-mode check exists; IAM Auto Payment with `poc: false` is the rule.
6. Reject any returned ID or field that equals a sent password or bearer
   before using it in a path, guard, or output.
7. Delete refuses when the preflight read shows backups or replicas. A
   dependency created between that read and the delete is an accepted race,
   as for other deletes. The live run deletes through the console if the API
   cannot.
8. Create succeeds only on `ACTIVE` with matching engine, version, flavor,
   and automatic backup off. A POST that may have reached the service without
   a confirmed ID returns `NotSettled` and is never resent.
9. If the price route cannot quote the same create, use `--yes` without a
   quote as a vDB-specific ADR 0002 exception, as for GLB and VKS.
10. Where the sections below are stricter, these decisions win.

A read-only release is the fallback if safety evidence is incomplete; it is
not a substitute for the lifecycle. A generic database/order client or a
Terraform dependency adds unverified auth, routes, and payloads. Use the
existing session and transport without adding dependencies.

## Routes and evidence

The [public OpenAPI][api] declares IAM bearer auth and an old gateway URL
with a malformed scheme. The [Terraform provider][tf-provider] uses
`https://vdb-gateway.vngcloud.vn` with a client-credentials client, not our
IAM login. The [vendor Go SDK][go-sdk] has no relational lifecycle client.
The [inventory evidence][inventory-api] verifies only the empty HCM console
list with an IAM bearer, no cookies or `portal-user-id`. Use the
proposed `Endpoints.Database` at `https://vdb.console.greennode.ai/` and the
observed `/vdb` prefix for inventory. Confirm each other route on the exact
first-party origin and prefix through manager evidence. Do not infer that the
other paths work on the console, probe guessed hosts with a bearer, or fall
back to the old gateway on failure.

Paths here are relative to the public prefix `/vdb-relational/v1`:

| Operation | Method and relative path | Published HTTP 200 payload |
|---|---|---|
| Create | `POST /payment/database-instances` | `data[]` of orders |
| List | `GET /database-instances` | `data` with rows and page metadata |
| Get | `GET /database-instances/id/{dbInstanceId}` | `data` database detail |
| Delete | `POST /database-instances/{instanceId}/delete` | `data[]` of action results |

The modeled responses use `code`, `message`, and `data`. Only the list's
`code: 200` is live-verified. OpenAPI names HTTP 202 and 204 without response
schemas; they do not establish acceptance or supply an ID. Pin exact HTTP
statuses, envelope codes, and required fields from evidence before shipping.
Neither the public reference nor the inventory list proves project/region
isolation or IAM mutation permission. Pin hcm-3 placement and verify how
returned scope joins to the independently resolved selected project. Refuse
ambiguous discovery and unsupported auth or endpoints before sending a write.
Global `--project-id` and `--region` do not filter an unscoped list request.

## Public SDK and CLI

Use `database.New(cfg vngcloud.Config) *Client` with Context, pointer Input
and Output, no root re-export, and no existing API changes. CLI commands
under `vngcloud database` call the public methods:

| SDK | CLI |
|---|---|
| `QuoteCreateDatabaseInstance` | `quote-create-database-instance` |
| `CreateDatabaseInstance` | `create-database-instance` |
| `ListDatabaseInstances` | `list-database-instances` |
| `GetDatabaseInstance` | `get-database-instance --id <id>` |
| `DeleteDatabaseInstance` | `delete-database-instance --id <id> --yes` |

Quote, catalog, list, and get are reads and work with `--read-only`. Both
writes require `--yes` in CLI and fail in read-only mode before reading a
password or requesting a token. Use normal flag and non-secret JSON merge,
JSON/table/text output, JMESPath query, and [CLI exit rules][cli]. No
`--no-wait`, name-based delete, force, or bulk operation. Native SDK mutation
calls express intent; the price cap still applies to SDK create when shipped.
If the owner approves unquoted creation, omit quote and `MaxPrice` from that
release rather than accept a cap the library cannot check.

List sends explicit `pageNumber` and `pageSize`, defaults 1 and 100, and no
unverified `filterRequest`. Return one page and verified metadata, not an
invented total or silent auto-paging. Do not treat `maxSize` as a proven cap.
Use small, typed projections of exact IDs, name, status, engine/version,
zone/project/subnet and non-secret storage fields that populated fixtures
verify. Keep list and Get wire types separate. Do not add arbitrary metadata,
address objects, usernames, or connection strings. Get must match the exact
requested ID; confirming the resource's control-plane configuration does not
establish data-plane reachability or validate the password.

## Create inputs and eligible placement

| Input / CLI flag | Candidate request mapping |
|---|---|
| `Name` / `--name` | Instance `name`, not the logical database name |
| `Engine` / `--engine`, `EngineVersion` / `--engine-version` | `datastoreType`, `datastoreVersion` as strings |
| `FlavorID` / `--flavor-id` | `packageId` string, not assumed to be the catalog's `flavorId` |
| `StorageType` / `--storage-type`, `StorageSizeGB` / `--storage-size-gb` | `volumeType` string and positive int32 `volumeSize` in GB |
| `ZoneID` / `--zone-id` | `locateZoneId`, not a vServer region name or assumed zone UUID |
| `VPCID` / `--vpc-id`, `SubnetID` / `--subnet-id` | Validate both; send exactly one subnet ID in `netIds`, no VPC key |
| `DatabaseName` / `--database-name` | Exactly one `databases[].name`; same name in `user.databases` if required |
| `AdminUsername` / `--admin-username` | `user.name` |
| `Password vngcloud.Secret` / `--password-file <path>` | `user.password`, create request only |
| `MaxPrice` / `--max-price` | Local required positive cap if quoted; no guessed create JSON field |

Require explicit selections, validate missing fields, safe path IDs and
wire representability before HTTP. Validate compatibility against the scoped
catalogs, not hard-coded size ranges or current provider name/password rules.
Send `publicAccess: false`, `backupAuto: false`, and `poc: false` explicitly.
Omit config groups, backup schedule and replication. OpenAPI gives
`utf8mb4`/`utf8mb4_general_ci` examples; [Terraform][tf-resource] sends
`utf8`/`utf8_general_ci` and repeats the logical database under the user.
Verify omission or pin the required MySQL defaults and user grant shape; do
not copy a sample blindly. Prove private access and backup/renewal defaults
at provisioning, not by a second write. [Terraform's example][tf-example]
warns of a default `0.0.0.0/0` rule without an allowlist. Verify the default
rule stays inside the permitted private network boundary; the slice neither
promises a restricted security group nor makes a later access-rule mutation.

Accept `--password-file -` for non-interactive stdin. Follow the existing
[IAM password input][password] semantics: bounded read, private regular file
(mode 0600 or stricter on Unix), no terminal or blocking special-file path,
and one trailing LF or CRLF stripped. Reject empty, non-UTF-8, and control
characters without echoing input or the path. Exclude the password from argv,
CLI JSON and skeletons, prompts, env-based options, quote calls, SDK outputs,
logs, debug output, errors, and captures. Validate non-secret CLI inputs and
conflicts before opening the secret source. Quote takes the same create Input
but never requires or reads a password; its CLI has no password-file flag.

### Catalogs

All paths below are `GET` under `/vdb-relational/v1/database-instances`.
Expose only the read projections needed to choose the first slice's IDs.

| Candidate SDK read / CLI name | Path and selection evidence |
|---|---|
| `ListDatabaseEngineVersions` / `list-database-engine-versions` | `/datastore`: type, version, names and license; verify `/engine` names/licenses, expose separately only if needed |
| `ListDatabaseZones` / `list-database-zones` | `/zones`: UUID, name and status; prove HCM region-to-zone and request ID mapping |
| `ListDatabaseFlavors` / `list-database-flavors` | `/flavors?type=…&version=…&zoneId=…`: confirm `id` to `packageId` and SKU mapping |
| `ListDatabaseStorageTypes` / `list-database-storage-types` | `/volume/types?zoneId=…`: confirm type token, size bounds, SKU, scope |
| `ListDatabaseNetworks` / `list-database-networks` | `/networks/subnets?zoneId=…`: nested VPC/subnet, zone and status; use `/networks` only if needed to prove the VPC ID join |

Normalize once per invocation. Require a unique supported version, eligible
flavor and storage that fit the selected zone/size, and a subnet that the vDB
catalog associates with the chosen VPC in the selected project and zone.
Do not select the first row, use API defaults for zone, confuse integer IDs,
UUIDs and display labels, or substitute a general VPC list for vDB eligibility.
Missing or ambiguous joins fail closed. Catalog rates are not quote totals.

## Payment and quote

OpenAPI says create's `user-type: IAM_USER` selects Auto Payment;
`ROOT_USER` selects checkout and is the default if omitted. `poc: true`
selects PoC (proof of concept) credit for Auto Payment. Send the IAM header
and `poc: false` every time. Terraform's relational create uses the same
order route and reads `resourceId`, but passes no `user-type` option. It is
not evidence that the IAM mode debits only existing regular credits. Verify
that the account is prepaid, the full initial debit or hold, when it settles,
its renewal behavior, and that low credit cannot trigger checkout, a card,
PoC or postpaid fallback. Never follow or print `orderUrl`; an unexpected
checkout response leaves the result unconfirmed. Never top up credits.

The embedded public OpenAPI has no quote. The generated [Terraform pricing
client][tf-price] has `POST /v1/price` at its configured vDB base, with
required string `resourceType` and `action` and optional opaque `resourceInfo`.
The response modeled by the client has integer `optimumPrice`,
`originalPrice`, `discountPrice`, a discount percent and `propertiesPrice`
with monthly and optimum values. The relational resource does not call it.
This is a candidate, not a verified relational quote. Confirm the IAM
console route and auth, that it is side-effect free, the action and resource
type literals, and the exact projection of flavor, storage, size, term, zone,
and funding mode. Do not guess the `resourceInfo` schema from its `interface{}`
field or from a delete's `resType: dbaas`. Identify the full positive initial
amount including tax/required charges, its unit/currency and term, whether it
is a debit or hold, and whether create binds the quoted price. Flavor
`monthlyCost`, instance `cost`/`period`, and the console display alone do not
establish those facts. [vDB billing][billing] documents renewals and PoC;
the [general hold guide][hold] does not list vDB. Neither proves refund,
term, or hold rules for this request.

Use one private normalization and request builder for quote and create.
Only the mutation marshals the password-bearing wire body. Quote projects
only proven pricing fields and returns the initial amount, unit/currency,
term and charge basis, not a debit receipt. Compare a fresh quote
immediately before create against the explicit `MaxPrice` in its exact
unit; accept equality. Use `int64` only if confirmed whole units. No float
comparison, rounding, or stale quotes. Reject missing, zero, malformed,
incomplete, wrong-unit or above-cap pricing with `ErrUnpriced` or
`ErrPriceAboveMax` as applicable.
Prefer a server price bind if proven. Without one the cap is local to the
observed quote, not a hard spend ceiling: price and credits can change before
acceptance, and later charges are outside the cap. Owner acceptance of that
race is required. In the separately approved no-quote variant, help must say
no cap is checked, describe the verified funding and charge basis, and warn
that deletion does not promise a refund. A flaky quote never switches modes.

## Send, wait, and recover

Both mutations use `Once` and `NoRedirect`. Send no retry on dial failure,
401, 429, 5xx, redirects, or lost/malformed replies. One invocation is not
cross-process idempotency. A name, empty list, or unchanged balance is not
proof that no order was placed. Reads use the existing bounded GET policy;
only a proven side-effect-free quote POST is explicitly idempotent. Use
verified first-party IAM and vDB endpoints, normal TLS, no arbitrary override
for the write workflow, no automatic redirects, and no server-supplied URLs.

- **Create.** Accept only the verified success envelope with exactly one
  path-safe `resourceId`; it is not `orderId`, `dbBackendId`, or a name.
  Hold the validated ID for recovery. Poll exact Get in the same identity
  and scope. Confirm `ACTIVE` and match ID, instance name, engine/version,
  zone, project, subnet, storage, and private/standalone configuration.
  Verify a flavor identity join to the selected catalog; matching CPU/RAM
  alone does not prove the flavor. The [console guide][console] says Build or
  Building then Active; [Terraform states][tf-states] suggest `BUILD` or
  `BUILDING` then `ACTIVE`.
  Wait only on evidenced transitions, including early NotFound only if its
  bounded visibility window is proven. Stop on a proven terminal failure
  or unrecognized state. No name lookup retry, guessed ID or automatic delete.
- **Delete preflight.** Get the exact ID immediately before the write.
  Require matching ID, supported engine/shape, verified ownership and project,
  proven HCM zone, stable `ACTIVE`, and no replication or backups. Treat
  `owner`, `role`, `replicaSourceId`, `replicas`, and `deleted` as candidate
  evidence, not an implicit authorization when missing. Verify the backup
  guard with a bounded authoritative read (candidate
  `GET /vdb-relational/v1/backups/insId/{instanceId}`), or equivalent evidence
  that absent backups and dependent resources are proven. Do not treat a
  partial list as empty. Missing, shared, unknown or changed scope, 403, and
  404 at preflight never send. No child or storage deletion.
- **Delete request and wait.** Verify a body with `action: "delete"`,
  `resType: "dbaas"`, and one `databaseInstances[].instancesId` identical to
  the path. Require verified no-final-backup/no-purge semantics, preferably
  explicit false for `createFinalBackup` and `deleteAllBackup`; Terraform
  omits this config, which does not establish its defaults. Validate the
  single action row, action, matching ID and success before polling. Confirm
  exact Get reports authoritative `DELETED`, or the service's distinct,
  verified absence behavior in the same scope. Terraform treats a bare 404
  or nil data as deleted; do not adopt that heuristic. No success for 2xx
  alone, a malformed response, changed scope, or an authorization error.

Propose an immediate Get then every 10 seconds for up to 30 minutes per
operation, within the caller's earlier context deadline. Test with a fake
clock. On timeout, cancel, unexpected status or unsafe read-back after the
write, return `NotSettled` with a safe partial Output containing only the
validated ID and no false success or `Deleted: true`; preserve known terminal
failure as `WriteFailed`, also with the ID. CLI prints safe partial Output
under its existing exit rules. If acceptance is ambiguous and no trusted ID
was returned, do not emit a resource ID. Instruct the operator to reconcile
read-only inventory and billing before any escalation; never send another
POST or delete by a name match.

## Response security and tests

Mark vDB quote, catalog, guard, read and write requests sensitive, including
bootstrap reads in these flows. Use the existing transport response bound.
Suppress captures and untrusted text on both success and failure. Allowlist
only typed fields used by output or guards. Reject wrong types, malformed or
missing payloads, and duplicate, case-variant, or conflicting guard keys.
Keep status/codes, HTTP classification and context cancellation semantics via
fresh safe errors. Never preserve provider `code`, `message`, `errorMsg`,
headers, decoder causes, response fragments, or secrets in the unwrap chain;
`WithholdMessage` alone only replaces the message. Use fixed local text and
`InvalidResponse` for malformed success. Debug shows operation and timing;
never include a credential, order URL, query token, or untrusted value.

| Test | Required evidence |
|---|---|
| Wire and preflights | Exact routes, queries and bodies; IAM header on create; explicit false fields; invalid, wrong-scope, shared, ambiguous and backup-bearing input sends no mutation |
| Quote | Same normalized choices, no password, matching/failing caps, wrong currency/term, stale or unpriced result never writes |
| Send once | Zero replays after redirect, dial loss, cancellation, 401/429/5xx, or malformed acceptance; no wrong-host credential transmission |
| Read and wait | Populated sanitized list and Get, genuine empty page and metadata, exact ID and configuration checks, pending/terminal/unknown states, truncation, auth loss, timeout and vetted absence |
| Secrets | Put canaries in nested success/error fields, codes and headers; assert no leaks across outputs, errors/unwrap, debug or captures; test bounded private files/stdin and pre-input gates |
| CLI | Registry, help, format/query, exit and partial output; no password in skeletons or quote; no deletion before exact-ID preflight |

Use loopback or synthetic tests, then sanitized raw wire fixtures after the
manager proves the contracts. Add safe example reads and generated CLI and
SDK wiki docs in the same release. Require independent design review before
implementation, adversarial code review, `make check`, and green exact-commit
CI. Keep other engine families independently gated.

## Manager live release gate

The manager alone runs the approved paid probe under [live-data rules][live]
in `hcm-3`. Scope is one prepaid IAM project, one eligible disposable VPC and
subnet, one private MySQL instance, an agreed initial-spend limit, and
exact-ID cleanup. Confirm those approvals and necessities before the paid
send. Do not buy credits, touch unrelated resources, or run a second create
just to obtain another fixture.

1. Verify IAM-only routes, project/zone mapping and eligibility with reads.
   Confirm the price candidate from public or actual console evidence before
   calling it. Price the smallest eligible MySQL 8.0 selection and settle the
   quote or explicit exception, funding/no-fallback and maximum spend before
   any paid call. Record available, held and PoC balances and credit history
   privately via [billing reads][balances]; avoid concurrent paid activity.
2. Create once with a unique synthetic name and private password input. Keep
   only the sanitized wire shape and validated resource/order linkage in
   private evidence. Check exact-ID transitions and final type, placement,
   privacy, no backups, renewal and scope. Verify populated List/Get and
   actual pagination for whatever ships. Reconcile initial balance and ledger
   against the quote or known charge; distinguish a hold from a debit.
3. Delete that exact instance once after preflight. Verify terminal state or
   authoritative absence, no backups, no parent attachment and no unintended
   resources. Do not remove the VPC/subnet until the child is gone. If any
   write or read is unclear, reconcile without replay; escalate leftovers
   privately for authorized operator cleanup instead of deleting a parent.
4. Recheck balances/credit history after deletion and settlement. Record any
   refund, released hold, retained charge, or delayed result without promising
   one. Unexplained movements, wrong funding, unproven cleanup or missing
   required wire evidence block release. Keep account/ID/secret values out of
   chat, Git, wiki and CI; report only sanitized shapes and conclusions.

[api]: https://docs.api.greennode.ai/service-docs/vdb-api.html
[go-sdk]: https://github.com/vngcloud/vngcloud-go-sdk/tree/a76d5fc0fdbd31663c51a7ba5cb71c15b3604188/vngcloud
[console]: https://docs.greennode.ai/vdb/relational-database-service-rds/getting-started/khoi-tao-rds-instance.md
[billing]: https://docs.greennode.ai/vdb/billing-information.md
[hold]: https://docs.greennode.ai/billing-management/experience-with-billing-and-payment/payment/credit-hold.md
[tf-provider]: https://github.com/vngcloud/terraform-provider-vngcloud/blob/230bd7d346853b2aba33a9ec06f8c544b06fa8a2/provider/provider.go
[tf-resource]: https://github.com/vngcloud/terraform-provider-vngcloud/blob/230bd7d346853b2aba33a9ec06f8c544b06fa8a2/resource/vdbv2/resource_relational_database.go
[tf-example]: https://github.com/vngcloud/terraform-provider-vngcloud/blob/230bd7d346853b2aba33a9ec06f8c544b06fa8a2/docs/resources/vdb_relational_database.md
[tf-states]: https://github.com/vngcloud/terraform-provider-vngcloud/blob/230bd7d346853b2aba33a9ec06f8c544b06fa8a2/resource/vdbv2/config.go
[tf-price]: https://github.com/vngcloud/terraform-provider-vngcloud/blob/230bd7d346853b2aba33a9ec06f8c544b06fa8a2/client/vdbv2/api_pricing_controller.go
[inventory]: database-services.md
[inventory-api]: database-services-api.md
[glb]: glb-create.md#owner-decisions
[writes]: ../adr/0002-write-api-conventions.md
[cli]: cli.md#errors-and-exit-codes
[password]: iam-users.md#credentials
[live]: ../../instructions/live-data.md
[balances]: billing.md#balances
