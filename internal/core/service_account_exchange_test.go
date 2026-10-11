package core

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

func TestServiceAccountExchangeRequest(t *testing.T) {
	fixture, err := os.ReadFile("../../testdata/auth/service_account_token.json")
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	rt := serviceAccountRoundTripper(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Method != "POST" || r.URL.String() != serviceAccountTokenURL {
			t.Fatal("wrong method or endpoint")
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		if string(body) != `{"grant_type":"client_credentials"}` {
			t.Fatalf("body = %s", body)
		}
		if r.Header.Get("Content-Type") != "application/json" || r.Header.Get("Accept") != "application/json" || r.Header.Get("Cookie") != "" {
			t.Fatal("wrong headers")
		}
		id, secret, ok := r.BasicAuth()
		if !ok || id != "client" || secret != "secret" {
			t.Fatal("wrong Basic auth")
		}
		return serviceAccountResponse(200, string(fixture)), nil
	})
	cfg, err := NewConfig(WithRegion("hcm-3"), WithServiceAccountCredentials("client", "secret", true), serviceAccountTestOption(rt, time.Now))
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Authenticate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal("exchange count")
	}
}

func TestServiceAccountExchangeStatuses(t *testing.T) {
	fixture, err := os.ReadFile("../../testdata/auth/service_account_authentication_failed.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, status := range []int{400, 401, 403, 404, 409, 429, 500, 502, 503, 504, 301, 302, 303, 307, 308, 201, 204} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			calls := 0
			rt := serviceAccountRoundTripper(func(*http.Request) (*http.Response, error) {
				calls++
				r := serviceAccountResponse(status, string(fixture))
				r.Header.Set("Location", serviceAccountTokenURL)
				return r, nil
			})
			cfg, err := NewConfig(WithRegion("hcm-3"), WithServiceAccountCredentials("client", "secret", true), WithRetry(5, 0), serviceAccountTestOption(rt, time.Now))
			if err != nil {
				t.Fatal(err)
			}
			err = cfg.Authenticate(context.Background())
			var login *ServiceAccountLoginError
			if !errors.As(err, &login) || !errors.Is(err, ErrAuth) || login.Status != status {
				t.Fatalf("failure = %v", err)
			}
			reason := "token endpoint rejected request"
			if status >= 300 && status < 400 {
				reason = "token endpoint redirected request"
			}
			if login.Reason != reason || calls != 1 {
				t.Fatalf("reason/count = %s/%d", login.Reason, calls)
			}
		})
	}
}

func TestServiceAccountExchangeDecode(t *testing.T) {
	// These adversarial bodies are synthetic mutations of the recorded shape.
	for _, body := range []string{
		`null`, `[]`, `{}`, `{"access_token":"token","expires_in":30}`, `{"access_token":"token","expires_in":0}`,
		`{"access_token":"token","expires_in":-1}`, `{"access_token":"token","expires_in":31.0}`, `{"access_token":"token","expires_in":"1800"}`,
		`{"access_token":"token","expires_in":null}`, `{"access_token":null,"expires_in":1800}`, `{"access_token":1,"expires_in":1800}`,
		`{"access_token":"","expires_in":1800}`, `{"access_token":"a b","expires_in":1800}`, `{"access_token":"a\u0000b","expires_in":1800}`,
		`{"access_token":"token","expires_in":1800,"token_type":null}`, `{"access_token":"token","expires_in":1800,"token_type":"MAC"}`,
		`{"access_token":"token","expires_in":1800,"token_type":3}`, `{"access_token":"token","expires_in":1800} {}`,
		`{"access_token":"token","access_token":"other","expires_in":1800}`, `{"access_token":"token","expires_in":1800,"expires_in":1801}`,
		`{"access_token":"token","expires_in":1800,"token_type":"bearer","token_type":"Bearer"}`,
		`{"access_token":"token","expires_in":9223372037}`, `{"access_token":"token","expires_in":9223372036854775808}`,
		`{"access_token":"token","expires_in":1800`, strings.Repeat(" ", 65537),
	} {
		t.Run(body[:min(len(body), 80)], func(t *testing.T) {
			s := newServiceAccountTokenSource(&serviceAccountCredentials{clientID: "client", secret: newServiceAccountSecret("secret")}, time.Minute, nil, serviceAccountRoundTripper(func(*http.Request) (*http.Response, error) { return serviceAccountResponse(200, body), nil }), time.Now)
			token, err := s.Token(context.Background())
			var login *ServiceAccountLoginError
			if !errors.As(err, &login) || login.Reason != "invalid token response" || !errors.Is(err, ErrAuth) || token.AccessToken != "" {
				t.Fatalf("token/error = %v/%v", token, err)
			}
		})
	}
	for _, body := range []string{`{"access_token":"token","expires_in":31}`, `{"access_token":"token","expires_in":1800,"token_type":"bEaReR","unknown":{"a":1},"refresh_token":"discarded"}`} {
		cfg, err := NewConfig(WithRegion("hcm-3"), WithServiceAccountCredentials("client", "secret", true), serviceAccountTestOption(serviceAccountRoundTripper(func(*http.Request) (*http.Response, error) { return serviceAccountResponse(200, body), nil }), time.Now))
		if err != nil {
			t.Fatal(err)
		}
		if err := cfg.Authenticate(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
}

func TestServiceAccountExchangeExpiryAndResponseBound(t *testing.T) {
	start := time.Unix(10000, 0)
	for _, elapsed := range []time.Duration{0, time.Second - time.Nanosecond, time.Second, 2 * time.Second} {
		now := start
		s := newServiceAccountTokenSource(&serviceAccountCredentials{clientID: "client", secret: newServiceAccountSecret("secret")}, time.Minute, nil, serviceAccountRoundTripper(func(*http.Request) (*http.Response, error) {
			now = start.Add(elapsed)
			return serviceAccountResponse(200, `{"access_token":"token","expires_in":31}`), nil
		}), func() time.Time { return now })
		token, err := s.Token(context.Background())
		if elapsed >= time.Second {
			if !errors.Is(err, ErrAuth) {
				t.Fatalf("elapsed %s: %v", elapsed, err)
			}
		} else if err != nil || !token.ExpiresAt.Equal(start.Add(31*time.Second)) {
			t.Fatalf("expiry = %v, %v", token.ExpiresAt, err)
		}
	}
	valid := `{"access_token":"token","expires_in":1800}`
	for _, size := range []int{65536, 65537} {
		body := valid + strings.Repeat(" ", size-len(valid))
		s := newServiceAccountTokenSource(&serviceAccountCredentials{clientID: "client", secret: newServiceAccountSecret("secret")}, time.Minute, nil, serviceAccountRoundTripper(func(*http.Request) (*http.Response, error) { return serviceAccountResponse(200, body), nil }), time.Now)
		_, err := s.Token(context.Background())
		if (err != nil) != (size > 65536) {
			t.Fatalf("size %d: %v", size, err)
		}
	}
}

func TestServiceAccountExchangeContextAndTimeout(t *testing.T) {
	for _, mode := range []string{"cancel", "deadline", "timeout", "network", "cancel-success"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "deadline" {
				var end context.CancelFunc
				ctx, end = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
				defer end()
			}
			timeout := time.Minute
			if mode == "timeout" {
				timeout = time.Millisecond
			}
			calls := 0
			rt := serviceAccountRoundTripper(func(r *http.Request) (*http.Response, error) {
				calls++
				switch mode {
				case "cancel":
					cancel()
					return nil, errors.New("synthetic-secret")
				case "cancel-success":
					cancel()
					return serviceAccountResponse(200, `{"access_token":"token","expires_in":1800}`), nil
				case "timeout":
					<-r.Context().Done()
					return nil, r.Context().Err()
				default:
					return nil, errors.New("synthetic-secret")
				}
			})
			s := newServiceAccountTokenSource(&serviceAccountCredentials{clientID: "client", secret: newServiceAccountSecret("secret")}, timeout, nil, rt, time.Now)
			token, err := s.Token(ctx)
			if token.AccessToken != "" || err == nil {
				t.Fatal("failed exchange returned token")
			}
			if ctx.Err() != nil {
				if !errors.Is(err, ctx.Err()) || errors.Is(err, ErrAuth) {
					t.Fatalf("context identity: %v", err)
				}
			} else if !errors.Is(err, ErrAuth) || errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("auth identity: %v", err)
			}
			if calls > 1 {
				t.Fatal("retried exchange")
			}
		})
	}
}

func TestServiceAccountExchangeRedirectDestinations(t *testing.T) {
	for _, status := range []int{301, 302, 303, 307, 308} {
		for _, destination := range []string{serviceAccountTokenURL, "https://foreign.example.test/token"} {
			calls := 0
			rt := serviceAccountRoundTripper(func(*http.Request) (*http.Response, error) {
				calls++
				response := serviceAccountResponse(status, "")
				response.Header.Set("Location", destination)
				return response, nil
			})
			source := newServiceAccountTokenSource(&serviceAccountCredentials{clientID: "client", secret: newServiceAccountSecret("secret")}, time.Minute, nil, rt, time.Now)
			_, err := source.Token(context.Background())
			var login *ServiceAccountLoginError
			if !errors.As(err, &login) || login.Reason != "token endpoint redirected request" || login.Status != status || calls != 1 {
				t.Fatalf("redirect %d: %v, sends %d", status, err, calls)
			}
		}
	}
}
