package pricing

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
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

func TestPricingZeroConfig(t *testing.T) {
	c := New(vngcloud.Config{})
	_, err := c.GetQuote(context.Background(), &GetQuoteInput{ResourceType: ResourceSnapshot})
	if !errors.Is(err, vngcloud.ErrInvalidConfig) {
		t.Fatalf("GetQuote() err = %v, want ErrInvalidConfig", err)
	}
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

func TestGetQuoteActionDefaultsToCreate(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := decodeBody(t, r)
		if body["action"] != ActionCreate {
			t.Fatalf("action = %v, want %v", body["action"], ActionCreate)
		}
		testutil.WriteFixture(t, w, "../testdata/pricing/GetQuoteSnapshot.json")
	}))

	_, err := client.GetQuote(context.Background(), &GetQuoteInput{ResourceType: ResourceSnapshot})
	if err != nil {
		t.Fatalf("GetQuote() error = %v", err)
	}
}

func TestGetQuoteActionResize(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := decodeBody(t, r)
		if body["action"] != ActionResize {
			t.Fatalf("action = %v, want %v", body["action"], ActionResize)
		}
		testutil.WriteFixture(t, w, "../testdata/pricing/GetQuoteSnapshot.json")
	}))

	_, err := client.GetQuote(context.Background(), &GetQuoteInput{
		ResourceType: ResourceServer,
		Action:       ActionResize,
	})
	if err != nil {
		t.Fatalf("GetQuote() error = %v", err)
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

// TestGetQuoteNullPriceIsError checks that an explicit JSON null for
// optimumPrice refuses the same way a missing key does, rather than
// decoding into a silent zero-value price: before this fix, a plain
// float64 field left a null price as 0, which a paid write's guard
// (quoted > MaxPrice) would compare as free and let through.
func TestGetQuoteNullPriceIsError(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"optimumPrice":null,"originalPrice":100}`))
	}))

	_, err := client.GetQuote(context.Background(), &GetQuoteInput{ResourceType: ResourceSnapshot})
	var apiErr *vngcloud.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *vngcloud.APIError, got %v", err)
	}
	if apiErr.Message != "quote response had no price" {
		t.Fatalf("Message = %q", apiErr.Message)
	}
}

// TestGetQuoteNegativePriceIsError checks that a negative optimumPrice
// refuses rather than decoding as-is: a paid write's guard
// (quoted > MaxPrice) would otherwise compare a negative price below any
// non-negative MaxPrice, including the default of 0, and let the write
// through unpriced.
func TestGetQuoteNegativePriceIsError(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"optimumPrice":-100}`))
	}))

	_, err := client.GetQuote(context.Background(), &GetQuoteInput{ResourceType: ResourceSnapshot})
	var apiErr *vngcloud.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *vngcloud.APIError, got %v", err)
	}
	if apiErr.Message == "" {
		t.Fatal("Message is empty, want a message naming the invalid price")
	}
}

// TestGetQuoteNaNLiteralPriceIsError checks that a quote response carrying
// a bare NaN token for optimumPrice, invalid JSON syntax that a hostile or
// broken gateway could still send, is refused rather than partially decoded.
func TestGetQuoteNaNLiteralPriceIsError(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"optimumPrice":NaN}`))
	}))

	if _, err := client.GetQuote(context.Background(), &GetQuoteInput{ResourceType: ResourceSnapshot}); err == nil {
		t.Fatal("err = nil, want an error for a NaN price")
	}
}

// TestValidQuotePrice is a white-box unit test of pricing's own price
// guard, covering NaN and infinite values directly: neither can arrive
// through a valid JSON number (encoding/json rejects a bare NaN token as a
// syntax error and a value that overflows float64 as a decode error), so
// this is the only way to exercise validQuotePrice's explicit checks for
// them, kept as defense in depth alongside the null and negative checks
// GetQuote's own tests exercise through the wire.
func TestValidQuotePrice(t *testing.T) {
	cases := []struct {
		name    string
		price   *float64
		wantErr bool
	}{
		{"nil", nil, true},
		{"negative", ptrFloat64(-1), true},
		{"NaN", ptrFloat64(math.NaN()), true},
		{"positive infinity", ptrFloat64(math.Inf(1)), true},
		{"negative infinity", ptrFloat64(math.Inf(-1)), true},
		{"zero", ptrFloat64(0), false},
		{"positive", ptrFloat64(347800), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := validQuotePrice("op", tc.price)
			if tc.wantErr && err == nil {
				t.Fatal("err = nil, want an error")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
		})
	}
}

func ptrFloat64(f float64) *float64 { return &f }

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
