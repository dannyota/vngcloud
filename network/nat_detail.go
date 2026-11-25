package network

import (
	"bytes"
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
func natReadError(op string, status int, err error) error {
	for _, sentinel := range []error{context.Canceled, context.DeadlineExceeded, transport.ErrBodyTooLarge} {
		if errors.Is(err, sentinel) {
			return &core.APIError{Operation: op, StatusCode: status, Code: core.ResolvedCode(status, ""), Message: "NAT response withheld", Err: sentinel}
		}
	}
	if status == http.StatusOK {
		return natInvalidRead(op)
	}
	safe := natSafeError(op, err)
	var api *core.APIError
	if errors.As(safe, &api) && status > 0 {
		api.StatusCode = status
		api.Code = core.ResolvedCode(status, "")
	}
	return safe
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
	if project != "" && core.CheckPathID(op, "ProjectID", project) != nil {
		return nil, nil, fmt.Errorf("%w: %s requires a path-safe ProjectID", core.ErrInvalidInput, op)
	}
	base, err := c.natEndpoint()
	if err != nil {
		return nil, nil, err
	}
	var creds []string
	project, err = c.natProjectIDForRead(ctx, op, &creds)
	if err != nil {
		return nil, nil, err
	}
	if core.CheckPathID(op, "ProjectID", project) != nil {
		return nil, nil, fmt.Errorf("%w: %s requires a path-safe ProjectID", core.ErrInvalidInput, op)
	}
	if zone == "" {
		zone, err = c.natZoneIDForRead(ctx, base, true, &creds)
		if err != nil {
			return nil, nil, err
		}
	}
	parts := append([]string{zone, project, "nats", id}, suffix...)
	var raw json.RawMessage
	var finalCreds []string
	status, err := c.c.DoJSONStatus(ctx, transport.Request{Operation: op, Method: http.MethodGet, URL: routes.URL(fixedVNetEndpoint{base: base}, routes.Route{Product: routes.ProductVNet, Version: "vnetwork/v1", Parts: parts}), OK: []int{200}, Sensitive: true, NoRedirect: true, NoCookies: true, BodyLimitOverridesStatus: true, KeepObservedStatus: true, WithholdMessage: "NAT response withheld", MaxBody: natMaxBody, SentCredentials: &finalCreds}, &raw)
	if err != nil {
		return nil, nil, natReadError(op, status, err)
	}
	creds = append(creds, finalCreds...)
	return raw, creds, nil
}
func natHasCredential(values []string, credentials []string) bool {
	for _, v := range values {
		for _, c := range credentials {
			wire := strings.TrimRight(c, " \t")
			if (c != "" && strings.Contains(v, c)) || (wire != "" && strings.Contains(v, wire)) {
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
	if *d.Status == "ACTIVE" && (d.Name == nil || d.GatewayIP == nil || d.PublicIP == nil || d.CreatedAt == nil || d.Package.Name == nil || d.VPC.Name == nil) {
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
	var e struct {
		Success *bool           `json:"success"`
		Data    json.RawMessage `json:"data"`
	}
	var keys map[string]json.RawMessage
	if jsonresponse.ValidateStrict(raw, &natRulesEnvelope{}) != nil || json.Unmarshal(raw, &keys) != nil || keys == nil {
		return nil, natInvalidRead(op)
	}
	for key := range keys {
		for _, name := range []string{"success", "data", "page", "pageSize", "paging", "pagination", "totalPage", "totalPages", "total", "totalItem", "totalItems", "next", "nextPage", "isNext", "hasMore", "continuationToken"} {
			if strings.EqualFold(key, name) && key != name {
				return nil, natInvalidRead(op)
			}
		}
	}
	if json.Unmarshal(raw, &e) != nil || e.Success == nil || !*e.Success || len(e.Data) == 0 {
		return nil, natInvalidRead(op)
	}
	decoder := json.NewDecoder(bytes.NewReader(e.Data))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('[') {
		return nil, natInvalidRead(op)
	}
	out := &ListNATInboundRulesOutput{Items: []NATInboundRule{}}
	seen := map[string]bool{}
	for decoder.More() {
		var row json.RawMessage
		var r natRuleWire
		var fields map[string]json.RawMessage
		if decoder.Decode(&row) != nil || natStrict(row, &r) != nil || json.Unmarshal(row, &fields) != nil || fields == nil {
			return nil, natInvalidRead(op)
		}
		_, minOK := fields["portRangeMin"]
		_, maxOK := fields["portRangeMax"]
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
	for _, name := range []string{"total", "totalItem", "totalItems", "page", "totalPage"} {
		if value, ok := keys[name]; ok {
			var n int
			if string(value) == "null" || json.Unmarshal(value, &n) != nil || n < 0 {
				return nil, natInvalidRead(op)
			}
			if ((name == "page" || name == "totalPage") && n > 1) || ((name == "total" || name == "totalItem" || name == "totalItems") && n > len(out.Items)) {
				return nil, natInvalidRead(op)
			}
		}
	}
	for _, name := range []string{"next", "nextPage"} {
		if value, ok := keys[name]; ok {
			var next string
			if string(value) == "null" || json.Unmarshal(value, &next) != nil || next != "" {
				return nil, natInvalidRead(op)
			}
		}
	}
	return out, nil
}
