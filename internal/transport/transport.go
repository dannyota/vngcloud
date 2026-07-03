package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
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

func (t Token) NeedsRefresh() bool {
	return t.AccessToken == "" || time.Until(t.ExpiresAt) < 30*time.Second
}

type TokenSource interface {
	Token(ctx context.Context) (Token, error)
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
}

func (c *Client) DoJSON(ctx context.Context, req Request, out any) error {
	if req.Method == "" {
		req.Method = http.MethodGet
	}
	if len(req.OK) == 0 {
		req.OK = []int{http.StatusOK}
	}
	if !req.SkipAuth {
		if err := c.ensureToken(ctx); err != nil {
			return err
		}
	}

	statusCode, body, err := c.do(ctx, req)
	if err != nil {
		return err
	}
	if statusCode == http.StatusUnauthorized && !req.SkipAuth && c.tokenSource != nil {
		c.clearToken()
		if err := c.refreshToken(ctx); err != nil {
			return err
		}
		statusCode, body, err = c.do(ctx, req)
		if err != nil {
			return err
		}
	}

	if !containsStatus(req.OK, statusCode) {
		return decodeError(req.Operation, statusCode, body)
	}
	if out != nil && len(body) > 0 {
		if err := json.Unmarshal(body, out); err != nil {
			return &APIError{Operation: req.Operation, Err: err}
		}
	}
	return nil
}

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
	d := c.retryInterval << attempt
	if d <= 0 || d > maxRetryDelay {
		d = maxRetryDelay
	}
	half := d / 2
	return half + rand.N(half+1) //nolint:gosec // retry jitter does not need cryptographic randomness
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

func (c *Client) do(ctx context.Context, req Request) (int, []byte, error) {
	body, err := jsonBody(req.Body)
	if err != nil {
		return 0, nil, &APIError{Operation: req.Operation, Err: err}
	}

	var lastErr error
	for attempt := 0; attempt <= c.retryCount; attempt++ {
		httpReq, err := http.NewRequestWithContext(ctx, req.Method, req.URL, bytes.NewReader(body))
		if err != nil {
			return 0, nil, &APIError{Operation: req.Operation, Err: err}
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
			if token := c.currentToken(); token.AccessToken != "" {
				httpReq.Header.Set("Authorization", "Bearer "+token.AccessToken)
			}
		}

		resp, err := c.httpClient.Do(httpReq)
		if err != nil {
			lastErr = err
			if attempt < c.retryCount {
				if serr := sleepContext(ctx, c.backoff(attempt, 0)); serr != nil {
					return 0, nil, &APIError{Operation: req.Operation, Err: serr}
				}
				continue
			}
			return 0, nil, &APIError{Operation: req.Operation, Retryable: true, Err: err}
		}

		respBody, readErr := io.ReadAll(resp.Body)
		closeErr := resp.Body.Close()
		if readErr != nil {
			return 0, nil, &APIError{Operation: req.Operation, Err: readErr}
		}
		if closeErr != nil {
			return 0, nil, &APIError{Operation: req.Operation, Err: closeErr}
		}

		if retryableStatus(resp.StatusCode) && attempt < c.retryCount {
			if serr := sleepContext(ctx, c.backoff(attempt, retryAfterHint(resp.Header))); serr != nil {
				return 0, nil, &APIError{Operation: req.Operation, Err: serr}
			}
			continue
		}
		c.captureResponse(req, resp.StatusCode, respBody)
		return resp.StatusCode, respBody, nil
	}

	return 0, nil, &APIError{Operation: req.Operation, Retryable: true, Err: lastErr}
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

func (c *Client) ensureToken(ctx context.Context) error {
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
	Code    string `json:"code"`
	Error   string `json:"error"`
	Message string `json:"message"`
	Detail  string `json:"detail"`
}

func decodeError(operation string, status int, body []byte) error {
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
		Operation:  operation,
		StatusCode: status,
		Code:       eb.Code,
		Message:    strings.TrimSpace(msg),
		Retryable:  retryableStatus(status),
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
