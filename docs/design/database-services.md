# Database inventory

Status: Draft (2026-10-10).

Add read-only vDB inventory to the SDK and CLI, one engine family per
release. Start with Kafka, whose published list contract has no pagination.
A family ships only when its endpoint, auth, scope, and response contract
pass the read checks below. A blocked family does not block another family.

This design follows [SDK and CLI](sdk-and-cli.md) and [CLI](cli.md).
[Database API contracts](database-services-api.md) records published models,
verified IAM console list routes, and probes still needed. All four lists
returned HTTP 200 with bearer auth in a fresh context without cookies on
2026-10-10. Every list was empty; resource models and Gets remain unverified.

## Scope

The first slices add inventory lists for Kafka, relational databases,
MemoryStore, and OpenSearch. Each resource detail read waits for a separate
later release and its own verified contract. PostgreSQL clusters belong to
the relational family when the relational list identifies them; do not
invent a separate cluster list route.

Exclude creation, purchases, resize, restart, deletion, backups, restore,
renewal, access changes, credential resets, password reveals, certificate
downloads, users, topics, query execution, and database data-plane access.
Do not add generic HTTP commands or expose raw response objects.

## Public surface

Use one public `database` package and one `database` CLI group. The four
families share the cloud product and security rules, but keep their routes,
decoders, and resource types separate. A single generic database model would
hide the different envelopes and identifiers. Four public packages would
duplicate the auth and endpoint setup without helping this inventory scope.

`database.New(cfg vngcloud.Config) *Client` shares the existing session.
Every operation uses the established context, Input, and Output signature.
The root package does not import `database` or re-export its resource types.
The SDK adds no dependency. The CLI calls only public SDK methods.

| SDK method | CLI operation |
|-|-|
| `ListKafkaClusters` | `list-kafka-clusters` |
| `ListRelationalDatabases` | `list-relational-databases` |
| `ListMemoryDatabases` | `list-memory-databases` |
| `ListOpenSearchClusters` | `list-opensearch-clusters` |

These detail methods are held for separate later releases:

| Proposed SDK method | Proposed CLI operation |
|-|-|
| `GetKafkaCluster` | `get-kafka-cluster` |
| `GetRelationalDatabase` | `get-relational-database` |
| `GetMemoryDatabase` | `get-memory-database` |
| `GetOpenSearchCluster` | `get-opensearch-cluster` |

Each method has its own `Input` and `Output` types. Kafka list input is empty
and accepts nil. Each Get input requires `ID string`, exposed as `--id`.
Validate IDs with `core.CheckPathID` before discovery or HTTP. Get outputs
hold `Cluster` or `Database`; list outputs hold `Items`.

Kafka lists have no invented page metadata. Relational and MemoryStore lists
use `Page` and `Size`. Both returned empty inventories for page 1 and size
100 with bearer auth in a fresh context without cookies. MemoryStore also
accepted size 500. Propose SDK defaults of page 1 and size 100 for both.
These are SDK defaults, not claims about omitted-query API defaults.
Zero uses the SDK default; reject negative values before HTTP. Do not infer
a hard cap from reported `maxSize`, or claim multi-page behavior is verified.
OpenSearch list inputs remain undecided until its paging query is verified.
Preserve real metadata and do not silently auto-page.

The public resource types are `KafkaCluster`, `RelationalDatabase`,
`MemoryDatabase`, and `OpenSearchCluster`. The API companion defines each
field allowlist. Resource fields keep API JSON tags; Input and Output fields
have no JSON tags. Use typed nested structs, never `map[string]any`, `any`,
`json.RawMessage`, or an exported raw-body field.

All commands use `Read`, work with `--read-only`, and support normal JSON,
table, text, and JMESPath output. Help states the actual account, project,
and region scope. A global `--project-id` or `--region` must not imply a
filter the API does not apply. Do not expose a family command until that
family's auth and scoping contract is settled.

## Endpoint and identity

Use the existing IAM credential provider. Do not introduce vDB passwords,
an API key, cookies, a second login, or service-account login. All four
observed console list routes accept `Authorization: Bearer` without a
`portal-user-id` header. The proof used a fresh isolated browser context
without cookies. The vendor SDK's extra header is unnecessary on these
verified console routes. Public service-account API auth remains unverified.

Add `Endpoints.Database` with default
`https://vdb.console.greennode.ai/`. The API companion gives exact list
prefixes; Kafka, relational, and MemoryStore add `/vdb/` before the public
gateway path. OpenSearch already has that prefix in the vendor source.
Do not follow cross-host credential redirects or try alternate hosts after
an auth failure. Tests override discovery and resource endpoints.

Do not add a portal user ID setting, flag, header, or discovery call.
OpenSearch uses the selected project ID in its path. The console reads
the project mapping from `/iam-vserver-gateway/v1/projects`, which returns
`projects` with `projectId` and `userId`. Only the project ID is needed.
Use the existing selected-project rules and fail on ambiguous discovery.
Do not parse token claims or choose the first project silently.

The observed Kafka, relational, and MemoryStore list requests contain no
project or region selector. That proves their request shape, not the full
scope of returned resources. OpenSearch embeds a project ID. The probes
cover only the HCM/current-region session; cross-region behavior and
completeness remain unverified. Do not invent regional paths or claim that
the global `--region` or `--project-id` filters an unscoped list.

## Response and error safety

Default models omit database passwords, credential-bearing connection URLs,
keys, certificates, tokens, credentials, arbitrary configuration values,
raw tags, and free-form server error messages. Unknown JSON fields are
discarded. There is no secret reveal switch in this design.

Mark every vDB resource request `Sensitive: true`. That suppresses raw
capture and decode-error bodies even when an upstream unexpectedly adds a
credential field. Keep debug logging to the existing method, path, status,
and duration fields. Never log request headers or response bodies.

The current transport's `WithholdMessage` leaves `APIError.Code` intact.
It is insufficient for a vDB response that puts a secret in `code`. The
vDB request wrapper must return a fresh error with a status-derived code
and fixed message for every server failure. Preserve operation, HTTP status,
retryability, and the matching SDK sentinel. Never wrap the original error
if any public field or cause can contain server response text. A known
context cancellation or deadline keeps `errors.Is` behavior.

Use the existing status mapping for 400, 401, 403, 404, 409, 429, and 5xx.
Use fixed messages such as `database request failed; response withheld`.
A malformed success body returns `InvalidResponse` with fixed text and no
decoder cause. A resource object without its required ID fails decoding.
An object in place of a list, missing required envelope fields, a null
required payload, or an HTML success response also fails. An empty array
is a valid empty inventory.

For relational and MemoryStore, require envelope `code: 200` before decoding
`data`, matching both observed empty-list responses. HTTP 200 alone does
not prove success. Do not preserve the envelope's raw code or message in
public output. A different envelope code is an API failure with fixed
`InvalidResponse` code and withheld text until its semantics are verified.

Only HTTP 200 is a known modeled read success. The spec also names 202 and
204 without bodies; reject those as unexpected until a probe establishes a
meaningful read result. Existing GET retries and token invalidation apply.
Do not add fallback hosts, broaden redirects, or disable TLS verification.

## Fixtures and live checks

Use raw responses as the fixture source, never marshaled SDK output.
Ordinary capture remains disabled for vDB. The manager may save a private
discovery response under ignored `examples/basic/output/raw/database/`
with the original body under `body`, following the live-data rules. The
sanitizer reads that file locally, replaces sensitive values, and writes a
fixture for review. No live content enters chat, CI, or Git.

Replace IDs, names, project and account fields, addresses, hostnames, URLs,
and secret values. Keep unknown and omitted fields in sanitized fixtures
so decode tests prove their exclusion. Add synthetic secret canaries under
password, URL, certificate, token, unknown-field, and error-code keys.
Never collect a credential download to make such a fixture.

The example calls every shipped list. A later detail release adds its Get
only for an existing listed resource. It writes the safe model under ignored
`examples/basic/output/sdk/database/`, and prints counts and field presence.
It never creates a resource to fill a fixture gap.

The live gate is a manager-run, read-only SDK and CLI check with `.env`
resolved inside the process. Check the observed origin, auth, scope, list
envelope, empty-list behavior, and every paging parameter shipped. For a
later detail release, assert the detail ID equals the list ID without
logging it. Check each family separately and record which operations have
live data.

An empty list can validate an endpoint and envelope but cannot validate
populated resource fields or a Get. The owner may accept published schema
evidence for resource fields only. That acceptance cannot waive verification
of an exact route, bearer-only IAM replay without cookies, success envelope,
ID mapping, scope, or safe error handling. The wiki must say which resource
fields lack live evidence. A 401, 403, HTML response, or unknown scope does
not satisfy even the empty-list gate.

Hold each Get until its exact console route, bearer-only IAM auth, success
envelope, list-to-detail ID mapping, and safe error behavior are verified.
Its resource fields need live evidence or the explicit field-only acceptance
above. A source-backed suffix combined with a verified list prefix does not
verify a detail route. Release each Get separately after those gates pass.

## Test matrix

| Area | Required evidence |
|-|-|
| Routes | Exact method, origin override, path, escaping, and query |
| Auth | Bearer only; no cookies or portal-user header |
| Scope | Selected OpenSearch project; ambiguous discovery stops |
| Input | Nil list input; nil Get and invalid ID stop before HTTP |
| Lists | Sanitized raw row, empty array, strict envelope, real metadata |
| Detail | Sanitized raw object, required ID, list/detail ID agreement |
| Paging | Page base, default, size limit, second page, exhausted page |
| Errors | 400, 401, 403, 404, 409, 429, 500, unexpected 202 and 204 |
| Decode | HTML, malformed JSON, wrong types, null and missing payload |
| Secrets | No canary in output, errors, causes, logs, or capture hooks |
| Retries | Existing GET retry and auth-refresh behavior stays intact |
| CLI | Registration, required flags, formats, query, read-only mode |

Inspect `APIError.Code`, `Message`, `Err`, `Error()`, and unwrap chains in
secret tests. Put server-derived canaries in both code and message, not
only password fields. Check JSON, table, text, JMESPath output, and debug
stderr. Verify a later unrelated request still uses its normal capture
behavior. No unit test calls a real API.

Run `make check` for each implementation and before every commit. Update
the matching SDK wiki page and generated CLI reference in the same release.
An independent review checks the service contract and the response-secret
boundary. The exact release commit needs green GitHub CI.

## Ownership and release order

The architect owns this design and its API companion and never implements
the slice. The manager owns coordination, integration, Git, and releases.
At most three workers run at once, with disjoint files.

The SDK worker owns `database/`, `testdata/database/`, the matching SDK wiki
page, and dedicated database example and live-test files. The first SDK
brief also owns the needed endpoint, route, and core endpoint-dispatch
edits. No root re-export is needed for service models.

The CLI worker owns `internal/cli/svc_database*.go`, its tests, and
`docs/wiki/CLI-Database.md`. It starts after the public SDK types are fixed.
The manager assigns `root.go`, `gendocs.go`, example registration, wiki
indexes, and any shared live-test registry to exactly one role per release.
Shared SDK infrastructure remains SDK-owned; shared CLI files remain
CLI-owned. The manager does not implement either role's code.

Use separate family files such as `kafka.go`, `relational.go`, `memory.go`,
and `opensearch.go`, each with models and tests split as needed. Share only
endpoint selection, verified identity discovery, and safe error handling.
Do not share a permissive decoder across unlike response envelopes.

| Release | Feature | Contract still needed |
|-|-|-|
| First | Kafka cluster list | Populated shape and scope |
| Second | Relational database list | Paging and populated shape |
| Third | MemoryStore database list | Paging and populated shape |
| Fourth | OpenSearch cluster list | Paging and populated shape |

Each Get is a separate later release after its detail gates pass. None is
part of these first four list slices.

Version numbers are assigned when each feature ships. If Kafka is blocked
and another family's contract is fully verified, that family may ship
first with the common foundation. Do not batch engine families into one
tag. Do not extend a release to unrelated catalogs, backups, users, or
write operations.

## Decisions still open

IAM console list routes, bearer-only auth, and empty envelopes are verified.
Populated resource fields and every Get remain source-backed proposals.
Paging semantics and scope beyond the HCM/current-region session still need
evidence as detailed in the API companion. The design remains a draft;
implementation has not started. The owner must approve the design and any
source-only resource-field exception before code starts. Such an exception
never waives the detail route, auth, envelope, ID, or error gates.
