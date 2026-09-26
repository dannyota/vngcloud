package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/monitor"
)

// logProjectClassesJSON renders one ListLogProjectClasses page holding a
// single active class named name, with one retention option: amount days,
// minSize gb, and packageID. It mirrors the shape
// TestMonitorQuoteCreateLogProjectSendsRequestBody already drives
// quote-create-log-project against, reused here for create-log-project,
// which quotes with the same call before it orders.
func logProjectClassesJSON(name string, amount, minSize int, packageID string) string {
	return fmt.Sprintf(
		`[{"id":"class-1","name":%q,"status":"ACTIVE",`+
			`"config":{"retentions":[{"amount":%d,"minSize":%d,"maxSize":5000,"step":10,"packageId":%q}]}}]`,
		name, amount, minSize, packageID)
}

// logProjectQuoteJSON renders one created-price response priced at
// optimumPrice.
func logProjectQuoteJSON(optimumPrice float64) string {
	return fmt.Sprintf(`{"optimumPrice":%v,"originalPrice":%v,"discountPrice":0,"propertiesPrice":[]}`,
		optimumPrice, optimumPrice)
}

// logProjectListEntryJSON renders one ListLogProjects page holding a single
// project matching id, name, and status, the shape create-log-project's
// post-order wait lists by name.
func logProjectListEntryJSON(id, name, status string) string {
	return fmt.Sprintf(
		`{"content":[{"id":%q,"name":%q,"status":%q}],`+
			`"currentPage":0,"pageSize":100,"totalElements":1,"totalPages":1}`,
		id, name, status)
}

// logProjectEmptyListJSON is one empty ListLogProjects page: no project
// matches, the shape create-log-project's own pre-order duplicate-name
// check (a ListLogProjects read by Name) gets for a name nothing already
// uses.
const logProjectEmptyListJSON = `{"content":[],"currentPage":0,"pageSize":100,"totalElements":0,"totalPages":0}`

// TestMonitorCreateLogProjectSendsOrderRequestBody checks create-log-project's
// flag-to-input mapping end to end: every flag-settable CreateLogProjectInput
// field reaches the order body the shared builder produces, the same body
// monitor.TestCreateLogProjectOrderUsesSharedBuilderBody checks at the SDK
// level for a direct call. --no-wait skips the post-order list, so this only
// exercises the quote and the order; the order response uses its real,
// live-confirmed shape (amount, orderId, paymentUrl, no project fields), so
// the only thing --no-wait can print is OrderID.
func TestMonitorCreateLogProjectSendsOrderRequestBody(t *testing.T) {
	var orderBody []byte
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/log-api/v1/projects":                     jsonHandler(http.StatusOK, logProjectEmptyListJSON),
		"/billing-api/v2/log/quota-class":          jsonHandler(http.StatusOK, logProjectClassesJSON("Pro", 7, 20, "pkg-pro-7d")),
		"/billing-api/v2/log/prices/created-price": jsonHandler(http.StatusOK, logProjectQuoteJSON(917000)),
		"/billing-api/v2/log/quotas": func(w http.ResponseWriter, r *http.Request) {
			defer func() { _ = r.Body.Close() }()
			if r.Method != http.MethodPost {
				t.Fatalf("method = %s, want POST", r.Method)
			}
			orderBody, _ = io.ReadAll(r.Body)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"amount":917000,"orderId":"order-1","paymentUrl":""}`))
		},
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "monitor", "create-log-project",
		"--name", "app", "--description", "logs", "--class", "Pro",
		"--retention-days", "7", "--gb-per-day", "20", "--max-price", "917000", "--no-wait",
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("create-log-project: %v (stderr=%s)", err, stderr.String())
	}

	var decoded map[string]any
	if err := json.Unmarshal(orderBody, &decoded); err != nil {
		t.Fatalf("order body is not valid JSON: %v (%s)", err, orderBody)
	}
	if decoded["packageId"] != "pkg-pro-7d" || decoded["quantity"] != 140.0 {
		t.Fatalf("order body = %+v, want packageId pkg-pro-7d and quantity 140", decoded)
	}
	if decoded["projectName"] != "app" || decoded["projectDescription"] != "logs" {
		t.Fatalf("order body = %+v, want projectName app and projectDescription logs", decoded)
	}
	if decoded["monthPeriod"] != 1.0 || decoded["pay"] != true {
		t.Fatalf("order body = %+v, want monthPeriod 1 and pay true", decoded)
	}

	out := stdout.String()
	if !strings.Contains(out, `"OrderID": "order-1"`) {
		t.Fatalf("stdout = %s, want the order's OrderID printed", out)
	}
	// LogProject holds no secret the monitor design's redaction rule covers
	// (see the ops table comment in svc_monitor.go), so nothing here should
	// ever print the "<redacted>" placeholder create-channel's Output does.
	if strings.Contains(out, "redacted") {
		t.Fatalf("stdout = %s, want no redaction placeholder: a log project holds no secret to redact", out)
	}
}

// TestMonitorCreateLogProjectDefaultOrdersOnlyFree checks that a bare
// create-log-project --name <name>, with --max-price left at its default of
// 0, only ever orders a class and retention priced at 0, mirroring
// monitor.TestCreateLogProjectDefaultMaxPriceOrdersOnlyFree at the CLI level.
func TestMonitorCreateLogProjectDefaultOrdersOnlyFree(t *testing.T) {
	var orderCalls atomic.Int64
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/log-api/v1/projects":                     jsonHandler(http.StatusOK, logProjectEmptyListJSON),
		"/billing-api/v2/log/quota-class":          jsonHandler(http.StatusOK, logProjectClassesJSON("Basic", 1, 10, "pkg-basic-1d")),
		"/billing-api/v2/log/prices/created-price": jsonHandler(http.StatusOK, logProjectQuoteJSON(0)),
		"/billing-api/v2/log/quotas": func(w http.ResponseWriter, r *http.Request) {
			orderCalls.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"amount":0,"orderId":"order-1","paymentUrl":""}`))
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "monitor", "create-log-project", "--name", "app", "--no-wait"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("create-log-project: %v (stderr=%s)", err, stderr.String())
	}
	if orderCalls.Load() != 1 {
		t.Fatalf("order calls = %d, want 1", orderCalls.Load())
	}
}

// TestMonitorCreateLogProjectRefusesAboveMaxPrice checks the design's price
// guard: a quote above --max-price (left at its default of 0 here) is
// refused with the PriceAboveMax error class and exit code 1, and no order
// is ever sent.
func TestMonitorCreateLogProjectRefusesAboveMaxPrice(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/log-api/v1/projects":                     jsonHandler(http.StatusOK, logProjectEmptyListJSON),
		"/billing-api/v2/log/quota-class":          jsonHandler(http.StatusOK, logProjectClassesJSON("Pro", 7, 20, "pkg-pro-7d")),
		"/billing-api/v2/log/prices/created-price": jsonHandler(http.StatusOK, logProjectQuoteJSON(917000)),
		"/billing-api/v2/log/quotas": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "monitor", "create-log-project",
		"--name", "app", "--class", "Pro", "--retention-days", "7", "--gb-per-day", "20", "--no-wait",
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected a PriceAboveMax refusal")
	}
	if got := classify(err).Code; got != "PriceAboveMax" {
		t.Fatalf("Code = %q, want PriceAboveMax (stderr=%s)", got, stderr.String())
	}
	if got := exitCode(err); got != 1 {
		t.Fatalf("exitCode = %d, want 1", got)
	}
	if n := fixture.requestCount(); n != 3 {
		t.Fatalf("requestCount = %d, want 3 (name check, classes, and quote only, no order)", n)
	}
}

// TestMonitorCreateLogProjectNotSettledOnCanceledContext drives a real
// create-log-project call whose order succeeds and whose post-order wait is
// interrupted by canceling the command's own context, mirroring a Ctrl-C
// during the wait, the same technique
// TestDNSCreateHostedZoneNotSettledOnCanceledContext uses for vDNS. The list
// fixture serves the pre-order duplicate-name check's first call an empty
// page (no project named "app" yet, so the order proceeds), then answers
// the post-order wait's lookup twice, with the project still CREATING (the
// order reached the server), and cancels only after that second wait
// answer, the third call overall: canceling after the first wait answer
// would race the client's own read of that response against the
// cancellation, risking a decode failure that leaves no project found at
// all. By the second wait answer the first has already been decoded, so
// the next poll step's sleep fails on the canceled context, not on the
// real 120-second bound, deterministically. Per the design, this must
// surface as NotSettled with the last project the SDK found by name
// printed on stdout, not the plain canceled-context path.
func TestMonitorCreateLogProjectNotSettledOnCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var listCalls atomic.Int64
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/billing-api/v2/log/quota-class":          jsonHandler(http.StatusOK, logProjectClassesJSON("Basic", 1, 10, "pkg-basic-1d")),
		"/billing-api/v2/log/prices/created-price": jsonHandler(http.StatusOK, logProjectQuoteJSON(0)),
		"/billing-api/v2/log/quotas":               jsonHandler(http.StatusOK, `{"amount":0,"orderId":"order-1","paymentUrl":""}`),
		"/log-api/v1/projects": func(w http.ResponseWriter, r *http.Request) {
			n := listCalls.Add(1)
			w.Header().Set("Content-Type", "application/json")
			if n == 1 {
				_, _ = w.Write([]byte(logProjectEmptyListJSON))
			} else {
				_, _ = w.Write([]byte(logProjectListEntryJSON("proj-1", "app", "CREATING")))
			}
			if n == 3 {
				cancel()
			}
		},
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "monitor", "create-log-project", "--name", "app"})
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
	if got := stdout.String(); !strings.Contains(got, `"ID": "proj-1"`) {
		t.Fatalf("stdout = %s, want the last-read project printed alongside the error", got)
	}
}

// TestMonitorCreateLogProjectReadOnlyRefusedWithZeroRequests checks the
// design's read-only rule: create-log-project is a Write operation, so a
// read-only profile refuses it with exit 2 before any request, including the
// quote's own class-list read.
func TestMonitorCreateLogProjectReadOnlyRefusedWithZeroRequests(t *testing.T) {
	home := withCleanEnv(t)
	writeConfigFile(t, home, "[profile agent]\nregion = hcm-3\nread_only = true\n")
	writeCredentialsFile(t, home, "[agent]\nusername = u\npassword = p\nroot_email = e@example.com\n")

	refuse := func(_ http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
	}
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/billing-api/v2/log/quota-class":          refuse,
		"/billing-api/v2/log/prices/created-price": refuse,
		"/billing-api/v2/log/quotas":               refuse,
	})
	opts := newFakeServer(t, fixture.mux)
	withTestOptions(t, append(opts, vngcloud.WithStaticToken("test-token"))...)

	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	root := newRootCmd(strings.NewReader(""), stdout, stderr)
	root.SetArgs([]string{"--profile", "agent", "monitor", "create-log-project", "--name", "app"})
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

// TestMonitorCreateLogProjectDebugLogsStartAndFinishWithOnlyOperationName
// mirrors TestBillingWriteDebugLogsStartAndFinishWithOnlyOperationName for
// create-log-project: --debug logs only the operation name around the call,
// never the order body, so a project's description or price never reaches a
// debug transcript.
func TestMonitorCreateLogProjectDebugLogsStartAndFinishWithOnlyOperationName(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/log-api/v1/projects":                     jsonHandler(http.StatusOK, logProjectEmptyListJSON),
		"/billing-api/v2/log/quota-class":          jsonHandler(http.StatusOK, logProjectClassesJSON("Basic", 1, 10, "pkg-basic-1d")),
		"/billing-api/v2/log/prices/created-price": jsonHandler(http.StatusOK, logProjectQuoteJSON(0)),
		"/billing-api/v2/log/quotas":               jsonHandler(http.StatusOK, `{"amount":0,"orderId":"order-1","paymentUrl":""}`),
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--debug", "monitor", "create-log-project",
		"--name", "app", "--description", "confidential-notes", "--no-wait",
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
		if !strings.Contains(line, `operation="monitor create-log-project"`) {
			t.Fatalf("write debug line missing the operation name: %s", line)
		}
		if strings.Contains(line, "confidential-notes") || strings.Contains(line, "app") {
			t.Fatalf("write debug line leaked more than the operation name: %s", line)
		}
	}
	if !sawStart || !sawFinish {
		t.Fatalf("stderr missing write started/write finished: %s", stderr.String())
	}
}

// TestMonitorCreateLogProjectCLIInputJSONRejectsUnknownField checks that
// --cli-input-json strictness (input.go's exact-field-name check) still
// holds for create-log-project's Input: an unrecognized key is refused with
// exit code 2 before any request, the same as every other operation.
func TestMonitorCreateLogProjectCLIInputJSONRejectsUnknownField(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/billing-api/v2/log/quota-class": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "monitor", "create-log-project",
		"--cli-input-json", `{"Name":"app","Bogus":1}`,
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

// TestMonitorCreateLogProjectMaxPriceNaNExitsWithZeroRequests checks the
// design's MaxPrice guard: a NaN --max-price is refused with InvalidUsage
// before any request, including the quote's own class-list read, mirroring
// monitor's own CreateLogProject MaxPrice guard test at the SDK level.
func TestMonitorCreateLogProjectMaxPriceNaNExitsWithZeroRequests(t *testing.T) {
	refuse := func(_ http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
	}
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/billing-api/v2/log/quota-class":          refuse,
		"/billing-api/v2/log/prices/created-price": refuse,
		"/billing-api/v2/log/quotas":               refuse,
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "monitor", "create-log-project",
		"--name", "app", "--max-price", "NaN",
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected an invalid-input refusal for a NaN --max-price")
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

// TestMonitorCreateLogProjectRefusesExistingNameWithZeroOrderRequests checks
// the design's duplicate-name guard: a --name matching an existing project
// is refused with InvalidUsage before any pricing or order request,
// mirroring monitor's own CreateLogProject duplicate-name guard test at the
// SDK level.
func TestMonitorCreateLogProjectRefusesExistingNameWithZeroOrderRequests(t *testing.T) {
	refuseOrder := func(_ http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
	}
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/log-api/v1/projects":                     jsonHandler(http.StatusOK, logProjectListEntryJSON("proj-1", "app", "ACTIVE")),
		"/billing-api/v2/log/quota-class":          refuseOrder,
		"/billing-api/v2/log/prices/created-price": refuseOrder,
		"/billing-api/v2/log/quotas":               refuseOrder,
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "monitor", "create-log-project", "--name", "app", "--no-wait"})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected an invalid-input refusal for a name that already exists")
	}
	if got := classify(err).Code; got != "InvalidUsage" {
		t.Fatalf("Code = %q, want InvalidUsage (stderr=%s)", got, stderr.String())
	}
	if got := exitCode(err); got != 2 {
		t.Fatalf("exitCode = %d, want 2 (stderr=%s)", got, stderr.String())
	}
	if n := fixture.requestCount(); n != 1 {
		t.Fatalf("requestCount = %d, want 1 (the name check only, no pricing or order)", n)
	}
}

// TestGoldenMonitorCreateLogProject checks create-log-project's exact output
// shape, {"LogProject": {...}, "OrderID": "..."}, with LogProject the same
// shape get-log-project uses.
func TestGoldenMonitorCreateLogProject(t *testing.T) {
	v := &monitor.CreateLogProjectOutput{LogProject: exampleLogProject("proj-1", "app"), OrderID: "order-1"}
	checkGolden(t, "monitor-create-log-project.json.golden", "json", "", v)
	checkGolden(t, "monitor-create-log-project.table.golden", "table", "", v)
	checkGolden(t, "monitor-create-log-project.text.golden", "text", "", v)
}

// TestGoldenMonitorDeleteLogProject checks delete-log-project's exact output
// shape: an empty object, since DeleteLogProjectOutput carries no field.
func TestGoldenMonitorDeleteLogProject(t *testing.T) {
	v := &monitor.DeleteLogProjectOutput{}
	checkGolden(t, "monitor-delete-log-project.json.golden", "json", "", v)
	checkGolden(t, "monitor-delete-log-project.table.golden", "table", "", v)
	checkGolden(t, "monitor-delete-log-project.text.golden", "text", "", v)
}

// TestMonitorDeleteLogProjectWithoutYesExitsWithZeroRequests mirrors
// TestMonitorDeleteChannelWithoutYesExitsWithZeroRequests for
// delete-log-project: Write and Destructive, so it needs --yes.
func TestMonitorDeleteLogProjectWithoutYesExitsWithZeroRequests(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/log-api/v1/projects/proj-1": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
		"/billing-api/v1/log/quotas/proj-1": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "monitor", "delete-log-project", "--log-project-id", "proj-1"})
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

// TestMonitorDeleteLogProjectWithYesSendsDelete checks that --yes together
// with --no-wait lets delete-log-project send exactly one DELETE to the
// project's trash-moving path and succeed, with no baseline read and no
// purge.
func TestMonitorDeleteLogProjectWithYesSendsDelete(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/billing-api/v1/log/quotas/proj-1": jsonHandler(http.StatusNoContent, ""),
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--yes", "monitor", "delete-log-project",
		"--log-project-id", "proj-1", "--no-wait",
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("delete-log-project: %v (stderr=%s)", err, stderr.String())
	}
	if got, ok := fixture.methodFor("/billing-api/v1/log/quotas/proj-1"); !ok || got != http.MethodDelete {
		t.Fatalf("delete-log-project method = %q, ok=%v, want DELETE", got, ok)
	}
	if n := fixture.requestCount(); n != 1 {
		t.Fatalf("requestCount = %d, want 1 (no baseline read, no purge)", n)
	}
}

// TestMonitorDeleteLogProjectPurgeSendsDeleteThenPurge checks that --purge
// maps to Purge: the trash-moving DELETE is sent before the purge DELETE,
// never the reverse, matching monitor.TestDeleteLogProjectSendsDeleteThenPurge
// at the SDK level.
func TestMonitorDeleteLogProjectPurgeSendsDeleteThenPurge(t *testing.T) {
	var order []string
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/billing-api/v1/log/quotas/proj-1": func(w http.ResponseWriter, r *http.Request) {
			order = append(order, r.URL.Path)
			w.WriteHeader(http.StatusNoContent)
		},
		"/billing-api/v1/trash/log/quotas/proj-1": func(w http.ResponseWriter, r *http.Request) {
			order = append(order, r.URL.Path)
			w.WriteHeader(http.StatusNoContent)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--yes", "monitor", "delete-log-project",
		"--log-project-id", "proj-1", "--purge", "--no-wait",
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("delete-log-project: %v (stderr=%s)", err, stderr.String())
	}
	want := []string{"/billing-api/v1/log/quotas/proj-1", "/billing-api/v1/trash/log/quotas/proj-1"}
	if len(order) != len(want) || order[0] != want[0] || order[1] != want[1] {
		t.Fatalf("request order = %v, want %v", order, want)
	}
}

// TestMonitorDeleteLogProjectNotSettledOnCanceledContext drives a real
// delete-log-project call whose baseline read and delete both succeed, and
// whose post-write wait is interrupted by canceling the command's own
// context: the confirm read answers once more with the same Status and
// BillingStatus the baseline saw (the wait's own settle condition not yet
// met), then cancels the context, so the next poll step's sleep fails on
// that canceled context rather than the real 60-second bound.
func TestMonitorDeleteLogProjectNotSettledOnCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var getCalls atomic.Int64
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/log-api/v1/projects/proj-1": func(w http.ResponseWriter, r *http.Request) {
			n := getCalls.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"proj-1","status":"ACTIVE","billingStatus":"PAID"}`))
			if n == 2 {
				cancel()
			}
		},
		"/billing-api/v1/log/quotas/proj-1": jsonHandler(http.StatusNoContent, ""),
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "--yes", "monitor", "delete-log-project", "--log-project-id", "proj-1"})
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
	if getCalls.Load() != 2 {
		t.Fatalf("get calls = %d, want 2 (baseline, one unsettled poll)", getCalls.Load())
	}
}

// TestMonitorDeleteLogProjectReadOnlyRefusedWithZeroRequests mirrors
// TestMonitorCreateLogProjectReadOnlyRefusedWithZeroRequests for
// delete-log-project.
func TestMonitorDeleteLogProjectReadOnlyRefusedWithZeroRequests(t *testing.T) {
	home := withCleanEnv(t)
	writeConfigFile(t, home, "[profile agent]\nregion = hcm-3\nread_only = true\n")
	writeCredentialsFile(t, home, "[agent]\nusername = u\npassword = p\nroot_email = e@example.com\n")

	refuse := func(_ http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
	}
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/log-api/v1/projects/proj-1":       refuse,
		"/billing-api/v1/log/quotas/proj-1": refuse,
	})
	opts := newFakeServer(t, fixture.mux)
	withTestOptions(t, append(opts, vngcloud.WithStaticToken("test-token"))...)

	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	root := newRootCmd(strings.NewReader(""), stdout, stderr)
	root.SetArgs([]string{"--profile", "agent", "monitor", "delete-log-project", "--log-project-id", "proj-1", "--yes"})
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

// TestMonitorDeleteLogProjectCLIInputJSONRejectsUnknownField mirrors
// TestMonitorCreateLogProjectCLIInputJSONRejectsUnknownField for
// delete-log-project's Input.
func TestMonitorDeleteLogProjectCLIInputJSONRejectsUnknownField(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/log-api/v1/projects/proj-1": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--yes", "monitor", "delete-log-project",
		"--cli-input-json", `{"LogProjectID":"proj-1","Bogus":1}`,
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
