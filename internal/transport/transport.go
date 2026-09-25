package transport

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Token struct {
	AccessToken string
	ExpiresAt   time.Time
}

// NeedsRefresh reports whether t should be replaced before use. A zero
// ExpiresAt makes this always true, so a TokenSource that leaves it unset
// has Token called before every request.
func (t Token) NeedsRefresh() bool {
	return t.AccessToken == "" || time.Until(t.ExpiresAt) < 30*time.Second
}

// TokenSource supplies access tokens. Invalidate drops accessToken from
// every cache the source holds, but only while it is still the token the
// source would otherwise hand out; a token another call already replaced is
// left alone. Invalidate is never called with an empty string.
type TokenSource interface {
	Token(ctx context.Context) (Token, error)
	Invalidate(accessToken string)
}

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

type Client struct {
	httpClient    *http.Client
	tokenSource   TokenSource
	retryCount    int
	retryInterval time.Duration
	userAgent     string
	capture       CaptureFunc

	mu        sync.RWMutex
	token     Token
	refreshMu sync.Mutex
}

type Capture struct {
	Operation  string
	Method     string
	URL        string
	StatusCode int
	Body       []byte
}

type CaptureFunc func(Capture)

type Config struct {
	HTTPClient    *http.Client
	TokenSource   TokenSource
	RetryCount    int
	RetryInterval time.Duration
	UserAgent     string
	Capture       CaptureFunc
}

func New(cfg Config) *Client {
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 120 * time.Second}
	}
	retryCount := cfg.RetryCount
	if retryCount < 0 {
		retryCount = 0
	}
	retryInterval := cfg.RetryInterval
	if retryInterval <= 0 {
		retryInterval = time.Second
	}
	return &Client{
		httpClient:    httpClient,
		tokenSource:   cfg.TokenSource,
		retryCount:    retryCount,
		retryInterval: retryInterval,
		userAgent:     cfg.UserAgent,
		capture:       cfg.Capture,
	}
}

type Request struct {
	Operation string
	Method    string
	URL       string
	Headers   map[string]string
	Body      any
	OK        []int
	SkipAuth  bool

	// Idempotent marks a request as safe to retry after a failure that may
	// have already reached the server. GET, HEAD, PUT, and DELETE are
	// idempotent regardless of this field; POST and PATCH are not unless it
	// is set true, which a caller does only when the call has no side
	// effect, such as a price quote.
	Idempotent bool
}

// idempotent reports whether req may be retried after an ambiguous failure.
func (r Request) idempotent() bool {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodPut, http.MethodDelete:
		return true
	default:
		return r.Idempotent
	}
}

func (c *Client) DoJSON(ctx context.Context, req Request, out any) error {
	_, err := c.DoJSONStatus(ctx, req, out)
	return err
}

// DoJSONStatus is DoJSON but also returns the final HTTP status, including
// on error, so a caller can build an error envelope that names the actual
// status a 2xx response carried.
func (c *Client) DoJSONStatus(ctx context.Context, req Request, out any) (int, error) {
	if req.Method == "" {
		req.Method = http.MethodGet
	}
	if len(req.OK) == 0 {
		req.OK = []int{http.StatusOK}
	}
	if !req.SkipAuth {
		if err := c.EnsureToken(ctx); err != nil {
			return 0, err
		}
		// A nil tokenSource means this Client was built without any
		// authentication at all (test wiring); it sends unauthenticated
		// requests on purpose. A configured source that yields an empty
		// token, in contrast, must never let the request go out.
		if c.tokenSource != nil && c.currentToken().AccessToken == "" {
			return 0, &APIError{Operation: req.Operation, StatusCode: http.StatusUnauthorized, Err: errNoToken}
		}
	}

	statusCode, body, sent, err := c.do(ctx, req)
	if err != nil {
		return 0, err
	}
	if statusCode == http.StatusUnauthorized && !req.SkipAuth && c.tokenSource != nil {
		if err := c.invalidateAndRefresh(ctx, sent); err != nil {
			return 0, err
		}
		statusCode, body, _, err = c.do(ctx, req)
		if err != nil {
			return 0, err
		}
	}

	if !containsStatus(req.OK, statusCode) {
		return statusCode, decodeError(req, statusCode, body)
	}
	if out != nil && len(body) > 0 {
		if err := json.Unmarshal(body, out); err != nil {
			return statusCode, &APIError{Operation: req.Operation, Err: err}
		}
	}
	return statusCode, nil
}

// errNoToken backs the synthetic 401 DoJSONStatus returns when EnsureToken
// leaves the token empty, so an authenticated request is never sent without
// one. It carries StatusCode so the caller's status-to-sentinel mapping
// (401 -> ErrAuth) applies exactly as it would for a real 401 response.
var errNoToken = errors.New("no access token available")

const maxRetryDelay = 30 * time.Second

func sleepContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// backoff doubles the base interval per attempt with equal jitter so
// concurrent clients do not retry in lockstep. A server Retry-After hint
// wins over the computed value; both are capped at maxRetryDelay.
func (c *Client) backoff(attempt int, retryAfter time.Duration) time.Duration {
	if retryAfter > 0 {
		if retryAfter > maxRetryDelay {
			return maxRetryDelay
		}
		return retryAfter
	}
	// Shift may overflow negative for very large attempt values; the <= 0
	// guard below caps it to maxRetryDelay intentionally.
	d := c.retryInterval << attempt
	if d <= 0 || d > maxRetryDelay {
		d = maxRetryDelay
	}
	half := d / 2
	return half + jitter(half)
}

// jitter returns a uniform duration in [0, limit].
func jitter(limit time.Duration) time.Duration {
	n, err := rand.Int(rand.Reader, big.NewInt(int64(limit)+1))
	if err != nil {
		return limit
	}
	return time.Duration(n.Int64())
}

func retryAfterHint(h http.Header) time.Duration {
	value := h.Get("Retry-After")
	if value == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(value); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	if when, err := http.ParseTime(value); err == nil {
		if d := time.Until(when); d > 0 {
			return d
		}
	}
	return 0
}

// do sends req, retrying per the idempotency and status rules below. Its
// third return value is the exact access token placed in the Authorization
// header for the final attempt (empty when SkipAuth is set or no token was
// available), so a caller handling a 401 knows exactly which token to
// invalidate.
func (c *Client) do(ctx context.Context, req Request) (int, []byte, string, error) {
	body, err := jsonBody(req.Body)
	if err != nil {
		return 0, nil, "", &APIError{Operation: req.Operation, Err: err}
	}

	var lastErr error
	var lastRetryable bool
	var sentToken string
	for attempt := 0; attempt <= c.retryCount; attempt++ {
		httpReq, err := http.NewRequestWithContext(ctx, req.Method, req.URL, bytes.NewReader(body))
		if err != nil {
			return 0, nil, "", &APIError{Operation: req.Operation, Err: err}
		}
		if req.Body != nil {
			httpReq.Header.Set("Content-Type", "application/json")
		}
		httpReq.Header.Set("Accept", "application/json")
		if c.userAgent != "" {
			httpReq.Header.Set("User-Agent", c.userAgent)
		}
		for key, value := range req.Headers {
			if key == "" || value == "" {
				continue
			}
			httpReq.Header.Set(key, value)
		}
		if !req.SkipAuth {
			sentToken = c.currentToken().AccessToken
			if sentToken != "" {
				httpReq.Header.Set("Authorization", "Bearer "+sentToken)
			}
		}

		resp, err := c.httpClient.Do(httpReq)
		if err != nil {
			lastErr = err
			lastRetryable = req.idempotent() || isDialError(err)
			if lastRetryable && attempt < c.retryCount {
				if serr := sleepContext(ctx, c.backoff(attempt, 0)); serr != nil {
					return 0, nil, sentToken, &APIError{Operation: req.Operation, Err: serr}
				}
				continue
			}
			return 0, nil, sentToken, &APIError{Operation: req.Operation, Retryable: retryableForContext(ctx, lastRetryable), Err: err}
		}

		respBody, readErr := io.ReadAll(resp.Body)
		closeErr := resp.Body.Close()
		if readErr != nil {
			return 0, nil, sentToken, &APIError{Operation: req.Operation, Err: readErr}
		}
		if closeErr != nil {
			return 0, nil, sentToken, &APIError{Operation: req.Operation, Err: closeErr}
		}

		// A non-idempotent request retries only on 429: the server has not
		// acted on it, unlike a 502/503/504 that may have reached a handler.
		retryStatus := resp.StatusCode == http.StatusTooManyRequests ||
			(req.idempotent() && retryableStatus(resp.StatusCode))
		if retryStatus && attempt < c.retryCount {
			if serr := sleepContext(ctx, c.backoff(attempt, retryAfterHint(resp.Header))); serr != nil {
				return 0, nil, sentToken, &APIError{Operation: req.Operation, Err: serr}
			}
			continue
		}
		c.captureResponse(req, resp.StatusCode, respBody)
		return resp.StatusCode, respBody, sentToken, nil
	}

	return 0, nil, sentToken, &APIError{Operation: req.Operation, Retryable: retryableForContext(ctx, lastRetryable), Err: lastErr}
}

// retryableForContext reports retryable, unless ctx is already done: a
// canceled or expired context is why the request failed, not a transient
// server or network condition, so it is never worth a caller retrying.
func retryableForContext(ctx context.Context, retryable bool) bool {
	if ctx.Err() != nil {
		return false
	}
	return retryable
}

// isDialError reports whether err is a failed dial, found by unwrapping
// through the *url.Error that http.Client.Do returns. The server cannot
// have received a request that never established a connection, so a dial
// failure is safe to retry even for a non-idempotent request.
func isDialError(err error) bool {
	var opErr *net.OpError
	return errors.As(err, &opErr) && opErr.Op == "dial"
}

func (c *Client) captureResponse(req Request, statusCode int, body []byte) {
	if c.capture == nil {
		return
	}
	bodyCopy := append([]byte(nil), body...)
	c.capture(Capture{
		Operation:  req.Operation,
		Method:     req.Method,
		URL:        req.URL,
		StatusCode: statusCode,
		Body:       bodyCopy,
	})
}

// EnsureToken fetches and caches a token if the current one is missing or
// near expiry. Safe for concurrent use.
func (c *Client) EnsureToken(ctx context.Context) error {
	if c.tokenSource == nil {
		return nil
	}
	if !c.currentToken().NeedsRefresh() {
		return nil
	}
	return c.refreshToken(ctx)
}

func (c *Client) refreshToken(ctx context.Context) error {
	c.refreshMu.Lock()
	defer c.refreshMu.Unlock()

	if !c.currentToken().NeedsRefresh() {
		return nil
	}
	token, err := c.tokenSource.Token(ctx)
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.token = token
	c.mu.Unlock()
	return nil
}

// invalidateAndRefresh runs the 401 recovery for one request under
// refreshMu: while the client's current token is still the one this request
// sent, it clears that token and asks the token source to invalidate it too.
// If another call already replaced the token, both steps are skipped, since
// invalidating a token this request never sent could drop a still-good
// token another goroutine is relying on. Either way, it leaves a usable
// token in place (fetching one if needed) before the caller retries.
func (c *Client) invalidateAndRefresh(ctx context.Context, sent string) error {
	c.refreshMu.Lock()
	defer c.refreshMu.Unlock()

	if sent != "" && c.currentToken().AccessToken == sent {
		c.clearToken()
		c.tokenSource.Invalidate(sent)
	}
	if !c.currentToken().NeedsRefresh() {
		return nil
	}
	token, err := c.tokenSource.Token(ctx)
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.token = token
	c.mu.Unlock()
	return nil
}

func (c *Client) clearToken() {
	c.mu.Lock()
	c.token = Token{}
	c.mu.Unlock()
}

func (c *Client) currentToken() Token {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.token
}

func jsonBody(v any) ([]byte, error) {
	if v == nil {
		return nil, nil
	}
	return json.Marshal(v)
}

func containsStatus(statuses []int, status int) bool {
	for _, candidate := range statuses {
		if candidate == status {
			return true
		}
	}
	return false
}

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
		Retryable:  status == http.StatusTooManyRequests || (req.idempotent() && retryableStatus(status)),
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
