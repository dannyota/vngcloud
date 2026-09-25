package billing

import (
	"context"
	"fmt"
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

type ListBudgetsOutput = core.List[Budget]

func (c *Client) ListBudgets(ctx context.Context, in *ListBudgetsInput) (*ListBudgetsOutput, error) {
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
	if _, err := c.do(ctx, req, &budgets); err != nil {
		return nil, err
	}
	return &ListBudgetsOutput{Items: budgets}, nil
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
	if _, err := c.do(ctx, req, &budget); err != nil {
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
	if _, err := c.do(ctx, req, &cost); err != nil {
		return nil, err
	}
	return &GetCurrentPeriodCostOutput{PeriodCost: cost}, nil
}

// ListBudgetThresholdsInput identifies the budget whose thresholds to list.
type ListBudgetThresholdsInput struct {
	BudgetUUID string `vngcloud:"required"`
}

type ListBudgetThresholdsOutput = core.List[Threshold]

func (c *Client) ListBudgetThresholds(ctx context.Context, in *ListBudgetThresholdsInput) (*ListBudgetThresholdsOutput, error) {
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
	if _, err := c.do(ctx, req, &thresholds); err != nil {
		return nil, err
	}
	return &ListBudgetThresholdsOutput{Items: thresholds}, nil
}

// ListBudgetAlertsInput identifies the budget whose alerts to list, and
// optionally the billing period, in "YYYY-MM" form, to filter to.
type ListBudgetAlertsInput struct {
	BudgetUUID string `vngcloud:"required"`
	PeriodKey  string
}

type ListBudgetAlertsOutput = core.List[Alert]

func (c *Client) ListBudgetAlerts(ctx context.Context, in *ListBudgetAlertsInput) (*ListBudgetAlertsOutput, error) {
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
	if _, err := c.do(ctx, req, &alerts); err != nil {
		return nil, err
	}
	return &ListBudgetAlertsOutput{Items: alerts}, nil
}

// writeOK lists the HTTP statuses a budget or threshold write accepts. Some
// writes answer 204 with an empty body, which do treats as success.
var writeOK = []int{200, 201, 204}

// CreateBudgetInput creates a budget. Status empty sends ACTIVE.
type CreateBudgetInput struct {
	// Name must match ^[a-zA-Z][a-zA-Z0-9 _.@-]*$; the server enforces it.
	Name        string `vngcloud:"required"`
	PeriodType  string `vngcloud:"required"`
	Type        string `vngcloud:"required"`
	LimitAmount int64  `vngcloud:"required"`
	Status      string
}

type CreateBudgetOutput struct {
	Budget Budget
}

type createBudgetBody struct {
	Name        string `json:"name"`
	PeriodType  string `json:"periodType"`
	Type        string `json:"type"`
	LimitAmount int64  `json:"limitAmount"`
	Status      string `json:"status"`
}

// CreateBudget creates a budget. It is not retried after a failure that may
// have already reached the server, because a POST is not idempotent.
func (c *Client) CreateBudget(ctx context.Context, in *CreateBudgetInput) (*CreateBudgetOutput, error) {
	const op = "billing.CreateBudget"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	status := in.Status
	if status == "" {
		status = StatusActive
	}

	var budget Budget
	req := transport.Request{
		Operation: op,
		Method:    http.MethodPost,
		URL:       c.route([]string{"gateway", "api", "v1", "budgets"}, nil),
		Body: createBudgetBody{
			Name:        in.Name,
			PeriodType:  in.PeriodType,
			Type:        in.Type,
			LimitAmount: in.LimitAmount,
			Status:      status,
		},
		OK: writeOK,
	}
	httpStatus, err := c.do(ctx, req, &budget)
	if err != nil {
		return nil, err
	}
	if budget.UUID == "" {
		return nil, &core.APIError{Operation: op, StatusCode: httpStatus, Message: "create response had no uuid"}
	}
	return &CreateBudgetOutput{Budget: budget}, nil
}

// UpdateBudgetInput changes a budget. Only non-nil fields are sent, so
// pausing a budget is UpdateBudget with Status set to StatusPaused and every
// other field left nil.
type UpdateBudgetInput struct {
	BudgetUUID  string `vngcloud:"required"`
	Name        *string
	PeriodType  *string
	Type        *string
	LimitAmount *int64
	Status      *string
}

type UpdateBudgetOutput struct{}

func (c *Client) UpdateBudget(ctx context.Context, in *UpdateBudgetInput) (*UpdateBudgetOutput, error) {
	const op = "billing.UpdateBudget"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "BudgetUUID", in.BudgetUUID); err != nil {
		return nil, err
	}
	if in.Name == nil && in.PeriodType == nil && in.Type == nil && in.LimitAmount == nil && in.Status == nil {
		return nil, fmt.Errorf("%w: %s requires at least one field to change", core.ErrInvalidInput, op)
	}

	body := map[string]any{}
	if in.Name != nil {
		body["name"] = *in.Name
	}
	if in.PeriodType != nil {
		body["periodType"] = *in.PeriodType
	}
	if in.Type != nil {
		body["type"] = *in.Type
	}
	if in.LimitAmount != nil {
		body["limitAmount"] = *in.LimitAmount
	}
	if in.Status != nil {
		body["status"] = *in.Status
	}

	req := transport.Request{
		Operation: op,
		Method:    http.MethodPut,
		URL:       c.route([]string{"gateway", "api", "v1", "budgets", in.BudgetUUID}, nil),
		Body:      body,
		OK:        writeOK,
	}
	if _, err := c.do(ctx, req, nil); err != nil {
		return nil, err
	}
	return &UpdateBudgetOutput{}, nil
}

// DeleteBudgetInput identifies the budget to delete. Deleting a budget
// deletes its thresholds on the server; the SDK does not delete them first.
type DeleteBudgetInput struct {
	BudgetUUID string `vngcloud:"required"`
}

type DeleteBudgetOutput struct{}

func (c *Client) DeleteBudget(ctx context.Context, in *DeleteBudgetInput) (*DeleteBudgetOutput, error) {
	const op = "billing.DeleteBudget"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "BudgetUUID", in.BudgetUUID); err != nil {
		return nil, err
	}

	req := transport.Request{
		Operation: op,
		Method:    http.MethodDelete,
		URL:       c.route([]string{"gateway", "api", "v1", "budgets", in.BudgetUUID}, nil),
		OK:        writeOK,
	}
	if _, err := c.do(ctx, req, nil); err != nil {
		return nil, err
	}
	return &DeleteBudgetOutput{}, nil
}

// Threshold console defaults, sent when the caller leaves the field zero.
const (
	defaultMaxAlertsPerPeriod    = 1
	defaultReminderIntervalHours = 648
)

// CreateBudgetThresholdInput creates an alert threshold on a budget. The SDK
// always sends comparisonOperator "GTE", the only value the console offers.
type CreateBudgetThresholdInput struct {
	BudgetUUID    string `vngcloud:"required"`
	ThresholdType string `vngcloud:"required"`
	// ThresholdPercentage must be 1 or more; the server enforces it.
	ThresholdPercentage int `vngcloud:"required"`
	// MaxAlertsPerPeriod 0 sends the console default, 1.
	MaxAlertsPerPeriod int
	// ReminderIntervalHours 0 sends the console default, 648.
	ReminderIntervalHours int
}

type CreateBudgetThresholdOutput struct {
	Threshold Threshold
}

// createThresholdBody has no enabled field: the server ignores it on create
// and always starts the threshold enabled. Disable it with UpdateBudgetThreshold.
type createThresholdBody struct {
	ThresholdType         string `json:"thresholdType"`
	ThresholdPercentage   int    `json:"thresholdPercentage"`
	ComparisonOperator    string `json:"comparisonOperator"`
	MaxAlertsPerPeriod    int    `json:"maxAlertsPerPeriod"`
	ReminderIntervalHours int    `json:"reminderIntervalHours"`
}

func (c *Client) CreateBudgetThreshold(ctx context.Context, in *CreateBudgetThresholdInput) (*CreateBudgetThresholdOutput, error) {
	const op = "billing.CreateBudgetThreshold"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "BudgetUUID", in.BudgetUUID); err != nil {
		return nil, err
	}

	maxAlerts := in.MaxAlertsPerPeriod
	if maxAlerts == 0 {
		maxAlerts = defaultMaxAlertsPerPeriod
	}
	reminder := in.ReminderIntervalHours
	if reminder == 0 {
		reminder = defaultReminderIntervalHours
	}

	var threshold Threshold
	req := transport.Request{
		Operation: op,
		Method:    http.MethodPost,
		URL:       c.route([]string{"gateway", "api", "v1", "budgets", in.BudgetUUID, "thresholds"}, nil),
		Body: createThresholdBody{
			ThresholdType:         in.ThresholdType,
			ThresholdPercentage:   in.ThresholdPercentage,
			ComparisonOperator:    "GTE",
			MaxAlertsPerPeriod:    maxAlerts,
			ReminderIntervalHours: reminder,
		},
		OK: writeOK,
	}
	httpStatus, err := c.do(ctx, req, &threshold)
	if err != nil {
		return nil, err
	}
	if threshold.UUID == "" {
		return nil, &core.APIError{Operation: op, StatusCode: httpStatus, Message: "create response had no uuid"}
	}
	return &CreateBudgetThresholdOutput{Threshold: threshold}, nil
}

// UpdateBudgetThresholdInput changes a threshold. Only non-nil fields are sent.
type UpdateBudgetThresholdInput struct {
	BudgetUUID            string `vngcloud:"required"`
	ThresholdUUID         string `vngcloud:"required"`
	ThresholdPercentage   *int
	Enabled               *bool
	MaxAlertsPerPeriod    *int
	ReminderIntervalHours *int
}

type UpdateBudgetThresholdOutput struct{}

func (c *Client) UpdateBudgetThreshold(ctx context.Context, in *UpdateBudgetThresholdInput) (*UpdateBudgetThresholdOutput, error) {
	const op = "billing.UpdateBudgetThreshold"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "BudgetUUID", in.BudgetUUID); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "ThresholdUUID", in.ThresholdUUID); err != nil {
		return nil, err
	}
	if in.ThresholdPercentage == nil && in.Enabled == nil && in.MaxAlertsPerPeriod == nil && in.ReminderIntervalHours == nil {
		return nil, fmt.Errorf("%w: %s requires at least one field to change", core.ErrInvalidInput, op)
	}

	body := map[string]any{}
	if in.ThresholdPercentage != nil {
		body["thresholdPercentage"] = *in.ThresholdPercentage
	}
	if in.Enabled != nil {
		body["enabled"] = *in.Enabled
	}
	if in.MaxAlertsPerPeriod != nil {
		body["maxAlertsPerPeriod"] = *in.MaxAlertsPerPeriod
	}
	if in.ReminderIntervalHours != nil {
		body["reminderIntervalHours"] = *in.ReminderIntervalHours
	}

	req := transport.Request{
		Operation: op,
		Method:    http.MethodPut,
		URL:       c.route([]string{"gateway", "api", "v1", "budgets", in.BudgetUUID, "thresholds", in.ThresholdUUID}, nil),
		Body:      body,
		OK:        writeOK,
	}
	if _, err := c.do(ctx, req, nil); err != nil {
		return nil, err
	}
	return &UpdateBudgetThresholdOutput{}, nil
}

// DeleteBudgetThresholdInput identifies the threshold to delete.
type DeleteBudgetThresholdInput struct {
	BudgetUUID    string `vngcloud:"required"`
	ThresholdUUID string `vngcloud:"required"`
}

type DeleteBudgetThresholdOutput struct{}

func (c *Client) DeleteBudgetThreshold(ctx context.Context, in *DeleteBudgetThresholdInput) (*DeleteBudgetThresholdOutput, error) {
	const op = "billing.DeleteBudgetThreshold"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "BudgetUUID", in.BudgetUUID); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "ThresholdUUID", in.ThresholdUUID); err != nil {
		return nil, err
	}

	req := transport.Request{
		Operation: op,
		Method:    http.MethodDelete,
		URL:       c.route([]string{"gateway", "api", "v1", "budgets", in.BudgetUUID, "thresholds", in.ThresholdUUID}, nil),
		OK:        writeOK,
	}
	if _, err := c.do(ctx, req, nil); err != nil {
		return nil, err
	}
	return &DeleteBudgetThresholdOutput{}, nil
}
