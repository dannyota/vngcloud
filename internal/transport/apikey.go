package transport

import (
	"bytes"
	"errors"
)

// errAPIKeyWithSkipAuth is the programming error for a request that asks for
// no credential and an API key at once. It never holds the key.
var errAPIKeyWithSkipAuth = errors.New("transport: APIKey and SkipAuth are mutually exclusive")

// usesToken reports whether req carries the IAM token: neither SkipAuth nor
// APIKey is set. An APIKey request never asks the token source for a token,
// never invalidates one, and never resends after a 401, since the key is
// fixed and a resend cannot succeed.
func (r Request) usesToken() bool {
	return !r.SkipAuth && r.APIKey == ""
}

// checkAPIKey fails a request that sets both SkipAuth and APIKey, before any
// request is sent.
func (r Request) checkAPIKey() error {
	if r.SkipAuth && r.APIKey != "" {
		return &APIError{Operation: r.Operation, Err: errAPIKeyWithSkipAuth}
	}
	return nil
}

// redactValues is Redact plus the API key, so no error or captured body
// echoes the key.
func (r Request) redactValues() []string {
	if r.APIKey == "" {
		return r.Redact
	}
	return append(append([]string(nil), r.Redact...), r.APIKey)
}

// redactBody copies the body and scrubs credentials and explicit secrets.
func (r Request) redactBody(body []byte) []byte {
	values := r.redactValues()
	hasSecret := false
	for _, value := range values {
		hasSecret = hasSecret || value != ""
	}
	if !hasSecret {
		return append([]byte(nil), body...)
	}
	if cleaned, ok := redactJSONCapture(body, values); ok {
		return cleaned
	}
	if bytes.Contains(body, []byte(`\u`)) {
		return []byte(redactedText)
	}
	return []byte(redact(string(body), values))
}

// RedactValues returns s with every occurrence of each secret in values
// replaced by "[redacted]". Callers that build an error from a 2xx body use
// it because successful response decoding leaves the body unchanged.
func RedactValues(s string, values ...string) string {
	return redact(s, values)
}
