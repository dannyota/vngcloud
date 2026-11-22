package billing

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strconv"
	"strings"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/jsonresponse"
)

func billingInvalid(op string, status int, cause error) error {
	return &core.APIError{
		Operation: op, StatusCode: status, Code: "InvalidResponse",
		Message: "invalid billing response; body withheld", Err: historySafeCause(cause),
	}
}

func billingResponseCredentials(raw []byte, credentials []string) bool {
	if historyHasNumericCredential(credentials) {
		return true
	}
	for _, credential := range credentials {
		if credential != "" && bytes.Contains(raw, []byte(credential)) {
			return true
		}
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	for {
		token, err := d.Token()
		if err != nil {
			return err != io.EOF
		}
		value, ok := token.(string)
		if !ok {
			continue
		}
		for _, credential := range credentials {
			if credential != "" && strings.Contains(value, credential) {
				return true
			}
		}
	}
}

func billingDecodedCredentials(value reflect.Value, credentials []string) bool {
	if !value.IsValid() || value.Type() == reflect.TypeFor[json.RawMessage]() {
		return false
	}
	var text string
	switch value.Kind() {
	case reflect.String:
		text = value.String()
	case reflect.Float32, reflect.Float64:
		text = strconv.FormatFloat(value.Float(), 'g', -1, value.Type().Bits())
	case reflect.Pointer, reflect.Interface:
		if !value.IsNil() {
			return billingDecodedCredentials(value.Elem(), credentials)
		}
	case reflect.Struct:
		for i := range value.NumField() {
			if billingDecodedCredentials(value.Field(i), credentials) {
				return true
			}
		}
	case reflect.Array, reflect.Slice:
		for i := range value.Len() {
			if billingDecodedCredentials(value.Index(i), credentials) {
				return true
			}
		}
	}
	for _, credential := range credentials {
		if credential != "" && strings.Contains(text, credential) {
			return true
		}
	}
	return false
}

func validateBillingJSON(raw []byte) error {
	if !json.Valid(raw) {
		var target json.RawMessage
		return json.Unmarshal(raw, &target)
	}
	if err := jsonresponse.Validate(raw); err != nil {
		return err
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	return billingUniqueFields(d)
}

func billingUniqueFields(d *json.Decoder) error {
	token, err := d.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	keys := make(map[string]bool)
	for d.More() {
		if delim == '{' {
			token, err := d.Token()
			if err != nil {
				return err
			}
			key, ok := token.(string)
			if !ok {
				return errors.New("invalid billing field")
			}
			key = strings.Map(historyFoldRune, key)
			if keys[key] {
				return errors.New("ambiguous billing field")
			}
			keys[key] = true
		}
		if err := billingUniqueFields(d); err != nil {
			return err
		}
	}
	_, err = d.Token()
	return err
}

func billingRequired(raw json.RawMessage, nullable bool, names ...string) bool {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return false
	}
	for _, name := range names {
		value := fields[name]
		if len(value) == 0 || (!nullable && isJSONNull(value)) {
			return false
		}
	}
	return true
}

func billingDataValid(op string, data json.RawMessage) bool {
	if len(data) == 0 || isJSONNull(data) {
		return false
	}
	switch op {
	case "billing.ListBudgets", "billing.ListBudgetThresholds", "billing.ListBudgetAlerts":
		var rows []json.RawMessage
		if json.Unmarshal(data, &rows) != nil || rows == nil {
			return false
		}
		for _, row := range rows {
			if isJSONNull(row) {
				return false
			}
		}
		return true
	case "billing.GetBudget", "billing.CreateBudget", "billing.CreateBudgetThreshold":
		var fields struct {
			UUID string `json:"uuid"`
		}
		return billingRequired(data, false, "uuid") && json.Unmarshal(data, &fields) == nil && strings.TrimSpace(fields.UUID) != ""
	case "billing.GetCurrentPeriodCost":
		return billingRequired(data, false, "actualCost", "forecastedCost")
	case "billing.GetBalances":
		return billingRequired(data, true, "cash", "poc", "cashAvailable", "cashHolding", "pocHolding")
	case "billing.GetCostOverview":
		var fields map[string]json.RawMessage
		return json.Unmarshal(data, &fields) == nil && billingRequired(fields["summary"], false, "currentCost")
	case "billing.ListCostResources":
		var fields map[string]json.RawMessage
		if json.Unmarshal(data, &fields) != nil || !billingRequired(data, false, "items", "pagination", "summary") {
			return false
		}
		if !billingRequired(fields["pagination"], false, "page", "size", "total", "totalPages") ||
			!billingRequired(fields["summary"], false, "totalAll", "activeCount", "avgPerResource") {
			return false
		}
		var items []json.RawMessage
		if json.Unmarshal(fields["items"], &items) != nil || items == nil {
			return false
		}
		for _, item := range items {
			if !billingRequired(item, false, "totalCost") {
				return false
			}
		}
		return true
	default:
		return true
	}
}
