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
`Webhook`. `CreateChannel`, `UpdateChannel`, and `DeleteChannel` create,
update, and delete a `Webhook` channel; every other type needs an OTP the
account holder must read and relay, which ships in a later release.

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

`CreateChannel` accepts only `Type: monitor.ChannelTypeWebhook` today;
every other type needs an OTP the account holder must read and relay,
which ships in a later release, and a create with any other `Type` fails
with `vngcloud.ErrInvalidInput` before any request. `Name`, `Type`, and
`Address` are required; `Headers` is optional and defaults to none.

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
channel's type, and `UpdateChannel` accepts only a channel whose current
`Type` is `monitor.ChannelTypeWebhook`, failing with
`vngcloud.ErrInvalidInput` before any request for any other type, until
support for OTP types ships. Its `Output.Channel` never carries a fresh
`UpdatedDate`, since the update's own 200 response has no body to read one
from.

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

`GetLogProject` reads one project by ID. The test account has never held
one, so `LogProject`'s field shape past `ID` is unverified: see its doc
comment for what it is based on, and confirm it against a live project
before depending on a field other than `ID`.

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
already reached the server, unlike a create. It always re-reads the class
list first, since the class list and its prices can change between one
request and the next; `CreateLogProject` re-reads the same class list
again immediately before ordering, for the same reason. The quote ignores
`MaxPrice` and `NoWait`: those fields govern only `CreateLogProject`'s
price ceiling and wait.

### Ordering, deleting, and purging a log project

```go
created, err := client.CreateLogProject(ctx, &monitor.CreateLogProjectInput{
	Name: "vngcloud-my-logs",
})
switch {
case errors.Is(err, monitor.ErrPriceAboveMax):
	log.Fatal("quoted price exceeds MaxPrice; raise MaxPrice to order it anyway")
case errors.Is(err, dns.ErrNotSettled): // "danny.vn/vngcloud/dns"
	log.Printf("log project %s was ordered; check it later, do not order again", created.LogProject.ID)
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

`CreateLogProject` quotes the order first with `QuoteCreateLogProject` and
refuses with `monitor.ErrPriceAboveMax`, ordering nothing, when the quote's
`OptimumPrice` exceeds `Input.MaxPrice`, which defaults to 0:
`CreateLogProjectInput{Name: "app"}` therefore only ever orders a project
whose class and retention price at 0 VND. Raise `MaxPrice` to allow a paid
order. The order is a `POST` and is never retried after a failure that may
have already reached the server, the same as `CreateHostedZone`: after any
error that is not a 4xx `*vngcloud.APIError` or `vngcloud.ErrInvalidInput`,
the project may have been ordered, and the caller lists projects by `Name`
before ordering again.

Unless `NoWait` is set, `CreateLogProject` then waits up to 120 seconds for
a project named `Input.Name` to appear, by listing projects, at
`monitor.LogProjectStatusActive`; `DeleteLogProject` waits up to 60 seconds
for a read of the deleted project to either come back not-found or no
longer match its pre-delete state. Either wait running out, or a read or a
sleep inside it failing, such as from a canceled `ctx`, returns an error
wrapping `dns.ErrNotSettled`: the design reuses vDNS's own sentinel here
rather than adding a new one, so the same [DNS](DNS.md#errors) handling
applies, and the write must not be repeated. `NoWait` returns at once
instead: `CreateLogProject` returns the order response on a best-effort
basis, since the test account's own order response shape is unverified,
and `DeleteLogProject` skips its pre-delete baseline read too.

`DeleteLogProject` moves a project to trash, stopping its billing; its logs
are lost. `Purge` also deletes it from trash, as a second request in the
same call, so a purge is never sent without the delete that precedes it. If
that first delete 404s while `Purge` is set, `DeleteLogProject` still sends
the purge, since the project most likely already sits in trash from an
earlier call and `Purge`'s job is to make sure it ends up gone either way;
without `Purge`, that same 404 comes back as the SDK's ordinary not-found
result, same as any other delete.

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
