package monitor

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/transport"
)

// SetNotificationQuotaAutoRenewInput requires Kind and Enabled.
type SetNotificationQuotaAutoRenewInput struct {
	// Kind must be Email or SMS, ignoring case.
	Kind string `vngcloud:"required"`
	// Enabled is required.
	Enabled *bool `vngcloud:"required"`
	// MaxPrice bounds the quoted price when enabling renewal.
	MaxPrice float64
	// ExpectedQuotaID checks the selected quota identity when nonempty.
	ExpectedQuotaID string
}

// SetSyntheticQuotaAutoRenewInput requires Enabled.
type SetSyntheticQuotaAutoRenewInput struct {
	// Enabled is required.
	Enabled *bool `vngcloud:"required"`
	// MaxPrice bounds the quoted price when enabling renewal.
	MaxPrice float64
	// ExpectedQuotaID checks the selected quota identity when nonempty.
	ExpectedQuotaID string
}

// SetQuotaAutoRenewOutput identifies the intended frozen quota, not proof of
// the affected resource. Changed means a read confirmed the target setting.
type SetQuotaAutoRenewOutput struct {
	Quota   *Quota
	Stage   string
	Changed bool
}

// SetNotificationQuotaAutoRenewOutput uses the SetQuotaAutoRenewOutput result
// shape.
type SetNotificationQuotaAutoRenewOutput = SetQuotaAutoRenewOutput

// SetSyntheticQuotaAutoRenewOutput uses the SetQuotaAutoRenewOutput result
// shape.
type SetSyntheticQuotaAutoRenewOutput = SetQuotaAutoRenewOutput

// DeleteNotificationQuotaInput requires Kind.
type DeleteNotificationQuotaInput struct {
	// Kind must be Email or SMS, ignoring case.
	Kind string `vngcloud:"required"`
	// Purge is refused because quota purge is not supported.
	Purge bool
	// ExpectedQuotaID checks the selected quota identity when nonempty.
	ExpectedQuotaID string
}

// DeleteSyntheticQuotaInput requires ExpectedQuotaID.
type DeleteSyntheticQuotaInput struct {
	// Purge is refused because quota purge is not supported.
	Purge bool
	// ExpectedQuotaID is required; use ASCII letters, digits, or hyphens.
	ExpectedQuotaID string `vngcloud:"required"`
}

// DeleteQuotaOutput identifies the intended frozen quota. Settlement is a
// not-found read; the kind-wide delete response provides no target identity.
type DeleteQuotaOutput struct {
	Quota  *Quota
	Stage  string
	Purged bool
}

// DeleteNotificationQuotaOutput uses the DeleteQuotaOutput result shape.
type DeleteNotificationQuotaOutput = DeleteQuotaOutput

// DeleteSyntheticQuotaOutput uses the DeleteQuotaOutput result shape.
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
	switch strings.ToLower(strings.TrimSpace(c.c.ProfileSetting("read_only"))) {
	case "1", "true":
		return &core.APIError{Operation: in.op, Code: "ReadOnly", Message: "read-only profile refuses quota writes", Err: core.ErrInvalidInput}
	case "", "0", "false":
	default:
		return fmt.Errorf("%s: %w: read_only must be 1, true, 0, false, or empty", in.op, core.ErrInvalidConfig)
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

// DeleteSyntheticQuota permanently deletes every synthetic check with its
// quota. ExpectedQuotaID guards the intended snapshot; the ID-less provider
// request cannot prevent replacement races. Purge remains unsupported.
func (c *Client) DeleteSyntheticQuota(ctx context.Context, in *DeleteSyntheticQuotaInput) (*DeleteSyntheticQuotaOutput, error) {
	const op = "monitor.DeleteSyntheticQuota"
	input := DeleteSyntheticQuotaInput{}
	if in != nil {
		input = *in
	}
	result, err := c.runSyntheticDelete(ctx, quotaLifecycleInput{op: op, kind: "synthetic", delete: true, purge: input.Purge, expectedID: input.ExpectedQuotaID})
	if result == nil {
		result = new(quotaWriteResult)
	}
	return deleteQuotaOutput(result), err
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
		if in.kind == "synthetic" && attempt.result.sent != "" {
			expected := in.expectedID
			if expected == "" {
				expected = attempt.key.quotaID
			}
			_, err := c.reconcileSyntheticMutation(ctx, syntheticMutationInput{op: in.op, recover: true, expectedID: expected}, time.Time{}, attempt)
			return nil, err
		}
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
	c.clearQuotaAttempt(in.kind)
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
	if err == nil && (op == "monitor.CreateSyntheticQuota" || op == "monitor.QuoteCreateSyntheticQuota" || op == "monitor.DeleteSyntheticQuota") {
		err = syntheticQuotaTimes(op, q)
	}
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
	limits := o.notificationLimits()
	d := SyntheticQuotaDetail{
		ChannelLimits: limits,
		API:           o.integer("api", false), Location: o.integer("location", false), Email: quotaLimitValue(limits.Email), SMS: quotaLimitValue(limits.SMS),
		Slack: quotaLimitValue(limits.Slack), Webhook: quotaLimitValue(limits.Webhook), Telegram: quotaLimitValue(limits.Telegram), Teams: quotaLimitValue(limits.Teams),
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

func (c *Client) runSyntheticDelete(ctx context.Context, in quotaLifecycleInput) (out *quotaWriteResult, err error) {
	out = new(quotaWriteResult)
	defer func() {
		if err != nil && out.stage != "sent" {
			if _, key, _, ok := c.syntheticEarlyEvidence(); ok {
				if in.expectedID != "" && core.CheckPathID(in.op, "ExpectedQuotaID", in.expectedID) == nil && in.expectedID != key.quotaID {
					err = errors.Join(err, core.ErrInvalidInput)
				}
				out = &quotaWriteResult{stage: "sent"}
				err = quotaUnsettled(in.op, err)
			}
		}
	}()
	if err = c.syntheticWriteProfile(in.op); err != nil {
		return out, err
	}
	if in.expectedID == "" {
		return out, fmt.Errorf("%s: ExpectedQuotaID is required: %w", in.op, core.ErrInvalidInput)
	}
	if err = core.CheckPathID(in.op, "ExpectedQuotaID", in.expectedID); err != nil {
		return out, err
	}

	if in.purge {
		return out, lifecycleUnsupported(in.op, "synthetic quota purge")
	}
	if err = c.quotaMu.acquire(ctx); err != nil {
		return out, err
	}
	defer c.quotaMu.release()
	credentials := new(monitorReadCredentials)
	defer func() {
		var api *core.APIError
		if errors.As(err, &api) && api.Message == "quota credential safety limit exceeded" {
			out.quota = nil
			if a := c.quotaAttempts["synthetic"]; a != nil {
				a.result.quota = nil
				a.result.orderID = ""
			}
		}
		c.scrubQuotaWriteResult(out, *credentials)
		if a := c.quotaAttempts["synthetic"]; a != nil {
			c.scrubQuotaWriteResult(&a.result, *credentials)
		}
	}()
	if a := c.quotaAttempts["synthetic"]; a != nil {
		if a.credentials != nil {
			credentials = a.credentials
		}
		out = &quotaWriteResult{quota: copyLifecycleQuota(a.result.quota), stage: "sent"}
		resolution, e := c.waitQuotaAttempt(ctx, in.op, a.reconcile, a.bound)
		mismatch := in.expectedID != a.key.quotaID
		if e != nil {
			if mismatch {
				e = errors.Join(e, core.ErrInvalidInput)
			}
			return out, quotaUnsettled(in.op, e)
		}
		c.clearQuotaAttempt("synthetic")
		if mismatch || a.key.operation != in.op || a.key.purge != in.purge {
			return new(quotaWriteResult), fmt.Errorf("%s: prior attempt changed intent: %w", in.op, core.ErrInvalidInput)
		}
		out.quota = copyLifecycleQuota(resolution.quota)
		out.stage = "settled"
		return out, nil
	}
	terms, err := c.readLifecycleTerms(ctx, in, credentials)
	if err != nil {
		return out, err
	}
	if err = lifecycleExpectedID(in.op, in.expectedID, terms.quota.ID); err != nil {
		return out, err
	}
	if err = lifecycleDeleteEligible(in.op, terms.quota); err != nil {
		return out, err
	}
	if !terms.quota.EndTime.After(c.now()) {
		return out, fmt.Errorf("%s: delete requires unexpired quota: %w", in.op, core.ErrInvalidInput)
	}
	out.quota = copyLifecycleQuota(&terms.quota)
	out.stage = "prepared"
	fresh, err := c.readLifecycleTerms(ctx, in, credentials)
	if err != nil {
		return out, err
	}
	if err = lifecycleDeleteEligible(in.op, fresh.quota); err != nil {
		return out, err
	}
	if !lifecycleSameTerms(terms, fresh) || !fresh.quota.EndTime.After(c.now()) {
		return out, fmt.Errorf("%s: quota terms changed before send: %w", in.op, core.ErrInvalidInput)
	}
	if err = ctx.Err(); err != nil {
		return out, err
	}
	a := &quotaAttempt{key: lifecycleAttemptKey(in, terms), credentials: credentials, bound: 60 * time.Second, result: *out}
	a.reconcile = func(ctx context.Context) (quotaAttemptResolution, error) {
		_, absent, e := c.readSyntheticQuotaPresence(ctx, in.op, credentials)
		return quotaAttemptResolution{confirmed: absent, quota: copyLifecycleQuota(&terms.quota)}, e
	}
	raw, status, sendErr, notSent := c.quotaRequestOutcome(ctx, transport.Request{Operation: in.op, Method: http.MethodDelete, URL: c.billingRoute("v1", []string{"synthetic", "quota"}), OK: []int{204}, Once: true, KeepObservedStatus: true}, credentials)
	if notSent || sendErr != nil && status >= 400 && status < 500 {
		return out, sendErr
	}
	out.stage = "sent"
	out.sent = "unknown"
	if status > 0 {
		out.sent = "yes"
	}
	out.status = status
	a.result = *out
	c.recordQuotaAttempt(a)
	if sendErr != nil {
		return out, quotaUnsettled(in.op, sendErr)
	}
	if len(raw) != 0 {
		return out, quotaUnsettled(in.op, quotaResponseError(in.op))
	}
	_, err = c.waitQuotaAttempt(ctx, in.op, a.reconcile, a.bound)
	if err != nil {
		return out, err
	}
	c.clearQuotaAttempt("synthetic")
	out.stage = "settled"
	return out, nil
}
