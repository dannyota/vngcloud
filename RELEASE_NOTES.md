# Release Notes

## v0.9.0 - vMonitor Create and Delete

### Highlights

- `monitor.CreateCheck` creates an HTTP synthetic check. It always verifies
  the target's TLS certificate and sends no notifications yet, so a created
  check alerts nobody. Zero-value fields take the console's defaults. It is
  never retried after a failure that may have reached the server; after
  such an error, list checks and look for the name before trying again.
- `monitor.DeleteCheck` deletes a check, and `monitor.ListLocations` lists
  the probe locations whose IDs `CreateCheck` takes.
- New `vngcloud monitor create-check`, `delete-check`, and
  `list-locations` commands. `delete-check` needs `--yes`, and a read-only
  profile refuses both writes. Locations, headers, query parameters, and
  assertions go through `--cli-input-json`.

### Behavior changes

None: this release adds methods and commands only.

## v0.8.0 - vMonitor Pause and Resume

### Highlights

- New `monitor` package for vMonitor synthetic checks: `ListChecks`,
  `GetCheck`, `PauseCheck`, and `ResumeCheck`. The API has one toggle
  request for both, so `PauseCheck` and `ResumeCheck` read the status
  first, send the toggle at most once, and confirm by reading. They report
  `Changed`, so a deploy resumes only a check it paused itself. See
  [Monitor](https://github.com/dannyota/vngcloud/wiki/Monitor) and ADR 0003.
- New `vngcloud monitor list-checks`, `get-check`, `pause-check`, and
  `resume-check` commands. Pause and resume are writes, so a read-only
  profile refuses them.
- New CLI error codes `UnexpectedStatus` and `StatusUnconfirmed`, both
  exit 1.
- `EndpointOverrides` gains `Monitor`.

### Behavior changes

None for existing callers. A new internal transport option sends a toggle
request once, with no retry, no resend after a 401, and no redirect.

## v0.7.0 - vCDN IP Ranges

### Highlights

- New `cdn` package: `cdn.New(cfg).ListIPRanges(ctx, nil)` returns the
  current GreenNode CDN IP ranges an origin must allow, as sorted,
  de-duplicated, canonical CIDR strings. vCDN has no API, so the SDK reads
  GreenNode's public FAQ page instead; the request carries no credential
  and no cookie. Any doubt about the page's shape fails the call with
  `cdn.ErrPageFormat` rather than an empty or partial list. See
  [CDN](https://github.com/dannyota/vngcloud/wiki/CDN).
- New `vngcloud cdn list-ip-ranges` command, a read allowed under a
  read-only profile.
- `EndpointOverrides` gains `CDNDocs`, for pointing the read at a mirror or
  a test server.

### Behavior changes

None: this release adds a package, an endpoint field, and an internal
transport option. No existing method, field, or command changes.

## v0.6.0 - The vngcloud Command

### Highlights

- New `vngcloud` command, installed with
  `go install danny.vn/vngcloud/cmd/vngcloud@latest`. It covers `billing`,
  `pricing`, `compute`, `network`, and `dns`, with one subcommand per SDK
  operation and flags derived from each operation's Input.
- `--output json|table|text`, `--query` (JMESPath), `--cli-input-json`, and
  `--debug`, which logs each request's method, path, status, and timing and
  never a body, header, token, or credential.
- Errors print to stderr as one JSON line with stable exit codes: 1 for API
  or network errors and cancellation, 2 for usage and config errors, 3 for
  credential and login errors, 4 for not found.
- Destructive commands (`billing delete-budget`, `delete-budget-threshold`)
  need `--yes`. Nothing prompts without a terminal.
- Read-only profiles: `read_only = true`, `VNGCLOUD_READ_ONLY=1`, or
  `--read-only` makes every write command fail before any request.
- `vngcloud configure` and `configure set|get|list` edit
  `~/.vngcloud/config` and `credentials` safely: mode 0600 files in a 0700
  directory, written through a temp file and rename, symlinks kept, secrets
  never taken from the command line.
- Generated CLI reference pages in the wiki.
- SDK: `vngcloud.LoginError` reports a failed login step, its HTTP status,
  and a suspected captcha, and never carries a credential. `WithLogger` now
  logs requests at Debug level. `Config.ProfileSetting` reads a key from the
  resolved profile.

### Behavior changes

- `APIError.Code` (and `ErrorCode`) now falls back to a status-derived code
  when the API gives none: `BadRequest`, `Unauthorized`, `Forbidden`,
  `NotFound`, `Conflict`, `Throttled`, `ServerError`, or `ClientError`. An
  envelope code equal to the HTTP status counts as none, so a billing 400 is
  `BadRequest` rather than `"400"`.
- Login failures return `*vngcloud.LoginError`. It matches `ErrAuth`,
  except when the login was cancelled or timed out: then it matches
  `context.Canceled` or `context.DeadlineExceeded` instead.

### Dependencies

The command adds `github.com/spf13/cobra`, `github.com/spf13/pflag`,
`github.com/jmespath/go-jmespath`, and `golang.org/x/term` (with
`golang.org/x/sys` and, on Windows, `github.com/inconshreveable/mousetrap`).
Only `cmd/vngcloud` and `internal/cli` import them; the SDK packages stay
standard-library only, and CI checks it.

## v0.5.0 - LoadConfig, Profiles, and the Token Cache

### Highlights

- `vngcloud.LoadConfig(ctx, opts...)` resolves a `Config` from options,
  `VNGCLOUD_*` environment variables, and AWS-style profile files
  (`~/.vngcloud/config` and `~/.vngcloud/credentials`, or
  `WithConfigFile`/`WithSharedCredentialsFile` and their environment
  variables). `NewConfig` keeps building from options only. Precedence is
  options, then environment variables, then the resolved profile's file
  section; credentials resolve as one set from the first source that sets
  any value, so a profile's password is never mixed with another source's
  username, and an access token wins over IAM User values within one
  source. See [Configuration](https://github.com/dannyota/vngcloud/wiki/Configuration#loadconfig)
  for the full precedence table, file formats, and environment variables.
- `WithProfile(name)` selects a profile explicitly; `VNGCLOUD_PROFILE` never
  does. An explicit profile skips environment variables for credentials and
  the project ID, so a `.env` file for one account can never send a call
  made with another profile to that account. An explicit profile with no
  credentials, from options or its own file section, fails with the new
  `vngcloud.ErrNoCredentials`, naming the profile.
- New sentinels `vngcloud.ErrNoCredentials` and `vngcloud.ErrCredentialsFile`
  both match `vngcloud.ErrInvalidConfig` via `errors.Is`.
  `ErrCredentialsFile` covers the credentials file missing at an explicit
  path, unreadable, refused for unsafe permissions, or malformed.
  `LoadConfig` refuses a credentials file that group or others can read,
  naming the file and `chmod 600`, before reading it; the check is skipped
  on Windows. A path that is not a regular file, such as a directory or a
  FIFO, is also an error. A source (the environment or a profile) that sets
  some IAM User credentials but not all of root_email, username, and
  password is also `ErrNoCredentials`, naming the source and the missing
  keys, so it is never completed by mixing in another source's values. No
  `LoadConfig` error names a credential value.
- New `vngcloud.CredentialsProvider` interface (`Token(ctx)`,
  `Invalidate(accessToken)`) and `vngcloud.WithCredentialsProvider` for a
  custom token source, such as a secrets manager. It wins over
  `WithStaticToken`, which wins over `WithIAMUser`. A provider returning an
  empty token with a nil error is `vngcloud.ErrAuth`; a zero `ExpiresAt`
  makes the SDK call `Token` before every request.
- `vngcloud.WithTokenCache(dir)` turns on an on-disk token cache shared
  across processes, so a program run repeatedly, or several programs
  sharing one profile, log in only once per token lifetime. Only IAM User
  credentials use it; a static token or a custom `CredentialsProvider` is
  never written to disk, and without this option the SDK writes nothing.
  The cache directory is mode 0700, each token file mode 0600, and one
  locked operation per credential set reads, logs in only if needed, and
  writes back, so two processes sharing one profile never log in with the
  same TOTP code.
- Fixed the retry after an HTTP 401: the SDK used to clear its in-memory
  token but let the IAM User's own cache immediately hand back that same
  rejected token, so the retry failed the same way every time. It now
  invalidates exactly the token it sent, in memory and (with a token cache)
  on disk, and logs in again before retrying once. A token younger than 30
  seconds is left in place instead, so a 401 that a fresh login cannot fix
  ends the call with `vngcloud.ErrAuth` rather than forcing a second login
  inside the same 30-second TOTP window. Parallel requests that all get a
  401 for the same token cause at most one new login. A request that needs
  authentication, including the retry itself, is never sent without a
  token; if the token source has none to give, the call fails with
  `vngcloud.ErrAuth` before anything goes out.
- Fixed the token cache stamping every token it reads from disk with the
  read time: a process that reused another process's still-cached token
  now keeps that token's real obtain time, so the 30-second rule can still
  invalidate an old, revoked token instead of treating it as freshly
  obtained forever.

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
