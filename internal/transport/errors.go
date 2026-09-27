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
		Code:       redact(codeString(eb.Code), req.Redact),
		Message:    redact(strings.TrimSpace(msg), req.Redact),
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

// redactLineMinLen is the shortest trimmed line redact treats as worth
// matching on its own. A shorter line, such as a bare "-----BEGIN" split
// oddly, is common enough in ordinary text that redacting it would corrupt
// unrelated messages; see Request.Redact.
const redactLineMinLen = 8

// redactedText replaces every match in s.
const redactedText = "[redacted]"

// redact returns s with every occurrence of each non-empty value in values
// replaced by "[redacted]": the value itself, its JSON-escaped form (the
// literal text a server produces when it echoes the value's own JSON
// encoding into a message, escape sequences and all, rather than decoding it
// first), and each of its lines that is still at least redactLineMinLen
// characters after trimming surrounding whitespace. An empty value is
// skipped, since it would otherwise match everywhere and corrupt s.
func redact(s string, values []string) string {
	for _, v := range values {
		if v == "" {
			continue
		}
		s = strings.ReplaceAll(s, v, redactedText)
		if escaped := jsonEscapedForm(v); escaped != "" {
			s = strings.ReplaceAll(s, escaped, redactedText)
		}
		for _, line := range strings.Split(v, "\n") {
			trimmed := strings.TrimSpace(line)
			if len(trimmed) >= redactLineMinLen {
				s = strings.ReplaceAll(s, trimmed, redactedText)
			}
		}
	}
	return s
}

// jsonEscapedForm returns the text between the quotes json.Marshal would
// produce for v: v itself with its control characters, quotes, and
// backslashes escaped the way encoding/json escapes them. It returns "" only
// if json.Marshal itself fails, which a plain string value never does.
func jsonEscapedForm(v string) string {
	encoded, err := json.Marshal(v)
	if err != nil || len(encoded) < 2 {
		return ""
	}
	return string(encoded[1 : len(encoded)-1])
}
