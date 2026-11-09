package transport

import (
	"context"
	"net/http"
	"slices"
)

type credentialObserverKey struct{}

// WithCredentialObserver observes each attempted credential before sending it.
// The observer returns all credentials retained by the operation, or an error
// to prevent the send. Guarded requests suppress captures and decode details,
// and use the retained credentials to redact errors and debug output.
// An empty credential requests a snapshot without recording a send.
// Contexts without an observer keep the normal transport behavior.
func WithCredentialObserver(ctx context.Context, observe func(string) ([]string, error)) context.Context {
	return context.WithValue(ctx, credentialObserverKey{}, observe)
}

func (r *Request) observeCredential(ctx context.Context, credential string) error {
	observe, ok := ctx.Value(credentialObserverKey{}).(func(string) ([]string, error))
	if !ok || observe == nil {
		return nil
	}
	values, err := observe(credential)
	if err != nil {
		return &APIError{Operation: r.Operation, Code: "InvalidResponse", Message: "credential observation failed; response withheld", Err: err}
	}
	// Later credentials can match earlier response fields. Suppress captures
	// throughout the guarded operation, including its nested pricing calls.
	r.Sensitive = true
	r.Redact = append([]string(nil), r.Redact...)
	for _, value := range values {
		if value != "" && !slices.Contains(r.Redact, value) {
			r.Redact = append(r.Redact, value)
		}
	}
	return nil
}

func (r *Request) resetSentCredentials() {
	if r.SentCredential != nil {
		*r.SentCredential = ""
	}
	if r.SentCredentials == nil {
		r.SentCredentials = new([]string)
	} else {
		*r.SentCredentials = nil
	}
}

func (r Request) recordSentCredential(credential string) {
	if r.SentCredential != nil {
		*r.SentCredential = credential
	}
	if credential != "" && !slices.Contains(*r.SentCredentials, credential) {
		*r.SentCredentials = append(*r.SentCredentials, credential)
	}
}

// DoRaw sends req and returns the response's status, its Content-Type
// header, and the raw body, without decoding JSON and without treating any
// status as an error. It never rides a cookie: it sends req through a copy
// of the underlying *http.Client with Jar cleared, so a caller-configured
// cookie jar never reaches the request. The shared send path enforces
// the SDK's same-scheme, same-host redirect rule even when the client is one
// the caller supplied. It otherwise applies the same retry policy and, when
// req.SkipAuth is not set, the same token handling as DoJSONStatus.
func (c *Client) DoRaw(ctx context.Context, req Request) (int, string, []byte, error) {
	if req.Method == "" {
		req.Method = http.MethodGet
	}
	req.resetSentCredentials()
	if err := req.observeCredential(ctx, ""); err != nil {
		return 0, "", nil, err
	}
	status, contentType, body, err := c.doAuthenticated(ctx, req, c.rawClient())
	return status, contentType, body, err
}
