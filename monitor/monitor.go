// Package monitor reads vMonitor synthetic checks (GreenNode calls them
// uptime checks) and pauses or resumes them. Every call is per account: it
// sends no project ID and ignores the configured region, as billing does.
//
// PauseCheck and ResumeCheck drive a check to a target status over a toggle
// API that flips the current one; see the design's discussion of ADR 0003
// for why they read first, send the toggle at most once, and confirm by
// reading rather than trusting the toggle response.
package monitor

import (
	"sync"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/routes"
)

// Client is the monitor service client.
type Client struct {
	c *core.Client

	// toggleMu serializes PauseCheck and ResumeCheck within one Client, so
	// two goroutines sharing it never race the read-then-toggle sequence
	// against each other. It does not, and cannot, prevent the same race
	// across two processes or two Clients; the design leaves that to the
	// caller.
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
// operation in this release sends a query string; a later one that needs
// one takes a q url.Values parameter then.
func (c *Client) route(parts []string) string {
	full := append([]string{"vmonitor-uptime-manager", "v1"}, parts...)
	return c.c.RouteURL(routes.Route{Product: routes.ProductMonitor, Parts: full})
}
