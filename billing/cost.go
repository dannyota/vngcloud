package billing

import (
	"context"
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
	StartDate    string `vngcloud:"required"`
	EndDate      string `vngcloud:"required"`
	Product      string
	ResourceType string
	ResourceID   string
	Query        string

	// GroupBy is "product", "resourceType", or "resourceId"; empty sends "product".
	GroupBy string
	// Interval is "hourly", "daily", "weekly", or "monthly"; empty sends "daily".
	Interval string
}

type GetCostOverviewOutput struct {
	Summary   CostSummary
	Series    []CostSeries
	Interval  string
	StartDate string
	EndDate   string
	GroupBy   string
}

// costOverviewData is the wire shape of GetCostOverview's data field.
type costOverviewData struct {
	Summary   CostSummary  `json:"summary"`
	Series    []CostSeries `json:"series"`
	Interval  string       `json:"interval"`
	StartDate string       `json:"startDate"`
	EndDate   string       `json:"endDate"`
	GroupBy   string       `json:"groupBy"`
}

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
	}
	if _, err := c.do(ctx, req, &data); err != nil {
		return nil, err
	}
	return &GetCostOverviewOutput{
		Summary:   data.Summary,
		Series:    data.Series,
		Interval:  data.Interval,
		StartDate: data.StartDate,
		EndDate:   data.EndDate,
		GroupBy:   data.GroupBy,
	}, nil
}

// ListCostResourcesInput scopes the per-resource cost list to a date range,
// optional filters, and a page.
type ListCostResourcesInput struct {
	StartDate    string `vngcloud:"required"`
	EndDate      string `vngcloud:"required"`
	Product      string
	ResourceType string
	ResourceID   string
	Query        string
	Page         int
	Size         int
	Sort         string
	Order        string
}

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
	q.Set("page", strconv.Itoa(page))
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
	}
	if _, err := c.do(ctx, req, &data); err != nil {
		return nil, err
	}
	return &ListCostResourcesOutput{
		PagedList: *core.NewPagedList(data.Items, data.Pagination.Page, data.Pagination.Size, data.Pagination.TotalPages, data.Pagination.Total),
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

type GetBalancesOutput struct {
	Balances Balances
}

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
	}
	if err := c.doBalances(ctx, req, &balances); err != nil {
		return nil, err
	}
	return &GetBalancesOutput{Balances: balances}, nil
}
