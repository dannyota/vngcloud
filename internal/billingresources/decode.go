package billingresources

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"

	"danny.vn/vngcloud/internal/jsonresponse"
)

func decodeExact(raw []byte, target any) error {
	if err := jsonresponse.Validate(raw); err != nil {
		return err
	}
	if err := exactNames(raw, reflect.TypeOf(target).Elem()); err != nil {
		return err
	}
	return json.Unmarshal(raw, target)
}

// encoding/json accepts case aliases that can overwrite guarded values.
func exactNames(raw json.RawMessage, t reflect.Type) error {
	if t == reflect.TypeFor[json.RawMessage]() {
		return nil
	}
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.Struct:
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			return err
		}
		for key, value := range fields {
			for i := range t.NumField() {
				field := t.Field(i)
				name := field.Tag.Get("json")
				if !strings.EqualFold(key, name) {
					continue
				}
				if key != name {
					return errors.New("unexpected billing field casing")
				}
				if err := exactNames(value, field.Type); err != nil {
					return err
				}
				break
			}
		}
	case reflect.Slice:
		var elements []json.RawMessage
		if err := json.Unmarshal(raw, &elements); err != nil {
			return err
		}
		for _, element := range elements {
			if err := exactNames(element, t.Elem()); err != nil {
				return err
			}
		}
	}
	return nil
}
