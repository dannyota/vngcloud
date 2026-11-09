package monitor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"reflect"
	"slices"
	"time"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/transport"
)

// CreateSyntheticQuotaInput requires ClassID, PackageID and Quantity.
type CreateSyntheticQuotaInput struct {
	// ClassID is required; use ASCII letters, digits, or hyphens.
	ClassID string `vngcloud:"required"`
	// PackageID must identify a package in the selected class.
	PackageID string `vngcloud:"required"`
	// Quantity must be positive and fit the selected package option.
	Quantity int `vngcloud:"required"`
	// MaxPrice bounds the create quote; QuoteCreateSyntheticQuota ignores it.
	MaxPrice float64
	// NoWait skips the post-order wait. QuoteCreateSyntheticQuota ignores it.
	NoWait bool
}

// QuoteCreateSyntheticQuotaOutput holds the result of
// QuoteCreateSyntheticQuota.
type QuoteCreateSyntheticQuotaOutput = QuotaQuote

// CreateSyntheticQuotaOutput holds the result of CreateSyntheticQuota.
type CreateSyntheticQuotaOutput struct {
	QuotaMutationOutcome
	Quota          *Quota
	Created        bool
	OrderID        string
	PaymentPending bool
	SentAt         *time.Time
}

type syntheticCreateTerms struct {
	class     SyntheticQuotaClass
	pkg       SyntheticClassPackage
	option    SyntheticClassOption
	periods   []int
	maximum   int
	payment   string
	quantity  int
	listPrice float64
}

type syntheticCreateQuoteBody struct {
	RedirectURL string   `json:"redirectUrl"`
	PackageID   string   `json:"packageId"`
	Quantity    int      `json:"quantity"`
	BuyWith     struct{} `json:"buyWith"`
}
type syntheticCreateOrderBody struct {
	syntheticCreateQuoteBody
	MonthPeriod int  `json:"monthPeriod"`
	Pay         bool `json:"pay"`
}

func (t syntheticCreateTerms) quoteBody() syntheticCreateQuoteBody {
	return syntheticCreateQuoteBody{PackageID: t.pkg.ID, Quantity: t.quantity}
}
func (t syntheticCreateTerms) orderBody() syntheticCreateOrderBody {
	body := t.quoteBody()
	body.RedirectURL = syntheticQuotaRedirect
	return syntheticCreateOrderBody{syntheticCreateQuoteBody: body, MonthPeriod: 1, Pay: true}
}
func validateSyntheticCreate(op string, in *CreateSyntheticQuotaInput, write bool) error {
	if err := core.CheckRequired(op, in); err != nil {
		return err
	}
	if err := core.CheckPathID(op, "ClassID", in.ClassID); err != nil {
		return err
	}
	if in.Quantity <= 0 {
		return fmt.Errorf("%s: Quantity must be positive: %w", op, core.ErrInvalidInput)
	}
	if write {
		return checkNotificationPrice(op, 0, 0, in.MaxPrice)
	}
	return nil
}

// readSyntheticQuotaPresence recognizes absence only on the quota route and
// only from the captured body. A detail or proxy 404 cannot authorize a write.
func (c *Client) readSyntheticQuotaPresence(ctx context.Context, op string, credentials *monitorReadCredentials) (*Quota, bool, error) {
	raw, status, err := c.quotaRequest(ctx, transport.Request{Operation: op, Method: http.MethodGet, URL: c.billingRoute("v1", []string{"synthetic", "quota"}), OK: []int{200, 404}, NoRedirect: true}, credentials)
	if err != nil {
		return nil, false, err
	}
	if status == 404 {
		obj := quotaObjectOf(raw)
		message := obj.text("message", false)
		if !obj.invalid && len(obj.fields) == 1 && message == "Not found synthetic quota" {
			return nil, true, nil
		}
		return nil, false, monitorReadError(op, 404, "NotFound", "quota response withheld", nil)
	}
	q, err := decodeQuota(op, raw)
	if err == nil {
		err = syntheticQuotaTimes(op, q)
	}
	return &q, false, err
}
func (c *Client) requireSyntheticAbsent(ctx context.Context, op string, credentials *monitorReadCredentials) error {
	_, absent, err := c.readSyntheticQuotaPresence(ctx, op, credentials)
	if err != nil {
		return err
	}
	if !absent {
		return fmt.Errorf("%s: synthetic quota already exists: %w", op, core.ErrInvalidInput)
	}
	return nil
}
func (c *Client) prepareSyntheticCreate(ctx context.Context, op string, in CreateSyntheticQuotaInput, credentials *monitorReadCredentials) (syntheticCreateTerms, error) {
	var t syntheticCreateTerms
	if err := c.requireSyntheticAbsent(ctx, op, credentials); err != nil {
		return t, err
	}
	reader := quotaCompositeReader{client: c, credentials: credentials}
	raw, err := reader.read(ctx, op, "v2", "synthetic", "quota-class")
	if err != nil {
		return t, err
	}
	classes, err := decodeSyntheticClasses(op, raw)
	if err != nil {
		return t, err
	}
	if len(classes) > 64 {
		return t, quotaResponseError(op)
	}
	for _, class := range classes {
		if class.ID == in.ClassID {
			t.class = class
		}
	}
	if t.class.ID == "" || t.class.Status != QuotaStatusActive {
		return t, lifecycleUnsupported(op, "selected synthetic class")
	}
	raw, err = reader.read(ctx, op, "v2", "synthetic", "quota-class", t.class.ID, "packages")
	if err != nil {
		return t, err
	}
	packages, err := decodeSyntheticClassPackages(op, raw)
	if err != nil {
		return t, err
	}
	for _, pkg := range packages {
		if pkg.ID == in.PackageID {
			t.pkg = pkg
		}
	}
	if t.pkg.ID == "" {
		return t, lifecycleUnsupported(op, "selected synthetic package")
	}
	matches := 0
	for _, option := range t.class.Options {
		if option.PackageID == t.pkg.ID && option.Location == t.pkg.Location {
			t.option = option
			matches++
		}
	}
	if matches != 1 || in.Quantity < t.option.MinAPI || in.Quantity > t.option.MaxAPI || (in.Quantity-t.option.MinAPI)%t.option.Step != 0 {
		return t, lifecycleUnsupported(op, "synthetic quantity or package option")
	}
	if t.class.Name != "Basic" || t.pkg.API != 1 || t.pkg.Location != 1 || in.Quantity != 10 {
		return t, lifecycleUnsupported(op, "synthetic class or capacity")
	}
	if t.pkg.Price == nil {
		return t, quotaResponseError(op)
	}
	t.quantity = in.Quantity
	t.listPrice = *t.pkg.Price * float64(in.Quantity)
	if math.IsNaN(t.listPrice) || math.IsInf(t.listPrice, 0) {
		return t, quotaResponseError(op)
	}
	raw, err = reader.read(ctx, op, "v1", "settings")
	if err != nil {
		return t, err
	}
	o := quotaObjectOf(raw)
	periods, ok := quotaArray(o.fields["monthPeriods"])
	if !ok {
		return t, quotaResponseError(op)
	}
	for _, period := range periods {
		item := quotaObjectOf(json.RawMessage(`{"period":` + string(period) + `}`))
		value := item.integer("period", false)
		if item.invalid {
			return t, quotaResponseError(op)
		}
		t.periods = append(t.periods, value)
	}
	t.maximum = o.integer("maxMonthPeriodForFreeQuota", false)
	t.payment = o.text("paymentMethod", false)
	seen := map[int]bool{}
	for _, p := range t.periods {
		if p <= 0 || seen[p] {
			o.invalid = true
		}
		seen[p] = true
	}
	if o.invalid || len(t.periods) == 0 || t.maximum < 1 {
		return t, quotaResponseError(op)
	}
	if !slices.Contains(t.periods, 1) {
		return t, lifecycleUnsupported(op, "synthetic month period")
	}
	return t, nil
}
func (c *Client) quoteSyntheticCreate(ctx context.Context, op string, t syntheticCreateTerms, credentials *monitorReadCredentials) (*QuotaQuote, error) {
	raw, _, err := c.quotaRequest(ctx, transport.Request{Operation: op, Method: http.MethodPost, URL: c.billingRoute("v2", []string{"synthetic", "prices", "created-price"}), Body: t.quoteBody(), Idempotent: true, OK: []int{200}, NoRedirect: true}, credentials)
	if err != nil {
		return nil, err
	}
	price, err := decodeQuotaPrice(op, raw)
	if err != nil {
		return nil, err
	}
	if t.listPrice > 0 && price == 0 {
		return nil, fmt.Errorf("%s: %w", op, ErrUnpriced)
	}
	return &QuotaQuote{PackageID: t.pkg.ID, Quantity: t.quantity, MonthPeriod: 1, ListPrice: t.listPrice, OptimumPrice: price, GuardPrice: math.Max(t.listPrice, price), Currency: "VND"}, nil
}

// QuoteCreateSyntheticQuota estimates the selected free Basic capacity.
// MaxPrice and NoWait are ignored. The quote never sends an order.
func (c *Client) QuoteCreateSyntheticQuota(ctx context.Context, in *CreateSyntheticQuotaInput) (*QuoteCreateSyntheticQuotaOutput, error) {
	const op = "monitor.QuoteCreateSyntheticQuota"
	if err := validateSyntheticCreate(op, in, false); err != nil {
		return nil, err
	}
	input := *in
	var credentials monitorReadCredentials
	terms, err := c.prepareSyntheticCreate(ctx, op, input, &credentials)
	if err != nil {
		return nil, err
	}
	out, err := c.quoteSyntheticCreate(ctx, op, terms, &credentials)
	if err != nil {
		return nil, err
	}
	if out.GuardPrice != 0 {
		return nil, lifecycleUnsupported(op, "paid synthetic quota")
	}
	out.PackageID, _ = c.c.RedactError(out.PackageID, "", credentials...)
	return out, nil
}

func (c *Client) syntheticCreateReconciler(op string, t syntheticCreateTerms, sentAt time.Time, credentials *monitorReadCredentials) func(context.Context) (quotaAttemptResolution, error) {
	return func(ctx context.Context) (quotaAttemptResolution, error) {
		q, absent, err := c.readSyntheticQuotaPresence(ctx, op, credentials)
		if err != nil || absent {
			return quotaAttemptResolution{}, err
		}
		if q.Status != QuotaStatusActive || q.PackageID != t.pkg.ID || q.StartTime.Before(sentAt) || !q.EndTime.After(q.StartTime) {
			return quotaAttemptResolution{}, nil
		}
		out, err := c.readSyntheticQuota(ctx, op, credentials)
		if err != nil {
			return quotaAttemptResolution{}, err
		}
		if out.Quota.ID != q.ID || out.Quota.PackageID != t.pkg.ID || out.Quota.Status != QuotaStatusActive || out.Quota.StartTime.Before(sentAt) || !out.Quota.EndTime.After(out.Quota.StartTime) || out.Detail.ClassName != "Basic" || out.Detail.API != t.quantity || out.Detail.Location != t.pkg.Location {
			return quotaAttemptResolution{}, nil
		}
		return quotaAttemptResolution{confirmed: true, quota: &out.Quota}, nil
	}
}
func syntheticCreateRetained(a *quotaAttempt) *CreateSyntheticQuotaOutput {
	sent := a.result.sent
	if sent == "" {
		sent = "unknown"
	}
	return &CreateSyntheticQuotaOutput{QuotaMutationOutcome: QuotaMutationOutcome{Sent: sent, Stage: "unconfirmed"}, OrderID: a.result.orderID, PaymentPending: a.result.paymentPending, SentAt: copyQuotaTime(a.result.sentAt)}
}

// CreateSyntheticQuota orders the selected free Basic quota once. Only a
// matching read-back confirms creation. Concurrent matching creates cannot be
// distinguished without a provider idempotency key or order-to-quota identity.
func (c *Client) CreateSyntheticQuota(ctx context.Context, in *CreateSyntheticQuotaInput) (out *CreateSyntheticQuotaOutput, err error) {
	const op = "monitor.CreateSyntheticQuota"
	out = &CreateSyntheticQuotaOutput{QuotaMutationOutcome: QuotaMutationOutcome{Sent: "no", Stage: "preflight"}}
	status := 0
	defer func() {
		if err != nil && out.Sent == "no" {
			if retained, observed, ok := c.syntheticCreateEarlyEvidence(); ok {
				out = retained
				status = observed
				err = quotaUnsettled(op, err)
			}
		}
		if out.Sent == "no" && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) {
			err = syntheticNotSentError(err)
		}
		out.Error = quotaOutcomeError(op, err, status)
	}()
	if err = validateSyntheticCreate(op, in, true); err != nil {
		return out, err
	}
	input := *in
	if err = c.syntheticWriteProfile(op); err != nil {
		return out, err
	}
	if err = c.quotaMu.acquire(ctx); err != nil {
		return out, err
	}
	defer c.quotaMu.release()
	credentials := new(monitorReadCredentials)
	defer func() {
		var api *core.APIError
		if errors.As(err, &api) && api.Message == "quota credential safety limit exceeded" {
			out.Quota = nil
			out.OrderID = ""
			if a := c.quotaAttempts["synthetic"]; a != nil {
				a.result.quota = nil
				a.result.orderID = ""
			}
		}
		scrub := quotaWriteResult{quota: out.Quota, orderID: out.OrderID}
		c.scrubQuotaWriteResult(&scrub, *credentials)
		out.OrderID = scrub.orderID
		if a := c.quotaAttempts["synthetic"]; a != nil {
			c.scrubQuotaWriteResult(&a.result, *credentials)
		}
	}()
	if a := c.quotaAttempts["synthetic"]; a != nil {
		if a.credentials != nil {
			credentials = a.credentials
		}
		out = syntheticCreateRetained(a)
		status = a.result.status
		resolution, e := c.waitQuotaAttempt(ctx, op, a.reconcile, a.bound)
		if e != nil {
			return out, quotaUnsettled(op, e)
		}
		c.clearQuotaAttempt("synthetic")
		if a.key.operation != op || a.key.classID != input.ClassID || a.key.packageID != input.PackageID || a.key.quantity != input.Quantity {
			out = &CreateSyntheticQuotaOutput{QuotaMutationOutcome: QuotaMutationOutcome{Sent: "no", Stage: "preflight"}}
			status = 0
			return out, fmt.Errorf("%s: prior attempt changed intent: %w", op, core.ErrInvalidInput)
		}
		out.Quota = copyLifecycleQuota(resolution.quota)
		out.Created = true
		out.PaymentPending = false
		out.Stage = "confirmed"
		return out, nil
	}
	terms, err := c.prepareSyntheticCreate(ctx, op, input, credentials)
	if err != nil {
		return out, err
	}
	quote, err := c.quoteSyntheticCreate(ctx, op, terms, credentials)
	if err != nil {
		return out, err
	}
	if err = checkNotificationPrice(op, quote.ListPrice, quote.OptimumPrice, input.MaxPrice); err != nil {
		return out, err
	}
	if quote.GuardPrice != 0 {
		return out, lifecycleUnsupported(op, "paid synthetic quota")
	}
	fresh, err := c.prepareSyntheticCreate(ctx, op, input, credentials)
	if err != nil {
		if errors.Is(err, core.ErrInvalidInput) {
			return out, fmt.Errorf("%s: final create terms changed: %w", op, ErrQuotaChanged)
		}
		return out, err
	}
	if !reflect.DeepEqual(terms, fresh) {
		return out, fmt.Errorf("%s: final create terms changed: %w", op, ErrQuotaChanged)
	}
	if err = ctx.Err(); err != nil {
		return out, err
	}
	sentAt := c.now().UTC()
	if _, e := sentAt.MarshalJSON(); e != nil {
		return out, quotaResponseError(op)
	}
	out.SentAt = &sentAt
	raw, status, sendErr, notSent := c.quotaRequestOutcome(ctx, transport.Request{Operation: op, Method: http.MethodPost, URL: c.billingRoute("v2", []string{"synthetic", "quota"}), Body: terms.orderBody(), OK: []int{200}, Once: true, KeepObservedStatus: true}, credentials)
	if notSent {
		return out, syntheticNotSentError(sendErr)
	}
	out.Sent = "unknown"
	if status > 0 {
		out.Sent = "yes"
	}
	if sendErr != nil && status >= 400 && status < 500 {
		out.Stage = "refused"
		return out, sendErr
	}
	out.Stage = "unconfirmed"
	a := &quotaAttempt{key: quotaAttemptKey{operation: op, kind: "synthetic", classID: input.ClassID, packageID: input.PackageID, quantity: input.Quantity, monthPeriod: 1}, credentials: credentials, bound: quotaSettlementBound, reconcile: c.syntheticCreateReconciler(op, terms, sentAt, credentials), result: quotaWriteResult{sent: out.Sent, status: status, sentAt: copyQuotaTime(&sentAt)}}
	c.recordQuotaAttempt(a)
	if sendErr != nil {
		return out, quotaUnsettled(op, sendErr)
	}
	decoded, err := decodeSyntheticMutation(op, raw)
	if err != nil {
		return out, quotaUnsettled(op, err)
	}
	out.OrderID = decoded.orderID
	out.PaymentPending = decoded.paymentPending
	a.result.orderID = out.OrderID
	a.result.paymentPending = out.PaymentPending
	if out.PaymentPending {
		return out, quotaUnsettled(op, nil)
	}
	if input.NoWait {
		out.Stage = "accepted"
		return out, nil
	}
	resolution, err := c.waitQuotaAttempt(ctx, op, a.reconcile, quotaSettlementBound)
	if err != nil {
		return out, err
	}
	c.clearQuotaAttempt("synthetic")
	out.Quota = copyLifecycleQuota(resolution.quota)
	out.Created = true
	out.Stage = "confirmed"
	return out, nil
}

func copyQuotaTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copied := *value
	return &copied
}
