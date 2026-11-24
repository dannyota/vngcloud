# Release Notes

## v1.1.0 - vMonitor check notification flags

- Set per-state notification channels directly on `monitor create-check`
  and `update-check`. `pricing get-quote --help` points to vMonitor's
  product-specific quotes.

## v1.0.1 - README for the stable release

- The README states the semantic versioning promise and lists the supported
  resource creates, including credit-paid creates and their `--max-price` or
  `--yes` guards. No code change.

## v1.0.0 - First stable release

The SDK and CLI cover AWS CLI-style reads and creates for GreenNode, including
credit-paid creates for VKS, GLB (bare, TCP pool and listener), vDB MySQL, and
private endpoints. Public SDK names, types and fields, CLI flags, query keys,
and exit codes are now stable; a breaking change needs a new major version.
Site-to-site VPN and CDN Web Accelerator creates are out of scope.

- Unconfirmed writes now exit 1 even when confirmation returns 401 or 404;
  reconcile inventory and billing before another mutation.

- Add shared `vngcloud.ErrNotSettled` and `vngcloud.ErrFailed` identities; existing service names remain aliases.

- `volume get-default-volume-type` (SDK `GetDefaultVolumeType`) now requires
  `--zone-id` (`ZoneID`). Without it the API used the region's first zone,
  which is disabled in `hcm-3` and returned 404.
- `network list-nat-instances` no longer fails with `invalid regions
  envelope` in `hcm-3` and `han-1`; it ignores region fields it does not use.
- Document `compute list-flavors` flavor zones and the provider's internal
  zone UUID in `ZoneID`. When the API omits `ZoneID`, it falls back to the
  input `ZoneID`, which is empty when only `FlavorZoneID` is supplied.
- Discover the project for the resolved region when a profile's project ID belongs to a different configured region.

## v0.103.0 - Private endpoint create

- Add `network quote-create-endpoint`, `create-endpoint`, and
  `delete-endpoint` (SDK `QuoteCreateEndpoint`, `CreateEndpoint`,
  `DeleteEndpoint`) for one vStorage Standard endpoint in `hcm-3` on an
  existing HCM03-1C subnet. Create takes a fresh quote, needs `--yes` and
  `--max-price`, requests auto-renew off, and sends once; it reports no
  renewal or billing status. Delete refuses an endpoint with an attached
  resource or domain and promises no refund. See
  [Private endpoints](docs/wiki/Network-Endpoints.md). A recorded run
  debited the 712,400 VND monthly quote at create, reached `ACTIVE` in about
  3 minutes, and deleted in about 30 seconds.

## v0.102.0 - Global Load Balancer TCP pool and listener

- Add `globalloadbalancer create-pool`, `create-listener`, `delete-listener`,
  and `delete-pool` for an existing Layer 4 load
  balancer: one TCP pool with one private vServer backend, then one TCP
  listener limited to explicit IPv4 CIDRs from `/8` through `/32`. Every write
  needs `--yes` and is sent once. A create succeeds only on exact read-back,
  and an unconfirmed write returns `NotSettled` with the IDs. No quote is
  available; GreenNode bills child usage under the account's terms, and
  deleting does not promise a refund. Delete the listener first, then the pool;
  deleting the pool also removes its member group and backend row, never the
  vServer. A recorded `hcm-3` run created the pool in about 145 seconds and
  the listener in about 23, and no child create or delete changed the
  account balance.

## v0.101.0 - vDB MySQL instance create

- Add `database create-database-instance` and `delete-database-instance` for
  a private, single-node MySQL 8.0 instance with backups off, paid from
  credits with no price check (a flavor's `monthlyCost` is a catalog
  estimate). Create needs `--yes` and a `--password-file`; both writes send
  once and report `NotSettled` with the ID when unconfirmed. A recorded
  `db.s1-highcpu-2x2` run with 20 GB debited about 659,660 VND, reached
  `ACTIVE` in about 3 minutes, and deleting it refunded all but 15 VND.

## v0.100.0 - Private VKS cluster create

- Add `vks create-cluster` and `delete-cluster` for a private cluster with
  one private node group, paid from credits with auto-renew off. A recorded
  one-node run debited about 2,137,000 VND, reached `ACTIVE` in about 4
  minutes, and deleting it refunded all but 345 VND.

## v0.99.0 - Global Load Balancer create

- Add `globalloadbalancer create-load-balancer` and `delete-load-balancer`
  for a bare Layer 4 load balancer, paid from credits. Create holds a
  three-day forecast of credits (176,000 VND for `GNLB_4000` in a recorded
  run, shown as `CashHolding`); deleting did not release the hold within
  minutes. Listener and pool creation follow in a later release.

## v0.98.0 - Core, storage, SDK input, and CLI hardening

- Harden compute, network, and portal identity, credential, inventory,
  replay, and wait guards. Server-group inputs rename `Page`/`Size` to
  `Offset`/`Limit`; migrate CLI flags to `--offset`/`--limit` and JSON input
  keys accordingly. Aggregates require complete parent and child inventories;
  route operations retain credential history and reject unsafe output. Invalid
  ACL associations block subnet deletion. NAT errors retain safe causes and
  uncertain reboot recovery uses reads.
- Harden billing and pricing responses: withhold credential echoes, reject
  malformed data and inexact amounts, and retain observed error statuses.
  Budget writes cap replies at 8 MiB and send once; budgets and quotes refuse
  redirects. Reconcile unconfirmed budget changes before retrying.
- Send bucket deletes once and require stopped writers. Reject ambiguous
  project-bucket lists, reflected S3 secrets, and missing or mismatched volume
  state. Complete snapshot pagination, enforce wait deadlines, and allow
  discovery waiters to cancel independently. Key recovery requires proven
  ownership. Reject explicit null pagination and retain definite bucket
  refusals, including NotFound exit 4. SDK fields and CLI flags stay compatible.
- Breaking: `IAMUserAuth.Password` and `SecretTOTP.Secret` now use
  `vngcloud.Secret`; wrap string variables with `vngcloud.Secret(value)`.
  IAM options snapshot credentials and known TOTP seeds; rebuild configs to
  change identities. Unsafe regions, cache entries, malformed inventories,
  and invalid token lifespans fail closed. Ordinary responses default to
  8 MiB, IAM login replies to 1 MiB, and retained discovery bodies to 32 MiB.
  Shared auth and project waiters cancel independently without changing the
  holder's state. No TOTP step is sent twice: a later login in the same
  30-second step waits for the next one, and only server-rejected logins are
  held for 30 seconds.
- Breaking: resize outputs rename `MonthlyPrice` to `QuotedAdjustment`, the
  signed remaining-term quote; CLI JSON and query keys follow.
  `PutBucketEncryptionInput.Enabled` is a required `*bool`, and nil sends
  nothing. Rename field reads and pass a pointer to the intended boolean.
- Breaking: query failures no longer include result values, ambiguous projects
  report `InvalidConfig`, and ACL rule removal rejects null priority. Use the
  error class instead of parsing query details and provide `--priority 0` or
  JSON `"Priority":0` explicitly when targeting priority zero. Monitor check
  creation now accepts repeatable `--locations` flags.
- Breaking: table output now aligns heterogeneous rows by the union of their
  keys. Tables exceeding 100 columns, 1,000,000 cells, or a projected 64 MiB
  are refused; use `--output json` for larger results and scripts parsing named
  keys instead. Do not rerun a write that succeeded before a rendering refusal.
  DNS `--vpc-ids`, monitor `--locations`, `--in-alarm`, and `--ok` accept
  repeatable values that override JSON. To set an explicit empty list, use
  `--cli-input-json`.
- Serialize config and credentials edits with per-file locks so a concurrent
  edit cannot clear a read-only profile, and write nothing when configure
  input or a lock wait is canceled.
- Runtime `--help` shows the same operation notes as the generated reference,
  including consent, destructive confirmation, ACL priority zero, and the
  pricing estimate resource shape, and marks each flag's type and whether it
  is required.

## v0.97.0 - Pre-1.0 hardening

- Load balancer member writes remerge fresh state after busy refusals and
  never replay ambiguous replacements. Waits enforce deadlines; `NoWait`
  outputs include submitted fields. CDN writes reject missing status or
  mismatched identity in detail responses.
- Harden Monitor order replay, price guards, credential withholding, name
  searches, check concurrency, and waits. Reject ambiguous quote/alarm fields;
  preserve ordinary check data. Error redaction uses `[redacted]`. SDK fields
  and CLI flags stay compatible.
- Harden IAM, registry, DNS, and tag mutation evidence and credential privacy.
  Secret-only registry creates now retain the secret with `ErrUserNotFound`
  and no cleanup ID; missing IAM create IDs retain secrets as uncertain
  output. Paged IAM outputs add nullable `PageNumber`, `PageSize`,
  `TotalItems`, and `TotalPages`; use keyed literals and update queries.
  Unsupported DNS and registry continuation returns an explicit incomplete
  result. Malformed snapshots fail, replacements send once, waits reject
  late success, and TXT confirmation preserves payload case. CLI secret-file
  recovery refuses name-based cleanup and protects issued values through
  guarded cleanup, errors, and saved-file diagnostics; cleanup messages omit
  IDs. For create cleanup use `CreateServiceAccountOutput.CleanupInput()`.
  Use keyed SDK literals and compare public fields explicitly because equality
  includes private state identity. Incomplete registry names refuse creates;
  DNS operation timeouts retain `ErrZoneBusy`.
- Harden developer helpers with private browser capabilities, operation gates
  for live writes, and offline safety checks in CI.
- `pricing.Client.Estimate` returns the caller's context error when its
  deadline ends during a shared lookup, instead of a late result.

## v0.96.0 - Setup cost estimate

- Add SDK `pricing.Client.Estimate` and CLI `pricing estimate` to quote a
  specified setup of servers, separate volumes, public IP products, and
  monthly object storage packages without buying resources.
- Quantities project independent monthly units. Server quotes include their
  root and bundled data disk; separate volumes count once. Prices use VND
  with VAT as returned, without inferred discounts or a price lock.
- Show a known subtotal, a nullable requested total, and cost coverage.
  Usage, unsupported products, and unverified IP costs remain unknown or
  excluded, so the estimate never claims to be a complete bill.
- The CLI accepts a strict JSON setup and supports read-only profiles.
  `--allow-partial` retains surviving results with an error and exit 1;
  deliberate unknown prices can complete successfully.

## v0.95.0 - Floating IP on server create

- `compute create-server --attach-floating-ip` (SDK `CreateServerInput.AttachFloatingIP`)
  asks GreenNode to allocate a floating IP and attach it to the new server's
  primary interface. It is opt-in; without it the create request sends
  `attachFloating: false` and nothing else changes. The SDK waits for the
  address unless `NoWait` is set, and `Server.PublicIP` shows it.
- Before quoting and again right before sending, the SDK reads the named
  security groups and refuses with `ErrOpenLogin` (CLI code `OpenLogin`) when
  TCP 22, 234, 3389, or 3490 is open to `0.0.0.0/0` or `::/0`, or when a rule
  uses an encoding it cannot prove safe, such as ICMP or UDP; the message says
  which. `--allow-open-login` (SDK `AllowOpenLogin`) overrides only that check.
- Pricing: monthly creates in `han-1` and `hcm-3` include the IP in the
  server order. `quote-create-server --attach-floating-ip` uses one server
  quote and a zero-price `FLOATING IP` line there. Every other region quotes
  the server and Elastic IP and sums them for totals, `--max-price`, and
  `MonthlyPrice`; the IP line labels a quoted charge with an unconfirmed
  extra create debit. The resolved region selects the path before quoting,
  without a runtime ledger lookup. Direct Elastic IP quotes are unchanged.
- `network list-all-security-group-rules` shares a bounded credential history
  across nested reads and checks retained output against the completed
  history, withholding credential reflections across token refreshes.
- Server create confirmation now checks the server ID on every read, so a
  failed wait never reports another server.
- Compatibility: `CreateServerInput` gains `AttachFloatingIP` and
  `AllowOpenLogin` before existing fields; positional struct literals stop
  compiling. Use keyed fields.
- The wiki moves price quotes, vServer prices, and discovery metadata to a
  new Price Quotes page.

## v0.94.1 - Go security update and quick starts

- Require Go 1.27.4, which fixes
  [GO-2026-6859](https://pkg.go.dev/vuln/GO-2026-6859). Update the CLI's
  terminal dependencies and CI tool pins. No CLI or SDK API changes.
- Lead the README with read-only CLI and Go SDK quick starts. Link to the
  repository's docs and invite voluntary feedback without adding telemetry.

## v0.94.0 - Billing history

- Add SDK `billing.ListTransactions` and `billing.ListPayments`, plus
  `billing list-transactions` and `billing list-payments`. Dates are inclusive
  in fixed UTC+07:00; an omitted end means today. Both reads support bounded
  complete walks and read-only profiles. Existing billing commands and
  JSON/query roots retain their contracts.
- Incomplete history walks return `ErrHistoryIncomplete` with nil Output;
  the CLI reports `HistoryIncomplete`, exit 1, with no report. Complete
  walks detect coverage changes but do not establish an atomic snapshot.
- JSON, text, and explicit queries can contain account data. Default tables
  omit descriptions and references. Values preserve the API's monetary
  basis; historical VAT normalization and service-account access remain
  unverified. Register both reads in the example and live suite.

## v0.93.3 - Bounded billing reads

- The eight billing reads (`get-cost-overview`, `list-cost-resources`,
  `get-balances`, `list-budgets`, `get-budget`, `get-current-period-cost`,
  `list-budget-thresholds`, and `list-budget-alerts`) now refuse response
  bodies over 8 MiB. An oversized 2xx response returns `InvalidResponse`
  with its HTTP status and no body text; an oversized 4xx or 5xx response
  keeps its status-based error but fails without retry or token refresh.
- Responses within the limit, budget writes, and output shapes are
  unchanged.

## v0.93.2 - Cost explorer overview fix

- `billing get-cost-overview` (SDK `GetCostOverview`) showed every
  breakdown cost as 0 and empty series, because the live API sends
  `value` and `percent` in each breakdown row and groups series by key
  with `x`/`y` points. `Summary.Breakdown[].Cost` now reads `value` and
  gains `Percent`; the new `GroupedSeries` holds one series per group with
  its points. Responses in the old flat shape still fill `Series`.
- The API refuses `--interval hourly` and `--group-by resourceId` with HTTP
  400; the wiki notes this.

## v0.93.1 - Quote response safety

- `pricing get-quote` (SDK `pricing.GetQuote`) and every command that
  quotes before a paid write now refuse a quote response that echoes a
  credential the call sent, raw or JSON-escaped and including a refreshed
  token, before returning a price; the error text never contains it.
  Routine quote responses are no longer passed to `WithResponseCapture`.
- If a credential the call sent could be read as a number, quotes fail
  closed with `InvalidResponse`. Real bearer tokens are long opaque strings,
  so normal use is unaffected.
- A malformed quote response still yields a `*json.SyntaxError` with its
  offset through `errors.As`, without the response text.

## v0.93.0 - Server public address

- Server reads (`compute get-server`, `list-servers`,
  `network list-servers-by-security-group`, and every write that returns
  a server) now show `PublicIP`: the server's floating IP, taken from its
  internal interfaces, so `--query Server.PublicIP --output text` answers
  "what is this server's public address" in one command. It is empty when
  the server has no valid public floating address; an empty value does not
  prove that no IP is allocated.
- Compatibility: `compute.Server` gains the `PublicIP` field, which breaks
  unkeyed `compute.Server{...}` literals, and CLI JSON for servers gains a
  `PublicIP` key. The SDK's own JSON encoding of `Server` is unchanged
  (`json:"-"`).
- Server responses are bounded at 8 MiB, and server reads and writes
  refuse a response that echoes a credential the operation sent, without
  returning the response. Redacted errors keep their identity for
  `errors.Is`, and expose `*APIError` through `errors.As` only when it
  holds no credential.
- The response scanner shared with NAT reads now fails closed on JSON it
  cannot scan completely.

## v0.92.0 - Resize quote credits

- `compute quote-resize-server` (SDK `QuoteResizeServer`) and
  `pricing get-quote --action resize` now return a server downsize quote.
  GreenNode prices a downsize as a negative prorated credit, which the SDK
  used to refuse as an invalid price. `OptimumPrice` and `MonthlyPrice` are
  signed: a negative value is a credit, and JSON, table, and text output
  show the minus sign. Create quotes still refuse a negative price, and a
  missing, null, NaN, or infinite price is still refused for every action.
- `compute resize-server` (SDK `ResizeServer`) now lets a quoted credit
  pass at every valid `--max-price`, including the default 0; a quote of
  exactly 0 still fails as unpriced. `--max-price` bounds the quote, not
  the final charge: the resize request carries no price, so if the server
  or prices change between the quote and the send, a quoted credit can
  become a charge.
- `volume resize-volume` still refuses a quote at or below 0 and now names
  the amount.
- A resize that fails before its request is sent, for example while
  getting a token, now returns no Output and the original error instead of
  reporting an accepted but unsettled write.
- Quote responses are bounded at 1 MiB.
- Bool flag help shows `[=true|false]` and invalid flag values give
  shorter errors, from pflag v1.1.0; usage errors still exit 2.

## v0.91.2 - Clearer IAM permission confirmations

- Policy attach and detach for service accounts, users, and groups, and
  adding or removing a user to or from a group, now say "grants
  permissions" or "removes permissions" when `--yes` is missing, instead
  of "is destructive". `--yes` stays required on the same commands, still
  with exit code 2 and no request sent.
- Help for `iam get-service-account` and `iam list-service-accounts` names
  `iam list-service-account-policies`; help for `iam get-user` and
  `iam list-users` names `iam list-user-policies` and
  `iam list-user-groups`.
- The global `--yes` help reads "confirm a destructive or
  permission-changing operation".

## v0.91.1 - Flavor prices with GPU catalogs

- `compute list-flavor-prices` (SDK `ListFlavorPrices`) works again.
  GreenNode now lists GPU flavors in the `gpu` family, which discovery
  rejected as an invalid response, so every call failed with
  `InvalidResponse` from `compute.ListFamilyFlavors`. GPU family scopes are
  now skipped with an `excluded-family` note; GPU flavors are neither listed
  nor priced, and malformed rows in other families still fail.

## v0.91.0 - vMonitor ensure-synthetic-quota

- SDK `EnsureSyntheticQuota` and CLI `monitor ensure-synthetic-quota` keep
  the free Synthetic Test quota alive from one command, for cron and CI.
  From one fresh read it renews when the quota is ACTIVE inside
  `--max-days-left` (CLI default 7), recovers when it is expired in trash,
  skips when it is not due, and refuses anything else. It never creates or
  deletes and never falls back from one action to another. It reuses the
  renewal and recovery guards (`--max-price` default 0, optional
  `--expected-quota-id`), sends once, and confirms by read-back.
- The output adds `Action` to the shared `Sent`, `Stage`, and `Error`
  outcome. An identity that may renew but not recover gets a clear
  `Forbidden` refusal with `Action` `recover` and exit 1.
- `quota-status` now suggests `monitor renew-synthetic-quota` or
  `monitor recover-synthetic-quota` when one applies. Tables show time left
  in days, hours, and minutes, and leave the error column blank on healthy
  rows; JSON fields are unchanged.
- When a call reconciles an earlier unresolved write on the same client,
  its error now names the operation that was called.
- The wiki moves Synthetic create, recovery, renewal, ensure, delete, and
  the Unattended CI guidance to a new Monitor Synthetic Quota page.

## v0.90.0 - vMonitor Synthetic quota create and delete

- SDK `CreateSyntheticQuota` and `QuoteCreateSyntheticQuota`, and CLI
  `monitor create-synthetic-quota` and `monitor quote-create-synthetic-quota`,
  order a free Synthetic Test quota when none exists. They require an
  explicit class, package, and quantity (API tests), refuse paid classes,
  price the order as unit price times quantity against `--max-price`
  (default 0), never bundle paid notification packages, send once, and
  confirm by read-back of an ACTIVE quota with the chosen package.
- SDK `DeleteSyntheticQuota` and CLI `monitor delete-synthetic-quota` now
  delete the quota instead of refusing. The command is destructive: it
  needs `--yes` and `--expected-quota-id`, re-reads the quota first, sends
  once, and settles when the quota read returns not found. Deleting the
  Synthetic Test quota permanently deletes every synthetic check; help and
  every output format say so. Purge stays unsupported.
- `list-synthetic-packages` adds each class's unit packages (from the
  class packages read), with unit price, units, and tier; this is the
  package ID that create, renewal, and recovery use.
- All three writes share the `Sent`, `Stage`, and `Error` outcome model and
  exit table from v0.89.0.
- Compatibility: `DeleteSyntheticQuotaInput.ExpectedQuotaID` is now
  required, and a call that refused before can now delete. New fields on
  `SyntheticQuotaClass` break unkeyed literals. The create response body is
  not verified live; success comes only from the read-back.

Older releases are in [docs/release-notes-archive-v0.89.md](docs/release-notes-archive-v0.89.md).
