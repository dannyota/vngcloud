package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
)

const DiscoveryMaxBody = 8 << 20

type discoveryCredentialsKey struct{}

type discoveryCredentials struct {
	mu     sync.Mutex
	values map[string]bool
}

// NewDiscoveryContext scopes sent credentials to one discovery invocation.
func NewDiscoveryContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, discoveryCredentialsKey{}, &discoveryCredentials{values: make(map[string]bool)})
}

type discoveryCredentialTransport struct {
	base        http.RoundTripper
	credentials *discoveryCredentials
}

func (t discoveryCredentialTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	credential := strings.TrimPrefix(req.Header.Get("Authorization"), "Bearer ")
	if credential != "" {
		t.credentials.mu.Lock()
		t.credentials.values[credential] = true
		t.credentials.mu.Unlock()
	}
	return t.base.RoundTrip(req)
}

// DoDiscoveryJSONStatus guards each physical exchange before interpreting it.
// before runs after authentication and before every send, including retries.
func (c *Client) DoDiscoveryJSONStatus(ctx context.Context, req Request, out any, before func(context.Context) error) (int, error) {
	credentials, ok := ctx.Value(discoveryCredentialsKey{}).(*discoveryCredentials)
	if !ok {
		ctx = NewDiscoveryContext(ctx)
		credentials = ctx.Value(discoveryCredentialsKey{}).(*discoveryCredentials)
	}
	req.discovery = true
	req.beforeAttempt = before
	req.MaxBody = DiscoveryMaxBody
	req.Sensitive = true
	req.NoRedirect = true
	client := *c.httpClient
	client.Jar = nil
	base := client.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	client.Transport = discoveryCredentialTransport{base: base, credentials: credentials}
	return c.doJSONStatus(ctx, req, out, &client)
}

func beforeDiscoveryAttempt(ctx context.Context, req Request) error {
	if !req.discovery {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if req.beforeAttempt != nil {
		return req.beforeAttempt(ctx)
	}
	return nil
}

func guardDiscoveryResponse(req Request, sent *http.Request, status int, raw []byte) error {
	if !req.discovery {
		return nil
	}
	credentials := sent.Context().Value(discoveryCredentialsKey{}).(*discoveryCredentials)
	credentials.mu.Lock()
	unsafe := unsafeDiscoveryResponse(raw, credentials.values)
	credentials.mu.Unlock()
	if unsafe {
		return &APIError{Operation: req.Operation, StatusCode: status, Code: "InvalidResponse", Message: "invalid discovery response; body withheld"}
	}
	return sent.Context().Err()
}

func unsafeDiscoveryResponse(raw []byte, credentials map[string]bool) bool {
	for credential := range credentials {
		if bytes.Contains(raw, []byte(credential)) {
			return true
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	depth := 0
	for {
		token, err := decoder.Token()
		if err != nil {
			return err != io.EOF || depth != 0
		}
		if delimiter, ok := token.(json.Delim); ok {
			switch delimiter {
			case '{', '[':
				depth++
			case '}', ']':
				depth--
			}
		}
		if value, ok := token.(string); ok {
			for credential := range credentials {
				if strings.Contains(value, credential) {
					return true
				}
			}
		}
	}
}

func (c *Client) refreshToken(ctx context.Context) error {
	c.refreshMu.Lock()
	defer c.refreshMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}

	if !c.currentToken().NeedsRefresh() {
		return nil
	}
	token, err := c.fetchToken(ctx)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	c.token = token
	return nil
}

// invalidateAndRefresh runs the 401 recovery for one request under
// refreshMu: while the client's current token is still the one this request
// sent, it asks the token source to invalidate it, then fetches a
// replacement. It never clears the current token up front, only on success:
// a concurrent goroutine reading currentToken while this call is mid-fetch
// must never observe an empty token and send its own request unauthenticated
// (do's own check refuses that regardless, but not clearing avoids the
// window in the first place). A failed fetch leaves the already-rejected
// token in place; the caller's retry then fails on the same rejection rather
// than on a request sent with no token at all. If another call already
// replaced the token, invalidation is skipped, since invalidating a token
// this request never sent could drop a still-good token another goroutine is
// relying on.
func (c *Client) invalidateAndRefresh(ctx context.Context, sent string) error {
	c.refreshMu.Lock()
	defer c.refreshMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}

	stillCurrent := sent != "" && c.currentToken().AccessToken == sent
	if !stillCurrent && !c.currentToken().NeedsRefresh() {
		return nil
	}
	if stillCurrent {
		if err := c.invalidateContext(ctx, sent); err != nil {
			return err
		}
	}
	token, err := c.fetchToken(ctx)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	c.token = token
	return nil
}

func (c *Client) invalidateContext(ctx context.Context, sent string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if source, ok := c.tokenSource.(interface {
		InvalidateContext(context.Context, string) error
	}); ok {
		return source.InvalidateContext(ctx, sent)
	}
	c.tokenSource.Invalidate(sent)
	return ctx.Err()
}

func (c *Client) invalidateOnceContext(ctx context.Context, sent string) error {
	c.refreshMu.Lock()
	defer c.refreshMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if sent == "" || c.currentToken().AccessToken != sent {
		return nil
	}
	if err := c.invalidateContext(ctx, sent); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	c.token = Token{AccessToken: sent}
	return nil
}
