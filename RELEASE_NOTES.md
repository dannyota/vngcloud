# Release Notes

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

## v0.59.0 - Encrypted Volumes

### Highlights

- `volume create-volume --encryption-type-id <id>` and `compute
  create-server --root-disk-encryption-type-id <id>
  [--data-disk-encryption-type-id <id>]` create encrypted volumes; the
  quote commands take the same flags. Ids come from `volume
  list-encryption-types` (`aes-xts-plain64_128`, `aes-xts-plain64_256`),
  whose decoding is fixed for the API's `key`/`displayKey` objects.
- Pricing, live in `HCM03-1C`: a server with an encrypted root disk quotes
  +85,140 VND a month on `s2-general-1x2` (432,940 against 347,800); an
  encrypted volume costs the same as a plain one (32,000 VND for 10 GB).
  The quote and the create's price guard send one body, so the guard
  prices encryption too.
- Verified live on 2026-10-10: an encrypted 10 GB volume created, read
  back with its type, and deleted with the refund posted; a server with
  encrypted root and data disks created, a separately created encrypted
  volume attached and detached, everything deleted and refunded. Read the
  type back with `volume get-volume` (`get-underlying-volume` does not
  carry it).

## v0.58.0 - Priced-Only Quotes

### Highlights

- `quote-create-server` now needs only `--zone-id`, `--flavor-id`,
  `--image-id`, `--root-disk-size`, and `--root-disk-type-id` (data disk
  size and type together when wanted); `--name`, `--vpc-id`,
  `--subnet-id`, `--security-group-id`, and `--ssh-key-id` are optional
  and left out of the request. `quote-create-volume` makes `--name`
  optional; `quote-create-load-balancer` makes `--name`, `--scheme`,
  `--subnet-id`, and `--type` optional. Optional values that are set are
  still shape-checked before any request.
- The paid creates' price guards quote the same priced-only body as the
  quote commands, so a guard and a quote always price the same request.
  Live, the priced-only quotes return the same amounts as the full ones
  (347,800 VND for `s2-general-1x2` with Ubuntu 24.04 and a 20 GB SSD
  root; 951,600 VND for `s2-general-2x4` with 40 GB root and 80 GB data).
- Note: `volume list-volume-types --iops 3000` in `HCM03-1A` returns two
  types both named "3000" with different prices; pick by `ID`.

## v0.57.0 - Flavors and Volume Types by Zone

### Highlights

- `compute list-flavors --zone-id <zone> [--name <flavor>]` lists the
  zone's flavor zones and then the flavors of each (one call per flavor
  zone), so one command finds `s2-general-2x4` in `HCM03-1A`; `--flavor-
  zone-id` still works and exactly one zone flag is required. `volume
  list-volume-types --zone-id <zone> [--iops N]` does the same over the
  zone's volume type zones. Rows keep the API's own `ZoneID`; filter by
  `FlavorZoneID` or `VolumeTypeZoneID`.
- New wiki page "IDs for create-server": each ID the create needs and the
  SDK call and CLI command that finds it. Every list returns
  `{"Items": [...]}`, so a `--query` starts with `Items[...]`; the CLI page
  now says so.
- SDK: `compute.ListFlavorsInput{ZoneID, Name}` and
  `volume.ListVolumeTypesInput{ZoneID, IOPS}`.

## v0.56.2 - IAM Timestamp Decode Fix

### Highlights

- IAM reads accept timestamps as a number, a numeric string, a Mongo
  `$numberLong` object, or an RFC 3339 string with or without fractional
  seconds and an offset; the accounts API now sends `createdAt` as an RFC
  3339 string on some service account rows, which failed
  `iam.ListServiceAccounts` until this fix. Exported fields stay `int64`
  epoch milliseconds.

## v0.56.1 - CLI Usability Fixes

### Highlights

- `vngcloud --version` and `-v` print the version like `vngcloud version`.
- `vngcloud configure list` prints a hint on stderr naming the config and
  credentials file paths it read when no region is set; stdout is
  unchanged.
- The Configuration wiki page gains a "Read-only agent profile" recipe
  (profile, credential keys, `region` with no default, `read_only` set
  last, how to verify).
- Notes: `portal list-zones` may report `HCM03-1A` with `IsEnabled=false`
  while quotes and creates work there; the gateway's quote text shows the
  root volume type id where the size belongs, the size is the
  `--root-disk-size` value.

## v0.56.0 - vCDN API Key, Certificates, and API Key Reads

### Highlights

- New `cdn` package calls on the documented vCDN API: `ListCertificates`,
  `GetCertificate`, and `ListAPIKeys`, with `vngcloud cdn
  list-certificates`, `get-certificate`, and `list-api-keys`. The vCDN
  API key comes from `vngcloud.WithCDNAPIKey`, `VNGCLOUD_VCDN_API_KEY`,
  or `vcdn_api_key` in the credentials file (`vngcloud configure set
  vcdn_api_key -` reads it from stdin); it is never a flag and never
  printed. The key is created once in the vCDN Portal by the root
  account.
- The API returns every certificate's private key and every API key's
  token to any valid key. The SDK marks these reads sensitive, drops the
  private keys, tokens, and the account email from its models, and the
  capture hook never sees their responses. `APIKey.Current` marks the key
  in use.
- Error handling for the vCDN API: an empty 401 becomes a fixed message,
  HTTP 200 envelopes with `success: false` become errors with the envelope
  code, problem+json bodies are read, and any message carrying an email
  address is replaced.
- Web Accelerator reads and analytics follow once the live body shape is
  confirmed on a real CDN: the documented `webacc/*` routes do not exist
  on the server; the live prefix is `cdn/*`.

## v0.55.0 - vStorage Bucket Policy, Versioning, and CORS

### Highlights

- New `storage.GetBucketPolicy`, `PutBucketPolicy`, and `DeleteBucketPolicy`,
  with `vngcloud storage get-bucket-policy`, `put-bucket-policy`
  (`--policy` takes the document or `file://path`), and
  `delete-bucket-policy`. The put refuses anything that is not a JSON
  object with a non-empty `Statement` array before any request; the server
  re-serializes the stored document, so compare decoded documents, not
  strings. A delete with no policy present succeeds. `put-bucket-policy`
  needs `--yes` when a statement names a public principal (`"*"` or
  `{"AWS":"*"}`), since that exposes the bucket to anonymous readers. A
  statement without a non-empty `Effect`, `Principal`, `Action`, and
  `Resource` is refused before any request: the server accepts one and
  the bucket's console policy and delete calls then answer empty bodies.
  Member names must be spelled canonically (`Principal`, not
  `principal`): the server refuses other spellings with code 400 and the
  SDK refuses them first. For a public read use `"Principal": "*"`; the
  `{"AWS": "*"}` form makes the console refuse the policy and bucket
  deletes with code 403 until the policy is removed through S3.
- The per-bucket key now works end to end: a policy that names a service
  account's principal grants its attached key that bucket and nothing
  else. The wiki gives a template with named object actions (not `s3:*`)
  and the order: bucket, service account, principal, policy, key with
  `--service-account-id`. A policy naming no real principal is accepted by
  the server and grants nothing; an attached key can still list and create
  buckets but cannot use a bucket it creates.
- Verified live on 2026-10-09 on the test project with Signature V4
  requests: allowed on the named bucket, denied elsewhere, denied again
  after the policy was deleted.
- New `storage.GetBucketVersioning`, `PutBucketVersioning`, `GetBucketCORS`,
  `PutBucketCORS`, and `DeleteBucketCORS`, with the matching `vngcloud
  storage` commands. Versioning reads `Enabled` and `Status` (`Off`,
  `Enabled`, or `Suspended`; the server never returns to `Off`), and the
  put requires an explicit value. CORS rules are checked before any
  request (origins, methods within GET, PUT, POST, DELETE, HEAD, max age);
  `ExposedHeaders` is read-only: the server exposes headers only when a
  rule sets `ExposeAllowedHeaders`, and then exposes the allowed headers
  (so browsers can read `ETag` after an upload). A CORS delete with no
  rules succeeds.
- A versioning, CORS, or policy call on a bucket that no longer exists
  answers an empty body on this server; the SDK now confirms with one
  bucket read and returns `NotFound`.
- `storage.PolicyHasPublicPrincipal` tells whether a policy document's
  Allow statements name a public principal; `put-bucket-policy` uses it
  for its `--yes` rule.
- Public access and ACL routes are not covered: the console's
  `public_access` endpoint answers 403 for an IAM user, and public reads
  are granted with a bucket policy instead (template in the wiki).
- Verified live on 2026-10-09 on the test project, including a CORS
  preflight before and after the rule delete.

## v0.54.0 - vStorage Service Account Keys

### Highlights

- New `storage.AttachS3Key`, `DetachS3Key`, and
  `EnsureServiceAccountPrincipal`, with `vngcloud storage attach-s3-key`,
  `detach-s3-key` (both `--yes`), `ensure-service-account-principal`, and
  `create-s3-key --service-account-id`, which creates the key, attaches it,
  and only then writes the secret file. An attached key carries the service
  account's principal in `S3Key.SubUserID` (the console's "Restriction by
  IAM") and has no bucket rights until a bucket policy names that
  principal; it can still list and create buckets.
- The principal is created explicitly: `EnsureServiceAccountPrincipal`
  sends the console's `generated=true` read for `sa-<id>` and returns
  `SubUserID` and `PrincipalARN`; it refuses anything that is not a service
  account principal.
- Attach and detach are sent once; the server refuses a repeat attach, an
  attach of a key bound elsewhere, and a detach of an unbound key with code
  `114` and a message the CLI prints.
- Verified live on 2026-10-09 on the test project with a service account
  created and deleted for the run.

## v0.53.0 - vStorage S3 Keys

### Highlights

- New `storage.ListS3Keys`, `CreateS3Key`, and `DeleteS3Key` on the
  vStorage console API, with `vngcloud storage list-s3-keys`,
  `create-s3-key`, and `delete-s3-key` (`--yes`). A key belongs to a
  project (global `--project-id`), has the creating IAM user's rights on
  every bucket of that project, and a project holds at most ten keys (the
  server refuses the 11th with code `114`).
- The secret is shown once. `SecretKey` is a `vngcloud.Secret` that prints,
  logs, and marshals as `[redacted]`; the create is sent once and its
  response never reaches a capture hook. `create-s3-key` needs
  `--secret-file <path>` and writes an AWS shared credentials file with mode
  0600 (`AWS_SHARED_CREDENTIALS_FILE` for rclone or the AWS CLI); it refuses
  an existing path or symlink, and deletes the new key if the write fails.
- The IAM accounts API key endpoints are not used: on an IAM user the
  create answers 500 and the IAM console cannot create a key either. A
  repeat delete of a key is refused by the server with code `114`.
- Verified live on 2026-10-09 on the test project: create, list, ten-key
  limit, delete, repeat delete, and `aws s3 ls` against
  `hcm04.vstorage.vngcloud.vn` with the new key.

## v0.52.1 - IAM Accounts API Error Codes

### Highlights

- Errors from the IAM accounts API arrive as `{"errors":[{"code","message"}]}`;
  the SDK now reads that wrapper, so `*APIError.Code` carries codes such as
  `NOT_FOUND_S3_KEY` and the CLI prints them instead of the bare HTTP status
  text. An empty or null list still falls back to the status text, and
  sentinel mapping is unchanged.

Older releases are in [docs/release-notes-archive.md](docs/release-notes-archive.md).
