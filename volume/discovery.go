package volume

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/discovery"
	"danny.vn/vngcloud/internal/pricecatalog"
	"danny.vn/vngcloud/pricing"
)

// ListVolumePricesInput selects parameters for ListVolumePrices.
type ListVolumePricesInput struct {
	// ZoneID selects one returned zone; empty selects all zones in the configured region.
	ZoneID string
	// VolumeTypeID filters by the volume type ID when nonempty.
	VolumeTypeID string
	// IOPS filters by exact IOPS when positive; zero leaves it unbounded.
	IOPS int
	// Size sets the volume size; zero uses each offering minimum.
	Size int
	// Encryption selects all, none, or an encryption key; empty selects all.
	Encryption string
	// MaxRequests limits physical requests; zero selects 500 and the maximum is 2000.
	MaxRequests int
	// AllowPartial retains partial results and reports incomplete discovery when set.
	AllowPartial bool
}

// ListVolumePricesOutput holds the result of ListVolumePrices.
type ListVolumePricesOutput struct {
	Items    []VolumePrice
	Metadata pricing.DiscoveryMetadata
}

// VolumePrice holds a volume offering and its discovered price.
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
	zones, err := pricecatalog.Zones(c.c, s, project, op)
	if err != nil {
		return nil, err
	}
	if in.ZoneID != "" && !slices.ContainsFunc(zones, func(z pricecatalog.Zone) bool { return z.ID == in.ZoneID }) {
		return nil, fmt.Errorf("%w: unknown ZoneID", core.ErrInvalidInput)
	}
	parents, err := pricecatalog.VolumeParents(c.c, s, project, op)
	if err != nil {
		return nil, err
	}
	encryptions, err := pricecatalog.Encryptions(c.c, s, project, op)
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
		values, readErr := pricecatalog.VolumeTypes(c.c, s, project, p.ID, pricecatalog.VolumeTypeOptions{RequireDetails: true, ValidationOperation: op})
		err = readErr
		types := make([]VolumeType, 0, len(values))
		for _, v := range values {
			types = append(types, VolumeType(v))
		}
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
	type quoteGroup struct {
		body map[string]any
		rows []int
	}
	groups := []quoteGroup{}
	bodies := map[string]int{}
	for i, row := range d.out.Items {
		if row.Price.Status != "unknown" {
			continue
		}
		body, err := volumeDiscoveryBody(row)
		if err != nil {
			return nil, err
		}
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		key := string(raw)
		index, ok := bodies[key]
		if !ok {
			index = len(groups)
			bodies[key] = index
			groups = append(groups, quoteGroup{body: body})
		}
		groups[index].rows = append(groups[index].rows, i)
	}
	if err := s.Preflight(len(groups)); err != nil {
		if !d.record(op, "quotes", err) {
			return nil, err
		}
		return d.finish()
	}
	for _, group := range groups {
		var price pricing.DiscoveryPrice
		_, err = s.DoJSON(c.c, pricecatalog.RegionalQuote(c.c, "pricing.GetQuote", group.body), nil, func(status int, raw json.RawMessage) error {
			var e error
			price, e = decodeVolumeDiscoveryPrice("pricing.GetQuote", status, raw)
			return e
		})
		for _, i := range group.rows {
			row := &d.out.Items[i]
			if err != nil {
				row.Price.Status = "failed"
				if !d.record("pricing.GetQuote", row.ZoneID+"/"+row.VolumeType.VolumeTypeZoneID+"/"+volumePriceID(*row)+"/"+row.EncryptionTypeID, err) {
					return nil, err
				}
				continue
			}
			row.Price = price
			rate := *price.MonthlyPrice / float64(row.SizeGB)
			row.PricePerGB = &rate
		}
		if err != nil && d.stop(err) {
			break
		}
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
