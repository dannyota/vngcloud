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
created, changed, and deleted. See [DNS](DNS.md). `network` covers
security group and rule writes too: groups and rules can be created,
changed, and deleted. See [Network](Network.md). `compute` covers SSH key
writes too: a key can be imported, created, or deleted. See
[Compute](Compute.md). `containerregistry` covers repository and repository
user writes too: a repository or a user can be created and deleted. See
[Container Registry](#container-registry) below. `iam` reads caller
identity, users, actions, policies, groups, and service accounts. See
[IAM](#iam) below.

## Coverage

| Product | Package | Coverage | Model Shape | Notes |
|---|---|---|---|---|
| Project | `project` | Project listing for the configured region | Typed | Used by optional project discovery. |
| Portal | `portal` | User info, zones, quota usage, quota detail, tag quota | Map-backed | Useful for account and quota metadata. |
| Compute | `compute` | Servers, server detail, SSH keys plus SSH key writes, placement groups, placement policies, images | Typed | Some methods flatten nested data already returned by list APIs; see [Compute](Compute.md) for SSH key writes. |
| Volume | `volume` | Volumes, volume detail, underlying volume, snapshots, volume types, type zones, encryption types | Typed | Includes a convenience method for walking snapshots. |
| Network | `network` | VPCs, subnets, WAN IPs, interfaces, security groups, rules, virtual IPs, address pairs, routes, peerings, ACLs, interconnects, endpoints, plus security group and rule writes | Typed | Some methods discover VNetwork region metadata before reading resources; see [Network](Network.md) for writes and waits. |
| Load Balancer | `loadbalancer` | Load balancers, listeners, pools, health monitors, pool members, policies, tags, packages, certificates plus certificate writes | Typed | Requires IAM User permissions for the target load balancer resources. |
| Global Load Balancer | `globalloadbalancer` | Packages, regions, load balancers, listeners, pools, pool members, usage history | Typed | Catalog methods do not require project selection. |
| DNS | `dns` | Hosted zones and records, plus zone and record writes | Typed | Not project-scoped like regional compute resources; see [DNS](DNS.md) for writes and waits. |
| Container Registry | `containerregistry` | Repositories and users, plus repository and user create and delete | Typed | See [Container Registry](#container-registry) below for writes, waits, and secret handling. |
| IAM | `iam` | Caller identity, IAM users, IAM actions, policies, groups, service accounts | Typed | Page numbers start at 0, unlike the rest of the SDK; see [IAM](#iam) below. |

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

SSH key writes (`ImportSSHKey`, `CreateSSHKey`, `DeleteSSHKey`) and the
`vngcloud.Secret` a create returns are on the [Compute](Compute.md) page.

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

`network` also writes security groups and their rules; see
[Network](Network.md) for `CreateSecurityGroup`, `UpdateSecurityGroup`,
`DeleteSecurityGroup`, `CreateSecurityGroupRule`, and
`DeleteSecurityGroupRule`, their waits, and their errors.

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
```

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

```go
vcrClient := containerregistry.New(cfg)
vcrClient.ListRepositories(ctx, in)     // AccessLevel, Name
vcrClient.GetRepository(ctx, in)        // RepositoryID (required)
vcrClient.CreateRepository(ctx, in)     // Name, QuotaLimitGB (both required), NoWait
vcrClient.DeleteRepository(ctx, in)     // RepositoryID (required), NoWait
vcrClient.ListUsers(ctx, in)            // Name, Page, Size
vcrClient.ListRepositoryUsers(ctx, in)  // RepositoryID (required), Name, Page, Size
vcrClient.ListPermissions(ctx, nil)
vcrClient.CreateUser(ctx, in)           // Name, Permissions (both required), Description, DurationDays
vcrClient.DeleteUser(ctx, in)           // UserID (required)
```

`Repository` and `User` are both typed structs, matching their live bodies.
`Repository`'s fields are `ID`, `Name`, `BackendName`, `AccessLevel`,
`RegistryURL`, `QuotaLimitGB`, `QuotaUsed`, `ImageCount`, `AttachedUsers`,
and `CreatedAt`; there is no `Status` field, since no response carries one.
`User`'s fields are `ID`, `Name`, `BackendName`, `Description`, `Disabled`,
`ExpiredAt`, `CreatedAt`, `NumberOfRepositories`, `UserID`, and
`Repositories` (each a `RepositoryPermission` of `RepositoryID`,
`RepositoryName`, `BackendRepositoryName`, and `Policies`, each a
`Permission` of `ID` and `Action`). `User.ID` is the id `DeleteUserInput`
and a `RepositoryPermission.RepositoryID` take; a live delete confirms
this, not `UserID`, is what the server expects. `User.UserID` decodes the
server's numeric `userId` to a string and is otherwise a separate,
unconfirmed field. A field the reference does not document is dropped
rather than kept for either type. This breaks code that indexed
`Repository` or `User` as a map.

`CreateRepository` always creates a private repository; there is no
`Public` option, since a public repository accepts anonymous push. The
server applies no account prefix, so the created `Repository.Name` equals
the Input's `Name` exactly. The server requires `Name` to be 6 to 20
characters, only `a-z`, `0-9`, `_`, and `-`, starting with a letter or
digit, and returns 400 naming the rule otherwise; the SDK sends `Name` as
given and does not check its shape. `CreateRepository` is a `POST` and is
never retried after a failure that may have already reached the server,
including a 408 or 499: after such a failure, list repositories with
`Name` set and match a row whose name equals the input exactly before
creating again.

`DeleteRepository` reads the repository first and returns
`ErrRepositoryNotEmpty`, sending nothing, when it still holds images;
delete the images with `docker` or the console first. A repository user
attached to it is not affected by the delete. That same read must confirm
the repository: a response with no image count, or one naming a different
repository, also sends nothing and returns an error.

The create and delete responses carry no status to wait on. Without
`NoWait`, `CreateRepository` confirms the new repository with
`GetRepository`, and `DeleteRepository` waits for `GetRepository` to
report it gone, each polling every 2 seconds for up to 60 seconds. A live
create was visible through `GetRepository` at once, so the confirm read
usually succeeds on its first try. Past the bound, or on a canceled
context, the returned error wraps `ErrNotSettled`: for a create, the
repository exists and must not be created again; for a delete, the delete
was sent and a rerun is safe.

`CreateUser` takes a permission's actions as names, such as `"Pull
Images"`, not raw policy ids: it reads `ListPermissions` and maps each name
to its policy id, matching the server's own list exactly (a live capture
shows the three actions "Pull Images", "Push Images", and "All"), so an
unknown action fails before any create is sent. The server's own name rule
for a user is 6 to 14 characters, only `a-z`, `A-Z`, `0-9`, `_`, and `-`,
starting with a letter or digit; the SDK sends `Name` as given and does not
check this. Before sending the create, `CreateUser` also lists users by the
exact input name and refuses with `vngcloud.ErrInvalidInput`, sending
nothing, if a user is already named that: the create response carries no
user id, so a lookup after the fact could otherwise resolve to an older
user that just happens to share the name.

A repository user is a credential that can push images other systems run.
The example below creates a pull-only user with an expiry, rather than an
unrestricted, permanent one:

```go
created, err := vcrClient.CreateUser(ctx, &containerregistry.CreateUserInput{
	Name:         "app-ci",
	DurationDays: vngcloud.Ptr(90),
	Permissions: []containerregistry.UserPermission{
		{RepositoryID: "<repository-id>", Actions: []string{"Pull Images"}},
	},
})
if err != nil {
	log.Fatal(err)
}
log.Println(created.SecretKey.Reveal())
```

The response carries only a secret key, no user id, so `CreateUser` finds
the new user with the same exact-name listing: a live capture shows the
server applies no account prefix. One match fills `User`; zero or more
than one returns an error wrapping `ErrUserNotFound`, naming `list-users
--name <name>` to check by hand, while `SecretKey` on the Output is still
set either way, since the create itself already succeeded. Both this
lookup and the pre-create check walk every page of the list and fail
closed, returning an error rather than a guess, when a page's own totals
do not add up. `SecretKey` is a `vngcloud.Secret`: printing, logging, or
JSON-encoding the Output gives `[redacted]`, and `Reveal()` is the only way
to read it back. `CreateUser` sends the create with `Once`: a followed
redirect is refused, since it would resend the same create and its secret
a second time. A 401 is not the same risk: the API gateway rejects a stale
or invalid token before the request reaches the create logic, so a 401
always creates nothing. A 5xx or a network error is still never resent,
since either can mean the server already acted; a user found afterward by
`list-users --name <name>` has already lost its secret and should be
deleted before creating again. `DurationDays` left nil creates a user with
no expiration; set one for a pull user meant to be temporary.

`DeleteUser` sends the delete directly: a user holds no data of its own,
so there is no pre-delete guard.

Repository and user names, registry URLs, and ids are account data.
`docker login vcr.vngcloud.vn -u <login name> --password-stdin` takes the
secret as the password; whether `<login name>` is the repository user's
own name or the repository's name is unverified.

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
