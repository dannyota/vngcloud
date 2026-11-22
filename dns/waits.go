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
	// StatusCreating is the "CREATING" DNS resource status.
	StatusCreating = "CREATING"
	// StatusActive is the "ACTIVE" DNS resource status.
	StatusActive = "ACTIVE"
	// StatusUpdating is the "UPDATING" DNS resource status.
	StatusUpdating = "UPDATING"
	// StatusError is the "ERROR" DNS resource status.
	StatusError = "ERROR"
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

func poll(ctx context.Context, now clockFunc, sleep sleepFunc, step func(ctx context.Context) (stop bool, err error), onTimeout func() error) error {
	return pollWithBound(ctx, pollBound, now, sleep, step, onTimeout)
}

func pollWithBound(ctx context.Context, bound time.Duration, now clockFunc, sleep sleepFunc, step func(ctx context.Context) (stop bool, err error), onTimeout func() error) error {
	parent := ctx
	ctx, cancel := context.WithTimeout(ctx, bound)
	defer cancel()
	deadline := now().Add(bound)
	checkDeadline := func() error {
		if err := parent.Err(); err != nil {
			return err
		}
		if ctx.Err() != nil || !now().Before(deadline) {
			return onTimeout()
		}
		return nil
	}
	for {
		if err := checkDeadline(); err != nil {
			return err
		}
		stop, err := step(ctx)
		if deadlineErr := checkDeadline(); deadlineErr != nil {
			return deadlineErr
		}
		if stop {
			return err
		}
		delay := min(pollInterval, deadline.Sub(now()))
		if err := sleep(ctx, delay); err != nil {
			if deadlineErr := checkDeadline(); deadlineErr != nil {
				return deadlineErr
			}
			return err
		}
	}
}

func (c *Client) getHostedZone(ctx context.Context, op, zoneID string) (*HostedZone, error) {
	type fields HostedZone
	var zone fields
	var resp struct {
		Data *struct {
			*fields
			Description *string   `json:"description"`
			VPCIDs      *[]string `json:"assocVpcIds"`
		} `json:"data"`
	}
	resp.Data = &struct {
		*fields
		Description *string   `json:"description"`
		VPCIDs      *[]string `json:"assocVpcIds"`
	}{fields: &zone}
	if err := c.c.DoJSON(ctx, transport.Request{
		Operation: op, Method: http.MethodGet,
		URL: c.url([]string{"dns", "hosted-zone", zoneID}, nil), OK: []int{200},
		StrictJSON: true, MaxBody: 256 << 10,
	}, &resp); err != nil {
		return nil, err
	}
	if resp.Data == nil || zone.ID != zoneID || zone.Status == "" {
		return nil, fmt.Errorf("%s: hosted zone identity or state is unproven", op)
	}
	if op == "dns.UpdateHostedZone" && (resp.Data.Description == nil || resp.Data.VPCIDs == nil) {
		return nil, fmt.Errorf("%s: incomplete hosted zone replacement snapshot", op)
	}
	if resp.Data.Description != nil {
		zone.Description = *resp.Data.Description
	}
	if resp.Data.VPCIDs != nil {
		zone.AssociatedVPCIDs = *resp.Data.VPCIDs
	}
	out := HostedZone(zone)
	return &out, nil
}

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
			return fmt.Errorf("%w: %s: hosted zone %s was not confirmed ready within %s", ErrZoneBusy, op, zoneID, pollBound)
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

// wrapAmbiguousCreateErr wraps err, from the create POST op just sent, with
// a hint to list before creating again, unless err is already a 4xx
// *core.APIError: a 4xx means the server rejected the request outright, so
// nothing was created and the exact same call is safe to retry. Any other
// error, a 5xx or a failure before any response ever came back, such as a
// canceled ctx or a dropped connection, leaves whether the resource was
// created unknown. Wrapping with %w keeps errors.Is and errors.As reaching
// err's own cause; a nil err stays nil.
func wrapAmbiguousCreateErr(op string, err error) error {
	if err == nil {
		return nil
	}
	var apiErr *core.APIError
	if errors.As(err, &apiErr) && apiErr.StatusCode >= 400 && apiErr.StatusCode < 500 {
		return err
	}
	return fmt.Errorf("%s: create may have already reached the server; list before creating it again: %w", op, err)
}
