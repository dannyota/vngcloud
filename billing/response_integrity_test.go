package billing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/endpoints"
	"danny.vn/vngcloud/internal/testutil"
	"danny.vn/vngcloud/internal/transport"
)

func TestBillingReadRequiresData(t *testing.T) {
	for _, tc := range billingReadTestCases() {
		for _, body := range []string{"", `null`, `{}`, `{"code":200}`, `{"code":200,"data":null}`, `{"code":200,"data":{}}`, `{"data":[]}`, `{"code":"200","data":[]}`} {
			t.Run(tc.name+"/"+body, func(t *testing.T) {
				out, err := tc.call(context.Background(), billingReadBodyClient(t, tc, 200, body))
				assertBillingInvalid(t, out, err)
			})
		}
	}
}

func assertBillingInvalid(t *testing.T, out any, err error) {
	t.Helper()
	var api *core.APIError
	if out != nil || !errors.As(err, &api) || api.StatusCode != 200 || api.Code != "InvalidResponse" || api.Retryable {
		t.Fatal("invalid response returned output or lost observed status and InvalidResponse")
	}
}

func TestBillingRejectsAmbiguousFields(t *testing.T) {
	for _, tc := range billingReadTestCases() {
		fixture := testutil.FixtureBody(t, "../testdata/billing/"+tc.fixture+".json")
		for _, key := range []string{"code", "Code", "data", "Data"} {
			t.Run(tc.name+"/"+key, func(t *testing.T) {
				body := `{"` + key + `":null,` + strings.TrimSpace(fixture)[1:]
				out, err := tc.call(context.Background(), billingReadBodyClient(t, tc, 200, body))
				assertBillingInvalid(t, out, err)
			})
		}
	}
	for _, body := range []string{
		`{"code":200,"data":[{"uuid":"a","UUID":"b","limitAmount":1}]}`,
		`{"code":200,"data":[{"limitAmount":1,"LimitAmount":2}]}`,
		`{"code":200,"data":[null]}`,
	} {
		c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) }))
		out, err := billingTestMethod((*Client).ListBudgets, &ListBudgetsInput{})(context.Background(), c)
		assertBillingInvalid(t, out, err)
	}
}

func TestBillingRejectsCaseVariantFields(t *testing.T) {
	for _, tc := range billingReadTestCases() {
		fixture := testutil.FixtureBody(t, "../testdata/billing/"+tc.fixture+".json")
		for _, field := range []string{"code", "data", "limitAmount", "cash", "currentCost", "totalCost", "value", "points", "y"} {
			if !strings.Contains(fixture, `"`+field+`"`) {
				continue
			}
			t.Run(tc.name+"/"+field, func(t *testing.T) {
				body := strings.Replace(fixture, `"`+field+`"`, `"`+strings.ToUpper(field)+`"`, 1)
				out, err := tc.call(context.Background(), billingReadBodyClient(t, tc, 200, body))
				assertBillingInvalid(t, out, err)
			})
		}
	}
}

func TestBudgetEmptyUUIDInvalidResponse(t *testing.T) {
	for _, tc := range billingWriteTestCases() {
		if tc.fixture == "" {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`{"code":200,"data":{"uuid":""}}`))
			}))
			out, err := tc.call(context.Background(), c)
			assertBillingInvalid(t, out, err)
			if !strings.Contains(err.Error(), "unconfirmed") {
				t.Fatal("invalid create identity lost write uncertainty")
			}
		})
	}
}

func TestBillingSuccessfulCredentialEcho(t *testing.T) {
	const initial = "synthetic-initial-billing-token"
	const refreshed = "synthetic-refreshed-billing-token"
	for _, tc := range billingReadTestCases() {
		fixture := testutil.FixtureBody(t, "../testdata/billing/"+tc.fixture+".json")
		for _, credential := range []string{initial, refreshed} {
			for _, escaped := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/%t", tc.name, credential, escaped), func(t *testing.T) {
					var calls atomic.Int32
					var captures atomic.Int32
					body := `{"echo":"` + credential + `",` + strings.TrimSpace(fixture)[1:]
					if tc.name == "ListBudgets" {
						body = fmt.Sprintf(`{"code":200,"data":[{"uuid":"budget-1","name":%q,"limitAmount":1}]}`, credential)
					}
					if tc.name == "GetBudget" {
						body = fmt.Sprintf(`{"code":200,"data":{"uuid":"budget-1","name":%q,"limitAmount":1}}`, credential)
					}
					if escaped {
						body = strings.ReplaceAll(body, "synthetic", `\u0073ynthetic`)
					}
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
						if calls.Add(1) == 1 {
							w.WriteHeader(401)
							return
						}
						_, _ = w.Write([]byte(body))
					}))
					defer server.Close()
					tcTransport := transport.New(transport.Config{
						HTTPClient: server.Client(), TokenSource: &credentialTestTokens{values: []string{initial, refreshed}},
						Capture: func(transport.Capture) { captures.Add(1) },
					})
					c := New(core.NewTestConfig("hcm-3", "", endpoints.Set{Billing: server.URL + "/"}, tcTransport))
					out, err := tc.call(context.Background(), c)
					assertBillingInvalid(t, out, err)
					if calls.Load() != 2 || captures.Load() != 0 {
						t.Fatal("credential response reached capture or refresh changed")
					}
					encoded, marshalErr := json.Marshal(err)
					if marshalErr != nil || strings.Contains(string(encoded), credential) || strings.Contains(fmt.Sprintf("%+v", err), credential) {
						t.Fatal("credential reached error output")
					}
				})
			}
		}
	}
}

func TestBillingDecodedCredentialEcho(t *testing.T) {
	for _, tc := range []struct {
		name, data string
		call       func(context.Context, *Client) (any, error)
	}{
		{"balances", `{"cash":"1e6","poc":null,"cashAvailable":null,"cashHolding":null,"pocHolding":null}`, billingTestMethod((*Client).GetBalances, &GetBalancesInput{})},
		{"period", `{"actualCost":1e6,"forecastedCost":0}`, billingTestMethod((*Client).GetCurrentPeriodCost, &GetCurrentPeriodCostInput{})},
		{"budget", `[{"uuid":"budget-1","limitAmount":1,"actualCost":1e6}]`, billingTestMethod((*Client).ListBudgets, &ListBudgetsInput{})},
		{"grouped series", `{"summary":{"currentCost":0},"series":[{"key":"vserver","points":[{"x":"bucket","y":1e6}]}]}`, billingTestMethod((*Client).GetCostOverview, &GetCostOverviewInput{StartDate: "2026-11-01", EndDate: "2026-11-09"})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`{"code":200,"data":` + tc.data + `}`))
			}))
			defer server.Close()
			transportClient := transport.New(transport.Config{HTTPClient: server.Client(), TokenSource: &credentialTestTokens{values: []string{"e+06"}}})
			c := New(core.NewTestConfig("hcm-3", "", endpoints.Set{Billing: server.URL + "/"}, transportClient))
			out, err := tc.call(context.Background(), c)
			assertBillingInvalid(t, out, err)
		})
	}
}

func TestBillingExplicitEmptyLists(t *testing.T) {
	for _, tc := range billingReadTestCases() {
		if tc.name != "ListBudgets" && tc.name != "ListBudgetThresholds" && tc.name != "ListBudgetAlerts" {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			out, err := tc.call(context.Background(), billingReadBodyClient(t, tc, 200, `{"code":200,"data":[]}`))
			if err != nil || out == nil {
				t.Fatal("explicit empty list rejected")
			}
		})
	}
}

func TestBillingRequiresFinancialValues(t *testing.T) {
	for _, tc := range []struct {
		name, data string
		call       func(context.Context, *Client) (any, error)
	}{
		{"overview summary", `{"summary":{}}`, billingTestMethod((*Client).GetCostOverview, &GetCostOverviewInput{StartDate: "2026-11-01", EndDate: "2026-11-09"})},
		{"overview null cost", `{"summary":{"currentCost":null}}`, billingTestMethod((*Client).GetCostOverview, &GetCostOverviewInput{StartDate: "2026-11-01", EndDate: "2026-11-09"})},
		{"resource cost", `{"items":[{"resourceId":"server-1"}],"pagination":{"page":0,"size":200,"total":1,"totalPages":1},"summary":{"totalAll":0}}`, billingTestMethod((*Client).ListCostResources, &ListCostResourcesInput{StartDate: "2026-11-01", EndDate: "2026-11-09"})},
		{"null series row", `{"summary":{"currentCost":0},"series":[null]}`, billingTestMethod((*Client).GetCostOverview, &GetCostOverviewInput{StartDate: "2026-11-01", EndDate: "2026-11-09"})},
		{"grouped point cost", `{"summary":{"currentCost":0},"series":[{"key":"vserver","points":[{"x":"bucket"}]}]}`, billingTestMethod((*Client).GetCostOverview, &GetCostOverviewInput{StartDate: "2026-11-01", EndDate: "2026-11-09"})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`{"code":200,"data":` + tc.data + `}`))
			}))
			out, err := tc.call(context.Background(), c)
			assertBillingInvalid(t, out, err)
		})
	}
}
