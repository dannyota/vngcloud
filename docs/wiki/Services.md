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
`dns` covers hosted zone and record writes too: zones and records can be
created, changed, and deleted. See [DNS](DNS.md). `loadbalancer` covers
load balancer writes too: a load balancer can be created, resized, and
deleted, with its pools, members, listeners, and L7 policies. See
[Load Balancer](LoadBalancer.md). `network` covers
security group and rule writes too: groups and rules can be created,
changed, and deleted. See [Network](Network.md). `compute` covers SSH key
writes too: a key can be imported, created, or deleted (see
[Compute](Compute.md)), and paid server writes: a server can be created,
started, stopped, rebooted, renamed, resized, and deleted (see
[Compute Servers](Compute-Servers.md)). `volume` covers paid volume
writes: a volume can be created, deleted, attached, detached, and resized.
See [Volume](Volume.md). `containerregistry` covers repository and repository
user writes too: a repository or a user can be created and deleted. See
[Container Registry](Container-Registry.md). `iam` covers IAM reads,
service account writes, policy writes, and group writes: a service account
can be created, updated, have its secret reset, and deleted; a customer
policy can be created, updated, deleted, and attached to or detached from
a service account, a group, or an IAM user; a group can be created,
updated, deleted, and have members added or removed. Every write is
guarded against changing the caller's own access or a principal that
already holds an IAM write right. See [IAM](#iam) below. `tagging` writes
tags on any resource type. See [Tagging](Tagging.md).

## Coverage

| Product | Package | Coverage | Model Shape | Notes |
|---|---|---|---|---|
| Project | `project` | Project listing for the configured region | Typed | Used by optional project discovery. |
| Portal | `portal` | User info, zones, quota usage, quota detail, tag quota | Map-backed | Useful for account and quota metadata. |
| Compute | `compute` | Servers, server detail, SSH keys plus SSH key writes, placement groups, placement policies, images, plus paid server writes | Typed | Some methods flatten nested data already returned by list APIs; see [Compute](Compute.md) for SSH key writes and [Compute Servers](Compute-Servers.md) for server writes. |
| Volume | `volume` | Volumes, volume detail, underlying volume, snapshots, volume types, type zones, encryption types, plus volume create and delete | Typed | Includes a convenience method for walking snapshots; see [Volume](Volume.md) for writes. |
| Network | `network` | VPCs, subnets, WAN IPs, interfaces, security groups, rules, virtual IPs, address pairs, routes, peerings, ACLs, interconnects, endpoints, plus security group and rule writes | Typed | Some methods discover VNetwork region metadata before reading resources; see [Network](Network.md) for writes and waits. |
| Load Balancer | `loadbalancer` | Load balancers, listeners, pools, health monitors, pool members, policies, tags, packages, certificates, plus certificate writes, create/resize price quotes, and load balancer, pool, listener, and policy writes | Typed | Requires IAM User permissions for the target load balancer resources; see [Load Balancer](LoadBalancer.md) for writes, the price guard, and waits. |
| Global Load Balancer | `globalloadbalancer` | Packages, regions, load balancers, listeners, pools, pool members, usage history | Typed | Catalog methods do not require project selection. |
| DNS | `dns` | Hosted zones and records, plus zone and record writes | Typed | Not project-scoped like regional compute resources; see [DNS](DNS.md) for writes and waits. |
| Container Registry | `containerregistry` | Repositories and users, plus repository and user create and delete | Typed | See [Container Registry](Container-Registry.md) for writes, waits, and secret handling. |
| IAM | `iam` | Caller identity, IAM users, IAM actions, policies, groups, service accounts, plus service account, policy, and group writes | Typed | Page numbers start at 0, unlike the rest of the SDK; see [IAM](#iam) below for writes and guards. |
| Tagging | `tagging` | Resource tag reads, plus tag writes | Typed | One tag API serves every resource type; see [Tagging](Tagging.md) for `TagResource` and its errors. |
| Storage | `storage` | vStorage regions and projects, buckets, bucket detail | Typed | Reads only; calls an undocumented console API. See [Storage](Storage.md). |

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
computeClient.ListFlavorZones(ctx, in)          // ZoneID
computeClient.ListFlavors(ctx, in)              // FlavorZoneID (required)
computeClient.QuoteCreateServer(ctx, in)        // *compute.CreateServerInput
```

`ListServerSecurityGroups` and `ListServerGroupMembers` flatten nested data
already returned by server and server-group list APIs. They do not require
extra API calls.

SSH key writes (`ImportSSHKey`, `CreateSSHKey`, `DeleteSSHKey`) and the
`vngcloud.Secret` a create returns are on the [Compute](Compute.md) page.
Server writes (`CreateServer`, `DeleteServer`, `StartServer`,
`StopServer`, `RebootServer`, `RenameServer`, `ResizeServer`) and their
price guard are on [Compute Servers](Compute-Servers.md).

`ListFlavorZones` filters the API's full flavor zone list to `Input.ZoneID`
itself; leave it unset to list every flavor zone. `QuoteCreateServer` prices
a server `compute.CreateServerInput` would create, without ordering it; see
[Billing and Pricing](Billing-and-Pricing.md#quoting-a-paid-write).

## Volume

```go
volumeClient := volume.New(cfg)
volumeClient.ListVolumes(ctx, in)          // Name, Page, Size
volumeClient.GetVolume(ctx, in)            // VolumeID (required)
volumeClient.GetUnderlyingVolume(ctx, in)  // VolumeID (required)
volumeClient.ListVolumesByServer(ctx, in)  // ServerID (required)
volumeClient.ListVolumeTypeZones(ctx, in)  // ZoneID
volumeClient.ListVolumeTypes(ctx, in)      // VolumeTypeZoneID
volumeClient.GetVolumeType(ctx, in)        // VolumeTypeID (required)
volumeClient.GetDefaultVolumeType(ctx, in) // ZoneID
volumeClient.ListEncryptionTypes(ctx, nil)
volumeClient.ListSnapshots(ctx, in)        // VolumeID (required), Page, Size
volumeClient.ListAllSnapshots(ctx, nil)
volumeClient.QuoteCreateVolume(ctx, in)    // *volume.CreateVolumeInput
```

Volume writes (`CreateVolume`, `DeleteVolume`, `AttachVolume`,
`DetachVolume`, `ResizeVolume`) and their price guard, waits, and errors
are on the [Volume](Volume.md) page.

`ProjectID` is optional in `Config`. Volume methods discover the project for
the configured region when needed.

`ListAllSnapshots` is a convenience method that walks visible volumes and
returns their snapshots. `ListVolumesByServer` lists the volumes attached to
one server, including its boot volume. `GetDefaultVolumeType`'s `ZoneID`
selects which zone's default to read; without it, the API looks up the
region's first zone, which can be disabled for the account.
`QuoteCreateVolume` prices a volume `volume.CreateVolumeInput` would create,
without ordering it; see
[Billing and Pricing](Billing-and-Pricing.md#quoting-a-paid-write).

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
networkClient.GetNetworkACL(ctx, in)                        // NetworkACLID (required)
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

The SDK has no method for a single network interface.

`network` also writes security groups and their rules, route tables and
routes, and network ACLs, their rules, and subnet associations; see
[Network](Network.md) for `CreateSecurityGroup`, `UpdateSecurityGroup`,
`DeleteSecurityGroup`, `CreateSecurityGroupRule`, `DeleteSecurityGroupRule`,
`CreateRouteTable`, `DeleteRouteTable`, `AddRoute`, and `RemoveRoute`, their
waits, and their errors, and [Network ACLs](Network-ACLs.md) for
`CreateNetworkACL`, `DeleteNetworkACL`, `AddNetworkACLRule`,
`RemoveNetworkACLRule`, `AssociateNetworkACLSubnet`, and
`DisassociateNetworkACLSubnet`.

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
lbClient.ImportCertificate(ctx, in)         // Name, Type, Certificate (all required); CertificateChain, PrivateKey, Passphrase
lbClient.DeleteCertificate(ctx, in)         // CertificateID (required)
lbClient.QuoteCreateLoadBalancer(ctx, in)   // *loadbalancer.CreateLoadBalancerInput
lbClient.QuoteResizeLoadBalancer(ctx, in)   // *loadbalancer.ResizeLoadBalancerInput
```

`QuoteCreateLoadBalancer` prices a load balancer `loadbalancer.CreateLoadBalancerInput`
would create, and `QuoteResizeLoadBalancer` prices a package change
`loadbalancer.ResizeLoadBalancerInput` describes, neither ordering anything;
see [Billing and Pricing](Billing-and-Pricing.md#quoting-a-paid-write).
Every path ID above, including one carried in a body such as `PackageID` or
`SubnetID`, is checked before any request; a malformed one, `..` or `/` for
example, returns `vngcloud.ErrInvalidInput`.

`ImportCertificate` sends `PrivateKey` and `Passphrase` to GreenNode, which
stores the certificate and never returns the key back; both fields are
`vngcloud.Secret`, so printing, logging, or JSON-encoding the Input gives
`[redacted]`. `Type` is `loadbalancer.CertificateTypeTLS` ("TLS/SSL") or
`loadbalancer.CertificateTypeCA` ("CA"); only `TLS/SSL` takes a key, chain,
or passphrase. A failing import never returns the server's own message: on
every failing status the returned error's message is fixed text
(`loadbalancer.ImportCertificateWithheldMessage`), since no pattern match
can be proven to catch every way a server might echo a rejected key or
passphrase back; the status and (still redacted) code still come from the
server. `ImportCertificate` is a POST and is never retried after a failure
that may have already reached the server; the error names `ListCertificates`
by name as the way to check what happened.
`DeleteCertificate` reads the certificate first and returns
`loadbalancer.ErrCertificateInUse`, sending nothing, when a listener still
uses it.

`loadbalancer` also creates, resizes, and deletes load balancers, and
creates, updates, and deletes their pools (with health monitors and
members), listeners, and L7 policies; see [Load Balancer](LoadBalancer.md)
for the price guard, busy handling, waits, and errors.

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

Zone writes (`CreateHostedZone`, `UpdateHostedZone`, `DeleteHostedZone`),
record writes (`CreateRecord`, `UpdateRecord`, `DeleteRecord`), and their
waits are on the [DNS](DNS.md) page.

## Container Registry

Repository and user reads and writes, their waits, and the registry secret
handling are on the [Container Registry](Container-Registry.md) page.

## IAM

```go
iamClient := iam.New(cfg)
iamClient.GetCallerIdentity(ctx, nil)
iamClient.ListUsers(ctx, in)                   // Page, Size
iamClient.ListActions(ctx, nil)
iamClient.ListServiceAccounts(ctx, in)         // Name, Page, Size
iamClient.GetServiceAccount(ctx, in)           // ServiceAccountID (required)
iamClient.ListPolicies(ctx, in)                // Name, Page, Size
iamClient.GetPolicy(ctx, in)                   // PolicyID (required)
iamClient.ListPolicyAttachments(ctx, in)       // PolicyID (required)
iamClient.ListGroups(ctx, nil)
iamClient.GetGroup(ctx, in)                    // GroupID (required)
iamClient.ListGroupPolicies(ctx, in)           // GroupID (required), Page, Size
iamClient.ListUserPolicies(ctx, in)            // UserID (required), Page, Size
iamClient.ListUserGroups(ctx, in)              // UserID (required)
iamClient.ListServiceAccountPolicies(ctx, in)  // ServiceAccountID (required), Page, Size
```

Policy and group reads run on the IAM console host; every other call,
including every service account call, runs on the dashboard host. Page
numbers start at 0 in `iam`, unlike the rest of the SDK: `Page: 0` is the
first page, and a non-positive `Size` sends `vngcloud.DefaultPageSize`.

`Policy.Managed()` reports whether a policy is one GreenNode manages: it
can be read and attached, but never updated or deleted.

`iam` also writes service accounts, policies, and groups; see
[IAM](IAM.md) for `CreateServiceAccount`, `UpdateServiceAccount`,
`ResetServiceAccountSecret`, `DeleteServiceAccount`, `CreatePolicy`,
`UpdatePolicy`, `DeletePolicy`, `AttachServiceAccountPolicy`,
`DetachServiceAccountPolicy`, `AttachUserPolicy`, `DetachUserPolicy`,
`CreateGroup`, `UpdateGroup`, `DeleteGroup`, `AddUserToGroup`,
`RemoveUserFromGroup`, `AttachGroupPolicy`, `DetachGroupPolicy`, their
guards, and their errors.

## Tagging

```go
taggingClient := tagging.New(cfg)
taggingClient.ListResourceTags(ctx, in)  // ResourceID (required)
```

`ListResourceTags` reads any resource's tags through the one tag API the
vServer gateway serves for every resource type. `TagResource`, its read-merge
write, and its errors are on the [Tagging](Tagging.md) page.
## Storage

```go
storageClient := storage.New(cfg)
storageClient.ListRegions(ctx, nil)
storageClient.ListProjects(ctx, in) // Region (optional; defaults from cfg's region)
storageClient.ListBuckets(ctx, in)  // ProjectID (required), Region
storageClient.GetBucket(ctx, in)    // ProjectID (required), Bucket (required), Region
```

`Region` is a vStorage region name, `HCM04` or `HAN02`. Empty maps `hcm-3`
to `HCM04` and `han-1` to `HAN02`. See [Storage](Storage.md) for the
console API's envelope errors and the state of an account with no project.
