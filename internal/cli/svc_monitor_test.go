package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"danny.vn/vngcloud"
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
