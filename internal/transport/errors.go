package transport

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

type APIError struct {
	Operation  string
	StatusCode int
	Code       string
	Message    string
	Retryable  bool
	Err        error
}

func (e *APIError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	if e.Err != nil {
		return e.Err.Error()
	}
	return fmt.Sprintf("request failed with status %d", e.StatusCode)
}

func (e *APIError) Unwrap() error {
	return e.Err
}

// ErrBodyTooLarge is returned by DoJSONStatus and DoRaw when a response body
// exceeds Request.MaxBody. It is never wrapped in an APIError: a caller
// that wants to tell it apart from a genuine HTTP or network failure uses
// errors.Is directly. DoRaw returns it alongside the response's real status
// code, rather than 0, so a caller can tell an oversized body on a 200 from
// one on a 503: the status, not the size, decides what the failure means.
// DoJSONStatus still returns status 0 on this error, since no JSON caller
// reads the status on an error path today.
var ErrBodyTooLarge = errors.New("transport: response body exceeds limit")

func retryableStatus(status int) bool {
	return status == http.StatusTooManyRequests || status == http.StatusBadGateway ||
		status == http.StatusServiceUnavailable || status == http.StatusGatewayTimeout
}

type errorBody struct {
	Code    json.RawMessage `json:"code"`
	Error   string          `json:"error"`
	Message string          `json:"message"`
	Detail  string          `json:"detail"`
}

// codeString renders an envelope code as decimal text: null or an empty
// value gives "", a JSON string gives its value, and a JSON number is
// already decimal text, so it is returned as is.
func codeString(raw json.RawMessage) string {
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

func decodeError(req Request, status int, body []byte) error {
	var eb errorBody
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) > 0 {
		if trimmed[0] == '[' {
			var items []errorBody
			if err := json.Unmarshal(trimmed, &items); err == nil && len(items) > 0 {
				eb = items[0]
			}
		} else {
			_ = json.Unmarshal(trimmed, &eb)
		}
	}
	msg := eb.Message
	if msg == "" {
		msg = eb.Error
	}
	if msg == "" {
		msg = eb.Detail
	}
	if msg == "" {
		msg = http.StatusText(status)
	}

	apiErr := &APIError{
		Operation:  req.Operation,
		StatusCode: status,
		Code:       codeString(eb.Code),
		Message:    strings.TrimSpace(msg),
		Retryable:  req.retryable(status),
	}
	switch status {
	case http.StatusUnauthorized:
		apiErr.Err = errors.New("authentication failed")
	case http.StatusForbidden:
		apiErr.Err = errors.New("permission denied")
	case http.StatusNotFound:
		apiErr.Err = errors.New("resource not found")
	case http.StatusTooManyRequests:
		apiErr.Err = errors.New("rate limited")
	}
	return apiErr
}
