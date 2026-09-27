package core

import "encoding/json"

// QuoteResourceInfo marshals body, a paid write's own request body, into a
// map for pricing.GetQuoteInput.ResourceInfo, then sets "period" to 1 and
// "isPoc" to false: every quoted create sends these two keys regardless of
// resource type, and the billing gateway ignores any key it does not
// price. body must marshal to a JSON object; a field tagged omitempty and
// left at its zero value is absent from the result, the same as it would be
// on the wire.
func QuoteResourceInfo(body any) (map[string]any, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	info := make(map[string]any)
	if err := json.Unmarshal(data, &info); err != nil {
		return nil, err
	}
	info["period"] = 1
	info["isPoc"] = false
	return info, nil
}
