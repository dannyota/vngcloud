package backup

import (
	"encoding/json"
	"errors"
	"math/big"
	"strconv"
	"strings"
)

type exactInt int

// The gateway uses decimal notation for whole numbers. Reject fractions and
// overflow rather than round them or change the public integer fields.
func (n *exactInt) UnmarshalJSON(data []byte) error {
	invalid := errors.New("invalid Backup Center integer")
	if len(data) == 0 || len(data) > 128 || data[0] == '"' {
		return invalid
	}
	var number json.Number
	if err := json.Unmarshal(data, &number); err != nil {
		return invalid
	}
	text := number.String()
	if i := strings.IndexAny(text, "eE"); i >= 0 {
		exponent, err := strconv.Atoi(text[i+1:])
		if err != nil || exponent < -64 || exponent > 64 {
			return invalid
		}
	}
	value, ok := new(big.Rat).SetString(text)
	if !ok || !value.IsInt() {
		return invalid
	}
	integer, err := strconv.Atoi(value.Num().String())
	if err != nil {
		return invalid
	}
	*n = exactInt(integer)
	return nil
}

func (c *PolicyConfig) UnmarshalJSON(data []byte) error {
	type plain PolicyConfig
	var aux struct {
		plain
		Hour   exactInt `json:"hour"`
		Minute exactInt `json:"minute"`
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return errors.New("invalid Backup Center policy configuration")
	}
	*c = PolicyConfig(aux.plain)
	c.Hour = int(aux.Hour)
	c.Minute = int(aux.Minute)
	return nil
}

func (c *DailyConfig) UnmarshalJSON(data []byte) error {
	var aux struct {
		Retention           *exactInt `json:"retention"`
		BackupType          *string   `json:"backupType"`
		IncrementalQuantity *exactInt `json:"incrementalQuantity"`
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return errors.New("invalid Backup Center daily configuration")
	}
	*c = DailyConfig{BackupType: aux.BackupType}
	if aux.Retention != nil {
		value := int(*aux.Retention)
		c.Retention = &value
	}
	if aux.IncrementalQuantity != nil {
		value := int(*aux.IncrementalQuantity)
		c.IncrementalQuantity = &value
	}
	return nil
}
