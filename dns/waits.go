package dns

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/transport"
)

// Status values a hosted zone reports. StatusCreating and StatusUpdating are
// the two states a write moves a zone through; a status this SDK does not
// know keeps every wait below polling rather than treating it as either
// ready or settled.
const (
	StatusCreating = "CREATING"
	StatusActive   = "ACTIVE"
	StatusUpdating = "UPDATING"
	StatusError    = "ERROR"
)

var (
	// ErrZoneBusy means the pre-write wait's bound ran out before the zone
	// left StatusCreating or StatusUpdating. Nothing was sent; running the
	// same call again is safe.
	ErrZoneBusy = errors.New("dns: zone busy")

	// ErrNotSettled means a write was sent, and may have reached the server,
	// but no read confirmed its result within the post-write wait's bound.
	// The write must not be repeated; the returned Output still holds the
	// resource the SDK last read, so the caller keeps its id to check again
	// later.
	ErrNotSettled = errors.New("dns: write accepted but not settled")

	// ErrFailed means the written resource reached StatusError after a
	// write. The returned Output still holds the resource the SDK last
	// read.
	ErrFailed = errors.New("dns: write failed on the server")
)

// pollInterval and pollBound set every wait's cadence: a wait reads with a
// plain GET at once and then every pollInterval, honoring ctx, until
// pollBound has elapsed since the wait began.
const (
	pollInterval = 2 * time.Second
	pollBound    = 60 * time.Second
)

// sleepFunc waits for d or ctx's end, whichever comes first, returning
// ctx.Err() when ctx ends first. Tests inject a fake one so a wait's real
// 2-second and 60-second bounds never really elapse.
type sleepFunc func(ctx context.Context, d time.Duration) error

// clockFunc reads the current time. poll uses it to bound a wait by elapsed
// wall time rather than by counting poll intervals, so a slow read itself
// counts against the bound. Tests inject a fake clock; production uses
// time.Now.
type clockFunc func() time.Time

// contextSleep is the real sleepFunc.
func contextSleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// poll runs step at once, then again every pollInterval, until step reports
// stop true or pollBound has elapsed, by now, since poll's first call to
// step. Elapsed time is read from now rather than counted in pollInterval
// steps, so a step that itself takes real time, such as a slow read, counts
// against the bound instead of only the sleeps between steps; a test
// injects both a fake clock and a sleepFunc that returns quickly.
//
// step reports stop true for two different reasons, told apart by its own
// err: a settled or otherwise final outcome (err nil or a wrapped
// ErrFailed), or an outright read failure (a plain error, which poll
// returns unchanged). onTimeout is called, and its result returned, only
// when the bound elapses with step never reporting stop.
func poll(ctx context.Context, now clockFunc, sleep sleepFunc, step func(ctx context.Context) (stop bool, err error), onTimeout func() error) error {
	deadline := now().Add(pollBound)
	for {
		stop, err := step(ctx)
		if stop {
			return err
		}
		if !now().Before(deadline) {
			return onTimeout()
		}
		if err := sleep(ctx, pollInterval); err != nil {
			return err
		}
	}
}

// getHostedZone is GetHostedZone's request, reused by every wait under the
// calling write's own operation name, so a failure names the write it
// happened inside rather than "dns.GetHostedZone". zoneID is assumed
// already checked with core.CheckPathID.
func (c *Client) getHostedZone(ctx context.Context, op, zoneID string) (*HostedZone, error) {
	var resp struct {
		Data HostedZone `json:"data"`
	}
	if err := c.c.DoJSON(ctx, transport.Request{
		Operation: op,
		Method:    http.MethodGet,
		URL:       c.url([]string{"dns", "hosted-zone", zoneID}, nil),
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	return &resp.Data, nil
}

// waitZoneReady is the pre-write wait every zone update and zone delete runs
// before sending anything: it reads the zone until its Status is
// StatusActive or StatusError, the two states the server accepts a write
// against; CREATING, UPDATING, and any status this SDK does not recognize
// all count as busy. It always runs, NoWait or not, since nothing has been
// sent yet, and it turns the zone lock's 400 into a short wait instead.
func (c *Client) waitZoneReady(ctx context.Context, op, zoneID string) (*HostedZone, error) {
	var zone *HostedZone
	err := poll(ctx, c.now, c.sleep,
		func(ctx context.Context) (bool, error) {
			z, err := c.getHostedZone(ctx, op, zoneID)
			if err != nil {
				return true, err
			}
			zone = z
			return z.Status == StatusActive || z.Status == StatusError, nil
		},
		func() error {
			return fmt.Errorf("%w: %s: hosted zone %s did not leave %s within %s", ErrZoneBusy, op, zoneID, zone.Status, pollBound)
		},
	)
	if err != nil {
		return nil, err
	}
	return zone, nil
}

// settleZone is the post-write wait CreateHostedZone and UpdateHostedZone
// run unless NoWait is set: it reads the zone until settled reports true
// for a read (the write's own success condition) or the zone's Status
// becomes StatusError, whichever happens first. It returns the last zone it
// read alongside the outcome: a nil error when settled matched, an error
// wrapping ErrFailed when the zone reached StatusError first, or one
// wrapping ErrNotSettled when neither happened before the bound.
//
// The write itself already succeeded by the time settleZone is called, so
// every error path here, a canceled sleep, a failed read, or the bound
// running out, means the same thing to the caller: do not send the write
// again. settleZone wraps every one of them in ErrNotSettled, never
// returning a bare context or transport error. The returned zone is the
// last one a read actually returned; it is nil only when no read after the
// write ever succeeded, in which case the caller falls back to whatever the
// write itself produced.
func (c *Client) settleZone(ctx context.Context, op, zoneID string, settled func(*HostedZone) bool) (*HostedZone, error) {
	var zone *HostedZone
	err := poll(ctx, c.now, c.sleep,
		func(ctx context.Context) (bool, error) {
			z, err := c.getHostedZone(ctx, op, zoneID)
			if err != nil {
				return true, err
			}
			zone = z
			if settled(z) {
				return true, nil
			}
			if z.Status == StatusError {
				return true, fmt.Errorf("%w: %s: hosted zone %s is ERROR; every associated VPC needs Private DNS ENABLED, not ENABLING",
					ErrFailed, op, zoneID)
			}
			return false, nil
		},
		func() error {
			return fmt.Errorf("%w: %s: hosted zone %s was accepted; do not send the same write again", ErrNotSettled, op, zoneID)
		},
	)
	if err != nil && !errors.Is(err, ErrFailed) && !errors.Is(err, ErrNotSettled) {
		err = fmt.Errorf("%w: %s: hosted zone %s: %w", ErrNotSettled, op, zoneID, err)
	}
	return zone, err
}

// waitZoneGone is DeleteHostedZone's post-write wait unless NoWait is set:
// it reads the zone until the read fails with NotFound, or returns an error
// wrapping ErrNotSettled once the bound runs out first.
//
// The delete itself already succeeded by the time waitZoneGone is called,
// so a canceled sleep or any other read failure means the same thing as
// the bound running out: do not delete the zone again. Every error path
// here wraps ErrNotSettled.
func (c *Client) waitZoneGone(ctx context.Context, op, zoneID string) error {
	err := poll(ctx, c.now, c.sleep,
		func(ctx context.Context) (bool, error) {
			_, err := c.getHostedZone(ctx, op, zoneID)
			if err == nil {
				return false, nil
			}
			if core.IsNotFound(err) {
				return true, nil
			}
			return true, err
		},
		func() error {
			return fmt.Errorf("%w: %s: hosted zone %s was accepted; do not delete it again", ErrNotSettled, op, zoneID)
		},
	)
	if err != nil && !errors.Is(err, ErrNotSettled) {
		err = fmt.Errorf("%w: %s: hosted zone %s: %w", ErrNotSettled, op, zoneID, err)
	}
	return err
}

// equalStringSets reports whether a and b hold the same strings, regardless
// of order or of duplicates' positions; a nil slice and a non-nil empty one
// compare equal. UpdateHostedZone uses it to tell whether a settle read's
// VPC ids match the ones the update itself sent.
func equalStringSets(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	counts := make(map[string]int, len(a))
	for _, v := range a {
		counts[v]++
	}
	for _, v := range b {
		counts[v]--
	}
	for _, n := range counts {
		if n != 0 {
			return false
		}
	}
	return true
}
