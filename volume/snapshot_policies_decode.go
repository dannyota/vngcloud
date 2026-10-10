package volume

import (
	"encoding/json"
	"strconv"
	"strings"
)

// Schedule numbers arrive as integral decimals. Convert without float rounding.
type snapshotInteger int

func (n *snapshotInteger) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		return nil
	}
	var number json.Number
	if len(data) == 0 || data[0] == '"' || json.Unmarshal(data, &number) != nil {
		return snapshotShapeError("volume.ListSnapshotPolicies")
	}
	mantissa, exponentText, hasExponent := strings.Cut(strings.ToLower(number.String()), "e")
	negative := strings.HasPrefix(mantissa, "-")
	mantissa = strings.TrimPrefix(mantissa, "-")
	whole, fraction, _ := strings.Cut(mantissa, ".")
	digits := strings.TrimLeft(whole+fraction, "0")
	if digits == "" {
		*n = 0
		return nil
	}
	exponent := 0
	if hasExponent {
		var err error
		exponent, err = strconv.Atoi(exponentText)
		if err != nil || exponent > len(mantissa)+20 || exponent < -len(mantissa)-20 {
			return snapshotShapeError("volume.ListSnapshotPolicies")
		}
	}
	scale := exponent - len(fraction)
	if scale < 0 {
		trim := -scale
		if trim >= len(digits) || strings.Trim(digits[len(digits)-trim:], "0") != "" {
			return snapshotShapeError("volume.ListSnapshotPolicies")
		}
		digits = digits[:len(digits)-trim]
	} else {
		if len(digits)+scale > 20 {
			return snapshotShapeError("volume.ListSnapshotPolicies")
		}
		digits += strings.Repeat("0", scale)
	}
	if negative {
		digits = "-" + digits
	}
	value, err := strconv.Atoi(digits)
	if err != nil {
		return snapshotShapeError("volume.ListSnapshotPolicies")
	}
	*n = snapshotInteger(value)
	return nil
}

func (c *SnapshotPolicyConfig) UnmarshalJSON(data []byte) error {
	type wire SnapshotPolicyConfig
	decoded := struct {
		*wire
		Hour   snapshotInteger `json:"hour"`
		Minute snapshotInteger `json:"minute"`
	}{wire: (*wire)(c)}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return snapshotShapeError("volume.ListSnapshotPolicies")
	}
	c.Hour = int(decoded.Hour)
	c.Minute = int(decoded.Minute)
	return nil
}

func (c *SnapshotPolicyHourlyConfig) UnmarshalJSON(data []byte) error {
	var decoded struct {
		Interval  *snapshotInteger `json:"interval"`
		Retention *snapshotInteger `json:"retention"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return snapshotShapeError("volume.ListSnapshotPolicies")
	}
	c.Interval = snapshotIntPointer(decoded.Interval)
	c.Retention = snapshotIntPointer(decoded.Retention)
	return nil
}

func (c *SnapshotPolicyDailyConfig) UnmarshalJSON(data []byte) error {
	var decoded struct {
		Retention *snapshotInteger `json:"retention"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return snapshotShapeError("volume.ListSnapshotPolicies")
	}
	c.Retention = snapshotIntPointer(decoded.Retention)
	return nil
}

func snapshotIntPointer(value *snapshotInteger) *int {
	if value == nil {
		return nil
	}
	result := int(*value)
	return &result
}
