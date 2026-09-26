# vMonitor Alerts Design

Status: Accepted (2026-09-26).

This design adds vMonitor notification channels, check notifications, log
projects, and log alarms to the SDK and CLI. aboutme needs its app-down
check to alert someone, a log project for its logs, and alarms on those
logs. It extends [vMonitor](monitor.md) and follows [SDK and
CLI](sdk-and-cli.md) and [ADR 0002](../adr/0002-write-api-conventions.md).

## Source

No public API reference covers channels or alarms. The calls below come
from the vMonitor console's public JavaScript and from live reads on
2026-09-26 with an SDK-issued IAM User token and no extra header. Every
read returned 200, so the IAM User token works here as it does for checks.
No write was sent. All paths are under the existing `Monitor` endpoint
root, `https://vmonitor.console.greennode.ai/`:

| Prefix | Serves |
|-|-|
| `notification-gateway/api/v1` | Channels and channel types |
| `vmonitor-api/api/v1`, `/v2` | Alarms |
| `log-api/v1` | Log projects |
| `billing-api/v1`, `/v2` | vMonitor's own quotas, prices, and orders |
| `vmonitor-uptime-manager/v1` | Checks, as in [vMonitor](monitor.md) |

### Channels

GreenNode calls a channel a "notification". The SDK says "channel", so it
does not clash with a check's `notifications` field.

| Call | Method and path | Seen |
|-|-|-|
| List types | `GET /type/list` | 200: `lstData` of `{id, name, description}` |
| List | `GET /notification/list/typeSearch?searchtext=&field=&type=&page=1&size=10` | 200: `lstData`, `page`, `pageSize`, `totalItem`, `totalPage` |
| Create | `POST /notification` | Console code only |
| Update | `PUT /notification`, ID in the body | Console code only |
| Delete | `DELETE /notification/{id}` | Console code only |
| Send OTP | `POST /notification/otps` | Console code only |
| Validate OTP | `POST /notification/otps/validate` | Console code only |

There is no get-by-ID call. The types are `Email`, `Slack`, `SMS`,
`Teams`, `Telegram`, and `Webhook`; the console no longer offers Teams.
The test account has no channels, so the list item shape is unseen.

The create body is `name`, `type` (a type name), `address` (the email,
Slack webhook URL, Telegram chat ID, phone number, or webhook URL),
`header` (a JSON string of `[{"key", "value"}]` for webhooks, else `""`),
and `otpCode`. Update sends the same fields plus `id`.

Email, Slack, SMS, and Telegram need an OTP; webhooks do not. Send OTP
takes `{type, address, header}`, messages the address, and returns
`{ref, expiredAt}` (epoch milliseconds). Validate OTP takes
`{otp, address, ref, header}` and returns `{code}`, null for a wrong OTP.
Create carries that `code` as `otpCode`. An edit of an OTP channel repeats
the OTP steps with `id` added.

### Check notifications

A check body carries `notifications` with `In-alarm`, `Up`, and
`Undetermined` lists of channel IDs. The console edits a check with
`PUT /uptimes/{id}` and the full create body: the check update that
[vMonitor](monitor.md) listed as not discovered.

### Log projects

A log project is a vMonitor log quota: the order creates the project, and
both share one ID. That comes from a public third-party vMonitor MCP
server and is unverified.

| Call | Method and path | Seen |
|-|-|-|
| List | `GET log-api/v1/projects?page=0&size=10` | 200: `content`, `currentPage`, `pageSize`, `totalElements`, `totalPages`; empty |
| Get | `GET log-api/v1/projects/{id}` | Console code only |
| Classes | `GET billing-api/v2/log/quota-class` | 200 |
| Quote | `POST billing-api/v2/log/prices/created-price` | 200 |
| Create | `POST billing-api/v2/log/quotas` | Console code only |
| Delete | `DELETE billing-api/v1/log/quotas/{id}` | Console code only; to trash |
| Purge | `DELETE billing-api/v1/trash/log/quotas/{id}` | Console code only |

The list's `page` is 0-based; it also filters on `query`,
`billing_status`, `project_type`, and `status`. Classes are `Basic`,
`Pro`, and a disabled `Enterprise`. Each active class lists retention
options: `amount` (days), `minSize`, `maxSize`, `step` (GB per day), and a
`packageId`. Basic is free: 1 day, 10 GB. Pro offers 7 to 90 days. A quote
returned 0 VND for Basic and 917,000 VND a month for Pro at 7 days and
20 GB a day. Billing settings cap a free quota at one month.

The create body is `redirectUrl`, `packageId`, `quantity` (GB per day
times days), `buyWith` (optional email and SMS package IDs), `monthPeriod`
(1), `projectName`, `projectDescription`, and `pay`, which the console
sets true for IAM users. The console checks the name against
`^[a-z]$|^[a-z](?:[a-z\d-]){0,61}[a-z\d]$`.

### Alarms

| Call | Method and path | Seen |
|-|-|-|
| List | `GET /alarms/list?type-alarm=Metric\|Log&name=&status=&severity=&page=1&size=10` | 200: `lstData` and paging; empty |
| Get | `GET /alarms/{id}`, result in `data` | Console code only |
| Create log alarm | `POST /alarms/logs` | Console code only |
| Update log alarm | `PUT /alarms/logs/{id}` | Console code only |
| Delete log alarm | `DELETE /alarms/logs/{id}` | Console code only |
| Metric alarm writes | `/alarms/metrics`, v1 and v2 | Console code only |

The log alarm body is in [Alarms](#alarms-1). Its `inAlarm` and `ok` are
channel IDs, each followed by a comma, in one string. Metric alarms name a
channel by its `metricMappingId`, not its ID. The console shows alarm
statuses OK, In-alarm, Undetermined, and Creating.

## Non-goals

- Metric alarm writes and metric quota orders. aboutme names no metric
  alarm, and they need a second order flow. Metric alarms are read only.
- Log shipping: project certificates, whose download holds a private key,
  field mappings, archives, and search. They get their own design.
- Renewing, resizing, or recovering a log project; Teams channels; alarm
  history.

## Decisions

| Topic | Decision |
|-|-|
| Package | Extend `monitor` |
| Names | `Channel`, `LogProject`, `Alarm`; `ChannelID`, `LogProjectID`, `AlarmID` |
| Scope | Per account, as for checks: no region and no project ID |
| Channel secrets | The SDK returns them; the CLI redacts them |
| OTP | Two calls: send the OTP, then create with it |
| Updates | Read, merge the set pointer fields, send the full body |
| Log project price | Quote first; refuse above `MaxPrice`, default 0 |
| Waits | In the SDK, with `NoWait`, as in vDNS |

`monitor` already owns the host, the auth, and checks, and channels attach
to checks; a second package would import `monitor` or copy its routes.

## SDK

"(r)" marks `vngcloud:"required"`, and "*" marks a pointer field sent
only when set (ADR 0002 rule 3). Operation names are `monitor.<Method>`.

### Channels

| Operation | Call | Input |
|-|-|-|
| `ListChannelTypes` | List types | none |
| `ListChannels` | List | `Type`, `Page`, `Size` |
| `GetChannel` | List every page; match the ID | `ChannelID` (r) |
| `SendChannelOTP` | Send OTP | `Type` (r), `Address` (r), `Headers` |
| `CreateChannel` | Validate OTP when set, then Create | `Name` (r), `Type` (r), `Address` (r), `Headers`, `OTPRef`, `OTP` |
| `UpdateChannel` | `GetChannel`, then Update | `ChannelID` (r), `Name`*, `Address`*, `Headers`*, `OTPRef`, `OTP` |
| `DeleteChannel` | Delete | `ChannelID` (r) |

- `Channel` holds `ID`, `Name`, `Type`, `Address`, `Headers`
  (`[]ChannelHeader{Key, Value}` decoded from `header`; a string that is
  not that JSON leaves it nil), `MetricMappingID`, and the fields the live
  read shows.
- Constants `ChannelTypeEmail`, `ChannelTypeSlack`, `ChannelTypeSMS`,
  `ChannelTypeTelegram`, and `ChannelTypeWebhook` name the types. `Type`
  is a plain string, so a new type reaches the server unchanged.
- `ListChannels` pages from 1. `GetChannel` returns the SDK's not-found
  sentinel when no item has the ID.
- `SendChannelOTP` returns `{Ref string; ExpiresAt time.Time}`. It is a
  write: it messages the address.
- `CreateChannel` with `OTP` set sends Validate OTP once. A null `code`
  returns `ErrOTPRejected` and sends no create. Without `OTP` it sends no
  `otpCode`, and the server refuses an OTP type.
- `UpdateChannel` keeps the type. A new `Address` on an OTP type needs a
  fresh OTP; the server enforces that.

### Check notifications

`Check` and `CreateCheckInput` gain `Notifications CheckNotifications`,
with `InAlarm`, `Up`, and `Undetermined` channel ID lists tagged
`In-alarm`, `Up`, and `Undetermined`. Nil lists send `[]`, as
`CreateCheck` does today, so neither addition breaks a caller.

`UpdateCheck` takes `CheckID` (r) and `Name`*, `URL`*, `Method`*,
`Headers`*, `Query`*, `Body`*, `Timeout`*, `TestFrequency`*, `Tests`*,
`FailedLocations`*, `Locations`*, `Assertions`*, and `Notifications`*. It
reads the check, applies the set fields, and sends the full create body
with `PUT /uptimes/{id}` and `verified_ssl` true. It returns the check
from the response, or from one read when the response has none. It holds
the Client's check mutex, so pause, resume, and update do not interleave
in one process. Across processes, one of two updates can be lost; the API
has no version field.

The `PUT` body has no status. If the live check shows that the `PUT`
changes a paused check's status, `UpdateCheck` refuses a `DISABLED` check
with `ErrUnexpectedStatus` until a design covers it.

### Log projects

| Operation | Call | Input |
|-|-|-|
| `ListLogProjects` | List | `Query`, `BillingStatus`, `Page`, `Size` |
| `GetLogProject` | Get | `LogProjectID` (r) |
| `ListLogProjectClasses` | Classes | none |
| `QuoteCreateLogProject` | Classes, Quote | `*CreateLogProjectInput` |
| `CreateLogProject` | Classes, Quote, Create | see below |
| `DeleteLogProject` | Delete, then Purge when set | `LogProjectID` (r), `Purge` |

`CreateLogProjectInput` has `Name` (r), `Description`, `Class` (empty
sends `Basic`), `RetentionDays`, `GBPerDay`, `MaxPrice` (VND a month), and
`NoWait`. `RetentionDays` 0 picks the class's option when it has only
one. `GBPerDay` 0 sends the option's `minSize`. A class or retention the
class list lacks returns `ErrInvalidInput` and orders nothing. The SDK
sends `quantity` = `GBPerDay` x `RetentionDays`, `monthPeriod` 1,
`buyWith` `{}`, `pay` true, and the console's `redirectUrl`. One builder
makes the quote and the create body (ADR 0002 rule 8).

`CreateLogProject` quotes first. When `optimumPrice` exceeds `MaxPrice`,
it returns `ErrPriceAboveMax` naming both amounts and orders nothing, so
`CreateLogProjectInput{Name: "app"}` buys only a free Basic project. The
price can change in the one request between quote and order.

`pay: true` charges the account balance at once, as the console does for
IAM users. `pay: false` returns a payment URL for a browser, which an
agent cannot use. The order is a paid `POST`, so the transport does not
retry it after a 5xx (ADR 0002 rule 2); the caller lists projects by name
before ordering again.

`DeleteLogProject` moves the project to trash and stops its billing; its
logs are lost. `Purge` then deletes it from trash.

### Alarms

| Operation | Call | Input |
|-|-|-|
| `ListAlarms` | List | `Kind` (r: `Metric` or `Log`), `Name`, `Status`, `Severity`, `Page`, `Size` |
| `GetAlarm` | Get | `AlarmID` (r) |
| `CreateLogAlarm` | Create log alarm | see below |
| `UpdateLogAlarm` | `GetAlarm`, then Update log alarm | `AlarmID` (r) and each create field as * |
| `DeleteLogAlarm` | Delete log alarm | `AlarmID` (r) |

`ListAlarms` always sends all five query keys, empty when unset, as the
console does. `Alarm` holds the common fields and a `Log` part for log
alarms; its exact shape waits for the live read.

`CreateLogAlarmInput` has `Name` (r), `LogProjectID` (r),
`ThresholdValue` (r, `float64`), `Severity` (`LOW`, `MEDIUM`, or `HIGH`;
empty sends `LOW`), `Description`, `Filter` (`json.RawMessage`; empty
sends `{"type":"match_all","value":{}}`), `QueryString`, `ThresholdType`
(empty sends `frequency`), `Condition` (`gt`, `gte`, `lt`, or `lte`;
empty sends `gt`), `TimeFrame` (minutes; 0 sends 5), `GroupByField`,
`InAlarm` and `OK` (channel IDs), `Resend` (`Enabled`, `Statuses`,
`Period`, `Times`), and `NoWait`. The SDK reads the project to send
`projectName` and `zone`, and fills `reason`, `logSearchQuery`,
`metricAggKey`, and `metricAggType` as the console does. The live check
confirms each console-derived value before code.

### Identifiers

Every operation with an ID in its path, reads included, checks it with
`core.CheckPathID` before any request, and so does `UpdateChannel` for its
body ID. Channel type IDs are 32 hex characters and package IDs are UUIDs;
both match. Channel and alarm ID shapes are unseen; a shape outside the
pattern needs a design change.

### Retries

| Request | Rule |
|-|-|
| Reads, the quote | Normal retries; the quote sets `Idempotent` |
| Creates, the order, Send OTP (messages the address), Validate OTP (may spend the OTP) | No retry after a 5xx (ADR 0002 rule 2) |
| Updates, a full-body `PUT` | Normal retries: resending a full replace is safe |
| Deletes | Normal retries; a retry that finds it gone returns `NotFound` |

### Waits

A wait polls with normal reads every 2 seconds, honours `ctx`, uses the
injected clock, and keeps polling on a status it does not know.

| Operation | Settled when | Bound |
|-|-|-|
| `CreateLogProject` | The project, found by name, is `ACTIVE` | 120 s |
| `DeleteLogProject` | Get is 404, or the project is in trash | 60 s |
| `CreateLogAlarm`, `UpdateLogAlarm` | Status is no longer Creating | 60 s |

Settled returns the last read; a failed status returns the Output and
`ErrFailed`; the bound returns the Output, when known, and `ErrNotSettled`,
which says the write was accepted and must not be repeated. `NoWait`
returns the response at once. Other writes are synchronous.

## Errors

| Case | Result | CLI code and exit |
|-|-|-|
| Missing field, bad ID shape | `ErrInvalidInput`, no request | `InvalidUsage`, 2 |
| Unknown class or retention | `ErrInvalidInput`, no order | `InvalidUsage`, 2 |
| No channel with the ID | Not-found sentinel | `NotFound`, 4 |
| Wrong or expired OTP | `ErrOTPRejected`, no create | `OTPRejected`, 1 |
| Quote above `MaxPrice` | `ErrPriceAboveMax`, no order | `PriceAboveMax`, 1 |
| Wait failed or timed out | `ErrFailed`, `ErrNotSettled` | `WriteFailed`, `NotSettled`, 1 |
| Server refuses | The server's `*APIError` | 1, or 4 on 404 |

The CLI error codes gain `OTPRejected` and `PriceAboveMax`. `ErrFailed` and
`ErrNotSettled` reuse the vDNS sentinels and codes `WriteFailed` and
`NotSettled` ([vDNS](dns.md#after-a-write)); no new names are added for them.

## CLI

| Command | Kind | `--yes` | Release |
|-|-|-|-|
| `monitor list-channel-types`, `list-channels`, `get-channel` | Read | No | M1 |
| `monitor create-channel`, `update-channel` | Write | No | M2 |
| `monitor delete-channel` | Write, destructive | Yes | M2 |
| `monitor update-check` | Write | No | M3 |
| `monitor send-channel-otp` | Write | No | M4 |
| `monitor list-log-projects`, `get-log-project`, `list-log-project-classes`, `quote-create-log-project` | Read | No | M5 |
| `monitor create-log-project` | Write | No | M6 |
| `monitor delete-log-project` | Write, destructive | Yes | M6 |
| `monitor list-alarms`, `get-alarm` | Read | No | M7 |
| `monitor create-log-alarm`, `update-log-alarm` | Write | No | M8 |
| `monitor delete-log-alarm` | Write, destructive | Yes | M8 |

- A read-only profile refuses every write, `send-channel-otp` included.
- Deletes need `--yes`. A new channel has a new ID that every check and
  alarm must be pointed at again, and a deleted project loses its logs.
- Check notifications come through `--cli-input-json`, as `Locations`
  does; `update-check` takes scalar fields as flags.
- `create-channel` and `update-channel` refuse `--address` for `Webhook`
  and `Slack` with exit 2 and name `--cli-input-json file://channel.json`,
  as `configure` refuses a literal password: a webhook URL in argv reaches
  `ps` and shell history. `Headers` has no flag type, so it comes only
  from that file.
- Email: `send-channel-otp --type Email --address <a>` prints `Ref` and
  `ExpiresAt`; a person reads the code; then `create-channel --name <n>
  --type Email --address <a> --otp-ref <ref> --otp <code>`. Nothing
  prompts. The OTP expires in minutes and is spent once validated, so
  argv is acceptable for it.
- `create-log-project --max-price <vnd>` raises the price limit; without
  it only a free project is ordered. `delete-log-project --purge` also
  empties it from trash. Waiting writes take `--no-wait`.

## Security

- A webhook or Slack address can embed a token, and webhook headers often
  hold credentials. Email addresses, phone numbers, and Telegram chat IDs
  are personal data.
- The SDK returns `Address` and `Headers` unchanged, because an update
  needs them. `--debug` logs no body or query string, so they never reach
  it. No error holds them, an OTP, or an `otpCode`: for channel calls the
  SDK replaces the sent address and header values in a server message with
  `<redacted>` before it builds the `*APIError`.
- The CLI shows only `Email`, `SMS`, and `Telegram` addresses; any other
  type's `http(s)` address keeps scheme and host plus `/<redacted>`, else
  all `<redacted>`. Header values print `<redacted>`, in every format and
  before `--query`. No flag reveals them; the SDK does.
- `CreateLogProject` orders nothing above `MaxPrice`, default 0. SMS and
  email beyond the free 20 each spend a paid package; the wiki says so.
- Each write release gets an adversarial review. It checks: no resend of
  a create, Send OTP, Validate OTP, or the order; the price guard runs
  before the order on the create's own body; redaction in output, errors,
  and `--debug`; path ID checks; `--yes` on the three deletes; and
  read-only refusal of every write.
- Fixtures replace addresses, headers, and chat IDs with `<secret>` or
  `<account>`, and IDs with `<id>`.

## Testing

Unit tests use `httptest` and the injected clock:

- A sanitized fixture and decode test per operation, including valid and
  malformed channel header strings.
- Every write body, with defaults and with all fields, including the
  merged `UpdateCheck` body and a log alarm's joined channel IDs.
- `CreateChannel`: a null `code` sends no create; no retry after a 502.
  `GetChannel` across two pages and with no match.
- `CreateLogProject`: a 0 quote orders; a higher one does not; quote and
  order bodies match; no retry after a 502; each wait outcome.
- Redaction in `json`, `table`, `text`, and `--query Items[0].Address`,
  and in an `*APIError` whose message echoes the address.
- CLI: `--yes`, read-only refusal with no request sent, the literal
  `--address` refusal, and the new error codes.

Live tests follow [live data](../../instructions/live-data.md), touch only
resources named `vngcloud-live-*`, and register cleanup in `t.Cleanup` as
soon as each ID is known. `make live` adds `ListChannelTypes`,
`ListChannels`, `ListLogProjects`, `ListLogProjectClasses`, a Basic
`QuoteCreateLogProject`, and `ListAlarms` for both kinds, without pinning
counts. Write tests skip unless an environment variable names the approved
run, and log only counts and statuses.

## Live checks before code

Each write run needs the owner's approval for the test account. The
manager records only field names, types, statuses, and timings.

1. Webhook channel `vngcloud-live-<hex>` to the URL in
   `VNGCLOUD_LIVE_MONITOR_WEBHOOK_URL`: create, list (the raw item, the ID
   shape, `metricMappingId`, `header`, and whether `size=10000` works),
   update without `header` (is it cleared?), delete, and delete again.
2. Check `vngcloud-live-<hex>` naming that channel in `In-alarm`: pause it,
   `PUT /uptimes/{id}` with a new name (does it stay `DISABLED`? what does
   the `PUT` return?), delete the channel, and read the check (does the ID
   dangle?). Delete the check.
3. Email channel to an address the owner reads: Send OTP, Validate, and
   create, then delete. The owner relays the code. Also whether an email
   alert needs an email package.
4. Free Basic log project `vngcloud-live-<hex>`: quote, order with
   `pay: true` (does the response hold the ID?), time to `ACTIVE`, the raw
   project, delete, purge, and whether a second free project can then be
   ordered. If purge fails, stop and tell the owner: the account may lose
   its one free project.
5. Log alarm on that project with the webhook channel: create, get (the
   read shape against the body), update, and delete; the status after
   create; whether the joined IDs need the trailing comma.

## Releases

| Release | Content |
|-|-|
| M1 | `ListChannelTypes`, `ListChannels`, `GetChannel`, and `Check.Notifications`, with CLI reads and redaction |
| M2 | Webhook `CreateChannel`, `UpdateChannel`, and `DeleteChannel`, and the literal-address refusal |
| M3 | `CreateCheckInput.Notifications` and `UpdateCheck`, with `update-check` |
| M4 | `SendChannelOTP` and the OTP fields, for Email, Slack, SMS, and Telegram |
| M5 | Log project reads, `ListLogProjectClasses`, and `QuoteCreateLogProject` |
| M6 | `CreateLogProject` with the price guard and wait, and `DeleteLogProject` with `Purge` |
| M7 | `ListAlarms` and `GetAlarm` for both kinds |
| M8 | `CreateLogAlarm`, `UpdateLogAlarm`, and `DeleteLogAlarm`, with waits |

M1 to M3 give aboutme's app-down check a webhook alert, which needs no OTP
or package. Log projects come before log alarms, which need one. No
release changes an existing method or command.

## Owner decisions

All 13 are approved as recommended.

1. Approved: extend `monitor` instead of adding a package.
2. Approved: call GreenNode's notifications "channels".
3. Approved: the CLI redacts all but Email, SMS, and Telegram addresses.
4. Approved: the CLI refuses a literal webhook or Slack `--address`.
5. Approved: OTP channels use two commands, with the OTP on argv.
6. Approved: SMS channels are allowed, not live-tested; a cost warning.
7. Approved: updates read, merge, and resend the full body; may lose one.
8. Approved: `CreateLogProject` sends `pay: true`; refuses a quote over
   `MaxPrice`, default 0.
9. Approved: the log project quote lives in `monitor`, not `pricing`.
10. Approved: `DeleteLogProject` has `Purge`; the command needs `--yes`.
11. Approved: no metric alarm writes or quotas until aboutme names one.
12. Approved: releases M1 to M8 in this order.
13. Approved: the owner relays one email OTP for live check 3, before M4.

## Open questions

- Whether a free log project expires after a month; what renewal costs.
- Whether alarms count against a quota.
- How aboutme ships logs into a project: the certificate design.
