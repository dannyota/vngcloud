package network

import (
	"encoding/json"
	"math"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/pricing"
)

func invalidNetworkDiscovery(op string, status int) error {
	return &core.APIError{Operation: op, StatusCode: status, Code: "InvalidResponse", Message: "invalid discovery response; body withheld"}
}

func decodeNetworkDiscoveryPrice(op string, status int, raw json.RawMessage) (pricing.DiscoveryPrice, error) {
	var value struct {
		Optimum  *float64 `json:"optimumPrice"`
		Original *float64 `json:"originalPrice"`
		Discount *float64 `json:"discountPrice"`
		Percent  *float64 `json:"discountPercent"`
	}
	if json.Unmarshal(raw, &value) != nil || value.Optimum == nil {
		return pricing.DiscoveryPrice{}, invalidNetworkDiscovery(op, status)
	}
	for _, p := range []*float64{value.Optimum, value.Original, value.Discount, value.Percent} {
		if p != nil && (*p < 0 || math.IsNaN(*p) || math.IsInf(*p, 0)) {
			return pricing.DiscoveryPrice{}, invalidNetworkDiscovery(op, status)
		}
	}
	// No evidenced free-entry catalog exists for these quote types.
	if *value.Optimum == 0 {
		return pricing.DiscoveryPrice{}, invalidNetworkDiscovery(op, status)
	}
	return pricing.DiscoveryPrice{Status: "quoted", Basis: "monthly-total", Currency: "VND", Tax: "as-returned", Source: op, MonthlyPrice: value.Optimum, OriginalPrice: value.Original, DiscountPrice: value.Discount, DiscountPercent: value.Percent}, nil
}
