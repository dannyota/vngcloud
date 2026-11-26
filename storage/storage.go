// Package storage provides vStorage management and native S3 object clients.
// Client uses the console API and its response envelopes. ObjectClient uses
// separate S3 credentials, fixed HTTPS endpoints, and Signature Version 4.
package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/discovery"
	"danny.vn/vngcloud/internal/jsonresponse"
	"danny.vn/vngcloud/internal/pricecatalog"
	"danny.vn/vngcloud/internal/routes"
	"danny.vn/vngcloud/internal/transport"
)

// Client is the storage service client.
type Client struct {
	c *core.Client

	// regionMu guards regionIDs, the vStorage region name to UUID map the
	// first ListRegions call fills. It is filled once per Client and kept.
	regionMu    sync.Mutex
	regionIDs   map[string]string
	regionNames map[string]string
	regionGate  chan struct{}

	// sleep and now drive the delete wait; tests replace them with fakes so
	// the wait never really elapses.
	sleep sleepFunc
	now   clockFunc
}

// New builds a Client from cfg. A Client built from the same Config as
// another service client shares its login and token cache.
func New(cfg vngcloud.Config) *Client {
	return &Client{c: core.ClientOf(cfg), sleep: contextSleep, now: core.StorageClock(cfg), regionGate: make(chan struct{}, 1)}
}

// route builds a URL under the Storage endpoint's console API prefix.
func (c *Client) route(parts []string, q url.Values) string {
	return c.c.RouteURL(routes.Route{Product: routes.ProductStorage, Version: "internal/v1", Parts: parts, Query: q})
}

type envelope = pricecatalog.StorageEnvelope

// call describes one console API request.
type call struct {
	session  *discovery.Session
	op       string
	method   string
	url      string
	regionID string
	body     any
	ok       []int
	validate func(json.RawMessage) error

	// write marks a request that changes state, so an empty response says the
	// change may have happened.
	write bool

	// sensitive keeps the response from the capture hook and from decode
	// errors, since it may carry a secret. once sends the request a single
	// time, with no retry, resend, or redirect, for a create that must not
	// run twice.
	sensitive  bool
	once       bool
	noRedirect bool
}

// do sends a GET and returns the decoded envelope; see exchange.
func (c *Client) do(ctx context.Context, op, rawURL, regionID string) (*envelope, error) {
	return c.exchange(ctx, call{op: op, method: http.MethodGet, url: rawURL, regionID: regionID, ok: []int{http.StatusOK}})
}

// exchange sends k and returns the decoded envelope. A non-empty regionID
// goes out as both the region and region_id headers: the server scopes
// results by region, and a request without it reads as an empty account. A
// 2xx with an empty or non-JSON body, with no success key, or with success
// false, is an *APIError.
func (c *Client) exchange(ctx context.Context, k call) (*envelope, error) {
	if k.session != nil {
		return c.exchangeDiscovery(k)
	}
	var credentials []string
	req := transport.Request{
		SentCredentials: &credentials,
		Operation:       k.op, Method: k.method, URL: k.url, Body: k.body, OK: k.ok,
		Sensitive: k.sensitive, Once: k.once, NoRedirect: k.noRedirect,
	}
	if k.regionID != "" {
		req.Headers = map[string]string{"region": k.regionID, "region_id": k.regionID}
	}
	var raw json.RawMessage
	status, err := c.c.DoJSONStatus(ctx, req, &raw)
	if err != nil {
		var syn *json.SyntaxError
		if status > 0 && (errors.As(err, &syn) || (k.sensitive && withheldDecode(status, err))) {
			return nil, emptyResponse(k, status)
		}
		var api *core.APIError
		if keyResponseOperation(k.op) && errors.As(err, &api) {
			return nil, safeKeyHTTPError(api)
		}
		return nil, err
	}
	if status == http.StatusNoContent {
		return &envelope{}, nil
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return nil, emptyResponse(k, status)
	}
	if k.validate != nil {
		if err := k.validate(raw); err != nil {
			return nil, err
		}
	}
	if keyResponseOperation(k.op) {
		if err := validateKeyResponse(k.op, status, raw, credentials); err != nil {
			return nil, err
		}
	}
	var env envelope
	if jsonresponse.ValidateStrict(raw, &env) != nil || json.Unmarshal(raw, &env) != nil {
		return nil, emptyResponse(k, status)
	}
	if env.Success == nil {
		return nil, emptyResponse(k, status)
	}
	if !*env.Success {
		return nil, c.envelopeError(k.op, status, &env, credentials...)
	}
	return &env, nil
}

// withheldDecode reports whether err is the transport's decode failure for a
// sensitive 2xx response, which carries no status or cause of its own.
func withheldDecode(status int, err error) bool {
	var apiErr *core.APIError
	return status/100 == 2 && errors.As(err, &apiErr) && apiErr.StatusCode == 0 && apiErr.Err == nil
}

func emptyResponse(k call, status int) error {
	msg := "response body was empty or not a JSON envelope"
	if k.write {
		msg += "; the change may have happened"
	}
	return &core.APIError{Operation: k.op, StatusCode: status, Code: "EmptyResponse", Message: msg}
}

func (c *Client) envelopeError(op string, status int, env *envelope, credentials ...string) error {
	return pricecatalog.StorageRefusal(c.c, op, status, env, credentials...)
}

// decodeList decodes the envelope's datas (or data) array into items.
func decodeList[T any](op string, status int, env *envelope) ([]T, error) {
	raw := env.Datas
	if len(raw) == 0 || string(raw) == "null" {
		raw = env.Data
	}
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var items []T
	if jsonresponse.ValidateStrict(raw, &items) != nil {
		return nil, &core.APIError{Operation: op, StatusCode: status, Code: "InvalidResponse", Message: "invalid list response; body withheld"}
	}
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, &core.APIError{Operation: op, StatusCode: status, Err: fmt.Errorf("decode list: %w", transport.SanitizeDecodeError(err))}
	}
	return items, nil
}

// regionID returns the vStorage region UUID for name, or for the region
// the config maps to when name is empty.
func (c *Client) regionID(ctx context.Context, op, name string) (string, error) {
	if name == "" {
		switch c.c.Region() {
		case "hcm-3":
			name = "HCM04"
		case "han-1":
			name = "HAN02"
		default:
			return "", fmt.Errorf("%w: %s requires Region: config region %q has no default vStorage region",
				core.ErrInvalidInput, op, c.c.Region())
		}
	}
	c.regionMu.Lock()
	id, ok := c.regionIDs[strings.ToUpper(name)]
	c.regionMu.Unlock()
	if ok {
		return id, nil
	}
	env, err := c.exchange(ctx, call{op: "storage.ListRegions", method: http.MethodGet, url: c.route([]string{"regions"}, nil), ok: []int{200}, validate: func(raw json.RawMessage) error {
		return validateProjectPagination(op, raw)
	}})
	if err != nil {
		return "", err
	}
	items, err := requiredProjectList[Region](op, env)
	if err != nil {
		return "", err
	}
	regions := make([]pricecatalog.StorageRegion, 0, len(items))
	names := make(map[string]string, len(items))
	for _, r := range items {
		if r.ID == "" || r.Name == "" {
			return "", fmt.Errorf("%w: %s: invalid vStorage region catalog", core.ErrInvalidInput, op)
		}
		regions = append(regions, pricecatalog.StorageRegion{ID: r.ID, Name: r.Name})
		names[r.ID] = r.Name
	}
	ids, err := pricecatalog.StorageRegionIDs(op, regions)
	if err != nil {
		return "", err
	}
	c.regionMu.Lock()
	c.regionIDs = ids
	c.regionNames = names
	id, ok = c.regionIDs[strings.ToUpper(name)]
	c.regionMu.Unlock()
	if !ok {
		return "", fmt.Errorf("%w: %s: unknown vStorage region %q", core.ErrInvalidInput, op, name)
	}
	return id, nil
}
