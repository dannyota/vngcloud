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

// lbCreatePollInterval and lbCreateBound time CreateLoadBalancer's
// post-create wait, per the design's wait table.
const (
	lbCreatePollInterval = 10 * time.Second
	lbCreateBound        = 20 * time.Minute
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
// that a quote is built from the create's own code. A quote requires only
// PackageID and ZoneID: Name, Scheme, SubnetID, and Type do not change the
// price and never reach the billing gateway. A quote still checks the shape
// of Scheme and SubnetID when they are set. Scheme has no default,
// unlike the console's Internet: an Internet load balancer gets a public
// address, and the caller names that exposure by setting Scheme itself.
type CreateLoadBalancerInput struct {
	Name      string `vngcloud:"required"`
	PackageID string `vngcloud:"required"`
	// Type is TypeLayer4 or TypeLayer7.
	Type string `vngcloud:"required"`
	// Scheme must be exactly SchemeInternet or SchemeInternal: any other
	// value, including one padded or cased differently, or an invented
	// value such as "Public", is refused before any request.
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
// It is not the create request body itself, which also carries name,
// scheme, subnetId, and type: the billing gateway prices a load balancer by
// its package and zone alone.
type createLoadBalancerQuoteBody struct {
	PackageID    string `json:"packageId"`
	ZoneID       string `json:"zoneId"`
	IsBuyMorePoc bool   `json:"isBuyMorePoc"`
}

// checkScheme returns core.ErrInvalidInput unless scheme is exactly
// SchemeInternet or SchemeInternal, the console's own values. There is no
// normalization: a padded, differently cased, or invented value such as
// "Public" is refused rather than silently sent to the server, since an
// Internet scheme is a public address the caller must name deliberately (see
// the design's Security section).
func checkScheme(op, scheme string) error {
	if scheme != SchemeInternet && scheme != SchemeInternal {
		return fmt.Errorf("%w: %s: Scheme must be %q or %q, got %q", core.ErrInvalidInput, op, SchemeInternet, SchemeInternal, scheme)
	}
	return nil
}

// checkLoadBalancerShape checks the shape of the fields of in that are set,
// under op's name: Scheme and the IDs. The quote and the create each require
// their own set first.
func checkLoadBalancerShape(op string, in *CreateLoadBalancerInput) error {
	if in.Scheme != "" {
		if err := checkScheme(op, in.Scheme); err != nil {
			return err
		}
	}
	for _, id := range [...]struct{ field, value string }{
		{"PackageID", in.PackageID},
		{"SubnetID", in.SubnetID},
	} {
		if id.value == "" {
			continue
		}
		if err := core.CheckPathID(op, id.field, id.value); err != nil {
			return err
		}
	}
	return nil
}

// createLoadBalancerQuoteInfo checks the priced fields of in under op's name
// and builds the create quote's resourceInfo from them alone, shared by
// QuoteCreateLoadBalancer and CreateLoadBalancer's price guard so both price
// the same request; each keeps its own op in every error it returns.
func createLoadBalancerQuoteInfo(op string, in *CreateLoadBalancerInput) (map[string]any, error) {
	if err := core.CheckRequiredFields(op, in, "PackageID", "ZoneID"); err != nil {
		return nil, err
	}
	if err := checkLoadBalancerShape(op, in); err != nil {
		return nil, err
	}
	return core.QuoteResourceInfo(createLoadBalancerQuoteBody{
		PackageID:    in.PackageID,
		ZoneID:       in.ZoneID,
		IsBuyMorePoc: false,
	})
}

// QuoteCreateLoadBalancer prices the load balancer Input would create,
// without ordering it. It requires and sends only PackageID and ZoneID, and
// checks the shape of Scheme and SubnetID when they are set, so a bad value
// fails here as it will at the create. It ignores Input.MaxPrice and
// Input.NoWait, which govern only an actual create.
func (c *Client) QuoteCreateLoadBalancer(ctx context.Context, in *CreateLoadBalancerInput) (*pricing.GetQuoteOutput, error) {
	const op = "loadbalancer.QuoteCreateLoadBalancer"
	info, err := createLoadBalancerQuoteInfo(op, in)
	if err != nil {
		return nil, err
	}
	return c.pricing.GetQuote(ctx, &pricing.GetQuoteInput{
		ResourceType: pricing.ResourceLoadBalancer,
		Action:       pricing.ActionCreate,
		ResourceInfo: info,
	})
}

// checkCreateLoadBalancerInput requires every field tagged
// vngcloud:"required" and checks the shape of the fields CreateLoadBalancer
// sends.
func checkCreateLoadBalancerInput(op string, in *CreateLoadBalancerInput) error {
	if err := core.CheckRequired(op, in); err != nil {
		return err
	}
	return checkLoadBalancerShape(op, in)
}

// CreateLoadBalancerOutput is CreateLoadBalancer's result. QuotedPrice is
// the OptimumPrice its price guard accepted.
type CreateLoadBalancerOutput struct {
	LoadBalancer LoadBalancer
	QuotedPrice  float64
}

// createLoadBalancerBody is CreateLoadBalancer's request body, per the
// design: name, packageId, scheme, subnetId, type, zoneId, autoScalable
// false, and isPoc false.
type createLoadBalancerBody struct {
	Name         string `json:"name"`
	PackageID    string `json:"packageId"`
	Scheme       string `json:"scheme"`
	SubnetID     string `json:"subnetId"`
	Type         string `json:"type"`
	ZoneID       string `json:"zoneId"`
	AutoScalable bool   `json:"autoScalable"`
	IsPoc        bool   `json:"isPoc"`
}

// CreateLoadBalancer orders a load balancer. Before any request, it checks
// Input's shape (checkCreateLoadBalancerInput, which also refuses a nil in)
// and then rejects a NaN, +Inf, -Inf, or negative MaxPrice with
// core.ErrInvalidInput (checkMaxPrice): the price guard below cannot compare
// any of those safely, and a bad guard on a paid create must fail closed
// rather than order anyway.
//
// It then quotes with the same priced-only body QuoteCreateLoadBalancer
// sends (createLoadBalancerQuoteInfo), so the quote command and the guard
// price one request. The quote is read and checked by
// quotedPrice, which refuses a missing, null, non-finite, or unpriced (0 or
// less, ErrUnpriced) price on its own, regardless of what
// pricing.Client.GetQuote would have done with the same response. When the price exceeds Input.MaxPrice (default
// 0), CreateLoadBalancer returns ErrPriceAboveMax naming both amounts,
// ordering nothing. A bare CreateLoadBalancerInput therefore orders nothing:
// a quote of 0 or less returns ErrUnpriced, whatever MaxPrice is, and any
// real price is above the default MaxPrice.
//
// The order is a POST sent with Once, so it reaches the server at most
// once: it is never retried after a failure that may have already reached
// the server, including a 401 (which would otherwise be resent with a
// refreshed token) and a 307 or 308 (which net/http would otherwise follow,
// resending the same body). After any error that is not a 4xx
// *core.APIError, the load balancer may have been ordered, and the caller
// lists load balancers by Name (list-load-balancers --name) and matches it
// exactly before ordering again, rather than retrying blind. A response
// with no uuid is the same kind of error.
//
// Without NoWait, CreateLoadBalancer then waits up to 20 minutes, polling
// every 10 seconds, for the new load balancer's progressStatus to reach
// CREATED; a 404 during that wait keeps polling, since the load balancer may
// not be readable yet. If it reaches ERROR instead, the returned error wraps
// ErrFailed; if the bound runs out, or a read or a sleep fails, such as from
// a canceled ctx, it wraps ErrNotSettled, whose message says the load
// balancer was ordered and must not be ordered again. Either way the Output
// is never nil: its LoadBalancer is the last one a read returned, or, if
// none did, the fields the order itself sent. NoWait skips this wait and
// returns that same fallback at once.
func (c *Client) CreateLoadBalancer(ctx context.Context, in *CreateLoadBalancerInput) (*CreateLoadBalancerOutput, error) {
	const op = "loadbalancer.CreateLoadBalancer"
	// checkCreateLoadBalancerInput runs core.CheckRequired first, so a nil in
	// is refused here rather than dereferenced below: checkMaxPrice(in.MaxPrice)
	// must never run before this, since in.MaxPrice would panic on a nil in.
	if err := checkCreateLoadBalancerInput(op, in); err != nil {
		return nil, err
	}
	info, err := createLoadBalancerQuoteInfo(op, in)
	if err != nil {
		return nil, err
	}
	if err := checkMaxPrice(op, in.MaxPrice); err != nil {
		return nil, err
	}

	price, err := c.quotedPrice(ctx, op, pricing.ActionCreate, info, false)
	if err != nil {
		return nil, err
	}
	if price > in.MaxPrice {
		return nil, fmt.Errorf("%w: %s: quote %.0f VND exceeds MaxPrice %.0f VND", ErrPriceAboveMax, op, price, in.MaxPrice)
	}

	projectID, err := c.c.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	var resp struct {
		UUID string `json:"uuid"`
	}
	req := transport.Request{
		Operation: op,
		Method:    http.MethodPost,
		URL:       c.lbURL([]string{projectID, "loadBalancers"}, nil),
		Body: createLoadBalancerBody{
			Name:         in.Name,
			PackageID:    in.PackageID,
			Scheme:       in.Scheme,
			SubnetID:     in.SubnetID,
			Type:         in.Type,
			ZoneID:       in.ZoneID,
			AutoScalable: false,
			IsPoc:        false,
		},
		OK: httpStatusOKCreate,
		// Once: a paid order must reach the server at most once. Without it,
		// a 401 is retried with a refreshed token, and a 307 or 308 is
		// followed by net/http, either of which would resend this POST and
		// risk a second load balancer.
		Once: true,
	}
	status, err := c.c.DoJSONStatus(ctx, req, &resp)
	if err != nil {
		return nil, wrapAmbiguousCreateErr(op, "list-load-balancers --name", err)
	}
	if resp.UUID == "" {
		return nil, errCreateResponseNoID(op, status, "list-load-balancers --name")
	}

	fallback := LoadBalancer{
		UUID:               resp.UUID,
		Name:               in.Name,
		PackageID:          in.PackageID,
		Type:               in.Type,
		LoadBalancerSchema: in.Scheme,
		PrivateSubnetID:    in.SubnetID,
		ZoneID:             in.ZoneID,
	}
	if in.NoWait {
		return &CreateLoadBalancerOutput{LoadBalancer: fallback, QuotedPrice: price}, nil
	}

	settled, waitErr := c.waitLoadBalancerCreated(ctx, op, resp.UUID)
	if settled == nil {
		settled = &fallback
	}
	return &CreateLoadBalancerOutput{LoadBalancer: *settled, QuotedPrice: price}, waitErr
}

// waitLoadBalancerCreated is CreateLoadBalancer's post-create wait unless
// NoWait is set: it reads id with GetLoadBalancer until its ProgressStatus
// reaches lbStatusCreated or lbStatusError; any other status, including one
// this SDK does not recognize, keeps it polling. A 404 keeps polling too,
// since a load balancer just ordered may not be readable yet; any other
// read failure stops the wait and is returned as is.
func (c *Client) waitLoadBalancerCreated(ctx context.Context, op, id string) (*LoadBalancer, error) {
	var lb *LoadBalancer
	err := poll(ctx, c.now, c.sleep, 0, lbCreatePollInterval, lbCreateBound,
		func(ctx context.Context) (bool, error) {
			out, err := c.GetLoadBalancer(ctx, &GetLoadBalancerInput{LoadBalancerID: id})
			if err != nil {
				if core.IsNotFound(err) {
					return false, nil
				}
				return true, err
			}
			lb = &out.LoadBalancer
			switch lb.ProgressStatus {
			case lbStatusCreated:
				return true, nil
			case lbStatusError:
				return true, fmt.Errorf("%w: %s: load balancer %s is ERROR", ErrFailed, op, id)
			default:
				return false, nil
			}
		},
		func() error {
			return fmt.Errorf("%w: %s: load balancer %s did not reach CREATED within %s; the load balancer was ordered and must not be ordered again",
				ErrNotSettled, op, id, lbCreateBound)
		},
	)
	return lb, wrapNotSettled(op, "load balancer "+id, err)
}
