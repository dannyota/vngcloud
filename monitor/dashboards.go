package monitor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/jsonresponse"
	"danny.vn/vngcloud/internal/routes"
	"danny.vn/vngcloud/internal/transport"
)

// Dashboard holds the public dashboard projection.
type Dashboard struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	System    bool   `json:"system"`
	Favorite  bool   `json:"favorite"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

// ListDashboardsInput selects a filtered page.
type ListDashboardsInput struct {
	// Page selects a page starting at 1; zero selects 1.
	Page int
	// Size selects 1 to 100 rows; zero selects 10.
	Size int
	// System selects generated resource dashboards instead of custom dashboards.
	System bool
}

// ListDashboardsOutput holds a page of dashboards.
type ListDashboardsOutput = core.PagedList[Dashboard]

// GetDashboardInput identifies a dashboard.
type GetDashboardInput struct {
	// DashboardID is the required path-safe identifier.
	DashboardID string `vngcloud:"required"`
}

// GetDashboardOutput holds the dashboard projection.
type GetDashboardOutput struct{ Dashboard Dashboard }

type dashboardPage struct {
	Items     []json.RawMessage `json:"lstData"`
	Page      *int              `json:"page"`
	PageSize  *int              `json:"pageSize"`
	TotalItem *int              `json:"totalItem"`
	TotalPage *int              `json:"totalPage"`
}

// ListDashboards reads a filtered page of dashboards.
func (c *Client) ListDashboards(ctx context.Context, in *ListDashboardsInput) (*ListDashboardsOutput, error) {
	const op = "monitor.ListDashboards"
	page, size, system := 0, 0, false
	if in != nil {
		page, size, system = in.Page, in.Size, in.System
	}
	if page < 0 || size < 0 || size > 100 {
		return nil, fmt.Errorf("%w: %s: invalid page or size", core.ErrInvalidInput, op)
	}
	if page == 0 {
		page = 1
	}
	if size == 0 {
		size = 10
	}
	filter := "system:false"
	if system {
		filter = "systemType:productVng"
	}
	q := url.Values{"searching-text": {""}, "searching-field": {""}, "filter": {filter}, "page": {strconv.Itoa(page)}, "size": {strconv.Itoa(size)}}
	raw, creds, err := c.dashboardGet(ctx, op, []string{"dashboards"}, q)
	if err != nil {
		return nil, err
	}
	var p dashboardPage
	if jsonresponse.ValidateStrict(raw, &p) != nil || !dashboardExactKeys(raw, []string{"lstData", "page", "pageSize", "totalItem", "totalPage"}) || json.Unmarshal(raw, &p) != nil || p.Items == nil || p.Page == nil || p.PageSize == nil || p.TotalItem == nil || p.TotalPage == nil || *p.Page != page || *p.PageSize != size || *p.TotalItem < 0 || *p.TotalPage < 0 || len(p.Items) > size {
		return nil, dashboardInvalid(op)
	}
	items := make([]Dashboard, 0, len(p.Items))
	seen := map[string]bool{}
	for _, row := range p.Items {
		d, ok := decodeDashboard(row, creds)
		if !ok || d.System != system || seen[d.ID] {
			return nil, dashboardInvalid(op)
		}
		seen[d.ID] = true
		items = append(items, d)
	}
	return core.NewPagedList(items, page, size, *p.TotalPage, *p.TotalItem), nil
}

// GetDashboard reads a dashboard by identifier.
func (c *Client) GetDashboard(ctx context.Context, in *GetDashboardInput) (*GetDashboardOutput, error) {
	const op = "monitor.GetDashboard"
	id := ""
	if in != nil {
		id = in.DashboardID
	}
	if core.CheckPathID(op, "DashboardID", id) != nil {
		return nil, fmt.Errorf("%w: %s requires DashboardID to match ^[A-Za-z0-9-]+$", core.ErrInvalidInput, op)
	}
	raw, creds, err := c.dashboardGet(ctx, op, []string{"dashboards", id}, nil)
	if err != nil {
		return nil, err
	}
	d, ok := decodeDashboard(raw, creds)
	if !ok || d.ID != id {
		return nil, dashboardInvalid(op)
	}
	return &GetDashboardOutput{Dashboard: d}, nil
}

func decodeDashboard(raw json.RawMessage, creds []string) (Dashboard, bool) {
	var d Dashboard
	var fields map[string]json.RawMessage
	var id string
	var system *bool
	if jsonresponse.ValidateStrict(raw, &d) != nil || !dashboardExactKeys(raw, []string{"id", "name", "system", "favorite", "createdAt", "updatedAt"}) || json.Unmarshal(raw, &fields) != nil || fields == nil || json.Unmarshal(fields["id"], &id) != nil || id == "" || json.Unmarshal(fields["system"], &system) != nil || system == nil || json.Unmarshal(raw, &d) != nil {
		return Dashboard{}, false
	}
	for _, credential := range creds {
		for _, value := range []string{d.ID, d.Name, d.CreatedAt, d.UpdatedAt} {
			if credential != "" && strings.Contains(value, credential) {
				return Dashboard{}, false
			}
		}
	}
	return d, true
}

func dashboardInvalid(op string) error {
	return &core.APIError{Operation: op, StatusCode: 200, Code: "InvalidResponse", Message: "vMonitor response withheld"}
}

func (c *Client) dashboardGet(ctx context.Context, op string, parts []string, q url.Values) (json.RawMessage, []string, error) {
	var raw json.RawMessage
	var credentials []string
	full := append([]string{"vmonitor-api", "api", "v1"}, parts...)
	status, err := c.c.DoJSONStatus(ctx, transport.Request{Operation: op, Method: "GET", URL: c.c.RouteURL(routes.Route{Product: routes.ProductMonitor, Parts: full, Query: q}), OK: []int{200}, Sensitive: true, WithholdMessage: "vMonitor response withheld", NoRedirect: true, MaxBody: 4 << 20, SentCredentials: &credentials}, &raw)
	if err != nil {
		if status == 200 && errors.Is(err, transport.ErrBodyTooLarge) {
			return nil, nil, &core.APIError{Operation: op, StatusCode: status, Message: "vMonitor response withheld", Err: transport.ErrBodyTooLarge}
		}
		var api *core.APIError
		if errors.As(err, &api) {
			if api.StatusCode == 0 && status > 0 {
				api.StatusCode = status
			}
			return nil, nil, &core.APIError{Operation: api.Operation, StatusCode: api.StatusCode, Code: core.ResolvedCode(api.StatusCode, ""), Message: api.Message, Retryable: api.Retryable, Err: api.Err}
		}
		return nil, nil, err
	}
	return raw, credentials, nil
}

func dashboardExactKeys(raw json.RawMessage, names []string) bool {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return false
	}
	for key := range fields {
		for _, name := range names {
			if key != name && strings.EqualFold(key, name) {
				return false
			}
		}
	}
	return true
}
