// Package monitor reads, creates, updates, and deletes vMonitor synthetic
// checks (GreenNode calls them uptime checks), pauses or resumes them, and
// lists the probe locations a check can run from. Every call is per
// account: it sends no project ID and ignores the configured region, as
// billing does.
//
// PauseCheck and ResumeCheck drive a check to a target status over a toggle
// API that flips the current one; see the design's discussion of ADR 0003
// for why they read first, send the toggle at most once, and confirm by
// reading rather than trusting the toggle response.
package monitor

import (
	"net/url"
	"sync"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/routes"
)

// Client is the monitor service client.
type Client struct {
	c *core.Client

	// toggleMu serializes PauseCheck, ResumeCheck, and UpdateCheck within
	// one Client, so two goroutines sharing it never race a read-then-write
	// sequence, toggle or update, against each other. It does not, and
	// cannot, prevent the same race across two processes or two Clients;
	// the design leaves that to the caller.
	toggleMu sync.Mutex

	// sleep waits for d or ctx's end, whichever comes first, between confirm
	// reads. Tests replace it with an injected clock so the 1, 2, and 4
	// second waits never really elapse.
	sleep sleepFunc
}

// New builds a Client from cfg. A Client built from the same Config as
// another service client shares its login and token cache.
func New(cfg vngcloud.Config) *Client {
	return &Client{c: core.ClientOf(cfg), sleep: contextSleep}
}

// route builds a URL under the Monitor endpoint's uptime manager prefix. No
// operation under this prefix sends a query string.
func (c *Client) route(parts []string) string {
	full := append([]string{"vmonitor-uptime-manager", "v1"}, parts...)
	return c.c.RouteURL(routes.Route{Product: routes.ProductMonitor, Parts: full})
}

// notificationRoute builds a URL under the Monitor endpoint's notification
// gateway prefix, which serves channels and channel types separately from
// the uptime manager prefix route builds under.
func (c *Client) notificationRoute(parts []string, q url.Values) string {
	full := append([]string{"notification-gateway", "api", "v1"}, parts...)
	return c.c.RouteURL(routes.Route{Product: routes.ProductMonitor, Parts: full, Query: q})
}
