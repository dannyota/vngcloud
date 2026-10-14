# vMonitor quota lifecycle: delete and auto-renew

Status: Accepted as part of [check state and quotas](monitor-quotas.md);
every path below ships only when its row of the support matrix is live or
covered. Nothing here approves a live run.

Add quota deletion, purge from trash, and the auto-renew setting for the
Email, SMS, and Synthetic Test quotas. The parent design owns the shared
mechanics: frozen terms, the price guard, `Once`, the per-Client lock, the
unresolved-attempt record, and the wait rules. [API evidence and
checks](monitor-quotas-api.md) owns the read, quote, order, and renewal
wire facts; this file owns the lifecycle wire evidence, the rules, the
support matrix for every operation, checks, and live checks.

## Wire facts and evidence

All paths are under `billing-api/v1` on the `Monitor` root. The SDK
accepts only the statuses recorded for a row; it never guesses one. The
owner-approved live checks below were run by the manager as the IAM User,
each write sent once, with the responses sanitized.

| Write | Request | Evidence |
|-|-|-|
| Auto-renew | `POST {kind}/quota/{id}/update-auto-renew` with `{"resourceId":<id>,"enabled":<bool>,"period":1}` | Live, paid SMS quota: enable and disable both 200 with an empty body; reads 3 s later showed `autoRenew` and `autoRenewPeriod` 1 and 1, then 0 and 0, with `updatedAt` moving. The enable was sent without the design's renewal quote: the v2 quote with `resourceId` was never sent for SMS, and the quota is now deleted. Live, free synthetic quota: 400 `Resource quota with free package is not available for auto-renew setting`, no state change. Email: covered by the SMS disable evidence under owner decision 11; the free Email quota is expected to answer enable with the same 400. POC quotas: unverified |
| Delete | `DELETE {email\|sms\|synthetic}/quota`, no body, no ID; targets the account's only quota of that kind | Live, ACTIVE paid SMS quota: 204 with an empty body, 404 `Not found sms quota` on the next read about 6 s later, and `trash/quotas` empty before and after. An active quota is removed outright with no trash entry. Email active quota: covered under owner decision 11. Synthetic and expired quotas: unverified |
| Purge | `DELETE trash/{kind}/quota`, no body, no ID | Console only. Designed for an expired quota that reached trash; whether expiry creates a trash entry, the item shape, and the purge response are unverified |

The console shows auto-renew instead of renew for paid quotas and hides
both for free and POC quotas; the server enforces the free case with the
400 above. The central billing console's resource auto-renew, accepted in
[prepaid resource auto-renew](auto-renew.md), is a different endpoint and
is not used here. The paid SMS run's net balance change was 0; the debit
and refund were not observed separately.

Action budget: five notification billing actions in one month (two
orders, two auto-renew changes, one delete) were not refused, but only two
were orders, so the three-a-month setting is neither confirmed nor ruled
out for any action type; what it counts stays unknown and is never
derived from message usage. Policy for later live probes: at most one
notification order or recovery per month beyond those already approved,
unless the owner approves more, with every attempt recorded privately.

## Support matrix

Each row is one operation on one kind. A row's implementation brief needs
raw response and settlement evidence for that exact row, recorded here or
in the API sibling, or coverage under owner decision 11 (decided in the
parent): SMS evidence covers Email auto-renew disable and Email delete of
an active quota, since the two kinds share one console path and request
bodies and their live responses match. Ordering is not covered; each order
was verified on its own. Otherwise evidence for one row never clears
another: an active delete says nothing about an expired one, and an
auto-renew response says nothing about the quote that precedes an enable.

| Operation | Email | SMS | Synthetic |
|-|-|-|-|
| Quota read | live | live | live |
| Usage read | live | console; refused until evidenced | live |
| Create quote and order | live (free) | live (paid) | not in scope |
| Enable auto-renew | deferred (decision 12); free-quota 400 expected | live response; deferred (decision 12) | live refusal (400 on the free quota) |
| Disable auto-renew | covered by SMS evidence (decision 11) | live | console; the free quota cannot enable |
| Delete, active quota | covered by SMS evidence (decision 11) | live | console; no disposable quota exists |
| Purge, expired quota | console | console | console |

"live" and "covered" rows ship; a "live refusal" ships as a documented
definite refusal with no state change; "console" rows have console code
only and are not implementable; a refused or deferred row returns
`ErrInvalidInput` naming it before any request. Notification enable is
deferred under owner decision 12, still proposed, until a live renewal
quote with `resourceId` exists.

## Effects the wiki must state

Verified effects come only from a row's own live reads:

- Deleting the active paid SMS quota removed it outright: the read was 404
  about 6 s later and no trash entry appeared, so there was nothing to
  purge. The wiki states this for SMS, and for Email under decision 11.
- Auto-renew on the free Synthetic Test quota is refused by the server
  with a 400; the wiki says the free synthetic quota cannot auto-renew and
  that the free Email quota is expected to answer the same.
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
  notification action is unknown.

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
that is not live or covered, and `Purge` on any row while the purge row
is console, returns `ErrInvalidInput` naming the row before any request.

1. Refuse read-only profiles and check `Kind` and the shape of
   `ExpectedQuotaID` before any request.
2. Under the lock, read the quota and freeze the terms. A 404 returns the
   not-found sentinel. Compare `ExpectedQuotaID` at once. Eligibility is
   status `ACTIVE` with `AutoRenew` false and `POC` false (the only
   evidenced row; disable auto-renew first; POC deletion is unknown). A
   known ineligible state (`CREATING`, `IN-TRASH`, `ERROR`, or a flag set)
   returns `ErrInvalidInput`; a status the SDK does not know returns the
   parent's `monitor.ErrUnexpectedStatus`. Neither sends anything.
3. Immediately before send, reread and require the same quota ID, package,
   status still `ACTIVE`, and the same flags; any change aborts without a
   send, with the same two error identities as step 2.
4. Send `DELETE {kind}/quota` once with `Once`, accepting 204 (the recorded
   status) with an empty body. A 4xx or a failed dial returns that error;
   any other failure opens the delete record and returns
   `dns.ErrNotSettled` at `Stage` `sent`.
5. Settle by reading every 2 seconds for up to 60 seconds. The evidenced
   outcome for an active quota is a 404 on the kind read (live: about
   6 s). Any other change, such as an intermediate or error status, a
   different quota ID, or a different package, is not settlement; polling
   continues to the bound and then returns `dns.ErrNotSettled` with the
   partial Output. Settlement sets `Stage` `settled` and marks the delete
   phase complete; with `Purge` false that retires the record, with `Purge`
   true the record stays.

Reconciliation of an open record, by reads only: a record in the delete
phase resolves to settled on the evidenced outcome; an unchanged read
(same ID, status, `UpdatedAt`) never proves the server did not act, so it
stays inconclusive, and the bound returns `dns.ErrNotSettled` again. A
record closes as rejected only on a definite refusal response at send time
or on authoritative provider evidence the manager records.

The CLI commands `delete-notification-quota` and `delete-synthetic-quota`
are Write and Destructive: `--yes` is required, `--purge` is accepted only
once the purge row is live, `--expected-quota-id` binds the target, and a
read-only profile refuses them before any request. Never retry after an
ambiguous outcome; the recovery is a read.

### Deferred: expired quotas and purge

This protocol ships with the expired-quota and purge rows, after their
live checks; the current release only proves that `Purge` sends nothing.

- Settlement for an expired quota is the same quota ID with status
  `IN-TRASH`, unverified.
- With `Purge`, after settlement read `GET trash/quotas` once the live
  check has established its item shape. Trash membership is a client
  precondition only: the purge request names no quota, so it acts on
  whatever trash entry of that kind the server holds. Proceed only when
  the listing holds exactly one entry of that kind and its ID is the
  frozen quota ID. No such entry returns `Purged: false` at `Stage`
  `settled` with no purge sent; more than one entry of that kind, or one
  with another ID, is an ambiguous selection and returns `ErrInvalidInput`
  with no purge sent. Then send `DELETE trash/{kind}/quota` once with
  `Once`, move the record to stage `purge`, and read the trash list every
  2 seconds for up to 60 seconds until the frozen ID is absent:
  `Purged: true`, `Stage` `purged`. A purge 404 or any other non-accepted
  answer, or the bound, is `dns.ErrNotSettled` at `Stage` `purge-sent`. A
  second entry can still appear between the listing and the send; that
  kind-wide race has no server-side guard and the wiki says so.
- A record with the delete phase complete and `Purge` true resumes purge
  preparation on the next matching call. A record in the purge phase
  resolves to purged when the trash list lacks the frozen ID and stays
  open while it lists it; no call resends the purge, per the parent's
  rule, and the recovery is the console.

## Auto-renew

`SetNotificationQuotaAutoRenewInput` has `Kind` (r), `Enabled *bool` (r),
`MaxPrice float64`, and `ExpectedQuotaID`; `SetSyntheticQuotaAutoRenewInput`
has the last three. The period is always 1 month. A row that is not live
or covered returns `ErrInvalidInput` naming it before any request; Email
and SMS enable are such rows until owner decision 12 is resolved with a
live renewal quote.

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
   No live notification renewal quote exists, which is why enable is
   deferred for Email and SMS.
4. Eligibility differs from renewal on purpose: a free or POC quota is not
   refused locally. The server refuses a free quota with the recorded 400;
   the SDK sends the request and returns the status-classified
   `BadRequest` `*APIError` naming the auto-renew operation, message
   withheld, no local code derived from message text, so an unrelated 400
   looks the same and the error stays visible as one to investigate. The
   wiki explains the free synthetic refusal and the CLI warns before
   sending on a free or POC quota; neither suppresses the error. No state
   changes and no record opens. This is the owner's send-and-report
   choice; every other check applies to free and POC quotas unchanged.
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
| Bad `Kind`, nil `Enabled`, bad `MaxPrice`, mismatched `ExpectedQuotaID`, or a row that is not live or covered | `ErrInvalidInput`, no request | `InvalidUsage`, 2 |
| No quota of that kind | Not-found sentinel | `NotFound`, 4 |
| Delete on a known ineligible state (`CREATING`, `IN-TRASH`, `ERROR`, auto-renew or POC on) | `ErrInvalidInput`, no request | `InvalidUsage`, 2 |
| Delete on a status the SDK does not know | `monitor.ErrUnexpectedStatus`, no request | `UnexpectedStatus`, 1 |
| Enable above the guard, or a zero quote for a priced package | `ErrPriceAboveMax`, `ErrUnpriced`, no request | `PriceAboveMax`, `Unpriced`, 1 |
| Free quota auto-renew refused by the server | `*APIError` status 400, code `BadRequest`, operation set | `BadRequest`, 1 |
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
  text beyond the status-classified codes.

## Deterministic checks

- Support: every row marked console or deferred, SMS usage read and both
  notification enables included, returns `ErrInvalidInput` naming the row
  with zero requests; the live and covered rows proceed for both kinds.
- Delete: `Purge` refused with zero requests; read-first with the 404
  case; `ExpectedQuotaID` compared right after the read and against an
  open record; zero mutation requests for every initial state other than
  `ACTIVE` with both flags false, with `CREATING`, `IN-TRASH`, `ERROR`,
  auto-renew on, and POC on returning `ErrInvalidInput` and an unknown
  status returning `monitor.ErrUnexpectedStatus`; abort on a changed
  reread with the same two identities, including a status that left
  `ACTIVE`; one `DELETE` under 204, 502, dropped connection, and 307;
  settle on 404 (live); an ERROR or intermediate status, a different ID,
  or a different package never settles; the 60-second bound; an unchanged
  read stays inconclusive and never closes the record as rejected; `--yes`
  and read-only refusal; `Stage` values in partial Output.
- Auto-renew: nil `Enabled` refused; `ExpectedQuotaID` compared before the
  no-op return; no-op at target; enable on an already-enabled quota with a
  period other than 1 refused before any quote, while a disabled quota
  with period 0 proceeds through the normal guarded enable path; enable
  quotes and guards on the frozen terms, disable sends no quote; the free
  synthetic refusal fixture decodes to a `BadRequest` `*APIError` with the
  operation set and the message withheld, no record opened, and no confirm
  reads, and an unrelated 400 body decodes the same way; a POC quota
  proceeds with the warning; reread aborts on a package, quantity, status,
  flag, or period change for both targets; one `POST` under 200, 502,
  dropped connection, and 307; confirm requires the same ID and package
  with the target and `AutoRenewPeriod` (1 and 1, or 0 and 0, per the SMS
  fixtures); a replacement quota at the target never confirms; the
  30-second bound; reconciliation outcomes applied and open, with an
  opposite read never closing the record; an opposing call while open
  returns `NotSettled` and, once resolved, `ErrInvalidInput` naming the
  attempt; path ID from the read; read-only refusal; `Changed`.
- Both: record identities include operation, kind, quota ID, and intent;
  Output is the intended snapshot, never asserted as the affected resource.

Deferred checks, implemented with the expired-quota and purge rows:
settlement on same-ID `IN-TRASH`; `Purge` sent only after settlement and a
trash listing with exactly one entry of that kind bearing the frozen ID,
skipped with `Purged: false` when absent, refused as ambiguous with two
entries or another ID, never after an unsettled delete; purge 404 while
listed as `NotSettled` at `purge-sent`; no purge resend on any later call;
a matching `Purge` call continuing from the reconciled stage while a
changed `Purge` intent returns `ErrInvalidInput`; an interruption after
the delete settles and before the first purge send (a failed trash read,
an ambiguous listing, a cancelled context) keeping the record at completed
delete phase, with the next matching `Purge` call resuming at purge
preparation from a fresh trash read, returning neither not-found for the
quota's 404 nor a second `DELETE`; each terminal outcome retiring the
record; a completed delete followed by an uncertain purge leaving `Stage`
`purge-sent` and reconciling from the trash list only.

## Live checks

1. Auto-renew flip, done. On the production free Synthetic Test quota the
   enable was refused with the 400 above and no disable was sent; on the
   disposable paid SMS quota the enable and disable both succeeded and
   read back within 3 s, the enable having been sent without the renewal
   quote the design requires. The protocol stands for any later row:
   validate the baseline (`AutoRenew` false, `POC` false, `ACTIVE`) before
   any write; a definite refusal records the refusal and sends nothing
   more; a read still showing the old value proves nothing, so send no
   restoring call blind, report the quota ID and last read as unresolved,
   and let the manager read and, if needed, restore by hand in the console.
2. Delete, done for an active SMS quota: ordered `sms5` under a 110000 VND
   cap, enabled and disabled auto-renew, deleted it, and read the kind
   path and `trash/quotas` until the 404 showed and trash stayed empty. No
   purge was sent because nothing reached trash. Still open: a notification
   renewal quote with `resourceId` for enable (decision 12, needing another
   paid purchase), synthetic delete (no disposable synthetic quota exists),
   and purge, which needs an expired quota that reached trash; a naturally
   lapsed, owner-approved quota is the only source, and production
   monitoring is never expired for it. Later probes follow the action
   budget policy above.

## Release order

The next release ships the live and covered rows: SMS create (with the SMS
quota read and quote in the parent), Email and SMS auto-renew disable,
Email and SMS delete of an active quota, and the synthetic auto-renew
refusal; the SMS usage read and both notification enables stay refused.
Renewals follow the in-window synthetic renewal. Enable, synthetic delete,
and purge ship later, each after its own live check, or never.
