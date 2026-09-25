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
	"strconv"
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

// doRaw sends req and returns the raw response body and HTTP status.
func (c *Client) doRaw(ctx context.Context, req transport.Request) (json.RawMessage, int, error) {
	var raw json.RawMessage
	status, err := c.c.DoJSONStatus(ctx, req, &raw)
	return raw, status, err
}

// decodeEnvelope unmarshals raw into an envelope. An empty raw body decodes
// to a zero envelope rather than an error, so a 204 with no body is not
// mistaken for a missing envelope.
func decodeEnvelope(raw json.RawMessage) (envelope, error) {
	var env envelope
	if len(raw) == 0 {
		return env, nil
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return envelope{}, err
	}
	return env, nil
}

// envelopeSuccess reports whether code, the envelope's decimal code text,
// marks success. An empty code (a null code, pending the status-to-code
// mapping) succeeds; otherwise the code must be 200 to 299, matching the
// gateway, which sends 200 for reads and 201 for a create.
func envelopeSuccess(code string) bool {
	if code == "" {
		return true
	}
	n, err := strconv.Atoi(code)
	if err != nil {
		return false
	}
	return n >= 200 && n <= 299
}

// finishEnvelope maps an envelope code outside 200 to 299 to an error, and
// otherwise decodes Data into out.
func (c *Client) finishEnvelope(req transport.Request, status int, env envelope, out any) error {
	code := envelopeCode(env.Code)
	if !envelopeSuccess(code) {
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

// do sends req and decodes its envelope into out. A non-empty body with
// neither a "code" nor a "data" key is an error: every dashboard gateway
// operation except GetBalances is enveloped, so an unenveloped body means
// the response does not match what the caller asked for.
func (c *Client) do(ctx context.Context, req transport.Request, out any) error {
	raw, status, err := c.doRaw(ctx, req)
	if err != nil {
		return mapNotFound(err)
	}
	if len(raw) == 0 {
		return nil
	}

	env, err := decodeEnvelope(raw)
	if err != nil {
		return &core.APIError{Operation: req.Operation, Err: err}
	}
	if env.Code == nil && env.Data == nil {
		return &core.APIError{
			Operation:  req.Operation,
			StatusCode: status,
			Message:    "response had no envelope",
		}
	}
	return c.finishEnvelope(req, status, env, out)
}

// doBalances is do, but a body with neither a "code" nor a "data" key is
// decoded directly into out instead of treated as an error. The balances
// endpoint is enveloped today but was not always, and this is a defense
// against it reverting; no other operation gets this fallback.
func (c *Client) doBalances(ctx context.Context, req transport.Request, out any) error {
	raw, status, err := c.doRaw(ctx, req)
	if err != nil {
		return mapNotFound(err)
	}
	if len(raw) == 0 {
		return nil
	}

	env, err := decodeEnvelope(raw)
	if err != nil {
		return &core.APIError{Operation: req.Operation, Err: err}
	}
	if env.Code == nil && env.Data == nil {
		if out != nil {
			if err := json.Unmarshal(raw, out); err != nil {
				return &core.APIError{Operation: req.Operation, Err: err}
			}
		}
		return nil
	}
	return c.finishEnvelope(req, status, env, out)
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
