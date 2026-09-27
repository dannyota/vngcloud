package loadbalancer

import (
	"context"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/pricing"
)

// CreateLoadBalancerInput's Type values.
const (
	TypeLayer4 = "Layer 4"
	TypeLayer7 = "Layer 7"
)

// CreateLoadBalancerInput's Scheme values.
const (
	SchemeInternet = "Internet"
	SchemeInternal = "Internal"
)

// CreateLoadBalancerInput creates a load balancer. QuoteCreateLoadBalancer
// takes the same Input and prices it, per the SDK's paid-write convention
// that a quote is built from the create's own code. Scheme has no default,
// unlike the console's Internet: an Internet load balancer gets a public
// address, and the caller names that exposure by setting Scheme itself.
type CreateLoadBalancerInput struct {
	Name      string `vngcloud:"required"`
	PackageID string `vngcloud:"required"`
	// Type is TypeLayer4 or TypeLayer7.
	Type string `vngcloud:"required"`
	// Scheme is SchemeInternet or SchemeInternal.
	Scheme   string `vngcloud:"required"`
	SubnetID string `vngcloud:"required"`
	ZoneID   string `vngcloud:"required"`

	// MaxPrice is VND a month; a paid CreateLoadBalancer refuses to order
	// above it. QuoteCreateLoadBalancer ignores it.
	MaxPrice float64
	// NoWait skips CreateLoadBalancer's post-create wait.
	// QuoteCreateLoadBalancer ignores it.
	NoWait bool
}

// createLoadBalancerQuoteBody is the create quote's own resourceInfo shape.
// It is not the create request body itself, which will also carry name,
// scheme, subnetId, and type once CreateLoadBalancer ships: the billing
// gateway prices a load balancer by its package and zone alone.
type createLoadBalancerQuoteBody struct {
	PackageID    string `json:"packageId"`
	ZoneID       string `json:"zoneId"`
	IsBuyMorePoc bool   `json:"isBuyMorePoc"`
}

// QuoteCreateLoadBalancer prices the load balancer Input would create,
// without ordering it. It checks every field a real CreateLoadBalancer will
// require, so a shape error surfaces here the same way it will for that
// write, then quotes only PackageID and ZoneID: Name, Scheme, SubnetID, and
// Type all reach the write's own body but never change the price. It
// ignores Input.MaxPrice and Input.NoWait, which govern only an actual
// create.
func (c *Client) QuoteCreateLoadBalancer(ctx context.Context, in *CreateLoadBalancerInput) (*pricing.GetQuoteOutput, error) {
	const op = "loadbalancer.QuoteCreateLoadBalancer"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	for _, id := range [...]struct{ field, value string }{
		{"PackageID", in.PackageID},
		{"SubnetID", in.SubnetID},
	} {
		if err := core.CheckPathID(op, id.field, id.value); err != nil {
			return nil, err
		}
	}

	info, err := core.QuoteResourceInfo(createLoadBalancerQuoteBody{
		PackageID:    in.PackageID,
		ZoneID:       in.ZoneID,
		IsBuyMorePoc: false,
	})
	if err != nil {
		return nil, err
	}
	return c.pricing.GetQuote(ctx, &pricing.GetQuoteInput{
		ResourceType: pricing.ResourceLoadBalancer,
		Action:       pricing.ActionCreate,
		ResourceInfo: info,
	})
}
