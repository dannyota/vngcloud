package compute

import (
	"encoding/json"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/pricecatalog"
	"danny.vn/vngcloud/internal/pricequote"
	"danny.vn/vngcloud/pricing"
)

func invalidFlavorDiscovery(op string, status int) error {
	return &core.APIError{Operation: op, StatusCode: status, Code: "InvalidResponse", Message: "invalid discovery response; body withheld"}
}

func flavorDiscoveryArray[T any](op string, status int, raw json.RawMessage, key string) ([]T, error) {
	return pricecatalog.Array[T](op, status, raw, key)
}

type flavorDiscoveryZone = pricecatalog.Zone

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
	value, err := pricequote.DecodeInstance(op, status, raw)
	return pricing.DiscoveryPrice(value), err
}

func decodeDiscoveryFlavors(op string, status int, raw json.RawMessage, key string) ([]Flavor, error) {
	entries, err := flavorDiscoveryArray[map[string]json.RawMessage](op, status, raw, key)
	if err != nil {
		return nil, err
	}
	records := raw
	if key != "" {
		var fields map[string]json.RawMessage
		if json.Unmarshal(raw, &fields) != nil {
			return nil, invalidFlavorDiscovery(op, status)
		}
		records = fields[key]
	}
	if pricecatalog.ValidateFields(records, (*[]Flavor)(nil)) != nil {
		return nil, invalidFlavorDiscovery(op, status)
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
