package transport

import (
	"errors"
	"fmt"
	"net/http"
)

// redirectClient copies base and checks destinations before and after the
// caller's hook, which can rewrite the URL.
func redirectClient(base *http.Client) *http.Client {
	cp := *base
	inner := base.CheckRedirect
	var scheme, host string
	cp.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return errRedirectLimit
		}
		// A hook can also mutate via[0], so later hops use the saved origin.
		if len(via) == 1 {
			scheme, host = via[0].URL.Scheme, via[0].URL.Host
		}
		if err := checkRedirectOrigin(req, scheme, host); err != nil {
			return err
		}
		if inner != nil {
			if err := inner(req, via); err != nil {
				return err
			}
		}
		return checkRedirectOrigin(req, scheme, host)
	}
	return &cp
}

func checkRedirectOrigin(req *http.Request, scheme, host string) error {
	if req.URL.Host != host {
		return &redirectError{from: host, to: req.URL.Host}
	}
	if req.URL.Scheme != scheme {
		return errRedirectScheme
	}
	return nil
}

var (
	errRedirectLimit  = errors.New("stopped after 10 redirects")
	errRedirectScheme = errors.New("redirect scheme change refused")
)

type redirectError struct{ from, to string }

func (e *redirectError) Error() string {
	return fmt.Sprintf("redirected from %q to %q: cross-host redirect refused", e.from, e.to)
}

// redactedText scrubs raw hosts before quoting can escape credential characters.
func (e *redirectError) redactedText(values []string) string {
	return (&redirectError{from: redact(e.from, values), to: redact(e.to, values)}).Error()
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
