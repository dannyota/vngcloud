package transport

import "errors"

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

// redactBody returns a copy of body for the capture hook. An APIKey request
// has the key scrubbed from it, since a server may echo the key in an error
// body.
func (r Request) redactBody(body []byte) []byte {
	if r.APIKey == "" {
		return append([]byte(nil), body...)
	}
	return []byte(redact(string(body), r.redactValues()))
}
