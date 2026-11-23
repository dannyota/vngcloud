package network

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"reflect"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/transport"
)

type endpointResourceInfo struct {
	IsBuyMorePoc      bool   `json:"isBuyMorePoc"`
	IsPoc             bool   `json:"isPoc"`
	IsEnableAutoRenew bool   `json:"isEnableAutoRenew"`
	EndpointName      string `json:"endpointName"`
	CategoryUUID      string `json:"categoryUuid"`
	ServiceUUID       string `json:"serviceUuid"`
	PackageUUID       string `json:"packageUuid"`
	VPCUUID           string `json:"vpcUuid"`
	SubnetUUID        string `json:"subnetUuid"`
	RegionUUID        string `json:"regionUuid"`
	ProjectUUID       string `json:"projectUuid"`
	EnableAZ          bool   `json:"enableAZ"`
	EnableDNSName     bool   `json:"enableDnsName"`
}

type endpointQuoteBody struct {
	ResourceType string               `json:"resourceType"`
	Action       string               `json:"action"`
	ResourceInfo endpointResourceInfo `json:"resourceInfo"`
}

type endpointCreateBody struct {
	endpointQuoteBody
	TagDetails []struct{} `json:"tagDetails"`
}

type endpointDeleteBody struct {
	EndpointUUID        string `json:"endpointUuid"`
	VPCUUID             string `json:"vpcUuid"`
	EndpointServiceUUID string `json:"endpointServiceUuid"`
	RegionUUID          string `json:"regionUuid"`
	ProjectUUID         string `json:"projectUuid"`
}

func endpointQuoteRequest(info endpointResourceInfo) endpointQuoteBody {
	return endpointQuoteBody{ResourceType: "endpoint", Action: "create", ResourceInfo: info}
}

func checkEndpointCreateInput(op string, in *CreateEndpointInput) error {
	if err := core.CheckRequired(op, in); err != nil {
		return err
	}
	if math.IsNaN(in.MaxPrice) || math.IsInf(in.MaxPrice, 0) || in.MaxPrice < 0 {
		return fmt.Errorf("%w: %s requires finite nonnegative MaxPrice", core.ErrInvalidInput, op)
	}
	if !utf8.ValidString(in.Name) || strings.TrimSpace(in.Name) == "" || strings.ContainsFunc(in.Name, unicode.IsControl) {
		return fmt.Errorf("%w: %s requires a nonblank Name without control characters", core.ErrInvalidInput, op)
	}
	if err := checkEndpointForm(op, "VPCID", in.VPCID, "net-<uuid>", vpcPattern); err != nil {
		return err
	}
	return checkEndpointForm(op, "SubnetID", in.SubnetID, "sub-<uuid>", subnetPattern)
}

func checkEndpointForm(op, name, value, form string, pattern *regexp.Regexp) error {
	if err := core.CheckPathID(op, name, value); err != nil {
		return err
	}
	if !pattern.MatchString(value) {
		return fmt.Errorf("%w: %s requires %s in the form %s", core.ErrInvalidInput, op, name, form)
	}
	return nil
}

type endpointSpec struct {
	info  endpointResourceInfo
	offer endpointOffer
}

func (c *Client) resolveEndpointSpec(ctx context.Context, o *endpointOp, in *CreateEndpointInput) (*endpointSpec, error) {
	if err := c.endpointProvenance(o.op); err != nil {
		return nil, err
	}
	if err := o.discoverScope(ctx); err != nil {
		return nil, err
	}
	offer, err := o.resolveOffer(ctx)
	if err != nil {
		return nil, err
	}
	if err := o.checkPlacement(ctx, offer, in.VPCID, in.SubnetID); err != nil {
		return nil, err
	}
	return &endpointSpec{offer: offer, info: endpointResourceInfo{
		EndpointName: in.Name, CategoryUUID: offer.category, ServiceUUID: offer.service, PackageUUID: offer.pkg,
		VPCUUID: in.VPCID, SubnetUUID: in.SubnetID, RegionUUID: o.scope.zone, ProjectUUID: o.scope.project, EnableAZ: true,
	}}, nil
}

func (o *endpointOp) quote(ctx context.Context, s *endpointSpec) (float64, error) {
	code := 0
	env, err := o.envelope(ctx, transport.Request{Method: http.MethodPost, URL: o.route("vnetwork/billing/v1", []string{"price"}, nil), Body: endpointQuoteRequest(s.info), Idempotent: true, OK: []int{http.StatusOK}}, &code)
	if err != nil {
		return 0, err
	}
	var price struct {
		OptimumPrice *float64 `json:"optimumPrice"`
		Properties   []struct {
			OptimumPrice *float64 `json:"optimumPrice"`
			MonthlyPrice *float64 `json:"monthlyPrice"`
			Name         string   `json:"name"`
		} `json:"propertiesPrice"`
	}
	if decodeNATEnvelope(env.Data, &price, reflect.TypeFor[struct{}]()) != nil || price.OptimumPrice == nil || len(price.Properties) != 1 {
		return 0, endpointInvalid(o.op)
	}
	amount, p := *price.OptimumPrice, price.Properties[0]
	if math.IsNaN(amount) || math.IsInf(amount, 0) || amount < 0 || p.Name != endpointPackage || p.OptimumPrice == nil || p.MonthlyPrice == nil || *p.OptimumPrice != amount || *p.MonthlyPrice != amount {
		return 0, endpointInvalid(o.op)
	}
	if amount == 0 {
		return 0, fmt.Errorf("%w: %s", core.ErrUnpriced, o.op)
	}
	return amount, nil
}

// QuoteCreateEndpoint reads a fresh monthly VND quote for one vStorage
// Standard endpoint. It creates nothing and ignores MaxPrice.
func (c *Client) QuoteCreateEndpoint(ctx context.Context, in *CreateEndpointInput) (*QuoteCreateEndpointOutput, error) {
	const op = "network.QuoteCreateEndpoint"
	if err := checkEndpointCreateInput(op, in); err != nil {
		return nil, err
	}
	o := &endpointOp{c: c, op: op}
	s, err := c.resolveEndpointSpec(ctx, o, in)
	if err != nil {
		return nil, err
	}
	amount, err := o.quote(ctx, s)
	if err != nil {
		return nil, err
	}
	return &QuoteCreateEndpointOutput{MonthlyPrice: amount, Currency: "VND"}, nil
}

// CreateEndpoint buys one vStorage Standard endpoint in hcm-3 with one POST
// and waits for ACTIVE. It requests auto-renew off but reads no billing state.
// The quote cap is not a server price lock.
func (c *Client) CreateEndpoint(ctx context.Context, in *CreateEndpointInput) (*CreateEndpointOutput, error) {
	const op = "network.CreateEndpoint"
	if err := checkEndpointCreateInput(op, in); err != nil {
		return nil, err
	}
	o := &endpointOp{c: c, op: op}
	s, err := c.resolveEndpointSpec(ctx, o, in)
	if err != nil {
		return nil, err
	}
	before, err := o.inventory(ctx)
	if err != nil {
		return nil, err
	}
	baseline := make(map[string]bool, len(before))
	for _, row := range before {
		baseline[row.UUID] = true
		if row.EndpointName == in.Name && row.VPCID == in.VPCID {
			return nil, fmt.Errorf("%w: %s: an endpoint with this name exists in the VPC", core.ErrInvalidInput, op)
		}
	}
	amount, err := o.quote(ctx, s)
	if err != nil {
		return nil, err
	}
	if amount > in.MaxPrice {
		return nil, fmt.Errorf("%w: %s: quote %.0f VND exceeds MaxPrice %.0f VND", core.ErrPriceAboveMax, op, amount, in.MaxPrice)
	}
	out := &CreateEndpointOutput{MonthlyPrice: amount, Currency: "VND"}
	code := 0
	env, notSent, err := o.send(ctx, transport.Request{Method: http.MethodPost, URL: o.route("vnetwork/v1", []string{"endpoints"}, nil), Body: endpointCreateBody{endpointQuoteBody: endpointQuoteRequest(s.info), TagDetails: []struct{}{}}, OK: []int{http.StatusCreated}, Once: true}, &code)
	if err != nil {
		if (notSent != nil && *notSent) || endpointDefiniteRefusal(err) {
			return nil, err
		}
		return out, endpointUnsettled(op, err)
	}
	var accepted struct {
		UUID string `json:"uuid"`
		Name string `json:"name"`
	}
	if decodeNATEnvelope(env.Data, &accepted, reflect.TypeFor[struct{}]()) != nil || !endpointIDForm.MatchString(accepted.UUID) || accepted.Name != in.Name || baseline[accepted.UUID] {
		return out, endpointUnsettled(op, endpointInvalid(op))
	}
	out.EndpointID = accepted.UUID
	deadline := c.now().Add(15 * time.Minute)
	waitCtx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	if err := o.waitCreate(waitCtx, s, baseline, out, deadline); err != nil {
		return out, err
	}
	return out, nil
}

func (o *endpointOp) preflightDelete(rows []endpointRow, in *DeleteEndpointInput, offer endpointOffer) (*endpointRow, error) {
	var row *endpointRow
	for i := range rows {
		if rows[i].UUID == in.EndpointID {
			row = &rows[i]
		}
	}
	if row == nil {
		return nil, fmt.Errorf("%w: %s", core.ErrNotFound, o.op)
	}
	if row.VPCID != in.VPCID || row.Service.UUID != offer.service || row.Service.Name != endpointService || row.PackageID.UUID != offer.pkg || row.ZoneUUID != endpointZone || row.Status != "ACTIVE" {
		return nil, fmt.Errorf("%w: %s: endpoint is not an ACTIVE vStorage Standard endpoint of the named VPC", core.ErrInvalidInput, o.op)
	}
	if len(row.resource) == 0 || len(row.domains) == 0 || string(row.domains) == "null" {
		return nil, endpointInvalid(o.op)
	}
	var domains []json.RawMessage
	if json.Unmarshal(row.domains, &domains) != nil {
		return nil, endpointInvalid(o.op)
	}
	if string(row.resource) != "null" || len(domains) != 0 {
		return nil, fmt.Errorf("%w: %s: endpoint has a resource or domain attached", ErrInUse, o.op)
	}
	return row, nil
}

// DeleteEndpoint deletes one unattached ACTIVE vStorage Standard endpoint
// with one DELETE and waits until a complete scoped list omits it. Deleting an
// endpoint a cluster uses breaks that cluster's private access, and deletion
// promises no refund.
func (c *Client) DeleteEndpoint(ctx context.Context, in *DeleteEndpointInput) (*DeleteEndpointOutput, error) {
	const op = "network.DeleteEndpoint"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := checkEndpointForm(op, "EndpointID", in.EndpointID, "enp-<uuid>", endpointIDForm); err != nil {
		return nil, err
	}
	if err := checkEndpointForm(op, "VPCID", in.VPCID, "net-<uuid>", vpcPattern); err != nil {
		return nil, err
	}
	if err := c.endpointProvenance(op); err != nil {
		return nil, err
	}
	o := &endpointOp{c: c, op: op}
	if err := o.discoverScope(ctx); err != nil {
		return nil, err
	}
	offer, err := o.resolveOffer(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := o.inventory(ctx)
	if err != nil {
		return nil, err
	}
	row, err := o.preflightDelete(rows, in, offer)
	if err != nil {
		return nil, err
	}
	out := &DeleteEndpointOutput{EndpointID: in.EndpointID}
	code := 0
	body := endpointDeleteBody{EndpointUUID: in.EndpointID, VPCUUID: in.VPCID, EndpointServiceUUID: row.Service.UUID, RegionUUID: o.scope.zone, ProjectUUID: o.scope.project}
	_, notSent, err := o.send(ctx, transport.Request{Method: http.MethodDelete, URL: o.route("vnetwork/v1", []string{"endpoints", in.EndpointID}, nil), Body: body, OK: []int{http.StatusOK}, Once: true}, &code)
	if err != nil {
		if (notSent != nil && *notSent) || endpointDefiniteRefusal(err) {
			return nil, err
		}
		return out, endpointUnsettled(op, err)
	}
	deadline := c.now().Add(10 * time.Minute)
	waitCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	if err := o.waitDelete(waitCtx, in.EndpointID, deadline); err != nil {
		return out, err
	}
	out.Deleted = true
	return out, nil
}
