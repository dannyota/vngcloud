package monitor

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"danny.vn/vngcloud/dns"
	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/transport"
)

// CreateNotificationQuotaOutput contains no checkout URL. Quota is present
// only after a read confirms settlement; NoWait leaves it nil.
type CreateNotificationQuotaOutput struct {
	Quota          *Quota
	OrderID        string
	PaymentPending bool
}

// The semaphore is initialized lazily so a Client's zero state stays usable.
type quotaWriteLock struct {
	once  sync.Once
	token chan struct{}
}

func (l *quotaWriteLock) acquire(ctx context.Context) error {
	l.once.Do(func() { l.token = make(chan struct{}, 1) })
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case l.token <- struct{}{}:
		if err := ctx.Err(); err != nil {
			l.release()
			return err
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (l *quotaWriteLock) release() { <-l.token }

// Keys describe the side effect, not the price estimate or caller wait policy.
// Additional baseline fields let extension and lifecycle writes share the ledger.
type quotaAttemptKey struct {
	operation       string
	kind            string
	packageID       string
	monthPeriod     int
	quotaID         string
	expectedEndTime time.Time
}

func (key quotaAttemptKey) matches(other quotaAttemptKey) bool {
	expiryMatches := key.expectedEndTime.Equal(other.expectedEndTime)
	key.expectedEndTime = time.Time{}
	other.expectedEndTime = time.Time{}
	return expiryMatches && key == other
}

type quotaWriteResult struct {
	quota          *Quota
	orderID        string
	paymentPending bool
}

type quotaAttemptResolution struct {
	confirmed bool
	rejected  error
	quota     *Quota
}

type quotaAttempt struct {
	key         quotaAttemptKey
	result      quotaWriteResult
	credentials *monitorReadCredentials
	// A read failure is inconclusive. Only an evidenced rejection may set rejected.
	reconcile func(context.Context) (quotaAttemptResolution, error)
}

func quotaUnsettled(op string, cause error) error {
	if cause == nil {
		return fmt.Errorf("%s: quota attempt remains unresolved; reconcile reads and inspect billing before another write: %w", op, dns.ErrNotSettled)
	}
	return fmt.Errorf("%s: quota attempt remains unresolved; reconcile reads and inspect billing before another write: %w: %w", op, dns.ErrNotSettled, cause)
}

// reconcileQuotaAttempt runs under quotaMu, before preparation of any new write.
// A changed request can resolve the ledger but cannot claim the prior result.
func (c *Client) reconcileQuotaAttempt(ctx context.Context, key quotaAttemptKey) (*quotaWriteResult, error, bool) {
	attempt := c.quotaAttempts[key.kind]
	if attempt == nil {
		return nil, nil, false
	}
	resolution, err := attempt.reconcile(ctx)
	if err != nil {
		return &attempt.result, quotaUnsettled(key.operation, err), true
	}
	if resolution.rejected != nil {
		delete(c.quotaAttempts, key.kind)
		return nil, resolution.rejected, true
	}
	if !resolution.confirmed {
		return &attempt.result, quotaUnsettled(key.operation, nil), true
	}
	delete(c.quotaAttempts, key.kind)
	if !key.matches(attempt.key) {
		return nil, fmt.Errorf("%s: prior %s attempt confirmed a changed quota state; reread before another write: %w", key.operation, attempt.key.operation, core.ErrInvalidInput), true
	}
	result := attempt.result
	result.quota = resolution.quota
	result.paymentPending = false
	return &result, nil, true
}

func (c *Client) recordQuotaAttempt(attempt *quotaAttempt) {
	if c.quotaAttempts == nil {
		c.quotaAttempts = make(map[string]*quotaAttempt)
	}
	c.quotaAttempts[attempt.key.kind] = attempt
}

func createQuotaOutput(result *quotaWriteResult) *CreateNotificationQuotaOutput {
	if result == nil {
		return nil
	}
	out := &CreateNotificationQuotaOutput{OrderID: result.orderID, PaymentPending: result.paymentPending}
	if result.quota != nil {
		q := *result.quota
		out.Quota = &q
	}
	return out
}

// sendQuotaMutation uses transport send evidence and known 4xx refusals.
func (c *Client) sendQuotaMutation(ctx context.Context, req transport.Request, credentials *monitorReadCredentials) ([]byte, error, bool) {
	req.Once = true
	raw, status, err, notSent := c.quotaRequestOutcome(ctx, req, credentials)
	if err == nil {
		return raw, nil, false
	}
	if notSent {
		return nil, err, true
	}
	var api *core.APIError
	definite := status >= 400 && status < 500
	if errors.As(err, &api) {
		safe := *api
		safe.Err = monitorReadCause(api.Err)
		err = &safe
	} else {
		err = monitorReadError(req.Operation, status, "RequestFailed", "quota request failed", err)
	}
	return nil, err, definite
}

func (c *Client) requireNotificationAbsent(ctx context.Context, op, kind string, credentials *monitorReadCredentials) error {
	_, err := c.readNotificationQuota(ctx, op, kind, credentials)
	var absent *notificationQuotaAbsent
	if errors.As(err, &absent) {
		return nil
	}
	if err != nil {
		return err
	}
	return fmt.Errorf("%s: an Email quota already exists: %w", op, core.ErrInvalidInput)
}

func decodeQuotaOrder(op string, raw []byte, redirect string) (quotaWriteResult, error) {
	obj := quotaObjectOf(raw)
	orderID := obj.text("orderId", true)
	payment := obj.text("paymentUrl", true)
	if obj.invalid {
		return quotaWriteResult{}, quotaResponseError(op)
	}
	return quotaWriteResult{orderID: orderID, paymentPending: payment != "" && payment != redirect}, nil
}

// CreateNotificationQuota orders one month of Email capacity. MaxPrice defaults
// to zero and guards the higher of list and quote prices. Each mutation is sent
// once. An unresolved result, including NoWait, blocks further writes on this
// kind until a read confirms it. Clients and processes do not share this ledger.
func (c *Client) CreateNotificationQuota(ctx context.Context, in *CreateNotificationQuotaInput) (out *CreateNotificationQuotaOutput, err error) {
	const op = "monitor.CreateNotificationQuota"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	input := *in
	kind, err := notificationEmailKind(op, input.Kind)
	if err != nil {
		return nil, err
	}
	if err := checkNotificationPrice(op, 0, 0, input.MaxPrice); err != nil {
		return nil, err
	}
	if err := c.quotaMu.acquire(ctx); err != nil {
		return nil, err
	}
	defer c.quotaMu.release()
	credentials := new(monitorReadCredentials)
	if attempt := c.quotaAttempts[kind]; attempt != nil && attempt.credentials != nil {
		credentials = attempt.credentials
	}
	defer func() {
		var api *core.APIError
		if errors.As(err, &api) && api.Message == "quota credential safety limit exceeded" {
			// No retained string is safe to project after a credential was omitted.
			if out != nil {
				out.OrderID = ""
				out.Quota = nil
			}
			if attempt := c.quotaAttempts[kind]; attempt != nil {
				attempt.result.orderID = ""
				attempt.result.quota = nil
			}
		}
		if out != nil {
			c.scrubQuotaWriteResult(&quotaWriteResult{quota: out.Quota}, *credentials)
			out.OrderID, _ = c.c.RedactError(out.OrderID, "", (*credentials)...)
		}
		if attempt := c.quotaAttempts[kind]; attempt != nil {
			c.scrubQuotaWriteResult(&attempt.result, *credentials)
		}
	}()
	key := quotaAttemptKey{operation: op, kind: kind, packageID: input.PackageID, monthPeriod: 1}
	// An omitted package means the original resolved default while reconciling,
	// rather than permission to resolve a different catalog default and reorder.
	if attempt := c.quotaAttempts[kind]; attempt != nil && input.PackageID == "" {
		key.packageID = attempt.key.packageID
	}
	if result, err, handled := c.reconcileQuotaAttempt(ctx, key); handled {
		if err != nil && errors.Is(err, core.ErrInvalidInput) {
			return nil, err
		}
		// A different operation must not expose its result as a create result.
		if attempt := c.quotaAttempts[kind]; attempt != nil && !key.matches(attempt.key) {
			return nil, err
		}
		return createQuotaOutput(result), err
	}
	terms, err := c.prepareNotificationCreate(ctx, op, &input, credentials)
	if err != nil {
		return nil, err
	}
	quote, err := c.quoteNotificationCreate(ctx, op, terms, credentials)
	if err != nil {
		return nil, err
	}
	if err := checkNotificationPrice(op, terms.listPrice, quote.OptimumPrice, input.MaxPrice); err != nil {
		return nil, err
	}
	if err := c.requireNotificationAbsent(ctx, op, kind, credentials); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	safePackageID, _ := c.c.RedactError(terms.packageID, "", (*credentials)...)
	if safePackageID != terms.packageID {
		return nil, quotaResponseError(op)
	}
	key.packageID = terms.packageID
	attempt := &quotaAttempt{key: key, credentials: credentials, reconcile: c.notificationCreateReconciler(op, terms, credentials)}
	body := terms.orderBody()
	raw, sendErr, definite := c.sendQuotaMutation(ctx, transport.Request{Operation: op, Method: http.MethodPost, URL: c.billingRoute("v1", []string{kind, "quota"}), Body: body, OK: []int{http.StatusOK}}, credentials)
	if definite {
		return nil, sendErr
	}
	c.recordQuotaAttempt(attempt)
	if sendErr != nil {
		return createQuotaOutput(&attempt.result), quotaUnsettled(op, sendErr)
	}
	result, err := decodeQuotaOrder(op, raw, body.RedirectURL)
	if err != nil {
		return createQuotaOutput(&attempt.result), quotaUnsettled(op, err)
	}
	attempt.result = result
	if result.paymentPending {
		return createQuotaOutput(&result), quotaUnsettled(op, nil)
	}
	if input.NoWait {
		return createQuotaOutput(&result), nil
	}
	resolution, err := c.waitQuotaAttempt(ctx, op, attempt.reconcile, quotaSettlementBound)
	if err != nil {
		return createQuotaOutput(&result), err
	}
	delete(c.quotaAttempts, kind)
	result.quota = resolution.quota
	return createQuotaOutput(&result), nil
}

func (c *Client) scrubQuotaWriteResult(result *quotaWriteResult, credentials monitorReadCredentials) {
	result.orderID, _ = c.c.RedactError(result.orderID, "", credentials...)
	if q := result.quota; q != nil {
		for _, field := range []*string{&q.ID, &q.Name, &q.Type, &q.Status, &q.PackageID} {
			*field, _ = c.c.RedactError(*field, "", credentials...)
		}
	}
}
