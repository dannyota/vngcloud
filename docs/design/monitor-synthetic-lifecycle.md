# Synthetic quota create, delete, and ensure

This extends [quota coverage](monitor-quota-coverage.md) with class packages,
free create, active delete, and cron renewal/recovery. The caller's catalog
replaces fixed free-quota class and capacity checks in the proposed change.
Independent design review precedes code. Live runs need separate approval.

Use `danny.vn/vngcloud/monitor`, standard library only, via public SDK calls.
Preserve public types, flags, and notification behavior. Copy Inputs before
I/O. Existing create flags select one offer explicitly. Selecting by class
name would depend on catalog labels, and adding selectors to renewal would
repeat identities already in the quota detail.

## Wire evidence and fixtures

Sanitized IAM console captures and NOTES.md come from the manager; no network
was used. These are observations, not published API guarantees. Paths are under
`billing-api/`; [API evidence](monitor-quotas-api.md) owns renewal/recovery.

- `GET v2/synthetic/quota-class/{classId}/packages`: Basic returns a bare array
  with one `unit-package-free`: id equals quota.packageId, price 0.0, location
  1, api 1, tierId 1001, tierName `Unit Tier Free`. Notification fields include
  zeros and nulls; resourceId/classId/className/packageId are null. The route
  supplies class membership, not those nullable fields. This establishes a
  class-to-package join, never a join to the v1 catalog.
- Create quote: `POST v2/synthetic/prices/created-price` with
  `{redirectUrl:"",packageId,quantity:10,buyWith:{}}` returns the zero-price
  shape. No resourceId or period is sent. Quantity counts API tests.
- Create order: `POST v2/synthetic/quota` with
  `{redirectUrl:<synthetic-page>,packageId,quantity:10,buyWith:{},`
  `monthPeriod:1,pay:true}` returns 200 application/json. The response body was
  lost on navigation. Read-back was 404 about 1 s later, then a new ID ACTIVE
  about 15 s later, start at order time, end one month later.
- Delete: `DELETE v1/synthetic/quota`, no body or ID, returns 204 about 4.6 s
  later. The quota GET then returns 404 with `{"message":"Not found synthetic
  quota"}`; trash count is 0. Every synthetic check was deleted: GetCheck
  returned 404 and the list was empty. Channels survived. The dialog says
  permanent deletion but omits checks. Recreating the quota does not restore
  checks or their IDs.

Live create covers Basic, 10 API tests, one location, one API test per
package unit, and one month. Other free class and capacity combinations
are unverified. The create body on success, disabled/paid package behavior,
and conditional writes also remain unverified.

| Source | Permitted use |
|-|-|
| `ListSyntheticClassPackages.json` | Raw Basic package array, 200; SDK catalog decode |
| `QuoteCreateSyntheticQuota.json` | Raw quote, 200; SDK quote decode |
| `GetSyntheticQuotaDeleted.json` | Raw absence body, 404; SDK absence recognition |
| `CountQuotasInTrashAfterDelete.json` | Raw count 0; evidence only, no settlement dependency |
| `CreateSyntheticQuota.request.json`, `QuoteCreateSyntheticQuota.request.json` | Request assertions, never response fixtures |
| `DeleteSyntheticQuota.request.json` | Method/path/status metadata, not a JSON response; use an empty 204 test response |
| `GetCheckAfterQuotaDelete.error.json` | Summary of check reads, not raw error evidence |
| `GetSyntheticQuotaCreated.json` | Sanitized quota-shaped read; establish raw origin before using as a raw fixture, since the notes describe CLI read-back |

Use the [catalog and detail fixtures](monitor-quotas-api.md#fixtures) for
identity joins. Decode raw fixtures through the public SDK and label mock
create responses, other free classes, and errors. The create response body
remains unverified.

## Class packages

Extend `SyntheticQuotaClass` additively with `Packages []SyntheticClassPackage`.
The new type has `ID, Name, TierName string`, `TierID, API, Location int`, and
`Price *float64` (monthly VND per unit). API and Location are units, not the
quota's purchased capacity. Keep top-level v1 Packages separate. The shared
[free terms resolver](#free-terms-from-the-caller-catalog) supplies class
option eligibility without changing the public types.

After the base catalog's three reads, fetch each returned class's packages in
sorted class order, including disabled classes; attach by the requested class
ID. Validate every response-derived path ID with CheckPathID first. Cap fan-out
at 64 classes; refuse before package requests if exceeded. This is a client
safety limit, not a provider maximum. No pagination is observed; never truncate
or invent pages. Empty arrays mean no returned packages. Any component failure,
including an unverified disabled-class 404, returns nil Output. Use the
[retained-body budget][body-budget], retries, cancellation.

Require nonempty IDs, unique package IDs within each class, valid integral
positive API/Location, nonnegative integral TierID, and finite nonnegative
prices when present. Preserve missing/null Price as unknown. Reject malformed
arrays or fields with safe InvalidResponse. Sort packages by Name/ID. Allow an
ID in different classes without merging. Tables show class package units,
unit price, tier, and class status. JSON/text/query retain all three views.

## Free terms from the caller catalog

Use one private resolver for create, renewal, recovery, and their quotes.
Reuse its eligibility rules for catalog and status. Read the caller's classes,
the matched class's packages, and settings through the existing sensitive
read path. Keep the 64-class, 1 MiB component, and 8 MiB retained-body caps.
Keep resolver reads within the caller's account and exclude the v1 package list.

1. For create, use input ClassID, PackageID, and Quantity (API test capacity).
   For an existing quota, read detail at `v2/synthetic/{quota.ID}/quota-detail`.
   Require `detail.resourceId == quota.ID` and `detail.packageId == quota.PackageID`.
   Use `detail.classId`, the quota's PackageID, and `detail.api` as selectors.
   Validate path IDs before use. Keep class ID private to the SDK.
2. Resolve exactly one class by ID with status ACTIVE, exactly one package by
   ID in that class, and exactly one option by package ID and package Location.
   If the option reports a status, require ACTIVE. Match existing detail
   location to the option's location. Names and priority are display fields.
3. Require positive integral units and capacity. Capacity must fit the option's
   min/max API and step from min, and be divisible by package API. Expected
   location capacity is the option's Location, independent of API quantity.
   Compute unit count as API capacity divided by package API, and ListPrice
   as unit price times unit count, with finite checked arithmetic. The wire
   quantity and SDK Quantity use API capacity for the verified unit mapping.
4. Validate distinct positive `monthPeriods` and a positive free maximum.
   Select exactly one listed period at or below `maxMonthPeriodForFreeQuota`.
   Apply the wire limits below. Freeze the period for quote output, mutation,
   and retained terms. Compare periods as a set and keep the payment-method
   check across rereads.
5. Require explicit zero package unit price and exactly zero `optimumPrice`.
   Existing quotas also require explicit zero detail price. Check exact zero
   on the JSON number so a nonzero value rounded to zero fails closed.
   Preserve the existing Unpriced and MaxPrice classifications. A budget
   that covers a positive price still refuses. Paid Synthetic remains class B.

Refuse a missing match, unavailable class/option, invalid capacity, or zero or
multiple eligible periods with ErrInvalidInput. Duplicate or malformed catalog
values, missing/null prices, and mismatched detail identity use body-free
InvalidResponse. A valid eligibility change on the final reread returns
ErrQuotaChanged. Compare the frozen IDs, option bounds, units, capacity, zero
prices, and period settings. Reconcile open attempts from frozen read-back
terms before a new derivation. The final reads narrow the race before Once;
the server supplies neither a conditional write nor a price lock.

In `monitor/quotas_synthetic_create.go`, replace the Basic, API, location,
and quantity literals in preparation with resolved terms. Confirm the frozen
class ID and capacities in read-back. Use the resolved period in place of the
fixed order, quote-output, and retained-attempt period.

Create and renewal quotes omit monthPeriod. The wire mapping for packages with
more than one API test per unit and zero-price orders for a term other than
one month need separate evidence. Refuse those shapes until that evidence
exists. Other class names and total API and location capacities use the same
catalog and read-back checks.

## Create

`CreateSyntheticQuotaInput` has required `ClassID, PackageID string` and
`Quantity int`, optional `MaxPrice float64` (zero default) and `NoWait bool`.
Callers select class/package/API-count Quantity using the existing flags.
`QuoteCreateSyntheticQuota` takes that Input, ignores MaxPrice/NoWait, and
returns `QuoteCreateSyntheticQuotaOutput`, an alias of QuotaQuote. Quantity and
MonthPeriod come from the resolved terms, ResourceID is empty, ExpectedEndTime
is nil, and Currency is VND. Quotes require absence and eligibility.

`CreateSyntheticQuotaOutput` embeds QuotaMutationOutcome and has `Quota *Quota`,
`Created bool`, `OrderID string`, `PaymentPending bool`, and `SentAt
*time.Time`. Initialize it before validation; Quota remains nil and Created
false until read-back. SentAt is the frozen local UTC send boundary, not
transmission proof; nil until then, retained across reconciliation.

Validate and execute in this order:

1. Reject nil Input, empty IDs, malformed ClassID path shape, nonpositive
   Quantity, and for writes nonfinite/negative MaxPrice before requests.
   PackageID is an opaque body value, not a path ID. Enforce the SDK loaded
   profile read-only rule for writes; quotes remain available read-only.
2. For writes acquire the existing context-aware synthetic lock and reconcile
   any open attempt before new preparation. A matching create reconciles frozen
   terms and SentAt, never rebuilds them from a catalog. Different intents
   follow the parent's no-send reconciliation rule.
3. Read only `v1/synthetic/quota` for absence. Accept solely its 404 with the
   exact captured message after bounded decoding. Other 404s, including
   auth/proxy or detail failures, are errors. Any existing quota, regardless of
   status, refuses with ErrInvalidInput before catalog or quote calls.
4. Resolve the free terms from the caller catalog. Freeze IDs, option bounds,
   unit price, capacity, and period. A single builder maps those terms to the
   evidenced quote and order bodies, including empty buyWith, pay true, and
   the fixed redirect. Quotes never open records and return nil Output on error.
5. Apply the free price requirements. Keep the existing budget and error
   order: Unpriced, then ErrPriceAboveMax if max(ListPrice, OptimumPrice)
   exceeds the write budget, then refusal of any remaining positive price.
6. Immediately before send, reread absence and resolve the same terms again.
   A quota appearing or changed terms returns ErrQuotaChanged. Malformed
   peer data returns safe APIError. Use the original quote and terms.
7. Capture SentAt immediately before the single Once order. Use the [tolerant
   response policy][response-policy] with observed status 200. NoWait returns
   accepted/unconfirmed and retains the record. Otherwise poll every 2 s with
   the shared hard 120 s child deadline and caller cancellation. Preparation
   uses caller context and normal request limits.

Confirm only an ACTIVE quota with expected PackageID, StartTime >= SentAt, and
EndTime > StartTime. Read detail at that quota's exact ID and match its resource
and package IDs, frozen class ID, and API and location capacities. Freeze the
confirmed ID. Confirmation uses state and identity. 404/CREATING, an older
start, or mismatched detail cannot confirm. Other read errors return
NotSettled. Clock skew or coarser server
timestamp precision can prevent confirmation; never round SentAt backwards or
resend to fix it. A concurrent matching create can still satisfy the read
predicate: no server idempotency key or order-to-quota proof exists. State that
limit plainly.

## Delete

Implement the existing refusal-only `DeleteSyntheticQuota` without changing
its import path or Output alias. Input keeps `Purge bool` and
`ExpectedQuotaID string`, now required. Nil, empty/malformed ID, Purge true,
or SDK read-only refuses before requests. No successful synthetic caller
previously depended on an omitted guard.

`DeleteSyntheticQuotaOutput` remains the alias of DeleteQuotaOutput: `Quota
*Quota`, `Stage string`, `Purged bool`. Quota is the intended frozen target,
never proof of which resource the ID-less server request deleted. Return nonnil
Output on every result: initially Quota nil, Stage empty, Purged false. Preserve
notification behavior/type identity, with one Stage.

Under the shared lock reconcile prior attempts first. With no open attempt, read
quota/detail, require expected ID equality, ACTIVE, unexpired EndTime, POC
false, and auto-renew false; freeze all lifecycle terms. Compare expected ID
before any no-op or mutation. An initial absence is NotFound, not success.
Reread the full snapshot immediately before send; changed ID, package, expiry,
flags, status, or quantity refuses. Do not quote, list checks, list trash, or
fetch packages. Delete is not priced and has no MaxPrice.

Send the captured DELETE with Once, accepting only empty 204. The existing
lifecycle stages apply: prepared after the snapshot, sent after a possible send,
settled only after the quota GET's evidenced absence response. Poll only that
GET every 2 s under a hard 60 s child deadline and caller context; a detail 404
cannot settle. A different ID, status change, or empty trash never settles.
Purged remains false. Nonempty/invalid success or any other possible-send
failure returns partial Output and NotSettled; keep the record. No NoWait is
introduced for delete. Never recreate quota/checks as cleanup.

Keep existing lifecycle ErrInvalidInput for an initial guard/state mismatch,
ErrUnexpectedStatus for an unknown status, and safe APIError for malformed peer
data. An unresolved prior attempt takes precedence: a guard mismatch keeps the
record, returns NotSettled wrapping ErrInvalidInput, and never claims no send.
Resolve only by reads, then return the changed-intent error without deleting
again. This precedence strengthens the synthetic path only.

## Ensure

`EnsureSyntheticQuotaInput` has optional `ExpectedQuotaID string`, `MaxPrice
float64`, `MaxDaysLeft int`, and `NoWait bool`. SDK defaults are 0, 0, false:
zero MaxDaysLeft disables the not-due skip, as renewal does; the CLI default is
7. Reject nil Input, bad ID, negative/overflowing day threshold,
nonfinite/negative budget, and read-only before requests. It reads its own
expiry baseline; zero permits early renewal and its visible 409.

`EnsureSyntheticQuotaOutput` embeds QuotaMutationOutcome and has `Action
string`, `Quota *Quota`, `PreviousEndTime *time.Time`, `Renewed, Recovered
bool`, `OrderID string`, and `PaymentPending bool`. Action is `none`, `renew`,
or `recover`; it identifies the selected action, not successful transmission.
Before selection and on not-due it is none. Only read-back sets the selected
confirmation boolean.

Acquire the same lock, reconcile retained intent first, then read fresh
quota/detail. Check optional ExpectedQuotaID before a skip. Validate identity,
times, catalog-derived free eligibility, and POC/auto-renew off using shared
preparation. A missing quota returns NotFound. Choose exactly once:

- ACTIVE, EndTime > now: if positive MaxDaysLeft and expiry is further away,
  return Sent no, Stage not-due, Action none, baseline expiry, nil Quota/error.
  Otherwise select renew with this ID and ExpectedEndTime.
- IN-TRASH, EndTime < now: select recover with this ID. MaxDaysLeft does not
  prevent recovery of an expired quota.
- Exact expiry, expired ACTIVE, unexpired IN-TRASH, CREATING, ERROR, or known
  ineligibility refuses with ErrInvalidInput. Unknown status uses
  ErrUnexpectedStatus. Never convert a failure into a different action.

Reuse lock-held renewal/recovery helpers, their quotes, price guards, final
rereads, Once, and 120 s settlement bounds. Do not call lock-acquiring public
methods while holding the lock. Pass the selected ID as an internal required
guard even when the caller omitted ExpectedQuotaID. A replacement with the same
expiry still fails ErrQuotaChanged. A state change between selection and send
aborts; the invocation never switches actions or sends two mutations.

Record outer operation EnsureSyntheticQuota, action, and the delegate's frozen
terms under the same synthetic account/kind hold. Repeated ensure reconciles
that action without selecting from the now-changed state. Other public writes
reconcile but cannot claim its result or send in that call. Use retained Sent
evidence; unresolved guard mismatch wraps ErrQuotaChanged under NotSettled.
After resolution apply the parent's changed-intent rule. All safe APIError and
outcome Operation values name monitor.EnsureSyntheticQuota.

A renew-only service account cannot recover. Preserve recovery quote/mutation
403 as Forbidden/exit 1 with Action recover: quote has Sent no/preflight;
mutation has Sent yes/refused. Never hide it as not-due, switch identity,
request grants, or create. Exact recovery permission names remain unevidenced.

## Outcomes and errors

Create and ensure reuse every row of the [common outcome table][outcomes],
including pre-SDK failures, safe stderr agreement, uncertainty precedence, and
errors.Is/errors.As. Their JSON keys are exactly their Output fields listed
above plus Sent, Stage, Error, with no omissions; use the existing quota
projection. Only create adds Created/SentAt; only ensure adds Action,
PreviousEndTime/Renewed/Recovered. Error.Operation names the public method. Sent
yes means a mutation response, unknown possible transmission, never
confirmation. Retained uncertainty cannot become Sent no on a later call.

| Additional result | Stage / confirmation | Exit |
|-|-|-|
| Create finds existing quota or unsupported class/quantity | preflight, Created false; InvalidUsage | 2 |
| Create final absence/terms changed | preflight, Created false; QuotaChanged | 1 |
| Create read-back matches | confirmed, Created true | 0 |
| Ensure not due | not-due, neither confirmation true | 0 |
| Ensure delegates | Common row, selected Action, only its confirmation | Common exit |
| Delete validation, read-only, or guard refusal | empty or prepared, Purged false; InvalidUsage or ReadOnly | 2 |
| Delete read/transport fails before send | empty or prepared; safe existing class | 1; auth 3, NotFound 4 |
| Delete definite mutation 4xx | prepared; safe APIError, no open record | 1; 401 is 3, 404 is 4 |
| Delete possible-send failure or wait fails | sent; NotSettled and partial target | 1 |
| Delete read confirms absence | settled, Purged false | 0 |

Delete retains its legacy stage model, not Sent/Stage from QuotaMutationOutcome.
Its CLI prints Quota, Stage, Purged, and fixed Effect on every return: `Deleting
this quota permanently deletes every synthetic check.` This is a warning, not a
claim that checks were enumerated or confirmed absent. Error classification is
on stderr. Empty/prepared alone is not evidence of no send; a definite server
refusal can have that stage. Initialize pre-SDK failures likewise. Preserve
errors.Is sentinels and errors.As safe APIError metadata. All operations
withhold peer messages and URLs. Definite 400/401/403/404/409/429 remain status
errors; 500/502/503/504, unexpected 2xx/3xx, malformed or oversized success, and
possible-send transport failures retain uncertainty.

## CLI and cron

Register create-synthetic-quota and ensure-synthetic-quota as Write,
quote-create-synthetic-quota as Read, and delete-synthetic-quota as Write and
Destructive. All writes refuse read-only flag/environment before config, profile
after config before requests; SDK writes check the loaded profile. No command
prompts. Quotes hide MaxPrice/NoWait flags and never order.

Create flags: required --class-id, --package-id, --quantity, and explicit
--max-price (or JSON fields, including MaxPrice 0); optional --no-wait. Delete
requires --yes and --expected-quota-id; --purge remains refused. Help, wiki,
table/text/JSON output state that every synthetic check is permanently deleted.
Consent covers that effect; channels survive in the observed run, but no
restoration or cleanup is automatic. Ensure flags: --expected-quota-id optional,
--max-days-left default 7, explicit --max-price, optional --no-wait. JSON
omitted MaxDaysLeft also gets 7; explicit zero stays zero. Reject --query on all
three writes before requests so scripts retain outcomes/effects. Catalog and
quote allow query. Positive budgets retain the pay:true/local-guard warning;
paid classes refuse.

The wiki adds `vngcloud monitor ensure-synthetic-quota --max-days-left 7
--max-price 0 --output json` as one invocation, with no NoWait. Keep the
[environment auth and pinned installation](cli.md#unattended-ci) guidance and
renewal-only example; ensure is an additional cron option. Require serialization
and a durable pending marker before invocation. Missing JSON, a killed process,
or unconfirmed output blocks later scheduled writes until read-only
reconciliation; a new process has no ledger. Explain permission refusal for
renew-only accounts on expiry. Do not install or enable cron. Update
Monitor-Quotas, Monitor, Monitor-Alerts with SDK types, guards, unit versus
total prices, create clock/race limits, delete effects, and ensure's exact
outcome/exit table. Generate CLI-Monitor/CLI from source notes, inspect links,
run goldens, generation idempotence, and wiki preview in each slice.

## Threat analysis

- **Leaked tokens, high:** a create response, delete error, or ensure refresh
  echoes credentials into output/ledger. Sensitive reads and sent-token
  scrubbing at `monitor/quotas.go:184`, write scrubbing at
  `monitor/quotas_write.go:321`, capture suppression and safe debug at
  `internal/transport/transport.go:594` and `:607` are the existing paths.
  Invariant: no credentials, raw body, or payment URL leaves any outcome, error,
  capture, or debug path. Retain credentials across reconciliation and refresh;
  test canaries in every field, malformed body, and CLI format.
- **Hostile peer, high:** inflated class fan-out, missing prices, forged
  absence, or false settlement triggers a wrong write. Existing bounds at
  `monitor/quotas.go:184`, decoder at `:282`, and identity checks at
  `monitor/quotas_lifecycle.go:375` must cover new reads. Invariant: bounded
  bodies/fan-out, exact absence route/body, valid unit arithmetic and
  identities, no invented pagination, and safe partial errors. Test retry body
  accounting, overflow, nulls, duplicates, response IDs, delayed reads, create
  start boundary, and delete detail-404 confusion before allocation.
- **Foreign host, high:** redirects or modified hooks move tokens or replay
  orders/deletes; ensure inherits either risk. Attachment is at
  `internal/transport/transport.go:471`, hook origin checks at
  `internal/transport/redirect.go:11`, Once redirect refusal at
  `internal/transport/transport.go:433`. Keep TLS and same-origin checks, fixed
  routes/redirect body, no URL following. Test every 3xx and hook rewrite, with
  zero foreign requests. Explicit endpoints/custom transports remain trusted
  caller choices, not protection from that caller.
- **Local tampering, high:** altered profiles/caches select another account or
  erase read-only intent before destructive create/delete or cron writes.
  Existing identity key at `internal/tokencache/cache.go:41`, directory check at
  `:242`, credential write at `internal/cli/configfile.go:158`, and CLI
  read-only at `internal/cli/readonly.go:24`/`:43` remain required. Preserve
  0600 files, 0700 cache directory, identity binding, and existing token reuse
  checks; no new storage. Test clean-home environment auth and tampered
  cache/profile refusal. Same-user compromise can bypass local policy;
  identity/expiry guards are not authentication or a cross-process ledger.
- **Concurrent writes, critical for delete, high for charges:** another actor
  replaces the quota after the final read, or cron repeats an uncertain
  order/extension. Existing lock/ledger at `monitor/quotas_write.go:109`, Once
  at `:168`, and bounded waits at `monitor/quotas_wait.go:38` must span
  create/delete/ensure plus existing synthetic writes. Invariant: one mutation
  attempt, frozen intent, no fallback, no cleanup after uncertainty. Test races,
  two callers, process-boundary limits, stale ID/expiry, quote changes, and real
  transport replay. No server price lock or conditional identity exists; delete
  can erase a replacement and all its checks. Caller serialization and explicit
  destructive consent narrow this risk; they cannot prove the affected identity
  or authorize automatic recreation.

## Deterministic checks

Use loopback HTTP, fake clock/sleep, synthetic auth, and raw SDK fixtures;
retain existing quota/notification/transport/cache regressions. Require:

- Catalog: joins, disabled/unknown classes, empty/malformed arrays, duplicates,
  units/tier/price, fan-out, cancellation, nil failures, and retry body budget.
  Mock a free class with a different name, API capacity, and location count.
  Check no match and ambiguous matches, missing prices, capacity bounds/step,
  indivisible units, and zero/multiple/unverified periods.
- Create: exact absence and required inputs, frozen quote/order bodies with
  buyWith {}, no resourceId, computed total and price refusal, final changes
  in class/package/option/settings, and tolerant responses. Check clock skew,
  delayed ACTIVE, wrong detail class ID with the same name, cancellation or
  slow-read deadline, NoWait, and repeated calls without a second order.
- Delete tests for required ID/yes, Purge/read-only zero requests, all
  ineligible states, initial versus final absence, replacement race, empty 204,
  every stage/error row, 60 s hard bound, reconciliation, and warning text in
  help and every output format, including failure output.
- Ensure tests for each state and exact boundary, threshold defaults/zero/
  overflow, stale optional ID before skip, replacement with equal expiry, time
  crossing while quoting, permission refusal at read/quote/mutation, both
  delegates, outer operation identity, repeated/different intent, and no
  create/delete/notification/v1 package calls. Test no deadlock.
- Real SDK transport and actual CLI through public SDK: positive retries, each
  outcome row, 400/401/403/404/409/429, 500/502/503/504, every redirect, one
  failed dial, disconnect after body receipt, cancellation before/after send,
  malformed/oversize success, and read-back failure. Count mutation routes and
  dials, not mocked method calls: zero for refusal/skip/read-only
  reconciliation, at most one otherwise, including bodyless DELETE. Retain
  HTTP/1 stale-connection and HTTP/2 REFUSED_STREAM/GOAWAY no-replay tests, no
  GetBody/idempotency headers, and no 401 refresh resend for mutations.
- Assert stdout/stderr, errors.Is/errors.As, exits, pre-SDK/render failures,
  canaries, capture suppression, and no mutation after uncertainty. Require
  release-specific ensure CLI transport evidence before schedule enablement.

[body-budget]: monitor-quota-coverage.md#read-contracts
[response-policy]: monitor-quota-coverage.md#unverified-write-responses
[outcomes]: monitor-quota-coverage.md#machine-readable-write-outcomes
