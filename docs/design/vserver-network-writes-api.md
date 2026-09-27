# vServer Network Writes: API

Status: Accepted (2026-09-27), with
[vServer network writes](vserver-network-writes.md).

The API shapes, server rules, and cost evidence behind
[vServer network writes](vserver-network-writes.md).

## Sources

Shapes come from public sources only, none checked live yet:

- The vServer API reference on `docs.api.greennode.ai`
  (`service-docs/vserver.html`): tags for networks, subnets, route tables,
  network ACLs, and server groups.
- VNG Cloud's Go SDK (`vngcloud/vngcloud-go-sdk`, `services/compute/v2`):
  server group create and delete, and error messages.
- VNG Cloud's Terraform provider (`vngcloud/terraform-provider-vngcloud`,
  `resource/vserver`): network, subnet, route table, and server group
  resources, and their waits.
- Product docs on `docs.greennode.ai`: VPC, Route Table, Network ACL, and
  Placement Group.

The reads already use the IAM vServer gateway with the same
`v2/{projectId}/...` paths; that the writes exist there is a live check, as
it was for security groups. Every shape here is inferred until its live
check passes.

## Calls

Paths are under `v2/{projectId}`. "Success" is the documented status; the
code accepts the status the live check records.

| Call | Method and path | Success |
|-|-|-|
| Get server group | `GET /serverGroups/{id}` | 200 |
| Create server group | `POST /serverGroups` | 201 |
| Update server group | `PUT /serverGroups/{id}` | 200 |
| Delete server group | `DELETE /serverGroups/{id}` | 204 |
| Create VPC | `POST /networks` | 200 |
| Rename VPC | `PATCH /networks/{vpcId}` | 200 |
| Delete VPC | `DELETE /networks/{vpcId}` | 200 |
| Enable Private DNS | `PATCH /networks/{vpcId}/enableDns` | 200 |
| Create subnet | `POST /networks/{vpcId}/subnets` | 200 |
| Rename subnet | `PATCH /networks/{vpcId}/subnets/{subnetId}` | 200 |
| Delete subnet | `DELETE /networks/{vpcId}/subnets/{subnetId}` | 200 |
| Servers in a subnet | `GET /servers/subnets/{subnetId}` | 200 |
| Get route table | `GET /route-table/{id}` | 200 |
| Create route table | `POST /route-table` | 200 |
| Replace routes | `PUT /route-table/{id}/routes` | 200 |
| Delete route table | `DELETE /route-table/{id}` | 202 |
| Get ACL | `GET /network-acl/{id}` | 200 |
| Create ACL | `POST /network-acl` | 201 |
| Replace ACL rules | `PUT /network-acl/{id}/rules` | 200 |
| Replace ACL subnets | `PUT /network-acl/{id}/subnets` | 200 |
| Delete ACL | `DELETE /network-acl/{id}` | 204 |

The reference marks a `portal-user-id` header required; the reads work
without it, and so must the writes.

## Bodies (inferred)

Every create and update body also takes `tags` and `zoneId`; the SDK sends
neither, except `zoneId` on VPC create when given.

- Server group create: `name`, `policyId` (both required), `description`.
  Update: `name` (required), `description`, and a `serverGroupId` string.
  Terraform's update leaves `serverGroupId` out; the SDK does too unless
  the live check shows it is needed, and then sends the path ID.
- VPC create: `name`, `cidr` (both required). Rename: `name`.
- Subnet create: `name`, `cidr` (both required); `secondarySubnetRequests`
  is never sent. Rename: `name`.
- Route table create: `name`, `networkId` (the VPC), and optional `routes`,
  which the SDK leaves out. Route replace: `routes`, the whole list, each
  `destinationCidrBlock` and `target`.
- ACL create: `name`, `vpc` (both required). Rules replace: `aclId` and
  `detailAclRuleList`, the whole list, each `type`, `seqNumber`,
  `protocol`, `port` (a string), `source`, `action`, `system`, and
  `interfaceAclPolicyUuid`. Subnets replace: `aclId` and `subnetUuids`, the
  whole list.

## Responses (inferred)

- Server group get, create, and update return `data` with `uuid`, an
  integer `serverGroupId`, `name`, `description`, `policyId`, and
  `createdAt`, and no `servers`. The list returns `servers`.
- VPC create, rename, and enable return `data` holding the VPC; the VPC get
  returns it at the top level, as `GetVPC` decodes today.
- Subnet create and rename return `data` holding the subnet; the subnet get
  returns it at the top level.
- Route table create returns a flat string map; Terraform reads `uuid`.
- ACL create returns `data` with `uuid`, `name`, `status`, `defaultAcl`,
  `interfaceNetworkUuid` (the VPC), and `projectUuid`. The ACL get adds
  `aclPolicyRules` and `subnetAssociationList`.

Each create decodes into a private response type and maps it to the public
model, as security group create does, so a field type that differs from
the read model cannot fail the decode.

## Server rules (from the product docs)

- A VPC is one `/16` from `10.0.0.0/8`, `172.16.0.0` to `172.24.0.0`, or
  `192.168.0.0/16`, in one zone. A subnet is a `/24` or `/28` inside it.
- Enabling Private DNS reserves `/28` subnets for vDNS. It takes about 5.5
  minutes ([vDNS](dns.md#private-dns-on-the-vpc)). The API has no disable
  call.
- Deleting a VPC deletes its ACLs, route tables, and subnets. It fails
  while servers, load balancers, or other interfaces use the VPC.
- A subnet can be deleted only when no resource uses it.
- Each VPC has a main route table. Only routes a user added can change.
- A new ACL has two default deny rules (inbound and outbound) that cannot
  change or be deleted. The docs also list an allow-all rule per
  direction; whether a new ACL has it is a live check. Rules are evaluated
  by priority, lowest first, up to 32766, and a priority is unique in an
  ACL. Protocols are `ANY`, `TCP`, `UDP`, and `ICMP`.
- A subnet belongs to at most one ACL. Associating it with another ACL
  moves it. An ACL with subnets cannot be deleted.
- Route table and ACL names are 5 to 50 of `a-z A-Z 0-9 _ -`.
- A server group's policy cannot change after create. Server group names
  are unique (`name must be unique`); a group with servers is refused
  (`server group is in use`).

## Waits (inferred)

Terraform waits for `ACTIVE` after VPC, subnet, and route table create,
for `ACTIVE` after a routes replace (from `UPDATING`), and for 404 after
VPC, subnet, and route table delete. It does not wait after a server group
write.

## Cost

No pricing page, calculator item, or quote resource type names server
groups, VPCs, subnets, route tables, or ACLs. The design treats all as
free, so ADR 0002 rule 8 (quote) does not apply. The
[cost probes](vserver-network-writes-checks.md#cost-probes) and the next
day's bill confirm it before each release's code merges.
