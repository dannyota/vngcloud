package storage

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/discovery"
	"danny.vn/vngcloud/internal/jsonresponse"
	"danny.vn/vngcloud/internal/pricecatalog"
	"danny.vn/vngcloud/internal/routes"
)

// ProjectType holds catalog metadata and regional package prices.
// See docs/design/storage-projects-api.md.
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
// See docs/design/storage-projects-api.md.
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

// ListProjectTypesInput selects parameters for ListProjectTypes.
type ListProjectTypesInput struct {
	// Region selects the storage region using normal region resolution.
	Region string
}

// ListProjectTypesOutput holds ProjectType items.
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
	if err := c.validateProjectRegion(op, region); err != nil {
		return nil, err
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

func (c *Client) validateProjectRegion(op, name string) error {
	return pricecatalog.ValidateStorageRegion(op, c.c.Region(), name)
}

func (c *Client) projectBillingRoute(version string, parts []string, q url.Values) string {
	return c.c.RouteURL(routes.Route{Product: routes.ProductStorage, Version: "billing-api/" + version, Parts: parts, Query: q})
}

func requiredProjectList[T any](op string, env *envelope) ([]T, error) {
	raw, err := completeProjectList(op, env)
	if err != nil {
		return nil, err
	}
	items := make([]T, 0, len(raw))
	for _, item := range raw {
		var value T
		if json.Unmarshal(item, &value) != nil {
			return nil, projectResponseError(op, "malformed catalog data")
		}
		items = append(items, value)
	}
	return items, nil
}

func projectResponseError(op, message string) error {
	return &core.APIError{Operation: op, StatusCode: 200, Message: message}
}

func (c *Client) readProjectCatalog(ctx context.Context, op, id string, sessions ...*discovery.Session) (*projectCatalog, error) {
	var session *discovery.Session
	if len(sessions) > 0 {
		session = sessions[0]
	}
	k := call{
		session: session,
		op:      op, method: http.MethodGet, regionID: id, ok: []int{http.StatusOK},
		url: c.route([]string{"billing", "project_types"}, nil),
		validate: func(raw json.RawMessage) error {
			if jsonresponse.Validate(raw) != nil {
				return projectResponseError(op, "malformed catalog data")
			}
			return nil
		},
	}
	env, err := c.exchange(ctx, k)
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
	k.url = c.route([]string{"billing", "purchase_types"}, nil)
	env, err = c.exchange(ctx, k)
	if err != nil {
		return nil, err
	}
	purchases, err := requiredProjectList[projectPurchaseType](op, env)
	if err != nil {
		return nil, err
	}
	catalog := &projectCatalog{types: types, purchases: purchases}
	if err := catalog.validateIdentities(op); err != nil {
		return nil, err
	}
	for _, item := range []struct {
		key    string
		target *int64
	}{
		{"vos_billing_normal_min_quota", &catalog.minQuota},
		{"vos_billing_normal_max_quota", &catalog.maxQuota},
		{"max_project_per_user_per_region", &catalog.maxProjects},
	} {
		value, err := c.projectConfiguration(ctx, op, id, item.key, session)
		if err != nil {
			return nil, err
		}
		n, err := pricecatalog.ConfigurationInteger(op, item.key, value)
		if err != nil {
			return nil, err
		}
		*item.target = n
	}
	if catalog.minQuota > catalog.maxQuota {
		return nil, projectResponseError(op, "inconsistent quota limits")
	}
	value, err := c.projectConfiguration(ctx, op, id, "enable_iam_checkout", session)
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

func (c *Client) projectConfiguration(ctx context.Context, op, id, key string, sessions ...*discovery.Session) (string, error) {
	var session *discovery.Session
	if len(sessions) > 0 {
		session = sessions[0]
	}
	q := url.Values{"keys": {key}, "region_id": {id}}
	env, err := c.exchange(ctx, call{
		session: session,
		op:      op, method: http.MethodGet, regionID: id, ok: []int{http.StatusOK},
		url: c.projectBillingRoute("v1", []string{"configurations"}, q),
		validate: func(raw json.RawMessage) error {
			if jsonresponse.Validate(raw) != nil {
				return projectResponseError(op, "malformed catalog data")
			}
			return nil
		},
	})
	if err != nil {
		return "", err
	}
	items, err := completeProjectList(op, env)
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(items)
	if err != nil {
		return "", projectResponseError(op, "malformed catalog data")
	}
	return pricecatalog.ConfigurationValue(op, key, raw)
}

func (catalog *projectCatalog) selection() *pricecatalog.StorageCatalog {
	values := &pricecatalog.StorageCatalog{Types: make([]pricecatalog.StorageType, 0, len(catalog.types)), Purchases: make([]pricecatalog.StoragePurchase, 0, len(catalog.purchases)), MinQuota: catalog.minQuota, MaxQuota: catalog.maxQuota, MaxProjects: catalog.maxProjects, IAMCheckout: catalog.iamCheckout}
	for _, typ := range catalog.types {
		resources := make([]pricecatalog.StorageResource, 0, len(typ.BillableResources))
		for _, r := range typ.BillableResources {
			resources = append(resources, pricecatalog.StorageResource(r))
		}
		values.Types = append(values.Types, pricecatalog.StorageType{ID: typ.ID, Name: typ.Name, Title: typ.Title, Status: typ.Status, AllowPeriod: typ.AllowPeriod, BillableResources: resources})
	}
	for _, p := range catalog.purchases {
		values.Purchases = append(values.Purchases, pricecatalog.StoragePurchase{ID: p.ID, Name: p.Name, Title: p.Title, Status: p.Status})
	}
	return values
}

func (catalog *projectCatalog) validateIdentities(op string) error {
	return catalog.selection().ValidateIdentities(op)
}

func (catalog *projectCatalog) monthlyPurchase(op string) (*projectPurchaseType, error) {
	monthly, err := catalog.selection().MonthlyPurchase(op)
	if err != nil || monthly == nil {
		return nil, err
	}
	for i := range catalog.purchases {
		if catalog.purchases[i].ID == monthly.ID && catalog.purchases[i].Status == 1 {
			return &catalog.purchases[i], nil
		}
	}
	return nil, nil
}

type projectPurchaseSpec struct {
	quota                         int64
	purchaseTypeID, projectTypeID int
}

func (catalog *projectCatalog) resolve(op, name string, quota int64) (projectPurchaseSpec, error) {
	spec, err := catalog.selection().Resolve(op, name, quota)
	return projectPurchaseSpec{quota: spec.Quota, purchaseTypeID: spec.PurchaseTypeID, projectTypeID: spec.ProjectTypeID}, err
}
