package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/monitor"
)

// noExistingLogAlarmsPage is an empty ListAlarms page, standing in for the
// pre-create duplicate-name check create-log-alarm runs before reading the
// log project or creating anything.
const noExistingLogAlarmsPage = `{"lstData":[],"page":1,"pageSize":10000,"totalPage":0,"totalItem":0}`

// exampleLogProjectForAlarmJSON is a GetLogProject response, standing in
// for the project create-log-alarm and update-log-alarm read for its name.
const exampleLogProjectForAlarmJSON = `{"id":"proj-1","name":"example-project","status":"ACTIVE","billingStatus":"ACTIVE"}`

// existingLogAlarmForUpdateJSON is a synthetic GetAlarm response for a
// fully populated log alarm, standing in for the alarm update-log-alarm
// reads before merging.
const existingLogAlarmForUpdateJSON = `{
	"data": {
		"id": "alarm-1",
		"name": "existing-alarm",
		"description": "old description",
		"type": "LOG",
		"status": "ACTIVE",
		"severity": "LOW",
		"alarmLog": {"id": "log-1",
			"logProject": "proj-1",
			"logProjectName": "old-project",
			"queryString": "status:500",
			"logSearchQuery": "[]",
			"thresholdType": "frequency",
			"condition": "gt",
			"thresholdValue": 100,
			"timeFrame": 5,
			"inAlarm": "ch-1,",
			"ok": "ch-2,"
		}
	}
}`

// decodeJSONBody decodes data, the raw bytes of a request body a fixture
// handler captured, into a map keyed by the wire's own field names, so a
// test can assert on individual fields without a full struct.
func decodeJSONBody(t *testing.T, data []byte) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(data, &body); err != nil {
		t.Fatalf("body is not valid JSON: %v (%s)", err, data)
	}
	return body
}

// TestMonitorCreateLogAlarmSendsRequestBody checks create-log-alarm's
// flag-to-input mapping end to end: every flag-settable CreateLogAlarmInput
// field reaches the create body the shared builder produces, and Filter,
// which has no flag, still reaches it through --cli-input-json alongside
// the other flags on the same command line.
func TestMonitorCreateLogAlarmSendsRequestBody(t *testing.T) {
	var createBody []byte
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/vmonitor-api/api/v1/alarms/list": jsonHandler(http.StatusOK, noExistingLogAlarmsPage),
		"/log-api/v1/projects/proj-1":      jsonHandler(http.StatusOK, exampleLogProjectForAlarmJSON),
		"/vmonitor-api/api/v1/alarms/logs": func(w http.ResponseWriter, r *http.Request) {
			defer func() { _ = r.Body.Close() }()
			if r.Method != http.MethodPost {
				t.Fatalf("method = %s, want POST", r.Method)
			}
			createBody, _ = io.ReadAll(r.Body)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"alarm-1"}`))
		},
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "monitor", "create-log-alarm",
		"--name", "my-alarm", "--log-project-id", "proj-1", "--threshold-value", "12.5",
		"--description", "desc", "--severity", "HIGH", "--query-string", "status:500",
		"--threshold-type", "frequency", "--condition", "lte", "--time-frame", "10",
		"--group-by-field", "host", "--no-wait",
		"--cli-input-json", `{"Filter":{"type":"bool","value":{"filter":[],"should":[],"must":[{"a":1}],"mustNot":[]}}}`,
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("create-log-alarm: %v (stderr=%s)", err, stderr.String())
	}

	body := decodeJSONBody(t, createBody)
	if body["name"] != "my-alarm" || body["description"] != "desc" || body["severity"] != "HIGH" {
		t.Fatalf("unexpected identity fields: %+v", body)
	}
	if body["logProjectId"] != "proj-1" || body["projectName"] != "example-project" {
		t.Fatalf("unexpected project fields: %+v", body)
	}
	if body["queryString"] != "status:500" {
		t.Fatalf("queryString = %v, want status:500", body["queryString"])
	}
	if _, ok := body["filter"]; !ok {
		t.Fatalf("filter missing, want the --cli-input-json value: %+v", body)
	}
	if body["thresholdType"] != "frequency" || body["condition"] != "lte" {
		t.Fatalf("unexpected threshold fields: %+v", body)
	}
	if body["thresholdValue"] != 12.5 || body["timeFrame"] != 10.0 {
		t.Fatalf("unexpected numeric fields: %+v", body)
	}
	if body["groupByField"] != "host" {
		t.Fatalf("groupByField = %v, want host", body["groupByField"])
	}

	out := stdout.String()
	if !strings.Contains(out, `"AlarmID": "alarm-1"`) {
		t.Fatalf("stdout = %s, want the create's AlarmID printed", out)
	}
}

// TestMonitorCreateLogAlarmReadOnlyRefusedWithZeroRequests checks the
// design's read-only rule: create-log-alarm is a Write operation, so a
// read-only profile refuses it with exit 2 before any request, including
// the duplicate-name check.
func TestMonitorCreateLogAlarmReadOnlyRefusedWithZeroRequests(t *testing.T) {
	home := withCleanEnv(t)
	writeConfigFile(t, home, "[profile agent]\nregion = hcm-3\nread_only = true\n")
	writeCredentialsFile(t, home, "[agent]\nusername = u\npassword = p\nroot_email = e@example.com\n")

	refuse := func(_ http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
	}
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/vmonitor-api/api/v1/alarms/list": refuse,
		"/log-api/v1/projects/proj-1":      refuse,
		"/vmonitor-api/api/v1/alarms/logs": refuse,
	})
	opts := newFakeServer(t, fixture.mux)
	withTestOptions(t, append(opts, vngcloud.WithStaticToken("test-token"))...)

	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	root := newRootCmd(strings.NewReader(""), stdout, stderr)
	root.SetArgs([]string{
		"--profile", "agent", "monitor", "create-log-alarm",
		"--name", "my-alarm", "--log-project-id", "proj-1", "--threshold-value", "1",
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected a read-only refusal")
	}
	if got := classify(err).Code; got != "ReadOnly" {
		t.Fatalf("Code = %q, want ReadOnly (stderr=%s)", got, stderr.String())
	}
	if got := exitCode(err); got != 2 {
		t.Fatalf("exitCode = %d, want 2", got)
	}
	if n := fixture.requestCount(); n != 0 {
		t.Fatalf("requestCount = %d, want 0", n)
	}
}

// TestMonitorCreateLogAlarmCLIInputJSONRejectsUnknownField checks that
// --cli-input-json strictness (input.go's exact-field-name check) holds for
// CreateLogAlarmInput: an unrecognized key is refused with exit code 2
// before any request.
func TestMonitorCreateLogAlarmCLIInputJSONRejectsUnknownField(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/vmonitor-api/api/v1/alarms/list": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "monitor", "create-log-alarm",
		"--cli-input-json", `{"Name":"my-alarm","Bogus":1}`,
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected a --cli-input-json refusal for an unknown field")
	}
	if got := exitCode(err); got != 2 {
		t.Fatalf("exitCode = %d, want 2 (stderr=%s)", got, stderr.String())
	}
	if !strings.Contains(err.Error(), `"Bogus"`) {
		t.Fatalf("error = %q, want it to name the unknown field", err.Error())
	}
	if n := fixture.requestCount(); n != 0 {
		t.Fatalf("requestCount = %d, want 0", n)
	}
}

// TestMonitorCreateLogAlarmThresholdValueNaNExitsWithZeroRequests checks
// the SDK's own NaN/infinite ThresholdValue refusal at the CLI level:
// --threshold-value NaN is refused with InvalidUsage before any request,
// including the duplicate-name check create-log-alarm otherwise runs
// first.
func TestMonitorCreateLogAlarmThresholdValueNaNExitsWithZeroRequests(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/vmonitor-api/api/v1/alarms/list": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
		"/log-api/v1/projects/proj-1": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
		"/vmonitor-api/api/v1/alarms/logs": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "monitor", "create-log-alarm",
		"--name", "my-alarm", "--log-project-id", "proj-1", "--threshold-value", "NaN",
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected an invalid-input refusal for a NaN --threshold-value")
	}
	if got := classify(err).Code; got != "InvalidUsage" {
		t.Fatalf("Code = %q, want InvalidUsage (stderr=%s)", got, stderr.String())
	}
	if got := exitCode(err); got != 2 {
		t.Fatalf("exitCode = %d, want 2 (stderr=%s)", got, stderr.String())
	}
	if n := fixture.requestCount(); n != 0 {
		t.Fatalf("requestCount = %d, want 0", n)
	}
}

// TestMonitorCreateLogAlarmDebugLogsStartAndFinishWithOnlyOperationName
// mirrors TestMonitorCreateLogProjectDebugLogsStartAndFinishWithOnlyOperationName
// for create-log-alarm: --debug logs only the operation name around the
// call, never the create body, so a query string or name never reaches a
// debug transcript.
func TestMonitorCreateLogAlarmDebugLogsStartAndFinishWithOnlyOperationName(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/vmonitor-api/api/v1/alarms/list": jsonHandler(http.StatusOK, noExistingLogAlarmsPage),
		"/log-api/v1/projects/proj-1":      jsonHandler(http.StatusOK, exampleLogProjectForAlarmJSON),
		"/vmonitor-api/api/v1/alarms/logs": jsonHandler(http.StatusOK, `{"id":"alarm-1"}`),
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--debug", "monitor", "create-log-alarm",
		"--name", "my-alarm", "--log-project-id", "proj-1", "--threshold-value", "1",
		"--query-string", "confidential-search-terms", "--no-wait",
		"--cli-input-json", `{"Filter":{}}`,
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("execute: %v (stderr=%s)", err, stderr.String())
	}

	var sawStart, sawFinish bool
	for _, line := range strings.Split(stderr.String(), "\n") {
		switch {
		case strings.Contains(line, "write started"):
			sawStart = true
		case strings.Contains(line, "write finished"):
			sawFinish = true
		default:
			continue
		}
		if !strings.Contains(line, `operation="monitor create-log-alarm"`) {
			t.Fatalf("write debug line missing the operation name: %s", line)
		}
		if strings.Contains(line, "confidential-search-terms") || strings.Contains(line, "my-alarm") {
			t.Fatalf("write debug line leaked more than the operation name: %s", line)
		}
	}
	if !sawStart || !sawFinish {
		t.Fatalf("stderr missing write started/write finished: %s", stderr.String())
	}
}

// TestMonitorCreateLogAlarmNotSettledOnCanceledContext drives a real
// create-log-alarm call whose create succeeds and whose post-create wait is
// interrupted by canceling the command's own context, mirroring a Ctrl-C
// during the wait, the same technique
// TestMonitorCreateLogProjectNotSettledOnCanceledContext uses. The create
// response carries the new alarm's id, so the wait polls GetAlarm by id;
// the fixture cancels the context from inside that first poll's own
// handler, so the wait's next step, sleeping before polling again, fails on
// the canceled context rather than the real 60-second bound.
func TestMonitorCreateLogAlarmNotSettledOnCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var getCalls atomic.Int64
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/vmonitor-api/api/v1/alarms/list": jsonHandler(http.StatusOK, noExistingLogAlarmsPage),
		"/log-api/v1/projects/proj-1":      jsonHandler(http.StatusOK, exampleLogProjectForAlarmJSON),
		"/vmonitor-api/api/v1/alarms/logs": jsonHandler(http.StatusOK, `{"id":"alarm-1"}`),
		"/vmonitor-api/api/v1/alarms/alarm-1": func(w http.ResponseWriter, r *http.Request) {
			getCalls.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":{"id":"alarm-1","name":"my-alarm","type":"LOG","status":"CREATING"}}`))
			cancel()
		},
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "monitor", "create-log-alarm",
		"--name", "my-alarm", "--log-project-id", "proj-1", "--threshold-value", "1",
	})
	err := root.ExecuteContext(ctx)
	if err == nil {
		t.Fatal("expected an error")
	}
	if got := classify(err).Code; got != "NotSettled" {
		t.Fatalf("Code = %q, want NotSettled, not the plain canceled-context path (stderr=%s)", got, stderr.String())
	}
	if got := exitCode(err); got != 1 {
		t.Fatalf("exitCode = %d, want 1", got)
	}
	if got := stdout.String(); !strings.Contains(got, `"AlarmID": "alarm-1"`) {
		t.Fatalf("stdout = %s, want the alarm id printed alongside the error", got)
	}
	if getCalls.Load() != 1 {
		t.Fatalf("get calls = %d, want 1", getCalls.Load())
	}
}

// TestGoldenMonitorCreateLogAlarm checks create-log-alarm's exact output
// shape, {"AlarmID": "...", "Alarm": {...}}.
func TestGoldenMonitorCreateLogAlarm(t *testing.T) {
	v := &monitor.CreateLogAlarmOutput{AlarmID: "alarm-1", Alarm: exampleLogAlarm()}
	checkGolden(t, "monitor-create-log-alarm.json.golden", "json", "", v)
	checkGolden(t, "monitor-create-log-alarm.table.golden", "table", "", v)
	checkGolden(t, "monitor-create-log-alarm.text.golden", "text", "", v)
}

// --- update-log-alarm ---

// TestMonitorUpdateLogAlarmSendsMergedBody checks update-log-alarm's
// flag-to-input mapping and merge: setting only --description on the
// command line reads the alarm first, then sends every other field back
// from that read unchanged, the same merge
// monitor.TestUpdateLogAlarmMergeUnsetFieldsResendReadValues checks at the
// SDK level for a direct call.
func TestMonitorUpdateLogAlarmSendsMergedBody(t *testing.T) {
	var updateBody []byte
	var projectCalls atomic.Int64
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/vmonitor-api/api/v1/alarms/alarm-1": jsonHandler(http.StatusOK, existingLogAlarmForUpdateJSON),
		"/log-api/v1/projects/proj-1": func(w http.ResponseWriter, r *http.Request) {
			projectCalls.Add(1)
			jsonHandler(http.StatusOK, exampleLogProjectForAlarmJSON)(w, r)
		},
		"/vmonitor-api/api/v1/alarms/logs/alarm-1": func(w http.ResponseWriter, r *http.Request) {
			defer func() { _ = r.Body.Close() }()
			if r.Method != http.MethodPut {
				t.Fatalf("method = %s, want PUT", r.Method)
			}
			updateBody, _ = io.ReadAll(r.Body)
			w.WriteHeader(http.StatusOK)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "monitor", "update-log-alarm",
		"--alarm-id", "alarm-1", "--description", "new description", "--no-wait",
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("update-log-alarm: %v (stderr=%s)", err, stderr.String())
	}

	body := decodeJSONBody(t, updateBody)
	if body["name"] != "existing-alarm" || body["description"] != "new description" || body["severity"] != "LOW" {
		t.Fatalf("unexpected identity fields: %+v", body)
	}
	if body["logProjectId"] != "proj-1" || body["projectName"] != "old-project" {
		t.Fatalf("unexpected project fields (LogProjectID untouched): %+v", body)
	}
	if body["queryString"] != "status:500" || body["thresholdValue"] != 100.0 {
		t.Fatalf("unexpected unset fields, want the read's own values: %+v", body)
	}
	if projectCalls.Load() != 0 {
		t.Fatalf("GetLogProject called %d times, want 0: LogProjectID was not touched", projectCalls.Load())
	}
}

// TestMonitorUpdateLogAlarmRefusesEmptyStatusWithoutPUT checks the status
// guard: a read whose status is empty is refused with InvalidUsage, sending
// no PUT, since only ACTIVE lets an update through.
func TestMonitorUpdateLogAlarmRefusesEmptyStatusWithoutPUT(t *testing.T) {
	emptyStatus := strings.Replace(existingLogAlarmForUpdateJSON, `"status": "ACTIVE"`, `"status": ""`, 1)
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/vmonitor-api/api/v1/alarms/alarm-1": jsonHandler(http.StatusOK, emptyStatus),
		"/vmonitor-api/api/v1/alarms/logs/alarm-1": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "monitor", "update-log-alarm",
		"--alarm-id", "alarm-1", "--description", "x", "--no-wait",
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected an invalid-input refusal for an alarm with an empty status")
	}
	if got := classify(err).Code; got != "InvalidUsage" {
		t.Fatalf("Code = %q, want InvalidUsage (stderr=%s)", got, stderr.String())
	}
	if got := exitCode(err); got != 2 {
		t.Fatalf("exitCode = %d, want 2 (stderr=%s)", got, stderr.String())
	}
}

// TestMonitorUpdateLogAlarmRefusesMetricAlarm checks the design's own
// refusal: update-log-alarm reads the alarm first and refuses with
// InvalidUsage, sending no PUT, when it is a metric alarm.
func TestMonitorUpdateLogAlarmRefusesMetricAlarm(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/vmonitor-api/api/v1/alarms/alarm-1": jsonHandler(http.StatusOK,
			`{"data":{"id":"alarm-1","name":"m","type":"METRIC","status":"OK","severity":"LOW","metricMappingId":"map-1"}}`),
		"/vmonitor-api/api/v1/alarms/logs/alarm-1": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "monitor", "update-log-alarm",
		"--alarm-id", "alarm-1", "--description", "x", "--no-wait",
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected an invalid-input refusal for a metric alarm")
	}
	if got := classify(err).Code; got != "InvalidUsage" {
		t.Fatalf("Code = %q, want InvalidUsage (stderr=%s)", got, stderr.String())
	}
	if got := exitCode(err); got != 2 {
		t.Fatalf("exitCode = %d, want 2 (stderr=%s)", got, stderr.String())
	}
}

// TestMonitorUpdateLogAlarmQueryStringWithoutFilterExitsWithZeroRequests
// checks the SDK's own pairing rule at the CLI level: --query-string alone,
// with no Filter set through --cli-input-json, is refused with InvalidUsage
// before any request, including the pre-update GetAlarm read, since setting
// only one would otherwise pair a new value for one with the read's stale
// value for the other.
func TestMonitorUpdateLogAlarmQueryStringWithoutFilterExitsWithZeroRequests(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/vmonitor-api/api/v1/alarms/alarm-1": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
		"/vmonitor-api/api/v1/alarms/logs/alarm-1": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "monitor", "update-log-alarm",
		"--alarm-id", "alarm-1", "--query-string", "status:500", "--no-wait",
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected an invalid-input refusal for --query-string without a Filter")
	}
	if got := classify(err).Code; got != "InvalidUsage" {
		t.Fatalf("Code = %q, want InvalidUsage (stderr=%s)", got, stderr.String())
	}
	if got := exitCode(err); got != 2 {
		t.Fatalf("exitCode = %d, want 2 (stderr=%s)", got, stderr.String())
	}
	if n := fixture.requestCount(); n != 0 {
		t.Fatalf("requestCount = %d, want 0", n)
	}
}

// TestMonitorUpdateLogAlarmReadOnlyRefusedWithZeroRequests checks that a
// read-only profile refuses update-log-alarm before any request, including
// its own pre-update GetAlarm read.
func TestMonitorUpdateLogAlarmReadOnlyRefusedWithZeroRequests(t *testing.T) {
	home := withCleanEnv(t)
	writeConfigFile(t, home, "[profile agent]\nregion = hcm-3\nread_only = true\n")
	writeCredentialsFile(t, home, "[agent]\nusername = u\npassword = p\nroot_email = e@example.com\n")

	refuse := func(_ http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
	}
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/vmonitor-api/api/v1/alarms/alarm-1":      refuse,
		"/vmonitor-api/api/v1/alarms/logs/alarm-1": refuse,
	})
	opts := newFakeServer(t, fixture.mux)
	withTestOptions(t, append(opts, vngcloud.WithStaticToken("test-token"))...)

	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	root := newRootCmd(strings.NewReader(""), stdout, stderr)
	root.SetArgs([]string{
		"--profile", "agent", "monitor", "update-log-alarm",
		"--alarm-id", "alarm-1", "--description", "x",
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected a read-only refusal")
	}
	if got := classify(err).Code; got != "ReadOnly" {
		t.Fatalf("Code = %q, want ReadOnly (stderr=%s)", got, stderr.String())
	}
	if got := exitCode(err); got != 2 {
		t.Fatalf("exitCode = %d, want 2", got)
	}
	if n := fixture.requestCount(); n != 0 {
		t.Fatalf("requestCount = %d, want 0", n)
	}
}

// TestMonitorUpdateLogAlarmCLIInputJSONRejectsUnknownField mirrors
// TestMonitorCreateLogAlarmCLIInputJSONRejectsUnknownField for
// update-log-alarm's Input.
func TestMonitorUpdateLogAlarmCLIInputJSONRejectsUnknownField(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/vmonitor-api/api/v1/alarms/alarm-1": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "monitor", "update-log-alarm",
		"--cli-input-json", `{"AlarmID":"alarm-1","Bogus":1}`,
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected a --cli-input-json refusal for an unknown field")
	}
	if got := exitCode(err); got != 2 {
		t.Fatalf("exitCode = %d, want 2 (stderr=%s)", got, stderr.String())
	}
	if n := fixture.requestCount(); n != 0 {
		t.Fatalf("requestCount = %d, want 0", n)
	}
}

// TestGoldenMonitorUpdateLogAlarm checks update-log-alarm's exact output
// shape, {"Alarm": {...}}.
func TestGoldenMonitorUpdateLogAlarm(t *testing.T) {
	v := &monitor.UpdateLogAlarmOutput{Alarm: exampleLogAlarm()}
	checkGolden(t, "monitor-update-log-alarm.json.golden", "json", "", v)
	checkGolden(t, "monitor-update-log-alarm.table.golden", "table", "", v)
	checkGolden(t, "monitor-update-log-alarm.text.golden", "text", "", v)
}

// --- delete-log-alarm ---

// TestMonitorDeleteLogAlarmWithoutYesExitsWithZeroRequests mirrors
// TestMonitorDeleteLogProjectWithoutYesExitsWithZeroRequests for
// delete-log-alarm: Write and Destructive, so it needs --yes.
func TestMonitorDeleteLogAlarmWithoutYesExitsWithZeroRequests(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/vmonitor-api/api/v1/alarms/logs/alarm-1": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "monitor", "delete-log-alarm", "--alarm-id", "alarm-1"})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected an error without --yes")
	}
	if got := exitCode(err); got != 2 {
		t.Fatalf("exitCode = %d, want 2 (stderr=%s)", got, stderr.String())
	}
	if n := fixture.requestCount(); n != 0 {
		t.Fatalf("requestCount = %d, want 0", n)
	}
}

// TestMonitorDeleteLogAlarmWithYesSendsDelete checks that --yes lets
// delete-log-alarm read the alarm first to confirm it is a log alarm, then
// send exactly one DELETE and succeed, with no wait.
func TestMonitorDeleteLogAlarmWithYesSendsDelete(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/vmonitor-api/api/v1/alarms/alarm-1": jsonHandler(http.StatusOK,
			`{"data":{"id":"alarm-1","name":"my-alarm","type":"LOG","status":"OK","severity":"LOW"}}`),
		"/vmonitor-api/api/v1/alarms/logs/alarm-1": jsonHandler(http.StatusNoContent, ""),
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--yes", "monitor", "delete-log-alarm", "--alarm-id", "alarm-1",
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("delete-log-alarm: %v (stderr=%s)", err, stderr.String())
	}
	if got, ok := fixture.methodFor("/vmonitor-api/api/v1/alarms/logs/alarm-1"); !ok || got != http.MethodDelete {
		t.Fatalf("delete-log-alarm method = %q, ok=%v, want DELETE", got, ok)
	}
	if n := fixture.requestCount(); n != 2 {
		t.Fatalf("requestCount = %d, want 2 (the pre-delete GetAlarm read, then the DELETE, no wait)", n)
	}
}

// TestMonitorDeleteLogAlarmRefusesMetricAlarm checks the design's own
// refusal: delete-log-alarm reads the alarm first and refuses with
// InvalidUsage, sending no DELETE, when it is a metric alarm.
func TestMonitorDeleteLogAlarmRefusesMetricAlarm(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/vmonitor-api/api/v1/alarms/alarm-1": jsonHandler(http.StatusOK,
			`{"data":{"id":"alarm-1","name":"m","type":"METRIC","status":"OK","severity":"LOW","metricMappingId":"map-1"}}`),
		"/vmonitor-api/api/v1/alarms/logs/alarm-1": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--yes", "monitor", "delete-log-alarm", "--alarm-id", "alarm-1",
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected an invalid-input refusal for a metric alarm")
	}
	if got := classify(err).Code; got != "InvalidUsage" {
		t.Fatalf("Code = %q, want InvalidUsage (stderr=%s)", got, stderr.String())
	}
	if got := exitCode(err); got != 2 {
		t.Fatalf("exitCode = %d, want 2 (stderr=%s)", got, stderr.String())
	}
	if n := fixture.requestCount(); n != 1 {
		t.Fatalf("requestCount = %d, want 1 (the pre-delete GetAlarm read, no DELETE)", n)
	}
}

// TestMonitorDeleteLogAlarmNotFound checks that a 404 from the pre-delete
// GetAlarm read classifies as NotFound and exits 4, the generic *APIError
// mapping classify and exitCode already give every not-found result. The
// fixture stubs only the DELETE path, so the unstubbed GET the read makes
// falls through to the mux's own default 404, which decodes into the same
// NotFound sentinel a real 404 body would.
func TestMonitorDeleteLogAlarmNotFound(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/vmonitor-api/api/v1/alarms/logs/alarm-1": jsonHandler(http.StatusNotFound, `{"message":"not found"}`),
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--yes", "monitor", "delete-log-alarm", "--alarm-id", "alarm-1",
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected a not-found error")
	}
	if got := classify(err).Code; got != "NotFound" {
		t.Fatalf("Code = %q, want NotFound (stderr=%s)", got, stderr.String())
	}
	if got := exitCode(err); got != 4 {
		t.Fatalf("exitCode = %d, want 4", got)
	}
}

// TestMonitorDeleteLogAlarmReadOnlyRefusedWithZeroRequests mirrors
// TestMonitorDeleteLogProjectReadOnlyRefusedWithZeroRequests for
// delete-log-alarm.
func TestMonitorDeleteLogAlarmReadOnlyRefusedWithZeroRequests(t *testing.T) {
	home := withCleanEnv(t)
	writeConfigFile(t, home, "[profile agent]\nregion = hcm-3\nread_only = true\n")
	writeCredentialsFile(t, home, "[agent]\nusername = u\npassword = p\nroot_email = e@example.com\n")

	refuse := func(_ http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
	}
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/vmonitor-api/api/v1/alarms/logs/alarm-1": refuse,
	})
	opts := newFakeServer(t, fixture.mux)
	withTestOptions(t, append(opts, vngcloud.WithStaticToken("test-token"))...)

	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	root := newRootCmd(strings.NewReader(""), stdout, stderr)
	root.SetArgs([]string{"--profile", "agent", "monitor", "delete-log-alarm", "--alarm-id", "alarm-1", "--yes"})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected a read-only refusal")
	}
	if got := classify(err).Code; got != "ReadOnly" {
		t.Fatalf("Code = %q, want ReadOnly (stderr=%s)", got, stderr.String())
	}
	if got := exitCode(err); got != 2 {
		t.Fatalf("exitCode = %d, want 2", got)
	}
	if n := fixture.requestCount(); n != 0 {
		t.Fatalf("requestCount = %d, want 0", n)
	}
}

// TestMonitorDeleteLogAlarmCLIInputJSONRejectsUnknownField mirrors
// TestMonitorDeleteLogProjectCLIInputJSONRejectsUnknownField for
// delete-log-alarm's Input.
func TestMonitorDeleteLogAlarmCLIInputJSONRejectsUnknownField(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/vmonitor-api/api/v1/alarms/logs/alarm-1": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--yes", "monitor", "delete-log-alarm",
		"--cli-input-json", `{"AlarmID":"alarm-1","Bogus":1}`,
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected a --cli-input-json refusal for an unknown field")
	}
	if got := exitCode(err); got != 2 {
		t.Fatalf("exitCode = %d, want 2 (stderr=%s)", got, stderr.String())
	}
	if n := fixture.requestCount(); n != 0 {
		t.Fatalf("requestCount = %d, want 0", n)
	}
}

// TestGoldenMonitorDeleteLogAlarm checks delete-log-alarm's exact output
// shape: an empty object, since DeleteLogAlarmOutput carries no field.
func TestGoldenMonitorDeleteLogAlarm(t *testing.T) {
	v := &monitor.DeleteLogAlarmOutput{}
	checkGolden(t, "monitor-delete-log-alarm.json.golden", "json", "", v)
	checkGolden(t, "monitor-delete-log-alarm.table.golden", "table", "", v)
	checkGolden(t, "monitor-delete-log-alarm.text.golden", "text", "", v)
}
