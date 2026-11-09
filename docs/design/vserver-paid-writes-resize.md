# vServer Paid Writes: Resize

Status follows [vServer paid writes](vserver-paid-writes.md).

This document owns server resize and rename rules, signed-credit evidence,
and resize threats. [API](vserver-paid-writes-api.md) owns request shapes,
quote bounds, waits, and open questions;
[checks](vserver-paid-writes-checks.md) owns unit tests and live procedures.

## Resize and rename

- `ResizeServer` reads the server. The same flavor is `ErrInvalidInput`;
  a status other than `ACTIVE` or `STOPPED` is `ErrUnexpectedStatus`. Both
  send nothing. Then it applies the
  [price guard](vserver-paid-writes.md#quote-before-a-paid-write), sends
  `flavorId` and `serverId` with `Once`, and waits until the flavor read
  back is the new one.
- `RenameServer` sends `newName` and returns the server from the
  response. Rename is free and keeps the transport's `PUT` retries.
- The root disk grows through `volume.ResizeVolume` on the server's
  `BootVolumeID`.

## Resize credits

The signed quote is an expected adjustment, with the evidence limits in
[evidence](#evidence).

- `pricing.GetQuote` accepts a finite negative `optimumPrice` only when
  the effective `Action` is exactly `resize`. Empty means `create`;
  `create`, unknown actions, whitespace, and case variants still refuse
  negatives as `*APIError`. Do not add action validation or normalization.
- All resource types share that read rule, including arbitrary strings.
  `QuoteResizeServer`, `QuoteResizeVolume`, `QuoteResizeLoadBalancer`, and
  `pricing get-quote --action resize` return signed quotes. This neither
  proves support for a resource nor authorizes any write.
- Missing, null, malformed, NaN, and infinite prices remain `*APIError`,
  with nil Output. Zero remains a valid quote read, including negative zero.
- The accepted `ResizeServer` policy permits a finite credit at every
  valid `MaxPrice`, rejects either signed zero with
  `ErrUnpriced`, and compares positive prices normally. Keep this policy
  local to server resize; do not relax `core.CheckPriceAboveMax` for
  creates or volume growth. `ResizeVolume` refuses nonpositive quotes
  with `ErrUnpriced` naming the amount; its quote read remains signed.
- No field, type, import path, or dependency changes. The SDK stays
  standard-library only; CLI calls use public SDK operations. Signed
  `float64` fields preserve JSON shape and minus signs in all renderers.
  Callers that assumed nonnegative values must update their handling.
- The write requires the same-flavor and status guards,
  `Once`, and CLI `--yes`. Failure before send returns nil Output. After
  an ambiguous send or failed wait, retain the signed quote and last server
  in Output; preserve `ErrNotSettled`/`ErrFailed`, underlying `*APIError`
  via `errors.As`, and context errors via `errors.Is`. Read `GetServer`
  to reconcile; never retry or clean up automatically. `NoWait` returns
  the pre-write server with the quote, not confirmation of a credit.

## Evidence

Evidence behind the [credit rules](#resize-credits):

- Published text: the [billing resize page][resize-billing] describes a
  prepaid configuration-change payment flow and minute-level unused
  value calculations, with service-specific refund exclusions. Its price
  formula is an image hosted on `1985221522-files.gitbook.io`, outside
  the permitted source hosts. The exact arithmetic and worked examples
  are unverified here. The page does not document `v1/price` fields or
  guarantee the direct IAM API uses the displayed payment calculation.
- Observation: the manager-supplied CLI error supports a negative server
  resize `optimumPrice`. The [vLB evidence](lb-writes-api.md#price-quotes)
  separately records negative load balancer resize quotes. Neither proves
  a server refund completed.
- Inference: a prepaid adjustment can represent new value minus unused
  old value over remaining time. If both rates and resource state stay
  fixed, elapsed time can reduce a credit toward zero. This conditional
  model is not a guarantee across price, discount, renewal, or state changes.
- Live capture (free check 7, `hcm-3`): a downsize quote from
  `s2-general-2x4` to `s2-general-1x2` on a server with volume
  encryption returned HTTP 200 with `optimumPrice` -372,552,
  `originalPrice` 368,940, `discountPrice` 0, `discountPercent` 0,
  `originalCurrentPrice` 737,880, `originalNewPrice` 85,140, and
  `artifactPrices` null. Each `propertiesPrice` row carries
  `optimumPrice` and `monthlyPrice` (the new configuration) and
  `currentPrice` (the old): instance 283,800 against 567,600, and `CES`
  85,140 against 170,280. The sanitized body is
  `testdata/pricing/GetQuoteServerResizeCredit.json`.
- Missing evidence: the formula behind `optimumPrice`, which is not the
  plain difference of the new and old totals, and the meaning of
  `originalCurrentPrice`, `originalNewPrice`, and `discountPercent` on
  credits. Preserve unknown fields in the fixture; do not infer their
  sign or recompute `optimumPrice`.

No documented bound relates the credit to one month's list price. A longer
prepaid term could exceed such a bound, and the quote contains neither
unused paid value nor an authoritative period end. Finite-only acceptance
is a representation policy, not verification of billing accuracy. Do not
round, take absolute values, add credits to budgets, or use credit size to
permit another operation. Only the sign and positive-price comparison
control the guard.

The [resize API][vserver-api] documents target IDs but no quote ID, price
cap, expected old flavor, or conditional version. If A quotes a downsize
from an expensive flavor to a medium one and B moves the server to a
cheaper flavor before A sends, A's target can become an upgrade. `Once`
prevents a resend within A's call; it neither serializes callers nor makes
the quote atomic. A price or discount change can also reverse the sign.
The actual charge can exceed `MaxPrice`, including 0. Another pre-read
only narrows the gap. [Decision 20](vserver-paid-writes.md#owner-decisions)
explicitly accepts this residual risk.

Volume growth retains its nonpositive-price refusal as SDK policy; the
published text does not prove a grow can never quote a credit. Generic
resize quotes remain reads for all resource types. Creates cannot select
resize semantics through a caller-controlled action.

[vserver-api]: https://docs.api.greennode.ai/service-docs/vserver.html
[resize-billing]: https://docs.greennode.ai/billing-management/experience-with-billing-and-payment/resource-lifecycle-management/resize-resource-configuration

## Resize credit threats

These are design obligations, not an independent review verdict. The
existing signed-reader and credit-write protections must remain intact.

- Leaked tokens, high impact: an echoed credential can escape through an
  HTTP/decode error, debug output, capture, or quote property text. Existing
  `internal/transport/transport.go:579` redacts captured bodies and skips
  sensitive captures; `:598` logs method/path/status/time only;
  `internal/transport/body.go:32` sanitizes decode errors. The numeric
  refusal at `pricing/pricing.go:187` contains only the price. Invariant:
  add no credential-bearing output or raw-response error. Check echoed
  synthetic tokens in success metadata, errors, debug, and captures;
  retain nested-escape limits from security instructions. Typed success
  metadata is not proved redacted by error/capture tests. Any leak found
  needs a scoped fix before release. Sanitize account data separately.
- Hostile peer, high impact: an unbounded quote can exhaust memory before
  decoding. `pricing/pricing.go:144` sets `MaxBody` to 1 MiB, enforced by
  `internal/transport/transport.go:508` before capture. Invariant: preserve
  the bound and return no partial quote. Keep the
  [body limit](vserver-paid-writes-api.md#quote-decoding-and-bounds).
  Check cap and cap+1, HTTP error identity, no partial capture or write,
  malformed types, finite extremes, and cancellation. A forged finite
  credit can still pass
  the guard: TLS authenticates the peer, not billing accuracy.
- Redirect or endpoint tampering, high impact: credentials attach at
  `internal/transport/transport.go:453`; `redirect.go:11` checks host and
  scheme before and after caller hooks; `transport.go:416` refuses all
  resize redirects under `Once`. Defaults use HTTPS at
  `internal/endpoints/endpoints.go:98`. Caller HTTP clients/transports
  and endpoint overrides remain trusted configuration
  (`internal/core/client.go:384`, `internal/core/client_options.go:134`),
  not a host allowlist. Invariant: no new endpoint or TLS bypass; no
  redirect may forward credentials cross-origin. Check foreign host, downgrade,
  hook mutation, and 307/308 without a second send. Do not treat a
  malicious initial endpoint or caller transport as protected by redirects.
- Local tampering, high impact: CLI configuration selects the account and
  consent policy before SDK calls (`internal/cli/op.go:419`); credential
  loading checks private file modes (`internal/core/load.go:174`). Cache keys
  bind profile, user, and auth endpoints (`internal/tokencache/cache.go:30`);
  `:169` rejects stale/empty tokens, `:199` writes mode 0600, and `:242`
  requires a mode 0700 directory on Unix. Invariant: reuse only the
  selected identity's token and preserve credential-file permissions.
  A same-user attacker can edit a profile or token; file modes are not
  cryptographic integrity. Check malformed/wrong-profile/stale caches,
  insecure directories, and credential writes at 0600. The credit path
  adds no file access; quote probes must not copy profiles into fixtures.
- Concurrent or repeated writes, high impact: the concrete sign-reversal
  race is in [evidence](#evidence).
  `compute/resize_server.go:128` reads before quoting; `:170` sets `Once`;
  neither locks a resource against another actor. Invariant: at most one
  resize per call, explicit CLI consent, no automatic retry or cleanup
  after uncertainty. Decision 20 explicitly accepts the residual
  quote-to-send risk.
  Check a deterministic peer changing the old flavor after the quote,
  unchanged target body, one send, and read-only recovery. This test
  exposes the residual risk; it must not assert a guaranteed zero charge.
  Check partial signed Output and context/error identity after send;
  wait remains 5-second polls within 15 minutes, honoring cancellation.
  Live cleanup still requires the run's existing explicit authority.
