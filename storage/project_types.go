package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"strconv"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/routes"
)

// ProjectType holds catalog metadata and regional package prices.
type ProjectType struct {
	ID                int                       `json:"id"`
	Name              string                    `json:"name"`
	Title             string                    `json:"title"`
	Group             string                    `json:"group"`
	Status            int                       `json:"status"`
	StoragePolicy     string                    `json:"storagePolicy"`
	StorageClass      json.RawMessage           `json:"storageClass"`
	AllowPeriod       []int                     `json:"allowPeriod"`
	SKU               []string                  `json:"sku"`
	SKUMappings       json.RawMessage           `json:"skuMappings"`
	BillingUnitPrice  json.RawMessage           `json:"billingUnitPrice"`
	DescriptionEn     *string                   `json:"descriptionEn"`
	DescriptionVi     *string                   `json:"descriptionVi"`
	BillableResources []ProjectBillableResource `json:"billableResources"`
	Order             *int                      `json:"order"`
	IsDefault         *bool                     `json:"isDefault"`
	Offers            []ProjectOffer            `json:"offers"`
}

// ProjectBillableResource identifies a catalog billing option.
type ProjectBillableResource struct {
	ID             int    `json:"id"`
	Name           string `json:"name"`
	PriceKey       string `json:"priceKey"`
	PurchaseTypeID int    `json:"purchaseTypeId"`
	ProjectTypeID  int    `json:"projectTypeId"`
	Period         *int   `json:"period"`
}

// ProjectOffer prices the minimum package, not one GB of storage.
type ProjectOffer struct {
	PurchaseTypeID   int
	PurchaseTypeName string
	PriceKey         string
	Period           *int
	MinQuotaGB       int64
	MaxQuotaGB       int64
	StepQuotaGB      *int64
	QuotedQuotaGB    int64
	MonthlyPrice     float64
	Currency         string
}

type ListProjectTypesInput struct{ Region string }
type ListProjectTypesOutput = core.List[ProjectType]

type projectPurchaseType struct {
	ID            int     `json:"id"`
	Name          string  `json:"name"`
	Title         string  `json:"title"`
	Status        int     `json:"status"`
	DescriptionEn *string `json:"descriptionEn"`
	DescriptionVi *string `json:"descriptionVi"`
	Order         *int    `json:"order"`
	IsDefault     *bool   `json:"isDefault"`
}

type projectCatalog struct {
	types                           []ProjectType
	purchases                       []projectPurchaseType
	minQuota, maxQuota, maxProjects int64
	iamCheckout                     bool
}

// ListProjectTypes reads fresh catalog, configuration, and minimum-package prices.
func (c *Client) ListProjectTypes(ctx context.Context, in *ListProjectTypesInput) (*ListProjectTypesOutput, error) {
	const op = "storage.ListProjectTypes"
	region := ""
	if in != nil {
		region = in.Region
	}
	id, err := c.regionID(ctx, op, region)
	if err != nil {
		return nil, err
	}
	catalog, err := c.readProjectCatalog(ctx, op, id)
	if err != nil {
		return nil, err
	}
	monthly, err := catalog.monthlyPurchase(op)
	if err != nil {
		return nil, err
	}
	for i := range catalog.types {
		typ := &catalog.types[i]
		for _, resource := range typ.BillableResources {
			offer := ProjectOffer{PurchaseTypeID: resource.PurchaseTypeID, PriceKey: resource.PriceKey, Period: resource.Period, MinQuotaGB: catalog.minQuota, MaxQuotaGB: catalog.maxQuota}
			for _, purchase := range catalog.purchases {
				if purchase.ID == resource.PurchaseTypeID {
					offer.PurchaseTypeName = purchase.Title
				}
			}
			if monthly != nil && typ.Status == 1 && resource.PurchaseTypeID == monthly.ID && slices.Contains(typ.AllowPeriod, 1) {
				spec, err := catalog.resolve(op, typ.Name, catalog.minQuota)
				if err != nil {
					return nil, err
				}
				quote, err := c.sendProjectQuote(ctx, op, id, spec)
				if err != nil {
					return nil, err
				}
				offer.QuotedQuotaGB = catalog.minQuota
				offer.MonthlyPrice = quote.MonthlyPrice
				offer.Currency = quote.Currency
			}
			typ.Offers = append(typ.Offers, offer)
		}
	}
	return &ListProjectTypesOutput{Items: catalog.types}, nil
}

func (c *Client) projectBillingRoute(version string, parts []string, q url.Values) string {
	return c.c.RouteURL(routes.Route{Product: routes.ProductStorage, Version: "billing-api/" + version, Parts: parts, Query: q})
}

func requiredProjectList[T any](op string, env *envelope) ([]T, error) {
	raw := env.Datas
	if len(raw) == 0 || string(raw) == "null" {
		raw = env.Data
	}
	if len(raw) == 0 || string(raw) == "null" {
		return nil, projectResponseError(op, "missing catalog data")
	}
	return decodeList[T](op, 200, env)
}

func projectResponseError(op, message string) error {
	return &core.APIError{Operation: op, StatusCode: 200, Message: message}
}

func (c *Client) readProjectCatalog(ctx context.Context, op, id string) (*projectCatalog, error) {
	env, err := c.do(ctx, op, c.route([]string{"billing", "project_types"}, nil), id)
	if err != nil {
		return nil, err
	}
	types, err := requiredProjectList[ProjectType](op, env)
	if err != nil {
		return nil, err
	}
	for i := range types {
		var class struct {
			StoragePolicy string `json:"storagePolicy"`
		}
		if len(types[i].StorageClass) > 0 && json.Unmarshal(types[i].StorageClass, &class) != nil {
			return nil, projectResponseError(op, "invalid storage class")
		}
		types[i].StoragePolicy = class.StoragePolicy
	}
	env, err = c.do(ctx, op, c.route([]string{"billing", "purchase_types"}, nil), id)
	if err != nil {
		return nil, err
	}
	purchases, err := requiredProjectList[projectPurchaseType](op, env)
	if err != nil {
		return nil, err
	}
	catalog := &projectCatalog{types: types, purchases: purchases}
	for _, item := range []struct {
		key    string
		target *int64
	}{
		{"vos_billing_normal_min_quota", &catalog.minQuota},
		{"vos_billing_normal_max_quota", &catalog.maxQuota},
		{"max_project_per_user_per_region", &catalog.maxProjects},
	} {
		value, err := c.projectConfiguration(ctx, op, id, item.key)
		if err != nil {
			return nil, err
		}
		// Accept decimal digits only; signs and whitespace are not configuration integers.
		for _, digit := range value {
			if digit < '0' || digit > '9' {
				return nil, projectResponseError(op, "invalid configuration "+item.key)
			}
		}
		n, err := strconv.ParseInt(value, 10, 64)
		if err != nil || n <= 0 {
			return nil, projectResponseError(op, "invalid configuration "+item.key)
		}
		*item.target = n
	}
	if catalog.minQuota > catalog.maxQuota {
		return nil, projectResponseError(op, "inconsistent quota limits")
	}
	value, err := c.projectConfiguration(ctx, op, id, "enable_iam_checkout")
	if err != nil {
		return nil, err
	}
	switch value {
	case "true":
		catalog.iamCheckout = true
	case "false":
	default:
		return nil, projectResponseError(op, "invalid configuration enable_iam_checkout")
	}
	return catalog, nil
}

func (c *Client) projectConfiguration(ctx context.Context, op, id, key string) (string, error) {
	q := url.Values{"keys": {key}, "region_id": {id}}
	env, err := c.do(ctx, op, c.projectBillingRoute("v1", []string{"configurations"}, q), id)
	if err != nil {
		return "", err
	}
	type configuration struct {
		Key   string  `json:"key"`
		Value *string `json:"value"`
	}
	items, err := requiredProjectList[configuration](op, env)
	if err != nil {
		return "", err
	}
	if len(items) != 1 || items[0].Key != key || items[0].Value == nil {
		return "", projectResponseError(op, "missing or duplicate configuration "+key)
	}
	return *items[0].Value, nil
}

func (catalog *projectCatalog) monthlyPurchase(op string) (*projectPurchaseType, error) {
	var selected *projectPurchaseType
	for i := range catalog.purchases {
		p := &catalog.purchases[i]
		if p.Status != 1 || p.Name != "Normal" || p.Title != "Pay monthly" {
			continue
		}
		if selected != nil || p.ID != 4 {
			return nil, fmt.Errorf("%w: %s: ambiguous or unsupported monthly purchase", core.ErrInvalidInput, op)
		}
		selected = p
	}
	return selected, nil
}

type projectPurchaseSpec struct {
	quota                         int64
	purchaseTypeID, projectTypeID int
}

func (catalog *projectCatalog) resolve(op, name string, quota int64) (projectPurchaseSpec, error) {
	invalid := func() (projectPurchaseSpec, error) {
		return projectPurchaseSpec{}, fmt.Errorf("%w: %s: unavailable or ambiguous project type, purchase, or quota", core.ErrInvalidInput, op)
	}
	if quota < catalog.minQuota || quota > catalog.maxQuota {
		return invalid()
	}
	var selected *ProjectType
	for i := range catalog.types {
		typ := &catalog.types[i]
		if typ.Name != name {
			continue
		}
		if selected != nil {
			return invalid()
		}
		selected = typ
	}
	if selected == nil || selected.ID <= 0 || selected.Status != 1 || !slices.Contains(selected.AllowPeriod, 1) {
		return invalid()
	}
	monthly, err := catalog.monthlyPurchase(op)
	if err != nil {
		return projectPurchaseSpec{}, err
	}
	if monthly == nil {
		return invalid()
	}
	count := 0
	for _, r := range selected.BillableResources {
		if r.PurchaseTypeID == monthly.ID {
			if r.ProjectTypeID != selected.ID || (r.Period != nil && *r.Period != 1) {
				return invalid()
			}
			count++
		}
	}
	if count != 1 {
		return invalid()
	}
	return projectPurchaseSpec{quota: quota, purchaseTypeID: monthly.ID, projectTypeID: selected.ID}, nil
}
