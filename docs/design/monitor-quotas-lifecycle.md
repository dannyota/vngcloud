# vMonitor quota lifecycle: delete and auto-renew

Status: Accepted as part of [check state and quotas](monitor-quotas.md);
every path below stays gated on its own row of the support matrix.
Nothing here approves a live run.

Add quota deletion, purge from trash, and the auto-renew setting for the
Email, SMS, and Synthetic Test quotas. The parent design owns the shared
mechanics: frozen terms, the price guard, `Once`, the per-Client lock, the
unresolved-attempt record, and the wait rules. [API evidence and
checks](monitor-quotas-api.md) owns the console wire facts; this file owns
the rules, the support matrix, checks, and live checks for these writes.

## Wire facts and unknowns

All paths are console code under `billing-api/v1` on the `Monitor` root;
none has been sent. Success statuses, response bodies, and error shapes
are unknown. The SDK accepts only the statuses the live check for that
row recorded; it does not guess 200 or 204.

| Write | Request | Unknown |
|-|-|-|
| Delete | `DELETE {email\|sms\|synthetic}/quota`, no body, no ID; targets the account's only quota of that kind | Status, body, whether the quota moves to trash or disappears, whether it spends a monthly notification action |
| Purge | `DELETE trash/{kind}/quota`, no body, no ID | Status, body, whether a trash entry must exist first; the trash list item shape |
| Auto-renew | `POST {kind}/quota/{id}/update-auto-renew` with `{"resourceId":<id>,"enabled":<bool>,"period":1}` | Status, body, behavior on a free or POC quota (the console hides the control), the nonzero `autoRenew` read encoding |

The console shows auto-renew instead of renew for paid quotas and hides
both for `isFree` and POC quotas. The central billing console's resource
auto-renew, accepted in [prepaid resource auto-renew](auto-renew.md), is a
different endpoint and is not used here.

## Support matrix

Each row is one operation on one kind. A row's implementation brief needs
raw response evidence (status and sanitized body) and settlement evidence
(the reads that followed, with timings) for that exact row. Evidence for
one row never clears another: a refused Email enable says nothing about a
Synthetic enable, a disable, or SMS, and a disposable SMS delete says
nothing about a Synthetic delete.

| Operation | Email | SMS | Synthetic |
|-|-|-|-|
| Enable auto-renew | console; live check 1 | console | console; live check 1 |
| Disable auto-renew | console; live check 1 | console | console; live check 1 |
| Delete | console; live check 2 on a disposable quota only | console; live check 2 | console; no disposable quota exists yet |
| Purge | console; live check 2 | console; live check 2 | console; none |

"console" means the wire shape is console code only and the row is not
implementable. A row becomes implementable when the API sibling records
its live response and settlement under a heading naming the row.

## Effects the wiki must state

Expected effects, stated as expectations until a row's live check verifies
them:

- Deleting the Synthetic Test quota is expected to stop every check from
  running until a quota exists again. Whether check configuration survives
  and whether the quota lands in trash are unverified; live check 2 cannot
  verify this row, so the wiki says so until a disposable synthetic quota
  exists.
- Deleting the Email or SMS quota is expected to stop the alerts that
  draw on that quota. An account whose Metric package grants a free
  allowance (`freeEmailQuota` or `freeSmsQuota` above 0) may keep sending
  from that allowance; the wiki tells users to read both sources.
- Whether a delete spends one of the three monthly notification actions
  is unknown and stays unknown until provider accounting shows it; message
  usage counters never stand in for it.
- Purge is expected to be final; a new order is then the only way back.

Verified effects are added per row from the live check's own reads, never
from console text or from another row.

## Frozen terms, record, and partial Output

Every lifecycle write freezes its terms from one read under the lock
before any quote or send: kind, quota ID, package ID, status, `EndTime`,
`AutoRenew`, `POC`, and for the synthetic quota the detail's API quantity;
for auto-renew also the target `Enabled` and period 1; for delete also
`Purge`. The unresolved-attempt record's identity is operation
(`set-auto-renew`, `delete`, or `purge`), kind, quota ID, and for
`set-auto-renew` the target `Enabled` and package. A delete with `Purge`
has two stages, each with its own record; the purge stage never opens
while the delete stage is unresolved.

An optional `ExpectedQuotaID` on every lifecycle Input is compared with the
read before send and refuses a mismatch with `ErrInvalidInput`. It narrows
the window in which a replacement quota could be acted on; the server
still has no conditional request, so a race between the final reread and
the send remains, and the Output's returned quota ID lets a caller detect
it after the fact. That comparison is retrospective detection, not
prevention.

Partial Outputs carry `Quota *Quota` (the frozen read), `Stage string`
(`prepared`, `sent`, `settled`, `purge-sent`, or `purged`), and for
auto-renew `Changed bool`, for delete `Purged bool`. `Stage` is the last
stage the SDK confirmed, never the one it attempted.

## Delete

`DeleteNotificationQuotaInput` has `Kind` (r), `Purge bool`, and
`ExpectedQuotaID`; `DeleteSyntheticQuotaInput` has the last two.

1. Refuse read-only profiles and check `Kind` and `ExpectedQuotaID` before
   any request.
2. Under the lock, read the quota and freeze the terms. A 404 returns the
   not-found sentinel. Refuse with `ErrInvalidInput` and no request when
   `AutoRenew` or `POC` is true: disable auto-renew first, and a POC
   quota's deletion semantics are unknown.
3. Immediately before send, reread and require the same quota ID, package,
   status, and flags; any change aborts without a send.
4. Send `DELETE {kind}/quota` once with `Once`, accepting only the recorded
   statuses. A 4xx or a failed dial returns that error; any other failure
   opens the delete record and returns `dns.ErrNotSettled` at `Stage`
   `sent`.
5. Settle by reading every 2 seconds for up to 60 seconds. The only
   evidenced outcomes are: the kind read returns 404, or it returns the
   same quota ID with status `IN-TRASH`. Any other change, such as an
   intermediate or error status, a different quota ID, or a different
   package, is not settlement; polling continues to the bound and then
   returns `dns.ErrNotSettled` with the partial Output. Settlement sets
   `Stage` `settled` and closes the delete record.
6. With `Purge`, after settlement read `GET trash/quotas` once the live
   check has established its item shape and that it lists the frozen quota
   ID. If the listing lacks that ID (a free quota can leave trash on its
   own), return `Purged: false` at `Stage` `settled` with no purge sent.
   Otherwise send `DELETE trash/{kind}/quota` once with `Once`, open the
   purge record, then read the trash list every 2 seconds for up to 60
   seconds until the ID is absent: `Purged: true`, `Stage` `purged`. A
   purge 404 or any other non-accepted answer while the ID was listed is
   uncertain, not tolerated: `dns.ErrNotSettled` at `Stage` `purge-sent`.

Reconciliation of an open record, by reads only: a delete record resolves
to settled on either evidenced outcome, to rejected when the read shows
the same quota unchanged (same ID, status, `UpdatedAt`) after the bound,
and otherwise stays open. A purge record resolves to purged when the trash
list lacks the ID and stays open while it lists it; because the purge
target is bound by ID and the call is idempotent, a matching call whose
trash read still lists the ID may send one new purge. That is the only
resend any quota write permits.

The CLI commands `delete-notification-quota` and `delete-synthetic-quota`
are Write and Destructive: `--yes` is required, `--purge` adds the second
stage, `--expected-quota-id` binds the target, and a read-only profile
refuses them before any request. Never retry after an ambiguous outcome;
the recovery is a read.

## Auto-renew

`SetNotificationQuotaAutoRenewInput` has `Kind` (r), `Enabled *bool` (r),
`MaxPrice float64`, and `ExpectedQuotaID`; `SetSyntheticQuotaAutoRenewInput`
has the last three. The period is always 1 month.

1. Refuse read-only profiles; check `Kind`, a nil `Enabled`, a finite
   nonnegative `MaxPrice`, and `ExpectedQuotaID` before any request.
2. Under the lock, read the quota and freeze the terms (404 is not-found).
   When `AutoRenew` already equals the target and, for enable,
   `AutoRenewPeriod` is 1, return `Changed: false` and send nothing.
3. For enable, quote the renewal from the frozen terms through the
   parent's renewal quote mapping and apply the price guard: the higher of
   quote and list price must not exceed `MaxPrice`, and a zero quote for a
   priced package is `ErrUnpriced`. A free quota quotes 0 and passes at the
   default 0. Disable needs no quote and works during a pricing outage.
4. Eligibility differs from renewal on purpose: a free or POC quota is not
   refused. The console hides the control there, so the server's answer is
   unknown; the SDK sends the request and returns the server's answer, and
   the CLI prints a warning before sending on a free or POC quota that the
   setting may be refused or ignored. This is the owner's chosen behavior.
   Every other check (identity, package, quantity, status, guard) applies
   to free and POC quotas unchanged.
5. Immediately before send, reread and require the same quota ID, package,
   status, `POC`, and for synthetic the same API quantity; for enable also
   that `AutoRenew` is still false. A package or quantity change during
   quoting aborts without send, since the guard priced different terms.
6. Send `POST {kind}/quota/{id}/update-auto-renew` once with `Once`. The
   path ID is the frozen quota ID after `core.CheckPathID`; the body
   repeats it as `resourceId`. Accept only the recorded statuses. A 4xx or a
   failed dial returns that error; any other failure opens the record and
   returns `dns.ErrNotSettled` at `Stage` `sent`.
7. Confirm by reading every 2 seconds for up to 30 seconds until the read
   shows the same quota ID and package with the target: enabled means
   `AutoRenew` true with `AutoRenewPeriod` 1, disabled means `AutoRenew`
   false. A different quota ID or package never confirms, whatever its
   setting. The bound returns `dns.ErrNotSettled`. A 2xx that never shows
   the target is reported, not retried; on a free quota that is how the
   SDK learns the server ignores the setting.

Reconciliation of an open auto-renew record: the same quota and package
showing the target resolves it as applied; the same quota showing the
opposite with `UpdatedAt` unchanged from the frozen read resolves it as
not applied and returns the original error; anything else stays open. An
opposing-setting call while a record is open reconciles first and, per the
parent rule, returns `ErrInvalidInput` naming the prior attempt once it
resolves; the caller reruns the opposing call deliberately.

The CLI commands `set-notification-quota-auto-renew` and
`set-synthetic-quota-auto-renew` are Write, not Destructive: the opposite
command undoes them, so no `--yes`. `--enabled` must be explicit
(`--enabled=true` or `--enabled=false`); `--max-price` guards enable only.

## Errors

| Case | Result | CLI code and exit |
|-|-|-|
| Bad `Kind`, nil `Enabled`, bad `MaxPrice`, mismatched `ExpectedQuotaID` | `ErrInvalidInput`, no request | `InvalidUsage`, 2 |
| No quota of that kind | Not-found sentinel | `NotFound`, 4 |
| Delete while auto-renew or POC is on | `ErrInvalidInput`, no request | `InvalidUsage`, 2 |
| Enable above the guard, or a zero quote for a priced package | `ErrPriceAboveMax`, `ErrUnpriced`, no request | `PriceAboveMax`, `Unpriced`, 1 |
| Sent but not settled, not confirmed, or purge uncertain | `dns.ErrNotSettled` with partial Output and `Stage` | `NotSettled`, 1 |
| Server refuses | The `*APIError` | 1, or 4 on 404 |

## Threat notes

- The delete has no ID, so a replacement quota created between the final
  reread and the send would be deleted instead. `ExpectedQuotaID`, the
  lock, and the reread narrow the window; the Output's quota ID detects
  the case afterwards. No server precondition exists to prevent it.
- Purge is irreversible, is sent only against a trash entry bound to the
  frozen ID, and never follows an unsettled delete.
- Auto-renew enable on a paid quota is a future-charge consent; the guard
  runs on the frozen terms before every enable, the reread aborts on a
  package or quantity change, and disable never quotes.
- Every write uses `Once`, carries no secret, and exposes no server message
  text beyond the fixed error codes.

## Deterministic checks

- Delete: read-first with the 404 case; refusal on auto-renew, POC, or an
  `ExpectedQuotaID` mismatch with no request; abort on a changed reread;
  one `DELETE` under each recorded status, 502, dropped connection, and
  307; settle on 404 and on same-ID `IN-TRASH`; an ERROR or intermediate
  status, a different ID, or a different package never settles; the
  60-second bound; `Purge` sent only after settlement and a trash listing
  with the frozen ID, skipped with `Purged: false` when absent, never after
  an unsettled delete; purge 404 while listed is `NotSettled` at
  `purge-sent`; one permitted purge resend on a matching call while still
  listed; `--yes` and read-only refusal; `Stage` values in partial Output.
- Auto-renew: nil `Enabled` refused; no-op at target; enable quotes and
  guards on the frozen terms, disable sends no quote; free and POC quotas
  proceed with the warning; reread aborts on a package, quantity, status,
  or flag change; one `POST` under each recorded status, 502, dropped
  connection, and 307; confirm requires the same ID and package with the
  target and `AutoRenewPeriod`; a replacement quota at the target never
  confirms; the 30-second bound; reconciliation outcomes applied, not
  applied, and open; an opposing call while open returns `ErrInvalidInput`
  naming the attempt; path ID from the read; read-only refusal; `Changed`.
- Both: record identities include operation, kind, quota ID, and the
  operation's terms; a completed delete followed by an uncertain purge
  leaves `Stage` `purge-sent` and reconciles from the trash list only.

## Live checks

1. Auto-renew flip, after owner approval naming the quota, on the
   production free Synthetic Test or Email quota. Protocol: read the
   baseline and require `AutoRenew` false, `POC` false, status `ACTIVE`,
   else stop before any write. Send enable once and record status, body,
   and timings. Read until the setting shows or 30 seconds pass. If the
   read shows enabled, send disable once and read until it shows disabled;
   that restores the baseline. If the read shows disabled with `UpdatedAt`
   unchanged, the server did not act: record the refusal, send nothing
   more. If reads stay inconclusive, send no disable blind; report the
   quota ID and last read as unresolved, and the manager restores by hand
   in the console after reading it there. The same read protocol governs
   an ambiguous disable. This check evidences only the rows it ran.
2. Delete and purge need a disposable quota and separate owner approval:
   order a free quota of a kind the account does not use for production
   (for example `smsfree`), within the privately kept action ledger, read
   it, delete it, read the kind path and `trash/quotas` until the outcome
   is evidenced, purge it, and read the trash list again. Record each
   status, body, settle timing, and the trash item shape. This evidences
   the SMS delete and purge rows only; the Email and Synthetic delete rows
   stay console until a disposable quota of that kind exists. The
   production Email and Synthetic Test quotas are never deleted for a
   test.

## Release order

Both ship after the renewals: auto-renew first for the rows live check 1
evidenced, then delete and purge for the rows live check 2 evidenced.
Each is one feature release with its SDK operations, commands, wiki text,
and adversarial review; rows still marked console ship later or never.
