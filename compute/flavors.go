package compute

import (
	"context"
	"fmt"

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
	// FlavorZoneID lists the flavors of one flavor zone. Set exactly one of
	// FlavorZoneID and ZoneID.
	FlavorZoneID string
	// ZoneID lists the flavors of every flavor zone in this network zone:
	// one request for the flavor zones, then one per flavor zone.
	ZoneID string
	// Name keeps only flavors whose Name equals it exactly.
	Name string
}

type ListFlavorsOutput = core.List[Flavor]

// ListFlavors lists the flavors in one flavor zone, or in every flavor zone
// of one network zone. Rows follow the order of the flavor zone list, then
// the API's order inside each flavor zone, and each carries its
// FlavorZoneID. The first failing request ends the call with no partial
// result.
func (c *Client) ListFlavors(ctx context.Context, in *ListFlavorsInput) (*ListFlavorsOutput, error) {
	const op = "compute.ListFlavors"
	if in == nil || (in.FlavorZoneID == "") == (in.ZoneID == "") {
		return nil, fmt.Errorf("%w: %s requires exactly one of FlavorZoneID and ZoneID", core.ErrInvalidInput, op)
	}
	var flavorZoneIDs []string
	if in.FlavorZoneID != "" {
		flavorZoneIDs = []string{in.FlavorZoneID}
	} else {
		zones, err := c.ListFlavorZones(ctx, &ListFlavorZonesInput{ZoneID: in.ZoneID})
		if err != nil {
			return nil, err
		}
		for _, zone := range zones.Items {
			flavorZoneIDs = append(flavorZoneIDs, zone.ID)
		}
	}
	items := []Flavor{}
	for _, id := range flavorZoneIDs {
		flavors, err := c.listFlavorsInZone(ctx, id)
		if err != nil {
			return nil, err
		}
		for _, f := range flavors {
			if in.Name != "" && f.Name != in.Name {
				continue
			}
			if f.FlavorZoneID == "" {
				f.FlavorZoneID = id
			}
			if f.ZoneID == "" {
				f.ZoneID = in.ZoneID
			}
			items = append(items, f)
		}
	}
	return &ListFlavorsOutput{Items: items}, nil
}

func (c *Client) listFlavorsInZone(ctx context.Context, flavorZoneID string) ([]Flavor, error) {
	const op = "compute.ListFlavors"
	if err := core.CheckPathID(op, "FlavorZoneID", flavorZoneID); err != nil {
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
		URL:       c.computeURL("v1", []string{projectID, flavorZoneID, "flavors"}, nil),
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	return resp.Flavors, nil
}
