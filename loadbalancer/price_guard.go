package loadbalancer

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/routes"
	"danny.vn/vngcloud/internal/transport"
	"danny.vn/vngcloud/pricing"
)

// quotedPriceBody is the billing gateway's quote request body: the same
// shape pricing.Client sends. It is defined again here, rather than reused
// from pricing, so quotedPrice can read the raw response itself; see its
// doc comment for why.
type quotedPriceBody struct {
	ResourceType string         `json:"resourceType"`
	Action       string         `json:"action"`
	ResourceInfo map[string]any `json:"resourceInfo,omitempty"`
}

// quotedPrice prices a load balancer create or resize and returns its
// optimumPrice, refusing a response with no price, a null one, or one that
// is not a finite number, and, unless allowNegative, one that is negative.
//
// CreateLoadBalancer and ResizeLoadBalancer's price guard must refuse an
// unpriced quote regardless of how pricing.Client.GetQuote itself decodes
// the same response: GetQuoteOutput.OptimumPrice is a plain float64, so a
// JSON null there silently becomes 0 with no error, indistinguishable from a
// genuine zero price. quotedPrice sends its own request and reads the raw
// body itself instead, so this guard never depends on that decode.
// allowNegative is true only for a resize, whose quote may legitimately
// price a downsize below zero as a refund; a create's negative quote is
// always refused, since acting on it would order for less than nothing.
func (c *Client) quotedPrice(ctx context.Context, op, action string, resourceInfo map[string]any, allowNegative bool) (float64, error) {
	var raw json.RawMessage
	req := transport.Request{
		Operation: op,
		Method:    http.MethodPost,
		URL: c.c.RouteURL(routes.Route{
			Product: routes.ProductPortal,
			Version: "v1",
			Parts:   []string{"price"},
		}),
		Body: quotedPriceBody{
			ResourceType: pricing.ResourceLoadBalancer,
			Action:       action,
			ResourceInfo: resourceInfo,
		},
		OK:         []int{http.StatusOK},
		Idempotent: true,
	}
	if err := c.c.DoJSON(ctx, req, &raw); err != nil {
		return 0, err
	}
	price, err := decodeQuotedPrice(op, raw)
	if err != nil {
		return 0, err
	}
	if err := checkQuotedPrice(op, price, allowNegative); err != nil {
		return 0, err
	}
	return price, nil
}

// decodeQuotedPrice reads optimumPrice from a quote response's raw JSON,
// refusing a body with no optimumPrice key or one whose value is a literal
// JSON null: either means the server has no price for this quote, and
// treating it as 0 would let a paid write proceed as if it were free.
func decodeQuotedPrice(op string, raw json.RawMessage) (float64, error) {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(raw, &probe); err != nil {
		return 0, &core.APIError{Operation: op, Err: err}
	}
	priceRaw, ok := probe["optimumPrice"]
	if !ok || string(priceRaw) == "null" {
		return 0, &core.APIError{Operation: op, Message: "quote response had no price"}
	}
	var price float64
	if err := json.Unmarshal(priceRaw, &price); err != nil {
		return 0, &core.APIError{Operation: op, Err: err}
	}
	return price, nil
}

// checkQuotedPrice refuses a price CreateLoadBalancer or ResizeLoadBalancer
// must never act on: NaN or infinite, whatever produced it (a valid JSON
// number can never decode to either, but this stays a defense in depth
// rather than an assumption about how a price arrived), or, unless
// allowNegative, negative.
func checkQuotedPrice(op string, price float64, allowNegative bool) error {
	if math.IsNaN(price) || math.IsInf(price, 0) {
		return &core.APIError{Operation: op, Message: fmt.Sprintf("quote optimumPrice must be a finite number, got %v", price)}
	}
	if !allowNegative && price < 0 {
		return &core.APIError{Operation: op, Message: fmt.Sprintf("quote optimumPrice %.0f VND is negative", price)}
	}
	return nil
}
