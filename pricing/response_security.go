package pricing

import (
	"bytes"
	"encoding/json"
	"io"
	"reflect"
	"strconv"
	"strings"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/transport"
)

// Keep unknown fields until credential checks finish, including JSON keys
// and values that typed decoding would discard or overwrite.
func decodeQuoteResponse(op string, raw json.RawMessage, credentials []string) (quoteResponse, error) {
	var resp quoteResponse
	if quoteReflectsCredentials(raw, credentials) {
		return resp, invalidQuoteResponse(op)
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &resp); err != nil {
			return quoteResponse{}, &core.APIError{Operation: op, Err: transport.SanitizeDecodeError(err)}
		}
	}
	// Check numbers after decoding, before a rejected price can be formatted
	// into an error. Exponent notation and rounding can change their text.
	if quoteDecodedReflectsCredentials(resp, credentials) {
		return quoteResponse{}, invalidQuoteResponse(op)
	}
	return resp, nil
}

func invalidQuoteResponse(op string) error {
	return &core.APIError{Operation: op, Code: "InvalidResponse", Message: "invalid quote response; body withheld"}
}

func quoteDecodedReflectsCredentials(value any, credentials []string) bool {
	raw, err := json.Marshal(value)
	return err != nil || quoteReflectsCredentials(raw, credentials) || quoteValueReflectsCredentials(reflect.ValueOf(value), credentials)
}

// JSON and Go format some numbers differently. Inspect decoded values as
// well as serialized output, following the server response checks.
func quoteValueReflectsCredentials(value reflect.Value, credentials []string) bool {
	var text string
	switch value.Kind() {
	case reflect.String:
		text = value.String()
	case reflect.Float32, reflect.Float64:
		text = strconv.FormatFloat(value.Float(), 'g', -1, value.Type().Bits())
	case reflect.Pointer, reflect.Interface:
		if !value.IsNil() {
			return quoteValueReflectsCredentials(value.Elem(), credentials)
		}
	case reflect.Struct:
		for i := range value.NumField() {
			if quoteValueReflectsCredentials(value.Field(i), credentials) {
				return true
			}
		}
	case reflect.Array, reflect.Slice:
		for i := range value.Len() {
			if quoteValueReflectsCredentials(value.Index(i), credentials) {
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

func quoteReflectsCredentials(raw []byte, credentials []string) bool {
	for _, credential := range credentials {
		if credential != "" && bytes.Contains(raw, []byte(credential)) {
			return true
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	for {
		token, err := decoder.Token()
		if err != nil {
			return err != io.EOF
		}
		if value, ok := token.(string); ok {
			for _, credential := range credentials {
				if credential != "" && strings.Contains(value, credential) {
					return true
				}
			}
		}
	}
}
