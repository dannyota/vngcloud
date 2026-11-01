# Monitor Synthetic Quota

Create, renew, recover, ensure, or delete the free Synthetic quota through
`danny.vn/vngcloud/monitor`. Use the client from
[Monitor Setup](Monitor.md#setup). See [Monitor Quotas](Monitor-Quotas.md) for
quota reads, catalogs, status, Email and SMS, and auto-renew.
[CLI Monitor](CLI-Monitor.md) lists the command flags.

## Synthetic recovery and renewal

Free quota renewal follows the console's last 7 days rule: renew only in the
last 7 days before expiry. Use `MaxDaysLeft: 7` or CLI `--max-days-left 7` to
skip an earlier renewal. An early free-quota renewal returns a visible
`Conflict` API error (409, exit 1), with no state change. This error is not
suppressed or retried.

`RecoverSyntheticQuota` requires `ExpectedQuotaID` from a fresh read and an
expired IN-TRASH quota. Recovery starts a new term at recovery time.
`RenewSyntheticQuota` requires an RFC 3339 `ExpectedEndTime`, an ACTIVE quota,
and a future expiry. Equivalent timezone offsets match as instants. Neither
method switches to another operation or creates a missing quota.
`QuoteRenewSyntheticQuota` takes the renewal input and ignores `MaxPrice`,
`MaxDaysLeft`, and `NoWait`; the CLI hides those quote flags.

Both writes require the evidenced Basic capacity, with POC and auto-renew off.
The identity-checked detail price supplies `ListPrice`; no v1 catalog ID or
price is substituted. The budget guards the higher of quote and detail prices.
Missing prices fail closed, a zero quote against a positive list price is
`Unpriced`, and positive prices remain unsupported even with a larger budget.
SDK `MaxPrice` defaults to zero. CLI writes require explicit `--max-price` or
JSON `MaxPrice`, including zero, and the required identity or expiry guard. They
need no `--yes` and never prompt.

Writes freeze terms, quote, reread eligibility, and send once. The server routes
cannot bind the expected identity or expiry, so a residual race remains. Outputs
include `PreviousEndTime`, `Quota`, `OrderID`, `PaymentPending`, and `Recovered`
or `Renewed`. Only read-back of the same ID/package, ACTIVE with a later expiry,
confirms. A 200 alone proves no settlement. Polling runs every two seconds for
at most 120 seconds, bounded also by caller cancellation. `NoWait` skips
settlement only and leaves an open attempt. Reconcile uncertainty with reads and
console billing; do not repeat a write or reset the client to bypass its
retained attempt.

## Synthetic ensure

`EnsureSyntheticQuota` and `ensure-synthetic-quota` choose one action from a
fresh quota/detail read: renew ACTIVE with future expiry, recover expired
IN-TRASH, or skip an ACTIVE quota outside a positive `MaxDaysLeft` window. Exact
expiry, expired ACTIVE, future IN-TRASH, and other ineligible states refuse. The
optional `ExpectedQuotaID` is checked before a skip. The SDK's `MaxDaysLeft`
defaults to zero; the CLI defaults to seven, including when JSON omits the
field. Explicit zero permits early renewal and its visible 409. CLI `MaxPrice`
consent is required, including zero.

Ensure uses the same free-package eligibility, quotes, price guards, final
reread, send-once rule, settlement bounds, and per-client lock and ledger as
renewal and recovery. It never creates or deletes and never switches actions
after a failure. A replacement with the same expiry still refuses. Repeated
calls reconcile the retained action before selection. Another write can
reconcile but cannot claim its result or send again in that call. Separate
processes share no ledger.

`EnsureSyntheticQuotaOutput` has exactly `Sent`, `Stage`, `Error`, `Action`,
`Quota`, `PreviousEndTime`, `Renewed`, `Recovered`, `OrderID`, and
`PaymentPending`. `Action` is `none` before selection and on not-due, otherwise
`renew` or `recover`, even on refusal. Only read-back confirms. The outcome and
exit table below applies. A renew-only identity cannot recover: quote or
mutation Forbidden returns exit 1 with `Action: recover`. Quote refusal has Sent
no/preflight; mutation refusal has Sent yes/refused. No permission grant or
fallback follows.

## Unattended CI

Use environment-only service-account credentials, with no profile, stdin, or
browser: `VNGCLOUD_CLIENT_ID`, `VNGCLOUD_CLIENT_SECRET`, `VNGCLOUD_REGION`, and
`VNGCLOUD_PROJECT_ID`. Store the secret in the job's secret store. Unset
`VNGCLOUD_PROFILE` and config/credential path overrides so another credential
source cannot win. Use an ephemeral writable home; automatic cache files use
0600 inside a 0700 directory. Do not upload that cache as an artifact. Pin a
reviewed version with checksum verification:

```sh
GOSUMDB=sum.golang.org GOPRIVATE= GONOSUMDB= \
  go install danny.vn/vngcloud/cmd/vngcloud@vX.Y.Z
```

Replace `vX.Y.Z` with the accepted release. Stop on installation or checksum
failure. Module checksums verify downloaded content, not a binary or build
attestation. Before enabling a schedule, retain evidence for that exact release
commit: the test command and CI run URL, request-count assertions, and
stdout/stderr/exit results for not-due, price, stale guard, early 409,
uncertainty, confirmation, and ensure action selection where applicable.

Renewal, recovery, and ensure emit one complete stdout result even on preflight
failure, plus a fixed safe lowercase `error` envelope on stderr on error. JSON
has exactly `Sent`, `Stage`, `Error`, `PreviousEndTime`, `Quota`, `OrderID`,
`PaymentPending`, and the operation's `Recovered` or `Renewed` key. Ensure also
has `Action` and both confirmation keys. `Error` is null on success; otherwise
it contains `Code`, `Operation`, and `Status`, including status 0 when absent.
Tables and text retain the same fields. `--query` is refused before requests.
Only confirmation makes the boolean true; null quota/expiry and false/zero stay
distinct.

| Result | Sent | Stage | Error.Code (Status) | Exit |
|-|-|-|-|-|
| Outside positive MaxDaysLeft | no | not-due | null Error | 0 |
| Read-back confirms | yes or retained unknown | confirmed | null Error | 0 |
| Price refusal | no | preflight | PriceAboveMax or Unpriced (0) | 1 |
| Stale guard or changed final terms | no | preflight | QuotaChanged (0) | 1 |
| Mismatched unresolved prior attempt | retained yes or unknown | unconfirmed | NotSettled (retained status or 0) | 1 |
| Definite mutation refusal | yes | refused | Conflict (409), BadRequest (400), Forbidden (403), Throttled (429) | 1 |
| Mutation auth or missing quota refusal | yes | refused | Unauthorized (401), NotFound (404) | 3, 4 |
| Accepted NoWait | yes | accepted | null Error | 0 |
| Response seen, settlement unconfirmed | yes | unconfirmed | NotSettled (mutation status, else cause status or 0) | 1 |
| Possible send without a response | unknown | unconfirmed | NotSettled (0) | 1 |
| Proven not sent | no | preflight | RequestFailed (0) | 1 |
| Input, consent, read-only, config refusal | no | preflight | InvalidUsage, ReadOnly, InvalidConfig (0) | 2 |
| Missing credentials or token exchange failure | no | preflight | NoCredentials or ServiceAccountLoginFailed (status or 0) | 3; canceled exchange 1 |
| Other pre-send failure | no | preflight | TokenCacheFailed, RequestFailed, UnexpectedStatus (0); NotFound (404); BadRequest, Unauthorized, Forbidden, Conflict, Throttled, ServerError, ClientError, InvalidResponse, ResponseTooLarge (source status) | 1; auth 3, NotFound 4 |

Serialize jobs per account/kind across processes. Persist the baseline and a
durable pending-attempt marker before invocation. New processes have no SDK
ledger. Keep the marker on unconfirmed or missing/invalid output, including a
killed process or broken stdout. A 404 or unchanged expiry is inconclusive.
Schedules reconcile through reads or stop; they never blindly retry or purchase.
Close the marker only for confirmation or a proven no-effect refusal/skip. Do
not use error message parsing.

The simplest cron command chooses renewal or recovery automatically:

```sh
vngcloud monitor ensure-synthetic-quota --max-days-left 7 \
  --max-price 0 --output json
```

Hold the account/kind lock and save a fresh read baseline plus a durable pending
marker before this command. Capture and validate its JSON and exit as in the
renewal example below; confirmation requires either `Renewed` or `Recovered`
according to `Action`. Keep the marker on missing or unconfirmed output. A
renew-only identity stops with Forbidden if recovery is needed.

For renewal-only access, hold the account/kind lock and use a fresh expiry:

```sh
set -eu
vngcloud monitor quota-status --kind synthetic
quota_json=$(vngcloud monitor get-synthetic-quota --output json) || exit $?
expiry=$(printf '%s' "$quota_json" | jq -er '.Quota.EndTime') || exit $?
# Persist quota_json and a pending marker in durable storage before this call.
rc=0
vngcloud monitor renew-synthetic-quota --expected-end-time "$expiry" \
  --max-days-left 7 --max-price 0 --output json \
  >outcome.json 2>outcome-error.json || rc=$?
if ! jq -e '(.Sent == "no" and (.Stage == "not-due" or
    .Stage == "preflight")) or (.Stage == "refused") or
    (.Stage == "confirmed" and .Renewed == true)' outcome.json >/dev/null; then
  # Keep the pending marker. Read-only reconciliation must resolve it.
  vngcloud monitor quota-status --kind synthetic --read-only
  exit 1
fi
# Archive outcome.json, stderr, and rc; close the durable pending marker.
vngcloud monitor quota-status --kind synthetic
exit "$rc"
```

## Free synthetic create

`CreateSyntheticQuota` and `QuoteCreateSyntheticQuota` require ClassID,
PackageID, and Quantity. Select them from `list-synthetic-packages` using class
unit packages. Only the evidenced free Basic package with one location and
quantity 10 is supported. A quota in any state refuses create; only the quota
route's exact absence response permits it. Paid classes and bundled notification
purchases remain unsupported.

The CLI commands are `create-synthetic-quota` and
`quote-create-synthetic-quota`, with required `--class-id`, `--package-id`, and
`--quantity`. Create also requires explicit `--max-price`, including 0. The
quote hides MaxPrice and NoWait and works read-only. Create sends `buyWith: {}`,
one month, and `pay: true` once. ListPrice is class unit price times quantity;
MaxPrice guards the higher of list and quote prices locally. It does not cap a
server debit. Final rereads refuse changed terms.

Every create result has Sent, Stage, Error, Quota, Created, OrderID,
PaymentPending, and SentAt. The outcome and exit rules above apply. SentAt is
the local UTC send boundary, not proof of transmission. Only ACTIVE quota/detail
read-back with matching package, Basic capacity, StartTime >= SentAt, and
EndTime > StartTime sets Created. Clock skew or coarse server timestamps can
prevent confirmation. A concurrent matching create can satisfy this predicate
without proof of order identity. No raw create success body was retained.
`--no-wait` returns accepted without confirmation and leaves an open attempt.
`--query` refuses. Serialize per account/kind, persist a pending marker, and
reconcile by reads after an uncertain or missing result; another process has no
ledger.

## Delete

Deleting this quota permanently deletes every synthetic check.
`delete-synthetic-quota` requires `--yes` and `--expected-quota-id`.
`DeleteSyntheticQuota` keeps its existing Output alias and requires the expected
ID. Only ACTIVE, unexpired quotas with auto-renew and POC off can be deleted.
Purge, query, and read-only writes refuse before requests. Every return prints
Quota, Stage, Purged false, and the fixed Effect warning, including preflight
failures. The ID-less DELETE sends once; only the quota route's exact absence
confirms settlement within 60 seconds. Detail 404, changed identity/status, and
empty trash do not confirm. Channels survived the observed deletion. Recreating
the quota restores neither checks nor check IDs. No automatic cleanup or
recreation follows uncertainty.
