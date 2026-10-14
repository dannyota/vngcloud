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
(`set-auto-renew` or `delete`), kind, quota ID, and the intent: for
`set-auto-renew` the target `Enabled` and package, for `delete` the
`Purge` flag. A delete record carries a phase, `delete` or `purge`, and a
marker for the last phase completed. Completing the delete phase is not
retiring the record: with `Purge` true the record keeps its intent and the
completed phase through purge preparation (the trash read and selection)
until a terminal outcome retires it. Terminal outcomes are: `Purge` false
and the delete settled; `Purge` true and the purge confirmed; `Purge` true
and the trash listing lacking the frozen ID; a definite refusal at send
time; or authoritative provider evidence. An interruption after the delete
settles but before the first purge send (a failed trash read, an ambiguous
selection, a cancelled context) leaves the record at completed delete
phase, and a later call with the same identity and `Purge` true resumes at
the purge phase from a fresh trash read: it never returns not-found for
the quota's 404 and never resends the delete. A call whose intent differs
(`Purge` false after a `Purge` true record, or the reverse) follows the
parent's rule and returns `ErrInvalidInput` naming the prior attempt and
its phase.

An optional `ExpectedQuotaID` on every lifecycle Input is compared with the
quota ID right after the initial read, before any no-op return, quote, or
send, and again against an existing attempt record's quota ID before that
record is reconciled; a mismatch returns `ErrInvalidInput`. It narrows the
window in which a replacement quota could be acted on; the server has no
conditional request, so a race between the final reread and the send
remains. The delete and purge requests name no quota, and no lifecycle
response has been captured, so whether a response identifies the affected
quota is unverified and the actual target can remain unknown; the Output's
quota ID is the intended snapshot, and comparing it afterwards with the
SDK's own reads is retrospective detection, not proof or prevention.

Partial Outputs carry `Quota *Quota` (the frozen read, the intended
target, not proof of the affected resource), `Stage string` (`prepared`,
`sent`, `settled`, `purge-sent`, or `purged`), and for auto-renew
`Changed bool`, for delete `Purged bool`. `Stage` is the last stage the
SDK confirmed, never the one it attempted.

## Delete

`DeleteNotificationQuotaInput` has `Kind` (r), `Purge bool`, and
`ExpectedQuotaID`; `DeleteSyntheticQuotaInput` has the last two.

1. Refuse read-only profiles and check `Kind` and the shape of
   `ExpectedQuotaID` before any request.
2. Under the lock, read the quota and freeze the terms. A 404 returns the
   not-found sentinel. Compare `ExpectedQuotaID` at once. Refuse with
   `ErrInvalidInput` and no request when `AutoRenew` or `POC` is true:
   disable auto-renew first, and a POC quota's deletion semantics are
   unknown.
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
   `Stage` `settled` and marks the delete phase complete; with `Purge`
   false that retires the record, with `Purge` true the record stays.
6. With `Purge`, after settlement read `GET trash/quotas` once the live
   check has established its item shape. Trash membership is a client
   precondition only: the purge request names no quota, so it acts on
   whatever trash entry of that kind the server holds. Proceed only when
   the listing holds exactly one entry of that kind and its ID is the
   frozen quota ID. No such entry (a free quota can leave trash on its
   own) returns `Purged: false` at `Stage` `settled` with no purge sent;
   more than one entry of that kind, or one with another ID, is an
   ambiguous selection and returns `ErrInvalidInput` with no purge sent.
   Then send `DELETE trash/{kind}/quota` once with `Once`, move the record
   to stage `purge`, and read the trash list every 2 seconds for up to 60
   seconds until the frozen ID is absent: `Purged: true`, `Stage` `purged`.
   A purge 404 or any other non-accepted answer, or the bound, is
   `dns.ErrNotSettled` at `Stage` `purge-sent`. A second entry can still
   appear between the listing and the send; that kind-wide race has no
   server-side guard and the wiki says so.

Reconciliation of an open record, by reads only: a record in the delete
phase resolves to settled on either evidenced outcome; an unchanged read
(same ID, status, `UpdatedAt`) never proves the server did not act, so it
stays inconclusive, and the bound returns `dns.ErrNotSettled` again. A
record closes as rejected only on a definite refusal response at send time
or on authoritative provider evidence the manager records. A record with
the delete phase complete and `Purge` true resumes purge preparation on
the next matching call. A record in the purge phase resolves to purged
when the trash list lacks the frozen ID and stays open while it lists it;
no call resends the purge, per the parent's rule, and the recovery is the
console.

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
   nonnegative `MaxPrice`, and the shape of `ExpectedQuotaID` before any
   request.
2. Under the lock, read the quota and freeze the terms (404 is not-found)
   and compare `ExpectedQuotaID` at once. When `AutoRenew` already equals
   the target and, for enable, `AutoRenewPeriod` is 1, return `Changed:
   false` and send nothing. An enable on a quota already enabled with a
   period other than 1 is refused with `ErrInvalidInput` before any quote:
   the SDK does not correct a period it did not set, and the period's
   semantics for a quota are unverified. This is the chosen behavior over a
   guarded correction to period 1.
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
   status, `POC`, the same `AutoRenew` and `AutoRenewPeriod` as the frozen
   baseline for both enable and disable, and for synthetic the same API
   quantity. A package or quantity change during quoting aborts without
   send, since the guard priced different terms; a flag or period change
   aborts because another actor already changed the setting.
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
showing the target resolves it as applied. The same quota showing the
opposite, with or without an unchanged `UpdatedAt`, never proves the
server did not act, so it stays open and the bound returns
`dns.ErrNotSettled` again; only a definite refusal response at send time
or authoritative provider evidence closes it as rejected. An
opposing-setting call while a record is open reconciles first and, per
the parent rule, returns `ErrInvalidInput` naming the prior attempt once
it resolves, and `dns.ErrNotSettled` while it stays open; the caller
reruns the opposing call deliberately only after a resolution.

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
  lock, and the reread narrow the window; nothing in the SDK detects a
  replacement made inside it, since no affected-resource identity has
  been verified in any response. No server precondition prevents it.
- Purge is irreversible and names no quota: the trash listing is a client
  precondition, an ambiguous listing refuses, a second entry can still
  appear before the send, and purge never follows an unsettled delete or
  is resent.
- Auto-renew enable on a paid quota is a future-charge consent; the guard
  runs on the frozen terms before every enable, the reread aborts on a
  package or quantity change, and disable never quotes.
- Every write uses `Once`, carries no secret, and exposes no server message
  text beyond the fixed error codes.

## Deterministic checks

- Delete: read-first with the 404 case; `ExpectedQuotaID` compared right
  after the read and against an open record; refusal on auto-renew or POC
  with no request; abort on a changed reread; one `DELETE` under each
  recorded status, 502, dropped connection, and 307; settle on 404 and on
  same-ID `IN-TRASH`; an ERROR or intermediate status, a different ID, or a
  different package never settles; the 60-second bound; an unchanged read
  stays inconclusive and never closes the record as rejected; `Purge` sent
  only after settlement and a trash listing with exactly one entry of that
  kind bearing the frozen ID, skipped with `Purged: false` when absent,
  refused as ambiguous with two entries or another ID, never after an
  unsettled delete; purge 404 while listed is `NotSettled` at
  `purge-sent`; no purge resend on any later call; a matching `Purge` call
  continues from the reconciled stage while a changed `Purge` intent
  returns `ErrInvalidInput`; an interruption after the delete settles and
  before the first purge send (a failed trash read, an ambiguous listing,
  a cancelled context) keeps the record at completed delete phase, and the
  next matching `Purge` call resumes at purge preparation with a fresh
  trash read, returning neither not-found for the quota's 404 nor a second
  `DELETE`; each terminal outcome retires the record; `--yes` and read-only
  refusal; `Stage` values in partial Output.
- Auto-renew: nil `Enabled` refused; `ExpectedQuotaID` compared before the
  no-op return; no-op at target; enable on an already-enabled quota with a
  period other than 1 refused before any quote, while a disabled quota
  with period 0 proceeds through the normal guarded enable path; enable
  quotes and guards on the frozen terms, disable
  sends no quote; free and POC quotas proceed with the warning; reread
  aborts on a package, quantity, status, flag, or period change for both
  targets; one `POST` under each recorded status, 502, dropped connection,
  and 307; confirm requires the same ID and package with the target and
  `AutoRenewPeriod`; a replacement quota at the target never confirms; the
  30-second bound; reconciliation outcomes applied and open, with an
  opposite read never closing the record; an opposing call while open
  returns `NotSettled` and, once resolved, `ErrInvalidInput` naming the
  attempt; path ID from the read; read-only refusal; `Changed`.
- Both: record identities include operation, kind, quota ID, and intent;
  a completed delete followed by an uncertain purge leaves `Stage`
  `purge-sent` and reconciles from the trash list only; Output is the
  intended snapshot, never asserted as the affected resource.

## Live checks

1. Auto-renew flip, after owner approval naming the quota, on the
   production free Synthetic Test or Email quota. Protocol: read the
   baseline and require `AutoRenew` false, `POC` false, status `ACTIVE`,
   else stop before any write. Send enable once and record status, body,
   and timings. Read until the setting shows or 30 seconds pass. If the
   read shows enabled, send disable once and read until it shows disabled;
   that restores the baseline. A definite refusal response at send time
   records the refusal and sends nothing more. A read that still shows
   disabled, with or without an unchanged `UpdatedAt`, proves nothing:
   treat the enable as inconclusive, send no disable blind, report the
   quota ID and last read as unresolved, and let the manager read and, if
   needed, restore by hand in the console. The same rule governs an
   ambiguous disable. This check evidences only the rows it ran.
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
