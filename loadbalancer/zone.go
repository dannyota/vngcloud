package loadbalancer

import "encoding/json"

// UnmarshalJSON decodes the API's zone object into ZoneID. A flat zoneId is
// kept when the response has no zone object.
func (lb *LoadBalancer) UnmarshalJSON(data []byte) error {
	type plain LoadBalancer
	aux := struct {
		*plain
		Zone struct {
			UUID string `json:"uuid"`
		} `json:"zone"`
	}{plain: (*plain)(lb)}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	if aux.Zone.UUID != "" {
		lb.ZoneID = aux.Zone.UUID
	}
	return nil
}
