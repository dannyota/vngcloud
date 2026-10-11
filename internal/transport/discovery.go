package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
)

const DiscoveryMaxBody = 8 << 20

// DoDiscoveryJSONStatus guards each physical exchange before interpreting it.
// before runs after authentication and before every send, including retries.
func (c *Client) DoDiscoveryJSONStatus(ctx context.Context, req Request, out any, before func(context.Context) error) (int, error) {
	req.discovery = true
	req.beforeAttempt = before
	req.MaxBody = DiscoveryMaxBody
	req.Sensitive = true
	req.NoRedirect = true
	client := *c.httpClient
	client.Jar = nil
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
	credential := strings.TrimPrefix(sent.Header.Get("Authorization"), "Bearer ")
	if reflectsCredential(raw, credential) {
		return &APIError{Operation: req.Operation, StatusCode: status, Code: "InvalidResponse", Message: "response reflected credential; body withheld"}
	}
	return sent.Context().Err()
}

func reflectsCredential(raw []byte, credential string) bool {
	if credential == "" {
		return false
	}
	if bytes.Contains(raw, []byte(credential)) {
		return true
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	for {
		token, err := decoder.Token()
		if err != nil {
			return false
		}
		if value, ok := token.(string); ok && strings.Contains(value, credential) {
			return true
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
