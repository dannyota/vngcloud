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

const listVPNOperation = "network.ListVPNConnections"

// ListVPNConnections reads one page of VPN inventory with inline sites and tunnels.
// Server error messages and codes are withheld because responses contain keys.
func (c *Client) ListVPNConnections(ctx context.Context, in *ListVPNConnectionsInput) (*ListVPNConnectionsOutput, error) {
	page, size := 1, 10
	if in != nil {
		if in.Page < 0 || in.Size < 0 {
			return nil, fmt.Errorf("%w: %s requires nonnegative Page and Size", core.ErrInvalidInput, listVPNOperation)
		}
		if in.Page > 0 {
			page = in.Page
		}
		if in.Size > 0 {
			size = in.Size
		}
	}
	if id := c.c.ProjectID(); id != "" {
		if err := core.CheckPathID(listVPNOperation, "ProjectID", id); err != nil {
			return nil, err
		}
	}
	origin := natRegionalOrigin(c.c.Region())
	if origin == "" {
		return nil, fmt.Errorf("%w: %s supports hcm-3 and han-1", core.ErrInvalidConfig, listVPNOperation)
	}
	base := origin + "/vnetwork-gateway/"
	if c.c.VNetworkOverride() {
		base = c.c.Endpoint(routes.ProductVNet)
	}
	projectID, err := c.c.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	if err := core.CheckPathID(listVPNOperation, "ProjectID", projectID); err != nil {
		return nil, err
	}
	params, _ := json.Marshal(struct {
		Search []vpnSearch `json:"search"`
		Sort   struct{}    `json:"sort"`
		Page   int         `json:"page"`
		Size   int         `json:"size"`
	}{Search: []vpnSearch{{Field: "any", Value: ""}}, Page: page, Size: size})
	var resp vpnListResponse
	status, err := c.c.DoJSONStatus(ctx, transport.Request{
		Operation: listVPNOperation, Method: http.MethodGet, Sensitive: true,
		WithholdMessage: "VPN request failed; server details withheld",
		URL:             routes.URL(fixedVNetEndpoint{base: base}, routes.Route{Product: routes.ProductVNet, Version: "vnetwork/v1", Parts: []string{projectID, "vpns"}, Query: url.Values{"params": {string(params)}}}), OK: []int{http.StatusOK},
	}, &resp)
	if err != nil {
		if status == http.StatusOK {
			return nil, invalidVPNResponse()
		}
		var apiErr *core.APIError
		if errors.As(err, &apiErr) {
			return nil, safeVPNError(apiErr.StatusCode, apiErr.Retryable, err)
		}
		return nil, err
	}
	if resp.Success == nil {
		return nil, invalidVPNResponse()
	}
	if !*resp.Success {
		return nil, safeVPNError(status, false, nil)
	}
	if resp.Page == nil || *resp.Page <= 0 || resp.Size == nil || *resp.Size <= 0 || resp.TotalPage == nil || *resp.TotalPage < 0 || resp.Total == nil || *resp.Total < 0 {
		return nil, invalidVPNResponse()
	}
	items := []VPNConnection{}
	if len(resp.Data) == 0 {
		if *resp.TotalPage != 0 || *resp.Total != 0 {
			return nil, invalidVPNResponse()
		}
	} else if err := decodeVPNItems(resp.Data, &items); err != nil {
		return nil, invalidVPNResponse()
	}
	for _, item := range items {
		if item.UUID == "" {
			return nil, invalidVPNResponse()
		}
	}
	return core.NewPagedList(items, *resp.Page, *resp.Size, *resp.TotalPage, *resp.Total), nil
}

type vpnSearch struct {
	Field string `json:"field"`
	Value string `json:"value"`
}

// Error envelope fields are deliberately absent so they cannot reach callers.
type vpnListResponse struct {
	Success   *bool           `json:"success"`
	Data      json.RawMessage `json:"data"`
	Page      *int            `json:"page"`
	Size      *int            `json:"size"`
	TotalPage *int            `json:"totalPage"`
	Total     *int            `json:"total"`
}

func invalidVPNResponse() error {
	return &core.APIError{Operation: listVPNOperation, StatusCode: http.StatusOK, Code: "InvalidResponse", Message: "invalid VPN response; body withheld"}
}

func safeVPNError(status int, retryable bool, original error) error {
	var cause error
	// Keep only shared sentinels, never a response-bearing error or its cause.
	for _, sentinel := range []error{core.ErrAuth, core.ErrPermission, core.ErrNotFound, core.ErrRateLimited, context.Canceled, context.DeadlineExceeded} {
		if errors.Is(original, sentinel) {
			cause = sentinel
			break
		}
	}
	return &core.APIError{Operation: listVPNOperation, StatusCode: status, Code: core.ResolvedCode(status, ""), Message: "VPN request failed; server details withheld", Retryable: retryable, Err: cause}
}
