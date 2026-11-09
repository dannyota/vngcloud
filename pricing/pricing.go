// Package pricing prices a create or resize on the regional
// billing gateway. GetQuote changes nothing and places no order.
package pricing

import (
	"context"
	"encoding/json"
	"errors"
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
	// ResourceSnapshot is the "snapshot" quote resource type.
	ResourceSnapshot = "snapshot"
	// ResourcePublicVIP is the "public-vip" quote resource type.
	ResourcePublicVIP = "public-vip"
	// ResourceServer is the "server" quote resource type.
	ResourceServer = "server"
	// ResourceVolume is the "volume" quote resource type.
	ResourceVolume = "volume"
	// ResourceLoadBalancer is the "load-balancer" quote resource type.
	ResourceLoadBalancer = "load-balancer"
)

// GetQuoteInput.Action values. ActionCreate is also GetQuoteInput's default
// when Action is left empty.
const (
	// ActionCreate is the "create" quote action.
	ActionCreate = "create"
	// ActionResize is the "resize" quote action.
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

// GetQuoteOutput is the account's current quote, recorded in
// docs/design/vserver-paid-writes-api.md. A resize OptimumPrice is signed:
// a negative value is an expected credit, not confirmation of a refund.
type GetQuoteOutput struct {
	OptimumPrice    float64
	OriginalPrice   float64
	DiscountPrice   float64
	DiscountPercent float64
	Properties      []PriceProperty
}

// PriceProperty is a quote line item; its wire fields are recorded in
// docs/design/vserver-paid-writes-api.md.
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

	var raw json.RawMessage
	var sentCredentials []string
	req := transport.Request{
		Operation: op,
		Method:    http.MethodPost,
		URL:       c.c.RouteURL(routes.Route{Product: routes.ProductPortal, Version: "v1", Parts: []string{"price"}}),
		Body: quoteBody{
			ResourceType: in.ResourceType,
			Action:       action,
			ResourceInfo: in.ResourceInfo,
		},
		OK:              []int{200},
		Idempotent:      true,
		MaxBody:         1 << 20,
		Sensitive:       true,
		SentCredentials: &sentCredentials,
	}
	if err := c.c.DoJSON(ctx, req, &raw); err != nil {
		if errors.Is(err, transport.ErrBodyTooLarge) {
			return nil, &core.APIError{Operation: op, Message: "quote response exceeds 1 MiB", Err: err}
		}
		return nil, err
	}
	resp, err := decodeQuoteResponse(op, raw, sentCredentials)
	if err != nil {
		return nil, err
	}

	price, err := validQuotePrice(op, action, resp.OptimumPrice)
	if err != nil {
		return nil, err
	}

	properties := make([]PriceProperty, len(resp.PropertiesPrice))
	for i, p := range resp.PropertiesPrice {
		properties[i] = PriceProperty(p)
		properties[i].Name = transport.RedactErrorText(p.Name, sentCredentials...)
		properties[i].Description = transport.RedactErrorText(p.Description, sentCredentials...)
	}
	return &GetQuoteOutput{
		OptimumPrice:    price,
		OriginalPrice:   resp.OriginalPrice,
		DiscountPrice:   resp.DiscountPrice,
		DiscountPercent: resp.DiscountPercent,
		Properties:      properties,
	}, nil
}

// validQuotePrice refuses absent and nonfinite prices. Only exact resize
// actions may return a credit; empty actions become create before this check.
func validQuotePrice(op, action string, price *float64) (float64, error) {
	if price == nil {
		return 0, &core.APIError{Operation: op, Message: "quote response had no price"}
	}
	if math.IsNaN(*price) || math.IsInf(*price, 0) || (*price < 0 && action != ActionResize) {
		return 0, &core.APIError{Operation: op, Message: fmt.Sprintf("quote response had an invalid price: %v", *price)}
	}
	return *price, nil
}
