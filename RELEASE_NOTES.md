# Release Notes

## v0.4.0 - Service Packages

### Breaking changes

- `vngcloud.NewClient` and the transitional `vngcloud.Client` type are gone.
  Every service is now its own top-level package with `New(cfg
  vngcloud.Config) *Client`, matching `billing` and `pricing`: `compute`,
  `volume`, `network`, `loadbalancer`, `globalloadbalancer`, `dns`,
  `containerregistry`, `portal`, and `project` (new; replaces
  `client.ListProjects`).
- `NewClient` used to log in eagerly, so bad credentials failed before its
  first return. `NewConfig` does not log in: call `cfg.Authenticate(ctx)`
  right after it to get that same fail-fast check, or skip it and let the
  first service call log in lazily.
- Every root re-export of a service type is gone; import the service package
  instead. `vngcloud.Server`, `NetworkInterface`, `SSHKey`, `ServerGroup`, and
  their `List*Options`/`List*Result` types are now `compute.*`.
  `vngcloud.Volume` and `Zone` are `volume.*`. `vngcloud.VPC`,
  `SecurityGroup`, `Subnet`, `RouteTable`, and `Peering` are `network.*`.
  `vngcloud.LoadBalancer`, `Certificate`, `Listener`, `Pool`, and `Policy` are
  `loadbalancer.*`. `vngcloud.GlobalLoadBalancer`, `GlobalPool`, and
  `GlobalListener` are `globalloadbalancer.*`. `vngcloud.HostedZone`,
  `RecordValue`, and `DNSRecord` are `dns.*`. `vngcloud.PortalUserInfo`,
  `PortalZone`, `PortalQuota`, and `PortalTagQuota` are `portal.UserInfo`,
  `portal.Zone`, `portal.Quota`, and `portal.TagQuota`.
  `vngcloud.ContainerRepository` and `ContainerRegistryUser` are
  `containerregistry.Repository` and `containerregistry.User`.
  `vngcloud.Project` and `ListProjectsOptions` are `project.Project` and
  `project.ListProjectsInput`. Every `*Service` type (`ComputeService`,
  `VolumeService`, `NetworkService`, `LoadBalancerService`,
  `GlobalLoadBalancerService`, `DNSService`, `PortalService`,
  `ContainerRegistryService`) is gone with `NewClient`; call the package's own
  `New(cfg)`. `vngcloud.ListOptions`, `Page`, and `ListResult[T]` are gone;
  see the paging change below.
- These root types moved to their package with the same name: `compute`:
  `Flavor`, `Image`, `PackageLimit`, `ServerSecgroup`, `ServerGroupMember`,
  `ServerSecurityGroup`, `ServerGroupMembership`, `ServerGroupPolicy`,
  `OSImage`, and `UserImage`. `network`: `WANIP`, `ElasticNetworkInterface`,
  `VirtualIPAddress`, `SubnetSecondarySubnet`, `SecurityGroupRule`,
  `RouteTableRoute`, `Tag`, `Interconnect`, `AddressPair`, and
  `VNetworkRegion`. `loadbalancer`: `PoolMember`, `HealthMonitor`, `L7Rule`,
  and `ListenerInsertHeader`.
- Every operation takes one `*OpInput` and returns one `*OpOutput`, for
  example `compute.New(cfg).GetServer(ctx, &compute.GetServerInput{ServerID:
  id})`. Methods that took bare arguments, such as `GetServer(ctx, id)` and
  `ListSubnets(ctx)`, moved to Input structs. A required Input field returns
  `ErrInvalidInput` before any request when it is empty, including a nil
  Input.
- List outputs name their field: `Items` for the page, plus `Page`,
  `PageSize`, `TotalPage`, and `TotalItem` for an API that pages. These sat
  under a nested `Page` field before, for example `res.Page.TotalItem`; they
  now sit directly on the result, for example `res.TotalItem`. A Get returns
  a struct with one named field, such as `GetServerOutput{Server Server}`.
- Model renames drop redundant package prefixes now that each service is its
  own package: `network` renames `NetworkRoute` to `Route`, `NetworkACL` to
  `ACL`, `NetworkEndpoint` to `Endpoint`, `NetworkEndpointDetail` to
  `EndpointDetail` (its embedded `NetworkEndpoint` field is now `Endpoint`,
  so `detail.NetworkEndpoint.Name` is now `detail.Endpoint.Name`), and
  `NetworkZone` to `Zone`. `loadbalancer` renames `LoadBalancerNode` to
  `Node`, `LoadBalancerPackage` to `Package`, and `LoadBalancerTag` to `Tag`;
  its `ListLoadBalancerPackages` method is renamed to `ListPackages`, matching
  `globalloadbalancer.ListPackages`. `globalloadbalancer` (renamed from the
  `glb` internal package) renames every `GLB`- and `Global`-prefixed type,
  for example `GlobalLoadBalancer` to `LoadBalancer`, `GLBPackage` to
  `Package`, `GLBVLBPackage` (`vngcloud.GlobalLoadBalancerRegionalPackage`)
  to `RegionalPackage`, and `GLBRegion`
  (`vngcloud.GlobalLoadBalancerRegion`) to `Region`, `GlobalLoadBalancerVIP`
  to `VIP`, `GlobalLoadBalancerDomain` to `Domain`, `GlobalPool` to `Pool`,
  `GlobalPoolHealthMonitor` to `PoolHealthMonitor`, `GlobalPoolMember` to
  `PoolMember`, `GlobalPoolMemberDetail` to `PoolMemberDetail`,
  `GlobalListener` to `Listener`, and `GlobalLoadBalancerUsageHistory` to
  `UsageHistory`; the root re-export
  `vngcloud.GlobalLoadBalancerPackage` is now `globalloadbalancer.Package`.
  `dns` renames `DNSRecord` to `Record`, `VpcMapRegion` to `VPCMapRegion`,
  and `HostedZone.AssocVpcMapRegion` to `AssocVPCMapRegion` (the
  `assocVpcMapRegion` JSON key is unchanged). `portal` renames
  `PortalUserInfo`, `PortalZone`, `PortalQuota`, and `PortalTagQuota` to
  `UserInfo`, `Zone`, `Quota`, and `TagQuota`. `containerregistry` renames
  `ContainerRepository` to `Repository` and `ContainerRegistryUser` to
  `User`.
- `APIError.Operation` is now `"<package>.<Method>"` in lowercase, such as
  `"compute.GetServer"`, everywhere including project discovery
  (`"project.ListProjects"`).

### Highlights

- `network` imports `compute` for `compute.Server`, since
  `ListServersBySecurityGroup` returns full server objects.
- No API coverage changed: every method that existed before keeps its
  behavior under the new signature.

## v0.3.0 - Budgets and Price Quotes

### Breaking changes

- `vngcloud.Config` is an opaque handle built by `vngcloud.NewConfig` with
  `WithRegion`, `WithProjectID`, `WithIAMUser`, and the existing options.
  `vngcloud.NewClient(ctx, cfg)` takes that Config and no options. Every
  client built from one Config shares one login and project lookup.
- The `ClientOption` alias is now `LoadOption`.
- A `POST` or `PATCH` is retried only after a 429 or a failed connection,
  never after a 5xx or a network error that may have reached the server.
  `IsRetryable` follows the same rule.
- `APIError.Code` and `ErrorCode` now return a numeric envelope code as
  decimal text, such as `"400"`, for every service. They returned `""`
  before.

### Highlights

- New `billing` package: budgets, budget thresholds, alert history,
  current-period cost, cost explorer, and balances. Budget and threshold
  create, update, and delete are the first write APIs.
- New `pricing` package: `GetQuote` prices a resource before it is created
  and places no order.
- `ErrInvalidInput` for a missing required field, a malformed ID, or a bad
  date, returned before any request.
- `vngcloud.Ptr` builds pointer fields for partial updates.
- `EndpointOverrides.Billing` moves the dashboard billing gateway.
- `make live-write` runs a gated live budget write test.

### Known limits

- The cost explorer and alert history models follow the console's field
  names but have not been checked against a live response with data yet.

## v0.2.1 - Canonical License Text

### Highlights

- Restored the canonical Apache License 2.0 text (the previous file had
  reflowed wording and a missing appendix, which pkg.go.dev's license
  detector did not recognize, hiding the documentation).

## v0.2.0 - GreenNode Domain Migration

### Highlights

- Migrated all default endpoints to GreenNode domains
  (`*.console.greennode.ai`, `signin.greennode.ai`). The vDNS API moved to
  a new host and gained the `/vdns-api/` path prefix
  (`vdns.console.greennode.ai/vdns-api/`).
- Fixed JSON error-body decoding to handle array-shaped error responses
  returned by some endpoints.
- Hardened the login flow with HTTP status checks, cross-host redirect
  guards (legacy `*.vngcloud.vn` hosts 301-redirect but Go strips
  Authorization cross-domain), and clearer error messages including a hint
  when captcha is required.
- Added context-aware retries with jittered exponential backoff and
  Retry-After header support.
- Fixed goroutine-safety in project discovery and vNetwork endpoint
  caching.
- Added eager authentication in `NewClient` so bad credentials fail fast
  at construction time.
- Added `WithStaticToken` option to bypass IAM login using a bearer token
  captured from the console (captcha workaround).
- Added `EndpointOverrides.Dashboard` field for the OAuth redirectUri;
  overriding Dashboard derives Token unless Token is set explicitly.
- Added `make live` smoke test that reads `.env` and exercises one
  read-only call per service against the real API (defaults to regions
  hcm-3 and han-1).

## v0.1.0 - Initial Read-Only IAM User SDK

This release introduces `danny.vn/vngcloud`, a read-only Go SDK for VNG Cloud
IAM User workflows.

### Highlights

- Added IAM User authentication with automatic token refresh and one retry after
  token expiry.
- Added optional TOTP support through shared-secret and callback-based providers.
- Added optional `ProjectID`; project discovery can select the regional project
  visible to the IAM User.
- Added product-grouped clients under one root package:
  `Compute`, `Volume`, `Network`, `LoadBalancer`, `GlobalLoadBalancer`, `DNS`,
  `ContainerRegistry`, and `Portal`.
- Added high default pagination size for list APIs to reduce missed resources
  when the server accepts larger page sizes.
- Added a basic smoke example with multi-region config support and separated
  raw HTTP output from SDK-decoded output.
- Added sanitized fixture coverage for supported read APIs and model decoding.
- Added public docs for setup, authentication, project discovery, example usage,
  and supported API coverage.

### Supported Read APIs

- Project discovery for IAM Users.
- Portal user info, zones, quota usage, quota lookup, and tag quota.
- Compute servers, server detail, SSH keys, placement groups, placement group
  policies, OS images, GPU images, and user images.
- Volume list/detail, underlying volume, snapshots, volume type zones, volume
  types, default volume type, and encryption types.
- Network VPCs, subnets, WAN IPs, interfaces, security groups and rules, virtual
  IPs, address pairs, route tables, peerings, ACLs, interconnects, endpoints, and
  endpoint tags.
- Regional Load Balancer inventory, listeners, pools, pool health monitor, pool
  members, policies, tags, packages, and certificates.
- Global Load Balancer packages, regions, inventory, listeners, pools, pool
  members, and usage history.
- DNS hosted zones and records.
- Container Registry repositories and users.

### Scope

- Write APIs are intentionally not included.
- Service account authentication is intentionally not included.
- Root-user authentication is intentionally not included.
- API URL versions such as `v1` and `v2` are treated as server route metadata,
  not SDK package versions.

### Validation

- Unit tests cover route construction, pagination defaults, authentication,
  transport behavior, and sanitized fixture decoding.
- The basic example can be used as a local smoke path. Its output is ignored by
  git because it may contain sensitive account and infrastructure data.
