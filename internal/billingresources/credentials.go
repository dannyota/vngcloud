package billingresources

import (
	"bytes"
	"encoding/json"
	"io"
	"reflect"
	"regexp"
	"strconv"
	"strings"
)

var numericCredential = regexp.MustCompile(`^[+-]?([0-9]+\.?[0-9]*|\.[0-9]+)([eE][+-]?[0-9]+)?$`)

func reflectsCredentials(raw []byte, credentials []string) bool {
	for _, credential := range credentials {
		if credential != "" && (numericCredential.MatchString(strings.TrimSpace(credential)) || bytes.Contains(raw, []byte(credential))) {
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

func decodedCredentials(value reflect.Value, credentials []string) bool {
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
			return decodedCredentials(value.Elem(), credentials)
		}
	case reflect.Struct:
		for i := range value.NumField() {
			if decodedCredentials(value.Field(i), credentials) {
				return true
			}
		}
	case reflect.Array, reflect.Slice:
		for i := range value.Len() {
			if decodedCredentials(value.Index(i), credentials) {
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
