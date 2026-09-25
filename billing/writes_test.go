package billing

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/testutil"
)

func decodeBody(t *testing.T, r *http.Request) map[string]any {
	t.Helper()
	data, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if len(data) == 0 {
		return nil
	}
	var body map[string]any
	if err := json.Unmarshal(data, &body); err != nil {
		t.Fatalf("decode body: %v, raw = %s", err, data)
	}
	return body
}

func TestCreateBudgetSendsFieldsAndDefaults(t *testing.T) {
	t.Run("every field set", func(t *testing.T) {
		client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				t.Fatalf("method = %s", r.Method)
			}
			if r.URL.Path != "/gateway/api/v1/budgets" {
				t.Fatalf("path = %s", r.URL.Path)
			}
			body := decodeBody(t, r)
			want := map[string]any{
				"name":        "prod-actual-2",
				"periodType":  "MONTHLY",
				"type":        "ACTUAL",
				"limitAmount": float64(2000000000),
				"status":      "PAUSED",
			}
			for k, v := range want {
				if body[k] != v {
					t.Fatalf("body[%q] = %v, want %v (body = %+v)", k, body[k], v, body)
				}
			}
			// The API answers a create with HTTP 200 and an envelope code of
			// 201; WriteFixture leaves the status at its default, 200.
			testutil.WriteFixture(t, w, "../testdata/billing/CreateBudget.json")
		}))

		out, err := client.CreateBudget(context.Background(), &CreateBudgetInput{
			Name:        "prod-actual-2",
			PeriodType:  PeriodMonthly,
			Type:        TypeActual,
			LimitAmount: 2000000000,
			Status:      StatusPaused,
		})
		if err != nil {
			t.Fatalf("CreateBudget() error = %v", err)
		}
		if out.Budget.UUID != "budget-3" || out.Budget.LimitAmount != 2000000000 {
			t.Fatalf("unexpected budget: %+v", out.Budget)
		}
		if out.Budget.Currency != "credit" || out.Budget.StartDate != "2026-09-25" {
			t.Fatalf("unexpected budget: %+v", out.Budget)
		}
		if out.Budget.CreatedAt != "2026-09-25T00:00:00Z" || out.Budget.UpdatedAt != "2026-09-25T00:00:00Z" {
			t.Fatalf("unexpected budget: %+v", out.Budget)
		}
	})

	t.Run("empty status defaults to ACTIVE", func(t *testing.T) {
		client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body := decodeBody(t, r)
			if body["status"] != "ACTIVE" {
				t.Fatalf("status = %v, want ACTIVE", body["status"])
			}
			testutil.WriteFixture(t, w, "../testdata/billing/CreateBudget.json")
		}))

		_, err := client.CreateBudget(context.Background(), &CreateBudgetInput{
			Name:        "prod-actual-2",
			PeriodType:  PeriodMonthly,
			Type:        TypeActual,
			LimitAmount: 2000000000,
		})
		if err != nil {
			t.Fatalf("CreateBudget() error = %v", err)
		}
	})
}

func TestCreateBudgetRequiredFields(t *testing.T) {
	failIfCalled := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called")
	})

	cases := []struct {
		name string
		in   *CreateBudgetInput
	}{
		{"nil input", nil},
		{"missing name", &CreateBudgetInput{PeriodType: PeriodMonthly, Type: TypeActual, LimitAmount: 1}},
		{"missing limit", &CreateBudgetInput{Name: "x", PeriodType: PeriodMonthly, Type: TypeActual}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := newTestClient(t, failIfCalled)
			_, err := client.CreateBudget(context.Background(), tc.in)
			if !errors.Is(err, vngcloud.ErrInvalidInput) {
				t.Fatalf("err = %v, want ErrInvalidInput", err)
			}
		})
	}
}

func TestCreateBudgetMissingUUID(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":200,"message":"Success","data":{"id":4,"name":"x","periodType":"MONTHLY","type":"ACTUAL","limitAmount":1000000,"status":"ACTIVE"}}`))
	}))

	out, err := client.CreateBudget(context.Background(), &CreateBudgetInput{
		Name:        "x",
		PeriodType:  PeriodMonthly,
		Type:        TypeActual,
		LimitAmount: 1000000,
	})
	if err != nil {
		t.Fatalf("CreateBudget() error = %v", err)
	}
	if out.Budget.UUID != "" {
		t.Fatalf("expected empty UUID when data omits it, got %q", out.Budget.UUID)
	}
}

func TestCreateBudgetNotRetriedAfter502(t *testing.T) {
	calls := 0
	client := New(testutil.NewRetryConfig(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusBadGateway)
	})))

	_, err := client.CreateBudget(context.Background(), &CreateBudgetInput{
		Name:        "x",
		PeriodType:  PeriodMonthly,
		Type:        TypeActual,
		LimitAmount: 1000000,
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}
	if vngcloud.IsRetryable(err) {
		t.Fatal("IsRetryable(err) = true, want false")
	}
}

func TestUpdateBudgetRetriedAfter502(t *testing.T) {
	calls := 0
	client := New(testutil.NewRetryConfig(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		testutil.WriteFixture(t, w, "../testdata/billing/UpdateBudget.json")
	})))

	_, err := client.UpdateBudget(context.Background(), &UpdateBudgetInput{
		BudgetUUID: "budget-1",
		Status:     vngcloud.Ptr(StatusPaused),
	})
	if err != nil {
		t.Fatalf("UpdateBudget() error = %v", err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
}

func TestUpdateBudgetSendsOnlySetFields(t *testing.T) {
	cases := []struct {
		name string
		in   *UpdateBudgetInput
		want map[string]any
	}{
		{
			name: "status only",
			in:   &UpdateBudgetInput{BudgetUUID: "budget-1", Status: vngcloud.Ptr(StatusPaused)},
			want: map[string]any{"status": "PAUSED"},
		},
		{
			name: "limit amount only",
			in:   &UpdateBudgetInput{BudgetUUID: "budget-1", LimitAmount: vngcloud.Ptr(int64(3000000))},
			want: map[string]any{"limitAmount": float64(3000000)},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPut {
					t.Fatalf("method = %s", r.Method)
				}
				if r.URL.Path != "/gateway/api/v1/budgets/budget-1" {
					t.Fatalf("path = %s", r.URL.Path)
				}
				body := decodeBody(t, r)
				if len(body) != len(tc.want) {
					t.Fatalf("body = %+v, want %+v", body, tc.want)
				}
				for k, v := range tc.want {
					if body[k] != v {
						t.Fatalf("body[%q] = %v, want %v", k, body[k], v)
					}
				}
				testutil.WriteFixture(t, w, "../testdata/billing/UpdateBudget.json")
			}))

			_, err := client.UpdateBudget(context.Background(), tc.in)
			if err != nil {
				t.Fatalf("UpdateBudget() error = %v", err)
			}
		})
	}
}

func TestDeleteBudgetSendsNoBody(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Fatalf("method = %s", r.Method)
		}
		if r.URL.Path != "/gateway/api/v1/budgets/budget-1" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if len(data) != 0 {
			t.Fatalf("body = %s, want empty", data)
		}
		testutil.WriteFixture(t, w, "../testdata/billing/DeleteBudget.json")
	}))

	_, err := client.DeleteBudget(context.Background(), &DeleteBudgetInput{BudgetUUID: "budget-1"})
	if err != nil {
		t.Fatalf("DeleteBudget() error = %v", err)
	}
}

func TestDeleteBudgetEmpty204IsSuccess(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	_, err := client.DeleteBudget(context.Background(), &DeleteBudgetInput{BudgetUUID: "budget-1"})
	if err != nil {
		t.Fatalf("DeleteBudget() error = %v", err)
	}
}

func TestDeleteBudgetNotFound(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		testutil.WriteFixture(t, w, "../testdata/billing/BudgetNotFound.json")
	}))

	_, err := client.DeleteBudget(context.Background(), &DeleteBudgetInput{BudgetUUID: "budget-x"})
	if !vngcloud.IsNotFound(err) {
		t.Fatalf("IsNotFound(err) = false, err = %v", err)
	}
}

func TestUpdateBudgetForbidden(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"forbidden"}`))
	}))

	_, err := client.UpdateBudget(context.Background(), &UpdateBudgetInput{
		BudgetUUID: "budget-1",
		Status:     vngcloud.Ptr(StatusPaused),
	})
	if !vngcloud.IsPermissionDenied(err) {
		t.Fatalf("IsPermissionDenied(err) = false, err = %v", err)
	}
}

func TestUpdateBudgetEnvelopeErrorOn200(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"code":500,"message":"boom","data":null}`))
	}))

	_, err := client.UpdateBudget(context.Background(), &UpdateBudgetInput{
		BudgetUUID: "budget-1",
		Status:     vngcloud.Ptr(StatusPaused),
	})
	var apiErr *vngcloud.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *vngcloud.APIError, got %v", err)
	}
	if apiErr.Code != "500" || apiErr.StatusCode != http.StatusOK {
		t.Fatalf("unexpected error: %+v", apiErr)
	}
}

func TestCreateBudgetThresholdSendsFieldsAndDefaults(t *testing.T) {
	t.Run("every field set", func(t *testing.T) {
		client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				t.Fatalf("method = %s", r.Method)
			}
			if r.URL.Path != "/gateway/api/v1/budgets/budget-1/thresholds" {
				t.Fatalf("path = %s", r.URL.Path)
			}
			body := decodeBody(t, r)
			want := map[string]any{
				"thresholdType":         "ACTUAL",
				"thresholdPercentage":   float64(90),
				"comparisonOperator":    "GTE",
				"maxAlertsPerPeriod":    float64(3),
				"reminderIntervalHours": float64(24),
			}
			for k, v := range want {
				if body[k] != v {
					t.Fatalf("body[%q] = %v, want %v (body = %+v)", k, body[k], v, body)
				}
			}
			if _, ok := body["enabled"]; ok {
				t.Fatalf("body contains enabled, want it omitted since the server ignores it on create: %+v", body)
			}
			testutil.WriteFixture(t, w, "../testdata/billing/CreateBudgetThreshold.json")
		}))

		out, err := client.CreateBudgetThreshold(context.Background(), &CreateBudgetThresholdInput{
			BudgetUUID:            "budget-1",
			ThresholdType:         TypeActual,
			ThresholdPercentage:   90,
			MaxAlertsPerPeriod:    3,
			ReminderIntervalHours: 24,
		})
		if err != nil {
			t.Fatalf("CreateBudgetThreshold() error = %v", err)
		}
		if out.Threshold.UUID != "threshold-2" {
			t.Fatalf("unexpected threshold: %+v", out.Threshold)
		}
	})

	t.Run("defaults", func(t *testing.T) {
		client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body := decodeBody(t, r)
			want := map[string]any{
				"comparisonOperator":    "GTE",
				"maxAlertsPerPeriod":    float64(1),
				"reminderIntervalHours": float64(648),
			}
			for k, v := range want {
				if body[k] != v {
					t.Fatalf("body[%q] = %v, want %v (body = %+v)", k, body[k], v, body)
				}
			}
			if _, ok := body["enabled"]; ok {
				t.Fatalf("body contains enabled, want it omitted since the server ignores it on create: %+v", body)
			}
			testutil.WriteFixture(t, w, "../testdata/billing/CreateBudgetThreshold.json")
		}))

		_, err := client.CreateBudgetThreshold(context.Background(), &CreateBudgetThresholdInput{
			BudgetUUID:          "budget-1",
			ThresholdType:       TypeActual,
			ThresholdPercentage: 80,
		})
		if err != nil {
			t.Fatalf("CreateBudgetThreshold() error = %v", err)
		}
	})
}

func TestCreateBudgetThresholdRequiredFields(t *testing.T) {
	failIfCalled := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called")
	})

	cases := []struct {
		name string
		in   *CreateBudgetThresholdInput
	}{
		{"nil input", nil},
		{"missing budget uuid", &CreateBudgetThresholdInput{ThresholdType: TypeActual, ThresholdPercentage: 80}},
		{"missing threshold type", &CreateBudgetThresholdInput{BudgetUUID: "budget-1", ThresholdPercentage: 80}},
		{"missing percentage", &CreateBudgetThresholdInput{BudgetUUID: "budget-1", ThresholdType: TypeActual}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := newTestClient(t, failIfCalled)
			_, err := client.CreateBudgetThreshold(context.Background(), tc.in)
			if !errors.Is(err, vngcloud.ErrInvalidInput) {
				t.Fatalf("err = %v, want ErrInvalidInput", err)
			}
		})
	}
}

func TestUpdateBudgetThresholdSendsOnlySetFields(t *testing.T) {
	cases := []struct {
		name string
		in   *UpdateBudgetThresholdInput
		want map[string]any
	}{
		{
			name: "percentage only",
			in: &UpdateBudgetThresholdInput{
				BudgetUUID:          "budget-1",
				ThresholdUUID:       "threshold-1",
				ThresholdPercentage: vngcloud.Ptr(95),
			},
			want: map[string]any{"thresholdPercentage": float64(95)},
		},
		{
			name: "enabled false is sent, not omitted",
			in: &UpdateBudgetThresholdInput{
				BudgetUUID:    "budget-1",
				ThresholdUUID: "threshold-1",
				Enabled:       vngcloud.Ptr(false),
			},
			want: map[string]any{"enabled": false},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPut {
					t.Fatalf("method = %s", r.Method)
				}
				if r.URL.Path != "/gateway/api/v1/budgets/budget-1/thresholds/threshold-1" {
					t.Fatalf("path = %s", r.URL.Path)
				}
				body := decodeBody(t, r)
				if len(body) != len(tc.want) {
					t.Fatalf("body = %+v, want %+v", body, tc.want)
				}
				for k, v := range tc.want {
					if body[k] != v {
						t.Fatalf("body[%q] = %v, want %v", k, body[k], v)
					}
				}
				testutil.WriteFixture(t, w, "../testdata/billing/UpdateBudgetThreshold.json")
			}))

			_, err := client.UpdateBudgetThreshold(context.Background(), tc.in)
			if err != nil {
				t.Fatalf("UpdateBudgetThreshold() error = %v", err)
			}
		})
	}
}

func TestDeleteBudgetThresholdSendsNoBody(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Fatalf("method = %s", r.Method)
		}
		if r.URL.Path != "/gateway/api/v1/budgets/budget-1/thresholds/threshold-1" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if len(data) != 0 {
			t.Fatalf("body = %s, want empty", data)
		}
		testutil.WriteFixture(t, w, "../testdata/billing/DeleteBudgetThreshold.json")
	}))

	_, err := client.DeleteBudgetThreshold(context.Background(), &DeleteBudgetThresholdInput{
		BudgetUUID:    "budget-1",
		ThresholdUUID: "threshold-1",
	})
	if err != nil {
		t.Fatalf("DeleteBudgetThreshold() error = %v", err)
	}
}

func TestWritePathIDs(t *testing.T) {
	failIfCalled := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called")
	})

	t.Run("UpdateBudget rejects ../x", func(t *testing.T) {
		client := newTestClient(t, failIfCalled)
		_, err := client.UpdateBudget(context.Background(), &UpdateBudgetInput{BudgetUUID: "../x", Status: vngcloud.Ptr(StatusPaused)})
		if !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Fatalf("err = %v, want ErrInvalidInput", err)
		}
	})

	t.Run("DeleteBudget rejects .", func(t *testing.T) {
		client := newTestClient(t, failIfCalled)
		_, err := client.DeleteBudget(context.Background(), &DeleteBudgetInput{BudgetUUID: "."})
		if !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Fatalf("err = %v, want ErrInvalidInput", err)
		}
	})

	t.Run("CreateBudgetThreshold rejects ../x budget uuid", func(t *testing.T) {
		client := newTestClient(t, failIfCalled)
		_, err := client.CreateBudgetThreshold(context.Background(), &CreateBudgetThresholdInput{
			BudgetUUID:          "../x",
			ThresholdType:       TypeActual,
			ThresholdPercentage: 80,
		})
		if !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Fatalf("err = %v, want ErrInvalidInput", err)
		}
	})

	t.Run("UpdateBudgetThreshold rejects . threshold uuid", func(t *testing.T) {
		client := newTestClient(t, failIfCalled)
		_, err := client.UpdateBudgetThreshold(context.Background(), &UpdateBudgetThresholdInput{
			BudgetUUID:    "budget-1",
			ThresholdUUID: ".",
			Enabled:       vngcloud.Ptr(false),
		})
		if !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Fatalf("err = %v, want ErrInvalidInput", err)
		}
	})

	t.Run("DeleteBudgetThreshold rejects ../x budget uuid", func(t *testing.T) {
		client := newTestClient(t, failIfCalled)
		_, err := client.DeleteBudgetThreshold(context.Background(), &DeleteBudgetThresholdInput{
			BudgetUUID:    "../x",
			ThresholdUUID: "threshold-1",
		})
		if !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Fatalf("err = %v, want ErrInvalidInput", err)
		}
	})
}
