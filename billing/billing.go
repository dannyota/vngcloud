// Package billing reads and changes budgets, thresholds, alerts, cost, and
// balances on the dashboard gateway. Every call is per account: it sends no
// project ID and ignores the configured region.
package billing

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/routes"
	"danny.vn/vngcloud/internal/transport"
)

// Client is the billing service client.
type Client struct {
	c *core.Client
}

// New builds a Client from cfg. A Client built from the same Config as
// another service client shares its login and token cache.
func New(cfg vngcloud.Config) *Client {
	return &Client{c: core.ClientOf(cfg)}
}

// route builds a URL under the Billing endpoint.
func (c *Client) route(parts []string, q url.Values) string {
	return c.c.RouteURL(routes.Route{Product: routes.ProductBilling, Parts: parts, Query: q})
}

// envelope is the dashboard gateway's response wrapper,
// {"code":<n or null>,"message":"...","data":...}. Code and Data stay nil
// when their key is absent from the body, which do uses to detect an
// unenveloped response.
type envelope struct {
	Code    json.RawMessage `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

// do sends req and decodes its envelope into out. A body with neither a
// "code" nor a "data" key is decoded directly into out instead: the
// balances endpoint is enveloped today but was not always, and this is a
// defense against it reverting.
func (c *Client) do(ctx context.Context, req transport.Request, out any) error {
	var raw json.RawMessage
	status, err := c.c.DoJSONStatus(ctx, req, &raw)
	if err != nil {
		return mapNotFound(err)
	}

	var env envelope
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &env); err != nil {
			return &core.APIError{Operation: req.Operation, Err: err}
		}
	}

	if env.Code == nil && env.Data == nil {
		if out != nil && len(raw) > 0 {
			if err := json.Unmarshal(raw, out); err != nil {
				return &core.APIError{Operation: req.Operation, Err: err}
			}
		}
		return nil
	}

	code := envelopeCode(env.Code)
	if code != "" && code != "200" {
		return mapNotFound(&core.APIError{
			Operation:  req.Operation,
			StatusCode: status,
			Code:       code,
			Message:    env.Message,
		})
	}
	if out != nil && len(env.Data) > 0 && !isJSONNull(env.Data) {
		if err := json.Unmarshal(env.Data, out); err != nil {
			return &core.APIError{Operation: req.Operation, Err: err}
		}
	}
	return nil
}

// envelopeCode renders the envelope's code field as decimal text: an absent
// or null code gives "", a JSON string gives its value, and a JSON number is
// already decimal text.
func envelopeCode(raw json.RawMessage) string {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return ""
	}
	if trimmed[0] == '"' {
		var s string
		if err := json.Unmarshal(trimmed, &s); err == nil {
			return s
		}
		return ""
	}
	return string(trimmed)
}

func isJSONNull(raw json.RawMessage) bool {
	return string(bytes.TrimSpace(raw)) == "null"
}

// mapNotFound turns a "Budget not found" or "Threshold not found" message
// into a NotFound error. The API sends these as a 400, or as an error
// envelope inside an HTTP 2xx, rather than a real 404.
func mapNotFound(err error) error {
	var apiErr *core.APIError
	if !errors.As(err, &apiErr) {
		return err
	}
	if !strings.HasPrefix(apiErr.Message, "Budget not found") &&
		!strings.HasPrefix(apiErr.Message, "Threshold not found") {
		return err
	}
	mapped := *apiErr
	mapped.Code = "NotFound"
	mapped.Err = core.ErrNotFound
	return &mapped
}
