package loadbalancer

import (
	"context"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/pricing"
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
