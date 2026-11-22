# VKS cluster create

Status: Accepted with the owner decisions below. Implement now; the manager
runs the live create and delete on disposable resources before landing. The
[accepted read contract](vks.md) stays unchanged.

## Owner decisions

1. Build `CreateCluster` (cluster with one bootstrap node group),
   `DeleteCluster`, and the read helpers they need now. The owner approves
   live create and delete of disposable test resources.
2. Use the public OpenAPI `CreateClusterComboDto` wire shape with query
   `autoRenewal=false&poc=false`. Accept the documented 202; the live run
   records the actual status.
3. VKS has no quote API. As an exception to ADR 0002 rule 8, create needs
   `--yes` without a quote. Help lists what the cluster bills: worker
   servers and disks, and for a private cluster the managed load balancer
   and four service endpoints. Delete does not promise a refund. Add a
   quote command if the live run finds a price endpoint.
4. The first release creates private clusters with private nodes only. The
   public OpenAPI create body has no API allowlist field, so a public
   control plane cannot be restricted at creation. Public clusters wait for
   a proven allowlist field.
5. No POC wallet, top-up, or payment-method operation. The sections below
   describe the stricter long-term design; where they conflict with these
   decisions, these decisions win.

## Recommendation

Ship one private, single-AZ (availability zone) cluster with one fixed private
worker in one bootstrap node group. This is the smallest proposed cluster
that can run a workload. Ship its quote, read-only reconciliation, and
explicit owned cleanup together. Validate one region and one funding path;
start with `hcm-3`, where the VKS workspace read was observed. Do not infer
`han-1` support or substitute regions at runtime.

| Alternative | Trade-off |
|-|-|
| Control plane only | Public OpenAPI allows omitting the group. Check this in the console, but keep it out of the workload-ready contract: it cannot run a workload and may still require billable private endpoints. If a control-plane-only product is sufficient, approve it as a smaller, different contract. |
| Public control plane, private worker | Requires existing verified egress and an API allowlist enforced from initial provisioning. The create example opens `0.0.0.0/0`; the current public create schema does not advertise that allowlist. Do not use this as a fallback. |
| Standalone node-group create | Requires another paid submission, its own quote, and partial-failure cleanup. Prefer one cluster submission with its bootstrap group. Defer expansion, scaling, and independently managing groups. |

Do not activate a workspace, reset its service account, grant IAM policies,
change existing network resources, create NAT, add a public IP, retrieve
kubeconfig, deploy workloads, or configure fleet, logging, autoscaling, or
workload load balancers and volumes. No arbitrary body, payment, or auth
fallback. If the verified product cannot meet these constraints, keep writes
held rather than widening the first release.

## Public evidence and limits

The [create guide][create] documents a bearer token, existing VPC/subnet,
image/flavor selection, SSH key and security group, and `POST /v1/clusters`.
Its sample misses a comma after `isEnableAutoRenew` and is not the wire
contract. The [public OpenAPI][api], in `__redoc_state.spec.data`, is the
public wire reference. It differs from the sample:

- `CreateClusterComboDto` prefers one `nodeGroup` object and `listSubnetIds`
  with one subnet for `SINGLE`. It deprecates `nodeGroups` and cluster-level
  `subnetId`. The group still has its own `subnetId`. The older [official
  Terraform models][tf] corroborate several fields but use `nodeGroups`.
- Send public `releaseChannel: "STABLE"` as a string. The enum and example
  are strings despite a union with object. The current group schema uses
  `os`, not the guide's `imageId`; verify actual image selection. Disk size
  prose, numeric bounds and root/data disk descriptions disagree. Use the
  eligible offer and quote; do not copy sample values or hardcode its bounds.
- On the public API the candidate uses query `autoRenewal=false&poc=false`.
  Public `autoRenewal` defaults to true. The guide's body switches
  `isBuyMorePoc`, `isPoc`, and `isEnableAutoRenew`, and its `whitelistIp`,
  `ipipEncapsulationMode`, and node-group `enableAutoHealing`, are not in
  the current create DTOs. Neither dialect by itself establishes funding or
  effective renewal state.
- The schema calls private cluster public-only in a field description, but
  the [private guide][private] documents private provisioning. Confirm the
  actual region, route, payload and flags that produce a private-only
  control plane and worker. Do not send both dialects or guess aliases.

The following are public API declarations, not IAM-console contracts:

| Operation | Method and path | Declared result |
|-|-|-|
| Create combo | `POST /v1/clusters` | 202 with cluster object and `id` |
| Read cluster | `GET /v1/clusters/{clusterId}` | 200 detail, 404 absent or outside user scope |
| List groups | `GET /v1/clusters/{clusterId}/node-groups` | 202 paged groups |
| Read group | `GET /v1/clusters/{clusterId}/node-groups/{nodeGroupId}` | 202 detail |
| Delete cluster | `DELETE /v1/clusters/{clusterId}` | 202 with cluster object, not completion |
| Standalone group | `POST` on group collection, `DELETE` on group ID | 202 group object; delete has `forceDelete`, default false |

The public reference advertises no VKS quote, credits-only selector,
price binding, idempotency key, or cascade or refund guarantee. It does not
establish workspace selection, status transitions, delete eligibility, or
permissions at console hosts. Do not generalize the accepted list, versions,
and quota observations to these routes. The newer public detail status
above does not change the held read contract or establish a live response.

A Playwright session signed in as an IAM user received `200` for the HCM
workspace read and opened the cluster create form. It displayed the name,
private-cluster choice, Kubernetes version, and subnet. No configuration
was completed; pricing, funding, and write permission remain unverified.
No activation or order was submitted.

## Auth, scope, and topology

Try the existing SDK-owned IAM-user login on the confirmed GreenNode
console flow, not the public service-account endpoint. [Public auth][auth]
documents a service-account client-credentials bearer. The [Terraform
setup][tf-guide] suggests `VKSFullAccess` for that service account; it does
not establish least-privilege actions, vServer discovery, billing access, or
IAM-user write permission. Do not auto-grant the broad policy. Verify exact
permission and scope for quote, create, lifecycle reads, and delete, plus the
read-only vServer and billing dependencies. No root, service-account, static,
custom provider, browser cookie, `portal-user-id`, or host fallback.

Require an already active workspace. Assert its confirmed `projectId`
against explicit `WorkspaceProjectID` on every lifecycle operation. This is
an assertion, not a selector or an extra header. `--project-id` continues
not to select a VKS workspace; never infer the workspace from a vServer
project or from a successful account-scoped read. Verify the VPC/project
relationship through the VKS resource picker or an equivalent verified join;
reject a missing or ambiguous association. Before obtaining credentials or
sending any request, validate the entire route set: IAM sign-in and token
exchange, VKS, and all vServer, network, volume, and billing dependencies.
Refuse endpoint overrides that change the approved routes, including
`Signin`, `Token`, and a dashboard override that changes token exchange.
Use verified first-party hosts with no redirects. Preserve existing SDK
hooks for released reads and use an internal seam for tests.

Require a dedicated existing active VPC and one active subnet in the same
verified vServer project and AZ, with private connectivity and DNS suitable
for VKS, no existing VKS clusters or VKS-managed endpoints on that VPC, and
no concurrent users of the VPC during create and cleanup. The caller must
have an existing private administration path and a privately reachable
image registry, such as vCR; the service endpoints do not provide public
registry access. The selected subnet is allowlisted for private access, but
`Ready` means provider provisioning is complete, not proven Kubernetes
API access or image pulls. The command does not dial the Kubernetes endpoint.
Use `CILIUM_OVERLAY`, an explicit
nonoverlapping private `PodCIDR`, `STABLE`, and an explicit enabled version.
Check VKS cluster, group and node limits and required vServer capacity as
separate [resource limits][limits]; neither is purchase approval.

Use one eligible Ubuntu worker, selected flavor, disk type and size, one
existing scoped security group and one existing eligible SSH key ID. No key
material. Verify that no provider-attached group opens broad inbound access
and that the node can join privately without new NAT or public IPs. Resolve
version-to-image selection with the VKS catalog; verify flavor, image and
volume availability in the subnet AZ. The [system image reference][images]
is not a live catalog. Existing `network` VPC, subnet and security
reads, `compute` SSH-key and flavor reads, and `volume` type reads are
selection aids, not proof of VKS eligibility. Use small verified internal
read helpers for lifecycle joins, not public SDK service dependencies or
new generic discovery commands.

Set private control plane and private nodes explicitly. Verify whether
`enabledServiceEndpoint` governs the four required private endpoints or
something distinct before setting it. Disable workload load balancer and
Block Store CSI plugins explicitly because public defaults are on. No
autoscaling or scheduled surge; verify omission/disable behavior for
autoscaling, auto-upgrade and auto-heal and any required upgrade
configuration before freezing the request. The public node-group DTO
requires `upgradeConfig.strategy: "SURGE"`; this is not permission to
schedule or buy extra nodes. Do not claim production HA.

## Quote and credit consent

Follow [ADR 0002](../adr/0002-write-api-conventions.md). Quote and create use
the same internal builder, validation and resolved resource spec. One fresh
provider-backed quote immediately precedes the only submission. A catalog
sum, a billing budget alert and the vServer calculator are not a VKS quote.
Only an observed side-effect-free quote may run as a read.

[What is VKS?][about] describes the control plane as free. The [private
guide][private] describes a managed private load balancer and four billable
service endpoints for IAM, vCR, vServer and vStorage. Pricing must explicitly
account for the control plane, bootstrap compute, every required boot/root
or data volume, managed load balancer, each required endpoint, tax and any
mandatory network, IP, traffic, or other fee. Require authoritative evidence
for zero, included and absent items, not just omitted rows. Verify the
control plane, worker and all enabled add-ons are covered by one quote and
one credits-only transaction. No deploying a cheaper-looking subset.

Recommend one confirmed fixed-term non-POC prepaid charge with auto-renew
off for every line. The [credit history guide][credit] describes normal
credit purchases and refunds in general. [Credit Hold][hold] instead
mentions vContainer (K8s), a rolling three-day hold and actual usage retained
after deletion. That is not proof that modern VKS uses that flow, of which
items it includes, or of a refund. If VKS only offers a rolling hold,
postpaid charge, or split checkout, stop. Review a separate contract for its
upfront hold, continuing exposure and stop conditions before supporting it.

Require a provider-backed credits-only constraint that rejects insufficient
eligible available credits without purchasing credits, charging another
funding source, falling back to invoice/postpaid, or drawing from or
converting a POC wallet. A displayed balance or `poc=false` is not such a
constraint. Do not call a top-up, payment-method, renewal correction, or
POC conversion route. The [VKS POC guide][poc] describes a different wallet.

Prefer a binding quote or provider-enforced maximum for the exact payload,
workspace, funding and term. If the provider supplies an authoritative
quote without a hard cap, require a separate owner decision on the remaining
price race; never present the local cap as a provider limit. That decision
cannot waive the credits-only funding constraint. Show a typed line-item
breakdown, exact total, term and funding mode. Use whole VND `int64` only if
verified; otherwise revise the unit, never round away spend. Missing, unknown,
duplicate, negative, overflowing, expired, or inconsistent quotes stop before
submission. `MaxPrice` must be explicitly present and nonnegative; zero
allows only an evidenced zero-cost offer. Exceeding it returns
`vngcloud.ErrPriceAboveMax`. No authoritative quote or credits-only mechanism
means no released create.

## SDK and CLI

Add methods to `vks.Client`, using `Method(ctx, *Input) (*Output, error)`.
The CLI registers these in the existing `vks` operation table:

| SDK | CLI leaf | Input |
|-|-|-|
| `QuoteCreateCluster` | `quote-create-cluster` | `CreateClusterInput`; no cap or confirmation required |
| `CreateCluster` | `create-cluster` | `CreateClusterInput`; require `--max-price` and `--yes` |
| `ReconcileCluster` | `reconcile-cluster` | `ReconcileClusterInput`, read only |
| `DeleteCluster` | `delete-cluster` | `DeleteClusterInput`; require owned ID and CLI `--yes` |

Keep input fields flat for flags and `--cli-input-json`. `CreateClusterInput`
requires string `WorkspaceProjectID`, `Name`, `AttemptID`, `Version`, `VPCID`,
`SubnetID`, `PodCIDR`, `FlavorID`, `VolumeTypeID`, `SecurityGroupID` and
`SSHKeyID`, and integer `DiskSize` in GiB once confirmed. `MaxPrice` is
`*int64`, required only for create. Fix the group name to `bootstrap`, the
node count to one and the OS to Ubuntu. No arbitrary flags for exposure,
funding, count, request body, or a provider's checkout fields.

`ReconcileClusterInput` and `DeleteClusterInput` both require
`WorkspaceProjectID`, `Name`, `AttemptID`, `VPCID`, and `SubnetID`.
`ClusterID` is optional for create reconciliation and required for delete
or post-delete reconciliation. An explicit `VerifyDeletion` boolean selects
post-delete reconciliation. Input errors name the field and rule, not values.
Apply the accepted VKS ID check
before authentication for every path ID; validate shape and service-selection
constraints locally, leave changing provider name and size rules to provider
validation. `AttemptID` is 32 lowercase hex characters from 16 random bytes,
generated and saved by the caller before create. It is a correlation value,
not a secret, permission, or promise of idempotency.

`QuoteCreateClusterOutput` carries typed `Lines` (`Component`, `Quantity`,
`Amount`), `TotalPrice`, `Currency`, `Term`, and `Funding`. Create, reconcile
and delete outputs carry `AttemptID`, `Name`, `ClusterID`, `NodeGroupID`, a
typed `Phase`, and any verified owned dependent IDs with typed resource
kinds. `Phase` is `Unknown`, `Creating`, `Ready`, `Failed`, `Deleting`, or
`Deleted`; it describes observations, not requested effects. Create includes
the fresh quote, not a measured charge.
Keep billing independent; never claim debit or refund from a quote or status
code. Omit raw bodies and headers, scope join rows, and kubeconfig.

Quote and reconcile work in `--read-only`; create and delete do not. Consent
is a CLI flag, not an Input field. Reject missing consent and cap before any
request. Keep existing formats, `--query`, generated references, and profile
and JSON/flag precedence. Preserve exit classes: usage and read-only refusal
exit 2, charge over cap or an incomplete or failed write exits 1. Add VKS to
the safe write-error path: success to stdout, partial result and recovery
advice to stderr on failure. Keep current constructors, reads and models; no
root aliases, module dependency, or generic cleanup command.

## One attempt and reconciliation

- Read a bounded complete cluster baseline before submission; refuse an
  existing exact name or marker, missing identity, or inconsistent pages.
  Use 10 per page, at most 100 pages, checking page/size, stable total, unique
  valid IDs, and a full item count. Reject a scan that cannot establish the
  scope. Require provider-enforced atomic name uniqueness within the same
  scope, including pending creates, before accepting a list-based preflight.
  A local check alone cannot exclude writers in other processes.
- Put `vngcloud-create-<AttemptID>` in the cluster description and
  `vngcloud-create-attempt=<AttemptID>` in the bootstrap group's cloud
  tags. Require exact preservation and typed reads of both. Verify scope,
  name, marker, VPC, subnet, private topology and any known ID in detail.
  Verify group-to-parent association, the bootstrap shape and the group
  tag; create also compares the resolved offer. Never adopt by name,
  creation time, or resemblance. A conflict, duplicate, stale scan or
  missing field authorizes no write. Markers guard against accidental
  adoption; IAM remains the authority.
- Submit one verified create with `Once` and `NoRedirect`. Do not replay for
  dial, 401, 429, 5xx, disconnect, cancellation, malformed success, or any
  other failure. Acceptance needs the verified status, object and a valid
  ID. Treat that ID as a candidate until the scoped read proves ownership.
  Use `NotSent` or a proved no-effect refusal to distinguish not submitted
  from uncertain. Do not treat an unverified 4xx body as proof of no action.
- Read immediately and then every five seconds, at most 20 minutes from
  submission, with the caller's shorter context taking precedence. Report
  `Ready` only after the verified active cluster, exact bootstrap group, one
  ready node, private flags and expected charge mode/renewal are confirmed.
  `vks.ErrFailed` denotes a verified terminal failure. Timeouts, cancellation,
  uncertain funding or acceptance, and unknown states after submission
  return `vks.ErrNotSettled` with safe partial output and known cancellation
  sentinel. A later read never rewrites a missing acceptance into successful
  create. Never perform automatic rollback.
- With `VerifyDeletion` off, `reconcile-cluster` can resume from the caller's
  saved create attempt without a local journal. Do the same scoped reads and
  match exactly one cluster;
  verify a supplied `ClusterID` rather than ignoring it. Return `Unknown`
  with no ID for a well-formed scan with no candidate, or a safe error for
  ambiguity or malformed/incomplete reads. `Unknown` is not evidence of
  absence, charge reversal, or permission to retry. Inspect the same region,
  workspace, and billing ledgers; never repeat create to resolve an outcome.

## Owned cleanup

`delete-cluster` is only for the verified, dedicated cluster returned by
create or reconciliation. On fresh reads, match exact cluster ID, workspace,
placement, name, description marker, group parent and tag, and every
provider-managed dependency. Use provider relationships and typed IDs, not
endpoint names. A failed partial create is eligible only if observed provider
rules and complete inventory identify every remaining owned dependency.
Refuse unrelated groups, changed ownership, shared or unknown dependencies,
unsafe states, or incomplete reads. The caller must attest the cluster has
no user workloads or external attachments and exclude other writers;
`--yes` acknowledges permanent destruction. The SDK cannot inspect
workloads without Kubernetes credentials or lock out independent actors.

Prefer a single normal cluster DELETE only if provider evidence establishes
safe service-managed child cleanup for this topology. Before DELETE require a
complete inventory independently readable by VPC, even after losing the
original cluster and the process result. It must account for the bootstrap
resources, managed load balancer, service endpoints, and every other
billable dependency. Hold this slice if any dependency can only be read
through the parent. Require the provider's cleanup order and what happens
on failed creates before attempting the live gate; follow [child-first
cleanup rules][live]. If deletion needs a separate
child mutation, force, or manual endpoint handling, hold this slice and
review the required cleanup separately. Never delete a caller's VPC, subnet,
security group, SSH key, shared endpoint, or foreign resource. No delete by
name, prefix sweep, or server-generated `Location` follow.

Send at most once. Return known no-effect refusals without resending. For a
valid acceptance or uncertain reply, read immediately and every five seconds
for up to 15 minutes from submission, bounded by context. Confirm absence
under the same still-authorized workspace and complete scoped inventory,
and removal of the known owned children and billable dependencies. A 404
by itself is not proof. Return `Deleted` only after cleanup is observed, not
from 202. If the reply or cleanup is uncertain, return `vks.ErrNotSettled`
with safe IDs and the last verified phase. Provide a read-only post-delete
reconciliation mode using the known parent ID and the dedicated VPC. It must
independently verify parent absence, complete VPC dependency inventory and
that no owned billable dependency remains, without relying on output from
the interrupted command. If any part is uncertain, keep `vks.ErrNotSettled`
and do not resubmit. A verified deletion does not promise a refund, hold
release, or invoice reversal.

## Evidence gates and checks

1. **No-charge console observation, manager only.** In an already active
   HCM workspace, view IAM policy actions and scopes, VKS workspace and create
   form, quota, private/Single-AZ option, cluster-only possibility, minimum
   worker count and eligible version/OS/image/flavor/disk/subnet/key/group.
   Check private access, effective security groups, forced prerequisites,
   plugin and renewal defaults and exactly which add-ons the form prices.
   Inspect only an independently verified no-effect estimate/review step;
   record sanitized method, host, path, request/response field types and scope,
   quote expiry and whether it binds price, tax, total, term, credit
   eligibility, and any hold, fallback or extra payment step. Inspect the
   normal-credit and POC ledger views separately. Do not click Activate,
   Create, Confirm, Pay, Top up or Convert,
   and do not send a fake or invalid mutation to discover a route. When
   an existing approved cluster is available, record sanitized detail and
   child statuses, marker visibility, ownership links and visible delete
   semantics without confirming deletion. Confirm atomic name uniqueness,
   including pending orders, through first-party evidence, not duplicate
   live submissions. If a fact requires a charge or write to observe, leave
   the gate open or obtain first-party confirmation.
2. **Owner decisions.** Approve first region and narrow topology, a confirmed
   credit-only transaction, any local-only quote cap, and provider-owned child-first
   cleanup. If gates fail, prefer holding the feature to enabling public
   access, relying on estimated pricing, or adding checkout/POC support.
   Incorporate sanitized evidence in the reviewed design before writing code;
   keep first unverified write evidence behind a separately approved probe.
3. **Approved live gate, manager only.** Separately approve one exact account,
   region, workspace, existing dependencies, quote total/currency/term,
   maximum spend, and deletion of only that new cluster and its owned
   children. Baseline inventory and normal-credit, POC, payment and billing
   history. Stop before submission if any guard differs. Submit once with
   the SDK credential path, confirm identity and private startup, trace the
   debit and each billed child by verified identifiers, and observe renewal
   off. Perform the approved owned cleanup once. Confirm child and billing
   stop state, record any hold release/refund or observed retained charge
   separately, and report leftovers privately. Unexpected charges, drift,
   unknown descendants, or permission gaps do not justify another order or
   broader cleanup. Follow [live data][live] for evidence retention.
4. **Before release.** Add deterministic SDK tests for exact wire body,
   explicit false flags and request counts, status and malformed replies,
   overflow/expiry/quote races, scope/auth/endpoint refusal, unsafe topology,
   pagination and concurrent submissions, lost responses and cancellation,
   identity mismatches, polling, foreign/shared resources, and failed
   cleanup. Assert no secret leaks through errors, causes, debug, fixtures,
   result formats, or capture hooks. Use `Sensitive` and fixed safe error
   projection on every new request, including dependencies; bound and
   strictly decode authorization and money fields. Test that changing an IAM
   sign-in or token-exchange route is refused with zero requests, and that
   reconciliation after losing the delete process does not mark unverified
   dependencies as deleted. CLI tests cover both
   normal and read-only modes, consent after JSON merge, cap presence,
   partial output/exit status, and generated help. Run `make check` and
   `make gen-docs` as applicable. Require independent design and adversarial
   code review, sanitized live evidence, and exact-commit CI. Link this
   design and update the VKS wiki in the implementation change, not this
   draft. Existing inventory remains independently releasable.

[api]: https://docs.api.greennode.ai/service-docs/vks-api.html
[auth]: https://docs.api.greennode.ai/#api-documentation
[create]: https://docs.greennode.ai/vks/getting-started/create-a-cluster-via-api.md
[private]: https://docs.greennode.ai/vks/getting-started/create-a-private-cluster.md
[about]: https://docs.greennode.ai/vks/vks-la-gi.md
[tf]: https://github.com/vngcloud/terraform-provider-vngcloud/tree/230bd7d346853b2aba33a9ec06f8c544b06fa8a2/client/vks
[tf-guide]: https://docs.greennode.ai/vks/getting-started/su-dung-terraform-de-khoi-tao-cluster-va-node-group.md
[credit]: https://docs.greennode.ai/billing-management/vconsole-management-channel-for-billing-and-resources/getting-started/credit-history.md
[hold]: https://docs.greennode.ai/billing-management/experience-with-billing-and-payment/payment/credit-hold.md
[poc]: https://docs.greennode.ai/vks/getting-started/create-a-cluster-via-poc.md
[limits]: https://docs.greennode.ai/vks/usage-limits.md
[images]: https://docs.greennode.ai/vks/reference/danh-sach-system-image-dang-ho-tro.md
[live]: ../../instructions/live-data.md
