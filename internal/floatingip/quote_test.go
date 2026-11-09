package floatingip

import (
	"context"
	"errors"
	"math"
	"testing"

	"danny.vn/vngcloud/internal/core"
)

func TestQuoteCheckedSumsAndFallback(t *testing.T) {
	calls := 0
	out, err := QuoteCreate(context.Background(), map[string]any{"zoneId": "zone-1"}, "zone-1", func(_ context.Context, resource string, info map[string]any) (*Quote, error) {
		calls++
		if calls == 1 {
			if resource != "server" {
				t.Error(resource)
			}
			return &Quote{OptimumPrice: 90, OriginalPrice: 100, DiscountPrice: 10, DiscountPercent: 10}, nil
		}
		if resource != "elastic-ip" || info["zoneId"] != "zone-1" || info["period"] != 1 || info["isPoc"] != false {
			t.Errorf("request %s %v", resource, info)
		}
		return &Quote{OptimumPrice: 45, OriginalPrice: 50, DiscountPrice: 5, DiscountPercent: 10}, nil
	})
	if err != nil || out.OptimumPrice != 135 || out.OriginalPrice != 150 || out.DiscountPrice != 15 || out.DiscountPercent != 10 || len(out.Properties) != 1 || out.Properties[0].Name != "FLOATING IP" || out.Properties[0].CurrentPrice != nil {
		t.Fatalf("out=%+v err=%v", out, err)
	}
}

func TestQuoteInvalidSumHasNoPartialOutput(t *testing.T) {
	for _, q := range []*Quote{{OptimumPrice: 0}, {OptimumPrice: -1}, {OptimumPrice: 1, OriginalPrice: 2}, {OptimumPrice: math.MaxFloat64, OriginalPrice: math.MaxFloat64}} {
		out, err := QuoteCreate(context.Background(), nil, "zone-1", func(context.Context, string, map[string]any) (*Quote, error) { return q, nil })
		if out != nil || err == nil {
			t.Fatalf("out=%v err=%v", out, err)
		}
		if q.OptimumPrice <= 0 && !errors.Is(err, core.ErrUnpriced) {
			t.Fatal(err)
		}
	}
}

func TestQuoteRejectsMaterialDiscountMismatchAtLargeTotals(t *testing.T) {
	out, err := QuoteCreate(context.Background(), nil, "zone-1", func(context.Context, string, map[string]any) (*Quote, error) {
		return &Quote{OptimumPrice: 1, OriginalPrice: 1e12, DiscountPrice: 1e12 - 2, DiscountPercent: 99.9999999999}, nil
	})
	if err == nil || out != nil {
		t.Fatalf("inconsistent component accepted: out=%v err=%v", out, err)
	}
}
