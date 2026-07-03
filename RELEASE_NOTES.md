# Release Notes

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
