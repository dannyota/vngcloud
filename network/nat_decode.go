package network

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
)

func decodeNATItems(raw []byte, items *[]NATInstance) error {
	if err := json.Unmarshal(raw, items); err != nil {
		return err
	}
	return checkNATNulls(raw, reflect.TypeFor[[]NATInstance]())
}

// encoding/json accepts null for scalars and structs. NAT allows null only
// on pointer fields; inspect only the public allowlist after typed decoding.
func checkNATNulls(raw json.RawMessage, t reflect.Type) error {
	if t.Kind() == reflect.Pointer {
		return nil
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return errors.New("unexpected null NAT field")
	}
	switch t.Kind() {
	case reflect.Struct:
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			return err
		}
		for i := range t.NumField() {
			field := t.Field(i)
			if value, ok := fields[field.Tag.Get("json")]; ok {
				if err := checkNATNulls(value, field.Type); err != nil {
					return err
				}
			}
		}
	case reflect.Slice:
		var elements []json.RawMessage
		if err := json.Unmarshal(raw, &elements); err != nil {
			return err
		}
		for _, element := range elements {
			if err := checkNATNulls(element, t.Elem()); err != nil {
				return err
			}
		}
	}
	return nil
}
