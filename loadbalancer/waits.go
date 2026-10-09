package loadbalancer

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"
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

// poll sleeps firstDelay (when positive), then runs step, then again every
// interval, until step reports stop true or bound has elapsed, by now, since
// poll was entered. Elapsed time is read from now rather than counted in
// interval steps, so a step that itself takes real time, such as a slow
// read, counts against the bound instead of only the sleeps between steps;
// a test injects both a fake clock and a sleepFunc that returns quickly.
// This is the same shape as vDNS's own unexported poll, duplicated here
// rather than shared, since each write in this package needs its own
// interval and bound and no package here imports another for this.
//
// firstDelay exists because a status read taken the instant a write returns
// can still show the resource's state from before the write: the server may
// not have started applying it yet, so step's very first call would call a
// stale read "settled" without the write ever having been observed to take
// effect. Terraform's own provider waits before its first read the same
// way. A caller checking current state before sending anything, rather than
// confirming a change it just made, passes 0: there step should read
// immediately, not wait out of caution.
func poll(ctx context.Context, now clockFunc, sleep sleepFunc, firstDelay, interval, bound time.Duration, step func(ctx context.Context) (stop bool, err error), onTimeout func() error) error {
	deadline := now().Add(bound)
	if firstDelay > 0 {
		if err := sleep(ctx, firstDelay); err != nil {
			return err
		}
	}
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

	// ErrBusy means either a write found the load balancer, or the child it
	// targets, still busy past the pre-write wait's bound and sent nothing,
	// or, only for a resize, that the PUT itself was sent and the server
	// refused it because the load balancer was busy. Either way a rerun is
	// safe: ResizeLoadBalancer always reads the load balancer first, so a
	// resize that was refused this way never partly applied.
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

// isLoadBalancerBusy reports whether a load balancer with this
// progressStatus refuses a write to it or one of its children, per the
// design: CREATING, CREATING-BILLING, UPDATING, or DELETING.
func isLoadBalancerBusy(status string) bool {
	switch status {
	case lbStatusCreating, lbStatusCreatingBilling, lbStatusUpdating, lbStatusDeleting:
		return true
	default:
		return false
	}
}

// isBusyRefusal reports whether err is a 4xx *core.APIError whose message
// matches one of the server's busy refusals (case-insensitive). A 5xx never
// qualifies, even with busy text: it may have reached the server. The
// messages are "... is not ready" (load balancer or listener), "... is
// updating" (load balancer or pool), "... is creating", or "... is
// deleting" (load balancer). It is the trigger for the one busy resend a
// free write gets, and for ResizeLoadBalancer's immediate ErrBusy.
func isBusyRefusal(err error) bool {
	var apiErr *core.APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode < 400 || apiErr.StatusCode >= 500 {
		return false
	}
	msg := strings.ToLower(apiErr.Message)
	for _, sub := range [...]string{"is not ready", "is updating", "is creating", "is deleting"} {
		if strings.Contains(msg, sub) {
			return true
		}
	}
	return false
}

// preWritePollInterval and preWriteBound time the wait every write except a
// load balancer create or delete runs first: until neither the load
// balancer nor the child it targets is busy. Past the bound: ErrBusy,
// nothing sent.
const (
	preWritePollInterval = 5 * time.Second
	preWriteBound        = 10 * time.Minute
)

// waitLoadBalancerPreWriteReady waits, within the pre-write bound, until
// GetLoadBalancer(lbID) reports a progressStatus isLoadBalancerBusy does not
// consider busy. It is ResizeLoadBalancer and CreatePool's (and every other
// create's) own pre-write wait, none of which have an existing child to
// also check; a write on an existing child wraps this logic with its own
// child check instead (see waitPreWriteReady). Past the bound it returns
// ErrBusy.
func (c *Client) waitLoadBalancerPreWriteReady(ctx context.Context, op, lbID string) error {
	_, err := c.readLoadBalancerPreWriteReady(ctx, op, lbID)
	return err
}

// readLoadBalancerPreWriteReady is waitLoadBalancerPreWriteReady that also
// returns the load balancer its last read saw.
func (c *Client) readLoadBalancerPreWriteReady(ctx context.Context, op, lbID string) (*LoadBalancer, error) {
	var lb *LoadBalancer
	err := poll(ctx, c.now, c.sleep, 0, preWritePollInterval, preWriteBound,
		func(ctx context.Context) (bool, error) {
			out, err := c.GetLoadBalancer(ctx, &GetLoadBalancerInput{LoadBalancerID: lbID})
			if err != nil {
				return true, err
			}
			lb = &out.LoadBalancer
			return !isLoadBalancerBusy(out.LoadBalancer.ProgressStatus), nil
		},
		func() error {
			return fmt.Errorf("%w: %s: load balancer %s is not ready within %s; nothing sent", ErrBusy, op, lbID, preWriteBound)
		},
	)
	if err != nil {
		return nil, err
	}
	return lb, nil
}

// isChildBusy reports whether a listener, pool, or policy with this
// progressStatus refuses a write to it, per the design: CREATING, UPDATING,
// or DELETING.
func isChildBusy(status string) bool {
	switch status {
	case lbStatusCreating, lbStatusUpdating, lbStatusDeleting:
		return true
	default:
		return false
	}
}

// childPollInterval and childBound time a child's post-write wait: create,
// update, members replace, and delete, per the design's wait table.
const (
	childPollInterval = 5 * time.Second
	childBound        = 10 * time.Minute
)

// waitPreWriteReady waits, within the pre-write bound, until the load
// balancer named lbID and the child named kind/id are both not busy. get
// reads the child and reports its progressStatus; a get that 404s should
// itself decide what to do (typically returning the error unchanged, since
// this wait always targets an existing child). It returns the last child a
// read returned, so the caller's read-merge uses this same fresh read
// instead of paying for a second request; a package-level function, not a
// method, since Go methods cannot take their own type parameters.
func waitPreWriteReady[T any](c *Client, ctx context.Context, op, lbID, kind, id string, get func(ctx context.Context) (T, string, error)) (T, error) {
	var child T
	err := poll(ctx, c.now, c.sleep, 0, preWritePollInterval, preWriteBound,
		func(ctx context.Context) (bool, error) {
			lbOut, err := c.GetLoadBalancer(ctx, &GetLoadBalancerInput{LoadBalancerID: lbID})
			if err != nil {
				return true, err
			}
			if isLoadBalancerBusy(lbOut.LoadBalancer.ProgressStatus) {
				return false, nil
			}
			v, status, err := get(ctx)
			if err != nil {
				return true, err
			}
			child = v
			return !isChildBusy(status), nil
		},
		func() error {
			return fmt.Errorf("%w: %s: load balancer %s or %s %s is not ready within %s; nothing sent",
				ErrBusy, op, lbID, kind, id, preWriteBound)
		},
	)
	return child, err
}

// waitChildSettled is a child create, update, or members-replace write's
// post-write wait unless NoWait is set: it polls, at childPollInterval up to
// childBound, until getStatus reports the child's progressStatus
// lbStatusCreated and a fresh GetLoadBalancer shows lbID no longer busy
// (settled), or either reports lbStatusError (failed). getStatus should
// treat a NotFound read as not yet settled (returning "", nil) rather than
// an error, since a child just written may not be readable for a moment; any
// other status, including one this SDK does not recognize, keeps it
// polling.
//
// It sleeps one childPollInterval before its first read (poll's firstDelay):
// an update or a members replace often leaves the child's own progressStatus
// at lbStatusCreated throughout, since only the child's contents changed, so
// an immediate read cannot be told apart from one taken before the write was
// even applied. Without this delay, a write that genuinely succeeded could
// settle on that first, too-early read, and a caller that then confirms the
// new content, such as the pool members replace does, could still catch the
// server mid-update and be wrongly told ErrNotSettled.
func (c *Client) waitChildSettled(ctx context.Context, op, lbID, kind, id string, getStatus func(ctx context.Context) (string, error)) error {
	err := poll(ctx, c.now, c.sleep, childPollInterval, childPollInterval, childBound,
		func(ctx context.Context) (bool, error) {
			status, err := getStatus(ctx)
			if err != nil {
				return true, err
			}
			switch status {
			case lbStatusError:
				return true, fmt.Errorf("%w: %s: %s %s is ERROR", ErrFailed, op, kind, id)
			case lbStatusCreated:
				lbOut, err := c.GetLoadBalancer(ctx, &GetLoadBalancerInput{LoadBalancerID: lbID})
				if err != nil {
					return true, err
				}
				if lbOut.LoadBalancer.ProgressStatus == lbStatusError {
					return true, fmt.Errorf("%w: %s: load balancer %s is ERROR", ErrFailed, op, lbID)
				}
				return !isLoadBalancerBusy(lbOut.LoadBalancer.ProgressStatus), nil
			default:
				return false, nil
			}
		},
		func() error {
			return fmt.Errorf("%w: %s: %s %s did not settle within %s; the write was accepted and must not be repeated",
				ErrNotSettled, op, kind, id, childBound)
		},
	)
	return wrapNotSettled(op, kind+" "+id, err)
}

// waitChildDeleted is a child delete's post-delete wait unless NoWait is
// set: it polls, at childPollInterval up to childBound, until getStatus
// reports the child gone (notFound true) and a fresh GetLoadBalancer shows
// lbID no longer busy (settled), or getStatus reports lbStatusError
// (failed); any other status keeps it polling.
//
// As with waitLoadBalancerDeleted, ERROR stops the wait at once rather than
// being polled through to the bound, matching the design's wait table
// (Child delete: Failed ERROR) rather than assuming it can still resolve to
// 404; see that function's doc comment.
func (c *Client) waitChildDeleted(ctx context.Context, op, lbID, kind, id string, getStatus func(ctx context.Context) (status string, notFound bool, err error)) error {
	err := poll(ctx, c.now, c.sleep, 0, childPollInterval, childBound,
		func(ctx context.Context) (bool, error) {
			status, notFound, err := getStatus(ctx)
			if err != nil {
				return true, err
			}
			if notFound {
				lbOut, err := c.GetLoadBalancer(ctx, &GetLoadBalancerInput{LoadBalancerID: lbID})
				if err != nil {
					return true, err
				}
				if lbOut.LoadBalancer.ProgressStatus == lbStatusError {
					return true, fmt.Errorf("%w: %s: load balancer %s is ERROR", ErrFailed, op, lbID)
				}
				return !isLoadBalancerBusy(lbOut.LoadBalancer.ProgressStatus), nil
			}
			if status == lbStatusError {
				return true, fmt.Errorf("%w: %s: %s %s is ERROR", ErrFailed, op, kind, id)
			}
			return false, nil
		},
		func() error {
			return fmt.Errorf("%w: %s: %s %s did not settle within %s; delete was sent and a rerun is safe",
				ErrNotSettled, op, kind, id, childBound)
		},
	)
	return wrapNotSettled(op, kind+" "+id, err)
}

// sendWithBusyResend runs do, which sends one free write request, and
// returns its result. If do fails with a busy refusal (isBusyRefusal),
// meaning the server did not act on it, sendWithBusyResend calls waitBusy to
// wait for the load balancer (and the child being changed, when waitBusy
// checks one) to go idle again, then runs do exactly once more and returns
// that result, whatever it is; a second busy refusal is returned as any
// other failure would be, with no further resend. Any failure that is not a
// busy refusal is returned at once, with no resend: a 5xx, a network error,
// or any other 4xx may have already reached the server, and a create in
// particular must never be sent twice on the chance it already landed. This
// is the design's only resend for a free write; it must never wrap a load
// balancer create, delete, or resize, whose own Once and price-guard rules
// apply instead.
func sendWithBusyResend(ctx context.Context, waitBusy func(ctx context.Context) error, do func() error) error {
	err := do()
	if err == nil || !isBusyRefusal(err) {
		return err
	}
	if err := waitBusy(ctx); err != nil {
		return err
	}
	return do()
}

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

// wrapAmbiguousResizeErr wraps err from a resize PUT that failed
// ambiguously: any error that is not a 4xx *core.APIError, where whether the
// resize reached the server is unknown. It is never called for a 4xx (which
// means the server rejected the request outright and never acted on it) or
// for a busy refusal (already mapped to ErrBusy by the caller). The resize
// PUT is sent with Once and is never resent, so after a 5xx, a network
// error, or a timeout, the caller must read the load balancer
// (GetLoadBalancer) and compare its package to the one requested before any
// rerun: a rerun that landed on top of a resize that already reached the
// server would send a second paid resize. A nil err stays nil.
func wrapAmbiguousResizeErr(op, loadBalancerID string, err error) error {
	if err == nil || is4xxAPIError(err) {
		return err
	}
	return fmt.Errorf("%s: load balancer %s: resize may have already reached the server; read the load balancer with GetLoadBalancer and compare its package to the one requested before any rerun: %w",
		op, loadBalancerID, err)
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
