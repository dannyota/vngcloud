# Wire Model Evidence

This index records the evidence for SDK model shapes. A fixture shows what
our decoder reads. It does not establish what the service returns unless
its capture provenance is recorded. Published schemas, empty responses,
and synthetic tests do not establish populated live behavior. Listed
fields do not imply presence, nullability, or completeness guarantees.

## Billing

`CostResourceSummary` is the `data.summary` object on
[the cost explorer resource list](billing.md#cost-explorer).
[The fixture](../../testdata/billing/ListCostResources.json) shows numeric
`totalAll` and `avgPerResource` and integer `activeCount`. The summary's
calculation and the fixture's raw provenance are not established.

## Compute

- `NetworkInterface`: [server response evidence](vserver-floating-ips-api.md#responses)
  records the published interface keys. The
  [sanitized server response](../../testdata/server/instance.json) has
  populated internal interfaces. The floating IP fixture is schema-built
  and does not establish live external interface fields.
- `ServerSecgroup`: the same sanitized server response shows `secGroups`
  entries with `name` and `uuid`.
- `ServerGroupMember`: [server group response evidence](vserver-network-writes-api.md#responses)
  records a `servers` list on the list route. The
  [list fixture](../../testdata/compute/list_server_groups.json) shows
  `listData[].servers[]` with `name` and `uuid`; populated raw provenance
  is not recorded.
- `ServerGroupPolicy`: the
  [policy fixture](../../testdata/compute/list_server_group_policies.json)
  shows `data[]` with `uuid`, `name`, `status`, `description`, and
  `descriptionVi` for the project server group policies read. Populated
  raw provenance and the `descriptions` field are unverified.
- `ServerSecurityGroup` and `ServerGroupMembership` are SDK projections,
  not separate wire types. They join child `name` and `uuid` to the parent
  server or server-group ID in [the aggregate reads](../../compute/aggregates.go).
- `UserImage`: [the project user image fixture](../../testdata/compute/list_user_images.json)
  shows paged `listData` with `uuid`, `name`, and `status`. Populated raw
  provenance and the optional image metadata fields remain unverified.

## DNS

`VPCMapRegion` is the hosted zone `assocVpcMapRegion` child. The
[DNS design](dns.md#source) records the zone source. The
[create](../../testdata/dns/create_hosted_zone.json) and
[list](../../testdata/dns/list_hosted_zones.json) fixtures show `vpcId`
and `region`; the get fixture does not establish that the list is always
present.

## Global load balancer

The [GLB read inventory](cli-reads.md#globalloadbalancer) distinguishes
populated reads from envelope verification. The
[parent lifecycle](glb-create.md) and [child contract](glb-pool.md#wire-contract-and-evidence)
confirm Layer 4 behavior, not all optional read fields.

- `RegionalPackage`: `vlbPackages[]` is absent from the
  [package fixture](../../testdata/glb/list_packages.json); a synthetic
  child test exercises `region`. The other nested fields are unverified.
- `LoadBalancer`, `VIP`, and `Domain`: [list](../../testdata/glb/list_load_balancers.json)
  and [get](../../testdata/glb/get_load_balancer.json) fixtures show a paged
  parent list and a direct object with `vips[]` and `domains[]`. Live
  provenance for nested fields, DNS IDs, and timestamps is unrecorded.
- `Pool` and `PoolHealthMonitor`: the
  [pool fixture](../../testdata/glb/list_pools.json) shows an array with
  nested `health`. The child contract verifies TCP health read-back and
  records the upstream `intervalTime` key. Other protocols are unverified.
- `PoolMember` is the group and `PoolMemberDetail` is a backend row in its
  `members[]`. See [group list](../../testdata/glb/list_pool_members.json),
  [group get](../../testdata/glb/get_pool_member.json), and the child
  contract's private-backend evidence. The fixture's IP variant is not
  live evidence for that variant.
- `Listener`: [list](../../testdata/glb/list_listeners.json) and
  [get](../../testdata/glb/get_listener.json) fixtures show an array and a
  direct object. The child contract verifies TCP pool binding, CIDRs, and
  timeouts; optional headers and non-TCP shapes remain unverified.
- `UsageHistory`: [the fixture](../../testdata/glb/list_usage_histories.json)
  shows `items[]` with `timestamp`, `value`, and `type`. The read inventory
  records the envelope, not live metrics or time semantics.

## Regional load balancer

- `Node`: [the list fixture](../../testdata/loadbalancer/list_load_balancers.json)
  shows `nodes[]` with `status`, `zoneId`, `zoneName`, and `subnetId`.
  [Root object verification](cli-reads.md#loadbalancer) does not prove
  the nested row.
- `Tag`: [the resource tags fixture](../../testdata/loadbalancer/list_tags.json)
  shows a bare array with `key`, `value`, `createdAt`, and `systemTag`.
  Populated raw provenance is unrecorded.
- `ListenerInsertHeader`: [listener write evidence](lb-writes-api.md)
  names `insertHeaders`. The
  [listener test](../../loadbalancer/listeners_write_test.go) exercises
  `headerName` and `headerValue` with a synthetic response.
- `L7Rule`: [the policy detail fixture](../../testdata/loadbalancer/get_policy.json)
  shows read-side `data.l7Rules[]`. Write-side `rules` alone do not
  establish the read-side IDs or statuses; live row provenance is absent.

## Network

- `WANIP`: the [published WAN IP response](vserver-floating-ips-api.md#responses)
  names `WanIpDto`. Its SDK fixture differs and lacks raw provenance. The
  attached and unattached field semantics and join to `floatingIpId`
  remain unverified.
- `ElasticNetworkInterface` and `Peering`: the
  [network fixtures](../../testdata/network) show paged `listData` for
  elastic interfaces and peering. The interface rows describe IDs, fixed
  IP, and MAC; the peering rows name both VPCs and CIDRs. Live row
  provenance is not recorded.
- `Interconnect`: the [route test](../../network/network_test.go) covers
  an empty `/interconnects` response. It does not verify populated rows.
- `SubnetSecondarySubnet`: [subnet responses](vserver-network-writes-api.md#responses)
  record `secondarySubnets`. The populated `uuid`, `name`, and `cidr`
  example in the [subnet test](../../network/subnets_write_test.go) is
  synthetic.
- `AddressPair`: [VIP delete evidence](vserver-network-writes-2.md#virtual-ip-delete)
  establishes address-pair use. The
  [network fixtures](../../testdata/network) show `data[]` for VIP and
  virtual subnet address pairs. The latter includes VIP, subnet, and
  interface IDs and CIDR. Populated raw provenance is not recorded.

## Volume

`Snapshot` here is the block volume snapshot model, not a Backup Center
or snapshot policy model. The [volume read inventory](cli-reads.md#volume)
verifies the envelope. The
[fixture](../../testdata/volume/list_snapshots.json) shows paged `items`
with `id`, `name`, `volumeId`, and `status`. The
[fixture note](../../testdata/volume/guard-provenance.md) does not establish
populated provenance or the wider field shapes.
