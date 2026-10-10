package network

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/routes"
	"danny.vn/vngcloud/internal/transport"
)

const listNATOperation = "network.ListNATInstances"

// ListNATInstances reads one page of Public NAT inventory in HCM or HAN.
func (c *Client) ListNATInstances(ctx context.Context, in *ListNATInstancesInput) (*ListNATInstancesOutput, error) {
	zoneID, page, size := "", 1, 10
	if in != nil {
		if in.ZoneID != "" {
			if err := core.CheckPathID(listNATOperation, "ZoneID", in.ZoneID); err != nil {
				return nil, err
			}
			zoneID = in.ZoneID
		}
		if in.Page < 0 || in.Size < 0 {
			return nil, fmt.Errorf("%w: %s requires nonnegative Page and Size", core.ErrInvalidInput, listNATOperation)
		}
		if in.Page > 0 {
			page = in.Page
		}
		if in.Size > 0 {
			size = in.Size
		}
	}
	if id := c.c.ProjectID(); id != "" {
		if err := core.CheckPathID(listNATOperation, "ProjectID", id); err != nil {
			return nil, err
		}
	}
	base, err := c.natEndpoint()
	if err != nil {
		return nil, err
	}
	projectID, err := c.c.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	if err := core.CheckPathID(listNATOperation, "ProjectID", projectID); err != nil {
		return nil, err
	}
	if zoneID == "" {
		zoneID, err = c.natZoneID(ctx, base)
		if err != nil {
			return nil, err
		}
	}
	params, _ := json.Marshal(struct {
		Search []string `json:"search"`
		Sort   struct{} `json:"sort"`
		Page   int      `json:"page"`
		Size   int      `json:"size"`
	}{Search: []string{}, Page: page, Size: size})
	var sentCredential string
	var resp natListResponse
	status, err := c.c.DoJSONStatus(ctx, transport.Request{
		Operation: listNATOperation, Method: http.MethodGet, SentCredential: &sentCredential,
		URL: routes.URL(fixedVNetEndpoint{base: base}, routes.Route{Product: routes.ProductVNet, Version: "vnetwork/v1", Parts: []string{zoneID, projectID, "nats"}, Query: url.Values{"params": {string(params)}}}), OK: []int{http.StatusOK},
	}, &resp)
	if err != nil {
		var apiErr *core.APIError
		if status == http.StatusOK && errors.As(err, &apiErr) {
			return nil, invalidNATResponse("invalid list envelope")
		}
		return nil, err
	}
	if resp.Success == nil {
		return nil, invalidNATResponse("missing success")
	}
	if !*resp.Success {
		message, code := c.c.RedactError(resp.Message, resp.Code, sentCredential)
		return nil, &core.APIError{Operation: listNATOperation, StatusCode: status, Code: code, Message: message}
	}
	if resp.Page == nil || *resp.Page <= 0 || resp.Size == nil || *resp.Size <= 0 || resp.TotalPage == nil || *resp.TotalPage < 0 || resp.Total == nil || *resp.Total < 0 {
		return nil, invalidNATResponse("invalid page metadata")
	}
	items := []NATInstance{}
	if len(resp.Data) == 0 {
		if *resp.TotalPage != 0 || *resp.Total != 0 {
			return nil, invalidNATResponse("missing data for nonempty list")
		}
	} else {
		if string(resp.Data) == "null" {
			return nil, invalidNATResponse("null data")
		}
		if err := decodeNATItems(resp.Data, &items); err != nil {
			return nil, invalidNATResponse("invalid NAT rows")
		}
	}
	for _, item := range items {
		if item.UUID == "" {
			return nil, invalidNATResponse("missing NAT uuid")
		}
	}
	return core.NewPagedList(items, *resp.Page, *resp.Size, *resp.TotalPage, *resp.Total), nil
}

type natListResponse struct {
	Success   *bool           `json:"success"`
	Data      json.RawMessage `json:"data"`
	Page      *int            `json:"page"`
	Size      *int            `json:"size"`
	TotalPage *int            `json:"totalPage"`
	Total     *int            `json:"total"`
	Code      string          `json:"code"`
	Message   string          `json:"message"`
}

func invalidNATResponse(message string) error {
	return &core.APIError{Operation: listNATOperation, StatusCode: http.StatusOK, Code: "InvalidResponse", Message: message}
}
