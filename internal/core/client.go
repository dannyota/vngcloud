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
	"danny.vn/vngcloud/internal/transport"
)

const defaultUserAgent = "danny.vn/vngcloud"

type Client struct {
	region    string
	endpoints endpoints.Set
	transport *transport.Client
	logger    *slog.Logger

	projectMu     sync.Mutex
	projectID     string
	projectUserID int
}

func NewClient(cfg Config, opts ...ClientOption) (*Client, error) {
	if cfg.Region == "" {
		return nil, fmt.Errorf("%w: Region is required", ErrInvalidConfig)
	}
	settings := clientConfig{
		timeout:       120 * time.Second,
		retryCount:    3,
		retryInterval: time.Second,
		userAgent:     defaultUserAgent,
	}
	for _, opt := range opts {
		opt.apply(&settings)
	}
	if settings.staticToken == "" {
		if err := cfg.IAMUser.validate(); err != nil {
			return nil, err
		}
	}
	if cfg.UserAgent != "" {
		settings.userAgent = cfg.UserAgent
	}

	resolvedEndpoints := endpoints.ResolveIAMUser(cfg.Region, endpoints.Overrides(settings.endpoints))

	httpClient := buildHTTPClient(settings)
	var ts transport.TokenSource
	if settings.staticToken != "" {
		ts = staticTokenSource(settings.staticToken)
	} else {
		ts = &iamTokenSource{auth: cfg.IAMUser, endpoints: loginEndpoints{
			signin:    resolvedEndpoints.Signin,
			token:     resolvedEndpoints.Token,
			dashboard: resolvedEndpoints.Dashboard,
		}}
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
		region:    cfg.Region,
		projectID: cfg.ProjectID,
		endpoints: resolvedEndpoints,
		transport: tc,
		logger:    logger,
	}
	return c, nil
}

// Authenticate performs the login flow eagerly and caches the token, so
// configuration and credential errors surface before the first API call.
func (c *Client) Authenticate(ctx context.Context) error {
	return c.transport.EnsureToken(ctx)
}

type staticTokenSource string

// Token returns the static token with a rolling 24-hour expiry window.
// NeedsRefresh will periodically re-invoke this source, which simply
// re-issues the same unchanging token; a static token never truly refreshes.
func (s staticTokenSource) Token(context.Context) (transport.Token, error) {
	return transport.Token{AccessToken: string(s), ExpiresAt: time.Now().Add(24 * time.Hour)}, nil
}

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
	default:
		return ""
	}
}

func (c *Client) RouteURL(route routes.Route) string {
	return routes.URL(c, route)
}

func (c *Client) DoJSON(ctx context.Context, req transport.Request, out any) error {
	err := c.transport.DoJSON(ctx, req, out)
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

type iamTokenSource struct {
	auth      *IAMUserAuth
	endpoints loginEndpoints
}

func (s *iamTokenSource) Token(ctx context.Context) (transport.Token, error) {
	token, expiresAt, err := s.auth.token(ctx, s.endpoints)
	if err != nil {
		return transport.Token{}, err
	}
	return transport.Token{AccessToken: token, ExpiresAt: expiresAt}, nil
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
