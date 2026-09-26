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
// monitor_status, history_alarm, and deleted_at; the SDK drops them, the
// same way it drops any field a caller's struct omits. CreatedAt and
// UpdatedAt hold whatever the API sends, a formatted string such as "Sep
// 26, 2026, 7:56:28 AM" rather than an ISO 8601 timestamp; the SDK does not
// parse them.
type Check struct {
	ID            string             `json:"id"`
	Name          string             `json:"name"`
	Type          string             `json:"type"`
	Subtype       string             `json:"subtype"`
	Status        string             `json:"status"`
	Config        CheckConfig        `json:"config"`
	Options       CheckOptions       `json:"options"`
	Locations     []string           `json:"locations"`
	Notifications CheckNotifications `json:"notifications"`
	CreatedAt     string             `json:"created_at"`
	UpdatedAt     string             `json:"updated_at"`
}

// CheckNotifications names, by ID, which Channels a check alerts on each
// alarm transition. CreateCheck sends [] for every list left nil.
type CheckNotifications struct {
	InAlarm      []string `json:"In-alarm"`
	Up           []string `json:"Up"`
	Undetermined []string `json:"Undetermined"`
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

// ChannelTypeEmail, ChannelTypeSlack, ChannelTypeSMS, ChannelTypeTelegram,
// and ChannelTypeWebhook name the notification channel types the console
// offers today; a Teams channel can still exist on an account, but the
// console no longer creates one. Channel.Type is a plain string field, so a
// type the console adds later reaches the caller unchanged.
const (
	ChannelTypeEmail    = "Email"
	ChannelTypeSlack    = "Slack"
	ChannelTypeSMS      = "SMS"
	ChannelTypeTelegram = "Telegram"
	ChannelTypeWebhook  = "Webhook"
)

// ChannelType is one kind of notification channel ListChannelTypes can
// return, and the shape of the typeNotification object a Channel read
// embeds.
type ChannelType struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

// ChannelHeader is one key/value pair a Webhook channel sends with every
// notification.
type ChannelHeader struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// Channel is one notification channel. GreenNode's own API calls it a
// "notification"; the SDK says "channel" so the name does not clash with a
// Check's Notifications field. There is no get-by-ID call for a channel;
// GetChannel lists every page and matches by ID. Address and Headers can
// hold a secret, such as a webhook URL or a header value carrying a token:
// the SDK returns them unchanged, because UpdateChannel needs them to resend
// the full body, and the CLI redacts them on print.
type Channel struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Address string `json:"address"`

	// Type is the API's typeNotification.name: one of the ChannelType*
	// constants, or a type the console adds later. The json:"-" tag only
	// keeps Type out of encoding/json's default field-by-field handling;
	// UnmarshalJSON and MarshalJSON read and write it through the
	// typeNotification object, so it still round-trips through JSON.
	Type string `json:"-"`

	// Headers decodes the header field: a JSON string of [{"key","value"}]
	// pairs for a Webhook channel. The field is absent for a channel with no
	// headers, and a header string that does not decode to that shape also
	// leaves Headers nil rather than failing the read. As with Type, the
	// json:"-" tag only bypasses default handling: UnmarshalJSON and
	// MarshalJSON read and write it through the header string field.
	Headers []ChannelHeader `json:"-"`

	// rawHeader is the header wire field exactly as read, kept alongside the
	// decoded Headers so UpdateChannel can resend it unchanged when the
	// caller leaves Headers nil, even when it does not decode to
	// [{key,value}] pairs and Headers is nil.
	rawHeader string

	// MetricMappingID names this channel in a metric alarm. Unseen in a
	// channel list read so far; the field stays empty until one is.
	MetricMappingID string `json:"metricMappingId"`

	// CreatedDate and UpdatedDate hold whatever the API sends, a timestamp
	// with no time zone such as "2026-09-26T15:46:45"; the SDK does not
	// parse them. UpdatedDate is absent until the channel is updated.
	CreatedDate string `json:"createdDate"`
	UpdatedDate string `json:"updatedDate"`
}

// UnmarshalJSON decodes Channel with Type read from the nested
// typeNotification.name and Headers read from the header JSON string.
func (ch *Channel) UnmarshalJSON(data []byte) error {
	type alias Channel
	aux := struct {
		TypeNotification ChannelType `json:"typeNotification"`
		Header           string      `json:"header"`
		*alias
	}{alias: (*alias)(ch)}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	ch.Type = aux.TypeNotification.Name
	ch.Headers = decodeChannelHeaders(aux.Header)
	ch.rawHeader = aux.Header
	return nil
}

// MarshalJSON encodes Channel with Type and Headers written back into the
// typeNotification and header wire fields UnmarshalJSON reads, so the two
// stay a round trip through JSON despite carrying a json:"-" tag against
// encoding/json's own default marshaling: a caller that marshals a Channel
// to cache it, then unmarshals the result, gets the same Type and Headers
// back rather than losing them.
func (ch Channel) MarshalJSON() ([]byte, error) {
	type alias Channel
	aux := struct {
		alias
		TypeNotification ChannelType `json:"typeNotification"`
		Header           string      `json:"header,omitempty"`
	}{alias: alias(ch), TypeNotification: ChannelType{Name: ch.Type}}
	switch {
	case len(ch.Headers) > 0:
		data, err := json.Marshal(ch.Headers)
		if err != nil {
			return nil, err
		}
		aux.Header = string(data)
	case ch.rawHeader != "":
		// Headers is nil, either because there were never any headers or
		// because the wire value did not decode; either way, rawHeader
		// still carries the original wire value, so a marshal/unmarshal
		// round trip does not lose an undecodable header string.
		aux.Header = ch.rawHeader
	}
	return json.Marshal(aux)
}

// decodeChannelHeaders parses raw as a JSON array of ChannelHeader. An empty
// string, and a string that is valid JSON but not that shape, both return
// nil rather than an error: the design leaves a channel readable even when
// its header field cannot be understood.
func decodeChannelHeaders(raw string) []ChannelHeader {
	if raw == "" {
		return nil
	}
	var headers []ChannelHeader
	if err := json.Unmarshal([]byte(raw), &headers); err != nil {
		return nil
	}
	return headers
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

// flexibleString decodes a field whose wire type the design has not
// confirmed, such as an ID or a timestamp that could turn out to be numeric
// or an epoch instead of the string every live capture has shown so far. A
// JSON number decodes through json.Number, so a large ID keeps its exact
// digits rather than a float64's rounding, and formats as a string. Any
// other JSON type an unconfirmed field might arrive as (an object, an
// array, a bool, or null) leaves the value empty rather than failing the
// whole item it belongs to: one field of an unexpected shape is not worth
// losing the rest of a list page over.
type flexibleString string

func (s *flexibleString) UnmarshalJSON(data []byte) error {
	var str string
	if err := json.Unmarshal(data, &str); err == nil {
		*s = flexibleString(str)
		return nil
	}
	var num json.Number
	if err := json.Unmarshal(data, &num); err == nil {
		*s = flexibleString(num.String())
		return nil
	}
	*s = ""
	return nil
}
