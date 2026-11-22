package pricing

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
)

func quoteExactFields(raw json.RawMessage, typ reflect.Type) error {
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	switch typ.Kind() {
	case reflect.Struct:
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			return err
		}
		for key, value := range fields {
			for i := range typ.NumField() {
				field := typ.Field(i)
				name := field.Tag.Get("json")
				if !strings.EqualFold(key, name) {
					continue
				}
				if key != name {
					return errors.New("unexpected quote field casing")
				}
				if err := quoteExactFields(value, field.Type); err != nil {
					return err
				}
				break
			}
		}
	case reflect.Slice:
		var rows []json.RawMessage
		if err := json.Unmarshal(raw, &rows); err != nil {
			return err
		}
		for _, row := range rows {
			if err := quoteExactFields(row, typ.Elem()); err != nil {
				return err
			}
		}
	}
	return nil
}
