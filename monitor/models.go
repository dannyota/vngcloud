package monitor

import (
	"encoding/json"
	"errors"
	"math"
)

// StatusEnabled and StatusDisabled are the two Check.Status values
// PauseCheck and ResumeCheck understand. A Status the SDK does not
// recognize is returned to the caller as is; PauseCheck and ResumeCheck
// refuse to toggle it rather than guess what it means.
const (
	StatusEnabled  = "ENABLED"
	StatusDisabled = "DISABLED"
)

// Check is one vMonitor synthetic check. The API also sends user_id,
// monitor_status, history_alarm, deleted_at, and notifications; the SDK
// drops them, the same way it drops any field a caller's struct omits.
// Notifications is left out until notification channels are designed:
// adding a Notifications field later breaks no caller. CreatedAt and
// UpdatedAt hold whatever the API sends, a formatted string such as "Sep
// 26, 2026, 7:56:28 AM" rather than an ISO 8601 timestamp; the SDK does not
// parse them.
type Check struct {
	ID        string       `json:"id"`
	Name      string       `json:"name"`
	Type      string       `json:"type"`
	Subtype   string       `json:"subtype"`
	Status    string       `json:"status"`
	Config    CheckConfig  `json:"config"`
	Options   CheckOptions `json:"options"`
	Locations []string     `json:"locations"`
	CreatedAt string       `json:"created_at"`
	UpdatedAt string       `json:"updated_at"`
}

// CheckConfig holds the request a check sends and the assertions it
// evaluates on the response.
type CheckConfig struct {
	Request    CheckRequest `json:"request"`
	Assertions []Assertion  `json:"assertions"`
}

// CheckRequest is the HTTP request a check sends on every run. Headers and
// Query decode as string-valued maps; the live capture behind this design
// has only shown them empty, so a header or query parameter sent with more
// than one value, if the API allows one, is not yet confirmed to decode.
type CheckRequest struct {
	URL     string            `json:"url"`
	Method  string            `json:"method"`
	Headers map[string]string `json:"headers"`
	Query   map[string]string `json:"query"`
	Body    string            `json:"body"`
	// Timeout is in seconds; CreateCheck sends 10, the console's own
	// default, when the caller leaves it zero. The API sends it as an
	// integral decimal, such as 10.0, alongside CheckOptions' three fields;
	// UnmarshalJSON accepts either shape.
	Timeout     int  `json:"timeout"`
	VerifiedSSL bool `json:"verified_ssl"`
}

// UnmarshalJSON decodes CheckRequest with Timeout routed through
// flexibleInt, so the API's integral-decimal shape lands in the int field.
func (r *CheckRequest) UnmarshalJSON(data []byte) error {
	type alias CheckRequest
	aux := struct {
		Timeout flexibleInt `json:"timeout"`
		*alias
	}{alias: (*alias)(r)}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	r.Timeout = int(aux.Timeout)
	return nil
}

// Assertion is one pass or fail rule a check evaluates against its
// response. The console's default checks the response status is not a 4xx
// or 5xx: Type "status_code", Operator "does_not_match_regex", Target
// "[4-5][0-9][0-9]".
type Assertion struct {
	Type     string `json:"type"`
	Operator string `json:"operator"`
	Target   string `json:"target"`
}

// CheckOptions controls how often and where a check runs, and how many
// failing locations mark it down. The API sends all three fields as an
// integral decimal, such as 60.0; UnmarshalJSON accepts either shape.
type CheckOptions struct {
	// TestFrequency is in minutes.
	TestFrequency   int `json:"test_frequency"`
	Tests           int `json:"tests"`
	FailedLocations int `json:"failed_locations"`
}

// UnmarshalJSON decodes CheckOptions with every field routed through
// flexibleInt.
func (o *CheckOptions) UnmarshalJSON(data []byte) error {
	type alias CheckOptions
	aux := struct {
		TestFrequency   flexibleInt `json:"test_frequency"`
		Tests           flexibleInt `json:"tests"`
		FailedLocations flexibleInt `json:"failed_locations"`
		*alias
	}{alias: (*alias)(o)}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	o.TestFrequency = int(aux.TestFrequency)
	o.Tests = int(aux.Tests)
	o.FailedLocations = int(aux.FailedLocations)
	return nil
}

// Location is a probe location ListLocations can return. The API also
// sends api_key, user_id, uptimes, and deleted_at; the SDK drops them, the
// same way it drops any field a caller's struct omits. Both locations seen
// so far have Type PUBLIC.
type Location struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Type        string `json:"type"`
	Description string `json:"description"`
	Status      string `json:"status"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

// flexibleInt decodes a JSON number the API sends as a plain integer in
// some responses and as an integral decimal, such as 30.0, in others. A
// fractional value is rejected rather than truncated.
type flexibleInt int

func (n *flexibleInt) UnmarshalJSON(data []byte) error {
	var f float64
	if err := json.Unmarshal(data, &f); err != nil {
		return err
	}
	if f != math.Trunc(f) {
		return errors.New("monitor: value is not an integer")
	}
	*n = flexibleInt(f)
	return nil
}
