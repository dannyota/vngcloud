package monitor

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/transport"
)

type SetNotificationQuotaAutoRenewInput struct {
	Kind            string `vngcloud:"required"`
	Enabled         *bool  `vngcloud:"required"`
	MaxPrice        float64
	ExpectedQuotaID string
}
type SetSyntheticQuotaAutoRenewInput struct {
	Enabled         *bool `vngcloud:"required"`
	MaxPrice        float64
	ExpectedQuotaID string
}

// SetQuotaAutoRenewOutput identifies the intended frozen quota, not proof of
// the affected resource. Changed means a read confirmed the target setting.
type SetQuotaAutoRenewOutput struct {
	Quota   *Quota
	Stage   string
	Changed bool
}
type SetNotificationQuotaAutoRenewOutput = SetQuotaAutoRenewOutput
type SetSyntheticQuotaAutoRenewOutput = SetQuotaAutoRenewOutput

type DeleteNotificationQuotaInput struct {
	Kind            string `vngcloud:"required"`
	Purge           bool
	ExpectedQuotaID string
}
type DeleteSyntheticQuotaInput struct {
	Purge           bool
	ExpectedQuotaID string
}

// DeleteQuotaOutput identifies the intended frozen quota. Settlement is a
// not-found read; the kind-wide delete response provides no target identity.
type DeleteQuotaOutput struct {
	Quota  *Quota
	Stage  string
	Purged bool
}
type DeleteNotificationQuotaOutput = DeleteQuotaOutput
type DeleteSyntheticQuotaOutput = DeleteQuotaOutput

type quotaLifecycleInput struct {
	op, kind, expectedID   string
	enabled, delete, purge bool
	maxPrice               float64
}
type quotaLifecycleTerms struct {
	quota    Quota
	quantity int
	price    *float64
}
type quotaAutoRenewBody struct {
	ResourceID string `json:"resourceId"`
	Enabled    bool   `json:"enabled"`
	Period     int    `json:"period"`
}
type syntheticRenewQuoteBody struct {
	RedirectURL string `json:"redirectUrl"`
	PackageID   string `json:"packageId"`
	Quantity    int    `json:"quantity"`
	ResourceID  string `json:"resourceId"`
}

func lifecycleUnsupported(op, row string) error {
	return fmt.Errorf("%s: %s is unsupported: %w", op, row, core.ErrInvalidInput)
}
func (c *Client) validateQuotaLifecycle(in quotaLifecycleInput) error {
	readOnly, _ := strconv.ParseBool(c.c.ProfileSetting("read_only"))
	if readOnly {
		return &core.APIError{Operation: in.op, Code: "ReadOnly", Message: "read-only profile refuses quota writes", Err: core.ErrInvalidInput}
	}
	if in.expectedID != "" {
		if err := core.CheckPathID(in.op, "ExpectedQuotaID", in.expectedID); err != nil {
			return err
		}
	}
	if !in.delete {
		if err := checkNotificationPrice(in.op, 0, 0, in.maxPrice); err != nil {
			return err
		}
	}
	if in.purge {
		return lifecycleUnsupported(in.op, in.kind+" quota purge")
	}
	if in.kind == "synthetic" {
		if in.delete {
			return lifecycleUnsupported(in.op, "synthetic quota delete")
		}
		if !in.enabled {
			return lifecycleUnsupported(in.op, "synthetic auto-renew disable")
		}
	} else if !in.delete && in.enabled {
		return lifecycleUnsupported(in.op, in.kind+" auto-renew enable")
	}
	return nil
}

// SetNotificationQuotaAutoRenew disables Email or SMS auto-renew. Enable is
// deferred until its renewal quote has evidence. Every mutation sends once.
func (c *Client) SetNotificationQuotaAutoRenew(ctx context.Context, in *SetNotificationQuotaAutoRenewInput) (*SetNotificationQuotaAutoRenewOutput, error) {
	const op = "monitor.SetNotificationQuotaAutoRenew"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	input := *in
	kind, err := notificationKind(op, input.Kind)
	if err != nil {
		return nil, err
	}
	result, err := c.runQuotaLifecycle(ctx, quotaLifecycleInput{op: op, kind: kind, enabled: *input.Enabled, maxPrice: input.MaxPrice, expectedID: input.ExpectedQuotaID})
	return autoRenewQuotaOutput(result), err
}

// SetSyntheticQuotaAutoRenew quotes and guards enable before sending once.
// A free quota's server refusal remains a status-classified APIError. Disable
// is unsupported. An ambiguous outcome blocks further writes until resolved.
func (c *Client) SetSyntheticQuotaAutoRenew(ctx context.Context, in *SetSyntheticQuotaAutoRenewInput) (*SetSyntheticQuotaAutoRenewOutput, error) {
	const op = "monitor.SetSyntheticQuotaAutoRenew"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	input := *in
	result, err := c.runQuotaLifecycle(ctx, quotaLifecycleInput{op: op, kind: "synthetic", enabled: *input.Enabled, maxPrice: input.MaxPrice, expectedID: input.ExpectedQuotaID})
	return autoRenewQuotaOutput(result), err
}

// DeleteNotificationQuota deletes an ACTIVE Email or SMS quota with auto-renew
// and POC off. Purge is unsupported. ExpectedQuotaID narrows the target race;
// the provider's kind-wide request has no conditional identity guard.
func (c *Client) DeleteNotificationQuota(ctx context.Context, in *DeleteNotificationQuotaInput) (*DeleteNotificationQuotaOutput, error) {
	const op = "monitor.DeleteNotificationQuota"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	input := *in
	kind, err := notificationKind(op, input.Kind)
	if err != nil {
		return nil, err
	}
	result, err := c.runQuotaLifecycle(ctx, quotaLifecycleInput{op: op, kind: kind, delete: true, purge: input.Purge, expectedID: input.ExpectedQuotaID})
	return deleteQuotaOutput(result), err
}

// DeleteSyntheticQuota refuses before any request until deletion is evidenced.
func (c *Client) DeleteSyntheticQuota(ctx context.Context, in *DeleteSyntheticQuotaInput) (*DeleteSyntheticQuotaOutput, error) {
	const op = "monitor.DeleteSyntheticQuota"
	input := DeleteSyntheticQuotaInput{}
	if in != nil {
		input = *in
	}
	return nil, c.validateQuotaLifecycle(quotaLifecycleInput{op: op, kind: "synthetic", delete: true, purge: input.Purge, expectedID: input.ExpectedQuotaID})
}
func autoRenewQuotaOutput(result *quotaWriteResult) *SetQuotaAutoRenewOutput {
	if result == nil {
		return nil
	}
	return &SetQuotaAutoRenewOutput{Quota: copyLifecycleQuota(result.quota), Stage: result.stage, Changed: result.changed}
}
func deleteQuotaOutput(result *quotaWriteResult) *DeleteQuotaOutput {
	if result == nil {
		return nil
	}
	return &DeleteQuotaOutput{Quota: copyLifecycleQuota(result.quota), Stage: result.stage, Purged: result.purged}
}
func copyLifecycleQuota(q *Quota) *Quota {
	if q == nil {
		return nil
	}
	copy := *q
	return &copy
}

func (c *Client) readLifecycleTerms(ctx context.Context, in quotaLifecycleInput, credentials *monitorReadCredentials) (quotaLifecycleTerms, error) {
	if in.kind == "synthetic" {
		out, err := c.readSyntheticQuota(ctx, in.op, credentials)
		if err != nil {
			return quotaLifecycleTerms{}, err
		}
		return quotaLifecycleTerms{quota: out.Quota, quantity: out.Detail.API, price: out.Detail.Price}, nil
	}
	q, err := c.readNotificationQuota(ctx, in.op, in.kind, credentials)
	return quotaLifecycleTerms{quota: q}, err
}
func lifecycleExpectedID(op, expected, actual string) error {
	if expected != "" && expected != actual {
		return fmt.Errorf("%s: ExpectedQuotaID differs from intended quota: %w", op, core.ErrInvalidInput)
	}
	return nil
}
func lifecycleDeleteEligible(op string, q Quota) error {
	switch q.Status {
	case QuotaStatusActive:
	case "CREATING", QuotaStatusInTrash, "ERROR":
		return fmt.Errorf("%s: delete requires ACTIVE: %w", op, core.ErrInvalidInput)
	default:
		return fmt.Errorf("%s: unknown quota status: %w", op, ErrUnexpectedStatus)
	}
	if q.AutoRenew || q.POC {
		return fmt.Errorf("%s: delete requires auto-renew and POC off: %w", op, core.ErrInvalidInput)
	}
	return nil
}
func lifecycleSameTerms(a, b quotaLifecycleTerms) bool {
	x, y := a.quota, b.quota
	return x.ID == y.ID && x.PackageID == y.PackageID && x.Status == y.Status && x.EndTime.Equal(y.EndTime) && x.AutoRenew == y.AutoRenew && x.AutoRenewPeriod == y.AutoRenewPeriod && x.POC == y.POC && a.quantity == b.quantity
}
func lifecycleAttemptKey(in quotaLifecycleInput, t quotaLifecycleTerms) quotaAttemptKey {
	q := t.quota
	return quotaAttemptKey{operation: in.op, kind: in.kind, packageID: q.PackageID, monthPeriod: 1, quotaID: q.ID, expectedEndTime: q.EndTime, status: q.Status, autoRenew: q.AutoRenew, autoRenewPeriod: q.AutoRenewPeriod, poc: q.POC, quantity: t.quantity, enabled: in.enabled, purge: in.purge}
}
func (c *Client) quoteSyntheticLifecycle(ctx context.Context, in quotaLifecycleInput, t quotaLifecycleTerms, credentials *monitorReadCredentials) error {
	if t.price == nil || t.quantity <= 0 {
		return quotaResponseError(in.op)
	}
	// Only the evidenced free synthetic package can be priced for a write.
	if *t.price != 0 {
		return lifecycleUnsupported(in.op, "paid synthetic auto-renew enable")
	}
	raw, _, err := c.quotaRequest(ctx, transport.Request{Operation: in.op, Method: http.MethodPost, URL: c.billingRoute("v2", []string{"synthetic", "prices", "created-price"}), Body: syntheticRenewQuoteBody{PackageID: t.quota.PackageID, Quantity: t.quantity, ResourceID: t.quota.ID}, Idempotent: true, OK: []int{200}}, credentials)
	if err != nil {
		return err
	}
	price, err := decodeQuotaPrice(in.op, raw)
	if err != nil {
		return err
	}
	return checkNotificationPrice(in.op, *t.price, price, in.maxPrice)
}
func (c *Client) lifecycleReconciler(in quotaLifecycleInput, t quotaLifecycleTerms, credentials *monitorReadCredentials) func(context.Context) (quotaAttemptResolution, error) {
	return func(ctx context.Context) (quotaAttemptResolution, error) {
		fresh, err := c.readLifecycleTerms(ctx, in, credentials)
		if in.delete && core.IsNotFound(err) {
			return quotaAttemptResolution{confirmed: true, quota: copyLifecycleQuota(&t.quota)}, nil
		}
		if err != nil {
			return quotaAttemptResolution{}, err
		}
		q := fresh.quota
		confirmed := !in.delete && q.ID == t.quota.ID && q.PackageID == t.quota.PackageID && q.AutoRenew == in.enabled && q.AutoRenewPeriod == map[bool]int{false: 0, true: 1}[in.enabled]
		return quotaAttemptResolution{confirmed: confirmed, quota: copyLifecycleQuota(&t.quota)}, nil
	}
}

func (c *Client) runQuotaLifecycle(ctx context.Context, in quotaLifecycleInput) (out *quotaWriteResult, err error) {
	if err := c.validateQuotaLifecycle(in); err != nil {
		return nil, err
	}
	if err := c.quotaMu.acquire(ctx); err != nil {
		return nil, err
	}
	defer c.quotaMu.release()
	credentials := new(monitorReadCredentials)
	if attempt := c.quotaAttempts[in.kind]; attempt != nil && attempt.credentials != nil {
		credentials = attempt.credentials
	}
	defer func() {
		var api *core.APIError
		if errors.As(err, &api) && api.Message == "quota credential safety limit exceeded" {
			if out != nil {
				out.quota = nil
			}
			if attempt := c.quotaAttempts[in.kind]; attempt != nil {
				attempt.result.quota = nil
			}
		}
		if out != nil {
			c.scrubQuotaWriteResult(out, *credentials)
		}
		if attempt := c.quotaAttempts[in.kind]; attempt != nil {
			c.scrubQuotaWriteResult(&attempt.result, *credentials)
		}
	}()
	if attempt := c.quotaAttempts[in.kind]; attempt != nil {
		if err := lifecycleExpectedID(in.op, in.expectedID, attempt.key.quotaID); err != nil {
			return nil, err
		}
		key := attempt.key
		key.operation = in.op
		key.enabled = in.enabled
		key.purge = in.purge
		result, e, _ := c.reconcileQuotaAttempt(ctx, key)
		if attempt.key.operation != in.op {
			return nil, e
		}
		return result, e
	}
	terms, err := c.readLifecycleTerms(ctx, in, credentials)
	if err != nil {
		return nil, err
	}
	if err := lifecycleExpectedID(in.op, in.expectedID, terms.quota.ID); err != nil {
		return nil, err
	}
	if in.delete {
		if err := lifecycleDeleteEligible(in.op, terms.quota); err != nil {
			return nil, err
		}
	} else {
		if terms.quota.AutoRenew == in.enabled {
			if in.enabled && terms.quota.AutoRenewPeriod != 1 {
				return nil, fmt.Errorf("%s: enabled period is not one month: %w", in.op, core.ErrInvalidInput)
			}
			return &quotaWriteResult{quota: copyLifecycleQuota(&terms.quota), stage: "prepared"}, nil
		}
		if in.enabled {
			if err := c.quoteSyntheticLifecycle(ctx, in, terms, credentials); err != nil {
				return nil, err
			}
		}
	}
	fresh, err := c.readLifecycleTerms(ctx, in, credentials)
	if err != nil {
		return nil, err
	}
	if in.delete {
		if err := lifecycleDeleteEligible(in.op, fresh.quota); err != nil {
			return nil, err
		}
	}
	if !lifecycleSameTerms(terms, fresh) {
		return nil, fmt.Errorf("%s: quota terms changed before send: %w", in.op, core.ErrInvalidInput)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	req := transport.Request{Operation: in.op, Method: http.MethodPost, URL: c.billingRoute("v1", []string{in.kind, "quota", terms.quota.ID, "update-auto-renew"}), Body: quotaAutoRenewBody{ResourceID: terms.quota.ID, Enabled: in.enabled, Period: 1}, OK: []int{200}}
	bound := 30 * time.Second
	if in.delete {
		req.Method = http.MethodDelete
		req.URL = c.billingRoute("v1", []string{in.kind, "quota"})
		req.Body = nil
		req.OK = []int{204}
		bound = 60 * time.Second
	}
	attempt := &quotaAttempt{key: lifecycleAttemptKey(in, terms), credentials: credentials, bound: bound, result: quotaWriteResult{quota: copyLifecycleQuota(&terms.quota), stage: "prepared"}, reconcile: c.lifecycleReconciler(in, terms, credentials)}
	raw, sendErr, definite := c.sendQuotaMutation(ctx, req, credentials)
	if definite {
		return nil, sendErr
	}
	attempt.result.stage = "sent"
	c.recordQuotaAttempt(attempt)
	if sendErr != nil {
		return &attempt.result, quotaUnsettled(in.op, sendErr)
	}
	if len(raw) != 0 {
		return &attempt.result, quotaUnsettled(in.op, quotaResponseError(in.op))
	}
	_, err = c.waitQuotaAttempt(ctx, in.op, attempt.reconcile, bound)
	if err != nil {
		return &attempt.result, err
	}
	delete(c.quotaAttempts, in.kind)
	attempt.result.stage = "settled"
	attempt.result.changed = !in.delete
	return &attempt.result, nil
}
func (c *Client) readSyntheticQuota(ctx context.Context, op string, credentials *monitorReadCredentials) (*GetSyntheticQuotaOutput, error) {
	raw, err := c.quotaReadCredentials(ctx, op, "v1", credentials, "synthetic", "quota")
	if err != nil {
		return nil, err
	}
	q, err := decodeQuota(op, raw)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	raw, err = c.quotaReadCredentials(ctx, op, "v2", credentials, "synthetic", q.ID, "quota-detail")
	if err != nil {
		return nil, err
	}
	o := quotaObjectOf(raw)
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
	for _, field := range []*string{&q.ID, &q.Name, &q.Type, &q.Status, &q.PackageID} {
		*field, _ = c.c.RedactError(*field, "", (*credentials)...)
	}
	return &GetSyntheticQuotaOutput{Quota: q, Detail: d}, nil
}
