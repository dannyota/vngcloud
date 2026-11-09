package billing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/testutil"
	"danny.vn/vngcloud/internal/transport"
)

const billingReadTestLimit = 8 << 20

type billingReadTestCase struct {
	name    string
	path    string
	fixture string
	call    func(context.Context, *Client) (any, error)
}

func billingTestMethod[I, O any](method func(*Client, context.Context, *I) (*O, error), in *I) func(context.Context, *Client) (any, error) {
	return func(ctx context.Context, c *Client) (any, error) {
		out, err := method(c, ctx, in)
		if out == nil {
			return nil, err
		}
		return out, err
	}
}

func billingReadTestCases() []billingReadTestCase {
	return []billingReadTestCase{
		{
			name: "GetCostOverview", path: "/gateway/api/v2/cost-explorer/overview", fixture: "GetCostOverviewCaptured",
			call: billingTestMethod((*Client).GetCostOverview, &GetCostOverviewInput{StartDate: "2026-11-01", EndDate: "2026-11-09"}),
		},
		{
			name: "ListCostResources", path: "/gateway/api/v2/cost-explorer/resources", fixture: "ListCostResources",
			call: billingTestMethod((*Client).ListCostResources, &ListCostResourcesInput{StartDate: "2026-09-01", EndDate: "2026-09-02"}),
		},
		{
			name: "GetBalances", path: "/navbar/balances/v1", fixture: "GetBalances",
			call: billingTestMethod((*Client).GetBalances, &GetBalancesInput{}),
		},
		{
			name: "ListBudgets", path: "/gateway/api/v1/budgets", fixture: "ListBudgetsSummary",
			call: billingTestMethod((*Client).ListBudgets, &ListBudgetsInput{}),
		},
		{
			name: "GetBudget", path: "/gateway/api/v1/budgets/budget-1", fixture: "GetBudget",
			call: billingTestMethod((*Client).GetBudget, &GetBudgetInput{BudgetUUID: "budget-1"}),
		},
		{
			name: "GetCurrentPeriodCost", path: "/gateway/api/v1/budgets/cost/overview", fixture: "GetCurrentPeriodCost",
			call: billingTestMethod((*Client).GetCurrentPeriodCost, &GetCurrentPeriodCostInput{}),
		},
		{
			name: "ListBudgetThresholds", path: "/gateway/api/v1/budgets/budget-1/thresholds", fixture: "ListBudgetThresholds",
			call: billingTestMethod((*Client).ListBudgetThresholds, &ListBudgetThresholdsInput{BudgetUUID: "budget-1"}),
		},
		{
			name: "ListBudgetAlerts", path: "/gateway/api/v1/budgets/budget-1/alerts", fixture: "ListBudgetAlerts",
			call: billingTestMethod((*Client).ListBudgetAlerts, &ListBudgetAlertsInput{BudgetUUID: "budget-1"}),
		},
	}
}

func paddedBillingBody(body string, size int) string {
	return body + strings.Repeat(" ", size-len(body))
}

func billingReadBodyClient(t *testing.T, tc billingReadTestCase, status int, body string) *Client {
	t.Helper()
	return newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != tc.path || r.ContentLength != 0 {
			t.Error("billing read changed its method, path, or empty request body")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
}

func TestBillingReadBodyBounds(t *testing.T) {
	for _, tc := range billingReadTestCases() {
		t.Run(tc.name, func(t *testing.T) {
			fixture := testutil.FixtureBody(t, "../testdata/billing/"+tc.fixture+".json")
			want, err := tc.call(context.Background(), billingReadBodyClient(t, tc, 200, fixture))
			if err != nil || want == nil {
				t.Fatal("normal fixture did not decode")
			}
			for _, status := range []int{200, 403} {
				for _, extra := range []int{0, 1} {
					t.Run(fmt.Sprintf("%d/limit+%d", status, extra), func(t *testing.T) {
						body := fixture
						if status == 403 {
							body = `{"code":"PeerCode","message":"Budget not found: synthetic-peer-canary"}`
						}
						body = paddedBillingBody(body, billingReadTestLimit+extra)
						out, err := tc.call(context.Background(), billingReadBodyClient(t, tc, status, body))
						if status == 200 && extra == 0 {
							if err != nil || !reflect.DeepEqual(out, want) {
								t.Fatal("at-limit response changed normal fixture output")
							}
							return
						}
						if out != nil {
							t.Error("failed read returned partial output")
						}
						var api *vngcloud.APIError
						if !errors.As(err, &api) {
							t.Fatal("read did not return APIError")
						}
						if api.Operation != "billing."+tc.name || api.StatusCode != status || api.Retryable {
							t.Error("read lost its operation, observed status, or retry classification")
						}
						if status == 200 {
							assertBillingOverflow(t, err, "billing."+tc.name, status)
							return
						}
						code, message := "PeerCode", "Budget not found: synthetic-peer-canary"
						if extra == 1 {
							code, message = "Forbidden", "Forbidden"
							if errors.Is(err, transport.ErrBodyTooLarge) {
								t.Error("body size replaced HTTP error classification")
							}
						}
						if api.Code != code || api.Message != message || !errors.Is(err, vngcloud.ErrPermission) || errors.Is(err, vngcloud.ErrNotFound) {
							t.Error("HTTP error classification or envelope behavior changed")
						}
					})
				}
			}
		})
	}
}

func assertBillingOverflow(t *testing.T, err error, operation string, status int) {
	t.Helper()
	wrapped := fmt.Errorf("caller: %w", err)
	var api *vngcloud.APIError
	if !errors.As(wrapped, &api) || !errors.Is(wrapped, transport.ErrBodyTooLarge) {
		t.Fatal("overflow lost APIError or body-limit error identity")
	}
	if api.Operation != operation || api.StatusCode != status || api.Code != "InvalidResponse" || api.Retryable {
		t.Error("overflow lost its operation, observed status, or InvalidResponse classification")
	}
	if api.Message != "billing response exceeds size limit; body withheld" {
		t.Error("overflow message was not fixed text")
	}
	for cause := err; cause != nil; cause = errors.Unwrap(cause) {
		if strings.Contains(cause.Error(), "synthetic-peer-canary") {
			t.Error("overflow error chain retained response text")
		}
	}
}

func TestBillingReadInvalidBodies(t *testing.T) {
	for _, tc := range billingReadTestCases() {
		for _, bodyCase := range []struct {
			name string
			body string
		}{
			{"malformed", `!synthetic-peer-canary`},
			{"truncated JSON", `{"code":200,"data":`},
			{"wrong data type", `{"code":200,"data":"synthetic-peer-canary"}`},
		} {
			for _, status := range []int{200, 403} {
				t.Run(fmt.Sprintf("%s/%s/%d", tc.name, bodyCase.name, status), func(t *testing.T) {
					out, err := tc.call(context.Background(), billingReadBodyClient(t, tc, status, bodyCase.body))
					var api *vngcloud.APIError
					if out != nil || !errors.As(err, &api) || errors.Is(err, transport.ErrBodyTooLarge) {
						t.Fatal("invalid body lost APIError or returned partial output")
					}
					if status == 403 {
						wantCode := "Forbidden"
						if bodyCase.name == "wrong data type" {
							wantCode = "200"
						}
						if api.StatusCode != 403 || api.Code != wantCode || !errors.Is(err, vngcloud.ErrPermission) {
							t.Error("invalid HTTP error body lost status classification")
						}
						return
					}
					var syntax *json.SyntaxError
					var mismatch *json.UnmarshalTypeError
					if bodyCase.name == "wrong data type" {
						if !errors.As(err, &mismatch) {
							t.Error("decode failure lost JSON type error identity")
						}
					} else if !errors.As(err, &syntax) {
						t.Error("decode failure lost JSON syntax error identity")
					}
					if api.StatusCode != 0 || api.Code != "" || api.Retryable {
						t.Error("within-limit decode error classification changed")
					}
					for cause := err; cause != nil; cause = errors.Unwrap(cause) {
						if strings.Contains(cause.Error(), "synthetic-peer-canary") {
							t.Error("decode error chain retained response text")
						}
					}
				})
			}
		}
	}
}

func TestBillingReadTruncatedHTTPBody(t *testing.T) {
	for _, tc := range billingReadTestCases() {
		for _, status := range []int{200, 403} {
			t.Run(tc.name+"/"+strconv.Itoa(status), func(t *testing.T) {
				client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					const body = `{"code":200,"data":null}`
					w.Header().Set("Content-Length", strconv.Itoa(len(body)+1))
					w.WriteHeader(status)
					_, _ = io.WriteString(w, body)
				}))
				out, err := tc.call(context.Background(), client)
				var api *vngcloud.APIError
				if out != nil || !errors.As(err, &api) || !errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, transport.ErrBodyTooLarge) {
					t.Fatal("truncated HTTP body lost error identity or returned partial output")
				}
				if api.StatusCode != 0 || api.Code != "" || api.Retryable {
					t.Error("within-limit read failure classification changed")
				}
			})
		}
	}
}

func TestBillingReadCancellation(t *testing.T) {
	for _, tc := range billingReadTestCases() {
		t.Run(tc.name, func(t *testing.T) {
			client := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				t.Error("canceled read sent a request")
			}))
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			out, err := tc.call(ctx, client)
			if out != nil || !errors.Is(err, context.Canceled) {
				t.Error("canceled read lost cancellation identity or returned output")
			}
		})
	}
}

func TestBillingReadRetryWithinLimit(t *testing.T) {
	for _, tc := range billingReadTestCases() {
		t.Run(tc.name, func(t *testing.T) {
			fixture := testutil.FixtureBody(t, "../testdata/billing/"+tc.fixture+".json")
			var calls atomic.Int32
			client := New(testutil.NewRetryConfig(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if calls.Add(1) == 1 {
					w.WriteHeader(http.StatusServiceUnavailable)
					_, _ = io.WriteString(w, paddedBillingBody(`{"code":null}`, billingReadTestLimit))
					return
				}
				_, _ = io.WriteString(w, fixture)
			})))
			out, err := tc.call(context.Background(), client)
			if err != nil || out == nil || calls.Load() != 2 {
				t.Error("at-limit 503 response changed the existing GET retry behavior")
			}
		})
	}
}

func TestBillingReadOversized503DoesNotRetry(t *testing.T) {
	for _, tc := range billingReadTestCases() {
		t.Run(tc.name, func(t *testing.T) {
			fixture := testutil.FixtureBody(t, "../testdata/billing/"+tc.fixture+".json")
			var calls atomic.Int32
			client := New(testutil.NewRetryConfig(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if calls.Add(1) == 1 {
					w.WriteHeader(http.StatusServiceUnavailable)
					_, _ = io.WriteString(w, paddedBillingBody(`{"code":null,"message":"synthetic-peer-canary"}`, billingReadTestLimit+1))
					return
				}
				_, _ = io.WriteString(w, fixture)
			})))
			out, err := tc.call(context.Background(), client)
			var api *vngcloud.APIError
			if calls.Load() != 1 || out != nil || !errors.As(err, &api) {
				t.Fatal("oversized 503 retried, returned output, or lost APIError")
			}
			if api.Operation != "billing."+tc.name || api.StatusCode != 503 || api.Code != "ServerError" || api.Message != "Service Unavailable" || !api.Retryable || errors.Is(err, transport.ErrBodyTooLarge) {
				t.Error("oversized 503 lost status-based classification")
			}
		})
	}
}

func TestBillingReadOversizedRedirect(t *testing.T) {
	body := paddedBillingBody(`{"code":200,"message":"synthetic-peer-canary","data":null}`, billingReadTestLimit+1)
	for _, tc := range billingReadTestCases() {
		for _, status := range []int{300, 301, 302, 307, 308} {
			t.Run(tc.name+"/"+strconv.Itoa(status), func(t *testing.T) {
				out, err := tc.call(context.Background(), billingReadBodyClient(t, tc, status, body))
				var api *vngcloud.APIError
				if out != nil || !errors.Is(err, transport.ErrBodyTooLarge) || errors.As(err, &api) || vngcloud.ErrorCode(err) != "" {
					t.Error("oversized redirect changed the existing body-limit error classification")
				}
			})
		}
	}
}

func TestBillingHelperBodyOverflow(t *testing.T) {
	for _, helper := range []string{"doRaw", "do", "doBalances"} {
		for _, status := range []int{200, 201, 206} {
			t.Run(helper+"/"+strconv.Itoa(status), func(t *testing.T) {
				client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(status)
					_, _ = io.WriteString(w, paddedBillingBody(`{"code":200,"message":"synthetic-peer-canary","data":null}`, 65))
				}))
				req := transport.Request{Operation: "billing.GetBalances", URL: client.route([]string{"test"}, nil), MaxBody: 64, OK: []int{200}}
				var err error
				var gotStatus int
				switch helper {
				case "doRaw":
					var raw json.RawMessage
					raw, gotStatus, err = client.doRaw(context.Background(), req)
					if raw != nil {
						t.Error("overflow returned a partial raw body")
					}
				case "do":
					gotStatus, err = client.do(context.Background(), req, nil)
				case "doBalances":
					err = client.doBalances(context.Background(), req, nil)
				}
				if helper != "doBalances" && gotStatus != status {
					t.Error("helper lost observed status")
				}
				assertBillingOverflow(t, err, req.Operation, status)
			})
		}
	}
}

func TestBillingHelperOversizedHTTPError(t *testing.T) {
	for _, tc := range []struct {
		status    int
		code      string
		cause     error
		retryable bool
	}{
		{400, "BadRequest", nil, false},
		{401, "Unauthorized", vngcloud.ErrAuth, false},
		{403, "Forbidden", vngcloud.ErrPermission, false},
		{404, "NotFound", vngcloud.ErrNotFound, false},
		{429, "Throttled", vngcloud.ErrRateLimited, true},
		{500, "ServerError", nil, false},
		{502, "ServerError", nil, true},
		{503, "ServerError", nil, true},
		{504, "ServerError", nil, true},
	} {
		t.Run(strconv.Itoa(tc.status), func(t *testing.T) {
			client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, paddedBillingBody(`{"code":"PeerCode","message":"synthetic-peer-canary"}`, 65))
			}))
			req := transport.Request{Operation: "billing.GetBudget", URL: client.route([]string{"test"}, nil), MaxBody: 64}
			raw, status, err := client.doRaw(context.Background(), req)
			var api *vngcloud.APIError
			if raw != nil || status != tc.status || !errors.As(err, &api) || errors.Is(err, transport.ErrBodyTooLarge) {
				t.Fatal("oversized HTTP error lost status classification or returned partial raw body")
			}
			if api.Operation != req.Operation || api.StatusCode != tc.status || api.Code != tc.code || api.Message != http.StatusText(tc.status) || api.Retryable != tc.retryable {
				t.Error("oversized HTTP error changed its status-based fields")
			}
			if tc.cause != nil && !errors.Is(err, tc.cause) {
				t.Error("oversized HTTP error lost sentinel identity")
			}
		})
	}
}
