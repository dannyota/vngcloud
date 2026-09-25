package core

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"danny.vn/vngcloud/internal/endpoints"
	"danny.vn/vngcloud/internal/routes"
	"danny.vn/vngcloud/internal/tokencache"
	"danny.vn/vngcloud/internal/transport"
)

const defaultUserAgent = "danny.vn/vngcloud"

type Client struct {
	region    string
	endpoints endpoints.Set
	transport *transport.Client
	logger    *slog.Logger

	// err is set on a Client returned by ClientOf for a zero Config, so every
	// call fails with ErrInvalidConfig instead of a nil-pointer panic.
	err error

	projectMu     sync.Mutex
	projectID     string
	projectUserID int
}

// defaultClientConfig is the clientConfig every option-application starts
// from, whether from NewConfig's opts or LoadConfig's own resolution.
func defaultClientConfig() clientConfig {
	return clientConfig{
		timeout:       120 * time.Second,
		retryCount:    3,
		retryInterval: time.Second,
		userAgent:     defaultUserAgent,
	}
}

func newClient(opts ...Option) (*Client, error) {
	settings := defaultClientConfig()
	for _, opt := range opts {
		opt.apply(&settings)
	}
	return buildClient(settings)
}

// buildClient builds a Client from a fully-resolved clientConfig, applying
// no further options. LoadConfig calls this directly after resolving
// region, project ID, and credentials itself from options, environment
// variables, and profile files, so those resolved values are set once, not
// overridden by re-applying the caller's original options afterward.
func buildClient(settings clientConfig) (*Client, error) {
	if settings.region == "" {
		return nil, fmt.Errorf("%w: Region is required", ErrInvalidConfig)
	}
	if settings.credentials == nil && settings.staticToken == "" {
		if err := settings.iamUser.validate(); err != nil {
			return nil, err
		}
	}

	resolvedEndpoints := endpoints.ResolveIAMUser(settings.region, endpoints.Overrides(settings.endpoints))

	httpClient := buildHTTPClient(settings)
	var ts transport.TokenSource
	switch {
	case settings.credentials != nil:
		ts = settings.credentials
	case settings.staticToken != "":
		ts = staticTokenSource(settings.staticToken)
	default:
		source := &iamTokenSource{auth: settings.iamUser, endpoints: loginEndpoints{
			signin:    resolvedEndpoints.Signin,
			token:     resolvedEndpoints.Token,
			dashboard: resolvedEndpoints.Dashboard,
		}}
		if settings.tokenCacheDir != "" {
			// The cache shares timeNow, the same clock auth.go's Invalidate
			// reads, rather than time.Now directly: a test that fakes the
			// clock to control the 30-second freshness window must control
			// both, or a disk token's ObtainedAt would be judged against real
			// time while its 401 invalidation is judged against fake time.
			source.cache = tokencache.New(settings.tokenCacheDir, func() time.Time { return timeNow() })
			source.cacheKey = tokencache.Key{
				Profile:   settings.profile,
				RootEmail: settings.iamUser.RootEmail,
				Username:  settings.iamUser.Username,
				SigninURL: firstNonEmpty(settings.iamUser.SigninBaseURL, resolvedEndpoints.Signin),
				TokenURL:  firstNonEmpty(settings.iamUser.TokenURL, resolvedEndpoints.Token),
			}
		}
		ts = source
	}
	var capture transport.CaptureFunc
	if settings.capture != nil {
		capture = func(captured transport.Capture) {
			settings.capture(ResponseCapture{
				Operation:  captured.Operation,
				Method:     captured.Method,
				URL:        captured.URL,
				StatusCode: captured.StatusCode,
				Body:       captured.Body,
			})
		}
	}
	tc := transport.New(transport.Config{
		HTTPClient:    httpClient,
		TokenSource:   ts,
		RetryCount:    settings.retryCount,
		RetryInterval: settings.retryInterval,
		UserAgent:     settings.userAgent,
		Capture:       capture,
	})

	logger := settings.logger
	if logger == nil {
		logger = slog.New(nopHandler{})
	}

	c := &Client{
		region:    settings.region,
		projectID: settings.projectID,
		endpoints: resolvedEndpoints,
		transport: tc,
		logger:    logger,
	}
	return c, nil
}

// Authenticate performs the login flow eagerly and caches the token, so
// configuration and credential errors surface before the first API call. A
// token source that yields an empty token fails here with ErrAuth, the same
// sentinel a request made without Authenticate would fail with.
func (c *Client) Authenticate(ctx context.Context) error {
	if c.err != nil {
		return c.err
	}
	return wrapTransportErr(c.transport.EnsureToken(ctx))
}

type staticTokenSource string

// Token returns the static token with a rolling 24-hour expiry window.
// NeedsRefresh will periodically re-invoke this source, which simply
// re-issues the same unchanging token; a static token never truly refreshes.
func (s staticTokenSource) Token(context.Context) (transport.Token, error) {
	return transport.Token{AccessToken: string(s), ExpiresAt: time.Now().Add(24 * time.Hour)}, nil
}

// Invalidate is a no-op: a static token is supplied by the caller and the
// SDK never refreshes it.
func (s staticTokenSource) Invalidate(string) {}

func (c *Client) Region() string {
	return c.region
}

func (c *Client) ProjectID() string {
	c.projectMu.Lock()
	defer c.projectMu.Unlock()
	return c.projectID
}

func (c *Client) ProjectUserID() int {
	c.projectMu.Lock()
	defer c.projectMu.Unlock()
	return c.projectUserID
}

func (c *Client) RequireProjectID(ctx context.Context) (string, error) {
	if c.err != nil {
		return "", c.err
	}
	c.projectMu.Lock()
	defer c.projectMu.Unlock()
	if c.projectID != "" {
		return c.projectID, nil
	}
	if err := c.discoverProjectLocked(ctx); err != nil {
		return "", err
	}
	return c.projectID, nil
}

func (c *Client) RequireProject(ctx context.Context) (Project, error) {
	if c.err != nil {
		return Project{}, c.err
	}
	projects, err := c.ListProjects(ctx, nil)
	if err != nil {
		return Project{}, err
	}
	c.projectMu.Lock()
	defer c.projectMu.Unlock()
	if c.projectID != "" {
		for _, project := range projects {
			if project.ID == c.projectID {
				c.projectUserID = project.UserID
				return project, nil
			}
		}
		return Project{ID: c.projectID, Region: c.region}, nil
	}
	switch len(projects) {
	case 0:
		return Project{}, fmt.Errorf("%w: %s", ErrProjectNotFound, c.region)
	case 1:
		c.projectID = projects[0].ID
		c.projectUserID = projects[0].UserID
		return projects[0], nil
	default:
		return Project{}, fmt.Errorf("%w: %s", ErrProjectAmbiguous, c.region)
	}
}

func (c *Client) Endpoint(product routes.Product) string {
	switch product {
	case routes.ProductVServer:
		return c.endpoints.VServer
	case routes.ProductVLB:
		return c.endpoints.VLB
	case routes.ProductVNet:
		return c.endpoints.VNetwork
	case routes.ProductGLB:
		return c.endpoints.GLB
	case routes.ProductDNS:
		return c.endpoints.DNS
	case routes.ProductVCR:
		return c.endpoints.VCR
	case routes.ProductPortal:
		return c.endpoints.Portal
	case routes.ProductBilling:
		return c.endpoints.Billing
	default:
		return ""
	}
}

func (c *Client) RouteURL(route routes.Route) string {
	return routes.URL(c, route)
}

func (c *Client) DoJSON(ctx context.Context, req transport.Request, out any) error {
	_, err := c.DoJSONStatus(ctx, req, out)
	return err
}

// DoJSONStatus is DoJSON but also returns the final HTTP status, so a
// service can build an error envelope that names the actual status a 2xx
// response carried.
func (c *Client) DoJSONStatus(ctx context.Context, req transport.Request, out any) (int, error) {
	if c.err != nil {
		return 0, c.err
	}
	status, err := c.transport.DoJSONStatus(ctx, req, out)
	return status, wrapTransportErr(err)
}

// wrapTransportErr converts a *transport.APIError into the SDK's own
// APIError, mapping its status code to the matching sentinel (ErrAuth,
// ErrNotFound, and so on), so Authenticate and every request-making method
// surface the same sentinel for the same status. An error that is not a
// *transport.APIError, such as a canceled context, passes through unchanged;
// a nil error stays nil.
func wrapTransportErr(err error) error {
	if err == nil {
		return nil
	}
	var terr *transport.APIError
	if !errors.As(err, &terr) {
		return err
	}
	apiErr := &APIError{
		Operation:  terr.Operation,
		StatusCode: terr.StatusCode,
		Code:       terr.Code,
		Message:    terr.Message,
		Retryable:  terr.Retryable,
		Err:        mapStatusError(terr.StatusCode),
	}
	if apiErr.Err == nil {
		apiErr.Err = terr.Err
	}
	return apiErr
}

func buildHTTPClient(cfg clientConfig) *http.Client {
	if cfg.httpClient != nil {
		return cfg.httpClient
	}
	roundTripper := cfg.transport
	if roundTripper == nil {
		roundTripper = http.DefaultTransport
	}
	return &http.Client{
		Transport: roundTripper,
		Timeout:   cfg.timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("stopped after 10 redirects")
			}
			if req.URL.Host != via[0].URL.Host {
				return fmt.Errorf("redirected from %q to %q: API endpoints have moved, update the endpoint configuration", via[0].URL.Host, req.URL.Host)
			}
			return nil
		},
	}
}

// iamTokenSource adapts an IAMUserAuth to transport.TokenSource. With no
// cache configured it delegates straight to auth's own in-memory caching.
// With a cache configured, it checks memory first, then the disk cache
// (which performs the login itself when needed), and remembers whatever the
// disk cache returns so this process's later calls skip the disk.
type iamTokenSource struct {
	auth      *IAMUserAuth
	endpoints loginEndpoints
	cache     *tokencache.Cache
	cacheKey  tokencache.Key

	mu       sync.Mutex
	rejected string
}

func (s *iamTokenSource) Token(ctx context.Context) (transport.Token, error) {
	if s.cache == nil {
		token, expiresAt, err := s.auth.token(ctx, s.endpoints)
		if err != nil {
			return transport.Token{}, err
		}
		return transport.Token{AccessToken: token, ExpiresAt: expiresAt}, nil
	}

	if token, expiresAt, ok := s.auth.cachedIfFresh(); ok {
		return transport.Token{AccessToken: token, ExpiresAt: expiresAt}, nil
	}

	s.mu.Lock()
	rejected := s.rejected
	s.rejected = ""
	s.mu.Unlock()

	cached, err := s.cache.Get(ctx, s.cacheKey, rejected, func(ctx context.Context) (tokencache.Token, error) {
		token, expiresAt, err := s.auth.doLogin(ctx, s.endpoints)
		if err != nil {
			return tokencache.Token{}, err
		}
		return tokencache.Token{AccessToken: token, ExpiresAt: expiresAt}, nil
	})
	if err != nil {
		// Cache.Get failed before it could act on rejected (a canceled
		// context, a lock it could not acquire in time): put it back so the
		// next call still treats the token as invalidated, instead of
		// silently reusing it because the rejection was lost here.
		if rejected != "" {
			s.mu.Lock()
			s.rejected = rejected
			s.mu.Unlock()
		}
		return transport.Token{}, err
	}
	s.auth.remember(cached.AccessToken, cached.ExpiresAt, cached.ObtainedAt)
	return transport.Token{AccessToken: cached.AccessToken, ExpiresAt: cached.ExpiresAt}, nil
}

// Invalidate clears auth's in-memory token as usual and, when a disk cache
// is configured, remembers sent so the next Token call passes it to
// Cache.Get as the rejected token. It never records an empty string.
func (s *iamTokenSource) Invalidate(sent string) {
	s.auth.Invalidate(sent)
	if s.cache != nil && sent != "" {
		s.mu.Lock()
		s.rejected = sent
		s.mu.Unlock()
	}
}

func mapStatusError(status int) error {
	switch status {
	case http.StatusUnauthorized:
		return ErrAuth
	case http.StatusForbidden:
		return ErrPermission
	case http.StatusNotFound:
		return ErrNotFound
	case http.StatusTooManyRequests:
		return ErrRateLimited
	default:
		return nil
	}
}

type nopHandler struct{}

func (nopHandler) Enabled(context.Context, slog.Level) bool  { return false }
func (nopHandler) Handle(context.Context, slog.Record) error { return nil }
func (h nopHandler) WithAttrs([]slog.Attr) slog.Handler      { return h }
func (h nopHandler) WithGroup(string) slog.Handler           { return h }
