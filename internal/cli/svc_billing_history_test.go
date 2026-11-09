package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"danny.vn/vngcloud"
)

// Synthetic bodies test presentation and safety, not upstream field evidence.
func billingHistoryBody(payments bool) string {
	row := `{"transactionID":1,"transactionType":5,"transactionValue":12.5,"cumulativeValue":100,"creditStatus":false,"sourceReference":"synthetic-reference","userServiceReference":"synthetic-service","invoiceId":null,"description":"synthetic-description","createTimeStamp":"2024-11-11T18:00:00+00:00"}`
	if payments {
		row = `{"paymentID":1,"transactionID":"synthetic-reference","payGate":"use_balance","paymentStatus":"1","paymentAmount":12.5,"paymentType":8084,"description":"synthetic-description","createTimeStamp":"2024-11-11T18:00:00+00:00"}`
	}
	return `{"code":200,"data":{"content":[` + row + `],"pageable":{"number":1,"size":50,"totalPages":1,"totalElements":1}}}`
}

func TestBillingHistoryRenderFailureIsBuffered(t *testing.T) {
	for _, format := range []string{"json", "table", "text"} {
		var stdout bytes.Buffer
		err := renderBillingHistory(&stdout, format, "", map[string]any{"Description": "synthetic-account", "Amount": math.NaN()})
		if err == nil || stdout.Len() != 0 || classify(err).Code != "QueryFailed" || strings.Contains(err.Error(), "synthetic-account") {
			t.Fatal("formatting failure exposed a partial report")
		}
	}
}

func TestBillingHistoryOutput(t *testing.T) {
	for _, name := range []string{"transactions", "payments"} {
		for _, format := range []string{"json", "table", "text"} {
			t.Run(name+"/"+format, func(t *testing.T) {
				fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){"/gateway/api/v1/" + name: jsonHandler(200, billingHistoryBody(name == "payments"))})
				root, stdout, stderr := newHistorySvcRoot(t, fixture)
				root.SetArgs([]string{"--region", "hcm-3", "--read-only", "--debug", "billing", "list-" + name, "--start-date", "2024-11-01", "--end-date", "2024-11-30", "--output", format})
				if err := root.ExecuteContext(context.Background()); err != nil {
					t.Fatalf("history command failed: %v", err)
				}
				if fixture.requestCount() != 1 {
					t.Fatal("history command sent extra requests")
				}
				if strings.Contains(stderr.String(), "synthetic-") {
					t.Fatal("account data reached debug output")
				}
				if format == "table" {
					for _, hidden := range []string{"Description", "SourceReference", "UserServiceReference", "TransactionID", "ID ", "synthetic-"} {
						if strings.Contains(stdout.String(), hidden) {
							t.Fatal("default history table exposed extra fields")
						}
					}
					for _, column := range []string{"CreatedAt", "TypeCode", "Amount"} {
						if !strings.Contains(stdout.String(), column) {
							t.Fatal("default history column missing")
						}
					}
				} else if !strings.Contains(stdout.String(), "synthetic-description") {
					t.Fatal("requested full output lost account data")
				}
				path := goldenPath("billing-list-" + name + "." + format + ".golden")
				if *updateGolden {
					if err := os.WriteFile(path, stdout.Bytes(), 0o644); err != nil {
						t.Fatal(err)
					}
				}
				want, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if stdout.String() != string(want) {
					t.Fatal("history output differs from golden")
				}
			})
		}
	}
}

func TestBillingHistoryQueriesAndControls(t *testing.T) {
	for _, name := range []string{"transactions", "payments"} {
		for _, format := range []string{"json", "table", "text"} {
			for _, query := range []string{"Items[0].Description", "PageSize", "Items[].Amount"} {
				t.Run(name+format+query, func(t *testing.T) {
					body := strings.Replace(billingHistoryBody(name == "payments"), "synthetic-description", `synthetic-description\u001b\r\n\t\u009b\u202e`, 1)
					fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){"/gateway/api/v1/" + name: jsonHandler(200, body)})
					root, stdout, _ := newHistorySvcRoot(t, fixture)
					root.SetArgs([]string{"--region", "hcm-3", "billing", "list-" + name, "--start-date", "2024-11-01", "--end-date", "2024-11-30", "--query", query, "--output", format})
					if err := root.ExecuteContext(context.Background()); err != nil {
						t.Fatal("exported query root failed")
					}
					for _, control := range []string{"\x1b", "\r", "\u009b", "\u202e"} {
						if strings.Contains(stdout.String(), control) {
							t.Fatal("terminal control escaped unencoded")
						}
					}
					if query == "Items[0].Description" && !strings.Contains(stdout.String(), "synthetic-description") {
						t.Fatal("explicit query lost description")
					}
					if query == "PageSize" && !strings.Contains(stdout.String(), "50") {
						t.Fatal("metadata query root changed")
					}
				})
			}
		}
	}
}

func TestBillingHistoryErrors(t *testing.T) {
	for _, name := range []string{"transactions", "payments"} {
		for _, tc := range []struct {
			status            int
			query, body, code string
			exit              int
		}{
			{404, "", `{"code":"synthetic-marker","message":"synthetic-account"}`, "NotFound", 4},
			{401, "", `{"message":"synthetic-account"}`, "Unauthorized", 3},
			{403, "", `{"message":"synthetic-account"}`, "Forbidden", 1},
			{400, "", `{"message":"synthetic-account"}`, "BadRequest", 1},
			{429, "", `{"message":"synthetic-account"}`, "Throttled", 1},
			{500, "", `{"message":"synthetic-account"}`, "ServerError", 1},
			{201, "", `{}`, "InvalidResponse", 1},
			{200, "", `{"code":400,"data":[{"type":"Max_value","field":"pageSize","errorMsg":"synthetic-account"}]}`, "400", 1},
			{200, "abs(Items[0].Description)", "", "QueryFailed", 1},
			{200, "[", "", "InvalidUsage", 2},
		} {
			t.Run(fmt.Sprint(name, tc.status, tc.query), func(t *testing.T) {
				body := tc.body
				if body == "" {
					body = billingHistoryBody(name == "payments")
				}
				fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){"/gateway/api/v1/" + name: jsonHandler(tc.status, body)})
				root, stdout, stderr := newHistorySvcRoot(t, fixture)
				args := []string{"--region", "hcm-3", "billing", "list-" + name, "--start-date", "2024-11-01", "--end-date", "2024-11-30", "--debug"}
				if tc.query != "" {
					args = append(args, "--query", tc.query)
				}
				root.SetArgs(args)
				err := root.ExecuteContext(context.Background())
				if err == nil || exitCode(err) != tc.exit || classify(err).Code != tc.code {
					t.Fatalf("wrong history error: code=%s exit=%d", classify(err).Code, exitCode(err))
				}
				if stdout.Len() != 0 || strings.Contains(err.Error()+stderr.String(), "synthetic-") {
					t.Fatal("history error exposed account data or partial output")
				}
				if tc.code == "QueryFailed" {
					var qe *queryFailedError
					if !errors.As(err, &qe) || qe.err != nil || errors.Unwrap(qe) != nil {
						t.Fatal("query failure retained peer-bearing cause")
					}
				}
			})
		}
	}
}

func TestBillingHistoryHelpAndInput(t *testing.T) {
	for _, name := range []string{"transactions", "payments"} {
		fixture := newSvcFixture(nil)
		root, stdout, _ := newHistorySvcRoot(t, fixture)
		root.SetArgs([]string{"--region", "hcm-3", "billing", "list-" + name, "--help"})
		if err := root.ExecuteContext(context.Background()); err != nil {
			t.Fatal(err)
		}
		for _, text := range []string{"--start-date", "--end-date", "--page", "--size", "--all", "account data", "Items", "UTC+07:00"} {
			if !strings.Contains(stdout.String(), text) {
				t.Fatalf("history help missing %s", text)
			}
		}
		cmd, _, err := root.Find([]string{"--region", "hcm-3", "billing", "list-" + name})
		if err != nil {
			t.Fatal(err)
		}
		if cmd.Flags().Lookup("since") != nil {
			t.Fatal("history added an unsupported flag")
		}
		if fixture.requestCount() != 0 {
			t.Fatal("help sent requests")
		}
		fixture = newSvcFixture(nil)
		root, stdout, _ = newHistorySvcRoot(t, fixture)
		root.SetArgs([]string{"--region", "hcm-3", "billing", "list-" + name})
		if err := root.ExecuteContext(context.Background()); err == nil || exitCode(err) != 2 || stdout.Len() != 0 || fixture.requestCount() != 0 {
			t.Fatal("missing required start did not fail locally")
		}
		fixture = newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){"/gateway/api/v1/" + name: jsonHandler(200, billingHistoryBody(name == "payments"))})
		root, _, _ = newHistorySvcRoot(t, fixture)
		home := withCleanEnv(t)
		writeConfigFile(t, home, "[profile agent]\nregion = hcm-3\nread_only = true\n")
		root.SetArgs([]string{"--profile", "agent", "billing", "list-" + name, "--cli-input-json", `{"StartDate":"2024-11-01","EndDate":"2024-11-30","All":true,"Size":50}`})
		if err := root.ExecuteContext(context.Background()); err != nil {
			t.Fatal("read-only profile or input JSON failed")
		}
	}
}

func newHistorySvcRoot(t *testing.T, fixture *svcFixture) (*cobra.Command, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	root, stdout, stderr := newSvcRoot(t, fixture)
	testOptions = append(testOptions, vngcloud.WithRetry(0, 0))
	return root, stdout, stderr
}

func TestBillingHistoryCredentialCanary(t *testing.T) {
	for _, name := range []string{"transactions", "payments"} {
		for _, format := range []string{"json", "table", "text"} {
			for _, query := range []string{"", "Items[0].Description"} {
				body := strings.ReplaceAll(billingHistoryBody(name == "payments"), "synthetic-description", "test-token synthetic-description")
				fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){"/gateway/api/v1/" + name: jsonHandler(200, body)})
				root, stdout, stderr := newHistorySvcRoot(t, fixture)
				args := []string{"--region", "hcm-3", "--debug", "billing", "list-" + name, "--start-date", "2024-11-01", "--end-date", "2024-11-30", "--output", format}
				if query != "" {
					args = append(args, "--query", query)
				}
				root.SetArgs(args)
				if err := root.ExecuteContext(context.Background()); err != nil {
					t.Fatal("credential-safe history output failed")
				}
				if strings.Contains(stdout.String()+stderr.String(), "test-token") || strings.Contains(stderr.String(), "synthetic-description") {
					t.Fatal("credential or account data escaped through the CLI")
				}
			}
		}
	}
}
