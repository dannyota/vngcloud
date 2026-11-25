package monitor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/transport"
)

// ListCheckResponseTimesInput identifies a check and time window.
type ListCheckResponseTimesInput struct {
	// CheckID identifies the check.
	CheckID string `vngcloud:"required"`
	// Start is an optional RFC 3339 window start.
	Start string
	// End is an optional RFC 3339 window end.
	End string
}

// ListCheckResponseTimesOutput contains the requested window and returned series.
type ListCheckResponseTimesOutput struct {
	Items      []CheckResponseTimeSeries
	Start, End time.Time
}

// CheckResponseTimeSeries contains buckets for a location.
type CheckResponseTimeSeries struct {
	LocationID string
	Points     []CheckResponseTimePoint
}

// CheckResponseTimePoint contains a response-time bucket in milliseconds.
type CheckResponseTimePoint struct {
	Timestamp   time.Time
	ValueMillis float64
}

const responseMetric = "syntt.http.response_time"

// ListCheckResponseTimes reads response times for an HTTP check.
func (c *Client) ListCheckResponseTimes(ctx context.Context, in *ListCheckResponseTimesInput) (*ListCheckResponseTimesOutput, error) {
	const op = "monitor.ListCheckResponseTimes"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "CheckID", in.CheckID); err != nil {
		return nil, err
	}
	for _, value := range []string{in.Start, in.End} {
		if value != "" {
			if _, err := responseParseTime(value); err != nil {
				return nil, checkHistoryInputError(op)
			}
		}
	}
	start, end, err := c.checkHistoryRange(op, &ListCheckAlarmHistoryInput{Start: strings.ToUpper(in.Start), End: strings.ToUpper(in.End)})
	if err != nil {
		return nil, err
	}
	if end.UnixMilli()-start.UnixMilli() > int64(24*time.Hour/time.Millisecond) {
		return nil, checkHistoryInputError(op)
	}
	start, end = time.UnixMilli(start.UnixMilli()).UTC(), time.UnixMilli(end.UnixMilli()).UTC()
	if !responseValidYear(start) || !responseValidYear(end) {
		return nil, checkHistoryInputError(op)
	}
	var credentials monitorReadCredentials
	var raw json.RawMessage
	_, err = c.readCheckAlarmJSON(ctx, op, c.route([]string{"uptimes", in.CheckID}), checkAlarmMaxBody, &raw, &credentials)
	if err != nil {
		return nil, err
	}
	snapshot, err := responseObject(raw, "id", "subtype", "history_alarm", "locations")
	if err != nil {
		return nil, checkAlarmResponseError(op, "InvalidResponse")
	}
	id, ok1 := responseString(snapshot["id"])
	subtype, ok2 := responseString(snapshot["subtype"])
	alarm, ok3 := responseString(snapshot["history_alarm"])
	var locations []string
	err = json.Unmarshal(snapshot["locations"], &locations)
	if !ok1 || !ok2 || subtype == "" || !ok3 || id != in.CheckID || err != nil || locations == nil {
		return nil, checkAlarmResponseError(op, "InvalidResponse")
	}
	seen := map[string]bool{}
	texts := []string{id, subtype, alarm}
	for _, loc := range locations {
		if core.CheckPathID(op, "LocationID", loc) != nil || seen[loc] {
			return nil, checkAlarmResponseError(op, "InvalidResponse")
		}
		seen[loc] = true
		texts = append(texts, loc)
	}
	if !responseSafe(texts, credentials) {
		return nil, checkAlarmResponseError(op, "InvalidResponse")
	}
	if alarm != "" && core.CheckPathID(op, "HistoryAlarmID", alarm) != nil {
		return nil, checkAlarmResponseError(op, "InvalidResponse")
	}
	if subtype != "HTTP" {
		return nil, &core.APIError{Operation: op, Code: "InvalidInput", Message: "check subtype must be HTTP", Err: core.ErrInvalidInput}
	}
	if alarm == "" {
		return nil, &core.APIError{Operation: op, Code: "NotFound", Message: "check has no alarm reference", Err: core.ErrNotFound}
	}
	q := url.Values{"name": {responseMetric}, "dimensions": {"syntt_id:" + in.CheckID}, "statistics": {"avg"}, "start_time": {strconv.FormatInt(start.UnixMilli(), 10)}, "end_time": {strconv.FormatInt(end.UnixMilli(), 10)}, "group_by": {"location_id"}, "alarm": {alarm}}
	blocked := false
	ctx = transport.WithCredentialObserver(ctx, func(value string) ([]string, error) {
		if !credentials.add([]string{value}) || !responseSafe(texts, credentials) {
			blocked = true
			return nil, errors.New("credential safety check failed")
		}
		return credentials, nil
	})
	_, err = c.readCheckAlarmJSON(ctx, op, c.alarmRoute([]string{"statistics", "synthetics"}, q), checkHistoryMaxBody, &raw, &credentials)
	if blocked {
		return nil, checkAlarmResponseError(op, "InvalidResponse")
	}
	if err != nil {
		return nil, err
	}
	if !responseSafe(texts, credentials) {
		return nil, checkAlarmResponseError(op, "InvalidResponse")
	}
	decoded := responseSeriesDecoder{locations: seen, start: start, end: end, credentials: credentials}
	err = decoded.UnmarshalJSON(raw)
	if decoded.limit {
		return nil, checkAlarmResponseError(op, "ResponseLimitExceeded")
	}
	if err != nil {
		return nil, checkAlarmResponseError(op, "InvalidResponse")
	}
	slices.SortFunc(decoded.items, func(a, b CheckResponseTimeSeries) int { return strings.Compare(a.LocationID, b.LocationID) })
	return &ListCheckResponseTimesOutput{Items: decoded.items, Start: start, End: end}, nil
}

var responseRFC3339 = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}[Tt][0-9]{2}:[0-9]{2}:[0-9]{2}(\.[0-9]+)?([Zz]|[+-]([01][0-9]|2[0-3]):[0-5][0-9])$`)

func responseParseTime(value string) (time.Time, error) {
	if !responseRFC3339.MatchString(value) {
		return time.Time{}, errors.New("invalid RFC 3339 timestamp")
	}
	return time.Parse(time.RFC3339, strings.ToUpper(value))
}

func responseValidYear(t time.Time) bool {
	return t.Year() >= 1 && t.Year() <= 9999
}

func responseSafe(texts []string, credentials monitorReadCredentials) bool {
	for _, text := range texts {
		if checkAlarmText(text, credentials) != text {
			return false
		}
	}
	return true
}

func responseString(raw json.RawMessage) (string, bool) {
	var s string
	ok := len(raw) > 0 && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) && json.Unmarshal(raw, &s) == nil
	return s, ok
}

func responseObject(raw json.RawMessage, keys ...string) (map[string]json.RawMessage, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	tok, err := dec.Token()
	if err != nil || tok != json.Delim('{') {
		return nil, errors.New("invalid object")
	}
	wanted := map[string]bool{}
	for _, key := range keys {
		wanted[key] = true
	}
	out := map[string]json.RawMessage{}
	for dec.More() {
		tok, err = dec.Token()
		if err != nil {
			return nil, err
		}
		key, ok := tok.(string)
		if !ok {
			return nil, errors.New("invalid key")
		}
		var v json.RawMessage
		if err = dec.Decode(&v); err != nil {
			return nil, err
		}
		if wanted[key] {
			if _, exists := out[key]; exists {
				return nil, errors.New("duplicate key")
			}
			out[key] = v
		}
	}
	if _, err = dec.Token(); err != nil {
		return nil, err
	}
	if _, err = dec.Token(); err != io.EOF {
		return nil, errors.New("trailing value")
	}
	return out, nil
}

type responseSeriesDecoder struct {
	locations   map[string]bool
	start, end  time.Time
	items       []CheckResponseTimeSeries
	credentials monitorReadCredentials
	limit       bool
}

func (d *responseSeriesDecoder) UnmarshalJSON(raw []byte) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	tok, err := dec.Token()
	if err != nil || tok != json.Delim('[') {
		return errors.New("invalid array")
	}
	d.items = []CheckResponseTimeSeries{}
	seen := map[string]bool{}
	count := 0
	for dec.More() {
		if len(d.items) >= 64 {
			d.limit = true
			return errors.New("series limit")
		}
		var group json.RawMessage
		if err = dec.Decode(&group); err != nil {
			return err
		}
		obj, e := responseObject(group, "name", "dimensions", "statistics")
		if e != nil {
			return e
		}
		name, ok := responseString(obj["name"])
		if !ok || name != responseMetric {
			return errors.New("invalid name")
		}
		dims, e := responseObject(obj["dimensions"], "location_id")
		if e != nil {
			return e
		}
		loc, ok := responseString(dims["location_id"])
		if !ok || !d.locations[loc] || seen[loc] {
			return errors.New("invalid location")
		}
		seen[loc] = true
		if !responseSafe([]string{name, loc}, d.credentials) {
			return errors.New("reflected credential")
		}
		points := json.NewDecoder(bytes.NewReader(obj["statistics"]))
		tok, e = points.Token()
		if e != nil || tok != json.Delim('[') {
			return errors.New("invalid statistics")
		}
		series := CheckResponseTimeSeries{LocationID: loc, Points: []CheckResponseTimePoint{}}
		buckets := map[time.Time]bool{}
		early := 0
		for points.More() {
			if count >= 100000 {
				d.limit = true
				return errors.New("point limit")
			}
			count++
			var point json.RawMessage
			if e = points.Decode(&point); e != nil {
				return e
			}
			p, e := responseObject(point, "key", "value")
			if e != nil {
				return e
			}
			key, a := responseString(p["key"])
			value, b := responseString(p["value"])
			if !a || !b {
				return errors.New("invalid point")
			}
			if !responseSafe([]string{key, value}, d.credentials) {
				return errors.New("reflected credential")
			}
			timestamp, e := responseParseTime(key)
			if e != nil {
				return e
			}
			timestamp = timestamp.UTC()
			if !responseValidYear(timestamp) {
				return errors.New("invalid point year")
			}
			if timestamp.Before(d.start) {
				early++
				if d.end.Sub(d.start) != 24*time.Hour || timestamp.Before(d.start.Add(-5*time.Minute)) || early > 1 {
					return errors.New("early point")
				}
			}
			if timestamp.After(d.end) || buckets[timestamp] {
				return errors.New("invalid bucket")
			}
			buckets[timestamp] = true
			if !responseDecimal(value) {
				return errors.New("invalid value")
			}
			v, e := strconv.ParseFloat(value, 64)
			if e != nil || math.IsInf(v, 0) || math.IsNaN(v) {
				return errors.New("invalid value")
			}
			series.Points = append(series.Points, CheckResponseTimePoint{Timestamp: timestamp, ValueMillis: v})
		}
		if _, e = points.Token(); e != nil {
			return e
		}
		if _, e = points.Token(); !errors.Is(e, io.EOF) {
			return errors.New("trailing statistics")
		}
		slices.SortFunc(series.Points, func(a, b CheckResponseTimePoint) int { return a.Timestamp.Compare(b.Timestamp) })
		d.items = append(d.items, series)
	}
	if _, err = dec.Token(); err != nil {
		return err
	}
	if _, err = dec.Token(); err != io.EOF {
		return errors.New("trailing array")
	}
	return nil
}

func responseDecimal(s string) bool {
	if s == "" {
		return false
	}
	dot := false
	digits := 0
	for _, r := range s {
		if r == '.' && !dot && digits > 0 {
			dot = true
			digits = 0
			continue
		}
		if r < '0' || r > '9' {
			return false
		}
		digits++
	}
	return digits > 0
}
