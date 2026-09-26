package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/spf13/cobra"

	"danny.vn/vngcloud"
)

// assertKindMatchesMethodName checks the CLI design's rule that Service
// tables follow: an operation named Get* or List* is a Read, and every
// other operation is a Write (billing's two deletes also carry Destructive,
// checked separately by the read-only and --yes tests).
func assertKindMatchesMethodName[C any](t *testing.T, serviceName string, ops []Op[C]) {
	t.Helper()
	for _, op := range ops {
		isGetOrList := strings.HasPrefix(op.methodName, "Get") || strings.HasPrefix(op.methodName, "List")
		wantWrite := !isGetOrList
		gotWrite := op.kind == kindWrite
		if gotWrite != wantWrite {
			t.Errorf("%s %s (method %s): kind is Write=%v, want Write=%v",
				serviceName, op.name, op.methodName, gotWrite, wantWrite)
		}
	}
}

func TestEveryNonGetListOpIsAWrite(t *testing.T) {
	assertKindMatchesMethodName(t, "billing", billingOps)
	assertKindMatchesMethodName(t, "pricing", pricingOps)
	assertKindMatchesMethodName(t, "compute", computeOps)
	assertKindMatchesMethodName(t, "network", networkOps)
	assertKindMatchesMethodName(t, "dns", dnsOps)
	assertKindMatchesMethodName(t, "cdn", cdnOps)
	assertKindMatchesMethodName(t, "monitor", monitorOps)
}

func opNames[C any](ops []Op[C]) []string {
	names := make([]string, len(ops))
	for i, op := range ops {
		names[i] = op.name
	}
	return names
}

func TestServiceHelpListsEveryOp(t *testing.T) {
	withCleanEnv(t)
	tests := []struct {
		service string
		names   []string
	}{
		{"billing", opNames(billingOps)},
		{"pricing", opNames(pricingOps)},
		{"compute", opNames(computeOps)},
		{"network", opNames(networkOps)},
		{"dns", opNames(dnsOps)},
		{"cdn", opNames(cdnOps)},
		{"monitor", opNames(monitorOps)},
	}
	for _, tt := range tests {
		t.Run(tt.service, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			root := newRootCmd(strings.NewReader(""), &stdout, &stderr)
			root.SetArgs([]string{tt.service, "--help"})
			if err := root.ExecuteContext(context.Background()); err != nil {
				t.Fatalf("--help: %v (stderr=%s)", err, stderr.String())
			}
			help := stdout.String()
			for _, name := range tt.names {
				if !strings.Contains(help, name) {
					t.Errorf("%s --help is missing operation %q:\n%s", tt.service, name, help)
				}
			}
		})
	}
}

// reqRecord is one request the fixture server observed.
type reqRecord struct {
	method string
	path   string
	query  string
}

// svcFixture records every request its handler receives and answers exact
// paths with canned fixture bodies, so a handful of representative
// operations can be driven through the real Service-built commands without a
// live API.
type svcFixture struct {
	mu       sync.Mutex
	requests []reqRecord
	mux      *http.ServeMux
}

func newSvcFixture(routes map[string]func(w http.ResponseWriter, r *http.Request)) *svcFixture {
	f := &svcFixture{mux: http.NewServeMux()}
	for path, handler := range routes {
		h := handler
		f.mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			f.mu.Lock()
			f.requests = append(f.requests, reqRecord{method: r.Method, path: r.URL.Path, query: r.URL.RawQuery})
			f.mu.Unlock()
			h(w, r)
		})
	}
	return f
}

func (f *svcFixture) requestCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.requests)
}

func (f *svcFixture) methodFor(path string) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.requests {
		if r.path == path {
			return r.method, true
		}
	}
	return "", false
}

func (f *svcFixture) queryFor(path string) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.requests {
		if r.path == path {
			return r.query, true
		}
	}
	return "", false
}

func jsonHandler(status int, body string) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}
}

// newSvcRoot builds a real production root command wired at fixture through
// the same test hook every other test in this package uses. Each test still
// passes --region and --project-id explicitly, exactly like a real command
// line, since there is no default here.
func newSvcRoot(t *testing.T, fixture *svcFixture) (root *cobra.Command, stdout, stderr *bytes.Buffer) {
	t.Helper()
	withCleanEnv(t)
	opts := newFakeServer(t, fixture.mux)
	withTestOptions(t, append(opts, vngcloud.WithStaticToken("test-token"))...)
	stdout, stderr = &bytes.Buffer{}, &bytes.Buffer{}
	root = newRootCmd(strings.NewReader(""), stdout, stderr)
	return root, stdout, stderr
}

func TestOneReadPerServicePrintsGoldenJSON(t *testing.T) {
	billingBody := `{"code":200,"message":"ok","data":[{"uuid":"b-1","id":1,"name":"monthly","periodType":"MONTHLY","type":"ACTUAL","limitAmount":100,"currency":"VND","status":"ACTIVE","periodKey":"2026-01","periodStart":"2026-01-01","periodEnd":"2026-01-31","startDate":"2026-01-01","createdAt":"2026-01-01T00:00:00Z","updatedAt":"2026-01-01T00:00:00Z","actualCost":null,"forecastedCost":null,"actualPercentage":null,"forecastedPercentage":null,"thresholdPercentage":null,"alarm":false,"thresholdCount":0,"alarmThresholdCount":0}]}`
	pricingBody := `{"optimumPrice":100,"originalPrice":120,"discountPrice":20,"discountPercent":16.7,"propertiesPrice":[]}`
	computeBody := `{"listData":[{"uuid":"s-1","name":"server-1","status":"ACTIVE"}],"page":1,"pageSize":10000,"totalPage":1,"totalItem":1}`
	networkBody := `{"listData":[{"id":"vpc-1","displayName":"my-vpc","status":"ACTIVE"}],"page":1,"pageSize":10000,"totalPage":1,"totalItem":1}`
	dnsBody := `{"listData":[{"hostedZoneId":"z-1","domainName":"example.com","status":"ACTIVE"}],"page":1,"pageSize":10000,"totalPage":1,"totalItem":1}`

	tests := []struct {
		name       string
		path       string
		args       []string
		body       string
		wantMethod string
		wantSubstr string
	}{
		{"billing", "/gateway/api/v1/budgets", []string{"billing", "list-budgets"}, billingBody, http.MethodGet, `"Name": "monthly"`},
		{"pricing", "/v1/price", []string{"pricing", "get-quote", "--resource-type", "snapshot"}, pricingBody, http.MethodPost, `"OptimumPrice": 100`},
		{"compute", "/v2/proj-1/servers", []string{"--project-id", "proj-1", "compute", "list-servers"}, computeBody, http.MethodGet, `"Name": "server-1"`},
		{"network", "/v2/proj-1/networks", []string{"--project-id", "proj-1", "network", "list-vpcs"}, networkBody, http.MethodGet, `"Name": "my-vpc"`},
		{"dns", "/v1/dns/hosted-zone", []string{"dns", "list-hosted-zones"}, dnsBody, http.MethodGet, `"DomainName": "example.com"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
				tt.path: jsonHandler(http.StatusOK, tt.body),
			})
			root, stdout, stderr := newSvcRoot(t, fixture)
			root.SetArgs(append([]string{"--region", "hcm-3"}, tt.args...))
			if err := root.ExecuteContext(context.Background()); err != nil {
				t.Fatalf("execute: %v (stderr=%s)", err, stderr.String())
			}
			if got, ok := fixture.methodFor(tt.path); !ok || got != tt.wantMethod {
				t.Fatalf("method for %s = %q, ok=%v, want %q", tt.path, got, ok, tt.wantMethod)
			}
			if !strings.Contains(stdout.String(), tt.wantSubstr) {
				t.Fatalf("stdout = %s, want it to contain %q", stdout.String(), tt.wantSubstr)
			}
		})
	}
}

func TestBillingDeleteBudgetWithoutYesExitsWithZeroRequests(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/gateway/api/v1/budgets/b-1": jsonHandler(http.StatusOK, `{}`),
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "billing", "delete-budget", "--budget-uuid", "b-1"})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatalf("expected an error without --yes")
	}
	if exitCode(err) != 2 {
		t.Fatalf("exitCode = %d, want 2 (stderr=%s)", exitCode(err), stderr.String())
	}
	if n := fixture.requestCount(); n != 0 {
		t.Fatalf("requestCount = %d, want 0", n)
	}
}

func TestBillingUpdateBudgetSendsOnlyTheChangedField(t *testing.T) {
	var body []byte
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/gateway/api/v1/budgets/b-1": func(w http.ResponseWriter, r *http.Request) {
			defer func() { _ = r.Body.Close() }()
			body, _ = io.ReadAll(r.Body)
			w.WriteHeader(http.StatusOK)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--yes",
		"billing", "update-budget", "--budget-uuid", "b-1", "--status", "PAUSED",
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("execute: %v (stderr=%s)", err, stderr.String())
	}

	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("body is not valid JSON: %v (%s)", err, body)
	}
	if len(decoded) != 1 {
		t.Fatalf("body has %d keys, want exactly 1: %s", len(decoded), body)
	}
	if decoded["status"] != "PAUSED" {
		t.Fatalf("body = %s, want only status=PAUSED", body)
	}
}

func TestEveryServiceCommandHasAShortDescription(t *testing.T) {
	withCleanEnv(t)
	root := newRootCmd(strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	for _, name := range []string{"billing", "pricing", "compute", "network", "dns", "cdn", "monitor"} {
		cmd, _, err := root.Find([]string{name})
		if err != nil {
			t.Fatalf("Find(%s): %v", name, err)
		}
		if cmd.Short == "" {
			t.Errorf("%s command has no Short description", name)
		}
	}
}

func TestBillingWriteRefusedByProfileReadOnlyWithZeroRequests(t *testing.T) {
	home := withCleanEnv(t)
	writeConfigFile(t, home, "[profile agent]\nregion = hcm-3\nread_only = true\n")
	writeCredentialsFile(t, home, "[agent]\nusername = u\npassword = p\nroot_email = e@example.com\n")

	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/gateway/api/v1/budgets/b-1": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	opts := newFakeServer(t, fixture.mux)
	withTestOptions(t, append(opts, vngcloud.WithStaticToken("test-token"))...)

	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	root := newRootCmd(strings.NewReader(""), stdout, stderr)
	root.SetArgs([]string{
		"--profile", "agent", "--yes",
		"billing", "update-budget", "--budget-uuid", "b-1", "--status", "PAUSED",
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatalf("expected a read-only refusal")
	}
	if classify(err).Code != "ReadOnly" {
		t.Fatalf("Code = %q, want ReadOnly (stderr=%s)", classify(err).Code, stderr.String())
	}
	if n := fixture.requestCount(); n != 0 {
		t.Fatalf("requestCount = %d, want 0", n)
	}
}

func TestReadOnlyFlagFalseDoesNotOverrideProfile(t *testing.T) {
	home := withCleanEnv(t)
	writeConfigFile(t, home, "[profile agent]\nregion = hcm-3\nread_only = true\n")
	writeCredentialsFile(t, home, "[agent]\nusername = u\npassword = p\nroot_email = e@example.com\n")

	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/gateway/api/v1/budgets/b-1": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	opts := newFakeServer(t, fixture.mux)
	withTestOptions(t, append(opts, vngcloud.WithStaticToken("test-token"))...)

	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	root := newRootCmd(strings.NewReader(""), stdout, stderr)
	root.SetArgs([]string{
		"--profile", "agent", "--read-only=false", "--yes",
		"billing", "update-budget", "--budget-uuid", "b-1", "--status", "PAUSED",
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatalf("expected a read-only refusal")
	}
	if classify(err).Code != "ReadOnly" {
		t.Fatalf("Code = %q, want ReadOnly (stderr=%s)", classify(err).Code, stderr.String())
	}
	if n := fixture.requestCount(); n != 0 {
		t.Fatalf("requestCount = %d, want 0", n)
	}
}

func TestBillingWriteDebugLogsStartAndFinishWithOnlyOperationName(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/gateway/api/v1/budgets/b-1": func(w http.ResponseWriter, r *http.Request) {
			_ = r.Body.Close()
			w.WriteHeader(http.StatusOK)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--yes", "--debug",
		"billing", "update-budget", "--budget-uuid", "b-1", "--status", "PAUSED",
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
		if !strings.Contains(line, `operation="billing update-budget"`) {
			t.Fatalf("write debug line missing the operation name: %s", line)
		}
		if strings.Contains(line, "PAUSED") || strings.Contains(line, "budget-uuid") || strings.Contains(line, "b-1") {
			t.Fatalf("write debug line leaked more than the operation name: %s", line)
		}
	}
	if !sawStart || !sawFinish {
		t.Fatalf("stderr missing write started/write finished: %s", stderr.String())
	}
}

func TestBillingGetCostOverviewSearchAndQuery(t *testing.T) {
	overviewBody := `{"code":200,"data":{"summary":{"currentCost":100,"lastPeriodCost":90,"changePercent":11.1,"forecastCost":110,"activeCount":2,"breakdown":[]},"series":[{"date":"2026-01-01","cost":10}],"interval":"daily","startDate":"2026-01-01","endDate":"2026-01-31","groupBy":"product"}}`
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/gateway/api/v2/cost-explorer/overview": jsonHandler(http.StatusOK, overviewBody),
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3",
		"billing", "get-cost-overview",
		"--start-date", "2026-01-01", "--end-date", "2026-01-31",
		"--search", "x",
		"--query", "Summary",
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("execute: %v (stderr=%s)", err, stderr.String())
	}

	q, ok := fixture.queryFor("/gateway/api/v2/cost-explorer/overview")
	if !ok {
		t.Fatalf("no request observed")
	}
	if !strings.Contains(q, "q=x") {
		t.Fatalf("query string = %q, want it to contain q=x", q)
	}

	out := stdout.String()
	if !strings.Contains(out, `"CurrentCost": 100`) {
		t.Fatalf("stdout = %s, want the Summary object", out)
	}
	if strings.Contains(out, "Series") {
		t.Fatalf("stdout = %s, want --query Summary to filter out Series", out)
	}
}
