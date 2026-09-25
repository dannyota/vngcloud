package billing

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
)

// Budget enum values, sent and received on the wire as is.
const (
	PeriodMonthly   = "MONTHLY"
	PeriodQuarterly = "QUARTERLY"
	TypeActual      = "ACTUAL"
	TypeForecasted  = "FORECASTED"
	StatusActive    = "ACTIVE"
	StatusPaused    = "PAUSED"
)

// Budget is a spend cap on one period type. GetBudget may omit the cost and
// percentage fields, so they are pointers.
type Budget struct {
	UUID                 string   `json:"uuid"`
	ID                   int64    `json:"id"`
	Name                 string   `json:"name"`
	PeriodType           string   `json:"periodType"`
	Type                 string   `json:"type"`
	LimitAmount          int64    `json:"limitAmount"`
	Currency             string   `json:"currency"`
	Status               string   `json:"status"`
	PeriodKey            string   `json:"periodKey"`
	PeriodStart          string   `json:"periodStart"`
	PeriodEnd            string   `json:"periodEnd"`
	StartDate            string   `json:"startDate"`
	CreatedAt            string   `json:"createdAt"`
	UpdatedAt            string   `json:"updatedAt"`
	ActualCost           *float64 `json:"actualCost"`
	ForecastedCost       *float64 `json:"forecastedCost"`
	ActualPercentage     *float64 `json:"actualPercentage"`
	ForecastedPercentage *float64 `json:"forecastedPercentage"`
	ThresholdPercentage  *float64 `json:"thresholdPercentage"`
	Alarm                bool     `json:"alarm"`
	ThresholdCount       int      `json:"thresholdCount"`
	AlarmThresholdCount  int      `json:"alarmThresholdCount"`
}

// budgetLimitAmount decodes limitAmount, which the API sends as a plain
// integer from CreateBudget and as an integral decimal such as
// 9999999999.0 from ListBudgets and GetBudget. A fractional value is
// rejected rather than truncated.
type budgetLimitAmount int64

func (a *budgetLimitAmount) UnmarshalJSON(data []byte) error {
	var f float64
	if err := json.Unmarshal(data, &f); err != nil {
		return err
	}
	if f != math.Trunc(f) {
		return errors.New("billing: limitAmount is not an integer")
	}
	*a = budgetLimitAmount(f)
	return nil
}

// UnmarshalJSON decodes Budget with LimitAmount routed through
// budgetLimitAmount, so both wire shapes land in the int64 field.
func (b *Budget) UnmarshalJSON(data []byte) error {
	type alias Budget
	aux := struct {
		LimitAmount budgetLimitAmount `json:"limitAmount"`
		*alias
	}{alias: (*alias)(b)}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	b.LimitAmount = int64(aux.LimitAmount)
	return nil
}

// PeriodCost is the current billing period's actual and forecasted cost.
type PeriodCost struct {
	PeriodKey      string  `json:"periodKey"`
	PeriodStart    string  `json:"periodStart"`
	PeriodEnd      string  `json:"periodEnd"`
	ActualCost     float64 `json:"actualCost"`
	ForecastedCost float64 `json:"forecastedCost"`
}

// Threshold triggers a budget alert once its percentage is reached.
type Threshold struct {
	UUID                  string  `json:"uuid"`
	ThresholdType         string  `json:"thresholdType"`
	ThresholdPercentage   float64 `json:"thresholdPercentage"`
	ComparisonOperator    string  `json:"comparisonOperator"`
	Enabled               bool    `json:"enabled"`
	MaxAlertsPerPeriod    int     `json:"maxAlertsPerPeriod"`
	ReminderIntervalHours int     `json:"reminderIntervalHours"`
	NotificationState     string  `json:"notificationState"`
	LastAlertAt           string  `json:"lastAlertAt"`
}

// Alert is one threshold breach notification.
type Alert struct {
	TriggeredAt         string
	ThresholdType       string
	ThresholdPercentage float64
	TriggeredValue      float64
	ActualPercentage    float64
	DeliveryStatus      string

	// Recipients holds the array the API sends as a JSON string. When that
	// string does not hold an array of strings, Recipients stays nil and
	// RecipientsRaw keeps the string, so one odd row does not fail the list.
	Recipients    []string
	RecipientsRaw string
}

type alertWire struct {
	TriggeredAt         string  `json:"triggeredAt"`
	ThresholdType       string  `json:"thresholdType"`
	ThresholdPercentage float64 `json:"thresholdPercentage"`
	TriggeredValue      float64 `json:"triggeredValue"`
	ActualPercentage    float64 `json:"actualPercentage"`
	DeliveryStatus      string  `json:"deliveryStatus"`
	Recipients          string  `json:"recipients"`
}

func (a *Alert) UnmarshalJSON(data []byte) error {
	var wire alertWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	a.TriggeredAt = wire.TriggeredAt
	a.ThresholdType = wire.ThresholdType
	a.ThresholdPercentage = wire.ThresholdPercentage
	a.TriggeredValue = wire.TriggeredValue
	a.ActualPercentage = wire.ActualPercentage
	a.DeliveryStatus = wire.DeliveryStatus

	var recipients []string
	if err := json.Unmarshal([]byte(wire.Recipients), &recipients); err == nil {
		a.Recipients = recipients
	} else {
		a.RecipientsRaw = wire.Recipients
	}
	return nil
}

// CostBreakdown is one entry in a cost summary's per-key breakdown.
type CostBreakdown struct {
	Key  string  `json:"key"`
	Cost float64 `json:"cost"`
}

// CostSummary is the cost explorer overview's headline numbers.
type CostSummary struct {
	CurrentCost    float64         `json:"currentCost"`
	LastPeriodCost float64         `json:"lastPeriodCost"`
	ChangePercent  float64         `json:"changePercent"`
	ForecastCost   float64         `json:"forecastCost"`
	ActiveCount    int             `json:"activeCount"`
	Breakdown      []CostBreakdown `json:"breakdown"`
}

// CostSeries is one point in the cost explorer overview's trend chart.
type CostSeries struct {
	Date string  `json:"date"`
	Cost float64 `json:"cost"`
}

// CostResource is one row of the per-resource cost list.
type CostResource struct {
	ResourceID   string  `json:"resourceId"`
	ResourceName string  `json:"resourceName"`
	ResourceType string  `json:"resourceType"`
	Product      string  `json:"product"`
	Cost         float64 `json:"cost"`
}

// CostResourceSummary totals the per-resource cost list.
type CostResourceSummary struct {
	TotalAll       float64 `json:"totalAll"`
	ActiveCount    int     `json:"activeCount"`
	AvgPerResource float64 `json:"avgPerResource"`
}

// Balances holds an account's cash and POC (pay-on-credit) balances. Each
// field is nil when the account has no balance of that kind.
type Balances struct {
	Cash          *float64
	POC           *float64
	CashAvailable *float64
	CashHolding   *float64
	POCHolding    *float64
}

type balancesWire struct {
	Cash          json.RawMessage `json:"cash"`
	POC           json.RawMessage `json:"poc"`
	CashAvailable json.RawMessage `json:"cashAvailable"`
	CashHolding   json.RawMessage `json:"cashHolding"`
	POCHolding    json.RawMessage `json:"pocHolding"`
}

func (b *Balances) UnmarshalJSON(data []byte) error {
	var wire balancesWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	var err error
	if b.Cash, err = decodeMoney("cash", wire.Cash); err != nil {
		return err
	}
	if b.POC, err = decodeMoney("poc", wire.POC); err != nil {
		return err
	}
	if b.CashAvailable, err = decodeMoney("cashAvailable", wire.CashAvailable); err != nil {
		return err
	}
	if b.CashHolding, err = decodeMoney("cashHolding", wire.CashHolding); err != nil {
		return err
	}
	if b.POCHolding, err = decodeMoney("pocHolding", wire.POCHolding); err != nil {
		return err
	}
	return nil
}

// decodeMoney accepts a JSON null, a JSON number, or a numeric string, since
// the balances endpoint has sent amounts as quoted strings. field names the
// balance field being decoded, for an error message that says which field
// failed without echoing the value that failed to parse.
func decodeMoney(field string, raw json.RawMessage) (*float64, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return nil, nil
	}
	if trimmed[0] == '"' {
		var s string
		if err := json.Unmarshal(trimmed, &s); err != nil {
			return nil, fmt.Errorf("billing: %s is not a string", field)
		}
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return nil, fmt.Errorf("billing: %s is not a number", field)
		}
		return &f, nil
	}
	var f float64
	if err := json.Unmarshal(trimmed, &f); err != nil {
		return nil, fmt.Errorf("billing: %s is not a number", field)
	}
	return &f, nil
}
