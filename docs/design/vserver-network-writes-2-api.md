# vServer Network Writes 2: API

Status: Accepted (2026-09-27), with
[vServer network writes 2](vserver-network-writes-2.md).

The API shapes, server rules, and cost evidence behind
[vServer network writes 2](vserver-network-writes-2.md).

## Sources

Shapes come from these public sources, from read-only calls on the test
account in `hcm-3` on 2026-09-27 (no write sent), and from DHCP options set
create, attach, and detach writes made through the GreenNode web console on
2026-09-28.

- The vServer API reference on `docs.api.greennode.ai`
  (`service-docs/vserver.html`): the DHCP option, virtual IP address, and
  tag operations.
- VNG Cloud's Go SDK (`vngcloud/vngcloud-go-sdk`): tag calls in
  `services/compute/v2` and `services/volume/v2`, and the resource types in
  `services/server/v1`.
- VNG Cloud's Terraform provider (`vngcloud/terraform-provider-vngcloud`,
  `resource/vserver/resource_vip.go`): virtual IP create, update, and
  delete.
- Product docs on `docs.greennode.ai`: DHCP Options Sets, DNS Server IP
  Address, Virtual IP, and VIP Mode.

A shape marked "live" was seen in a read-only call or a console write; the
rest is from the reference until its own live check records it.

## Calls

Paths are under `v2/{projectId}` on the IAM vServer gateway. The reference
marks a `portal-user-id` header required; the reads work without it.

| Call | Method and path | Success | Source |
|-|-|-|-|
| List DHCP sets | `GET /dhcp_option` | 200, paged `listData` | Live |
| Get DHCP set | `GET /dhcp_option/{id}` | 200, the set at the top level | Live |
| Create DHCP set | `POST /dhcp_option` | 201, `data` | Live |
| Delete DHCP set | `DELETE /dhcp_option/{id}` | 204 | Live |
| Set a VPC's DHCP set | `PATCH /networks/{vpcId}/updateDhcpOption` | 200, `data` holding the VPC | Live |
| Create virtual IP | `POST /virtualIpAddress` | 201, `data` | Docs |
| Update virtual IP | `PUT /virtualIpAddress/{id}` | 200, `data` | Docs |
| Delete virtual IP | `DELETE /virtualIpAddress/{id}` | 204 | Docs |
| List a resource's tags | `GET /tag/resource/{resourceId}` | 200, a bare array | Live |
| Write a resource's tags | `PUT /tag/resource/{resourceId}` | 200, a bare array | Docs |
| Tag quota | `GET /tag/quota` | 200 | Live |

The reference lists only 200 or 201, 401, and 500 for each call; the live
checks record the 4xx answers.

## Bodies

- DHCP set create: `name` (required, live: 5 to 50 characters of letters,
  digits, `_`, and `-`), `dnsServers` (a list of addresses; live: the
  console's form fills in the region's two default resolvers), `mtu`
  (integer), `tags`, `zoneId`. The SDK sends `name`, `dnsServers`, and `mtu`
  when set.
- Set a VPC's DHCP set: `dhcpOptionId` (required), `tags`, `zoneId`. The
  SDK sends `dhcpOptionId` only.
- Detach a VPC's DHCP set: live, the console's Detach action sends the same
  `PATCH` with an empty body (`{}`, no `dhcpOptionId`); the response VPC
  comes back with `dhcpOptionId` and `dhcpOptionName` both null.
- Virtual IP create: `subnetId`, `name`, and `mode` (required), `ipAddress`,
  `description`. `mode` is `Active/Active` or `Active/Passive`. Terraform
  sends only `name`, `description`, and `subnetId`, so the server may
  accept a create without `mode`; the live check records it.
- Virtual IP update: `mode` (required), `name`, `description`.
- Tag write: `resourceId` and `resourceType` (required), and
  `tagRequestList`, each `key` and `value`. The reference example uses
  `resourceType` `Server`; VNG Cloud's Go SDK sends `SERVER`, `VOLUME`, and,
  on the vLB gateway, `LOAD-BALANCER`. The body also lists `tags` and
  `zoneId`, which the SDK does not send. The Go SDK's tag model has an
  optional `isEdited` boolean whose effect is unknown; the SDK does not send
  it.

## Responses

- A DHCP set (live) has `uuid` (`dop-...`), `name`, `status` (`ACTIVE`),
  `dnsServers`, `mtu` (1450), `associatedNetworks` (VPC IDs), `createdAt`,
  and `updatedAt`. The list pages as the other `network` lists do.
- The VPC read (live) carries `dhcpOptionId` and `dhcpOptionName`: set on
  a VPC with Private DNS, empty on one without.
- A virtual IP has `uuid`, `name`, `ipAddress`, `networkId`, `subnetId`,
  `description`, `mode`, `type`, `status`, `addressPairIps`, `zone`, and
  CIDR and name fields, as `VirtualIPAddress` decodes today.
- A tag (live for the read, on VPC IDs) is `key`, `value`, `systemTag`, and
  `createdAt`. The read returns 200 with `[]` for a VPC with no tags; that
  shows the read accepts any ID, not that a write accepts a VPC type.
- The tag quota (live) is one row, `TAG_PER_RESOURCE`, limit 10, type
  `Server`.

## Server rules

From the product docs, unless marked live:

- A DHCP set holds at most 4 DNS server addresses. The region's default
  resolvers are `10.166.12.196` and `10.166.12.197` in HCM and
  `10.236.10.196` and `10.236.10.197` in HAN; without them, some platform
  services may not resolve.
- A user may create at most 10 DHCP sets, and the limit cannot be raised.
- A set belongs to its region, may serve many VPCs, and a VPC has at most
  one set. A set must be detached from every VPC before delete. Live: the
  console's own delete confirmation states that deleting an attached set
  detaches it from every associated VPC automatically; this was not
  exercised against a live delete of an attached set, and the SDK keeps its
  stricter guard (refuse while attached) rather than rely on it.
- New servers use the VPC's set; existing servers pick it up after a
  reboot or a DHCP renew (`dhclient`, `ipconfig /renew`).
- Live: enabling Private DNS on a VPC creates and attaches a set named
  `dhcp-option-dns-<n>` with one resolver. Deleting the VPC leaves the set,
  unattached and `ACTIVE`.
- A virtual IP serves servers in its own subnet only; address pairs bind
  it to server interfaces. The Active/Passive mode suits Keepalived
  failover.
- Live quotas on the test account: 3 virtual IPs, and 10 tags per
  resource. No quota row names DHCP sets.

## Cost

- DHCP sets and tags: no pricing page, calculator item, quote type, or
  package names them, and neither body has a size or period. The design
  treats both as free; the next day's bill confirms it.
- Private virtual IPs: unknown. The pricing API accepts these resource
  types (live): `volume`, `server`, `elastic-ip`, `image`, `container`,
  `load-balancer`, `mp-server`, `snapshot`, `bandwidth`, `public-vip`, and
  AI Platform types. None is a private virtual IP. A `public-vip` quote
  returns 120,000 VND a month whatever `resourceInfo.type` says, including
  `private`, so it cannot price a private one. Public virtual IPs have their
  own create call (`POST public-vips`) with a `type` of `public-vm` or
  `public-mkp`, which suggests the private create is the free base, but no
  source says so. The
  [cost probe](vserver-network-writes-2-checks.md#probes-the-manager-runs)
  decides.

## Tag resource types

No public source shows a free resource type accepted by the tag write:

- The reference example is `Server`; VNG Cloud's Go SDK names `SERVER`,
  `VOLUME`, and `LOAD-BALANCER`, all paid.
- The create bodies for VPCs, subnets, DHCP sets, and route tables take
  `tags`, so the server stores tags for those resources under some type.
- VNG Cloud's Go SDK comments that the tag `PUT` upserts by key and leaves
  unlisted keys alone; the survey read the reference as a full replace.

The [type probe](vserver-network-writes-2-checks.md#probes-the-manager-runs)
settles both.
