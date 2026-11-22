//revive:disable:exported Internal contracts live in docs/design/cost-estimate.md.
package pricecatalog

import (
	"encoding/json"
	"net/http"
	"net/url"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/discovery"
	"danny.vn/vngcloud/internal/routes"
	"danny.vn/vngcloud/internal/transport"
)

func Invalid(op string, status int) error {
	return &core.APIError{Operation: op, StatusCode: status, Code: "InvalidResponse", Message: "invalid discovery response; body withheld"}
}

func Array[T any](op string, status int, raw json.RawMessage, key string) ([]T, error) {
	if ValidateFields(raw, &EnvelopeFields{}) != nil {
		return nil, Invalid(op, status)
	}
	if key != "" {
		var fields map[string]json.RawMessage
		if json.Unmarshal(raw, &fields) != nil || fields == nil {
			return nil, Invalid(op, status)
		}
		for _, name := range []string{"page", "paging", "pagination", "pageSize", "totalPage", "totalPages", "totalItem", "next", "nextPage", "isNext", "hasMore", "continuationToken"} {
			if _, ok := fields[name]; ok {
				return nil, Invalid(op, status)
			}
		}
		if v, ok := fields["success"]; ok && string(v) != "true" {
			return nil, Invalid(op, status)
		}
		raw = fields[key]
	}
	var values []T
	if len(raw) == 0 || ValidateFields(raw, &values) != nil || json.Unmarshal(raw, &values) != nil || values == nil {
		return nil, Invalid(op, status)
	}
	return values, nil
}

type Zone struct {
	ID      string `json:"uuid"`
	Name    string `json:"name"`
	Enabled *bool  `json:"isEnabled"`
}

func regionalRead(c *core.Client, s *discovery.Session, op string, parts []string, q url.Values, validate func(int, json.RawMessage) error) error {
	_, err := s.DoJSON(c, transport.Request{Operation: op, Method: http.MethodGet, URL: c.RouteURL(routes.Route{Product: routes.ProductVServer, Version: "v1", Parts: parts, Query: q}), OK: []int{200}}, nil, validate)
	return err
}

func Zones(c *core.Client, s *discovery.Session, project string, validationOp ...string) ([]Zone, error) {
	op := "portal.ListZones"
	checkOp := op
	if len(validationOp) > 0 {
		checkOp = validationOp[0]
	}
	var values []Zone
	err := regionalRead(c, s, op, []string{project, "zones"}, nil, func(status int, raw json.RawMessage) error {
		var err error
		values, err = Array[Zone](checkOp, status, raw, "data")
		if err != nil {
			return err
		}
		seen := map[string]bool{}
		for _, z := range values {
			if core.CheckPathID(checkOp, "ZoneID", z.ID) != nil || z.Enabled == nil || seen[z.ID] {
				return Invalid(checkOp, status)
			}
			seen[z.ID] = true
		}
		return s.AddRows(len(values))
	})
	return values, err
}

func RegionalQuote(c *core.Client, op string, body any) transport.Request {
	return transport.Request{Operation: op, Method: http.MethodPost, URL: c.RouteURL(routes.Route{Product: routes.ProductPortal, Version: "v1", Parts: []string{"price"}}), Body: body, OK: []int{200}}
}

func StorageURL(c *core.Client, version string, parts []string, q url.Values) string {
	return c.RouteURL(routes.Route{Product: routes.ProductStorage, Version: version, Parts: parts, Query: q})
}

func StorageHeaders(id string) map[string]string {
	return map[string]string{"region": id, "region_id": id}
}

func StorageQuote(c *core.Client, op, id string, body any) transport.Request {
	return transport.Request{Operation: op, Method: http.MethodPost, URL: StorageURL(c, "billing-api/v2", []string{"price"}, url.Values{"region_id": {id}}), Headers: StorageHeaders(id), Body: body, OK: []int{200}}
}
