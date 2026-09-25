// Package pricing prices a resource before it is created, on the regional
// billing gateway. GetQuote changes nothing and places no order.
package pricing

import (
	"context"
	"encoding/json"
	"net/http"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/routes"
	"danny.vn/vngcloud/internal/transport"
)

// Verified resource types for GetQuote. Other types work through the string.
const (
	ResourceSnapshot  = "snapshot"
	ResourcePublicVIP = "public-vip"
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

// GetQuoteInput asks what ResourceType would cost to create.
type GetQuoteInput struct {
	// ResourceType is, for example, ResourceSnapshot or ResourcePublicVIP.
	ResourceType string `vngcloud:"required"`
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

// quoteBody is GetQuote's request body. The SDK always sends action
// "create". ResourceInfo uses omitempty so a nil map sends no resourceInfo
// key.
type quoteBody struct {
	ResourceType string         `json:"resourceType"`
	Action       string         `json:"action"`
	ResourceInfo map[string]any `json:"resourceInfo,omitempty"`
}

// quoteResponse is GetQuote's wire response. It is not enveloped: the price
// fields sit at the top level. artifactPrices is left out until a capture
// shows it filled.
type quoteResponse struct {
	OptimumPrice    float64             `json:"optimumPrice"`
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

	var raw json.RawMessage
	req := transport.Request{
		Operation: op,
		Method:    http.MethodPost,
		URL:       c.c.RouteURL(routes.Route{Product: routes.ProductPortal, Version: "v1", Parts: []string{"price"}}),
		Body: quoteBody{
			ResourceType: in.ResourceType,
			Action:       "create",
			ResourceInfo: in.ResourceInfo,
		},
		OK:         []int{200},
		Idempotent: true,
	}
	if err := c.c.DoJSON(ctx, req, &raw); err != nil {
		return nil, err
	}

	// A quote response always carries optimumPrice. Its absence, on an
	// otherwise successful HTTP 200, means the body is an error shape (such
	// as a billing-style envelope) rather than a priced quote; decoding it
	// as one would silently return a zero-value price.
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil, &core.APIError{Operation: op, Err: err}
	}
	if _, ok := probe["optimumPrice"]; !ok {
		return nil, &core.APIError{Operation: op, Message: "quote response had no price"}
	}

	var resp quoteResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, &core.APIError{Operation: op, Err: err}
	}

	properties := make([]PriceProperty, len(resp.PropertiesPrice))
	for i, p := range resp.PropertiesPrice {
		properties[i] = PriceProperty(p)
	}
	return &GetQuoteOutput{
		OptimumPrice:    resp.OptimumPrice,
		OriginalPrice:   resp.OriginalPrice,
		DiscountPrice:   resp.DiscountPrice,
		DiscountPercent: resp.DiscountPercent,
		Properties:      properties,
	}, nil
}
