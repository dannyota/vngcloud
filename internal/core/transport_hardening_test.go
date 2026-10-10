package core

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"danny.vn/vngcloud/internal/endpoints"
	"danny.vn/vngcloud/internal/transport"
)

func TestCallerClientRedirectGuard(t *testing.T) {
	for _, path := range []string{"json", "status", "raw"} {
		for _, destination := range []string{"host", "port", "same"} {
			t.Run(path+"/"+destination, func(t *testing.T) {
				var hits atomic.Int64
				target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { hits.Add(1); _, _ = w.Write([]byte(`{}`)) }))
				defer target.Close()
				source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/final" {
						hits.Add(1)
						_, _ = w.Write([]byte(`{}`))
						return
					}
					location := target.URL
					if destination == "host" {
						location = strings.Replace(location, "127.0.0.1", "localhost", 1)
					}
					if destination == "same" {
						location = "/final"
					}
					http.Redirect(w, r, location, http.StatusFound)
				}))
				defer source.Close()
				caller := source.Client()
				c, err := newClient(WithRegion("hcm-3"), WithStaticToken("synthetic-token"), WithHTTPClient(caller), WithRetry(0, 0))
				if err != nil {
					t.Fatal(err)
				}
				req := transport.Request{URL: source.URL}
				switch path {
				case "json":
					err = c.transport.DoJSON(context.Background(), req, nil)
				case "status":
					_, err = c.transport.DoJSONStatus(context.Background(), req, nil)
				case "raw":
					_, _, _, err = c.transport.DoRaw(context.Background(), req)
				}
				if destination == "same" {
					if err != nil || hits.Load() != 1 {
						t.Fatalf("same-host redirect: error %v, hits %d", err, hits.Load())
					}
				} else if err == nil || hits.Load() != 0 {
					t.Fatalf("unsafe redirect: error %v, hits %d", err, hits.Load())
				}
				if caller.CheckRedirect != nil {
					t.Fatal("caller client mutated")
				}
			})
		}
	}
}

func TestTransportCauseTextIsSafe(t *testing.T) {
	const secret = "synthetic-query-secret"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", "http://host/%zz?projectId="+secret)
		w.WriteHeader(http.StatusFound)
	}))
	defer server.Close()
	for _, malformed := range []bool{false, true} {
		client := server.Client()
		if !malformed {
			client = &http.Client{Transport: &failingRoundTripper{err: errors.New("failure http://host/path?projectId=" + secret)}}
		}
		tc := transport.New(transport.Config{HTTPClient: client})
		c := NewTestClient("hcm-3", "", endpoints.Set{}, tc)
		err := c.DoJSON(context.Background(), transport.Request{URL: server.URL, SkipAuth: true}, nil)
		if err == nil {
			t.Fatal("expected error")
		}
		for _, value := range []string{secret, "http://", "projectId", "%zz"} {
			if strings.Contains(err.Error(), value) {
				t.Errorf("error leaked %q", value)
			}
		}
	}
}

func TestAllPathsApplyRedirectHookAndLimit(t *testing.T) {
	for _, path := range []string{"json", "status", "raw"} {
		for _, mode := range []string{"cross-host", "hook", "limit", "cookies"} {
			t.Run(path+"/"+mode, func(t *testing.T) {
				var hits, hooks atomic.Int64
				var cookieSeen atomic.Bool
				server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					hits.Add(1)
					if r.Header.Get("Cookie") != "" {
						cookieSeen.Store(true)
					}
					location := "/final"
					if mode == "cross-host" {
						location = "http://elsewhere.invalid/path?projectId=synthetic-secret"
					}
					if mode == "limit" {
						location = "/loop"
					}
					if r.URL.Path != "/final" {
						http.Redirect(w, r, location, http.StatusFound)
						return
					}
					_, _ = w.Write([]byte(`{}`))
				}))
				defer server.Close()
				caller := server.Client()
				jar, err := cookiejar.New(nil)
				if err != nil {
					t.Fatal(err)
				}
				u, err := url.Parse(server.URL)
				if err != nil {
					t.Fatal(err)
				}
				jar.SetCookies(u, []*http.Cookie{{Name: "session", Value: "synthetic-cookie", Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode}})
				caller.Jar = jar
				caller.CheckRedirect = func(*http.Request, []*http.Request) error {
					hooks.Add(1)
					if mode == "hook" {
						return errors.New("caller refuses")
					}
					return nil
				}
				c, err := newClient(WithRegion("hcm-3"), WithStaticToken("synthetic-token"), WithHTTPClient(caller), WithRetry(0, 0))
				if err != nil {
					t.Fatal(err)
				}
				req := transport.Request{URL: server.URL}
				switch path {
				case "json":
					err = c.transport.DoJSON(context.Background(), req, nil)
				case "status":
					_, err = c.transport.DoJSONStatus(context.Background(), req, nil)
				case "raw":
					_, _, _, err = c.transport.DoRaw(context.Background(), req)
				}
				switch mode {
				case "cross-host":
					if err == nil || hooks.Load() != 0 || hits.Load() != 1 {
						t.Fatalf("error %v, hooks %d, hits %d", err, hooks.Load(), hits.Load())
					}
					if !strings.Contains(err.Error(), "cross-host redirect refused") || strings.Contains(err.Error(), "synthetic-secret") {
						t.Fatalf("unsafe refusal: %v", err)
					}
				case "hook":
					if err == nil || hooks.Load() != 1 || hits.Load() != 1 {
						t.Fatalf("error %v, hooks %d, hits %d", err, hooks.Load(), hits.Load())
					}
				case "limit":
					if err == nil || hits.Load() != 10 || hooks.Load() != 9 {
						t.Fatalf("error %v, hooks %d, hits %d", err, hooks.Load(), hits.Load())
					}
				case "cookies":
					if err != nil || cookieSeen.Load() != (path != "raw") {
						t.Fatalf("error %v, cookie seen %t", err, cookieSeen.Load())
					}
				}
				if caller.Jar != jar || caller.CheckRedirect == nil {
					t.Fatal("caller client mutated")
				}
			})
		}
	}
}

func TestHTTPSRedirectRefusesSchemeChange(t *testing.T) {
	for _, mode := range []string{"default", "caller"} {
		for _, path := range []string{"json", "status", "raw"} {
			t.Run(mode+"/"+path, func(t *testing.T) {
				var targetHits, hooks atomic.Int64
				target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					targetHits.Add(1)
					_, _ = w.Write([]byte(`{}`))
				}))
				defer target.Close()
				var sourceURL string
				source := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					http.Redirect(w, r, strings.Replace(sourceURL, "https:", "http:", 1)+"/private-path?projectId=synthetic-query-secret", http.StatusFound)
				}))
				defer source.Close()
				sourceURL = source.URL
				base := source.Client().Transport.(*http.Transport).Clone()
				defer base.CloseIdleConnections()
				if base.TLSClientConfig.InsecureSkipVerify {
					t.Fatal("TLS verification disabled")
				}
				dialer := &net.Dialer{}
				tlsDialer := &tls.Dialer{NetDialer: dialer, Config: base.TLSClientConfig}
				base.DialTLSContext = tlsDialer.DialContext
				targetURL, err := url.Parse(target.URL)
				if err != nil {
					t.Fatal(err)
				}
				// Route cleartext traffic to a local target while preserving URL.Host.
				base.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
					return dialer.DialContext(ctx, network, targetURL.Host)
				}
				opts := []ClientOption{WithRegion("hcm-3"), WithStaticToken("synthetic-bearer-secret"), WithRetry(0, 0)}
				if mode == "default" {
					opts = append(opts, WithTransport(base))
				} else {
					caller := &http.Client{Transport: base, CheckRedirect: func(*http.Request, []*http.Request) error { hooks.Add(1); return nil }}
					opts = append(opts, WithHTTPClient(caller))
				}
				c, err := newClient(opts...)
				if err != nil {
					t.Fatal(err)
				}
				req := transport.Request{URL: source.URL}
				switch path {
				case "json":
					err = c.transport.DoJSON(context.Background(), req, nil)
				case "status":
					_, err = c.transport.DoJSONStatus(context.Background(), req, nil)
				case "raw":
					_, _, _, err = c.transport.DoRaw(context.Background(), req)
				}
				if err == nil || targetHits.Load() != 0 || hooks.Load() != 0 {
					t.Fatalf("error %v, target hits %d, hooks %d", err, targetHits.Load(), hooks.Load())
				}
				for _, text := range []string{"/private-path", "projectId", "synthetic-query-secret", "synthetic-bearer-secret"} {
					if strings.Contains(err.Error(), text) {
						t.Errorf("error leaked %q", text)
					}
				}
				if !strings.Contains(err.Error(), "scheme change refused") {
					t.Fatalf("error = %v", err)
				}
				if mode == "default" {
					initial, err := http.NewRequestWithContext(context.Background(), http.MethodGet, source.URL, nil)
					if err != nil {
						t.Fatal(err)
					}
					next, err := http.NewRequestWithContext(context.Background(), http.MethodGet, strings.Replace(source.URL, "https:", "http:", 1)+"/private-path?projectId=synthetic-query-secret", nil)
					if err != nil {
						t.Fatal(err)
					}
					refusal := buildHTTPClient(clientConfig{transport: base}).CheckRedirect(next, []*http.Request{initial})
					if refusal == nil {
						t.Fatal("default redirect guard accepted scheme change")
					}
					if strings.Contains(refusal.Error(), "private-path") || strings.Contains(refusal.Error(), "synthetic-query-secret") {
						t.Fatal("default refusal leaked URL")
					}
				}
			})
		}
	}
}
