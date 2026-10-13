# Private endpoint checks

Endpoint implementation remains blocked on the
[API evidence](network-endpoints-api.md) and approval of the
[proposed contract](network-endpoints.md). These checks define acceptance
criteria; they do not claim endpoint lifecycle tests or live writes have
run. The [recorded catalog reads](network-endpoints-api.md) establish only
their named read-only subset.

## Contract and fixture checks

- Match every new request path, body, header, and success/error envelope to
  cited evidence. Verify catalog IDs, region versus availability-zone
  meaning, service/package eligibility, and create/delete service-ID mapping.
- Decode manager-sanitized raw populated fixtures through the SDK. Preserve
  fixture provenance. Reject malformed, missing, null, duplicate-key, and
  oversized critical response fields with safe errors. Label synthetic
  variants and do not promote them to API evidence.
- Preserve existing endpoint read inputs, outputs, paging, and CLI behavior.
  New writes use dedicated typed allowlists. Unknown or untyped inventory
  data must not reach mutation, price, or confirmation decisions.
- For the recorded catalog subset, keep nonpaged catalog envelopes separate
  from paged VPC responses. Accept a valid positive response size at most
  the recorded request bound of 100 without requiring equality. Verify
  totals, decoded rows, and forward progress; one consistent page proves no
  general completeness or service eligibility. Null-only and unmodelled price
  fields remain evidence gates, not permissive public model fields.
- An HTTP 200 or `success=true` without the required catalog data is not a
  usable catalog or an empty inventory. Keep the recorded subnet envelope
  unresolved until evidence establishes its meaning; do not guess a code
  or expose its withheld field to explain the failure.

## Local security and mutation tests

Use `httptest`, injected clocks, and deterministic fixtures. Never use a
real API in unit tests.

- Validate nil input, missing and invalid IDs, omitted DNS choice, bad cap,
  unsupported auth/region, invalid output/query settings, read-only policy,
  and missing `--yes` before a mutation. Pure local failures send no request.
- Prove VPC/subnet scope and service/package eligibility before quote or
  purchase. Refuse mismatched, duplicate, incomplete, and changing inventory;
  cover every page, repeated IDs, inconsistent totals, and stalled paging.
- Compare quote/create field parity. Refuse malformed, non-finite, zero,
  negative, wrong-unit, or wrong-term prices. Zero cap refuses purchase;
  equality passes and an over-cap quote sends no order.
- Prove at most one mutation attempt after every response and transport
  class, including 401, 429, 5xx, failed dial, lost body, and redirects.
  Do not resend or invoke checkout after confirmation failure.
- Confirm identity, readiness, payment, and observed renewal off independently.
  Missing renewal is unknown. Contradictory data fails closed. Test partial
  outputs, unknown statuses, terminal failures, deadlines, and cancellation.
- Verify delete's exact target, explicit VPC, and body identity. Confirm
  removal and the evidence-defined future-charge condition. Refuse cascade
  behavior. A list endpoint's 404 or one empty page does not prove absence.
  Refuse VKS-managed, reused, or unknown-ownership targets; misleading name
  prefixes and `--yes` cannot bypass that guard. Obtain ownership and
  consumer evidence without cluster access.
- Inject credential-like values, payment links, and hostile nested fields
  into responses. Check JSON, text, tables, queries, debug logs, errors,
  captures, and error causes. No raw server body or payment URL is exposed.
- Prove credentials stay on the approved origin and TLS verification stays
  enabled. Reads never call response-directed URLs. Write recovery does not
  adopt or delete resources solely because names resemble the request.

## Release and operator evidence

The implementation plan must own network code and tests, CLI registration
and tests, fixtures, example coverage, and matching wiki/reference updates.
The primary agent runs `make check`, the live-tag compile/vet commands in
[testing](../../instructions/testing.md#live-verification), and wiki preview.
Require independent adversarial review of writes, billing, and response
handling. Add no SDK dependency.

The manager reports separately authorized live results by region and auth
kind, including catalog/read compatibility, one purchase, identity,
payment, DNS choice, renewal, deletion, future charges, and residual cost.
One successful region does not enable another. A source inspection or a
successful compile is not live evidence.

The application operator provides any private-network read/write evidence
from vServer or VKS. No test connects to a guest, changes a host file,
uses the Kubernetes API, or injects credentials. Private-path evidence must
name its DNS and TLS assumptions without disclosing account data publicly.
