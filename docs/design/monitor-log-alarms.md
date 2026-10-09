# vMonitor Log Alarms Design

Status: Accepted (2026-09-27). The owner approved every item under
[Owner decisions](#owner-decisions).

This design adds log alarm writes to the SDK and CLI: `CreateLogAlarm`,
`UpdateLogAlarm`, and `DeleteLogAlarm`, and the alarm read model they need.
aboutme needs an alarm on its logs. It extends [vMonitor
Alerts](monitor-alerts.md), which owns channels, log projects, and alarm
reads, and follows [ADR 0002](../adr/0002-write-api-conventions.md).

## Source

The calls and bodies below come from the vMonitor console's public
JavaScript, read on 2026-09-27. The read shape below was confirmed live on
2026-10-09; the write bodies are from the console source. Paths
are under `vmonitor-api/api/v1` on the `Monitor` endpoint root.

| Call | Method and path |
|-|-|
| Create | `POST /alarms/logs` |
| Update | `PUT /alarms/logs/{id}` |
| Delete | `DELETE /alarms/logs/{id}` |
| Get | `GET /alarms/{id}`, result in `data` |
| Status | `GET /alarms/logs/butler/{id}/status`, result in `data` |

The console ignores the create, update, and delete responses, so their
shapes are unseen.

### Create body

| Wire field | What the console sends |
|-|-|
| `name` | Form value |
| `description` | Form value, `""` when empty |
| `severity` | `LOW` (default), `MEDIUM`, or `HIGH` |
| `logProjectId` | The chosen project's ID |
| `projectName` | That project's `name`, from the log project list |
| `zone` | Always `""` (see below) |
| `queryString` | The log search text |
| `logSearchQuery` | A JSON string of the search box tokens; `"[]"` in the text editor mode |
| `filter` | A search query object built from `queryString` |
| `thresholdType` | `frequency` (default), `flatline`, or `metric_aggregation` |
| `condition` | `gt`, `gte`, `lt`, or `lte`; `gt` by default, `lt` for `flatline` |
| `thresholdValue` | Form value: a whole number for `frequency` and `flatline`, a signed decimal for `metric_aggregation` |
| `timeFrame` | Minutes, default 5 |
| `groupByField` | One field name or `null`; `null` for `metric_aggregation` |
| `metricAggKey`, `metricAggType` | Only for `metric_aggregation`: a field, and `cardinality`, `sum`, `avg`, `min`, `max`, or `percentiles` |
| `reason` | Display text, below |
| `inAlarm`, `ok` | Channel IDs, each followed by a comma; `""` for none |
| `undetermined` | `""` for a log alarm |
| `resendEnabled` | `false` by default |
| `resendPeriod` | Minutes, 10 to 1440, default 30 |
| `resendTimes` | 0 to 10, default 0 |
| `resendStatus` | `OK`, `ALARM`, `UNDETERMINED` joined by commas; default `ALARM` |

`zone` is not a project field. The console's log zone list is a constant
holding one zone whose `id` is `""`, and it sends that `id`. So the only
wire field the project read supplies is `projectName`.

`filter` is `{"type":"bool","value":{"filter":[],"should":[],"must":[...],
"mustNot":[]}}`. The console fills `must` from `queryString` with its own
query parser, then clears the time range from `filter`.

`reason` is `query(<queryString>) <op><thresholdValue>`, or
`<metricAggType>[<metricAggKey>](<queryString>) <op><thresholdValue>` for
`metric_aggregation`. `<op>` is `>`, ` >= `, `<`, or ` <= ` for `gt`,
`gte`, `lt`, and `lte`.

The console checks `name` against
`^[a-zA-Z](?:[a-zA-Z0-9-_.@]){3,48}[a-zA-Z0-9]$` and offers only projects
whose `billingStatus` is `ACTIVE`. It does not convert `thresholdValue` or
`timeFrame` to numbers, so it may send them as strings.

### Read shape

Live read on 2026-10-09: Get's `data` and a List item have `id`, `type`
(`Log`; the SDK matches `LOG` and `METRIC` in any case), `name`,
`description`, `severity`, `progressStatus` (`ACTIVE`; the SDK also reads
`status` when `progressStatus` is absent; Get returns `""` right after
create, which the SDK treats as settled), `createdAt`, and, for a log
alarm, `alarmLog`. The Delete response's `data` has the same shape. List items carry
the same `alarmLog`. `alarmLog` holds the log fields under the create body
names, except `logProject` (the project ID) and `logProjectName`. It also
holds `id`, `zone`, `inAlarm` and `ok` (comma-joined channel IDs with a
trailing comma, `""` for none), and the resend fields once set.
`logSearchQuery` is a string holding JSON, such as `"[]"`.

The console edits a log alarm by sending the read `alarmLog`, with the
edited fields replaced and `name`, `description`, and `severity` from the
top level. It disables edits while `status` is `CREATING` or `UPDATING`.
The detail page reads the Status call instead: `data.status`, shown as
`CREATING` when null, and `data.updated_on` in epoch seconds.

## Non-goals

Metric alarm writes, alarm history, and the Status call. The console's
query parser: the SDK does not build `filter` from `queryString`.

## Decisions

| Topic | Decision |
|-|-|
| Project read | `GetLogProject` supplies `projectName`; `zone` is `""` |
| Filter | `QueryString` and `Filter` come together or not at all; neither sends a match-all alarm |
| Duplicate names | Create refuses a name a log alarm already has |
| Create | One `POST`, no retry after a 5xx; then the wait |
| Update | Read, merge the set fields, send the full body |
| Delete | One `DELETE`, no wait, `--yes` in the CLI |

## SDK

"(r)" marks `vngcloud:"required"` and "*" a pointer field sent only when
set (ADR 0002 rule 3).

### Read model

`Alarm` gains `Description`. `Kind` comes from `type` (`Log` or `LOG`
gives `AlarmKindLog`, `Metric` or `METRIC` gives `AlarmKindMetric`), and
`Status` from `progressStatus`, so `GetAlarm` sets it
too; `ListAlarms` still sets it from its filter. `Log` decodes from
`alarmLog`; a response without `alarmLog` keeps today's top-level
`inAlarm` and `ok` decode. `LogAlarmDetail` gains `ID` (`alarmLog.id`), `LogProjectID`
(`logProject`), `LogProjectName`, `QueryString`, `Filter`
(`json.RawMessage`, nil when absent), `ThresholdType`, `Condition`,
`ThresholdValue` (`float64`), `TimeFrame` (`int`), `GroupByField`,
`AggField` (`metricAggKey`), `AggType` (`metricAggType`), and `Resend`.
`ThresholdValue`, `TimeFrame`, `Resend.Period`, and `Resend.Times` accept
a JSON number or a numeric string, and read any other shape as 0;
`GroupByField` reads a non-string as empty. One odd alarm never fails a
list.

The read returns `condition` uppercase (`GT`) although the write accepts
only lowercase (`gt`) and refuses `GT` with a 400; `thresholdType` comes back
lowercase. `Condition` on the read model is the wire value as read.
`CreateLogAlarm` and `UpdateLogAlarm` lowercase the condition they send,
whether it comes from the caller or from the read.

`LogAlarmResend` has `Enabled`, `Statuses` (`[]string`, split from
`resendStatus`), `Period` (minutes), and `Times`. Constants name the
threshold types, the conditions, and the severities; each field is a plain
string, so a new value reaches the server unchanged.

### CreateLogAlarm

`CreateLogAlarmInput` has `Name` (r), `LogProjectID` (r), `ThresholdValue`
(r, `*float64`, so 0 is a value), `Description`, `Severity` (empty sends
`LOW`), `QueryString`, `Filter` (`json.RawMessage`), `ThresholdType` (empty
sends `frequency`), `Condition` (empty sends `lt` for `flatline`, else
`gt`), `TimeFrame` (0 sends 5), `GroupByField` (empty sends `null`),
`AggField`, `AggType`, `InAlarm` and `OK` (`[]string`), `Resend`
(`*LogAlarmResend`; nil sends the console defaults), and `NoWait`.

In order, with no request before step 4:

1. `core.CheckRequired`, and `core.CheckPathID` on `LogProjectID` and on
   each channel ID, which also keeps a comma out of the joined string. A
   NaN or infinite `ThresholdValue` returns `ErrInvalidInput`; update
   checks the same.
2. `QueryString` and `Filter` both set or both empty, else
   `ErrInvalidInput`. A set `Filter` must be a JSON object.
3. Build the body. Both empty sends `queryString` `*`, `logSearchQuery`
   `"[]"`, and `filter` with four empty lists, which matches every log. Set
   ones send `logSearchQuery` `"[]"` and both values unchanged. `zone` is
   `""`; `undetermined` is `""`; `metricAggKey` and `metricAggType` are sent
   only when set; `reason` follows the console's format.
4. List log alarms by name and scan every page for an exact match. A match
   returns `ErrInvalidInput` and creates nothing.
5. `GetLogProject`; its `name` becomes `projectName`. A 404 returns
   not-found and creates nothing.
6. Send the create once. After a 5xx or a failure with no response, the
   error says to list alarms by name before trying again.
7. Take the ID from the response (`data.id` or `id`) when present, else
   find the alarm by exact name, then wait. An accepted status with any
   other body, JSON or not, is not an error.

The value rules the console checks stay on the server (ADR 0002 rule 5).
The SDK does not check `billingStatus`.

`CreateLogAlarmOutput` has `AlarmID`, empty only with `NoWait` when the
response has no ID, and `Alarm`, set after the wait.

### UpdateLogAlarm

`UpdateLogAlarmInput` has `AlarmID` (r), `NoWait`, and each create field
but `NoWait` as a pointer: `Name`*, `Description`*, `Severity`*,
`LogProjectID`*, `QueryString`*, `Filter`*, `ThresholdType`*,
`Condition`*, `ThresholdValue`*, `TimeFrame`*, `GroupByField`*,
`AggField`*, `AggType`*, `InAlarm`* and `OK`* (`*[]string`; an empty list
clears), and `Resend`*.

It checks `AlarmID` and any new channel or project ID, reads the alarm,
and refuses with `ErrInvalidInput` when `Kind` is not `Log`, when the read
lacks a field the create always sends, or when `status` is `CREATING` or
`UPDATING`. `QueryString` and `Filter` must be set together or both left
unset; new values follow the create's pairing rule. It applies the set
fields to the read and builds the body with the create's builder. A new
`LogProjectID` gets `projectName` from `GetLogProject`; otherwise the read's
`LogProjectName` is sent. An unset `QueryString` keeps the read's
`logSearchQuery`. `reason` is rebuilt. It sends `PUT` with normal retries,
then waits.

The update body follows the console's own edit (live, 2026-10-09: the
create's body gets `500 Update alarm log is failed`). Beyond the create's
fields it carries `id` (the read's `alarmLog.id`, not the alarm's id; the
server answers 403 `you don't have permission on this resource` to the
alarm's id, so a read with no `alarmLog.id` returns `ErrInvalidInput`
before the PUT),
`logProject`, and `logProjectName`. It always carries `filter`: the read's
when it has one, else the console's match-all filter for `queryString`
(`LogAlarmDetail.ID` holds the id). It carries `resendStatus` (`ALARM` when
the read has none) and sends `resendEnabled`, `resendPeriod`, and
`resendTimes` only when the caller sets `Resend` or the read has resend
enabled. `metricAggKey` and `metricAggType` stay omitted when empty. The
create body is unchanged: it worked live without `filter` beyond its own
match-all.

### DeleteLogAlarm

`DeleteLogAlarmInput` has `AlarmID` (r). It checks the ID, reads the
alarm, and refuses with `ErrInvalidInput` unless `Kind` is `Log`, since
nothing shows the server refuses a metric alarm's ID on this path. It then
sends one `DELETE` with normal retries; a retry that finds the alarm gone
returns not-found. There is no wait: the console treats a success as done.

### Wait

Create and update poll `GetAlarm` every 2 seconds, up to 60 seconds, with
the injected clock, until `status` is neither `CREATING` nor `UPDATING`;
an empty `status` counts as settled. Before the ID is known, create polls
the list by exact name. The bound returns `ErrNotSettled` (the vDNS
sentinel): the write was accepted and must not be repeated. `NoWait`
returns at once.

### Retries

| Request | Rule |
|-|-|
| Reads, the name scan, `GetLogProject` | Normal retries |
| Create `POST` | No retry after a 5xx (ADR 0002 rule 2) |
| Update `PUT` | Normal retries: a full replace is safe to resend |
| Delete | Normal retries; a retry that finds it gone returns not-found |

## Errors

| Case | Result | CLI code and exit |
|-|-|-|
| Missing field, bad ID, unpaired query | `ErrInvalidInput`, no request | `InvalidUsage`, 2 |
| Name taken, or a metric alarm to update | `ErrInvalidInput`, no write | `InvalidUsage`, 2 |
| No such project or alarm | Not-found sentinel | `NotFound`, 4 |
| Wait timed out | `ErrNotSettled` | `NotSettled`, 1 |
| Server refuses | The server's `*APIError` | 1, or 4 on 404 |

No new sentinel or CLI code is added.

## CLI

| Command | Kind | `--yes` |
|-|-|-|
| `monitor create-log-alarm` | Write | No |
| `monitor update-log-alarm` | Write | No |
| `monitor delete-log-alarm` | Write, destructive | Yes |

- Scalar fields are flags: `--name`, `--log-project-id`,
  `--threshold-value`, `--query-string`, `--time-frame`, and the rest.
  `Filter`, `InAlarm`, `OK`, and `Resend` come through `--cli-input-json`.
- `--threshold-value` uses the `float64` and `*float64` flag types
  ([CLI](cli.md#operation-table)).
- A read-only profile refuses all three. Waiting writes take `--no-wait`.
- Delete needs `--yes`: a deleted alarm's history is lost, and a new alarm
  has a new ID.

## Security

- Alarm bodies hold channel IDs, not addresses, and no secret. A query
  string may name personal data; `--debug` logs no body.
- The adversarial review checks: no resend of the create; the name scan
  and project read run before the create; path ID checks on every ID;
  unset update fields resend the read values unchanged; no update of a
  metric alarm; `--yes` on delete; read-only refusal of all three.

## Testing

Unit tests with `httptest` and the injected clock cover: the create body
with defaults and with every field; the match-all body; the pairing
refusal; joined channel IDs; `reason` for each type and condition; no
create after a taken name or a project 404; no retry after a 502; the ID
from the response and by name; each wait outcome; the update merge, a
project change, and the metric refusal; decode from `alarmLog`, the
top-level fallback, and a string `thresholdValue`; delete 404; and CLI
`--yes` and read-only refusal.

## Live check

This needs an `ACTIVE` log project. The Basic class allows 3 orders or
recoveries a month, and the test account has used them, so none can be
ordered until the limit resets. The release can be built and reviewed now;
its live check and tag wait for the reset.

With the owner's approval, on the test account, with an existing `ACTIVE` log
project named by `VNGCLOUD_LIVE_MONITOR_LOG_PROJECT_ID` (the test never orders
one) and a webhook channel it creates. Create a `frequency` alarm with the
match-all query, threshold 1000, and that channel in `inAlarm`. Record the
create response (does it hold the ID?), the get and list shapes (the `alarmLog`
keys, whether `filter` is there, the number types), and the statuses and their
timing. Update the threshold and name, read back, delete, and delete again.
Clean up the channel. Adjust the design to what the check shows before the tag.

## Release

One release: the read model change, the three writes, and their commands.
It adds fields and fills `GetAlarm`'s `Kind`, so no caller breaks.

## Owner decisions

1. Pair `QueryString` and `Filter`, with match-all when both are empty.
   This replaces the default `{"type":"match_all","value":{}}` in the
   accepted vMonitor Alerts design, which the console never sends.
   Options: (a) pair them; (b) port the console's query parser; (c)
   require both. Approved: (a). A parser port copies undocumented
   behavior, and a query string without its filter would show one query
   and evaluate another.
2. Refuse a create whose name a log alarm already has. Approved,
   as for log projects: the wait finds the alarm by name, and a repeated
   create would otherwise add a copy.
3. Wait after create and update on `status`, not on the Status call.
   Approved. The console shows a null Status call result as
   `CREATING`; if that lasts until the first evaluation, it can outlast
   any fair bound.
4. Build and review now; live check and tag after the monthly limit
   resets. Approved.

## Open questions

- Whether the server evaluates `filter` or `queryString`. The live check
  shows only the round trip; logs must ship to see an alarm fire.
- Whether `resendTimes` 0 means no resend or no limit.
