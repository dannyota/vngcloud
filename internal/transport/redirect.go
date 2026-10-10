package transport

import (
	"errors"
	"fmt"
	"net/http"
)

// redirectClient copies base so SDK rules run before the caller's hook.
func redirectClient(base *http.Client) *http.Client {
	cp := *base
	inner := base.CheckRedirect
	cp.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return errRedirectLimit
		}
		if req.URL.Host != via[0].URL.Host {
			return &redirectError{from: via[0].URL.Host, to: req.URL.Host}
		}
		if req.URL.Scheme != via[0].URL.Scheme {
			return errRedirectScheme
		}
		if inner != nil {
			return inner(req, via)
		}
		return nil
	}
	return &cp
}

var (
	errRedirectLimit  = errors.New("stopped after 10 redirects")
	errRedirectScheme = errors.New("redirect scheme change refused")
)

type redirectError struct{ from, to string }

func (e *redirectError) Error() string {
	return fmt.Sprintf("redirected from %q to %q: cross-host redirect refused", e.from, e.to)
}

// rawClient clears the jar without changing the configured client's cookies.
func (c *Client) rawClient() *http.Client {
	cp := *c.httpClient
	cp.Jar = nil
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
