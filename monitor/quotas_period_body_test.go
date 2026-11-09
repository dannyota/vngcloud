package monitor

import (
	"encoding/json"
	"testing"
)

func TestQuotaPeriodObjectBytes(t *testing.T) {
	for _, tc := range []struct {
		name, period, want string
	}{
		{"integer", `1`, `{"period":1}`},
		{"decimal", `1.0`, `{"period":1.0}`},
		{"exponent", `1e2`, `{"period":1e2}`},
		{"negative", `-1`, `{"period":-1}`},
		{"null", `null`, `{"period":null}`},
		{"boolean", `true`, `{"period":true}`},
		{"string", `"1"`, `{"period":"1"}`},
		{"escaped-string", `"\u003c\"\\\u0026"`, `{"period":"\u003c\"\\\u0026"}`},
		{"object", `{"x":1}`, `{"period":{"x":1}}`},
		{"array", `[1,2]`, `{"period":[1,2]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := string(quotaPeriodJSON(json.RawMessage(tc.period))); got != tc.want {
				t.Errorf("period object = %q, want %q", got, tc.want)
			}
		})
	}
}
