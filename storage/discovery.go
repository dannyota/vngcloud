package storage

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/discovery"
	"danny.vn/vngcloud/internal/pricecatalog"
	"danny.vn/vngcloud/internal/pricequote"
	"danny.vn/vngcloud/internal/transport"
	"danny.vn/vngcloud/pricing"
)

// ListStoragePricesInput selects parameters for ListStoragePrices.
type ListStoragePricesInput struct {
	// Region selects the storage region using the normal region resolution.
	Region string
	// Type filters by an exact, case-sensitive type name when nonempty.
	Type string
	// QuotaGB sets the quota in GB; zero selects the catalog minimum.
	QuotaGB int64
	// MaxRequests limits physical requests; zero selects 500 and the maximum is 2000.
	MaxRequests int
	// AllowPartial retains partial results and reports incomplete discovery when set.
	AllowPartial bool
}

// ListStoragePricesOutput holds the result of ListStoragePrices.
type ListStoragePricesOutput struct {
	Items    []StoragePrice
	Metadata pricing.DiscoveryMetadata
}

// StoragePrice holds a storage offering and its discovered price.
type StoragePrice struct {
	Region         string
	Type           string
	PurchaseType   string
	PriceKey       string
	TypeID         int
	PurchaseTypeID int
	QuotaGB        int64
	MinQuotaGB     int64
	MaxQuotaGB     int64
	AllowPeriod    []int
	PricePerGB     *float64
	Price          pricing.DiscoveryPrice
}

func storageDiscoveryError(op string, status int) error {
	return &core.APIError{Operation: op, StatusCode: status, Code: "InvalidResponse", Message: "invalid discovery response; body withheld"}
}

func (c *Client) exchangeDiscovery(k call) (*envelope, error) {
	req := transport.Request{Operation: k.op, Method: k.method, URL: k.url, Body: k.body, OK: k.ok}
	if k.regionID != "" {
		req.Headers = pricecatalog.StorageHeaders(k.regionID)
	}
	return pricecatalog.StorageExchange(c.c, k.session, req, k.validate)
}

func (c *Client) discoveryRegion(s *discovery.Session, name string) (string, string, error) {
	const op = "storage.ListStoragePrices"
	name = pricecatalog.StorageRegionName(c.c.Region(), name)
	c.regionMu.Lock()
	defer c.regionMu.Unlock()
	if err := s.Context().Err(); err != nil {
		return "", "", err
	}
	if id := c.regionIDs[name]; id != "" {
		return id, name, nil
	}
	env, err := c.exchange(s.Context(), call{session: s, op: "storage.ListRegions", method: http.MethodGet, url: c.route([]string{"regions"}, nil), ok: []int{200}})
	if err != nil {
		return "", "", err
	}
	regions, err := requiredProjectList[Region](op, env)
	if err != nil {
		return "", "", err
	}
	values := make([]pricecatalog.StorageRegion, 0, len(regions))
	for _, r := range regions {
		values = append(values, pricecatalog.StorageRegion{ID: r.ID, Name: r.Name})
	}
	ids, err := pricecatalog.StorageRegionIDs(op, values)
	if err != nil {
		return "", "", err
	}
	if err := s.Context().Err(); err != nil {
		return "", "", err
	}
	if ids[name] == "" {
		return "", "", fmt.Errorf("%w: unknown storage region", core.ErrInvalidInput)
	}
	c.regionIDs = ids
	return ids[name], name, nil
}

// ListStoragePrices reads monthly package prices without placing an order.
// PricePerGB is the effective rate at the quoted quota, not a marginal tariff.
func (c *Client) ListStoragePrices(ctx context.Context, in *ListStoragePricesInput) (*ListStoragePricesOutput, error) {
	const op = "storage.ListStoragePrices"
	if in == nil {
		in = &ListStoragePricesInput{}
	}
	if in.QuotaGB < 0 {
		return nil, fmt.Errorf("%w: QuotaGB must be nonnegative", core.ErrInvalidInput)
	}
	if err := c.validateProjectRegion(op, in.Region); err != nil {
		return nil, err
	}
	s, err := discovery.New(ctx, in.MaxRequests)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", core.ErrInvalidInput, err)
	}
	defer s.Close()
	id, region, err := c.discoveryRegion(s, in.Region)
	if err != nil {
		return nil, err
	}
	catalog, err := c.readProjectCatalog(s.Context(), op, id, s)
	if err != nil {
		if errors.Is(err, core.ErrInvalidInput) {
			return nil, storageDiscoveryError(op, 200)
		}
		return nil, err
	}
	if err := validateStorageDiscoveryCatalog(catalog); err != nil {
		return nil, err
	}
	monthly, err := catalog.monthlyPurchase(op)
	if err != nil {
		return nil, err
	}
	out := &ListStoragePricesOutput{Items: []StoragePrice{}, Metadata: pricing.DiscoveryMetadata{Region: region, Complete: true, Failures: []pricing.DiscoveryFailure{}, Notes: pricing.DiscoveryNotes()}}
	out.Metadata.Notes = slices.DeleteFunc(out.Metadata.Notes, func(n pricing.PricingNote) bool { return n.Scope == "compute" || n.Scope == "network" })
	out.Metadata.Notes = append(out.Metadata.Notes, pricing.PricingNote{Topic: "excluded-purchase", Scope: region, Text: "PAYG, egress, and request charges are excluded from monthly quota quotes.", Evidence: "observed"})
	var errs []error
	record := func(key string, err error) {
		errs = append(errs, err)
		out.Metadata.Complete = false
		f := pricing.DiscoveryFailure{Operation: op, ItemKey: key, Code: "DiscoveryIncomplete"}
		var api *core.APIError
		if errors.As(err, &api) {
			f.Operation = api.Operation
			f.Status = api.StatusCode
			f.Code = core.ResolvedCode(api.StatusCode, "")
			if api.Code == "InvalidResponse" {
				f.Code = api.Code
			}
		}
		out.Metadata.Failures = append(out.Metadata.Failures, f)
	}
	for _, p := range catalog.purchases {
		if monthly == nil || p.ID != monthly.ID {
			out.Metadata.Notes = append(out.Metadata.Notes, pricing.PricingNote{Topic: "excluded-purchase", Scope: region, Text: "Non-monthly and unknown purchase types, including PAYG, are not quoted.", Evidence: "observed"})
		}
	}
	if monthly != nil {
		for _, typ := range catalog.types {
			if typ.Status != 1 || !slices.Contains(typ.AllowPeriod, 1) || (in.Type != "" && typ.Name != in.Type) {
				continue
			}
			quota := in.QuotaGB
			if quota == 0 {
				quota = catalog.minQuota
			}
			row := StoragePrice{Region: region, Type: typ.Name, TypeID: typ.ID, PurchaseType: monthly.Title, PurchaseTypeID: monthly.ID, QuotaGB: quota, MinQuotaGB: catalog.minQuota, MaxQuotaGB: catalog.maxQuota, AllowPeriod: append([]int{}, typ.AllowPeriod...), Price: pricing.DiscoveryPrice{Status: "unknown", Source: "storage.QuoteCreateProject"}}
			count := 0
			conflicting := false
			for _, r := range typ.BillableResources {
				if r.PurchaseTypeID == monthly.ID {
					if r.ProjectTypeID != typ.ID || (r.Period != nil && *r.Period != 1) {
						conflicting = true
					}
					row.PriceKey = r.PriceKey
					count++
				}
			}
			if count != 1 || conflicting {
				err := storageDiscoveryError(op, 200)
				if !in.AllowPartial {
					return nil, err
				}
				record(region+"/"+typ.Name, err)
				continue
			}
			if quota < catalog.minQuota || quota > catalog.maxQuota {
				row.Price.Status = "inapplicable"
				out.Metadata.Notes = append(out.Metadata.Notes, pricing.PricingNote{Topic: "inapplicable", Scope: typ.Name, Text: "The requested quota is outside the offering's bounds.", Evidence: "observed"})
			}
			if err := s.AddRows(1); err != nil {
				if !in.AllowPartial || len(out.Items) == 0 {
					return nil, err
				}
				record("rows", err)
				return finishStorageDiscovery(out, s, errs)
			}
			out.Items = append(out.Items, row)
		}
	}
	needed := 0
	for _, row := range out.Items {
		if row.Price.Status == "unknown" {
			needed++
		}
	}
	if err := s.Preflight(needed); err != nil {
		if !in.AllowPartial || len(out.Items) == 0 {
			return nil, err
		}
		record("quotes", err)
	} else {
		for i := range out.Items {
			row := &out.Items[i]
			if row.Price.Status != "unknown" {
				continue
			}
			spec := projectPurchaseSpec{quota: row.QuotaGB, projectTypeID: row.TypeID, purchaseTypeID: row.PurchaseTypeID}
			var price pricing.DiscoveryPrice
			req := pricecatalog.StorageQuote(c.c, "storage.QuoteCreateProject", id, projectPriceRequest(spec))
			_, err := s.DoJSON(c.c, req, nil, func(status int, raw json.RawMessage) error {
				var e error
				price, e = c.decodeStorageDiscoveryPrice(req.Operation, status, raw)
				return e
			})
			if err != nil {
				if !in.AllowPartial {
					return nil, err
				}
				row.Price.Status = "failed"
				record(region+"/"+row.Type, err)
				if c.c.DiscoveryFatal(err) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
					break
				}
				continue
			}
			row.Price = price
			rate := *price.MonthlyPrice / float64(row.QuotaGB)
			row.PricePerGB = &rate
		}
	}
	return finishStorageDiscovery(out, s, errs)
}

func finishStorageDiscovery(out *ListStoragePricesOutput, s *discovery.Session, errs []error) (*ListStoragePricesOutput, error) {
	if len(errs) > 0 && len(out.Items) == 0 {
		return nil, errs[0]
	}
	slices.SortStableFunc(out.Items, func(a, b StoragePrice) int {
		return cmp.Or(cmp.Compare(a.Region, b.Region), cmp.Compare(a.Type, b.Type), cmp.Compare(a.TypeID, b.TypeID), cmp.Compare(a.PurchaseTypeID, b.PurchaseTypeID))
	})
	out.Metadata.Requests = s.Requests()
	out.Metadata.QuotedAt = s.CompletedAt()
	if len(errs) > 0 {
		return out, &pricing.DiscoveryError{Errors: errs}
	}
	return out, nil
}

func validateStorageDiscoveryCatalog(catalog *projectCatalog) error {
	return catalog.selection().Validate("storage.ListStoragePrices")
}

func (c *Client) decodeStorageDiscoveryPrice(op string, status int, raw json.RawMessage) (pricing.DiscoveryPrice, error) {
	var env envelope
	if json.Unmarshal(raw, &env) != nil || env.Success == nil {
		return pricing.DiscoveryPrice{}, storageDiscoveryError(op, status)
	}
	if !*env.Success {
		return pricing.DiscoveryPrice{}, c.envelopeError(op, status, &env)
	}
	value, err := pricequote.DecodeMonthly(op, status, env.Data)
	return pricing.DiscoveryPrice(value), err
}
