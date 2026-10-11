package compute

import (
	"encoding/json"
	"math"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/pricing"
)

func invalidFlavorDiscovery(op string, status int) error {
	return &core.APIError{Operation: op, StatusCode: status, Code: "InvalidResponse", Message: "invalid discovery response; body withheld"}
}

func flavorDiscoveryArray[T any](op string, status int, raw json.RawMessage, key string) ([]T, error) {
	if key != "" {
		var fields map[string]json.RawMessage
		if json.Unmarshal(raw, &fields) != nil || fields == nil {
			return nil, invalidFlavorDiscovery(op, status)
		}
		for _, name := range []string{"page", "paging", "pagination", "pageSize", "totalPage", "totalPages", "totalItem", "next", "nextPage", "isNext", "hasMore", "continuationToken"} {
			if _, ok := fields[name]; ok {
				return nil, invalidFlavorDiscovery(op, status)
			}
		}
		if v, ok := fields["success"]; ok && string(v) != "true" {
			return nil, invalidFlavorDiscovery(op, status)
		}
		raw = fields[key]
	}
	var values []T
	if len(raw) == 0 || json.Unmarshal(raw, &values) != nil || values == nil {
		return nil, invalidFlavorDiscovery(op, status)
	}
	return values, nil
}

type flavorDiscoveryZone struct {
	ID      string `json:"uuid"`
	Name    string `json:"name"`
	Enabled *bool  `json:"isEnabled"`
}

type flavorDiscoveryPlatform struct {
	Key         string `json:"key"`
	Value       string `json:"value"`
	Description string `json:"description"`
}

type flavorDiscoveryFamily struct {
	flavorDiscoveryPlatform
	Condition struct {
		Codes []string `json:"codes"`
	} `json:"condition"`
	// Types retain catalog evidence without inventing a flavor-to-type join.
	Types json.RawMessage `json:"types"`
}

func decodeFlavorDiscoveryPrice(op string, status int, raw json.RawMessage) (pricing.DiscoveryPrice, error) {
	var value struct {
		Properties []struct {
			Name    string   `json:"name"`
			Monthly *float64 `json:"monthlyPrice"`
			Optimum *float64 `json:"optimumPrice"`
			Percent *float64 `json:"discountPercent"`
		} `json:"propertiesPrice"`
	}
	if json.Unmarshal(raw, &value) != nil {
		return pricing.DiscoveryPrice{}, invalidFlavorDiscovery(op, status)
	}
	price := pricing.DiscoveryPrice{Status: "quoted", Basis: "instance-type-line", Currency: "VND", Tax: "as-returned", Source: op}
	matches := 0
	for _, line := range value.Properties {
		if line.Name != "INSTANCE TYPE" {
			continue
		}
		matches++
		if line.Monthly == nil || line.Optimum == nil || *line.Monthly <= 0 || *line.Monthly != *line.Optimum {
			return pricing.DiscoveryPrice{}, invalidFlavorDiscovery(op, status)
		}
		for _, amount := range []*float64{line.Monthly, line.Optimum, line.Percent} {
			if amount != nil && (*amount < 0 || math.IsNaN(*amount) || math.IsInf(*amount, 0)) {
				return pricing.DiscoveryPrice{}, invalidFlavorDiscovery(op, status)
			}
		}
		price.MonthlyPrice = line.Monthly
		price.DiscountPercent = line.Percent
	}
	if matches != 1 {
		return pricing.DiscoveryPrice{}, invalidFlavorDiscovery(op, status)
	}
	return price, nil
}

func decodeDiscoveryFlavors(op string, status int, raw json.RawMessage, key string) ([]Flavor, error) {
	entries, err := flavorDiscoveryArray[map[string]json.RawMessage](op, status, raw, key)
	if err != nil {
		return nil, err
	}
	values := make([]Flavor, 0, len(entries))
	for _, entry := range entries {
		if entry == nil {
			return nil, invalidFlavorDiscovery(op, status)
		}
		for _, name := range []string{"cpu", "memory", "remainingVms", "isSoldOut"} {
			if value, ok := entry[name]; !ok || string(value) == "null" {
				return nil, invalidFlavorDiscovery(op, status)
			}
		}
		var metadata string
		if json.Unmarshal(entry["metaData"], &metadata) != nil {
			metadata = ""
		}
		delete(entry, "metaData")
		encoded, err := json.Marshal(entry)
		if err != nil {
			return nil, err
		}
		var value Flavor
		if json.Unmarshal(encoded, &value) != nil {
			return nil, invalidFlavorDiscovery(op, status)
		}
		value.Metadata = metadata
		values = append(values, value)
	}
	return values, nil
}
