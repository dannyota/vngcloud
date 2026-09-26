# Monitor

`monitor` is a separate package, `danny.vn/vngcloud/monitor`, with its own
`New(cfg)`. It reads vMonitor synthetic checks (GreenNode calls them uptime
checks), pauses or resumes them, creates, updates, and deletes them, lists
probe locations, reads and writes notification channels, and reads log
projects and alarms. Every call is per account: it sends no project ID and
ignores the region in `Config`, like billing.

`CreateCheck` always makes an HTTP `API` check with `verified_ssl` on. A
check's `Notifications` names, by channel ID, which [channels](#notification-channels)
alert on each alarm transition; a check created with no `Notifications` set
alerts nobody.

See [Monitor Alerts](Monitor-Alerts.md) for notification
channels, log projects, and alarms.

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
`Notifications` (the [channels](Monitor-Alerts.md#notification-channels)
that alert on each alarm transition, by ID). It leaves out alarms and a few
account-internal fields the API also sends.

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
of locations passed, the console's own assertion (fail on a 4xx or 5xx
response) when `Assertions` is empty, and an empty list for each of
`Notifications`' `InAlarm`, `Up`, and `Undetermined`. The server checks the
name pattern (5 to 30 characters, starting with a letter), the frequency
range, and that every location is a known UUID; a bad value there comes
back as a plain `*vngcloud.APIError`, not `vngcloud.ErrInvalidInput`.

`CreateCheck` is a `POST` and is never retried after a failure that may
already have reached the server. After any error that is not a 4xx
`*vngcloud.APIError` or `vngcloud.ErrInvalidInput`, the check may exist:
that covers a 5xx, a network error, a 201 with no `id`, and a body the SDK
could not decode. Call `ListChecks` and look for the check's name before
creating it again, so a retry never creates two checks for the same name.

`DeleteCheck` removes a check and its history; there is no undo. A second
delete of the same `CheckID` returns `vngcloud.IsNotFound(err) == true`.

## Updating checks

```go
newName := "vngcloud-my-check-renamed"
updated, err := client.UpdateCheck(ctx, &monitor.UpdateCheckInput{
	CheckID: checkID,
	Name:    &newName,
})
if err != nil {
	log.Fatal(err)
}
log.Println(updated.Check.Name)
```

`UpdateCheck` changes any combination of `Name`, `URL`, `Method`, `Headers`,
`Query`, `Body`, `Timeout`, `TestFrequency`, `Tests`, `FailedLocations`,
`Locations`, `Assertions`, and `Notifications`; a field left `nil` keeps the
check's current value, and at least one must be set. GreenNode's own API
takes a full replacement body and clears any field a request leaves out, so
`UpdateCheck` reads the check first with `GetCheck` and resends every field
the caller did not set itself, the same read-merge shape `UpdateChannel`
uses for a channel. The read and the write are two separate requests, with
nothing to detect a change in between: if another caller updates the check
after `UpdateCheck`'s own `GetCheck` but before its `PUT` lands, that change
is silently overwritten by whichever fields this call resends.

The `PUT` never changes a check's `Status`: pausing and resuming a check
stays `PauseCheck` and `ResumeCheck`'s job alone, and `UpdateCheck` accepts a
check in any status, including `DISABLED`, without touching it. Its `PUT` is
idempotent, since resending the same full replacement body is safe, so
`UpdateCheck` keeps the transport's normal retries, unlike `CreateCheck`'s
`POST`. `UpdateCheck` holds the same internal lock `PauseCheck` and
`ResumeCheck` do, so the three never interleave their own read-then-write
sequences against one `Client` within one process; across processes, or
across two `Client` values, whichever of two concurrent updates lands last
silently overwrites the other's change, since the API has no version field.

The `PUT` always sends `verified_ssl: true`, the same as `CreateCheck`,
regardless of the check's current value: updating a check made in the
console with TLS verification off turns it on.

`Notifications`, when set, replaces all three of the check's notification
lists at once (`InAlarm`, `Up`, and `Undetermined` together), not just the
one the caller means to change. To change one list, read the check first
with `GetCheck` and send back its full `Notifications` with that one list
changed.

`UpdateCheck` refuses, before any `PUT`, a `Locations` that is set but
empty, the same as `CreateCheck` refuses an empty `Locations`. It also
refuses, after reading the check and before any `PUT`, a pre-update read
that comes back with no usable check (an empty body, `null`, or a shape
that does not decode to the requested check's own ID): merging into that
would send a full-replace `PUT` that clears the check instead of updating
it. It refuses the same way when the check's current `Type` or `Subtype`
is not `API`/`HTTP`, since `Check`'s model only carries an HTTP request.

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

## Endpoint

The uptime API defaults to
`https://vmonitor.console.greennode.ai/`. Override it with `Monitor` on
`vngcloud.EndpointOverrides`, passed through `vngcloud.WithEndpointOverrides`.
The configured region is ignored: checks are per account, not per region.
