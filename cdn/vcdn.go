package cdn

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"unicode"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/routes"
	"danny.vn/vngcloud/internal/transport"
)

// ErrNoAPIKey means a vCDN call found no API key. It wraps
// vngcloud.ErrNoCredentials, and nothing was sent. Set the key with
// vngcloud.WithCDNAPIKey, VNGCLOUD_VCDN_API_KEY, or vcdn_api_key in the
// credentials file.
var ErrNoAPIKey error = noAPIKeyError{}

type noAPIKeyError struct{}

func (noAPIKeyError) Error() string {
	return "cdn: no vCDN API key: set WithCDNAPIKey, VNGCLOUD_VCDN_API_KEY, or vcdn_api_key in the credentials file"
}

func (noAPIKeyError) Unwrap() error { return vngcloud.ErrNoCredentials }

const (
	// maxMessageBytes bounds every error message the package builds from a
	// server message.
	maxMessageBytes = 256

	rejectedKeyMessage  = "vCDN API key rejected: check the key and its expiry"
	forbiddenKeyMessage = "vCDN API key not allowed to call this API"

	// codeEmptyResponse marks a 2xx that is not the expected envelope.
	codeEmptyResponse = "EmptyResponse"
)

// envelope is the body of every vCDN 2xx answer, success or not.
type envelope struct {
	Success *bool           `json:"success"`
	Code    json.RawMessage `json:"code"`
	Message *string         `json:"message"`
	Data    json.RawMessage `json:"data"`
}

// call describes one vCDN request.
type call struct {
	op     string
	method string
	parts  []string
	// sensitive withholds the response from the capture hook and from decode
	// errors, for a read whose body holds a private key or a token.
	sensitive bool
	// notFound, when set, marks a detail read: an envelope with success false
	// and no data is ErrNotFound with this message.
	notFound string
}

// shortOp is the operation without its package prefix, for messages.
func (r call) shortOp() string { return strings.TrimPrefix(r.op, "cdn.") }

// do sends r with the API key and returns the envelope's data. Every failure
// is an *core.APIError or a context or configuration error.
func (c *Client) do(ctx context.Context, r call) (json.RawMessage, error) {
	key, err := c.c.CDNAPIKey()
	if err != nil {
		return nil, err
	}
	if key == "" {
		return nil, ErrNoAPIKey
	}
	var raw json.RawMessage
	status, err := c.c.DoJSONStatus(ctx, transport.Request{
		Operation: r.op,
		Method:    r.method,
		URL:       c.c.RouteURL(routes.Route{Product: routes.ProductCDN, Version: "v1", Parts: r.parts}),
		OK:        []int{http.StatusOK, http.StatusCreated, http.StatusAccepted, http.StatusNoContent},
		APIKey:    key,
		Sensitive: r.sensitive,
	}, &raw)
	if err != nil {
		if status >= 200 && status < 300 {
			return nil, r.unexpected(status)
		}
		return nil, r.cleanError(err)
	}
	if len(raw) == 0 {
		return nil, r.unexpected(status)
	}
	var env envelope
	if json.Unmarshal(raw, &env) != nil || env.Success == nil {
		return nil, r.unexpected(status)
	}
	if *env.Success {
		return env.Data, nil
	}
	return nil, r.envelopeError(status, env)
}

func (r call) unexpected(status int) error {
	return &core.APIError{
		Operation:  r.op,
		StatusCode: status,
		Code:       codeEmptyResponse,
		Message:    "vCDN " + r.shortOp() + " returned an unexpected response",
	}
}

// envelopeError builds the error for a 2xx answer with success false: the
// envelope code is the Code, and a code of 400, 401, 403, or 404 also
// matches that status's sentinel. The HTTP status stays 2xx, so the call is
// never retried.
func (r call) envelopeError(status int, env envelope) error {
	code := codeText(env.Code)
	effective, _ := strconv.Atoi(code)
	if r.notFound != "" && emptyData(env.Data) {
		return &core.APIError{
			Operation:  r.op,
			StatusCode: status,
			Code:       "NotFound",
			Message:    "vCDN " + r.shortOp() + ": " + r.notFound,
			Err:        core.ErrNotFound,
		}
	}
	if code == "" {
		code = "EnvelopeError"
	}
	msg := ""
	if env.Message != nil {
		msg = *env.Message
	}
	return &core.APIError{
		Operation:  r.op,
		StatusCode: status,
		Code:       code,
		Message:    r.message(effective, msg, true),
		Err:        sentinelFor(effective),
	}
}

// cleanError applies the message rules to a non-2xx error from the
// transport: the fixed 401 and 403 texts, the withheld text for a message
// with an account name in it, and the length cap. A 400 also matches
// ErrInvalidInput. Errors with no HTTP status pass through.
func (r call) cleanError(err error) error {
	var apiErr *core.APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode == 0 {
		return err
	}
	cleaned := *apiErr
	cleaned.Message = r.message(apiErr.StatusCode, apiErr.Message, false)
	if cleaned.Err == nil {
		cleaned.Err = sentinelFor(apiErr.StatusCode)
	}
	return &cleaned
}

// message returns the text an error carries for server message msg and
// effective status. An empty msg becomes the no-reason text when
// noReason is set; a transport message is never empty.
func (r call) message(status int, msg string, noReason bool) string {
	switch {
	case status == http.StatusUnauthorized:
		return rejectedKeyMessage
	case status == http.StatusForbidden:
		return forbiddenKeyMessage
	case strings.Contains(msg, "@"):
		return "vCDN " + r.shortOp() + " refused: the server message named an account user and was withheld; check that every domain is a CDN of this account"
	case strings.TrimSpace(msg) == "" && noReason:
		return "vCDN " + r.shortOp() + " failed; the server gave no reason"
	}
	return limitMessage(msg)
}

// limitMessage removes control characters and cuts msg to maxMessageBytes on
// a rune boundary.
func limitMessage(msg string) string {
	msg = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, msg)
	if len(msg) <= maxMessageBytes {
		return msg
	}
	cut := maxMessageBytes
	for cut > 0 && !isRuneStart(msg[cut]) {
		cut--
	}
	return msg[:cut]
}

func isRuneStart(b byte) bool { return b&0xC0 != 0x80 }

// sentinelFor maps an HTTP status or envelope code to the error sentinel it
// matches. Code 500 and every other value match none.
func sentinelFor(status int) error {
	switch status {
	case http.StatusBadRequest:
		return core.ErrInvalidInput
	case http.StatusUnauthorized:
		return core.ErrAuth
	case http.StatusForbidden:
		return core.ErrPermission
	case http.StatusNotFound:
		return core.ErrNotFound
	}
	return nil
}

// codeText renders an envelope code, a JSON number or string, as text.
func codeText(raw json.RawMessage) string {
	s := strings.TrimSpace(string(raw))
	if s == "" || s == "null" {
		return ""
	}
	if s[0] == '"' {
		var text string
		if json.Unmarshal(raw, &text) != nil {
			return ""
		}
		return text
	}
	return s
}

// emptyData reports whether data is absent, null, or an empty string.
func emptyData(data json.RawMessage) bool {
	s := strings.TrimSpace(string(data))
	return s == "" || s == "null" || s == `""`
}

// decodeList decodes data, a JSON array, into a slice. A null or absent data
// is an empty list when allowNull is set. Anything else is an unexpected
// response.
func decodeList[T any](r call, data json.RawMessage, allowNull bool) ([]T, error) {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" || trimmed == "null" {
		if allowNull {
			return nil, nil
		}
		return nil, r.unexpected(http.StatusOK)
	}
	var items []T
	if err := json.Unmarshal(data, &items); err != nil {
		return nil, r.unexpected(http.StatusOK)
	}
	return items, nil
}

// decodeObject decodes data, a JSON object, into out.
func decodeObject(data json.RawMessage, out any) error {
	return json.Unmarshal(data, out)
}
