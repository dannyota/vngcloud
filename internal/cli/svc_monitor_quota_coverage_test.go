package cli

import (
	"bytes"
	"context"
	"errors"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/monitor"
)

func coverageFixture(t *testing.T, denied ...bool) *svcFixture {
	t.Helper()
	bodies := map[string]string{
		"/billing-api/v2/synthetic/quota-class":                                               "ListSyntheticQuotaClasses",
		"/billing-api/v2/synthetic/quota-class/e574cf4a-460b-48d8-a794-80367289a6c8/packages": "ListSyntheticClassPackages",
		"/billing-api/v1/synthetic/packages":                                                  "ListSyntheticPackages",
		"/billing-api/v1/settings":                                                            "GetQuotaSettings",
		"/billing-api/v1/synthetic/quota":                                                     "GetSyntheticQuota",
		"/billing-api/v2/synthetic/quota-syn-1/quota-detail":                                  "GetSyntheticQuotaDetailNullable",
		"/billing-api/v1/email/quota":                                                         "GetNotificationQuotaEmail",
		"/billing-api/v1/email/packages":                                                      "ListNotificationPackagesEmail",
		"/billing-api/v1/sms/quota":                                                           "GetNotificationQuotaSMS",
		"/billing-api/v1/sms/packages":                                                        "ListNotificationPackagesSMS",
	}
	handlers := map[string]func(http.ResponseWriter, *http.Request){}
	for path, name := range bodies {
		b, err := os.ReadFile("../../testdata/monitor/" + name + ".json")
		if err != nil {
			t.Fatal(err)
		}
		handlers[path] = jsonHandler(200, string(b))
	}
	// Disabled-class package arrays are synthetic mocks.
	for _, id := range []string{"70b57f46-edaf-4f6f-9293-acf816a5b81d", "d8c1d829-205b-4ce4-9c1c-32756f748d25"} {
		handlers["/billing-api/v2/synthetic/quota-class/"+id+"/packages"] = jsonHandler(200, "[]")
	}
	if len(denied) > 0 && denied[0] {
		for _, kind := range []string{"email", "sms"} {
			handlers["/billing-api/v1/"+kind+"/quota"] = jsonHandler(403, `{"message":"peer-secret"}`)
		}
	}
	return newSvcFixture(handlers)
}

func TestMonitorCoverageCommandsReadOnly(t *testing.T) {
	for _, command := range []string{"list-synthetic-packages", "quota-status"} {
		for _, source := range []string{"flag", "environment", "profile"} {
			t.Run(command+"/"+source, func(t *testing.T) {
				fixture := coverageFixture(t)
				root, stdout, stderr := newSvcRoot(t, fixture)
				args := []string{"--region", "hcm-3", "monitor", command}
				switch source {
				case "flag":
					args = append(args, "--read-only")
				case "environment":
					t.Setenv("VNGCLOUD_READ_ONLY", "true")
				case "profile":
					writeConfigFile(t, os.Getenv("HOME"), "[profile agent]\nregion = hcm-3\nread_only = true\n")
					args = append(args, "--profile", "agent")
				}
				if command == "quota-status" {
					args = append(args, "--kind", "sYnThEtIc")
				}
				if err := execCmd(t, root, args); err != nil {
					t.Fatalf("err=%v stderr=%s", err, stderr)
				}
				if !strings.Contains(stdout.String(), "Basic") {
					t.Fatalf("output=%s", stdout)
				}
				want := 6
				if command == "quota-status" {
					want = 2
				}
				if fixture.requestCount() != want {
					t.Fatalf("requests=%d want=%d", fixture.requestCount(), want)
				}
			})
		}
	}
}

func TestMonitorCoveragePartialOutput(t *testing.T) {
	for _, format := range []string{"json", "table", "text"} {
		t.Run(format, func(t *testing.T) {
			fixture := coverageFixture(t, true)
			root, stdout, stderr := newSvcRoot(t, fixture)
			err := execCmd(t, root, []string{"--region", "hcm-3", "monitor", "quota-status", "--output", format, "--query", "Items[0].Kind"})
			if !errors.Is(err, monitor.ErrQuotaStatusIncomplete) || exitCode(err) != 1 || classify(err).Code != "QuotaStatusIncomplete" {
				t.Fatalf("err=%v class=%+v", err, classify(err))
			}
			if !strings.Contains(stdout.String(), "Synthetic") || !strings.Contains(stdout.String(), "unavailable") || !strings.Contains(stdout.String(), "Forbidden") {
				t.Fatalf("partial output=%s", stdout)
			}
			if strings.Contains(stdout.String()+stderr.String()+err.Error(), "peer-secret") {
				t.Fatal("peer error leaked")
			}
		})
	}
}

func TestMonitorCoverageInvalidKind(t *testing.T) {
	fixture := coverageFixture(t)
	root, _, _ := newSvcRoot(t, fixture)
	err := execCmd(t, root, []string{"--region", "hcm-3", "monitor", "quota-status", "--kind", "Slack"})
	if exitCode(err) != 2 || fixture.requestCount() != 0 {
		t.Fatalf("err=%v requests=%d", err, fixture.requestCount())
	}
}

func TestGoldenMonitorCoverage(t *testing.T) {
	// Golden status uses a fixed snapshot rather than the process clock.
	now := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	price := 0.0
	no := false
	yes := true
	seconds := int64(-1)
	end := now.Add(-time.Second)
	status := &monitor.GetQuotaStatusOutput{AsOf: now, Complete: true, Items: []monitor.QuotaStatusRow{
		{Kind: "Synthetic", ClassName: "Basic", Status: "IN-TRASH", Price: &price, EndTime: &end, TimeLeftSeconds: &seconds, AutoRenew: &no, RenewWindowOpen: &no, NextSafeCommand: "monitor recover-synthetic-quota"},
		{Kind: "Email", Status: "none", NextSafeCommand: "monitor get-notification-quota"},
		{Kind: "SMS", PackageName: "sms5", Status: "ACTIVE", AutoRenew: &yes, NextSafeCommand: "monitor get-notification-quota"},
	}}
	catalog := &monitor.ListSyntheticPackagesOutput{Items: []monitor.SyntheticQuotaClass{
		{ID: "class-1", Name: "Basic", Status: "ACTIVE", Description: "Free class", Priority: 1, Packages: []monitor.SyntheticClassPackage{{ID: "class-package-1", Name: "unit-package-free", TierName: "Unit Tier Free", TierID: 1001, API: 1, Location: 1, Price: &price}}, Options: []monitor.SyntheticClassOption{{PackageID: "class-package-1", Location: 1, MinAPI: 10, MaxAPI: 10, Step: 1, MonthPeriods: []int{1}, AutoRenewEligible: &no}}},
		{ID: "class-2", Name: "Pro", Status: "DISABLED", Description: "Coming soon.", Priority: 2, Options: []monitor.SyntheticClassOption{}},
	}, Packages: []monitor.SyntheticPackage{{ID: "v1-package-1", Name: "free10", TierID: 24, API: 10, Location: 1, Price: &price, MonthPeriods: []int{1}}}, MonthPeriods: []int{1, 3}, MaxMonthPeriodForFreeQuota: 1, PaymentMethod: "PRE-PAID"}
	for _, tc := range []struct {
		name  string
		out   any
		query string
	}{{"list-synthetic-packages", catalog, "Packages[0].Price"}, {"quota-status", status, "Items[0].Price"}} {
		for _, format := range []string{"json", "table", "text"} {
			var stdout bytes.Buffer
			if err := renderMonitorQuota(&stdout, format, "", tc.out); err != nil {
				t.Fatal(err)
			}
			name := "monitor-" + tc.name + "." + format + ".golden"
			path := goldenPath(name)
			if *updateGolden && tc.name == "list-synthetic-packages" {
				if err := os.WriteFile(path, stdout.Bytes(), 0o644); err != nil {
					t.Fatal(err)
				}
			} else {
				want, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(want, stdout.Bytes()) {
					t.Fatalf("%s got:\n%s\nwant:\n%s", name, &stdout, want)
				}
			}
			stdout.Reset()
			if err := renderMonitorQuota(&stdout, format, tc.query, tc.out); err != nil {
				t.Fatal(err)
			}
			queryPath := goldenPath("monitor-" + tc.name + "-query." + format + ".golden")
			if *updateGolden && tc.name == "list-synthetic-packages" {
				if err := os.WriteFile(queryPath, stdout.Bytes(), 0o644); err != nil {
					t.Fatal(err)
				}
			} else {
				want, err := os.ReadFile(queryPath)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(want, stdout.Bytes()) {
					t.Fatalf("query got=%s want=%s", &stdout, want)
				}
			}
			if !strings.Contains(stdout.String(), "0") || strings.Contains(stdout.String(), "unknown") {
				t.Fatalf("query=%q", &stdout)
			}
		}
	}
}

func TestQuotaStatusIncompletePrecedence(t *testing.T) {
	for _, cause := range []error{vngcloud.ErrAuth, vngcloud.ErrNotFound, context.Canceled, context.DeadlineExceeded} {
		err := errors.Join(monitor.ErrQuotaStatusIncomplete, cause)
		if exitCode(err) != 1 || classify(err).Code != "QuotaStatusIncomplete" {
			t.Fatalf("err=%v exit=%d class=%+v", err, exitCode(err), classify(err))
		}
	}
}

func TestMonitorCoverageMainPartialResult(t *testing.T) {
	withCleanEnv(t)
	fixture := coverageFixture(t, true)
	options := newFakeServer(t, fixture.mux)
	withTestOptions(t, append(options, vngcloud.WithStaticToken("synthetic-token"))...)
	var stdout, stderr bytes.Buffer
	code := Main(context.Background(), []string{"--region", "hcm-3", "monitor", "quota-status", "--query", "Items[0].Kind"}, strings.NewReader(""), &stdout, &stderr)
	if code != 1 || !strings.Contains(stdout.String(), `"Complete": false`) || !strings.Contains(stdout.String(), `"Forbidden"`) || !strings.Contains(stderr.String(), `"code":"QuotaStatusIncomplete"`) || !strings.Contains(stderr.String(), `"operation":"monitor.GetQuotaStatus"`) || strings.Contains(stderr.String(), "peer-secret") {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, &stdout, &stderr)
	}
}

func TestMonitorQuotaStatusHelp(t *testing.T) {
	fixture := newSvcFixture(nil)
	root, stdout, _ := newSvcRoot(t, fixture)
	if err := execCmd(t, root, []string{"monitor", "quota-status", "--help"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "fresh read") || fixture.requestCount() != 0 {
		t.Fatalf("help=%s requests=%d", stdout, fixture.requestCount())
	}
}

func TestMonitorEnsureSyntheticQuotaDocs(t *testing.T) {
	dir := t.TempDir()
	if err := runGenDocs(dir); err != nil {
		t.Fatal(err)
	}
	page, err := os.ReadFile(filepath.Join(dir, "CLI-Monitor.md"))
	if err != nil {
		t.Fatal(err)
	}
	_, section, ok := strings.Cut(string(page), "\n## ensure-synthetic-quota\n")
	if !ok {
		t.Fatal("generated monitor reference is missing ensure-synthetic-quota")
	}
	section, _, _ = strings.Cut(section, "\n## ")
	for _, row := range []string{
		"| `--expected-quota-id` | `string` |  |",
		"| `--max-price` | `float64` |  |",
		"| `--max-days-left` | `int` |  |",
		"| `--no-wait` | `bool` |  |",
	} {
		if count := strings.Count(section, row); count != 1 {
			t.Errorf("generated flag row %q appears %d times, want once", row, count)
		}
	}
	if !strings.Contains(section, "--max-days-left defaults to 7") {
		t.Error("generated ensure reference is missing the seven-day default")
	}
}

func TestQuotaStatusHumanTimeAndBlankError(t *testing.T) {
	for _, tc := range []struct {
		seconds int64
		want    string
	}{
		{2589120, "29d 23h 12m"},
		{-2589120, "expired 29d 23h 12m ago"},
		{0, "expires now"},
		{1, "0d 0h 0m"},
		{-1, "expired 0d 0h 0m ago"},
		{251578310400, "2911786d 0h 0m"},
		{math.MaxInt64, "106751991167300d 15h 30m"},
		{math.MinInt64, "expired 106751991167300d 15h 30m ago"},
	} {
		row := monitor.QuotaStatusRow{Kind: "Synthetic", Status: "ACTIVE", TimeLeftSeconds: &tc.seconds}
		var out bytes.Buffer
		if err := renderQuotaStatus(&out, "table", "", &monitor.GetQuotaStatusOutput{Items: []monitor.QuotaStatusRow{row}}); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.String(), tc.want) {
			t.Fatalf("output=%s want=%s", &out, tc.want)
		}
		// Give every other nullable cell a value so unknown can only mean Error.
		row.Price = new(float64)
		row.AutoRenew = new(bool)
		row.RenewWindowOpen = new(bool)
		row.ClassName = "Basic"
		row.PackageName = "free"
		row.StartTime = new(time.Time)
		row.EndTime = new(time.Time)
		out.Reset()
		if err := renderQuotaStatus(&out, "table", "", &monitor.GetQuotaStatusOutput{Items: []monitor.QuotaStatusRow{row}}); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out.String(), "unknown") {
			t.Fatalf("error must be blank: %s", &out)
		}
	}
}
