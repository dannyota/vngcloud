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

const provisionalDescription = "Conservative Elastic IP estimate; extra create charge unconfirmed"

// QuoteCreate prices the server followed by the zone-bearing Elastic IP reference.
func QuoteCreate(ctx context.Context, serverInfo map[string]any, zone string, fetch func(context.Context, string, map[string]any) (*Quote, error)) (*Quote, error) {
	const op = "compute.QuoteCreateServer"
	ctx = context.WithValue(ctx, componentKey{}, true)
	get := func(resource string, info map[string]any) (*Quote, error) {
		quoteCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		q, err := fetch(quoteCtx, resource, info)
		if err != nil {
			return nil, err
		}
		if err := validateQuote(q); err != nil {
			return nil, err
		}
		return q, nil
	}
	server, err := get("server", serverInfo)
	if err != nil {
		return nil, err
	}
	ip, err := get("elastic-ip", map[string]any{"period": 1, "isPoc": false, "zoneId": zone})
	if err != nil {
		return nil, err
	}
	out := &Quote{OptimumPrice: server.OptimumPrice + ip.OptimumPrice, OriginalPrice: server.OriginalPrice + ip.OriginalPrice, DiscountPrice: server.DiscountPrice + ip.DiscountPrice, Properties: append([]Property(nil), server.Properties...)}
	if !finite(out.OptimumPrice) || !finite(out.OriginalPrice) || !finite(out.DiscountPrice) {
		return nil, InvalidResponse(op, nil)
	}
	if out.OriginalPrice != 0 {
		out.DiscountPercent = 100 * ((out.OriginalPrice - out.OptimumPrice) / out.OriginalPrice)
	}
	for _, p := range ip.Properties {
		p.Name = "FLOATING IP: " + p.Name
		p.Description = provisionalDescription
		out.Properties = append(out.Properties, p)
	}
	if len(ip.Properties) == 0 {
		out.Properties = append(out.Properties, Property{Name: "FLOATING IP", Description: provisionalDescription, OptimumPrice: ip.OptimumPrice, MonthlyPrice: ip.OptimumPrice, DiscountPercent: ip.DiscountPercent})
	}
	if err := validateQuote(out); err != nil {
		return nil, err
	}
	return out, nil
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
