package monitor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"time"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/transport"
)

// ErrUnpriced refuses a zero quote for a package with a positive list price.
var ErrUnpriced = vngcloud.ErrUnpriced

type CreateNotificationQuotaInput struct {
	Kind      string `vngcloud:"required"`
	PackageID string
	MaxPrice  float64
	NoWait    bool
}

type QuotaQuote struct {
	PackageID       string
	Quantity        int
	MonthPeriod     int
	ResourceID      string
	ExpectedEndTime *time.Time
	OptimumPrice    float64
	ListPrice       float64
	GuardPrice      float64
	Currency        string
}

type QuoteCreateNotificationQuotaOutput = QuotaQuote

// Terms contain copied values only, so later input or catalog changes cannot
// alter the one-month resource described by either body.
type notificationCreateTerms struct {
	kind        string
	packageID   string
	monthPeriod int
	listPrice   float64
}

type notificationCreateQuoteBody struct {
	RedirectURL string `json:"redirectUrl"`
	PackageID   string `json:"packageId"`
	Quantity    int    `json:"quantity"`
}

type notificationCreateOrderBody struct {
	MonthPeriod int    `json:"monthPeriod"`
	PackageID   string `json:"packageId"`
	RedirectURL string `json:"redirectUrl"`
	Pay         bool   `json:"pay"`
}

func (t notificationCreateTerms) quoteBody() notificationCreateQuoteBody {
	return notificationCreateQuoteBody{PackageID: t.packageID, Quantity: 1}
}

func (t notificationCreateTerms) orderBody() notificationCreateOrderBody {
	return notificationCreateOrderBody{MonthPeriod: t.monthPeriod, PackageID: t.packageID, RedirectURL: "https://vmonitor.console.vngcloud.vn/quota-usages/notification", Pay: true}
}

func (c *Client) prepareNotificationCreate(ctx context.Context, op string, in *CreateNotificationQuotaInput) (notificationCreateTerms, error) {
	var terms notificationCreateTerms
	if err := core.CheckRequired(op, in); err != nil {
		return terms, err
	}
	kind, err := notificationEmailKind(op, in.Kind)
	if err != nil {
		return terms, err
	}
	// Freeze caller values before the first request.
	packageID := in.PackageID
	_, err = c.readNotificationQuota(ctx, op, kind)
	if err == nil {
		return terms, fmt.Errorf("%s: an Email quota already exists: %w", op, core.ErrInvalidInput)
	}
	var absent *notificationQuotaAbsent
	if !errors.As(err, &absent) {
		return terms, err
	}
	if err := ctx.Err(); err != nil {
		return terms, err
	}
	packages, err := c.ListNotificationPackages(ctx, &ListNotificationPackagesInput{Kind: kind})
	if err != nil {
		var api *core.APIError
		if errors.As(err, &api) {
			safe := *api
			safe.Operation = op
			return terms, &safe
		}
		return terms, err
	}
	var selected *NotificationPackage
	seen := make(map[string]bool, len(packages.Items))
	for i := range packages.Items {
		p := &packages.Items[i]
		if seen[p.ID] {
			return terms, fmt.Errorf("%s: duplicate package IDs: %w", op, core.ErrInvalidInput)
		}
		seen[p.ID] = true
		if (packageID != "" && p.ID == packageID) || (packageID == "" && p.Price != nil && *p.Price == 0) {
			if selected != nil {
				return terms, fmt.Errorf("%s: ambiguous free package selection: %w", op, core.ErrInvalidInput)
			}
			selected = p
		}
	}
	if selected == nil {
		return terms, fmt.Errorf("%s: no matching package: %w", op, core.ErrInvalidInput)
	}
	if selected.Price == nil {
		return terms, quotaResponseError(op)
	}
	return notificationCreateTerms{kind: kind, packageID: selected.ID, monthPeriod: 1, listPrice: *selected.Price}, nil
}

// QuoteCreateNotificationQuota estimates an Email quota order for one month.
// MaxPrice and NoWait do not affect the estimate or authorize an order.
func (c *Client) QuoteCreateNotificationQuota(ctx context.Context, in *CreateNotificationQuotaInput) (*QuoteCreateNotificationQuotaOutput, error) {
	const op = "monitor.QuoteCreateNotificationQuota"
	terms, err := c.prepareNotificationCreate(ctx, op, in)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	raw, _, err := c.quotaRequest(ctx, transport.Request{Operation: op, Method: http.MethodPost, URL: c.billingRoute("v2", []string{terms.kind, "prices", "created-price"}), Body: terms.quoteBody(), Idempotent: true, OK: []int{http.StatusOK}})
	if err != nil {
		return nil, err
	}
	price, err := decodeQuotaPrice(op, raw)
	if err != nil {
		return nil, err
	}
	if terms.listPrice > 0 && price == 0 {
		return nil, fmt.Errorf("%s: %w", op, ErrUnpriced)
	}
	return &QuotaQuote{PackageID: terms.packageID, Quantity: 1, MonthPeriod: terms.monthPeriod, OptimumPrice: price, ListPrice: terms.listPrice, GuardPrice: math.Max(price, terms.listPrice), Currency: "VND"}, nil
}

func decodeQuotaPrice(op string, raw json.RawMessage) (float64, error) {
	o := quotaObjectOf(raw)
	var price float64
	o.decode("optimumPrice", &price, false)
	if o.invalid || price < 0 || math.IsNaN(price) || math.IsInf(price, 0) {
		return 0, quotaResponseError(op)
	}
	return price, nil
}

func checkNotificationPrice(op string, list, quote, maxPrice float64) error {
	if maxPrice < 0 || math.IsNaN(maxPrice) || math.IsInf(maxPrice, 0) {
		return fmt.Errorf("%s: MaxPrice must be finite and nonnegative: %w", op, core.ErrInvalidInput)
	}
	if list < 0 || quote < 0 || math.IsNaN(list) || math.IsNaN(quote) || math.IsInf(list, 0) || math.IsInf(quote, 0) {
		return quotaResponseError(op)
	}
	if list > 0 && quote == 0 {
		return fmt.Errorf("%s: %w", op, ErrUnpriced)
	}
	if math.Max(list, quote) > maxPrice {
		return fmt.Errorf("%s: %w", op, ErrPriceAboveMax)
	}
	return nil
}
