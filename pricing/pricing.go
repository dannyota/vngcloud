// Package pricing prices a resource before it is created, on the regional
// billing gateway. GetQuote changes nothing and places no order.
package pricing

import (
	"context"
	"fmt"
	"math"
	"net/http"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/routes"
	"danny.vn/vngcloud/internal/transport"
)

// Verified resource types for GetQuote. Other types work through the string.
const (
	ResourceSnapshot     = "snapshot"
	ResourcePublicVIP    = "public-vip"
	ResourceServer       = "server"
	ResourceVolume       = "volume"
	ResourceLoadBalancer = "load-balancer"
)

// GetQuoteInput.Action values. ActionCreate is also GetQuoteInput's default
// when Action is left empty.
const (
	ActionCreate = "create"
	ActionResize = "resize"
)

// Client is the pricing service client.
type Client struct {
	c *core.Client
}

// New builds a Client from cfg. A Client built from the same Config as
// another service client shares its login and token cache.
func New(cfg vngcloud.Config) *Client {
	return &Client{c: core.ClientOf(cfg)}
}

// GetQuoteInput asks what ResourceType would cost to create or resize.
type GetQuoteInput struct {
	// ResourceType is, for example, ResourceSnapshot or ResourcePublicVIP.
	ResourceType string `vngcloud:"required"`
	// Action is ActionCreate or ActionResize. Empty sends ActionCreate, so
	// existing callers that never set it are unchanged.
	Action string
	// ResourceInfo describes the resource in the create call's own shape.
	// Nil sends no resourceInfo key.
	ResourceInfo map[string]any
}

// GetQuoteOutput is the account's current price for the requested resource.
type GetQuoteOutput struct {
	OptimumPrice    float64
	OriginalPrice   float64
	DiscountPrice   float64
	DiscountPercent float64
	Properties      []PriceProperty
}

// PriceProperty is one line item of a quote's propertiesPrice list.
type PriceProperty struct {
	Name            string
	Description     string
	OptimumPrice    float64
	MonthlyPrice    float64
	CurrentPrice    *float64
	DiscountPercent float64
}

// quoteBody is GetQuote's request body. Action is GetQuoteInput's own
// Action, or ActionCreate when it is empty. ResourceInfo uses omitempty so a
// nil map sends no resourceInfo key.
type quoteBody struct {
	ResourceType string         `json:"resourceType"`
	Action       string         `json:"action"`
	ResourceInfo map[string]any `json:"resourceInfo,omitempty"`
}

// quoteResponse is GetQuote's wire response. It is not enveloped: the price
// fields sit at the top level. artifactPrices is left out until a capture
// shows it filled. OptimumPrice is a pointer: a response that omits it, or
// sends it null, must not silently decode as a free price and let a paid
// write's guard (quoted > MaxPrice) compare a real budget against 0.
type quoteResponse struct {
	OptimumPrice    *float64            `json:"optimumPrice"`
	OriginalPrice   float64             `json:"originalPrice"`
	DiscountPrice   float64             `json:"discountPrice"`
	DiscountPercent float64             `json:"discountPercent"`
	PropertiesPrice []quotePropertyWire `json:"propertiesPrice"`
}

type quotePropertyWire struct {
	Name            string   `json:"name"`
	Description     string   `json:"description"`
	OptimumPrice    float64  `json:"optimumPrice"`
	MonthlyPrice    float64  `json:"monthlyPrice"`
	CurrentPrice    *float64 `json:"currentPrice"`
	DiscountPercent float64  `json:"discountPercent"`
}

// GetQuote prices ResourceType as ResourceInfo describes it. It is a read
// sent over POST, so it sets Idempotent explicitly and is retried after a
// failure that may have already reached the server.
func (c *Client) GetQuote(ctx context.Context, in *GetQuoteInput) (*GetQuoteOutput, error) {
	const op = "pricing.GetQuote"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}

	action := in.Action
	if action == "" {
		action = ActionCreate
	}

	var resp quoteResponse
	req := transport.Request{
		Operation: op,
		Method:    http.MethodPost,
		URL:       c.c.RouteURL(routes.Route{Product: routes.ProductPortal, Version: "v1", Parts: []string{"price"}}),
		Body: quoteBody{
			ResourceType: in.ResourceType,
			Action:       action,
			ResourceInfo: in.ResourceInfo,
		},
		OK:         []int{200},
		Idempotent: true,
	}
	if err := c.c.DoJSON(ctx, req, &resp); err != nil {
		return nil, err
	}

	price, err := validQuotePrice(op, resp.OptimumPrice)
	if err != nil {
		return nil, err
	}

	properties := make([]PriceProperty, len(resp.PropertiesPrice))
	for i, p := range resp.PropertiesPrice {
		properties[i] = PriceProperty(p)
	}
	return &GetQuoteOutput{
		OptimumPrice:    price,
		OriginalPrice:   resp.OriginalPrice,
		DiscountPrice:   resp.DiscountPrice,
		DiscountPercent: resp.DiscountPercent,
		Properties:      properties,
	}, nil
}

// validQuotePrice returns price's value, refusing every shape a paid
// write's guard (quoted > MaxPrice) cannot compare safely. price is nil
// when the response omitted optimumPrice or sent it null, which an
// otherwise successful HTTP 200 gives for an error shape (such as a
// billing-style envelope) rather than a priced quote; decoding that as a
// price would silently return 0 and let a write guarded by the default
// MaxPrice of 0 through unpriced. A negative price would compare below any
// non-negative MaxPrice and pass the same guard; NaN and an infinite price
// can never arrive through a valid JSON number (encoding/json rejects both
// the bare literal and a value that overflows float64 while decoding), but
// are still refused here as defense in depth, the same values
// core.CheckMaxPrice refuses for the caller's own MaxPrice.
func validQuotePrice(op string, price *float64) (float64, error) {
	if price == nil {
		return 0, &core.APIError{Operation: op, Message: "quote response had no price"}
	}
	if math.IsNaN(*price) || math.IsInf(*price, 0) || *price < 0 {
		return 0, &core.APIError{Operation: op, Message: fmt.Sprintf("quote response had an invalid price: %v", *price)}
	}
	return *price, nil
}
