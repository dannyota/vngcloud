package cdn

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"sync/atomic"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/endpoints"
	"danny.vn/vngcloud/internal/transport"
)

// newTestConfig builds a Config whose CDNDocs endpoint points at an
// httptest server, using the given HTTP client so a test can control
// cookies and redirects directly instead of going through vngcloud.LoadConfig.
func newTestConfig(cdnDocsURL string, httpClient *http.Client, ts transport.TokenSource) vngcloud.Config {
	return core.NewTestConfig("hcm-3", "", endpoints.Set{CDNDocs: cdnDocsURL},
		transport.New(transport.Config{HTTPClient: httpClient, TokenSource: ts}))
}

// failTokenSource fails the test if Token or Invalidate is ever called, so a
// test using it proves ListIPRanges never consulted a credentials provider.
type failTokenSource struct{ t *testing.T }

func (f failTokenSource) Token(context.Context) (transport.Token, error) {
	f.t.Helper()
	f.t.Fatal("credentials provider called for an unauthenticated request")
	return transport.Token{}, nil
}

func (f failTokenSource) Invalidate(string) {
	f.t.Helper()
	f.t.Fatal("credentials provider invalidated for an unauthenticated request")
}

func TestListIPRangesZeroConfig(t *testing.T) {
	c := New(vngcloud.Config{})
	if _, err := c.ListIPRanges(context.Background(), nil); !errors.Is(err, vngcloud.ErrInvalidConfig) {
		t.Fatalf("ListIPRanges() err = %v, want ErrInvalidConfig", err)
	}
}

// TestListIPRangesSendsNoAuthOrCookie checks the design's core security
// property: the request carries no Authorization header and no cookie, even
// when the client's credentials provider would fail loudly if asked for a
// token and its HTTP client has a cookie jar holding a cookie for the docs
// host.
func TestListIPRangesSendsNoAuthOrCookie(t *testing.T) {
	fixture, err := os.ReadFile("../testdata/cdn/faq-vcdn.html")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "" {
			t.Errorf("Authorization = %q, want none", got)
		}
		if _, err := r.Cookie("session"); err == nil {
			t.Error("Cookie reached the server")
		}
		if got := r.Header.Get("Accept"); got != "text/html" {
			t.Errorf("Accept = %q, want text/html", got)
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(fixture)
	}))
	defer server.Close()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New() error = %v", err)
	}
	serverURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("url.Parse() error = %v", err)
	}
	jar.SetCookies(serverURL, []*http.Cookie{{Name: "session", Value: "secret", HttpOnly: true, SameSite: http.SameSiteLaxMode}}) //nolint:gosec // Secure must stay false: the test server is plain HTTP, and the jar would never attach a Secure cookie to it
	httpClient := &http.Client{Jar: jar}

	client := New(newTestConfig(server.URL, httpClient, failTokenSource{t}))
	out, err := client.ListIPRanges(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListIPRanges() error = %v", err)
	}
	if len(out.Items) != len(wantIPRanges) {
		t.Fatalf("got %d items, want %d", len(out.Items), len(wantIPRanges))
	}
	if out.Source != server.URL {
		t.Fatalf("Source = %q, want %q", out.Source, server.URL)
	}
}

func TestListIPRangesNotFoundIsAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client := New(newTestConfig(server.URL, nil, nil))
	_, err := client.ListIPRanges(context.Background(), nil)
	var apiErr *vngcloud.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %v, want *vngcloud.APIError", err)
	}
	if apiErr.StatusCode != http.StatusNotFound {
		t.Fatalf("StatusCode = %d, want 404", apiErr.StatusCode)
	}
	if apiErr.Code != "NotFound" {
		t.Fatalf("Code = %q, want NotFound", apiErr.Code)
	}
	if apiErr.Operation != "cdn.ListIPRanges" {
		t.Fatalf("Operation = %q, want cdn.ListIPRanges", apiErr.Operation)
	}
	if errors.Is(err, ErrPageFormat) {
		t.Fatal("a 404 must not match ErrPageFormat")
	}
	if errors.Is(err, vngcloud.ErrAuth) {
		t.Fatal("a docs-host error must never match ErrAuth: the request carried no credential")
	}
	if !errors.Is(err, vngcloud.ErrNotFound) {
		t.Fatal("a 404 must match ErrNotFound, the same sentinel every other SDK error wraps for it")
	}
	if apiErr.Retryable {
		t.Fatal("Retryable = true, want false for 404")
	}
}

// TestListIPRangesUnauthorizedDoesNotMatchErrAuth checks the design's
// exception to the usual status mapping: unlike every authenticated call, a
// 401 here must never match ErrAuth, because ListIPRanges sends no
// credential for the docs host to reject.
func TestListIPRangesUnauthorizedDoesNotMatchErrAuth(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	client := New(newTestConfig(server.URL, nil, nil))
	_, err := client.ListIPRanges(context.Background(), nil)
	var apiErr *vngcloud.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %v, want *vngcloud.APIError", err)
	}
	if apiErr.StatusCode != http.StatusUnauthorized {
		t.Fatalf("StatusCode = %d, want 401", apiErr.StatusCode)
	}
	if errors.Is(err, vngcloud.ErrAuth) {
		t.Fatal("a 401 from the docs host must not match ErrAuth: the request carried no credential")
	}
	if apiErr.Retryable {
		t.Fatal("Retryable = true, want false for 401")
	}
}

// TestListIPRangesForbiddenMatchesErrPermission checks that a 403 wraps
// ErrPermission, the same sentinel every other SDK error wraps for it.
func TestListIPRangesForbiddenMatchesErrPermission(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()

	client := New(newTestConfig(server.URL, nil, nil))
	_, err := client.ListIPRanges(context.Background(), nil)
	if !errors.Is(err, vngcloud.ErrPermission) {
		t.Fatalf("error = %v, want to match ErrPermission", err)
	}
	if errors.Is(err, vngcloud.ErrAuth) {
		t.Fatal("a 403 must not match ErrAuth")
	}
}

// TestListIPRangesServiceUnavailableIsRetryable checks that a 503, unlike a
// 404 or a 500, is reported as retryable.
func TestListIPRangesServiceUnavailableIsRetryable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	client := New(newTestConfig(server.URL, nil, nil))
	_, err := client.ListIPRanges(context.Background(), nil)
	var apiErr *vngcloud.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %v, want *vngcloud.APIError", err)
	}
	if apiErr.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("StatusCode = %d, want 503", apiErr.StatusCode)
	}
	if !apiErr.Retryable {
		t.Fatal("Retryable = false, want true for 503")
	}
}

func TestListIPRangesServerErrorIsAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	client := New(newTestConfig(server.URL, nil, nil))
	_, err := client.ListIPRanges(context.Background(), nil)
	var apiErr *vngcloud.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %v, want *vngcloud.APIError", err)
	}
	if apiErr.StatusCode != http.StatusInternalServerError {
		t.Fatalf("StatusCode = %d, want 500", apiErr.StatusCode)
	}
	if apiErr.Code != "ServerError" {
		t.Fatalf("Code = %q, want ServerError", apiErr.Code)
	}
	if errors.Is(err, ErrPageFormat) {
		t.Fatal("a 500 must not match ErrPageFormat")
	}
	if apiErr.Retryable {
		t.Fatal("Retryable = true, want false for 500")
	}
}

func TestListIPRangesWrongContentType(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()

	client := New(newTestConfig(server.URL, nil, nil))
	_, err := client.ListIPRanges(context.Background(), nil)
	if !errors.Is(err, ErrPageFormat) {
		t.Fatalf("error = %v, want ErrPageFormat", err)
	}
}

// endlessBody yields 'a' forever. A test serving it as a response body, with
// the package's fixed maxBodyBytes cap, proves the cap is enforced by
// streaming (io.LimitReader in the transport) rather than by measuring a
// large but finite byte slice after reading it all into memory: a body this
// server would happily keep sending forever still fails fast.
type endlessBody struct{}

func (endlessBody) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'a'
	}
	return len(p), nil
}

func TestListIPRangesBodyTooLarge(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = io.Copy(w, endlessBody{})
	}))
	defer server.Close()

	client := New(newTestConfig(server.URL, nil, nil))
	_, err := client.ListIPRanges(context.Background(), nil)
	if !errors.Is(err, ErrPageFormat) {
		t.Fatalf("error = %v, want ErrPageFormat", err)
	}
}

// TestListIPRangesLargeBody503IsAPIError checks the design's status-wins
// rule: a body over the cap maps to ErrPageFormat only when the status is
// 200. A non-200 status with an oversized body is instead an *APIError for
// that status, retryable when the status is.
func TestListIPRangesLargeBody503IsAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.Copy(w, endlessBody{})
	}))
	defer server.Close()

	client := New(newTestConfig(server.URL, nil, nil))
	_, err := client.ListIPRanges(context.Background(), nil)
	if errors.Is(err, ErrPageFormat) {
		t.Fatal("a 503 with an oversized body must not match ErrPageFormat: the status wins")
	}
	var apiErr *vngcloud.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %v (%T), want *vngcloud.APIError", err, err)
	}
	if apiErr.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("StatusCode = %d, want 503", apiErr.StatusCode)
	}
	if !apiErr.Retryable {
		t.Fatal("Retryable = false, want true for 503")
	}
}

// TestListIPRangesSameHostRedirectSucceeds checks that a same-host redirect
// (for example a trailing-slash normalization by the docs host) is followed.
func TestListIPRangesSameHostRedirectSucceeds(t *testing.T) {
	fixture, err := os.ReadFile("../testdata/cdn/faq-vcdn.html")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	defer server.Close()
	mux.HandleFunc("/faq/vcdn", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, server.URL+"/faq/vcdn/", http.StatusMovedPermanently)
	})
	mux.HandleFunc("/faq/vcdn/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(fixture)
	})

	client := New(newTestConfig(server.URL+"/faq/vcdn", nil, nil))
	out, err := client.ListIPRanges(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListIPRanges() error = %v", err)
	}
	if len(out.Items) != len(wantIPRanges) {
		t.Fatalf("got %d items, want %d", len(out.Items), len(wantIPRanges))
	}
}

// TestListIPRangesCrossHostRedirectFails checks that a redirect to a
// different host is refused rather than followed, matching every other SDK
// request.
func TestListIPRangesCrossHostRedirectFails(t *testing.T) {
	var targetHits atomic.Int64
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetHits.Add(1)
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html></html>"))
	}))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/", http.StatusMovedPermanently)
	}))
	defer source.Close()

	client := New(newTestConfig(source.URL, nil, nil))
	_, err := client.ListIPRanges(context.Background(), nil)
	if err == nil {
		t.Fatal("expected an error for a cross-host redirect")
	}
	if errors.Is(err, ErrPageFormat) {
		t.Fatal("a refused redirect is a network failure, not ErrPageFormat")
	}
	var apiErr *vngcloud.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %v (%T), want *vngcloud.APIError", err, err)
	}
	if targetHits.Load() != 0 {
		t.Fatalf("redirect target was hit %d times, want 0: a refused redirect must never be followed", targetHits.Load())
	}
}
