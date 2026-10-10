package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/routes"
	"danny.vn/vngcloud/internal/transport"
)

var (
	// ErrFailed means reads proved a terminal provisioning failure. No failure
	// status is classified until the API contract establishes one.
	ErrFailed = errors.New("storage: write failed")
	// ErrPaymentRequired means an order needs console reconciliation before
	// another purchase. It does not establish whether money moved.
	ErrPaymentRequired = errors.New("storage: payment requires console reconciliation")
)

const projectOrderRecovery = "Order may exist; run storage list-projects in the same region before retrying. Check pending orders and payment history if no project appears. Do not submit another order while payment or provisioning is unresolved."
const projectPaymentRecovery = "The server asked for a browser checkout; no project was created by this call as far as the project list shows. Check pending orders in the console, and do not retry until resolved."

// CreateProjectOutput retains quoted prices after an order attempt. Project
// stays nil until a read confirms its identity, including with NoWait.
type CreateProjectOutput struct {
	Project      *Project
	OrderID      string
	MonthlyPrice float64
	TotalPrice   float64
}

type projectOrderBody struct {
	ResourceType string           `json:"resourceType"`
	Action       string           `json:"action"`
	PaymentType  string           `json:"paymentType"`
	ResourceInfo projectOrderInfo `json:"resourceInfo"`
}

type projectOrderInfo struct {
	ProjectName      string `json:"projectName"`
	PurchaseTypeID   int    `json:"purchaseTypeId"`
	ProjectType      int    `json:"projectType"`
	ProjectTypeGroup string `json:"projectTypeGroup"`
	Quota            int64  `json:"quota"`
	ArchivePeriod    int    `json:"archivePeriod"`
	BillingTimeType  string `json:"billingTimeType"`
	IsTrial          bool   `json:"isTrial"`
	IsPoc            bool   `json:"isPoc"`
	EnableAutoRenew  bool   `json:"enableAutoRenew"`
	AutoRenewPeriod  int    `json:"autoRenewPeriod"`
}

// CreateProject buys one monthly package with renewal disabled. MaxPrice
// caps the fresh quote, not the server's debit: the API has no price lock.
// The order is sent once. An uncertain result requires console reconciliation.
func (c *Client) CreateProject(ctx context.Context, in *CreateProjectInput) (*CreateProjectOutput, error) {
	const op = "storage.CreateProject"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if in.Name == "" || in.QuotaGB <= 0 || math.IsNaN(in.MaxPrice) || math.IsInf(in.MaxPrice, 0) || in.MaxPrice < 0 {
		return nil, fmt.Errorf("%w: %s requires Name, positive QuotaGB, and finite nonnegative MaxPrice", core.ErrInvalidInput, op)
	}
	id, err := c.regionID(ctx, op, in.Region)
	if err != nil {
		return nil, err
	}
	catalog, err := c.readProjectCatalog(ctx, op, id)
	if err != nil {
		return nil, err
	}
	spec, err := catalog.resolve(op, in.Type, in.QuotaGB)
	if err != nil {
		return nil, err
	}
	group := ""
	for _, typ := range catalog.types {
		if typ.Name == in.Type {
			group = typ.Group
		}
	}
	if group == "" {
		return nil, projectResponseError(op, "selected project type has no group")
	}
	before, err := c.completeProjects(ctx, op, id)
	if err != nil {
		return nil, err
	}
	for _, p := range before {
		if p.project.Name == in.Name {
			return nil, fmt.Errorf("%w: %s: project name already exists", core.ErrInvalidInput, op)
		}
	}
	if int64(len(before)) >= catalog.maxProjects {
		return nil, fmt.Errorf("%w: %s: regional project limit reached", core.ErrInvalidInput, op)
	}
	body := projectOrderBody{ResourceType: "object_storage", Action: "create", PaymentType: "auto", ResourceInfo: projectOrderInfo{ProjectName: in.Name, PurchaseTypeID: spec.purchaseTypeID, ProjectType: spec.projectTypeID, ProjectTypeGroup: group, Quota: spec.quota, BillingTimeType: "block"}}
	quote, err := c.sendProjectQuote(ctx, op, id, spec)
	if err != nil {
		return nil, err
	}
	if math.IsNaN(quote.TotalPrice) || math.IsInf(quote.TotalPrice, 0) {
		return nil, projectResponseError(op, "quote total is not finite")
	}
	if quote.TotalPrice > in.MaxPrice {
		return nil, fmt.Errorf("%w: %s: total %.0f VND exceeds MaxPrice %.0f VND", core.ErrPriceAboveMax, op, quote.TotalPrice, in.MaxPrice)
	}
	out := &CreateProjectOutput{MonthlyPrice: quote.MonthlyPrice, TotalPrice: quote.TotalPrice}
	env, err := c.exchangeProjectWrite(ctx, call{op: op, method: http.MethodPost, url: c.c.RouteURL(routes.Route{Product: routes.ProductStorage, Version: "internal/v2", Parts: []string{"orders"}, Query: url.Values{"region_id": {id}}}), regionID: id, body: body, ok: []int{http.StatusOK}, write: true, once: true, sensitive: true})
	if err != nil {
		return out, projectWriteError(op, err, projectOrderRecovery)
	}
	// Order ID fields remain unverified. Never derive an ID from a redirect.
	return c.confirmProjectOrder(ctx, op, id, in, spec, before, env, out)
}

// Keep the shared sentinel's value without displaying its legacy delete
// wording for an order whose acceptance is unknown.
type projectUnsettledError struct {
	op       string
	cause    error
	recovery string
}

func (e *projectUnsettledError) Error() string {
	message := e.op + ": write outcome is unconfirmed"
	if e.cause != nil {
		message += "; " + e.cause.Error()
	}
	return message + "; " + e.recovery
}

func (e *projectUnsettledError) Unwrap() []error {
	if e.cause == nil {
		return []error{ErrNotSettled}
	}
	return []error{ErrNotSettled, e.cause}
}

func projectUnsettled(op string, err error, recovery string) error {
	return &projectUnsettledError{op: op, cause: err, recovery: recovery}
}

func projectWriteError(op string, err error, recovery string) error {
	var api *core.APIError
	if errors.As(err, &api) && errors.Is(err, errProjectWriteRefused) {
		return err
	}
	return projectUnsettled(op, err, recovery)
}

// exchangeProjectWrite withholds payment responses from capture and errors.
// The live check uses a private HTTP transport to capture the response safely.
func (c *Client) exchangeProjectWrite(ctx context.Context, k call, redact ...string) (*envelope, error) {
	var raw json.RawMessage
	var credential string
	// Decode 4xx envelopes to distinguish a refusal from an uncertain result.
	ok := append([]int(nil), k.ok...)
	for status := 400; status < 500; status++ {
		ok = append(ok, status)
	}
	status, err := c.c.DoJSONStatus(ctx, transport.Request{SentCredential: &credential, Redact: redact, Operation: k.op, Method: k.method, URL: k.url, Body: k.body, OK: ok, Once: true, Sensitive: true, WithholdMessage: "project write response withheld", Headers: map[string]string{"region": k.regionID, "region_id": k.regionID}}, &raw)
	if err != nil {
		var api *core.APIError
		if errors.As(err, &api) {
			safe := *api
			if safe.StatusCode == 0 && status > 0 {
				safe.StatusCode = status
				mapped := c.envelopeError(k.op, status, &envelope{Code: json.RawMessage(strconv.Itoa(status))})
				var statusError *core.APIError
				if errors.As(mapped, &statusError) {
					safe.Err = errors.Join(safe.Err, statusError.Err)
					safe.Code = statusError.Code
				}
			}
			safe.Message = "project write response withheld"
			safe.Code = safeProjectErrorCode(safe.StatusCode, safe.Code)
			return nil, &safe
		}
		return nil, err
	}
	var env envelope
	if json.Unmarshal(raw, &env) != nil || env.Success == nil {
		if status/100 == 4 {
			return nil, c.envelopeError(k.op, status, &envelope{Code: json.RawMessage(strconv.Itoa(status)), ErrMsg: "project write response withheld"})
		}
		return nil, emptyResponse(k, status)
	}
	if !*env.Success {
		code, valid := transport.ParseErrorCode(env.Code)
		if (!valid || strings.TrimSpace(code) == "") && status/100 == 4 {
			return nil, c.envelopeError(k.op, status, &envelope{Code: json.RawMessage(strconv.Itoa(status)), ErrMsg: "project write response withheld"})
		}
		err = c.envelopeError(k.op, status, &env, credential)
		var api *core.APIError
		if errors.As(err, &api) {
			api.Message = "server refused project write; response withheld"
			api.Code = safeProjectErrorCode(status, api.Code)
			// A refusal is distinct from an unreadable HTTP 200 response.
			if valid && strings.TrimSpace(code) != "" && (status == http.StatusOK || status/100 == 4) {
				api.Err = errors.Join(api.Err, errProjectWriteRefused)
			}
		}
		return nil, err
	}
	if status != http.StatusOK {
		return nil, c.envelopeError(k.op, status, &envelope{Code: json.RawMessage(strconv.Itoa(status)), ErrMsg: "project write response withheld"})
	}
	return &env, nil
}

var errProjectWriteRefused = errors.New("storage: server refused project write")

func safeProjectErrorCode(status int, code string) string {
	for _, r := range code {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '_' && r != '-' {
			return core.ResolvedCode(status, "")
		}
	}
	return code
}
