// Package storage manages vStorage object storage: regions, projects, the
// buckets in a project, the project's S3 keys, and the attach of a key to a
// service account. It uses the vStorage console API, which wraps every
// response in one envelope and reports many failures as HTTP 200.
package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/routes"
	"danny.vn/vngcloud/internal/transport"
)

// codeInvalidInput is the envelope code the server uses for each input check
// it reports; errorMsg names the rule.
const codeInvalidInput = 112

// maxEnvelopeMessage caps the envelope errorMsg copied into an error.
const maxEnvelopeMessage = 256

// Client is the storage service client.
type Client struct {
	c *core.Client

	// regionMu guards regionIDs, the vStorage region name to UUID map the
	// first ListRegions call fills. It is filled once per Client and kept.
	regionMu  sync.Mutex
	regionIDs map[string]string

	// sleep and now drive the delete wait; tests replace them with fakes so
	// the wait never really elapses.
	sleep sleepFunc
	now   clockFunc
}

// New builds a Client from cfg. A Client built from the same Config as
// another service client shares its login and token cache.
func New(cfg vngcloud.Config) *Client {
	return &Client{c: core.ClientOf(cfg), sleep: contextSleep, now: time.Now}
}

// route builds a URL under the Storage endpoint's console API prefix.
func (c *Client) route(parts []string, q url.Values) string {
	return c.c.RouteURL(routes.Route{Product: routes.ProductStorage, Version: "internal/v1", Parts: parts, Query: q})
}

// envelope is the shape every console API response shares.
type envelope struct {
	Success *bool           `json:"success"`
	Code    json.RawMessage `json:"code"`
	ErrMsg  string          `json:"errorMsg"`
	Data    json.RawMessage `json:"data"`
	Datas   json.RawMessage `json:"datas"`
	IsNext  bool            `json:"isNext"`
}

// call describes one console API request.
type call struct {
	op       string
	method   string
	url      string
	regionID string
	body     any
	ok       []int

	// write marks a request that changes state, so an empty response says the
	// change may have happened.
	write bool

	// sensitive keeps the response from the capture hook and from decode
	// errors, since it may carry a secret. once sends the request a single
	// time, with no retry, resend, or redirect, for a create that must not
	// run twice.
	sensitive bool
	once      bool
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
	var credential string
	req := transport.Request{
		SentCredential: &credential,
		Operation:      k.op, Method: k.method, URL: k.url, Body: k.body, OK: k.ok,
		Sensitive: k.sensitive, Once: k.once,
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
		return nil, err
	}
	if status == http.StatusNoContent {
		return &envelope{}, nil
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return nil, emptyResponse(k, status)
	}
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, emptyResponse(k, status)
	}
	if env.Success == nil {
		return nil, emptyResponse(k, status)
	}
	if !*env.Success {
		return nil, c.envelopeError(k.op, status, &env, credential)
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

// envelopeError turns a success:false envelope into an *APIError. A code
// from 400 to 599 also matches that status's sentinel, and code 112, the
// server's input check, matches ErrInvalidInput.
func (c *Client) envelopeError(op string, status int, env *envelope, credentials ...string) error {
	code := codeText(env.Code)
	if _, valid := transport.ParseErrorCode(env.Code); !valid {
		code = core.ResolvedCode(status, "")
	}
	message, cleanCode := c.c.RedactError(env.ErrMsg, code, credentials...)
	apiErr := &core.APIError{Operation: op, StatusCode: status, Code: cleanCode, Message: cut(message, maxEnvelopeMessage)}
	if n, err := strconv.Atoi(code); err == nil && n == codeInvalidInput {
		apiErr.Err = core.ErrInvalidInput
	} else if err == nil && n >= 400 && n <= 599 {
		switch n {
		case http.StatusUnauthorized:
			apiErr.Err = core.ErrAuth
		case http.StatusForbidden:
			apiErr.Err = core.ErrPermission
		case http.StatusNotFound:
			apiErr.Err = core.ErrNotFound
		case http.StatusTooManyRequests:
			apiErr.Err = core.ErrRateLimited
		}
	}
	return apiErr
}

func codeText(raw json.RawMessage) string {
	code, _ := transport.ParseErrorCode(raw)
	return code
}

// cut returns s limited to max bytes without splitting a UTF-8 rune.
func cut(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	s = s[:limit]
	for !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
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
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, &core.APIError{Operation: op, StatusCode: status, Err: fmt.Errorf("decode list: %w", err)}
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
	out, err := c.ListRegions(ctx, nil)
	if err != nil {
		return "", err
	}
	c.regionMu.Lock()
	if c.regionIDs == nil {
		c.regionIDs = map[string]string{}
	}
	for _, r := range out.Items {
		if r.ID == "" || r.Name == "" {
			continue
		}
		c.regionIDs[strings.ToUpper(r.Name)] = r.ID
	}
	id, ok = c.regionIDs[strings.ToUpper(name)]
	c.regionMu.Unlock()
	if !ok {
		return "", fmt.Errorf("%w: %s: unknown vStorage region %q", core.ErrInvalidInput, op, name)
	}
	return id, nil
}
