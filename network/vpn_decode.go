package network

import (
	"encoding/json"
	"reflect"
)

func (r *vpnListResponse) UnmarshalJSON(raw []byte) error {
	type wire vpnListResponse
	return decodeNATEnvelope(raw, (*wire)(r), reflect.TypeFor[[]VPNConnection]())
}

func decodeVPNItems(raw []byte, items *[]VPNConnection) error {
	if err := json.Unmarshal(raw, items); err != nil {
		return err
	}
	return checkNATNulls(raw, reflect.TypeFor[[]VPNConnection]())
}
