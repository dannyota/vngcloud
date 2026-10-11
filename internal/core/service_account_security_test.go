package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestServiceAccountPrivateClient(t *testing.T) {
	for _, timeout := range []time.Duration{-time.Second, 0, time.Second} {
		client := newServiceAccountHTTPClient(timeout, nil)
		want := timeout
		if want <= 0 {
			want = 120 * time.Second
		}
		if client.Timeout != want || client.Jar != nil {
			t.Fatal("timeout or cookie jar")
		}
		rt, ok := client.Transport.(*http.Transport)
		if !ok || rt == http.DefaultTransport || rt.TLSClientConfig != nil && rt.TLSClientConfig.InsecureSkipVerify {
			t.Fatal("unsafe transport")
		}
		other := newServiceAccountHTTPClient(timeout, nil)
		if client.Transport == other.Transport {
			t.Fatal("shared transport")
		}
		rt.CloseIdleConnections()
		other.Transport.(*http.Transport).CloseIdleConnections()
	}
}

func TestServiceAccountTLSRefusal(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("untrusted TLS reached handler") }))
	defer server.Close()
	rt := newServiceAccountHTTPClient(time.Second, nil).Transport.(*http.Transport)
	rt.Proxy = nil
	rt.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	}
	defer rt.CloseIdleConnections()
	source := newServiceAccountTokenSource(&serviceAccountCredentials{clientID: "client", secret: newServiceAccountSecret("secret")}, time.Second, nil, rt, time.Now)
	_, err := source.Token(context.Background())
	var login *ServiceAccountLoginError
	if !errors.As(err, &login) || login.Reason != "token request failed" || !errors.Is(err, ErrAuth) {
		t.Fatal(err)
	}
}

func TestServiceAccountHookIsolationAndSafeOutput(t *testing.T) {
	const marker = "synthetic-secret-canary"
	var log bytes.Buffer
	captures, hooks := 0, 0
	public := serviceAccountRoundTripper(func(*http.Request) (*http.Response, error) { hooks++; return nil, errors.New("public hook") })
	private := serviceAccountRoundTripper(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != serviceAccountTokenURL {
			t.Fatal("endpoint override reached exchange")
		}
		return serviceAccountResponse(401, `{"errors":[{"code":"`+marker+`","message":"`+marker+`"}],"nested":"{\"secret\":\"`+marker+`\"}"}`), nil
	})
	opts := []Option{WithRegion("hcm-3"), WithServiceAccountCredentials("client", marker, true), WithHTTPClient(&http.Client{Transport: public}), WithTransport(public), WithEndpointOverrides(EndpointOverrides{Token: "https://override.example.test", IAM: "https://override.example.test", Dashboard: "https://override.example.test"}), WithResponseCapture(func(ResponseCapture) { captures++ }), WithLogger(slog.New(slog.NewTextHandler(&log, &slog.HandlerOptions{Level: slog.LevelDebug}))), serviceAccountTestOption(private, time.Now)}
	cfg, err := NewConfig(opts...)
	if err != nil {
		t.Fatal(err)
	}
	err = cfg.Authenticate(context.Background())
	if !errors.Is(err, ErrAuth) {
		t.Fatal(err)
	}
	if hooks != 0 || captures != 0 {
		t.Fatal("public hook or capture received exchange")
	}
	values := []any{cfg, ClientOf(cfg), opts[1], err}
	settings := defaultClientConfig()
	opts[1].apply(&settings)
	values = append(values, settings, settings.serviceAccount, settings.serviceAccount.secret)
	for _, value := range values {
		for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x"} {
			if strings.Contains(fmt.Sprintf(verb, value), marker) {
				t.Fatal("format leaked")
			}
		}
		b, jsonErr := json.Marshal(value)
		if jsonErr != nil {
			t.Fatal(jsonErr)
		}
		if strings.Contains(string(b), marker) {
			t.Fatal("JSON leaked")
		}
		if text, ok := value.(interface{ MarshalText() ([]byte, error) }); ok {
			b, textErr := text.MarshalText()
			if textErr != nil || strings.Contains(string(b), marker) {
				t.Fatal("text leaked")
			}
		}
		logger := slog.New(slog.NewTextHandler(&log, &slog.HandlerOptions{Level: slog.LevelDebug}))
		logger.DebugContext(context.Background(), "format", "value", value)
	}
	for cause := err; cause != nil; cause = errors.Unwrap(cause) {
		if strings.Contains(fmt.Sprintf("%#v", cause), marker) {
			t.Fatal("chain leaked")
		}
	}
	if strings.Contains(log.String(), marker) || !strings.Contains(log.String(), "exchange started") || !strings.Contains(log.String(), "ok=false") {
		t.Fatal("unsafe exchange log")
	}
}

func TestServiceAccountFailureCanaries(t *testing.T) {
	const marker = "synthetic-canary"
	for _, mode := range []string{"network", "decode", "redirect", "reject"} {
		rt := serviceAccountRoundTripper(func(r *http.Request) (*http.Response, error) {
			echo := marker + " " + r.Header.Get("Authorization") + " https://" + marker + " Cookie=" + marker
			if mode == "network" {
				return nil, errors.New(echo)
			}
			status := 200
			if mode == "redirect" {
				status = 302
			}
			if mode == "reject" {
				status = 500
			}
			response := serviceAccountResponse(status, echo)
			response.Header.Set("Location", "https://"+marker)
			response.Header.Set("Set-Cookie", marker)
			return response, nil
		})
		source := newServiceAccountTokenSource(&serviceAccountCredentials{clientID: "client", secret: newServiceAccountSecret(marker)}, time.Second, nil, rt, time.Now)
		_, err := source.Token(context.Background())
		if !errors.Is(err, ErrAuth) {
			t.Fatal(err)
		}
		for cause := err; cause != nil; cause = errors.Unwrap(cause) {
			for _, verb := range []string{"%v", "%+v", "%#v"} {
				if strings.Contains(fmt.Sprintf(verb, cause), marker) || strings.Contains(fmt.Sprintf(verb, cause), "Basic ") {
					t.Fatal("failure leaked")
				}
			}
		}
	}
}
