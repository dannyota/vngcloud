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
`Webhook`; the SDK does not yet create, update, or delete one.

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
at its zero value, lists every channel from `core.DefaultPage` at
`core.DefaultPageSize`.

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

`Channel.Address` is the email, Slack webhook URL, Telegram chat ID, phone
number, or webhook URL the channel notifies, and `Channel.Headers` is the
key/value pairs a `Webhook` channel sends with every notification; both can
hold a secret, such as a token in a header value. The SDK returns them
exactly as the API does, so a caller that will recreate or update a channel
can read them back; they never appear in log output (`vngcloud.WithLogger`
never logs a body) or in an error message. The
[CLI](CLI-Monitor.md) redacts both on print instead, with no flag to reveal
them.

## Endpoint

The uptime API defaults to
`https://vmonitor.console.greennode.ai/`. Override it with `Monitor` on
`vngcloud.EndpointOverrides`, passed through `vngcloud.WithEndpointOverrides`.
The configured region is ignored: checks are per account, not per region.
