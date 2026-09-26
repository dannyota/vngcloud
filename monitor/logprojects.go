package monitor

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/routes"
	"danny.vn/vngcloud/internal/transport"
)

// logRoute builds a URL under the Monitor endpoint's log-api v1 prefix,
// which serves log projects.
func (c *Client) logRoute(parts []string, q url.Values) string {
	full := append([]string{"log-api", "v1"}, parts...)
	return c.c.RouteURL(routes.Route{Product: routes.ProductMonitor, Parts: full, Query: q})
}

// logBillingRoute builds a URL under the Monitor endpoint's billing-api v2
// prefix, which classes and prices log projects. This is a different
// billing surface from the account-wide billing package: both live under
// the Monitor endpoint's own gateway, not the shared billing host.
func (c *Client) logBillingRoute(parts []string) string {
	full := append([]string{"billing-api", "v2"}, parts...)
	return c.c.RouteURL(routes.Route{Product: routes.ProductMonitor, Parts: full})
}

// LogProject is one vMonitor log project: the design's source section notes
// it is a log quota order and the log project it provisions, sharing one
// ID. The test account has never held one, so this shape is unconfirmed
// beyond ID: ProjectName and Zone come from the design's note that a log
// alarm's create body reads them off the project (see the alarms section);
// the rest are inferred from the log project's own create body and the
// list's query filters. Confirm and correct this shape against a live
// project once one exists.
type LogProject struct {
	ID                 string `json:"id"`
	ProjectName        string `json:"projectName"`
	ProjectDescription string `json:"projectDescription"`
	Status             string `json:"status"`
	BillingStatus      string `json:"billingStatus"`
	ProjectType        string `json:"projectType"`
	Zone               string `json:"zone"`
	CreatedAt          string `json:"createdAt"`
	UpdatedAt          string `json:"updatedAt"`
}

// UnmarshalJSON decodes LogProject with ID, CreatedAt, and UpdatedAt routed
// through flexibleString: three fields this shape has not confirmed the
// wire type of, and a numeric id or an epoch createdAt would otherwise fail
// the whole item's decode, taking the rest of a list page down with it.
func (lp *LogProject) UnmarshalJSON(data []byte) error {
	type alias LogProject
	aux := struct {
		ID        flexibleString `json:"id"`
		CreatedAt flexibleString `json:"createdAt"`
		UpdatedAt flexibleString `json:"updatedAt"`
		*alias
	}{alias: (*alias)(lp)}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	lp.ID = string(aux.ID)
	lp.CreatedAt = string(aux.CreatedAt)
	lp.UpdatedAt = string(aux.UpdatedAt)
	return nil
}

// ListLogProjectsInput filters the log project list. Query and
// BillingStatus filter by name and billing status; the API also takes
// project_type and status query keys the SDK does not expose yet, always
// sent empty, as ListChannels does for searchtext and field. Page and Size
// page the result; unlike ListChannels' page, the API's page here is
// 0-based, so Page's zero value already names the first page rather than
// meaning "unset" (see logProjectPageQuery). A non-positive Size sends
// logProjectDefaultPageSize; a Size over the API's own maximum of 100 is
// sent unchanged and rejected by the server (ADR 0002 rule 5).
type ListLogProjectsInput struct {
	Query         string
	BillingStatus string
	Page          int
	Size          int
}

type ListLogProjectsOutput = core.PagedList[LogProject]

type listLogProjectsResponse struct {
	Content       []LogProject `json:"content"`
	CurrentPage   int          `json:"currentPage"`
	PageSize      int          `json:"pageSize"`
	TotalElements int          `json:"totalElements"`
	TotalPages    int          `json:"totalPages"`
}

// ListLogProjects lists log projects on the account. A nil Input is valid
// and lists every project from page 0 at logProjectDefaultPageSize, the
// same as &ListLogProjectsInput{}. The test account has none, so only the
// empty envelope shape is confirmed live; LogProject's own field shape is
// not (see its doc comment).
func (c *Client) ListLogProjects(ctx context.Context, in *ListLogProjectsInput) (*ListLogProjectsOutput, error) {
	const op = "monitor.ListLogProjects"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}

	var query, billingStatus string
	page, size := 0, 0
	if in != nil {
		query, billingStatus, page, size = in.Query, in.BillingStatus, in.Page, in.Size
	}
	q := logProjectPageQuery(page, size)
	q.Set("query", query)
	q.Set("billing_status", billingStatus)
	q.Set("project_type", "")
	q.Set("status", "")

	var resp listLogProjectsResponse
	req := transport.Request{
		Operation: op,
		Method:    http.MethodGet,
		URL:       c.logRoute([]string{"projects"}, q),
		OK:        []int{200},
	}
	if err := c.c.DoJSON(ctx, req, &resp); err != nil {
		return nil, err
	}
	return core.NewPagedList(resp.Content, resp.CurrentPage, resp.PageSize, resp.TotalPages, resp.TotalElements), nil
}

// logProjectDefaultPageSize is the size ListLogProjects sends when Size is
// left unset. A live read confirmed the log project list rejects any size
// over 100 with a 400 ("size must be less than or equal to 100"), unlike
// ListChannels' underlying API, which accepts core.DefaultPageSize (10000)
// in one call; 100 is the largest this default can be without exceeding
// that cap. A caller-supplied Size over 100 is still sent unchanged and
// left to the server to reject (ADR 0002 rule 5).
const logProjectDefaultPageSize = 100

// logProjectPageQuery builds the page and size query parameters for the log
// project list. The list's page is 0-based (unlike ListChannels' 1-based
// page), so page 0 is a real, distinct first page: core.PageQuery would
// promote it to 1 and silently skip it, whether the caller left Page unset
// or asked for page 0 by name. Only a negative page, which the API does not
// define, is clamped, to 0, its lowest valid value.
func logProjectPageQuery(page, size int) url.Values {
	if page < 0 {
		page = 0
	}
	if size <= 0 {
		size = logProjectDefaultPageSize
	}
	q := url.Values{}
	q.Set("page", strconv.Itoa(page))
	q.Set("size", strconv.Itoa(size))
	return q
}

// GetLogProjectInput identifies the log project to read.
type GetLogProjectInput struct {
	LogProjectID string `vngcloud:"required"`
}

type GetLogProjectOutput struct {
	LogProject LogProject
}

// GetLogProject reads one log project by ID. The design's source section
// marks this call "Console code only": no live project has confirmed it or
// LogProject's per-project fields (see LogProject's doc comment). A
// fabricated ID against the live account did confirm the path shape and
// that a missing project 404s into core.ErrNotFound, the same as GetCheck.
func (c *Client) GetLogProject(ctx context.Context, in *GetLogProjectInput) (*GetLogProjectOutput, error) {
	const op = "monitor.GetLogProject"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "LogProjectID", in.LogProjectID); err != nil {
		return nil, err
	}

	var project LogProject
	req := transport.Request{
		Operation: op,
		Method:    http.MethodGet,
		URL:       c.logRoute([]string{"projects", in.LogProjectID}, nil),
		OK:        []int{200},
	}
	if err := c.c.DoJSON(ctx, req, &project); err != nil {
		return nil, err
	}
	return &GetLogProjectOutput{LogProject: project}, nil
}

// LogProjectClassBasic and LogProjectClassPro name the classes
// ListLogProjectClasses returns live; Enterprise is disabled and not
// orderable today, and has no constant since CreateLogProjectInput.Class
// never needs to name it on purpose. Class is a plain string field, so a
// class the console adds later still reaches the server unchanged.
const (
	LogProjectClassBasic = "Basic"
	LogProjectClassPro   = "Pro"
)

// LogProjectClassStatusActive and LogProjectClassStatusDisabled are the two
// LogProjectClass.Status values seen live: Basic and Pro are active,
// Enterprise is disabled.
const (
	LogProjectClassStatusActive   = "ACTIVE"
	LogProjectClassStatusDisabled = "DISABLED"
)

// LogProjectClass is one log project class ListLogProjectClasses can
// return. The API also sends a class-level "type" (LOG for every class
// seen); the SDK drops it, the same way it drops any field a caller's
// struct omits. Retentions is empty for a class with no config key at all,
// seen live for the disabled Enterprise class.
type LogProjectClass struct {
	ID          string
	Name        string
	Description string
	Priority    int
	Status      string
	Retentions  []LogProjectRetention
}

// LogProjectRetention is one retention option of a LogProjectClass, from
// its nested config.retentions list. The API also sends a per-option
// priority and status, seen live always matching the parent class's; the
// SDK drops them.
type LogProjectRetention struct {
	Amount    int    `json:"amount"`
	MinSize   int    `json:"minSize"`
	MaxSize   int    `json:"maxSize"`
	Step      int    `json:"step"`
	PackageID string `json:"packageId"`
}

// UnmarshalJSON decodes LogProjectClass with Retentions lifted out of the
// nested config.retentions the wire sends, so callers read it as a direct
// field rather than reaching through an extra Config layer that otherwise
// exists only to hold it.
func (lc *LogProjectClass) UnmarshalJSON(data []byte) error {
	var wire struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		Description string `json:"description"`
		Priority    int    `json:"priority"`
		Status      string `json:"status"`
		Config      struct {
			Retentions []LogProjectRetention `json:"retentions"`
		} `json:"config"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	lc.ID = wire.ID
	lc.Name = wire.Name
	lc.Description = wire.Description
	lc.Priority = wire.Priority
	lc.Status = wire.Status
	lc.Retentions = wire.Config.Retentions
	return nil
}

// ListLogProjectClassesInput has no fields today; a nil Input is valid.
// Fields may be added later without breaking callers.
type ListLogProjectClassesInput struct{}

type ListLogProjectClassesOutput = core.List[LogProjectClass]

// ListLogProjectClasses lists the log project classes the account may order
// a log project from. There is no paging.
func (c *Client) ListLogProjectClasses(ctx context.Context, in *ListLogProjectClassesInput) (*ListLogProjectClassesOutput, error) {
	const op = "monitor.ListLogProjectClasses"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}

	classes, err := c.readLogProjectClasses(ctx, op)
	if err != nil {
		return nil, err
	}
	return &ListLogProjectClassesOutput{Items: classes}, nil
}

// readLogProjectClasses is ListLogProjectClasses' request, reused by
// QuoteCreateLogProject under its own op name rather than
// "monitor.ListLogProjectClasses", so a class-list failure inside the quote
// reports the operation the caller actually made.
func (c *Client) readLogProjectClasses(ctx context.Context, op string) ([]LogProjectClass, error) {
	var classes []LogProjectClass
	req := transport.Request{
		Operation: op,
		Method:    http.MethodGet,
		URL:       c.logBillingRoute([]string{"log", "quota-class"}),
		OK:        []int{200},
	}
	if err := c.c.DoJSON(ctx, req, &classes); err != nil {
		return nil, err
	}
	return classes, nil
}

// logProjectRedirectURL is sent as the order's redirectUrl. The console
// only follows it after a browser payment, which pay true (sent below)
// skips, so its exact value has no effect the live checks confirmed; this
// sends the Monitor console's own host.
const logProjectRedirectURL = "https://vmonitor.console.greennode.ai/"

// CreateLogProjectInput orders a log project. QuoteCreateLogProject prices
// it; CreateLogProject, a later release, orders it. Both build their
// request from this same Input through buildLogProjectOrderBody, so the
// quote always prices exactly the resource a create would order (ADR 0002
// rule 8).
//
// Class empty sends LogProjectClassBasic. RetentionDays 0 picks the class's
// only retention option; a class with more than one needs RetentionDays to
// name one, in days. GBPerDay 0 sends the chosen option's minimum size, in
// GB per day. MaxPrice bounds CreateLogProject's own order, in VND a month,
// default 0; QuoteCreateLogProject ignores it. NoWait is CreateLogProject's
// too. The server checks the name pattern and the GBPerDay and
// RetentionDays ranges; the SDK only checks that the named class and
// retention option exist (ADR 0002 rule 5).
type CreateLogProjectInput struct {
	Name string `vngcloud:"required"`

	Description   string
	Class         string
	RetentionDays int
	GBPerDay      int
	MaxPrice      float64
	NoWait        bool
}

// logProjectOrderBody is CreateLogProject and QuoteCreateLogProject's shared
// request body, built by buildLogProjectOrderBody.
type logProjectOrderBody struct {
	RedirectURL        string         `json:"redirectUrl"`
	PackageID          string         `json:"packageId"`
	Quantity           int            `json:"quantity"`
	BuyWith            map[string]any `json:"buyWith"`
	MonthPeriod        int            `json:"monthPeriod"`
	ProjectName        string         `json:"projectName"`
	ProjectDescription string         `json:"projectDescription"`
	Pay                bool           `json:"pay"`
}

// buildLogProjectOrderBody resolves in's Class, RetentionDays, and GBPerDay
// against classes, a fresh ListLogProjectClasses read, and returns the body
// CreateLogProject and QuoteCreateLogProject both send. Neither method
// caches classes: the design notes the price can change between one
// request and the next, and a cached list could also miss a package the
// live one still has. in must already have passed core.CheckRequired.
func buildLogProjectOrderBody(op string, in *CreateLogProjectInput, classes []LogProjectClass) (logProjectOrderBody, error) {
	className := in.Class
	if className == "" {
		className = LogProjectClassBasic
	}

	var class *LogProjectClass
	for i := range classes {
		if classes[i].Name == className {
			class = &classes[i]
			break
		}
	}
	if class == nil || class.Status != LogProjectClassStatusActive || len(class.Retentions) == 0 {
		return logProjectOrderBody{}, fmt.Errorf("%w: %s class %q is not an orderable class",
			core.ErrInvalidInput, op, className)
	}

	days := in.RetentionDays
	if days == 0 {
		if len(class.Retentions) != 1 {
			return logProjectOrderBody{}, fmt.Errorf("%w: %s class %q has %d retention options, RetentionDays must name one",
				core.ErrInvalidInput, op, className, len(class.Retentions))
		}
		days = class.Retentions[0].Amount
	}

	var retention *LogProjectRetention
	for i := range class.Retentions {
		if class.Retentions[i].Amount == days {
			retention = &class.Retentions[i]
			break
		}
	}
	if retention == nil {
		return logProjectOrderBody{}, fmt.Errorf("%w: %s class %q has no %d-day retention option",
			core.ErrInvalidInput, op, className, days)
	}

	gb := in.GBPerDay
	if gb == 0 {
		gb = retention.MinSize
	}

	return logProjectOrderBody{
		RedirectURL:        logProjectRedirectURL,
		PackageID:          retention.PackageID,
		Quantity:           gb * days,
		BuyWith:            map[string]any{},
		MonthPeriod:        1,
		ProjectName:        in.Name,
		ProjectDescription: in.Description,
		Pay:                true,
	}, nil
}

// QuoteCreateLogProjectOutput is a log project order's price.
// DiscountPercent is nil when the response sends it null, seen live for
// both a free and a paid class.
type QuoteCreateLogProjectOutput struct {
	OptimumPrice    float64
	OriginalPrice   float64
	DiscountPrice   float64
	DiscountPercent *float64
	Properties      []LogProjectPriceProperty
}

// LogProjectPriceProperty is one line item of a log project quote's
// propertiesPrice list. Every response seen live has exactly one, named
// "monitor-platform-log".
type LogProjectPriceProperty struct {
	Name            string
	Description     *string
	OptimumPrice    float64
	OriginalPrice   float64
	DiscountPrice   float64
	DiscountPercent *float64
}

// logProjectQuoteResponse is QuoteCreateLogProject's wire response. It is
// not enveloped: the price fields sit at the top level, the same shape
// pricing.GetQuote decodes but with its own field set (no monthlyPrice or
// currentPrice).
type logProjectQuoteResponse struct {
	OptimumPrice    float64                       `json:"optimumPrice"`
	OriginalPrice   float64                       `json:"originalPrice"`
	DiscountPrice   float64                       `json:"discountPrice"`
	DiscountPercent *float64                      `json:"discountPercent"`
	PropertiesPrice []logProjectPricePropertyWire `json:"propertiesPrice"`
}

type logProjectPricePropertyWire struct {
	Name            string   `json:"name"`
	Description     *string  `json:"description"`
	OptimumPrice    float64  `json:"optimumPrice"`
	OriginalPrice   float64  `json:"originalPrice"`
	DiscountPrice   float64  `json:"discountPrice"`
	DiscountPercent *float64 `json:"discountPercent"`
}

// QuoteCreateLogProject prices the log project in would order, without
// ordering it: it reads the live class list, builds the same request body
// CreateLogProject would send (ADR 0002 rule 8), and sends it as a read.
// The created-price call is a POST, but it changes nothing, so it is a read
// under ADR 0002 rule 1, and sets Idempotent so a failure that may have
// reached the server is still retried.
func (c *Client) QuoteCreateLogProject(ctx context.Context, in *CreateLogProjectInput) (*QuoteCreateLogProjectOutput, error) {
	const op = "monitor.QuoteCreateLogProject"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}

	classes, err := c.readLogProjectClasses(ctx, op)
	if err != nil {
		return nil, err
	}
	body, err := buildLogProjectOrderBody(op, in, classes)
	if err != nil {
		return nil, err
	}

	var resp logProjectQuoteResponse
	req := transport.Request{
		Operation:  op,
		Method:     http.MethodPost,
		URL:        c.logBillingRoute([]string{"log", "prices", "created-price"}),
		Body:       body,
		OK:         []int{200},
		Idempotent: true,
	}
	if err := c.c.DoJSON(ctx, req, &resp); err != nil {
		return nil, err
	}

	properties := make([]LogProjectPriceProperty, len(resp.PropertiesPrice))
	for i, p := range resp.PropertiesPrice {
		properties[i] = LogProjectPriceProperty(p)
	}
	return &QuoteCreateLogProjectOutput{
		OptimumPrice:    resp.OptimumPrice,
		OriginalPrice:   resp.OriginalPrice,
		DiscountPrice:   resp.DiscountPrice,
		DiscountPercent: resp.DiscountPercent,
		Properties:      properties,
	}, nil
}
