package network

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"time"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/transport"
)

type endpointOffer struct{ zone, category, service, serviceName, pkg, packageName string }

type endpointRow struct {
	UUID              string `json:"uuid"`
	EndpointName      string `json:"endpointName"`
	EndpointServiceID string `json:"endpointServiceId"`
	VPCID             string `json:"vpcId"`
	Status            string `json:"status"`
	EndpointType      string `json:"endpointType"`
	ZoneUUID          string `json:"zoneUuid"`
	EnableDNSName     bool   `json:"enableDnsName"`
	Service           struct {
		UUID string `json:"uuid"`
		Name string `json:"name"`
	} `json:"service"`
	Subnet struct {
		UUID string `json:"uuid"`
	} `json:"subnet"`
	PackageID struct {
		UUID string `json:"uuid"`
	} `json:"packageId"`
	Project struct {
		VServerProjectID string `json:"VServerProjectId"`
	} `json:"project"`
	resource, domains json.RawMessage
}

func endpointRows[T any](o *endpointOp, env *natEnvelope, required ...string) ([]T, error) {
	rows, err := natDecodeRows[T](o.op, env.Data, required...)
	if err != nil {
		return nil, endpointSafeError(o.op, err)
	}
	return rows, nil
}

func (o *endpointOp) catalogRead(ctx context.Context, parts []string, search []endpointFilter, extra url.Values) (*natEnvelope, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var all []json.RawMessage
	seen := map[string]bool{}
	total, pages, size := -1, -1, -1
	for page := 1; page <= 50; page++ {
		query := endpointParams(page, search)
		for key, values := range extra {
			query[key] = values
		}
		raw, _, err := o.raw(ctx, transport.Request{Method: http.MethodGet, URL: o.route("vnetwork/v1", append([]string{"endpoints"}, parts...), query), OK: []int{http.StatusOK}})
		if err != nil {
			return nil, err
		}
		var env natEnvelope
		var fields map[string]json.RawMessage
		if decodeNATEnvelope(raw, &env, reflect.TypeFor[map[string]json.RawMessage]()) != nil || env.Success == nil || !*env.Success || json.Unmarshal(raw, &fields) != nil {
			return nil, endpointInvalid(o.op)
		}
		for _, name := range []string{"nextPage", "next", "hasMore", "nextToken", "nextMarker", "pagination"} {
			if _, ok := fields[name]; ok {
				return nil, endpointInvalid(o.op)
			}
		}
		_, hasPage := fields["page"]
		_, hasSize := fields["size"]
		_, hasTotal := fields["total"]
		_, hasPages := fields["totalPage"]
		if page == 1 && !hasPage && !hasSize && !hasTotal && !hasPages {
			return &env, nil
		}
		if env.Page == nil || *env.Page != page || env.Size == nil || *env.Size <= 0 || env.Total == nil || *env.Total < 0 || env.TotalPage == nil || *env.TotalPage < 0 || *env.TotalPage > 50 {
			return nil, endpointInvalid(o.op)
		}
		if page == 1 {
			total, pages, size = *env.Total, *env.TotalPage, *env.Size
			count := total / size
			if total%size != 0 {
				count++
			}
			if count != pages {
				return nil, endpointInvalid(o.op)
			}
		} else if total != *env.Total || pages != *env.TotalPage || size != *env.Size {
			return nil, endpointInvalid(o.op)
		}
		var rows []json.RawMessage
		if len(env.Data) == 0 && total == 0 && pages == 0 {
			rows = []json.RawMessage{}
		} else if json.Unmarshal(env.Data, &rows) != nil || rows == nil {
			return nil, endpointInvalid(o.op)
		}
		expected := size
		if page == pages {
			expected = total - size*(pages-1)
		}
		if total == 0 {
			expected = 0
		}
		if len(rows) != expected {
			return nil, endpointInvalid(o.op)
		}
		for _, row := range rows {
			var id struct {
				UUID string `json:"uuid"`
			}
			if json.Unmarshal(row, &id) != nil || core.CheckPathID(o.op, "catalog ID", id.UUID) != nil || seen[id.UUID] {
				return nil, endpointInvalid(o.op)
			}
			seen[id.UUID] = true
			all = append(all, row)
		}
		if page >= pages {
			if len(all) != total {
				return nil, endpointInvalid(o.op)
			}
			if all == nil {
				all = []json.RawMessage{}
			}
			env.Data, err = json.Marshal(all)
			if err != nil {
				return nil, endpointInvalid(o.op)
			}
			return &env, nil
		}
	}
	return nil, endpointInvalid(o.op)
}

func (o *endpointOp) resolveOffer(ctx context.Context, in *CreateEndpointInput) (endpointOffer, error) {
	refuse := func(what string) (endpointOffer, error) {
		return endpointOffer{}, fmt.Errorf("%w: %s: %s", core.ErrInvalidInput, o.op, what)
	}
	env, err := o.catalogRead(ctx, []string{"zones"}, nil, nil)
	if err != nil {
		return endpointOffer{}, err
	}
	zones, err := endpointRows[struct {
		UUID      string `json:"uuid"`
		ZoneType  string `json:"zoneType"`
		IsEnabled bool   `json:"isEnabled"`
	}](o, env, "uuid", "zoneType", "isEnabled")
	if err != nil {
		return endpointOffer{}, err
	}
	zone := in.AvailabilityZoneID
	if zone == "" {
		zone = endpointZone
	}
	matches := 0
	var selectedZone struct {
		UUID      string `json:"uuid"`
		ZoneType  string `json:"zoneType"`
		IsEnabled bool   `json:"isEnabled"`
	}
	for _, z := range zones {
		if z.UUID == zone {
			matches++
			selectedZone = z
		}
	}
	if matches != 1 || core.CheckPathID(o.op, "availability zone", zone) != nil || !selectedZone.IsEnabled || selectedZone.ZoneType != "AVAILABILITY" {
		return refuse("zone is not eligible exactly once")
	}

	if env, err = o.catalogRead(ctx, []string{"categories"}, nil, nil); err != nil {
		return endpointOffer{}, err
	}
	categories, err := endpointRows[struct {
		UUID      string `json:"uuid"`
		IsDefault bool   `json:"isDefault"`
	}](o, env, "uuid", "isDefault")
	if err != nil {
		return endpointOffer{}, err
	}
	offer := endpointOffer{zone: zone}
	matches = 0
	for _, category := range categories {
		if (in.CategoryID != "" && category.UUID == in.CategoryID) || (in.CategoryID == "" && category.IsDefault) {
			matches++
			offer.category = category.UUID
		}
	}
	categoryIDs := 0
	for _, category := range categories {
		if category.UUID == offer.category {
			categoryIDs++
		}
	}
	if matches != 1 || categoryIDs != 1 || (in.CategoryID == "" && len(categories) != 1) || !hex24Pattern.MatchString(offer.category) {
		return refuse("category is not offered exactly once")
	}

	if env, err = o.catalogRead(ctx, []string{offer.category, "services"}, nil, url.Values{"zoneUuid": {offer.zone}}); err != nil {
		return endpointOffer{}, err
	}
	services, err := endpointRows[struct {
		UUID         string `json:"uuid"`
		Name         string `json:"name"`
		EndpointType string `json:"endpointType"`
	}](o, env, "uuid", "name", "endpointType")
	if err != nil {
		return endpointOffer{}, err
	}
	matches = 0
	var selectedServiceType string
	for _, s := range services {
		if (in.ServiceID != "" && s.UUID == in.ServiceID) || (in.ServiceID == "" && s.Name == endpointService) {
			matches++
			offer.service, offer.serviceName, selectedServiceType = s.UUID, s.Name, s.EndpointType
		}
	}
	idMatches := 0
	for _, s := range services {
		if s.UUID == offer.service {
			idMatches++
		}
	}
	if matches != 1 || idMatches != 1 || !uuidPattern.MatchString(offer.service) || strings.TrimSpace(offer.serviceName) == "" || selectedServiceType != "Gateway" {
		return refuse("service is not offered exactly once")
	}

	if env, err = o.catalogRead(ctx, []string{offer.service, "packages"}, nil, nil); err != nil {
		return endpointOffer{}, err
	}
	packages, err := endpointRows[struct {
		UUID         string `json:"uuid"`
		Name         string `json:"name"`
		ServiceName  string `json:"serviceName"`
		CurrencyUnit string `json:"currencyUnit"`
	}](o, env, "uuid", "name", "serviceName", "currencyUnit")
	if err != nil {
		return endpointOffer{}, err
	}
	matches = 0
	var selectedService, selectedCurrency string
	for _, p := range packages {
		if (in.PackageID != "" && p.UUID == in.PackageID) || (in.PackageID == "" && p.Name == endpointPackage) {
			matches++
			offer.pkg, offer.packageName, selectedService, selectedCurrency = p.UUID, p.Name, p.ServiceName, p.CurrencyUnit
		}
	}
	idMatches, nameMatches := 0, 0
	for _, p := range packages {
		if p.UUID == offer.pkg {
			idMatches++
		}
		if p.Name == offer.packageName {
			nameMatches++
		}
	}
	if matches != 1 || idMatches != 1 || nameMatches != 1 || !uuidPattern.MatchString(offer.pkg) || strings.TrimSpace(offer.packageName) == "" || selectedService != offer.serviceName || selectedCurrency != "VND" {
		return refuse("package is not offered exactly once")
	}
	return offer, nil
}

func (o *endpointOp) checkPlacement(ctx context.Context, offer endpointOffer, vpc, subnet string) error {
	refuse := func(what string) error {
		return fmt.Errorf("%w: %s: %s", core.ErrInvalidInput, o.op, what)
	}
	type vpcRow struct {
		UUID        string `json:"uuid"`
		Status      string `json:"status"`
		ProjectUUID string `json:"projectUuid"`
		RegionUUID  string `json:"regionUuid"`
	}
	vpcs, err := endpointScan(ctx, o, []string{"endpoints", "vpcs"}, []endpointFilter{{"status", "ACTIVE"}, {"endpointServiceId", offer.service}, {"zoneType", "AVAILABILITY"}},
		func(raw json.RawMessage) ([]vpcRow, error) {
			rows, err := natDecodeRows[vpcRow](o.op, raw, "uuid", "status", "projectUuid", "regionUuid")
			if err != nil {
				return nil, endpointSafeError(o.op, err)
			}
			for _, v := range rows {
				if !vpcPattern.MatchString(v.UUID) {
					return nil, endpointInvalid(o.op)
				}
			}
			return rows, nil
		}, func(v vpcRow) string { return v.UUID })
	if err != nil {
		return err
	}
	found := false
	for _, v := range vpcs {
		if v.UUID == vpc {
			found = true
			if v.Status != "ACTIVE" || v.ProjectUUID != o.scope.project || v.RegionUUID != o.scope.zone {
				return refuse("VPC is not an active VPC of the selected scope")
			}
		}
	}
	if !found {
		return fmt.Errorf("%w: %s: VPC is not eligible for the endpoint offer", core.ErrNotFound, o.op)
	}

	env, err := o.catalogRead(ctx, []string{"vpcs", vpc, "subnets"}, []endpointFilter{{"isEndpointUsable", "true"}, {"zoneUuid", offer.zone}}, nil)
	if err != nil {
		return err
	}
	subnets, err := endpointRows[struct {
		UUID    string `json:"uuid"`
		Status  string `json:"status"`
		VPCUUID string `json:"vpcUuid"`
		Zone    struct {
			UUID string `json:"uuid"`
		} `json:"zone"`
	}](o, env, "uuid", "status", "vpcUuid", "zone")
	if err != nil {
		return err
	}
	matches := 0
	for _, s := range subnets {
		if !subnetPattern.MatchString(s.UUID) {
			return endpointInvalid(o.op)
		}
		if s.UUID == subnet {
			matches++
			if s.Status != "ACTIVE" || s.VPCUUID != vpc || s.Zone.UUID != offer.zone {
				return refuse("subnet is not an active subnet of the VPC in the zone")
			}
		}
	}
	if matches != 1 {
		return fmt.Errorf("%w: %s: subnet is not eligible for the endpoint offer", core.ErrNotFound, o.op)
	}
	return nil
}

func (o *endpointOp) inventory(ctx context.Context) ([]endpointRow, error) {
	return endpointScan(ctx, o, []string{"endpoints"}, nil, func(raw json.RawMessage) ([]endpointRow, error) {
		rows, err := natDecodeRows[endpointRow](o.op, raw, "uuid", "endpointName", "endpointServiceId", "vpcId", "status", "endpointType", "zoneUuid", "enableDnsName", "subnet", "packageId", "project", "service")
		if err != nil {
			return nil, endpointSafeError(o.op, err)
		}
		var fields []map[string]json.RawMessage
		if json.Unmarshal(raw, &fields) != nil || len(fields) != len(rows) {
			return nil, endpointInvalid(o.op)
		}
		for i := range rows {
			for key := range fields[i] {
				for _, name := range []string{"endpointResource", "endpointDomains"} {
					if strings.EqualFold(key, name) && key != name {
						return nil, endpointInvalid(o.op)
					}
				}
			}
			row := &rows[i]
			row.resource, row.domains = fields[i]["endpointResource"], fields[i]["endpointDomains"]
			if row.EndpointName == "" || row.Status == "" || row.Project.VServerProjectID != o.scope.project || !hex24Pattern.MatchString(row.EndpointServiceID) ||
				!endpointIDForm.MatchString(row.UUID) || !vpcPattern.MatchString(row.VPCID) || !subnetPattern.MatchString(row.Subnet.UUID) ||
				!uuidPattern.MatchString(row.Service.UUID) || !uuidPattern.MatchString(row.PackageID.UUID) {
				return nil, endpointInvalid(o.op)
			}
		}
		return rows, nil
	}, func(r endpointRow) string { return r.UUID })
}
