# Release Notes

## v0.81.3 - Credential Redaction Across Retries

- Requests that retried or refreshed their token redacted only the last
  credential they sent. A server that echoed an earlier token in a response
  could get it into SDK output, errors, captures, or logs. Every credential
  a request sends is now redacted. This affects billing, billing resources,
  NAT, and vStorage project operations.
- An oversized error response now keeps its HTTP status and public error
  identity (`ErrAuth`, `ErrPermission`, `ErrNotFound`, `ErrRateLimited`)
  instead of status 0, with the body withheld. The server console log keeps
  its documented rule: a body over 8 MiB on any status is
  `ResponseTooLarge` with status 0.
- No public SDK or CLI surface changes.

## v0.81.2 - Billing Cost Resource Fixes

- `billing.ListCostResources` and CLI `billing list-cost-resources` now
  return the first page. The cost explorer API pages from 0; the SDK sent
  the 1-based `Page` unchanged, so it skipped the first page and returned no
  rows at all when every row fit on one page. `Page` stays 1-based: the SDK
  sends `Page - 1` and reports the server's page plus 1. A response whose
  page is negative or the largest int returns an `InvalidResponse` API
  error.
- `CostResource.Cost` now reads the API's `totalCost`, the resource's total
  cost over the requested range in VND. It previously read `cost`, which the
  API never sends, so every row showed 0.
- `CostResource` adds `DailyAverage` (`dailyAvg`) and `StartDate`
  (`startDate`, `YYYY-MM-DD`). The account ID and region in each item stay
  omitted.
- Compatibility: SDK JSON that serialized `CostResource` with the old `cost`
  key no longer decodes into `Cost`; re-encode it with `totalCost`. Unkeyed
  `CostResource` struct literals no longer compile. CLI JSON keeps the `Cost`
  key and adds `DailyAverage` and `StartDate`; table and text output gain two
  columns, so scripts that read text columns by position must be updated.

## v0.81.1 - User Guides and Storage Workload Access

- Document application access to vStorage from vServer and VKS: dedicated
  identities, bucket grants, private S3 key handoff, rotation, retirement,
  and network prerequisites. See [Storage Workload Access](
  docs/wiki/Storage-Workload-Access.md).
- Clarify key retirement and ambiguous-create recovery: delete identified
  keys while attached, reconcile uncertain results with known inventory and
  other creators, and preserve unrelated keys and policy grants.
- Improve README and Wiki navigation, put credential prerequisites before
  first-use examples, keep quick starts read-only, and identify SDK server
  counts as one page. Add CDN, Monitor, and current object operations to the
  service overview.
- Correct guidance on secret redaction, Windows file permissions, endpoint
  overrides, and operation-specific retries. Clarify the CLI read-only guard
  and quote checks without implying IAM restrictions or final billing locks.
- This patch changes documentation only. It adds no SDK or CLI operations or
  endpoint APIs.

## v0.81.0 - Native vStorage Server-Side Copy

- SDK `storage.ObjectClient.CopyObject` and CLI `storage copy-object` copy one
  current object directly within vStorage, retaining the source. Both buckets
  must belong to the same project and region.
- The SDK requires `Replace: true`; the CLI requires `--replace --yes`.
  Read-only settings refuse before credentials load. Source and destination
  names remain subject to concurrent changes; no version selector or
  create-only destination guarantee is provided.
- One bodyless signed PUT uses `x-amz-copy-source`. There is no local spool,
  download/upload fallback, source deletion, multipart copy, or retry.
  A complete bounded result is required; HTTP 200 with an Error is not success.
- Ambiguous results use `ErrObjectCopyUnconfirmed` and CLI
  `ObjectCopyUnconfirmed` with exit 1. Inspect the destination before deciding
  whether to repeat the copy. Failed metadata output after confirmed success
  reports completion and never resends.
- Provider metadata defaults apply without a lossless clone or encryption
  inheritance guarantee. Recorded HCM04 encrypted sources return 501, and a
  plain-source copy can remain unencrypted despite destination bucket
  encryption. HAN02 encryption and GreenNode's exact copy size limit remain
  unverified. No new live verification is claimed.
- Existing transfers, listing, HEAD, and deletion remain available. Move/rename
  stays gated on safe source removal. See [copy guidance](
  docs/wiki/Storage-Objects-Copy.md).

## v0.80.0 - Native vStorage Object Deletion

- SDK `storage.ObjectClient.DeleteObject` and CLI `storage delete-object`
  delete one current object name. The CLI requires `--yes`; every read-only
  setting refuses before S3 credentials load.
- Each invocation sends one unconditional DELETE. HTTP 204 confirms acceptance,
  without proving prior existence, permanent erasure, or reclaimed quota.
  Concurrent writers remain unprotected, and versioning can retain data or add
  a delete marker. No version selector, precondition, retry, or cleanup is added.
- Ambiguous results return `ErrObjectDeleteUnconfirmed` and CLI
  `ObjectDeleteUnconfirmed` with exit 1. Inspect the current key and version
  history before another deletion. Failed metadata output after confirmed
  success reports completion and never resends.
- Explicit S3 region and private credentials-file flags, safe metadata output,
  and the five-minute default timeout remain. No new live verification is
  claimed. Native copy is a separate release; move/rename remains gated.
  See [object guidance](docs/wiki/Storage-Objects.md#current-name-deletion).

## v0.79.0 - Native vStorage Object Reads

- SDK `storage.ObjectClient.ListObjects` and `HeadObject`, plus CLI
  `storage list-objects` and `storage head-object`, add object discovery and
  metadata lookup without downloading content.
- Listing returns one page of at most 1,000 combined objects and common
  prefixes. Continue explicitly with the returned opaque token and unchanged
  filters. Concurrent changes can cause omissions or repeats across pages.
- HEAD reports bounded metadata. Missing length is -1; unsafe, invalid, or
  oversized optional fields are omitted. Version and encryption fields remain
  observations, not locks or integrity guarantees.
- Both CLI commands require explicit S3 region and private credentials-file
  flags. They support Linux and macOS, read-only mode, and metadata queries.
  No application retry or automatic pagination is added.
- Both regions rely on published compatibility, with additional recorded
  listing evidence for HCM04. No new live verification is claimed.
- PUT/GET behavior is preserved. Delete and native copy are separate releases;
  safe move/rename remains gated. See [object guidance](
  docs/wiki/Storage-Objects.md).

## v0.78.0 - Native vStorage Object Transfers

- SDK `storage.NewObjectClient`, `PutObject`, and `GetObject` transfer arbitrary
  binary objects with explicit in-memory S3 credentials and region.
- CLI `storage put-object` and `storage get-object` transfer regular files on
  Linux and macOS using a private `--s3-credentials-file`. Output contains
  metadata; object bytes stay in files. Management authentication is separate.
- Uploads create only by default. Remote or local replacement requires
  `--replace --yes`. Read-only mode blocks uploads and permits downloads.
- Uploads are capped at 5,000,000,000 bytes. Downloads have a configurable
  finite bound. Transfers default to a five-minute deadline. Uploads use a
  private snapshot; downloads publish only after complete local writes.
- HCM04 supports create-only uploads. HAN02 supports downloads and explicit
  replacement; create-only uploads remain disabled pending server evidence.
- An unconfirmed upload may already exist. Download to a fresh path and compare
  byte length and SHA256 before retrying. No transfer retries automatically.
  ETag remains opaque; a local digest does not verify a remote checksum.
- Listing, HEAD, append, delete, sync, multipart uploads, and presigned URLs
  remain outside the native transfer surface. See [object transfer guidance](
  docs/wiki/Storage-Objects.md).

## v0.77.0 - Typed Portal Zones

### Breaking changes

- `portal.Zone` is now a struct. Replace map access such as
  `zone["uuid"].(string)` with a nil check on `zone.UUID`, then read
  `*zone.UUID`. Map literals, iteration, and type assertions must also
  migrate to struct fields. `Name` is a string; every other field is a
  pointer. Absent or null fields stay nil. Present false, zero, empty
  strings, and empty arrays remain distinct.
- `portal list-zones` uses Go field names in JSON, table, text, and queries.
  Change `--query 'Items[].[uuid,name]'` to
  `--query 'Items[].[UUID,Name]'`. SDK JSON retains API spellings such as
  `uuid` and `isEnabled`; nil pointers encode as null in both SDK and CLI
  JSON. Unknown fields are ignored.
- `ID` and `UUID` remain separate fields with no fallback. Use `UUID` for
  zone selection when present. User info and quotas remain map-backed.

## v0.76.1 - IAM Guard Fix for Hyphenated Products

### Fixes

- `iam attach-service-account-policy` and the other IAM policy guards no
  longer refuse read-only policies whose product name has a hyphen, such as
  GreenNode's managed `vStorageReadOnlyAccess` (`vstorage-hcm03:List*`).
  Hyphens are allowed only between letter and digit runs in the product
  part; patterns that can match an IAM write action and every other unusual
  shape are still refused.

## v0.76.0 - Public IP Price Discovery

### Highlights

- `network list-network-prices` quotes each public virtual IP type
  (`public`, `public-vm`, `public-mkp`) for the account per month, and lists
  Floating IP, External Interface, and reservation charges as unknown with
  their documentation links. SDK: `network.ListNetworkPrices`.
- Bandwidth and NAT costs are excluded and named in the notes; use the NAT
  zone, package, and quote commands for NAT.
- Verified live on 2026-10-11 in hcm-3: each public virtual IP type quoted
  120,000 VND per month.

## v0.75.0 - Service-Account Login

### Highlights

- The SDK and CLI can log in as a GreenNode service account, so a tool or
  team can use its own machine identity whose policies GreenNode enforces.
  SDK: `vngcloud.WithServiceAccount(&vngcloud.ServiceAccountAuth{ClientID,
  ClientSecret})`. Environment: `VNGCLOUD_CLIENT_ID` and
  `VNGCLOUD_CLIENT_SECRET`. Profiles: `client_id` and `client_secret` in
  the credentials file, set with `vngcloud configure set client_secret -`
  (stdin only; never printed).
- Precedence is credentials provider, static token, service account, then
  IAM user. A selected method that fails never falls back to another.
- The token request goes only to the fixed GreenNode token host, is sent
  once, follows no redirects, and is decoded strictly. Tokens are cached
  under a key bound to the profile, client ID, and secret, so a replaced
  secret never reuses an old token.
- New CLI error codes `ServiceAccountLoginFailed` and `TokenCacheFailed`.
- A secret reset rejects the old secret at once but does not revoke tokens
  already issued (default lifetime 30 minutes): replace the secret and
  restart the tools that use it.
- Verified live on 2026-10-11 in hcm-3 with a disposable service account
  and a resource-scoped read-only policy: the allowed security group read
  succeeded, another group and a write were refused by GreenNode with 403,
  and every resource was cleaned up. Paid NAT writes still require IAM-user
  login.

### Fixes

- Error messages withhold text that follows a credential key such as
  `client_secret`, including failure envelopes returned with HTTP 200.

## v0.74.0 - Compute Flavor Price Discovery

### Highlights

- `compute list-flavor-prices` lists each flavor family, platform, and size
  in every zone with the account's monthly compute price and the price per
  unit of RAM. Filter with `--family`, `--platform`, `--name`, `--zone-id`,
  `--min-ram`, `--max-ram`, or `--available-only`; sort with `--sort-by ram`
  or `--sort-by unit-price`. SDK: `compute.ListFlavorPrices`.
- GreenNode cannot quote a flavor alone, so each price is the instance line
  of a quote for a fixed reference server: an unlicensed Ubuntu image the
  flavor supports and the minimum 3000 IOPS root disk, whose cost is
  excluded. Each row shows the reference it used. The command is read-only
  and never orders anything.
- RAM is shown in the API's own unit; GiB values stay unknown because the
  API does not state whether its memory figure is GB or GiB.
- Verified live on 2026-10-11 in hcm-3: all 33 s2 general-purpose flavors
  quoted in the enabled zone, from about 103,000 VND per RAM unit per month
  (`s2-highmem` sizes).


## v0.73.0 - Volume and Storage Price Discovery

### Highlights

- `volume list-volume-prices` lists every volume type in each enabled zone
  with the account's monthly quote, plain and with each encryption type, and
  the effective price per GB at the quoted size. Filter with `--zone-id`,
  `--iops`, `--size`, `--encryption`, or `--volume-type-id`; sort with
  `--sort-by unit-price`. SDK: `volume.ListVolumePrices`.
- `storage list-storage-prices` lists each monthly object storage offering
  with its quote at the minimum or `--quota-gb` size and the price per GB.
  SDK: `storage.ListStoragePrices`.
- Both commands are read-only and work under `--read-only`. Unknown
  charges, such as egress and requests, stay unknown and are named in the
  notes on stderr; JSON output carries them in `Metadata`. `--allow-partial`
  prints the rows that could be priced, then the error, and exits 1.
- Discovery calls are serial, paced, bounded in requests, rows, and body
  size, and reject a reply that echoes a sent credential.
- Verified live on 2026-10-11 in hcm-3: ten volume types quoted in the
  enabled zone (3000 IOPS from 3200 VND per GB-month; encryption did not
  change the price) and object storage at 1000 VND (Gold) and 530 VND
  (Archive) per GB-month in HCM04.

### Fixes

- Shared project lookup, token refresh, and the token cache now check the
  caller's context after waiting for another caller, so a cancelled call
  cannot change the token or project another call uses.

## v0.72.0 - Public NAT Create and Delete

### Highlights

- `network create-nat-instance` buys one Public NAT for one month from
  credit, in `han-1` with IAM-user login. It needs `--yes` and
  `--max-price`, refuses a VPC that already has a NAT or a duplicate name,
  waits for ACTIVE, then switches auto-renew off through billing and
  confirms it. GreenNode turns auto-renew on for every IAM purchase
  regardless of the order, so this second step is part of create. Creating
  a NAT adds a `0.0.0.0/0` route to the whole VPC; use a disposable VPC to
  test. SDK: `network.CreateNATInstance`.
- `network quote-create-nat-instance` prices the same input without buying,
  and `network delete-nat-instance --yes` deletes a NAT after checking it
  belongs to the named VPC, then waits until it is gone (`--no-wait` returns
  after acceptance). SDK: `network.QuoteCreateNATInstance`,
  `network.DeleteNATInstance`.
- `network list-nat-zones` and `network list-nat-packages
  --availability-zone-id <az>` show the availability zones and per-zone
  packages a purchase needs. SDK: `network.ListNATZones`,
  `network.ListNATPackages`.
- The order, the renewal write, and the delete are each sent once. A reply
  that cannot be confirmed returns `NotSettled` with the known NAT ID and
  recovery steps; a NAT that fails provisioning returns `WriteFailed`.
- Verified live on 2026-10-11 in Hanoi: a disposable VPC, one NAT ACTIVE in
  about 6 minutes, renewal confirmed MANUAL, deletion, and a full refund
  (net cost 0 VND).

### Fixes

- Billing renewal reads and writes, shared with `storage
  put-project-auto-renew`, now reject case-variant and duplicate response
  keys and oversized replies.

## v0.71.0 - Site-to-Site VPN List

### Highlights

- `network list-vpn-connections` lists site-to-site VPN connections in
  `hcm-3` and `han-1`, with `--page` and `--size`. SDK:
  `network.ListVPNConnections`. Each connection includes its package, VPC,
  subnet, and inline sites and tunnels with their IKE and IPsec settings.
- The API returns each site's pre-shared key in plain text; the SDK and CLI
  never keep, print, log, or capture it, and the account ID is left out.
- Verified live on 2026-10-11 through the SDK login and the CLI in both
  regions (no VPN existed at release time); the populated shape comes from a
  console capture of a real VPN, and the tests use synthetic fixtures.

## v0.70.0 - Public NAT List

### Highlights

- `network list-nat-instances` lists Public NAT gateways in `hcm-3` and
  `han-1`, with `--zone-id`, `--page`, and `--size`. SDK:
  `network.ListNATInstances`. Each item has the NAT name, status, gateway and
  public IPs, package, and VPC; the account ID and the zero price the API
  returns are left out. Other regions are refused before any request.
- Live tests moved from the repository root into `livetest/`; run them with
  `go test -tags live ./livetest/` (see `make live`).
- Verified live on 2026-10-11 through the SDK login in both regions, including
  a populated Hanoi list in PROVISIONING and ACTIVE states.

Older releases are in [docs/release-notes-archive-v0.69.md](docs/release-notes-archive-v0.69.md).
