package iamuser

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

func routeLoginHTTP(t *testing.T, source, target *httptest.Server) *http.Transport {
	t.Helper()
	base := source.Client().Transport.(*http.Transport).Clone()
	t.Cleanup(base.CloseIdleConnections)
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
	// Route HTTP to the local target without changing the requested host or port.
	base.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return dialer.DialContext(ctx, network, targetURL.Host)
	}
	return base
}

func TestLoginRejectsForeignTOTPDestination(t *testing.T) {
	for _, mode := range []string{"host", "port", "scheme", "network-path"} {
		t.Run(mode, func(t *testing.T) {
			var hits, codes atomic.Int64
			handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				hits.Add(1)
				_, _ = w.Write([]byte(`<input name="_csrf" value="csrf">`))
			})
			target := httptest.NewTLSServer(handler)
			defer target.Close()
			plain := httptest.NewServer(handler)
			defer plain.Close()
			var location string
			source := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost {
					http.Redirect(w, r, location, http.StatusFound)
					return
				}
				_, _ = w.Write([]byte(`<input name="_csrf" value="csrf">`))
			}))
			defer source.Close()
			location = target.URL + twoFAPathMatch
			if mode == "host" {
				location = strings.Replace(location, "127.0.0.1", target.Certificate().DNSNames[0], 1)
			}
			if mode == "scheme" {
				location = strings.Replace(source.URL, "https:", "http:", 1) + twoFAPathMatch
			}
			if mode == "network-path" {
				location = strings.TrimPrefix(location, "https:")
			}
			base := routeLoginHTTP(t, source, plain)
			if mode == "host" {
				original := base.DialTLSContext
				base.DialTLSContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
					if strings.HasPrefix(addr, target.Certificate().DNSNames[0]+":") {
						config := base.TLSClientConfig.Clone()
						config.ServerName = target.Certificate().DNSNames[0]
						return (&tls.Dialer{Config: config}).DialContext(ctx, network, target.Listener.Addr().String())
					}
					return original(ctx, network, addr)
				}
			}
			_, err := Login(context.Background(), LoginRequest{SigninBaseURL: source.URL, HTTPClient: &http.Client{Transport: base}, TOTP: TOTPFuncForTest(func(context.Context) (string, error) { codes.Add(1); return "synthetic-totp", nil })})
			var failure *LoginFailure
			if !errors.As(err, &failure) || failure.Step != StepTOTPPage {
				t.Errorf("error = %v, want TOTP page failure", err)
			}
			if hits.Load() != 0 || codes.Load() != 0 {
				t.Errorf("foreign hits %d, code requests %d", hits.Load(), codes.Load())
			}
		})
	}
}

func TestLoginGETRefusesSchemeChange(t *testing.T) {
	for _, step := range []FailureStep{StepSigninPage, StepTOTPPage} {
		t.Run(string(step), func(t *testing.T) {
			var hits atomic.Int64
			plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				hits.Add(1)
				_, _ = w.Write([]byte(`<input name="_csrf" value="csrf">`))
			}))
			defer plain.Close()
			var sourceURL string
			source := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost {
					http.Redirect(w, r, twoFAPathMatch, http.StatusFound)
					return
				}
				if step == StepSigninPage || r.URL.Path == twoFAPathMatch {
					http.Redirect(w, r, strings.Replace(sourceURL, "https:", "http:", 1)+"/private-path?secret=synthetic-secret", http.StatusFound)
					return
				}
				_, _ = w.Write([]byte(`<input name="_csrf" value="csrf">`))
			}))
			defer source.Close()
			sourceURL = source.URL
			_, err := Login(context.Background(), LoginRequest{SigninBaseURL: source.URL, HTTPClient: &http.Client{Transport: routeLoginHTTP(t, source, plain)}, TOTP: TOTPFuncForTest(func(context.Context) (string, error) {
				t.Error("TOTP requested after downgrade")
				return "synthetic-totp", nil
			})})
			var failure *LoginFailure
			if !errors.As(err, &failure) || failure.Step != step {
				t.Errorf("error = %v, want %s", err, step)
			}
			if hits.Load() != 0 {
				t.Errorf("HTTP target hits = %d", hits.Load())
			}
		})
	}
}
