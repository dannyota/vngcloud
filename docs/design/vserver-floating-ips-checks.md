# vServer Floating IPs: Checks

Status: Owner decisions accepted; evidence and independent review gates open.
See [vServer floating IPs](vserver-floating-ips.md).

These are implementation and review obligations; the
[API](vserver-floating-ips-api.md) owns evidence. Unit checks use `httptest`,
synthetic IDs, documentation addresses, and injected clocks, not the cloud.

## Fixture provenance

Retain the existing server fixture for the empty-address case. Build
positive R1 cases from the published `InterfaceNetworkInterface` schema
and label them schema-built, not captured. Label malformed and race cases
synthetic. The WAN fixture's existing fields are not proof of a live row.

For each authorized capture, the manager records the operation, method,
route template, authentication kind, status, content type, source class,
and sanitation substitutions. Preserve the raw JSON envelope and types;
replace IDs with synthetic IDs and addresses with `192.0.2.0/24` values.
Do not copy decoded SDK output as raw evidence or place capture dates,
account data, credentials, or private notes in living public documents.
Decode sanitized raw fixtures through the public SDK. Compare captured
raw and SDK data privately before publishing a fixture. Empty lists prove
only the envelope, never row shape or attachment state.

## Unit tests

### R1

- Decode an internal address, empty interfaces, duplicate equal values,
  distinct addresses, invalid values, mapped addresses, scoped IPv6,
  private/loopback/link-local/multicast values, and control characters.
  External-only addresses and a hostile top-level `publicIp` never fill
  `PublicIP`. Reusing a `Server` for another decode clears stale data.
- Exercise get, both server list paths, and every write decode/wait path.
  Preserve all existing interface fields and ordinary JSON tags. CLI
  JSON gains one key; text query prints the canonical address. An empty
  field makes no claim about attachment or allocation.
- Oversized, truncated, and hostile server bodies fail without returning
  a partial decoded object or leaking response text. Test both the new
  derivation and the reused bounded read paths.

### R2

- Default create sends `attachFloating:false`; opt-in sends true. The
  override is never a wire field. Reject override without public create,
  malformed IDs, nil Input, invalid shape, and invalid create `MaxPrice`
  before requests. Quote requires only priced fields and still ignores
  `MaxPrice`/`NoWait`, including invalid guard-only price values.
- Default quote keeps its type, values, and one request. Opt-in sends
  the observed server and zone-bearing IP bodies in order; retain numeric
  values and labeled provisional IP lines/fallback. Check absent/null optimum,
  zero/negative prices, missing/inconsistent totals, overflow, and IP errors.
  Test each quote at 1 MiB and 1 MiB + 1 byte; oversized bodies are not
  captured. Failures return no partial sum and send no order.
- Guard example: 347800 + 120000 = 467800. A cap of 347800 orders nothing;
  467800 allows one order. `MonthlyPrice` reports 467800. The message
  names estimate and cap, never a confirmed charge. Test discount arithmetic
  and price drift after quote; the guard cannot lock the backend price.
- Included follow-up: one quote, zero IP annotation, total/`MonthlyPrice`
  347800, cap 347799 refuses and 347800 sends once. Unverified terms keep
  the sum. Separate outcome keeps 467800 and evidence-backed labels.
  CLI goldens match; neither case reads transactions or switches at runtime.
- Drift: shared priced keys match their create values, including zone.
  User data and its base64 form stay out of both quotes, logs, errors,
  captures, and CLI output. Malicious quote property text reflecting a
  sent credential is rejected or withheld, not appended to output.
- Exposure tests run through public `compute.CreateServer` directly as
  well as the CLI. The CLI must not duplicate or replace SDK guards.
- `ACTIVE` without a complete valid address/ID keeps polling; success,
  `ERROR`, 404 during create, timeout, a failed confirmation, and caller
  cancellation preserve the documented error identity and safe Output.
  A 15 min create bound and 2 min address bound include request time.
  `NoWait` gives UUID/name and total, never invented address completion.
- 202 with/without `data.uuid`, documented 401/500, synthetic 400/403/404/
  409/429/502, unexpected 2xx, redirect, and connection loss. Count one
  order under every failure. No auth refresh or HTTP replay resends it.

### Exposure guard

- Read every named group, deduplicate IDs, and refuse zero groups, bad
  IDs, missing/null data, unreadable groups, more than 64 groups or 4096
  rules, oversized bodies, and 30 s exhaustion. Do not use
  `ListAllSecurityGroupRules`, which does not establish full membership.
- Reject TCP ingress covering 22, 234, 3389, or 3490 from either `/0`.
  Test alternate CIDR spelling, two `/1` rules in separate groups,
  overlapping ranges, IPv4 and IPv6 separately, unknown direction or
  protocol, `any` and `TCP`/`tcp` casing, `any` with a partial range, null
  ports or prefixes, invalid ranges, malformed CIDRs, and a CIDR rule with
  `remoteGroupId`. Wire ports are integers.
- Narrow prefixes, known egress, and TCP ranges excluding the listed ports
  pass; this infers no guest hardening. `AllowOpenLogin` bypasses exposure
  refusal only; permission, identity, response-limit, attachment, and
  price failures still stop the write.
- Recheck before send: changed membership, interface, or rules refuse.
  Interleave a rule change after the final read to show the unavoidable
  race; never assert zero exposure under concurrent changes. No group or
  guest mutation is an SDK side effect.

### R3

- `GetFloatingIP`: match on page 2; verified empty encoding; complete
  absence; wrong page/size, missing metadata, changing totals, repeated
  UUIDs, count mismatch, 51 pages, timeout, and cancellation. Failed
  completeness is `InvalidResponse`, never false `NotFound` or deletion.
  Existing list defaults and legacy public fields remain unchanged.
- Attach no-op requires the requested identity on both server and WAN
  row. Different IP, partial fields, moving status, missing/multiple
  internal interfaces, External Interface, absent zone, zone mismatch,
  and attached/unknown WAN state refuse before PUT. Validate API-derived
  IDs before reuse. Empty ID and reserved `auto` cannot allocate in R3.
- Existing-IP attach sends the exact path and interface body, no quote
  and no allocation. Price is nil, not zero ongoing cost. Confirm the
  requested ID, interface, and WAN relation, not any non-empty address.
- Detach uses the owning interface even when it is not the first row.
  Reject ambiguous fields. No-op, keep, delete, failed nested delete,
  replacement IP, and cancellation retain the captured ID and correct
  `Detached`, `Deleted`, `Changed`, and `BillingContinues` values.
  `Delete && NoWait` fails before any request; detached-ID recovery uses
  `delete-floating-ip`, not a blind repeat of detach.
- Delete refuses attached or unknown state. 204 then complete absence
  succeeds; pre-read absence is `NotFound`. Incomplete list cannot prove
  deletion. `NoWait` keeps `Deleted` false and billing unknown. Confirm
  uncertain deletion by reads only; no replay or implicit cleanup.
- Every mutation: expected 204, documented 401/500, synthetic 400/403/
  404/409/429/502, 200/202, 307/308, partial body, and connection loss.
  Check one send, fixed body, safe partial Output, `errors.Is` for every
  sentinel/context cause, and `errors.As` for the original API error.
- Wait spacing, whole-phase deadlines, canceled requests, bounded page
  walks inside waits, `NoWait`, and failed confirmation reads. No timer
  permits an extra send or unbounded transport retry after the deadline.
- Interleave two independent callers' pre-reads. Each can send once;
  neither a process-local lock nor `Once` proves one global mutation.
  Test a peer conflict and changing attachment before delete. Do not
  claim that a fake peer proves server atomicity or orphan-free billing.
- CLI JSON/text/table and query goldens; missing `--yes` on detach/delete;
  read-only refusal before requests; exit codes; safe partial output;
  warnings on retained/unknown IP charges even with an output query.
  No `quote-attach-floating-ip` or empty-ID allocation ships in R3.

### Mutation outcomes and CLI precedence

- For each of attach, detach, and standalone delete, cross accepted 204
  and uncertain 500 with both `NoWait` values. No-wait sends once, reads
  no confirmation, leaves settlement false/billing nil, and returns nil
  only for 204; 500 wraps `ErrNotSettled` and the original API error.
- With waits enabled, test both send outcomes followed by the exact
  confirmed target. Both fill settled Output; only 204 returns nil.
  A DELETE that acts but returns 500 followed by complete-list absence
  sets `Deleted=true`, billing false, and still wraps `ErrNotSettled`.
  Attach sets `Changed=true`; detach sets `Detached=true`, `Changed=true`,
  and billing true for the retained row. All send exactly once.
- Cross both sends and all three operations with an unchanged target
  until the bound, malformed/incomplete confirmation, a read error,
  cancellation, and deadline expiry. Return safe partial Output plus
  `ErrNotSettled`; preserve send and confirmation causes and their order.
  Where Output declares it, acceptance sets `Changed=true`; uncertainty
  without target proof does not. No read failure implies detached,
  deleted, or stopped billing.
- For both no-wait values, direct 401/404 and proven-unsent failures keep
  the original error without `ErrNotSettled`, confirmation, or resend.
  R2 uncertain create retains its existing error and performs no adoption.
- Nested delete: confirmed 204 detach permits one DELETE; uncertain
  detach never does; proven absence still sets `Deleted=true`, billing
  false, with its error retained. Cross nested 204/500
  with confirmed absence, timeout, incomplete list, and failed/canceled
  read. Retain `Detached=true` and the IP ID. Confirmed absence sets
  `Deleted=true`, billing false; 500 still returns `ErrNotSettled`.
  Otherwise `Deleted=false`; preserve unknown/retained billing evidence.
  A direct nested rejection preserves its error and the retained IP.
  Nested pre-read absence completes cleanup without DELETE; standalone
  pre-read absence remains `NotFound`. Reject `Delete && NoWait` before I/O.
- CLI: direct 401, missing credentials, credential-file and login errors
  exit 3; direct not-found exits 4; invalid usage/read-only exits 2;
  403 and ordinary failures exit 1. Context cancellation wins over auth.
  `NotSettled` wrapping 401, 404, auth, or context errors stays code
  `NotSettled`, exit 1, with partial Output. Reuse `ResourceInUse` for
  `network.ErrInUse`, including wrapped API errors; add only `OpenLogin`,
  exit 1. Preserve existing API codes and query-failure behavior.
- For nested 500 plus confirmed absence, assert stdout shows completed
  cleanup, stderr retains `NotSettled`, and no warning claims continued
  billing or recommends another delete. Retained/unknown deletion still
  warns even when `--query` hides billing fields.

## Free checks

Only the manager runs these under a separate read-only brief following
[live data](../../instructions/live-data.md). No credit balance is assumed.

1. The [quote-body probe](vserver-floating-ips-api.md#elastic-ip-quote) is
   complete; raw fixture decode and create-charge attribution remain open.
2. After that contract is implemented, `QuoteCreateServer` with public
   IP opt-in must return the two-request sum. A capturing transport
   allows only specified reads and price POSTs. `CreateServer` with
   `MaxPrice` equal to the server price must stop at `ErrPriceAboveMax`;
   a transport allowlist rejects the order even if the guard is broken.
3. `ListWANIPs` records the IAM route, status, empty-list encoding, and
   paging envelope. No row claim follows from an empty list.
4. Once R3 exists, bad-ID get must fail before requests and a synthetic
   well-formed absent ID must return `NotFound` only after a complete walk.
5. Group-rule reads verified the unpaged `data` array and the guard's
   [encodings](vserver-floating-ips-api.md#security-group-rules). Never
   modify the default group or create publicly to show its exposure.

## Live run

L4 scope/cap is not run authorization. Each approval names account,
`hcm-3`, enabled zone, mode, resources, and cleanup under the accepted cap.
Require quote evidence and cleanup before purchase. Set `VNGCLOUD_LIVE_WRITE=1`,
`VNGCLOUD_LIVE_FLOATING_IP=1`, and `VNGCLOUD_LIVE_MAX_VND=467800` in a
manager-owned test with a 60 min timeout. Each run needs new approval;
never reuse consent or raise the cap.

Buy one `s2-general-1x2` server with a 20 GB SSD root selected by its
verified zone-specific `RootDiskTypeID`, and one IP through create
`AttachFloatingIP`. No data disk, resize, second IP, or auto-attach. Keep
467,800 VND as the conservative gross quote cap, not an expected charge.
Requote before purchase; refuse above the cap. Never use the NVME default.
Pass the approved fresh sum as `MaxPrice`; the API cannot lock the bill.
No refund funds another purchase. Measure IP refunds; never assume them.

Follow [parent rules](vserver-paid-writes-checks.md#parents): run-owned
restrictive group, imported throwaway key, subnet, approved VPC. No ingress
is needed; keep override false. Guest-login/banner/password/port probes
are excluded; an SSH banner does not list auth methods.

### Modes and release independence

- R2-only: verify create and its raw reads, then the manager uses the
  approved console detach/release route before deleting the server.
  Confirm controls and permissions before starting; otherwise the run
  stays blocked. The CLI never uses a private API bypass for cleanup.
- Combined: build R3 after R2's quote/schema gates and perform the steps
  below on the same server/IP. This saves a purchase but is optional;
  R2 does not depend on unshipped R3 for cleanup or a release tag.
- R3-only: use released R2 to create the same single-server fixture in
  a separately approved run, then perform the management steps. Its own
  cap remains 467800; combined approval is not standing purchase consent.

### Steps

1. Verify credit-history access/coverage; apply the API
   [decision procedure](vserver-floating-ips-api.md#credit-history-decision-procedure)
   in every mode. Save baseline inventory/balances/cost privately; inventory
   never proves ownership. Quote, check cap, create once, register known IDs
   for cleanup. Save raw create/server responses, linkage, timing, and debit
   versus estimate. Charges outside either hypothesis or above quote stop
   experiments; approved cleanup remains necessary.
2. Capture the exact WAN row linked by the created server's internal
   `floatingIpId` and interface UUID. Sanitize this pair for decode tests.
   If linkage is absent or ambiguous, stop for manager reconciliation;
   do not adopt an IP merely because it appeared during the run.
3. In combined/R3 mode, attach that same ID to the same server: no-op,
   no mutation. Detach without delete: record 204, timing, exact server
   fields, unattached WAN encoding, and ledger delta. Retain the one IP.
4. Reattach the retained ID once. Record status, identity, timing, and
   any additional charge. An unexpected charge stops the test and gates
   R3; do not call this free just because no allocation was requested.
5. Detach with `Delete`: confirm detach first, then delete that ID and
   confirm full-list absence. Record gross debits and refunds separately;
   neither net zero nor absence proves no past charge. Exercise direct
   delete of the absent ID as a no-op error check, with no extra resource.
6. Delete the server with `DeleteVolumes:false`; verify boot-volume and
   server absence and remove only run-owned parents. Record ending
   balances/cost. Reconcile posting under the bounded API procedure without
   retaining resources overnight; unresolved billing blocks the tag.

No second allocation or server-delete cascade test is allowed. Auto-attach
needs its own paid evidence; synthetic coverage cannot close that gate.

### Cleanup

Keep a private ledger of IDs returned by this run's create and their
verified relationships. Prefixes and inventory deltas alone do not prove
ownership. Never sweep all `vngcloud-live-` resources or adopt another
actor's newly visible IP. Serialize the run against other live writers.

Use a fresh cleanup context, capped at 10 min in total: detach the owned
IP, confirm, delete it, confirm absence, then delete the owned server,
its remaining owned volumes, group, key, subnet, and owned VPC. In
R2-only mode the manager performs the first IP steps through the approved
console controls. On uncertain ownership or a failed child cleanup,
stop parent deletion and report leftovers privately; never silently
claim cleanup or refund. No delete runs solely because a quote failed.

## Security review

These five traces are design obligations, not an independent verdict.
New derivation, guard, quote aggregation, and IP lifecycle paths are
unimplemented. Severity below describes the failure scenario, not a
claim that a new implementation already contains it.

### Leaked tokens: high

The bearer is attached at `internal/transport/transport.go:471` and
`:480`; a floating-IP request does carry a credential. Debug logging
omits headers/bodies at `:611`; capture redaction runs at `:593` and
error redaction is in `internal/transport/errors.go:426`. Sensitive
user-data creates suppress captures at `compute/create_server.go:385`.
These mechanisms do not by themselves sanitize successful model fields.
A hostile peer can reflect a token into a server name, WAN ID, or quote
property and leak it through partial Output, capture, or CLI rendering.

Invariant: no request credential or user-data secret reaches logs,
errors, captures, or returned models. For all affected reads and quotes,
collect sent credentials across retries/refresh and the whole operation;
reject reflections in raw JSON or decoded strings before mapping or
returning data. Recheck retained outputs against subsequently observed
credentials. Bound the tracking set to 256 credentials; exhaustion
withholds Output and returns an error, never drops old protection.
`internal/transport/sent_credentials.go:16` and
`network/nat_response_security.go:11` are existing mechanisms to inspect,
not proof that reused server/WAN reads already enforce this invariant.

Use sanitized or suppressed capture hooks and fixed errors when checking
fails. Authorized raw live capture is manager-only and stays private.
Tests inject raw/JSON-escaped, overlapping, earlier-attempt, refreshed,
and later-reflected token canaries into every output/error surface,
including partial detach/delete output. Preserve the shared accepted
nested-encoding limit from [security](../../instructions/security.md).

### Hostile responses: high

An unbounded response can exhaust memory before a wait expires.
`pricing/pricing.go:144` caps quotes at 1 MiB; preserve that limit.
Server reads use 8 MiB (`compute/server_response_security.go:301`).
Require 8 MiB for reused WAN/group and mutation responses too.
Apply limits before capture/decode; zero means unlimited under
`internal/transport/transport.go:127`.

Invariant: bound every body before capture/decode, every list walk by
pages/items/time, and each wait by a context deadline covering requests.
Missing fields cannot imply detached, absent, free, or safe. The generic
name-walk helper at `internal/core/pagination.go:45` is only a reference;
it does not prove stable pagination or an atomic inventory snapshot.
Require private presence-aware models and strict identity checks for
write guards. Test limit and limit+1 bodies, truncation, wrong types,
missing metadata, invalid IP strings, stale pages, and credential echoes.

### Redirected requests: high

`internal/transport/redirect.go:11` checks scheme/host before and after
caller redirect hooks; `transport.go:430` disables retry/redirect on
`Once`. TLS uses the standard verified transport by default
(`internal/core/client.go:380`). Credential attachment occurs after
route selection, so redirect checks do not validate an initial foreign
endpoint override. Trusted caller-supplied transport/endpoint settings
are a boundary, not an enforced GreenNode host allowlist.

Invariant: no new endpoint override, insecure TLS option, credential in
URLs, response-selected host, or cross-origin follow. All mutation
requests refuse redirects; reads retain the shared same-origin checks.
Do not claim safety against a malicious caller-supplied RoundTripper.
Test 307/308 to same/foreign hosts, scheme changes, hook URL rewrites,
zero credentials at redirect targets, and default TLS rejection. If
stronger initial-origin restrictions are required, brief shared-core
hardening separately before enabling dependent writes.

### Tampered local state: high

`internal/core/load.go:174` validates opened credential files as regular
and owner-only on Unix. `internal/tokencache/cache.go:41` binds IAM cache
names to profile, account and auth endpoints; `:199` writes mode 0600,
and `:242` checks the directory mode. Service-account keys also bind the
secret and token origin (`internal/tokencache/service_account.go:25`).
A local actor with write access can replace profile settings or cached
bearers and redirect spending. Permissions do not authenticate state
against the same OS user; IAM `readFresh` at `cache.go:157` also lacks
the strict file checks present in the service-account cache path.

Invariant: reuse the same configured identity across server, WAN, group,
and quote calls; add no credential store or cache key. Preserve 0600
files/0700 directories and read-only refusal (`internal/cli/readonly.go:47`).
Test synthetic config/cache permissions, identity separation, rejected
expired tokens, and read-only zero sends. Report the inherited IAM
cache limitation for a separate brief; do not call it fixed here or
attempt to read real credential files for this design.

### Concurrent paid writes and cleanup: high

`compute/create_server.go:362` checks duplicate names and `:410` sends
once. Neither is an atomic uniqueness guarantee. Price checks in
`internal/core/price.go:28` compare a quote, not a locked backend price.
The accepted reserve may overstate cost; removing it without attributable
ledger evidence could understate cost. Test both hypotheses and cap refusal.
Two callers can read the same state and each send one request; an
uncertain allocation may also remain invisible briefly. Repeating it can
buy another IP. A global exactly-once test would promise nonexistent API
support. A blanket cleanup sweep could delete another actor's IP.

Invariant: explicit opt-in, finite conservative price, one mutation send per
call, no automatic paid retry/rollback, and identity-based cleanup only
under owner authority. Fresh reads reduce races but cannot eliminate
price, rule, attachment, or list-consistency races. Keep the auto-attach
orphan gate closed; on ambiguity inspect IDs and billing before any new
order. Checks interleave callers, suppress resends on every failure,
keep foreign resources out of cleanup, and retain charged-IP warnings
on partial failure. Server-side conflict enforcement and billing remain
live evidence requirements, never facts proved by fake-server tests.
