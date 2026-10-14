package monitor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"time"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/transport"
)

const (
	NotificationKindEmail = "Email"
	NotificationKindSMS   = "SMS"
	QuotaStatusActive     = "ACTIVE"
	QuotaStatusInTrash    = "IN-TRASH"
	quotaMaxBody          = 1 << 20
)

type GetQuotaUsageInput struct{}
type GetQuotaUsageOutput struct{ Usage QuotaUsage }

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

type NotificationUsage struct {
	Amount int `json:"amount"`
}

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

type SyntheticQuotaDetail struct {
	API       int
	Location  int
	Email     int
	SMS       int
	Slack     int
	Webhook   int
	Telegram  int
	Teams     int
	ClassName string
	TierName  string
	// Price is in VND. An absent or null price is unknown, not free.
	Price *float64
}

type QuotaUsageDetail struct {
	Counts       UsageCounts
	Amount       int
	Descriptions map[string]UsageDescription
}

type UsageDescription struct {
	Name  string `json:"name"`
	Specs string `json:"specs"`
	Unit  string `json:"unit"`
}

type NotificationPackage struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Price is in VND. An absent or null price is unknown, not free.
	Price  *float64 `json:"price"`
	Amount int      `json:"amount"`
}

type ListNotificationPackagesInput struct {
	Kind string `vngcloud:"required"`
}
type ListNotificationPackagesOutput = core.List[NotificationPackage]
type GetSyntheticQuotaInput struct{}
type GetSyntheticQuotaOutput struct {
	Quota  Quota
	Detail SyntheticQuotaDetail
}
type GetSyntheticQuotaUsageInput struct{}
type GetSyntheticQuotaUsageOutput struct{ Usage QuotaUsageDetail }

type syntheticPackage struct {
	ID       string
	Name     string
	Price    *float64
	TierID   int
	API      int
	Location int
}

func quotaResponseError(op string) error {
	return &core.APIError{Operation: op, StatusCode: http.StatusOK, Code: "InvalidResponse", Message: "quota response had invalid structure"}
}

// quotaRead suppresses routine captures and retains only fixed error metadata.
func (c *Client) quotaRead(ctx context.Context, op, version string, parts ...string) (json.RawMessage, error) {
	var raw json.RawMessage
	var credential string
	status, err := c.c.DoJSONStatus(ctx, transport.Request{
		Operation: op, Method: http.MethodGet, URL: c.billingRoute(version, parts), OK: []int{http.StatusOK},
		Sensitive: true, MaxBody: quotaMaxBody, WithholdMessage: "quota response withheld", SentCredential: &credential,
	}, &raw)
	if err != nil {
		if errors.Is(err, transport.ErrBodyTooLarge) {
			return nil, &core.APIError{Operation: op, StatusCode: status, Code: "ResponseTooLarge", Message: "quota response exceeded size limit"}
		}
		var api *core.APIError
		if errors.As(err, &api) {
			safe := *api
			if safe.StatusCode == 0 && status > 0 {
				safe.StatusCode = status
			}
			safe.Code = core.ResolvedCode(safe.StatusCode, "")
			if safe.Code == "" {
				safe.Code = "InvalidResponse"
			}
			safe.Message = "quota response withheld"
			return nil, &safe
		}
		return nil, err
	}
	// Scrub retained strings, including description keys, before projection.
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if decoder.Decode(&value) != nil {
		return nil, quotaResponseError(op)
	}
	value = c.scrubQuotaStrings(value, credential)
	raw, err = json.Marshal(value)
	if err != nil {
		return nil, quotaResponseError(op)
	}
	return raw, nil
}

func (c *Client) scrubQuotaStrings(value any, credential string) any {
	switch v := value.(type) {
	case string:
		safe, _ := c.c.RedactError(v, "", credential)
		return safe
	case []any:
		for i := range v {
			v[i] = c.scrubQuotaStrings(v[i], credential)
		}
	case map[string]any:
		safe := make(map[string]any, len(v))
		for k, item := range v {
			key, _ := c.c.RedactError(k, "", credential)
			safe[key] = c.scrubQuotaStrings(item, credential)
		}
		return safe
	}
	return value
}

type quotaObject struct {
	fields  map[string]json.RawMessage
	invalid bool
}

func quotaObjectOf(raw json.RawMessage) *quotaObject {
	o := &quotaObject{}
	o.invalid = json.Unmarshal(raw, &o.fields) != nil || o.fields == nil
	return o
}

func (o *quotaObject) decode(key string, out any, nullable bool) {
	raw, ok := o.fields[key]
	if !ok {
		o.invalid = true
		return
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		if !nullable {
			o.invalid = true
		}
		return
	}
	if json.Unmarshal(raw, out) != nil {
		o.invalid = true
	}
}

func (o *quotaObject) text(key string, nullable bool) string {
	var v string
	o.decode(key, &v, nullable)
	return v
}

// Integral decimals are accepted without converting through float64.
func (o *quotaObject) integer(key string, nullable bool) int {
	raw, ok := o.fields[key]
	if !ok {
		o.invalid = true
		return 0
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		if !nullable {
			o.invalid = true
		}
		return 0
	}
	var n json.Number
	if json.Unmarshal(raw, &n) != nil || len(raw) > 128 || bytes.TrimSpace(raw)[0] == '"' {
		o.invalid = true
		return 0
	}
	// Bound exponent expansion before big.Rat allocates its integer.
	if i := strings.IndexAny(n.String(), "eE"); i >= 0 {
		exponent, err := strconv.Atoi(n.String()[i+1:])
		if err != nil || exponent < -1024 || exponent > 1024 {
			o.invalid = true
			return 0
		}
	}
	r, ok := new(big.Rat).SetString(n.String())
	if !ok || !r.IsInt() || !r.Num().IsInt64() {
		o.invalid = true
		return 0
	}
	v := r.Num().Int64()
	if int64(int(v)) != v {
		o.invalid = true
		return 0
	}
	return int(v)
}

func (o *quotaObject) flag(key string) bool {
	v := o.integer(key, false)
	if v != 0 && v != 1 {
		o.invalid = true
	}
	return v == 1
}

func (o *quotaObject) timestamp(key string) time.Time {
	value := o.text(key, false)
	v, err := time.Parse(time.RFC3339, value)
	if err != nil {
		o.invalid = true
	}
	return v
}

func (o *quotaObject) price() *float64 {
	var v *float64
	// Only price permits both absence and null.
	if _, ok := o.fields["price"]; !ok {
		return nil
	}
	o.decode("price", &v, true)
	if v != nil && (*v < 0 || math.IsNaN(*v) || math.IsInf(*v, 0)) {
		o.invalid = true
	}
	return v
}

func (o *quotaObject) counts(nullable bool) UsageCounts {
	return UsageCounts{
		API: o.integer("api", false), Location: o.integer("location", false),
		Email: o.integer("email", false), SMS: o.integer("sms", false),
		Slack: o.integer("slack", nullable), Webhook: o.integer("webhook", nullable),
		Telegram: o.integer("telegram", nullable), Teams: o.integer("teams", nullable),
	}
}

func quotaArray(raw json.RawMessage) ([]json.RawMessage, bool) {
	var items []json.RawMessage
	if json.Unmarshal(raw, &items) != nil || items == nil {
		return nil, false
	}
	return items, true
}

func (c *Client) GetQuotaUsage(ctx context.Context, in *GetQuotaUsageInput) (*GetQuotaUsageOutput, error) {
	const op = "monitor.GetQuotaUsage"
	raw, err := c.quotaRead(ctx, op, "v1", "quotas", "usages")
	if err != nil {
		return nil, err
	}
	o := quotaObjectOf(raw)
	u := QuotaUsage{
		FreeEmailQuota: o.integer("freeEmailQuota", false), FreeSMSQuota: o.integer("freeSmsQuota", false),
		LogSeverity: o.text("logSeverity", true), MetricSeverity: o.text("metricSeverity", true),
		SyntheticSeverity: o.text("syntheticSeverity", true), EmailSeverity: o.text("emailSeverity", true),
		SMSSeverity: o.text("smsSeverity", true), NotificationSeverity: o.text("notificationSeverity", true),
	}
	for _, kind := range []string{"log", "metric", "sms", "synthetic", "email"} {
		items, ok := quotaArray(o.fields[kind+"Usages"])
		if !ok {
			return nil, quotaResponseError(op)
		}
		switch kind {
		case "synthetic":
			u.SyntheticUsages = make([]UsageCounts, 0, len(items))
			for _, item := range items {
				obj := quotaObjectOf(item)
				counts := obj.counts(false)
				if obj.invalid {
					return nil, quotaResponseError(op)
				}
				u.SyntheticUsages = append(u.SyntheticUsages, counts)
			}
		case "email":
			u.EmailUsages = make([]NotificationUsage, 0, len(items))
			for _, item := range items {
				obj := quotaObjectOf(item)
				amount := obj.integer("amount", false)
				if obj.invalid {
					return nil, quotaResponseError(op)
				}
				u.EmailUsages = append(u.EmailUsages, NotificationUsage{Amount: amount})
			}
		default:
			if len(items) > 0 {
				u.UnmodeledKinds = append(u.UnmodeledKinds, kind)
			}
		}
	}
	if o.invalid {
		return nil, quotaResponseError(op)
	}
	return &GetQuotaUsageOutput{Usage: u}, nil
}

func (c *Client) ListNotificationPackages(ctx context.Context, in *ListNotificationPackagesInput) (*ListNotificationPackagesOutput, error) {
	const op = "monitor.ListNotificationPackages"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	var kind string
	switch {
	case strings.EqualFold(in.Kind, NotificationKindEmail):
		kind = "email"
	case strings.EqualFold(in.Kind, NotificationKindSMS):
		kind = "sms"
	default:
		return nil, fmt.Errorf("%s: Kind must be Email or SMS: %w", op, core.ErrInvalidInput)
	}
	raw, err := c.quotaRead(ctx, op, "v1", kind, "packages")
	if err != nil {
		return nil, err
	}
	items, ok := quotaArray(raw)
	if !ok {
		return nil, quotaResponseError(op)
	}
	out := &ListNotificationPackagesOutput{Items: make([]NotificationPackage, 0, len(items))}
	for _, item := range items {
		o := quotaObjectOf(item)
		p := NotificationPackage{ID: o.text("id", false), Name: o.text("name", false), Price: o.price(), Amount: o.integer("amount", false)}
		if o.invalid || p.ID == "" {
			return nil, quotaResponseError(op)
		}
		out.Items = append(out.Items, p)
	}
	return out, nil
}

func (c *Client) GetSyntheticQuota(ctx context.Context, in *GetSyntheticQuotaInput) (*GetSyntheticQuotaOutput, error) {
	const op = "monitor.GetSyntheticQuota"
	raw, err := c.quotaRead(ctx, op, "v1", "synthetic", "quota")
	if err != nil {
		return nil, err
	}
	o := quotaObjectOf(raw)
	q := Quota{
		ID: o.text("id", false), Name: o.text("name", false), Type: o.text("type", false), Status: o.text("status", false), PackageID: o.text("packageId", false),
		StartTime: o.timestamp("startTime"), EndTime: o.timestamp("endTime"), CreatedAt: o.timestamp("createdAt"), UpdatedAt: o.timestamp("updatedAt"),
		AutoRenew: o.flag("autoRenew"), POC: o.flag("poc"), AutoRenewPeriod: o.integer("autoRenewPeriod", false),
	}
	o.decode("system", &q.System, false)
	if o.invalid || q.PackageID == "" || core.CheckPathID(op, "quota ID", q.ID) != nil {
		return nil, quotaResponseError(op)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	raw, err = c.quotaRead(ctx, op, "v2", "synthetic", q.ID, "quota-detail")
	if err != nil {
		return nil, err
	}
	o = quotaObjectOf(raw)
	counts := o.counts(true)
	d := SyntheticQuotaDetail{
		API: counts.API, Location: counts.Location, Email: counts.Email, SMS: counts.SMS,
		Slack: counts.Slack, Webhook: counts.Webhook, Telegram: counts.Telegram, Teams: counts.Teams,
		ClassName: o.text("className", false), TierName: o.text("tierName", true), Price: o.price(),
	}
	resourceID, packageID := o.text("resourceId", false), o.text("packageId", false)
	if o.invalid || resourceID != q.ID || packageID != q.PackageID {
		return nil, quotaResponseError(op)
	}
	return &GetSyntheticQuotaOutput{Quota: q, Detail: d}, nil
}

func (c *Client) GetSyntheticQuotaUsage(ctx context.Context, in *GetSyntheticQuotaUsageInput) (*GetSyntheticQuotaUsageOutput, error) {
	const op = "monitor.GetSyntheticQuotaUsage"
	raw, err := c.quotaRead(ctx, op, "v1", "synthetic", "quota", "usages")
	if err != nil {
		return nil, err
	}
	o := quotaObjectOf(raw)
	usage := quotaObjectOf(o.fields["usage"])
	u := QuotaUsageDetail{Counts: usage.counts(false)}
	description := quotaObjectOf(o.fields["description"])
	u.Descriptions = make(map[string]UsageDescription, len(description.fields))
	for key, item := range description.fields {
		d := quotaObjectOf(item)
		u.Descriptions[key] = UsageDescription{Name: d.text("name", false), Specs: d.text("specs", false), Unit: d.text("unit", false)}
		if d.invalid {
			return nil, quotaResponseError(op)
		}
	}
	if o.invalid || usage.invalid || description.invalid {
		return nil, quotaResponseError(op)
	}
	return &GetSyntheticQuotaUsageOutput{Usage: u}, nil
}

func (c *Client) listSyntheticPackages(ctx context.Context, op string) ([]syntheticPackage, error) {
	raw, err := c.quotaRead(ctx, op, "v1", "synthetic", "packages")
	if err != nil {
		return nil, err
	}
	items, ok := quotaArray(raw)
	if !ok {
		return nil, quotaResponseError(op)
	}
	out := make([]syntheticPackage, 0, len(items))
	for _, item := range items {
		o := quotaObjectOf(item)
		p := syntheticPackage{ID: o.text("id", false), Name: o.text("name", false), Price: o.price(), TierID: o.integer("tierId", false), API: o.integer("api", false), Location: o.integer("location", false)}
		if o.invalid || p.ID == "" {
			return nil, quotaResponseError(op)
		}
		out = append(out, p)
	}
	return out, nil
}
