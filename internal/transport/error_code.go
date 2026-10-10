package transport

import (
	"bytes"
	"encoding/json"
	"io"
)

// ParseErrorCode accepts only JSON strings and numbers. Objects and arrays
// can hide escaped credentials, so they never supply an error code.
func ParseErrorCode(raw json.RawMessage) (string, bool) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) != nil {
		return "", false
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF {
		return "", false
	}
	switch v := value.(type) {
	case string:
		return v, true
	case json.Number:
		return v.String(), true
	default:
		return "", false
	}
}
