package core

import "encoding/json"

func DecodeFlexibleList[T any](data []byte) ([]T, int, int, int, int, error) {
	var direct []T
	if err := json.Unmarshal(data, &direct); err == nil && direct != nil {
		return direct, 0, 0, 0, len(direct), nil
	}
	var envelope struct {
		ListData        []T `json:"listData"`
		Data            []T `json:"data"`
		Items           []T `json:"items"`
		Content         []T `json:"content"`
		Results         []T `json:"results"`
		Projects        []T `json:"projects"`
		Regions         []T `json:"regions"`
		Zones           []T `json:"zones"`
		Quotas          []T `json:"quotas"`
		QuotaUsed       []T `json:"quotaUsed"`
		VolumeTypes     []T `json:"volumeTypes"`
		VolumeTypeZones []T `json:"volumeTypeZones"`
		Page            int `json:"page"`
		PageSize        int `json:"pageSize"`
		Size            int `json:"size"`
		TotalPage       int `json:"totalPage"`
		TotalPages      int `json:"totalPages"`
		TotalItem       int `json:"totalItem"`
		TotalItems      int `json:"totalItems"`
		Total           int `json:"total"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, 0, 0, 0, 0, err
	}
	items := envelope.ListData
	if items == nil {
		items = envelope.Data
	}
	if items == nil {
		items = envelope.Items
	}
	if items == nil {
		items = envelope.Content
	}
	if items == nil {
		items = envelope.Results
	}
	if items == nil {
		items = envelope.Projects
	}
	if items == nil {
		items = envelope.Regions
	}
	if items == nil {
		items = envelope.Zones
	}
	if items == nil {
		items = envelope.Quotas
	}
	if items == nil {
		items = envelope.QuotaUsed
	}
	if items == nil {
		items = envelope.VolumeTypes
	}
	if items == nil {
		items = envelope.VolumeTypeZones
	}
	pageSize := envelope.PageSize
	if pageSize == 0 {
		pageSize = envelope.Size
	}
	totalPage := envelope.TotalPage
	if totalPage == 0 {
		totalPage = envelope.TotalPages
	}
	totalItem := envelope.TotalItem
	if totalItem == 0 {
		totalItem = envelope.TotalItems
	}
	if totalItem == 0 {
		totalItem = envelope.Total
	}
	return items, envelope.Page, pageSize, totalPage, totalItem, nil
}
