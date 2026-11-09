package billing

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/url"
	"strconv"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/transport"
)

// maxCostResourcesSize is the largest page size ListCostResources accepts;
// the API returns HTTP 400 above it.
const maxCostResourcesSize = 200

// GetCostOverviewInput scopes the cost explorer overview to a date range and
// optional filters.
type GetCostOverviewInput struct {
	// StartDate is required in YYYY-MM-DD form.
	StartDate string `vngcloud:"required"`
	// EndDate is required in YYYY-MM-DD form.
	EndDate string `vngcloud:"required"`
	// Product is sent as the product filter when nonempty.
	Product string
	// ResourceType is sent as the resourceType filter when nonempty.
	ResourceType string
	// ResourceID is sent as the resourceId filter when nonempty.
	ResourceID string
	// Query is sent as the q filter when nonempty.
	Query string

	// GroupBy is "product", "resourceType", or "resourceId"; empty sends "product".
	GroupBy string
	// Interval is "hourly", "daily", "weekly", or "monthly"; empty sends "daily".
	Interval string
}

// GetCostOverviewOutput holds the cost summary and grouped series.
type GetCostOverviewOutput struct {
	Summary CostSummary
	// Series retains legacy flat date/cost responses. Grouped responses leave it nil.
	Series []CostSeries
	// GroupedSeries holds the API's series groups, colors, and points in their returned order.
	GroupedSeries []CostSeriesGroup
	Interval      string
	StartDate     string
	EndDate       string
	GroupBy       string
}

// costOverviewData is the wire shape of GetCostOverview's data field.
type costOverviewData struct {
	Summary   CostSummary          `json:"summary"`
	Series    []costOverviewSeries `json:"series"`
	Interval  string               `json:"interval"`
	StartDate string               `json:"startDate"`
	EndDate   string               `json:"endDate"`
	GroupBy   string               `json:"groupBy"`
}

type costOverviewSeries struct {
	grouped bool
	group   CostSeriesGroup
	point   CostSeries
}

func (s *costOverviewSeries) UnmarshalJSON(data []byte) error {
	var fields struct {
		Key    json.RawMessage `json:"key"`
		Color  json.RawMessage `json:"color"`
		Points json.RawMessage `json:"points"`
	}
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	s.grouped = len(fields.Key) > 0 || len(fields.Color) > 0 || len(fields.Points) > 0
	if s.grouped {
		return json.Unmarshal(data, &s.group)
	}
	return json.Unmarshal(data, &s.point)
}

// GetCostOverview reads a cost summary for the selected dates and filters.
func (c *Client) GetCostOverview(ctx context.Context, in *GetCostOverviewInput) (*GetCostOverviewOutput, error) {
	const op = "billing.GetCostOverview"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckDate(op, "StartDate", in.StartDate); err != nil {
		return nil, err
	}
	if err := core.CheckDate(op, "EndDate", in.EndDate); err != nil {
		return nil, err
	}

	q := costQuery(in.StartDate, in.EndDate, in.Product, in.ResourceType, in.ResourceID, in.Query)
	groupBy := in.GroupBy
	if groupBy == "" {
		groupBy = "product"
	}
	interval := in.Interval
	if interval == "" {
		interval = "daily"
	}
	q.Set("groupBy", groupBy)
	q.Set("interval", interval)

	var data costOverviewData
	req := transport.Request{
		Operation: op,
		Method:    http.MethodGet,
		URL:       c.route([]string{"gateway", "api", "v2", "cost-explorer", "overview"}, q),
		OK:        []int{200},
		MaxBody:   8 << 20,
	}
	if _, err := c.do(ctx, req, &data); err != nil {
		return nil, err
	}
	out := &GetCostOverviewOutput{
		Summary:   data.Summary,
		Interval:  data.Interval,
		StartDate: data.StartDate,
		EndDate:   data.EndDate,
		GroupBy:   data.GroupBy,
	}
	if data.Series != nil && len(data.Series) == 0 {
		out.Series = []CostSeries{}
	}
	for _, series := range data.Series {
		if series.grouped {
			out.GroupedSeries = append(out.GroupedSeries, series.group)
		} else {
			out.Series = append(out.Series, series.point)
		}
	}
	return out, nil
}

// ListCostResourcesInput scopes the per-resource cost list to a date range,
// optional filters, and a page. Page starts at 1; non-positive values select
// the first page.
type ListCostResourcesInput struct {
	// StartDate is required in YYYY-MM-DD form.
	StartDate string `vngcloud:"required"`
	// EndDate is required in YYYY-MM-DD form.
	EndDate string `vngcloud:"required"`
	// Product is sent as the product filter when nonempty.
	Product string
	// ResourceType is sent as the resourceType filter when nonempty.
	ResourceType string
	// ResourceID is sent as the resourceId filter when nonempty.
	ResourceID string
	// Query is sent as the q filter when nonempty.
	Query string
	// Page starts at 1; nonpositive values select the first page.
	Page int
	// Size defaults to 200 when nonpositive or above 200.
	Size int
	// Sort is sent as the sort parameter when nonempty.
	Sort string
	// Order is sent as the order parameter when nonempty.
	Order string
}

// ListCostResourcesOutput holds a page of resource costs and its summary.
type ListCostResourcesOutput struct {
	core.PagedList[CostResource]
	Summary CostResourceSummary
}

type costResourcesPage struct {
	Page       int `json:"page"`
	Size       int `json:"size"`
	Total      int `json:"total"`
	TotalPages int `json:"totalPages"`
}

// costResourcesData is the wire shape of ListCostResources's data field.
type costResourcesData struct {
	Items      []CostResource      `json:"items"`
	Pagination costResourcesPage   `json:"pagination"`
	Summary    CostResourceSummary `json:"summary"`
}

// ListCostResources reads a page of resource costs for the selected dates.
func (c *Client) ListCostResources(ctx context.Context, in *ListCostResourcesInput) (*ListCostResourcesOutput, error) {
	const op = "billing.ListCostResources"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckDate(op, "StartDate", in.StartDate); err != nil {
		return nil, err
	}
	if err := core.CheckDate(op, "EndDate", in.EndDate); err != nil {
		return nil, err
	}

	q := costQuery(in.StartDate, in.EndDate, in.Product, in.ResourceType, in.ResourceID, in.Query)
	page := in.Page
	if page <= 0 {
		page = 1
	}
	size := in.Size
	if size <= 0 || size > maxCostResourcesSize {
		size = maxCostResourcesSize
	}
	q.Set("page", strconv.Itoa(page-1))
	q.Set("size", strconv.Itoa(size))
	if in.Sort != "" {
		q.Set("sort", in.Sort)
	}
	if in.Order != "" {
		q.Set("order", in.Order)
	}

	var data costResourcesData
	req := transport.Request{
		Operation: op,
		Method:    http.MethodGet,
		URL:       c.route([]string{"gateway", "api", "v2", "cost-explorer", "resources"}, q),
		OK:        []int{200},
		MaxBody:   8 << 20,
	}
	if _, err := c.do(ctx, req, &data); err != nil {
		return nil, err
	}
	if data.Pagination.Page < 0 || data.Pagination.Page == math.MaxInt {
		return nil, &core.APIError{
			Operation:  op,
			StatusCode: http.StatusOK,
			Code:       "InvalidResponse",
			Message:    "invalid cost resource page; body withheld",
		}
	}
	return &ListCostResourcesOutput{
		PagedList: *core.NewPagedList(data.Items, data.Pagination.Page+1, data.Pagination.Size, data.Pagination.TotalPages, data.Pagination.Total),
		Summary:   data.Summary,
	}, nil
}

// costQuery builds the query parameters shared by both cost explorer reads.
func costQuery(startDate, endDate, product, resourceType, resourceID, query string) url.Values {
	q := url.Values{}
	q.Set("startDate", startDate)
	q.Set("endDate", endDate)
	if product != "" {
		q.Set("product", product)
	}
	if resourceType != "" {
		q.Set("resourceType", resourceType)
	}
	if resourceID != "" {
		q.Set("resourceId", resourceID)
	}
	if query != "" {
		q.Set("q", query)
	}
	return q
}

// GetBalancesInput takes no fields; balances are read for the account the
// caller is authenticated as.
type GetBalancesInput struct{}

// GetBalancesOutput holds the account balances.
type GetBalancesOutput struct {
	Balances Balances
}

// GetBalances reads balances for the authenticated account.
func (c *Client) GetBalances(ctx context.Context, in *GetBalancesInput) (*GetBalancesOutput, error) {
	const op = "billing.GetBalances"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}

	var balances Balances
	req := transport.Request{
		Operation: op,
		Method:    http.MethodGet,
		URL:       c.route([]string{"navbar", "balances", "v1"}, nil),
		OK:        []int{200},
		MaxBody:   8 << 20,
	}
	if err := c.doBalances(ctx, req, &balances); err != nil {
		return nil, err
	}
	return &GetBalancesOutput{Balances: balances}, nil
}
