package volume

import (
	"encoding/json"

	"danny.vn/vngcloud/internal/pricequote"
	"danny.vn/vngcloud/pricing"
)

func decodeVolumeDiscoveryPrice(op string, status int, raw json.RawMessage) (pricing.DiscoveryPrice, error) {
	value, err := pricequote.DecodeMonthly(op, status, raw)
	return pricing.DiscoveryPrice(value), err
}
