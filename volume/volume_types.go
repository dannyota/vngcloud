package volume

import (
	"context"
	"fmt"
	"net/url"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/transport"
)

type ListVolumeTypeZonesInput struct {
	ZoneID string
}

type ListVolumeTypeZonesOutput = core.List[VolumeTypeZone]

func (c *Client) ListVolumeTypeZones(ctx context.Context, in *ListVolumeTypeZonesInput) (*ListVolumeTypeZonesOutput, error) {
	projectID, err := c.c.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	q := url.Values{}
	if in != nil && in.ZoneID != "" {
		q.Set("zoneId", in.ZoneID)
	}

	var resp struct {
		VolumeTypeZones []VolumeTypeZone `json:"volumeTypeZones"`
	}
	if err := c.c.DoJSON(ctx, transport.Request{
		Operation: "volume.ListVolumeTypeZones",
		Method:    "GET",
		URL:       c.volumeURL("v1", []string{projectID, "volume_type_zones"}, q),
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	return &ListVolumeTypeZonesOutput{Items: resp.VolumeTypeZones}, nil
}

type ListVolumeTypesInput struct {
	// VolumeTypeZoneID lists the volume types of one volume type zone. At
	// most one of VolumeTypeZoneID and ZoneID may be set; with neither, the
	// call lists the project's volume types.
	VolumeTypeZoneID string
	// ZoneID lists the volume types of every volume type zone in this
	// network zone: one request for the volume type zones, then one per
	// zone.
	ZoneID string
	// IOPS keeps only volume types whose IOPS equals it. 0 means no filter.
	IOPS int
}

type ListVolumeTypesOutput = core.List[VolumeType]

// ListVolumeTypes lists volume types project-wide, in one volume type zone,
// or in every volume type zone of one network zone. Rows follow the order
// of the volume type zone list, then the API's order inside each zone. The
// first failing request ends the call with no partial result.
func (c *Client) ListVolumeTypes(ctx context.Context, in *ListVolumeTypesInput) (*ListVolumeTypesOutput, error) {
	const op = "volume.ListVolumeTypes"
	if in == nil {
		in = &ListVolumeTypesInput{}
	}
	if in.ZoneID != "" && in.VolumeTypeZoneID != "" {
		return nil, fmt.Errorf("%w: %s accepts at most one of VolumeTypeZoneID and ZoneID", core.ErrInvalidInput, op)
	}
	if in.IOPS < 0 {
		return nil, fmt.Errorf("%w: %s requires IOPS to be 0 or more", core.ErrInvalidInput, op)
	}

	zoneIDs := []string{in.VolumeTypeZoneID}
	if in.ZoneID != "" {
		zones, err := c.ListVolumeTypeZones(ctx, &ListVolumeTypeZonesInput{ZoneID: in.ZoneID})
		if err != nil {
			return nil, err
		}
		zoneIDs = zoneIDs[:0]
		for _, zone := range zones.Items {
			// The API's zoneId filter is not relied on.
			if zone.Zone.UUID == in.ZoneID {
				zoneIDs = append(zoneIDs, zone.ID)
			}
		}
	}

	items := []VolumeType{}
	for _, id := range zoneIDs {
		types, err := c.listVolumeTypesIn(ctx, id)
		if err != nil {
			return nil, err
		}
		for _, vt := range types {
			if in.IOPS > 0 && vt.IOPS != in.IOPS {
				continue
			}
			if id != "" && vt.VolumeTypeZoneID == "" {
				vt.VolumeTypeZoneID = id
			}
			if in.ZoneID != "" && vt.ZoneID == "" {
				vt.ZoneID = in.ZoneID
			}
			items = append(items, vt)
		}
	}
	return &ListVolumeTypesOutput{Items: items}, nil
}

// listVolumeTypesIn lists one volume type zone's types, or the project's
// when volumeTypeZoneID is empty.
func (c *Client) listVolumeTypesIn(ctx context.Context, volumeTypeZoneID string) ([]VolumeType, error) {
	const op = "volume.ListVolumeTypes"
	projectID, err := c.c.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	parts := []string{projectID, "volume_types"}
	if volumeTypeZoneID != "" {
		if err := core.CheckPathID(op, "VolumeTypeZoneID", volumeTypeZoneID); err != nil {
			return nil, err
		}
		parts = []string{projectID, volumeTypeZoneID, "volume_types"}
	}
	var resp struct {
		VolumeTypes []VolumeType `json:"volumeTypes"`
	}
	if err := c.c.DoJSON(ctx, transport.Request{
		Operation: op,
		Method:    "GET",
		URL:       c.volumeURL("v1", parts, nil),
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	return resp.VolumeTypes, nil
}
