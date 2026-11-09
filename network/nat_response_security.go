package network

import (
	"bytes"
	"encoding/json"
	"io"
	"reflect"
	"strings"
)

const natMaxBody = 4 << 20

func natReflectsCredential(raw []byte, credential string) bool {
	if credential == "" {
		return false
	}
	if bytes.Contains(raw, []byte(credential)) {
		return true
	}
	if !json.Valid(raw) {
		return true
	}
	// A JSON string can reflect the token through Unicode escapes.
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	for {
		token, err := decoder.Token()
		if err != nil {
			return err != io.EOF
		}
		if value, ok := token.(string); ok && strings.Contains(value, credential) {
			return true
		}
	}
}

// Inspect decoded fields too: derived strings can be absent from wire JSON.
func natValueReflectsCredentials(value reflect.Value, credentials []string) bool {
	switch value.Kind() {
	case reflect.String:
		for _, credential := range credentials {
			if credential != "" && strings.Contains(value.String(), credential) {
				return true
			}
		}
	case reflect.Pointer, reflect.Interface:
		if !value.IsNil() {
			return natValueReflectsCredentials(value.Elem(), credentials)
		}
	case reflect.Struct:
		for i := range value.NumField() {
			if natValueReflectsCredentials(value.Field(i), credentials) {
				return true
			}
		}
	case reflect.Array, reflect.Slice:
		for i := range value.Len() {
			if natValueReflectsCredentials(value.Index(i), credentials) {
				return true
			}
		}
	case reflect.Map:
		for rows := value.MapRange(); rows.Next(); {
			if natValueReflectsCredentials(rows.Key(), credentials) || natValueReflectsCredentials(rows.Value(), credentials) {
				return true
			}
		}
	}
	return false
}
