package network

import (
	"context"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/transport"
)

func (c *Client) ListSubnets(ctx context.Context, _ *ListSubnetsInput) (*ListSubnetsOutput, error) {
	vpcs, err := c.ListVPCs(ctx, nil)
	if err != nil {
		return nil, err
	}
	items := make([]Subnet, 0)
	for _, vpc := range vpcs.Items {
		subnets, err := c.ListSubnetsByVPC(ctx, &ListSubnetsByVPCInput{VPCID: vpc.UUID})
		if err != nil {
			return nil, err
		}
		items = append(items, subnets.Items...)
	}
	return &ListSubnetsOutput{Items: items}, nil
}

func (c *Client) ListSubnetsByVPC(ctx context.Context, in *ListSubnetsByVPCInput) (*ListSubnetsByVPCOutput, error) {
	if err := core.CheckRequired("network.ListSubnetsByVPC", in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID("network.ListSubnetsByVPC", "VPCID", in.VPCID); err != nil {
		return nil, err
	}
	projectID, err := c.c.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	var resp []subnetResponse
	if err := c.c.DoJSON(ctx, transport.Request{
		Operation: "network.ListSubnetsByVPC",
		Method:    "GET",
		URL:       c.networkURL([]string{projectID, "networks", in.VPCID, "subnets"}, nil),
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	items := make([]Subnet, 0, len(resp))
	for _, item := range resp {
		items = append(items, item.toSubnet())
	}
	return &ListSubnetsByVPCOutput{Items: items}, nil
}

func (c *Client) GetVPC(ctx context.Context, in *GetVPCInput) (*GetVPCOutput, error) {
	if err := core.CheckRequired("network.GetVPC", in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID("network.GetVPC", "VPCID", in.VPCID); err != nil {
		return nil, err
	}
	projectID, err := c.c.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	// Unlike GetSecurityGroup, this endpoint returns the VPC fields at the
	// top level, not wrapped in a "data" object.
	var resp VPC
	if err := c.c.DoJSON(ctx, transport.Request{
		Operation: "network.GetVPC",
		Method:    "GET",
		URL:       c.networkURL([]string{projectID, "networks", in.VPCID}, nil),
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	return &GetVPCOutput{VPC: resp}, nil
}

func (c *Client) GetSubnet(ctx context.Context, in *GetSubnetInput) (*GetSubnetOutput, error) {
	if err := core.CheckRequired("network.GetSubnet", in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID("network.GetSubnet", "VPCID", in.VPCID); err != nil {
		return nil, err
	}
	if err := core.CheckPathID("network.GetSubnet", "SubnetID", in.SubnetID); err != nil {
		return nil, err
	}
	projectID, err := c.c.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	// Like GetVPC, and unlike GetSecurityGroup, this endpoint returns the
	// subnet fields at the top level, not wrapped in a "data" object.
	var resp subnetResponse
	if err := c.c.DoJSON(ctx, transport.Request{
		Operation: "network.GetSubnet",
		Method:    "GET",
		URL:       c.networkURL([]string{projectID, "networks", in.VPCID, "subnets", in.SubnetID}, nil),
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	return &GetSubnetOutput{Subnet: resp.toSubnet()}, nil
}
