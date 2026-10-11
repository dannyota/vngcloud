package volume

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/discovery"
	"danny.vn/vngcloud/internal/routes"
	"danny.vn/vngcloud/internal/transport"
	"danny.vn/vngcloud/pricing"
)

type ListVolumePricesInput struct {
	ZoneID       string
	VolumeTypeID string
	IOPS         int
	Size         int
	Encryption   string
	MaxRequests  int
	AllowPartial bool
}

type ListVolumePricesOutput struct {
	Items    []VolumePrice
	Metadata pricing.DiscoveryMetadata
}

type VolumePrice struct {
	VolumeType       VolumeType
	ZoneID           string
	ZoneName         string
	EncryptionTypeID string
	ZoneEnabled      *bool
	SizeGB           int
	PricePerGB       *float64
	Price            pricing.DiscoveryPrice
}

type volumeDiscovery struct {
	client  *Client
	session *discovery.Session
	out     *ListVolumePricesOutput
	errs    []error
	partial bool
}

// ListVolumePrices compares monthly provisioned volume prices at each type's
// minimum or the requested size. Quotes reserve no capacity or future price.
func (c *Client) ListVolumePrices(ctx context.Context, in *ListVolumePricesInput) (*ListVolumePricesOutput, error) {
	const op = "volume.ListVolumePrices"
	if in == nil {
		in = &ListVolumePricesInput{}
	}
	if err := validateVolumeDiscovery(in); err != nil {
		return nil, err
	}
	s, err := discovery.New(ctx, in.MaxRequests)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", core.ErrInvalidInput, err)
	}
	defer s.Close()
	d := volumeDiscovery{client: c, session: s, partial: in.AllowPartial, out: &ListVolumePricesOutput{Items: []VolumePrice{}, Metadata: pricing.DiscoveryMetadata{Region: c.c.Region(), Complete: true, Failures: []pricing.DiscoveryFailure{}, Notes: pricing.DiscoveryNotes()}}}
	d.out.Metadata.Notes = slices.DeleteFunc(d.out.Metadata.Notes, func(n pricing.PricingNote) bool {
		return n.Scope == "compute" || n.Scope == "network" || n.Scope == "object-storage"
	})
	d.out.Metadata.Notes = append(d.out.Metadata.Notes, pricing.PricingNote{Topic: "exclusions", Scope: "volume", Text: "Provisioned volume quotes exclude snapshots, backups, and server CES.", Evidence: "observed"})
	project, err := c.c.RequireDiscoveryProjectID(s)
	if err != nil {
		return nil, err
	}
	var zones []discoveryZone
	err = d.read("portal.ListZones", c.volumeURL("v1", []string{project, "zones"}, nil), func(status int, raw json.RawMessage) error {
		var e error
		zones, e = discoveryArray[discoveryZone](op, status, raw, "data")
		if e != nil {
			return e
		}
		seen := map[string]bool{}
		for _, z := range zones {
			if core.CheckPathID(op, "ZoneID", z.ID) != nil || z.Enabled == nil || seen[z.ID] {
				return invalidDiscovery(op, status)
			}
			seen[z.ID] = true
		}
		return s.AddRows(len(zones))
	})
	if err != nil {
		return nil, err
	}
	if in.ZoneID != "" && !slices.ContainsFunc(zones, func(z discoveryZone) bool { return z.ID == in.ZoneID }) {
		return nil, fmt.Errorf("%w: unknown ZoneID", core.ErrInvalidInput)
	}
	var parents []VolumeTypeZone
	err = d.read("volume.ListVolumeTypeZones", c.volumeURL("v1", []string{project, "volume_type_zones"}, nil), func(status int, raw json.RawMessage) error {
		var e error
		parents, e = discoveryArray[VolumeTypeZone](op, status, raw, "volumeTypeZones")
		if e != nil {
			return e
		}
		seen := map[string]bool{}
		for _, p := range parents {
			if core.CheckPathID(op, "VolumeTypeZoneID", p.ID) != nil || seen[p.ID] {
				return invalidDiscovery(op, status)
			}
			seen[p.ID] = true
		}
		return s.AddRows(len(parents))
	})
	if err != nil {
		return nil, err
	}
	var encryptions []EncryptionType
	err = d.read("volume.ListEncryptionTypes", c.volumeURL("v1", []string{project, "volumes", "encryption_types"}, nil), func(status int, raw json.RawMessage) error {
		var e error
		encryptions, e = discoveryArray[EncryptionType](op, status, raw, "")
		if e != nil {
			return e
		}
		seen := map[string]bool{}
		for _, v := range encryptions {
			if v.Key == "" || v.Key == "none" || v.Key == "all" || core.CheckTypeID(op, "Encryption", v.Key) != nil || seen[v.Key] {
				return invalidDiscovery(op, status)
			}
			seen[v.Key] = true
		}
		return s.AddRows(len(encryptions))
	})
	if err != nil {
		return nil, err
	}
	choices := []string{}
	if in.Encryption == "" || in.Encryption == "all" || in.Encryption == "none" {
		choices = append(choices, "")
	}
	for _, v := range encryptions {
		if in.Encryption == "" || in.Encryption == "all" || in.Encryption == v.Key {
			choices = append(choices, v.Key)
		}
	}
	for _, p := range parents {
		var types []VolumeType
		err = d.read("volume.ListVolumeTypes", c.volumeURL("v1", []string{project, p.ID, "volume_types"}, nil), func(status int, raw json.RawMessage) error {
			var e error
			types, e = discoveryArray[VolumeType](op, status, raw, "volumeTypes")
			if e != nil {
				return e
			}
			seen := map[string]bool{}
			for _, v := range types {
				id := v.VolumeTypeID
				if id == "" {
					id = v.ID
				}
				if core.CheckPathID(op, "VolumeTypeID", id) != nil || seen[id] || v.Name == "" || v.IOPS < 0 || v.MinSize <= 0 || v.MaxSize < v.MinSize || (v.VolumeTypeZoneID != "" && v.VolumeTypeZoneID != p.ID) {
					return invalidDiscovery(op, status)
				}
				seen[id] = true
			}
			return s.AddRows(len(types))
		})
		if err != nil {
			if !d.record("volume.ListVolumeTypes", p.ID, err) {
				return nil, err
			}
			if d.stop(err) {
				return d.finish()
			}
			continue
		}
		for _, v := range types {
			id := v.VolumeTypeID
			if id == "" {
				id = v.ID
			}
			if (in.VolumeTypeID != "" && in.VolumeTypeID != id) || (in.IOPS > 0 && in.IOPS != v.IOPS) {
				continue
			}
			if v.VolumeTypeZoneID == "" {
				v.VolumeTypeZoneID = p.ID
			}
			size := in.Size
			if size == 0 {
				size = v.MinSize
			}
			for _, z := range zones {
				if in.ZoneID != "" && in.ZoneID != z.ID {
					continue
				}
				for _, encryption := range choices {
					row := VolumePrice{VolumeType: v, ZoneID: z.ID, ZoneName: z.Name, ZoneEnabled: z.Enabled, EncryptionTypeID: encryption, SizeGB: size, Price: pricing.DiscoveryPrice{Status: "unknown", Source: "pricing.GetQuote"}}
					if !*z.Enabled || size < v.MinSize || size > v.MaxSize {
						row.Price.Status = "inapplicable"
						d.out.Metadata.Notes = append(d.out.Metadata.Notes, pricing.PricingNote{Topic: "inapplicable", Scope: z.ID + "/" + id, Text: "The zone is disabled or the requested size is outside the offering's bounds.", Evidence: "observed"})
					}
					if err := s.AddRows(1); err != nil {
						if !d.record(op, "rows", err) {
							return nil, err
						}
						return d.finish()
					}
					d.out.Items = append(d.out.Items, row)
				}
			}
		}
	}
	bodies := map[string]bool{}
	for _, row := range d.out.Items {
		if row.Price.Status == "unknown" {
			body, _ := volumeDiscoveryBody(row)
			raw, _ := json.Marshal(body)
			bodies[string(raw)] = true
		}
	}
	if err := s.Preflight(len(bodies)); err != nil {
		if !d.record(op, "quotes", err) {
			return nil, err
		}
		return d.finish()
	}
	for i := range d.out.Items {
		row := &d.out.Items[i]
		if row.Price.Status != "unknown" {
			continue
		}
		body, err := volumeDiscoveryBody(*row)
		if err != nil {
			return nil, err
		}
		var price pricing.DiscoveryPrice
		_, err = s.DoJSON(c.c, transport.Request{Operation: "pricing.GetQuote", Method: http.MethodPost, URL: c.c.RouteURL(routes.Route{Product: routes.ProductPortal, Version: "v1", Parts: []string{"price"}}), Body: body, OK: []int{200}}, nil, func(status int, raw json.RawMessage) error {
			var e error
			price, e = decodeVolumeDiscoveryPrice("pricing.GetQuote", status, raw)
			return e
		})
		if err != nil {
			row.Price.Status = "failed"
			if !d.record("pricing.GetQuote", row.ZoneID+"/"+row.VolumeType.VolumeTypeZoneID+"/"+volumePriceID(*row)+"/"+row.EncryptionTypeID, err) {
				return nil, err
			}
			if d.stop(err) {
				break
			}
			continue
		}
		row.Price = price
		rate := *price.MonthlyPrice / float64(row.SizeGB)
		row.PricePerGB = &rate
	}
	return d.finish()
}

func validateVolumeDiscovery(in *ListVolumePricesInput) error {
	const op = "volume.ListVolumePrices"
	if in.Size < 0 || in.IOPS < 0 {
		return fmt.Errorf("%w: Size and IOPS must be nonnegative", core.ErrInvalidInput)
	}
	for name, value := range map[string]string{"ZoneID": in.ZoneID, "VolumeTypeID": in.VolumeTypeID} {
		if value != "" {
			if err := core.CheckPathID(op, name, value); err != nil {
				return err
			}
		}
	}
	if in.Encryption != "" && in.Encryption != "all" && in.Encryption != "none" {
		return core.CheckTypeID(op, "Encryption", in.Encryption)
	}
	return nil
}

func volumePriceID(row VolumePrice) string {
	if row.VolumeType.VolumeTypeID != "" {
		return row.VolumeType.VolumeTypeID
	}
	return row.VolumeType.ID
}
func volumeDiscoveryBody(row VolumePrice) (map[string]any, error) {
	info, err := buildVolumeQuoteInfo("volume.ListVolumePrices", &CreateVolumeInput{ZoneID: row.ZoneID, VolumeTypeID: volumePriceID(row), Size: row.SizeGB, EncryptionTypeID: row.EncryptionTypeID})
	return map[string]any{"resourceType": "volume", "action": "create", "resourceInfo": info}, err
}
func (d *volumeDiscovery) read(op, url string, validate func(int, json.RawMessage) error) error {
	_, err := d.session.DoJSON(d.client.c, transport.Request{Operation: op, Method: http.MethodGet, URL: url, OK: []int{200}}, nil, validate)
	return err
}
func (d *volumeDiscovery) stop(err error) bool {
	return d.client.c.DiscoveryFatal(err) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, discovery.ErrLimit)
}
func (d *volumeDiscovery) record(op, key string, err error) bool {
	if !d.partial {
		return false
	}
	d.errs = append(d.errs, err)
	d.out.Metadata.Complete = false
	failure := pricing.DiscoveryFailure{Operation: op, ItemKey: key, Code: "DiscoveryIncomplete"}
	var api *core.APIError
	if errors.As(err, &api) {
		failure.Status = api.StatusCode
		failure.Code = core.ResolvedCode(api.StatusCode, "")
		if api.Code == "InvalidResponse" {
			failure.Code = api.Code
		}
	}
	d.out.Metadata.Failures = append(d.out.Metadata.Failures, failure)
	return true
}
func (d *volumeDiscovery) finish() (*ListVolumePricesOutput, error) {
	if len(d.errs) > 0 && len(d.out.Items) == 0 {
		return nil, d.errs[0]
	}
	slices.SortStableFunc(d.out.Items, func(a, b VolumePrice) int {
		return cmp.Or(cmp.Compare(a.ZoneID, b.ZoneID), cmp.Compare(a.VolumeType.Name, b.VolumeType.Name), cmp.Compare(volumePriceID(a), volumePriceID(b)), cmp.Compare(a.EncryptionTypeID, b.EncryptionTypeID))
	})
	d.out.Metadata.Requests = d.session.Requests()
	d.out.Metadata.QuotedAt = d.session.CompletedAt()
	if len(d.errs) > 0 {
		return d.out, &pricing.DiscoveryError{Errors: d.errs}
	}
	return d.out, nil
}
