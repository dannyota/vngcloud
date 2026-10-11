package network

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/routes"
	"danny.vn/vngcloud/internal/transport"
)

type natScope struct{ base, zone, project string }

func (s natScope) route(version string, parts []string, query url.Values) string {
	return routes.URL(fixedVNetEndpoint{base: s.base}, routes.Route{Product: routes.ProductVNet, Version: version, Parts: append([]string{s.zone, s.project}, parts...), Query: query})
}
func (s natScope) collection(parts ...string) string {
	return s.route("vnetwork/v1", append([]string{"nats"}, parts...), nil)
}
func natParams(page int) url.Values {
	raw, _ := json.Marshal(struct {
		Search []string `json:"search"`
		Sort   struct{} `json:"sort"`
		Page   int      `json:"page"`
		Size   int      `json:"size"`
	}{Search: []string{}, Page: page, Size: 1000})
	return url.Values{"params": {string(raw)}}
}
func (c *Client) natScope(ctx context.Context, op, zone string) (natScope, error) {
	if zone != "" {
		if err := core.CheckPathID(op, "ZoneID", zone); err != nil {
			return natScope{}, err
		}
	}
	if id := c.c.ProjectID(); id != "" {
		if err := core.CheckPathID(op, "ProjectID", id); err != nil {
			return natScope{}, err
		}
	}
	base, err := c.natEndpoint()
	if err != nil {
		return natScope{}, err
	}
	project, err := c.natProjectID(ctx, op)
	if err != nil {
		return natScope{}, natSafeError(op, err)
	}
	if err = core.CheckPathID(op, "ProjectID", project); err != nil {
		return natScope{}, err
	}
	if zone == "" {
		zone, err = c.natZoneIDWithPrivacy(ctx, base, true)
		if err != nil {
			return natScope{}, natSafeError(op, err)
		}
	}
	return natScope{base: base, zone: zone, project: project}, nil
}

type natEnvelope struct {
	Code      json.RawMessage `json:"code"`
	Success   *bool           `json:"success"`
	Data      json.RawMessage `json:"data"`
	Page      *int            `json:"page"`
	Size      *int            `json:"size"`
	TotalPage *int            `json:"totalPage"`
	Total     *int            `json:"total"`
}

func natInvalid(op string) error {
	return &core.APIError{Operation: op, StatusCode: http.StatusOK, Code: "InvalidResponse", Message: "NAT response had invalid structure"}
}
func natSafeError(op string, err error) error {
	if err == nil {
		return nil
	}
	for _, sentinel := range []error{context.Canceled, context.DeadlineExceeded, core.ErrInvalidConfig, core.ErrInvalidInput, core.ErrMissingProjectID, core.ErrProjectNotFound, core.ErrProjectAmbiguous} {
		if errors.Is(err, sentinel) {
			return fmt.Errorf("%s: %w", op, sentinel)
		}
	}
	var api *core.APIError
	if errors.As(err, &api) {
		var cause error
		switch api.StatusCode {
		case 401:
			cause = core.ErrAuth
		case 403:
			cause = core.ErrPermission
		case 404:
			cause = core.ErrNotFound
		case 429:
			cause = core.ErrRateLimited
		}
		code := core.ResolvedCode(api.StatusCode, "")
		if api.Code == "InvalidResponse" {
			code = "InvalidResponse"
		}
		return &core.APIError{Operation: op, StatusCode: api.StatusCode, Code: code, Message: "NAT response withheld", Retryable: api.Retryable, Err: cause}
	}
	if errors.Is(err, core.ErrAuth) {
		return fmt.Errorf("%s: %w", op, core.ErrAuth)
	}
	return &core.APIError{Operation: op, Message: "NAT request failed"}
}

// Sensitive suppresses catalog images, account identifiers, and server text.
func (c *Client) natExchange(ctx context.Context, req transport.Request, code *int) (*natEnvelope, error) {
	req.Sensitive = true
	req.MaxBody = natMaxBody
	var credential string
	req.SentCredential = &credential
	req.NoRedirect = true
	req.WithholdMessage = "NAT response withheld"
	var raw json.RawMessage
	status, err := c.c.DoJSONStatus(ctx, req, &raw)
	if err != nil {
		if errors.Is(err, transport.ErrBodyTooLarge) {
			return nil, natInvalid(req.Operation)
		}
		if status > 0 && status < 300 {
			return nil, natInvalid(req.Operation)
		}
		return nil, natSafeError(req.Operation, err)
	}
	if natReflectsCredential(raw, credential) {
		return nil, natInvalid(req.Operation)
	}
	var env natEnvelope
	if decodeNATEnvelope(raw, &env, reflect.TypeFor[map[string]json.RawMessage]()) != nil || env.Success == nil || !*env.Success || (code != nil && !natNumericCode(env.Code, *code)) {
		return nil, natInvalid(req.Operation)
	}
	return &env, nil
}

func natDecodeRows[T any](op string, raw json.RawMessage, required ...string) ([]T, error) {
	var rows []T
	if decodeNATEnvelope(raw, &rows, reflect.TypeFor[T]()) != nil || rows == nil || checkNATNulls(raw, reflect.TypeFor[[]T]()) != nil {
		return nil, natInvalid(op)
	}
	var fields []map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return nil, natInvalid(op)
	}
	for _, row := range fields {
		for _, key := range required {
			if len(row[key]) == 0 || string(row[key]) == "null" {
				return nil, natInvalid(op)
			}
		}
	}
	return rows, nil
}

// ListNATZones reads the unpaged catalog, preserving unknown zone types.
func (c *Client) ListNATZones(ctx context.Context, in *ListNATZonesInput) (*ListNATZonesOutput, error) {
	const op = "network.ListNATZones"
	zone := ""
	if in != nil {
		zone = in.ZoneID
	}
	scope, err := c.natScope(ctx, op, zone)
	if err != nil {
		return nil, err
	}
	items, err := c.natZones(ctx, op, scope)
	if err != nil {
		return nil, err
	}
	return &ListNATZonesOutput{Items: items}, nil
}
func (c *Client) natZones(ctx context.Context, op string, s natScope) ([]NATAvailabilityZone, error) {
	code := 200
	env, err := c.natExchange(ctx, transport.Request{Operation: op, Method: http.MethodGet, URL: s.route("vnetwork/v1", []string{"nats", "zones"}, natParams(1)), OK: []int{200}}, &code)
	if err != nil {
		return nil, err
	}
	rows, err := natDecodeRows[NATAvailabilityZone](op, env.Data, "uuid", "zoneType", "isEnabled", "isDefault")
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		if row.UUID == "" || row.ZoneType == "" {
			return nil, natInvalid(op)
		}
	}
	return rows, nil
}

// ListNATPackages requires an explicit availability zone for the catalog query.
func (c *Client) ListNATPackages(ctx context.Context, in *ListNATPackagesInput) (*ListNATPackagesOutput, error) {
	const op = "network.ListNATPackages"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "AvailabilityZoneID", in.AvailabilityZoneID); err != nil {
		return nil, err
	}
	s, err := c.natScope(ctx, op, in.ZoneID)
	if err != nil {
		return nil, err
	}
	rows, err := c.natPackages(ctx, op, s, in.AvailabilityZoneID)
	if err != nil {
		return nil, err
	}
	return &ListNATPackagesOutput{Items: rows}, nil
}
func (c *Client) natPackages(ctx context.Context, op string, s natScope, az string) ([]NATPackageOffer, error) {
	code := 200
	query := natParams(1)
	query.Set("zoneUuid", az)
	env, err := c.natExchange(ctx, transport.Request{Operation: op, Method: http.MethodGet, URL: s.route("vnetwork/v1", []string{"nats", "nat-package"}, query), OK: []int{200}}, &code)
	if err != nil {
		return nil, err
	}
	rows, err := natDecodeRows[NATPackageOffer](op, env.Data, "uuid", "billingSku", "currencyUnit", "isDefault", "monthlyPrice", "price")
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		if row.UUID == "" || row.BillingSKU == "" {
			return nil, natInvalid(op)
		}
	}
	var prices []struct {
		Price map[string]json.RawMessage `json:"price"`
	}
	if json.Unmarshal(env.Data, &prices) != nil {
		return nil, natInvalid(op)
	}
	for _, row := range prices {
		for _, field := range []string{"optimumPrice", "originalPrice", "discountPrice", "discountPercent"} {
			if len(row.Price[field]) == 0 {
				return nil, natInvalid(op)
			}
		}
	}
	return rows, nil
}

func natNumericCode(raw json.RawMessage, expected int) bool {
	if len(raw) == 0 || len(raw) > 128 || raw[0] == '"' {
		return false
	}
	var n json.Number
	if json.Unmarshal(raw, &n) != nil || n.String() == "" {
		return false
	}
	if expected == 0 {
		mantissa, _, _ := strings.Cut(strings.ToLower(n.String()), "e")
		for _, digit := range mantissa {
			if digit >= '1' && digit <= '9' {
				return false
			}
		}
		return true
	}
	approximate, err := strconv.ParseFloat(n.String(), 64)
	if err != nil || approximate != float64(expected) {
		return false
	}
	value, ok := new(big.Rat).SetString(n.String())
	return ok && value.Cmp(big.NewRat(int64(expected), 1)) == 0
}

// Project discovery omits portal identity and suppresses its raw capture.
func (c *Client) natProjectID(ctx context.Context, op string) (string, error) {
	if id := c.c.ProjectID(); id != "" {
		return id, nil
	}
	var raw json.RawMessage
	var credential string
	status, err := c.c.DoJSONStatus(ctx, transport.Request{MaxBody: natMaxBody, SentCredential: &credential, Operation: op, Method: http.MethodGet, URL: c.c.RouteURL(routes.Route{Product: routes.ProductVServer, Version: "v1", Parts: []string{"projects"}}), OK: []int{200}, Sensitive: true, NoRedirect: true, WithholdMessage: "NAT project discovery withheld"}, &raw)
	if err != nil {
		if errors.Is(err, transport.ErrBodyTooLarge) {
			return "", natInvalid(op)
		}
		if status == 200 {
			return "", natInvalid(op)
		}
		return "", natSafeError(op, err)
	}
	if natReflectsCredential(raw, credential) {
		return "", natInvalid(op)
	}
	var wire struct {
		Projects []struct {
			ID     string `json:"projectId"`
			Region string `json:"region"`
		} `json:"projects"`
	}
	if decodeNATEnvelope(raw, &wire, reflect.TypeFor[struct{}]()) != nil || wire.Projects == nil || checkNATNulls(raw, reflect.TypeOf(wire)) != nil {
		return "", natInvalid(op)
	}
	id := ""
	for _, project := range wire.Projects {
		if project.Region != "" && project.Region != c.c.Region() {
			continue
		}
		if core.CheckPathID(op, "discovered ProjectID", project.ID) != nil {
			return "", natInvalid(op)
		}
		if id != "" {
			return "", fmt.Errorf("%w: %s", core.ErrProjectAmbiguous, op)
		}
		id = project.ID
	}
	if id == "" {
		return "", fmt.Errorf("%w: %s", core.ErrProjectNotFound, op)
	}
	return id, nil
}
