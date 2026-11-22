package core

import (
	"encoding/json"
	"errors"

	"danny.vn/vngcloud/internal/jsonresponse"
)

type flexibleEnvelope struct {
	ListData        json.RawMessage `json:"listData"`
	Data            json.RawMessage `json:"data"`
	Items           json.RawMessage `json:"items"`
	Content         json.RawMessage `json:"content"`
	Results         json.RawMessage `json:"results"`
	Projects        json.RawMessage `json:"projects"`
	Regions         json.RawMessage `json:"regions"`
	Zones           json.RawMessage `json:"zones"`
	Quotas          json.RawMessage `json:"quotas"`
	QuotaUsed       json.RawMessage `json:"quotaUsed"`
	VolumeTypes     json.RawMessage `json:"volumeTypes"`
	VolumeTypeZones json.RawMessage `json:"volumeTypeZones"`
	Page            json.RawMessage `json:"page"`
	PageSize        json.RawMessage `json:"pageSize"`
	Size            json.RawMessage `json:"size"`
	TotalPage       json.RawMessage `json:"totalPage"`
	TotalPages      json.RawMessage `json:"totalPages"`
	TotalItem       json.RawMessage `json:"totalItem"`
	TotalItems      json.RawMessage `json:"totalItems"`
	Total           json.RawMessage `json:"total"`
}

// DecodeFlexibleList requires one explicit array in a supported list shape.
func DecodeFlexibleList[T any](data []byte) ([]T, int, int, int, int, error) {
	invalid := errors.New("invalid list response")
	var direct []T
	if jsonresponse.ValidateStrict(data, &direct) == nil && json.Unmarshal(data, &direct) == nil && direct != nil {
		return direct, 0, 0, 0, len(direct), nil
	}
	var envelope flexibleEnvelope
	if jsonresponse.ValidateStrict(data, &envelope) != nil || json.Unmarshal(data, &envelope) != nil {
		return nil, 0, 0, 0, 0, invalid
	}
	var list json.RawMessage
	for _, candidate := range []json.RawMessage{envelope.ListData, envelope.Data, envelope.Items, envelope.Content, envelope.Results, envelope.Projects, envelope.Regions, envelope.Zones, envelope.Quotas, envelope.QuotaUsed, envelope.VolumeTypes, envelope.VolumeTypeZones} {
		if candidate != nil {
			if list != nil {
				return nil, 0, 0, 0, 0, invalid
			}
			list = candidate
		}
	}
	if list == nil || jsonresponse.ValidateStrict(list, &direct) != nil || json.Unmarshal(list, &direct) != nil || direct == nil {
		return nil, 0, 0, 0, 0, invalid
	}
	values := [4]int{}
	for i, fields := range [][]json.RawMessage{{envelope.Page}, {envelope.PageSize, envelope.Size}, {envelope.TotalPage, envelope.TotalPages}, {envelope.TotalItem, envelope.TotalItems, envelope.Total}} {
		seen := false
		for _, raw := range fields {
			if raw == nil {
				continue
			}
			var value *int
			if json.Unmarshal(raw, &value) != nil || value == nil || *value < 0 || seen && values[i] != *value {
				return nil, 0, 0, 0, 0, invalid
			}
			seen, values[i] = true, *value
		}
	}
	return direct, values[0], values[1], values[2], values[3], nil
}

// DecodePagedList requires listData and all four inventory pagination fields.
func DecodePagedList[T any](data []byte) (*PagedList[T], error) {
	var envelope flexibleEnvelope
	invalid := errors.New("incomplete inventory response")
	if jsonresponse.ValidateStrict(data, &envelope) != nil || json.Unmarshal(data, &envelope) != nil || envelope.Page == nil || envelope.PageSize == nil || envelope.TotalPage == nil || envelope.TotalItem == nil {
		return nil, invalid
	}
	items, page, size, totalPage, totalItem, err := DecodeFlexibleList[T](data)
	if err != nil || page < 1 || size < 1 || envelope.ListData == nil {
		return nil, invalid
	}
	return NewPagedList(items, page, size, totalPage, totalItem), nil
}
