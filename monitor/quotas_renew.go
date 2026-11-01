package monitor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/transport"
)

type RenewSyntheticQuotaInput struct {
	ExpectedEndTime string `vngcloud:"required"`
	MaxPrice        float64
	MaxDaysLeft     int
	NoWait          bool
}

type RenewQuotaOutput struct {
	QuotaMutationOutcome
	PreviousEndTime *time.Time
	Quota           *Quota
	OrderID         string
	PaymentPending  bool
	Renewed         bool
}
type RenewSyntheticQuotaOutput = RenewQuotaOutput
type QuoteRenewSyntheticQuotaOutput = QuotaQuote

type syntheticMutationInput struct {
	op                      string
	recover                 bool
	expectedID, expectedEnd string
	maxPrice                float64
	maxDays                 int
	noWait                  bool
}
type syntheticMutationResult struct {
	outcome                   QuotaMutationOutcome
	previous                  *time.Time
	quota                     *Quota
	orderID                   string
	paymentPending, confirmed bool
}
type syntheticMutationTerms struct {
	quota  Quota
	detail SyntheticQuotaDetail
}
type syntheticMutationBody struct {
	RedirectURL string `json:"redirectUrl"`
	MonthPeriod int    `json:"monthPeriod"`
	Pay         bool   `json:"pay"`
}

const syntheticQuotaRedirect = "https://vmonitor.console.vngcloud.vn/quota-usages/synthetic-test"

func (t syntheticMutationTerms) key(in syntheticMutationInput) quotaAttemptKey {
	q := t.quota
	return quotaAttemptKey{operation: in.op, kind: "synthetic", packageID: q.PackageID, quotaID: q.ID, expectedEndTime: q.EndTime, monthPeriod: 1, status: q.Status, autoRenew: q.AutoRenew, autoRenewPeriod: q.AutoRenewPeriod, poc: q.POC, quantity: t.detail.API}
}
func (t syntheticMutationTerms) quoteBody() syntheticRenewQuoteBody {
	return syntheticRenewQuoteBody{PackageID: t.quota.PackageID, Quantity: t.detail.API, ResourceID: t.quota.ID}
}
func (syntheticMutationTerms) body() syntheticMutationBody {
	return syntheticMutationBody{RedirectURL: syntheticQuotaRedirect, MonthPeriod: 1, Pay: true}
}
func validateSyntheticMutation(in syntheticMutationInput, write bool) (time.Time, error) {
	if in.recover {
		if in.expectedID == "" {
			return time.Time{}, fmt.Errorf("%s: ExpectedQuotaID is required: %w", in.op, core.ErrInvalidInput)
		}
		if err := core.CheckPathID(in.op, "ExpectedQuotaID", in.expectedID); err != nil {
			return time.Time{}, err
		}
	} else {
		expiry, err := time.Parse(time.RFC3339, in.expectedEnd)
		if err != nil || expiry.IsZero() {
			return time.Time{}, fmt.Errorf("%s: ExpectedEndTime must be RFC 3339: %w", in.op, core.ErrInvalidInput)
		}
		if !write {
			return expiry, nil
		}
		if in.maxDays < 0 || uint64(in.maxDays) > uint64(math.MaxInt64/int64(24*time.Hour)) {
			return time.Time{}, fmt.Errorf("%s: MaxDaysLeft is out of range: %w", in.op, core.ErrInvalidInput)
		}
		if err := checkNotificationPrice(in.op, 0, 0, in.maxPrice); err != nil {
			return time.Time{}, err
		}
		return expiry, nil
	}
	if write {
		return time.Time{}, checkNotificationPrice(in.op, 0, 0, in.maxPrice)
	}
	return time.Time{}, nil
}
func (c *Client) syntheticWriteProfile(op string) error {
	switch strings.ToLower(strings.TrimSpace(c.c.ProfileSetting("read_only"))) {
	case "1", "true":
		return &core.APIError{Operation: op, Code: "ReadOnly", Message: "read-only profile refuses quota writes", Err: core.ErrInvalidInput}
	case "", "0", "false":
		return nil
	default:
		return fmt.Errorf("%s: read_only must be 1, true, 0, false, or empty: %w", op, core.ErrInvalidConfig)
	}
}
func (c *Client) syntheticTerms(ctx context.Context, in syntheticMutationInput, credentials *monitorReadCredentials) (syntheticMutationTerms, error) {
	out, err := c.readSyntheticQuota(ctx, in.op, credentials)
	if err != nil {
		return syntheticMutationTerms{}, err
	}
	if err := syntheticQuotaTimes(in.op, out.Quota); err != nil {
		return syntheticMutationTerms{}, err
	}
	return syntheticMutationTerms{quota: out.Quota, detail: out.Detail}, nil
}
func (c *Client) syntheticEligible(in syntheticMutationInput, t syntheticMutationTerms, expiry time.Time) error {
	q := t.quota
	if (in.recover && q.ID != in.expectedID) || (!in.recover && !q.EndTime.Equal(expiry)) {
		return fmt.Errorf("%s: quota guard differs: %w", in.op, ErrQuotaChanged)
	}
	status := QuotaStatusActive
	if in.recover {
		status = QuotaStatusInTrash
	}
	if q.Status != status {
		switch q.Status {
		case QuotaStatusActive, QuotaStatusInTrash, "CREATING", "ERROR":
			return fmt.Errorf("%s: quota is ineligible: %w", in.op, core.ErrInvalidInput)
		default:
			return fmt.Errorf("%s: unknown quota status: %w", in.op, ErrUnexpectedStatus)
		}
	}
	now := c.now()
	if q.ID == "" || q.PackageID == "" || q.StartTime.IsZero() || q.EndTime.IsZero() || q.POC || q.AutoRenew || (in.recover && !q.EndTime.Before(now)) || (!in.recover && !q.EndTime.After(now)) {
		return fmt.Errorf("%s: quota is ineligible: %w", in.op, core.ErrInvalidInput)
	}
	if t.detail.ClassName != "Basic" || t.detail.API != 10 || t.detail.Location != 1 {
		return lifecycleUnsupported(in.op, "synthetic package")
	}
	if t.detail.Price == nil {
		return quotaResponseError(in.op)
	}
	return nil
}
func (c *Client) quoteSyntheticMutation(ctx context.Context, op string, t syntheticMutationTerms, credentials *monitorReadCredentials) (*QuotaQuote, error) {
	raw, _, err := c.quotaRequest(ctx, transport.Request{Operation: op, Method: http.MethodPost, URL: c.billingRoute("v2", []string{"synthetic", "prices", "created-price"}), Body: t.quoteBody(), Idempotent: true, OK: []int{200}}, credentials)
	if err != nil {
		return nil, err
	}
	price, err := decodeQuotaPrice(op, raw)
	if err != nil {
		return nil, err
	}
	list := *t.detail.Price
	if list > 0 && price == 0 {
		return nil, fmt.Errorf("%s: %w", op, ErrUnpriced)
	}
	end := t.quota.EndTime
	packageID, _ := c.c.RedactError(t.quota.PackageID, "", (*credentials)...)
	resourceID, _ := c.c.RedactError(t.quota.ID, "", (*credentials)...)
	return &QuotaQuote{PackageID: packageID, Quantity: t.detail.API, MonthPeriod: 1, ResourceID: resourceID, ExpectedEndTime: &end, OptimumPrice: price, ListPrice: list, GuardPrice: math.Max(price, list), Currency: "VND"}, nil
}

// QuoteRenewSyntheticQuota estimates the same frozen one-month terms as renewal.
// Write-only budget, wait, and day controls do not affect this read.
func (c *Client) QuoteRenewSyntheticQuota(ctx context.Context, in *RenewSyntheticQuotaInput) (*QuoteRenewSyntheticQuotaOutput, error) {
	const op = "monitor.QuoteRenewSyntheticQuota"
	input := RenewSyntheticQuotaInput{}
	if in != nil {
		input = *in
	}
	args := syntheticMutationInput{op: op, expectedEnd: input.ExpectedEndTime}
	expiry, err := validateSyntheticMutation(args, false)
	if err != nil {
		return nil, err
	}
	var credentials monitorReadCredentials
	terms, err := c.syntheticTerms(ctx, args, &credentials)
	if err != nil {
		return nil, err
	}
	if err = c.syntheticEligible(args, terms, expiry); err != nil {
		return nil, err
	}
	return c.quoteSyntheticMutation(ctx, op, terms, &credentials)
}

// RenewSyntheticQuota extends an ACTIVE free quota. A not-due result sends
// nothing. NoWait skips settlement only; it leaves an open attempt record.
func (c *Client) RenewSyntheticQuota(ctx context.Context, in *RenewSyntheticQuotaInput) (*RenewSyntheticQuotaOutput, error) {
	const op = "monitor.RenewSyntheticQuota"
	input := RenewSyntheticQuotaInput{}
	if in != nil {
		input = *in
	}
	result, err := c.runSyntheticMutation(ctx, syntheticMutationInput{op: op, expectedEnd: input.ExpectedEndTime, maxPrice: input.MaxPrice, maxDays: input.MaxDaysLeft, noWait: input.NoWait})
	return &RenewQuotaOutput{QuotaMutationOutcome: result.outcome, PreviousEndTime: result.previous, Quota: result.quota, OrderID: result.orderID, PaymentPending: result.paymentPending, Renewed: result.confirmed}, err
}

func syntheticRetainedResult(a *quotaAttempt) syntheticMutationResult {
	end := a.key.expectedEndTime
	sent := a.result.sent
	if sent == "" {
		sent = "unknown"
	}
	return syntheticMutationResult{outcome: QuotaMutationOutcome{Sent: sent, Stage: "unconfirmed"}, previous: &end, orderID: a.result.orderID, paymentPending: a.result.paymentPending}
}
func syntheticGuardMismatch(in syntheticMutationInput, expiry time.Time, key quotaAttemptKey) bool {
	if in.recover {
		return in.expectedID != key.quotaID
	}
	return !expiry.Equal(key.expectedEndTime)
}
func (c *Client) reconcileSyntheticMutation(ctx context.Context, in syntheticMutationInput, expiry time.Time, a *quotaAttempt) (syntheticMutationResult, error) {
	result := syntheticRetainedResult(a)
	resolution, err := c.waitQuotaAttempt(ctx, in.op, a.reconcile, quotaSettlementBound)
	mismatch := syntheticGuardMismatch(in, expiry, a.key)
	if err != nil || !resolution.confirmed {
		if mismatch {
			err = errors.Join(err, ErrQuotaChanged)
		}
		return result, quotaUnsettled(in.op, err)
	}
	c.clearQuotaAttempt("synthetic")
	if mismatch {
		return syntheticMutationResult{outcome: QuotaMutationOutcome{Sent: "no", Stage: "preflight"}}, fmt.Errorf("%s: %w", in.op, ErrQuotaChanged)
	}
	if in.op != a.key.operation {
		return syntheticMutationResult{outcome: QuotaMutationOutcome{Sent: "no", Stage: "preflight"}}, fmt.Errorf("%s: prior attempt changed intent: %w", in.op, core.ErrInvalidInput)
	}
	result.quota = copyLifecycleQuota(resolution.quota)
	result.confirmed = true
	result.paymentPending = false
	result.outcome.Stage = "confirmed"
	return result, nil
}
func (c *Client) syntheticMutationReconciler(in syntheticMutationInput, t syntheticMutationTerms, credentials *monitorReadCredentials) func(context.Context) (quotaAttemptResolution, error) {
	return func(ctx context.Context) (quotaAttemptResolution, error) {
		raw, err := c.quotaReadCredentials(ctx, in.op, "v1", credentials, "synthetic", "quota")
		if core.IsNotFound(err) {
			return quotaAttemptResolution{}, nil
		}
		if err != nil {
			return quotaAttemptResolution{}, err
		}
		q, err := decodeQuota(in.op, raw)
		if err == nil {
			err = syntheticQuotaTimes(in.op, q)
		}
		if err != nil {
			return quotaAttemptResolution{}, err
		}
		if q.ID != t.quota.ID || q.PackageID != t.quota.PackageID || q.Status != QuotaStatusActive || !q.EndTime.After(t.quota.EndTime) {
			return quotaAttemptResolution{}, nil
		}
		return quotaAttemptResolution{confirmed: true, quota: &q}, nil
	}
}
func decodeSyntheticMutation(op string, raw []byte) (quotaWriteResult, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return quotaWriteResult{}, nil
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) != nil || obj == nil {
		return quotaWriteResult{}, quotaResponseError(op)
	}
	var result quotaWriteResult
	for key, target := range map[string]*string{"orderId": &result.orderID, "paymentUrl": new(string)} {
		value, ok := obj[key]
		if !ok || string(value) == "null" {
			continue
		}
		if json.Unmarshal(value, target) != nil {
			return quotaWriteResult{}, quotaResponseError(op)
		}
		if key == "paymentUrl" {
			result.paymentPending = *target != "" && *target != syntheticQuotaRedirect
		}
	}
	return result, nil
}

func (c *Client) runSyntheticMutation(ctx context.Context, in syntheticMutationInput) (result syntheticMutationResult, err error) {
	result.outcome = QuotaMutationOutcome{Sent: "no", Stage: "preflight"}
	status := 0
	defer func() {
		if err != nil && result.outcome.Sent == "no" {
			if retained, key, observed, ok := c.syntheticEarlyEvidence(); ok {
				result = retained
				status = observed
				if guarded, guardErr := validateSyntheticMutation(in, false); guardErr == nil && syntheticGuardMismatch(in, guarded, key) {
					err = errors.Join(err, ErrQuotaChanged)
				}
				err = quotaUnsettled(in.op, err)
			}
		}
		if result.outcome.Sent == "no" && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) {
			err = syntheticNotSentError(err)
		}
		result.outcome.Error = quotaOutcomeError(in.op, err, status)
	}()
	expiry, err := validateSyntheticMutation(in, true)
	if err != nil {
		return result, err
	}
	if err = c.syntheticWriteProfile(in.op); err != nil {
		return result, err
	}
	if err = c.quotaMu.acquire(ctx); err != nil {
		return result, err
	}
	defer c.quotaMu.release()
	credentials := new(monitorReadCredentials)
	defer func() {
		var api *core.APIError
		if errors.As(err, &api) && api.Message == "quota credential safety limit exceeded" {
			result.quota = nil
			result.orderID = ""
			if a := c.quotaAttempts["synthetic"]; a != nil {
				a.result.orderID = ""
				a.result.quota = nil
			}
		}
		scrub := quotaWriteResult{quota: result.quota, orderID: result.orderID}
		c.scrubQuotaWriteResult(&scrub, *credentials)
		result.orderID = scrub.orderID
		if a := c.quotaAttempts["synthetic"]; a != nil {
			c.scrubQuotaWriteResult(&a.result, *credentials)
		}
	}()
	if a := c.quotaAttempts["synthetic"]; a != nil {
		if a.credentials != nil {
			credentials = a.credentials
		}
		result, err = c.reconcileSyntheticMutation(ctx, in, expiry, a)
		status = a.result.status
		return result, err
	}
	terms, err := c.syntheticTerms(ctx, in, credentials)
	if err != nil {
		return result, err
	}
	previous := terms.quota.EndTime
	result.previous = &previous
	if err = c.syntheticEligible(in, terms, expiry); err != nil {
		return result, err
	}
	if !in.recover && in.maxDays > 0 && terms.quota.EndTime.After(c.now().Add(time.Duration(in.maxDays)*24*time.Hour)) {
		result.outcome.Stage = "not-due"
		return result, nil
	}
	quote, err := c.quoteSyntheticMutation(ctx, in.op, terms, credentials)
	if err != nil {
		return result, err
	}
	if err = checkNotificationPrice(in.op, quote.ListPrice, quote.OptimumPrice, in.maxPrice); err != nil {
		return result, err
	}
	if quote.GuardPrice != 0 {
		return result, lifecycleUnsupported(in.op, "paid synthetic quota")
	}
	fresh, err := c.syntheticTerms(ctx, in, credentials)
	if err != nil {
		return result, err
	}
	eligibilityErr := c.syntheticEligible(in, fresh, expiry)
	var invalidResponse *core.APIError
	if errors.As(eligibilityErr, &invalidResponse) {
		return result, eligibilityErr
	}
	if eligibilityErr != nil || !terms.key(in).matches(fresh.key(in)) || terms.detail.ClassName != fresh.detail.ClassName || terms.detail.Location != fresh.detail.Location || fresh.detail.Price == nil || *fresh.detail.Price != 0 {
		return result, fmt.Errorf("%s: final quota terms changed: %w", in.op, ErrQuotaChanged)
	}
	if err = ctx.Err(); err != nil {
		return result, err
	}
	path := []string{"synthetic", "quota", "extend-time"}
	if in.recover {
		path = []string{"trash", "synthetic", "quota"}
	}
	raw, status, sendErr, notSent := c.quotaRequestOutcome(ctx, transport.Request{Operation: in.op, Method: http.MethodPost, URL: c.billingRoute("v2", path), Body: terms.body(), OK: []int{200}, Once: true, KeepObservedStatus: true}, credentials)
	if notSent {
		return result, syntheticNotSentError(sendErr)
	}
	result.outcome.Sent = "unknown"
	if status > 0 {
		result.outcome.Sent = "yes"
	}
	if sendErr != nil && status >= 400 && status < 500 {
		result.outcome.Stage = "refused"
		return result, sendErr
	}
	result.outcome.Stage = "unconfirmed"
	a := &quotaAttempt{key: terms.key(in), credentials: credentials, bound: quotaSettlementBound, reconcile: c.syntheticMutationReconciler(in, terms, credentials), result: quotaWriteResult{sent: result.outcome.Sent, status: status}}
	c.recordQuotaAttempt(a)
	if sendErr != nil {
		return result, quotaUnsettled(in.op, sendErr)
	}
	decoded, err := decodeSyntheticMutation(in.op, raw)
	if err != nil {
		return result, quotaUnsettled(in.op, err)
	}
	a.result.orderID = decoded.orderID
	a.result.paymentPending = decoded.paymentPending
	result.orderID = decoded.orderID
	result.paymentPending = decoded.paymentPending
	if result.paymentPending {
		return result, quotaUnsettled(in.op, nil)
	}
	if in.noWait {
		result.outcome.Stage = "accepted"
		return result, nil
	}
	resolution, err := c.waitQuotaAttempt(ctx, in.op, a.reconcile, quotaSettlementBound)
	if err != nil {
		return result, err
	}
	c.clearQuotaAttempt("synthetic")
	result.quota = resolution.quota
	result.confirmed = true
	result.outcome.Stage = "confirmed"
	return result, nil
}

func syntheticNotSentError(err error) error {
	var api *core.APIError
	if errors.As(err, &api) && api.StatusCode == 0 {
		safe := *api
		safe.Code = "RequestFailed"
		return &safe
	}
	return err
}

func syntheticQuotaTimes(op string, q Quota) error {
	for _, value := range []time.Time{q.StartTime, q.EndTime, q.CreatedAt, q.UpdatedAt} {
		if _, err := value.MarshalJSON(); err != nil {
			return quotaResponseError(op)
		}
	}
	return nil
}

func (c *Client) syntheticEarlyEvidence() (syntheticMutationResult, quotaAttemptKey, int, bool) {
	c.quotaMu.attemptMu.Lock()
	defer c.quotaMu.attemptMu.Unlock()
	a := c.quotaMu.syntheticEvidence
	if a == nil {
		return syntheticMutationResult{}, quotaAttemptKey{}, 0, false
	}
	// Only immutable evidence is safe without the long-lived quota lock.
	sent := a.sent
	if sent == "" {
		sent = "unknown"
	}
	out := syntheticMutationResult{outcome: QuotaMutationOutcome{Sent: sent, Stage: "unconfirmed"}}
	end := a.key.expectedEndTime
	if _, err := end.MarshalJSON(); err == nil {
		out.previous = &end
	}
	return out, a.key, a.status, true
}
