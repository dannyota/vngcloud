package core

import (
	"log/slog"
	"net/http"
	"time"
)

type ClientOption interface {
	apply(*clientConfig)
}

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

// WithResponseCapture installs a synchronous debug hook for examples and tests.
// Captured bodies may contain sensitive data and should only be written to
// ignored local paths.
func WithResponseCapture(capture ResponseCaptureFunc) ClientOption {
	return clientOptionFunc(func(cfg *clientConfig) {
		cfg.capture = capture
	})
}
