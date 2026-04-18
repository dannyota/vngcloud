# Features

This page summarizes the public read-only surface. The SDK can only read
resources that the IAM User can access, so supported methods may still return
permission errors for products or regions outside the account's grants.

## Coverage

| Product | Coverage | Model Shape | Notes |
|---|---|---|---|
| Project | Project listing for the configured region | Typed | Used by optional project discovery. |
| Portal | User info, zones, quota usage, quota detail, tag quota | Map-backed | Useful for account and quota metadata. |
| Compute | Servers, server detail, SSH keys, placement groups, placement policies, images | Typed | Some helpers flatten nested data already returned by list APIs. |
| Volume | Volumes, volume detail, underlying volume, snapshots, volume types, type zones, encryption types | Typed | Includes convenience helpers for walking snapshots. |
| Network | VPCs, subnets, WAN IPs, interfaces, security groups, rules, virtual IPs, address pairs, routes, peerings, ACLs, interconnects, endpoints | Typed | Some methods discover VNetwork region metadata before reading resources. |
| Load Balancer | Load balancers, listeners, pools, health monitors, pool members, policies, tags, packages, certificates | Typed | Requires IAM User permissions for the target load balancer resources. |
| Global Load Balancer | Packages, regions, load balancers, listeners, pools, pool members, usage history | Typed | Catalog methods do not require project selection. |
| DNS | Hosted zones and records | Typed | DNS APIs are not project-scoped in the same way as regional compute resources. |
| Container Registry | Repositories and users | Map-backed | Map-backed until the API surface is stable enough for typed structs. |

## Scope

The SDK does not expose write APIs. Mutating operations such as create, update,
delete, attach, detach, resize, and migrate are intentionally out of scope.

Service account auth and root-user auth are not implemented.

## Compute

Compute APIs are exposed under `client.Compute`.

```go
client.Compute.ListServers(ctx, opts)
client.Compute.GetServer(ctx, id)
client.Compute.ListSSHKeys(ctx, opts)
client.Compute.ListServerGroups(ctx, opts)
client.Compute.ListServerSecurityGroups(ctx)
client.Compute.ListServerGroupMembers(ctx)
client.Compute.ListServerGroupPolicies(ctx)
client.Compute.ListOSImages(ctx, opts)
client.Compute.ListGPUImages(ctx)
client.Compute.ListUserImages(ctx, opts)
```

`ListServerSecurityGroups` and `ListServerGroupMembers` flatten nested data
already returned by server and server-group list APIs. They do not require extra
API calls.

## Volume

Volume APIs are exposed under `client.Volume`.

```go
client.Volume.ListVolumes(ctx, opts)
client.Volume.GetVolume(ctx, id)
client.Volume.GetUnderlyingVolume(ctx, id)
client.Volume.ListVolumeTypeZones(ctx, opts)
client.Volume.ListVolumeTypes(ctx, opts)
client.Volume.GetVolumeType(ctx, id)
client.Volume.GetDefaultVolumeType(ctx)
client.Volume.ListEncryptionTypes(ctx)
client.Volume.ListSnapshots(ctx, volumeID, opts)
client.Volume.ListAllSnapshots(ctx)
```

`ProjectID` is optional in client config. Volume methods discover the project for
the configured region when needed.

`ListAllSnapshots` is a convenience helper that walks visible volumes and returns
their snapshots.

## Network

Network APIs are exposed under `client.Network`.

```go
client.Network.ListVNetworkRegions(ctx)
client.Network.ListVPCs(ctx, opts)
client.Network.GetVPC(ctx, id)
client.Network.ListWANIPs(ctx, opts)
client.Network.ListNetworkInterfaces(ctx, opts)
client.Network.ListSecurityGroups(ctx, opts)
client.Network.GetSecurityGroup(ctx, id)
client.Network.ListServersBySecurityGroup(ctx, id)
client.Network.ListVirtualIPAddresses(ctx, opts)
client.Network.ListRouteTables(ctx, opts)
client.Network.ListPeerings(ctx, opts)
client.Network.ListNetworkACLs(ctx, opts)
client.Network.ListInterconnects(ctx, opts)
client.Network.ListSubnets(ctx)
client.Network.ListSubnetsByVPC(ctx, vpcID)
client.Network.GetSubnet(ctx, vpcID, subnetID)
client.Network.ListSecurityGroupRules(ctx, securityGroupID)
client.Network.ListAllSecurityGroupRules(ctx)
client.Network.ListRouteTableRoutes(ctx)
client.Network.GetVirtualIPAddress(ctx, id)
client.Network.ListAddressPairsByVirtualIPAddress(ctx, id)
client.Network.ListAddressPairsByVirtualSubnet(ctx, virtualSubnetID)
client.Network.ListAllVirtualIPAddressAddressPairs(ctx)
client.Network.ListEndpoints(ctx, opts)
client.Network.GetEndpoint(ctx, id)
client.Network.ListEndpointTags(ctx, id)
```

`ListEndpoints` discovers VNetwork region metadata when needed before reading
endpoint resources.

Not currently exposed:

- Network ACL rule listing. The SDK currently exposes `ListNetworkACLs`; it does
  not expose a separate rule API for ACL entries.
- Network interface detail lookup. The SDK currently exposes
  `ListNetworkInterfaces`; it does not expose `GetNetworkInterface`.

## Load Balancing

Regional Load Balancer APIs are exposed under `client.LoadBalancer`.

```go
client.LoadBalancer.ListLoadBalancers(ctx, opts)
client.LoadBalancer.GetLoadBalancer(ctx, id)
client.LoadBalancer.ListListeners(ctx, loadBalancerID)
client.LoadBalancer.GetListener(ctx, loadBalancerID, listenerID)
client.LoadBalancer.ListPools(ctx, loadBalancerID)
client.LoadBalancer.GetPool(ctx, loadBalancerID, poolID)
client.LoadBalancer.GetPoolHealthMonitor(ctx, loadBalancerID, poolID)
client.LoadBalancer.ListPoolMembers(ctx, loadBalancerID, poolID)
client.LoadBalancer.ListPolicies(ctx, loadBalancerID, listenerID)
client.LoadBalancer.GetPolicy(ctx, loadBalancerID, listenerID, policyID)
client.LoadBalancer.ListTags(ctx, loadBalancerID)
client.LoadBalancer.ListLoadBalancerPackages(ctx, opts)
client.LoadBalancer.ListCertificates(ctx, opts)
client.LoadBalancer.GetCertificate(ctx, id)
```

Global Load Balancer APIs are exposed under `client.GlobalLoadBalancer`.

```go
client.GlobalLoadBalancer.ListPackages(ctx)
client.GlobalLoadBalancer.ListRegions(ctx)
client.GlobalLoadBalancer.ListLoadBalancers(ctx, opts)
client.GlobalLoadBalancer.GetLoadBalancer(ctx, id)
client.GlobalLoadBalancer.ListPools(ctx, loadBalancerID)
client.GlobalLoadBalancer.ListListeners(ctx, loadBalancerID)
client.GlobalLoadBalancer.GetListener(ctx, loadBalancerID, listenerID)
client.GlobalLoadBalancer.ListPoolMembers(ctx, loadBalancerID, poolID)
client.GlobalLoadBalancer.GetPoolMember(ctx, loadBalancerID, poolID, poolMemberID)
client.GlobalLoadBalancer.ListUsageHistories(ctx, loadBalancerID, opts)
```

Package and region catalog methods are global metadata calls. Inventory and
nested resource methods require IAM User access to the target resources.

## DNS, Portal, And Container Registry

DNS APIs are exposed under `client.DNS`.

```go
client.DNS.ListHostedZones(ctx, opts)
client.DNS.GetHostedZone(ctx, id)
client.DNS.ListRecords(ctx, hostedZoneID, opts)
client.DNS.GetRecord(ctx, hostedZoneID, recordID)
```

DNS APIs are not project-scoped in the same way as regional compute resources.
They may still require IAM User permissions for the DNS product.

Portal APIs are exposed under `client.Portal`.

```go
client.Portal.GetUserInfo(ctx)
client.Portal.ListZones(ctx)
client.Portal.ListQuotaUsed(ctx)
client.Portal.GetQuota(ctx, name)
client.Portal.GetTagQuota(ctx)
```

Portal models are map-backed so the SDK preserves returned fields without
requiring a breaking model update when the portal payload changes.

Container Registry APIs are exposed under `client.ContainerRegistry`.

```go
client.ContainerRegistry.ListRepositories(ctx, opts)
client.ContainerRegistry.ListUsers(ctx, opts)
```

Repository and user items are currently map-backed models. This preserves fields
returned by the API while the SDK keeps a read-only, compatibility-friendly
surface.
