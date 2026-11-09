package monitor

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/transport"
)

// ListSyntheticPackagesInput takes no fields.
type ListSyntheticPackagesInput struct{}

// ListSyntheticPackagesOutput holds the result of ListSyntheticPackages.
type ListSyntheticPackagesOutput struct {
	Items                      []SyntheticQuotaClass
	Packages                   []SyntheticPackage
	MonthPeriods               []int
	MaxMonthPeriodForFreeQuota int
	PaymentMethod              string
}

// SyntheticQuotaClass holds a synthetic quota class and its options.
type SyntheticQuotaClass struct {
	ID, Name, Status, Description string
	Priority                      int
	Options                       []SyntheticClassOption
	Packages                      []SyntheticClassPackage
}

// SyntheticClassPackage describes monthly units within its requested class.
// A nil price is unknown; API and Location are units, not purchased capacity.
type SyntheticClassPackage struct {
	ID, Name, TierName    string
	TierID, API, Location int
	Price                 *float64
}

// SyntheticClassOption holds a class option and its supported periods.
type SyntheticClassOption struct {
	PackageID                      string
	Location, MinAPI, MaxAPI, Step int
	Price                          *float64
	MonthPeriods                   []int
	AutoRenewEligible              *bool
}

// SyntheticPackage holds a synthetic package and its supported periods.
type SyntheticPackage struct {
	ID, Name              string
	TierID, API, Location int
	Price                 *float64
	MonthPeriods          []int
	AutoRenewEligible     *bool
}

// ListSyntheticPackages keeps class option identifiers separate from v1
// package identifiers. No relationship between these catalogs is evidenced.
func (c *Client) ListSyntheticPackages(ctx context.Context, _ *ListSyntheticPackagesInput) (*ListSyntheticPackagesOutput, error) {
	const op = "monitor.ListSyntheticPackages"
	var credentials monitorReadCredentials
	// Count sanitized bodies, which can expand when JSON escapes strings.
	// Discarded retry bodies remain bounded independently by the transport.
	reader := quotaCompositeReader{client: c, credentials: &credentials}
	classes, err := reader.read(ctx, op, "v2", "synthetic", "quota-class")
	if err != nil {
		return nil, err
	}
	items, err := decodeSyntheticClasses(op, classes)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	raw, err := reader.read(ctx, op, "v1", "synthetic", "packages")
	if err != nil {
		return nil, err
	}
	packages, err := decodeSyntheticCatalogPackages(op, raw)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	raw, err = reader.read(ctx, op, "v1", "settings")
	if err != nil {
		return nil, err
	}
	o := quotaObjectOf(raw)
	periods, ok := quotaArray(o.fields["monthPeriods"])
	out := &ListSyntheticPackagesOutput{Items: items, Packages: packages, MonthPeriods: make([]int, 0, len(periods)), MaxMonthPeriodForFreeQuota: o.integer("maxMonthPeriodForFreeQuota", false), PaymentMethod: o.text("paymentMethod", false)}
	seen := map[int]bool{}
	for _, period := range periods {
		p := quotaObjectOf(quotaPeriodJSON(period))
		value := p.integer("period", false)
		if p.invalid || value <= 0 || seen[value] {
			return nil, quotaResponseError(op)
		}
		seen[value] = true
		out.MonthPeriods = append(out.MonthPeriods, value)
	}
	if o.invalid || !ok || len(periods) == 0 || out.MaxMonthPeriodForFreeQuota <= 0 {
		return nil, quotaResponseError(op)
	}
	if len(out.Items) > 64 {
		return nil, quotaResponseError(op)
	}
	slices.SortFunc(out.Items, compareSyntheticClasses)
	// Validate the entire fan-out before using any peer ID as a path segment.
	for _, class := range out.Items {
		if core.CheckPathID(op, "ClassID", class.ID) != nil {
			return nil, quotaResponseError(op)
		}
	}
	for i := range out.Items {
		class := &out.Items[i]
		raw, err := reader.read(ctx, op, "v2", "synthetic", "quota-class", class.ID, "packages")
		if err != nil {
			return nil, err
		}
		class.Packages, err = decodeSyntheticClassPackages(op, raw)
		if err != nil {
			return nil, err
		}
	}
	for i := range out.Packages {
		p := &out.Packages[i]
		if p.Price != nil {
			p.MonthPeriods = catalogPeriods(out.MonthPeriods, out.MaxMonthPeriodForFreeQuota, *p.Price == 0)
		}
	}
	for i := range out.Items {
		class := &out.Items[i]
		for j := range class.Options {
			option := &class.Options[j]
			if class.Name == "Basic" && class.Status == QuotaStatusActive && option.Location == 1 && option.MinAPI == 10 && option.MaxAPI == 10 && option.Step == 1 {
				refused := false
				option.AutoRenewEligible = &refused
				option.MonthPeriods = catalogPeriods(out.MonthPeriods, out.MaxMonthPeriodForFreeQuota, true)
			}
		}
	}
	// A later read can reveal another credential; scrub earlier strings again
	// before returning any component of the composed result.
	for i := range out.Items {
		p := &out.Items[i]
		c.scrubCatalogText(&credentials, &p.ID, &p.Name, &p.Status, &p.Description)
		for j := range p.Packages {
			pkg := &p.Packages[j]
			c.scrubCatalogText(&credentials, &pkg.ID, &pkg.Name, &pkg.TierName)
		}
		for j := range p.Options {
			c.scrubCatalogText(&credentials, &p.Options[j].PackageID)
		}
	}
	for i := range out.Packages {
		p := &out.Packages[i]
		c.scrubCatalogText(&credentials, &p.ID, &p.Name)
	}
	c.scrubCatalogText(&credentials, &out.PaymentMethod)

	slices.SortFunc(out.Packages, func(a, b SyntheticPackage) int {
		if n := strings.Compare(a.Name, b.Name); n != 0 {
			return n
		}
		return strings.Compare(a.ID, b.ID)
	})
	return out, nil
}

func quotaPeriodJSON(period json.RawMessage) json.RawMessage {
	value := struct {
		Period json.RawMessage `json:"period"`
	}{Period: period}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	return encoded
}

func (c *Client) scrubCatalogText(credentials *monitorReadCredentials, values ...*string) {
	for _, value := range values {
		*value, _ = c.c.RedactError(*value, "", (*credentials)...)
	}
}

func catalogPeriods(periods []int, maximum int, free bool) []int {
	out := make([]int, 0, len(periods))
	for _, period := range periods {
		if !free || period <= maximum {
			out = append(out, period)
		}
	}
	return out
}

func decodeSyntheticClasses(op string, raw json.RawMessage) ([]SyntheticQuotaClass, error) {
	entries, ok := quotaArray(raw)
	if !ok {
		return nil, quotaResponseError(op)
	}
	out := make([]SyntheticQuotaClass, 0, len(entries))
	seen := map[string]bool{}
	for _, entry := range entries {
		o := quotaObjectOf(entry)
		class := SyntheticQuotaClass{ID: o.text("id", false), Name: o.text("name", false), Status: o.text("status", false), Description: o.text("description", false), Priority: o.integer("priority", false), Options: []SyntheticClassOption{}}
		if o.invalid || class.ID == "" || seen[class.ID] {
			return nil, quotaResponseError(op)
		}
		seen[class.ID] = true
		if config, exists := o.fields["config"]; exists {
			obj := quotaObjectOf(config)
			if obj.text("packages", false) == "" {
				obj.invalid = true
			}
			locations, valid := quotaArray(obj.fields["locations"])
			if obj.invalid || !valid {
				return nil, quotaResponseError(op)
			}
			type identity struct {
				id       string
				location int
			}
			options := map[identity]bool{}
			for _, location := range locations {
				item := quotaObjectOf(location)
				option := SyntheticClassOption{PackageID: item.text("packageId", false), Location: item.integer("amount", false), MinAPI: item.integer("minApi", false), MaxAPI: item.integer("maxApi", false), Step: item.integer("step", false), MonthPeriods: []int{}}
				key := identity{option.PackageID, option.Location}
				if item.invalid || option.PackageID == "" || option.Location <= 0 || option.MinAPI <= 0 || option.MaxAPI < option.MinAPI || option.Step <= 0 || options[key] {
					return nil, quotaResponseError(op)
				}
				options[key] = true
				class.Options = append(class.Options, option)
			}
		} else if class.Status == QuotaStatusActive {
			return nil, quotaResponseError(op)
		}
		slices.SortFunc(class.Options, func(a, b SyntheticClassOption) int {
			if a.Location < b.Location {
				return -1
			}
			if a.Location > b.Location {
				return 1
			}
			return strings.Compare(a.PackageID, b.PackageID)
		})
		out = append(out, class)
	}
	return out, nil
}

func decodeSyntheticCatalogPackages(op string, raw json.RawMessage) ([]SyntheticPackage, error) {
	entries, ok := quotaArray(raw)
	if !ok {
		return nil, quotaResponseError(op)
	}
	out := make([]SyntheticPackage, 0, len(entries))
	seen := map[string]bool{}
	for _, entry := range entries {
		o := quotaObjectOf(entry)
		p := SyntheticPackage{ID: o.text("id", false), Name: o.text("name", false), TierID: o.integer("tierId", false), API: o.integer("api", false), Location: o.integer("location", false), Price: o.price(), MonthPeriods: []int{}}
		if o.invalid || p.ID == "" || p.API < 0 || p.Location < 0 || p.TierID < 0 || seen[p.ID] {
			return nil, quotaResponseError(op)
		}
		seen[p.ID] = true
		out = append(out, p)
	}
	return out, nil
}

func (o *quotaObject) notificationLimits() SyntheticNotificationLimits {
	values := make([]*int, 6)
	for i, key := range []string{"email", "sms", "slack", "webhook", "telegram", "teams"} {
		value := o.integer(key, true)
		if value < 0 {
			o.invalid = true
		}
		if raw, ok := o.fields[key]; ok && strings.TrimSpace(string(raw)) != "null" {
			values[i] = &value
		}
	}
	return SyntheticNotificationLimits{Email: values[0], SMS: values[1], Slack: values[2], Webhook: values[3], Telegram: values[4], Teams: values[5]}
}

func quotaLimitValue(value *int) int {
	if value == nil {
		return 0
	}
	return *value
}

const quotaCompositeMaxBody = 8 << 20

type quotaCompositeReader struct {
	client      *Client
	credentials *monitorReadCredentials
	retained    int
	exhausted   bool
}

func (r *quotaCompositeReader) read(ctx context.Context, op, version string, parts ...string) (json.RawMessage, error) {
	if r.exhausted {
		return nil, monitorReadError(op, 0, "RequestFailed", "quota retained body budget exhausted", transport.ErrBodyTooLarge)
	}
	raw, _, err := r.client.quotaRequest(ctx, transport.Request{
		Operation: op, Method: http.MethodGet, URL: r.client.billingRoute(version, parts), OK: []int{http.StatusOK}, NoRedirect: true,
	}, r.credentials)
	if err != nil {
		return nil, err
	}
	// The returned sanitized bytes are retained only after the budget check.
	// Charge bodies before decoding, including ones validation later rejects.
	if len(raw) > quotaCompositeMaxBody-r.retained {
		r.exhausted = true
		return nil, monitorReadError(op, http.StatusOK, "ResponseTooLarge", "quota retained body budget exceeded", transport.ErrBodyTooLarge)
	}
	r.retained += len(raw)
	return raw, nil
}

func compareSyntheticClasses(a, b SyntheticQuotaClass) int {
	if a.Priority < b.Priority {
		return -1
	}
	if a.Priority > b.Priority {
		return 1
	}
	if n := strings.Compare(a.Name, b.Name); n != 0 {
		return n
	}
	return strings.Compare(a.ID, b.ID)
}

func decodeSyntheticClassPackages(op string, raw json.RawMessage) ([]SyntheticClassPackage, error) {
	entries, ok := quotaArray(raw)
	if !ok {
		return nil, quotaResponseError(op)
	}
	out := make([]SyntheticClassPackage, 0, len(entries))
	seen := map[string]bool{}
	for _, entry := range entries {
		o := quotaObjectOf(entry)
		p := SyntheticClassPackage{ID: o.text("id", false), Name: o.text("name", false), TierName: o.text("tierName", false), TierID: o.integer("tierId", false), API: o.integer("api", false), Location: o.integer("location", false), Price: o.price()}
		if o.invalid || p.ID == "" || seen[p.ID] || p.API <= 0 || p.Location <= 0 || p.TierID < 0 {
			return nil, quotaResponseError(op)
		}
		seen[p.ID] = true
		out = append(out, p)
	}
	slices.SortFunc(out, func(a, b SyntheticClassPackage) int {
		if n := strings.Compare(a.Name, b.Name); n != 0 {
			return n
		}
		return strings.Compare(a.ID, b.ID)
	})
	return out, nil
}
