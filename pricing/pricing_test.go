package pricing

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

func newTestClient(t *testing.T, handler http.Handler) *Client {
	t.Helper()
	return New(testutil.NewConfig(t, handler))
}

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

func TestGetQuoteSnapshot(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s", r.Method)
		}
		if r.URL.Path != "/v1/price" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		body := decodeBody(t, r)
		want := map[string]any{
			"resourceType": ResourceSnapshot,
			"action":       "create",
			"resourceInfo": map[string]any{"sizeGb": float64(40)},
		}
		for k, v := range want {
			if got := body[k]; !jsonEqual(got, v) {
				t.Fatalf("body[%q] = %v, want %v (body = %+v)", k, got, v, body)
			}
		}
		testutil.WriteFixture(t, w, "../testdata/pricing/GetQuoteSnapshot.json")
	}))

	out, err := client.GetQuote(context.Background(), &GetQuoteInput{
		ResourceType: ResourceSnapshot,
		ResourceInfo: map[string]any{"sizeGb": 40},
	})
	if err != nil {
		t.Fatalf("GetQuote() error = %v", err)
	}
	if out.OptimumPrice != 5040 || out.OriginalPrice != 5040 {
		t.Fatalf("unexpected prices: %+v", out)
	}
	if out.DiscountPrice != 0 || out.DiscountPercent != 0 {
		t.Fatalf("unexpected discount: %+v", out)
	}
	if len(out.Properties) != 0 {
		t.Fatalf("unexpected properties: %+v", out.Properties)
	}
}

func TestGetQuotePublicVIP(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s", r.Method)
		}
		if r.URL.Path != "/v1/price" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		body := decodeBody(t, r)
		if body["resourceType"] != ResourcePublicVIP {
			t.Fatalf("resourceType = %v, want %v", body["resourceType"], ResourcePublicVIP)
		}
		if body["action"] != "create" {
			t.Fatalf("action = %v, want create", body["action"])
		}
		testutil.WriteFixture(t, w, "../testdata/pricing/GetQuotePublicVIP.json")
	}))

	out, err := client.GetQuote(context.Background(), &GetQuoteInput{
		ResourceType: ResourcePublicVIP,
		ResourceInfo: map[string]any{"billingType": "monthly"},
	})
	if err != nil {
		t.Fatalf("GetQuote() error = %v", err)
	}
	if out.OptimumPrice != 120000 || out.OriginalPrice != 120000 {
		t.Fatalf("unexpected prices: %+v", out)
	}
	if len(out.Properties) != 1 {
		t.Fatalf("unexpected properties: %+v", out.Properties)
	}
	prop := out.Properties[0]
	if prop.Name != "Public Virtual Ip Address" {
		t.Fatalf("Name = %q", prop.Name)
	}
	if prop.Description != "" {
		t.Fatalf("Description = %q, want empty for a null description", prop.Description)
	}
	if prop.OptimumPrice != 120000 || prop.MonthlyPrice != 120000 {
		t.Fatalf("unexpected property prices: %+v", prop)
	}
	if prop.CurrentPrice != nil {
		t.Fatalf("CurrentPrice = %v, want nil for a null currentPrice", *prop.CurrentPrice)
	}
	if prop.DiscountPercent != 0 {
		t.Fatalf("DiscountPercent = %v", prop.DiscountPercent)
	}
}

func TestGetQuoteNilResourceInfoOmitsKey(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := decodeBody(t, r)
		if _, ok := body["resourceInfo"]; ok {
			t.Fatalf("body has resourceInfo = %v, want it omitted", body["resourceInfo"])
		}
		testutil.WriteFixture(t, w, "../testdata/pricing/GetQuoteSnapshot.json")
	}))

	_, err := client.GetQuote(context.Background(), &GetQuoteInput{ResourceType: ResourceSnapshot})
	if err != nil {
		t.Fatalf("GetQuote() error = %v", err)
	}
}

func TestGetQuoteRequiredFields(t *testing.T) {
	failIfCalled := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called")
	})

	cases := []struct {
		name string
		in   *GetQuoteInput
	}{
		{"nil input", nil},
		{"missing resource type", &GetQuoteInput{ResourceInfo: map[string]any{"sizeGb": 40}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := newTestClient(t, failIfCalled)
			_, err := client.GetQuote(context.Background(), tc.in)
			if !errors.Is(err, vngcloud.ErrInvalidInput) {
				t.Fatalf("err = %v, want ErrInvalidInput", err)
			}
		})
	}
}

func TestGetQuoteRetriedAfter502(t *testing.T) {
	calls := 0
	client := New(testutil.NewRetryConfig(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		testutil.WriteFixture(t, w, "../testdata/pricing/GetQuoteSnapshot.json")
	})))

	out, err := client.GetQuote(context.Background(), &GetQuoteInput{ResourceType: ResourceSnapshot})
	if err != nil {
		t.Fatalf("GetQuote() error = %v", err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
	if out.OptimumPrice != 5040 {
		t.Fatalf("unexpected output: %+v", out)
	}
}

// TestGetQuoteNoPriceIsError checks that an HTTP 200 body without an
// optimumPrice key is an error rather than a silent zero-price quote. The
// price gateway is not enveloped in the normal case, but an error can still
// arrive as a billing-style envelope on a 200; either shape lacks
// optimumPrice and must not decode into a fake zero-value quote.
func TestGetQuoteNoPriceIsError(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"empty object", `{}`},
		{"error envelope", `{"code":400,"message":"x"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(tc.body))
			}))

			_, err := client.GetQuote(context.Background(), &GetQuoteInput{ResourceType: ResourceSnapshot})
			var apiErr *vngcloud.APIError
			if !errors.As(err, &apiErr) {
				t.Fatalf("expected *vngcloud.APIError, got %v", err)
			}
			if apiErr.Message != "quote response had no price" {
				t.Fatalf("Message = %q", apiErr.Message)
			}
		})
	}
}

func TestGetQuoteBadRequestIsAPIError(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"message":"invalid resource type"}`))
	}))

	_, err := client.GetQuote(context.Background(), &GetQuoteInput{ResourceType: "not-a-real-type"})
	var apiErr *vngcloud.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *vngcloud.APIError, got %v", err)
	}
	if apiErr.StatusCode != http.StatusBadRequest {
		t.Fatalf("StatusCode = %d, want %d", apiErr.StatusCode, http.StatusBadRequest)
	}
}

// jsonEqual compares two values decoded from JSON, where map and slice
// operands compare deeply rather than by identity.
func jsonEqual(a, b any) bool {
	ab, err := json.Marshal(a)
	if err != nil {
		return false
	}
	bb, err := json.Marshal(b)
	if err != nil {
		return false
	}
	return string(ab) == string(bb)
}
