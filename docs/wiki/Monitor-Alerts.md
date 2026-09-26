# Monitor Alerts

Notification channels, log projects, and alarms for
`danny.vn/vngcloud/monitor`. See [Monitor](Monitor.md) for checks,
locations, and pausing and resuming them.

This page assumes `cfg`, `ctx`, and `client := monitor.New(cfg)` from
[Monitor's Setup](Monitor.md#setup).

## Notification channels

A notification channel is what GreenNode's own API calls a "notification";
the SDK says "channel" so the name does not clash with a check's
`Notifications` field. `ListChannels` and `GetChannel` return every channel
type the console offers, including `Email`, `Slack`, `SMS`, `Telegram`, and
`Webhook`. `CreateChannel`, `UpdateChannel`, and `DeleteChannel` work with
any of the five; `Webhook` needs no OTP, and every other type needs one
from `SendChannelOTP`, covered next, before a create or an `Address`
update.

```go
types, err := client.ListChannelTypes(ctx, nil)
if err != nil {
	log.Fatal(err)
}
for _, t := range types.Items {
	log.Printf("%s: %s", t.ID, t.Name)
}

channels, err := client.ListChannels(ctx, &monitor.ListChannelsInput{Type: monitor.ChannelTypeWebhook})
if err != nil {
	log.Fatal(err)
}
for _, ch := range channels.Items {
	log.Printf("%s: %s (%s)", ch.ID, ch.Name, ch.Type)
}
```

`ListChannelTypesInput` has no fields; a nil Input is valid, and the API
returns every type in one response with no paging. `ListChannelsInput` has
`Type` (empty for every type), `Page`, and `Size`; a nil Input, or one left
at its zero value, lists every channel from page 1 at size 10000.

```go
found, err := client.GetChannel(ctx, &monitor.GetChannelInput{ChannelID: channelID})
if err != nil {
	if vngcloud.IsNotFound(err) {
		log.Printf("no channel %s", channelID)
	} else {
		log.Fatal(err)
	}
}
```

There is no get-by-ID call for a channel: `GetChannel` lists every page and
returns the item whose ID matches, so `vngcloud.IsNotFound(err)` is true both
for an unknown ID and for an account with no channels at all.

### Sending and validating an OTP

`Email`, `Slack`, `SMS`, and `Telegram` each need a one-time code before a
create or an `Address` update; `Webhook` needs none. `SendChannelOTP`
messages `Address` with the code and returns a `Ref`. Read the code from
the address, then pass both to `CreateChannel` or `UpdateChannel` as
`OTPRef` and `OTP`.

```go
sent, err := client.SendChannelOTP(ctx, &monitor.SendChannelOTPInput{
	Type:    monitor.ChannelTypeEmail,
	Address: "ops@example.com",
})
if err != nil {
	log.Fatal(err)
}
// ... read the code from the address, then: ...
created, err := client.CreateChannel(ctx, &monitor.CreateChannelInput{
	Name:    "vngcloud-my-email",
	Type:    monitor.ChannelTypeEmail,
	Address: "ops@example.com",
	OTPRef:  sent.Ref,
	OTP:     "123456",
})
if errors.Is(err, monitor.ErrOTPRejected) {
	log.Fatal("wrong or expired code")
}
```

`SendChannelOTP` refuses `Webhook`, and any type outside the five the
console offers, with `vngcloud.ErrInvalidInput` before any request. It is
a `POST` that messages the address, so it is never retried after a
failure that may have already reached the server: a retry could send a
second message, and a caller that wants one anyway calls
`SendChannelOTP` again itself. `CreateChannel` and `UpdateChannel` apply
the same no-retry rule to the validate step they run internally when
`OTP` is set, since a retry there could spend a code the first attempt
already validated.

`OTPRef` and `OTP` are meant to be set together, or both left empty; `OTP`
set with no `OTPRef` fails with `vngcloud.ErrInvalidInput` before any
request. A wrong or expired code makes `CreateChannel` or `UpdateChannel`
return `monitor.ErrOTPRejected` and send no create or update. Leaving
`OTPRef` and `OTP` both empty sends no `otpCode`, which is what `Webhook`
needs and every other type is refused for.

`Address`, `OTPRef`, and the OTP itself are secrets, the same as a header
value: none ever appears in an error message, and a server message that
echoes one back comes back with `<redacted>` in its place instead.
Sending an OTP to, and later notifying, an `SMS` or `Email` channel counts
toward that channel's free 20 messages; either one past its free 20 spends
a paid package. `Slack` and `Telegram` cost nothing extra.

### Creating, updating, and deleting channels

```go
created, err := client.CreateChannel(ctx, &monitor.CreateChannelInput{
	Name:    "vngcloud-my-webhook",
	Type:    monitor.ChannelTypeWebhook,
	Address: "https://example.com/hook",
	Headers: []monitor.ChannelHeader{{Key: "X-Token", Value: "<secret>"}},
})
if err != nil {
	log.Fatal(err)
}
log.Println(created.Channel.ID)

newName := "vngcloud-my-webhook-renamed"
updated, err := client.UpdateChannel(ctx, &monitor.UpdateChannelInput{
	ChannelID: created.Channel.ID,
	Name:      &newName,
})
if err != nil {
	log.Fatal(err)
}
log.Println(updated.Channel.Name)

if _, err := client.DeleteChannel(ctx, &monitor.DeleteChannelInput{ChannelID: created.Channel.ID}); err != nil {
	log.Fatal(err)
}
```

`CreateChannel` accepts `Type` `Email`, `Slack`, `SMS`, `Telegram`, or
`Webhook`; any other value fails with `vngcloud.ErrInvalidInput` before
any request. `Name`, `Type`, and `Address` are required; `Headers` is
optional and defaults to none, and every type but `Webhook` also needs
`OTPRef` and `OTP` (see [Sending and validating an
OTP](#sending-and-validating-an-otp) above) to create.

`CreateChannel` is a `POST` and is never retried after a failure that may
already have reached the server, the same as `CreateCheck`: after any
error that is not a 4xx `*vngcloud.APIError` or `vngcloud.ErrInvalidInput`,
the channel may exist, and the caller lists channels by name before
creating it again rather than retrying blind.

`UpdateChannel` changes `Name`, `Address`, `Headers`, or any combination of
the three; a field left `nil` keeps the channel's current value, and at
least one must be set. To clear every header on purpose, set `Headers` to a
non-nil empty slice (`&[]monitor.ChannelHeader{}`); leaving `Headers` `nil`
resends the channel's current headers unchanged. GreenNode's own API takes a
full replacement body and clears any field a request leaves out, so
`UpdateChannel` reads the channel first with `GetChannel` and resends every
field the caller did not set itself, rather than trusting the API to leave
them alone. It keeps the channel's `Type`; there is no way to change a
channel's type, and `UpdateChannel` never sends one other than the
channel's own current `Type`. Changing an `Email`, `Slack`, `SMS`, or
`Telegram` channel's `Address` needs a fresh `OTPRef` and `OTP` (see
[Sending and validating an OTP](#sending-and-validating-an-otp) above);
the server enforces that, not the SDK. Its `Output.Channel` never carries
a fresh `UpdatedDate`, since the update's own 200 response has no body to
read one from.

The read and the write are two separate requests, with nothing to detect a
change in between: if another caller updates the channel after
`UpdateChannel`'s own `GetChannel` but before its `PUT` lands, that change is
silently overwritten by whichever fields this call resends. Serialize
concurrent updates to the same channel elsewhere if that matters.

`DeleteChannel` removes a channel; GreenNode's own API strips the deleted
channel's ID from every check's `Notifications`, so alerting through that
channel silently stops on every check that used it, with no warning and no
undo. A second delete of the same `ChannelID` returns
`vngcloud.IsNotFound(err) == true`, the same as `DeleteCheck`, even though
GreenNode's own API answers that specific case with a 400 rather than a
404; a retried `DeleteChannel` whose first attempt already reached the
server gets this same not-found error on the retry, which the caller
treats the same as a successful delete.

`Channel.Address` is the email, Slack webhook URL, Telegram chat ID, phone
number, or webhook URL the channel notifies, and `Channel.Headers` is the
key/value pairs a `Webhook` channel sends with every notification; both can
hold a secret, such as a token in a header value. The SDK returns them
exactly as the API does, so a caller that will recreate or update a channel
can read them back; they never appear in log output (`vngcloud.WithLogger`
never logs a body). `CreateChannel` and `UpdateChannel` also strip both out
of a server error message before building the `*vngcloud.APIError`, in
either its raw form or the form the request's own JSON encoding produced
(a header value's literal `&` sent as `\u0026`, say): if GreenNode's own
response happens to echo the sent address or a header value back, that
call's error carries `<redacted>` in its place instead, unless the value is
too short to cut out safely, in which case the whole message is withheld
rather than returned with unrelated text also cut out around it. The
[CLI](CLI-Monitor.md) shows `Address` in full only for `Email`, `SMS`, and
`Telegram`, whose address is personal data rather than a secret; every
other type, known or not, has its `Address` redacted, keeping only the
scheme and host for an `http` or `https` URL and redacting the rest whole
otherwise. Every header value is always redacted, regardless of channel
type. There is no flag to reveal either.

## Log projects

A log project is a vMonitor log quota: ordering one both provisions the log
project and creates its billing quota, sharing one ID. `ListLogProjects` and
`GetLogProject` read them; `ListLogProjectClasses` lists the classes and
retention options a project can be ordered from; `QuoteCreateLogProject`
prices an order without placing it; and `CreateLogProject` and
`DeleteLogProject` order and remove one.

```go
projects, err := client.ListLogProjects(ctx, nil)
if err != nil {
	log.Fatal(err)
}
for _, p := range projects.Items {
	log.Printf("%s: %s (%s)", p.ID, p.ProjectName, p.Status)
}

classes, err := client.ListLogProjectClasses(ctx, nil)
if err != nil {
	log.Fatal(err)
}
for _, c := range classes.Items {
	log.Printf("%s: %s, %d retention options", c.Name, c.Status, len(c.Retentions))
}
```

`ListLogProjectsInput` has `Query`, `BillingStatus`, `Page`, and `Size`; a
nil Input, or one left at its zero value, lists from page 0 at size 100.
Unlike `ListChannels`, the underlying API's page is 0-based, so `Page: 0`
asks for the real first page rather than being promoted to page 1, and its
`Size` tops out at 100: a larger value, including what `ListChannels`
itself defaults to, gets a 400 from the server. The output's `TotalPage`
counts from that same 0-based `Page`, so the last page is `Page ==
TotalPage-1`, not `TotalPage`.

`GetLogProject` reads one project by ID; a live project confirms
`LogProject`'s field shape (see its doc comment).

`ListLogProjectClassesInput` has no fields; a nil Input is valid, and the
API returns every class in one response with no paging.
`LogProjectClass.Retentions` is empty for a disabled class such as
Enterprise, which has no retention options to order from.

### Pricing an order

```go
quote, err := client.QuoteCreateLogProject(ctx, &monitor.CreateLogProjectInput{
	Name:          "vngcloud-my-logs",
	Class:         monitor.LogProjectClassPro,
	RetentionDays: 7,
	GBPerDay:      20,
})
if err != nil {
	log.Fatal(err)
}
log.Printf("%.0f VND/month", quote.OptimumPrice)
```

`CreateLogProjectInput.Class` empty prices `monitor.LogProjectClassBasic`.
`RetentionDays` 0 picks the class's only retention option; a class with more
than one, such as Pro, needs it named. `GBPerDay` 0 sends the chosen
option's minimum size. A class or retention the live class list does not
have returns `vngcloud.ErrInvalidInput` before any pricing request.

`QuoteCreateLogProject` sends a `POST`, but it prices an order without
placing one, so it is a read: it is retried after a failure that may have
already reached the server, unlike a create. It always reads the class
list fresh on every call, since the class list and its prices can change
between one request and the next; it does not share that read with
`CreateLogProject`, which instead reads the class list once and sends that
exact order body to both its own quote and its order (see below). A quote
response missing `optimumPrice`, or sending it `null`, fails with a
`*vngcloud.APIError` rather than pricing the order at a silent 0. The
quote ignores `MaxPrice` and `NoWait`: those fields govern only
`CreateLogProject`'s price ceiling and wait.

### Ordering, deleting, and purging a log project

```go
created, err := client.CreateLogProject(ctx, &monitor.CreateLogProjectInput{
	Name: "vngcloud-my-logs",
})
switch {
case errors.Is(err, monitor.ErrPriceAboveMax):
	log.Fatal("quoted price exceeds MaxPrice; raise MaxPrice to order it anyway")
case errors.Is(err, dns.ErrNotSettled): // "danny.vn/vngcloud/dns"
	log.Printf("log project order %s was accepted; check it later, do not order again", created.OrderID)
case err != nil:
	log.Fatal(err)
}

if _, err := client.DeleteLogProject(ctx, &monitor.DeleteLogProjectInput{
	LogProjectID: created.LogProject.ID,
	Purge:        true,
}); err != nil {
	log.Fatal(err)
}
```

Before any request, `CreateLogProject` rejects a `NaN`, `+Inf`, `-Inf`, or
negative `MaxPrice` with `vngcloud.ErrInvalidInput`: its price guard cannot
compare any of those safely, and a `NaN` `MaxPrice` in particular compares
false against every quote, which would silently disable the guard on a
paid create. It also lists projects by `Name` first and refuses, also with
`vngcloud.ErrInvalidInput`, when one already exists with that exact name,
since the post-order wait below would otherwise risk settling on that
existing project instead of the one this call orders.

`CreateLogProject` then reads the class list once, builds one order body
from `Input`, and sends that exact body first to price the order and then,
unless it prices above `Input.MaxPrice`, to place it, so the quote and the
order always price and create the identical resource. It refuses with
`monitor.ErrPriceAboveMax`, ordering nothing, when that quote's
`OptimumPrice` exceeds `Input.MaxPrice`, which defaults to 0:
`CreateLogProjectInput{Name: "app"}` therefore only ever orders a project
whose class and retention price at 0 VND. Raise `MaxPrice` to allow a paid
order; the Basic class itself allows only 3 orders or recoveries a month,
and an order past that quota returns the server's own 409 "Exceeded quota"
rather than a distinct SDK error. The order is a `POST` and is never
retried after a failure that may have already reached the server, the
same as `CreateHostedZone`: after any error that is not a 4xx
`*vngcloud.APIError` or `vngcloud.ErrInvalidInput`, the project may have
been ordered, and the caller lists projects by `Name` before ordering
again.

The order response is confirmed live to carry only `amount`, `orderId`, and
`paymentUrl`, none of `LogProject`'s own fields: it names no project id,
name, or status. A live free order returned `orderId` empty or null, so
`Output.OrderID` is not guaranteed non-empty either way; check
`created.LogProject.ID` from the post-order wait instead when the project's
own ID is needed. Unless `NoWait` is set, `CreateLogProject` therefore
waits up to 120 seconds for a project named `Input.Name` to appear, by
listing projects, at `monitor.LogProjectStatusActive`, looking it up by the
name the order itself just sent rather than by anything the order response
might carry; `DeleteLogProject` waits up to 60 seconds for a read of the
deleted project to either come back not-found or no longer match its
pre-delete state. Either wait running out, or a read or a sleep inside it
failing, such as from a canceled `ctx`, returns an error wrapping
`dns.ErrNotSettled`: the design reuses vDNS's own sentinel here rather than
adding a new one, so the same [DNS](DNS.md#errors) handling applies, and
the write must not be repeated. `NoWait` returns at once instead:
`Output.LogProject` stays at its zero value and only `Output.OrderID` is
set, from the order response's own `orderId`; `DeleteLogProject` skips its
pre-delete baseline read too.

`DeleteLogProject` moves a project to trash, stopping its billing; its logs
are lost. `Purge` also deletes it from trash, as a second request in the
same call, so a purge is never sent without the delete that precedes it,
and one `DeleteLogProject` call with `Purge: true` is the main way to use
it, though that combined call has not itself been run live yet. A separate
purge sent right after an earlier, already-settled delete was seen live to
return a plain 409 Conflict once, for a reason still unconfirmed; the SDK
adds no retry or other tolerance for that status, so a caller that hits it
decides for itself whether to call `DeleteLogProject` again.

A free project was seen live to leave trash on its own within a few
seconds of a delete, removed from the log-api list, the billing list, and
the trash list together, so `DeleteLogProject`'s own pre-delete baseline
read can already 404 by the time a later `Purge` call runs. When that
happens with `Purge` set, `DeleteLogProject` still sends the delete and the
purge, tolerating a 404 from either, and returns success at once, with no
wait, since there is no baseline left to wait against, unless the delete
and the purge both 404 too, in which case nothing here ever confirmed
`LogProjectID` named a real project, and `DeleteLogProject` returns the
not-found error instead of that tolerant success. Without `Purge`, the
baseline 404 alone comes back as the SDK's ordinary not-found result, same
as any other delete. A second `DeleteLogProject` call on a project another
call is still in the middle of removing, rather than one the SDK's own
baseline read already saw as gone, can instead surface a plain 409
Conflict, or a 404 the delete or purge step does not tolerate; the SDK
adds no retry or other special handling for either, since the project is
already headed to the state the call asked for.

## Alarms

`ListAlarms` and `GetAlarm` read vMonitor alarms, of either `Kind`:
`monitor.AlarmKindMetric` or `monitor.AlarmKindLog`. There is no create,
update, or delete yet: a log alarm needs a log project, which
`CreateLogProject` can now order, but log alarm writes do not exist yet.

```go
alarms, err := client.ListAlarms(ctx, &monitor.ListAlarmsInput{Kind: monitor.AlarmKindLog})
if err != nil {
	log.Fatal(err)
}
for _, a := range alarms.Items {
	log.Printf("%s: %s (%s)", a.ID, a.Name, a.Status)
}

if len(alarms.Items) > 0 {
	detail, err := client.GetAlarm(ctx, &monitor.GetAlarmInput{AlarmID: alarms.Items[0].ID})
	if err != nil {
		log.Fatal(err)
	}
	log.Println(detail.Alarm.Status)
}
```

`ListAlarmsInput.Kind` is required; `Name`, `Status`, and `Severity` narrow
the list further, empty for no filter, and `Page`/`Size` page the result
starting at page 1. The output's `TotalPage` counts from that same 1-based
`Page`, so the last page is `Page == TotalPage`. Every returned
`Alarm.Kind` is set to the `Kind` filter sent, since one call always lists
one kind; `ListAlarms` also sets each item's `Log` and `MetricMappingID`
from that same `Kind`, clearing whichever field does not belong to it,
rather than trusting which one the response happens to carry.

`Alarm` holds `ID`, `Name`, `Kind`, `Status`, and `Severity` for both kinds.
A Log alarm's `Log` field is non-nil and holds `InAlarm` and `OK`, the
channel IDs that alert on entering and leaving the alarm state. A Metric
alarm has `Log` nil and instead sets `MetricMappingID`, naming the channel
by the same `MetricMappingID` a channel read itself returns, rather than by
the channel's `ID`.

`GetAlarm` takes no `Kind` filter, and the API sends no field confirmed to
name the kind itself, so a `GetAlarm` read's `Alarm.Kind` comes back empty
rather than inferred; unverified until a live read confirms what, if
anything, would name it. `Log` and `MetricMappingID` still decode from
whichever wire fields the response carries. This call's response shape has
not been checked against a real alarm at all: the test account has none of
either kind, and the design's source for the alarm calls is the console's
own JavaScript, not a live capture. A live `GetAlarm` for an ID with no
matching alarm returned a 500, not a 404, so unlike `GetCheck` and
`GetChannel`, a missing alarm does not resolve to
`vngcloud.IsNotFound(err) == true`; it comes back as a plain
`*vngcloud.APIError`.
