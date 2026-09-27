package loadbalancer

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/transport"
	"danny.vn/vngcloud/pricing"
)

// lbResizePollInterval and lbResizeBound time ResizeLoadBalancer's
// post-resize wait, per the design's wait table.
const (
	lbResizePollInterval = 10 * time.Second
	lbResizeBound        = 45 * time.Minute
)

// ResizeLoadBalancerInput changes a load balancer's package.
// QuoteResizeLoadBalancer takes the same Input and prices the change, per
// the SDK's paid-write convention that a quote is built from the write's
// own code.
type ResizeLoadBalancerInput struct {
	LoadBalancerID string `vngcloud:"required"`
	// PackageID is the package to change to.
	PackageID string `vngcloud:"required"`

	// MaxPrice is VND; a paid ResizeLoadBalancer refuses to order above it.
	// QuoteResizeLoadBalancer ignores it.
	MaxPrice float64
	// NoWait skips ResizeLoadBalancer's post-resize wait.
	// QuoteResizeLoadBalancer ignores it.
	NoWait bool
}

// QuoteResizeLoadBalancer prices the package change Input describes,
// without ordering it. The billing gateway prices a resize by the new
// PackageID and the LoadBalancerID being resized; a quote for a missing
// LoadBalancerID returns the server's own error unchanged (a 400, not the
// 404 every other load-balancer read gives for a missing ID: the server
// checks the shape before the ID). It ignores Input.MaxPrice and
// Input.NoWait, which govern only an actual resize.
func (c *Client) QuoteResizeLoadBalancer(ctx context.Context, in *ResizeLoadBalancerInput) (*pricing.GetQuoteOutput, error) {
	const op = "loadbalancer.QuoteResizeLoadBalancer"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	for _, id := range [...]struct{ field, value string }{
		{"LoadBalancerID", in.LoadBalancerID},
		{"PackageID", in.PackageID},
	} {
		if err := core.CheckPathID(op, id.field, id.value); err != nil {
			return nil, err
		}
	}

	return c.pricing.GetQuote(ctx, &pricing.GetQuoteInput{
		ResourceType: pricing.ResourceLoadBalancer,
		Action:       pricing.ActionResize,
		ResourceInfo: map[string]any{
			"packageId":      in.PackageID,
			"loadBalancerId": in.LoadBalancerID,
		},
	})
}

// ResizeLoadBalancerOutput is ResizeLoadBalancer's result. Changed is false,
// with QuotedPrice 0 and nothing quoted or sent, only when PackageID already
// matched the load balancer's current package.
type ResizeLoadBalancerOutput struct {
	LoadBalancer LoadBalancer
	QuotedPrice  float64
	Changed      bool
}

// resizeLoadBalancerBody is ResizeLoadBalancer's request body: packageId
// alone, per the design.
type resizeLoadBalancerBody struct {
	PackageID string `json:"packageId"`
}

// ResizeLoadBalancer changes a load balancer's package. Before any request,
// it checks Input's shape and rejects a NaN, +Inf, -Inf, or negative
// MaxPrice with core.ErrInvalidInput (checkMaxPrice), for the same reason
// CreateLoadBalancer does.
//
// It reads the load balancer first. When PackageID already matches its
// current package, ResizeLoadBalancer returns Changed false at once,
// quoting and sending nothing. Otherwise it waits, within the pre-write
// bound (10 minutes, polling every 5 seconds), until the load balancer is
// no longer busy; past that bound it returns ErrBusy, sending nothing.
//
// It then quotes with QuoteResizeLoadBalancer's own fields and, when the
// quote's OptimumPrice exceeds Input.MaxPrice (default 0), returns
// ErrPriceAboveMax naming both amounts, sending nothing.
//
// The resize PUT is sent with transport.Request.Once: it is never resent,
// whatever the failure, since a resend could race a resize already in
// progress. A busy refusal from the PUT itself, matched the same way as the
// pre-write wait, returns ErrBusy: the server did not act, and a rerun is
// safe because ResizeLoadBalancer always reads first. Any other failure is
// returned as is.
//
// Without NoWait, ResizeLoadBalancer then waits up to 45 minutes, polling
// every 10 seconds, for the load balancer's progressStatus to reach CREATED
// with PackageID equal to the new package. If it reaches ERROR instead, the
// returned error wraps ErrFailed; if the bound runs out, or a read or a
// sleep fails, such as from a canceled ctx, it wraps ErrNotSettled, whose
// message says the resize was accepted and must not be repeated. Either way
// the Output is never nil. NoWait skips this wait and returns the load
// balancer as last read, before the PUT, at once.
func (c *Client) ResizeLoadBalancer(ctx context.Context, in *ResizeLoadBalancerInput) (*ResizeLoadBalancerOutput, error) {
	const op = "loadbalancer.ResizeLoadBalancer"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	for _, id := range [...]struct{ field, value string }{
		{"LoadBalancerID", in.LoadBalancerID},
		{"PackageID", in.PackageID},
	} {
		if err := core.CheckPathID(op, id.field, id.value); err != nil {
			return nil, err
		}
	}
	if err := checkMaxPrice(op, in.MaxPrice); err != nil {
		return nil, err
	}

	unlock, err := c.lockLoadBalancer(ctx, in.LoadBalancerID)
	if err != nil {
		return nil, err
	}
	defer unlock()

	current, err := c.GetLoadBalancer(ctx, &GetLoadBalancerInput{LoadBalancerID: in.LoadBalancerID})
	if err != nil {
		return nil, err
	}
	if current.LoadBalancer.PackageID == in.PackageID {
		return &ResizeLoadBalancerOutput{LoadBalancer: current.LoadBalancer, Changed: false}, nil
	}

	if _, err := c.waitLoadBalancerPreWriteReady(ctx, op, in.LoadBalancerID); err != nil {
		return nil, err
	}

	// Quote with ResizeLoadBalancer's own op, rather than calling
	// QuoteResizeLoadBalancer directly, so a quote failure here is reported
	// under this call's own name; QuoteResizeLoadBalancer, called
	// separately, keeps its own independent behavior. The shape checks
	// above already cover everything QuoteResizeLoadBalancer would check.
	quote, err := c.pricing.GetQuote(ctx, &pricing.GetQuoteInput{
		ResourceType: pricing.ResourceLoadBalancer,
		Action:       pricing.ActionResize,
		ResourceInfo: map[string]any{
			"packageId":      in.PackageID,
			"loadBalancerId": in.LoadBalancerID,
		},
	})
	if err != nil {
		return nil, err
	}
	if quote.OptimumPrice > in.MaxPrice {
		return nil, fmt.Errorf("%w: %s: quote %.0f VND exceeds MaxPrice %.0f VND", ErrPriceAboveMax, op, quote.OptimumPrice, in.MaxPrice)
	}

	projectID, err := c.c.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	req := transport.Request{
		Operation: op,
		Method:    http.MethodPut,
		URL:       c.lbURL([]string{projectID, "loadBalancers", in.LoadBalancerID, "resize"}, nil),
		Body:      resizeLoadBalancerBody{PackageID: in.PackageID},
		OK:        httpStatusOKWrite,
		Once:      true,
	}
	if err := c.c.DoJSON(ctx, req, nil); err != nil {
		if isBusyRefusal(err) {
			return nil, fmt.Errorf("%w: %s: load balancer %s: %w", ErrBusy, op, in.LoadBalancerID, err)
		}
		return nil, err
	}

	if in.NoWait {
		fallback := current.LoadBalancer
		fallback.PackageID = in.PackageID
		return &ResizeLoadBalancerOutput{LoadBalancer: fallback, QuotedPrice: quote.OptimumPrice, Changed: true}, nil
	}

	settled, waitErr := c.waitLoadBalancerResized(ctx, op, in.LoadBalancerID, in.PackageID)
	if settled == nil {
		settled = &current.LoadBalancer
	}
	return &ResizeLoadBalancerOutput{LoadBalancer: *settled, QuotedPrice: quote.OptimumPrice, Changed: true}, waitErr
}

// waitLoadBalancerResized is ResizeLoadBalancer's post-resize wait unless
// NoWait is set: it reads id with GetLoadBalancer until its ProgressStatus
// reaches lbStatusCreated with PackageID equal to newPackageID (settled) or
// lbStatusError (failed); any other status, including one this SDK does not
// recognize, keeps it polling.
func (c *Client) waitLoadBalancerResized(ctx context.Context, op, id, newPackageID string) (*LoadBalancer, error) {
	var lb *LoadBalancer
	err := poll(ctx, c.now, c.sleep, lbResizePollInterval, lbResizeBound,
		func(ctx context.Context) (bool, error) {
			out, err := c.GetLoadBalancer(ctx, &GetLoadBalancerInput{LoadBalancerID: id})
			if err != nil {
				return true, err
			}
			lb = &out.LoadBalancer
			switch {
			case lb.ProgressStatus == lbStatusError:
				return true, fmt.Errorf("%w: %s: load balancer %s is ERROR", ErrFailed, op, id)
			case lb.ProgressStatus == lbStatusCreated && lb.PackageID == newPackageID:
				return true, nil
			default:
				return false, nil
			}
		},
		func() error {
			return fmt.Errorf("%w: %s: load balancer %s did not reach CREATED with package %s within %s; the resize was accepted and must not be repeated",
				ErrNotSettled, op, id, newPackageID, lbResizeBound)
		},
	)
	return lb, wrapNotSettled(op, "load balancer "+id, err)
}
