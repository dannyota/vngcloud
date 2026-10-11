# Release Notes

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

## v0.69.0 - CDN Path Purge

### Highlights

- `cdn purge-paths --cdn-domain <cdn-domain> --cli-input-json
  '{"Paths":["/index.html"]}'` purges cached paths on a Web Accelerator.
  SDK: `cdn.PurgePaths`. At least one non-empty path is required, and a path
  cannot contain `*`; the server refuses a bare `/`. A purge within 30 seconds
  of the previous one returns `PurgeCooldown` (`cdn.ErrPurgeCooldown`). The
  purge is never resent after a server error or an unclear network failure.
  The Basic package allows five purges a day.
- Verified live on 2026-10-11 on a portal-made test CDN, which the live test
  then deleted.
- Release notes v0.52.1 to v0.59.0 moved to
  [docs/release-notes-archive-v0.59.md](docs/release-notes-archive-v0.59.md).

## v0.68.1 - Server User Data Fix

### Fixes

- `compute create-server --user-data-file` always failed with HTTP 400:
  GreenNode refuses user data together with an SSH key ("User data don't allow
  input username, password and ssh key."), and `--ssh-key-id` was required.
  Now set exactly one of `--ssh-key-id` or `--user-data-file`; with user data,
  the cloud-config installs the login keys (for example
  `ssh_authorized_keys`). Both or neither is refused before any request. SDK:
  `CreateServerInput.SSHKeyID` is optional and `sshKeyId` is omitted when
  empty.
- `compute import-ssh-key` help now says GreenNode accepts RSA public keys
  only; ed25519 and ECDSA keys are refused with 400 "Invalid public key".
- Verified live on 2026-10-11 in `hcm-3`: a server created with user data and
  no SSH key reached ACTIVE, cloud-init ran the user data, and the server was
  deleted (8 VND net).

## v0.68.0 - Server Console Log

### Highlights

- `compute get-server-console-log --server-id <id>` reads a server's boot and
  serial output, for a server that SSH cannot reach. Logs can hold passwords
  and keys. `--output text` writes the log as is to a pipe or file; on a
  terminal it escapes control characters other than newline and tab, so a log
  cannot drive the terminal. JSON prints `{"Log": "..."}`. SDK:
  `compute.GetServerConsoleLog`, whose `Log` is a `vngcloud.Secret` (hidden
  from formatting, JSON, and slog until `Log.Reveal()`).
- The log never reaches stderr, error messages, `--debug`, or captures. The
  response is capped at 8 MiB, with no partial output; redirects are refused.
- Every command's text and table output now also escapes C1 controls and
  bidirectional format characters (for example U+009B and U+202E), not only
  C0 and DEL, and JSON output writes them as `\u` escapes that decode to the
  same value.
- Verified live on 2026-10-11 in `hcm-3`: a 100 KB log read through the SDK
  and the CLI, piped, as JSON, and on a pseudo-terminal; an unknown server ID
  exits 4 with `NotFound`.

## v0.67.0 - vStorage Project Auto-Renew

### Highlights

- `storage get-project-auto-renew --project-id <id>` joins the project list
  with billing resources and shows the renewal type, period, term end, and a
  fresh renewal estimate (VAT included). SDK: `storage.GetProjectAutoRenew`.
- `storage put-project-auto-renew --project-id <id> --enabled=true
  --period-months 1 --max-price 30000` turns auto-renew on or changes the
  period; `--enabled=false` turns it off. `--enabled` is required. Enable and
  period changes quote first and refuse above `--max-price` (default 0
  allows none); disable needs no price. One PUT, no retries; the result is
  confirmed by reads, and an uncertain outcome returns `NotSettled`. The cap
  checks today's estimate only: auto-renew can charge every period until
  turned off. SDK: `storage.PutProjectAutoRenew`.
- `billing list-resources` lists prepaid resources across products with
  their renewal type and billing times. SDK: `billing.ListResources`.
- Responses that feed a price or auto-renew guard now fail with an API
  error when a JSON object repeats a key: the project catalog, price quotes,
  the project list read by auto-renew, and billing resources. This also
  covers `create-project`, `quote-create-project`, and `list-project-types`.
- Verified live on 2026-10-11 in `HCM04` with an IAM user login: enable for
  one month, change to three months, and disable each confirmed by reads,
  with no charge.

## v0.66.1 - VAT-Inclusive Prices

### Highlights

- Quotes, price lists, and the price guards of paid creates are VND totals
  that include VAT, as GreenNode's invoice documentation states. The help of
  every quote and paid create command and the wiki now say so. The API
  returns no VAT amount or rate, so none is shown.

## v0.66.0 - vStorage Bucket Encryption

### Highlights

- `storage create-bucket --encryption` creates a bucket, turns on default
  encryption, and reads it back; it succeeds only when encryption reads as
  on. If enabling fails after the bucket exists, it returns
  `BucketEncryptionIncomplete` naming the bucket and never deletes it.
- `storage get-bucket-encryption` and `storage put-bucket-encryption
  --enabled=true|false` read and set it; `--enabled` is required, so an
  omitted value cannot turn encryption off. SDK: `GetBucketEncryption`,
  `PutBucketEncryption`, and `CreateBucketInput.Encryption`.
- Verified live on 2026-10-10 in `HCM04`: encryption is SSE-S3 (`AES256`)
  and applies to uploads only; objects stored before enabling stay
  unencrypted and encrypted objects stay encrypted after disabling.
  Conditional `PutObject` (`If-None-Match: *` gives 412 on an existing
  key), multipart uploads, and `DeleteObjects` work on an encrypted bucket.
  S3 copy of an encrypted object fails with 501 `NotImplemented`.
- The wiki records HCM04 S3 compatibility notes (conditional requests,
  `KeyCount`, OpenTofu state locking).

## v0.65.0 - vStorage Project Purchase and Delete

### Highlights

- `storage create-project --region hcm-3 --name <name> --type Gold
  --quota-gb 30 --max-price 30000` buys a one-month vStorage project and
  charges the account balance at once. It quotes first and refuses when the
  quote is missing, zero, or above `--max-price` (default 0 buys nothing),
  sends one order with auto-renew off, never retries, and confirms the new
  project by reads. SDK: `storage.CreateProject`.
- `storage delete-project --project-id <id> --yes` deletes an empty project;
  a project with buckets is refused. SDK: `storage.DeleteProject`.
- Uncertain outcomes return `NotSettled` with the steps to check before any
  retry; a checkout-only answer returns `PaymentRequired`.
- Verified live on 2026-10-10 in `HCM04`: one order for Gold 30 GB charged
  exactly 30,000 VND, the project was active at once, and delete refunded
  the full amount (0 VND net).

## v0.64.0 - vStorage Project Prices

### Highlights

- `storage list-project-types` lists the vStorage project types a region
  offers, with the monthly price at the region's minimum size and the
  minimum and maximum size in GB; `storage quote-create-project --type Gold
  --quota-gb 30` prices a package without ordering. The SDK methods are
  `ListProjectTypes` and `QuoteCreateProject`.
- Live on 2026-10-10 in `HCM04`: Gold costs 1,000 VND per GB a month (30 GB
  is 30,000 VND) and Instant Archive 530 VND per GB (30 GB is 15,900 VND);
  the minimum is 30 GB. A size outside the region's limits is refused
  before any price call.
- Buying and deleting a project come in a later release.
- The CLI now renders raw JSON fields as JSON instead of byte arrays.

## v0.63.0 - Backup Center Backends and Policies

### Highlights

- New `backup` package and CLI group: `backup list-backends` and `backup
  list-policies` read Backup Center in `hcm-3`. Other regions are refused
  before login.
- `list-policies` reads one page, page 1 and size 200 by default. Policies
  carry the daily schedule and the enable flag of every cadence; hourly,
  weekly, and monthly details are not included.
- `--project-id` does not scope these reads. Backup Center is separate from
  the `volume` snapshot policies, and its backend IDs are its own.
- Verified live on 2026-10-10 in `hcm-3` through the SDK login.

## v0.62.0 - Snapshot Policies

### Highlights

- `volume list-snapshot-backends --name HCM-03` finds the snapshot backend
  ID, and `volume list-snapshot-policies --backend-id <id>` lists the
  snapshot policies in the profile's project, with `ListSnapshotBackends`
  and `ListSnapshotPolicies` in the SDK. Both work only in `hcm-3`.
- Policies carry their type, timezone, hourly and daily settings, snapshot
  counts, and the enable flags for every cadence. Weekly and monthly
  details are not included until their fields are verified.
- Snapshot backend IDs belong to the vServer snapshot gateway and are not
  Backup Center IDs.
- Verified live on 2026-10-10 in `hcm-3` through the SDK login.

## v0.61.0 - VKS Cluster Inventory

### Highlights

- New `vks` package and CLI group: `vks list-clusters`, `vks
  list-cluster-versions`, and `vks get-quota` in `hcm-3` and `han-1`,
  using the profile's IAM User login. Other regions are refused before
  login.
- `list-clusters` pages from 0 with a default size of 10; `--project-id`
  does not select a VKS workspace.
- Verified live on 2026-10-10 in both regions through the SDK login: the
  cluster list, version catalog, and quota decode. The test account has no
  clusters in either region, so cluster item decoding is checked against the
  published schema only.
- Cluster detail, node groups, nodes, events, and kubeconfig are not
  included.

## v0.60.1 - Transport and Login Hardening

### Highlights

- Every API request enforces the same-host, same-scheme redirect rule,
  including with a client from `WithHTTPClient` and after that client's own
  redirect hook. An HTTPS request never follows a redirect to HTTP.
- IAM User login posts the TOTP code only to the sign-in origin and refuses
  a login page redirect that changes scheme.
- The access token or vCDN API key a request sent is replaced with
  `[redacted]` in error codes, messages, debug log paths, and captured
  response bodies. This covers errors built from success-status envelopes in
  billing, storage, and vCDN.
- Transport failures use fixed descriptions, such as `canceled`,
  `timed out`, `no such host`, `connection reset`, a TLS certificate class,
  or `response failed to decode`, and never a request URL, redirect
  location, or query value.

### Compatibility

- `errors.As` no longer reaches the original `*url.Error` or other raw
  transport cause inside an SDK error. `errors.Is` still matches
  `context.Canceled` and `context.DeadlineExceeded`, and a timeout still
  satisfies `net.Error` with `Timeout()` true.
- An error code a server sends as a JSON object or array falls back to the
  status-derived code.

## v0.60.0 - CDN Web Accelerators

### Highlights

- The SDK and CLI list and read Web Accelerators, traffic reports, traffic,
  request rates, cache status, and HTTP codes through the vCDN API.
  Configure `VNGCLOUD_VCDN_API_KEY` or the `vcdn_api_key` profile key.
- Update, enable, disable, and delete read the CDN status before writing.
  Update merges changes into the current settings and preserves fields the
  SDK does not model. Each write sends once. Update, enable, and disable
  wait up to six minutes unless `--no-wait` is set.
- Delete and disable require `--yes`. Read-only profiles refuse every
  write and allow analytics reads. A wait that ends after an accepted
  write returns `NotSettled` with the last read, including on cancellation.
- Create remains a portal operation because the API rejects valid create
  bodies. Purge and certificate writes remain planned separately.

Older releases are in [docs/release-notes-archive-v0.59.md](docs/release-notes-archive-v0.59.md).
