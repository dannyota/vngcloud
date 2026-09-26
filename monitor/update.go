package monitor

import (
	"context"
	"fmt"
	"net/http"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/transport"
)

// UpdateCheckInput changes a check identified by CheckID. Every other field
// left nil keeps the check's current value; at least one of them must be
// set. The API takes a full replacement body and has no per-field patch, so
// UpdateCheck reads the check first with GetCheck and resends every field
// the caller left nil unchanged, the same read-merge shape UpdateChannel
// uses for a channel.
//
// The PUT carries no status field, and leaves a check's status exactly as
// it was: pausing or resuming a check stays PauseCheck and ResumeCheck's
// job alone. UpdateCheck accepts a check in any status, including
// StatusDisabled, and never touches it.
type UpdateCheckInput struct {
	CheckID string `vngcloud:"required"`

	Name            *string
	URL             *string
	Method          *string
	Headers         *map[string]string
	Query           *map[string]string
	Body            *string
	Timeout         *int
	TestFrequency   *int
	Tests           *int
	FailedLocations *int
	Locations       *[]string
	Assertions      *[]Assertion

	// Notifications, when set, replaces all three of the check's
	// notification lists at once: In-alarm, Up, and Undetermined together,
	// not just the one the caller means to change. A caller that wants to
	// add or remove a channel on one list reads the check first with
	// GetCheck and sends back its full Notifications with that one list
	// changed, the same way any other field here is merged.
	Notifications *CheckNotifications
}

type UpdateCheckOutput struct {
	Check Check
}

// UpdateCheck changes a check. It holds the same mutex PauseCheck and
// ResumeCheck do, so a pause, a resume, and an update against the same
// Client never interleave their own read-then-write sequences within one
// process. Across processes, or across two Client values, the API has no
// version field, so whichever of two concurrent updates lands last wins,
// and either one can silently lose the other's change.
//
// The PUT is idempotent, since resending the same full replacement body is
// safe, so it keeps the transport's normal retries, unlike CreateCheck's
// POST. UpdateCheck returns the check the PUT's own 200 response carries;
// if that response ever holds no check (an empty body, or one with no id),
// it reads the check once with GetCheck instead of failing the call, since
// nothing about a full-replacement PUT makes the write itself uncertain the
// way a create's missing id does.
//
// The pre-merge read is different: if it returns 200 with no usable check
// (an empty body, "null", or a shape Check does not decode into its ID),
// merging into that zero value would send a PUT that clears the check, so
// UpdateCheck refuses and sends no PUT instead. It also refuses a check
// whose current Type or Subtype is not API/HTTP, since checkWriteBody only
// carries an HTTP request and resending it would silently rewrite the
// check's type.
func (c *Client) UpdateCheck(ctx context.Context, in *UpdateCheckInput) (*UpdateCheckOutput, error) {
	const op = "monitor.UpdateCheck"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "CheckID", in.CheckID); err != nil {
		return nil, err
	}
	if in.Name == nil && in.URL == nil && in.Method == nil && in.Headers == nil &&
		in.Query == nil && in.Body == nil && in.Timeout == nil && in.TestFrequency == nil &&
		in.Tests == nil && in.FailedLocations == nil && in.Locations == nil &&
		in.Assertions == nil && in.Notifications == nil {
		return nil, fmt.Errorf("%w: %s requires at least one field to change", core.ErrInvalidInput, op)
	}
	// A non-nil Locations means the caller wants to replace the check's
	// locations; an empty one would clear every location the check runs
	// from, the same as CreateCheck refuses an empty Locations outright.
	if in.Locations != nil && len(*in.Locations) == 0 {
		return nil, fmt.Errorf("%w: %s Locations, if set, requires at least one entry", core.ErrInvalidInput, op)
	}

	c.toggleMu.Lock()
	defer c.toggleMu.Unlock()

	current, err := c.readCheck(ctx, op, in.CheckID)
	if err != nil {
		return nil, err
	}
	// A 200 that decodes to no usable check (an empty body, "null", or a
	// wrapped shape the SDK's Check does not match) leaves current
	// zero-valued. Merging into that would send the full-replace PUT with
	// every field cleared, wiping the check instead of updating it.
	if current.ID != in.CheckID {
		return nil, &core.APIError{
			Operation:  op,
			StatusCode: 200,
			Message:    "pre-update read returned no usable check; refusing to overwrite it",
		}
	}
	// checkWriteBody only carries an HTTP request, so a check whose current
	// Type or Subtype is not API/HTTP cannot be resent through it without
	// silently rewriting the check to a type it never was.
	if current.Type != checkType || current.Subtype != checkSubtype {
		return nil, fmt.Errorf("%w: %s cannot update a check with type %q and subtype %q; only %s/%s checks are supported",
			core.ErrInvalidInput, op, current.Type, current.Subtype, checkType, checkSubtype)
	}

	name := current.Name
	if in.Name != nil {
		name = *in.Name
	}
	requestURL := current.Config.Request.URL
	if in.URL != nil {
		requestURL = *in.URL
	}
	method := current.Config.Request.Method
	if in.Method != nil {
		method = *in.Method
	}
	headers := current.Config.Request.Headers
	if in.Headers != nil {
		headers = *in.Headers
	}
	if headers == nil {
		headers = map[string]string{}
	}
	query := current.Config.Request.Query
	if in.Query != nil {
		query = *in.Query
	}
	if query == nil {
		query = map[string]string{}
	}
	requestBody := current.Config.Request.Body
	if in.Body != nil {
		requestBody = *in.Body
	}
	timeout := current.Config.Request.Timeout
	if in.Timeout != nil {
		timeout = *in.Timeout
	}
	testFrequency := current.Options.TestFrequency
	if in.TestFrequency != nil {
		testFrequency = *in.TestFrequency
	}
	tests := current.Options.Tests
	if in.Tests != nil {
		tests = *in.Tests
	}
	failedLocations := current.Options.FailedLocations
	if in.FailedLocations != nil {
		failedLocations = *in.FailedLocations
	}
	locations := current.Locations
	if in.Locations != nil {
		locations = *in.Locations
	}
	if locations == nil {
		locations = []string{}
	}
	assertions := current.Config.Assertions
	if in.Assertions != nil {
		assertions = *in.Assertions
	}
	if assertions == nil {
		assertions = []Assertion{}
	}
	notifications := current.Notifications
	if in.Notifications != nil {
		notifications = *in.Notifications
	}

	body := checkWriteBody{
		Type:    checkType,
		Subtype: checkSubtype,
		Name:    name,
		Config: CheckConfig{
			Request: CheckRequest{
				URL:         requestURL,
				Method:      method,
				Headers:     headers,
				Query:       query,
				Body:        requestBody,
				Timeout:     timeout,
				VerifiedSSL: true,
			},
			Assertions: assertions,
		},
		Options: CheckOptions{
			TestFrequency:   testFrequency,
			Tests:           tests,
			FailedLocations: failedLocations,
		},
		Locations:     locations,
		Notifications: normalizeNotifications(notifications),
	}

	var check Check
	req := transport.Request{
		Operation: op,
		Method:    http.MethodPut,
		URL:       c.route([]string{"uptimes", in.CheckID}),
		Body:      body,
		OK:        []int{200},
	}
	if err := c.c.DoJSON(ctx, req, &check); err != nil {
		return nil, err
	}
	if check.ID == "" {
		reread, err := c.readCheck(ctx, op, in.CheckID)
		if err != nil {
			return nil, err
		}
		return &UpdateCheckOutput{Check: *reread}, nil
	}
	return &UpdateCheckOutput{Check: check}, nil
}
