package containerregistry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/jsonresponse"
	"danny.vn/vngcloud/internal/transport"
)

type registryPage[T any] struct {
	Data      []T  `json:"data"`
	ListData  []T  `json:"listData"`
	Page      int  `json:"page"`
	PageSize  int  `json:"pageSize"`
	TotalPage *int `json:"totalPage"`
	TotalItem *int `json:"totalItem"`
}

// UnmarshalJSON requires an explicit collection and consistent list metadata.
func (p *registryPage[T]) UnmarshalJSON(data []byte) error {
	type fields registryPage[T]
	var wire fields
	if err := jsonresponse.ValidateStrict(data, &wire); err != nil {
		return err
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(data, &keys); err != nil {
		return err
	}
	envelopes := 0
	for key := range keys {
		if strings.EqualFold(key, "data") || strings.EqualFold(key, "listData") {
			envelopes++
		}
	}
	if envelopes != 1 || wire.Data == nil && wire.ListData == nil || wire.TotalPage == nil || wire.TotalItem == nil {
		return errors.New("containerregistry: missing or conflicting list evidence")
	}
	*p = registryPage[T](wire)
	items := p.items()
	if p.Page < 0 || p.PageSize < 0 || *p.TotalPage < 0 || *p.TotalItem < 0 || len(items) > *p.TotalItem || (*p.TotalPage == 0 && *p.TotalItem != 0) || (*p.TotalItem == 0 && *p.TotalPage > 1) || (*p.TotalPage <= 1 && len(items) != *p.TotalItem) {
		return errors.New("containerregistry: invalid list metadata")
	}
	return nil
}

func (p *registryPage[T]) items() []T {
	if p.ListData != nil {
		return p.ListData
	}
	return p.Data
}

type registryPrivacy struct{ credentials []string }

func (p *registryPrivacy) request(ctx context.Context, c *Client, req transport.Request, out any) error {
	var sent []string
	defer func() { clear(sent) }()
	req.Sensitive = true
	req.StrictJSON = true
	req.MaxBody = 4 << 20
	req.Redact = append([]string(nil), p.credentials...)
	req.SentCredentials = &sent
	req.WithholdMessage = "registry response withheld"
	req.ClassifyError = func(status int, _ string) string {
		if code := core.ResolvedCode(status, ""); code != "" {
			return code
		}
		return "RequestFailed"
	}
	err := c.c.DoJSON(ctx, req, out)
	p.credentials = append(p.credentials, sent...)
	if err != nil {
		return err
	}
	if p.contains(reflect.ValueOf(out)) {
		return fmt.Errorf("%s: unsafe registry response", req.Operation)
	}
	return nil
}

func (p *registryPrivacy) contains(value reflect.Value) bool {
	switch value.Kind() {
	case reflect.String:
		for _, secret := range p.credentials {
			if secret != "" && strings.Contains(value.String(), secret) {
				return true
			}
		}
	case reflect.Pointer, reflect.Interface:
		return !value.IsNil() && p.contains(value.Elem())
	case reflect.Struct:
		for i := 0; i < value.NumField(); i++ {
			if value.Type().Field(i).IsExported() && p.contains(value.Field(i)) {
				return true
			}
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < value.Len(); i++ {
			if p.contains(value.Index(i)) {
				return true
			}
		}
	case reflect.Map:
		iter := value.MapRange()
		for iter.Next() {
			if p.contains(iter.Key()) || p.contains(iter.Value()) {
				return true
			}
		}
	}
	return false
}
