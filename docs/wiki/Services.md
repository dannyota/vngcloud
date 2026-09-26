# Services

Each GreenNode product is its own package, for example
`danny.vn/vngcloud/compute`. Every package has `New(cfg vngcloud.Config)
*Client`, built from the same `Config` other services use, and read methods
shaped `Method(ctx, *MethodInput) (*MethodOutput, error)`. Most methods are
reads. A method can still return a permission error when the IAM User
lacks access to that product or region. A required Input field is marked
"required"; passing it empty, or passing a nil Input when a field is
required, returns `vngcloud.ErrInvalidInput` before any request.

`billing` and `pricing` cover writes too: budgets can be created, changed,
paused, and deleted. See [Billing and Pricing](Billing-and-Pricing.md).
`dns` covers hosted zone writes too: zones can be created, changed, and
deleted. See [DNS](DNS.md).

## Coverage

| Product | Package | Coverage | Model Shape | Notes |
|---|---|---|---|---|
| Project | `project` | Project listing for the configured region | Typed | Used by optional project discovery. |
| Portal | `portal` | User info, zones, quota usage, quota detail, tag quota | Map-backed | Useful for account and quota metadata. |
| Compute | `compute` | Servers, server detail, SSH keys, placement groups, placement policies, images | Typed | Some methods flatten nested data already returned by list APIs. |
| Volume | `volume` | Volumes, volume detail, underlying volume, snapshots, volume types, type zones, encryption types | Typed | Includes a convenience method for walking snapshots. |
| Network | `network` | VPCs, subnets, WAN IPs, interfaces, security groups, rules, virtual IPs, address pairs, routes, peerings, ACLs, interconnects, endpoints | Typed | Some methods discover VNetwork region metadata before reading resources. |
| Load Balancer | `loadbalancer` | Load balancers, listeners, pools, health monitors, pool members, policies, tags, packages, certificates | Typed | Requires IAM User permissions for the target load balancer resources. |
| Global Load Balancer | `globalloadbalancer` | Packages, regions, load balancers, listeners, pools, pool members, usage history | Typed | Catalog methods do not require project selection. |
| DNS | `dns` | Hosted zones and records, plus zone writes | Typed | Not project-scoped like regional compute resources; see [DNS](DNS.md) for writes and waits. |
| Container Registry | `containerregistry` | Repositories and users | Map-backed | Map-backed until the API surface is stable enough for typed structs. |

## Project

```go
projectClient := project.New(cfg)
projectClient.ListProjects(ctx, in) // Region (optional; defaults to cfg's region)
```

Every other service discovers its project the same way when `Config` has no
`ProjectID` set and exactly one project matches the region.

## Portal

```go
portalClient := portal.New(cfg)
portalClient.GetUserInfo(ctx, nil)
portalClient.ListZones(ctx, nil)
portalClient.ListQuotaUsed(ctx, nil)
portalClient.GetQuota(ctx, in)     // Name (required)
portalClient.GetTagQuota(ctx, nil)
```

Portal models are map-backed so the SDK preserves returned fields without
requiring a breaking model update when the portal payload changes.

## Compute

```go
computeClient := compute.New(cfg)
computeClient.ListServers(ctx, in)              // Page, Size
computeClient.GetServer(ctx, in)                // ServerID (required)
computeClient.ListSSHKeys(ctx, in)              // Name, Page, Size
computeClient.ListServerGroups(ctx, in)         // Name, Page, Size
computeClient.ListServerSecurityGroups(ctx, nil)
computeClient.ListServerGroupMembers(ctx, nil)
computeClient.ListServerGroupPolicies(ctx, nil)
computeClient.ListOSImages(ctx, in)             // ZoneID
computeClient.ListGPUImages(ctx, nil)
computeClient.ListUserImages(ctx, in)           // Page, Size
```

`ListServerSecurityGroups` and `ListServerGroupMembers` flatten nested data
already returned by server and server-group list APIs. They do not require
extra API calls.

## Volume

```go
volumeClient := volume.New(cfg)
volumeClient.ListVolumes(ctx, in)          // Name, Page, Size
volumeClient.GetVolume(ctx, in)            // VolumeID (required)
volumeClient.GetUnderlyingVolume(ctx, in)  // VolumeID (required)
volumeClient.ListVolumeTypeZones(ctx, in)  // ZoneID
volumeClient.ListVolumeTypes(ctx, in)      // VolumeTypeZoneID
volumeClient.GetVolumeType(ctx, in)        // VolumeTypeID (required)
volumeClient.GetDefaultVolumeType(ctx, nil)
volumeClient.ListEncryptionTypes(ctx, nil)
volumeClient.ListSnapshots(ctx, in)        // VolumeID (required), Page, Size
volumeClient.ListAllSnapshots(ctx, nil)
```

`ProjectID` is optional in `Config`. Volume methods discover the project for
the configured region when needed.

`ListAllSnapshots` is a convenience method that walks visible volumes and
returns their snapshots.

## Network

`network` imports `compute` for the `compute.Server` model, since
`ListServersBySecurityGroup` returns full server objects.

```go
networkClient := network.New(cfg)
networkClient.ListVNetworkRegions(ctx, nil)
networkClient.ListVPCs(ctx, in)                            // Name, Page, Size
networkClient.GetVPC(ctx, in)                               // VPCID (required)
networkClient.ListWANIPs(ctx, in)                           // Name, Page, Size
networkClient.ListNetworkInterfaces(ctx, in)                // Name, Page, Size
networkClient.ListSecurityGroups(ctx, in)                   // Name, Page, Size
networkClient.GetSecurityGroup(ctx, in)                     // SecurityGroupID (required)
networkClient.ListServersBySecurityGroup(ctx, in)           // SecurityGroupID (required)
networkClient.ListVirtualIPAddresses(ctx, in)               // Name, Page, Size
networkClient.ListRouteTables(ctx, in)                      // Name, Page, Size
networkClient.ListPeerings(ctx, in)                         // Name, Page, Size
networkClient.ListNetworkACLs(ctx, in)                      // Name, Page, Size
networkClient.ListInterconnects(ctx, in)                    // Name, Page, Size
networkClient.ListSubnets(ctx, nil)
networkClient.ListSubnetsByVPC(ctx, in)                     // VPCID (required)
networkClient.GetSubnet(ctx, in)                            // VPCID, SubnetID (both required)
networkClient.ListSecurityGroupRules(ctx, in)               // SecurityGroupID (required)
networkClient.ListAllSecurityGroupRules(ctx, nil)
networkClient.ListRouteTableRoutes(ctx, nil)
networkClient.GetVirtualIPAddress(ctx, in)                  // VirtualIPAddressID (required)
networkClient.ListAddressPairsByVirtualIPAddress(ctx, in)   // VirtualIPAddressID (required)
networkClient.ListAddressPairsByVirtualSubnet(ctx, in)      // VirtualSubnetID (required)
networkClient.ListAllVirtualIPAddressAddressPairs(ctx, nil)
networkClient.ListEndpoints(ctx, in)                        // ZoneID, VPCID, UUID, Page, Size
networkClient.GetEndpoint(ctx, in)                          // EndpointID (required)
networkClient.ListEndpointTags(ctx, in)                     // EndpointID (required)
```

`ListEndpoints` and `GetEndpoint` discover VNetwork region metadata when
needed before reading endpoint resources.

The SDK has no method for network ACL rules or for a single network
interface.

## Load Balancing

Regional Load Balancer APIs are in `loadbalancer`.

```go
lbClient := loadbalancer.New(cfg)
lbClient.ListLoadBalancers(ctx, in)         // Name, Page, Size
lbClient.GetLoadBalancer(ctx, in)           // LoadBalancerID (required)
lbClient.ListListeners(ctx, in)             // LoadBalancerID (required)
lbClient.GetListener(ctx, in)               // LoadBalancerID, ListenerID (both required)
lbClient.ListPools(ctx, in)                 // LoadBalancerID (required)
lbClient.GetPool(ctx, in)                   // LoadBalancerID, PoolID (both required)
lbClient.GetPoolHealthMonitor(ctx, in)      // LoadBalancerID, PoolID (both required)
lbClient.ListPoolMembers(ctx, in)           // LoadBalancerID, PoolID (both required)
lbClient.ListPolicies(ctx, in)              // LoadBalancerID, ListenerID (both required)
lbClient.GetPolicy(ctx, in)                 // LoadBalancerID, ListenerID, PolicyID (all required)
lbClient.ListTags(ctx, in)                  // LoadBalancerID (required)
lbClient.ListPackages(ctx, in)              // ZoneID
lbClient.ListCertificates(ctx, in)          // Name, Page, Size
lbClient.GetCertificate(ctx, in)            // CertificateID (required)
```

Global Load Balancer APIs are in `globalloadbalancer`.

```go
glbClient := globalloadbalancer.New(cfg)
glbClient.ListPackages(ctx, nil)
glbClient.ListRegions(ctx, nil)
glbClient.ListLoadBalancers(ctx, in)   // Name, Offset, Limit
glbClient.GetLoadBalancer(ctx, in)     // LoadBalancerID (required)
glbClient.ListPools(ctx, in)           // LoadBalancerID (required)
glbClient.ListListeners(ctx, in)       // LoadBalancerID (required)
glbClient.GetListener(ctx, in)         // LoadBalancerID, ListenerID (both required)
glbClient.ListPoolMembers(ctx, in)     // LoadBalancerID, PoolID (both required)
glbClient.GetPoolMember(ctx, in)       // LoadBalancerID, PoolID, PoolMemberID (all required)
glbClient.ListUsageHistories(ctx, in)  // LoadBalancerID (required), From, To, Type
```

Package and region catalog methods are global metadata calls. Inventory and
nested resource methods require IAM User access to the target resources.

## DNS

```go
dnsClient := dns.New(cfg)
dnsClient.ListHostedZones(ctx, in)  // Name, Page, Size
dnsClient.GetHostedZone(ctx, in)    // HostedZoneID (required)
dnsClient.ListRecords(ctx, in)      // HostedZoneID (required), Name
dnsClient.GetRecord(ctx, in)        // HostedZoneID, RecordID (both required)
```

DNS APIs are not project-scoped in the same way as regional compute
resources. They may still require IAM User permissions for the DNS product.

Zone writes (`CreateHostedZone`, `UpdateHostedZone`, `DeleteHostedZone`) and
their waits are on the [DNS](DNS.md) page.

## Container Registry

```go
vcrClient := containerregistry.New(cfg)
vcrClient.ListRepositories(ctx, in)  // AccessLevel
vcrClient.ListUsers(ctx, in)         // Name, Page, Size
```

Repository and user items are map-backed, so the SDK keeps every field the
API returns.
