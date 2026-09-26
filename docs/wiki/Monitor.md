# Monitor

`monitor` is a separate package, `danny.vn/vngcloud/monitor`, with its own
`New(cfg)`. It reads vMonitor synthetic checks (GreenNode calls them uptime
checks) and pauses or resumes them. Every call is per account: it sends no
project ID and ignores the region in `Config`, like billing.

Creating and deleting checks, and listing probe locations, are not covered
yet; they need their own release.

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
sends) and `Config.Assertions` (the pass/fail rules it evaluates). It leaves
out fields the design has not covered yet: notification channels, alarms,
and a few account-internal fields the API also sends.

A check's request headers and body may hold a credential for the monitored
service, such as a bearer token in a header. The SDK returns them exactly
as the API does, and `GetCheck` and `ListChecks` return them in full; they
never appear in log output (`vngcloud.WithLogger` never logs a body) or in
an error message. Avoid putting a long-lived secret in a check header if
you can help it, since anyone who can read the check can read it back.

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
