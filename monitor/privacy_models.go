package monitor

import (
	"reflect"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/transport"
)

func privateMonitorValue(value reflect.Value, secrets []string) bool {
	if !value.IsValid() || len(secrets) == 0 {
		return true
	}
	switch value.Kind() {
	case reflect.Pointer, reflect.Interface:
		return value.IsNil() || privateMonitorValue(value.Elem(), secrets)
	case reflect.String:
		text := value.String()
		return transport.RedactValues(text, secrets...) == text
	case reflect.Struct:
		for i := range value.NumField() {
			if value.Type().Field(i).IsExported() && !privateMonitorValue(value.Field(i), secrets) {
				return false
			}
		}
	case reflect.Slice, reflect.Array:
		for i := range value.Len() {
			if !privateMonitorValue(value.Index(i), secrets) {
				return false
			}
		}
	case reflect.Map:
		for entries := value.MapRange(); entries.Next(); {
			if !privateMonitorValue(entries.Key(), secrets) || !privateMonitorValue(entries.Value(), secrets) {
				return false
			}
		}
	}
	return true
}

func (p *monitorPrivacy) validate(op string, status int, full, ordinary any) error {
	if p.exhausted || !privateMonitorValue(reflect.ValueOf(full), p.credentials) || !privateMonitorValue(reflect.ValueOf(ordinary), p.modelValues) {
		return &core.APIError{Operation: op, StatusCode: status, Code: "InvalidResponse", Message: "response withheld because credential safety could not be confirmed"}
	}
	return nil
}

func (p *monitorPrivacy) check(op string, status int, check Check) error {
	ordinary := check
	ordinary.Config.Request = CheckRequest{Method: check.Config.Request.Method}
	return p.validate(op, status, check, ordinary)
}

func (p *monitorPrivacy) channel(op string, status int, channel Channel) error {
	ordinary := channel
	ordinary.Address, ordinary.Headers, ordinary.rawHeader = "", nil, ""
	full := struct {
		Channel Channel
		Header  string
	}{channel, channel.rawHeader}
	return p.validate(op, status, full, ordinary)
}
