package network

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/transport"
)

type endpointOffer struct{ category, service, pkg string }

type endpointRow struct {
	UUID              string `json:"uuid"`
	EndpointName      string `json:"endpointName"`
	EndpointServiceID string `json:"endpointServiceId"`
	VPCID             string `json:"vpcId"`
	Status            string `json:"status"`
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
	query := endpointParams(1, search)
	for key, values := range extra {
		query[key] = values
	}
	return o.envelope(ctx, transport.Request{Method: http.MethodGet, URL: o.route("vnetwork/v1", append([]string{"endpoints"}, parts...), query), OK: []int{http.StatusOK}}, nil)
}

func (o *endpointOp) resolveOffer(ctx context.Context) (endpointOffer, error) {
	refuse := func(what string) (endpointOffer, error) {
		return endpointOffer{}, fmt.Errorf("%w: %s: %s", core.ErrInvalidInput, o.op, what)
	}
	env, err := o.catalogRead(ctx, []string{"zones"}, nil, nil)
	if err != nil {
		return endpointOffer{}, err
	}
	zones, err := endpointRows[struct {
		UUID      string `json:"uuid"`
		IsEnabled bool   `json:"isEnabled"`
	}](o, env, "uuid", "isEnabled")
	if err != nil {
		return endpointOffer{}, err
	}
	matches := 0
	for _, z := range zones {
		if z.UUID == endpointZone {
			matches++
			if !z.IsEnabled {
				return refuse("zone is not enabled")
			}
		}
	}
	if matches != 1 {
		return refuse("zone is not offered exactly once")
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
	if len(categories) != 1 || !categories[0].IsDefault || !hex24Pattern.MatchString(categories[0].UUID) {
		return refuse("category is not the sole default")
	}
	offer := endpointOffer{category: categories[0].UUID}

	if env, err = o.catalogRead(ctx, []string{offer.category, "services"}, nil, url.Values{"zoneUuid": {endpointZone}}); err != nil {
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
	for _, s := range services {
		if s.Name == endpointService {
			matches++
			offer.service = s.UUID
			if s.EndpointType != "Gateway" {
				return refuse("service is not a Gateway")
			}
		}
	}
	if matches != 1 || !uuidPattern.MatchString(offer.service) {
		return refuse("service is not offered exactly once")
	}

	if env, err = o.catalogRead(ctx, []string{offer.service, "packages"}, nil, nil); err != nil {
		return endpointOffer{}, err
	}
	packages, err := endpointRows[struct {
		UUID        string `json:"uuid"`
		Name        string `json:"name"`
		ServiceName string `json:"serviceName"`
	}](o, env, "uuid", "name", "serviceName")
	if err != nil {
		return endpointOffer{}, err
	}
	matches = 0
	for _, p := range packages {
		if p.Name == endpointPackage {
			matches++
			offer.pkg = p.UUID
			if p.ServiceName != endpointService {
				return refuse("package belongs to another service")
			}
		}
	}
	if matches != 1 || !uuidPattern.MatchString(offer.pkg) {
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
			return rows, endpointSafeError(o.op, err)
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

	env, err := o.catalogRead(ctx, []string{"vpcs", vpc, "subnets"}, []endpointFilter{{"isEndpointUsable", "true"}, {"zoneUuid", endpointZone}}, nil)
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
		if s.UUID == subnet {
			matches++
			if s.Status != "ACTIVE" || s.VPCUUID != vpc || s.Zone.UUID != endpointZone {
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
		rows, err := natDecodeRows[endpointRow](o.op, raw, "uuid", "endpointName", "endpointServiceId", "vpcId", "status", "zoneUuid", "enableDnsName", "subnet", "packageId", "project", "service")
		if err != nil {
			return nil, endpointSafeError(o.op, err)
		}
		var fields []map[string]json.RawMessage
		if json.Unmarshal(raw, &fields) != nil || len(fields) != len(rows) {
			return nil, endpointInvalid(o.op)
		}
		for i := range rows {
			rows[i].resource, rows[i].domains = fields[i]["endpointResource"], fields[i]["endpointDomains"]
			if rows[i].EndpointName == "" || !hex24Pattern.MatchString(rows[i].EndpointServiceID) || rows[i].Status == "" || rows[i].Project.VServerProjectID != o.scope.project {
				return nil, endpointInvalid(o.op)
			}
		}
		return rows, nil
	}, func(r endpointRow) string { return r.UUID })
}
