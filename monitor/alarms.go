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
		items[i].Kind = in.Kind
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
// field. Unlike ListAlarms, no kind filter names it up front, so
// Output.Alarm.Kind comes only from whichever channel-reference field the
// response carries; see Alarm.UnmarshalJSON. This call has not been made
// against a real alarm, so that inference, and the rest of the shape, is
// unconfirmed.
func (c *Client) GetAlarm(ctx context.Context, in *GetAlarmInput) (*GetAlarmOutput, error) {
	const op = "monitor.GetAlarm"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "AlarmID", in.AlarmID); err != nil {
		return nil, err
	}

	var resp struct {
		Data Alarm `json:"data"`
	}
	req := transport.Request{
		Operation: op,
		Method:    http.MethodGet,
		URL:       c.alarmRoute([]string{"alarms", in.AlarmID}, nil),
		OK:        []int{200},
	}
	if err := c.c.DoJSON(ctx, req, &resp); err != nil {
		return nil, err
	}
	return &GetAlarmOutput{Alarm: resp.Data}, nil
}

// Alarm is one alert rule, of either Kind. Its response shape has not been
// checked against a live alarm: the test account has none, and the
// design's only source for it is the console's own JavaScript. ID, Name,
// Status, and Severity are the fields the design names as both list
// filters and console-shown fields for both kinds; MetricMappingID and Log
// cover the channel reference the design describes for each kind. Nothing
// else is modeled until a live read confirms more.
type Alarm struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	Status   string `json:"status"`
	Severity string `json:"severity"`

	// MetricMappingID names, for a Metric alarm, the channel a state
	// transition notifies: a Metric alarm refers to a channel by the
	// channel's own MetricMappingID rather than its ID. Empty for a Log
	// alarm.
	MetricMappingID string `json:"metricMappingId,omitempty"`

	// Log holds the fields specific to a Log alarm. Nil for a Metric alarm.
	Log *LogAlarmDetail `json:"log,omitempty"`
}

// LogAlarmDetail holds the fields specific to a Log alarm. InAlarm and OK
// are the channel IDs that alert on entering and leaving the alarm state,
// decoded from the wire's comma-joined inAlarm and ok strings.
type LogAlarmDetail struct {
	InAlarm []string `json:"inAlarm,omitempty"`
	OK      []string `json:"ok,omitempty"`
}

// UnmarshalJSON decodes Alarm from either kind's wire shape. A response
// carrying an inAlarm or ok key, even present but empty, is read as a Log
// alarm: those two fields are the comma-joined channel ID strings the
// design describes for a log alarm body, and a read is assumed to mirror
// that shape the same way Channel's create and read shapes match. A
// response carrying a metricMappingId key instead is read as a Metric
// alarm. A response with none of the three keys decodes the common fields
// with Kind left empty, rather than guessing which kind it is.
func (a *Alarm) UnmarshalJSON(data []byte) error {
	var aux struct {
		ID              string  `json:"id"`
		Name            string  `json:"name"`
		Status          string  `json:"status"`
		Severity        string  `json:"severity"`
		MetricMappingID *string `json:"metricMappingId"`
		InAlarm         *string `json:"inAlarm"`
		OK              *string `json:"ok"`
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	a.ID = aux.ID
	a.Name = aux.Name
	a.Status = aux.Status
	a.Severity = aux.Severity
	a.Kind = ""
	a.MetricMappingID = ""
	a.Log = nil

	switch {
	case aux.InAlarm != nil || aux.OK != nil:
		a.Kind = AlarmKindLog
		a.Log = &LogAlarmDetail{
			InAlarm: splitChannelIDs(aux.InAlarm),
			OK:      splitChannelIDs(aux.OK),
		}
	case aux.MetricMappingID != nil:
		a.Kind = AlarmKindMetric
		a.MetricMappingID = *aux.MetricMappingID
	}
	return nil
}

// splitChannelIDs splits a Log alarm's comma-joined channel ID string, as
// the design formats inAlarm and ok: each ID followed by a comma,
// including the last one, so a naive split leaves a trailing empty element
// that this drops. A nil or empty string returns nil.
func splitChannelIDs(raw *string) []string {
	if raw == nil || *raw == "" {
		return nil
	}
	parts := strings.Split(*raw, ",")
	if len(parts) > 0 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	if len(parts) == 0 {
		return nil
	}
	return parts
}
