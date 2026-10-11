package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type discoveryTokenSource struct{ calls atomic.Int32 }

func (s *discoveryTokenSource) Token(context.Context) (Token, error) {
	s.calls.Add(1)
	return Token{AccessToken: "canary-secret", ExpiresAt: time.Now().Add(time.Hour)}, nil
}
func (*discoveryTokenSource) Invalidate(string) {}

func TestDiscoveryReflectionBeforeRetryRefreshCapture(t *testing.T) {
	for _, status := range []int{200, 401, 429, 500, 502, 503, 504} {
		for _, body := range []string{`{"name":"canary-secret"}`, `{"metadata":"canary-\u0073ecret"}`, `malformed canary-secret`} {
			t.Run(fmt.Sprintf("%d/%s", status, body), func(t *testing.T) {
				var calls, captures atomic.Int32
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					w.WriteHeader(status)
					_, _ = w.Write([]byte(body))
				}))
				defer srv.Close()
				source := &discoveryTokenSource{}
				c := New(Config{HTTPClient: srv.Client(), TokenSource: source, RetryCount: 2, Capture: func(Capture) { captures.Add(1) }})
				var out any
				got, err := c.DoDiscoveryJSONStatus(context.Background(), Request{Operation: "test.Discovery", URL: srv.URL}, &out, nil)
				var api *APIError
				if !errors.As(err, &api) || api.Code != "InvalidResponse" || api.StatusCode != status || got != status || api.Retryable {
					t.Fatalf("status=%d error=%+v", got, err)
				}
				if strings.Contains(err.Error(), "canary") || out != nil || calls.Load() != 1 || source.calls.Load() != 1 || captures.Load() != 0 {
					t.Fatalf("guard leaked or scheduled work: calls=%d tokens=%d captures=%d", calls.Load(), source.calls.Load(), captures.Load())
				}
			})
		}
	}
}

func TestDiscoveryBodyBoundaryAndRedirect(t *testing.T) {
	for _, n := range []int{DiscoveryMaxBody, DiscoveryMaxBody + 1} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(strings.Repeat(" ", n))) }))
		c := New(Config{HTTPClient: srv.Client()})
		status, err := c.DoDiscoveryJSONStatus(context.Background(), Request{URL: srv.URL}, nil, nil)
		srv.Close()
		if n == DiscoveryMaxBody && err != nil {
			t.Fatal(err)
		}
		if n > DiscoveryMaxBody && (!errors.Is(err, ErrBodyTooLarge) || status != 200) {
			t.Fatalf("status=%d err=%v", status, err)
		}
	}
	var redirected atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/mutation" {
			redirected.Add(1)
			return
		}
		http.Redirect(w, r, "/mutation", http.StatusTemporaryRedirect)
	}))
	defer srv.Close()
	c := New(Config{HTTPClient: srv.Client()})
	_, err := c.DoDiscoveryJSONStatus(context.Background(), Request{URL: srv.URL}, nil, nil)
	if err == nil || redirected.Load() != 0 {
		t.Fatalf("redirect err=%v calls=%d", err, redirected.Load())
	}
}

type heldDiscoverySource struct {
	entered       chan struct{}
	release       chan struct{}
	calls         atomic.Int32
	invalidations atomic.Int32
}

func (s *heldDiscoverySource) Token(context.Context) (Token, error) {
	if s.calls.Add(1) == 1 {
		close(s.entered)
		<-s.release
	}
	return Token{AccessToken: "replacement-token", ExpiresAt: time.Now().Add(time.Hour)}, nil
}
func (s *heldDiscoverySource) Invalidate(string) { s.invalidations.Add(1) }

func TestDiscoveryRefreshContention(t *testing.T) {
	for _, unauthorized := range []bool{false, true} {
		t.Run(fmt.Sprintf("unauthorized=%v", unauthorized), func(t *testing.T) {
			source := &heldDiscoverySource{entered: make(chan struct{}), release: make(chan struct{})}
			c := New(Config{TokenSource: source})
			if unauthorized {
				c.token = Token{AccessToken: "sent-token", ExpiresAt: time.Now().Add(time.Hour)}
			}
			holder := make(chan error, 1)
			go func() {
				if unauthorized {
					holder <- c.invalidateAndRefresh(context.Background(), "sent-token")
				} else {
					holder <- c.refreshToken(context.Background())
				}
			}()
			<-source.entered
			ctx, cancel := context.WithCancel(context.Background())
			waiter := make(chan error, 1)
			go func() {
				if unauthorized {
					waiter <- c.invalidateAndRefresh(ctx, "never-sent")
				} else {
					waiter <- c.refreshToken(ctx)
				}
			}()
			cancel()
			close(source.release)
			if err := <-holder; err != nil {
				t.Fatal(err)
			}
			if err := <-waiter; !errors.Is(err, context.Canceled) {
				t.Fatalf("waiter=%v", err)
			}
			want := int32(0)
			if unauthorized {
				want = 1
			}
			if source.calls.Load() != 1 || source.invalidations.Load() != want || c.currentToken().AccessToken != "replacement-token" {
				t.Fatal("canceled waiter changed shared token state")
			}
		})
	}
}

func TestDiscoveryRotatedCredentialGuard(t *testing.T) {
	var attempts atomic.Int32
	source := &rotatingDiscoverySource{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		if r.Header.Get("Authorization") == "Bearer old-token" {
			w.WriteHeader(401)
			_, _ = w.Write([]byte(`{"message":"expired"}`))
			return
		}
		_, _ = w.Write([]byte(`{"name":"new-\u0074oken"}`))
	}))
	defer srv.Close()
	c := New(Config{HTTPClient: srv.Client(), TokenSource: source})
	_, err := c.DoDiscoveryJSONStatus(context.Background(), Request{URL: srv.URL}, nil, nil)
	var api *APIError
	if !errors.As(err, &api) || api.Code != "InvalidResponse" || api.StatusCode != 200 || attempts.Load() != 2 || source.calls.Load() != 2 {
		t.Fatalf("err=%v attempts=%d tokens=%d", err, attempts.Load(), source.calls.Load())
	}
}

type rotatingDiscoverySource struct{ calls atomic.Int32 }

func (s *rotatingDiscoverySource) Token(context.Context) (Token, error) {
	token := "old-token"
	if s.calls.Add(1) > 1 {
		token = "new-token"
	}
	return Token{AccessToken: token, ExpiresAt: time.Now().Add(time.Hour)}, nil
}
func (*rotatingDiscoverySource) Invalidate(string) {}

func TestDiscoveryDoesNotSendCookies(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "" {
			t.Error("discovery sent cookies")
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	jar.SetCookies(u, []*http.Cookie{{Name: "session", Value: "cookie-canary", Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode}})
	client := srv.Client()
	client.Jar = jar
	c := New(Config{HTTPClient: client})
	if _, err := c.DoDiscoveryJSONStatus(context.Background(), Request{URL: srv.URL}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if client.Jar != jar {
		t.Fatal("changed shared cookie jar")
	}
}

func TestDiscoveryRetryStartsAndHTTP500Once(t *testing.T) {
	for _, status := range []int{429, 500, 502, 503, 504} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{"code":"OriginalFailure","message":"unavailable"}`))
			}))
			defer srv.Close()
			c := New(Config{HTTPClient: srv.Client(), RetryCount: 2, RetryInterval: time.Nanosecond})
			var starts int
			before := func(ctx context.Context) error { starts++; return ctx.Err() }
			actual, err := c.DoDiscoveryJSONStatus(context.Background(), Request{URL: srv.URL}, nil, before)
			var api *APIError
			want := int32(3)
			if status == 500 {
				want = 1
			}
			if !errors.As(err, &api) || api.Code != "OriginalFailure" || api.StatusCode != status || actual != status || calls.Load() != want || starts != int(want) {
				t.Fatalf("actual=%d err=%v calls=%d starts=%d", actual, err, calls.Load(), starts)
			}
		})
	}
}

func TestDiscoveryDebugAndForeignRedirect(t *testing.T) {
	var logs bytes.Buffer
	var captures, foreignCalls atomic.Int32
	foreign := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { foreignCalls.Add(1) }))
	defer foreign.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Set-Cookie", "session=header-canary")
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, foreign.URL, http.StatusTemporaryRedirect)
			return
		}
		_, _ = w.Write([]byte(`{"canary-\u0073ecret":"metadata"}`))
	}))
	defer source.Close()
	c := New(Config{HTTPClient: source.Client(), TokenSource: &discoveryTokenSource{}, Logger: slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})), Capture: func(Capture) { captures.Add(1) }})
	for _, path := range []string{"/reflection", "/redirect"} {
		if _, err := c.DoDiscoveryJSONStatus(context.Background(), Request{URL: source.URL + path}, nil, nil); err == nil {
			t.Fatalf("accepted %s", path)
		}
	}
	for _, canary := range []string{"canary-secret", "header-canary", "Authorization", "Cookie"} {
		if strings.Contains(logs.String(), canary) {
			t.Fatalf("debug leaked %s", canary)
		}
	}
	if foreignCalls.Load() != 0 || captures.Load() != 0 {
		t.Fatalf("foreign=%d captures=%d", foreignCalls.Load(), captures.Load())
	}
}

func TestDiscoveryRefreshWaiterDeadline(t *testing.T) {
	for _, unauthorized := range []bool{false, true} {
		t.Run(fmt.Sprintf("unauthorized=%v", unauthorized), func(t *testing.T) {
			source := &heldDiscoverySource{entered: make(chan struct{}), release: make(chan struct{})}
			c := New(Config{TokenSource: source})
			if unauthorized {
				c.token = Token{AccessToken: "sent-token", ExpiresAt: time.Now().Add(time.Hour)}
			}
			holder := make(chan error, 1)
			go func() {
				if unauthorized {
					holder <- c.invalidateAndRefresh(context.Background(), "sent-token")
				} else {
					holder <- c.refreshToken(context.Background())
				}
			}()
			<-source.entered
			ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
			defer cancel()
			waiter := make(chan error, 1)
			go func() {
				if unauthorized {
					waiter <- c.invalidateAndRefresh(ctx, "sent-token")
				} else {
					waiter <- c.refreshToken(ctx)
				}
			}()
			<-ctx.Done()
			close(source.release)
			if err := <-holder; err != nil {
				t.Fatal(err)
			}
			if err := <-waiter; !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("waiter=%v", err)
			}
			want := int32(0)
			if unauthorized {
				want = 1
			}
			if source.calls.Load() != 1 || source.invalidations.Load() != want || c.currentToken().AccessToken != "replacement-token" {
				t.Fatal("expired waiter changed shared state")
			}
		})
	}
}

type discoveryRoundTripper func(*http.Request) (*http.Response, error)

func (f discoveryRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

type discoveryCloseFailure struct{ io.Reader }

func (discoveryCloseFailure) Close() error { return errors.New("synthetic close failure") }

func TestDiscoveryReflectionBeforeBodyCloseError(t *testing.T) {
	c := New(Config{TokenSource: &discoveryTokenSource{}, HTTPClient: &http.Client{Transport: discoveryRoundTripper(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: discoveryCloseFailure{strings.NewReader(`{"name":"canary-secret"}`)}, Request: req}, nil
	})}})
	status, err := c.DoDiscoveryJSONStatus(context.Background(), Request{URL: "https://example.test/v1/projects"}, nil, nil)
	var api *APIError
	if !errors.As(err, &api) || api.Code != "InvalidResponse" || api.StatusCode != 200 || status != 200 {
		t.Fatalf("status=%d err=%v", status, err)
	}
}

func TestDiscoveryScannerErrorsBeforeStatus(t *testing.T) {
	for _, status := range []int{200, 401, 503} {
		for _, body := range []string{`{"unused":1e999,"data":[{"name":"canary\u002dsecret"}]}`, `{"unused":?,"data":[]}`, `{"unused":1e999,`} {
			t.Run(fmt.Sprintf("%d/%s", status, body), func(t *testing.T) {
				var attempts atomic.Int32
				source := &discoveryTokenSource{}
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					attempts.Add(1)
					w.WriteHeader(status)
					_, _ = w.Write([]byte(body))
				}))
				defer srv.Close()
				c := New(Config{HTTPClient: srv.Client(), TokenSource: source, RetryCount: 2, RetryInterval: time.Nanosecond})
				var out json.RawMessage
				got, err := c.DoDiscoveryJSONStatus(context.Background(), Request{URL: srv.URL}, &out, nil)
				var api *APIError
				if !errors.As(err, &api) || api.Code != "InvalidResponse" || api.StatusCode != status || got != status || api.Retryable || attempts.Load() != 1 || source.calls.Load() != 1 || len(out) != 0 {
					t.Fatalf("status=%d err=%v attempts=%d tokens=%d", got, err, attempts.Load(), source.calls.Load())
				}
			})
		}
	}
}

func TestDiscoveryPreviousCredentialAfterRotation(t *testing.T) {
	source := &rotatingDiscoverySource{}
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		if r.Header.Get("Authorization") == "Bearer old-token" {
			w.WriteHeader(401)
			_, _ = w.Write([]byte(`{"message":"expired"}`))
			return
		}
		_, _ = w.Write([]byte(`{"name":"old\u002dtoken"}`))
	}))
	defer srv.Close()
	c := New(Config{HTTPClient: srv.Client(), TokenSource: source})
	var out json.RawMessage
	_, err := c.DoDiscoveryJSONStatus(context.Background(), Request{URL: srv.URL}, &out, nil)
	var api *APIError
	if !errors.As(err, &api) || api.Code != "InvalidResponse" || api.StatusCode != 200 || attempts.Load() != 2 || len(out) != 0 {
		t.Fatalf("err=%v attempts=%d", err, attempts.Load())
	}
}

func TestDiscoveryScannerPreservesLargeJSONNumbers(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"unused":1e999,"name":"safe"}`))
	}))
	defer srv.Close()
	c := New(Config{HTTPClient: srv.Client(), TokenSource: &discoveryTokenSource{}})
	var out struct {
		Name string `json:"name"`
	}
	status, err := c.DoDiscoveryJSONStatus(context.Background(), Request{URL: srv.URL}, &out, nil)
	if err != nil || status != 200 || out.Name != "safe" {
		t.Fatalf("status=%d error=%v output=%+v", status, err, out)
	}
}
