// Package iam reads and changes IAM callers, users, service accounts,
// groups, and policies. Service account calls run on the accounts API,
// under the Dashboard endpoint; every other call runs on the policies API,
// under the IAM endpoint.
package iam

import (
	"net/url"
	"strconv"
	"sync"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/routes"
)

// Client is the iam service client.
type Client struct {
	c *core.Client

	// guardMu protects guardCaller and guardWriteActions, the only state a
	// guard check reuses across calls: the caller's identity and the
	// account's IAM write action names. Every other guard read, such as a
	// service account's attached policies, is fetched fresh for each
	// guarded write. A nil guardCaller means the caller identity is not
	// cached yet. guardWriteActions is cached only once ListActions has
	// named at least one write action: an account whose action list names
	// none, however that happens, is refused as a failed guard read (see
	// guardWriteActionNames) and refetched on the next guarded write,
	// rather than being cached as proof that nothing is privileged.
	guardMu           sync.Mutex
	guardCaller       *GetCallerIdentityOutput
	guardWriteActions []string
}

// New builds a Client from cfg. A Client built from the same Config as
// another service client shares its login and token cache.
func New(cfg vngcloud.Config) *Client {
	return &Client{c: core.ClientOf(cfg)}
}

// accountsURL builds a URL under the Dashboard endpoint's accounts API.
func (c *Client) accountsURL(parts []string, q url.Values) string {
	return c.c.RouteURL(routes.Route{Product: routes.ProductDashboard, Version: "accounts-api/v1", Parts: parts, Query: q})
}

// policiesURL builds a URL under the IAM endpoint's policies API.
func (c *Client) policiesURL(parts []string, q url.Values) string {
	return c.c.RouteURL(routes.Route{Product: routes.ProductIAM, Version: "policies-api/v1", Parts: parts, Query: q})
}

// pageQuery builds the pageNumber and pageSize query parameters. Unlike
// core.PageQuery, page 0 is left as the first page rather than floored to
// core.DefaultPage: the accounts and policies APIs both start numbering at
// 0. A non-positive size sends core.DefaultPageSize.
func pageQuery(page, size int) url.Values {
	if page < 0 {
		page = 0
	}
	if size <= 0 {
		size = core.DefaultPageSize
	}
	q := url.Values{}
	q.Set("pageNumber", strconv.Itoa(page))
	q.Set("pageSize", strconv.Itoa(size))
	return q
}
