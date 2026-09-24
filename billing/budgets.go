package billing

import (
	"context"
	"net/http"
	"net/url"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/transport"
)

// ListBudgetsInput lists budgets in summary view.
type ListBudgetsInput struct {
	// Status filters the list to ACTIVE or PAUSED budgets; empty returns both.
	Status string
}

func (c *Client) ListBudgets(ctx context.Context, in *ListBudgetsInput) (*core.List[Budget], error) {
	if err := core.CheckRequired("billing.ListBudgets", in); err != nil {
		return nil, err
	}
	if in == nil {
		in = &ListBudgetsInput{}
	}
	q := url.Values{}
	q.Set("view", "summary")
	if in.Status != "" {
		q.Set("status", in.Status)
	}

	var budgets []Budget
	req := transport.Request{
		Operation: "billing.ListBudgets",
		Method:    http.MethodGet,
		URL:       c.route([]string{"gateway", "api", "v1", "budgets"}, q),
		OK:        []int{200},
	}
	if err := c.do(ctx, req, &budgets); err != nil {
		return nil, err
	}
	return &core.List[Budget]{Items: budgets}, nil
}

// GetBudgetInput identifies the budget to read.
type GetBudgetInput struct {
	BudgetUUID string `vngcloud:"required"`
}

type GetBudgetOutput struct {
	Budget Budget
}

func (c *Client) GetBudget(ctx context.Context, in *GetBudgetInput) (*GetBudgetOutput, error) {
	const op = "billing.GetBudget"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "BudgetUUID", in.BudgetUUID); err != nil {
		return nil, err
	}

	var budget Budget
	req := transport.Request{
		Operation: op,
		Method:    http.MethodGet,
		URL:       c.route([]string{"gateway", "api", "v1", "budgets", in.BudgetUUID}, nil),
		OK:        []int{200},
	}
	if err := c.do(ctx, req, &budget); err != nil {
		return nil, err
	}
	return &GetBudgetOutput{Budget: budget}, nil
}

// GetCurrentPeriodCostInput takes no fields; the API reports the account's
// current billing period.
type GetCurrentPeriodCostInput struct{}

type GetCurrentPeriodCostOutput struct {
	PeriodCost PeriodCost
}

func (c *Client) GetCurrentPeriodCost(ctx context.Context, in *GetCurrentPeriodCostInput) (*GetCurrentPeriodCostOutput, error) {
	const op = "billing.GetCurrentPeriodCost"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}

	var cost PeriodCost
	req := transport.Request{
		Operation: op,
		Method:    http.MethodGet,
		URL:       c.route([]string{"gateway", "api", "v1", "budgets", "cost", "overview"}, nil),
		OK:        []int{200},
	}
	if err := c.do(ctx, req, &cost); err != nil {
		return nil, err
	}
	return &GetCurrentPeriodCostOutput{PeriodCost: cost}, nil
}

// ListBudgetThresholdsInput identifies the budget whose thresholds to list.
type ListBudgetThresholdsInput struct {
	BudgetUUID string `vngcloud:"required"`
}

func (c *Client) ListBudgetThresholds(ctx context.Context, in *ListBudgetThresholdsInput) (*core.List[Threshold], error) {
	const op = "billing.ListBudgetThresholds"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "BudgetUUID", in.BudgetUUID); err != nil {
		return nil, err
	}

	var thresholds []Threshold
	req := transport.Request{
		Operation: op,
		Method:    http.MethodGet,
		URL:       c.route([]string{"gateway", "api", "v1", "budgets", in.BudgetUUID, "thresholds"}, nil),
		OK:        []int{200},
	}
	if err := c.do(ctx, req, &thresholds); err != nil {
		return nil, err
	}
	return &core.List[Threshold]{Items: thresholds}, nil
}

// ListBudgetAlertsInput identifies the budget whose alerts to list, and
// optionally the billing period, in "YYYY-MM" form, to filter to.
type ListBudgetAlertsInput struct {
	BudgetUUID string `vngcloud:"required"`
	PeriodKey  string
}

func (c *Client) ListBudgetAlerts(ctx context.Context, in *ListBudgetAlertsInput) (*core.List[Alert], error) {
	const op = "billing.ListBudgetAlerts"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "BudgetUUID", in.BudgetUUID); err != nil {
		return nil, err
	}
	q := url.Values{}
	if in.PeriodKey != "" {
		q.Set("periodKey", in.PeriodKey)
	}

	var alerts []Alert
	req := transport.Request{
		Operation: op,
		Method:    http.MethodGet,
		URL:       c.route([]string{"gateway", "api", "v1", "budgets", in.BudgetUUID, "alerts"}, q),
		OK:        []int{200},
	}
	if err := c.do(ctx, req, &alerts); err != nil {
		return nil, err
	}
	return &core.List[Alert]{Items: alerts}, nil
}
