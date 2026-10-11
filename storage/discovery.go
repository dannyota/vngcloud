package storage

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/discovery"
	"danny.vn/vngcloud/internal/transport"
	"danny.vn/vngcloud/pricing"
)

type ListStoragePricesInput struct {
	Region       string
	Type         string
	QuotaGB      int64
	MaxRequests  int
	AllowPartial bool
}

type ListStoragePricesOutput struct {
	Items    []StoragePrice
	Metadata pricing.DiscoveryMetadata
}

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

// exchangeDiscovery guards each physical exchange before envelope handling.
func (c *Client) exchangeDiscovery(k call) (*envelope, error) {
	req := transport.Request{Operation: k.op, Method: k.method, URL: k.url, Body: k.body, OK: k.ok}
	if k.regionID != "" {
		req.Headers = map[string]string{"region": k.regionID, "region_id": k.regionID}
	}
	var env envelope
	_, err := k.session.DoJSON(c.c, req, nil, func(status int, raw json.RawMessage) error {
		var fields map[string]json.RawMessage
		if json.Unmarshal(raw, &fields) != nil || fields == nil || json.Unmarshal(raw, &env) != nil || env.Success == nil {
			return storageDiscoveryError(k.op, status)
		}
		for _, key := range []string{"page", "paging", "pagination", "pageSize", "totalPage", "totalPages", "totalItem", "next", "nextPage", "hasMore", "continuationToken"} {
			if _, ok := fields[key]; ok {
				return storageDiscoveryError(k.op, status)
			}
		}
		if env.IsNext {
			return storageDiscoveryError(k.op, status)
		}
		if !*env.Success {
			return c.envelopeError(k.op, status, &env)
		}
		if k.validate != nil {
			if err := k.validate(raw); err != nil {
				return storageDiscoveryError(k.op, status)
			}
		}
		if k.method == http.MethodGet {
			rawList := env.Datas
			if len(rawList) == 0 {
				rawList = env.Data
			}
			var items []json.RawMessage
			if json.Unmarshal(rawList, &items) != nil || items == nil {
				return storageDiscoveryError(k.op, status)
			}
			keys := []string{}
			if strings.HasSuffix(k.url, "/billing/project_types") {
				keys = []string{"id", "name", "status", "allowPeriod", "billableResources"}
			}
			if strings.HasSuffix(k.url, "/billing/purchase_types") {
				keys = []string{"id", "name", "title", "status"}
			}
			for _, item := range items {
				if len(keys) == 0 {
					break
				}
				var fields map[string]json.RawMessage
				if json.Unmarshal(item, &fields) != nil || fields == nil {
					return storageDiscoveryError(k.op, status)
				}
				for _, key := range keys {
					if len(fields[key]) == 0 || string(fields[key]) == "null" {
						return storageDiscoveryError(k.op, status)
					}
				}
			}
			return k.session.AddRows(len(items))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &env, nil
}

func (c *Client) discoveryRegion(s *discovery.Session, name string) (string, string, error) {
	const op = "storage.ListStoragePrices"
	if name == "" {
		if c.c.Region() == "hcm-3" {
			name = "HCM04"
		} else {
			name = "HAN02"
		}
	}
	name = strings.ToUpper(name)
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
	ids := map[string]string{}
	seen := map[string]bool{}
	for _, r := range regions {
		key := strings.ToUpper(r.Name)
		if core.CheckPathID(op, "RegionID", r.ID) != nil || key == "" || ids[key] != "" || seen[r.ID] {
			return "", "", storageDiscoveryError(op, 200)
		}
		ids[key] = r.ID
		seen[r.ID] = true
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
			req := transport.Request{Operation: "storage.QuoteCreateProject", Method: http.MethodPost, URL: c.projectBillingRoute("v2", []string{"price"}, url.Values{"region_id": {id}}), Headers: map[string]string{"region": id, "region_id": id}, Body: projectPriceRequest(spec), OK: []int{200}}
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
	const op = "storage.ListStoragePrices"
	ids := map[int]bool{}
	names := map[string]bool{}
	for _, typ := range catalog.types {
		if typ.ID <= 0 || typ.Name == "" || ids[typ.ID] || names[typ.Name] {
			return storageDiscoveryError(op, 200)
		}
		ids[typ.ID] = true
		names[typ.Name] = true
	}
	ids = map[int]bool{}
	for _, p := range catalog.purchases {
		if p.ID <= 0 || p.Name == "" || ids[p.ID] {
			return storageDiscoveryError(op, 200)
		}
		ids[p.ID] = true
	}
	return nil
}

func (c *Client) decodeStorageDiscoveryPrice(op string, status int, raw json.RawMessage) (pricing.DiscoveryPrice, error) {
	var env envelope
	if json.Unmarshal(raw, &env) != nil || env.Success == nil {
		return pricing.DiscoveryPrice{}, storageDiscoveryError(op, status)
	}
	if !*env.Success {
		return pricing.DiscoveryPrice{}, c.envelopeError(op, status, &env)
	}
	var value struct {
		Optimum  *float64 `json:"optimumPrice"`
		Original *float64 `json:"originalPrice"`
		Discount *float64 `json:"discountPrice"`
		Percent  *float64 `json:"discountPercent"`
		Currency string   `json:"currency"`
		Tax      string   `json:"tax"`
	}
	if json.Unmarshal(env.Data, &value) != nil || value.Optimum == nil {
		return pricing.DiscoveryPrice{}, storageDiscoveryError(op, status)
	}
	for _, p := range []*float64{value.Optimum, value.Original, value.Discount, value.Percent} {
		if p != nil && (*p < 0 || math.IsNaN(*p) || math.IsInf(*p, 0)) {
			return pricing.DiscoveryPrice{}, storageDiscoveryError(op, status)
		}
	}
	if *value.Optimum <= 0 {
		return pricing.DiscoveryPrice{}, storageDiscoveryError(op, status)
	}
	if value.Currency == "" {
		value.Currency = "VND"
	}
	return pricing.DiscoveryPrice{Status: "quoted", Basis: "monthly-total", Currency: value.Currency, Tax: value.Tax, Source: op, MonthlyPrice: value.Optimum, OriginalPrice: value.Original, DiscountPrice: value.Discount, DiscountPercent: value.Percent}, nil
}
