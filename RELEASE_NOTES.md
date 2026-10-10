# Release Notes

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

## v0.52.0 - vStorage Bucket Create and Delete

### Highlights

- New `storage.CreateBucket` and `DeleteBucket`, with `vngcloud storage
  create-bucket` and `delete-bucket` (`--yes`, global `--project-id`). The
  create sends the console body for a bucket without object lock and
  returns the bucket as read back; a repeat create of a name the account
  owns is idempotent. The delete reads the bucket first and refuses with
  `storage.ErrBucketNotEmpty` (CLI code `BucketNotEmpty`) when the object
  count is above 0, null, or the size is above 0, sending nothing.
- The server deletes asynchronously, so `DeleteBucket` polls `GetBucket`
  every second for up to 30 s until the bucket is gone; `NoWait`
  (`--no-wait`) skips the wait, and the bound returns
  `storage.ErrNotSettled` (CLI code `NotSettled`).
- Envelope code 112, the server's input refusal (for example an upper-case
  bucket name), now matches `vngcloud.ErrInvalidInput`; the CLI prints code
  `112` and exits 2.
- Verified live on 2026-10-09 on a Gold 30 GB project in `HCM04`: create,
  read back, duplicate create, invalid name, delete, and repeat delete.

## v0.51.1 - vStorage Reads Fix

### Highlights

- Every `storage` call except `ListRegions` now sends the region ID in both
  the `region` and `region_id` headers. The server scopes results by
  `region`, so v0.41.0's `ListProjects` returned an empty list and
  `ListBuckets` failed with code 114 on an account that has a project.
- `testdata/storage/list_projects.json` now follows the live project shape;
  the `Project` model is unchanged and `Period` decodes as zero when the API
  returns null.
- Verified live on 2026-10-09 in `HCM04` against a Gold 30 GB project:
  `ListProjects` found it and `ListBuckets` listed zero buckets.

## v0.51.0 - vMonitor Log Alarm Writes

### Highlights

- New `monitor.CreateLogAlarm`, `UpdateLogAlarm`, and `DeleteLogAlarm`,
  with `vngcloud monitor create-log-alarm`, `update-log-alarm`, and
  `delete-log-alarm`. A create needs an `ACTIVE` log project, a threshold,
  and at least one channel; the POST is sent once and the wait settles only
  when the alarm reads `ACTIVE` (up to 120 s). An update reads the alarm
  first, resends every unset field as the console does, and refuses while
  the alarm is still settling (the server answers 403 for about 30 s after
  a create). `delete-log-alarm` needs `--yes`.
- Read model: `Alarm.Kind` and `Status` now decode the API's `type` and
  `progressStatus`; `Log.ID` carries the log detail's own id. A deleted
  alarm lingers in the list for a few seconds, and a repeat delete is
  refused by the server; the SDK confirms by listing and reports NotFound.
- Verified live on 2026-10-09 on a Pro log project: create settled in 4 s,
  update and delete passed, the project was deleted the same day and its
  unused month refunded (637 VND net).

## v0.50.0 - Load Balancer Policies

### Highlights

- New `loadbalancer.CreatePolicy`, `UpdatePolicy`, and `DeletePolicy` for
  Layer 7 listeners, with the matching `vngcloud loadbalancer` commands. A
  policy needs at least one rule and carries only the fields its action
  uses: `REDIRECT_TO_POOL` takes a pool ID; `REDIRECT_TO_URL` takes a URL,
  an HTTP code, and the keep-query-string flag. A field set for the wrong
  action is refused before any request. `delete-policy` needs `--yes`.
- An update reads the policy first and resends the unset fields of its
  action unchanged, then reads again to confirm.
- Verified live on 2026-10-09 on an `ALB_Small`: both policy kinds
  created, rules replaced, a pool a policy uses refused deletion, and
  everything deleted.

With this release every load balancer write in the design has shipped and
has been checked live. The whole day of paid checks cost about 574 VND net
after the deletes refunded the unused value.

Older releases are in [docs/release-notes-archive.md](docs/release-notes-archive.md).
