package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/jsonresponse"
	"danny.vn/vngcloud/internal/transport"
)

// CreateProjectInput describes a one-month package. Quotes ignore Name,
// MaxPrice, and NoWait.
type CreateProjectInput struct {
	Region   string
	Name     string
	Type     string `vngcloud:"required"`
	QuotaGB  int64  `vngcloud:"required"`
	MaxPrice float64
	NoWait   bool
}

type ProjectPriceProperty struct {
	OptimumPrice    float64  `json:"optimumPrice"`
	MonthlyPrice    float64  `json:"monthlyPrice"`
	DiscountPercent *float64 `json:"discountPercent"`
	Name            *string  `json:"name"`
	Description     *string  `json:"description"`
}

type QuoteCreateProjectOutput struct {
	OptimumPrice    float64
	OriginalPrice   float64
	DiscountPrice   float64
	DiscountPercent *float64
	Properties      []ProjectPriceProperty
	MonthlyPrice    float64
	TotalPrice      float64
	Currency        string
}

// QuoteCreateProject prices a package without placing an order.
func (c *Client) QuoteCreateProject(ctx context.Context, in *CreateProjectInput) (*QuoteCreateProjectOutput, error) {
	return c.quoteCreateProject(ctx, in, nil)
}

// Renewal guards check the identities from the catalog used by this quote,
// avoiding a second catalog selection that could price a different purchase.
func (c *Client) quoteCreateProject(ctx context.Context, in *CreateProjectInput, expected *Project) (*QuoteCreateProjectOutput, error) {
	const op = "storage.QuoteCreateProject"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if in.QuotaGB <= 0 {
		return nil, fmt.Errorf("%w: %s: QuotaGB must be positive", core.ErrInvalidInput, op)
	}
	if err := c.validateProjectRegion(op, in.Region); err != nil {
		return nil, err
	}
	id, err := c.regionID(ctx, op, in.Region)
	if err != nil {
		return nil, err
	}
	catalog, err := c.readProjectCatalog(ctx, op, id)
	if err != nil {
		return nil, err
	}
	if expected != nil {
		typeKnown, purchaseKnown := false, false
		for _, typ := range catalog.types {
			if typ.ID == expected.ProjectType && typ.Name == expected.ProjectTypeName {
				typeKnown = true
			}
		}
		for _, purchase := range catalog.purchases {
			if purchase.ID == expected.PurchaseTypeID && purchase.Title == expected.PurchaseTypeName {
				purchaseKnown = true
			}
		}
		if !typeKnown || !purchaseKnown {
			return nil, projectResponseError(op, "project catalog identity is unknown or conflicting")
		}
	}
	spec, err := catalog.resolve(op, in.Type, in.QuotaGB)
	if err != nil {
		return nil, err
	}
	if expected != nil {
		monthly, identityErr := catalog.monthlyPurchase(op)
		if identityErr != nil {
			return nil, identityErr
		}
		if monthly == nil || spec.projectTypeID != expected.ProjectType || spec.purchaseTypeID != expected.PurchaseTypeID || monthly.Title != expected.PurchaseTypeName {
			return nil, fmt.Errorf("%w: %s: unsupported project purchase identity", core.ErrInvalidInput, op)
		}
	}
	return c.sendProjectQuote(ctx, op, id, spec)
}

type projectPriceBody struct {
	ResourceType string           `json:"resourceType"`
	Action       string           `json:"action"`
	ResourceInfo projectPriceInfo `json:"resourceInfo"`
}

type projectPriceInfo struct {
	Quota          int64 `json:"quota"`
	PurchaseTypeID int   `json:"purchaseTypeId"`
	ProjectType    int   `json:"projectType"`
}

func projectPriceRequest(spec projectPurchaseSpec) projectPriceBody {
	// Order-only fields make the price API return zero.
	return projectPriceBody{ResourceType: "object_storage", Action: "create", ResourceInfo: projectPriceInfo{Quota: spec.quota, PurchaseTypeID: spec.purchaseTypeID, ProjectType: spec.projectTypeID}}
}

func (c *Client) sendProjectQuote(ctx context.Context, op, id string, spec projectPurchaseSpec) (*QuoteCreateProjectOutput, error) {
	k := call{op: op, method: http.MethodPost, url: c.projectBillingRoute("v2", []string{"price"}, url.Values{"region_id": {id}}), regionID: id, body: projectPriceRequest(spec), ok: []int{http.StatusOK}}
	k.validate = func(raw json.RawMessage) error {
		if jsonresponse.Validate(raw) != nil {
			return projectResponseError(op, "quote response had no valid price")
		}
		return nil
	}
	env, err := c.exchangeProjectPrice(ctx, k)
	if err != nil {
		return nil, err
	}
	var price struct {
		OptimumPrice    *float64               `json:"optimumPrice"`
		OriginalPrice   float64                `json:"originalPrice"`
		DiscountPrice   float64                `json:"discountPrice"`
		DiscountPercent *float64               `json:"discountPercent"`
		Properties      []ProjectPriceProperty `json:"propertiesPrice"`
	}
	if json.Unmarshal(env.Data, &price) != nil || price.OptimumPrice == nil || math.IsNaN(*price.OptimumPrice) || math.IsInf(*price.OptimumPrice, 0) {
		return nil, projectResponseError(op, "quote response had no valid price")
	}
	if *price.OptimumPrice <= 0 {
		return nil, fmt.Errorf("%w: %s", core.ErrUnpriced, op)
	}
	return &QuoteCreateProjectOutput{OptimumPrice: *price.OptimumPrice, OriginalPrice: price.OriginalPrice, DiscountPrice: price.DiscountPrice, DiscountPercent: price.DiscountPercent, Properties: price.Properties, MonthlyPrice: *price.OptimumPrice, TotalPrice: *price.OptimumPrice, Currency: "VND"}, nil
}

// exchangeProjectPrice retries the read-only POST without changing write retries.
func (c *Client) exchangeProjectPrice(ctx context.Context, k call) (*envelope, error) {
	var credential string
	var raw json.RawMessage
	status, err := c.c.DoJSONStatus(ctx, transport.Request{Operation: k.op, Method: k.method, URL: k.url, Body: k.body, OK: k.ok, Idempotent: true, NoRedirect: true, SentCredential: &credential, Headers: map[string]string{"region": k.regionID, "region_id": k.regionID}}, &raw)
	if err != nil {
		var syn *json.SyntaxError
		if status > 0 && errors.As(err, &syn) {
			return nil, emptyResponse(k, status)
		}
		return nil, err
	}
	if k.validate != nil {
		if err := k.validate(raw); err != nil {
			return nil, err
		}
	}
	var env envelope
	if json.Unmarshal(raw, &env) != nil || env.Success == nil {
		return nil, emptyResponse(k, status)
	}
	if !*env.Success {
		return nil, c.envelopeError(k.op, status, &env, credential)
	}
	return &env, nil
}
