package network

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strings"

	"danny.vn/vngcloud/internal/jsonresponse"
)

func (r *natListResponse) UnmarshalJSON(raw []byte) error {
	type wire natListResponse
	return decodeNATEnvelope(raw, (*wire)(r), reflect.TypeFor[[]NATInstance]())
}

type natRegionsResponse struct {
	Success *bool           `json:"success"`
	Data    json.RawMessage `json:"data"`
}

func (r *natRegionsResponse) UnmarshalJSON(raw []byte) error {
	type wire natRegionsResponse
	return decodeNATEnvelope(raw, (*wire)(r), reflect.TypeFor[[]VNetworkRegion]())
}

func decodeNATEnvelope(raw []byte, target any, dataType reflect.Type) error {
	if err := jsonresponse.Validate(raw); err != nil {
		return err
	}
	if err := checkNATFieldNames(raw, reflect.TypeOf(target).Elem(), dataType); err != nil {
		return err
	}
	return json.Unmarshal(raw, target)
}

// Require exact wire names wherever encoding/json would accept a case alias.
func checkNATFieldNames(raw json.RawMessage, t, dataType reflect.Type) error {
	if t == reflect.TypeFor[json.RawMessage]() {
		t = dataType
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
					return errors.New("unexpected NAT field casing")
				}
				if err := checkNATFieldNames(value, field.Type, dataType); err != nil {
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
			if err := checkNATFieldNames(element, t.Elem(), dataType); err != nil {
				return err
			}
		}
	}
	return nil
}

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
