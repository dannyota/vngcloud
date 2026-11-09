package monitor

import (
	"time"

	"danny.vn/vngcloud/internal/core"
)

// GetQuotaUsageInput takes no fields.
type GetQuotaUsageInput struct{}

// GetQuotaUsageOutput holds the account quota usage.
type GetQuotaUsageOutput struct{ Usage QuotaUsage }

// QuotaUsage holds aggregate quota usage and unmodeled kind names.
type QuotaUsage struct {
	SyntheticUsages      []UsageCounts
	EmailUsages          []NotificationUsage
	FreeEmailQuota       int
	FreeSMSQuota         int
	LogSeverity          string
	MetricSeverity       string
	SyntheticSeverity    string
	EmailSeverity        string
	SMSSeverity          string
	NotificationSeverity string
	// UnmodeledKinds names nonempty log, metric, or SMS arrays whose item
	// shapes are unknown. These items do not contribute modeled totals.
	UnmodeledKinds []string
}

// UsageCounts holds usage counts data from the API.
// See docs/design/monitor-quotas-api.md.
type UsageCounts struct {
	API      int `json:"api"`
	Location int `json:"location"`
	Email    int `json:"email"`
	SMS      int `json:"sms"`
	Slack    int `json:"slack"`
	Webhook  int `json:"webhook"`
	Telegram int `json:"telegram"`
	Teams    int `json:"teams"`
}

// NotificationUsage holds notification usage data from the API.
// See docs/design/monitor-quotas-api.md.
type NotificationUsage struct {
	Amount int `json:"amount"`
}

// Quota holds quota data from the API.
// See docs/design/monitor-quotas-api.md.
type Quota struct {
	ID              string    `json:"id"`
	Name            string    `json:"name"`
	Type            string    `json:"type"`
	Status          string    `json:"status"`
	PackageID       string    `json:"packageId"`
	StartTime       time.Time `json:"startTime"`
	EndTime         time.Time `json:"endTime"`
	CreatedAt       time.Time `json:"createdAt"`
	UpdatedAt       time.Time `json:"updatedAt"`
	AutoRenew       bool      `json:"autoRenew"`
	POC             bool      `json:"poc"`
	System          bool      `json:"system"`
	AutoRenewPeriod int       `json:"autoRenewPeriod"`
}

// SyntheticNotificationLimits holds optional synthetic notification limits.
type SyntheticNotificationLimits struct {
	Email, SMS, Slack, Webhook, Telegram, Teams *int
}

// SyntheticQuotaDetail holds the synthetic quota detail and optional price.
type SyntheticQuotaDetail struct {
	ChannelLimits SyntheticNotificationLimits
	API           int
	Location      int
	Email         int
	SMS           int
	Slack         int
	Webhook       int
	Telegram      int
	Teams         int
	ClassName     string
	TierName      string
	// Price is in VND. An absent or null price is unknown, not free.
	Price *float64
}

// QuotaUsageDetail holds quota usage detail data from the API.
// See docs/design/monitor-quotas-api.md.
type QuotaUsageDetail struct {
	// Counts is present only for synthetic usage; notification usage omits it.
	Counts       UsageCounts `json:",omitzero"`
	Amount       int
	Descriptions map[string]UsageDescription
}

// UsageDescription holds usage description data from the API.
// See docs/design/monitor-quotas-api.md.
type UsageDescription struct {
	Name  string `json:"name"`
	Specs string `json:"specs"`
	Unit  string `json:"unit"`
}

// NotificationPackage holds notification package data from the API.
// See docs/design/monitor-quotas-api.md.
type NotificationPackage struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Price is in VND. An absent or null price is unknown, not free.
	Price  *float64 `json:"price"`
	Amount int      `json:"amount"`
}

// ListNotificationPackagesInput requires Kind.
type ListNotificationPackagesInput struct {
	// Kind is required.
	Kind string `vngcloud:"required"`
}

// ListNotificationPackagesOutput holds NotificationPackage items.
type ListNotificationPackagesOutput = core.List[NotificationPackage]

// GetSyntheticQuotaInput takes no fields.
type GetSyntheticQuotaInput struct{}

// GetSyntheticQuotaOutput holds synthetic quota and detail.
type GetSyntheticQuotaOutput struct {
	Quota  Quota
	Detail SyntheticQuotaDetail
}

// GetSyntheticQuotaUsageInput takes no fields.
type GetSyntheticQuotaUsageInput struct{}

// GetSyntheticQuotaUsageOutput holds synthetic quota usage.
type GetSyntheticQuotaUsageOutput struct{ Usage QuotaUsageDetail }

// GetNotificationQuotaInput requires Kind.
type GetNotificationQuotaInput struct {
	// Kind is required.
	Kind string `vngcloud:"required"`
}

// GetNotificationQuotaOutput holds the selected notification quota.
type GetNotificationQuotaOutput struct{ Quota Quota }

// GetNotificationQuotaUsageInput requires Kind.
type GetNotificationQuotaUsageInput struct {
	// Kind is required.
	Kind string `vngcloud:"required"`
}

// GetNotificationQuotaUsageOutput holds the selected notification quota usage.
type GetNotificationQuotaUsageOutput struct{ Usage QuotaUsageDetail }
