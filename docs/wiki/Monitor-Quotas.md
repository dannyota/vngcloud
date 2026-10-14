# Monitor Quotas

Read check alarm state, transition history, quota usage, notification
packages, Email and SMS quota reads and orders, and quota lifecycle through
`danny.vn/vngcloud/monitor`. Calls are per account and ignore the configured
region. Use the client from [Monitor Setup](Monitor.md#setup).
[CLI Monitor](CLI-Monitor.md) lists the command flags.

## Run state and alarm state

A check's `Status` is its `ENABLED` or `DISABLED` run state. Its
`MonitorStatus` is its alarm state: `OK`, `ALARM`, or `UNDETERMINED`.
Unknown alarm states pass through. `HistoryAlarmID` identifies the alarm
definition lookup, rather than a probe location's alarm.

`GetCheckAlarm` reads the current definition state and its location states.
An empty check alarm reference is `vngcloud.ErrNotFound`. An unknown
definition can return a server error 500 instead of NotFound.

```sh
vngcloud monitor get-check-alarm --check-id <check-id>
vngcloud monitor list-check-alarm-history --check-id <check-id>
```

`ListCheckAlarmHistory` accepts RFC 3339 strings for `Start` and `End`.
End defaults to now; Start defaults to 24 hours before the resolved End.
Invalid, empty, or reversed ranges at millisecond precision fail before
requests. Optional `LocationID` selects one location. The output includes
the resolved range and transitions sorted by timestamp, location, alarm,
and transition ID.

History contains only the records returned by the server. Paging and
truncation are unknown, and it supplies no state at the window start.
Empty history proves neither that no transition occurred nor that the
alarm exists. Use `GetCheckAlarm` for current state.

## Quota reads

```sh
vngcloud monitor get-notification-quota --kind Email
vngcloud monitor get-notification-quota --kind SMS
vngcloud monitor get-notification-quota-usage --kind Email
vngcloud monitor get-quota-usage
vngcloud monitor list-notification-packages --kind Email
vngcloud monitor list-notification-packages --kind SMS
vngcloud monitor get-synthetic-quota
vngcloud monitor get-synthetic-quota --query Quota.EndTime
vngcloud monitor get-synthetic-quota-usage
```

The SDK exposes `GetQuotaUsage`, `ListNotificationPackages`,
`GetSyntheticQuota`, `GetSyntheticQuotaUsage`, `GetNotificationQuota`, and
`GetNotificationQuotaUsage`. Notification methods require `Kind`; other
reads accept nil input. Package lists support Email and SMS. Notification
quota reads support both kinds. Usage reads support Email only; SMS usage
refuses locally before any request (exit 2). Kinds match case insensitively.

`GetNotificationQuota` returns quota identity, package, status, expiry, and
`AutoRenew`. `GetNotificationQuotaUsage` returns the message amount and its
description. A missing quota returns NotFound (CLI exit 4). Table and text
quota timestamps show UTC; JSON preserves SDK values.

`GetQuotaUsage` returns composite usage, free Email and SMS allowances,
and severity strings. `UnmodeledKinds` names nonempty usage arrays whose
item shapes are unknown; they do not contribute invented totals.
`GetSyntheticQuotaUsage` returns synthetic counters and their descriptions.

`ListNotificationPackages` returns package IDs, names, prices in VND,
and message amounts. A missing price means unknown, never free: table
and text views print `unknown`, and JSON prints `null`. A listed
zero-price package does not grant an allowance automatically.

`GetSyntheticQuota` returns quota identity, status, expiry, `AutoRenew`,
and package capacity and price. Table and text timestamps show UTC.
Synthetic quota expiry stops checks. A missing quota returns NotFound;
inspect the quota in the console. Synthetic ordering, renewal, and deletion
remain unavailable. Auto-renew enable sends once and reports the free
quota's server refusal as described below.

## Email allowances and consumption

Allowances depend on the account and package. The observed Basic synthetic
quota grants zero Email and SMS messages. An available zero-price
notification package advertises 20 messages. Ordering a package and
confirming delivery are separate steps.

Each alert email consumes Email quota usage; the observed synthetic
`email` counter stayed zero. On the tested account, with no Email quota
and zero free allowance, no alert email was sent until a free Email package
was ordered. Metric packages can grant a free allowance, so check both
`freeEmailQuota` in composite usage and the Email quota using the direct
reads above.

The 4 received alert emails, including the Up email, match Email usage 4.
Location history logged 3 transitions for those 4 emails; the difference
remains unexplained. An `update-check` URL change was followed by an extra
transition. OTP quota accounting remains unverified. Do not use transition
count as an exact billing counter.

## Notification quotes and orders

`QuoteCreateNotificationQuota` estimates an Email or SMS order without
purchasing. `CreateNotificationQuota` orders one month of capacity. Both
require `Kind: Email` or `Kind: SMS` and accept a `PackageID` from the list.
For Email, omitting the package selects the unique package with a present
zero price:
`emailfree`, 0 VND for 20 emails a month. Both refuse an existing quota.
SMS orders use a listed package ID and a sufficient `--max-price`; the
verified paid package costs 55000 VND on the package list.

```sh
vngcloud monitor quote-create-notification-quota --kind Email
vngcloud monitor quote-create-notification-quota --kind Email \
  --package-id vMonitor-Platform-Notification-email5k
vngcloud monitor quote-create-notification-quota --kind SMS \
  --package-id <sms-package-id>
```

```go
quote, err := client.QuoteCreateNotificationQuota(ctx,
    &monitor.CreateNotificationQuotaInput{
        Kind: monitor.NotificationKindEmail,
    })
if err != nil {
    log.Fatal(err)
}
log.Printf("%.0f VND before VAT", quote.GuardPrice)
```

The quote returns `OptimumPrice`, `ListPrice`, and their higher value,
`GuardPrice`, in VND for one month. Email prices are inferred to be before
value-added tax (VAT); SMS VAT treatment is unverified. An actual debit can
exceed the estimate. A quote grants no purchase authority. `MaxPrice` and
`NoWait` do not affect quotes; the CLI
hides their flags on the quote command and ignores those JSON fields.

Order consent is a finite nonnegative `MaxPrice`, CLI `--max-price`,
default 0. No `--yes` is required. Paid packages can be ordered when the
budget covers the higher of list and quote prices. `PriceAboveMax` refuses
an over-budget order; `Unpriced` refuses a zero quote on a paid package.
Both exit 1. Missing or invalid prices refuse rather than imply free.

Orders send `pay: true`, which may debit the balance. Before a
positive-budget call, the CLI prints a warning on stderr: `MaxPrice` is a
local guard, not a server cap. There is no server price lock, and prices or
state can change after preparation. Paid SMS order and settlement responses
were verified; paid Email ordering remains unverified. Read-only flag,
environment, and profile settings refuse orders before any network call.
Quotes remain available under read-only.

Without `NoWait`, the SDK waits up to 120 seconds, bounded by the caller's
context, for the selected package to become ACTIVE. `NoWait`, CLI
`--no-wait`, returns an unconfirmed output with `Quota` nil. `OrderID` can
be empty even after a successful order. An uncertain send, failed wait, or
pending payment returns partial output with `NotSettled` (exit 1), taking
precedence over wrapped cancellation, authentication, or NotFound errors.
The CLI prints that partial output even when an error follows and ignores
`--query` on that error path so recovery information survives.

Save the attempt and baseline before an order. After an uncertain outcome,
reconcile using read-only quota identity, package, status, and expiry, and
inspect billing in the console. A stale 404 or an unchanged quota does not
prove that no order happened and never authorizes retry. If reads remain
inconclusive, resolve the outcome with the owner or provider. Never start
a fresh client to bypass an unresolved attempt.

The SDK serializes writes and retains unresolved attempts, including
`NoWait`, within one client. Separate clients and processes share no lock
or attempt record. Serialize writes per account and kind across processes;
this is not an exactly-once guarantee.

Monthly notification action accounting is unknown. A setting and an
observed conflict suggest a limit of three actions a month, but whether
orders, renewal, recovery, deletion, failed attempts, or shared Email/SMS
activity consume it is unverified. Reserve an action per mutation attempt,
assume a shared budget of three, and stop when the remaining budget is
unknown. Failures must not trigger automatic retries.

Free quota renewal follows the console's last 7 days rule: renew only in
the last 7 days before expiry. Renewal arrives in a later release; there is
no SDK or CLI renewal command yet. An early free-quota renewal returns a
visible `Conflict` API error (409, exit 1), with no state change. This error
is not suppressed or retried.

## Auto-renew

`set-notification-quota-auto-renew` disables Email or SMS auto-renew with
explicit `--enabled=false`. Enable refuses locally before any request
(exit 2), because the notification renewal quote remains unverified.
Disable needs no quote and no `--yes`.

`set-synthetic-quota-auto-renew` requires explicit `--enabled=true` and a
finite nonnegative `--max-price` (default 0). It quotes and guards enable,
then sends once. The free Synthetic Test quota cannot auto-renew: the
server returns a visible `BadRequest` API error (400, exit 1), with no state
change. The free Email quota is expected to return the same refusal if
notification enable becomes available. POC quota behavior is unverified.
The CLI warns about free and POC quotas before the synthetic call. Synthetic
disable remains unavailable.

Both commands support `--expected-quota-id` and refuse read-only flag,
environment, or profile settings before requests. An uncertain result
prints partial output, including `Stage` and the intended quota, even with
an error. Reconcile with reads and the console; never retry automatically.

## Delete

`delete-notification-quota` supports Email and SMS, requires `--yes`, and
refuses read-only settings before requests. The quota must be ACTIVE with
auto-renew and POC off; disable auto-renew first. Synthetic delete and
purge remain unavailable.

The active SMS quota was removed outright, with a subsequent NotFound read
and no trash entry. Email delete follows the same supported behavior.
Deleting either notification quota is expected to stop alerts that draw on
it; alert delivery after deletion was not verified. Metric packages may
still grant a free allowance. Read `freeEmailQuota` and `freeSmsQuota` in
composite usage as well as the direct quota reads.

A deleted quota cannot be recovered by these commands. A replacement order
requires a separate deliberate purchase. Whether deletion spends a monthly
notification action remains unknown.

`--expected-quota-id` checks the intended snapshot before sending. The
server delete names only the kind, with no ID or conditional guard, so a
replacement created between the final read and delete can still be affected.
The output quota identifies the intended target, not proof of the affected
resource. `Stage` records the last confirmed stage; partial output survives
errors and ignores `--query`. An uncertain result is `NotSettled` (exit 1).
Use read-only reconciliation and console billing, without automatic retries.
