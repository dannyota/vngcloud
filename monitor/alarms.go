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
// field. Unlike ListAlarms, no kind filter names it up front, and the API
// sends no field confirmed to name the kind itself, so Output.Alarm.Kind
// comes back empty; Log and MetricMappingID still decode from whichever
// wire fields the response carries. This call has not been made against a
// real alarm, so that, and the rest of the shape, is unconfirmed.
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
// cover the channel reference the design describes for each kind. Kind
// itself is set by ListAlarms from its own Kind filter, never guessed from
// the response; GetAlarm has no such filter and the API sends no field
// confirmed to name it, so a GetAlarm read leaves Kind empty. Nothing else
// is modeled until a live read confirms more.
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

// UnmarshalJSON decodes Alarm's common fields, plus Log from an inAlarm or
// ok key and MetricMappingID from a metricMappingId key, whichever the
// response carries; either, both, or neither may be present, and this
// decodes each independently rather than picking one to trust based on
// which key showed up. It never sets Kind: ListAlarms sets it, and clears
// whichever of Log or MetricMappingID does not belong to its own Kind
// filter, from that filter rather than from this decode; GetAlarm has no
// such filter and the API sends no field confirmed to name the kind
// itself, so Kind stays empty there. ID routes through flexibleString: an
// unconfirmed field that could arrive as a number instead of the string
// every capture so far has shown.
func (a *Alarm) UnmarshalJSON(data []byte) error {
	var aux struct {
		ID              flexibleString `json:"id"`
		Name            string         `json:"name"`
		Status          string         `json:"status"`
		Severity        string         `json:"severity"`
		MetricMappingID *string        `json:"metricMappingId"`
		InAlarm         *string        `json:"inAlarm"`
		OK              *string        `json:"ok"`
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	a.ID = string(aux.ID)
	a.Name = aux.Name
	a.Status = aux.Status
	a.Severity = aux.Severity
	a.Kind = ""
	a.MetricMappingID = ""
	a.Log = nil

	if aux.MetricMappingID != nil {
		a.MetricMappingID = *aux.MetricMappingID
	}
	if aux.InAlarm != nil || aux.OK != nil {
		a.Log = &LogAlarmDetail{
			InAlarm: splitChannelIDs(aux.InAlarm),
			OK:      splitChannelIDs(aux.OK),
		}
	}
	return nil
}

// splitChannelIDs splits a Log alarm's comma-joined channel ID string, as
// the design formats inAlarm and ok: each ID followed by a comma, including
// the last one. It drops every empty element the split produces, not just a
// trailing one, so a leading or doubled comma in a malformed value cannot
// leave an empty channel ID in the result. A nil or empty string, and a
// string with no non-empty element, all return nil.
func splitChannelIDs(raw *string) []string {
	if raw == nil || *raw == "" {
		return nil
	}
	var ids []string
	for _, part := range strings.Split(*raw, ",") {
		if part != "" {
			ids = append(ids, part)
		}
	}
	return ids
}
