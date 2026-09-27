# vServer Network Writes: API

Status: Accepted (2026-09-27), with
[vServer network writes](vserver-network-writes.md).

The API shapes, server rules, and cost evidence behind
[vServer network writes](vserver-network-writes.md).

## Sources

Shapes come from these public sources, and from the cost probes on the
test account in `hcm-3` (2026-09-27, every probe resource deleted):

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

The reads and writes use the IAM vServer gateway with the same
`v2/{projectId}/...` paths; the probes sent every write there. A shape marked "live" was seen in the probes;
the rest is inferred until its live check passes.

## Calls

Paths are under `v2/{projectId}`. "Success" is the status the code
accepts: the probe's status where the source says "live", else the
documented one until the live check records it.

| Call | Method and path | Success | Source |
|-|-|-|-|
| Get server group | `GET /serverGroups/{id}` | 200 | Live |
| Create server group | `POST /serverGroups` | 201 | Live |
| Update server group | `PUT /serverGroups/{id}` | 200 | Live |
| Delete server group | `DELETE /serverGroups/{id}` | 204 | Live |
| Create VPC | `POST /networks` | 200 | Live |
| Rename VPC | `PATCH /networks/{vpcId}` | 200 | Live |
| Delete VPC | `DELETE /networks/{vpcId}` | 200 | Live |
| Enable Private DNS | `PATCH /networks/{vpcId}/enableDns` | 200 | Live |
| Create subnet | `POST /networks/{vpcId}/subnets` | 200 | Live |
| Rename subnet | `PATCH /networks/{vpcId}/subnets/{subnetId}` | 200 | Live |
| Delete subnet | `DELETE /networks/{vpcId}/subnets/{subnetId}` | 200 | Live |
| Servers in a subnet | `GET /servers/subnets/{subnetId}` | 200, a bare array | Live |
| Get route table | `GET /route-table/{id}` | 200 | Live |
| Create route table | `POST /route-table` | 202 | Live |
| Replace routes | `PUT /route-table/{id}/routes` | 200 | Docs |
| Delete route table | `DELETE /route-table/{id}` | 202 | Live |
| Get ACL | `GET /network-acl/{id}` | 200 | Live |
| Create ACL | `POST /network-acl` | 201 | Live |
| Replace ACL rules | `PUT /network-acl/{id}/rules` | 200 | Docs |
| Replace ACL subnets | `PUT /network-acl/{id}/subnets` | 200 | Docs |
| Delete ACL | `DELETE /network-acl/{id}` | 204 | Live |

The reference marks a `portal-user-id` header required; the reads work
without it, and so must the writes.

## Bodies

Every create and update body also takes `tags` and `zoneId`; the SDK sends
neither, except `zoneId` on subnet create.

- Server group create (live): `name`, `policyId`, `description`. Update
  (live): `name`, `description`, and `serverGroupId` set to the path ID;
  without it the server returns 400 `serverGroupId: must not be null;`.
- VPC create (live): `name`, `cidr`. A `zoneId` is ignored. Rename (live):
  `name`.
- Subnet create (live): `name`, `cidr`, and `zoneId` naming a zone enabled
  for the account; `secondarySubnetRequests` is never sent. Rename (live):
  `name`.
- Route table create (live): `name`, `networkId` (the VPC), and optional
  `routes`, which the SDK leaves out. Route replace (inferred): `routes`,
  the whole list, each `destinationCidrBlock` and `target`.
- ACL create (live): `name`, `vpc`. Rules replace (inferred): `aclId` and
  `detailAclRuleList`, the whole list, each `type`, `seqNumber`,
  `protocol`, `port` (a string), `source`, `action`, `system`, and
  `interfaceAclPolicyUuid`. Subnets replace (inferred): `aclId` and
  `subnetUuids`, the whole list.

## Responses

All live, except the replace calls.

- Server group get, create, and update return `data` with `uuid`
  (`server-group-...`), an integer `serverGroupId`, `name`, `description`,
  `policyId`, and `createdAt`, and no `servers`. The list returns
  `servers`.
- VPC create, rename, and enable return `data` holding the VPC: `id`
  (`net-...`), `status`, `displayName`, `cidr`, `zone`, `dnsStatus`, `mtu`,
  `dhcpOptionId`, `routeTableId`, and more. The VPC get returns it at the
  top level, as `GetVPC` decodes today.
- Subnet create and rename return `data` holding the subnet: `uuid`
  (`sub-...`), `status`, `cidr`, `networkUuid`, `name`, `zone`,
  `secondarySubnets`, `routeTableUuid`, `interfaceAclPolicyUuid`, and
  more. The subnet get returns it at the top level.
- Route table create returns a flat `{"uuid": "rt-..."}`. The get returns
  `data` with `uuid`, `name`, `status`, `networkId`, `createdAt`, and
  `routes`, empty on a new table.
- ACL create returns `data` with `uuid` (`netPolicy-...`), `name`,
  `status` (`ACTIVE`), `defaultAcl` (false), `interfaceNetworkUuid` (the
  VPC), `projectUuid`, and `createdAt`. The ACL get adds `aclPolicyRules`
  and `subnetAssociationList`.

## Reads after a delete

| Resource | Get after delete | Repeat delete |
|-|-|-|
| Server group | 200 with `data` null | Not probed |
| VPC | 404, 3 to 42 s after the delete | Not probed |
| Subnet | 200 with status `DELETED` for more than 5 minutes; gone from the VPC's subnet list | 500 |
| Route table | 404 `Route Table with uuid <id> not found`, about 5 s after | Not probed |
| ACL | 500 | Not probed |

Each create decodes into a private response type and maps it to the public
model, as security group create does, so a field type that differs from
the read model cannot fail the decode.

## Server rules (from the product docs)

- A VPC is one `/16` from `10.0.0.0/8`, `172.16.0.0` to `172.24.0.0`, or
  `192.168.0.0/16`. A subnet is a `/24` or `/28` inside it.
- Live: every VPC lands in the region's first zone (`HCM03-1A` in
  `hcm-3`), whatever `zoneId` says, even when that zone is disabled for
  the account, as it is for the test account. A subnet create without
  `zoneId` looks up that zone and fails with 404 `Cannot get zone with id
  HCM03-1A`; with an enabled zone from `portal list-zones` it succeeds.
- Enabling Private DNS reserves `/28` subnets for vDNS. Live: those
  subnets do not appear in the VPC's subnet list, and the VPC deletes
  normally afterwards. The API has no disable call.
- Deleting a VPC deletes its ACLs and route tables. It fails while
  servers, load balancers, or other interfaces use the VPC. Live: it fails
  with 400 `Cannot delete this VPC because it contains the subnet.` while
  a subnet is listed, and kept failing after a deleted subnet left the
  list; it succeeded about 11 minutes after the subnet delete.
- A subnet can be deleted only when no resource uses it.
- Each VPC has a main route table. Only routes a user added can change.
- The docs say a new ACL has two default deny rules (inbound and
  outbound) that cannot change or be deleted, and list an allow-all rule
  per direction. Live: a new ACL has at least an inbound rule with
  `seqNumber` 0, `protocol` `ANY`, `port` `"0-65535"`, `source`
  `0.0.0.0/0`, and `action` `pass`; the rest of its default list was not
  captured. Rules are evaluated by priority, lowest first, up to 32766,
  and a priority is unique in an ACL. Protocols are `ANY`, `TCP`, `UDP`,
  and `ICMP`.
- A subnet belongs to at most one ACL. Associating it with another ACL
  moves it. An ACL with subnets cannot be deleted.
- Route table and ACL names are 5 to 50 of `a-z A-Z 0-9 _ -`.
- A server group's policy cannot change after create. Server group names
  are unique (live: a duplicate create returns 400); a group with servers
  is refused (`server group is in use`).
- Live quota on the test account: 2 VPCs, 5 subnets, 5 server groups, 100
  routes, and 10 routes per route table.

## Waits

Terraform waits for `ACTIVE` after VPC, subnet, and route table create,
for `ACTIVE` after a routes replace (from `UPDATING`), and for 404 after
VPC, subnet, and route table delete. It does not wait after a server group
write. Probe times:

| Write | Seen |
|-|-|
| VPC create, `CREATING` to `ACTIVE` | 3 to 5 s |
| Subnet create, `CREATING` to `ACTIVE` | About 1 s |
| Route table create to `ACTIVE`; delete to 404 | About 5 s each |
| VPC delete to 404 | 3 to 42 s |
| Private DNS enable, `ENABLING` to `ENABLED` | 5 min 36 s |
| Server group delete, the `DELETE` call itself | About 5 s |

## Cost

No pricing page, calculator item, or quote resource type names server
groups, VPCs, subnets, route tables, or ACLs. The design treats all as
free, so ADR 0002 rule 8 (quote) does not apply. The
[cost probes](vserver-network-writes-checks.md#cost-probes) ran on an
account with a zero balance and no cost in the period, where a paid
action would fail; no call was refused for payment. The next day's bill
confirms it before any release's code merges.
