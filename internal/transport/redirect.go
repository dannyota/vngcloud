package transport

import (
	"errors"
	"fmt"
	"net/http"
)

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

// refuseRedirects returns a client that behaves exactly like base except it
// never follows a redirect: its CheckRedirect always returns
// http.ErrUseLastResponse, so http.Client.Do returns the 3xx response itself
// instead of resending req's method and body at the Location it names. It
// builds a copy rather than mutating base, which the caller may still reuse
// for a request this rule must not apply to.
func refuseRedirects(base *http.Client) *http.Client {
	cp := *base
	cp.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &cp
}
