// Package storage reads vStorage object storage: regions, projects, and the
// buckets in a project. It uses the vStorage console API, which wraps every
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
	"unicode/utf8"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/routes"
	"danny.vn/vngcloud/internal/transport"
)

// maxEnvelopeMessage caps the envelope errorMsg copied into an error.
const maxEnvelopeMessage = 256

// Client is the storage service client.
type Client struct {
	c *core.Client

	// regionMu guards regionIDs, the vStorage region name to UUID map the
	// first ListRegions call fills. It is filled once per Client and kept.
	regionMu  sync.Mutex
	regionIDs map[string]string
}

// New builds a Client from cfg. A Client built from the same Config as
// another service client shares its login and token cache.
func New(cfg vngcloud.Config) *Client {
	return &Client{c: core.ClientOf(cfg)}
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

// do sends a GET and returns the decoded envelope. A 2xx with an empty or
// non-JSON body, with no success key, or with success false, is an *APIError.
func (c *Client) do(ctx context.Context, op, rawURL, regionID string) (*envelope, error) {
	req := transport.Request{Operation: op, Method: http.MethodGet, URL: rawURL, OK: []int{http.StatusOK}}
	if regionID != "" {
		req.Headers = map[string]string{"region_id": regionID}
	}
	var raw json.RawMessage
	status, err := c.c.DoJSONStatus(ctx, req, &raw)
	if err != nil {
		var syn *json.SyntaxError
		if status > 0 && (errors.As(err, &syn)) {
			return nil, emptyResponse(op, status)
		}
		return nil, err
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return nil, emptyResponse(op, status)
	}
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, emptyResponse(op, status)
	}
	if env.Success == nil {
		return nil, emptyResponse(op, status)
	}
	if !*env.Success {
		return nil, envelopeError(op, status, &env)
	}
	return &env, nil
}

func emptyResponse(op string, status int) error {
	return &core.APIError{Operation: op, StatusCode: status, Code: "EmptyResponse",
		Message: "response body was empty or not a JSON envelope"}
}

// envelopeError turns a success:false envelope into an *APIError. A code
// from 400 to 599 also matches that status's sentinel.
func envelopeError(op string, status int, env *envelope) error {
	code := codeText(env.Code)
	apiErr := &core.APIError{Operation: op, StatusCode: status, Code: code, Message: cut(env.ErrMsg, maxEnvelopeMessage)}
	if n, err := strconv.Atoi(code); err == nil && n >= 400 && n <= 599 {
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
	s := strings.TrimSpace(string(raw))
	if s == "" || s == "null" {
		return ""
	}
	var str string
	if json.Unmarshal(raw, &str) == nil {
		return str
	}
	return s
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
