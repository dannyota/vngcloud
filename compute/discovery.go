package compute

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

type ListFlavorPricesInput struct {
	ZoneID           string
	Family           string
	Platform         string
	Name             string
	MinRAM           int64
	MaxRAM           int64
	IncludeNonPublic bool
	AvailableOnly    bool
	MaxRequests      int
	AllowPartial     bool
}

type ListFlavorPricesOutput struct {
	Items    []FlavorPrice
	Metadata pricing.DiscoveryMetadata
}

type FlavorPrice struct {
	Flavor          Flavor
	ZoneID          string
	ZoneName        string
	Family          string
	FamilyName      string
	Platform        string
	Visibility      string
	RAMUnit         string
	ZoneEnabled     *bool
	RAMGiB          *float64
	PricePerGiB     *float64
	PricePerRAMUnit *float64
	Reference       *FlavorPriceReference
	Price           pricing.DiscoveryPrice
}

type FlavorPriceReference struct {
	ImageID        string
	ImageType      string
	ImageVersion   string
	RootDiskTypeID string
	RootDiskSizeGB int
}

type flavorDiscovery struct {
	client     *Client
	session    *discovery.Session
	out        *ListFlavorPricesOutput
	errs       []error
	partial    bool
	identities map[string]Flavor
	tuples     map[string]Flavor
}

// ListFlavorPrices quotes the instance component of a deterministic reference
// server. Raw RAM has no verified byte unit. Quotes reserve no capacity.
func (c *Client) ListFlavorPrices(ctx context.Context, in *ListFlavorPricesInput) (*ListFlavorPricesOutput, error) {
	if in == nil {
		in = &ListFlavorPricesInput{}
	}
	if err := validateFlavorDiscovery(in); err != nil {
		return nil, err
	}
	s, err := discovery.New(ctx, in.MaxRequests)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", core.ErrInvalidInput, err)
	}
	defer s.Close()
	d := flavorDiscovery{client: c, session: s, partial: in.AllowPartial, identities: map[string]Flavor{}, tuples: map[string]Flavor{}, out: &ListFlavorPricesOutput{Items: []FlavorPrice{}, Metadata: pricing.DiscoveryMetadata{Region: c.c.Region(), Complete: true, Failures: []pricing.DiscoveryFailure{}, Notes: pricing.DiscoveryNotes()}}}
	d.out.Metadata.Notes = slices.DeleteFunc(d.out.Metadata.Notes, func(n pricing.PricingNote) bool {
		return n.Scope == "storage" || n.Scope == "object-storage" || n.Scope == "network"
	})
	d.note("visibility", "compute", "Console-family scope excludes custom, partner, and legacy catalogs unless requested. Catalog visibility establishes no sale eligibility.")
	d.note("reference-component", "compute", "Each price is the instance component of its recorded reference configuration. The root disk and other excluded costs are not included.")
	project, err := c.c.RequireDiscoveryProjectID(s)
	if err != nil {
		return nil, err
	}
	zones, err := d.zones(project, in.ZoneID)
	if err != nil {
		return nil, err
	}
	platforms, err := d.platforms(project)
	if err != nil {
		return nil, err
	}
	for _, zone := range zones {
		if !*zone.Enabled {
			d.note("inapplicable", zone.ID, "The zone is disabled; discovery retains its catalog rows without quotes.")
		}
		if err = d.consoleCatalog(project, zone, platforms, in); err != nil {
			if !d.record("compute.ListFlavorPrices", zone.ID, err) {
				return nil, err
			}
			if d.stop(err) {
				return d.finish()
			}
		}
	}
	if in.IncludeNonPublic {
		if err = d.nonPublicCatalog(project, zones, in); err != nil {
			if !d.record("compute.ListFlavorPrices", "non-public", err) {
				return nil, err
			}
			if d.stop(err) {
				return d.finish()
			}
		}
	}
	if len(d.out.Items) == 0 && len(d.errs) == 0 {
		d.note("unmatched-scope", "compute", "No flavor matches the requested catalog scope and exact filters.")
	}
	if !slices.ContainsFunc(d.out.Items, func(row FlavorPrice) bool { return row.ZoneEnabled != nil && *row.ZoneEnabled }) {
		return d.finish()
	}
	types, err := d.volumeCatalog(project)
	if err != nil {
		if !d.record("volume.ListVolumeTypes", "reference-disks", err) {
			return nil, err
		}
		return d.finish()
	}
	for _, zone := range zones {
		if !*zone.Enabled {
			continue
		}
		images, e := d.images(project, zone.ID)
		if e != nil {
			if !d.record("compute.ListOSImages", zone.ID, e) {
				return nil, e
			}
			if d.stop(e) {
				return d.finish()
			}
			continue
		}
		for i := range d.out.Items {
			row := &d.out.Items[i]
			if row.ZoneID != zone.ID {
				continue
			}
			row.Reference, e = selectFlavorReference(s.Context(), row.Flavor, images, types)
			if e != nil {
				if !d.record("compute.ListFlavorPrices", flavorDiscoveryKey(*row), e) {
					return nil, e
				}
				return d.finish()
			}
			if row.Reference == nil {
				d.note("reference-unavailable", flavorDiscoveryKey(*row), "No supported unlicensed Ubuntu image and bounded 3000 IOPS root pair is available.")
			}
		}
	}
	if err = d.quotes(); err != nil {
		return nil, err
	}
	return d.finish()
}

func validateFlavorDiscovery(in *ListFlavorPricesInput) error {
	if in.MinRAM < 0 || in.MaxRAM < 0 || (in.MaxRAM > 0 && in.MinRAM > in.MaxRAM) {
		return fmt.Errorf("%w: invalid raw RAM bounds", core.ErrInvalidInput)
	}
	for name, value := range map[string]string{"ZoneID": in.ZoneID, "Family": in.Family, "Platform": in.Platform} {
		if value != "" {
			if err := core.CheckPathID("compute.ListFlavorPrices", name, value); err != nil {
				return err
			}
		}
	}
	return nil
}

func (d *flavorDiscovery) quotes() error {
	type group struct {
		body map[string]any
		rows []int
	}
	groups := []group{}
	seen := map[string]int{}
	for i, row := range d.out.Items {
		if row.Reference == nil || row.Price.Status == "inapplicable" {
			continue
		}
		body, err := flavorDiscoveryBody(row)
		if err != nil {
			return err
		}
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		key := string(raw)
		index, ok := seen[key]
		if !ok {
			index = len(groups)
			seen[key] = index
			groups = append(groups, group{body: body})
		}
		groups[index].rows = append(groups[index].rows, i)
	}
	if err := d.session.Preflight(len(groups)); err != nil {
		if !d.record("compute.ListFlavorPrices", "quotes", err) {
			return err
		}
		return nil
	}
	for _, group := range groups {
		var price pricing.DiscoveryPrice
		_, err := d.session.DoJSON(d.client.c, transport.Request{Operation: "pricing.GetQuote", Method: http.MethodPost, URL: d.client.c.RouteURL(routes.Route{Product: routes.ProductPortal, Version: "v1", Parts: []string{"price"}}), Body: group.body, OK: []int{200}}, nil, func(status int, raw json.RawMessage) error {
			var e error
			price, e = decodeFlavorDiscoveryPrice("pricing.GetQuote", status, raw)
			return e
		})
		for _, index := range group.rows {
			row := &d.out.Items[index]
			if err != nil {
				row.Price.Status = "failed"
				if !d.record("pricing.GetQuote", flavorDiscoveryKey(*row), err) {
					return err
				}
			} else {
				row.Price = price
				if row.Flavor.Memory > 0 {
					rate := *price.MonthlyPrice / float64(row.Flavor.Memory)
					row.PricePerRAMUnit = &rate
				}
			}
		}
		if err != nil && d.stop(err) {
			break
		}
	}
	return nil
}

func flavorDiscoveryKey(row FlavorPrice) string {
	return row.ZoneID + "/" + row.Flavor.FlavorZoneID + "/" + row.Flavor.FlavorID + "/" + row.Family + "/" + row.Platform
}
func (d *flavorDiscovery) read(op, url string, validate func(int, json.RawMessage) error) error {
	_, err := d.session.DoJSON(d.client.c, transport.Request{Operation: op, Method: http.MethodGet, URL: url, OK: []int{200}}, nil, validate)
	return err
}
func (d *flavorDiscovery) note(topic, scope, text string) {
	d.out.Metadata.Notes = append(d.out.Metadata.Notes, pricing.PricingNote{Topic: topic, Scope: scope, Text: text, Evidence: "observed"})
}
func (d *flavorDiscovery) stop(err error) bool {
	return d.client.c.DiscoveryFatal(err) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, discovery.ErrLimit)
}
func (d *flavorDiscovery) record(op, key string, err error) bool {
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
func (d *flavorDiscovery) finish() (*ListFlavorPricesOutput, error) {
	if len(d.errs) > 0 && len(d.out.Items) == 0 {
		return nil, d.errs[0]
	}
	slices.SortStableFunc(d.out.Items, func(a, b FlavorPrice) int {
		return cmp.Or(cmp.Compare(a.ZoneID, b.ZoneID), cmp.Compare(a.Family, b.Family), cmp.Compare(a.Platform, b.Platform), cmp.Compare(a.Flavor.Memory, b.Flavor.Memory), cmp.Compare(a.Flavor.CPU, b.Flavor.CPU), cmp.Compare(a.Flavor.FlavorID, b.Flavor.FlavorID))
	})
	d.out.Metadata.Requests = d.session.Requests()
	d.out.Metadata.QuotedAt = d.session.CompletedAt()
	if len(d.errs) > 0 {
		return d.out, &pricing.DiscoveryError{Errors: d.errs}
	}
	return d.out, nil
}
