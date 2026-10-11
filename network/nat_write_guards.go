package network

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"reflect"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/transport"
)

type natPurchaseSpec struct {
	scope    natScope
	info     natResourceInfo
	offer    NATPackageOffer
	vpc      natPickerVPC
	baseline map[string]bool
}

type natPickerVPC struct {
	UUID        string  `json:"uuid"`
	ProjectUUID string  `json:"projectUuid"`
	RegionUUID  *string `json:"regionUuid"`
	RegionID    string  `json:"regionId"`
	Zones       []struct {
		UUID string `json:"uuid"`
	} `json:"zones"`
}

func (c *Client) natWriteAuth(op string) error {
	if c.c.Region() != "han-1" || !c.c.UsesIAMUserLogin() {
		return fmt.Errorf("%w: %s requires SDK IAM-user login in han-1", core.ErrInvalidConfig, op)
	}
	return nil
}
func checkNATCreateInput(op string, in *CreateNATInstanceInput) error {
	if err := core.CheckRequired(op, in); err != nil {
		return err
	}
	if math.IsNaN(in.MaxPrice) || math.IsInf(in.MaxPrice, 0) || in.MaxPrice < 0 {
		return fmt.Errorf("%w: %s requires finite nonnegative MaxPrice", core.ErrInvalidInput, op)
	}
	for _, id := range []struct{ name, value string }{{"ZoneID", in.ZoneID}, {"AvailabilityZoneID", in.AvailabilityZoneID}, {"PackageID", in.PackageID}, {"VPCID", in.VPCID}} {
		if err := core.CheckPathID(op, id.name, id.value); err != nil {
			return err
		}
	}
	return nil
}
func (c *Client) resolveNATPurchase(ctx context.Context, op string, in *CreateNATInstanceInput) (*natPurchaseSpec, error) {
	if err := checkNATCreateInput(op, in); err != nil {
		return nil, err
	}
	s, err := c.natScope(ctx, op, in.ZoneID)
	if err != nil {
		return nil, err
	}
	env, err := c.natExchange(ctx, transport.Request{Operation: op, Method: http.MethodGet, URL: s.collection("v3-whitelist"), OK: []int{200}}, nil)
	if err != nil {
		return nil, err
	}
	// The private model intentionally has no portal-user membership field.
	var whitelist struct {
		EnabledForAll *bool `json:"enabledForAll"`
	}
	if decodeNATEnvelope(env.Data, &whitelist, reflect.TypeFor[struct{}]()) != nil || whitelist.EnabledForAll == nil {
		return nil, natInvalid(op)
	}
	if !*whitelist.EnabledForAll {
		return nil, fmt.Errorf("%w: %s requires NAT V3 enabledForAll", core.ErrInvalidConfig, op)
	}
	zones, err := c.natZones(ctx, op, s)
	if err != nil {
		return nil, err
	}
	matches := 0
	for _, zone := range zones {
		if zone.UUID == in.AvailabilityZoneID {
			matches++
			if zone.ZoneType != "AVAILABILITY" || !zone.IsEnabled {
				return nil, fmt.Errorf("%w: %s requires an enabled AVAILABILITY zone", core.ErrInvalidInput, op)
			}
		}
	}
	if matches != 1 {
		return nil, fmt.Errorf("%w: %s requires one exact availability zone", core.ErrInvalidInput, op)
	}
	packages, err := c.natPackages(ctx, op, s, in.AvailabilityZoneID)
	if err != nil {
		return nil, err
	}
	var offer NATPackageOffer
	matches = 0
	for _, p := range packages {
		if p.UUID == in.PackageID {
			matches++
			offer = p
		}
	}
	if matches != 1 || offer.CurrencyUnit != "VND" {
		return nil, fmt.Errorf("%w: %s requires one exact VND package in the availability zone", core.ErrInvalidInput, op)
	}
	vpc, err := c.natGuardVPC(ctx, op, s, in.VPCID, in.AvailabilityZoneID)
	if err != nil {
		return nil, err
	}
	return &natPurchaseSpec{scope: s, offer: offer, vpc: *vpc, info: natResourceInfo{NATName: in.Name, PackageUUID: in.PackageID, VPCUUID: in.VPCID, RegionUUID: s.zone, ProjectUUID: s.project}}, nil
}

func natScan[T any](ctx context.Context, c *Client, op string, s natScope, resource string, decode func(json.RawMessage) ([]T, error), id func(T) string) ([]T, error) {
	items := []T{}
	seen := map[string]bool{}
	total, pages, size := -1, -1, -1
	for page := 1; page <= 50; page++ {
		parts := []string{"nats"}
		if resource != "" {
			parts = append(parts, resource)
		}
		env, err := c.natExchange(ctx, transport.Request{Operation: op, Method: http.MethodGet, URL: s.route("vnetwork/v1", parts, natParams(page)), OK: []int{200}}, nil)
		if err != nil {
			return nil, err
		}
		if env.Page == nil || *env.Page != page || env.Size == nil || *env.Size <= 0 || env.Total == nil || *env.Total < 0 || env.TotalPage == nil || *env.TotalPage < 0 || *env.TotalPage > 50 {
			return nil, natInvalid(op)
		}
		if page == 1 {
			total, pages, size = *env.Total, *env.TotalPage, *env.Size
		} else if total != *env.Total || pages != *env.TotalPage || size != *env.Size {
			return nil, natInvalid(op)
		}
		rows := []T{}
		if len(env.Data) == 0 {
			if total != 0 || pages != 0 {
				return nil, natInvalid(op)
			}
		} else {
			rows, err = decode(env.Data)
			if err != nil {
				return nil, err
			}
		}
		if len(rows) > size || (total == 0 && (len(rows) != 0 || pages != 0)) || (total > 0 && (pages == 0 || len(rows) == 0)) {
			return nil, natInvalid(op)
		}
		for _, row := range rows {
			key := id(row)
			if core.CheckPathID(op, "inventory ID", key) != nil || seen[key] {
				return nil, natInvalid(op)
			}
			seen[key] = true
			items = append(items, row)
		}
		if len(items) > total {
			return nil, natInvalid(op)
		}
		if page >= pages {
			if len(items) != total {
				return nil, natInvalid(op)
			}
			return items, nil
		}
	}
	return nil, natInvalid(op)
}
func (c *Client) natGuardVPC(ctx context.Context, op string, s natScope, vpcID, az string) (*natPickerVPC, error) {
	rows, err := natScan(ctx, c, op, s, "vpcs", func(raw json.RawMessage) ([]natPickerVPC, error) {
		return natDecodeRows[natPickerVPC](op, raw, "uuid", "projectUuid", "regionId", "zones")
	}, func(v natPickerVPC) string { return v.UUID })
	if err != nil {
		return nil, err
	}
	var found *natPickerVPC
	for _, v := range rows {
		if v.ProjectUUID != s.project || v.RegionID != s.zone || (v.RegionUUID != nil && *v.RegionUUID != s.zone) {
			return nil, natInvalid(op)
		}
		seen := map[string]bool{}
		for _, zone := range v.Zones {
			if core.CheckPathID(op, "picker zone", zone.UUID) != nil || seen[zone.UUID] {
				return nil, natInvalid(op)
			}
			seen[zone.UUID] = true
		}
		if v.UUID == vpcID {
			if az != "" && !seen[az] {
				return nil, fmt.Errorf("%w: %s: VPC does not list the availability zone", core.ErrInvalidInput, op)
			}
			found = &v
		}
	}
	if found != nil {
		return found, nil
	}
	return nil, fmt.Errorf("%w: %s: VPC absent from selected scope", core.ErrNotFound, op)
}
func (c *Client) natInventory(ctx context.Context, op string, s natScope) ([]NATInstance, error) {
	return natScan(ctx, c, op, s, "", func(raw json.RawMessage) ([]NATInstance, error) {
		rows, err := natDecodeRows[NATInstance](op, raw, "uuid", "natName", "status", "projectUuid", "zoneUuid", "vpc", "natPackage")
		if err != nil {
			return nil, err
		}
		for _, v := range rows {
			if v.NATName == "" || v.Status == "" || v.ProjectUUID != s.project || v.ZoneUUID == "" || core.CheckPathID(op, "NAT VPC", v.VPC.UUID) != nil || v.VPC.RegionID != s.zone || core.CheckPathID(op, "NAT package", v.NATPackage.UUID) != nil {
				return nil, natInvalid(op)
			}
		}
		return rows, nil
	}, func(v NATInstance) string { return v.UUID })
}

func (c *Client) guardNATBaseline(ctx context.Context, op string, s *natPurchaseSpec) error {
	before, err := c.natInventory(ctx, op, s.scope)
	if err != nil {
		return err
	}
	baseline := make(map[string]bool, len(before))
	for _, row := range before {
		baseline[row.UUID] = true
		if row.NATName == s.info.NATName || row.VPC.UUID == s.info.VPCUUID {
			return fmt.Errorf("%w: %s: NAT name or VPC already has a NAT", core.ErrInvalidInput, op)
		}
	}
	s.baseline = baseline
	return nil
}
