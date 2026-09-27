package monitor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"sync/atomic"
	"testing"

	"danny.vn/vngcloud/dns"
	"danny.vn/vngcloud/internal/core"
)

// noExistingLogAlarmsPage is an empty ListAlarms page, standing in for the
// pre-create duplicate-name check CreateLogAlarm runs before reading the
// log project or creating anything.
const noExistingLogAlarmsPage = `{"lstData":[],"page":1,"pageSize":10000,"totalPage":0,"totalItem":0}`

// logAlarmListPage builds one ListAlarms page holding a single item named
// name at status, or none when name is "".
func logAlarmListPage(id, name, status string) string {
	if name == "" {
		return noExistingLogAlarmsPage
	}
	return fmt.Sprintf(`{"lstData":[{"id":%q,"name":%q,"status":%q,"type":"LOG"}],"page":1,"pageSize":10000,"totalPage":1,"totalItem":1}`, id, name, status)
}

// exampleLogProjectBody is a getLogProject response, standing in for the
// project CreateLogAlarm and UpdateLogAlarm read for its name.
func exampleLogProjectBody(id, name string) string {
	return fmt.Sprintf(`{"id":%q,"name":%q,"status":"ACTIVE","billingStatus":"ACTIVE"}`, id, name)
}

// --- CreateLogAlarm: body construction ---

func TestCreateLogAlarmBodyDefaults(t *testing.T) {
	var postCalls atomic.Int64
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/vmonitor-api/api/v1/alarms/list":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(noExistingLogAlarmsPage))
		case r.URL.Path == "/log-api/v1/projects/proj-1":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(exampleLogProjectBody("proj-1", "example-project")))
		case r.URL.Path == "/vmonitor-api/api/v1/alarms/logs" && r.Method == http.MethodPost:
			postCalls.Add(1)
			body := decodeLogProjectBody(t, r)
			checkCreateLogAlarmDefaultBody(t, body)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"alarm-1"}`))
		default:
			t.Fatalf("unexpected request to %s %s", r.Method, r.URL.Path)
		}
	}))

	out, err := client.CreateLogAlarm(context.Background(), &CreateLogAlarmInput{
		Name: "my-alarm", LogProjectID: "proj-1", ThresholdValue: ptrFloat(100), NoWait: true,
	})
	if err != nil {
		t.Fatalf("CreateLogAlarm() error = %v", err)
	}
	if out.AlarmID != "alarm-1" {
		t.Fatalf("AlarmID = %q, want alarm-1", out.AlarmID)
	}
	if postCalls.Load() != 1 {
		t.Fatalf("post calls = %d, want 1", postCalls.Load())
	}
}

func checkCreateLogAlarmDefaultBody(t *testing.T, body map[string]any) {
	t.Helper()
	if body["name"] != "my-alarm" || body["description"] != "" || body["severity"] != "LOW" {
		t.Fatalf("unexpected identity fields: %+v", body)
	}
	if body["logProjectId"] != "proj-1" || body["projectName"] != "example-project" || body["zone"] != "" {
		t.Fatalf("unexpected project fields: %+v", body)
	}
	if body["queryString"] != "*" || body["logSearchQuery"] != "[]" {
		t.Fatalf("unexpected query fields: %+v", body)
	}
	var wantFilter, gotFilter any
	if err := json.Unmarshal([]byte(matchAllLogFilter), &wantFilter); err != nil {
		t.Fatalf("parse matchAllLogFilter: %v", err)
	}
	gotFilter = body["filter"]
	if fmt.Sprint(gotFilter) != fmt.Sprint(wantFilter) {
		t.Fatalf("filter = %v, want %v", gotFilter, wantFilter)
	}
	if body["thresholdType"] != "frequency" || body["condition"] != "gt" {
		t.Fatalf("unexpected threshold fields: %+v", body)
	}
	if body["thresholdValue"] != 100.0 || body["timeFrame"] != 5.0 {
		t.Fatalf("unexpected numeric fields: %+v", body)
	}
	if v, ok := body["groupByField"]; !ok || v != nil {
		t.Fatalf("groupByField = %v, ok = %v, want present and null", v, ok)
	}
	if _, ok := body["metricAggKey"]; ok {
		t.Fatalf("metricAggKey present, want omitted when unset: %+v", body)
	}
	if _, ok := body["metricAggType"]; ok {
		t.Fatalf("metricAggType present, want omitted when unset: %+v", body)
	}
	if body["reason"] != "query(*) >100" {
		t.Fatalf("reason = %q", body["reason"])
	}
	if body["inAlarm"] != "" || body["ok"] != "" || body["undetermined"] != "" {
		t.Fatalf("unexpected channel fields: %+v", body)
	}
	if body["resendEnabled"] != false || body["resendPeriod"] != 30.0 || body["resendTimes"] != 0.0 || body["resendStatus"] != "ALARM" {
		t.Fatalf("unexpected resend fields: %+v", body)
	}
}

func TestCreateLogAlarmBodyAllFieldsFrequency(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/vmonitor-api/api/v1/alarms/list":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(noExistingLogAlarmsPage))
		case r.URL.Path == "/log-api/v1/projects/proj-1":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(exampleLogProjectBody("proj-1", "example-project")))
		case r.URL.Path == "/vmonitor-api/api/v1/alarms/logs" && r.Method == http.MethodPost:
			body := decodeLogProjectBody(t, r)
			if body["description"] != "desc" || body["severity"] != "HIGH" {
				t.Fatalf("unexpected fields: %+v", body)
			}
			if body["queryString"] != "status:500" || body["logSearchQuery"] != "[]" {
				t.Fatalf("unexpected query fields: %+v", body)
			}
			filterJSON, err := json.Marshal(body["filter"])
			if err != nil {
				t.Fatalf("marshal filter: %v", err)
			}
			var gotFilter, wantFilter any
			_ = json.Unmarshal(filterJSON, &gotFilter)
			_ = json.Unmarshal([]byte(`{"type":"bool","value":{"filter":[],"should":[],"must":[{"a":1}],"mustNot":[]}}`), &wantFilter)
			if fmt.Sprint(gotFilter) != fmt.Sprint(wantFilter) {
				t.Fatalf("filter = %s", filterJSON)
			}
			if body["condition"] != "lte" || body["thresholdValue"] != 12.5 || body["timeFrame"] != 10.0 {
				t.Fatalf("unexpected threshold fields: %+v", body)
			}
			if body["groupByField"] != "host" {
				t.Fatalf("groupByField = %v", body["groupByField"])
			}
			if body["inAlarm"] != "ch-1,ch-2," || body["ok"] != "ch-3," {
				t.Fatalf("unexpected channel fields: %+v", body)
			}
			if body["resendEnabled"] != true || body["resendPeriod"] != 15.0 || body["resendTimes"] != 3.0 || body["resendStatus"] != "OK,ALARM" {
				t.Fatalf("unexpected resend fields: %+v", body)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"alarm-1"}`))
		default:
			t.Fatalf("unexpected request to %s %s", r.Method, r.URL.Path)
		}
	}))

	_, err := client.CreateLogAlarm(context.Background(), &CreateLogAlarmInput{
		Name: "my-alarm", LogProjectID: "proj-1", ThresholdValue: ptrFloat(12.5), NoWait: true,
		Description:   "desc",
		Severity:      LogAlarmSeverityHigh,
		QueryString:   "status:500",
		Filter:        json.RawMessage(`{"type":"bool","value":{"filter":[],"should":[],"must":[{"a":1}],"mustNot":[]}}`),
		ThresholdType: LogAlarmThresholdTypeFrequency,
		Condition:     LogAlarmConditionLTE,
		TimeFrame:     10,
		GroupByField:  "host",
		InAlarm:       []string{"ch-1", "ch-2"},
		OK:            []string{"ch-3"},
		Resend:        &LogAlarmResend{Enabled: true, Statuses: []string{"OK", "ALARM"}, Period: 15, Times: 3},
	})
	if err != nil {
		t.Fatalf("CreateLogAlarm() error = %v", err)
	}
}

func TestCreateLogAlarmBodyMetricAggregation(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/vmonitor-api/api/v1/alarms/list":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(noExistingLogAlarmsPage))
		case r.URL.Path == "/log-api/v1/projects/proj-1":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(exampleLogProjectBody("proj-1", "example-project")))
		case r.URL.Path == "/vmonitor-api/api/v1/alarms/logs" && r.Method == http.MethodPost:
			body := decodeLogProjectBody(t, r)
			if body["metricAggKey"] != "host" || body["metricAggType"] != "cardinality" {
				t.Fatalf("unexpected agg fields: %+v", body)
			}
			if v, ok := body["groupByField"]; !ok || v != nil {
				t.Fatalf("groupByField = %v, ok = %v, want present and null", v, ok)
			}
			if body["reason"] != "cardinality[host](*) >5" {
				t.Fatalf("reason = %q", body["reason"])
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"alarm-1"}`))
		default:
			t.Fatalf("unexpected request to %s %s", r.Method, r.URL.Path)
		}
	}))

	_, err := client.CreateLogAlarm(context.Background(), &CreateLogAlarmInput{
		Name: "my-alarm", LogProjectID: "proj-1", ThresholdValue: ptrFloat(5), NoWait: true,
		ThresholdType: LogAlarmThresholdTypeMetricAggregation,
		AggField:      "host",
		AggType:       "cardinality",
	})
	if err != nil {
		t.Fatalf("CreateLogAlarm() error = %v", err)
	}
}

// --- shared body-builder unit tests ---

func TestJoinChannelIDs(t *testing.T) {
	cases := []struct {
		ids  []string
		want string
	}{
		{nil, ""},
		{[]string{}, ""},
		{[]string{"a"}, "a,"},
		{[]string{"a", "b"}, "a,b,"},
	}
	for _, tc := range cases {
		if got := joinChannelIDs(tc.ids); got != tc.want {
			t.Fatalf("joinChannelIDs(%v) = %q, want %q", tc.ids, got, tc.want)
		}
	}
}

func TestBuildLogAlarmReason(t *testing.T) {
	cases := []struct {
		name string
		f    logAlarmFields
		q    string
		want string
	}{
		{"gt", logAlarmFields{Condition: LogAlarmConditionGT, ThresholdValue: 5}, "q", "query(q) >5"},
		{"gte", logAlarmFields{Condition: LogAlarmConditionGTE, ThresholdValue: 5}, "q", "query(q)  >= 5"},
		{"lt", logAlarmFields{Condition: LogAlarmConditionLT, ThresholdValue: 5}, "q", "query(q) <5"},
		{"lte", logAlarmFields{Condition: LogAlarmConditionLTE, ThresholdValue: 5}, "q", "query(q)  <= 5"},
		{
			"metric_aggregation",
			logAlarmFields{Condition: LogAlarmConditionGT, ThresholdType: LogAlarmThresholdTypeMetricAggregation, ThresholdValue: 5, AggField: "host", AggType: "cardinality"},
			"q", "cardinality[host](q) >5",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := buildLogAlarmReason(tc.f, tc.q); got != tc.want {
				t.Fatalf("buildLogAlarmReason() = %q, want %q", got, tc.want)
			}
		})
	}
}

// --- CreateLogAlarm: QueryString/Filter pairing ---

func TestCreateLogAlarmQueryFilterPairingRefusal(t *testing.T) {
	cases := []struct {
		name   string
		query  string
		filter json.RawMessage
	}{
		{"query without filter", "status:500", nil},
		{"filter without query", "", json.RawMessage(`{"a":1}`)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Fatalf("unexpected request for a pairing violation: %s", r.URL.Path)
			}))
			_, err := client.CreateLogAlarm(context.Background(), &CreateLogAlarmInput{
				Name: "a", LogProjectID: "proj-1", ThresholdValue: ptrFloat(1),
				QueryString: tc.query, Filter: tc.filter,
			})
			if !errors.Is(err, core.ErrInvalidInput) {
				t.Fatalf("CreateLogAlarm() error = %v, want ErrInvalidInput", err)
			}
		})
	}
}

func TestCreateLogAlarmFilterMustBeObject(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("unexpected request for a non-object filter: %s", r.URL.Path)
	}))
	_, err := client.CreateLogAlarm(context.Background(), &CreateLogAlarmInput{
		Name: "a", LogProjectID: "proj-1", ThresholdValue: ptrFloat(1),
		QueryString: "q", Filter: json.RawMessage(`["not","an","object"]`),
	})
	if !errors.Is(err, core.ErrInvalidInput) {
		t.Fatalf("CreateLogAlarm() error = %v, want ErrInvalidInput", err)
	}
}

// --- CreateLogAlarm: duplicate name and project lookup ---

func TestCreateLogAlarmRefusesExistingName(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/vmonitor-api/api/v1/alarms/list":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(logAlarmListPage("alarm-0", "my-alarm", "OK")))
		default:
			t.Fatalf("unexpected request to %s after a taken name: %s", r.Method, r.URL.Path)
		}
	}))
	_, err := client.CreateLogAlarm(context.Background(), &CreateLogAlarmInput{
		Name: "my-alarm", LogProjectID: "proj-1", ThresholdValue: ptrFloat(1),
	})
	if !errors.Is(err, core.ErrInvalidInput) {
		t.Fatalf("CreateLogAlarm() error = %v, want ErrInvalidInput", err)
	}
}

func TestCreateLogAlarmProjectNotFound(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/vmonitor-api/api/v1/alarms/list":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(noExistingLogAlarmsPage))
		case "/log-api/v1/projects/proj-missing":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"not found"}`))
		default:
			t.Fatalf("unexpected request to %s after a missing project: %s", r.Method, r.URL.Path)
		}
	}))
	_, err := client.CreateLogAlarm(context.Background(), &CreateLogAlarmInput{
		Name: "my-alarm", LogProjectID: "proj-missing", ThresholdValue: ptrFloat(1),
	})
	if !core.IsNotFound(err) {
		t.Fatalf("CreateLogAlarm() error = %v, want NotFound", err)
	}
}

// --- CreateLogAlarm: create retry and id resolution ---

func TestCreateLogAlarmNotRetriedAfter502(t *testing.T) {
	var postCalls atomic.Int64
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/vmonitor-api/api/v1/alarms/list":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(noExistingLogAlarmsPage))
		case "/log-api/v1/projects/proj-1":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(exampleLogProjectBody("proj-1", "example-project")))
		case "/vmonitor-api/api/v1/alarms/logs":
			postCalls.Add(1)
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(`{"message":"upstream error"}`))
		default:
			t.Fatalf("unexpected request to %s %s", r.Method, r.URL.Path)
		}
	}))
	_, err := client.CreateLogAlarm(context.Background(), &CreateLogAlarmInput{
		Name: "my-alarm", LogProjectID: "proj-1", ThresholdValue: ptrFloat(1),
	})
	if err == nil {
		t.Fatal("CreateLogAlarm() error = nil, want an error")
	}
	if postCalls.Load() != 1 {
		t.Fatalf("post calls = %d, want 1 (no retry after a 5xx)", postCalls.Load())
	}
	if got := err.Error(); !containsStr(got, "list log alarms by name") {
		t.Fatalf("error = %q, want a hint to list by name", got)
	}
}

func TestCreateLogAlarmIDFromResponse(t *testing.T) {
	cases := []struct {
		name string
		resp string
	}{
		{"data.id", `{"data":{"id":"alarm-9"}}`},
		{"top-level id", `{"id":"alarm-9"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/vmonitor-api/api/v1/alarms/list":
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(noExistingLogAlarmsPage))
				case "/log-api/v1/projects/proj-1":
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(exampleLogProjectBody("proj-1", "example-project")))
				case "/vmonitor-api/api/v1/alarms/logs":
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(tc.resp))
				default:
					t.Fatalf("unexpected request to %s %s", r.Method, r.URL.Path)
				}
			}))
			out, err := client.CreateLogAlarm(context.Background(), &CreateLogAlarmInput{
				Name: "my-alarm", LogProjectID: "proj-1", ThresholdValue: ptrFloat(1), NoWait: true,
			})
			if err != nil {
				t.Fatalf("CreateLogAlarm() error = %v", err)
			}
			if out.AlarmID != "alarm-9" {
				t.Fatalf("AlarmID = %q, want alarm-9", out.AlarmID)
			}
		})
	}
}

// TestCreateLogAlarmIgnoresUnseenResponseShapes covers a create response
// the console ignores and never confirmed a shape for: a data field holding
// a plain string or bool, and a body that is not JSON at all. None of them
// fail the create; NoWait shows the decode found no id in any of the three.
func TestCreateLogAlarmIgnoresUnseenResponseShapes(t *testing.T) {
	cases := []struct {
		name        string
		body        string
		contentType string
	}{
		{"data is a success string", `{"data":"success"}`, "application/json"},
		{"data is a bool", `{"data":true}`, "application/json"},
		{"a non-JSON body", "OK", "text/plain"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/vmonitor-api/api/v1/alarms/list":
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(noExistingLogAlarmsPage))
				case "/log-api/v1/projects/proj-1":
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(exampleLogProjectBody("proj-1", "example-project")))
				case "/vmonitor-api/api/v1/alarms/logs":
					w.Header().Set("Content-Type", tc.contentType)
					_, _ = w.Write([]byte(tc.body))
				default:
					t.Fatalf("unexpected request to %s %s", r.Method, r.URL.Path)
				}
			}))
			out, err := client.CreateLogAlarm(context.Background(), &CreateLogAlarmInput{
				Name: "my-alarm", LogProjectID: "proj-1", ThresholdValue: ptrFloat(1), NoWait: true,
			})
			if err != nil {
				t.Fatalf("CreateLogAlarm() error = %v, want no error for an unseen response shape", err)
			}
			if out.AlarmID != "" {
				t.Fatalf("AlarmID = %q, want empty: this response shape carries no id", out.AlarmID)
			}
		})
	}
}

// TestCreateLogAlarm502WithNonJSONBodyStillErrors checks that swallowing a
// decode failure on an accepted status does not also swallow a real
// server error: a 502 must still fail the create even though its body is
// not JSON either.
func TestCreateLogAlarm502WithNonJSONBodyStillErrors(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/vmonitor-api/api/v1/alarms/list":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(noExistingLogAlarmsPage))
		case "/log-api/v1/projects/proj-1":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(exampleLogProjectBody("proj-1", "example-project")))
		case "/vmonitor-api/api/v1/alarms/logs":
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte("upstream error"))
		default:
			t.Fatalf("unexpected request to %s %s", r.Method, r.URL.Path)
		}
	}))
	_, err := client.CreateLogAlarm(context.Background(), &CreateLogAlarmInput{
		Name: "my-alarm", LogProjectID: "proj-1", ThresholdValue: ptrFloat(1),
	})
	if err == nil {
		t.Fatal("CreateLogAlarm() error = nil, want an error for a 502")
	}
}

// --- CreateLogAlarm: waits ---

func TestCreateLogAlarmWaitByIDSettles(t *testing.T) {
	var getCalls atomic.Int64
	client := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/vmonitor-api/api/v1/alarms/list":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(noExistingLogAlarmsPage))
		case "/log-api/v1/projects/proj-1":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(exampleLogProjectBody("proj-1", "example-project")))
		case "/vmonitor-api/api/v1/alarms/logs":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"alarm-1"}`))
		case "/vmonitor-api/api/v1/alarms/alarm-1":
			n := getCalls.Add(1)
			status := "OK"
			if n == 1 {
				status = LogAlarmStatusCreating
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"data":{"id":"alarm-1","name":"my-alarm","type":"LOG","status":%q}}`, status)
		default:
			t.Fatalf("unexpected request to %s %s", r.Method, r.URL.Path)
		}
	})))

	out, err := client.CreateLogAlarm(context.Background(), &CreateLogAlarmInput{
		Name: "my-alarm", LogProjectID: "proj-1", ThresholdValue: ptrFloat(1),
	})
	if err != nil {
		t.Fatalf("CreateLogAlarm() error = %v", err)
	}
	if out.AlarmID != "alarm-1" || out.Alarm.Status != "OK" {
		t.Fatalf("unexpected output: %+v", out)
	}
	if getCalls.Load() != 2 {
		t.Fatalf("get calls = %d, want 2", getCalls.Load())
	}
}

func TestCreateLogAlarmWaitByNameWhenNoID(t *testing.T) {
	var listCalls atomic.Int64
	client := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/vmonitor-api/api/v1/alarms/list":
			n := listCalls.Add(1)
			w.Header().Set("Content-Type", "application/json")
			switch n {
			case 1: // pre-create duplicate-name check
				_, _ = w.Write([]byte(noExistingLogAlarmsPage))
			case 2: // first wait poll: not visible yet
				_, _ = w.Write([]byte(noExistingLogAlarmsPage))
			case 3: // second wait poll: creating
				_, _ = w.Write([]byte(logAlarmListPage("alarm-1", "my-alarm", LogAlarmStatusCreating)))
			default: // third wait poll: settled
				_, _ = w.Write([]byte(logAlarmListPage("alarm-1", "my-alarm", "OK")))
			}
		case "/log-api/v1/projects/proj-1":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(exampleLogProjectBody("proj-1", "example-project")))
		case "/vmonitor-api/api/v1/alarms/logs":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{}`))
		default:
			t.Fatalf("unexpected request to %s %s", r.Method, r.URL.Path)
		}
	})))

	out, err := client.CreateLogAlarm(context.Background(), &CreateLogAlarmInput{
		Name: "my-alarm", LogProjectID: "proj-1", ThresholdValue: ptrFloat(1),
	})
	if err != nil {
		t.Fatalf("CreateLogAlarm() error = %v", err)
	}
	if out.AlarmID != "alarm-1" || out.Alarm.Status != "OK" {
		t.Fatalf("unexpected output: %+v", out)
	}
	if listCalls.Load() != 4 {
		t.Fatalf("list calls = %d, want 4", listCalls.Load())
	}
}

func TestCreateLogAlarmWaitTimesOut(t *testing.T) {
	client := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/vmonitor-api/api/v1/alarms/list":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(noExistingLogAlarmsPage))
		case "/log-api/v1/projects/proj-1":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(exampleLogProjectBody("proj-1", "example-project")))
		case "/vmonitor-api/api/v1/alarms/logs":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"alarm-1"}`))
		case "/vmonitor-api/api/v1/alarms/alarm-1":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":{"id":"alarm-1","name":"my-alarm","type":"LOG","status":"CREATING"}}`))
		default:
			t.Fatalf("unexpected request to %s %s", r.Method, r.URL.Path)
		}
	})))

	out, err := client.CreateLogAlarm(context.Background(), &CreateLogAlarmInput{
		Name: "my-alarm", LogProjectID: "proj-1", ThresholdValue: ptrFloat(1),
	})
	if !errors.Is(err, dns.ErrNotSettled) {
		t.Fatalf("CreateLogAlarm() error = %v, want ErrNotSettled", err)
	}
	if out == nil || out.Alarm.Status != LogAlarmStatusCreating {
		t.Fatalf("unexpected output: %+v", out)
	}
}

func TestCreateLogAlarmNoWaitSkipsWait(t *testing.T) {
	var afterCreateCalls atomic.Int64
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/vmonitor-api/api/v1/alarms/list":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(noExistingLogAlarmsPage))
		case "/log-api/v1/projects/proj-1":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(exampleLogProjectBody("proj-1", "example-project")))
		case "/vmonitor-api/api/v1/alarms/logs":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"alarm-1"}`))
		case "/vmonitor-api/api/v1/alarms/alarm-1":
			afterCreateCalls.Add(1)
			t.Fatal("NoWait must not read the alarm after create")
		default:
			t.Fatalf("unexpected request to %s %s", r.Method, r.URL.Path)
		}
	}))
	out, err := client.CreateLogAlarm(context.Background(), &CreateLogAlarmInput{
		Name: "my-alarm", LogProjectID: "proj-1", ThresholdValue: ptrFloat(1), NoWait: true,
	})
	if err != nil {
		t.Fatalf("CreateLogAlarm() error = %v", err)
	}
	if out.AlarmID != "alarm-1" || out.Alarm != (Alarm{}) {
		t.Fatalf("unexpected output: %+v", out)
	}
}

// --- CreateLogAlarm: input validation ---

func TestCreateLogAlarmMissingRequiredFields(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("unexpected request for a missing required field")
	}))
	base := CreateLogAlarmInput{Name: "a", LogProjectID: "proj-1", ThresholdValue: ptrFloat(1)}

	missingName := base
	missingName.Name = ""
	missingProject := base
	missingProject.LogProjectID = ""
	missingThreshold := base
	missingThreshold.ThresholdValue = nil

	for _, in := range []*CreateLogAlarmInput{&missingName, &missingProject, &missingThreshold} {
		if _, err := client.CreateLogAlarm(context.Background(), in); !errors.Is(err, core.ErrInvalidInput) {
			t.Fatalf("CreateLogAlarm(%+v) error = %v, want ErrInvalidInput", in, err)
		}
	}
	if _, err := client.CreateLogAlarm(context.Background(), nil); !errors.Is(err, core.ErrInvalidInput) {
		t.Fatalf("CreateLogAlarm(nil) error = %v, want ErrInvalidInput", err)
	}
}

func TestCreateLogAlarmZeroThresholdValueIsValid(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/vmonitor-api/api/v1/alarms/list":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(noExistingLogAlarmsPage))
		case "/log-api/v1/projects/proj-1":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(exampleLogProjectBody("proj-1", "example-project")))
		case "/vmonitor-api/api/v1/alarms/logs":
			body := decodeLogProjectBody(t, r)
			if body["thresholdValue"] != 0.0 {
				t.Fatalf("thresholdValue = %v, want 0", body["thresholdValue"])
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"alarm-1"}`))
		default:
			t.Fatalf("unexpected request to %s %s", r.Method, r.URL.Path)
		}
	}))
	_, err := client.CreateLogAlarm(context.Background(), &CreateLogAlarmInput{
		Name: "my-alarm", LogProjectID: "proj-1", ThresholdValue: ptrFloat(0), NoWait: true,
	})
	if err != nil {
		t.Fatalf("CreateLogAlarm() error = %v", err)
	}
}

// TestCreateLogAlarmRejectsNaNOrInfThresholdValue covers the same guard
// CreateLogProject runs for MaxPrice: a NaN or infinite ThresholdValue
// would make the create body's reason and comparison meaningless, so it is
// refused before any request rather than sent to the server.
func TestCreateLogAlarmRejectsNaNOrInfThresholdValue(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("unexpected request for a NaN/Inf ThresholdValue: %s %s", r.Method, r.URL.Path)
	}))
	for _, v := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		_, err := client.CreateLogAlarm(context.Background(), &CreateLogAlarmInput{
			Name: "a", LogProjectID: "proj-1", ThresholdValue: ptrFloat(v),
		})
		if !errors.Is(err, core.ErrInvalidInput) {
			t.Fatalf("CreateLogAlarm(%v) error = %v, want ErrInvalidInput", v, err)
		}
	}
}

func TestCreateLogAlarmChannelIDRejectsComma(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("unexpected request for a rejected channel id")
	}))
	_, err := client.CreateLogAlarm(context.Background(), &CreateLogAlarmInput{
		Name: "a", LogProjectID: "proj-1", ThresholdValue: ptrFloat(1), InAlarm: []string{"a,b"},
	})
	if !errors.Is(err, core.ErrInvalidInput) {
		t.Fatalf("CreateLogAlarm() error = %v, want ErrInvalidInput", err)
	}
}

func TestCreateLogAlarmLogProjectIDPathRejection(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("unexpected request for a rejected LogProjectID")
	}))
	_, err := client.CreateLogAlarm(context.Background(), &CreateLogAlarmInput{
		Name: "a", LogProjectID: "..", ThresholdValue: ptrFloat(1),
	})
	if !errors.Is(err, core.ErrInvalidInput) {
		t.Fatalf("CreateLogAlarm() error = %v, want ErrInvalidInput", err)
	}
}

// ptrFloat is a local *float64 helper so tests do not need vngcloud.Ptr.
func ptrFloat(v float64) *float64 { return &v }

func containsStr(s, substr string) bool {
	return len(s) >= len(substr) && (func() bool {
		for i := 0; i+len(substr) <= len(s); i++ {
			if s[i:i+len(substr)] == substr {
				return true
			}
		}
		return false
	})()
}

// --- UpdateLogAlarm ---

// existingLogAlarmRaw is a synthetic GetAlarm response for a fully
// populated log alarm, standing in for the alarm UpdateLogAlarm reads
// before merging.
const existingLogAlarmRaw = `{
	"data": {
		"id": "alarm-1",
		"name": "existing-alarm",
		"description": "old description",
		"type": "LOG",
		"status": "OK",
		"severity": "LOW",
		"alarmLog": {
			"logProject": "proj-1",
			"logProjectName": "old-project",
			"queryString": "status:500",
			"logSearchQuery": "[{\"field\":\"token\"}]",
			"filter": {"type":"bool","value":{"filter":[],"should":[],"must":[{"a":1}],"mustNot":[]}},
			"thresholdType": "frequency",
			"condition": "gt",
			"thresholdValue": 100,
			"timeFrame": 5,
			"groupByField": "host",
			"reason": "query(status:500) >100",
			"inAlarm": "ch-1,",
			"ok": "ch-2,",
			"undetermined": "",
			"resendEnabled": true,
			"resendPeriod": 15,
			"resendTimes": 2,
			"resendStatus": "OK,ALARM"
		}
	}
}`

// existingLogAlarmNoFilterRaw is the same alarm but with no filter key at
// all in alarmLog, covering the design's "a read without filter sends
// none".
const existingLogAlarmNoFilterRaw = `{
	"data": {
		"id": "alarm-1",
		"name": "existing-alarm",
		"type": "LOG",
		"status": "OK",
		"severity": "LOW",
		"alarmLog": {
			"logProject": "proj-1",
			"logProjectName": "old-project",
			"queryString": "status:500",
			"logSearchQuery": "[]",
			"thresholdType": "frequency",
			"condition": "gt",
			"thresholdValue": 100,
			"timeFrame": 5,
			"inAlarm": "",
			"ok": ""
		}
	}
}`

func TestUpdateLogAlarmMergeUnsetFieldsResendReadValues(t *testing.T) {
	var projectCalls atomic.Int64
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/vmonitor-api/api/v1/alarms/alarm-1" && r.Method == http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(existingLogAlarmRaw))
		case r.URL.Path == "/log-api/v1/projects/proj-1":
			projectCalls.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(exampleLogProjectBody("proj-1", "example-project")))
		case r.URL.Path == "/vmonitor-api/api/v1/alarms/logs/alarm-1" && r.Method == http.MethodPut:
			body := decodeLogProjectBody(t, r)
			checkUpdateLogAlarmMergedBody(t, body)
			w.WriteHeader(http.StatusOK)
		default:
			t.Fatalf("unexpected request to %s %s", r.Method, r.URL.Path)
		}
	}))

	_, err := client.UpdateLogAlarm(context.Background(), &UpdateLogAlarmInput{
		AlarmID: "alarm-1", NoWait: true, Description: ptrStr("new description"),
	})
	if err != nil {
		t.Fatalf("UpdateLogAlarm() error = %v", err)
	}
	if projectCalls.Load() != 0 {
		t.Fatalf("GetLogProject called %d times, want 0: LogProjectID was not touched", projectCalls.Load())
	}
}

func checkUpdateLogAlarmMergedBody(t *testing.T, body map[string]any) {
	t.Helper()
	if body["name"] != "existing-alarm" || body["description"] != "new description" || body["severity"] != "LOW" {
		t.Fatalf("unexpected identity fields: %+v", body)
	}
	if body["logProjectId"] != "proj-1" || body["projectName"] != "old-project" {
		t.Fatalf("unexpected project fields (LogProjectID untouched): %+v", body)
	}
	if body["queryString"] != "status:500" {
		t.Fatalf("queryString = %v, want unchanged", body["queryString"])
	}
	if body["logSearchQuery"] != `[{"field":"token"}]` {
		t.Fatalf("logSearchQuery = %v, want the read's own value preserved", body["logSearchQuery"])
	}
	if body["thresholdType"] != "frequency" || body["condition"] != "gt" || body["thresholdValue"] != 100.0 || body["timeFrame"] != 5.0 {
		t.Fatalf("unexpected threshold fields: %+v", body)
	}
	if body["groupByField"] != "host" {
		t.Fatalf("groupByField = %v, want unchanged", body["groupByField"])
	}
	if body["inAlarm"] != "ch-1," || body["ok"] != "ch-2," {
		t.Fatalf("unexpected channel fields: %+v", body)
	}
	if body["resendEnabled"] != true || body["resendPeriod"] != 15.0 || body["resendTimes"] != 2.0 || body["resendStatus"] != "OK,ALARM" {
		t.Fatalf("unexpected resend fields: %+v", body)
	}
	if body["reason"] != "query(status:500) >100" {
		t.Fatalf("reason = %q", body["reason"])
	}
}

func TestUpdateLogAlarmProjectChangeReReadsProjectName(t *testing.T) {
	var projectCalls atomic.Int64
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/vmonitor-api/api/v1/alarms/alarm-1" && r.Method == http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(existingLogAlarmRaw))
		case r.URL.Path == "/log-api/v1/projects/proj-2":
			projectCalls.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(exampleLogProjectBody("proj-2", "new-project")))
		case r.URL.Path == "/vmonitor-api/api/v1/alarms/logs/alarm-1" && r.Method == http.MethodPut:
			body := decodeLogProjectBody(t, r)
			if body["logProjectId"] != "proj-2" || body["projectName"] != "new-project" {
				t.Fatalf("unexpected project fields: %+v", body)
			}
			w.WriteHeader(http.StatusOK)
		default:
			t.Fatalf("unexpected request to %s %s", r.Method, r.URL.Path)
		}
	}))

	_, err := client.UpdateLogAlarm(context.Background(), &UpdateLogAlarmInput{
		AlarmID: "alarm-1", NoWait: true, LogProjectID: ptrStr("proj-2"),
	})
	if err != nil {
		t.Fatalf("UpdateLogAlarm() error = %v", err)
	}
	if projectCalls.Load() != 1 {
		t.Fatalf("GetLogProject called %d times, want 1", projectCalls.Load())
	}
}

func TestUpdateLogAlarmRefusesMetricAlarm(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/vmonitor-api/api/v1/alarms/alarm-1" && r.Method == http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":{"id":"alarm-1","name":"m","type":"METRIC","status":"OK","severity":"LOW","metricMappingId":"map-1"}}`))
		default:
			t.Fatalf("unexpected request to %s %s after a metric alarm refusal", r.Method, r.URL.Path)
		}
	}))
	_, err := client.UpdateLogAlarm(context.Background(), &UpdateLogAlarmInput{
		AlarmID: "alarm-1", NoWait: true, Description: ptrStr("x"),
	})
	if !errors.Is(err, core.ErrInvalidInput) {
		t.Fatalf("UpdateLogAlarm() error = %v, want ErrInvalidInput", err)
	}
}

// TestUpdateLogAlarmQueryFilterPairing covers the update pairing rule:
// QueryString and Filter must both be set or both left nil in the input
// itself, checked before any request, so a new value can never end up
// paired with the read's stale one for the other field.
func TestUpdateLogAlarmQueryFilterPairing(t *testing.T) {
	t.Run("one set alone is refused before any request", func(t *testing.T) {
		cases := []struct {
			name string
			in   *UpdateLogAlarmInput
		}{
			{"QueryString without Filter", &UpdateLogAlarmInput{AlarmID: "alarm-1", QueryString: ptrStr("new query")}},
			{"Filter without QueryString", &UpdateLogAlarmInput{AlarmID: "alarm-1", Filter: ptrRaw(`{"a":1}`)}},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					t.Fatalf("unexpected request for a one-sided QueryString/Filter update: %s %s", r.Method, r.URL.Path)
				}))
				_, err := client.UpdateLogAlarm(context.Background(), tc.in)
				if !errors.Is(err, core.ErrInvalidInput) {
					t.Fatalf("UpdateLogAlarm() error = %v, want ErrInvalidInput", err)
				}
			})
		}
	})

	t.Run("both set together sends the new pair, not the old one", func(t *testing.T) {
		client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.URL.Path == "/vmonitor-api/api/v1/alarms/alarm-1" && r.Method == http.MethodGet:
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(existingLogAlarmRaw))
			case r.URL.Path == "/vmonitor-api/api/v1/alarms/logs/alarm-1" && r.Method == http.MethodPut:
				body := decodeLogProjectBody(t, r)
				if body["queryString"] != "new query" {
					t.Fatalf("queryString = %v, want new query", body["queryString"])
				}
				filterJSON, err := json.Marshal(body["filter"])
				if err != nil {
					t.Fatalf("marshal filter: %v", err)
				}
				if string(filterJSON) != `{"b":2}` {
					t.Fatalf("filter = %s, want the new value, not the read's old one", filterJSON)
				}
				w.WriteHeader(http.StatusOK)
			default:
				t.Fatalf("unexpected request to %s %s", r.Method, r.URL.Path)
			}
		}))
		_, err := client.UpdateLogAlarm(context.Background(), &UpdateLogAlarmInput{
			AlarmID: "alarm-1", NoWait: true,
			QueryString: ptrStr("new query"), Filter: ptrRaw(`{"b":2}`),
		})
		if err != nil {
			t.Fatalf("UpdateLogAlarm() error = %v", err)
		}
	})

	t.Run("both left nil resends the read's own pairing unchanged", func(t *testing.T) {
		client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.URL.Path == "/vmonitor-api/api/v1/alarms/alarm-1" && r.Method == http.MethodGet:
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(existingLogAlarmNoFilterRaw))
			case r.URL.Path == "/vmonitor-api/api/v1/alarms/logs/alarm-1" && r.Method == http.MethodPut:
				body := decodeLogProjectBody(t, r)
				if _, ok := body["filter"]; ok {
					t.Fatalf("filter present, want omitted: %+v", body)
				}
				if body["queryString"] != "status:500" {
					t.Fatalf("queryString = %v, want unchanged", body["queryString"])
				}
				w.WriteHeader(http.StatusOK)
			default:
				t.Fatalf("unexpected request to %s %s", r.Method, r.URL.Path)
			}
		}))
		_, err := client.UpdateLogAlarm(context.Background(), &UpdateLogAlarmInput{
			AlarmID: "alarm-1", NoWait: true, Description: ptrStr("x"),
		})
		if err != nil {
			t.Fatalf("UpdateLogAlarm() error = %v", err)
		}
	})
}

// TestUpdateLogAlarmRejectsNaNOrInfThresholdValue mirrors
// TestCreateLogAlarmRejectsNaNOrInfThresholdValue for the update path; the
// check runs before the pre-update GetAlarm read, so no request is made.
func TestUpdateLogAlarmRejectsNaNOrInfThresholdValue(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("unexpected request for a NaN/Inf ThresholdValue: %s %s", r.Method, r.URL.Path)
	}))
	for _, v := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		_, err := client.UpdateLogAlarm(context.Background(), &UpdateLogAlarmInput{
			AlarmID: "alarm-1", ThresholdValue: ptrFloat(v),
		})
		if !errors.Is(err, core.ErrInvalidInput) {
			t.Fatalf("UpdateLogAlarm(%v) error = %v, want ErrInvalidInput", v, err)
		}
	}
}

// TestUpdateLogAlarmRefusesWhenLogAlarmDetailNil covers a read with Kind
// AlarmKindLog but no alarmLog and no top-level inAlarm/ok either, so
// current.Log stays nil: there is nothing to merge the update onto, and
// sending the body anyway would replace the alarm with mostly blank
// fields.
func TestUpdateLogAlarmRefusesWhenLogAlarmDetailNil(t *testing.T) {
	const raw = `{"data":{"id":"alarm-1","name":"legacy-alarm","type":"LOG","status":"OK","severity":"LOW"}}`
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/vmonitor-api/api/v1/alarms/alarm-1" && r.Method == http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(raw))
		default:
			t.Fatalf("unexpected request to %s %s after a log-detail-less read", r.Method, r.URL.Path)
		}
	}))
	_, err := client.UpdateLogAlarm(context.Background(), &UpdateLogAlarmInput{
		AlarmID: "alarm-1", NoWait: true, Description: ptrStr("x"),
	})
	if !errors.Is(err, core.ErrInvalidInput) {
		t.Fatalf("UpdateLogAlarm() error = %v, want ErrInvalidInput", err)
	}
}

// TestUpdateLogAlarmRefusesIncompleteLogDetail covers a read whose alarmLog
// is present but missing a field the create body always sends: LogProjectID,
// ThresholdType, Condition, or a nonzero TimeFrame. Each is refused before
// the PUT, one at a time, rather than sending a body with that field blank.
func TestUpdateLogAlarmRefusesIncompleteLogDetail(t *testing.T) {
	base := map[string]any{
		"logProject": "proj-1", "logProjectName": "old-project", "queryString": "status:500",
		"logSearchQuery": "[]", "thresholdType": "frequency", "condition": "gt",
		"thresholdValue": 100, "timeFrame": 5, "inAlarm": "", "ok": "",
	}
	cases := []struct {
		name  string
		strip string
	}{
		{"missing LogProjectID", "logProject"},
		{"missing ThresholdType", "thresholdType"},
		{"missing Condition", "condition"},
		{"zero TimeFrame", "timeFrame"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			alarmLog := make(map[string]any, len(base))
			for k, v := range base {
				alarmLog[k] = v
			}
			if tc.strip == "timeFrame" {
				alarmLog["timeFrame"] = 0
			} else {
				delete(alarmLog, tc.strip)
			}
			raw, err := json.Marshal(map[string]any{"data": map[string]any{
				"id": "alarm-1", "name": "partial-alarm", "type": "LOG", "status": "OK", "severity": "LOW",
				"alarmLog": alarmLog,
			}})
			if err != nil {
				t.Fatalf("marshal fixture: %v", err)
			}
			client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.URL.Path == "/vmonitor-api/api/v1/alarms/alarm-1" && r.Method == http.MethodGet:
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write(raw)
				default:
					t.Fatalf("unexpected request to %s %s after an incomplete read", r.Method, r.URL.Path)
				}
			}))
			_, err = client.UpdateLogAlarm(context.Background(), &UpdateLogAlarmInput{
				AlarmID: "alarm-1", NoWait: true, Description: ptrStr("x"),
			})
			if !errors.Is(err, core.ErrInvalidInput) {
				t.Fatalf("UpdateLogAlarm() error = %v, want ErrInvalidInput", err)
			}
		})
	}
}

// TestUpdateLogAlarmRefusesWhileCreatingOrUpdating covers the console's own
// rule: it blocks edits while an alarm's Status is CREATING or UPDATING.
func TestUpdateLogAlarmRefusesWhileCreatingOrUpdating(t *testing.T) {
	for _, status := range []string{LogAlarmStatusCreating, LogAlarmStatusUpdating} {
		t.Run(status, func(t *testing.T) {
			raw := fmt.Sprintf(`{"data":{"id":"alarm-1","name":"existing-alarm","type":"LOG","status":%q,"severity":"LOW",
				"alarmLog":{"logProject":"proj-1","logProjectName":"old-project","thresholdType":"frequency",
				"condition":"gt","thresholdValue":100,"timeFrame":5,"inAlarm":"","ok":""}}}`, status)
			client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.URL.Path == "/vmonitor-api/api/v1/alarms/alarm-1" && r.Method == http.MethodGet:
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(raw))
				default:
					t.Fatalf("unexpected request to %s %s while status is %s", r.Method, r.URL.Path, status)
				}
			}))
			_, err := client.UpdateLogAlarm(context.Background(), &UpdateLogAlarmInput{
				AlarmID: "alarm-1", NoWait: true, Description: ptrStr("x"),
			})
			if !errors.Is(err, core.ErrInvalidInput) {
				t.Fatalf("UpdateLogAlarm() error = %v, want ErrInvalidInput", err)
			}
		})
	}
}

func TestUpdateLogAlarmClearsChannelListWithEmptySlice(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/vmonitor-api/api/v1/alarms/alarm-1" && r.Method == http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(existingLogAlarmRaw))
		case r.URL.Path == "/vmonitor-api/api/v1/alarms/logs/alarm-1" && r.Method == http.MethodPut:
			body := decodeLogProjectBody(t, r)
			if body["inAlarm"] != "" {
				t.Fatalf("inAlarm = %v, want cleared", body["inAlarm"])
			}
			if body["ok"] != "ch-2," {
				t.Fatalf("ok = %v, want unchanged", body["ok"])
			}
			w.WriteHeader(http.StatusOK)
		default:
			t.Fatalf("unexpected request to %s %s", r.Method, r.URL.Path)
		}
	}))
	empty := []string{}
	_, err := client.UpdateLogAlarm(context.Background(), &UpdateLogAlarmInput{
		AlarmID: "alarm-1", NoWait: true, InAlarm: &empty,
	})
	if err != nil {
		t.Fatalf("UpdateLogAlarm() error = %v", err)
	}
}

func TestUpdateLogAlarmWaitSettles(t *testing.T) {
	var getCalls atomic.Int64
	client := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/vmonitor-api/api/v1/alarms/alarm-1" && r.Method == http.MethodGet:
			n := getCalls.Add(1)
			if n == 1 {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(existingLogAlarmRaw))
				return
			}
			status := "OK"
			if n == 2 {
				status = LogAlarmStatusUpdating
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"data":{"id":"alarm-1","name":"existing-alarm","type":"LOG","status":%q}}`, status)
		case r.URL.Path == "/vmonitor-api/api/v1/alarms/logs/alarm-1" && r.Method == http.MethodPut:
			w.WriteHeader(http.StatusOK)
		default:
			t.Fatalf("unexpected request to %s %s", r.Method, r.URL.Path)
		}
	})))

	out, err := client.UpdateLogAlarm(context.Background(), &UpdateLogAlarmInput{
		AlarmID: "alarm-1", Description: ptrStr("x"),
	})
	if err != nil {
		t.Fatalf("UpdateLogAlarm() error = %v", err)
	}
	if out.Alarm.Status != "OK" {
		t.Fatalf("Alarm.Status = %q, want OK", out.Alarm.Status)
	}
	if getCalls.Load() != 3 {
		t.Fatalf("get calls = %d, want 3", getCalls.Load())
	}
}

func TestUpdateLogAlarmNoWaitSkipsWait(t *testing.T) {
	var getCalls atomic.Int64
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/vmonitor-api/api/v1/alarms/alarm-1" && r.Method == http.MethodGet:
			getCalls.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(existingLogAlarmRaw))
		case r.URL.Path == "/vmonitor-api/api/v1/alarms/logs/alarm-1" && r.Method == http.MethodPut:
			w.WriteHeader(http.StatusOK)
		default:
			t.Fatalf("unexpected request to %s %s", r.Method, r.URL.Path)
		}
	}))
	out, err := client.UpdateLogAlarm(context.Background(), &UpdateLogAlarmInput{
		AlarmID: "alarm-1", NoWait: true, Description: ptrStr("x"),
	})
	if err != nil {
		t.Fatalf("UpdateLogAlarm() error = %v", err)
	}
	if out.Alarm != (Alarm{}) {
		t.Fatalf("Alarm = %+v, want zero value", out.Alarm)
	}
	if getCalls.Load() != 1 {
		t.Fatalf("get calls = %d, want 1 (the pre-update read only)", getCalls.Load())
	}
}

func TestUpdateLogAlarmWaitTimesOut(t *testing.T) {
	var getCalls atomic.Int64
	client := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/vmonitor-api/api/v1/alarms/alarm-1" && r.Method == http.MethodGet:
			n := getCalls.Add(1)
			if n == 1 {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(existingLogAlarmRaw))
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":{"id":"alarm-1","name":"existing-alarm","type":"LOG","status":"UPDATING"}}`))
		case r.URL.Path == "/vmonitor-api/api/v1/alarms/logs/alarm-1" && r.Method == http.MethodPut:
			w.WriteHeader(http.StatusOK)
		default:
			t.Fatalf("unexpected request to %s %s", r.Method, r.URL.Path)
		}
	})))
	_, err := client.UpdateLogAlarm(context.Background(), &UpdateLogAlarmInput{
		AlarmID: "alarm-1", Description: ptrStr("x"),
	})
	if !errors.Is(err, dns.ErrNotSettled) {
		t.Fatalf("UpdateLogAlarm() error = %v, want ErrNotSettled", err)
	}
}

func TestUpdateLogAlarmMissingAlarmID(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("unexpected request for a missing AlarmID")
	}))
	if _, err := client.UpdateLogAlarm(context.Background(), &UpdateLogAlarmInput{}); !errors.Is(err, core.ErrInvalidInput) {
		t.Fatalf("UpdateLogAlarm() error = %v, want ErrInvalidInput", err)
	}
	if _, err := client.UpdateLogAlarm(context.Background(), nil); !errors.Is(err, core.ErrInvalidInput) {
		t.Fatalf("UpdateLogAlarm(nil) error = %v, want ErrInvalidInput", err)
	}
}

func TestUpdateLogAlarmPathIDRejection(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("unexpected request for a rejected id")
	}))
	if _, err := client.UpdateLogAlarm(context.Background(), &UpdateLogAlarmInput{AlarmID: ".."}); !errors.Is(err, core.ErrInvalidInput) {
		t.Fatalf("UpdateLogAlarm() error = %v, want ErrInvalidInput", err)
	}
	badProject := ".."
	if _, err := client.UpdateLogAlarm(context.Background(), &UpdateLogAlarmInput{AlarmID: "alarm-1", LogProjectID: &badProject}); !errors.Is(err, core.ErrInvalidInput) {
		t.Fatalf("UpdateLogAlarm() error = %v, want ErrInvalidInput", err)
	}
	badChannels := []string{"a,b"}
	if _, err := client.UpdateLogAlarm(context.Background(), &UpdateLogAlarmInput{AlarmID: "alarm-1", InAlarm: &badChannels}); !errors.Is(err, core.ErrInvalidInput) {
		t.Fatalf("UpdateLogAlarm() error = %v, want ErrInvalidInput", err)
	}
}

// ptrStr is a local *string helper so tests do not need vngcloud.Ptr.
func ptrStr(v string) *string { return &v }

// ptrRaw is a local *json.RawMessage helper so tests do not need
// vngcloud.Ptr.
func ptrRaw(v string) *json.RawMessage {
	raw := json.RawMessage(v)
	return &raw
}

// --- DeleteLogAlarm ---

// TestDeleteLogAlarmSuccess covers the safety guard: DeleteLogAlarm reads
// the alarm first and only sends the DELETE once that read confirms Kind
// is AlarmKindLog.
func TestDeleteLogAlarmSuccess(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/vmonitor-api/api/v1/alarms/alarm-1" && r.Method == http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":{"id":"alarm-1","type":"LOG","status":"OK"}}`))
		case r.URL.Path == "/vmonitor-api/api/v1/alarms/logs/alarm-1" && r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected request to %s %s", r.Method, r.URL.Path)
		}
	}))
	out, err := client.DeleteLogAlarm(context.Background(), &DeleteLogAlarmInput{AlarmID: "alarm-1"})
	if err != nil {
		t.Fatalf("DeleteLogAlarm() error = %v", err)
	}
	if out == nil {
		t.Fatal("DeleteLogAlarm() output = nil")
	}
}

// TestDeleteLogAlarmRefusesMetricAlarm is the safety guard's main case: an
// id that reads back as a Metric alarm must never reach the log alarm
// DELETE path. No DELETE request is expected; the handler fatals if one is
// sent.
func TestDeleteLogAlarmRefusesMetricAlarm(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/vmonitor-api/api/v1/alarms/alarm-1" && r.Method == http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":{"id":"alarm-1","type":"METRIC","status":"OK","metricMappingId":"map-1"}}`))
		default:
			t.Fatalf("unexpected request to %s %s after a metric alarm refusal", r.Method, r.URL.Path)
		}
	}))
	_, err := client.DeleteLogAlarm(context.Background(), &DeleteLogAlarmInput{AlarmID: "alarm-1"})
	if !errors.Is(err, core.ErrInvalidInput) {
		t.Fatalf("DeleteLogAlarm() error = %v, want ErrInvalidInput", err)
	}
}

// TestDeleteLogAlarmNotFound covers a 404 on the pre-delete GetAlarm read:
// it returns the SDK's not-found sentinel directly, the same as a 404 on
// the DELETE itself would.
func TestDeleteLogAlarmNotFound(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"not found"}`))
	}))
	_, err := client.DeleteLogAlarm(context.Background(), &DeleteLogAlarmInput{AlarmID: "alarm-1"})
	if !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("DeleteLogAlarm() error = %v, want ErrNotFound", err)
	}
}

func TestDeleteLogAlarmMissingAlarmID(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("unexpected request for a missing AlarmID")
	}))
	if _, err := client.DeleteLogAlarm(context.Background(), &DeleteLogAlarmInput{}); !errors.Is(err, core.ErrInvalidInput) {
		t.Fatalf("DeleteLogAlarm() error = %v, want ErrInvalidInput", err)
	}
	if _, err := client.DeleteLogAlarm(context.Background(), nil); !errors.Is(err, core.ErrInvalidInput) {
		t.Fatalf("DeleteLogAlarm(nil) error = %v, want ErrInvalidInput", err)
	}
}

func TestDeleteLogAlarmPathIDRejection(t *testing.T) {
	for _, id := range []string{"..", ".", "/", ""} {
		t.Run(fmt.Sprintf("%q", id), func(t *testing.T) {
			client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Fatal("unexpected request for a rejected AlarmID")
			}))
			_, err := client.DeleteLogAlarm(context.Background(), &DeleteLogAlarmInput{AlarmID: id})
			if !errors.Is(err, core.ErrInvalidInput) {
				t.Fatalf("DeleteLogAlarm(%q) error = %v, want ErrInvalidInput", id, err)
			}
		})
	}
}
