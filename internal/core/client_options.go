package core

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"danny.vn/vngcloud/internal/transport"
)

// Token is an access token and the time it expires. It aliases
// transport.Token, so any CredentialsProvider is directly usable as the
// transport layer's token source with no adapter.
type Token = transport.Token

// CredentialsProvider supplies access tokens for authenticated requests. A
// custom provider must return a non-empty AccessToken with a nil error; an
// empty token with a nil error is treated as ErrAuth. A zero ExpiresAt makes
// the SDK call Token before every request. Invalidate drops accessToken from
// the provider's own cache, but only while it is still the token the
// provider would otherwise hand out; it is never called with an empty
// string.
type CredentialsProvider interface {
	Token(ctx context.Context) (Token, error)
	Invalidate(accessToken string)
}

type ClientOption interface {
	apply(*clientConfig)
}

// Option is the public name for ClientOption; NewConfig takes Options.
type Option = ClientOption

type clientOptionFunc func(*clientConfig)

func (f clientOptionFunc) apply(cfg *clientConfig) {
	f(cfg)
}

type clientConfig struct {
	httpClient    *http.Client
	transport     http.RoundTripper
	timeout       time.Duration
	retryCount    int
	retryInterval time.Duration
	userAgent     string
	logger        *slog.Logger
	endpoints     EndpointOverrides
	capture       ResponseCaptureFunc
	staticToken   string
	region        string
	projectID     string
	iamUser       *IAMUserAuth
	credentials   CredentialsProvider
}

type ResponseCapture struct {
	Operation  string
	Method     string
	URL        string
	StatusCode int
	Body       []byte
}

type ResponseCaptureFunc func(ResponseCapture)

func WithHTTPClient(c *http.Client) ClientOption {
	return clientOptionFunc(func(cfg *clientConfig) {
		cfg.httpClient = c
	})
}

func WithTransport(t http.RoundTripper) ClientOption {
	return clientOptionFunc(func(cfg *clientConfig) {
		cfg.transport = t
	})
}

func WithTimeout(timeout time.Duration) ClientOption {
	return clientOptionFunc(func(cfg *clientConfig) {
		cfg.timeout = timeout
	})
}

func WithRetry(count int, interval time.Duration) ClientOption {
	return clientOptionFunc(func(cfg *clientConfig) {
		cfg.retryCount = count
		cfg.retryInterval = interval
	})
}

func WithUserAgent(userAgent string) ClientOption {
	return clientOptionFunc(func(cfg *clientConfig) {
		cfg.userAgent = userAgent
	})
}

func WithLogger(logger *slog.Logger) ClientOption {
	return clientOptionFunc(func(cfg *clientConfig) {
		cfg.logger = logger
	})
}

func WithEndpointOverrides(overrides EndpointOverrides) ClientOption {
	return clientOptionFunc(func(cfg *clientConfig) {
		cfg.endpoints = overrides
	})
}

// WithStaticToken bypasses IAM User login and sends the given bearer token on
// every request. Useful with a token captured from an authenticated console
// session. When set, Config.IAMUser may be nil.
func WithStaticToken(token string) ClientOption {
	return clientOptionFunc(func(cfg *clientConfig) {
		cfg.staticToken = token
	})
}

// WithResponseCapture installs a synchronous debug hook for examples and tests.
// Captured bodies may contain sensitive data and should only be written to
// ignored local paths.
func WithResponseCapture(capture ResponseCaptureFunc) ClientOption {
	return clientOptionFunc(func(cfg *clientConfig) {
		cfg.capture = capture
	})
}

// WithRegion sets the region every service call targets. Required unless the
// Config is never used to make a call.
func WithRegion(region string) Option {
	return clientOptionFunc(func(cfg *clientConfig) {
		cfg.region = region
	})
}

// WithProjectID sets the project ID, skipping project discovery on the first
// call that needs one.
func WithProjectID(projectID string) Option {
	return clientOptionFunc(func(cfg *clientConfig) {
		cfg.projectID = projectID
	})
}

// WithIAMUser sets the IAM User credentials used to log in. Not required when
// WithStaticToken or WithCredentialsProvider is set.
func WithIAMUser(auth *IAMUserAuth) Option {
	return clientOptionFunc(func(cfg *clientConfig) {
		cfg.iamUser = auth
	})
}

// WithCredentialsProvider sets a custom source of access tokens. It wins
// over WithStaticToken, which wins over WithIAMUser; setting more than one
// is not an error, only the highest-precedence one is used.
func WithCredentialsProvider(p CredentialsProvider) Option {
	return clientOptionFunc(func(cfg *clientConfig) {
		cfg.credentials = p
	})
}
