# vServer Network Writes 2: API

Status: Accepted (2026-09-27), with
[vServer network writes 2](vserver-network-writes-2.md).

The API shapes, server rules, and cost evidence behind
[vServer network writes 2](vserver-network-writes-2.md).

## Sources

Shapes come from these public sources, and from read-only calls on the
test account in `hcm-3` on 2026-09-27. On 2026-09-28, the tag write itself
was checked on `hcm-3` by hand through the GreenNode web console, adding,
editing, and removing a tag on a private virtual IP address, a free
resource.

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

A shape marked "live" was seen in the read-only calls or the console check;
the rest is from the reference until its live check records it.

## Calls

Paths are under `v2/{projectId}` on the IAM vServer gateway. The reference
marks a `portal-user-id` header required; the reads work without it.

| Call | Method and path | Success | Source |
|-|-|-|-|
| List DHCP sets | `GET /dhcp_option` | 200, paged `listData` | Live |
| Get DHCP set | `GET /dhcp_option/{id}` | 200, the set at the top level | Live |
| Create DHCP set | `POST /dhcp_option` | 201, `data` | Docs |
| Delete DHCP set | `DELETE /dhcp_option/{id}` | 204 | Docs |
| Set a VPC's DHCP set | `PATCH /networks/{vpcId}/updateDhcpOption` | 200, `data` holding the VPC | Docs |
| Create virtual IP | `POST /virtualIpAddress` | 201, `data` | Docs |
| Update virtual IP | `PUT /virtualIpAddress/{id}` | 200, `data` | Docs |
| Delete virtual IP | `DELETE /virtualIpAddress/{id}` | 204 | Docs |
| List a resource's tags | `GET /tag/resource/{resourceId}` | 200, a bare array | Live |
| Write a resource's tags | `PUT /tag/resource/{resourceId}` | 200, a bare array of the user tags | Live |
| Tag quota | `GET /tag/quota` | 200 | Live |

The reference lists only 200 or 201, 401, and 500 for each call; the live
checks record the 4xx answers.

## Bodies

- DHCP set create: `name` (required), `dnsServers` (a list of addresses),
  `mtu` (integer), `tags`, `zoneId`. The SDK sends `name`, `dnsServers`,
  and `mtu` when set.
- Set a VPC's DHCP set: `dhcpOptionId` (required), `tags`, `zoneId`. The
  SDK sends `dhcpOptionId` only.
- Virtual IP create: `subnetId`, `name`, and `mode` (required), `ipAddress`,
  `description`. `mode` is `Active/Active` or `Active/Passive`. Terraform
  sends only `name`, `description`, and `subnetId`, so the server may
  accept a create without `mode`; the live check records it.
- Virtual IP update: `mode` (required), `name`, `description`.
- Tag write: `resourceId` and `resourceType` (required), and
  `tagRequestList`, each `key` and `value`. The reference example uses
  `resourceType` `Server`; VNG Cloud's Go SDK sends `SERVER`, `VOLUME`, and,
  on the vLB gateway, `LOAD-BALANCER`, all paid. The console check confirms
  `VIRTUAL-IP-ADDRESS` as a free `resourceType` the write accepts. The body
  also lists `tags` and `zoneId`, which the SDK does not send. `tagRequestList`
  replaces the resource's whole user tag list (live): the console sends
  `isEdited` on the changed tag, but an empty list still clears every user
  tag, so the replace does not depend on it, and the SDK does not send it.

## Responses

- A DHCP set (live) has `uuid` (`dop-...`), `name`, `status` (`ACTIVE`),
  `dnsServers`, `mtu` (1450), `associatedNetworks` (VPC IDs), `createdAt`,
  and `updatedAt`. The list pages as the other `network` lists do.
- The VPC read (live) carries `dhcpOptionId` and `dhcpOptionName`: set on
  a VPC with Private DNS, empty on one without.
- A virtual IP has `uuid`, `name`, `ipAddress`, `networkId`, `subnetId`,
  `description`, `mode`, `type`, `status`, `addressPairIps`, `zone`, and
  CIDR and name fields, as `VirtualIPAddress` decodes today.
- A tag (live) is `key`, `value`, `systemTag`, and `createdAt`. The read
  returns 200 with `[]` for a resource with no user tags; on a virtual IP
  address it also lists three system tags (`vng.zone`, `vng.region`,
  `vng.createdBy`, `systemTag` true, `createdAt` null), which the user
  cannot edit. The write's 200 response is a bare array of the user tags
  only: the system tags are never in the request and never in this
  response, and a live check confirms they are unchanged on the resource
  afterward.
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
  one set. A set must be detached from every VPC before delete.
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

The console check confirms `VIRTUAL-IP-ADDRESS` as a free `resourceType`
the tag write accepts, on a private virtual IP address:

- The reference example is `Server`; VNG Cloud's Go SDK names `SERVER`,
  `VOLUME`, and `LOAD-BALANCER`, all paid, none tried.
- The create bodies for VPCs, subnets, DHCP sets, and route tables take
  `tags`, so the server stores tags for those resources under some type,
  not checked here.
- The tag `PUT` replaces the resource's whole user tag list, confirmed
  live: sending an empty `tagRequestList` clears every user tag, and the
  system tags stay exactly as they were before the write. VNG Cloud's Go
  SDK's own comment, that the write upserts by key, does not match this.
