package monitor

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/transport"
)

const (
	checkAlarmMaxBody      int64 = 1 << 20
	checkHistoryMaxBody    int64 = 16 << 20
	checkHistoryMaxTotal   int64 = 32 << 20
	checkAlarmMaxLocations       = 64
)

// GetCheckAlarmInput identifies the check whose current alarm state to read.
type GetCheckAlarmInput struct {
	CheckID string `vngcloud:"required"`
}

type GetCheckAlarmOutput struct {
	Alarm CheckAlarm
}

// CheckAlarm is a definition's current state and its location alarms.
type CheckAlarm struct {
	ID              string
	Status          string
	StatusUpdatedAt time.Time
	Locations       []CheckLocationAlarm
}

type CheckLocationAlarm struct {
	AlarmID        string
	LocationID     string
	LocationName   string
	State          string
	StateUpdatedAt time.Time
	UpdatedAt      time.Time
	CreatedAt      time.Time
}

// ListCheckAlarmHistoryInput accepts RFC 3339 endpoints. End defaults to now;
// Start defaults to 24 hours before the resolved End.
type ListCheckAlarmHistoryInput struct {
	CheckID    string `vngcloud:"required"`
	Start      string
	End        string
	LocationID string
}

// ListCheckAlarmHistoryOutput holds only the records the server returned.
// Paging and truncation are unknown. Empty Items proves neither that no
// transition happened nor that an alarm exists. Use GetCheckAlarm for state.
type ListCheckAlarmHistoryOutput struct {
	Items []CheckAlarmTransition
	Start time.Time
	End   time.Time
}

type CheckAlarmTransition struct {
	ID           string
	AlarmID      string
	LocationID   string
	LocationName string
	OldState     string
	NewState     string
	Timestamp    time.Time
	SubAlarms    []CheckSubAlarmState
}

type CheckSubAlarmState struct {
	State         string
	CurrentValues []float64
}

func (c *Client) GetCheckAlarm(ctx context.Context, in *GetCheckAlarmInput) (*GetCheckAlarmOutput, error) {
	const op = "monitor.GetCheckAlarm"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "CheckID", in.CheckID); err != nil {
		return nil, err
	}
	var credentials monitorReadCredentials
	alarm, _, err := c.readCheckAlarm(ctx, op, in.CheckID, &credentials)
	if err != nil {
		return nil, err
	}
	return &GetCheckAlarmOutput{Alarm: alarm}, nil
}

func (c *Client) ListCheckAlarmHistory(ctx context.Context, in *ListCheckAlarmHistoryInput) (*ListCheckAlarmHistoryOutput, error) {
	const op = "monitor.ListCheckAlarmHistory"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "CheckID", in.CheckID); err != nil {
		return nil, err
	}
	if in.LocationID != "" {
		if err := core.CheckPathID(op, "LocationID", in.LocationID); err != nil {
			return nil, err
		}
	}
	start, end, err := c.checkHistoryRange(op, in)
	if err != nil {
		return nil, err
	}
	var credentials monitorReadCredentials
	alarm, frequency, err := c.readCheckAlarm(ctx, op, in.CheckID, &credentials)
	if err != nil {
		return nil, err
	}
	locations := alarm.Locations
	if in.LocationID != "" {
		locations = nil
		for _, l := range alarm.Locations {
			if l.LocationID == in.LocationID {
				locations = append(locations, l)
			}
		}
		if len(locations) == 0 {
			return nil, &core.APIError{Operation: op, Code: "NotFound", Message: "check location not found", Err: core.ErrNotFound}
		}
	}
	out := &ListCheckAlarmHistoryOutput{Start: start, End: end, Items: []CheckAlarmTransition{}}
	remaining := checkHistoryMaxTotal
	for _, l := range locations {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if checkAlarmText(l.AlarmID, credentials) != l.AlarmID {
			return nil, checkAlarmResponseError(op, "InvalidResponse")
		}
		q := url.Values{"start_time": {strconv.FormatInt(start.UnixMilli(), 10)}, "end_time": {strconv.FormatInt(end.UnixMilli(), 10)}}
		if frequency > 0 {
			q.Set("interval", strconv.Itoa(frequency))
		}
		var body struct {
			Data *[]checkTransitionWire `json:"data"`
		}
		size, err := c.readCheckAlarmJSON(ctx, op, c.alarmRoute([]string{"alarms", "synthetic", "metrics", "mona", l.AlarmID, "histories"}, q), min(checkHistoryMaxBody, remaining), &body, &credentials)
		if err != nil {
			return nil, err
		}
		remaining -= size
		if body.Data == nil {
			return nil, checkAlarmResponseError(op, "InvalidResponse")
		}
		for _, item := range *body.Data {
			transition, err := item.project(op, l, credentials)
			if err != nil {
				return nil, err
			}
			out.Items = append(out.Items, transition)
		}
	}
	for i := range out.Items {
		item := &out.Items[i]
		for _, field := range []*string{&item.ID, &item.AlarmID, &item.LocationID, &item.LocationName, &item.OldState, &item.NewState} {
			*field = checkAlarmText(*field, credentials)
		}
		for j := range item.SubAlarms {
			item.SubAlarms[j].State = checkAlarmText(item.SubAlarms[j].State, credentials)
		}
	}
	slices.SortStableFunc(out.Items, func(a, b CheckAlarmTransition) int {
		if n := a.Timestamp.Compare(b.Timestamp); n != 0 {
			return n
		}
		if n := strings.Compare(a.LocationID, b.LocationID); n != 0 {
			return n
		}
		if n := strings.Compare(a.AlarmID, b.AlarmID); n != 0 {
			return n
		}
		return strings.Compare(a.ID, b.ID)
	})
	return out, nil
}

func (c *Client) checkHistoryRange(op string, in *ListCheckAlarmHistoryInput) (time.Time, time.Time, error) {
	end := c.now()
	var err error
	if in.End != "" {
		end, err = time.Parse(time.RFC3339, in.End)
		if err != nil {
			return time.Time{}, time.Time{}, checkHistoryInputError(op)
		}
	}
	start := end.Add(-24 * time.Hour)
	if in.Start != "" {
		start, err = time.Parse(time.RFC3339, in.Start)
		if err != nil {
			return time.Time{}, time.Time{}, checkHistoryInputError(op)
		}
	}
	if start.UnixMilli() >= end.UnixMilli() {
		return time.Time{}, time.Time{}, checkHistoryInputError(op)
	}
	return start, end, nil
}

func checkHistoryInputError(op string) error {
	return &core.APIError{Operation: op, Code: "InvalidInput", Message: "Start and End must form a nonempty RFC 3339 range at millisecond precision", Err: core.ErrInvalidInput}
}

func checkAlarmResponseError(op, code string) error {
	return &core.APIError{Operation: op, StatusCode: http.StatusOK, Code: code, Message: "check alarm response rejected; body withheld"}
}

// Decode privately to count JSON bytes without retaining raw response maps.
// Sensitive suppresses capture and withholds decode errors and server text.
type checkAlarmDecoder struct {
	out  any
	size int64
}

func (d *checkAlarmDecoder) UnmarshalJSON(body []byte) error {
	d.size = int64(len(body))
	return json.Unmarshal(body, d.out)
}

func (c *Client) readCheckAlarmJSON(ctx context.Context, op, route string, limit int64, out any, credentials *monitorReadCredentials) (int64, error) {
	if limit <= 0 {
		return 0, checkAlarmResponseError(op, "ResponseLimitExceeded")
	}
	var sent []string
	decoder := checkAlarmDecoder{out: out}
	status, err := c.c.DoJSONStatus(ctx, transport.Request{
		Operation: op, Method: http.MethodGet, URL: route, OK: []int{200}, MaxBody: limit,
		Sensitive: true, WithholdMessage: "check alarm response withheld", SentCredentials: &sent, Redact: *credentials,
		ClassifyError: func(status int, _ string) string { return core.ResolvedCode(status, "") },
	}, &decoder)
	if !credentials.add(sent) {
		return 0, monitorReadError(op, status, "InvalidResponse", "check alarm credential safety limit exceeded", nil)
	}
	if err != nil {
		if errors.Is(err, transport.ErrBodyTooLarge) {
			return 0, monitorReadError(op, status, "ResponseLimitExceeded", "check alarm response exceeds safety limit", err)
		}
		var apiErr *core.APIError
		if errors.As(err, &apiErr) {
			safe := *apiErr
			safe.Operation = op
			if safe.StatusCode == 0 && status > 0 {
				safe.StatusCode = status
			}
			safe.Code = core.ResolvedCode(safe.StatusCode, "")
			if safe.Code == "" {
				safe.Code = "InvalidResponse"
			}
			safe.Message = "check alarm request failed; response withheld"
			safe.Err = monitorReadCause(apiErr.Err)
			return 0, &safe
		}
		return 0, monitorReadError(op, status, "InvalidResponse", "check alarm request failed; cause withheld", err)
	}
	if decoder.size == 0 {
		return 0, checkAlarmResponseError(op, "InvalidResponse")
	}
	return decoder.size, nil
}

type checkAlarmFrequency int

func (f *checkAlarmFrequency) UnmarshalJSON(raw []byte) error {
	obj := &quotaObject{fields: map[string]json.RawMessage{"frequency": raw}}
	value := obj.integer("frequency", false)
	if obj.invalid || value <= 0 {
		return errors.New("invalid check frequency")
	}
	*f = checkAlarmFrequency(value)
	return nil
}

type checkAlarmSnapshot struct {
	ID             string  `json:"id"`
	HistoryAlarmID *string `json:"history_alarm"`
	Options        *struct {
		TestFrequency *checkAlarmFrequency `json:"test_frequency"`
	} `json:"options"`
}

type checkMetricWire struct {
	Dimensions struct {
		LocationID   string `json:"location_id"`
		LocationName string `json:"location_name"`
	} `json:"dimensions"`
}

type checkLocationWire struct {
	ID             string            `json:"id"`
	Metrics        []checkMetricWire `json:"metrics"`
	State          string            `json:"state"`
	StateUpdatedAt *time.Time        `json:"state_updated_timestamp"`
	UpdatedAt      *time.Time        `json:"updated_timestamp"`
	CreatedAt      *time.Time        `json:"created_timestamp"`
}

type checkAlarmWire struct {
	ID     string `json:"id"`
	Status *struct {
		Status    string     `json:"status"`
		UpdatedAt *time.Time `json:"status_updated_at"`
	} `json:"status"`
	Alarms *[]checkLocationWire `json:"alarms"`
}

func (c *Client) readCheckAlarm(ctx context.Context, op, id string, credentials *monitorReadCredentials) (CheckAlarm, int, error) {
	var snapshot checkAlarmSnapshot
	_, err := c.readCheckAlarmJSON(ctx, op, c.route([]string{"uptimes", id}), checkAlarmMaxBody, &snapshot, credentials)
	if err != nil {
		return CheckAlarm{}, 0, err
	}
	if snapshot.ID != id || snapshot.HistoryAlarmID == nil || snapshot.Options == nil || snapshot.Options.TestFrequency == nil {
		return CheckAlarm{}, 0, checkAlarmResponseError(op, "InvalidResponse")
	}
	ref := *snapshot.HistoryAlarmID
	if ref == "" {
		return CheckAlarm{}, 0, &core.APIError{Operation: op, Code: "NotFound", Message: "check has no alarm reference", Err: core.ErrNotFound}
	}
	if core.CheckPathID(op, "HistoryAlarmID", ref) != nil || checkAlarmText(ref, *credentials) != ref {
		return CheckAlarm{}, 0, checkAlarmResponseError(op, "InvalidResponse")
	}
	if err := ctx.Err(); err != nil {
		return CheckAlarm{}, 0, err
	}
	var definition struct {
		Data *checkAlarmWire `json:"data"`
	}
	_, err = c.readCheckAlarmJSON(ctx, op, c.alarmRoute([]string{"alarms", "synthetic", "metrics", "mona", ref}, nil), checkAlarmMaxBody, &definition, credentials)
	if err != nil {
		return CheckAlarm{}, 0, err
	}
	d := definition.Data
	if d == nil || core.CheckPathID(op, "ID", d.ID) != nil || d.Status == nil || d.Status.Status == "" || !checkAlarmTimePresent(d.Status.UpdatedAt) || d.Alarms == nil {
		return CheckAlarm{}, 0, checkAlarmResponseError(op, "InvalidResponse")
	}
	if len(*d.Alarms) > checkAlarmMaxLocations {
		return CheckAlarm{}, 0, checkAlarmResponseError(op, "FanOutLimitExceeded")
	}
	a := CheckAlarm{ID: checkAlarmText(d.ID, *credentials), Status: checkAlarmText(d.Status.Status, *credentials), StatusUpdatedAt: *d.Status.UpdatedAt, Locations: []CheckLocationAlarm{}}
	seenLocations := map[string]bool{}
	seenAlarms := map[string]bool{}
	for _, wire := range *d.Alarms {
		locationID, locationName, err := checkAlarmLocation(op, wire.Metrics)
		if err != nil {
			return CheckAlarm{}, 0, err
		}
		if core.CheckPathID(op, "AlarmID", wire.ID) != nil || wire.State == "" || !checkAlarmTimePresent(wire.StateUpdatedAt) || !checkAlarmTimePresent(wire.UpdatedAt) || !checkAlarmTimePresent(wire.CreatedAt) || seenLocations[locationID] || seenAlarms[wire.ID] {
			return CheckAlarm{}, 0, checkAlarmResponseError(op, "InvalidResponse")
		}
		seenLocations[locationID] = true
		seenAlarms[wire.ID] = true
		a.Locations = append(a.Locations, CheckLocationAlarm{AlarmID: checkAlarmText(wire.ID, *credentials), LocationID: checkAlarmText(locationID, *credentials), LocationName: checkAlarmText(locationName, *credentials), State: checkAlarmText(wire.State, *credentials), StateUpdatedAt: *wire.StateUpdatedAt, UpdatedAt: *wire.UpdatedAt, CreatedAt: *wire.CreatedAt})
	}
	// Projected IDs must remain valid before they can become another route.
	for _, l := range a.Locations {
		if core.CheckPathID(op, "AlarmID", l.AlarmID) != nil || core.CheckPathID(op, "LocationID", l.LocationID) != nil {
			return CheckAlarm{}, 0, checkAlarmResponseError(op, "InvalidResponse")
		}
	}
	return a, int(*snapshot.Options.TestFrequency), nil
}

func checkAlarmTimePresent(t *time.Time) bool { return t != nil && !t.IsZero() }
func checkAlarmText(s string, credentials []string) string {
	return transport.RedactErrorText(s, credentials...)
}

func checkAlarmLocation(op string, metrics []checkMetricWire) (string, string, error) {
	var id, name string
	for _, m := range metrics {
		d := m.Dimensions
		if core.CheckPathID(op, "LocationID", d.LocationID) != nil || d.LocationName == "" || (id != "" && (id != d.LocationID || name != d.LocationName)) {
			return "", "", checkAlarmResponseError(op, "InvalidResponse")
		}
		id, name = d.LocationID, d.LocationName
	}
	if id == "" {
		return "", "", checkAlarmResponseError(op, "InvalidResponse")
	}
	return id, name, nil
}

type checkTransitionWire struct {
	ID        string     `json:"id"`
	AlarmID   string     `json:"alarm_id"`
	OldState  string     `json:"old_state"`
	NewState  string     `json:"new_state"`
	Timestamp *time.Time `json:"timestamp"`
	SubAlarms *[]struct {
		State         string      `json:"sub_alarm_state"`
		CurrentValues *[]*float64 `json:"current_values"`
	} `json:"sub_alarms"`
}

func (w checkTransitionWire) project(op string, l CheckLocationAlarm, credentials []string) (CheckAlarmTransition, error) {
	if core.CheckPathID(op, "ID", w.ID) != nil || w.AlarmID != l.AlarmID || w.OldState == "" || w.NewState == "" || !checkAlarmTimePresent(w.Timestamp) || w.SubAlarms == nil {
		return CheckAlarmTransition{}, checkAlarmResponseError(op, "InvalidResponse")
	}
	out := CheckAlarmTransition{ID: checkAlarmText(w.ID, credentials), AlarmID: l.AlarmID, LocationID: l.LocationID, LocationName: l.LocationName, OldState: checkAlarmText(w.OldState, credentials), NewState: checkAlarmText(w.NewState, credentials), Timestamp: *w.Timestamp, SubAlarms: []CheckSubAlarmState{}}
	for _, sub := range *w.SubAlarms {
		if sub.State == "" || sub.CurrentValues == nil {
			return CheckAlarmTransition{}, checkAlarmResponseError(op, "InvalidResponse")
		}
		state := CheckSubAlarmState{State: checkAlarmText(sub.State, credentials), CurrentValues: []float64{}}
		for _, value := range *sub.CurrentValues {
			if value == nil {
				return CheckAlarmTransition{}, checkAlarmResponseError(op, "InvalidResponse")
			}
			state.CurrentValues = append(state.CurrentValues, *value)
		}
		out.SubAlarms = append(out.SubAlarms, state)
	}
	return out, nil
}
