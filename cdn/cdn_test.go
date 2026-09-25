package cdn

import (
	"context"
	"errors"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
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

func TestListIPRangesBodyTooLarge(t *testing.T) {
	huge := make([]byte, maxBodyBytes+1)
	for i := range huge {
		huge[i] = 'a'
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write(huge)
	}))
	defer server.Close()

	client := New(newTestConfig(server.URL, nil, nil))
	_, err := client.ListIPRanges(context.Background(), nil)
	if !errors.Is(err, ErrPageFormat) {
		t.Fatalf("error = %v, want ErrPageFormat", err)
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
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
}
