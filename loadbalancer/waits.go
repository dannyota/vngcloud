package loadbalancer

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"time"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/core"
)

// sleepFunc waits for d or ctx's end, whichever comes first, returning
// ctx.Err() when ctx ends first. Tests inject a fake one so the real waits
// never really elapse.
type sleepFunc func(ctx context.Context, d time.Duration) error

// clockFunc reads the current time. poll uses it, alongside a sleepFunc, to
// bound a wait by elapsed wall time rather than by counting poll intervals,
// so a slow read itself counts against the bound. Tests inject a fake
// clock; production uses time.Now.
type clockFunc func() time.Time

// contextSleep is the real sleepFunc.
func contextSleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// poll runs step at once, then again every interval, until step reports
// stop true or bound has elapsed, by now, since poll's first call to step.
// Elapsed time is read from now rather than counted in interval steps, so a
// step that itself takes real time, such as a slow read, counts against the
// bound instead of only the sleeps between steps; a test injects both a
// fake clock and a sleepFunc that returns quickly. This is the same shape
// as vDNS's own unexported poll, duplicated here rather than shared, since
// each write in this package needs its own interval and bound and no
// package here imports another for this.
func poll(ctx context.Context, now clockFunc, sleep sleepFunc, interval, bound time.Duration, step func(ctx context.Context) (stop bool, err error), onTimeout func() error) error {
	deadline := now().Add(bound)
	for {
		stop, err := step(ctx)
		if stop {
			return err
		}
		if !now().Before(deadline) {
			return onTimeout()
		}
		if err := sleep(ctx, interval); err != nil {
			return err
		}
	}
}

// Sentinel errors for vLB writes.
var (
	// ErrPriceAboveMax means CreateLoadBalancer or ResizeLoadBalancer's
	// quote priced the order above Input.MaxPrice. Nothing was ordered. It
	// is the same value as vngcloud.ErrPriceAboveMax, so errors.Is matches
	// either name.
	ErrPriceAboveMax = vngcloud.ErrPriceAboveMax

	// ErrBusy means a write found the load balancer, or the child it
	// targets, still busy past the pre-write wait's bound, or the server
	// refused a resize because the load balancer was busy. Nothing was
	// sent by this call, so the write is safe to try again.
	ErrBusy = errors.New("loadbalancer: resource busy")

	// ErrInUse means DeletePool was refused because a listener still names
	// the pool as its default pool, found by a pre-delete read, or because
	// the server's own refusal named the pool in use. In the first case
	// nothing was sent; in the second, the request reached the server.
	ErrInUse = errors.New("loadbalancer: resource in use")

	// ErrFailed means a write's post-write wait saw the load balancer or
	// its child reach ERROR. The Output, when the call returns one, still
	// holds the last resource a read returned.
	ErrFailed = errors.New("loadbalancer: write failed on the server")

	// ErrNotSettled means a write was sent, and may have reached the
	// server, but no read confirmed its result within the write's bound, or
	// a confirming read did not match what was sent. A create or a resize
	// must not be repeated; every other write in this package always reads
	// first, so rerunning it is safe.
	ErrNotSettled = errors.New("loadbalancer: write accepted but not settled")
)

// Load balancer progressStatus values this package acts on. Any other value,
// including one this SDK does not recognize, keeps a wait polling rather
// than treating it as settled or failed.
const (
	lbStatusCreatingBilling = "CREATING-BILLING"
	lbStatusCreating        = "CREATING"
	lbStatusUpdating        = "UPDATING"
	lbStatusDeleting        = "DELETING"
	lbStatusCreated         = "CREATED"
	lbStatusError           = "ERROR"
)

// lockLoadBalancer acquires the per-ID write lock for loadBalancerID,
// honoring ctx: if ctx ends before the lock is free, it returns ctx.Err()
// without ever taking the lock. The returned func releases the lock and
// must be called exactly once. This serializes writes to one load balancer
// within a single Client; it does nothing across processes or Clients, where
// the server's own busy refusal, and this SDK's busy resend, are what apply.
func (c *Client) lockLoadBalancer(ctx context.Context, loadBalancerID string) (func(), error) {
	c.locksMu.Lock()
	ch, ok := c.locks[loadBalancerID]
	if !ok {
		ch = make(chan struct{}, 1)
		c.locks[loadBalancerID] = ch
	}
	c.locksMu.Unlock()

	select {
	case ch <- struct{}{}:
		return func() { <-ch }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// checkMaxPrice refuses a maxPrice that CreateLoadBalancer or
// ResizeLoadBalancer's own price guard (quote.OptimumPrice > maxPrice)
// cannot compare safely: NaN compares false against every quote, which
// would silently disable the guard rather than block an overpriced order;
// +Inf and -Inf are never a real budget; and a negative value can never be
// exceeded by a live quote, the same effective hole as NaN. This runs
// before any request, including the quote itself.
func checkMaxPrice(op string, maxPrice float64) error {
	if math.IsNaN(maxPrice) || math.IsInf(maxPrice, 0) || maxPrice < 0 {
		return fmt.Errorf("%w: %s: MaxPrice must be a non-negative, finite number, got %v", core.ErrInvalidInput, op, maxPrice)
	}
	return nil
}

// is4xxAPIError reports whether err is a *core.APIError whose StatusCode is
// 4xx, meaning the server rejected the request outright and never acted on
// it.
func is4xxAPIError(err error) bool {
	var apiErr *core.APIError
	return errors.As(err, &apiErr) && apiErr.StatusCode >= 400 && apiErr.StatusCode < 500
}

// wrapAmbiguousCreateErr wraps err from a create POST that failed
// ambiguously: any error that is not a 4xx *core.APIError, where whether
// the resource reached the server is unknown. It is never called for a 4xx,
// which means the server rejected the request outright and nothing was
// created. listCmd names the exact CLI list command a caller checks before
// creating again, matching the name exactly. A nil err stays nil.
func wrapAmbiguousCreateErr(op, listCmd string, err error) error {
	if err == nil {
		return nil
	}
	if is4xxAPIError(err) {
		return err
	}
	return fmt.Errorf("%s: create may have already reached the server; check with %s and match the name exactly: %w", op, listCmd, err)
}

// errCreateResponseNoID builds the error a create returns when its response
// carries no uuid: the resource may exist, and listCmd names the exact CLI
// list command to check with before creating it again.
func errCreateResponseNoID(op string, status int, listCmd string) error {
	return &core.APIError{
		Operation:  op,
		StatusCode: status,
		Message:    fmt.Sprintf("create response had no id; the resource may exist, check with %s and match the name exactly", listCmd),
	}
}

// wrapNotSettled wraps err with ErrNotSettled, naming op and resource,
// unless it already wraps ErrFailed or ErrNotSettled: both already carry a
// specific outcome (the resource reached ERROR, or an earlier step already
// classified this failure), which a blanket ErrNotSettled wrap would blur.
func wrapNotSettled(op, resource string, err error) error {
	if err == nil || errors.Is(err, ErrFailed) || errors.Is(err, ErrNotSettled) {
		return err
	}
	return fmt.Errorf("%w: %s: %s: %w", ErrNotSettled, op, resource, err)
}

// httpStatusOKCreate and httpStatusOKWrite are the success statuses this
// package accepts for a create and for every other write: the reference
// documents 200 for every vLB write, while VNG Cloud's SDK accepts only 202
// for all but certificate calls (already shipped). Both are accepted until
// a live check settles on one.
var (
	httpStatusOKCreate = []int{http.StatusOK, http.StatusAccepted}
	httpStatusOKWrite  = []int{http.StatusOK, http.StatusAccepted}
)
