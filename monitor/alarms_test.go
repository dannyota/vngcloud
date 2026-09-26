package monitor

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"testing"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/testutil"
)

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
// with a trailing comma, decode into Log.InAlarm and Log.OK.
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
// rather than by a comma-joined list of channel IDs.
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
	if got.Kind != AlarmKindLog {
		t.Fatalf("Kind = %q, want %q", got.Kind, AlarmKindLog)
	}
	if got.Log == nil {
		t.Fatal("Log = nil, want non-nil")
	}
	if want := []string{"channel-1"}; !slices.Equal(got.Log.InAlarm, want) {
		t.Fatalf("Log.InAlarm = %v, want %v", got.Log.InAlarm, want)
	}
	// ok is an empty string in the fixture: no channel alerts on leaving the
	// alarm state, which must decode to nil rather than a slice with an
	// empty element.
	if got.Log.OK != nil {
		t.Fatalf("Log.OK = %v, want nil", got.Log.OK)
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
// channel ID followed by a comma, including the last one.
func TestSplitChannelIDs(t *testing.T) {
	one := "channel-1,"
	two := "channel-1,channel-2,"
	empty := ""
	noTrailingComma := "channel-1"

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
