package billing

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/testutil"
)

func newTestClient(t *testing.T, handler http.Handler) *Client {
	t.Helper()
	return New(testutil.NewConfig(t, handler))
}

func TestBillingZeroConfig(t *testing.T) {
	c := New(vngcloud.Config{})
	if _, err := c.ListBudgets(context.Background(), nil); !errors.Is(err, vngcloud.ErrInvalidConfig) {
		t.Fatalf("ListBudgets() err = %v, want ErrInvalidConfig", err)
	}
}

func TestListBudgets(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("method = %s", r.Method)
		}
		if r.URL.Path != "/gateway/api/v1/budgets" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if r.URL.Query().Get("view") != "summary" || r.URL.Query().Get("status") != StatusPaused {
			t.Fatalf("query = %s", r.URL.RawQuery)
		}
		testutil.WriteFixture(t, w, "../testdata/billing/ListBudgets.json")
	}))

	out, err := client.ListBudgets(context.Background(), &ListBudgetsInput{Status: StatusPaused})
	if err != nil {
		t.Fatalf("ListBudgets() error = %v", err)
	}
	// The live capture behind this fixture found the test account with no
	// budgets at all three points it called ListBudgets. An empty list is
	// the real shape of a successful call; TestListBudgetsDecodesSummaryFields
	// below covers a populated item with a synthetic fixture instead.
	if len(out.Items) != 0 {
		t.Fatalf("unexpected budgets: %+v", out.Items)
	}
}

// TestListBudgetsDecodesSummaryFields checks that every field the summary
// view (view=summary) can send decodes correctly. No live capture has shown
// a populated ListBudgets response, so ListBudgetsSummary.json holds
// synthetic values rather than a sanitized live one; GetBudget and
// CreateBudget cover the fields a live capture did confirm.
func TestListBudgetsDecodesSummaryFields(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		testutil.WriteFixture(t, w, "../testdata/billing/ListBudgetsSummary.json")
	}))

	out, err := client.ListBudgets(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListBudgets() error = %v", err)
	}
	if len(out.Items) != 1 {
		t.Fatalf("unexpected budgets: %+v", out.Items)
	}
	b := out.Items[0]
	if b.UUID != "budget-1" || b.ID != 1 || b.Name != "example-budget" {
		t.Fatalf("unexpected budget: %+v", b)
	}
	if b.PeriodType != PeriodMonthly || b.Type != TypeActual || b.Status != StatusPaused {
		t.Fatalf("unexpected budget: %+v", b)
	}
	// limitAmount arrives as a decimal (9999999999.0-shaped); it must still
	// decode into the int64 field.
	if b.LimitAmount != 1000000 {
		t.Fatalf("LimitAmount = %d, want 1000000", b.LimitAmount)
	}
	if b.Currency != "credit" || b.PeriodKey != "2026-09" || b.PeriodStart != "2026-09-01" || b.PeriodEnd != "2026-09-30" {
		t.Fatalf("unexpected budget: %+v", b)
	}
	if b.ActualCost == nil || *b.ActualCost != 0 || b.ForecastedCost == nil || *b.ForecastedCost != 0 {
		t.Fatalf("unexpected costs: %+v", b)
	}
	if b.ActualPercentage == nil || *b.ActualPercentage != 0 || b.ForecastedPercentage == nil || *b.ForecastedPercentage != 0 {
		t.Fatalf("unexpected percentages: %+v", b)
	}
	if b.ThresholdPercentage != nil {
		t.Fatalf("ThresholdPercentage = %v, want nil", *b.ThresholdPercentage)
	}
	if b.Alarm {
		t.Fatalf("Alarm = true, want false")
	}
	if b.ThresholdCount != 0 || b.AlarmThresholdCount != 0 {
		t.Fatalf("unexpected threshold counts: %+v", b)
	}
}

func TestListBudgetsNoStatus(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Has("status") {
			t.Fatalf("unexpected status query: %s", r.URL.RawQuery)
		}
		testutil.WriteFixture(t, w, "../testdata/billing/ListBudgets.json")
	}))

	if _, err := client.ListBudgets(context.Background(), nil); err != nil {
		t.Fatalf("ListBudgets(nil) error = %v", err)
	}
}

func TestGetBudget(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/gateway/api/v1/budgets/budget-1" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		testutil.WriteFixture(t, w, "../testdata/billing/GetBudget.json")
	}))

	out, err := client.GetBudget(context.Background(), &GetBudgetInput{BudgetUUID: "budget-1"})
	if err != nil {
		t.Fatalf("GetBudget() error = %v", err)
	}
	if out.Budget.UUID != "budget-1" || out.Budget.Name != "example-budget" {
		t.Fatalf("unexpected budget: %+v", out.Budget)
	}
	// GetBudget sends limitAmount as a decimal, unlike CreateBudget's plain
	// integer; it must still decode into the int64 field.
	if out.Budget.LimitAmount != 1000000 {
		t.Fatalf("LimitAmount = %d, want 1000000", out.Budget.LimitAmount)
	}
	if out.Budget.ActualCost != nil {
		t.Fatalf("expected omitted ActualCost to stay nil, got %v", *out.Budget.ActualCost)
	}
}

func TestGetCurrentPeriodCost(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/gateway/api/v1/budgets/cost/overview" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		testutil.WriteFixture(t, w, "../testdata/billing/GetCurrentPeriodCost.json")
	}))

	out, err := client.GetCurrentPeriodCost(context.Background(), nil)
	if err != nil {
		t.Fatalf("GetCurrentPeriodCost() error = %v", err)
	}
	if out.PeriodCost.PeriodKey != "2026-09" || out.PeriodCost.ActualCost != 4200000 {
		t.Fatalf("unexpected period cost: %+v", out.PeriodCost)
	}
}

func TestListBudgetThresholds(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/gateway/api/v1/budgets/budget-1/thresholds" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		testutil.WriteFixture(t, w, "../testdata/billing/ListBudgetThresholds.json")
	}))

	out, err := client.ListBudgetThresholds(context.Background(), &ListBudgetThresholdsInput{BudgetUUID: "budget-1"})
	if err != nil {
		t.Fatalf("ListBudgetThresholds() error = %v", err)
	}
	// The fixture captures the threshold after it was disabled and its
	// reminder interval changed, per the live write test's sequence.
	if len(out.Items) != 1 || out.Items[0].UUID != "threshold-1" || out.Items[0].Enabled {
		t.Fatalf("unexpected thresholds: %+v", out.Items)
	}
	if out.Items[0].ReminderIntervalHours != 24 {
		t.Fatalf("ReminderIntervalHours = %d, want 24", out.Items[0].ReminderIntervalHours)
	}
	if out.Items[0].LastAlertAt != "" {
		t.Fatalf("LastAlertAt = %q, want empty for a null value", out.Items[0].LastAlertAt)
	}
}

func TestListBudgetAlerts(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/gateway/api/v1/budgets/budget-1/alerts" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if r.URL.Query().Get("periodKey") != "2026-09" {
			t.Fatalf("query = %s", r.URL.RawQuery)
		}
		testutil.WriteFixture(t, w, "../testdata/billing/ListBudgetAlerts.json")
	}))

	out, err := client.ListBudgetAlerts(context.Background(), &ListBudgetAlertsInput{BudgetUUID: "budget-1", PeriodKey: "2026-09"})
	if err != nil {
		t.Fatalf("ListBudgetAlerts() error = %v", err)
	}
	if len(out.Items) != 2 {
		t.Fatalf("unexpected alerts: %+v", out.Items)
	}
	if len(out.Items[0].Recipients) != 1 || out.Items[0].Recipients[0] != "<account>" {
		t.Fatalf("unexpected recipients: %+v", out.Items[0])
	}
}

func TestAlertRecipientsFallback(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		testutil.WriteFixture(t, w, "../testdata/billing/ListBudgetAlerts.json")
	}))

	out, err := client.ListBudgetAlerts(context.Background(), &ListBudgetAlertsInput{BudgetUUID: "budget-1"})
	if err != nil {
		t.Fatalf("ListBudgetAlerts() error = %v", err)
	}
	odd := out.Items[1]
	if odd.Recipients != nil {
		t.Fatalf("expected nil Recipients for an unparseable value, got %v", odd.Recipients)
	}
	if odd.RecipientsRaw != "not-an-array" {
		t.Fatalf("RecipientsRaw = %q", odd.RecipientsRaw)
	}
}

// TestEnvelopeSuccessRange checks that a 2xx HTTP response is only a
// success when its envelope code is 200 to 299: CreateBudget answers 201,
// and any code above 299 stays an error even though the HTTP status is OK.
func TestEnvelopeSuccessRange(t *testing.T) {
	cases := []struct {
		name    string
		code    int
		wantErr bool
	}{
		{"201 succeeds", 201, false},
		{"299 succeeds", 299, false},
		{"300 fails", 300, true},
		{"500 fails", 500, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				_, _ = fmt.Fprintf(w, `{"code":%d,"message":"x","data":{"uuid":"budget-1"}}`, tc.code)
			}))

			out, err := client.GetBudget(context.Background(), &GetBudgetInput{BudgetUUID: "budget-1"})
			if tc.wantErr {
				var apiErr *vngcloud.APIError
				if !errors.As(err, &apiErr) {
					t.Fatalf("expected *vngcloud.APIError, got %v", err)
				}
				if apiErr.StatusCode != http.StatusOK {
					t.Fatalf("StatusCode = %d, want 200", apiErr.StatusCode)
				}
				return
			}
			if err != nil {
				t.Fatalf("GetBudget() error = %v", err)
			}
			if out.Budget.UUID != "budget-1" {
				t.Fatalf("unexpected budget: %+v", out.Budget)
			}
		})
	}
}

func TestEnvelopeErrorOn200(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"code":500,"message":"boom","data":null}`))
	}))

	_, err := client.GetBudget(context.Background(), &GetBudgetInput{BudgetUUID: "budget-1"})
	var apiErr *vngcloud.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *vngcloud.APIError, got %v", err)
	}
	if apiErr.Code != "500" || apiErr.Message != "boom" || apiErr.StatusCode != http.StatusOK {
		t.Fatalf("unexpected error: %+v", apiErr)
	}
}

func TestEnvelopeNullCode(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"code":null,"message":"Success","data":{"uuid":"budget-1"}}`))
		}))
		out, err := client.GetBudget(context.Background(), &GetBudgetInput{BudgetUUID: "budget-1"})
		if err != nil {
			t.Fatalf("GetBudget() error = %v", err)
		}
		if out.Budget.UUID != "budget-1" {
			t.Fatalf("unexpected budget: %+v", out.Budget)
		}
	})

	t.Run("error falls back to the status code", func(t *testing.T) {
		client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"code":null,"message":"boom"}`))
		}))
		_, err := client.GetBudget(context.Background(), &GetBudgetInput{BudgetUUID: "budget-1"})
		if err == nil {
			t.Fatal("expected error")
		}
		// A null envelope code leaves Code empty for ResolvedCode to fill in
		// from the status-derived table: 500 falls back to "ServerError".
		if vngcloud.ErrorCode(err) != "ServerError" {
			t.Fatalf("ErrorCode(err) = %q, want ServerError", vngcloud.ErrorCode(err))
		}
	})
}

func TestBudgetNotFound(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		testutil.WriteFixture(t, w, "../testdata/billing/BudgetNotFound.json")
	}))

	_, err := client.GetBudget(context.Background(), &GetBudgetInput{BudgetUUID: "budget-x"})
	if !vngcloud.IsNotFound(err) {
		t.Fatalf("IsNotFound(err) = false, err = %v", err)
	}
	if vngcloud.ErrorCode(err) != "NotFound" {
		t.Fatalf("ErrorCode(err) = %q", vngcloud.ErrorCode(err))
	}
	var apiErr *vngcloud.APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusBadRequest {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestNotFoundMessageOnlyMappedFor400Or2xx checks that mapNotFound applies
// only to the two shapes the server actually uses for "not found": a 400,
// or an error code inside a 2xx envelope. A 401, 403, or 5xx that happens to
// carry the same message text keeps its original error and status.
func TestNotFoundMessageOnlyMappedFor400Or2xx(t *testing.T) {
	cases := []struct {
		name   string
		status int
	}{
		{"401", http.StatusUnauthorized},
		{"403", http.StatusForbidden},
		{"500", http.StatusInternalServerError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(`{"message":"Budget not found: budget-x"}`))
			}))

			_, err := client.GetBudget(context.Background(), &GetBudgetInput{BudgetUUID: "budget-x"})
			if vngcloud.IsNotFound(err) {
				t.Fatalf("IsNotFound(err) = true, want false for status %d", tc.status)
			}
			var apiErr *vngcloud.APIError
			if !errors.As(err, &apiErr) || apiErr.StatusCode != tc.status {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestOtherBadRequestNotMapped(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"code":400,"message":"Invalid budget name"}`))
	}))

	_, err := client.GetBudget(context.Background(), &GetBudgetInput{BudgetUUID: "budget-1"})
	if vngcloud.IsNotFound(err) {
		t.Fatalf("IsNotFound(err) = true, want false")
	}
	// The envelope's own code (400) equals the HTTP status, which counts as
	// none, so Code falls back to the status-derived "BadRequest".
	if vngcloud.ErrorCode(err) != "BadRequest" {
		t.Fatalf("ErrorCode(err) = %q", vngcloud.ErrorCode(err))
	}
}

func TestNoEnvelopeIsError(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"foo":1}`))
	}))

	_, err := client.GetBudget(context.Background(), &GetBudgetInput{BudgetUUID: "budget-1"})
	var apiErr *vngcloud.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *vngcloud.APIError, got %v", err)
	}
	if apiErr.Operation != "billing.GetBudget" {
		t.Fatalf("Operation = %q", apiErr.Operation)
	}
	if apiErr.Message != "response had no envelope" {
		t.Fatalf("Message = %q", apiErr.Message)
	}
}

func TestEnvelopeNotFoundOn200(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"code":400,"message":"Budget not found: budget-x"}`))
	}))

	_, err := client.GetBudget(context.Background(), &GetBudgetInput{BudgetUUID: "budget-x"})
	if !vngcloud.IsNotFound(err) {
		t.Fatalf("IsNotFound(err) = false, err = %v", err)
	}
}

func TestRequiredAndPathIDs(t *testing.T) {
	failIfCalled := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called")
	})

	t.Run("nil input", func(t *testing.T) {
		client := newTestClient(t, failIfCalled)
		_, err := client.GetBudget(context.Background(), nil)
		if !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Fatalf("err = %v, want ErrInvalidInput", err)
		}
	})

	t.Run("bad path id", func(t *testing.T) {
		client := newTestClient(t, failIfCalled)
		_, err := client.GetBudget(context.Background(), &GetBudgetInput{BudgetUUID: ".."})
		if !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Fatalf("err = %v, want ErrInvalidInput", err)
		}
	})

	t.Run("bad threshold path id", func(t *testing.T) {
		client := newTestClient(t, failIfCalled)
		_, err := client.ListBudgetThresholds(context.Background(), &ListBudgetThresholdsInput{BudgetUUID: "a/b"})
		if !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Fatalf("err = %v, want ErrInvalidInput", err)
		}
	})
}
