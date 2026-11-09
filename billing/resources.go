package billing

import (
	"context"
	"encoding/json"

	"danny.vn/vngcloud/internal/billingresources"
	"danny.vn/vngcloud/internal/core"
)

// Renewal types are returned as strings in prepaid resource responses.
const (
	// RenewTypeNonRenewable is the "NON-RENEWABLE" renewal type.
	RenewTypeNonRenewable = "NON-RENEWABLE"
	// RenewTypeManual is the "MANUAL" renewal type.
	RenewTypeManual = "MANUAL"
	// RenewTypeAutoRenew is the "AUTO-RENEW" renewal type.
	RenewTypeAutoRenew = "AUTO-RENEW"
)

// Resource describes renewal state. Cost has no established meaning or currency.
// See docs/design/auto-renew.md.
type Resource struct {
	ArtifactID       string                   `json:"artifactId"`
	ArtifactName     string                   `json:"artifactName"`
	ArtifactType     string                   `json:"artifactType"`
	Product          string                   `json:"product"`
	RenewType        string                   `json:"renewType"`
	BillingType      string                   `json:"billingType"`
	StartBillingTime *int64                   `json:"startBillingTime"`
	EndBillingTime   *int64                   `json:"endBillingTime"`
	DateLeft         *int64                   `json:"dateLeft"`
	RenewPeriod      *int64                   `json:"renewPeriod"`
	Channel          *int64                   `json:"channel"`
	IsRenewing       *bool                    `json:"isRenewing"`
	Cost             *float64                 `json:"cost"`
	Status           json.RawMessage          `json:"status"`
	StatusUI         json.RawMessage          `json:"statusUI"`
	BillingElements  []ResourceBillingElement `json:"billingElements"`
}

// ResourceBillingElement holds a billing element from a resource response.
// See docs/design/auto-renew.md.
type ResourceBillingElement struct {
	SKU      string          `json:"sku"`
	Quantity json.RawMessage `json:"quantity"`
	MetaKey  json.RawMessage `json:"metaKey"`
}

// ResourceThresholds holds threshold data from a resource response.
// See docs/design/auto-renew.md.
type ResourceThresholds struct {
	WarningThresholds json.RawMessage `json:"warningThresholds"`
	AlarmThresholds   json.RawMessage `json:"alarmThresholds"`
}

// ListResourcesInput has no filters.
type ListResourcesInput struct{}

// ListResourcesOutput holds prepaid resources and their aggregate counts.
type ListResourcesOutput struct {
	Items                 []Resource
	TotalAutoRenews       *int64
	TotalGoodResources    *int64
	TotalWarningResources *int64
	TotalAlertedResources *int64
	TotalExpiredResources *int64
	Extra                 ResourceThresholds
}

// ListResources reads prepaid resource state across products without pricing.
func (c *Client) ListResources(ctx context.Context, _ *ListResourcesInput) (*ListResourcesOutput, error) {
	raw, err := billingresources.List(ctx, c.c)
	if err != nil {
		return nil, err
	}
	var wire struct {
		Items                 []Resource         `json:"data"`
		TotalAutoRenews       *int64             `json:"totalAutoRenews"`
		TotalGoodResources    *int64             `json:"totalGoodResources"`
		TotalWarningResources *int64             `json:"totalWarningResources"`
		TotalAlertedResources *int64             `json:"totalAlertedResources"`
		TotalExpiredResources *int64             `json:"totalExpiredResources"`
		Extra                 ResourceThresholds `json:"extra"`
	}
	if json.Unmarshal(raw, &wire) != nil {
		return nil, &core.APIError{Operation: billingresources.ListOperation, StatusCode: 200, Code: "InvalidResponse", Message: "billing resource fields had invalid types"}
	}
	return &ListResourcesOutput{Items: wire.Items, TotalAutoRenews: wire.TotalAutoRenews, TotalGoodResources: wire.TotalGoodResources, TotalWarningResources: wire.TotalWarningResources, TotalAlertedResources: wire.TotalAlertedResources, TotalExpiredResources: wire.TotalExpiredResources, Extra: wire.Extra}, nil
}
