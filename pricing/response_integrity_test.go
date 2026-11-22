package pricing

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/floatingip"
	"danny.vn/vngcloud/internal/testutil"
)

func TestQuoteInvalidResponseKeepsStatus(t *testing.T) {
	for _, body := range []string{
		``, `null`, `{}`, `{"optimumPrice":null}`, `{"optimumPrice":"peer-canary"}`,
		`{"optimumPrice":`, `{"optimumPrice":-1}`,
		`{"optimumPrice":100,"optimumPrice":0}`,
		`{"optimumPrice":100,"OptimumPrice":0}`,
		`{"optimumPrice":100,"propertiesPrice":[{"monthlyPrice":100,"MonthlyPrice":0}]}`,
	} {
		t.Run(body, func(t *testing.T) {
			c := New(testutil.NewConfig(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) })))
			out, err := c.GetQuote(context.Background(), &GetQuoteInput{ResourceType: ResourceSnapshot})
			var api *core.APIError
			if out != nil || !errors.As(err, &api) || api.StatusCode != 200 || api.Code != "InvalidResponse" || api.Retryable {
				t.Fatal("invalid quote returned output or lost observed status and InvalidResponse")
			}
		})
	}
}

func TestQuoteRejectsCaseVariantFields(t *testing.T) {
	for _, field := range []string{"optimumPrice", "originalPrice", "discountPrice", "discountPercent", "propertiesPrice", "name", "description", "monthlyPrice", "currentPrice"} {
		t.Run(field, func(t *testing.T) {
			body := `{"optimumPrice":100,"originalPrice":100,"discountPrice":0,"discountPercent":0,"propertiesPrice":[{"name":"disk","description":"synthetic","monthlyPrice":100,"currentPrice":100}]}`
			body = strings.Replace(body, `"`+field+`"`, `"`+strings.ToUpper(field)+`"`, 1)
			c := New(testutil.NewConfig(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) })))
			out, err := c.GetQuote(context.Background(), &GetQuoteInput{ResourceType: ResourceSnapshot})
			var api *core.APIError
			if out != nil || !errors.As(err, &api) || api.Code != "InvalidResponse" || api.StatusCode != 200 {
				t.Fatal("case-variant quote field returned data or lost classification")
			}
		})
	}
}

func TestQuoteComponentPreservesUnpriced(t *testing.T) {
	c := New(testutil.NewConfig(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"optimumPrice":0,"originalPrice":0,"discountPrice":0,"discountPercent":0}`))
	})))
	_, err := floatingip.QuoteCreate(context.Background(), true, func(ctx context.Context, _ bool) (*floatingip.Quote, error) {
		_, err := c.GetQuote(ctx, &GetQuoteInput{ResourceType: ResourceServer})
		return nil, err
	})
	if !errors.Is(err, core.ErrUnpriced) {
		t.Fatal("component quote lost the price guard sentinel")
	}
}

func TestQuoteRefusesOperationRedirect(t *testing.T) {
	for _, status := range []int{307, 308} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			var quotes, mutations atomic.Int32
			c := New(testutil.NewRetryConfig(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/mutate" {
					mutations.Add(1)
					_, _ = w.Write([]byte(`{"optimumPrice":1}`))
					return
				}
				quotes.Add(1)
				w.Header().Set("Location", "/mutate")
				w.WriteHeader(status)
			})))
			out, err := c.GetQuote(context.Background(), &GetQuoteInput{ResourceType: ResourceSnapshot})
			if out != nil || err == nil || quotes.Load() != 1 || mutations.Load() != 0 {
				t.Fatal("quote redirect sent a mutation or returned success")
			}
		})
	}
}
