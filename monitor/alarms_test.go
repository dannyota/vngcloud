package monitor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/testutil"
)

// TestListAlarmsQueryParametersDefaults loads ListAlarmsLog.json only for
// its paging envelope; that fixture's item is synthetic (see
// TestListAlarmsDecodesLogFixture), but this test does not inspect it.
func TestListAlarmsQueryParametersDefaults(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("method = %s", r.Method)
		}
		if r.URL.Path != "/vmonitor-api/api/v1/alarms/list" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		q := r.URL.Query()
		if q.Get("type-alarm") != AlarmKindLog {
			t.Fatalf("type-alarm = %q, want %q", q.Get("type-alarm"), AlarmKindLog)
		}
		if q.Get("name") != "" || q.Get("status") != "" || q.Get("severity") != "" {
			t.Fatalf("unexpected filter query: %v", q)
		}
		if q.Get("page") != "1" || q.Get("size") != strconv.Itoa(core.DefaultPageSize) {
			t.Fatalf("unexpected paging query: %v", q)
		}
		testutil.WriteFixture(t, w, "../testdata/monitor/ListAlarmsLog.json")
	}))

	if _, err := client.ListAlarms(context.Background(), &ListAlarmsInput{Kind: AlarmKindLog}); err != nil {
		t.Fatalf("ListAlarms() error = %v", err)
	}
}

// TestListAlarmsQueryParametersFiltersAndPaging loads ListAlarmsMetric.json
// only for its paging envelope; that fixture's item is synthetic (see
// TestListAlarmsDecodesMetricFixture), but this test does not inspect it.
func TestListAlarmsQueryParametersFiltersAndPaging(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("type-alarm") != AlarmKindMetric {
			t.Fatalf("type-alarm = %q, want %q", q.Get("type-alarm"), AlarmKindMetric)
		}
		if q.Get("name") != "my-alarm" || q.Get("status") != "OK" || q.Get("severity") != "HIGH" {
			t.Fatalf("unexpected filter query: %v", q)
		}
		if q.Get("page") != "2" || q.Get("size") != "5" {
			t.Fatalf("unexpected paging query: %v", q)
		}
		testutil.WriteFixture(t, w, "../testdata/monitor/ListAlarmsMetric.json")
	}))

	in := &ListAlarmsInput{
		Kind:     AlarmKindMetric,
		Name:     "my-alarm",
		Status:   "OK",
		Severity: "HIGH",
		Page:     2,
		Size:     5,
	}
	if _, err := client.ListAlarms(context.Background(), in); err != nil {
		t.Fatalf("ListAlarms() error = %v", err)
	}
}

func TestListAlarmsMissingKind(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("unexpected request for a missing Kind")
	}))
	if _, err := client.ListAlarms(context.Background(), &ListAlarmsInput{}); !errors.Is(err, core.ErrInvalidInput) {
		t.Fatalf("ListAlarms() error = %v, want ErrInvalidInput", err)
	}
	if _, err := client.ListAlarms(context.Background(), nil); !errors.Is(err, core.ErrInvalidInput) {
		t.Fatalf("ListAlarms(nil) error = %v, want ErrInvalidInput", err)
	}
}

// TestListAlarmsDecodesLogFixture also covers the design's log alarm
// channel reference: inAlarm and ok, each a comma-joined channel ID string
// with a trailing comma, decode into Log.InAlarm and Log.OK. The fixture is
// synthetic: the design's alarm list call was only seen live with an empty
// result, so this item's fields are hand-built, not a live capture.
func TestListAlarmsDecodesLogFixture(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		testutil.WriteFixture(t, w, "../testdata/monitor/ListAlarmsLog.json")
	}))

	out, err := client.ListAlarms(context.Background(), &ListAlarmsInput{Kind: AlarmKindLog})
	if err != nil {
		t.Fatalf("ListAlarms() error = %v", err)
	}
	if out.TotalItem != 1 || out.TotalPage != 1 || out.Page != 1 || out.PageSize != 10000 {
		t.Fatalf("unexpected paging: %+v", out)
	}
	if len(out.Items) != 1 {
		t.Fatalf("unexpected items: %+v", out.Items)
	}

	got := out.Items[0]
	if got.ID != "alarm-1" || got.Name != "example-log-alarm" {
		t.Fatalf("unexpected identity: %+v", got)
	}
	if got.Status != "OK" || got.Severity != "MEDIUM" {
		t.Fatalf("unexpected status/severity: %+v", got)
	}
	if got.Kind != AlarmKindLog {
		t.Fatalf("Kind = %q, want %q", got.Kind, AlarmKindLog)
	}
	if got.MetricMappingID != "" {
		t.Fatalf("MetricMappingID = %q, want empty", got.MetricMappingID)
	}
	if got.Log == nil {
		t.Fatal("Log = nil, want non-nil for a Log alarm")
	}
	if want := []string{"channel-1", "channel-2"}; !slices.Equal(got.Log.InAlarm, want) {
		t.Fatalf("Log.InAlarm = %v, want %v", got.Log.InAlarm, want)
	}
	if want := []string{"channel-1"}; !slices.Equal(got.Log.OK, want) {
		t.Fatalf("Log.OK = %v, want %v", got.Log.OK, want)
	}
}

// TestListAlarmsDecodesMetricFixture also covers the design's metric alarm
// channel reference: a Metric alarm names a channel by metricMappingId
// rather than by a comma-joined list of channel IDs. The fixture is
// synthetic, for the same reason TestListAlarmsDecodesLogFixture's is.
func TestListAlarmsDecodesMetricFixture(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		testutil.WriteFixture(t, w, "../testdata/monitor/ListAlarmsMetric.json")
	}))

	out, err := client.ListAlarms(context.Background(), &ListAlarmsInput{Kind: AlarmKindMetric})
	if err != nil {
		t.Fatalf("ListAlarms() error = %v", err)
	}
	if len(out.Items) != 1 {
		t.Fatalf("unexpected items: %+v", out.Items)
	}

	got := out.Items[0]
	if got.ID != "alarm-2" || got.Name != "example-metric-alarm" {
		t.Fatalf("unexpected identity: %+v", got)
	}
	if got.Status != "In-alarm" || got.Severity != "HIGH" {
		t.Fatalf("unexpected status/severity: %+v", got)
	}
	if got.Kind != AlarmKindMetric {
		t.Fatalf("Kind = %q, want %q", got.Kind, AlarmKindMetric)
	}
	if got.MetricMappingID != "metric-map-1" {
		t.Fatalf("MetricMappingID = %q, want %q", got.MetricMappingID, "metric-map-1")
	}
	if got.Log != nil {
		t.Fatalf("Log = %+v, want nil for a Metric alarm", got.Log)
	}
}

// TestListAlarmsSetsLogAndMetricMappingIDFromKind checks that Log and
// MetricMappingID come from in.Kind, not from which of the two the response
// happens to carry: a response naming both, which the design says should
// never happen but the SDK's decode does not assume, still ends up with
// only the field its own Kind filter names set.
func TestListAlarmsSetsLogAndMetricMappingIDFromKind(t *testing.T) {
	const raw = `{
		"lstData": [{"id":"alarm-3","name":"both-fields","status":"OK","severity":"LOW",
			"inAlarm":"channel-1,","ok":"","metricMappingId":"metric-map-2"}],
		"page": 1, "pageSize": 10, "totalPage": 1, "totalItem": 1
	}`

	for _, tt := range []struct {
		kind        string
		wantLog     bool
		wantMapping string
	}{
		{AlarmKindLog, true, ""},
		{AlarmKindMetric, false, "metric-map-2"},
	} {
		t.Run(tt.kind, func(t *testing.T) {
			client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(raw))
			}))
			out, err := client.ListAlarms(context.Background(), &ListAlarmsInput{Kind: tt.kind})
			if err != nil {
				t.Fatalf("ListAlarms() error = %v", err)
			}
			got := out.Items[0]
			if (got.Log != nil) != tt.wantLog {
				t.Fatalf("Log = %+v, want non-nil: %v", got.Log, tt.wantLog)
			}
			if got.MetricMappingID != tt.wantMapping {
				t.Fatalf("MetricMappingID = %q, want %q", got.MetricMappingID, tt.wantMapping)
			}
		})
	}
}

// TestGetAlarmDecodesFixture uses a synthetic fixture: the design's Get
// alarm call is console code only, never seen live, so GetAlarm.json is
// hand-built to exercise the decode, not a sanitized live capture. It
// covers the corrected read model: Kind decoded from the top-level type,
// and every log field nested under alarmLog rather than at the top level.
func TestGetAlarmDecodesFixture(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("method = %s", r.Method)
		}
		if r.URL.Path != "/vmonitor-api/api/v1/alarms/alarm-1" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		testutil.WriteFixture(t, w, "../testdata/monitor/GetAlarm.json")
	}))

	out, err := client.GetAlarm(context.Background(), &GetAlarmInput{AlarmID: "alarm-1"})
	if err != nil {
		t.Fatalf("GetAlarm() error = %v", err)
	}
	got := out.Alarm
	if got.ID != "alarm-1" || got.Name != "example-log-alarm" {
		t.Fatalf("unexpected identity: %+v", got)
	}
	if got.Description != "Alerts when error logs spike" {
		t.Fatalf("Description = %q", got.Description)
	}
	// GetAlarm sets no Kind filter of its own, so Kind comes from the
	// response's own type field rather than staying empty.
	if got.Kind != AlarmKindLog {
		t.Fatalf("Kind = %q, want %q", got.Kind, AlarmKindLog)
	}
	if got.Status != "OK" || got.Severity != "MEDIUM" {
		t.Fatalf("unexpected status/severity: %+v", got)
	}
	if got.Log == nil {
		t.Fatal("Log = nil, want non-nil")
	}
	log := got.Log
	if log.LogProjectID != "proj-1" || log.LogProjectName != "example-project" {
		t.Fatalf("unexpected project reference: %+v", log)
	}
	if log.QueryString != "status:500" {
		t.Fatalf("QueryString = %q", log.QueryString)
	}
	if len(log.Filter) == 0 || !strings.Contains(string(log.Filter), "status:500") {
		t.Fatalf("Filter = %s, want it to carry the query", log.Filter)
	}
	if log.ThresholdType != "frequency" || log.Condition != "gt" {
		t.Fatalf("unexpected threshold shape: %+v", log)
	}
	if log.ThresholdValue != 100 || log.TimeFrame != 5 {
		t.Fatalf("ThresholdValue = %v, TimeFrame = %v", log.ThresholdValue, log.TimeFrame)
	}
	if log.GroupByField != "" {
		t.Fatalf("GroupByField = %q, want empty", log.GroupByField)
	}
	if want := []string{"channel-1"}; !slices.Equal(log.InAlarm, want) {
		t.Fatalf("Log.InAlarm = %v, want %v", log.InAlarm, want)
	}
	// ok is an empty string in the fixture: no channel alerts on leaving the
	// alarm state, which must decode to nil rather than a slice with an
	// empty element.
	if log.OK != nil {
		t.Fatalf("Log.OK = %v, want nil", log.OK)
	}
	wantResend := LogAlarmResend{Enabled: false, Statuses: []string{"ALARM"}, Period: 30, Times: 0}
	if log.Resend.Enabled != wantResend.Enabled || log.Resend.Period != wantResend.Period ||
		log.Resend.Times != wantResend.Times || !slices.Equal(log.Resend.Statuses, wantResend.Statuses) {
		t.Fatalf("Resend = %+v, want %+v", log.Resend, wantResend)
	}
}

// TestGetAlarmFallsBackWithoutAlarmLog covers a response with no alarmLog
// at all: the design's fallback for a shape that has not been fully
// confirmed. Only InAlarm and OK decode; every other LogAlarmDetail field
// stays zero, and Kind stays empty since this raw response also sends no
// type.
func TestGetAlarmFallsBackWithoutAlarmLog(t *testing.T) {
	const raw = `{"data":{"id":"alarm-2","name":"legacy-alarm","status":"OK","severity":"LOW","inAlarm":"channel-1,channel-2,","ok":"channel-1,"}}`
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(raw))
	}))

	out, err := client.GetAlarm(context.Background(), &GetAlarmInput{AlarmID: "alarm-2"})
	if err != nil {
		t.Fatalf("GetAlarm() error = %v", err)
	}
	got := out.Alarm
	if got.Kind != "" {
		t.Fatalf("Kind = %q, want empty: no type field in this response", got.Kind)
	}
	if got.Log == nil {
		t.Fatal("Log = nil, want non-nil from the top-level fallback")
	}
	if want := []string{"channel-1", "channel-2"}; !slices.Equal(got.Log.InAlarm, want) {
		t.Fatalf("Log.InAlarm = %v, want %v", got.Log.InAlarm, want)
	}
	if want := []string{"channel-1"}; !slices.Equal(got.Log.OK, want) {
		t.Fatalf("Log.OK = %v, want %v", got.Log.OK, want)
	}
	if got.Log.QueryString != "" || got.Log.ThresholdType != "" || len(got.Log.Filter) != 0 {
		t.Fatalf("unexpected non-zero fallback fields: %+v", got.Log)
	}
}

// TestAlarmKindFromType covers Kind decoding from the top-level type field:
// LOG and METRIC map to the two AlarmKind constants, and any other value,
// including a missing type, leaves Kind empty rather than guessed.
func TestAlarmKindFromType(t *testing.T) {
	cases := []struct {
		raw  string
		want string
	}{
		{`{"id":"a","type":"LOG"}`, AlarmKindLog},
		{`{"id":"a","type":"Log"}`, AlarmKindLog},
		{`{"id":"a","type":"METRIC"}`, AlarmKindMetric},
		{`{"id":"a","type":"other"}`, ""},
		{`{"id":"a"}`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			var a Alarm
			if err := json.Unmarshal([]byte(tc.raw), &a); err != nil {
				t.Fatalf("Unmarshal() error = %v", err)
			}
			if a.Kind != tc.want {
				t.Fatalf("Kind = %q, want %q", a.Kind, tc.want)
			}
		})
	}
}

// TestLogAlarmDetailDecodesStringThresholdValueAndTimeFrame covers the
// design's note that thresholdValue and timeFrame may arrive as a JSON
// number or a numeric string, since the console's own create form does not
// convert either to a number before sending it.
func TestLogAlarmDetailDecodesStringThresholdValueAndTimeFrame(t *testing.T) {
	const raw = `{"thresholdValue":"12.5","timeFrame":"10"}`
	var d LogAlarmDetail
	if err := json.Unmarshal([]byte(raw), &d); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if d.ThresholdValue != 12.5 {
		t.Fatalf("ThresholdValue = %v, want 12.5", d.ThresholdValue)
	}
	if d.TimeFrame != 10 {
		t.Fatalf("TimeFrame = %v, want 10", d.TimeFrame)
	}
}

// TestLogAlarmDetailDecodesOddNumericAndGroupByFieldShapes covers fields
// that used to fail LogAlarmDetail's whole decode: an empty thresholdValue,
// a string resendPeriod/resendTimes, and a non-string groupByField. None of
// them fail the decode now; each falls back to its zero value instead.
func TestLogAlarmDetailDecodesOddNumericAndGroupByFieldShapes(t *testing.T) {
	const raw = `{"thresholdValue":"","resendPeriod":"15","resendTimes":"abc","groupByField":true}`
	var d LogAlarmDetail
	if err := json.Unmarshal([]byte(raw), &d); err != nil {
		t.Fatalf("Unmarshal() error = %v, want no error", err)
	}
	if d.ThresholdValue != 0 {
		t.Fatalf("ThresholdValue = %v, want 0", d.ThresholdValue)
	}
	if d.Resend.Period != 15 {
		t.Fatalf("Resend.Period = %v, want 15", d.Resend.Period)
	}
	if d.Resend.Times != 0 {
		t.Fatalf("Resend.Times = %v, want 0", d.Resend.Times)
	}
	if d.GroupByField != "" {
		t.Fatalf("GroupByField = %q, want empty", d.GroupByField)
	}
}

// TestGetAlarmDecodesOddNumericFieldsWithoutFailing covers the same odd
// shapes at the GetAlarm level: a single alarm with a blank thresholdValue,
// a string resendPeriod, a non-numeric resendTimes, and a numeric
// groupByField must not fail the read.
func TestGetAlarmDecodesOddNumericFieldsWithoutFailing(t *testing.T) {
	const raw = `{"data":{"id":"alarm-5","name":"odd-alarm","type":"LOG","status":"OK","severity":"LOW",
		"alarmLog":{"logProject":"proj-1","logProjectName":"p","thresholdType":"frequency","condition":"gt",
		"thresholdValue":"","timeFrame":5,"groupByField":123,"resendPeriod":"","resendTimes":"n/a","inAlarm":"","ok":""}}}`
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(raw))
	}))
	out, err := client.GetAlarm(context.Background(), &GetAlarmInput{AlarmID: "alarm-5"})
	if err != nil {
		t.Fatalf("GetAlarm() error = %v, want no error for odd numeric fields", err)
	}
	if out.Alarm.Log.ThresholdValue != 0 {
		t.Fatalf("ThresholdValue = %v, want 0", out.Alarm.Log.ThresholdValue)
	}
	if out.Alarm.Log.Resend.Period != 0 || out.Alarm.Log.Resend.Times != 0 {
		t.Fatalf("Resend = %+v, want zero", out.Alarm.Log.Resend)
	}
	// groupByField:123 is a JSON number, which flexibleString keeps as its
	// exact digit string rather than treating it as empty.
	if out.Alarm.Log.GroupByField != "123" {
		t.Fatalf("GroupByField = %q, want %q", out.Alarm.Log.GroupByField, "123")
	}
}

// TestListAlarmsDecodesLogNestedFixture covers a list item whose Log field
// nests under alarmLog, the shape GetAlarm's own fixture uses, rather than
// the top-level inAlarm/ok fallback TestListAlarmsDecodesLogFixture covers.
// The fixture is synthetic, for the same reason that one is.
func TestListAlarmsDecodesLogNestedFixture(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		testutil.WriteFixture(t, w, "../testdata/monitor/ListAlarmsLogNested.json")
	}))

	out, err := client.ListAlarms(context.Background(), &ListAlarmsInput{Kind: AlarmKindLog})
	if err != nil {
		t.Fatalf("ListAlarms() error = %v", err)
	}
	if len(out.Items) != 1 {
		t.Fatalf("unexpected items: %+v", out.Items)
	}
	got := out.Items[0]
	if got.Log == nil {
		t.Fatal("Log = nil, want non-nil for a Log alarm")
	}
	if got.Log.LogProjectID != "proj-1" || got.Log.LogProjectName != "example-project" {
		t.Fatalf("unexpected project reference: %+v", got.Log)
	}
	if got.Log.ThresholdType != "frequency" || got.Log.Condition != "gt" ||
		got.Log.ThresholdValue != 100 || got.Log.TimeFrame != 5 {
		t.Fatalf("unexpected threshold fields: %+v", got.Log)
	}
	if len(got.Log.Filter) == 0 {
		t.Fatalf("Filter = %v, want present", got.Log.Filter)
	}
	if want := []string{"channel-1", "channel-2"}; !slices.Equal(got.Log.InAlarm, want) {
		t.Fatalf("Log.InAlarm = %v, want %v", got.Log.InAlarm, want)
	}
}

// TestAlarmIDDecodesStringOrNumber checks Alarm.ID accepts either shape a
// guessed-type field might arrive in: the design has not confirmed whether
// the API sends it as a string or a number, and a numeric ID must not fail
// the whole item's decode.
func TestAlarmIDDecodesStringOrNumber(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{"string", `{"id":"alarm-9","name":"n"}`, "alarm-9"},
		{"number", `{"id":42,"name":"n"}`, "42"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var a Alarm
			if err := json.Unmarshal([]byte(tt.raw), &a); err != nil {
				t.Fatalf("Unmarshal() error = %v", err)
			}
			if a.ID != tt.want {
				t.Fatalf("ID = %q, want %q", a.ID, tt.want)
			}
		})
	}
}

func TestGetAlarmNotFound(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"not found"}`))
	}))

	_, err := client.GetAlarm(context.Background(), &GetAlarmInput{AlarmID: "alarm-1"})
	if !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("GetAlarm() error = %v, want ErrNotFound", err)
	}
}

func TestGetAlarmMissingAlarmID(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("unexpected request for a missing AlarmID")
	}))
	if _, err := client.GetAlarm(context.Background(), &GetAlarmInput{}); !errors.Is(err, core.ErrInvalidInput) {
		t.Fatalf("GetAlarm() error = %v, want ErrInvalidInput", err)
	}
	if _, err := client.GetAlarm(context.Background(), nil); !errors.Is(err, core.ErrInvalidInput) {
		t.Fatalf("GetAlarm(nil) error = %v, want ErrInvalidInput", err)
	}
}

func TestGetAlarmPathIDRejection(t *testing.T) {
	for _, id := range []string{"..", ".", "/", ""} {
		t.Run(fmt.Sprintf("%q", id), func(t *testing.T) {
			client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Fatal("unexpected request for a rejected AlarmID")
			}))
			_, err := client.GetAlarm(context.Background(), &GetAlarmInput{AlarmID: id})
			if !errors.Is(err, core.ErrInvalidInput) {
				t.Fatalf("GetAlarm(%q) error = %v, want ErrInvalidInput", id, err)
			}
		})
	}
}

// TestSplitChannelIDs covers the design's exact comma-joined format: each
// channel ID followed by a comma, including the last one, and checks that
// every empty element a malformed value produces is dropped, not just a
// trailing one.
func TestSplitChannelIDs(t *testing.T) {
	one := "channel-1,"
	two := "channel-1,channel-2,"
	empty := ""
	noTrailingComma := "channel-1"
	doubleComma := "channel-1,,channel-2,"
	leadingComma := ",channel-1,"

	cases := []struct {
		name string
		raw  *string
		want []string
	}{
		{"nil", nil, nil},
		{"empty", &empty, nil},
		{"one", &one, []string{"channel-1"}},
		{"two", &two, []string{"channel-1", "channel-2"}},
		{"no trailing comma", &noTrailingComma, []string{"channel-1"}},
		{"interior empty from a doubled comma", &doubleComma, []string{"channel-1", "channel-2"}},
		{"leading comma", &leadingComma, []string{"channel-1"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := splitChannelIDs(tc.raw)
			if !slices.Equal(got, tc.want) {
				t.Fatalf("splitChannelIDs(%v) = %v, want %v", tc.raw, got, tc.want)
			}
		})
	}
}

func assertLiveLogAlarm(t *testing.T, got Alarm) {
	t.Helper()
	if got.Kind != AlarmKindLog || got.Status != "ACTIVE" || got.Severity != "LOW" {
		t.Fatalf("kind/status/severity = %q/%q/%q", got.Kind, got.Status, got.Severity)
	}
	if got.Log == nil {
		t.Fatal("Log = nil")
	}
	l := got.Log
	if l.LogProjectID != "<project-id>" || l.LogProjectName != "vngcloud-live-logalarm" {
		t.Fatalf("project = %q / %q", l.LogProjectID, l.LogProjectName)
	}
	if l.QueryString != "*" || l.rawLogSearchQuery != "[]" || l.ThresholdValue != 1000 || l.TimeFrame != 5 {
		t.Fatalf("unexpected detail: %+v", l)
	}
	if !slices.Equal(l.InAlarm, []string{"<channel-id>"}) || len(l.OK) != 0 {
		t.Fatalf("channels = %v / %v", l.InAlarm, l.OK)
	}
}

func TestLiveLogAlarmShapeDecodes(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/vmonitor-api/api/v1/alarms/list" {
			testutil.WriteFixture(t, w, "../testdata/monitor/ListAlarmsLogLive.json")
			return
		}
		testutil.WriteFixture(t, w, "../testdata/monitor/GetAlarmLogLive.json")
	}))
	list, err := client.ListAlarms(context.Background(), &ListAlarmsInput{Kind: AlarmKindLog})
	if err != nil || len(list.Items) != 1 {
		t.Fatalf("ListAlarms() = %v, %v", list, err)
	}
	assertLiveLogAlarm(t, list.Items[0])
	got, err := client.GetAlarm(context.Background(), &GetAlarmInput{AlarmID: "x"})
	if err != nil {
		t.Fatalf("GetAlarm() error = %v", err)
	}
	assertLiveLogAlarm(t, got.Alarm)

	var del struct{ Data Alarm }
	raw, err := os.ReadFile("../testdata/monitor/DeleteLogAlarmLive.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &del); err != nil {
		t.Fatal(err)
	}
	assertLiveLogAlarm(t, del.Data)
}
