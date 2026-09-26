package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/monitor"
)

// exampleCheck is the same sanitized shape the monitor SDK package's own
// fixtures decode, reused here so the golden files below exercise a
// realistic Check rather than an empty struct.
func exampleCheck(status string) monitor.Check {
	return monitor.Check{
		ID:      "chk-1",
		Name:    "example-check",
		Type:    "API",
		Subtype: "HTTP",
		Status:  status,
		Config: monitor.CheckConfig{
			Request: monitor.CheckRequest{
				URL:         "https://example.com",
				Method:      "GET",
				Timeout:     30,
				VerifiedSSL: true,
			},
			Assertions: []monitor.Assertion{
				{Type: "status_code", Operator: "does_not_match_regex", Target: "[4-5][0-9][0-9]"},
			},
		},
		Options:   monitor.CheckOptions{TestFrequency: 60, Tests: 1, FailedLocations: 1},
		Locations: []string{"loc-1"},
		CreatedAt: "Jan 1, 2026, 12:00:00 AM",
		UpdatedAt: "Jan 2, 2026, 1:00:00 PM",
	}
}

// TestGoldenMonitorListChecks checks list-checks' exact output shapes: JSON
// keeps {"Items": [...]}, one check per row for table and text.
func TestGoldenMonitorListChecks(t *testing.T) {
	v := &monitor.ListChecksOutput{Items: []monitor.Check{exampleCheck(monitor.StatusEnabled)}}
	checkGolden(t, "monitor-list-checks.json.golden", "json", "", v)
	checkGolden(t, "monitor-list-checks.table.golden", "table", "", v)
	checkGolden(t, "monitor-list-checks.text.golden", "text", "", v)
}

// TestGoldenMonitorGetCheck checks get-check's exact output shape:
// {"Check": {...}}.
func TestGoldenMonitorGetCheck(t *testing.T) {
	v := &monitor.GetCheckOutput{Check: exampleCheck(monitor.StatusEnabled)}
	checkGolden(t, "monitor-get-check.json.golden", "json", "", v)
	checkGolden(t, "monitor-get-check.table.golden", "table", "", v)
	checkGolden(t, "monitor-get-check.text.golden", "text", "", v)
}

// TestGoldenMonitorPauseCheck checks pause-check's exact output shape,
// {"Check": {...}, "Changed": bool}, per the monitor design: a deploy script
// reads Changed with --query Changed.
func TestGoldenMonitorPauseCheck(t *testing.T) {
	v := &monitor.PauseCheckOutput{Check: exampleCheck(monitor.StatusDisabled), Changed: true}
	checkGolden(t, "monitor-pause-check.json.golden", "json", "", v)
	checkGolden(t, "monitor-pause-check.table.golden", "table", "", v)
	checkGolden(t, "monitor-pause-check.text.golden", "text", "", v)
}

// exampleWebhookChannel is a Webhook channel shaped like the notification
// gateway's own fixtures, with a raw Address and header Value: the golden
// tests below redact it themselves with redactChannel, the same
// transformation ListChannels and GetChannel apply before rendering, so the
// golden file shows exactly what a real command prints.
func exampleWebhookChannel() monitor.Channel {
	return monitor.Channel{
		ID:      "channel-1",
		Name:    "example-webhook",
		Address: "https://example.com/hooks/incoming?token=super-secret-token",
		Type:    monitor.ChannelTypeWebhook,
		Headers: []monitor.ChannelHeader{
			{Key: "X-Api-Key", Value: "super-secret-header-value"},
		},
		CreatedDate: "2026-09-26T15:46:45",
	}
}

// exampleEmailChannel is an Email channel: its Address is personal data, not
// a secret the monitor design's CLI redaction rule covers, so it prints
// unchanged.
func exampleEmailChannel() monitor.Channel {
	return monitor.Channel{
		ID:          "channel-2",
		Name:        "example-email",
		Address:     "someone@example.com",
		Type:        monitor.ChannelTypeEmail,
		CreatedDate: "2026-09-26T15:40:00",
		UpdatedDate: "2026-09-26T16:00:00",
	}
}

// TestGoldenMonitorListChannelTypes checks list-channel-types' exact output
// shape: JSON keeps {"Items": [...]}, one type per row for table and text.
func TestGoldenMonitorListChannelTypes(t *testing.T) {
	v := &monitor.ListChannelTypesOutput{Items: []monitor.ChannelType{
		{ID: "type-webhook", Name: monitor.ChannelTypeWebhook, Description: "Webhook"},
		{ID: "type-email", Name: monitor.ChannelTypeEmail, Description: "Email"},
	}}
	checkGolden(t, "monitor-list-channel-types.json.golden", "json", "", v)
	checkGolden(t, "monitor-list-channel-types.table.golden", "table", "", v)
	checkGolden(t, "monitor-list-channel-types.text.golden", "text", "", v)
}

// TestGoldenMonitorListChannels checks list-channels' exact output shape,
// including paging metadata alongside Items, with every channel already
// redacted the way the real command redacts it before rendering.
func TestGoldenMonitorListChannels(t *testing.T) {
	v := core.NewPagedList([]monitor.Channel{
		redactChannel(exampleWebhookChannel()),
		redactChannel(exampleEmailChannel()),
	}, 1, 10000, 1, 2)
	checkGolden(t, "monitor-list-channels.json.golden", "json", "", v)
	checkGolden(t, "monitor-list-channels.table.golden", "table", "", v)
	checkGolden(t, "monitor-list-channels.text.golden", "text", "", v)
}

// TestGoldenMonitorGetChannel checks get-channel's exact output shape,
// {"Channel": {...}}, with the channel already redacted.
func TestGoldenMonitorGetChannel(t *testing.T) {
	v := &monitor.GetChannelOutput{Channel: redactChannel(exampleWebhookChannel())}
	checkGolden(t, "monitor-get-channel.json.golden", "json", "", v)
	checkGolden(t, "monitor-get-channel.table.golden", "table", "", v)
	checkGolden(t, "monitor-get-channel.text.golden", "text", "", v)
}

// exampleLogAlarm and exampleMetricAlarm are shaped like the monitor SDK
// package's own ListAlarmsLog.json and ListAlarmsMetric.json fixtures, reused
// here so the golden tests below exercise the same identity and channel
// reference shape both kinds decode to.
func exampleLogAlarm() monitor.Alarm {
	return monitor.Alarm{
		ID:       "alarm-1",
		Name:     "example-log-alarm",
		Kind:     monitor.AlarmKindLog,
		Status:   "OK",
		Severity: "MEDIUM",
		Log: &monitor.LogAlarmDetail{
			InAlarm: []string{"channel-1", "channel-2"},
			OK:      []string{"channel-1"},
		},
	}
}

func exampleMetricAlarm() monitor.Alarm {
	return monitor.Alarm{
		ID:              "alarm-2",
		Name:            "example-metric-alarm",
		Kind:            monitor.AlarmKindMetric,
		Status:          "In-alarm",
		Severity:        "HIGH",
		MetricMappingID: "metric-map-1",
	}
}

// TestGoldenMonitorListAlarms checks list-alarms' exact output shape,
// including paging metadata alongside Items, with one alarm of each kind so
// the golden file shows both a Log alarm's channel references and a Metric
// alarm's MetricMappingID side by side.
func TestGoldenMonitorListAlarms(t *testing.T) {
	v := core.NewPagedList([]monitor.Alarm{exampleLogAlarm(), exampleMetricAlarm()}, 1, 10000, 1, 2)
	checkGolden(t, "monitor-list-alarms.json.golden", "json", "", v)
	checkGolden(t, "monitor-list-alarms.table.golden", "table", "", v)
	checkGolden(t, "monitor-list-alarms.text.golden", "text", "", v)
}

// TestGoldenMonitorGetAlarm checks get-alarm's exact output shape,
// {"Alarm": {...}}, for a Log alarm.
func TestGoldenMonitorGetAlarm(t *testing.T) {
	v := &monitor.GetAlarmOutput{Alarm: exampleLogAlarm()}
	checkGolden(t, "monitor-get-alarm.json.golden", "json", "", v)
	checkGolden(t, "monitor-get-alarm.table.golden", "table", "", v)
	checkGolden(t, "monitor-get-alarm.text.golden", "text", "", v)
}

// exampleLocation is a probe location shaped like the uptime manager's own
// fixtures, reused by the list-locations golden tests below.
func exampleLocation(id, name string) monitor.Location {
	return monitor.Location{
		ID:          id,
		Name:        name,
		Type:        "PUBLIC",
		Description: "Public Location",
		Status:      "REPORTING",
		CreatedAt:   "Jan 1, 2026, 12:00:00 AM",
		UpdatedAt:   "Jan 2, 2026, 1:00:00 PM",
	}
}

// TestGoldenMonitorCreateCheck checks create-check's exact output shape,
// {"Check": {...}}, the same shape get-check uses.
func TestGoldenMonitorCreateCheck(t *testing.T) {
	v := &monitor.CreateCheckOutput{Check: exampleCheck(monitor.StatusEnabled)}
	checkGolden(t, "monitor-create-check.json.golden", "json", "", v)
	checkGolden(t, "monitor-create-check.table.golden", "table", "", v)
	checkGolden(t, "monitor-create-check.text.golden", "text", "", v)
}

// TestGoldenMonitorUpdateCheck checks update-check's exact output shape,
// {"Check": {...}}, the same shape create-check and get-check use.
func TestGoldenMonitorUpdateCheck(t *testing.T) {
	v := &monitor.UpdateCheckOutput{Check: exampleCheck(monitor.StatusEnabled)}
	checkGolden(t, "monitor-update-check.json.golden", "json", "", v)
	checkGolden(t, "monitor-update-check.table.golden", "table", "", v)
	checkGolden(t, "monitor-update-check.text.golden", "text", "", v)
}

// TestGoldenMonitorDeleteCheck checks delete-check's exact output shape: an
// empty object, since DeleteCheckOutput carries no fields.
func TestGoldenMonitorDeleteCheck(t *testing.T) {
	v := &monitor.DeleteCheckOutput{}
	checkGolden(t, "monitor-delete-check.json.golden", "json", "", v)
	checkGolden(t, "monitor-delete-check.table.golden", "table", "", v)
	checkGolden(t, "monitor-delete-check.text.golden", "text", "", v)
}

// TestGoldenMonitorListLocations checks list-locations' exact output shapes:
// JSON keeps {"Items": [...]}, one location per row for table and text.
func TestGoldenMonitorListLocations(t *testing.T) {
	v := &monitor.ListLocationsOutput{Items: []monitor.Location{
		exampleLocation("loc-1", "SYNTT-VN-HCM01"),
		exampleLocation("loc-2", "SYNTT-VN-HAN01"),
	}}
	checkGolden(t, "monitor-list-locations.json.golden", "json", "", v)
	checkGolden(t, "monitor-list-locations.table.golden", "table", "", v)
	checkGolden(t, "monitor-list-locations.text.golden", "text", "", v)
}

// monitorUptimesJSON renders one check as the uptime manager's own JSON
// shape (snake_case), the wire format ListChecks, GetCheck, and the toggle's
// confirm reads all share.
func monitorUptimesJSON(id, status string) string {
	return `{"id":"` + id + `","name":"example-check","type":"API","subtype":"HTTP","status":"` + status + `",` +
		`"config":{"request":{"url":"https://example.com","method":"GET","headers":{},"query":{},"body":"",` +
		`"timeout":30,"verified_ssl":true},"assertions":[{"type":"status_code",` +
		`"operator":"does_not_match_regex","target":"[4-5][0-9][0-9]"}]},` +
		`"options":{"test_frequency":60,"tests":1,"failed_locations":1},"locations":["loc-1"]}`
}

// TestMonitorListChecksAndGetCheckEndToEnd runs the real list-checks and
// get-check commands against a fixture uptime manager, checking the request
// method and path and that the decoded Check survives the round trip.
func TestMonitorListChecksAndGetCheckEndToEnd(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/vmonitor-uptime-manager/v1/uptimes":       jsonHandler(http.StatusOK, "["+monitorUptimesJSON("chk-1", monitor.StatusEnabled)+"]"),
		"/vmonitor-uptime-manager/v1/uptimes/chk-1": jsonHandler(http.StatusOK, monitorUptimesJSON("chk-1", monitor.StatusEnabled)),
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "monitor", "list-checks"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("list-checks: %v (stderr=%s)", err, stderr.String())
	}
	if got, ok := fixture.methodFor("/vmonitor-uptime-manager/v1/uptimes"); !ok || got != http.MethodGet {
		t.Fatalf("list-checks method = %q, ok=%v, want GET", got, ok)
	}
	var listed struct{ Items []monitor.Check }
	if err := json.Unmarshal(stdout.Bytes(), &listed); err != nil {
		t.Fatalf("list-checks stdout is not valid JSON: %v (%s)", err, stdout.String())
	}
	if len(listed.Items) != 1 || listed.Items[0].Name != "example-check" {
		t.Fatalf("list-checks Items = %+v", listed.Items)
	}

	stdout.Reset()
	root, stdout, stderr = newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "monitor", "get-check", "--check-id", "chk-1"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("get-check: %v (stderr=%s)", err, stderr.String())
	}
	var got struct{ Check monitor.Check }
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("get-check stdout is not valid JSON: %v (%s)", err, stdout.String())
	}
	if got.Check.ID != "chk-1" || got.Check.Status != monitor.StatusEnabled {
		t.Fatalf("get-check Check = %+v", got.Check)
	}
}

// TestMonitorGetCheckNotFoundExitsFour checks that a 404 from the uptime
// manager reaches the CLI as the NotFound error class with exit code 4, the
// same path every other service's not-found result already takes.
func TestMonitorGetCheckNotFoundExitsFour(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/vmonitor-uptime-manager/v1/uptimes/missing": jsonHandler(http.StatusNotFound, `{"message":"not found"}`),
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "monitor", "get-check", "--check-id", "missing"})
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

// TestMonitorPauseCheckAlreadyAtTargetSendsNoToggle checks that pause-check
// on an already-DISABLED check sends only the one read, per the monitor
// design's toggle sequence, and reports Changed: false.
func TestMonitorPauseCheckAlreadyAtTargetSendsNoToggle(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/vmonitor-uptime-manager/v1/uptimes/chk-1": jsonHandler(http.StatusOK, monitorUptimesJSON("chk-1", monitor.StatusDisabled)),
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "monitor", "pause-check", "--check-id", "chk-1", "--query", "Changed"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("pause-check: %v (stderr=%s)", err, stderr.String())
	}
	if n := fixture.requestCount(); n != 1 {
		t.Fatalf("requestCount = %d, want 1 (the one read, no toggle)", n)
	}
	if got := stdout.String(); got != "false\n" {
		t.Fatalf("--query Changed = %q, want %q", got, "false\n")
	}
}

// monitorToggleFixture serves the uptime manager for one check whose status
// starts at fromStatus and flips to toStatus the instant its PUT
// .../status/{id} handler runs, so the toggle's own first confirm read (no
// wait, per the monitor design's confirmWaits) already observes the target:
// the test never waits through a real sleep.
func monitorToggleFixture(id, fromStatus, toStatus string) *svcFixture {
	status := fromStatus
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/vmonitor-uptime-manager/v1/uptimes/" + id: func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(monitorUptimesJSON(id, status)))
		},
		"/vmonitor-uptime-manager/v1/uptimes/status/" + id: func(w http.ResponseWriter, _ *http.Request) {
			status = toStatus
			w.WriteHeader(http.StatusNoContent)
		},
	})
	return fixture
}

// TestMonitorPauseCheckTogglesAndConfirms drives an ENABLED check through
// pause-check end to end: one read, one PUT, one confirming read, Changed:
// true, and no --yes required (pause is a Write but not Destructive).
func TestMonitorPauseCheckTogglesAndConfirms(t *testing.T) {
	fixture := monitorToggleFixture("chk-1", monitor.StatusEnabled, monitor.StatusDisabled)
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "monitor", "pause-check", "--check-id", "chk-1"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("pause-check: %v (stderr=%s)", err, stderr.String())
	}

	var out struct {
		Check   monitor.Check
		Changed bool
	}
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("stdout is not valid JSON: %v (%s)", err, stdout.String())
	}
	if !out.Changed {
		t.Fatalf("Changed = false, want true")
	}
	if out.Check.Status != monitor.StatusDisabled {
		t.Fatalf("Check.Status = %s, want %s", out.Check.Status, monitor.StatusDisabled)
	}
	if got, want := fixture.requestCount(), 3; got != want {
		t.Fatalf("requestCount = %d, want %d (read, toggle, confirm)", got, want)
	}
}

// TestMonitorResumeCheckTogglesAndConfirms mirrors
// TestMonitorPauseCheckTogglesAndConfirms for resume-check.
func TestMonitorResumeCheckTogglesAndConfirms(t *testing.T) {
	fixture := monitorToggleFixture("chk-1", monitor.StatusDisabled, monitor.StatusEnabled)
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "monitor", "resume-check", "--check-id", "chk-1", "--query", "Changed"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("resume-check: %v (stderr=%s)", err, stderr.String())
	}
	if got := stdout.String(); got != "true\n" {
		t.Fatalf("--query Changed = %q, want %q", got, "true\n")
	}
}

// TestMonitorPauseAndResumeReadOnlyRefusedWithZeroRequests checks the
// monitor design's read-only rule: pause-check and resume-check are Write
// operations, so a read-only profile refuses either with exit 2 before any
// request, and neither needs --yes to be refused this way.
func TestMonitorPauseAndResumeReadOnlyRefusedWithZeroRequests(t *testing.T) {
	for _, op := range []string{"pause-check", "resume-check"} {
		t.Run(op, func(t *testing.T) {
			home := withCleanEnv(t)
			writeConfigFile(t, home, "[profile agent]\nregion = hcm-3\nread_only = true\n")
			writeCredentialsFile(t, home, "[agent]\nusername = u\npassword = p\nroot_email = e@example.com\n")

			fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
				"/vmonitor-uptime-manager/v1/uptimes/chk-1": func(_ http.ResponseWriter, r *http.Request) {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				},
				"/vmonitor-uptime-manager/v1/uptimes/status/chk-1": func(_ http.ResponseWriter, r *http.Request) {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				},
			})
			opts := newFakeServer(t, fixture.mux)
			withTestOptions(t, append(opts, vngcloud.WithStaticToken("test-token"))...)

			stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
			root := newRootCmd(strings.NewReader(""), stdout, stderr)
			root.SetArgs([]string{"--profile", "agent", "monitor", op, "--check-id", "chk-1"})
			err := root.ExecuteContext(context.Background())
			if err == nil {
				t.Fatalf("expected a read-only refusal")
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
		})
	}
}

// TestMonitorCreateCheckWithLocationsFromCLIInputJSON drives create-check
// with every flag-settable field on the command line and Locations, the
// required field with no flag type, through --cli-input-json only. It
// checks the exact request body the SDK builds from that merge and that the
// command still succeeds: the monitor design says Locations, Headers,
// Query, and Assertions reach CreateCheckInput only this way, so this is
// the only path that ever sets the required field.
func TestMonitorCreateCheckWithLocationsFromCLIInputJSON(t *testing.T) {
	var body []byte
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/vmonitor-uptime-manager/v1/uptimes": func(w http.ResponseWriter, r *http.Request) {
			defer func() { _ = r.Body.Close() }()
			body, _ = io.ReadAll(r.Body)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(monitorUptimesJSON("chk-9", monitor.StatusEnabled)))
		},
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "monitor", "create-check",
		"--name", "vngcloud-test-check",
		"--url", "https://example.com/health",
		"--method", "POST",
		"--body", "ping",
		"--timeout", "5",
		"--test-frequency", "15",
		"--tests", "3",
		"--failed-locations", "2",
		"--cli-input-json", `{"Locations":["loc-1","loc-2"]}`,
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("create-check: %v (stderr=%s)", err, stderr.String())
	}
	if got, ok := fixture.methodFor("/vmonitor-uptime-manager/v1/uptimes"); !ok || got != http.MethodPost {
		t.Fatalf("create-check method = %q, ok=%v, want POST", got, ok)
	}

	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("body is not valid JSON: %v (%s)", err, body)
	}
	if decoded["name"] != "vngcloud-test-check" {
		t.Fatalf("body[name] = %v, want vngcloud-test-check (%s)", decoded["name"], body)
	}
	config, _ := decoded["config"].(map[string]any)
	request, _ := config["request"].(map[string]any)
	if request["url"] != "https://example.com/health" || request["method"] != "POST" || request["body"] != "ping" {
		t.Fatalf("config.request = %+v", request)
	}
	if request["timeout"] != float64(5) {
		t.Fatalf("config.request.timeout = %v, want 5", request["timeout"])
	}
	options, _ := decoded["options"].(map[string]any)
	wantOptions := map[string]any{"test_frequency": float64(15), "tests": float64(3), "failed_locations": float64(2)}
	for k, want := range wantOptions {
		if options[k] != want {
			t.Fatalf("options[%q] = %v, want %v (%+v)", k, options[k], want, options)
		}
	}
	locations, _ := decoded["locations"].([]any)
	if len(locations) != 2 || locations[0] != "loc-1" || locations[1] != "loc-2" {
		t.Fatalf("locations = %+v, want [loc-1 loc-2]", decoded["locations"])
	}

	var out struct{ Check monitor.Check }
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("stdout is not valid JSON: %v (%s)", err, stdout.String())
	}
	if out.Check.ID != "chk-9" {
		t.Fatalf("Check.ID = %q, want chk-9", out.Check.ID)
	}
}

// TestMonitorCreateCheckMissingLocationsExitsWithZeroRequests checks that
// create-check without Locations, from either a flag (there is none) or
// --cli-input-json, fails the required-field check before any request:
// Locations has no flag type, so this is the only way an operator can leave
// it unset.
func TestMonitorCreateCheckMissingLocationsExitsWithZeroRequests(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/vmonitor-uptime-manager/v1/uptimes": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "monitor", "create-check",
		"--name", "vngcloud-test-check",
		"--url", "https://example.com/health",
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatalf("expected a required-field error without Locations")
	}
	if got := exitCode(err); got != 2 {
		t.Fatalf("exitCode = %d, want 2 (stderr=%s)", got, stderr.String())
	}
	if n := fixture.requestCount(); n != 0 {
		t.Fatalf("requestCount = %d, want 0", n)
	}
}

// TestMonitorCreateCheckEmptyLocationsExitsWithZeroRequests checks
// create-check with Locations set to an explicit empty list through
// --cli-input-json. A non-nil empty slice is not zero, so the CLI's own
// required-field check (checkRequiredFlags) passes it through; the command
// still must exit 2 with zero requests, on the SDK's own empty-Locations
// check (monitor.CreateCheck returns ErrInvalidInput).
func TestMonitorCreateCheckEmptyLocationsExitsWithZeroRequests(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/vmonitor-uptime-manager/v1/uptimes": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "monitor", "create-check",
		"--name", "vngcloud-test-check",
		"--url", "https://example.com/health",
		"--cli-input-json", `{"Locations":[]}`,
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatalf("expected an error with an empty Locations list")
	}
	if got := exitCode(err); got != 2 {
		t.Fatalf("exitCode = %d, want 2 (stderr=%s)", got, stderr.String())
	}
	if n := fixture.requestCount(); n != 0 {
		t.Fatalf("requestCount = %d, want 0", n)
	}
}

// monitorCheckPathHandler dispatches GET and PUT for one check's own path to
// get and put, the shape update-check's read-then-write needs on a single
// registered route: newSvcFixture's mux takes one handler per path, and the
// real uptime manager answers both methods at /uptimes/{id}.
func monitorCheckPathHandler(t *testing.T, get, put func(http.ResponseWriter, *http.Request)) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			get(w, r)
		case http.MethodPut:
			put(w, r)
		default:
			t.Errorf("unexpected method %s", r.Method)
		}
	}
}

// TestMonitorUpdateCheckFromCLIInputJSON drives update-check with a literal
// --name flag and no other field, checking that the merged PUT body keeps
// every other field GetCheck's own read supplied unchanged, and that the
// body carries no status field, the same read-merge shape
// TestMonitorUpdateChannelFromCLIInputJSON checks for a channel.
func TestMonitorUpdateCheckFromCLIInputJSON(t *testing.T) {
	var putBody []byte
	var getCalls, putCalls int
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/vmonitor-uptime-manager/v1/uptimes/chk-1": monitorCheckPathHandler(t,
			func(w http.ResponseWriter, _ *http.Request) {
				getCalls++
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(monitorUptimesJSON("chk-1", monitor.StatusEnabled)))
			},
			func(w http.ResponseWriter, r *http.Request) {
				putCalls++
				defer func() { _ = r.Body.Close() }()
				putBody, _ = io.ReadAll(r.Body)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(monitorUptimesJSON("chk-1", monitor.StatusEnabled)))
			}),
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "monitor", "update-check",
		"--check-id", "chk-1", "--name", "renamed-check",
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("update-check: %v (stderr=%s)", err, stderr.String())
	}
	if getCalls != 1 || putCalls != 1 {
		t.Fatalf("getCalls = %d, putCalls = %d, want 1 each", getCalls, putCalls)
	}

	var decoded map[string]any
	if err := json.Unmarshal(putBody, &decoded); err != nil {
		t.Fatalf("body is not valid JSON: %v (%s)", err, putBody)
	}
	if _, ok := decoded["status"]; ok {
		t.Fatalf("body carries a status field: %+v", decoded)
	}
	if decoded["name"] != "renamed-check" {
		t.Fatalf("body[name] = %v, want renamed-check", decoded["name"])
	}
	config, _ := decoded["config"].(map[string]any)
	request, _ := config["request"].(map[string]any)
	if request["url"] != "https://example.com" || request["method"] != "GET" {
		t.Fatalf("config.request = %+v, want the unchanged url and method resent", request)
	}
	options, _ := decoded["options"].(map[string]any)
	if options["test_frequency"] != float64(60) || options["tests"] != float64(1) || options["failed_locations"] != float64(1) {
		t.Fatalf("options = %+v, want unchanged", options)
	}
	locations, _ := decoded["locations"].([]any)
	if len(locations) != 1 || locations[0] != "loc-1" {
		t.Fatalf("locations = %+v, want unchanged", decoded["locations"])
	}

	var out struct{ Check monitor.Check }
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("stdout is not valid JSON: %v (%s)", err, stdout.String())
	}
	if out.Check.ID != "chk-1" {
		t.Fatalf("Check.ID = %q, want chk-1", out.Check.ID)
	}
}

// TestMonitorUpdateCheckRequiresAtLeastOneField checks that update-check with
// only --check-id set reaches the SDK's own "at least one field" refusal
// (monitor.UpdateCheck) with zero requests: the CLI's required-flags check
// passes, since CheckID is the only field tagged required, but the SDK
// itself checks for at least one other field before its own read, the same
// as a missing CheckID or a bad ID shape.
func TestMonitorUpdateCheckRequiresAtLeastOneField(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/vmonitor-uptime-manager/v1/uptimes/chk-1": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "monitor", "update-check", "--check-id", "chk-1"})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatalf("expected an error with no field to change")
	}
	if got := exitCode(err); got != 2 {
		t.Fatalf("exitCode = %d, want 2 (stderr=%s)", got, stderr.String())
	}
	if n := fixture.requestCount(); n != 0 {
		t.Fatalf("requestCount = %d, want 0", n)
	}
}

// TestMonitorCreateCheckNotificationsFromCLIInputJSON checks that
// CreateCheckInput's Notifications field, which has no flag type, reaches
// the request body through --cli-input-json. The nested CheckNotifications
// struct keeps its own wire tag, "In-alarm" rather than the Go field name
// InAlarm, and --cli-input-json's final decode refuses any key, at any
// depth, that names no field (input.go), so the JSON value must use that
// wire tag to set it; see TestMonitorCreateCheckNotificationsInAlarmKeyIsAUsageError
// for the Go-name spelling being refused instead of silently ignored.
func TestMonitorCreateCheckNotificationsFromCLIInputJSON(t *testing.T) {
	var body []byte
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/vmonitor-uptime-manager/v1/uptimes": func(w http.ResponseWriter, r *http.Request) {
			defer func() { _ = r.Body.Close() }()
			body, _ = io.ReadAll(r.Body)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(monitorUptimesJSON("chk-9", monitor.StatusEnabled)))
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "monitor", "create-check",
		"--name", "vngcloud-test-check",
		"--url", "https://example.com/health",
		"--cli-input-json", `{"Locations":["loc-1"],"Notifications":{"In-alarm":["chan-1"]}}`,
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("create-check: %v (stderr=%s)", err, stderr.String())
	}

	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("body is not valid JSON: %v (%s)", err, body)
	}
	notifications, _ := decoded["notifications"].(map[string]any)
	inAlarm, _ := notifications["In-alarm"].([]any)
	if len(inAlarm) != 1 || inAlarm[0] != "chan-1" {
		t.Fatalf("notifications[In-alarm] = %v, want [chan-1]", notifications["In-alarm"])
	}
	for _, key := range []string{"Up", "Undetermined"} {
		got, _ := notifications[key].([]any)
		if len(got) != 0 {
			t.Fatalf("notifications[%q] = %v, want empty", key, notifications[key])
		}
	}
}

// TestMonitorCreateCheckNotificationsInAlarmKeyIsAUsageError checks that
// create-check's --cli-input-json refuses the Go-name spelling "InAlarm"
// inside Notifications, with zero requests sent: the wire tag is
// "In-alarm" (monitor.CheckNotifications), so "InAlarm" names no field of
// that nested struct at any depth, and the final decode must refuse it
// rather than silently drop it and send an empty notification list.
func TestMonitorCreateCheckNotificationsInAlarmKeyIsAUsageError(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/vmonitor-uptime-manager/v1/uptimes": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "monitor", "create-check",
		"--name", "vngcloud-test-check",
		"--url", "https://example.com/health",
		"--cli-input-json", `{"Locations":["loc-1"],"Notifications":{"InAlarm":["chan-1"]}}`,
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatalf("expected an error for the Go-name spelling InAlarm")
	}
	if got := exitCode(err); got != 2 {
		t.Fatalf("exitCode = %d, want 2 (stderr=%s)", got, stderr.String())
	}
	if n := fixture.requestCount(); n != 0 {
		t.Fatalf("requestCount = %d, want 0", n)
	}
}

// TestMonitorUpdateCheckNotificationsFromCLIInputJSON mirrors
// TestMonitorCreateCheckNotificationsFromCLIInputJSON for update-check:
// Notifications has no flag type either, so --cli-input-json is the only way
// to change it, and the merged PUT body must carry the new value while every
// other field stays what GetCheck's own read supplied.
func TestMonitorUpdateCheckNotificationsFromCLIInputJSON(t *testing.T) {
	var putBody []byte
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/vmonitor-uptime-manager/v1/uptimes/chk-1": monitorCheckPathHandler(t,
			func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(monitorUptimesJSON("chk-1", monitor.StatusEnabled)))
			},
			func(w http.ResponseWriter, r *http.Request) {
				defer func() { _ = r.Body.Close() }()
				putBody, _ = io.ReadAll(r.Body)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(monitorUptimesJSON("chk-1", monitor.StatusEnabled)))
			}),
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "monitor", "update-check",
		"--check-id", "chk-1",
		"--cli-input-json", `{"Notifications":{"In-alarm":["chan-9"]}}`,
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("update-check: %v (stderr=%s)", err, stderr.String())
	}

	var decoded map[string]any
	if err := json.Unmarshal(putBody, &decoded); err != nil {
		t.Fatalf("body is not valid JSON: %v (%s)", err, putBody)
	}
	if decoded["name"] != "example-check" {
		t.Fatalf("body[name] = %v, want the unchanged example-check resent", decoded["name"])
	}
	notifications, _ := decoded["notifications"].(map[string]any)
	inAlarm, _ := notifications["In-alarm"].([]any)
	if len(inAlarm) != 1 || inAlarm[0] != "chan-9" {
		t.Fatalf("notifications[In-alarm] = %v, want [chan-9]", notifications["In-alarm"])
	}
}

// TestMonitorUpdateCheckNotificationsInAlarmKeyIsAUsageError mirrors
// TestMonitorCreateCheckNotificationsInAlarmKeyIsAUsageError for
// update-check: the Go-name spelling "InAlarm" inside Notifications must be
// refused before even the read half of update-check's read-then-write, so
// no GET and no PUT reach the fixture.
func TestMonitorUpdateCheckNotificationsInAlarmKeyIsAUsageError(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/vmonitor-uptime-manager/v1/uptimes/chk-1": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "monitor", "update-check",
		"--check-id", "chk-1",
		"--cli-input-json", `{"Notifications":{"InAlarm":["chan-9"]}}`,
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatalf("expected an error for the Go-name spelling InAlarm")
	}
	if got := exitCode(err); got != 2 {
		t.Fatalf("exitCode = %d, want 2 (stderr=%s)", got, stderr.String())
	}
	if n := fixture.requestCount(); n != 0 {
		t.Fatalf("requestCount = %d, want 0", n)
	}
}

// TestMonitorDeleteCheckWithoutYesExitsWithZeroRequests checks the monitor
// design's --yes rule for delete-check: it is Write and Destructive, so it
// fails with exit code 2 and sends no request unless --yes is given.
func TestMonitorDeleteCheckWithoutYesExitsWithZeroRequests(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/vmonitor-uptime-manager/v1/uptimes/chk-1": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "monitor", "delete-check", "--check-id", "chk-1"})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatalf("expected an error without --yes")
	}
	if got := exitCode(err); got != 2 {
		t.Fatalf("exitCode = %d, want 2 (stderr=%s)", got, stderr.String())
	}
	if n := fixture.requestCount(); n != 0 {
		t.Fatalf("requestCount = %d, want 0", n)
	}
}

// TestMonitorDeleteCheckWithYesSendsDelete checks that --yes lets
// delete-check send exactly one DELETE to the check's path and succeed on
// 204.
func TestMonitorDeleteCheckWithYesSendsDelete(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/vmonitor-uptime-manager/v1/uptimes/chk-1": jsonHandler(http.StatusNoContent, ""),
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "--yes", "monitor", "delete-check", "--check-id", "chk-1"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("delete-check: %v (stderr=%s)", err, stderr.String())
	}
	if got, ok := fixture.methodFor("/vmonitor-uptime-manager/v1/uptimes/chk-1"); !ok || got != http.MethodDelete {
		t.Fatalf("delete-check method = %q, ok=%v, want DELETE", got, ok)
	}
	if n := fixture.requestCount(); n != 1 {
		t.Fatalf("requestCount = %d, want 1", n)
	}
}

// TestMonitorListLocationsEndToEnd runs the real list-locations command
// against a fixture uptime manager, checking the request method and path
// and that the decoded Location list survives the round trip.
func TestMonitorListLocationsEndToEnd(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/vmonitor-uptime-manager/v1/locations": jsonHandler(http.StatusOK,
			`[{"id":"loc-1","name":"SYNTT-VN-HCM01","type":"PUBLIC","status":"REPORTING"}]`),
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "monitor", "list-locations"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("list-locations: %v (stderr=%s)", err, stderr.String())
	}
	if got, ok := fixture.methodFor("/vmonitor-uptime-manager/v1/locations"); !ok || got != http.MethodGet {
		t.Fatalf("list-locations method = %q, ok=%v, want GET", got, ok)
	}
	var out struct{ Items []monitor.Location }
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("stdout is not valid JSON: %v (%s)", err, stdout.String())
	}
	if len(out.Items) != 1 || out.Items[0].Name != "SYNTT-VN-HCM01" {
		t.Fatalf("list-locations Items = %+v", out.Items)
	}
}

// TestMonitorCreateUpdateAndDeleteCheckReadOnlyRefusedWithZeroRequests
// checks the monitor design's read-only rule for create-check, update-check,
// and delete-check: all three are Write operations, so a read-only profile
// refuses each with exit 2 before any request. delete-check needs no --yes
// to be refused this way (read-only is checked before the --yes guard), and
// update-check needs no second field to change: its own "at least one field"
// check (monitor.UpdateCheck) never runs, since read-only refuses the
// command first.
func TestMonitorCreateUpdateAndDeleteCheckReadOnlyRefusedWithZeroRequests(t *testing.T) {
	tests := []struct {
		op   string
		args []string
	}{
		{"create-check", []string{"create-check", "--name", "n", "--url", "https://example.com", "--cli-input-json", `{"Locations":["loc-1"]}`}},
		{"update-check", []string{"update-check", "--check-id", "chk-1"}},
		{"delete-check", []string{"delete-check", "--check-id", "chk-1", "--yes"}},
	}
	for _, tc := range tests {
		t.Run(tc.op, func(t *testing.T) {
			home := withCleanEnv(t)
			writeConfigFile(t, home, "[profile agent]\nregion = hcm-3\nread_only = true\n")
			writeCredentialsFile(t, home, "[agent]\nusername = u\npassword = p\nroot_email = e@example.com\n")

			fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
				"/vmonitor-uptime-manager/v1/uptimes": func(_ http.ResponseWriter, r *http.Request) {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				},
				"/vmonitor-uptime-manager/v1/uptimes/chk-1": func(_ http.ResponseWriter, r *http.Request) {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				},
			})
			opts := newFakeServer(t, fixture.mux)
			withTestOptions(t, append(opts, vngcloud.WithStaticToken("test-token"))...)

			stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
			root := newRootCmd(strings.NewReader(""), stdout, stderr)
			root.SetArgs(append([]string{"--profile", "agent", "monitor"}, tc.args...))
			err := root.ExecuteContext(context.Background())
			if err == nil {
				t.Fatalf("expected a read-only refusal")
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
		})
	}
}

// TestMonitorListChannelTypesEndToEnd runs the real list-channel-types
// command against a fixture notification gateway, checking the request
// method and path and that the decoded types survive the round trip.
func TestMonitorListChannelTypesEndToEnd(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/notification-gateway/api/v1/type/list": jsonHandler(http.StatusOK,
			`{"lstData":[{"id":"type-webhook","name":"Webhook","description":"Webhook"}]}`),
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "monitor", "list-channel-types"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("list-channel-types: %v (stderr=%s)", err, stderr.String())
	}
	if got, ok := fixture.methodFor("/notification-gateway/api/v1/type/list"); !ok || got != http.MethodGet {
		t.Fatalf("list-channel-types method = %q, ok=%v, want GET", got, ok)
	}
	var out struct{ Items []monitor.ChannelType }
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("stdout is not valid JSON: %v (%s)", err, stdout.String())
	}
	if len(out.Items) != 1 || out.Items[0].Name != monitor.ChannelTypeWebhook {
		t.Fatalf("list-channel-types Items = %+v", out.Items)
	}
}

// monitorChannelListJSON renders one Webhook channel, id "channel-1", as the
// notification gateway's own JSON shape (the same shape ListChannels and
// GetChannel both decode through, since there is no get-by-ID call), with a
// raw Address carrying a token in its query string and a raw header Value,
// so the redaction tests below exercise the real command against
// real-looking secrets rather than data that happens to already look
// redacted.
func monitorChannelListJSON() string {
	return `{"lstData":[{"id":"channel-1","name":"example-webhook",` +
		`"address":"https://example.com/hooks/incoming?token=super-secret-token",` +
		`"header":"[{\"key\":\"X-Api-Key\",\"value\":\"super-secret-header-value\"}]",` +
		`"typeNotification":{"id":"type-webhook","name":"Webhook","description":"Webhook"},` +
		`"createdDate":"2026-09-26T15:46:45"}],` +
		`"page":1,"pageSize":10000,"totalPage":1,"totalItem":1}`
}

// monitorSecretMarkers are the raw secret substrings monitorChannelListJSON
// carries. Every redaction test below checks stdout for their absence
// rather than only for the redacted placeholder's presence, so a bug that
// redacts the wrong field still fails the test.
var monitorSecretMarkers = []string{"super-secret-token", "super-secret-header-value"}

func assertNoMonitorSecretMarkers(t *testing.T, label, output string) {
	t.Helper()
	for _, marker := range monitorSecretMarkers {
		if strings.Contains(output, marker) {
			t.Fatalf("%s: output contains an unredacted secret %q:\n%s", label, marker, output)
		}
	}
}

// TestMonitorListChannelsRedactsAcrossFormatsAndQuery checks the monitor
// design's CLI redaction rule end to end: a fixture Webhook channel with a
// real-looking Address and header Value never reaches stdout unredacted, in
// json, table, or text, or through a --query naming the field directly.
// Redaction runs on the SDK's own Output before any of those, not on the
// rendered text, so a query can never reach past it.
func TestMonitorListChannelsRedactsAcrossFormatsAndQuery(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/notification-gateway/api/v1/notification/list/typeSearch": jsonHandler(http.StatusOK, monitorChannelListJSON()),
	})

	tests := []struct {
		name     string
		args     []string
		wantHost bool // whether this output still names the webhook's host
	}{
		{"json", []string{"--output", "json"}, true},
		{"table", []string{"--output", "table"}, true},
		{"text", []string{"--output", "text"}, true},
		{"query-address", []string{"--query", "Items[0].Address"}, true},
		{"query-header", []string{"--query", "Items[0].Headers[0].Value"}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root, stdout, stderr := newSvcRoot(t, fixture)
			root.SetArgs(append([]string{"--region", "hcm-3", "monitor", "list-channels"}, tc.args...))
			if err := root.ExecuteContext(context.Background()); err != nil {
				t.Fatalf("list-channels: %v (stderr=%s)", err, stderr.String())
			}
			assertMonitorRedacted(t, tc.name, stdout.String(), tc.wantHost)
		})
	}
}

// assertMonitorRedacted checks out for the monitor design's CLI redaction
// rule: no raw secret marker survives, and the word "redacted" appears
// (json.Marshal HTML-escapes "<" and ">" to "\u003c" and "\u003e" inside a
// nested object rendered as a compact JSON table or text cell, so the
// literal string "<redacted>" is not a reliable substring to look for
// across every render path; "redacted" alone is). wantHost also requires
// the webhook channel's host, which redaction keeps, still to appear.
func assertMonitorRedacted(t *testing.T, label, out string, wantHost bool) {
	t.Helper()
	assertNoMonitorSecretMarkers(t, label, out)
	if !strings.Contains(out, "redacted") {
		t.Fatalf("%s: stdout = %s, want it to contain the redacted placeholder", label, out)
	}
	if wantHost && !strings.Contains(out, "example.com") {
		t.Fatalf("%s: stdout = %s, want it to still show the webhook's host", label, out)
	}
}

// TestMonitorGetChannelRedactsAcrossFormatsAndQuery mirrors
// TestMonitorListChannelsRedactsAcrossFormatsAndQuery for get-channel, whose
// Output nests the same Channel one level deeper under "Channel".
func TestMonitorGetChannelRedactsAcrossFormatsAndQuery(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/notification-gateway/api/v1/notification/list/typeSearch": jsonHandler(http.StatusOK, monitorChannelListJSON()),
	})

	tests := []struct {
		name     string
		args     []string
		wantHost bool
	}{
		{"json", []string{"--output", "json"}, true},
		{"table", []string{"--output", "table"}, true},
		{"text", []string{"--output", "text"}, true},
		{"query-address", []string{"--query", "Channel.Address"}, true},
		{"query-header", []string{"--query", "Channel.Headers[0].Value"}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root, stdout, stderr := newSvcRoot(t, fixture)
			args := append([]string{"--region", "hcm-3", "monitor", "get-channel", "--channel-id", "channel-1"}, tc.args...)
			root.SetArgs(args)
			if err := root.ExecuteContext(context.Background()); err != nil {
				t.Fatalf("get-channel: %v (stderr=%s)", err, stderr.String())
			}
			assertMonitorRedacted(t, tc.name, stdout.String(), tc.wantHost)
		})
	}
}

// monitorSlackChannelListJSON renders one Slack channel in the same shape
// monitorChannelListJSON uses for Webhook, with the secret token in the
// incoming webhook URL's path rather than a query string (the real shape a
// Slack webhook takes), so the redaction tests below also cover a channel
// type whose secret sits in a different part of the Address.
func monitorSlackChannelListJSON(id string) string {
	return `{"lstData":[{"id":"` + id + `","name":"example-slack",` +
		`"address":"https://hooks.slack.com/services/T000/B000/super-secret-slack-token",` +
		`"typeNotification":{"id":"type-slack","name":"Slack","description":"Slack"},` +
		`"createdDate":"2026-09-26T15:46:45"}],` +
		`"page":1,"pageSize":10000,"totalPage":1,"totalItem":1}`
}

// TestMonitorListChannelsRedactsSlackChannel checks the redaction rule end
// to end for a Slack channel, not just Webhook: the secret in its Address
// path never reaches stdout, while the host redaction keeps still does.
func TestMonitorListChannelsRedactsSlackChannel(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/notification-gateway/api/v1/notification/list/typeSearch": jsonHandler(http.StatusOK, monitorSlackChannelListJSON("channel-2")),
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "monitor", "list-channels"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("list-channels: %v (stderr=%s)", err, stderr.String())
	}
	out := stdout.String()
	if strings.Contains(out, "super-secret-slack-token") {
		t.Fatalf("stdout contains the unredacted Slack token:\n%s", out)
	}
	if !strings.Contains(out, "redacted") {
		t.Fatalf("stdout = %s, want it to contain the redacted placeholder", out)
	}
	if !strings.Contains(out, "hooks.slack.com") {
		t.Fatalf("stdout = %s, want it to still show the Slack channel's host", out)
	}
}

// TestMonitorListChannelsAndGetChannelDebugLeakNoSecrets checks that
// --debug, which turns on the SDK transport's own request logging, never
// puts the fixture's raw Address token or header value on stderr, and that
// stdout still redacts them as usual, for both channel Read commands.
func TestMonitorListChannelsAndGetChannelDebugLeakNoSecrets(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/notification-gateway/api/v1/notification/list/typeSearch": jsonHandler(http.StatusOK, monitorChannelListJSON()),
	})

	tests := []struct {
		name string
		args []string
	}{
		{"list-channels", []string{"monitor", "list-channels"}},
		{"get-channel", []string{"monitor", "get-channel", "--channel-id", "channel-1"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root, stdout, stderr := newSvcRoot(t, fixture)
			root.SetArgs(append([]string{"--region", "hcm-3", "--debug"}, tc.args...))
			if err := root.ExecuteContext(context.Background()); err != nil {
				t.Fatalf("%s: %v (stderr=%s)", tc.name, err, stderr.String())
			}
			assertNoMonitorSecretMarkers(t, tc.name+" stdout", stdout.String())
			assertNoMonitorSecretMarkers(t, tc.name+" stderr", stderr.String())
		})
	}
}

// TestMonitorGetChannelNotFoundExitsFour checks that GetChannel's not-found
// sentinel, from a full page walk with no matching ID, reaches the CLI as
// the NotFound error class with exit code 4, the design's result for a
// missing channel.
func TestMonitorGetChannelNotFoundExitsFour(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/notification-gateway/api/v1/notification/list/typeSearch": jsonHandler(http.StatusOK,
			`{"lstData":[],"page":1,"pageSize":10000,"totalPage":0,"totalItem":0}`),
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "monitor", "get-channel", "--channel-id", "missing"})
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

// exampleLogProject is a LogProject shaped like the design's inferred
// fields, reused by the golden tests below. LogProject's own per-project
// field shape is unverified live (see monitor.LogProject's doc comment), so
// this is a plausible shape, not a captured one.
func exampleLogProject(id, name string) monitor.LogProject {
	return monitor.LogProject{
		ID:                 id,
		ProjectName:        name,
		ProjectDescription: "app logs",
		Status:             "ACTIVE",
		BillingStatus:      "PAID",
		ProjectType:        "LOG",
		Zone:               "hcm-3",
		CreatedAt:          "Jan 1, 2026, 12:00:00 AM",
		UpdatedAt:          "Jan 2, 2026, 1:00:00 PM",
	}
}

// TestGoldenMonitorListLogProjects checks list-log-projects' exact output
// shape: a PagedList with the list's own 0-based Page, unlike ListChannels'
// 1-based one.
func TestGoldenMonitorListLogProjects(t *testing.T) {
	v := core.NewPagedList([]monitor.LogProject{exampleLogProject("proj-1", "app")}, 0, 100, 1, 1)
	checkGolden(t, "monitor-list-log-projects.json.golden", "json", "", v)
	checkGolden(t, "monitor-list-log-projects.table.golden", "table", "", v)
	checkGolden(t, "monitor-list-log-projects.text.golden", "text", "", v)
}

// TestGoldenMonitorGetLogProject checks get-log-project's exact output
// shape: {"LogProject": {...}}.
func TestGoldenMonitorGetLogProject(t *testing.T) {
	v := &monitor.GetLogProjectOutput{LogProject: exampleLogProject("proj-1", "app")}
	checkGolden(t, "monitor-get-log-project.json.golden", "json", "", v)
	checkGolden(t, "monitor-get-log-project.table.golden", "table", "", v)
	checkGolden(t, "monitor-get-log-project.text.golden", "text", "", v)
}

// exampleLogProjectClasses is a short LogProjectClass list covering an
// active class with one retention option and a disabled class with none,
// shaped like the billing quota-class fixture ListLogProjectClasses decodes
// live.
func exampleLogProjectClasses() []monitor.LogProjectClass {
	return []monitor.LogProjectClass{
		{
			ID:          "class-basic",
			Name:        monitor.LogProjectClassBasic,
			Description: "A free option with a 1-day retention.",
			Priority:    1,
			Status:      monitor.LogProjectClassStatusActive,
			Retentions: []monitor.LogProjectRetention{
				{Amount: 1, MinSize: 10, MaxSize: 10, Step: 1, PackageID: "pkg-basic-1d"},
			},
		},
		{
			ID:          "class-enterprise",
			Name:        "Enterprise (Coming soon)",
			Description: "Not orderable today.",
			Priority:    3,
			Status:      monitor.LogProjectClassStatusDisabled,
		},
	}
}

// TestGoldenMonitorListLogProjectClasses checks list-log-project-classes'
// exact output shape: JSON keeps {"Items": [...]}, one class per row for
// table and text, with each class's Retentions rendered as a nested list.
func TestGoldenMonitorListLogProjectClasses(t *testing.T) {
	v := &monitor.ListLogProjectClassesOutput{Items: exampleLogProjectClasses()}
	checkGolden(t, "monitor-list-log-project-classes.json.golden", "json", "", v)
	checkGolden(t, "monitor-list-log-project-classes.table.golden", "table", "", v)
	checkGolden(t, "monitor-list-log-project-classes.text.golden", "text", "", v)
}

// exampleQuoteCreateLogProjectOutput is a QuoteCreateLogProjectOutput shaped
// like the created-price fixture QuoteCreateLogProject decodes live: a
// DiscountPercent of nil, seen for both a free and a paid class.
func exampleQuoteCreateLogProjectOutput() *monitor.QuoteCreateLogProjectOutput {
	return &monitor.QuoteCreateLogProjectOutput{
		OptimumPrice:  917000,
		OriginalPrice: 917000,
		DiscountPrice: 0,
		Properties: []monitor.LogProjectPriceProperty{
			{Name: "monitor-platform-log", OptimumPrice: 917000, OriginalPrice: 917000, DiscountPrice: 0},
		},
	}
}

// TestGoldenMonitorQuoteCreateLogProject checks quote-create-log-project's
// exact output shape: the price fields directly, not wrapped in a resource
// field, the same shape pricing get-quote uses.
func TestGoldenMonitorQuoteCreateLogProject(t *testing.T) {
	v := exampleQuoteCreateLogProjectOutput()
	checkGolden(t, "monitor-quote-create-log-project.json.golden", "json", "", v)
	checkGolden(t, "monitor-quote-create-log-project.table.golden", "table", "", v)
	checkGolden(t, "monitor-quote-create-log-project.text.golden", "text", "", v)
}

// TestMonitorListLogProjectsEndToEnd checks list-log-projects' flags reach
// the list's own query keys: --search (Query's renamed flag, since --query
// is a global flag), --billing-status, --page, and --size.
func TestMonitorListLogProjectsEndToEnd(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/log-api/v1/projects": jsonHandler(http.StatusOK,
			`{"content":[{"id":"proj-1","projectName":"app","status":"ACTIVE"}],`+
				`"currentPage":0,"pageSize":50,"totalElements":1,"totalPages":1}`),
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "monitor", "list-log-projects",
		"--search", "app", "--billing-status", "PAID", "--page", "0", "--size", "50",
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("list-log-projects: %v (stderr=%s)", err, stderr.String())
	}
	if got, ok := fixture.methodFor("/log-api/v1/projects"); !ok || got != http.MethodGet {
		t.Fatalf("list-log-projects method = %q, ok=%v, want GET", got, ok)
	}
	q, ok := fixture.queryFor("/log-api/v1/projects")
	if !ok {
		t.Fatal("no recorded query for /log-api/v1/projects")
	}
	for _, want := range []string{"query=app", "billing_status=PAID", "page=0", "size=50"} {
		if !strings.Contains(q, want) {
			t.Fatalf("list-log-projects query = %q, want it to contain %q", q, want)
		}
	}
	var out struct{ Items []monitor.LogProject }
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("stdout is not valid JSON: %v (%s)", err, stdout.String())
	}
	if len(out.Items) != 1 || out.Items[0].ProjectName != "app" {
		t.Fatalf("list-log-projects Items = %+v", out.Items)
	}
}

// TestMonitorGetLogProjectEndToEnd runs the real get-log-project command
// against a fixture log API, checking the request path and that the
// decoded LogProject survives the round trip.
func TestMonitorGetLogProjectEndToEnd(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/log-api/v1/projects/proj-1": jsonHandler(http.StatusOK,
			`{"id":"proj-1","projectName":"app","status":"ACTIVE"}`),
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "monitor", "get-log-project", "--log-project-id", "proj-1"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("get-log-project: %v (stderr=%s)", err, stderr.String())
	}
	if got, ok := fixture.methodFor("/log-api/v1/projects/proj-1"); !ok || got != http.MethodGet {
		t.Fatalf("get-log-project method = %q, ok=%v, want GET", got, ok)
	}
	var out struct{ LogProject monitor.LogProject }
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("stdout is not valid JSON: %v (%s)", err, stdout.String())
	}
	if out.LogProject.ID != "proj-1" {
		t.Fatalf("get-log-project LogProject = %+v", out.LogProject)
	}
}

// TestMonitorGetLogProjectNotFoundExitsFour checks that a 404 from the log
// API reaches the CLI as the NotFound error class with exit code 4, the
// same path monitor.GetLogProject's own 404 test confirms at the SDK level.
func TestMonitorGetLogProjectNotFoundExitsFour(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/log-api/v1/projects/missing": jsonHandler(http.StatusNotFound, `{"message":"not found"}`),
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "monitor", "get-log-project", "--log-project-id", "missing"})
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

// TestMonitorListLogProjectClassesEndToEnd runs the real
// list-log-project-classes command against a fixture billing surface,
// checking the request path and that the nested Retentions list survives
// the round trip.
func TestMonitorListLogProjectClassesEndToEnd(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/billing-api/v2/log/quota-class": jsonHandler(http.StatusOK,
			`[{"id":"class-basic","name":"Basic","status":"ACTIVE",`+
				`"config":{"retentions":[{"amount":1,"minSize":10,"maxSize":10,"step":1,"packageId":"pkg-basic-1d"}]}}]`),
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "monitor", "list-log-project-classes"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("list-log-project-classes: %v (stderr=%s)", err, stderr.String())
	}
	if got, ok := fixture.methodFor("/billing-api/v2/log/quota-class"); !ok || got != http.MethodGet {
		t.Fatalf("list-log-project-classes method = %q, ok=%v, want GET", got, ok)
	}
	// The CLI's own JSON encoder writes Retentions as a top-level key (Go
	// field names, per the CLI design's "Output"), but LogProjectClass
	// carries a custom UnmarshalJSON that expects the wire shape's nested
	// config.retentions instead: decoding stdout back into that type would
	// invoke it and read an empty Retentions. A plain local struct, with no
	// such method, decodes the CLI's own output shape correctly, the same
	// workaround TestProjectListProjectsRegionFiltersClientSide uses for a
	// resource type whose JSON tags do not match its own Go field names.
	type decodedClass struct {
		Name       string
		Status     string
		Retentions []monitor.LogProjectRetention
	}
	var out struct{ Items []decodedClass }
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("stdout is not valid JSON: %v (%s)", err, stdout.String())
	}
	if len(out.Items) != 1 || out.Items[0].Name != monitor.LogProjectClassBasic || len(out.Items[0].Retentions) != 1 {
		t.Fatalf("list-log-project-classes Items = %+v", out.Items)
	}
}

// TestMonitorQuoteCreateLogProjectSendsRequestBody drives
// quote-create-log-project with every flag-settable CreateLogProjectInput
// field set, checking the exact created-price request body the CLI builds
// from that merge, the same body buildLogProjectOrderBody's own SDK test
// (monitor.TestQuoteCreateLogProjectDecodesFixture) checks for a direct SDK
// call: this only confirms the flags reach the SDK's Input field for field,
// not the builder's own logic, which is already covered there.
func TestMonitorQuoteCreateLogProjectSendsRequestBody(t *testing.T) {
	var body []byte
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/billing-api/v2/log/quota-class": jsonHandler(http.StatusOK,
			`[{"id":"class-pro","name":"Pro","status":"ACTIVE",`+
				`"config":{"retentions":[{"amount":7,"minSize":20,"maxSize":5000,"step":10,"packageId":"pkg-pro-7d"}]}}]`),
		"/billing-api/v2/log/prices/created-price": func(w http.ResponseWriter, r *http.Request) {
			defer func() { _ = r.Body.Close() }()
			body, _ = io.ReadAll(r.Body)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"optimumPrice":917000,"originalPrice":917000,"discountPrice":0,"propertiesPrice":[]}`))
		},
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "monitor", "quote-create-log-project",
		"--name", "app",
		"--description", "logs",
		"--class", "Pro",
		"--retention-days", "7",
		"--gb-per-day", "20",
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("quote-create-log-project: %v (stderr=%s)", err, stderr.String())
	}
	if got, ok := fixture.methodFor("/billing-api/v2/log/prices/created-price"); !ok || got != http.MethodPost {
		t.Fatalf("quote-create-log-project method = %q, ok=%v, want POST", got, ok)
	}

	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("body is not valid JSON: %v (%s)", err, body)
	}
	if decoded["packageId"] != "pkg-pro-7d" {
		t.Fatalf("body[packageId] = %v, want pkg-pro-7d (%s)", decoded["packageId"], body)
	}
	if decoded["quantity"] != 140.0 {
		t.Fatalf("body[quantity] = %v, want 140 (%s)", decoded["quantity"], body)
	}
	if decoded["projectName"] != "app" || decoded["projectDescription"] != "logs" {
		t.Fatalf("body[projectName/projectDescription] = %+v (%s)", decoded, body)
	}
	if decoded["monthPeriod"] != 1.0 || decoded["pay"] != true {
		t.Fatalf("body[monthPeriod/pay] = %+v (%s)", decoded, body)
	}

	var out monitor.QuoteCreateLogProjectOutput
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("stdout is not valid JSON: %v (%s)", err, stdout.String())
	}
	if out.OptimumPrice != 917000 {
		t.Fatalf("OptimumPrice = %v, want 917000", out.OptimumPrice)
	}
}

// TestMonitorQuoteCreateLogProjectHasNoMaxPriceOrNoWaitFlag checks that
// MaxPrice and NoWait, which only govern create-log-project's own order and
// wait, a later release, register no flag on quote-create-log-project; both
// stay settable only through --cli-input-json until create-log-project
// ships, and this command ignores them even then.
func TestMonitorQuoteCreateLogProjectHasNoMaxPriceOrNoWaitFlag(t *testing.T) {
	cmd := newMonitorCmd(&env{flags: &globalFlags{}})
	sub, _, err := cmd.Find([]string{"quote-create-log-project"})
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if f := sub.Flags().Lookup("max-price"); f != nil {
		t.Fatalf("quote-create-log-project registered its own --max-price flag: %+v", f)
	}
	if f := sub.Flags().Lookup("no-wait"); f != nil {
		t.Fatalf("quote-create-log-project registered its own --no-wait flag: %+v", f)
	}
}

// TestMonitorQuoteCreateLogProjectMissingNameExitsWithZeroRequests checks
// that quote-create-log-project without --name fails the required-field
// check before any request, including the class list read.
func TestMonitorQuoteCreateLogProjectMissingNameExitsWithZeroRequests(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/billing-api/v2/log/quota-class": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "monitor", "quote-create-log-project"})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatalf("expected a required-field error without --name")
	}
	if got := exitCode(err); got != 2 {
		t.Fatalf("exitCode = %d, want 2 (stderr=%s)", got, stderr.String())
	}
	if n := fixture.requestCount(); n != 0 {
		t.Fatalf("requestCount = %d, want 0", n)
	}
}

// monitorAlarmListJSON renders one Log alarm, id "alarm-1", as the alarm
// API's own list shape (lstData plus paging), with inAlarm and ok as the
// comma-joined channel ID strings the design describes.
func monitorAlarmListJSON() string {
	return `{"lstData":[{"id":"alarm-1","name":"example-log-alarm","status":"OK","severity":"MEDIUM",` +
		`"inAlarm":"channel-1,channel-2,","ok":"channel-1,"}],` +
		`"page":1,"pageSize":10000,"totalPage":1,"totalItem":1}`
}

// monitorAlarmGetJSON renders the same Log alarm as GetAlarm's own shape:
// the alarm wrapped in a top-level "data" field, with no channel alerting on
// leaving the alarm state.
func monitorAlarmGetJSON() string {
	return `{"data":{"id":"alarm-1","name":"example-log-alarm","status":"OK","severity":"MEDIUM",` +
		`"inAlarm":"channel-1,","ok":""}}`
}

// TestMonitorListAlarmsEndToEnd runs the real list-alarms command against a
// fixture alarm API, checking the request method, path, and the type-alarm
// query key --kind sends, and that the decoded Alarm survives the round
// trip, Kind included.
func TestMonitorListAlarmsEndToEnd(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/vmonitor-api/api/v1/alarms/list": jsonHandler(http.StatusOK, monitorAlarmListJSON()),
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "monitor", "list-alarms", "--kind", monitor.AlarmKindLog})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("list-alarms: %v (stderr=%s)", err, stderr.String())
	}
	if got, ok := fixture.methodFor("/vmonitor-api/api/v1/alarms/list"); !ok || got != http.MethodGet {
		t.Fatalf("list-alarms method = %q, ok=%v, want GET", got, ok)
	}
	if q, ok := fixture.queryFor("/vmonitor-api/api/v1/alarms/list"); !ok || !strings.Contains(q, "type-alarm=Log") {
		t.Fatalf("list-alarms query = %q, ok=%v, want it to contain type-alarm=Log", q, ok)
	}
	// Decoded into a plain local struct, not monitor.Alarm: the CLI's own
	// JSON keys are Go field names (per the CLI design's "Output"), but
	// Alarm's custom UnmarshalJSON expects the wire's lowercase keys and
	// never sets Kind itself (ListAlarms sets it from its own Kind filter
	// after decode), so round-tripping this output back through it would
	// lose Kind rather than read it.
	var out struct {
		Items []struct {
			ID   string
			Name string
			Kind string
		}
	}
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("list-alarms stdout is not valid JSON: %v (%s)", err, stdout.String())
	}
	if len(out.Items) != 1 || out.Items[0].Name != "example-log-alarm" {
		t.Fatalf("list-alarms Items = %+v", out.Items)
	}
	if out.Items[0].Kind != monitor.AlarmKindLog {
		t.Fatalf("list-alarms Items[0].Kind = %q, want %q", out.Items[0].Kind, monitor.AlarmKindLog)
	}
}

// TestMonitorListAlarmsMissingKindExitsWithZeroRequests checks that
// list-alarms without --kind fails the required-field check before any
// request: Kind is ListAlarmsInput's only required field, and the console
// always sends one, per the monitor design.
func TestMonitorListAlarmsMissingKindExitsWithZeroRequests(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/vmonitor-api/api/v1/alarms/list": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "monitor", "list-alarms"})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatalf("expected a required-field error without --kind")
	}
	if got := exitCode(err); got != 2 {
		t.Fatalf("exitCode = %d, want 2 (stderr=%s)", got, stderr.String())
	}
	if n := fixture.requestCount(); n != 0 {
		t.Fatalf("requestCount = %d, want 0", n)
	}
}

// TestMonitorGetAlarmEndToEnd runs the real get-alarm command against a
// fixture alarm API, checking the request method and path and that the
// decoded Alarm survives the round trip: Kind comes back empty, since
// get-alarm takes no kind filter and the API sends no field confirmed to
// name it, while Log still decodes from the inAlarm and ok fields the
// response carries.
func TestMonitorGetAlarmEndToEnd(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/vmonitor-api/api/v1/alarms/alarm-1": jsonHandler(http.StatusOK, monitorAlarmGetJSON()),
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "monitor", "get-alarm", "--alarm-id", "alarm-1"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("get-alarm: %v (stderr=%s)", err, stderr.String())
	}
	if got, ok := fixture.methodFor("/vmonitor-api/api/v1/alarms/alarm-1"); !ok || got != http.MethodGet {
		t.Fatalf("get-alarm method = %q, ok=%v, want GET", got, ok)
	}
	// Decoded into a plain local struct, not monitor.Alarm: see the same note
	// on TestMonitorListAlarmsEndToEnd.
	var out struct {
		Alarm struct {
			ID   string
			Name string
			Kind string
			Log  struct {
				InAlarm []string
			}
		}
	}
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("get-alarm stdout is not valid JSON: %v (%s)", err, stdout.String())
	}
	if out.Alarm.ID != "alarm-1" || out.Alarm.Kind != "" {
		t.Fatalf("get-alarm Alarm = %+v", out.Alarm)
	}
	if want := []string{"channel-1"}; !slices.Equal(out.Alarm.Log.InAlarm, want) {
		t.Fatalf("get-alarm Alarm.Log.InAlarm = %v, want %v", out.Alarm.Log.InAlarm, want)
	}
}

// TestMonitorGetAlarmUnknownIDExitsOneNotFour checks the monitor design's
// own note for get-alarm: the API answers an unknown ID with a 500, not a
// 404, so vngcloud.IsNotFound never matches it and the command exits 1 with
// the status-derived ServerError class rather than exit 4 with NotFound.
func TestMonitorGetAlarmUnknownIDExitsOneNotFour(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/vmonitor-api/api/v1/alarms/missing": jsonHandler(http.StatusInternalServerError, `{"message":"internal error"}`),
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "monitor", "get-alarm", "--alarm-id", "missing"})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected an error for the unknown ID")
	}
	if got := classify(err).Code; got != "ServerError" {
		t.Fatalf("Code = %q, want ServerError (stderr=%s)", got, stderr.String())
	}
	if got := exitCode(err); got != 1 {
		t.Fatalf("exitCode = %d, want 1", got)
	}
}
