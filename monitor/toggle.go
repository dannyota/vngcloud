package monitor

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/transport"
)

// ErrUnexpectedStatus means a check's Status was neither StatusEnabled nor
// StatusDisabled when PauseCheck or ResumeCheck read it. The SDK never
// sends the toggle for a status it does not understand.
var ErrUnexpectedStatus = errors.New("monitor: unexpected check status")

// ErrStatusUnconfirmed means the toggle was sent, or may have been, but no
// confirm read showed the target status before the reads ran out. The
// check may still reach the target on its own (a confirm read that lagged
// the toggle), or the toggle may still land later. The recovery differs by
// call and is never a rerun in a loop: after it from PauseCheck, the
// pre-toggle read already proved the check was StatusEnabled, so a caller
// treats the pause as its own and resumes it later; after it from
// ResumeCheck, that proof does not exist, so a caller stops and alerts a
// person instead of guessing.
var ErrStatusUnconfirmed = errors.New("monitor: check status not confirmed")

// confirmWaits are the delays between confirm reads after the toggle PUT:
// read at once, then after 1, 2, and 4 seconds, stopping at the first read
// whose Status is the target. Four reads bound the wait at about 7 seconds
// plus request time, per the design's ADR 0002 rule 7 accounting.
var confirmWaits = []time.Duration{0, time.Second, 2 * time.Second, 4 * time.Second}

// sleepFunc waits for d or ctx's end, whichever comes first, returning
// ctx.Err() when ctx ends first. Tests inject a fake one so the confirm
// waits above never really elapse.
type sleepFunc func(ctx context.Context, d time.Duration) error

// contextSleep is the real sleepFunc: an ordinary context-aware timer wait.
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

// PauseCheckInput identifies the check to pause.
type PauseCheckInput struct {
	CheckID string `vngcloud:"required"`
}

// PauseCheckOutput is the check after the call, and whether the call itself
// changed its status. Changed is false when the check was already
// StatusDisabled: a caller that wants to restore the prior state, such as a
// deploy resuming a check it paused, resumes only when Changed was true, so
// a check a person paused for maintenance stays paused.
type PauseCheckOutput struct {
	Check   Check
	Changed bool
}

// PauseCheck drives CheckID to StatusDisabled. It reads the check's current
// status first and sends the toggle only when the status is not already
// StatusDisabled; see the package doc and ADR 0003 for the full sequence.
func (c *Client) PauseCheck(ctx context.Context, in *PauseCheckInput) (*PauseCheckOutput, error) {
	const op = "monitor.PauseCheck"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "CheckID", in.CheckID); err != nil {
		return nil, err
	}
	check, changed, err := c.toggle(ctx, op, in.CheckID, StatusDisabled)
	if err != nil {
		return nil, err
	}
	return &PauseCheckOutput{Check: *check, Changed: changed}, nil
}

// ResumeCheckInput identifies the check to resume.
type ResumeCheckInput struct {
	CheckID string `vngcloud:"required"`
}

// ResumeCheckOutput is PauseCheckOutput's counterpart for ResumeCheck.
type ResumeCheckOutput struct {
	Check   Check
	Changed bool
}

// ResumeCheck drives CheckID to StatusEnabled. See PauseCheck; the two share
// one implementation with a different target status.
func (c *Client) ResumeCheck(ctx context.Context, in *ResumeCheckInput) (*ResumeCheckOutput, error) {
	const op = "monitor.ResumeCheck"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "CheckID", in.CheckID); err != nil {
		return nil, err
	}
	check, changed, err := c.toggle(ctx, op, in.CheckID, StatusEnabled)
	if err != nil {
		return nil, err
	}
	return &ResumeCheckOutput{Check: *check, Changed: changed}, nil
}

// toggle is PauseCheck and ResumeCheck's shared implementation, driving
// checkID to target. op names the caller, used as every request's
// Operation and in the ErrStatusUnconfirmed message. Callers have already
// checked checkID with core.CheckRequired and core.CheckPathID.
//
// toggleMu serializes this against every other toggle call on c: the design
// runs a Client's pause and resume calls one at a time, so two goroutines
// sharing one Client never race the read-then-toggle sequence against each
// other (see the design's discussion of the read-toggle race).
func (c *Client) toggle(ctx context.Context, op, checkID, target string) (*Check, bool, error) {
	c.toggleMu.Lock()
	defer c.toggleMu.Unlock()

	check, err := c.readCheck(ctx, op, checkID)
	if err != nil {
		return nil, false, err
	}
	if check.Status == target {
		return check, false, nil
	}
	if check.Status != StatusEnabled && check.Status != StatusDisabled {
		return nil, false, fmt.Errorf("%w: %s is %q", ErrUnexpectedStatus, checkID, truncateStatus(check.Status))
	}

	putErr := c.sendToggle(ctx, op, checkID)
	if putErr != nil && serverDidNotAct(putErr) {
		return nil, false, putErr
	}

	confirmed, lastStatus, readErr := c.confirmStatus(ctx, op, checkID, target)
	if confirmed != nil {
		return confirmed, true, nil
	}
	return nil, false, &statusUnconfirmedError{
		msg:     unconfirmedMessage(target, lastStatus),
		putErr:  putErr,
		readErr: readErr,
	}
}

// toggleOK lists every status the toggle PUT accepts: the design says any
// 2xx is accepted, and the API sends 204 today.
var toggleOK = []int{200, 201, 202, 203, 204, 205, 206}

// sendToggle sends the toggle PUT at most once (transport.Request.Once), per
// ADR 0003: any resend would act on the status toggle already read, which
// only grows staler with each attempt.
func (c *Client) sendToggle(ctx context.Context, op, checkID string) error {
	req := transport.Request{
		Operation: op,
		Method:    http.MethodPut,
		URL:       c.route([]string{"uptimes", "status", checkID}),
		OK:        toggleOK,
		Once:      true,
	}
	return c.c.DoJSON(ctx, req, nil)
}

// serverDidNotAct reports whether err from the toggle PUT proves the server
// never acted on it: a 4xx response (401, 403, 404, 409, and 429 included),
// or a failed dial. Both fail the call with err itself. Anything else,
// including a 5xx or a network error after the dial succeeded (timeout,
// reset, a canceled context), does not: the server may have acted, so the
// caller confirms by reading instead of trusting err.
func serverDidNotAct(err error) bool {
	var apiErr *core.APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	if apiErr.StatusCode >= 400 && apiErr.StatusCode < 500 {
		return true
	}
	// Retryable is true at StatusCode 0 only for a failed dial (see
	// transport.Request.Once's doc comment): a canceled context or any
	// other post-connect network failure leaves it false.
	return apiErr.StatusCode == 0 && apiErr.Retryable
}

// confirmStatus reads checkID after the toggle, waiting confirmWaits between
// attempts, stopping at the first read whose Status is target. It returns
// the confirming check, or nil with the last successfully read status (""
// if every read failed) and the last read's own error (nil if the last
// attempted read succeeded but did not show target).
func (c *Client) confirmStatus(ctx context.Context, op, checkID, target string) (confirmed *Check, lastStatus string, lastErr error) {
	for _, wait := range confirmWaits {
		if wait > 0 {
			if err := c.sleep(ctx, wait); err != nil {
				return nil, lastStatus, err
			}
		}
		check, err := c.readCheck(ctx, op, checkID)
		if err != nil {
			lastErr = err
			continue
		}
		lastErr = nil
		lastStatus = check.Status
		if check.Status == target {
			return check, lastStatus, nil
		}
	}
	return nil, lastStatus, lastErr
}

// readCheck is GetCheck's request, reused for the pre-toggle read and every
// confirm read, under op's name rather than "monitor.GetCheck" so every
// APIError names the PauseCheck or ResumeCheck call it happened inside.
func (c *Client) readCheck(ctx context.Context, op, checkID string) (*Check, error) {
	var check Check
	req := transport.Request{
		Operation: op,
		Method:    http.MethodGet,
		URL:       c.route([]string{"uptimes", checkID}),
		OK:        []int{200},
	}
	if err := c.c.DoJSON(ctx, req, &check); err != nil {
		return nil, err
	}
	return &check, nil
}

// statusUnconfirmedError is ErrStatusUnconfirmed's concrete type. Unwrap
// always includes ErrStatusUnconfirmed itself, so errors.Is(err,
// ErrStatusUnconfirmed) matches, and separately includes the toggle PUT's
// error and the last confirm read's error, when either exists, so a caller
// can still errors.As into either one, for example to inspect a wrapped
// *vngcloud.APIError's status.
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

// unconfirmedMessage builds ErrStatusUnconfirmed's message, for example
// "monitor: check status not confirmed: toggle sent or may have been sent,
// check still ENABLED; treat the pause as done and resume later". lastStatus
// is the last status a confirm read observed, or "" when every read failed.
// target picks the recovery text: a pause (target StatusDisabled) can be
// treated as done because the pre-toggle read proved the check was
// StatusEnabled; a resume (target StatusEnabled) has no such proof, so the
// recovery points at a person instead of telling the caller to rerun.
func unconfirmedMessage(target, lastStatus string) string {
	statusText := lastStatus
	if statusText == "" {
		statusText = "unknown"
	}
	recovery := "ask a person to check it"
	if target == StatusDisabled {
		recovery = "treat the pause as done and resume later"
	}
	return fmt.Sprintf("%s: toggle sent or may have been sent, check still %s; %s",
		ErrStatusUnconfirmed.Error(), statusText, recovery)
}

// truncateStatus caps an echoed status value at 64 bytes, so ErrUnexpectedStatus
// never repeats an oversized or abusive value back to the caller.
func truncateStatus(status string) string {
	const maxEchoLen = 64
	if len(status) <= maxEchoLen {
		return status
	}
	return status[:maxEchoLen]
}
