package billing

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/testutil"
)

func newTestClient(t *testing.T, handler http.Handler) *Client {
	t.Helper()
	return New(testutil.NewConfig(t, handler))
}

func TestListBudgets(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("method = %s", r.Method)
		}
		if r.URL.Path != "/gateway/api/v1/budgets" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if r.URL.Query().Get("view") != "summary" || r.URL.Query().Get("status") != StatusActive {
			t.Fatalf("query = %s", r.URL.RawQuery)
		}
		testutil.WriteFixture(t, w, "../testdata/billing/ListBudgets.json")
	}))

	out, err := client.ListBudgets(context.Background(), &ListBudgetsInput{Status: StatusActive})
	if err != nil {
		t.Fatalf("ListBudgets() error = %v", err)
	}
	if len(out.Items) != 2 {
		t.Fatalf("unexpected budgets: %+v", out.Items)
	}
	if out.Items[0].UUID != "budget-1" || out.Items[0].Status != StatusActive || *out.Items[0].ActualCost != 1200000 {
		t.Fatalf("unexpected budget-1: %+v", out.Items[0])
	}
	if out.Items[1].Status != StatusPaused || out.Items[1].ActualCost != nil {
		t.Fatalf("unexpected budget-2: %+v", out.Items[1])
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
	if out.Budget.UUID != "budget-1" || out.Budget.Name != "prod-actual" {
		t.Fatalf("unexpected budget: %+v", out.Budget)
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
	if len(out.Items) != 1 || out.Items[0].UUID != "threshold-1" || !out.Items[0].Enabled {
		t.Fatalf("unexpected thresholds: %+v", out.Items)
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

	t.Run("error keeps code empty", func(t *testing.T) {
		client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"code":null,"message":"boom"}`))
		}))
		_, err := client.GetBudget(context.Background(), &GetBudgetInput{BudgetUUID: "budget-1"})
		if err == nil {
			t.Fatal("expected error")
		}
		if vngcloud.ErrorCode(err) != "" {
			t.Fatalf("ErrorCode(err) = %q, want empty", vngcloud.ErrorCode(err))
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

func TestOtherBadRequestNotMapped(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"code":400,"message":"Invalid budget name"}`))
	}))

	_, err := client.GetBudget(context.Background(), &GetBudgetInput{BudgetUUID: "budget-1"})
	if vngcloud.IsNotFound(err) {
		t.Fatalf("IsNotFound(err) = true, want false")
	}
	if vngcloud.ErrorCode(err) != "400" {
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
