# Setup estimate: evidence and checks

This file records the evidence delta for [setup estimates](cost-estimate.md).
It adds no live observation. Existing sources below establish bounded observed
behavior, not universal account, region, image, discount, or purchase support.
All estimate paths are proposed and unimplemented.

## Request and response contracts

[Paid quote bodies](vserver-paid-writes-api.md#quote-requests) own server and
volume construction. The wrapper is exactly `resourceType`, `action:create`,
and `resourceInfo`. Regional quotes use `POST v1/price` through
`routes.ProductPortal`, honoring configured Portal overrides. Success is HTTP
200 with top-level price fields. The following are the complete info-key sets:

- Server: `zoneId`, `flavorId`, `imageId`, `rootDiskSize`, `rootDiskTypeId`,
  `period:1`, `isPoc:false`, `encryptionVolume` as a boolean. Optional
  `dataDiskSize` and `dataDiskTypeId` appear together. Encryption IDs determine
  the boolean but never appear in the quote. No name, login, network, IP,
  order, renewal, or encryption-key material is sent.
- Volume: `zoneId`, `volumeTypeId`, `size`, `period:1`, `isPoc:false`, and
  `encryptionType` only when supplied.
- Public VIP: `type`, `isBuyMorePo:false`, `isPoc:false`, with no period.
  The type is exactly `public`, `public-vm`, or `public-mkp`, under the
  [accepted IP contract](pricing-discovery-api.md#volume-and-ip-prices).

[Storage price](storage-projects-api.md#price) owns the other route:
`POST billing-api/v2/price?region_id=<resolved-id>` on ProductStorage, with
`region` and `region_id` headers both equal to that ID. Its complete wrapper is
`resourceType:object_storage`, `action:create`, and `resourceInfo` containing
only numeric `projectType`, `purchaseTypeId`, and `quota`. Resolve IDs from the
active monthly catalog; never embed example IDs. Success requires HTTP 200,
a successful storage envelope, and price fields inside `data`.

All required `optimumPrice` fields must be present positive finite numbers.
Optional original/discount prices and percentages retain presence, rejecting
negative or non-finite values when supplied. No currency field is required:
VND comes from the accepted contract. If currency appears, require VND.
Reject duplicate JSON keys, invalid types, and malformed envelopes before
caching. A breakdown uses present nonnegative finite `monthlyPrice` values;
missing/null stays unknown. A malformed present amount fails its quote. Never
infer a monthly amount from a property name or `currentPrice`. Top-level
`optimumPrice` is authoritative; breakdowns are not a second total. If known
breakdown amounts do not reconcile, add a static `breakdown-mismatch` note,
without adjusting prices or interpreting the gap as a discount.

The existing discovery allowlist in `internal/discovery/session.go:225`
allows only an unencrypted server without a bundled data disk. Extend the
server entry for estimate use to allow boolean `encryptionVolume` and the
paired optional `dataDiskSize`/`dataDiskTypeId`. Require positive integer size
and valid type ID. Keep `period:1`, `isPoc:false`, every required priced key,
fixed resource types, and exact price routes. Keep discovery's existing
reference-server builder plain. No generic body escape hatch, new mutation
route, action, or type is authorized. This extension needs adversarial tests.

Documented quote refusals keep their existing identities. A nonexistent volume
type has observed HTTP 500, and invalid period has observed 400; neither means
not-found. Synthetic status checks cover 400, terminal 401, 403, 404, 429, 500,
502, 503, and 504 without claiming every status has been observed on every
route. Storage can refuse inside HTTP 200; retain its envelope classification.
Only configured transport retries apply. Test exactly one send for HTTP 500
and bounded attempts for retryable statuses and 401 refresh. Any guard failure
is static `InvalidResponse` with the actual status, before retry or refresh.

## Arithmetic failures

Local non-finite multiplication or subtotal addition returns
`*pricing.EstimateArithmeticError` with `Stage` and `ItemKey` string fields.
Stage is `multiplication` or `subtotal`; ItemKey is the component key for
multiplication and empty for subtotal. `Unwrap` returns the new sentinel
`pricing.ErrEstimateOverflow`, so `errors.Is` finds that identity and
`errors.As` finds the typed error, also through `DiscoveryError`. These errors
are neither invalid input nor API errors. Messages are static, without amounts.

Multiply after validating each unit quote. On overflow, fail that row, set
`Price.Status=failed`, clear all numeric Price fields and `MonthlyAmount`, and
clear Breakdown to `[]`. Without partial mode, return nil output and that
error immediately. With partial mode, continue independent rows and use the
normal surviving-row rule. Sum only valid extended amounts. Returned output
has `Metadata.Complete=false`, `RequestedComplete=false`, `CostComplete=false`,
and nil `RequestedMonthlyTotal`; its known subtotal remains valid if possible.

Compute the subtotal once after row processing, in output order. Subtotal
overflow is an aggregate failure: never drop the row that crossed the bound,
return a prefix sum, clamp, or round to hide it. Fail-fast returns nil output
and the arithmetic error. Partial mode retains every row and its valid amounts,
sets both total amounts nil and all three completeness flags false, and returns
output with `DiscoveryError` containing the arithmetic error and any earlier
causes. Valid rows remain quoted; this explicitly overrides the usual rule
that surviving quoted rows produce a known subtotal. No extra requests follow.

For either failure, append coverage with `Status=failed`, reason
`arithmetic-overflow`, and scope equal to the component key or `setup`. Append
`DiscoveryFailure` with `Operation=pricing.Estimate`, the typed error's ItemKey,
`Status=0`, and Code `EstimateMultiplicationOverflow` or
`EstimateSubtotalOverflow`. Partial failures exit 1, including queried output.
Never serialize a non-finite number in any format.

## Live facts required by the design

These are the complete external-behavior dependencies. Sources were read from
the repository; no website or live API was contacted for this design.

1. Price POSTs are read-only, not orders; regional full create quotes are
   monthly VND with VAT as returned. Established by
   [paid billing evidence](vserver-paid-writes-api.md#billing-model) and
   [quote bodies](vserver-paid-writes-api.md#quote-requests). Quotes reserve no
   price, capacity, or provisioning eligibility. Addition is local arithmetic.
2. Full server quotes include root disk; their compute and root properties are
   separate. Established by
   [server observations](pricing-discovery-api.md#server-quote-results) and
   [raw fixture provenance](../../testdata/pricing/discovery-provenance.md).
   Unlike compute discovery, estimates sum the full top-level amount.
3. Optional bundled data disks change the full server quote, and encryption
   can add CES. The boolean is derived from either encryption field; the quote
   omits type keys. Established for recorded configurations by
   [paid quote evidence](vserver-paid-writes-api.md#quote-requests) and
   [encrypted live results](encrypted-volumes.md#live-results). No universal
   CES percentage or type-independent tariff is assumed. Raw estimate decoder
   fixtures for bundled/encrypted server responses remain missing.
4. Standalone volume quotes accept an optional encryption key. Tested encrypted
   prices equal plain, but do not include server CES. Established by
   [volume observations][volume-evidence] and encrypted live results. Existing
   sanitized plain/encrypted discovery fixtures cover the decoder shape.
5. Explicit zone/type/image catalog selection uses region-wide volume parents,
   enabled zones, returned encryption keys, image licence presence, and image
   minimum disk size. Existing [catalog evidence][catalog-evidence] and
   [image evidence][image-evidence] establish those fields in the tested scope.
   Image zone metadata does not prove compatibility. The new helper does not
   depend on image-independent pricing, RAM units, or a flavor-name parser.
6. Monthly storage packages use the price-only body, resolved storage regions,
   returned active type/purchase IDs, allowed periods, and configured quota
   bounds. Established by [storage evidence](storage-projects-api.md#price),
   [catalog evidence](storage-projects-api.md#project-types), and existing
   storage raw fixtures. A positive quote alone does not enforce quota minima.
   Order-only keys can yield zero; they must never enter estimate quotes.
7. Public VIP types have separate quote results, even when prices match.
   Established by [network results][network-evidence]. The `public` fixture
   records a shorter body than the implemented discovery body; accepted console
   construction supplies the two false flags. Exact flagged-`public` live
   equivalence is not established by that fixture. Reuse does not upgrade it.
8. Floating IP, External Interface, reservation, bandwidth, traffic, and object
   egress lack a complete matching account quote contract. Established as gaps
   by [published conflicts][tariff-evidence] and network results. No IP product
   equivalence, free count, usage allowance, or egress amount is assumed.
9. Tested periods showed no term discount; this is not universal policy or
   proof that period input was honored. The exact
   [probe coverage](pricing-discovery-api.md#manager-read-only-probes) is the
   authority. Monthly-only estimates do not depend on longer-term behavior.
10. Summing independently quoted resources and multiplying quantities is a
    planning assumption, not an observed combined-order price. No live fact
    establishes bulk-discount absence, shared allowances, synchronized prices,
    or equal prices across accounts/regions. Output states that limit.

[volume-evidence]:
  pricing-discovery-api.md#volume-catalog-reconciliation-and-prices
[catalog-evidence]: pricing-discovery-api.md#catalog-results
[image-evidence]: pricing-discovery-api.md#image-compatibility-evidence
[network-evidence]: pricing-discovery-api.md#storage-and-network-results
[tariff-evidence]: pricing-discovery-api.md#published-tariffs-and-conflicts

## Evidence gates and read-only probes

Existing [fixture provenance](../../testdata/pricing/discovery-provenance.md)
records sanitized raw bodies and requests. Reuse those fixtures through the
new SDK helper, not through decoded-output snapshots. Existing server body and
encryption tests prove serialization, not raw response decoding for CES or
bundled disks. No matching raw fixture containing those lines was found.

Before releasing those branches, the manager must supply sanitized raw server
quotes covering plain bundled data, encrypted root, and encrypted bundled data.
Prefer already captured raw bodies with provenance. If unavailable, propose at
most four `server/create` price POSTs: a fixed plain baseline; that baseline
plus a data disk; encrypted root; encrypted root plus encrypted data disk.
Use existing valid returned IDs, period 1, fixed accepted bodies, and no order.
Record full top-level prices and returned property names/types, without
assuming the data-disk property's spelling. No paid resource is required.

Also propose one `public-vip/create` quote for `type:public` with both false
flags to close the exact-body provenance gap. Until that evidence exists, keep
that branch's live-equivalence claim gated; `public-vm` and `public-mkp` already
have matching raw-body provenance. These proposals grant no live-run approval.
If a branch lacks its required fixture, hold that branch out of the first
release and resolve the advertised scope with the owner before implementation.

No probe here attempts bandwidth, FIP, egress, eligibility, NAT, or an unknown
route. No price probe can establish a universal discount policy. Broader region
or authentication coverage needs separate owner-authorized read-only checks.
Keep unknown products unknown instead of buying resources to investigate them.

## Security obligations

These obligations guide independent review; they are not a review verdict.
Shared [response security](pricing-discovery-api.md#response-security) and
[shared waits](pricing-discovery.md#shared-waits-and-cancellation) remain the
mechanism contracts. The extraction and aggregate output are new attack paths.

- **Log reader, high:** a quote or catalog echoes a token into a property, then
  aggregate JSON, query output, debug logs, or errors leak it. Existing
  `internal/transport/discovery.go:44` sets Sensitive, body bounds, no cookies,
  and no redirects; `:78` guards responses before interpretation with all sent
  credentials. Keep every estimate read/quote on that path, including catalog
  resolution and retries. Use trusted input keys in failures, omit descriptions,
  and emit static parse errors. Canary tests cover escaped keys/values, current
  and rotated tokens, success/error bodies, caches, capture hooks, every CLI
  format, queries, and stderr. Retain the accepted nested-string redaction
  limit; do not claim protection from an intentionally malicious caller hook.
- **Hostile API peer, high:** huge catalogs exhaust memory or absent prices
  become a falsely complete cheap setup. `internal/discovery/session.go:136`
  bounds rows; `:167` validates before caching; the guard bounds each body to
  8 MiB. Share those limits across services, count breakdowns, reject paging,
  preserve numeric presence, and check overflow. Test malformed and duplicate
  fields, envelopes, incompatible currencies, unknowns, budget exhaustion, and
  partial rows. A valid total never depends on summing unvalidated line items.
- **Redirect or endpoint attacker, high:** a price POST reaches an order path
  or sends credentials to another host. `internal/transport/discovery.go:54`
  sets NoRedirect; `internal/transport/transport.go:411` refuses redirects.
  Use trusted Config endpoints only, retaining TLS verification and existing
  caller-client/transport policy. The JSON spec cannot override endpoints;
  catalog URLs never direct requests. Test foreign-host and same-host mutation
  redirects, configured base paths, no fallback to production, TLS policy, and
  unchanged caller clients. Trusted custom transports remain caller authority,
  not a sandbox the estimator can police.
- **Local profile/cache attacker, high:** a tampered profile selects another
  account or reuses an unrelated token, producing a misleading account quote.
  `internal/core/load.go:53` resolves and validates profiles;
  `internal/cli/config.go:95` checks cache-directory permissions;
  `internal/tokencache/cache.go:41` binds IAM cache keys to identity/endpoints,
  and `:229` protects token files. Reuse existing auth-specific cache rules,
  0600 credential/cache files, and 0700 cache directories. Add no credential
  source or persisted estimate. Test profile isolation, unsafe permissions,
  identity separation, and spec rejection of auth/endpoint fields. The helper
  cannot defeat an attacker who already controls the trusted Config. A local
  actor can also replace the spec with a FIFO: `internal/cli/input.go:117`
  uses blocking open. Require `openRegularFileNonBlocking` from
  `internal/cli/openfile_unix.go:25` and the descriptor type check used at
  `internal/cli/inputfile.go:32`, before reading the same bounded bytes.
  Neither the byte cap nor the SDK deadline prevents a blocking file open.
- **Concurrent/repeated caller, high:** an estimate is reused to authorize a
  purchase, or an overly broad allowlist admits an order. The quote is marked
  idempotent at `pricing/pricing.go:131`; the allowlist lives at
  `internal/discovery/session.go:225`. Extend only the accepted server shape,
  test all order/resize/renewal variants are refused, and keep quantities local.
  Concurrent invocations share auth coordination but never mutable sessions or
  price caches. Test bounded retry, duplicate-body accounting, cancellation
  after shared waits, and preserved token identity. The helper cannot call a
  write or cleanup path and grants no cleanup authority. A later paid command
  must still take its own fresh quote and apply its existing price guard.

## Required checks

Require sanitized raw fixtures decoded through `Estimate`, with request/body
assertions for all supported products and synthetic hostile variants labelled
as such. Compare the extracted builders against existing server/volume quote
and paid-guard drift tests and the storage price-only serializer. Test a plain
server's full total against its fixture without adding root twice; test bundled
and additional volumes, CES, quantities, zero/missing prices, optional
discounts, all-unknown results, and currency conflicts. Deterministic overflow
cases use a finite unit quote of `1e308` with quantity 2, and two distinct rows
of `1e308` with quantity 1. Test both partial modes. Add an ordinary valid row
to multiplication cases to prove continuation and a subtotal excluding the
failed row; also test no surviving rows. For subtotal overflow, retain both
quoted rows in partial output but require both totals nil. Assert typed/sentinel
identities, failure scope/code/status, coverage, every completeness flag, no
arithmetic retries, exit 1, and JSON encoding without non-finite values.

Use deterministic HTTP servers and injected clocks for the shared request
budget, all preflight refusals, 500 ms retry pacing, terminal failures, context
cancellation, locks, per-call caching, and no calls for unknown IPs. Keep every
input row in partial output, including dependent rows and unscheduled work.
Assert `errors.Is`/`errors.As`, both partial modes, exact statuses, and static
safe diagnostics. Prove no request occurs for malformed specs or invalid flags.

CLI goldens cover JSON/table/text, quantities, grouped breakdowns, mandatory
coverage, nil totals, query-hidden metadata, exits, duplicate/case-mismatched
JSON keys, nested unknown fields, size/depth caps, and control characters.
Require a Unix FIFO-refusal test with no writer: invoke estimate using
`--cli-input-json file://<fifo>`, require exit 2, no SDK/auth calls, and no
stdout output. A bounded watchdog must fail a blocking-open regression; do not
open a writer to make the call pass. Also test regular files, directories, size
boundaries, and descriptor-based validation without reopening the path.
The manager runs `make check`, generated-doc freshness and SDK dependency
boundary checks before landing. No dependency is proposed, so no dependency
approval or new dependency scan is required by this design. Existing release
security and vulnerability checks still apply.

Not checked here: runtime tests, live behavior, manager-private raw captures,
independent review, or implementation correctness of the proposed paths.
