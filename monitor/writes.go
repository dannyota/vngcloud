package monitor

import (
	"context"
	"fmt"
	"net/http"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/transport"
)

// checkType and checkSubtype are the only values the console offers today;
// the SDK always sends them on create.
const (
	checkType    = "API"
	checkSubtype = "HTTP"
)

// Console create-form defaults for CreateCheckInput's zero-value fields,
// read from the create form on 2026-09-26 without submitting it. The form
// has no client-side maximum for any of these fields; the server enforces
// whatever range applies.
const (
	defaultTimeout       = 10 // seconds
	defaultTestFrequency = 1  // minutes
	defaultTests         = 1
)

// defaultAssertion is the console's own default assertion, sent when the
// caller leaves Assertions empty: fail the check on a 4xx or 5xx response.
var defaultAssertion = Assertion{Type: "status_code", Operator: "does_not_match_regex", Target: "[4-5][0-9][0-9]"}

// normalizeNotifications returns n with every nil channel ID list replaced
// by a non-nil empty one, so a request body always encodes each of the
// three keys as [] rather than null: CreateCheck for a caller that leaves
// Notifications at its zero value, and UpdateCheck for a check whose
// current Notifications the caller does not change.
func normalizeNotifications(n CheckNotifications) CheckNotifications {
	if n.InAlarm == nil {
		n.InAlarm = []string{}
	}
	if n.Up == nil {
		n.Up = []string{}
	}
	if n.Undetermined == nil {
		n.Undetermined = []string{}
	}
	return n
}

// checkWriteBody is the request body CreateCheck's POST and UpdateCheck's PUT
// both send: the full config, options, locations, and notifications that
// together describe a check. It carries no status field; the API's PUT
// leaves a check's status exactly as it was, so UpdateCheck never sends
// one, and pausing or resuming a check stays PauseCheck and ResumeCheck's
// job alone. Config and Options reuse CheckConfig and CheckOptions: both
// already carry the create request's own JSON tags, and neither type's
// custom UnmarshalJSON affects how it marshals.
type checkWriteBody struct {
	Type          string             `json:"type"`
	Subtype       string             `json:"subtype"`
	Name          string             `json:"name"`
	Config        CheckConfig        `json:"config"`
	Options       CheckOptions       `json:"options"`
	Locations     []string           `json:"locations"`
	Notifications CheckNotifications `json:"notifications"`
}

// CreateCheckInput creates a check. Method empty sends GET; Headers and
// Query nil send what the console sends for none (an empty JSON object);
// Body empty sends what the console sends for none (an empty string).
// Timeout, TestFrequency, and Tests zero send the console's own defaults.
// FailedLocations zero sends len(Locations): the console's own default is
// every selected location must fail. Assertions empty sends the console's
// default assertion. Notifications left at its zero value sends an empty
// list for In-alarm, Up, and Undetermined alike, so a created check with no
// Notifications set alerts nobody, the same as before this field existed.
// Zero is never a valid value for any of these fields, so none needs a
// pointer (ADR 0002 rule 3).
//
// The server checks the name pattern, the frequency range, and that every
// location is a known UUID; the SDK only checks that the required fields
// are set (ADR 0002 rule 5).
type CreateCheckInput struct {
	Name      string   `vngcloud:"required"`
	URL       string   `vngcloud:"required"`
	Locations []string `vngcloud:"required"`

	Method          string
	Headers         map[string]string
	Query           map[string]string
	Body            string
	Timeout         int
	TestFrequency   int
	Tests           int
	FailedLocations int
	Assertions      []Assertion
	Notifications   CheckNotifications
}

type CreateCheckOutput struct {
	Check Check
}

// CreateCheck creates a check with type API and subtype HTTP, the only
// values the console offers today, and verified_ssl always true. It is not
// retried after a failure that may have already reached the server,
// because a POST is not idempotent. After any error that is not a 4xx
// *core.APIError or core.ErrInvalidInput, the check may exist: that covers
// a 5xx, a network error, a 201 with no id, and a body the SDK could not
// decode. The caller lists checks and looks for its name before trying
// again.
func (c *Client) CreateCheck(ctx context.Context, in *CreateCheckInput) (*CreateCheckOutput, error) {
	const op = "monitor.CreateCheck"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	// CheckRequired treats a non-nil empty slice as set, so an empty but
	// non-nil Locations passes it; check its length here too.
	if len(in.Locations) == 0 {
		return nil, fmt.Errorf("%w: %s requires at least one Locations entry", core.ErrInvalidInput, op)
	}

	method := in.Method
	if method == "" {
		method = http.MethodGet
	}
	headers := in.Headers
	if headers == nil {
		headers = map[string]string{}
	}
	query := in.Query
	if query == nil {
		query = map[string]string{}
	}
	timeout := in.Timeout
	if timeout == 0 {
		timeout = defaultTimeout
	}
	testFrequency := in.TestFrequency
	if testFrequency == 0 {
		testFrequency = defaultTestFrequency
	}
	tests := in.Tests
	if tests == 0 {
		tests = defaultTests
	}
	failedLocations := in.FailedLocations
	if failedLocations == 0 {
		failedLocations = len(in.Locations)
	}
	assertions := in.Assertions
	if len(assertions) == 0 {
		assertions = []Assertion{defaultAssertion}
	}

	body := checkWriteBody{
		Type:    checkType,
		Subtype: checkSubtype,
		Name:    in.Name,
		Config: CheckConfig{
			Request: CheckRequest{
				URL:         in.URL,
				Method:      method,
				Headers:     headers,
				Query:       query,
				Body:        in.Body,
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
		Locations:     in.Locations,
		Notifications: normalizeNotifications(in.Notifications),
	}

	var check Check
	req := transport.Request{
		Operation: op,
		Method:    http.MethodPost,
		URL:       c.route([]string{"uptimes"}),
		Body:      body,
		OK:        []int{201},
	}
	status, err := c.c.DoJSONStatus(ctx, req, &check)
	if err != nil {
		return nil, err
	}
	if check.ID == "" {
		return nil, &core.APIError{Operation: op, StatusCode: status, Message: "create response had no id"}
	}
	return &CreateCheckOutput{Check: check}, nil
}

// DeleteCheckInput identifies the check to delete.
type DeleteCheckInput struct {
	CheckID string `vngcloud:"required"`
}

type DeleteCheckOutput struct{}

// DeleteCheck deletes a check and its history. DELETE is idempotent, so the
// transport retries it as a read: a retry that finds the check already gone
// returns NotFound.
func (c *Client) DeleteCheck(ctx context.Context, in *DeleteCheckInput) (*DeleteCheckOutput, error) {
	const op = "monitor.DeleteCheck"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "CheckID", in.CheckID); err != nil {
		return nil, err
	}

	req := transport.Request{
		Operation: op,
		Method:    http.MethodDelete,
		URL:       c.route([]string{"uptimes", in.CheckID}),
		OK:        []int{204},
	}
	if err := c.c.DoJSON(ctx, req, nil); err != nil {
		return nil, err
	}
	return &DeleteCheckOutput{}, nil
}
