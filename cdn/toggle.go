package cdn

import (
	"context"
	"errors"
	"net/http"

	"danny.vn/vngcloud/internal/core"
)

// EnableWebAcceleratorInput identifies the CDN to enable. NoWait returns
// after one read of the CDN instead of waiting for it to become ACTIVE.
type EnableWebAcceleratorInput struct {
	CDNID  string `vngcloud:"required"`
	NoWait bool
}

// EnableWebAcceleratorOutput is the CDN after the call. Changed is false
// when the CDN was already ACTIVE and nothing was sent.
type EnableWebAcceleratorOutput struct {
	WebAccelerator WebAccelerator
	Changed        bool
}

// EnableWebAccelerator drives a disabled CDN to ACTIVE. It reads the CDN
// first and sends the toggle only when the CDN is DISABLED; a CDN that is
// DEPLOYING or DISABLING gives ErrBusy. The toggle is sent at most once
// (ADR 0003). Unless NoWait is set, the call waits up to six minutes for
// ACTIVE; on ErrNotSettled the Output is the last good read, and the enable
// must not be repeated.
func (c *Client) EnableWebAccelerator(ctx context.Context, in *EnableWebAcceleratorInput) (*EnableWebAcceleratorOutput, error) {
	const op = "cdn.EnableWebAccelerator"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "CDNID", in.CDNID); err != nil {
		return nil, err
	}
	wa, changed, err := c.toggle(ctx, op, in.CDNID, kindEnable, in.NoWait)
	if wa == nil {
		return nil, err
	}
	return &EnableWebAcceleratorOutput{WebAccelerator: *wa, Changed: changed}, err
}

// DisableWebAcceleratorInput identifies the CDN to disable. NoWait returns
// after one read of the CDN instead of waiting for it to become DISABLED.
type DisableWebAcceleratorInput struct {
	CDNID  string `vngcloud:"required"`
	NoWait bool
}

// DisableWebAcceleratorOutput is the CDN after the call. Changed is false
// when the CDN was already DISABLED and nothing was sent.
type DisableWebAcceleratorOutput struct {
	WebAccelerator WebAccelerator
	Changed        bool
}

// DisableWebAccelerator drives an active CDN to DISABLED, as
// EnableWebAccelerator drives it to ACTIVE. The CDN passes through
// DISABLING for about four minutes.
func (c *Client) DisableWebAccelerator(ctx context.Context, in *DisableWebAcceleratorInput) (*DisableWebAcceleratorOutput, error) {
	const op = "cdn.DisableWebAccelerator"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "CDNID", in.CDNID); err != nil {
		return nil, err
	}
	wa, changed, err := c.toggle(ctx, op, in.CDNID, kindDisable, in.NoWait)
	if wa == nil {
		return nil, err
	}
	return &DisableWebAcceleratorOutput{WebAccelerator: *wa, Changed: changed}, err
}

// toggle drives the CDN to the target of kind (kindEnable or kindDisable).
// It returns the last good read with any error after the toggle was sent.
func (c *Client) toggle(ctx context.Context, op, cdnID string, kind writeKind, noWait bool) (*WebAccelerator, bool, error) {
	target := settleActive
	if kind == kindDisable {
		target = settleDisabled
	}
	_, wa, err := c.readCDN(ctx, op, cdnID)
	if err != nil {
		return nil, false, err
	}
	send, err := guard(op, kind, wa.Status)
	if err != nil {
		return nil, false, err
	}
	if !send {
		return wa, false, nil
	}

	r := call{op: op, method: http.MethodPut, parts: []string{"cdn", "status", "change", cdnID}, once: true}
	_, putErr := c.do(ctx, r)
	if putErr != nil && serverDidNotAct(putErr) {
		return nil, false, c.explainRefusedToggle(ctx, op, cdnID, putErr)
	}

	confirmed, readErr := c.confirm(ctx, op, cdnID, target)
	if confirmed == nil {
		return nil, false, &statusUnconfirmedError{
			msg:    ErrStatusUnconfirmed.Error() + ": the toggle was sent, or may have been sent, but no read showed the CDN changing; read the CDN before doing anything else",
			putErr: putErr, readErr: readErr,
		}
	}
	if noWait {
		return confirmed, true, nil
	}
	out, err := c.settle(ctx, op, cdnID, target, confirmed)
	return out, true, err
}

// explainRefusedToggle returns err, except for a 401. The server answers a
// toggle on a CDN that no longer exists with a 401 and an empty body, the
// same as a rejected key. The read before the toggle proved the key works, so
// a second read tells the two apart: when the CDN is gone, the NotFound from
// that read replaces the misleading key message.
func (c *Client) explainRefusedToggle(ctx context.Context, op, cdnID string, err error) error {
	var apiErr *core.APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusUnauthorized {
		return err
	}
	if _, rerr := c.detail(ctx, op, cdnID); errors.Is(rerr, core.ErrNotFound) {
		return rerr
	}
	return err
}

// confirm reads the CDN at once and after 2, 4, and 8 seconds, stopping at
// the first read that shows the pending or the final status. It returns nil
// when none does, with the last read error.
func (c *Client) confirm(ctx context.Context, op, cdnID string, t settleTarget) (*WebAccelerator, error) {
	var lastErr error
	for _, wait := range confirmWaits {
		if wait > 0 {
			if err := c.sleeper()(ctx, wait); err != nil {
				return nil, err
			}
		}
		wa, err := c.detail(ctx, op, cdnID)
		if err != nil {
			lastErr = err
			continue
		}
		lastErr = nil
		if wa.Status == t.pending || wa.Status == t.settled {
			return wa, nil
		}
	}
	return nil, lastErr
}

// serverDidNotAct reports whether err from a write proves the server did
// not act: a 4xx, an envelope failure, or a failed dial. A 5xx, a network
// error after the dial, or a body that was not the envelope may have
// reached the handler.
func serverDidNotAct(err error) bool {
	var apiErr *core.APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	switch {
	case apiErr.StatusCode >= 400 && apiErr.StatusCode < 500:
		return true
	case apiErr.StatusCode >= 200 && apiErr.StatusCode < 300:
		return apiErr.Code != codeEmptyResponse
	}
	return apiErr.StatusCode == 0 && apiErr.Retryable
}

// statusUnconfirmedError is ErrStatusUnconfirmed with the toggle error and
// the last read error, so a caller can still reach either.
type statusUnconfirmedError struct {
	msg     string
	putErr  error
	readErr error
}

func (e *statusUnconfirmedError) Error() string { return e.msg }

func (e *statusUnconfirmedError) Unwrap() []error {
	wrapped := []error{ErrStatusUnconfirmed}
	if e.putErr != nil {
		wrapped = append(wrapped, e.putErr)
	}
	if e.readErr != nil {
		wrapped = append(wrapped, e.readErr)
	}
	return wrapped
}

// DeleteWebAcceleratorInput identifies the CDN to delete.
type DeleteWebAcceleratorInput struct {
	CDNID string `vngcloud:"required"`
}

type DeleteWebAcceleratorOutput struct{}

// DeleteWebAccelerator deletes a CDN that is ACTIVE or DISABLED. It reads
// the CDN first, sends the delete once, and does not wait: the CDN is gone
// from the next read. A deleted CDN loses its generated CDNDomain, which the
// customer's DNS points at. A CDN that is DEPLOYING or DISABLING gives
// ErrBusy. An unknown ID gives ErrNotFound.
func (c *Client) DeleteWebAccelerator(ctx context.Context, in *DeleteWebAcceleratorInput) (*DeleteWebAcceleratorOutput, error) {
	const op = "cdn.DeleteWebAccelerator"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "CDNID", in.CDNID); err != nil {
		return nil, err
	}
	_, wa, err := c.readCDN(ctx, op, in.CDNID)
	if err != nil {
		return nil, err
	}
	if _, err := guard(op, kindDelete, wa.Status); err != nil {
		return nil, err
	}
	r := call{op: op, method: http.MethodDelete, parts: []string{"cdn", "delete", in.CDNID}, once: true}
	if _, err := c.do(ctx, r); err != nil {
		return nil, maybeLanded(err, "delete")
	}
	return &DeleteWebAcceleratorOutput{}, nil
}
