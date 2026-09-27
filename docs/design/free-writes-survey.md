# Free Writes Survey

Status: Accepted (2026-09-27). The owner approved every recommendation.

This survey ranks the GreenNode writes that cost nothing and that the test
account can create and clean up, and proposes which to design next. It is
not a design: each pick still gets its own design, with its own live checks,
before code starts.

The test account has no credit, so only free writes can be verified live.
Shipped writes are budgets and thresholds, vDNS private zones and records,
vMonitor checks, channels, and log projects, security groups and rules, and
SSH keys.

## Sources

Public pages and repositories only. No authenticated call was made.

- The OpenAPI specs embedded in `docs.api.greennode.ai/service-docs/`:
  `vserver`, `vlb-api`, `vcr`, `accounts-api`, and `policies-api`.
- VNG Cloud's Go SDK (`vngcloud/vngcloud-go-sdk`): `compute/v2`
  (server groups, tags), `network/v2`, `loadbalancer/v2` (certificates),
  and `server/v1` (tag resource types).
- VNG Cloud's Terraform provider (`vngcloud/terraform-provider-vngcloud`):
  `resource/vserver` (network, route table, server group) and
  `resource/vloadbalancing/resource_certificate.go`.
- Product docs on `docs.greennode.ai`: vServer pricing, Placement Group,
  VPC, Route Table, Network ACL, DHCP Options Sets, Virtual IP, vLB
  Certificate, vCR getting started, and vMonitor pricing.
- This repository: `testdata/pricing/GetQuotePublicVIP.json`, and the cost
  notes in [vDNS](dns.md#cost) and [vStorage](storage.md).

## How cost was judged

No GreenNode page gives a price list for vServer; the vServer pricing page
points to the console calculator. So "free" rests on three kinds of
evidence, named per row below:

- **No product.** No pricing page, quote resource type, or package names
  the resource, and its create body has no package, period, or size.
- **Refusal on a paid create.** The test account is prepaid with no credit,
  so a paid create fails instead of billing, as the vDNS live checks found.
  This makes a single create a safe probe; it is an assumption for any
  service not yet probed.
- **Next-day bill.** The final check for every pick: no line for the
  service on the next day's bill.

A body with a size or package field (vCR `quotaLimit`) or a known price
(public virtual IP, 120,000 VND a month in the quote fixture) is treated as
paid or unknown until a `pricing.GetQuote` or a probe says otherwise.

## Ranking

Rank weighs cost confidence, paid prerequisites, cleanup, risk, and size.
Size is S (one resource, two or three calls), M (a parent and children, or
waits), or L (several resources or a secret flow). Paths are under
`v2/{projectId}` on the vServer gateway unless noted; every read path
below is already used by the SDK on the IAM vServer gateway.

| # | Candidate | Endpoints | Paid prerequisite | Cost evidence | Cleanup | Risk | Size |
|-|-|-|-|-|-|-|-|
| 1 | Server groups | `POST serverGroups`, `PUT`, `DELETE serverGroups/{id}` | None; `policyId` from the existing policy list | No product; body has no package; placement is a scheduling hint | Delete, 204 | Low | S |
| 2 | VPCs and subnets | `POST networks`, `PATCH`, `DELETE networks/{id}`, `PATCH networks/{id}/enableDns`; `POST`, `PATCH`, `DELETE networks/{id}/subnets/{sid}` | None | No product; the VPC page names no charge, while NAT, bandwidth, and floating IPs are priced | Delete subnet, then VPC; VPC delete removes its ACLs and route tables | Low: VPC quota and CIDR use | M |
| 3 | vLB certificates | `POST cas`, `DELETE cas/{id}` on the vLB gateway | None; import needs no load balancer | No product; the Certificate page names no charge | Delete, 204 | Medium: sends a private key | S |
| 4 | Route tables and routes | `POST route-table`, `DELETE route-table/{id}`, `PUT route-table/{id}/routes` (full replace) | A VPC (#2) | No product | Delete, 202, async | Low in a test VPC | M |
| 5 | Network ACLs | `POST network-acl`, `DELETE network-acl/{id}`, `PUT network-acl/{id}/rules` (full replace), `PUT network-acl/{id}/subnets` | A VPC and subnet (#2) | No product | Detach subnets, then delete | Low in a test VPC; an ACL on a live subnet cuts traffic | M |
| 6 | DHCP options sets | `POST dhcp_option`, `DELETE dhcp_option/{id}`, `PATCH networks/{id}/updateDhcpOption` | None to create | No product; limit 10 per user | Delete | Low; a set on a live VPC changes its DNS | S, plus reads |
| 7 | Private virtual IPs | `POST virtualIpAddress`, `PUT`, `DELETE virtualIpAddress/{id}` | A subnet (#2) | Unknown: the public VIP costs 120,000 VND a month; the private one has no quote | Delete | Low | S |
| 8 | vCR repositories and users | `POST v1/repository`, `DELETE v1/repository/{id}`, `POST v1/user`, `DELETE v1/user/{id}` on the vCR API | None | Unknown: `quotaLimit` in GB suggests storage billing | Delete user, then repository; 202, async | Medium: a user create returns a registry secret | M |
| 9 | IAM service accounts | `POST`, `PATCH`, `DELETE v1/service-accounts/{id}` on the IAM accounts API | None | Free per [vStorage](storage.md) | Delete, 204 | High: account-wide identity with a client secret | M |
| 10 | IAM groups and policies | `POST`, `PATCH`, `DELETE v1/groups/{id}`; `POST`, `PUT`, `DELETE v1/policies/{id}` on the IAM policies API | None | No product | Delete; attached ones first detach | High: an attach changes a principal's rights | M |
| 11 | Resource tags | `PUT tag/resource/{resourceId}` (full replace), with `resourceType` | A taggable resource | No product; tag quota exists | `PUT` an empty list | Low | S |
| 12 | vMonitor metric alarms | `/alarms/metrics` on the vMonitor console API | A metric quota package | vMonitor pricing: metric quota is a paid package | Delete | Low | M |

Left out, with the reason:

- **Address pairs** (private and public): they need a network interface,
  which means a paid server. An elastic network interface
  (`POST network-interfaces-elastic`) might stand in, but its cost is
  unknown.
- **Budget alerts**: thresholds already ship in `billing`, and alerts are
  events the service raises, with no create call.
- **Access keys**: GreenNode has no separate access key API. Service
  account client secrets are #9; S3 keys need a paid vStorage project.
- **IAM users, identity providers, MFA, and password calls**: they change
  who can sign in to the account. No design should add them without an
  owner request.
- **Project settings**: no public spec documents a project or portal
  write.
- **Resource tags** (#11) stay low: the only resource types the Go SDK
  names are `SERVER`, `VOLUME`, and `LOAD-BALANCER`, all paid. Create
  bodies for VPCs, route tables, ACLs, and server groups take `tags`, but no
  source shows a free resource type accepted by the tag API.

## Top picks

### 1. Server groups

Add `CreateServerGroup`, `UpdateServerGroup`, and `DeleteServerGroup` to
`compute`, with CLI commands, beside the existing server group and policy
reads. Create takes `name`, `policyId` (required), and `description`;
update takes `name` and `description`, since the policy cannot change after
create. The delete guard reads the group's members and refuses a group
with servers, as security group delete does. Terraform shows a synchronous
create with no wait. Live checks: create with each policy, update, delete,
and a repeat delete; the quota row in `portal list-quota-used`; the next
day's bill.

### 2. VPCs and subnets

Add VPC create, rename, delete, and Private DNS enable, and subnet create,
rename, and delete, to `network`. Create takes a `/16` CIDR from the
documented private blocks; a subnet takes a `/24` or `/28` inside it. The
SDK checks CIDR shape only; the block rules stay on the server. Terraform
waits on `status` after VPC create and delete, so the design needs waits.
Delete is destructive and needs `--yes`. A test VPC also gives vDNS live
checks their own VPC instead of a shared one. Live checks: VPC quota, time
to active, whether delete with a subnet left fails or cascades, and whether
`enableDns` can be turned off again.

### 3. vLB certificates

Add `ImportCertificate` and `DeleteCertificate` to `loadbalancer`, beside
the existing certificate reads. The import body holds `name`, `type` (`CA`
or `TLS/SSL`), `certificate`, `certificateChain`, `privateKey`, and
`passphrase`. The private key and passphrase are secret inputs: the SDK
takes them as `vngcloud.Secret`, the CLI reads them only from files, and
neither may reach `--debug` output or an error. The live test builds a
throwaway self-signed certificate in the test process. This pick needs the
adversarial review AGENTS.md requires for credential handling. Live checks:
import without a load balancer, the response shape, whether a get ever
returns the key (log a boolean only), and delete.

### 4. Route tables and routes

Add route table create and delete and a routes replace to `network`. It
builds on pick 2: the live test makes its own VPC. `PUT .../routes` sends
the whole route list, so the SDK reads the table and offers add and remove
on top, as vDNS zone update does. The route `target` is an IP address in
the docs; whether it must belong to a server interface is a live check,
since the test account has none. Delete returns 202 and is asynchronous.

### 5. Network ACLs

Add ACL create and delete, a rules replace, and subnet association to
`network`, on pick 2's test VPC. A new ACL denies all traffic until rules
are added, and its default deny rules cannot change; the rules replace must
keep them. Association is the risky call: an ACL on a subnet with servers
can cut them off, so the CLI needs `--yes` when a subnet has interfaces.
Delete needs the subnets detached first.

## Owner decisions

1. **What to design next.** Recommendation: one design, "vServer network
   writes", covering picks 1, 2, 4, and 5, released in that order as four
   tags. They share the IAM vServer gateway, the waits, and the delete
   guards of [vServer free writes](vserver-writes.md), and server groups
   ship first because they need nothing else. Certificates (pick 3) get
   their own design next, because the private key flow needs its own
   security review. The alternative, a design per pick, repeats the
   gateway and wait sections five times.
2. **Cost probes.** Recommendation: approve one create and delete per pick
   on the test account in `hcm-3`, relying on the no-credit refusal and the
   next-day bill. For private virtual IPs (#7) and vCR (#8), run a
   `pricing.GetQuote`, which is read-only, before any create, and keep them
   out of any design until a quote or a probe shows zero cost.
3. **IAM writes, flagged for explicit approval.** Service accounts (#9) and
   groups and policies (#10) change who can do what in the whole account.
   Recommendation: defer both. If approved, allow create, update, and
   delete of unattached objects only, on the test account, and never attach
   a policy to a principal, add a user to a group, or create an IAM user in
   a live test. Service accounts are already in the accepted
   [vStorage](storage.md) design; splitting them out needs its own approval.
4. **Account-wide settings, flagged.** DHCP options sets (#6) and ACL
   association (#5) change network behavior for everything in a VPC.
   Recommendation: allow them only on a VPC the live test creates, never on
   an existing one.
5. **Tags and metric alarms.** Recommendation: defer both. Tags wait for a
   live read showing a free taggable resource type; metric alarms wait for
   a metric quota package, which is paid.
