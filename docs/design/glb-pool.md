# Global Load Balancer TCP children

Status: Accepted with the owner decisions below. Wire values marked
candidate and the child waiter states are frozen from the live probe's
record before landing.

## Owner decisions

1. Add one TCP pool with one private member group containing one vServer
   backend, then one TCP listener bound to that pool, on an existing Layer 4
   Global Load Balancer (GLB). Start with `hcm-3` only. Keep the shipped bare
   GLB create/delete and all existing reads unchanged.
2. Require explicit canonical IPv4 listener prefixes of `/8` or longer.
   Resolve the backend address from an exact server, project, VPC and subnet.
   Do not accept a raw address or relax either guard when evidence is missing.
3. Require `--yes` for both creates and all deletes. Accept HTTP 200 or 202
   for the supported writes, but return success only after scoped read-back.
   Clean up listener, then pool; owner decision 8 covers the pool's group.
4. Child creates ship without a quote, under an [ADR 0002][writes] rule 8
   exception that this decision extends from the bare GLB to its listener and
   pool. No quote route exists and the child requests carry no price, SKU or
   payment field. GreenNode bills child usage under the account's terms; the
   SDK neither selects nor verifies the payment source, and help says so.
   `--yes` records consent. Before the probe's first child POST the manager
   confirms that the account is funded by credits only, as the owner
   authorized; if that cannot be confirmed, the probe stops before the POST.
   The probe records cash and available balance privately immediately before
   and after each child POST and DELETE. Help and wiki name only a debit or
   hold attributable to a child resource, say "undetermined" when none can be
   attributed, never "free", and state that deletion promises no refund.
5. Ownership follows the shipped bare GLB delete: SDK-owned IAM-user login,
   default auth, GLB and vServer endpoints, and an exact scoped GLB detail
   read under that token. `BackendProjectID` must equal the client's resolved
   project (`RequireProjectID`), whose discovery runs only on the default
   vServer endpoint. That equality checks configuration consistency only;
   access is proven by the exact server, VPC and subnet GETs under that
   project with the same IAM client. No `userId` or account-ID join is used.
6. The listener's CIDR set is verified by exact read-back. Enforcement is the
   provider's contract. The live probe must show that the GLB accepts a TCP
   connection from one allowed `/32` source; backend reachability from GLB
   health sources is the operator's network concern and gates nothing, as
   does a denied-source check.
7. The live probe is approved under the standing create-then-delete
   authorization: `hcm-3`, the default profile, one disposable bare GLB on the
   smallest package, one smallest private vServer with a disposable TCP
   responder, and run-owned network resources, all deleted by recorded ID.
   Candidate wire values (health `3,3,30,5`, timeouts `50,5,50`, dial `100`,
   weight `1`) are confirmed or corrected from that probe. An unexplained 4xx
   is resolved by a console walk with writes blocked, not by guessing.

8. The API refuses to delete a pool's sole member group (HTTP 403), and a
   pool DELETE removes the pool with its nested group and backend row, never
   the vServer. No `DeletePoolMember` ships. `DeletePool` deletes a
   listener-free pool together with its one expected group. Groups live
   under the pool path, so once the complete pool collection omits the
   pool, a 404 or HTTP 500 `internal_error` from that pool's group list is
   accepted as the group's absence; while the pool is listed, no group-list
   error counts.

## Scope and alternatives

The [bare GLB owner decisions][parent] take precedence over the bundled
proposal in that design. The child slice uses two independent POSTs. Create
and confirm the pool first, then send its ID as the listener's `globalPoolId`.
The read-back chain is listener → pool → member, not the write order. A pool
without a listener can still run health checks but does not expose a listener.

- **Use a pool with its members in one POST.** The API accepts nested members
  and a listener can name an existing default pool. This avoids a temporary
  unbound listener, a separate member POST, and an attachment update.
- **Listener first or an empty pool plus member POST.** A listener-first run
  would need a later PUT or undocumented auto-binding. A member POST adds an
  unneeded write and has weaker upstream evidence. Neither is in this slice.
- **Change the top-level create or orchestrate multiple writes in one
  command.** Neither configures an arbitrary existing bare GLB with separate
  consent and recovery at each step. Leave the shipped parent contract alone.

No other backend regions, multiple listeners or pools on one GLB, public
backends, Layer 7, UDP, TLS, PROXY, update, bulk mutation, auto-cleanup, or
writes to payment, DNS, compute, VPCs, subnets or firewalls in this feature.
Separately approved probe setup and teardown may use existing commands.

## SDK and CLI

Add methods to `globalloadbalancer.Client` using `context.Context`, pointer
Input, and pointer Output. Commands run under `vngcloud globalloadbalancer`.
All listed input fields are required. Names are nonblank; provider naming
limits stay on the server. Ports are integers from 1 through 65535. Validate
all input and provider-derived path IDs with `^[A-Za-z0-9-]+$` before use.

|SDK method / CLI command|Input fields beyond `LoadBalancerID`|Confirmed output|
|-|-|-|
|`CreatePool` / `create-pool`|`Name`, `MemberName`, `BackendProjectID`, `BackendServerID`, `BackendVPCID`, `BackendSubnetID`, `BackendPort`|`Pool`, `PoolMember` (the group and its one backend)|
|`CreateListener` / `create-listener`|`Name`, `PoolID`, `Port`, `AllowedCIDRs []string`, `BackendProjectID`, `BackendServerID`|`Listener`|
|`DeleteListener` / `delete-listener`|`ListenerID`|GLB and listener IDs, `Deleted`|
|`DeletePool` / `delete-pool`|`PoolID`|GLB and pool IDs, `Deleted`|

Use kebab-case flags (`--backend-server-id`, `--backend-vpc-id`,
`--backend-subnet-id`, `--allowed-cidrs`), standard list flag parsing and
`--cli-input-json`. Bind `BackendProjectID` to the existing global
`--project-id` with `WriteGlobalProjectID`; no separate project flag or
implicit discovery. SDK callers supply that field directly. Use
`Config.Region` (`--region` in CLI) for the backend lookup only. Neither
project nor region goes into the GLB route. Disallow writes in read-only
mode, check `--yes` before network access, and use normal [CLI exits][cli].
Help calls out the order, CIDR scope, possible GLB usage charges, and no
refund guarantee. The pool delete removes the pool with its group and
backend row, never the vServer itself.

Add no dependency. Use private guard projections of existing reads, plus
one private exact pool GET. Do not widen existing reads' models, requests,
permissions, output, or CLI flags. No `--force`, `--no-wait`, raw request,
provider endpoint, or placement override.

## Wire contract and evidence

The [OpenAPI][openapi] declares the methods below with HTTP 200 and no
response schemas. The [pinned SDK operations][up-ops] expect 202 for the
listed writes and 200 for their reads. Use the shipped SDK's
`https://glb.console.greennode.ai/glb-controller/v1` with SDK IAM-user bearer
login. The spec's `glb.api.vngcloud.vn` is not a fallback. Upstream client
credentials do not establish IAM-user write permission.

Paths below are relative to `/v1/global-load-balancers/{glbid}`.

|Method and suffix|OpenAPI / upstream SDK|Use|
|-|-|-|
|`POST /global-pools`|200 / 202, direct pool object|Create pool and nested group|
|`POST /global-listeners`|200 / 202, direct listener object|Create bound listener|
|`DELETE /global-listeners/{listenerid}`|200 / 202, no upstream body decode|Delete exact listener|
|`DELETE /global-pools/{poolid}/pool-members/{poolmemberid}`|403 live for the sole group|Not used|
|`DELETE /global-pools/{poolid}`|202 live|Delete pool with its group|
|`GET /global-pools/{poolid}`|200 / no upstream method|Private exact pool guard|
|`GET /global-listeners/{listenerid}` and `GET /global-pools/{poolid}/pool-members/{poolmemberid}`|200 / 200, direct objects|Exact listener and group guards|
|`GET /global-listeners`, `GET /global-pools`, `GET /global-pools/{poolid}/pool-members`|200 / 200, direct arrays upstream|Corroborate identity, uniqueness and absence|
|`POST /global-pools/{poolid}/pool-members`|200 / no upstream POST; upstream PATCH uses 202|Out of scope|

Proposed exact pool request for the probe, with synthetic IDs and address.
Replace `region` with the independently matched GLB catalog value. Do not
publish these values as supported defaults until confirmed.

```json
{
  "name": "tcp-pool",
  "protocol": "TCP",
  "algorithm": "ROUND_ROBIN",
  "health": {
    "protocol": "TCP", "healthyThreshold": 3, "unhealthyThreshold": 3,
    "interval": 30, "timeout": 5
  },
  "globalPoolMembers": [{
    "name": "tcp-backend", "region": "<glb-region-id>", "type": "PRIVATE",
    "vpcId": "vpc-1", "trafficDial": 100,
    "members": [{
      "name": "tcp-backend", "address": "10.1.2.3", "subnetId": "sub-1",
      "port": 8080, "monitorPort": 8080, "weight": 1, "backupRole": false
    }]
  }]
}
```

Use `MemberName` for the group and row. Derive `address`, `vpcId`,
`subnetId`, and `region` from verified placement. Send `backupRole: false`
explicitly; `monitorPort` equals `BackendPort`. The [upstream request][up-pool]
supports these keys and proposes round robin and health values `3,3,30,5`.
Its [member update example][up-test] supplies dial `100`, weight `1`, and
monitor port equal to service port. That is a candidate for create, not
proof. Verify units, values, required fields and actual read-back before
freezing them. Omit HTTP health fields, optional descriptions, `stickySession`
and `tlsEnabled` (upstream calls the last two `stickiness` and
`tlsEncryption` for Layer 7). Do not send payment or project fields.

Proposed exact listener request; `globalPoolId` is required by this slice
although optional in OpenAPI. All three timeout fields are required integers.

```json
{
  "name": "tcp-listener", "protocol": "TCP", "port": 443,
  "globalPoolId": "gpool-1", "allowedCidrs": "192.0.2.5/32,198.51.100.0/24",
  "timeoutClient": 50, "timeoutConnection": 5, "timeoutMember": 50
}
```

The [upstream listener][up-listener] joins prefixes with `,` and uses
`50,5,50` for the three timeouts. Confirm accepted values, units, binding and
CIDR encoding. Never copy the upstream `/0` default. Omit `headers` (the
spec says string and the upstream create type says slice) and `description`.
DELETE sends no body. Accept only 200 or 202 as write acceptance; neither
status confirms the result.

## Scope guards

Use SDK-owned IAM-user login and built-in auth, dashboard, GLB and regional
vServer endpoint provenance before acquiring a token or reading a guard.
Use the same authenticated core client for all guards and the write. Keep the
[shipped caller-supplied transport trust boundary][parent]. Do not follow
redirects or URLs returned by the region catalog. Check the exact parent ID,
Layer 4 type and eligible state using the scoped GLB detail read. Ownership
follows owner decision 5: the exact GLB detail under the SDK-owned IAM-user
token, and a backend project equal to the client's resolved project. Do not
compare GLB `userId` to the IAM principal's `UserID`.

On both creates prove backend placement under the explicit project ID:

1. Match the configured `hcm-3` to one enabled, package-eligible GLB region
   using the guarded `ListRegions` and `ListPackages` routes. Verify the
   region code sent to GLB and package support. Do not assume the GLB region
   ID is the SDK configuration string or use a displayed region name as ID.
2. Require `BackendProjectID` to equal the client's resolved project. Do not
   fall back to a default or synthesize region evidence from `ListProjects`'
   missing-region fill.
3. Reuse the routes behind [compute.GetServer][compute] and
   [network.GetVPC/GetSubnet][network] with strict private projections:
   `GET /v2/{project}/servers/{server}`, `GET /v2/{project}/networks/{vpc}`,
   and `GET /v2/{project}/networks/{vpc}/subnets/{subnet}` on the pinned
   vServer IAM gateway. Match `data.uuid` to the server ID. Require one
   `internalInterfaces` row in the requested VPC/subnet whose `serverUuid`
   is that server. Match `networkUuid` and `subnetUuid` to the exact network
   `id`, subnet `uuid`, and subnet `networkUuid` in the same project. The
   selected `fixedIp` must be canonical RFC 1918 IPv4 inside the confirmed
   subnet CIDR. Never select `floatingIp` or an external interface. Require
   evidenced eligible server/network state. Missing, ambiguous, or changed
   scope authorizes no POST.
4. `CreatePool` requires empty listener and pool inventories, and its
   confirmation requires the complete pool list to hold exactly one pool,
   whose ID equals the accepted ID; otherwise `ErrNotSettled` with that ID. For
   `CreateListener`, read the sole pool and sole group by ID, take their
   VPC/subnet and port from exact scoped read-back, and recheck the server
   project and fixed address. Require a settled TCP `ROUND_ROBIN` pool, the
   supported TCP health settings, one supported `PRIVATE` group and one
   primary backend row. Reject an existing listener or mismatched relation.

Validate each `AllowedCIDRs` item with `net/netip`: a canonical dotted IPv4
network plus prefix `/8` through `/32` inclusive, with all host bits zero.
Require at least one item. Reject empty items, whitespace within a value,
noncanonical text, IPv6 including mapped IPv4, `/0` through `/7`, repeated
prefixes, and a set whose union covers all IPv4 addresses. Join with commas,
no spaces. Compare read-back as exact canonical sets, allowing only order
changes. Reject a missing value, extra prefix, wildcard or widened range.
Prefer `/32` for known client IPs. Configuration echo does not prove
enforcement; owner decision 6 sets what the probe checks.

## Send, wait and recover

Reuse the shipped [GLB write transport and error sanitizer][write-code].
Use `Once`, `NoRedirect`, `Sensitive`, and observed HTTP status on mutations.
No replay after 401, 429, 5xx, redirect, failed dial, or ambiguous timeout.
Guard reads are bounded (at most 8 MiB uncompressed), strict, sensitive and
nonredirecting. Reject malformed, absent or null guard fields where required,
duplicate keys, casing aliases, and duplicate or inconsistent resource IDs.
Use private projections, not public inventory decoding, for consent checks.

Generalize `sanitizeCreateError` to reuse status-derived codes and value
redaction for all new paths without changing the shipped calls. Withhold
peer messages on guard and mutation paths, including non-JSON replies. Do
not expose request values, raw captures, response bodies or wrapped peer
errors. Reject any accepted or read value that contains a submitted
credential, decoded and as a substring, before it is used in a path, body or
output, partial output included; no further request follows. Reject
reflected credentials in projected output, including strings
that pass path-ID validation. Retain only validated IDs and confirmed fields.
Preflight 404 is `NotFound` with no write, a refused guard is
`ErrInvalidInput` or `ErrInvalidConfig`, malformed preflight is
`InvalidResponse`, and definitive HTTP failures keep typed sanitized status.

For create, use only the live-confirmed acceptance schema. The
[upstream pool response][up-pool-resp] has a direct `id`,
`globalLoadBalancerId`, `health`, and `globalPoolMembers`; the
[listener response][up-listener-resp] is direct and includes `id` and both
parent relations. Require a validated accepted ID and parent association.
Pool group identity must come from acceptance, not a name or list diff.
Verify the exact embedded backend ID when acceptance supplies it; it has no
independent route, so otherwise obtain it from the sole matching row in the
exact group detail. Never infer IDs from order, name, or a response URL.

Poll immediately and every 5 seconds for asynchronous states, bounded by 15
minutes after create and 10 after delete, with shorter context deadlines
winning. Use only child-specific success, transitional and terminal failure
states, absence signals and post-DELETE error mappings recorded per operation
by the probe; the waiter is written after that record exists. Do not copy parent status values or treat
backend health and provisioning status as interchangeable. Retry only known
transitions, including post-acceptance not-yet-visible 404 only if evidenced
and the parent is still exact. Malformed data, unexpected state or relation,
403, cancellation or a failed wait is not success.

- Pool: GET the exact pool and group. Match pool name, protocol, algorithm,
  parent, TCP health values (`intervalTime` in upstream read-back) and health
  parent IDs. Confirm the group links to both IDs and matches name, region,
  VPC, type and dial. Its one row must match assigned IP, subnet, ports,
  weight, primary role and parent IDs. Corroborate exactly one group and no
  listener. Require evidenced provisioning success; do not promise traffic.
- Listener: GET the exact listener and match GLB, pool, name, TCP, port, CIDR
  set, and timeouts. Confirm the same pool and member chain is still eligible
  and there are no other children in scope. Return the verified listener.
- With ambiguous acceptance or failed confirmation, return `ErrNotSettled`
  and safe partial output with any attributable IDs, not raw fields. Use
  `ErrFailed` for a verified terminal failure. CLI prints safe partial output
  on stdout, errors on stderr. Advise read-only inventory and billing checks
  before retry or cleanup. Never send a replacement write or auto-delete.

`Once` is per invocation. Preflight is not an atomic compare-and-set. Require
exclusive control of the GLB and backend attachment during the workflow.
If another writer can change topology or the assigned address between steps,
stop; a name is not an idempotency key and the SDK cannot enforce exclusivity.

## Delete order and preflight

Delete the listener, then the pool with its nested group, then the GLB
through its existing command. Before every child DELETE, re-read the exact
owned Layer 4 parent and target, cross-check parent links and complete
relevant inventories, and require the pool `ACTIVE` with health `ACTIVE`.
Member health (`ONLINE`, `OFFLINE`, `HEALTHY`, `UNHEALTHY`) is reported,
never required. Require matching IDs, known TCP/private types and no extra
affected children. In particular:

- Listener: verify its sole pool binding and report which listener will
  stop receiving traffic. The operation must not delete its pool.
- Pool: require no listener and exactly the one expected private group with
  its one row. The pool DELETE removes both; the vServer stays.

Never send after a preflight 404, ambiguous inventory or scope mismatch.
`Deleted=true` requires the complete collection under the still-present
exact parent to omit the ID (and, for a pool, its group), with intended
survivors present. The exact-ID GET only corroborates: after an accepted
DELETE, a 404 or an HTTP 500 `internal_error` is consistent with absence; a
200 with the same ID keeps polling. A 403, parent loss, malformed read or a
2xx alone is not absence, and an unproven absence returns `ErrNotSettled`
with the ID and `Deleted=false`. Do not change parent delete.

## Probe record

One `hcm-3` run under decision 7: a fresh GNLB_4000 GLB, a private
s2-general-1x2 vServer with only SSH listening, backend port 22, and the
laptop's `/32` on listener port 2222.

- **Pool create:** accepted and, after about 147 seconds, read back with
  pool and health `ACTIVE`, health `TCP 3,3,30,5`, group `PRIVATE`, region
  `hcm` (the `ListRegions` ID whose name is `HCM-3`; regions report an empty
  `Status`), dial `100`, row weight `1`, port and monitor port equal.
- **Member status is health:** `OFFLINE` before a listener, then
  `UNHEALTHY`; it never gates provisioning.
- **Listener create:** `ACTIVE` in 11 seconds with the CIDR set and
  timeouts `50,5,50` echoed. A TCP connection from the allowed `/32` to the
  GLB address was accepted; the private backend stayed `UNHEALTHY`, so no
  backend bytes returned. Backend reachability from GLB health sources is
  unproven and stays the operator's concern.
- **Deletes:** the listener DELETE removed it (list empty; exact GET then
  HTTP 500). The sole-group DELETE returned 403. The pool DELETE returned
  202 and removed the pool and its group.
- **Billing:** no child create or delete changed cash, available balance or
  holding. The bare GLB holds 176,000 VND, still held after its delete.

## Billing, approvals and verification

[GreenNode pricing][pricing] describes a GLB-level forecast hold for three
prepaid days and later connection and data charges. [Managing NGLB][manage]
shows a listener and pool as parts of a GLB and lets users add them later.
The API documents no child quote route and the child requests have no price,
SKU or payment fields. None of this establishes that child writes create no
additional charges, regional components or credit holds, or that deleting a
child earns a refund. Treat separate child billing and funding as unknown.
A package catalog price, missing payment field, or flat account balance is
not proof. Owner decision 4 records the exception: child creates ship with
`--yes` consent and no quote; balance records stay private, and help and
wiki name only attributable child charges.

Manager-only live plan under the [live-data rules][live]:

1. Approval is owner decision 7. Before spending, verify backend project
   eligibility, known request fields and cleanup routes, and keep the probe
   to one GLB, one server and one run.
2. Record resource-linked billing baseline. Create one disposable bare GLB
   with the shipped command and one separately approved tiny private vServer
   in an eligible subnet. No public IP. Run only a disposable TCP responder;
   restrict its firewall to verified GLB data/health sources. No production
   traffic or data. Verify server, network and GLB identity and ownership,
   confirm the GLB region/package join, and record private cleanup IDs
   before adding children.
3. On that GLB, use one pool POST including its member, then one listener
   POST with the verified pool ID and a controlled source `/32`. Record
   method, status, safe shape, IDs, transitions and chain evidence. Check the
   allowed `/32` connection under owner decision 6. Do not send a second
   mutation to rescue missing evidence.
4. Use only recorded owned IDs. Re-read exclusive inventory, delete listener,
   pool (with its group), GLB, then server and any run-owned network resources in
   dependency order. Confirm absence and record billing and leftovers. Stop
   rather than deleting a name match or a parent still holding an uncertain
   child. Reconcile any holds, new billable resources and delayed usage;
   publish only synthetic fixtures and sanitized contract findings.

Loopback checks: exact methods, path, body and omission; both success statuses;
invalid CIDR, ID, port, profile, project, region, topology and backend rejected
before mutation; private-IP and ownership joins, duplicate/hostile JSON, missing
and wrong IDs, impossible state, bounded waits, ambiguous outcomes, no resend,
redirect/capture/credential canaries, and preflight/confirmation 404 versus 403
and parent loss. Test deletion order, survivors and no cascade; CLI help,
flag/JSON precedence, read-only, `--yes`, query, partial output and exit codes.
Run regression checks for shipped bare GLB and reads. Land SDK, CLI and
updated generated wiki together after the probe record, `make check` and
adversarial review.

[parent]: glb-create.md#owner-decisions
[writes]: ../adr/0002-write-api-conventions.md
[write-code]: ../../globalloadbalancer/write.go
[compute]: ../../compute/compute.go
[network]: ../../network/vpcs.go
[cli]: cli.md#errors-and-exit-codes
[live]: ../../instructions/live-data.md
[openapi]: https://docs.api.greennode.ai/service-docs/glb-api.html
[pricing]: https://docs.greennode.ai/glb/pricing.md
[manage]: https://docs.greennode.ai/glb/network-glb/managing-nglb.md
[up-ops]: https://github.com/GreenNodeHub/vngcloud-go-sdk/blob/54700ac4189008f1d0b8aa0270aa9cad79dad4c3/vngcloud/services/glb/v1/glb.go#L11-L315
[up-pool]: https://github.com/GreenNodeHub/vngcloud-go-sdk/blob/54700ac4189008f1d0b8aa0270aa9cad79dad4c3/vngcloud/services/glb/v1/glb_pool_requests.go
[up-listener]: https://github.com/GreenNodeHub/vngcloud-go-sdk/blob/54700ac4189008f1d0b8aa0270aa9cad79dad4c3/vngcloud/services/glb/v1/glb_listener_request.go#L53-L165
[up-pool-resp]: https://github.com/GreenNodeHub/vngcloud-go-sdk/blob/54700ac4189008f1d0b8aa0270aa9cad79dad4c3/vngcloud/services/glb/v1/glb_pool_response.go
[up-listener-resp]: https://github.com/GreenNodeHub/vngcloud-go-sdk/blob/54700ac4189008f1d0b8aa0270aa9cad79dad4c3/vngcloud/services/glb/v1/glb_listener_response.go
[up-test]: https://github.com/GreenNodeHub/vngcloud-go-sdk/blob/54700ac4189008f1d0b8aa0270aa9cad79dad4c3/test/glb_test.go#L74-L84
