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
	"danny.vn/vngcloud/internal/transport"
)

// BandwidthPackage describes an offer from docs/design/bandwidth-packages.md.
type BandwidthPackage struct {
	ID          string `json:"uuid"`
	Name        string `json:"name"`
	Type        string `json:"type"`
	Description string `json:"description"`
	Bandwidth   int64  `json:"bandwidth"`
}

// ListBandwidthPackagesInput selects the configured project.
type ListBandwidthPackagesInput struct{}

// ListBandwidthPackagesOutput holds the catalog items.
type ListBandwidthPackagesOutput = core.List[BandwidthPackage]

type bandwidthEnvelope struct {
	Data         json.RawMessage `json:"data"`
	Success      json.RawMessage `json:"success"`
	Code         json.RawMessage `json:"code"`
	Error        json.RawMessage `json:"error"`
	Errors       json.RawMessage `json:"errors"`
	ErrorCode    json.RawMessage `json:"errorCode"`
	ErrorMsg     json.RawMessage `json:"errorMsg"`
	ErrorMessage json.RawMessage `json:"errorMessage"`
}

func bandwidthInvalid(op string, status int) error {
	return &core.APIError{Operation: op, StatusCode: status, Code: "InvalidResponse", Message: "invalid bandwidth response; body withheld"}
}

func bandwidthIncomplete(raw json.RawMessage, count int) bool {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return true
	}
	for key, value := range fields {
		switch strings.ToLower(key) {
		case "total", "totalitem", "totalitems":
			if key != "total" && key != "totalItem" && key != "totalItems" {
				return true
			}
			var total *int64
			if json.Unmarshal(value, &total) != nil || total == nil || *total < 0 || *total > int64(count) {
				return true
			}
		case "page", "pagesize", "paging", "pagination", "totalpage", "totalpages", "next", "nextpage", "isnext", "hasmore", "continuationtoken", "nexttoken", "cursor", "offset", "limit":
			return true
		}
	}
	return false
}

func bandwidthOrigin(region string) string {
	switch region {
	case "han-1":
		return "https://han-1.console.greennode.ai"
	case "hcm-3":
		return "https://hcm-3.console.greennode.ai"
	default:
		return ""
	}
}

func (c *Client) bandwidthProject(ctx context.Context, op, origin string) (string, error) {
	id := c.c.ConfiguredProjectID()
	if id != "" {
		if err := core.CheckPathID(op, "ProjectID", id); err != nil {
			return "", err
		}
		return id, nil
	}
	var raw json.RawMessage
	status, err := c.c.DoProjectDiscoveryJSONStatus(ctx, transport.Request{Operation: op, Method: http.MethodGet, URL: origin + "/vserver/iam-vserver-gateway/v1/projects", OK: []int{200}, MaxBody: 4 << 20, BodyLimitOverridesStatus: true, WithholdMessage: "bandwidth response withheld"}, &raw)
	if err != nil {
		return "", err
	}
	var wire struct {
		Projects []struct {
			ID     string `json:"projectId"`
			Region string `json:"region"`
		} `json:"projects"`
	}
	if decodeNATEnvelope(raw, &wire, reflect.TypeFor[struct{}]()) != nil || wire.Projects == nil || checkNATNulls(raw, reflect.TypeOf(wire)) != nil || bandwidthIncomplete(raw, len(wire.Projects)) {
		return "", bandwidthInvalid(op, status)
	}
	for _, p := range wire.Projects {
		if p.Region != "" && p.Region != c.c.Region() {
			continue
		}
		if core.CheckPathID(op, "ProjectID", p.ID) != nil {
			return "", bandwidthInvalid(op, status)
		}
		if id != "" {
			return "", fmt.Errorf("%s: %w", op, core.ErrProjectAmbiguous)
		}
		id = p.ID
	}
	if id == "" {
		return "", fmt.Errorf("%s: %w", op, core.ErrProjectNotFound)
	}
	return id, nil
}

// ListBandwidthPackages reads the configured project's regional catalog.
func (c *Client) ListBandwidthPackages(ctx context.Context, _ *ListBandwidthPackagesInput) (*ListBandwidthPackagesOutput, error) {
	const op = "network.ListBandwidthPackages"
	origin := bandwidthOrigin(c.c.Region())
	if origin == "" {
		return nil, fmt.Errorf("%s: %w", op, core.ErrInvalidConfig)
	}
	ctx = transport.NewDiscoveryContext(ctx)
	project, err := c.bandwidthProject(ctx, op, origin)
	if err != nil {
		return nil, err
	}
	var raw json.RawMessage
	status, err := c.c.DoDiscoveryJSONStatus(ctx, transport.Request{Operation: op, Method: http.MethodGet, URL: origin + "/vserver/bandwidth-api/v1/projects/" + project + "/bandwidth/package-config", OK: []int{200}, MaxBody: 4 << 20, BodyLimitOverridesStatus: true, WithholdMessage: "bandwidth response withheld"}, &raw, nil)
	if err != nil {
		if errors.Is(err, transport.ErrBodyTooLarge) {
			return nil, &core.APIError{Operation: op, StatusCode: status, Message: "bandwidth response withheld", Err: transport.ErrBodyTooLarge}
		}
		if status == 200 {
			return nil, bandwidthInvalid(op, status)
		}
		if status == 0 {
			return nil, err
		}
		var api *core.APIError
		if errors.As(err, &api) {
			code := core.ResolvedCode(status, "")
			if api.Code == "InvalidResponse" && api.Message == "invalid discovery response; body withheld" {
				code = "InvalidResponse"
			}
			return nil, &core.APIError{Operation: op, StatusCode: status, Code: code, Message: "bandwidth response withheld", Retryable: api.Retryable, Err: api.Err}
		}
		return nil, err
	}
	var shape struct {
		Data         []BandwidthPackage `json:"data"`
		Success      any                `json:"success"`
		Code         any                `json:"code"`
		Error        any                `json:"error"`
		Errors       any                `json:"errors"`
		ErrorCode    any                `json:"errorCode"`
		ErrorMsg     any                `json:"errorMsg"`
		ErrorMessage any                `json:"errorMessage"`
	}
	var env bandwidthEnvelope
	if decodeNATEnvelope(raw, &shape, reflect.TypeFor[struct{}]()) != nil || json.Unmarshal(raw, &env) != nil {
		return nil, bandwidthInvalid(op, status)
	}
	if len(env.Success) > 0 && string(env.Success) != "true" || len(env.Code) > 0 && string(env.Code) != "200" {
		return nil, bandwidthInvalid(op, status)
	}
	for _, marker := range []json.RawMessage{env.Error, env.Errors, env.ErrorCode, env.ErrorMsg, env.ErrorMessage} {
		if len(marker) > 0 && string(marker) != "null" {
			return nil, bandwidthInvalid(op, status)
		}
	}
	rows, err := natDecodeRows[BandwidthPackage](op, env.Data, "uuid", "name", "type", "description", "bandwidth")
	if err != nil || bandwidthIncomplete(raw, len(rows)) {
		return nil, bandwidthInvalid(op, status)
	}
	seen := make(map[string]bool)
	for _, row := range rows {
		if row.ID == "" || seen[row.ID] {
			return nil, bandwidthInvalid(op, status)
		}
		seen[row.ID] = true
	}
	return &ListBandwidthPackagesOutput{Items: rows}, nil
}
