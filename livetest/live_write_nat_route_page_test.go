//go:build livewrite

package livetest_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/jsonresponse"
	"danny.vn/vngcloud/internal/routes"
	"danny.vn/vngcloud/internal/transport"
	"danny.vn/vngcloud/network"
)

func liveNATRoutePage(ctx context.Context, cfg vngcloud.Config, page int) (*core.PagedList[network.RouteTable], error) {
	client := core.ClientOf(cfg)
	project, err := client.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	if core.CheckPathID("NAT route inventory", "project", project) != nil {
		return nil, errors.New("invalid route project")
	}
	query := url.Values{"name": {""}, "page": {strconv.Itoa(page)}, "size": {"1000"}}
	endpoint := routes.URL(client, routes.Route{Product: routes.ProductVServer, Version: "v2", Parts: []string{project, "route-table"}, Query: query})
	request := transport.Request{Operation: "network.ListRouteTables", Method: http.MethodGet, URL: endpoint, OK: []int{http.StatusOK}, Sensitive: true, NoRedirect: true, MaxBody: 8 << 20, WithholdMessage: "NAT route response withheld"}
	var raw json.RawMessage
	if err := client.DoJSON(ctx, request, &raw); err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return nil, errors.New("invalid route page")
	}
	for _, name := range []string{"listData", "page", "pageSize", "totalPage", "totalItem"} {
		if _, ok := fields[name]; !ok {
			return nil, errors.New("invalid route page")
		}
	}
	var wire struct {
		Items     []network.RouteTable `json:"listData"`
		Page      *int                 `json:"page"`
		Size      *int                 `json:"pageSize"`
		TotalPage *int                 `json:"totalPage"`
		TotalItem *int                 `json:"totalItem"`
	}
	if jsonresponse.ValidateStrict(raw, &wire) != nil || json.Unmarshal(raw, &wire) != nil || wire.Items == nil || wire.Page == nil || wire.Size == nil || wire.TotalPage == nil || wire.TotalItem == nil {
		return nil, errors.New("invalid route page")
	}
	return core.NewPagedList(wire.Items, *wire.Page, *wire.Size, *wire.TotalPage, *wire.TotalItem), nil
}
