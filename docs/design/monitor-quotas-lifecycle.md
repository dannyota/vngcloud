# vMonitor quota lifecycle: delete and auto-renew

Status: Accepted as part of [check state and quotas](monitor-quotas.md);
each write here stays gated on its own live check. Nothing here approves a
live run.

Add quota deletion, purge from trash, and the auto-renew setting for the
Email, SMS, and Synthetic Test quotas. The parent design owns the shared
mechanics: frozen terms, the price guard, `Once`, the per-Client lock, the
unresolved-attempt record, and the wait rules. [API evidence and
checks](monitor-quotas-api.md) owns the console wire facts; this file owns
the rules, checks, and live checks for these two writes.

## Wire facts and unknowns

All paths are console code under `billing-api/v1` on the `Monitor` root;
none has been sent. Responses, success statuses, and error shapes are
unknown until the live checks below run.

| Write | Request | Unknown |
|-|-|-|
| Delete | `DELETE {email\|sms\|synthetic}/quota`, no body, no ID; targets the account's only quota of that kind | Response, whether it moves to trash or removes, whether it spends a monthly notification action |
| Purge | `DELETE trash/{kind}/quota`, no body, no ID | Response; whether a quota must be in trash first |
| Auto-renew | `POST {kind}/quota/{id}/update-auto-renew` with `{"resourceId":<id>,"enabled":<bool>,"period":1}` | Response; behavior on a free or POC quota, for which the console hides the control; the nonzero `autoRenew` read encoding |

The console hides renew and auto-renew for `isFree` and POC quotas and
shows auto-renew instead of renew for paid ones. The central billing
console's resource auto-renew, accepted in [prepaid resource
auto-renew](auto-renew.md), is a different endpoint and is not used here.

## Effects the wiki must state

- Deleting the Synthetic Test quota stops every check from running until a
  quota exists again; a check's configuration is not deleted by the quota
  call. Whether the quota goes to trash and can be recovered is unverified.
- Deleting the Email or SMS quota stops alert emails or messages at once;
  checks keep transitioning and the composite usage keeps counting nothing.
- A delete may spend one of the three monthly notification actions; this
  is unknown, so the SDK and wiki say it may, and the caller budgets for
  it.
- Purge is final: a purged quota cannot be recovered, and a new order is
  the only way back.
- Auto-renew on a paid quota authorizes repeated charges until disabled;
  `MaxPrice` is a local check of today's renewal estimate, not a cap the
  server keeps. On a free quota the server's behavior is unknown.

## Delete

`DeleteNotificationQuotaInput` has `Kind` (r) and `Purge bool`;
`DeleteSyntheticQuotaInput` has `Purge bool`. Both Outputs hold
`Quota *Quota` (the pre-delete read) and `Purged bool`.

1. Refuse read-only profiles and check `Kind` before any request.
2. Under the per-Client lock, read the quota. A 404 returns the not-found
   sentinel; the delete has no ID, so the read is the only way to name
   what is about to be deleted, and the Output returns that read.
3. Refuse with `ErrInvalidInput` and no request when the read shows
   `AutoRenew` true or `POC` true: disable auto-renew first, and a POC
   quota's deletion semantics are unknown.
4. Send `DELETE {kind}/quota` once with `Once`. Accept 200 or 204 and
   ignore the body. A 4xx or a failed dial returns that error; any other
   failure records an unresolved attempt keyed by operation, kind, and the
   read quota ID, and returns `dns.ErrNotSettled`.
5. Settle by reading every 2 seconds for up to 60 seconds until the read
   is 404 or shows a status other than the pre-delete one; the bound
   returns `dns.ErrNotSettled` with the partial Output.
6. With `Purge`, send `DELETE trash/{kind}/quota` once after the delete
   settles, tolerating a 404 from the purge alone. A purge is never sent
   without the delete in the same call; a purge after an unsettled delete
   is not sent.

The CLI commands `delete-notification-quota` and `delete-synthetic-quota`
are Write and Destructive: `--yes` is required, `--purge` adds the second
request, and a read-only profile refuses them before any request. Never
retry after an ambiguous outcome; the recovery is a read.

## Auto-renew

`SetNotificationQuotaAutoRenewInput` has `Kind` (r), `Enabled *bool` (r),
and `MaxPrice float64`; `SetSyntheticQuotaAutoRenewInput` has the last two.
The period is always 1 month. Both Outputs hold `Quota *Quota` and
`Changed bool`.

1. Refuse read-only profiles; check `Kind`, a nil `Enabled`, and a finite
   nonnegative `MaxPrice` before any request.
2. Under the lock, read the quota (404 is not-found). When `AutoRenew`
   already equals the target and, for enable, `AutoRenewPeriod` is 1,
   return `Changed: false` and send nothing.
3. For enable, quote the renewal through the parent's renewal quote mapping
   and apply the price guard: the higher of quote and list price must not
   exceed `MaxPrice`, and a zero quote for a priced package is
   `ErrUnpriced`. A free quota quotes 0 and passes at the default 0.
   Disable needs no quote and works during a pricing outage.
4. The console hides the control for free and POC quotas, so the server's
   answer there is unknown. The SDK sends the request anyway and returns
   the server's answer; the CLI prints a warning before sending on a free
   or POC quota that the setting may be refused or ignored. This is the
   owner's chosen behavior; refusing locally would hide a working path.
5. Send `POST {kind}/quota/{id}/update-auto-renew` once with `Once`. The
   path ID is the read's quota ID after `core.CheckPathID`; the body
   repeats it as `resourceId`. Accept any 2xx and ignore the body. A 4xx or
   a failed dial returns that error; any other failure records an
   unresolved attempt and returns `dns.ErrNotSettled`.
6. Confirm by reading every 2 seconds for up to 30 seconds until the read
   shows the target: enabled means `AutoRenew` true with `AutoRenewPeriod`
   1, disabled means `AutoRenew` false. The bound returns
   `dns.ErrNotSettled`. A server that answered 2xx but never shows the
   target is reported, not retried; on a free quota that is the expected
   way to learn the server ignores the setting.

The CLI commands `set-notification-quota-auto-renew` and
`set-synthetic-quota-auto-renew` are Write, not Destructive: the opposite
command undoes them, so no `--yes`. `--enabled` must be explicit
(`--enabled=true` or `--enabled=false`); `--max-price` guards enable only.

## Errors

| Case | Result | CLI code and exit |
|-|-|-|
| Bad `Kind`, nil `Enabled`, bad `MaxPrice` | `ErrInvalidInput`, no request | `InvalidUsage`, 2 |
| No quota of that kind | Not-found sentinel | `NotFound`, 4 |
| Delete while auto-renew or POC is on | `ErrInvalidInput`, no request | `InvalidUsage`, 2 |
| Enable above the guard, or a zero quote for a priced package | `ErrPriceAboveMax`, `ErrUnpriced`, no request | `PriceAboveMax`, `Unpriced`, 1 |
| Sent but not settled or not confirmed | `dns.ErrNotSettled` with partial Output | `NotSettled`, 1 |
| Server refuses | The `*APIError` | 1, or 4 on 404 |

## Threat notes

- The delete has no ID, so a stale read can delete a quota that replaced
  the one the caller saw. The pre-delete read, the lock, and the Output's
  returned quota make the target visible; a caller that needs certainty
  compares the Output's quota ID with its own read.
- Purge is irreversible and never runs alone or after an unsettled delete.
- Auto-renew enable on a paid quota is a future-charge consent; the guard
  runs on a fresh quote before every enable, and disable never quotes.
- Both writes use `Once`, carry no secret, and expose no server message
  text beyond the fixed error codes.

## Deterministic checks

- Delete: read-first with the 404 case; refusal on auto-renew or POC with
  no request; one `DELETE` on 200, 204, 502, dropped connection, and 307;
  settle on 404 and on a status change; the bound; `Purge` sent only after
  settle, tolerating its own 404, never after an unsettled delete; `--yes`
  and read-only refusal; `Purged` and the pre-delete quota in the Output.
- Auto-renew: nil `Enabled` refused; no-op when already at target; enable
  quotes and guards, disable sends no quote; the free-quota warning; one
  `POST` on 2xx, 502, dropped connection, and 307; confirm reads for both
  targets including `AutoRenewPeriod`; the 30-second bound; a 2xx with no
  confirmed change is `NotSettled`; path ID from the read, never from the
  caller; read-only refusal; `Changed` values.
- Both: the unresolved-attempt record and reconciliation rules from the
  parent apply, including a later delete or renewal on the same kind.

## Live checks

1. Auto-renew flip, after owner approval naming the quota: on the
   production free Synthetic Test or Email quota, enable auto-renew once,
   read the quota (this records the server's answer and the nonzero
   `autoRenew` encoding, or its refusal), then disable it at once and read
   again. Record statuses, bodies, and timings. Nothing is deleted and no
   renewal is ordered; the flip restores the original state in the same
   run. If enable is refused on a free quota, the auto-renew release ships
   with that refusal documented.
2. Delete and purge need a disposable quota and separate owner approval:
   order a free quota of a kind the account does not use for production
   (for example `smsfree`), within the monthly action budget, delete it,
   read it, purge it, and read again. Record whether the delete lands in
   trash, each response, the settle timing, and the composite usage
   before and after, which is the only available signal for a spent
   action. The production Email and Synthetic Test quotas are never
   deleted for a test.

## Release order

Both ship after the renewals: auto-renew first, because its live check is
cheap and restores state, then delete and purge once a disposable quota
has been approved and the live check has run. Each is one feature release
with its SDK operations, commands, wiki text, and adversarial review.
