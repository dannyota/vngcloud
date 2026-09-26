# Monitor

`monitor` is a separate package, `danny.vn/vngcloud/monitor`, with its own
`New(cfg)`. It reads vMonitor synthetic checks (GreenNode calls them uptime
checks), pauses or resumes them, creates and deletes them, and lists probe
locations. Every call is per account: it sends no project ID and ignores
the region in `Config`, like billing.

`CreateCheck` always makes an HTTP `API` check with `verified_ssl` on; it
ships with no way to name a notification channel, so a check it creates
alerts nobody until channels get their own release.

## Setup

```go
package main

import (
	"context"
	"log"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/monitor"
)

func main() {
	ctx := context.Background()

	cfg, err := vngcloud.NewConfig(
		vngcloud.WithRegion("hcm-3"),
		vngcloud.WithIAMUser(&vngcloud.IAMUserAuth{
			RootEmail: "<root-email>",
			Username:  "<iam-username>",
			Password:  "<password>",
		}),
	)
	if err != nil {
		log.Fatal(err)
	}

	client := monitor.New(cfg)
	_ = ctx
	_ = client
}
```

The rest of this page assumes `cfg` and `ctx` from this setup, plus
`client := monitor.New(cfg)`.

## Reading checks

```go
checks, err := client.ListChecks(ctx, nil)
if err != nil {
	log.Fatal(err)
}
for _, check := range checks.Items {
	log.Printf("%s: %s", check.Name, check.Status)
}

detail, err := client.GetCheck(ctx, &monitor.GetCheckInput{CheckID: checks.Items[0].ID})
if err != nil {
	log.Fatal(err)
}
log.Println(detail.Check.Config.Request.URL)
```

`ListChecksInput` has no fields; a nil Input is valid, and the API returns
every check in one response with no paging. `Check` decodes the console's
own field names, including `Config.Request` (the HTTP request the check
sends), `Config.Assertions` (the pass/fail rules it evaluates), and
`Notifications` (the [channels](#notification-channels) that alert on each
alarm transition, by ID). It leaves out alarms and a few account-internal
fields the API also sends.

A check's request headers and body may hold a credential for the monitored
service, such as a bearer token in a header. The SDK returns them exactly
as the API does, and `GetCheck` and `ListChecks` return them in full; they
never appear in log output (`vngcloud.WithLogger` never logs a body) or in
an error message. Avoid putting a long-lived secret in a check header if
you can help it, since anyone who can read the check can read it back.

## Listing locations

```go
locations, err := client.ListLocations(ctx, nil)
if err != nil {
	log.Fatal(err)
}
for _, loc := range locations.Items {
	log.Printf("%s: %s (%s)", loc.ID, loc.Name, loc.Status)
}
```

`ListLocationsInput` has no fields; a nil Input is valid. Each `Location.ID`
is the UUID `CreateCheck`'s `Locations` field takes; a location name such as
`SYNTT-VN-HCM01` is not accepted there and gets a 404 from the server.

## Creating and deleting checks

```go
created, err := client.CreateCheck(ctx, &monitor.CreateCheckInput{
	Name:      "vngcloud-my-check",
	URL:       "https://example.com/health",
	Locations: []string{locations.Items[0].ID},
})
if err != nil {
	log.Fatal(err)
}
log.Println(created.Check.ID)

if _, err := client.DeleteCheck(ctx, &monitor.DeleteCheckInput{CheckID: created.Check.ID}); err != nil {
	log.Fatal(err)
}
```

`Name`, `URL`, and `Locations` are required; every other field defaults to
what the console's own create form sends when left at zero: `Method` `GET`,
empty `Headers` and `Query` objects, an empty `Body`, a 10-second `Timeout`,
a 1-minute `TestFrequency`, 1 `Tests`, `FailedLocations` equal to the number
of locations passed, and the console's own assertion (fail on a 4xx or 5xx
response) when `Assertions` is empty. The server checks the name pattern
(5 to 30 characters, starting with a letter), the frequency range, and that
every location is a known UUID; a bad value there comes back as a plain
`*vngcloud.APIError`, not `vngcloud.ErrInvalidInput`.

`CreateCheck` is a `POST` and is never retried after a failure that may
already have reached the server. After any error that is not a 4xx
`*vngcloud.APIError` or `vngcloud.ErrInvalidInput`, the check may exist:
that covers a 5xx, a network error, a 201 with no `id`, and a body the SDK
could not decode. Call `ListChecks` and look for the check's name before
creating it again, so a retry never creates two checks for the same name.

`DeleteCheck` removes a check and its history; there is no undo. A second
delete of the same `CheckID` returns `vngcloud.IsNotFound(err) == true`.

## Pausing and resuming

`PauseCheck` and `ResumeCheck` read the check's current status first and
send a toggle request only when it is not already at the target. Calling
either again after it succeeds is safe. Calling it again after
`ErrStatusUnconfirmed` is not; see [Errors](#errors) below for the recovery.

```go
paused, err := client.PauseCheck(ctx, &monitor.PauseCheckInput{CheckID: checkID})
if err != nil {
	log.Fatal(err)
}
log.Printf("changed: %v, status: %s", paused.Changed, paused.Check.Status)

// ... later ...

if paused.Changed {
	if _, err := client.ResumeCheck(ctx, &monitor.ResumeCheckInput{CheckID: checkID}); err != nil {
		log.Fatal(err)
	}
}
```

`Changed` is false when the check was already at the target status, so a
caller that resumes only when its own pause changed something never
re-enables a check someone else paused for maintenance. This is the
pattern a deploy script uses: pause before a risky step, and resume after
only if the pause reported `Changed: true` (or failed with
`ErrStatusUnconfirmed`, per [Errors](#errors): that also means the pause
may have landed).

Both calls can take a few seconds: after sending the toggle, the SDK
confirms it landed by reading the check again, waiting up to 1, 2, and 4
seconds between reads if the first one has not shown the new status yet.
`ctx` bounds the whole call, including those waits.

### Errors

```go
var ErrUnexpectedStatus = errors.New("monitor: unexpected check status")
var ErrStatusUnconfirmed = errors.New("monitor: check status not confirmed")
```

`ErrUnexpectedStatus` means the check's status was neither `ENABLED` nor
`DISABLED` when the SDK read it. It sends no toggle in this case: an
unrecognized status is never guessed at.

`ErrStatusUnconfirmed` means the toggle was sent, or may have been, but no
confirm read showed the target status in time. The check may still reach it
on its own, or the toggle may still land later. Never call `PauseCheck` or
`ResumeCheck` again to resolve it, and never in a loop: a rerun reads
whatever status the check is actually at and, if that is not the target,
returns `Changed: false` for a toggle it never sent, which can leave
monitoring off with nothing left to alert on it.

The recovery differs by call, because only a pause's pre-toggle read proves
what the check was before the toggle:

- After `ErrStatusUnconfirmed` from `PauseCheck`, treat the pause as your
  own and resume the check later. This is safe because the call's own
  pre-toggle read already found the check `ENABLED`, so a toggle it sent,
  or may have sent, can only have paused it.
- After `ErrStatusUnconfirmed` from `ResumeCheck`, stop and have a person
  check the status by hand. The pre-toggle read only found the check
  `DISABLED`; nothing proves a toggle that may not have landed would have
  been safe to send, so a person decides instead of the caller guessing.

```go
paused, err := client.PauseCheck(ctx, &monitor.PauseCheckInput{CheckID: checkID})
switch {
case errors.Is(err, monitor.ErrStatusUnconfirmed):
	// Treat checkID as paused. Resume it later, the same as if
	// paused.Changed had come back true.
case err != nil:
	log.Fatal(err)
case paused.Changed:
	// ... later ...
	if _, err := client.ResumeCheck(ctx, &monitor.ResumeCheckInput{CheckID: checkID}); err != nil {
		log.Fatal(err)
	}
}
```

A 4xx on the toggle itself (401, 403, 404, 409, or 429) returns that
`*vngcloud.APIError` directly instead: the server never acted on it, so
there is nothing to confirm and nothing to treat as done.

Two callers that read the same status at the same moment and both toggle
leave the check where it started; each one's confirm reads then fail to
find their target, so each gets `ErrStatusUnconfirmed`. The API offers no
conditional request to prevent this. Within one process, a `monitor.Client`
runs its pause and resume calls one at a time, so this can only happen
across two processes or two `Client` values.

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
retention options a project can be ordered from, and
`QuoteCreateLogProject` prices an order without placing it. Ordering one
ships in a later release.

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
itself defaults to, gets a 400 from the server.

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
request and the next; `CreateLogProject`, a later release, re-reads the
same class list again immediately before ordering, for the same reason.

## Endpoint

The uptime API defaults to
`https://vmonitor.console.greennode.ai/`. Override it with `Monitor` on
`vngcloud.EndpointOverrides`, passed through `vngcloud.WithEndpointOverrides`.
The configured region is ignored: checks are per account, not per region.
