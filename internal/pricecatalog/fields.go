//revive:disable:exported Internal contracts live in docs/design/cost-estimate.md.
package pricecatalog

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strings"

	"danny.vn/vngcloud/internal/jsonresponse"
)

type EnvelopeFields struct {
	Success           json.RawMessage `json:"success"`
	Code              json.RawMessage `json:"code"`
	ErrorCode         json.RawMessage `json:"errorCode"`
	ErrorMsg          json.RawMessage `json:"errorMsg"`
	Data              json.RawMessage `json:"data"`
	Datas             json.RawMessage `json:"datas"`
	Page              json.RawMessage `json:"page"`
	Paging            json.RawMessage `json:"paging"`
	Pagination        json.RawMessage `json:"pagination"`
	PageSize          json.RawMessage `json:"pageSize"`
	TotalPage         json.RawMessage `json:"totalPage"`
	TotalPages        json.RawMessage `json:"totalPages"`
	TotalItem         json.RawMessage `json:"totalItem"`
	Next              json.RawMessage `json:"next"`
	NextPage          json.RawMessage `json:"nextPage"`
	IsNext            json.RawMessage `json:"isNext"`
	HasMore           json.RawMessage `json:"hasMore"`
	ContinuationToken json.RawMessage `json:"continuationToken"`
	Images            json.RawMessage `json:"images"`
	VolumeTypes       json.RawMessage `json:"volumeTypes"`
	VolumeTypeZones   json.RawMessage `json:"volumeTypeZones"`
	Flavors           json.RawMessage `json:"flavors"`
	FlavorZones       json.RawMessage `json:"flavorZones"`
}

func ValidateFields(raw []byte, target any) error {
	if err := jsonresponse.Validate(raw); err != nil {
		return err
	}
	return exactFields(raw, reflect.TypeOf(target))
}

func exactFields(raw json.RawMessage, t reflect.Type) error {
	for t != nil && t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == nil || t == reflect.TypeFor[json.RawMessage]() {
		return nil
	}
	raw = bytes.TrimSpace(raw)
	switch t.Kind() {
	case reflect.Struct:
		if len(raw) == 0 || raw[0] != '{' {
			return nil
		}
		var values map[string]json.RawMessage
		if err := json.Unmarshal(raw, &values); err != nil {
			return err
		}
		fields := make(map[string]reflect.Type)
		responseFields(t, fields)
		for key, value := range values {
			child, ok := fields[key]
			if !ok {
				for name := range fields {
					if strings.EqualFold(name, key) {
						return errors.New("invalid response field name")
					}
				}
				continue
			}
			if err := exactFields(value, child); err != nil {
				return err
			}
		}
	case reflect.Slice, reflect.Array:
		if len(raw) == 0 || raw[0] != '[' {
			return nil
		}
		var values []json.RawMessage
		if err := json.Unmarshal(raw, &values); err != nil {
			return err
		}
		for _, value := range values {
			if err := exactFields(value, t.Elem()); err != nil {
				return err
			}
		}
	case reflect.Map:
		if len(raw) == 0 || raw[0] != '{' {
			return nil
		}
		var values map[string]json.RawMessage
		if err := json.Unmarshal(raw, &values); err != nil {
			return err
		}
		for _, value := range values {
			if err := exactFields(value, t.Elem()); err != nil {
				return err
			}
		}
	}
	return nil
}

func responseFields(t reflect.Type, fields map[string]reflect.Type) {
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		ft := field.Type
		if ft.Kind() == reflect.Pointer {
			ft = ft.Elem()
		}
		if !field.IsExported() && (!field.Anonymous || ft.Kind() != reflect.Struct) {
			continue
		}
		name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		if name == "-" {
			continue
		}
		if field.Anonymous && name == "" && ft.Kind() == reflect.Struct {
			responseFields(ft, fields)
			continue
		}
		if name == "" {
			name = field.Name
		}
		fields[name] = field.Type
	}
}
