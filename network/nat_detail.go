package network

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/jsonresponse"
	"danny.vn/vngcloud/internal/routes"
	"danny.vn/vngcloud/internal/transport"
)

// GetNATInstanceInput identifies a NAT.
type GetNATInstanceInput struct {
	// NATID is the required path-safe NAT identifier.
	NATID string `vngcloud:"required"`
	// ZoneID selects the region scope; empty discovers it.
	ZoneID string
}

// GetNATInstanceOutput holds the NAT projection.
type GetNATInstanceOutput struct{ NAT NATDetail }

// NATDetail holds the detail projection.
type NATDetail struct{ ID, Name, Status, GatewayIP, PublicIP, CreatedAt, PackageID, PackageName, VPCID, VPCName string }

// ListNATInboundRulesInput identifies a NAT.
type ListNATInboundRulesInput struct {
	// NATID is the required path-safe NAT identifier.
	NATID string `vngcloud:"required"`
	// ZoneID selects the region scope; empty discovers it.
	ZoneID string
}

// ListNATInboundRulesOutput holds inbound rules.
type ListNATInboundRulesOutput struct{ Items []NATInboundRule }

// NATInboundRule holds an inbound rule projection.
type NATInboundRule struct {
	ID, Protocol, EtherType, Direction string
	PortRangeMin, PortRangeMax         *int
	Description, CreatedAt             string
	System                             bool
}

type natDetailWire struct {
	ID        *string        `json:"uuid"`
	Name      *string        `json:"natName"`
	Status    *string        `json:"status"`
	GatewayIP *string        `json:"natGatewayIp"`
	PublicIP  *string        `json:"publicIp"`
	CreatedAt *string        `json:"createdAt"`
	Package   *natNestedWire `json:"natPackage"`
	VPC       *natNestedWire `json:"vpc"`
}
type natNestedWire struct {
	ID   *string `json:"uuid"`
	Name *string `json:"name"`
}
type natRuleWire struct {
	ID          *string `json:"uuid"`
	Protocol    *string `json:"protocol"`
	EtherType   *string `json:"etherType"`
	Direction   *string `json:"direction"`
	Min         *int    `json:"portRangeMin"`
	Max         *int    `json:"portRangeMax"`
	Description *string `json:"description"`
	CreatedAt   *string `json:"createdAt"`
	System      *bool   `json:"isSystem"`
}
type natDetailEnvelope struct {
	Success *bool          `json:"success"`
	Data    *natDetailWire `json:"data"`
}
type natRulesEnvelope struct {
	Success *bool         `json:"success"`
	Data    []natRuleWire `json:"data"`
}

func natDetailString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
func natInvalidRead(op string) error {
	return &core.APIError{Operation: op, StatusCode: 200, Code: "InvalidResponse", Message: "NAT response withheld"}
}
func natStrict(raw []byte, v any) error {
	if err := jsonresponse.ValidateStrict(raw, v); err != nil {
		return err
	}
	if err := checkNATFieldNames(raw, reflect.TypeOf(v).Elem(), nil); err != nil {
		return err
	}
	return json.Unmarshal(raw, v)
}
func (c *Client) natRead(ctx context.Context, op, id, zone string, suffix []string) (json.RawMessage, []string, error) {
	if core.CheckPathID(op, "NATID", id) != nil {
		return nil, nil, fmt.Errorf("%w: %s requires a path-safe NATID", core.ErrInvalidInput, op)
	}
	if zone != "" && core.CheckPathID(op, "ZoneID", zone) != nil {
		return nil, nil, fmt.Errorf("%w: %s requires a path-safe ZoneID", core.ErrInvalidInput, op)
	}
	project, err := c.c.ProjectIDContext(ctx)
	if err != nil {
		return nil, nil, err
	}
	if project != "" {
		if err = core.CheckPathID(op, "ProjectID", project); err != nil {
			return nil, nil, err
		}
	}
	base, err := c.natEndpoint()
	if err != nil {
		return nil, nil, err
	}
	project, err = c.c.RequireProjectID(ctx)
	if err != nil {
		return nil, nil, err
	}
	if err = core.CheckPathID(op, "ProjectID", project); err != nil {
		return nil, nil, err
	}
	if zone == "" {
		zone, err = c.natZoneID(ctx, base)
		if err != nil {
			return nil, nil, err
		}
	}
	parts := append([]string{zone, project, "nats", id}, suffix...)
	var raw json.RawMessage
	var creds []string
	status, err := c.c.DoJSONStatus(ctx, transport.Request{Operation: op, Method: http.MethodGet, URL: routes.URL(fixedVNetEndpoint{base: base}, routes.Route{Product: routes.ProductVNet, Version: "vnetwork/v1", Parts: parts}), OK: []int{200}, Sensitive: true, NoRedirect: true, WithholdMessage: "NAT response withheld", MaxBody: natMaxBody, SentCredentials: &creds}, &raw)
	if err != nil {
		if status == 200 && errors.Is(err, transport.ErrBodyTooLarge) {
			return nil, nil, &core.APIError{Operation: op, StatusCode: 200, Message: "NAT response withheld", Err: transport.ErrBodyTooLarge}
		}
		var api *core.APIError
		if status == 200 {
			return nil, nil, natInvalidRead(op)
		}
		if errors.As(err, &api) {
			s := api.StatusCode
			if s == 0 {
				s = status
			}
			return nil, nil, &core.APIError{Operation: op, StatusCode: s, Code: core.ResolvedCode(s, ""), Message: api.Message, Retryable: api.Retryable, Err: api.Err}
		}
		return nil, nil, err
	}
	return raw, creds, nil
}
func natHasCredential(values []string, credentials []string) bool {
	for _, v := range values {
		for _, c := range credentials {
			if c != "" && strings.Contains(v, c) {
				return true
			}
		}
	}
	return false
}

// GetNATInstance reads a Public NAT detail.
func (c *Client) GetNATInstance(ctx context.Context, in *GetNATInstanceInput) (*GetNATInstanceOutput, error) {
	const op = "network.GetNATInstance"
	var id, zone string
	if in != nil {
		id, zone = in.NATID, in.ZoneID
	}
	raw, creds, err := c.natRead(ctx, op, id, zone, nil)
	if err != nil {
		return nil, err
	}
	var e natDetailEnvelope
	if natStrict(raw, &e) != nil || e.Success == nil || !*e.Success || e.Data == nil {
		return nil, natInvalidRead(op)
	}
	d := e.Data
	if d.ID == nil || *d.ID != id || d.Status == nil || d.Package == nil || d.Package.ID == nil || d.VPC == nil || d.VPC.ID == nil || core.CheckPathID(op, "VPCID", *d.VPC.ID) != nil {
		return nil, natInvalidRead(op)
	}
	n := NATDetail{ID: *d.ID, Name: natDetailString(d.Name), Status: *d.Status, GatewayIP: natDetailString(d.GatewayIP), PublicIP: natDetailString(d.PublicIP), CreatedAt: natDetailString(d.CreatedAt), PackageID: *d.Package.ID, PackageName: natDetailString(d.Package.Name), VPCID: *d.VPC.ID, VPCName: natDetailString(d.VPC.Name)}
	if natHasCredential([]string{n.ID, n.Name, n.Status, n.GatewayIP, n.PublicIP, n.CreatedAt, n.PackageID, n.PackageName, n.VPCID, n.VPCName}, creds) {
		return nil, natInvalidRead(op)
	}
	return &GetNATInstanceOutput{NAT: n}, nil
}

// ListNATInboundRules reads the inbound rules of a Public NAT.
func (c *Client) ListNATInboundRules(ctx context.Context, in *ListNATInboundRulesInput) (*ListNATInboundRulesOutput, error) {
	const op = "network.ListNATInboundRules"
	var id, zone string
	if in != nil {
		id, zone = in.NATID, in.ZoneID
	}
	raw, creds, err := c.natRead(ctx, op, id, zone, []string{"sec-groups", "rules"})
	if err != nil {
		return nil, err
	}
	var e natRulesEnvelope
	if natStrict(raw, &e) != nil || e.Success == nil || !*e.Success || e.Data == nil {
		return nil, natInvalidRead(op)
	}
	var envelope struct {
		Data []map[string]json.RawMessage `json:"data"`
	}
	if json.Unmarshal(raw, &envelope) != nil {
		return nil, natInvalidRead(op)
	}
	out := &ListNATInboundRulesOutput{Items: make([]NATInboundRule, 0, len(e.Data))}
	seen := map[string]bool{}
	for i, r := range e.Data {
		_, minOK := envelope.Data[i]["portRangeMin"]
		_, maxOK := envelope.Data[i]["portRangeMax"]
		if r.ID == nil || core.CheckPathID(op, "ID", *r.ID) != nil || seen[*r.ID] || r.Protocol == nil || r.EtherType == nil || r.Direction == nil || r.System == nil || !minOK || !maxOK {
			return nil, natInvalidRead(op)
		}
		seen[*r.ID] = true
		item := NATInboundRule{ID: *r.ID, Protocol: *r.Protocol, EtherType: *r.EtherType, Direction: *r.Direction, PortRangeMin: r.Min, PortRangeMax: r.Max, Description: natDetailString(r.Description), CreatedAt: natDetailString(r.CreatedAt), System: *r.System}
		if natHasCredential([]string{item.ID, item.Protocol, item.EtherType, item.Direction, item.Description, item.CreatedAt}, creds) {
			return nil, natInvalidRead(op)
		}
		out.Items = append(out.Items, item)
	}
	return out, nil
}
