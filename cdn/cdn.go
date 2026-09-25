// Package cdn reads the GreenNode CDN IP ranges a vCDN origin must allow.
// vCDN has no API of its own, so the ranges come from GreenNode's public FAQ
// page instead; see the design doc for why. ListIPRanges is the package's
// only operation.
package cdn

import (
	"context"
	"errors"
	"fmt"
	"mime"
	"net/http"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/routes"
	"danny.vn/vngcloud/internal/transport"
)

// ErrPageFormat means the FAQ page no longer looks like the page this
// parser was written for: the heading it looks for is missing or duplicated,
// the section holds no valid CIDR, or the response is not an HTML page at
// all. A caller that retries on this error keeps failing until the parser is
// updated; every other ListIPRanges error means only that the read failed
// this time.
var ErrPageFormat = errors.New("cdn: IP range page format not recognized")

// maxBodyBytes bounds the page read, per the design's discussion of memory
// safety for a hostile or broken response. The page is about 900 KiB today.
const maxBodyBytes = 4 << 20

// headingNeedle is the case-insensitive substring that identifies the FAQ
// section listing the CDN IP ranges.
const headingNeedle = "CDN IP range"

// Client is the cdn service client.
type Client struct {
	c *core.Client
}

// New builds a Client from cfg. A Client built from the same Config as
// another service client shares its login and token cache, though
// ListIPRanges never uses either: it sends no credential of its own.
func New(cfg vngcloud.Config) *Client {
	return &Client{c: core.ClientOf(cfg)}
}

// ListIPRangesInput has no fields today; a nil Input is valid. Fields may be
// added later without breaking callers.
type ListIPRangesInput struct{}

// ListIPRangesOutput is the current CDN IP ranges.
type ListIPRangesOutput struct {
	// Items holds sorted, de-duplicated, canonical CIDRs. Every item comes
	// from netip.Prefix.String() on a validated prefix, so
	// netip.ParsePrefix never fails on it.
	Items []string
	// Source is the page URL fetched.
	Source string
}

// ListIPRanges reads the CDN IP ranges GreenNode publishes for vCDN origins
// to allow, from its public FAQ page. The request carries no credential and
// no cookie: it sets SkipAuth, so the transport never asks a credentials
// provider for a token, and it strips any cookie jar from the configured
// HTTP client before sending.
//
// Any doubt about the page's shape fails the call with ErrPageFormat rather
// than returning an empty or partial list.
func (c *Client) ListIPRanges(ctx context.Context, in *ListIPRangesInput) (*ListIPRangesOutput, error) {
	const op = "cdn.ListIPRanges"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}

	source := c.c.RouteURL(routes.Route{Product: routes.ProductCDNDocs})
	status, contentType, body, err := c.c.DoRaw(ctx, transport.Request{
		Operation: op,
		Method:    http.MethodGet,
		URL:       source,
		Headers:   map[string]string{"Accept": "text/html"},
		SkipAuth:  true,
		MaxBody:   maxBodyBytes,
	})
	if err != nil {
		if errors.Is(err, transport.ErrBodyTooLarge) {
			// The status wins over the body size: a 200 with too much body
			// is a page format the parser cannot trust, but any other
			// status is an ordinary API error for that status, same as if
			// the body had never been read at all.
			if status == http.StatusOK {
				return nil, fmt.Errorf("%w: body over %d bytes", ErrPageFormat, maxBodyBytes)
			}
			return nil, statusError(op, status)
		}
		return nil, err
	}
	if status != http.StatusOK {
		return nil, statusError(op, status)
	}
	if mediaType, _, mErr := mime.ParseMediaType(contentType); mErr != nil || mediaType != "text/html" {
		return nil, fmt.Errorf("%w: content-type %q is not text/html", ErrPageFormat, contentType)
	}

	items, err := parseIPRanges(body)
	if err != nil {
		return nil, err
	}
	return &ListIPRangesOutput{Items: items, Source: source}, nil
}

// statusError builds the *core.APIError a non-200 response from the docs
// host produces. It wraps the same sentinel other SDK errors wrap for the
// status (core.ErrNotFound, core.ErrPermission, core.ErrRateLimited), except
// that a 401 wraps nothing: the request carried no credential (SkipAuth is
// always set), so it can never have failed authentication the way a 401 from
// an authenticated call does, and errors.Is(err, vngcloud.ErrAuth) must stay
// false for it.
func statusError(op string, status int) *core.APIError {
	return &core.APIError{
		Operation:  op,
		StatusCode: status,
		Code:       core.ResolvedCode(status, ""),
		Retryable:  retryableStatus(status),
		Err:        sentinelForStatus(status),
	}
}

func sentinelForStatus(status int) error {
	switch status {
	case http.StatusForbidden:
		return core.ErrPermission
	case http.StatusNotFound:
		return core.ErrNotFound
	case http.StatusTooManyRequests:
		return core.ErrRateLimited
	default:
		return nil
	}
}

// retryableStatus matches the transport's own retry policy for an idempotent
// request: a GET is retried on 429 and every 5xx that a load balancer or
// upstream commonly returns for a request the origin never acted on.
func retryableStatus(status int) bool {
	switch status {
	case http.StatusTooManyRequests, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}
