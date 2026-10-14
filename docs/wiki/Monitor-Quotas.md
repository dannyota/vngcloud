# Monitor Quotas

Read check alarm state, transition history, quota usage, notification
packages, and synthetic quota expiry through
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
vngcloud monitor list-check-alarm-history --check-id <check-id> \
  --start 2026-01-01T00:00:00Z --end 2026-01-02T00:00:00Z
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
vngcloud monitor get-quota-usage
vngcloud monitor list-notification-packages --kind Email
vngcloud monitor list-notification-packages --kind SMS
vngcloud monitor get-synthetic-quota
vngcloud monitor get-synthetic-quota-usage
```

The SDK exposes `GetQuotaUsage`, `ListNotificationPackages`,
`GetSyntheticQuota`, and `GetSyntheticQuotaUsage`. All accept nil input
except `ListNotificationPackages`, which requires `Kind` Email or SMS,
case insensitively.

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
inspect the quota in the console. Ordering, renewal, deletion, and
auto-renew changes are not yet available in the SDK or CLI.

## Email allowances and consumption

Allowances depend on the account and package. The observed Basic synthetic
quota grants zero Email and SMS messages. An available zero-price
notification package advertises 20 messages. Ordering a package and
confirming delivery are separate steps.

Each alert email consumes Email quota usage; the observed synthetic
`email` counter stayed zero. On the tested account, with no Email quota
and zero free allowance, no alert email was sent until a free Email package
was ordered. Metric packages can grant a free allowance, so check both
`freeEmailQuota` in composite usage and the Email quota in the console.
Direct Email quota and usage reads are not yet available in the SDK or CLI.

Observed usage can exceed the transition count: usage was 4 for 3 observed
transitions, and the extra count remains unexplained. An `update-check`
URL change was followed by an extra transition. Only the In-alarm email
receipt was confirmed; Up receipts and OTP quota accounting remain
unverified. Do not use transition count as an exact billing counter.
