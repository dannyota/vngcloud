package volume

import (
	"encoding/json"
	"math"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/pricing"
)

func invalidDiscovery(op string, status int) error {
	return &core.APIError{Operation: op, StatusCode: status, Code: "InvalidResponse", Message: "invalid discovery response; body withheld"}
}

func discoveryArray[T any](op string, status int, raw json.RawMessage, key string) ([]T, error) {
	if key != "" {
		var fields map[string]json.RawMessage
		if json.Unmarshal(raw, &fields) != nil || fields == nil {
			return nil, invalidDiscovery(op, status)
		}
		for _, name := range []string{"page", "paging", "pagination", "pageSize", "totalPage", "totalPages", "totalItem", "next", "nextPage", "hasMore", "continuationToken"} {
			if _, ok := fields[name]; ok {
				return nil, invalidDiscovery(op, status)
			}
		}
		if success, ok := fields["success"]; ok && string(success) != "true" {
			return nil, invalidDiscovery(op, status)
		}
		raw = fields[key]
	}
	var values []T
	if len(raw) == 0 || json.Unmarshal(raw, &values) != nil || values == nil {
		return nil, invalidDiscovery(op, status)
	}
	return values, nil
}

type discoveryZone struct {
	ID      string `json:"uuid"`
	Name    string `json:"name"`
	Enabled *bool  `json:"isEnabled"`
}

func decodeVolumeDiscoveryPrice(op string, status int, raw json.RawMessage) (pricing.DiscoveryPrice, error) {
	var value struct {
		Optimum  *float64 `json:"optimumPrice"`
		Original *float64 `json:"originalPrice"`
		Discount *float64 `json:"discountPrice"`
		Percent  *float64 `json:"discountPercent"`
		Currency string   `json:"currency"`
		Tax      string   `json:"tax"`
	}
	if json.Unmarshal(raw, &value) != nil || value.Optimum == nil {
		return pricing.DiscoveryPrice{}, invalidDiscovery(op, status)
	}
	for _, p := range []*float64{value.Optimum, value.Original, value.Discount, value.Percent} {
		if p != nil && (*p < 0 || math.IsNaN(*p) || math.IsInf(*p, 0)) {
			return pricing.DiscoveryPrice{}, invalidDiscovery(op, status)
		}
	}
	if *value.Optimum <= 0 {
		return pricing.DiscoveryPrice{}, invalidDiscovery(op, status)
	}
	if value.Currency == "" {
		value.Currency = "VND"
	}
	return pricing.DiscoveryPrice{Status: "quoted", Basis: "monthly-total", Currency: value.Currency, Tax: value.Tax, Source: op, MonthlyPrice: value.Optimum, OriginalPrice: value.Original, DiscountPrice: value.Discount, DiscountPercent: value.Percent}, nil
}
