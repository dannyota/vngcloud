package floatingip

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"time"

	"danny.vn/vngcloud/internal/core"
)

// Property carries the existing pricing line fields without importing a service.
type Property struct {
	Name            string
	Description     string
	OptimumPrice    float64
	MonthlyPrice    float64
	CurrentPrice    *float64
	DiscountPercent float64
}

// Quote carries the existing pricing output fields inside shared internals.
type Quote struct {
	OptimumPrice    float64
	OriginalPrice   float64
	DiscountPrice   float64
	DiscountPercent float64
	Properties      []Property
}

// QuoteCreate validates the server quote and annotates its included floating IP.
func QuoteCreate(ctx context.Context, fetch func(context.Context) (*Quote, error)) (*Quote, error) {
	ctx = context.WithValue(ctx, componentKey{}, true)
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	server, err := fetch(ctx)
	if err != nil {
		return nil, err
	}
	if err := validateQuote(server); err != nil {
		return nil, err
	}
	server.Properties = append(server.Properties, Property{
		Name:        "FLOATING IP",
		Description: "Included in server order for the verified configuration",
	})
	return server, nil
}

func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0 }

func consistent(original, optimum, discount float64) bool {
	// Allow only a few representable floating-point steps, not a percentage
	// of the price that could hide a material discrepancy on a large quote.
	tolerance := 4 * (original - math.Nextafter(original, 0))
	return math.Abs((original-optimum)-discount) <= tolerance
}

func validateQuote(q *Quote) error {
	if q == nil {
		return InvalidResponse("pricing.GetQuote", nil)
	}
	if q.OptimumPrice <= 0 {
		return fmt.Errorf("%w: conservative monthly component estimate is not positive", core.ErrUnpriced)
	}
	if !finite(q.OptimumPrice) || !finite(q.OriginalPrice) || !finite(q.DiscountPrice) || !finite(q.DiscountPercent) || q.DiscountPercent > 100 || q.OriginalPrice < q.OptimumPrice || !consistent(q.OriginalPrice, q.OptimumPrice, q.DiscountPrice) {
		return InvalidResponse("pricing.GetQuote", nil)
	}
	if q.OriginalPrice > 0 && math.Abs(q.DiscountPercent-100*((q.OriginalPrice-q.OptimumPrice)/q.OriginalPrice)) > 1e-7 {
		return InvalidResponse("pricing.GetQuote", nil)
	}
	for _, p := range q.Properties {
		if !finite(p.OptimumPrice) || !finite(p.MonthlyPrice) || !finite(p.DiscountPercent) || p.DiscountPercent > 100 || (p.CurrentPrice != nil && !finite(*p.CurrentPrice)) {
			return InvalidResponse("pricing.GetQuote", nil)
		}
	}
	return nil
}

// ValidateComponentWire retains presence checks before the public pricing model
// loses the difference between missing totals and numeric zero.
func ValidateComponentWire(raw []byte) error {
	var wire struct {
		Optimum  *float64 `json:"optimumPrice"`
		Original *float64 `json:"originalPrice"`
		Discount *float64 `json:"discountPrice"`
		Percent  *float64 `json:"discountPercent"`
	}
	if json.Unmarshal(raw, &wire) != nil || wire.Optimum == nil || wire.Original == nil || wire.Discount == nil || wire.Percent == nil {
		return InvalidResponse("pricing.GetQuote", nil)
	}
	return validateQuote(&Quote{OptimumPrice: *wire.Optimum, OriginalPrice: *wire.Original, DiscountPrice: *wire.Discount, DiscountPercent: *wire.Percent})
}
