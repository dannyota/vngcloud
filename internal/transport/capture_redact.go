package transport

import (
	"bytes"
	"encoding/json"
	"io"
)

// redactJSONCapture preserves the raw body unless a decoded string or key
// needs redaction. UseNumber keeps numeric text intact when JSON is rebuilt.
func redactJSONCapture(body []byte, values []string) ([]byte, bool) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) != nil {
		return nil, false
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF {
		return nil, false
	}
	// A map discards duplicate keys, so inspect every token before rebuilding.
	scan := json.NewDecoder(bytes.NewReader(body))
	scan.UseNumber()
	matched := false
	for {
		token, err := scan.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, false
		}
		if text, ok := token.(string); ok && redact(text, values) != text {
			matched = true
		}
	}
	cleaned, changed := redactJSONValue(value, values)
	if !changed && !matched {
		return append([]byte(nil), body...), true
	}
	encoded, err := json.Marshal(cleaned)
	if err != nil {
		return []byte(redactedText), true
	}
	return encoded, true
}

func redactJSONValue(value any, values []string) (any, bool) {
	switch v := value.(type) {
	case string:
		cleaned := redact(v, values)
		return cleaned, cleaned != v
	case []any:
		changed := false
		for i, item := range v {
			cleaned, replaced := redactJSONValue(item, values)
			v[i] = cleaned
			changed = changed || replaced
		}
		return v, changed
	case map[string]any:
		cleaned := make(map[string]any, len(v))
		changed := false
		for key, item := range v {
			cleanKey := redact(key, values)
			cleanValue, replaced := redactJSONValue(item, values)
			changed = changed || replaced || key != cleanKey
			cleaned[cleanKey] = cleanValue
		}
		return cleaned, changed
	default:
		return value, false
	}
}
