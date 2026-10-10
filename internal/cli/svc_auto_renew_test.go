package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/storage"
)

type cliAutoRenewServer struct {
	enabled      bool
	months       int
	puts, prices int
	rejection    bool
}

func (s *cliAutoRenewServer) routes(t *testing.T) map[string]func(http.ResponseWriter, *http.Request) {
	t.Helper()
	routes := storageProjectPricingRoutes(t, "region-hcm", 30, &s.prices)
	routes["/internal/v1/projects"] = func(w http.ResponseWriter, r *http.Request) {
		body := fmt.Sprintf(`{"success":true,"datas":[{"projectId":"proj-s1","projectName":"backups","regionId":"region-hcm","regionName":"HCM04","status":1,"totalQuota":30,"projectType":1,"projectTypeName":"Gold","purchaseTypeId":4,"purchaseTypeName":"Pay monthly","enableAutoRenew":%t,"autoRenewPeriod":%d}]}`, s.enabled, s.months)
		jsonHandler(200, body)(w, r)
	}
	routes["/gateway/api/v1/resources"] = func(w http.ResponseWriter, r *http.Request) {
		renew, period := "MANUAL", "null"
		if s.enabled {
			renew, period = "AUTO-RENEW", strconv.Itoa(s.months)
		}
		body := fmt.Sprintf(`{"code":200,"data":{"data":[{"artifactId":"proj-s1","artifactName":"backups","artifactType":"object-storage","product":"vstorage","renewType":%q,"billingType":"PREPAID","channel":0,"isRenewing":false,"endBillingTime":1890000000000,"renewPeriod":%s,"cost":987654321}]}}`, renew, period)
		jsonHandler(200, body)(w, r)
	}
	routes["/gateway/api/v1/home/user-info"] = jsonHandler(200, `{"code":200,"data":{"accountId":12345}}`)
	routes["/gateway/api/v1/resources/autoRenew"] = func(w http.ResponseWriter, r *http.Request) {
		s.puts++
		if r.Method != http.MethodPut || r.Header.Get("portal-user-id") != "12345" {
			t.Error("wrong PUT method or account header")
		}
		var body []struct {
			Info struct {
				Enabled bool `json:"isEnable"`
				Period  int  `json:"period"`
			} `json:"autoRenewInfo"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body) != 1 {
			t.Fatal("invalid PUT body")
		}
		if s.rejection {
			jsonHandler(200, `{"code":200,"data":{"successAll":false,"errorAutoRenewResources":[]}}`)(w, r)
			return
		}
		s.enabled, s.months = body[0].Info.Enabled, body[0].Info.Period/43200
		if !s.enabled {
			s.months = 0
		}
		jsonHandler(200, `{"code":200,"data":{"successAll":true,"errorAutoRenewResources":[]}}`)(w, r)
	}
	return routes
}

func autoRenewArgs(command string, flags ...string) []string {
	return append([]string{"storage", command, "--project-id", "proj-s1"}, flags...)
}

func TestCLIAutoRenewEnabledPresence(t *testing.T) {
	for _, flags := range [][]string{nil, {"--cli-input-json", `{}`}, {"--cli-input-json", `{"Enabled":null}`}, {"--enabled", "false"}} {
		r := runAutoRenew(t, map[string]func(http.ResponseWriter, *http.Request){}, autoRenewArgs("put-project-auto-renew", flags...)...)
		if r.err == nil || exitCode(r.err) != 2 || r.fixture.requestCount() != 0 {
			t.Fatalf("flags %q: error %v requests %d", flags, r.err, r.fixture.requestCount())
		}
		if len(flags) == 0 || flags[0] == "--cli-input-json" {
			if !strings.Contains(r.err.Error(), "--enabled") {
				t.Fatalf("missing presence error: %v", r.err)
			}
		}
	}
	for _, enabled := range []bool{false, true} {
		for _, mode := range []string{"flag", "json", "override"} {
			t.Run(fmt.Sprintf("%s/%t", mode, enabled), func(t *testing.T) {
				s := &cliAutoRenewServer{enabled: !enabled}
				if s.enabled {
					s.months = 1
				}
				flags := []string{"--enabled=" + strconv.FormatBool(enabled)}
				if mode == "json" {
					flags = []string{"--cli-input-json", fmt.Sprintf(`{"Enabled":%t}`, enabled)}
				}
				if mode == "override" {
					flags = append(flags, "--cli-input-json", fmt.Sprintf(`{"Enabled":%t}`, !enabled))
				}
				if enabled {
					flags = append(flags, "--max-price", "90000", "--period-months", "3")
				}
				r := runAutoRenew(t, s.routes(t), autoRenewArgs("put-project-auto-renew", flags...)...)
				if r.err != nil || s.puts != 1 || s.enabled != enabled || !strings.Contains(r.stdout, `"Changed": true`) {
					t.Fatalf("error %v puts %d output %s", r.err, s.puts, r.stdout)
				}
				if !enabled && s.prices != 0 {
					t.Fatal("disable requested a quote")
				}
				if enabled && (s.months != 3 || s.prices != 1 || !strings.Contains(r.stdout, `"NextCharge": 90000`)) {
					t.Fatal("period or quote differs")
				}
			})
		}
	}
}

func TestCLIAutoRenewReadOnlyProfile(t *testing.T) {
	home := withCleanEnv(t)
	writeConfigFile(t, home, "[profile agent]\nregion = hcm-3\nread_only = true\n")
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){})
	withTestOptions(t, append(newFakeServer(t, fixture.mux), vngcloud.WithStaticToken("test-token"))...)
	root := newRootCmd(strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	root.SetArgs(append([]string{"--profile", "agent"}, autoRenewArgs("put-project-auto-renew", "--enabled=false")...))
	err := root.ExecuteContext(context.Background())
	if err == nil || exitCode(err) != 2 || classify(err).Code != "ReadOnly" || fixture.requestCount() != 0 {
		t.Fatalf("error %v requests %d", err, fixture.requestCount())
	}
}

func TestCLIAutoRenewPriceCapAndRejection(t *testing.T) {
	for _, tc := range []struct {
		name, code string
		cap        string
		reject     bool
	}{
		{"below", "PriceAboveMax", "89999", false}, {"missing cap", "PriceAboveMax", "0", false}, {"rejected", "AutoRenewRejected", "90000", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &cliAutoRenewServer{rejection: tc.reject}
			r := runAutoRenew(t, s.routes(t), autoRenewArgs("put-project-auto-renew", "--enabled=true", "--period-months", "3", "--max-price", tc.cap)...)
			wantPuts := 0
			if tc.reject {
				wantPuts = 1
			}
			if r.err == nil || exitCode(r.err) != 1 || classify(r.err).Code != tc.code || s.puts != wantPuts {
				t.Fatalf("error %v class %s puts %d", r.err, classify(r.err).Code, s.puts)
			}
		})
	}
}

func TestCLIAutoRenewReadFormats(t *testing.T) {
	for _, format := range []string{"json", "text", "table"} {
		for _, unavailable := range []bool{false, true} {
			s := &cliAutoRenewServer{}
			routes := s.routes(t)
			if unavailable {
				routes["/billing-api/v2/price"] = jsonHandler(200, `{"success":true,"data":{"optimumPrice":0}}`)
			}
			r := runAutoRenew(t, routes, autoRenewArgs("get-project-auto-renew", "--output", format)...)
			if r.err != nil || s.puts != 0 {
				t.Fatalf("error %v", r.err)
			}
			if format == "table" {
				for _, phrase := range []string{"EndTime", "Z", "MANUAL", "PeriodMonths", "QuotePeriodMonths", "QuotedRenewalCharge", "NextCharge", "none scheduled", "PriceStatus"} {
					if !strings.Contains(r.stdout, phrase) {
						t.Errorf("table missing %s: %s", phrase, r.stdout)
					}
				}
				if unavailable && !strings.Contains(r.stdout, "unavailable") {
					t.Errorf("missing unavailable: %s", r.stdout)
				}
			}
			if format == "json" && !strings.Contains(r.stdout, `"NextCharge": null`) {
				t.Errorf("JSON lost null: %s", r.stdout)
			}
		}
	}
	s := &cliAutoRenewServer{enabled: true, months: 3}
	r := runAutoRenew(t, s.routes(t), autoRenewArgs("get-project-auto-renew", "--period-months", "6", "--output", "json", "--query", "State.{Preview:QuotedRenewalCharge,Next:NextCharge}")...)
	if r.err != nil || !strings.Contains(r.stdout, `"Preview": 180000`) || !strings.Contains(r.stdout, `"Next": 90000`) {
		t.Fatalf("query: %v %s", r.err, r.stdout)
	}
}

func TestCLIBillingResourcesTable(t *testing.T) {
	for _, format := range []string{"json", "table", "text"} {
		s := &cliAutoRenewServer{}
		r := runAutoRenew(t, s.routes(t), "billing", "list-resources", "--output", format)
		if r.err != nil || s.prices != 0 {
			t.Fatalf("error %v prices %d", r.err, s.prices)
		}
		if format == "table" && (strings.Contains(r.stdout, "Cost") || strings.Contains(r.stdout, "987654321") || !strings.Contains(r.stdout, "EndBillingTime")) {
			t.Fatalf("billing table: %s", r.stdout)
		}
		if format == "json" && !strings.Contains(r.stdout, `"Cost": 987654321`) {
			t.Fatalf("JSON dropped cost: %s", r.stdout)
		}
	}
}

func TestCLIAutoRenewHelp(t *testing.T) {
	for _, command := range []string{"get-project-auto-renew", "put-project-auto-renew"} {
		r := runAutoRenew(t, map[string]func(http.ResponseWriter, *http.Request){}, "storage", command, "--help")
		if r.err != nil || r.fixture.requestCount() != 0 {
			t.Fatalf("help: %v", r.err)
		}
		phrases := []string{"estimate", "VAT"}
		if command == "put-project-auto-renew" {
			phrases = append(phrases, "repeated future charges until disabled", "--enabled=true", "--enabled=false", "--max-price", "monthly quote times months", "required for enable and period changes", "Disable needs no price")
		}
		for _, phrase := range phrases {
			if !strings.Contains(r.stdout, phrase) {
				t.Errorf("%s missing %q", command, phrase)
			}
		}
	}
}

func TestCLIAutoRenewExitClasses(t *testing.T) {
	for _, tc := range []struct {
		err  error
		code int
	}{
		{storage.ErrNotSettled, 1}, {fmt.Errorf("%w: %w", storage.ErrNotSettled, vngcloud.ErrNotFound), 1},
		{vngcloud.ErrInvalidInput, 2}, {vngcloud.ErrNotFound, 4}, {vngcloud.ErrPriceAboveMax, 1}, {vngcloud.ErrUnpriced, 1},
		{&vngcloud.APIError{Code: "AutoRenewRejected", StatusCode: 200}, 1}, {&vngcloud.APIError{Code: "InvalidResponse", StatusCode: 200}, 1},
	} {
		if got := exitCode(tc.err); got != tc.code {
			t.Errorf("%v exit %d want %d", tc.err, got, tc.code)
		}
	}
}

func TestCLIAutoRenewOperationErrors(t *testing.T) {
	for _, tc := range []struct {
		name, path, body, code string
		exit                   int
		puts                   int
	}{
		{"not found", "/internal/v1/projects", `{"success":true,"datas":[]}`, "NotFound", 4, 0},
		{"unpriced", "/billing-api/v2/price", `{"success":true,"data":{"optimumPrice":0}}`, "Unpriced", 1, 0},
		{"invalid response", "/gateway/api/v1/resources", `{}`, "InvalidResponse", 1, 0},
		{"not settled", "/gateway/api/v1/resources/autoRenew", `{}`, "NotSettled", 1, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &cliAutoRenewServer{}
			routes := s.routes(t)
			if tc.puts == 1 {
				original := routes[tc.path]
				routes[tc.path] = func(w http.ResponseWriter, r *http.Request) {
					recorder := httptest.NewRecorder()
					original(recorder, r)
					jsonHandler(200, tc.body)(w, r)
				}
			} else {
				routes[tc.path] = jsonHandler(200, tc.body)
			}
			r := runAutoRenew(t, routes, autoRenewArgs("put-project-auto-renew", "--enabled=true", "--max-price", "30000")...)
			if r.err == nil || exitCode(r.err) != tc.exit || classify(r.err).Code != tc.code || s.puts != tc.puts {
				t.Fatalf("error %v class %s puts %d", r.err, classify(r.err).Code, s.puts)
			}
			if tc.code == "NotSettled" && !strings.Contains(r.stdout, `"Changed": false`) {
				t.Fatalf("lost last state: %s", r.stdout)
			}
		})
	}
}

func TestCLIAutoRenewPeriodChangeCap(t *testing.T) {
	s := &cliAutoRenewServer{enabled: true, months: 1}
	r := runAutoRenew(t, s.routes(t), autoRenewArgs("put-project-auto-renew", "--enabled=true", "--period-months", "3", "--max-price", "89999")...)
	if r.err == nil || classify(r.err).Code != "PriceAboveMax" || exitCode(r.err) != 1 || s.puts != 0 {
		t.Fatalf("period cap: %v puts %d", r.err, s.puts)
	}
}

func TestCLIAutoRenewDisableTable(t *testing.T) {
	s := &cliAutoRenewServer{enabled: true, months: 1}
	r := runAutoRenew(t, s.routes(t), autoRenewArgs("put-project-auto-renew", "--enabled=false", "--output", "table")...)
	if r.err != nil || s.prices != 0 || !strings.Contains(r.stdout, "none scheduled") || !strings.Contains(r.stdout, "unavailable") || !strings.Contains(r.stdout, "Changed") {
		t.Fatalf("disable table: %v %s", r.err, r.stdout)
	}
}

func TestCLIAutoRenewEnabledUnavailableTable(t *testing.T) {
	s := &cliAutoRenewServer{enabled: true, months: 3}
	routes := s.routes(t)
	routes["/billing-api/v2/price"] = jsonHandler(200, `{"success":true,"data":{"optimumPrice":0}}`)
	r := runAutoRenew(t, routes, autoRenewArgs("get-project-auto-renew", "--output", "table")...)
	if r.err != nil || strings.Count(r.stdout, "unavailable") != 2 || strings.Contains(r.stdout, "none scheduled") {
		t.Fatalf("enabled unavailable: %v %s", r.err, r.stdout)
	}
}

func TestCLIAutoRenewProfileTable(t *testing.T) {
	home := withCleanEnv(t)
	writeConfigFile(t, home, "[default]\nregion = hcm-3\noutput = table\n")
	s := &cliAutoRenewServer{}
	routes := s.routes(t)
	routes["/internal/v1/regions"] = jsonHandler(200, storageRegionsBody)
	fixture := newSvcFixture(routes)
	withTestOptions(t, append(newFakeServer(t, fixture.mux), vngcloud.WithStaticToken("test-token"))...)
	var stdout bytes.Buffer
	root := newRootCmd(strings.NewReader(""), &stdout, &bytes.Buffer{})
	root.SetArgs(autoRenewArgs("get-project-auto-renew"))
	err := root.ExecuteContext(context.Background())
	if err != nil || !strings.Contains(stdout.String(), "none scheduled") {
		t.Fatalf("profile table: %v %s", err, stdout.String())
	}
}

func runAutoRenew(t *testing.T, routes map[string]func(http.ResponseWriter, *http.Request), args ...string) storageRun {
	t.Helper()
	routes["/internal/v1/regions"] = jsonHandler(http.StatusOK, storageRegionsBody)
	fixture := newSvcFixture(routes)
	root, stdout, stderr := newSvcRoot(t, fixture)
	testOptions = append(testOptions, core.WithStorageTestClock(func() time.Time { return time.Date(2019, time.January, 1, 0, 0, 0, 0, time.UTC) }))
	root.SetArgs(append([]string{"--region", "hcm-3"}, args...))
	err := root.ExecuteContext(context.Background())
	return storageRun{fixture: fixture, stdout: stdout.String(), stderr: stderr.String(), err: err}
}

func TestCLIAutoRenewFixedClock(t *testing.T) {
	s := &cliAutoRenewServer{}
	routes := s.routes(t)
	original := routes["/gateway/api/v1/resources"]
	routes["/gateway/api/v1/resources"] = func(w http.ResponseWriter, r *http.Request) {
		recorder := httptest.NewRecorder()
		original(recorder, r)
		// The term is past on a real clock and future on the injected clock.
		jsonHandler(200, strings.ReplaceAll(recorder.Body.String(), "1890000000000", "1577836800000"))(w, r)
	}
	r := runAutoRenew(t, routes, autoRenewArgs("put-project-auto-renew", "--enabled=true", "--max-price", "30000")...)
	if r.err != nil || s.puts != 1 {
		t.Fatal("enable used the calendar instead of the injected clock")
	}
}
