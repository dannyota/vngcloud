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
send a toggle request only when it is not already at the target, so
calling either twice in a row is safe:

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
only if the pause reported `Changed: true`.

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
on its own, or the toggle may need resending; either way, the fix is to
call `PauseCheck` or `ResumeCheck` again, since it reads the current status
first and sends nothing when that already matches:

```go
out, err := client.PauseCheck(ctx, &monitor.PauseCheckInput{CheckID: checkID})
if errors.Is(err, monitor.ErrStatusUnconfirmed) {
	// Wait a moment, then call PauseCheck again; it is safe to retry.
}
```

A 4xx on the toggle itself (401, 403, 404, 409, or 429) returns that
`*vngcloud.APIError` directly instead: the server never acted on it, so
there is nothing to confirm.

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
