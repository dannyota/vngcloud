package network

import (
	"context"
	"net/http"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/transport"
)

func (c *Client) ListDHCPOptions(ctx context.Context, in *ListDHCPOptionsInput) (*ListDHCPOptionsOutput, error) {
	name, page, size := "", 0, 0
	if in != nil {
		name, page, size = in.Name, in.Page, in.Size
	}
	var resp listDHCPOptionsResponse
	if err := c.list(ctx, "network.ListDHCPOptions", []string{"dhcp_option"}, name, page, size, &resp); err != nil {
		return nil, err
	}
	return core.NewPagedList(resp.ListData, resp.Page, resp.PageSize, resp.TotalPage, resp.TotalItem), nil
}

// GetDHCPOptions reads a DHCP options set. Unlike the security group and
// route table reads, and like GetVPC and GetSubnet, the set's fields decode
// at the top level, not wrapped in a "data" object (live).
func (c *Client) GetDHCPOptions(ctx context.Context, in *GetDHCPOptionsInput) (*GetDHCPOptionsOutput, error) {
	const op = "network.GetDHCPOptions"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "DHCPOptionsID", in.DHCPOptionsID); err != nil {
		return nil, err
	}
	projectID, err := c.c.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	var resp DHCPOptions
	if err := c.c.DoJSON(ctx, transport.Request{
		Operation: op,
		Method:    http.MethodGet,
		URL:       c.networkURL([]string{projectID, "dhcp_option", in.DHCPOptionsID}, nil),
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	return &GetDHCPOptionsOutput{DHCPOptions: resp}, nil
}
