# vMonitor quota lifecycle: delete and auto-renew

Status: Accepted as part of [check state and quotas](monitor-quotas.md);
every path below ships only when its row of the support matrix is live.
Nothing here approves a live run.

Add quota deletion, purge from trash, and the auto-renew setting for the
Email, SMS, and Synthetic Test quotas. The parent design owns the shared
mechanics: frozen terms, the price guard, `Once`, the per-Client lock, the
unresolved-attempt record, and the wait rules. [API evidence and
checks](monitor-quotas-api.md) owns the order and renewal wire facts; this
file owns the lifecycle wire evidence, the rules, the support matrix,
checks, and live checks.

## Wire facts and evidence

All paths are under `billing-api/v1` on the `Monitor` root. The SDK
accepts only the statuses recorded for a row; it never guesses one. The
owner-approved live checks below were run by the manager, each write sent
once, with the responses sanitized.

| Write | Request | Evidence |
|-|-|-|
| Auto-renew | `POST {kind}/quota/{id}/update-auto-renew` with `{"resourceId":<id>,"enabled":<bool>,"period":1}` | Live, paid SMS quota: enable and disable both 200 with an empty body; reads 3 s later showed `autoRenew` and `autoRenewPeriod` 1 and 1, then 0 and 0, which fixes the nonzero encoding as numeric 1 and `updatedAt` moving. Live, free synthetic quota: 400 `Resource quota with free package is not available for auto-renew setting`, no state change. Email and POC quotas: unverified |
| Delete | `DELETE {email\|sms\|synthetic}/quota`, no body, no ID; targets the account's only quota of that kind | Live, ACTIVE paid SMS quota: 204 with an empty body, 404 `Not found sms quota` on the next read about 6 s later, and `trash/quotas` empty before and after. An active quota is removed outright with no trash entry. Email, synthetic, and expired quotas: unverified |
| Purge | `DELETE trash/{kind}/quota`, no body, no ID | Console only. Designed for an expired quota that reached trash; whether expiry creates a trash entry, the item shape, and the purge response are unverified |

The console shows auto-renew instead of renew for paid quotas and hides
both for free and POC quotas; the server enforces the free case with the
400 above. The central billing console's resource auto-renew, accepted in
[prepaid resource auto-renew](auto-renew.md), is a different endpoint and
is not used here. The paid SMS run showed a net balance change of 0 after
the delete; whether the order debited and the delete refunded was not
observed. Five notification billing actions in one month (two orders, two
auto-renew changes, one delete) were not refused, so the three-a-month
setting is not treated as a likely limit for these actions; what it
counts stays unknown.

## Support matrix

Each row is one operation on one kind. A row's implementation brief needs
raw response evidence (status and sanitized body) and settlement evidence
(the reads that followed, with timings) for that exact row, recorded in
the table above. Evidence for one row never clears another, even on the
same API shape: the SMS rows say nothing about Email, and an active-quota
delete says nothing about an expired one.

| Operation | Email | SMS | Synthetic |
|-|-|-|-|
| Enable auto-renew | console | live | live refusal (400 on the free quota) |
| Disable auto-renew | console | live | console; the free quota cannot enable |
| Delete, active quota | console | live | console; no disposable quota exists |
| Purge, expired quota | console | console | console |

"live" means the row ships; "live refusal" means the row ships as a
documented definite refusal with no state change; "console" means the
wire shape is console code only and the row is not implementable. The
Email rows stay console under owner decision 11: the only Email quota on
the account is the production one, and deleting or flipping it to gather
evidence would cut alerts.

## Effects the wiki must state

Verified effects come only from a row's own live reads:

- Deleting the active paid SMS quota removed it outright: the read was 404
  about 6 s later and no trash entry appeared, so there was nothing to
  purge. The wiki states this for SMS only.
- Auto-renew on the free Synthetic Test quota is refused by the server
  with a 400; the wiki says free quotas cannot auto-renew.
- A free quota renews only in its last 7 days; the server refuses an
  earlier renewal with a 409 (parent design).

Expected effects, stated as expectations until their row is live:

- Deleting the Synthetic Test quota is expected to stop every check from
  running until a quota exists again; whether check configuration survives
  is unverified, and no disposable synthetic quota exists to test it.
- Deleting the Email quota is expected to stop the alerts that draw on it.
  An account whose Metric package grants a free allowance
  (`freeEmailQuota` or `freeSmsQuota` above 0) may keep sending from that
  allowance; the wiki tells users to read both sources.
- Purge is expected to be final for an expired quota in trash; a new order
  is then the only way back. Whether a delete spends a monthly
  notification action is unknown; five actions in a month were not
  refused, and message usage counters never stand in for the accounting.

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
remains. The delete and purge requests name no quota, and the recorded
responses carry no body, so no affected-resource identity has been
verified and the actual target can remain unknown; the Output's quota ID
is the intended snapshot, and comparing it afterwards with the SDK's own
reads is retrospective detection, not proof or prevention.

Partial Outputs carry `Quota *Quota` (the frozen read, the intended
target, not proof of the affected resource), `Stage string` (`prepared`,
`sent`, `settled`, `purge-sent`, or `purged`), and for auto-renew
`Changed bool`, for delete `Purged bool`. `Stage` is the last stage the
SDK confirmed, never the one it attempted.

## Delete

`DeleteNotificationQuotaInput` has `Kind` (r), `Purge bool`, and
`ExpectedQuotaID`; `DeleteSyntheticQuotaInput` has the last two. A row
that is not live, and `Purge` on any row while the purge row is console,
returns `ErrInvalidInput` naming the unsupported row before any request.

1. Refuse read-only profiles and check `Kind` and the shape of
   `ExpectedQuotaID` before any request.
2. Under the lock, read the quota and freeze the terms. A 404 returns the
   not-found sentinel. Compare `ExpectedQuotaID` at once. Refuse with
   `ErrInvalidInput` and no request when `AutoRenew` or `POC` is true:
   disable auto-renew first, and a POC quota's deletion semantics are
   unknown.
3. Immediately before send, reread and require the same quota ID, package,
   status, and flags; any change aborts without a send.
4. Send `DELETE {kind}/quota` once with `Once`, accepting 204 (the recorded
   status) with an empty body. A 4xx or a failed dial returns that error;
   any other failure opens the delete record and returns
   `dns.ErrNotSettled` at `Stage` `sent`.
5. Settle by reading every 2 seconds for up to 60 seconds. The evidenced
   outcome for an active quota is a 404 on the kind read (live: about
   6 s). The designed outcome for an expired quota is the same quota ID
   with status `IN-TRASH`; it is unverified and ships only with its row.
   Any other change, such as an intermediate or error status, a different
   quota ID, or a different package, is not settlement; polling continues
   to the bound and then returns `dns.ErrNotSettled` with the partial
   Output. Settlement sets `Stage` `settled` and marks the delete phase
   complete; with `Purge` false that retires the record, with `Purge` true
   the record stays.
6. With `Purge`, after settlement read `GET trash/quotas` once the live
   check has established its item shape. Trash membership is a client
   precondition only: the purge request names no quota, so it acts on
   whatever trash entry of that kind the server holds. Proceed only when
   the listing holds exactly one entry of that kind and its ID is the
   frozen quota ID. No such entry (an active quota leaves none, and a free
   quota can leave trash on its own) returns `Purged: false` at `Stage`
   `settled` with no purge sent; more than one entry of that kind, or one
   with another ID, is an ambiguous selection and returns `ErrInvalidInput`
   with no purge sent. Then send `DELETE trash/{kind}/quota` once with
   `Once`, move the record to stage `purge`, and read the trash list every
   2 seconds for up to 60 seconds until the frozen ID is absent:
   `Purged: true`, `Stage` `purged`. A purge 404 or any other non-accepted
   answer, or the bound, is `dns.ErrNotSettled` at `Stage` `purge-sent`. A
   second entry can still appear between the listing and the send; that
   kind-wide race has no server-side guard and the wiki says so.

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
has the last three. The period is always 1 month. A row that is not live
returns `ErrInvalidInput` naming the unsupported row before any request.

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
   refused locally. The server refuses a free quota with the recorded 400;
   the SDK sends the request and returns that refusal as an `*APIError`
   with the fixed local code `AutoRenewNotAvailable` (status and message
   matched against the recorded refusal, the message withheld), no state
   changes, and no record opens. The CLI names the code and exits 1. On a
   POC quota the answer is unverified, so the CLI prints a warning before
   sending that the setting may be refused or ignored. This is the owner's
   chosen send-and-report behavior. Every other check (identity, package,
   quantity, status, guard) applies to free and POC quotas unchanged.
5. Immediately before send, reread and require the same quota ID, package,
   status, `POC`, the same `AutoRenew` and `AutoRenewPeriod` as the frozen
   baseline for both enable and disable, and for synthetic the same API
   quantity. A package or quantity change during quoting aborts without
   send, since the guard priced different terms; a flag or period change
   aborts because another actor already changed the setting.
6. Send `POST {kind}/quota/{id}/update-auto-renew` once with `Once`. The
   path ID is the frozen quota ID after `core.CheckPathID`; the body
   repeats it as `resourceId`. Accept 200 (the recorded status) with an
   empty body. A 4xx or a failed dial returns that error; any other failure
   opens the record and returns `dns.ErrNotSettled` at `Stage` `sent`.
7. Confirm by reading every 2 seconds for up to 30 seconds until the read
   shows the same quota ID and package with the target: enabled means
   `AutoRenew` true with `AutoRenewPeriod` 1, disabled means `AutoRenew`
   false with `AutoRenewPeriod` 0 (live: both within 3 s). A different
   quota ID or package never confirms, whatever its setting. The bound
   returns `dns.ErrNotSettled`. A 200 that never shows the target is
   reported, not retried.

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
| Bad `Kind`, nil `Enabled`, bad `MaxPrice`, mismatched `ExpectedQuotaID`, or a row that is not live | `ErrInvalidInput`, no request | `InvalidUsage`, 2 |
| No quota of that kind | Not-found sentinel | `NotFound`, 4 |
| Delete while auto-renew or POC is on | `ErrInvalidInput`, no request | `InvalidUsage`, 2 |
| Enable above the guard, or a zero quote for a priced package | `ErrPriceAboveMax`, `ErrUnpriced`, no request | `PriceAboveMax`, `Unpriced`, 1 |
| Free quota auto-renew refused by the server | `*APIError`, code `AutoRenewNotAvailable` | `AutoRenewNotAvailable`, 1 |
| Sent but not settled, not confirmed, or purge uncertain | `dns.ErrNotSettled` with partial Output and `Stage` | `NotSettled`, 1 |
| Server refuses otherwise | The `*APIError` | 1, or 4 on 404 |

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

- Delete: an Email or synthetic row, and `Purge` on any row, refused as
  unsupported before any request; read-first with the 404 case;
  `ExpectedQuotaID` compared right after the read and against an open
  record; refusal on auto-renew or POC with no request; abort on a changed
  reread; one `DELETE` under 204, 502, dropped connection, and 307; settle
  on 404 (live) and, for the expired-row design, on same-ID `IN-TRASH`; an
  ERROR or intermediate status, a different ID, or a different package
  never settles; the 60-second bound; an unchanged read stays inconclusive
  and never closes the record as rejected; `Purge` sent only after
  settlement and a trash listing with exactly one entry of that kind
  bearing the frozen ID, skipped with `Purged: false` when absent, refused
  as ambiguous with two entries or another ID, never after an unsettled
  delete; purge 404 while listed is `NotSettled` at `purge-sent`; no purge
  resend on any later call; a matching `Purge` call continues from the
  reconciled stage while a changed `Purge` intent returns
  `ErrInvalidInput`; an interruption after the delete settles and before
  the first purge send (a failed trash read, an ambiguous listing, a
  cancelled context) keeps the record at completed delete phase, and the
  next matching `Purge` call resumes at purge preparation with a fresh
  trash read, returning neither not-found for the quota's 404 nor a second
  `DELETE`; each terminal outcome retires the record; `--yes` and read-only
  refusal; `Stage` values in partial Output.
- Auto-renew: an Email row refused as unsupported before any request; nil
  `Enabled` refused; `ExpectedQuotaID` compared before the no-op return;
  no-op at target; enable on an already-enabled quota with a period other
  than 1 refused before any quote, while a disabled quota with period 0
  proceeds through the normal guarded enable path; enable quotes and
  guards on the frozen terms, disable sends no quote; the free synthetic
  refusal fixture decodes to `AutoRenewNotAvailable` with the message
  withheld, no record opened, and no confirm reads; a POC quota proceeds
  with the warning; reread aborts on a package, quantity, status, flag, or
  period change for both targets; one `POST` under 200, 502, dropped
  connection, and 307; confirm requires the same ID and package with the
  target and `AutoRenewPeriod` (1 and 1, or 0 and 0, per the SMS fixtures);
  a replacement quota at the target never confirms; the 30-second bound;
  reconciliation outcomes applied and open, with an opposite read never
  closing the record; an opposing call while open returns `NotSettled` and,
  once resolved, `ErrInvalidInput` naming the attempt; path ID from the
  read; read-only refusal; `Changed`.
- Both: record identities include operation, kind, quota ID, and intent;
  a completed delete followed by an uncertain purge leaves `Stage`
  `purge-sent` and reconciles from the trash list only; Output is the
  intended snapshot, never asserted as the affected resource.

## Live checks

1. Auto-renew flip, done. On the production free Synthetic Test quota the
   enable was refused with the 400 above and no disable was sent; on the
   disposable paid SMS quota the enable and disable both succeeded and
   read back within 3 s. The protocol stands for any later row: validate
   the baseline (`AutoRenew` false, `POC` false, `ACTIVE`) before any
   write; a definite refusal records the refusal and sends nothing more; a
   read still showing the old value proves nothing, so send no restoring
   call blind, report the quota ID and last read as unresolved, and let
   the manager read and, if needed, restore by hand in the console.
2. Delete, done for an active SMS quota: ordered `sms5` under a 110000 VND
   cap, enabled and disabled auto-renew, deleted it, and read the kind
   path and `trash/quotas` until the 404 showed and trash stayed empty. No
   purge was sent because nothing reached trash. Still open: Email delete
   (gated under owner decision 11), synthetic delete (no disposable
   synthetic quota exists), and purge, which needs an expired quota that
   reached trash; a naturally lapsed, owner-approved quota is the only
   source, and production monitoring is never expired for it.

## Release order

The next release ships the live rows only: SMS auto-renew enable and
disable, SMS delete of an active quota, and the synthetic auto-renew
refusal, together with SMS enablement in the parent (reads, quote, and
order). Renewals follow the in-window synthetic renewal. Email rows,
synthetic delete, and purge ship later, each after its own live check, or
never.
