package compute

import (
	"context"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/transport"
)

// FlavorZone is one flavor group in a network zone, such as "General
// Purpose". ListFlavors takes its ID to list the flavors it groups.
type FlavorZone struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	ZoneID      string `json:"zoneId"`
}

type ListFlavorZonesInput struct {
	// ZoneID filters the result to flavor zones in this network zone. The
	// API returns every flavor zone regardless of network zone; the SDK
	// filters the result itself.
	ZoneID string
}

type ListFlavorZonesOutput = core.List[FlavorZone]

// ListFlavorZones lists every flavor zone, or only those in Input.ZoneID
// when it is set.
func (c *Client) ListFlavorZones(ctx context.Context, in *ListFlavorZonesInput) (*ListFlavorZonesOutput, error) {
	projectID, err := c.c.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	var resp struct {
		FlavorZones []FlavorZone `json:"flavorZones"`
	}
	if err := c.c.DoJSON(ctx, transport.Request{
		Operation: "compute.ListFlavorZones",
		Method:    "GET",
		URL:       c.computeURL("v1", []string{projectID, "flavor_zones", "product"}, nil),
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	items := resp.FlavorZones
	if in != nil && in.ZoneID != "" {
		filtered := make([]FlavorZone, 0, len(items))
		for _, zone := range items {
			if zone.ZoneID == in.ZoneID {
				filtered = append(filtered, zone)
			}
		}
		items = filtered
	}
	return &ListFlavorZonesOutput{Items: items}, nil
}

type ListFlavorsInput struct {
	FlavorZoneID string `vngcloud:"required"`
}

type ListFlavorsOutput = core.List[Flavor]

// ListFlavors lists the flavors in one flavor zone.
func (c *Client) ListFlavors(ctx context.Context, in *ListFlavorsInput) (*ListFlavorsOutput, error) {
	const op = "compute.ListFlavors"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "FlavorZoneID", in.FlavorZoneID); err != nil {
		return nil, err
	}
	projectID, err := c.c.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Flavors []Flavor `json:"flavors"`
	}
	if err := c.c.DoJSON(ctx, transport.Request{
		Operation: op,
		Method:    "GET",
		URL:       c.computeURL("v1", []string{projectID, in.FlavorZoneID, "flavors"}, nil),
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	return &ListFlavorsOutput{Items: resp.Flavors}, nil
}
