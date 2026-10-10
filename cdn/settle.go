package cdn

import (
	"context"
	"errors"
	"fmt"
	"time"

	"danny.vn/vngcloud/internal/core"
)

var (
	// ErrBusy means the CDN is changing status, so the server refuses a
	// write. Nothing changed; run the same call again after the CDN settles.
	ErrBusy = errors.New("cdn: CDN is changing status")

	// ErrUnexpectedStatus means a CDN has a status the SDK does not know
	// (not DISABLED, ACTIVE, DEPLOYING, DELETING, or DISABLING), or an
	// unexpected one during a wait. A write that finds one sends nothing.
	ErrUnexpectedStatus = errors.New("cdn: unexpected CDN status")

	// ErrNotSettled means the server accepted a write, but the CDN did not
	// reach its final status within the wait. The write must not be
	// repeated. The call's Output holds the last good read.
	ErrNotSettled = errors.New("cdn: CDN not settled")

	// ErrStatusUnconfirmed means an enable or disable was sent, or may have
	// been, but no read showed the CDN changing. Do not retry the call in a
	// loop: read the CDN first.
	ErrStatusUnconfirmed = errors.New("cdn: CDN status change not confirmed")
)

const (
	settleInterval = 10 * time.Second
	settleBound    = 6 * time.Minute
)

// confirmWaits are the delays before each confirm read after a toggle: at
// once, then after 2, 4, and 8 seconds.
var confirmWaits = []time.Duration{0, 2 * time.Second, 4 * time.Second, 8 * time.Second}

func (c *Client) sleeper() func(context.Context, time.Duration) error {
	if c.sleep != nil {
		return c.sleep
	}
	return contextSleep
}

func (c *Client) clock() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

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

// writeKind is the write a status guard decides for.
type writeKind int

const (
	kindUpdate writeKind = iota
	kindDelete
	kindEnable
	kindDisable
)

// guard applies the status table. It returns send false with a nil error
// for a toggle already on its target. Every refusal sends nothing.
func guard(op string, kind writeKind, status int) (send bool, err error) {
	switch status {
	case StatusDeploying, StatusDisabling, StatusDeleting:
		return false, fmt.Errorf("%w: %s: the CDN is %s; nothing was sent, run the call again after it settles",
			ErrBusy, op, StatusName(status))
	case StatusActive:
		return kind != kindEnable, nil
	case StatusDisabled:
		switch kind {
		case kindUpdate:
			return false, fmt.Errorf("%w: %s: the CDN is DISABLED; enable it first", core.ErrInvalidInput, op)
		case kindDisable:
			return false, nil
		}
		return true, nil
	}
	return false, fmt.Errorf("%w: %s: the CDN has status %d; nothing was sent", ErrUnexpectedStatus, op, status)
}

// settleTarget is the pending and final status of a write.
type settleTarget struct{ pending, settled int }

var (
	settleActive   = settleTarget{StatusDeploying, StatusActive}
	settleDisabled = settleTarget{StatusDisabling, StatusDisabled}
)

// notSettledError is ErrNotSettled with the cause that ended the wait.
type notSettledError struct {
	msg   string
	cause error
}

func (e *notSettledError) Error() string { return e.msg }

func (e *notSettledError) Unwrap() []error {
	if e.cause == nil {
		return []error{ErrNotSettled}
	}
	return []error{ErrNotSettled, e.cause}
}

func notSettled(op string, last *WebAccelerator, cause error) error {
	state := "unknown"
	if last != nil {
		state = StatusName(last.Status)
	}
	return &notSettledError{
		msg: fmt.Sprintf("%s: the CDN is not settled (last seen %s) but the server accepted the write; do not repeat it, read the CDN later",
			op, state),
		cause: cause,
	}
}

// settle reads the CDN until it reaches t.settled, starting with cur when a
// read is already in hand and with a fresh read when cur is nil. deadline is
// measured from the write response. It returns the last good read with every
// error.
func (c *Client) settle(ctx context.Context, op, cdnID string, t settleTarget, deadline time.Time, cur, last *WebAccelerator) (*WebAccelerator, error) {
	var readErr error
	for {
		if cur == nil {
			wa, err := c.detailByDeadline(ctx, op, cdnID, deadline)
			switch {
			case err == nil:
				cur, last, readErr = wa, wa, nil
			case errors.Is(err, core.ErrNotFound):
				return last, err
			case ctx.Err() != nil:
				return last, notSettled(op, last, ctx.Err())
			default:
				readErr = err
			}
		}
		if cur != nil {
			switch cur.Status {
			case t.settled:
				return cur, nil
			case t.pending:
			default:
				return cur, fmt.Errorf("%w: %s: the CDN went to %s while waiting for %s",
					ErrUnexpectedStatus, op, StatusName(cur.Status), StatusName(t.settled))
			}
		}
		now := c.clock()
		if !now.Before(deadline) {
			return last, notSettled(op, last, readErr)
		}
		wait := min(settleInterval, deadline.Sub(now))
		if err := c.sleeper()(ctx, wait); err != nil {
			return last, notSettled(op, last, err)
		}
		cur = nil
	}
}

func (c *Client) detailByDeadline(ctx context.Context, op, cdnID string, deadline time.Time) (*WebAccelerator, error) {
	remaining := deadline.Sub(c.clock())
	if remaining <= 0 {
		return nil, context.DeadlineExceeded
	}
	readCtx, cancel := context.WithTimeout(ctx, remaining)
	defer cancel()
	wa, err := c.detail(readCtx, op, cdnID)
	if !c.clock().Before(deadline) {
		return nil, context.DeadlineExceeded
	}
	return wa, err
}

// detail is a detail read that drops the raw object.
func (c *Client) detail(ctx context.Context, op, cdnID string) (*WebAccelerator, error) {
	_, wa, err := c.readCDN(ctx, op, cdnID)
	return wa, err
}

// maybeLanded marks the error of a write that may have reached the server
// before it failed: a 5xx, a network failure after the connection, or a
// body that was not the envelope.
func maybeLanded(err error, what string) error {
	var apiErr *core.APIError
	if !errors.As(err, &apiErr) {
		return err
	}
	if apiErr.StatusCode >= 500 || apiErr.Code == codeEmptyResponse || (apiErr.StatusCode == 0 && !apiErr.Retryable) {
		return fmt.Errorf("%w; the %s may have been applied: read the CDN before running it again", err, what)
	}
	return err
}
