package billing

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
)

func billingExactFields(raw json.RawMessage, typ reflect.Type) error {
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	if typ == reflect.TypeFor[json.RawMessage]() {
		return nil
	}
	switch typ {
	case reflect.TypeFor[Alert]():
		typ = reflect.TypeFor[alertWire]()
	case reflect.TypeFor[Balances]():
		typ = reflect.TypeFor[balancesWire]()
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
				if !field.IsExported() {
					continue
				}
				name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
				if name == "" {
					name = field.Name
				}
				if !strings.EqualFold(key, name) {
					continue
				}
				if key != name {
					return errors.New("unexpected billing field casing")
				}
				if err := billingExactFields(value, field.Type); err != nil {
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
			if err := billingExactFields(row, typ.Elem()); err != nil {
				return err
			}
		}
	}
	return nil
}
