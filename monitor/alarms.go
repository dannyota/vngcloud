package monitor

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/routes"
	"danny.vn/vngcloud/internal/transport"
)

// AlarmKindMetric and AlarmKindLog name the two kinds of Alarm ListAlarms
// filters on, matching the type-alarm query value the console sends.
const (
	AlarmKindMetric = "Metric"
	AlarmKindLog    = "Log"
)

// alarmRoute builds a URL under the Monitor endpoint's alarm API prefix,
// separate from the uptime manager and notification gateway prefixes route
// and notificationRoute build under.
func (c *Client) alarmRoute(parts []string, q url.Values) string {
	full := append([]string{"vmonitor-api", "api", "v1"}, parts...)
	return c.c.RouteURL(routes.Route{Product: routes.ProductMonitor, Parts: full, Query: q})
}

// ListAlarmsInput filters the alarm list. Kind selects Metric or Log alarms
// and is required, as the console always sends one. Name, Status, and
// Severity narrow it further, empty for no filter. Page and Size page the
// result; a non-positive value sends core.DefaultPage and
// core.DefaultPageSize, as core.PageQuery does for other services.
type ListAlarmsInput struct {
	Kind     string `vngcloud:"required"`
	Name     string
	Status   string
	Severity string
	Page     int
	Size     int
}

type ListAlarmsOutput = core.PagedList[Alarm]

// ListAlarms lists alarms of one Kind on the account. It always sends the
// type-alarm, name, status, and severity query keys, empty when unset, plus
// page and size, the same way the console does. Every returned item's Kind
// is set to in.Kind: a list is always filtered to one kind, so this holds
// regardless of what Alarm.UnmarshalJSON itself infers from the item.
func (c *Client) ListAlarms(ctx context.Context, in *ListAlarmsInput) (*ListAlarmsOutput, error) {
	const op = "monitor.ListAlarms"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	return c.listAlarms(ctx, op, in)
}

// listAlarms is ListAlarms's request, reused by CreateLogAlarm's
// duplicate-name check and by CreateLogAlarm and UpdateLogAlarm's own
// post-write wait under the calling write's own operation name, so a
// failure names the write it happened inside rather than
// "monitor.ListAlarms". in is assumed already checked with
// core.CheckRequired.
func (c *Client) listAlarms(ctx context.Context, op string, in *ListAlarmsInput) (*ListAlarmsOutput, error) {
	q := core.PageQuery(in.Page, in.Size)
	q.Set("type-alarm", in.Kind)
	q.Set("name", in.Name)
	q.Set("status", in.Status)
	q.Set("severity", in.Severity)

	var resp listAlarmsResponse
	req := transport.Request{
		Operation: op,
		Method:    http.MethodGet,
		URL:       c.alarmRoute([]string{"alarms", "list"}, q),
		OK:        []int{200},
	}
	if err := c.c.DoJSON(ctx, req, &resp); err != nil {
		return nil, err
	}
	items := resp.LstData
	for i := range items {
		// in.Kind, not which of Log or MetricMappingID the wire happened to
		// carry, decides which one this item keeps: a list is always
		// filtered to one kind, so that is known here regardless of what the
		// response sent, and trusting it instead of the wire keeps the two
		// fields mutually exclusive even if a response ever carried both.
		items[i].Kind = in.Kind
		switch in.Kind {
		case AlarmKindLog:
			items[i].MetricMappingID = ""
		case AlarmKindMetric:
			items[i].Log = nil
		}
	}
	return core.NewPagedList(items, resp.Page, resp.PageSize, resp.TotalPage, resp.TotalItem), nil
}

type listAlarmsResponse struct {
	LstData   []Alarm `json:"lstData"`
	Page      int     `json:"page"`
	PageSize  int     `json:"pageSize"`
	TotalPage int     `json:"totalPage"`
	TotalItem int     `json:"totalItem"`
}

// GetAlarmInput identifies the alarm to read.
type GetAlarmInput struct {
	AlarmID string `vngcloud:"required"`
}

type GetAlarmOutput struct {
	Alarm Alarm
}

// GetAlarm reads one alarm by ID, of either kind, from the response's data
// field. Unlike ListAlarms, it sets no Kind filter of its own: Output.Alarm
// .Kind comes from the response's own type (LOG or METRIC), decoded by
// Alarm.UnmarshalJSON. This call has not been made against a real alarm, so
// the shape is unconfirmed beyond what the design's console source shows.
func (c *Client) GetAlarm(ctx context.Context, in *GetAlarmInput) (*GetAlarmOutput, error) {
	const op = "monitor.GetAlarm"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "AlarmID", in.AlarmID); err != nil {
		return nil, err
	}
	alarm, err := c.getAlarm(ctx, op, in.AlarmID)
	if err != nil {
		return nil, err
	}
	return &GetAlarmOutput{Alarm: *alarm}, nil
}

// getAlarm is GetAlarm's request, reused by UpdateLogAlarm's pre-update read
// and by CreateLogAlarm and UpdateLogAlarm's own post-write wait under the
// calling write's own operation name, so a failure names the write it
// happened inside rather than "monitor.GetAlarm". id is assumed already
// checked with core.CheckPathID.
func (c *Client) getAlarm(ctx context.Context, op, id string) (*Alarm, error) {
	var resp struct {
		Data Alarm `json:"data"`
	}
	req := transport.Request{
		Operation: op,
		Method:    http.MethodGet,
		URL:       c.alarmRoute([]string{"alarms", id}, nil),
		OK:        []int{200},
	}
	if err := c.c.DoJSON(ctx, req, &resp); err != nil {
		return nil, err
	}
	return &resp.Data, nil
}

// Alarm is one alert rule, of either Kind. ID, Name, Description, Status,
// and Severity are top-level fields for both kinds; MetricMappingID and Log
// cover the channel reference the design describes for each kind. Kind
// decodes from the response's own type field (LOG gives AlarmKindLog,
// METRIC gives AlarmKindMetric; anything else, including no type at all,
// leaves it empty); ListAlarms then overwrites it from its own Kind filter,
// since a list is always filtered to one kind regardless of what the wire
// sends. Nothing else is modeled until a live read confirms more.
type Alarm struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Kind        string `json:"kind"`
	Status      string `json:"status"`
	Severity    string `json:"severity"`

	// MetricMappingID names, for a Metric alarm, the channel a state
	// transition notifies: a Metric alarm refers to a channel by the
	// channel's own MetricMappingID rather than its ID. Empty for a Log
	// alarm.
	MetricMappingID string `json:"metricMappingId,omitempty"`

	// Log holds the fields specific to a Log alarm. Nil for a Metric alarm.
	// It decodes from the alarmLog object a Log alarm's response nests every
	// log field under; a response with no alarmLog at all still decodes
	// InAlarm and OK from the top-level inAlarm and ok keys, so a caller
	// whose response takes that shape, or one this design has not yet seen,
	// is not left with a nil Log purely because alarmLog is missing.
	Log *LogAlarmDetail `json:"log,omitempty"`
}

// LogAlarmDetail holds the fields specific to a Log alarm, decoded from the
// response's alarmLog object (or, when that is absent, from the top-level
// inAlarm and ok keys alone; every other field then stays at its zero
// value). LogProjectID and LogProjectName are alarmLog's own logProject and
// logProjectName keys, which name the project differently than the create
// body's logProjectId and projectName. Filter is nil when alarmLog carries
// no filter key at all, rather than an empty JSON value. ThresholdValue,
// TimeFrame, Resend.Period, and Resend.Times accept a JSON number or a
// numeric string through flexibleNumber; any other shape, including an
// empty string, decodes as 0 rather than failing the read. GroupByField
// accepts any JSON scalar through flexibleString, for the same reason.
// InAlarm and OK are the channel IDs that alert on entering and leaving
// the alarm state, decoded from the wire's comma-joined inAlarm and ok
// strings.
type LogAlarmDetail struct {
	LogProjectID   string
	LogProjectName string
	QueryString    string
	Filter         json.RawMessage
	ThresholdType  string
	Condition      string
	ThresholdValue float64
	TimeFrame      int
	GroupByField   string
	AggField       string
	AggType        string
	InAlarm        []string
	OK             []string
	Resend         LogAlarmResend

	// rawLogSearchQuery is alarmLog's logSearchQuery value exactly as read.
	// The SDK never builds or exposes a value for this field beyond the "[]"
	// it always sends for a query it constructs itself (see
	// buildLogAlarmBody); an alarm the console created through its own
	// token-based search box may carry a different value here, which
	// UpdateLogAlarm must resend unchanged when the caller leaves
	// QueryString unset, rather than silently switching the alarm to the
	// SDK's own text-query mode.
	rawLogSearchQuery string
}

// LogAlarmResend describes a log alarm's resend settings: whether it
// resends a notification while an alarm condition persists, which
// transitions it resends for, how often, and how many times.
type LogAlarmResend struct {
	Enabled bool
	// Statuses is split from the wire's comma-joined resendStatus, such as
	// "OK,ALARM,UNDETERMINED"; each element is one of the status strings the
	// console's own resendStatus values name (OK, ALARM, UNDETERMINED).
	Statuses []string
	// Period is in minutes.
	Period int
	Times  int
}

// UnmarshalJSON decodes LogAlarmDetail from alarmLog's own wire field
// names, distinct from the create body's logProjectId and projectName (see
// LogAlarmDetail's doc comment).
func (d *LogAlarmDetail) UnmarshalJSON(data []byte) error {
	var aux struct {
		LogProjectID   flexibleString  `json:"logProject"`
		LogProjectName string          `json:"logProjectName"`
		QueryString    string          `json:"queryString"`
		LogSearchQuery string          `json:"logSearchQuery"`
		Filter         json.RawMessage `json:"filter"`
		ThresholdType  string          `json:"thresholdType"`
		Condition      string          `json:"condition"`
		ThresholdValue flexibleNumber  `json:"thresholdValue"`
		TimeFrame      flexibleNumber  `json:"timeFrame"`
		GroupByField   flexibleString  `json:"groupByField"`
		AggField       string          `json:"metricAggKey"`
		AggType        string          `json:"metricAggType"`
		InAlarm        *string         `json:"inAlarm"`
		OK             *string         `json:"ok"`
		ResendEnabled  bool            `json:"resendEnabled"`
		ResendPeriod   flexibleNumber  `json:"resendPeriod"`
		ResendTimes    flexibleNumber  `json:"resendTimes"`
		ResendStatus   string          `json:"resendStatus"`
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	d.LogProjectID = string(aux.LogProjectID)
	d.LogProjectName = aux.LogProjectName
	d.QueryString = aux.QueryString
	d.rawLogSearchQuery = aux.LogSearchQuery
	d.Filter = aux.Filter
	d.ThresholdType = aux.ThresholdType
	d.Condition = aux.Condition
	d.ThresholdValue = float64(aux.ThresholdValue)
	d.TimeFrame = int(aux.TimeFrame)
	d.GroupByField = string(aux.GroupByField)
	d.AggField = aux.AggField
	d.AggType = aux.AggType
	d.InAlarm = splitChannelIDs(aux.InAlarm)
	d.OK = splitChannelIDs(aux.OK)
	d.Resend = LogAlarmResend{
		Enabled:  aux.ResendEnabled,
		Statuses: splitCommaList(&aux.ResendStatus),
		Period:   int(aux.ResendPeriod),
		Times:    int(aux.ResendTimes),
	}
	return nil
}

// alarmKindFromType maps the response's top-level type (LOG or METRIC) to
// AlarmKindLog or AlarmKindMetric. Any other value, including empty for a
// response that sends no type at all, leaves Kind empty rather than
// guessing it from which of Log or MetricMappingID the response happens to
// carry.
func alarmKindFromType(wireType string) string {
	switch wireType {
	case "LOG":
		return AlarmKindLog
	case "METRIC":
		return AlarmKindMetric
	default:
		return ""
	}
}

// UnmarshalJSON decodes Alarm's top-level fields, Kind from type, and Log
// from an alarmLog object when present. A response with no alarmLog at all
// still decodes Log from the top-level inAlarm and ok keys alone, the
// fallback shape the console's own code falls back to when alarmLog is
// absent; every other LogAlarmDetail field then stays zero. ListAlarms
// overwrites Kind, and clears whichever of Log or MetricMappingID does not
// belong to its own Kind filter, from that filter rather than from this
// decode; a GetAlarm read keeps whatever this decode found. ID routes
// through flexibleString: no alarm has been read live, so the design does
// not know whether the server sends it as a string or a number.
func (a *Alarm) UnmarshalJSON(data []byte) error {
	var aux struct {
		ID              flexibleString  `json:"id"`
		Name            string          `json:"name"`
		Description     string          `json:"description"`
		Type            string          `json:"type"`
		Status          string          `json:"status"`
		Severity        string          `json:"severity"`
		MetricMappingID *string         `json:"metricMappingId"`
		InAlarm         *string         `json:"inAlarm"`
		OK              *string         `json:"ok"`
		AlarmLog        *LogAlarmDetail `json:"alarmLog"`
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	a.ID = string(aux.ID)
	a.Name = aux.Name
	a.Description = aux.Description
	a.Status = aux.Status
	a.Severity = aux.Severity
	a.Kind = alarmKindFromType(aux.Type)
	a.MetricMappingID = ""
	a.Log = nil

	if aux.MetricMappingID != nil {
		a.MetricMappingID = *aux.MetricMappingID
	}

	switch {
	case aux.AlarmLog != nil:
		a.Log = aux.AlarmLog
	case aux.InAlarm != nil || aux.OK != nil:
		a.Log = &LogAlarmDetail{
			InAlarm: splitChannelIDs(aux.InAlarm),
			OK:      splitChannelIDs(aux.OK),
		}
	}
	return nil
}

// splitCommaList splits raw on commas, dropping every empty element the
// split produces, not just a trailing one, so a leading, trailing, or
// doubled comma in a malformed value never leaves an empty element in the
// result. A nil or empty string, and a string with no non-empty element,
// all return nil.
func splitCommaList(raw *string) []string {
	if raw == nil || *raw == "" {
		return nil
	}
	var items []string
	for _, part := range strings.Split(*raw, ",") {
		if part != "" {
			items = append(items, part)
		}
	}
	return items
}

// splitChannelIDs splits a Log alarm's comma-joined channel ID string, as
// the design formats inAlarm and ok: each ID followed by a comma, including
// the last one.
func splitChannelIDs(raw *string) []string {
	return splitCommaList(raw)
}
