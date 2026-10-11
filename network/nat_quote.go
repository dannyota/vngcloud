package network

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/transport"
)

type natResourceInfo struct {
	IsPoc             bool   `json:"isPoc"`
	IsEnableAutoRenew bool   `json:"isEnableAutoRenew"`
	IsBuyMorePoc      bool   `json:"isBuyMorePoc"`
	NATName           string `json:"natName"`
	PackageUUID       string `json:"packageUuid"`
	VPCUUID           string `json:"vpcUuid"`
	RegionUUID        string `json:"regionUuid"`
	ProjectUUID       string `json:"projectUuid"`
}
type natPriceBody struct {
	ResourceType string          `json:"resourceType"`
	Action       string          `json:"action"`
	ResourceInfo natResourceInfo `json:"resourceInfo"`
}

func natPriceRequest(s *natPurchaseSpec) natPriceBody {
	return natPriceBody{ResourceType: "nat", Action: "create", ResourceInfo: s.info}
}

// QuoteCreateNATInstance performs the placement and V3 guards without placing an order.
// MaxPrice does not cap quotes, but must have a finite nonnegative shape.
func (c *Client) QuoteCreateNATInstance(ctx context.Context, in *CreateNATInstanceInput) (*NetworkQuoteOutput, error) {
	const op = "network.QuoteCreateNATInstance"
	s, err := c.resolveNATPurchase(ctx, op, in)
	if err != nil {
		return nil, err
	}
	return c.natQuote(ctx, op, s)
}
func (c *Client) natQuote(ctx context.Context, op string, s *natPurchaseSpec) (*NetworkQuoteOutput, error) {
	code := 0
	env, err := c.natExchange(ctx, transport.Request{Operation: op, Method: http.MethodPost, URL: s.scope.route("vnetwork/billing/v1", []string{"price"}, nil), Body: natPriceRequest(s), Idempotent: true, OK: []int{200}}, &code)
	if err != nil {
		return nil, err
	}
	var price struct {
		OptimumPrice    *float64               `json:"optimumPrice"`
		OriginalPrice   *float64               `json:"originalPrice"`
		DiscountPrice   *float64               `json:"discountPrice"`
		DiscountPercent *float64               `json:"discountPercent"`
		Properties      []NetworkPriceProperty `json:"propertiesPrice"`
	}
	if decodeNATEnvelope(env.Data, &price, reflect.TypeFor[struct{}]()) != nil || price.OptimumPrice == nil || price.OriginalPrice == nil || price.DiscountPrice == nil || price.Properties == nil {
		return nil, natInvalid(op)
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(env.Data, &fields) != nil || len(fields["discountPercent"]) == 0 {
		return nil, natInvalid(op)
	}
	props, err := natDecodeRows[NetworkPriceProperty](op, fields["propertiesPrice"], "optimumPrice", "monthlyPrice", "name", "description")
	if err != nil {
		return nil, err
	}
	var properties []map[string]json.RawMessage
	if json.Unmarshal(fields["propertiesPrice"], &properties) != nil {
		return nil, natInvalid(op)
	}
	for _, property := range properties {
		if len(property["discountPercent"]) == 0 || len(property["currentPrice"]) == 0 {
			return nil, natInvalid(op)
		}
	}
	if *price.OptimumPrice <= 0 {
		return nil, fmt.Errorf("%w: %s", core.ErrUnpriced, op)
	}
	return &NetworkQuoteOutput{OptimumPrice: *price.OptimumPrice, OriginalPrice: *price.OriginalPrice, DiscountPrice: *price.DiscountPrice, DiscountPercent: price.DiscountPercent, Properties: props, MonthlyPrice: *price.OptimumPrice, TotalPrice: *price.OptimumPrice, Currency: "VND"}, nil
}
