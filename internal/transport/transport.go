package transport

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"strconv"
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

type Client struct {
	httpClient    *http.Client
	tokenSource   TokenSource
	retryCount    int
	retryInterval time.Duration
	userAgent     string
	capture       CaptureFunc
	logger        *slog.Logger

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

	// Logger, when set, receives one Debug record named "request" per HTTP
	// attempt, with the method, the URL path without its query string, the
	// status (omitted when no response was received), and the duration. A
	// nil Logger logs nothing.
	Logger *slog.Logger
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
		logger:        cfg.Logger,
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

	// MaxBody caps the response body at MaxBody bytes; zero means unlimited.
	// The transport reads at most MaxBody+1 bytes, so it can tell a body
	// that exactly fills the cap from one that overflows it: reading that
	// many means the real body is larger, and the call fails with
	// ErrBodyTooLarge instead of returning a partial body.
	MaxBody int64

	// Once sends req at most once, overriding every retry rule Idempotent
	// and the method would otherwise apply: no retry after any status or
	// network error, including 429 and a failed dial, and no resend after a
	// 401 (the token is still invalidated, as for any other request). It is
	// for a toggle write that must not be sent twice; see ADR 0003. The
	// resulting APIError.Retryable is true only for a 429 or a failed dial,
	// the two cases where the server provably never acted, never for a 5xx
	// or a non-dial network error, which Once still sent only once but which
	// may have reached a handler.
	Once bool
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

// retryable reports whether a response with status is one a caller should
// itself consider safe to retry by rerunning the whole operation, for the
// APIError.Retryable field. A 429 always is: the server never acted on it,
// whatever the method or Once. Once narrows this to 429 alone, since a 5xx
// on the single attempt it allows may have reached a handler, unlike the
// ordinary rule that also trusts an idempotent method's 5xx.
func (r Request) retryable(status int) bool {
	if status == http.StatusTooManyRequests {
		return true
	}
	if r.Once {
		return false
	}
	return r.idempotent() && retryableStatus(status)
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

	statusCode, _, body, err := c.doAuthenticated(ctx, req, c.httpClient)
	if err != nil {
		return 0, err
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

// DoRaw sends req and returns the response's status, its Content-Type
// header, and the raw body, without decoding JSON and without treating any
// status as an error. It never rides a cookie: it sends req through a copy
// of the underlying *http.Client with Jar cleared (see rawClient), so a
// caller-configured cookie jar never reaches the request, and it enforces
// the SDK's same-host redirect rule even when the underlying client is one
// the caller supplied. It otherwise applies the same retry policy and, when
// req.SkipAuth is not set, the same token handling as DoJSONStatus.
func (c *Client) DoRaw(ctx context.Context, req Request) (int, string, []byte, error) {
	if req.Method == "" {
		req.Method = http.MethodGet
	}
	return c.doAuthenticated(ctx, req, c.rawClient())
}

// doAuthenticated attaches a token to req (unless req.SkipAuth is set),
// sends it through client with send's retry policy, and retries once more
// with a refreshed token after a 401 that req did not opt out of
// authentication for. It returns the final status, Content-Type header, and
// raw body.
func (c *Client) doAuthenticated(ctx context.Context, req Request, client *http.Client) (int, string, []byte, error) {
	if !req.SkipAuth {
		if err := c.EnsureToken(ctx); err != nil {
			return 0, "", nil, err
		}
		// A nil tokenSource means this Client was built without any
		// authentication at all (test wiring); it sends unauthenticated
		// requests on purpose. A configured source that yields an empty
		// token, in contrast, must never let the request go out.
		if c.tokenSource != nil && c.currentToken().AccessToken == "" {
			return 0, "", nil, &APIError{Operation: req.Operation, StatusCode: http.StatusUnauthorized, Err: errNoToken}
		}
	}

	statusCode, contentType, body, sent, err := c.send(ctx, req, client)
	if err != nil {
		// statusCode carries a real value only for ErrBodyTooLarge (see its
		// doc comment); every other error path in send leaves it 0.
		return statusCode, "", nil, err
	}
	if statusCode == http.StatusUnauthorized && !req.SkipAuth && c.tokenSource != nil {
		if req.Once {
			// ADR 0003 rule 3: invalidate the sent token as usual, but never
			// resend. The caller gets this 401 back; a later call, Once or
			// not, fetches a fresh token instead of reusing the rejected one.
			c.invalidateOnce(sent)
			return statusCode, contentType, body, nil
		}
		if err := c.invalidateAndRefresh(ctx, sent); err != nil {
			return 0, "", nil, err
		}
		statusCode, contentType, body, _, err = c.send(ctx, req, client)
		if err != nil {
			return statusCode, "", nil, err
		}
	}
	return statusCode, contentType, body, nil
}

// rawClient returns a copy of c.httpClient with Jar cleared, so a request
// sent through it carries no cookie even when the configured client has a
// cookie jar. The copy's CheckRedirect enforces the SDK's same-host, at
// most 10 hops rule first, then calls the original client's own
// CheckRedirect, if it had one: a caller-supplied client that never set
// CheckRedirect at all otherwise follows a redirect to any host, which
// DoRaw must never do.
func (c *Client) rawClient() *http.Client {
	cp := *c.httpClient
	cp.Jar = nil
	inner := c.httpClient.CheckRedirect
	cp.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return errors.New("stopped after 10 redirects")
		}
		if req.URL.Host != via[0].URL.Host {
			return fmt.Errorf("redirected from %q to %q: cross-host redirect refused", via[0].URL.Host, req.URL.Host)
		}
		if inner != nil {
			return inner(req, via)
		}
		return nil
	}
	return &cp
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

// send sends req through client, retrying per the idempotency and status
// rules below. Its third return value is the raw body and its fourth is the
// exact access token placed in the Authorization header for the final
// attempt (empty when SkipAuth is set or no token was available), so a
// caller handling a 401 knows exactly which token to invalidate.
func (c *Client) send(ctx context.Context, req Request, client *http.Client) (int, string, []byte, string, error) {
	body, err := jsonBody(req.Body)
	if err != nil {
		return 0, "", nil, "", &APIError{Operation: req.Operation, Err: err}
	}

	// maxAttempts is the last attempt index the loop below may reach before
	// it must return whatever it has. Once forces it to 0: exactly one
	// attempt, whatever the response, per ADR 0003 rule 3.
	maxAttempts := c.retryCount
	if req.Once {
		maxAttempts = 0
	}

	var lastErr error
	var lastRetryable bool
	var sentToken string
	for attempt := 0; attempt <= maxAttempts; attempt++ {
		start := time.Now()
		httpReq, err := http.NewRequestWithContext(ctx, req.Method, req.URL, bytes.NewReader(body))
		if err != nil {
			return 0, "", nil, "", &APIError{Operation: req.Operation, Err: err}
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
			if sentToken == "" && c.tokenSource != nil {
				// A configured token source with nothing to hand out: never
				// send this request unauthenticated on its behalf.
				return 0, "", nil, "", &APIError{Operation: req.Operation, StatusCode: http.StatusUnauthorized, Err: errNoToken}
			}
			if sentToken != "" {
				httpReq.Header.Set("Authorization", "Bearer "+sentToken)
			}
		}

		resp, err := client.Do(httpReq)
		duration := time.Since(start)
		if err != nil {
			c.logRequest(ctx, httpReq, 0, false, duration)
			lastErr = err
			if req.Once {
				// A failed dial never reached the server, so rerunning the
				// whole operation is safe even though this one attempt is
				// not retried; any other network failure may have reached a
				// handler after connecting, so Once never marks it retryable.
				lastRetryable = isDialError(err)
			} else {
				lastRetryable = req.idempotent() || isDialError(err)
			}
			if lastRetryable && attempt < maxAttempts {
				if serr := sleepContext(ctx, c.backoff(attempt, 0)); serr != nil {
					return 0, "", nil, sentToken, &APIError{Operation: req.Operation, Err: serr}
				}
				continue
			}
			return 0, "", nil, sentToken, &APIError{Operation: req.Operation, Retryable: retryableForContext(ctx, lastRetryable), Err: err}
		}
		c.logRequest(ctx, httpReq, resp.StatusCode, true, duration)

		respBody, readErr := readBody(resp.Body, req.MaxBody)
		closeErr := resp.Body.Close()
		if errors.Is(readErr, ErrBodyTooLarge) {
			return resp.StatusCode, "", nil, sentToken, ErrBodyTooLarge
		}
		if readErr != nil {
			return 0, "", nil, sentToken, &APIError{Operation: req.Operation, Err: readErr}
		}
		if closeErr != nil {
			return 0, "", nil, sentToken, &APIError{Operation: req.Operation, Err: closeErr}
		}

		// A non-idempotent request retries only on 429: the server has not
		// acted on it, unlike a 502/503/504 that may have reached a handler.
		// maxAttempts already forces this to never fire for Once.
		retryStatus := resp.StatusCode == http.StatusTooManyRequests ||
			(req.idempotent() && retryableStatus(resp.StatusCode))
		if retryStatus && attempt < maxAttempts {
			if serr := sleepContext(ctx, c.backoff(attempt, retryAfterHint(resp.Header))); serr != nil {
				return 0, "", nil, sentToken, &APIError{Operation: req.Operation, Err: serr}
			}
			continue
		}
		c.captureResponse(req, resp.StatusCode, respBody)
		return resp.StatusCode, resp.Header.Get("Content-Type"), respBody, sentToken, nil
	}

	return 0, "", nil, sentToken, &APIError{Operation: req.Operation, Retryable: retryableForContext(ctx, lastRetryable), Err: lastErr}
}

// readBody reads r fully when maxBody is not positive. Otherwise it reads at
// most maxBody+1 bytes: reading that many means the real body is larger than
// maxBody, and it returns ErrBodyTooLarge instead of a partial body.
func readBody(r io.Reader, maxBody int64) ([]byte, error) {
	if maxBody <= 0 {
		return io.ReadAll(r)
	}
	data, err := io.ReadAll(io.LimitReader(r, maxBody+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBody {
		return nil, ErrBodyTooLarge
	}
	return data, nil
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

// logRequest writes the one Debug "request" record for one HTTP attempt:
// httpReq's method and URL path with no query string, its status (omitted
// when hasStatus is false, for an attempt that never got a response), and
// duration. Nothing else about the attempt, such as its headers, query
// string, or body, is ever logged. A nil logger logs nothing.
func (c *Client) logRequest(ctx context.Context, httpReq *http.Request, status int, hasStatus bool, duration time.Duration) {
	if c.logger == nil {
		return
	}
	attrs := []any{"method", httpReq.Method, "path", httpReq.URL.Path}
	if hasStatus {
		attrs = append(attrs, "status", status)
	}
	attrs = append(attrs, "duration", duration)
	c.logger.DebugContext(ctx, "request", attrs...)
}

// EnsureToken fetches and caches a token if the current one is missing or
// near expiry, failing with the no-token APIError (ErrAuth at the core
// layer) if the token source cannot produce a usable one. Safe for
// concurrent use.
func (c *Client) EnsureToken(ctx context.Context) error {
	if c.tokenSource == nil {
		return nil
	}
	if !c.currentToken().NeedsRefresh() {
		return nil
	}
	return c.refreshToken(ctx)
}

// fetchToken calls tokenSource.Token and treats a returned empty AccessToken
// (nil error) as a failure, the same as a real error: neither refreshToken
// nor invalidateAndRefresh may swap the client's live token for one do could
// not safely send.
func (c *Client) fetchToken(ctx context.Context) (Token, error) {
	token, err := c.tokenSource.Token(ctx)
	if err != nil {
		return Token{}, err
	}
	if token.AccessToken == "" {
		return Token{}, &APIError{StatusCode: http.StatusUnauthorized, Err: errNoToken}
	}
	return token, nil
}

func (c *Client) refreshToken(ctx context.Context) error {
	c.refreshMu.Lock()
	defer c.refreshMu.Unlock()

	if !c.currentToken().NeedsRefresh() {
		return nil
	}
	token, err := c.fetchToken(ctx)
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

	stillCurrent := sent != "" && c.currentToken().AccessToken == sent
	if !stillCurrent && !c.currentToken().NeedsRefresh() {
		return nil
	}
	if stillCurrent {
		c.tokenSource.Invalidate(sent)
	}
	token, err := c.fetchToken(ctx)
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.token = token
	c.mu.Unlock()
	return nil
}

// invalidateOnce drops sent from every cache the token source holds, the
// same as invalidateAndRefresh's own invalidation step, but never fetches a
// replacement: a Once request that hits a 401 is never resent, so there is
// nothing to fetch a replacement token for. It also clears the token from
// this Client's own in-memory cache, so a later call, on this Client, never
// resends the same rejected token; that later call fetches a fresh one
// through the ordinary EnsureToken path instead. Invalidation runs only
// while sent is still the Client's current token, the same guard
// invalidateAndRefresh uses, so a token another goroutine already replaced
// is left alone.
func (c *Client) invalidateOnce(sent string) {
	c.refreshMu.Lock()
	defer c.refreshMu.Unlock()

	if sent == "" || c.currentToken().AccessToken != sent {
		return
	}
	c.tokenSource.Invalidate(sent)
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
