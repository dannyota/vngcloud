# Monitor Log Alarms

Log alarm writes for `danny.vn/vngcloud/monitor`. See [Monitor
Alerts](Monitor-Alerts.md) for channels, log projects, and the read-only
`ListAlarms` and `GetAlarm` calls this page builds on.

This page assumes `cfg`, `ctx`, and `client := monitor.New(cfg)` from
[Monitor's Setup](Monitor.md#setup), and a `LogProjectID` from an existing,
`ACTIVE` log project (see [Ordering, deleting, and purging a log
project](Monitor-Alerts.md#ordering-deleting-and-purging-a-log-project)).

## Read model

`Alarm.Kind` decodes from the response's `type` (sent as `Log`, matched in
any case) and `Alarm.Status` from `progressStatus` (such as `ACTIVE`).
A Log alarm's `Alarm.Log` field decodes from the response's `alarmLog`
object and holds `LogProjectID` (wire key `logProject`), `LogProjectName`,
`QueryString`, `Filter` (`json.RawMessage`, nil when `alarmLog` carries no
`filter` key), `ThresholdType`, `Condition`, `ThresholdValue` (`float64`),
`TimeFrame` (`int`, minutes), `GroupByField`, `AggField` (wire
`metricAggKey`), `AggType` (wire `metricAggType`), `InAlarm` and `OK`
(`[]string`, split from a comma-joined wire string), and `Resend`.
`ThresholdValue` and `TimeFrame` accept a JSON number or a numeric string,
since the console's own create form sends either. A response with no
`alarmLog` at all still decodes `InAlarm` and `OK` from top-level `inAlarm`
and `ok` keys; every other field then stays zero.

A read returns `Condition` uppercase (`GT`). `CreateLogAlarm` and
`UpdateLogAlarm` lowercase the condition they send, so `GT` and `gt` both
send `gt`.

`LogAlarmResend` holds `Enabled`, `Statuses` (`[]string`, split from the
wire's comma-joined `resendStatus`, such as `"OK,ALARM"`), `Period`
(minutes), and `Times`.

Every string field is a plain Go string: `Severity`, `ThresholdType`, and
`Condition` have matching constants (`monitor.LogAlarmSeverityLow`,
`monitor.LogAlarmThresholdTypeFrequency`, `monitor.LogAlarmConditionGT`,
and so on), but a value the console adds later still reaches a caller, or
the server, unchanged.

## Creating a log alarm

```go
threshold := 1000.0
created, err := client.CreateLogAlarm(ctx, &monitor.CreateLogAlarmInput{
	Name:           "vngcloud-my-alarm",
	LogProjectID:   logProjectID,
	ThresholdValue: &threshold,
	InAlarm:        []string{channelID},
})
switch {
case errors.Is(err, dns.ErrNotSettled): // "danny.vn/vngcloud/dns"
	log.Printf("log alarm %s was accepted; check it later, do not create it again", created.AlarmID)
case err != nil:
	log.Fatal(err)
}
log.Println(created.Alarm.Status)
```

`Name`, `LogProjectID`, and `ThresholdValue` are required; `ThresholdValue`
is a `*float64` so `0` counts as a value distinct from leaving it unset.
Every other field takes the create body's own default when left at its
zero value: `Severity` empty sends `monitor.LogAlarmSeverityLow`,
`ThresholdType` empty sends `monitor.LogAlarmThresholdTypeFrequency`,
`Condition` empty sends `monitor.LogAlarmConditionLT` for a flatline
threshold or `monitor.LogAlarmConditionGT` otherwise, `TimeFrame` `0` sends
5 minutes, and `GroupByField` empty sends JSON `null`. `AggField` and
`AggType` are sent only when set, for a `metric_aggregation` threshold.
`Resend` left `nil` sends the console's own defaults (resend disabled,
`ALARM` only, every 30 minutes, no limit); a non-nil value is sent exactly
as given, with no range check, the same as every write in this SDK.

`QueryString` and `Filter` must both be set or both left empty; leaving
both empty sends a match-all query, matching every log the project holds.
A set `Filter` must be a JSON object. `InAlarm` and `OK` name channel IDs
that alert on entering and leaving the alarm state; every ID is checked
with `core.CheckPathID` before any request, since a comma in one would
otherwise land inside the joined wire string as an extra, unintended
channel ID.

Before any request, `CreateLogAlarm` lists log alarms by `Name` and
refuses with `vngcloud.ErrInvalidInput`, creating nothing, when one already
has that exact name: the wait below can only settle by name when the
create response carries no id, so a duplicate name risks settling on the
existing alarm instead of the one this call creates. It then reads the log
project with `GetLogProject` for its `Name`, which becomes the body's
`projectName`; a 404 there returns `vngcloud.IsNotFound(err) == true` and
creates nothing.

The create is a `POST` and, like `CreateLogProject`'s order, is never
retried after a failure that may have already reached the server: after
any error that is not a 4xx `*vngcloud.APIError`, the alarm may exist, and
the returned error says to list log alarms by `Name` before creating again.

Unless `NoWait` is set, `CreateLogAlarm` waits up to 120 seconds for the
alarm's `Status` to read `ACTIVE`, which takes about 30 seconds (the empty
status right after create and `CREATING` keep it polling; `ERROR` or
`FAILED` returns an error wrapping `dns.ErrFailed`): by `AlarmID` when the create response carried one,
else by listing log alarms for an exact `Name` match, the same way
`CreateLogProject`'s own wait works before an id is known. The wait
running out, or a read inside it failing, returns an error wrapping
`dns.ErrNotSettled`, the same sentinel [Monitor
Alerts](Monitor-Alerts.md#ordering-deleting-and-purging-a-log-project)
covers for log projects: the write was accepted and must not be sent
again. `NoWait` returns at once instead: `Output.AlarmID` is empty only
when the create response itself carried no id, and `Output.Alarm` stays
at its zero value.

## Updating a log alarm

```go
newThreshold := 2000.0
updated, err := client.UpdateLogAlarm(ctx, &monitor.UpdateLogAlarmInput{
	AlarmID:        created.AlarmID,
	ThresholdValue: &newThreshold,
})
if err != nil {
	log.Fatal(err)
}
log.Println(updated.Alarm.Status)
```

Every field but `AlarmID` is a pointer; a field left `nil` keeps the
alarm's current value. `UpdateLogAlarm` reads the alarm first with
`GetAlarm` and refuses with `vngcloud.ErrInvalidInput`, sending no `PUT`,
when its `Kind` is not `monitor.AlarmKindLog`; when its `Status` is
not `ACTIVE` (the server refuses an update with 403 until it is, so the
empty status right after create is refused too; retry once it reads
`ACTIVE`); or when the read carries no log alarm detail at all, or one
missing `LogProjectID`, `ThresholdType`, `Condition`, or a nonzero
`TimeFrame`, since a full-replace `PUT` built from that read would send
those fields blank. `LogProjectID`, when set, is re-read with
`GetLogProject` for a fresh `ProjectName`, even when it names the same
project the alarm already has. `InAlarm` and `OK` are `*[]string`; setting
either to a non-nil empty slice clears that channel list, the same
convention `UpdateChannel` uses for `Headers`.

`QueryString` and `Filter` must both be set or both left `nil`: setting
only one would pair a new value for it with the read's stale value for the
other, so `UpdateLogAlarm` refuses that before any request rather than
guess which value should win. A set pair follows `CreateLogAlarm`'s own
pairing rule; leaving both `nil` resends the read's exact pairing
unchanged, even if it was never valid to create in the first place, rather
than the SDK inventing a fix for state it was not asked to touch. Leaving
`QueryString` unset also resends the read's own internal query-editor
value unchanged, in case the alarm was built through the console's own
token-based search box rather than this SDK; setting `QueryString`
switches that value to the SDK's own plain-text convention.

The `PUT` body matches the console's own edit: it also carries the
alarm detail's own `id`, `logProject`, and `logProjectName`, and always a
`filter` (the read's, else a match-all filter for `QueryString`). It sends
`resendEnabled`, `resendPeriod`, and `resendTimes` only when you set
`Resend` or the alarm has resend enabled.

The `PUT` is a full replace and keeps the transport's normal retries,
since resending it is safe. `UpdateLogAlarm` waits the same way
`CreateLogAlarm` does, always by `AlarmID` since it is already known;
`NoWait` skips that wait and returns as soon as the `PUT` succeeds, with
`Output.Alarm` at its zero value.

## Deleting a log alarm

```go
if _, err := client.DeleteLogAlarm(ctx, &monitor.DeleteLogAlarmInput{AlarmID: created.AlarmID}); err != nil {
	log.Fatal(err)
}
```

`DeleteLogAlarm` reads the alarm first with `GetAlarm` and refuses with
`vngcloud.ErrInvalidInput`, deleting nothing, when its `Kind` is not
`monitor.AlarmKindLog`: this keeps the call from ever deleting a Metric
alarm by id. A 404 on that read returns `vngcloud.IsNotFound(err) == true`
directly. Past that read, there is no further wait: the console treats a
successful delete as done at once, and the alarm's history is lost with
it. `DELETE` keeps the transport's normal retries. The server answers a
repeat delete of a deleted alarm with 400 or 500, not 404, so when the
`DELETE` fails with 400 or a 5xx, the SDK lists log alarms: if the alarm is
absent it returns `vngcloud.IsNotFound(err) == true`, which a caller treats
as done, and if it is still listed it returns the server's error.

## What is unverified

No log alarm has been created, updated, or deleted live: the test account
has no alarm, and a log alarm needs a log project, which the account's
free-order quota did not allow while this page was written. Every body
shape above comes from the vMonitor console's own JavaScript, not a live
capture. In particular: whether the create, update, and delete responses
carry a body at all (the console itself ignores them); whether the server
evaluates `Filter` or `QueryString` when both are sent; and whether
`Resend.Times` `0` means no resend limit or none at all.
