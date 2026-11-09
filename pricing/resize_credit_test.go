package pricing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/testutil"
	"danny.vn/vngcloud/internal/transport"
)

// Synthetic policy input, not a captured server response. Other field signs
// remain unverified; do not infer discounts or line items from this total.
const resizeCreditSynthetic = `{"optimumPrice":-180797}`

func TestGetQuoteSignedActionMatrixSynthetic(t *testing.T) {
	for _, resource := range []string{ResourceServer, ResourceVolume, ResourceLoadBalancer, "arbitrary-resource"} {
		for _, action := range []string{ActionResize, ActionCreate, "", "Resize", " resize", "unknown"} {
			t.Run(resource+"/"+action, func(t *testing.T) {
				c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					body := decodeBody(t, r)
					wantAction := action
					if wantAction == "" {
						wantAction = ActionCreate
					}
					if body["action"] != wantAction || body["resourceType"] != resource {
						t.Errorf("body = %+v, want action %q and resource %q", body, wantAction, resource)
					}
					_, _ = w.Write([]byte(resizeCreditSynthetic))
				}))
				out, err := c.GetQuote(context.Background(), &GetQuoteInput{ResourceType: resource, Action: action})
				if action == ActionResize {
					if err != nil || out == nil || out.OptimumPrice != -180797 {
						t.Fatalf("out = %+v, err = %v, want signed credit", out, err)
					}
				} else {
					var apiErr *vngcloud.APIError
					if out != nil || !errors.As(err, &apiErr) {
						t.Fatalf("out = %+v, err = %v, want nil and APIError", out, err)
					}
				}
			})
		}
	}
}

func TestGetQuoteInvalidPricesSynthetic(t *testing.T) {
	for _, action := range []string{ActionResize, ActionCreate, ""} {
		for _, body := range []string{
			``, `{}`, `null`, `{"code":400,"message":"unpriced"}`, `{"optimumPrice":null}`,
			`{"optimumPrice":"-180797"}`, `{"optimumPrice":true}`, `{"optimumPrice":{}}`,
			`{"optimumPrice":NaN}`, `{"optimumPrice":Infinity}`, `{"optimumPrice":-Infinity}`,
			`{"optimumPrice":1e309}`, `{"optimumPrice":-1e309}`, `{"optimumPrice":`,
		} {
			t.Run(action+"/"+body, func(t *testing.T) {
				c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					_, _ = w.Write([]byte(body))
				}))
				out, err := c.GetQuote(context.Background(), &GetQuoteInput{ResourceType: ResourceServer, Action: action})
				var apiErr *vngcloud.APIError
				if out != nil || !errors.As(err, &apiErr) {
					t.Fatalf("out = %+v, err = %v, want nil and APIError", out, err)
				}
			})
		}
	}
}

func TestGetQuoteFiniteExtremesSynthetic(t *testing.T) {
	for _, price := range []float64{0, math.Copysign(0, -1), -math.SmallestNonzeroFloat64, -math.MaxFloat64} {
		for _, action := range []string{ActionCreate, ActionResize} {
			t.Run(fmt.Sprint(price)+"/"+action, func(t *testing.T) {
				c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					_, _ = fmt.Fprintf(w, `{"optimumPrice":%v}`, price)
				}))
				out, err := c.GetQuote(context.Background(), &GetQuoteInput{ResourceType: ResourceServer, Action: action})
				if price < 0 && action != ActionResize {
					var apiErr *vngcloud.APIError
					if out != nil || !errors.As(err, &apiErr) {
						t.Fatalf("out = %+v, err = %v, want refusal", out, err)
					}
					return
				}
				if err != nil || out == nil || out.OptimumPrice != price || math.Signbit(out.OptimumPrice) != math.Signbit(price) {
					t.Fatalf("out = %+v, err = %v, want %v with sign preserved", out, err, price)
				}
			})
		}
	}
}

func TestGetQuoteResponseBoundSynthetic(t *testing.T) {
	for _, status := range []int{200, 400, 404, 429, 502} {
		for _, size := range []int{1 << 20, 1<<20 + 1} {
			t.Run(fmt.Sprintf("%d/%d", status, size), func(t *testing.T) {
				captured := 0
				body := `{"optimumPrice":100}`
				body += strings.Repeat(" ", size-len(body))
				c := New(testutil.NewConfigWithCapture(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(status)
					_, _ = w.Write([]byte(body))
				}), func(transport.Capture) { captured++ }))
				out, err := c.GetQuote(context.Background(), &GetQuoteInput{ResourceType: ResourceServer, Action: ActionResize})
				if size == 1<<20 && status == 200 {
					if err != nil || out == nil || out.OptimumPrice != 100 || captured != 1 {
						t.Fatalf("out = %+v, err = %v, captures = %d", out, err, captured)
					}
					return
				}
				var apiErr *vngcloud.APIError
				if out != nil || !errors.As(err, &apiErr) {
					t.Fatalf("out = %+v, err = %v, want nil and APIError", out, err)
				}
				if status != 200 && apiErr.StatusCode != status {
					t.Fatalf("status = %d, want %d", apiErr.StatusCode, status)
				}
				if status == 404 && !errors.Is(err, vngcloud.ErrNotFound) {
					t.Fatalf("err = %v, want ErrNotFound", err)
				}
				if size > 1<<20 && captured != 0 {
					t.Fatalf("oversized response captured %d times", captured)
				}
			})
		}
	}
}

func TestGetQuoteCancellationSynthetic(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		cancel()
		<-r.Context().Done()
	}))
	out, err := c.GetQuote(ctx, &GetQuoteInput{ResourceType: ResourceServer, Action: ActionResize})
	if out != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("out = %+v, err = %v, want nil and context.Canceled", out, err)
	}
}

// This test is the raw-evidence gate. The manager must add sanitized capture
// provenance (operation, HTTP status, region, request shape, replacement rules)
// and assess every unknown field when supplying the fixture. No fallback body.
func TestGetQuoteServerResizeCreditFixture(t *testing.T) {
	const path = "../testdata/pricing/GetQuoteServerResizeCredit.json"
	body, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		t.Skip("raw server credit fixture awaits manager capture and provenance")
	}
	if err != nil {
		t.Fatal(err)
	}
	if len(body) > 1<<20 {
		t.Fatal("raw fixture exceeds pricing response limit")
	}
	var raw struct {
		OptimumPrice    *float64 `json:"optimumPrice"`
		OriginalPrice   float64  `json:"originalPrice"`
		DiscountPrice   float64  `json:"discountPrice"`
		DiscountPercent float64  `json:"discountPercent"`
		Properties      []struct {
			Name            string   `json:"name"`
			Description     string   `json:"description"`
			OptimumPrice    float64  `json:"optimumPrice"`
			MonthlyPrice    float64  `json:"monthlyPrice"`
			CurrentPrice    *float64 `json:"currentPrice"`
			DiscountPercent float64  `json:"discountPercent"`
		} `json:"propertiesPrice"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatal(err)
	}
	if raw.OptimumPrice == nil || *raw.OptimumPrice >= 0 {
		t.Fatal("fixture must contain a negative numeric optimumPrice")
	}
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(body) }))
	out, err := c.GetQuote(context.Background(), &GetQuoteInput{ResourceType: ResourceServer, Action: ActionResize})
	if err != nil {
		t.Fatal(err)
	}
	want := &GetQuoteOutput{OptimumPrice: *raw.OptimumPrice, OriginalPrice: raw.OriginalPrice,
		DiscountPrice: raw.DiscountPrice, DiscountPercent: raw.DiscountPercent, Properties: make([]PriceProperty, len(raw.Properties))}
	for i, p := range raw.Properties {
		want.Properties[i] = PriceProperty{Name: p.Name, Description: p.Description, OptimumPrice: p.OptimumPrice,
			MonthlyPrice: p.MonthlyPrice, CurrentPrice: p.CurrentPrice, DiscountPercent: p.DiscountPercent}
	}
	if !reflect.DeepEqual(out, want) {
		t.Fatalf("decoded fields = %+v, want raw fields %+v", out, want)
	}
}
