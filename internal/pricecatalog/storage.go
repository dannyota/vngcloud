//revive:disable:exported Internal contracts live in docs/design/cost-estimate.md.
package pricecatalog

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/discovery"
	"danny.vn/vngcloud/internal/jsonresponse"
	"danny.vn/vngcloud/internal/transport"
)

type StorageEnvelope struct {
	Success *bool           `json:"success"`
	Code    json.RawMessage `json:"code"`
	ErrMsg  string          `json:"errorMsg"`
	Data    json.RawMessage `json:"data"`
	Datas   json.RawMessage `json:"datas"`
	IsNext  bool            `json:"isNext"`
}

func StorageRefusal(c *core.Client, op string, status int, env *StorageEnvelope, credentials ...string) error {
	code, valid := transport.ParseErrorCode(env.Code)
	if !valid {
		code = core.ResolvedCode(status, "")
	}
	message, cleanCode := c.RedactError(env.ErrMsg, code, credentials...)
	if len(message) > 256 {
		message = message[:256]
		for !utf8.ValidString(message) {
			message = message[:len(message)-1]
		}
	}
	api := &core.APIError{Operation: op, StatusCode: status, Code: cleanCode, Message: message}
	if n, err := strconv.Atoi(code); err == nil && n == 112 {
		api.Err = core.ErrInvalidInput
	} else if err == nil && n >= 400 && n <= 599 {
		switch n {
		case 401:
			api.Err = core.ErrAuth
		case 403:
			api.Err = core.ErrPermission
		case 404:
			api.Err = core.ErrNotFound
		case 429:
			api.Err = core.ErrRateLimited
		}
	}
	return api
}

func SafeStorageRefusal(c *core.Client, op string, status int, env *StorageEnvelope) error {
	code, _ := transport.ParseErrorCode(env.Code)
	safe := StorageEnvelope{ErrMsg: "storage request refused; body withheld"}
	if n, err := strconv.Atoi(code); err == nil && n >= 0 {
		safe.Code = json.RawMessage(strconv.Itoa(n))
	}
	return StorageRefusal(c, op, status, &safe)
}

func StorageExchange(c *core.Client, s *discovery.Session, req transport.Request, validate func(json.RawMessage) error, safe ...bool) (*StorageEnvelope, error) {
	var env StorageEnvelope
	_, err := s.DoJSON(c, req, nil, func(status int, raw json.RawMessage) error {
		var fields map[string]json.RawMessage
		if ValidateFields(raw, &EnvelopeFields{}) != nil || json.Unmarshal(raw, &fields) != nil || fields == nil || json.Unmarshal(raw, &env) != nil || env.Success == nil {
			return Invalid(req.Operation, status)
		}
		for _, key := range []string{"page", "paging", "pagination", "pageSize", "totalPage", "totalPages", "totalItem", "next", "nextPage", "hasMore", "continuationToken"} {
			if _, ok := fields[key]; ok {
				return Invalid(req.Operation, status)
			}
		}
		if env.IsNext {
			return Invalid(req.Operation, status)
		}
		if !*env.Success {
			if len(safe) > 0 && safe[0] {
				return SafeStorageRefusal(c, req.Operation, status, &env)
			}
			return StorageRefusal(c, req.Operation, status, &env)
		}
		if req.Method == http.MethodGet {
			rawList := env.Datas
			if len(rawList) == 0 {
				rawList = env.Data
			}
			if err := validateStorageList(req.URL, rawList); err != nil {
				return Invalid(req.Operation, status)
			}
		}
		if validate != nil {
			if err := validate(raw); err != nil {
				return Invalid(req.Operation, status)
			}
		}
		if req.Method != http.MethodGet {
			return nil
		}
		rawList := env.Datas
		if len(rawList) == 0 {
			rawList = env.Data
		}
		var items []json.RawMessage
		if json.Unmarshal(rawList, &items) != nil || items == nil {
			return Invalid(req.Operation, status)
		}
		keys := []string{}
		u, _ := url.Parse(req.URL)
		if strings.HasSuffix(u.Path, "/billing/project_types") {
			keys = []string{"id", "name", "status", "allowPeriod", "billableResources"}
		}
		if strings.HasSuffix(u.Path, "/billing/purchase_types") {
			keys = []string{"id", "name", "title", "status"}
		}
		for _, item := range items {
			if len(keys) == 0 {
				break
			}
			var entry map[string]json.RawMessage
			if json.Unmarshal(item, &entry) != nil || entry == nil {
				return Invalid(req.Operation, status)
			}
			for _, key := range keys {
				if len(entry[key]) == 0 || string(entry[key]) == "null" {
					return Invalid(req.Operation, status)
				}
			}
		}
		return s.AddRows(len(items))
	})
	if err != nil {
		return nil, err
	}
	return &env, nil
}

type StorageRegion struct {
	ID   string `json:"regionId"`
	Name string `json:"regionName"`
}

func StorageRegions(c *core.Client, s *discovery.Session) ([]StorageRegion, error) {
	const op = "storage.ListRegions"
	req := transport.Request{Operation: op, Method: http.MethodGet, URL: StorageURL(c, "internal/v1", []string{"regions"}, nil), OK: []int{200}}
	var regions []StorageRegion
	_, err := StorageExchange(c, s, req, func(raw json.RawMessage) error {
		var env StorageEnvelope
		if json.Unmarshal(raw, &env) != nil {
			return Invalid(op, 200)
		}
		var err error
		regions, err = storageList[StorageRegion](env)
		if err != nil {
			return err
		}
		_, err = StorageRegionIDs(op, regions)
		return err
	}, true)
	return regions, err
}

func StorageRegionIDs(op string, regions []StorageRegion) (map[string]string, error) {
	for _, r := range regions {
		if core.CheckPathID(op, "RegionID", r.ID) != nil {
			return nil, Invalid(op, 200)
		}
	}
	return StorageRegionAliases(op, regions)
}

func StorageRegionAliases(op string, regions []StorageRegion) (map[string]string, error) {
	ids := map[string]string{}
	seen := map[string]bool{}
	for _, r := range regions {
		key := strings.ToUpper(r.Name)
		if r.ID == "" || key == "" || ids[key] != "" || seen[r.ID] {
			return nil, Invalid(op, 200)
		}
		ids[key] = r.ID
		seen[r.ID] = true
	}
	for _, r := range regions {
		key := strings.ToUpper(r.ID)
		if id := ids[key]; id != "" && id != r.ID {
			return nil, Invalid(op, 200)
		}
		ids[key] = r.ID
	}
	return ids, nil
}

func StorageRegionName(configured, name string) string {
	if name == "" {
		if configured == "hcm-3" {
			name = "HCM04"
		} else {
			name = "HAN02"
		}
	}
	return strings.ToUpper(name)
}

func ResolveStorageRegion(op, configured, name string, regions []StorageRegion) (string, string, error) {
	if err := ValidateStorageRegion(op, configured, name); err != nil {
		return "", "", err
	}
	name = StorageRegionName(configured, name)
	ids, err := StorageRegionIDs(op, regions)
	if err != nil {
		return "", "", err
	}
	if ids[name] == "" {
		return "", "", fmt.Errorf("%w: unknown storage region", core.ErrInvalidInput)
	}
	for _, region := range regions {
		if region.ID == ids[name] {
			return region.ID, strings.ToUpper(region.Name), nil
		}
	}
	return "", "", fmt.Errorf("%w: unknown storage region", core.ErrInvalidInput)
}

type StorageResource struct {
	ID             int    `json:"id"`
	Name           string `json:"name"`
	PriceKey       string `json:"priceKey"`
	PurchaseTypeID int    `json:"purchaseTypeId"`
	ProjectTypeID  int    `json:"projectTypeId"`
	Period         *int   `json:"period"`
}

type StorageType struct {
	ID                int               `json:"id"`
	Name              string            `json:"name"`
	Title             string            `json:"title"`
	Status            int               `json:"status"`
	AllowPeriod       []int             `json:"allowPeriod"`
	BillableResources []StorageResource `json:"billableResources"`
}

type StoragePurchase struct {
	ID     int    `json:"id"`
	Name   string `json:"name"`
	Title  string `json:"title"`
	Status int    `json:"status"`
}

type StorageCatalog struct {
	Types                           []StorageType
	Purchases                       []StoragePurchase
	MinQuota, MaxQuota, MaxProjects int64
	IAMCheckout                     bool
}

type StorageSpec struct {
	Quota                         int64
	PurchaseTypeID, ProjectTypeID int
}

func (catalog *StorageCatalog) ValidateIdentities(op string) error {
	types := map[int]StorageType{}
	for _, typ := range catalog.Types {
		if previous, ok := types[typ.ID]; ok && (previous.Name != typ.Name || previous.Title != typ.Title) {
			return fmt.Errorf("%w: %s: conflicting project type identity", core.ErrInvalidInput, op)
		}
		types[typ.ID] = typ
	}
	purchases := map[int]StoragePurchase{}
	for _, p := range catalog.Purchases {
		if previous, ok := purchases[p.ID]; ok && (previous.Name != p.Name || previous.Title != p.Title) {
			return fmt.Errorf("%w: %s: conflicting purchase type identity", core.ErrInvalidInput, op)
		}
		purchases[p.ID] = p
	}
	return nil
}

func (catalog *StorageCatalog) Validate(op string) error {
	ids := map[int]bool{}
	names := map[string]bool{}
	for _, typ := range catalog.Types {
		if typ.ID <= 0 || typ.Name == "" || ids[typ.ID] || names[typ.Name] {
			return Invalid(op, 200)
		}
		ids[typ.ID], names[typ.Name] = true, true
	}
	ids = map[int]bool{}
	for _, p := range catalog.Purchases {
		if p.ID <= 0 || p.Name == "" || ids[p.ID] {
			return Invalid(op, 200)
		}
		ids[p.ID] = true
	}
	return nil
}

func (catalog *StorageCatalog) MonthlyPurchase(op string) (*StoragePurchase, error) {
	var selected *StoragePurchase
	for i := range catalog.Purchases {
		p := &catalog.Purchases[i]
		if p.Status != 1 || p.Name != "Normal" || p.Title != "Pay monthly" {
			continue
		}
		if selected != nil || p.ID <= 0 {
			return nil, fmt.Errorf("%w: %s: ambiguous or invalid monthly purchase", core.ErrInvalidInput, op)
		}
		selected = p
	}
	return selected, nil
}

func (catalog *StorageCatalog) Resolve(op, name string, quota int64) (StorageSpec, error) {
	invalid := func() (StorageSpec, error) {
		return StorageSpec{}, fmt.Errorf("%w: %s: unavailable or ambiguous project type, purchase, or quota", core.ErrInvalidInput, op)
	}
	if quota < catalog.MinQuota {
		return StorageSpec{}, fmt.Errorf("%w: %s: QuotaGB %d is below the region minimum of %d GB", core.ErrInvalidInput, op, quota, catalog.MinQuota)
	}
	if quota > catalog.MaxQuota {
		return StorageSpec{}, fmt.Errorf("%w: %s: QuotaGB %d is above the region maximum of %d GB", core.ErrInvalidInput, op, quota, catalog.MaxQuota)
	}
	var selected *StorageType
	for i := range catalog.Types {
		typ := &catalog.Types[i]
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
	monthly, err := catalog.MonthlyPurchase(op)
	if err != nil {
		return StorageSpec{}, err
	}
	if monthly == nil {
		return invalid()
	}
	if _, err := StoragePriceKey(op, *selected, monthly.ID); err != nil {
		return invalid()
	}
	return StorageSpec{Quota: quota, PurchaseTypeID: monthly.ID, ProjectTypeID: selected.ID}, nil
}

func StoragePriceKey(op string, typ StorageType, purchase int) (string, error) {
	count := 0
	key := ""
	for _, r := range typ.BillableResources {
		if r.PurchaseTypeID != purchase {
			continue
		}
		if r.ProjectTypeID != typ.ID || (r.Period != nil && *r.Period != 1) {
			return "", Invalid(op, 200)
		}
		key = r.PriceKey
		count++
	}
	if count != 1 {
		return "", Invalid(op, 200)
	}
	return key, nil
}

func ConfigurationValue(op, key string, raw json.RawMessage) (string, error) {
	var items []struct {
		Key   string  `json:"key"`
		Value *string `json:"value"`
	}
	if json.Unmarshal(raw, &items) != nil {
		return "", responseError(op, "malformed catalog data")
	}
	if len(items) != 1 || items[0].Key != key || items[0].Value == nil {
		return "", responseError(op, "missing or duplicate configuration "+key)
	}
	return *items[0].Value, nil
}

func ConfigurationInteger(op, key, value string) (int64, error) {
	for _, digit := range value {
		if digit < '0' || digit > '9' {
			return 0, responseError(op, "invalid configuration "+key)
		}
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil || n <= 0 {
		return 0, responseError(op, "invalid configuration "+key)
	}
	return n, nil
}

func responseError(op, message string) error {
	return &core.APIError{Operation: op, StatusCode: 200, Message: message}
}

func storageList[T any](env StorageEnvelope) ([]T, error) {
	raw := env.Datas
	if len(raw) == 0 {
		raw = env.Data
	}
	var items []T
	if ValidateFields(raw, &items) != nil || json.Unmarshal(raw, &items) != nil || items == nil {
		return nil, errors.New("invalid storage catalog")
	}
	return items, nil
}

func ReadStorage(c *core.Client, s *discovery.Session, op, id string) (*StorageCatalog, error) {
	read := func(version string, parts []string, q url.Values, target any) error {
		req := transport.Request{Operation: op, Method: http.MethodGet, URL: StorageURL(c, version, parts, q), Headers: StorageHeaders(id), OK: []int{200}}
		_, err := StorageExchange(c, s, req, func(raw json.RawMessage) error {
			if jsonresponse.Validate(raw) != nil {
				return Invalid(op, 200)
			}
			var env StorageEnvelope
			if json.Unmarshal(raw, &env) != nil {
				return Invalid(op, 200)
			}
			list := env.Datas
			if len(list) == 0 {
				list = env.Data
			}
			if ValidateFields(list, target) != nil || json.Unmarshal(list, target) != nil {
				return Invalid(op, 200)
			}
			return nil
		}, true)
		return err
	}
	catalog := &StorageCatalog{}
	if err := read("internal/v1", []string{"billing", "project_types"}, nil, &catalog.Types); err != nil {
		return nil, err
	}
	if err := read("internal/v1", []string{"billing", "purchase_types"}, nil, &catalog.Purchases); err != nil {
		return nil, err
	}
	if err := catalog.Validate(op); err != nil {
		return nil, err
	}
	monthly, err := catalog.MonthlyPurchase(op)
	if err != nil {
		return nil, Invalid(op, 200)
	}
	for _, typ := range catalog.Types {
		if err := s.AddRows(len(typ.BillableResources)); err != nil {
			return nil, err
		}
		if monthly != nil && typ.Status == 1 && slices.Contains(typ.AllowPeriod, 1) {
			if _, err := StoragePriceKey(op, typ, monthly.ID); err != nil {
				return nil, err
			}
		}
	}
	for _, item := range []struct {
		key    string
		target *int64
	}{
		{"vos_billing_normal_min_quota", &catalog.MinQuota},
		{"vos_billing_normal_max_quota", &catalog.MaxQuota},
		{"max_project_per_user_per_region", &catalog.MaxProjects},
	} {
		var rows json.RawMessage
		if err := read("billing-api/v1", []string{"configurations"}, url.Values{"keys": {item.key}, "region_id": {id}}, &rows); err != nil {
			return nil, err
		}
		value, err := ConfigurationValue(op, item.key, rows)
		if err != nil {
			return nil, Invalid(op, 200)
		}
		*item.target, err = ConfigurationInteger(op, item.key, value)
		if err != nil {
			return nil, Invalid(op, 200)
		}
	}
	if catalog.MinQuota > catalog.MaxQuota {
		return nil, Invalid(op, 200)
	}
	var rows json.RawMessage
	if err := read("billing-api/v1", []string{"configurations"}, url.Values{"keys": {"enable_iam_checkout"}, "region_id": {id}}, &rows); err != nil {
		return nil, err
	}
	value, err := ConfigurationValue(op, "enable_iam_checkout", rows)
	if err != nil || (value != "true" && value != "false") {
		return nil, Invalid(op, 200)
	}
	catalog.IAMCheckout = value == "true"
	return catalog, nil
}
func ValidateStorageRegion(op, configured, name string) error {
	if region := configured; name == "" && region != "hcm-3" && region != "han-1" {
		return fmt.Errorf("%w: %s: unsupported config region %q", core.ErrInvalidInput, op, region)
	}
	return nil
}

func validateStorageList(rawURL string, raw json.RawMessage) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return err
	}
	switch {
	case strings.HasSuffix(u.Path, "/billing/project_types"):
		return ValidateFields(raw, (*[]StorageType)(nil))
	case strings.HasSuffix(u.Path, "/billing/purchase_types"):
		return ValidateFields(raw, (*[]StoragePurchase)(nil))
	case strings.HasSuffix(u.Path, "/regions"):
		return ValidateFields(raw, (*[]StorageRegion)(nil))
	case strings.HasSuffix(u.Path, "/configurations"):
		return ValidateFields(raw, (*[]struct {
			Key   string  `json:"key"`
			Value *string `json:"value"`
		})(nil))
	default:
		return nil
	}
}
