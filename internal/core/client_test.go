package core

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"danny.vn/vngcloud/internal/endpoints"
	"danny.vn/vngcloud/internal/routes"
	"danny.vn/vngcloud/internal/transport"
)

// failingRoundTripper fails every RoundTrip with err, standing in for a
// refused connection or similar network failure that never reaches the
// server.
type failingRoundTripper struct {
	err error
}

func (f *failingRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, f.err
}

// TestDoJSONNetworkFailureErrorHidesQueryString drives a real dial failure
// through DoJSON and wrapTransportErr end to end, using a request URL that
// carries a token in its query string. The resulting *APIError must still
// name the cause without the token or the host, matching the direct
// APIError.Error() tests in errors_test.go.
func TestDoJSONNetworkFailureErrorHidesQueryString(t *testing.T) {
	rt := &failingRoundTripper{err: &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connect: connection refused")}}
	tc := transport.New(transport.Config{HTTPClient: &http.Client{Transport: rt}})
	c := NewTestClient("hcm-3", "", endpoints.Set{}, tc)

	err := c.DoJSON(context.Background(), transport.Request{
		Operation: "compute.ListServers",
		Method:    http.MethodPost,
		URL:       "http://vngcloud.invalid/v1/servers?token=secret-XYZ",
		OK:        []int{200},
		SkipAuth:  true,
	}, nil)

	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("DoJSON() error = %v, want *APIError", err)
	}
	msg := apiErr.Error()
	if strings.Contains(msg, "secret-XYZ") {
		t.Fatalf("Error() leaked the query string: %q", msg)
	}
	if strings.Contains(msg, "vngcloud.invalid") {
		t.Fatalf("Error() leaked the host: %q", msg)
	}
	if !strings.Contains(msg, "connection refused") {
		t.Fatalf("Error() = %q, want it to name the cause", msg)
	}
}

func TestNewClientDoesNotMutateAuthConfig(t *testing.T) {
	auth := &IAMUserAuth{RootEmail: "root@example.test", Username: "user", Password: "pass"}
	if _, err := newClient(WithRegion("hcm-3"), WithIAMUser(auth)); err != nil {
		t.Fatalf("newClient() error = %v", err)
	}
	if auth.SigninBaseURL != "" || auth.TokenURL != "" || auth.DashboardURI != "" {
		t.Fatalf("NewClient mutated the caller's IAMUserAuth: %+v", auth)
	}
}

func TestBuildHTTPClientRejectsCrossHostRedirects(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusMovedPermanently)
	}))
	defer source.Close()

	client := buildHTTPClient(clientConfig{timeout: 5 * time.Second})
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, source.URL, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	resp, err := client.Do(req) //nolint:bodyclose // err path has no body
	if err == nil {
		_ = resp.Body.Close()
		t.Fatalf("expected cross-host redirect error, got nil")
	}
	if !strings.Contains(err.Error(), "endpoints have moved") {
		t.Fatalf("expected cross-host redirect error, got %v", err)
	}
}

func TestRequireProjectIDConcurrent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"projects":[{"projectId":"project-1","region":"hcm-3","userId":7}]}`))
	}))
	defer server.Close()

	c := NewTestClient("hcm-3", "", endpoints.Set{VServer: server.URL + "/"},
		transport.New(transport.Config{HTTPClient: server.Client()}))

	var wg sync.WaitGroup
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.RequireProjectID(context.Background()); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if c.ProjectID() != "project-1" {
		t.Fatalf("unexpected project: %s", c.ProjectID())
	}
}

func TestDoJSONStatusReturnsFinalStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	c := NewTestClient("hcm-3", "", endpoints.Set{VServer: server.URL + "/"},
		transport.New(transport.Config{HTTPClient: server.Client()}))

	var out struct {
		OK bool `json:"ok"`
	}
	status, err := c.DoJSONStatus(context.Background(), transport.Request{
		Operation: "Op",
		Method:    http.MethodPost,
		URL:       server.URL,
		OK:        []int{http.StatusCreated},
		SkipAuth:  true,
	}, &out)
	if err != nil {
		t.Fatalf("DoJSONStatus() error = %v", err)
	}
	if status != http.StatusCreated {
		t.Fatalf("status = %d, want %d", status, http.StatusCreated)
	}
	if !out.OK {
		t.Fatal("response was not decoded")
	}
}

func TestDoJSONStatusReturnsStatusOnError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"message":"bad"}`))
	}))
	defer server.Close()

	c := NewTestClient("hcm-3", "", endpoints.Set{VServer: server.URL + "/"},
		transport.New(transport.Config{HTTPClient: server.Client()}))

	status, err := c.DoJSONStatus(context.Background(), transport.Request{
		Operation: "Op",
		Method:    http.MethodGet,
		URL:       server.URL,
		OK:        []int{http.StatusOK},
		SkipAuth:  true,
	}, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", status, http.StatusBadRequest)
	}
}

func TestDoJSONStatusZeroConfig(t *testing.T) {
	c := ClientOf(Config{})
	status, err := c.DoJSONStatus(context.Background(), transport.Request{Operation: "x.Y", Method: "GET", URL: "http://127.0.0.1/", OK: []int{200}}, nil)
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("err = %v, want ErrInvalidConfig", err)
	}
	if status != 0 {
		t.Fatalf("status = %d, want 0", status)
	}
}

func TestClientEndpointReturnsBilling(t *testing.T) {
	c := NewTestClient("hcm-3", "", endpoints.Set{Billing: "https://billing.example/"},
		transport.New(transport.Config{}))
	if got := c.Endpoint(routes.ProductBilling); got != "https://billing.example/" {
		t.Fatalf("Endpoint(ProductBilling) = %s, want https://billing.example/", got)
	}
}

func TestClientEndpointReturnsCDNDocs(t *testing.T) {
	c := NewTestClient("hcm-3", "", endpoints.Set{CDNDocs: "https://docs.example/faq/vcdn"},
		transport.New(transport.Config{}))
	if got := c.Endpoint(routes.ProductCDNDocs); got != "https://docs.example/faq/vcdn" {
		t.Fatalf("Endpoint(ProductCDNDocs) = %s, want https://docs.example/faq/vcdn", got)
	}
}

func TestDoRawZeroConfig(t *testing.T) {
	c := ClientOf(Config{})
	status, contentType, body, err := c.DoRaw(context.Background(), transport.Request{Operation: "x.Y", Method: "GET", URL: "http://127.0.0.1/"})
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("err = %v, want ErrInvalidConfig", err)
	}
	if status != 0 || contentType != "" || body != nil {
		t.Fatalf("status = %d, contentType = %q, body = %v, want zero values", status, contentType, body)
	}
}

func TestDoRawReturnsStatusAndBodyWithoutError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("nope"))
	}))
	defer server.Close()

	c := NewTestClient("hcm-3", "", endpoints.Set{},
		transport.New(transport.Config{HTTPClient: server.Client()}))

	status, contentType, body, err := c.DoRaw(context.Background(), transport.Request{
		Operation: "cdn.ListIPRanges",
		Method:    http.MethodGet,
		URL:       server.URL,
		SkipAuth:  true,
	})
	if err != nil {
		t.Fatalf("DoRaw() error = %v", err)
	}
	if status != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", status)
	}
	if contentType != "text/html; charset=utf-8" {
		t.Fatalf("contentType = %q", contentType)
	}
	if string(body) != "nope" {
		t.Fatalf("body = %q", body)
	}
}

func TestNewClientWithStaticToken(t *testing.T) {
	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"projects":[{"projectId":"project-1","region":"hcm-3"}]}`))
	}))
	defer server.Close()

	c, err := newClient(WithRegion("hcm-3"),
		WithStaticToken("static-token"),
		WithEndpointOverrides(EndpointOverrides{VServer: server.URL}))
	if err != nil {
		t.Fatalf("newClient() error = %v", err)
	}
	if err := c.Authenticate(context.Background()); err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	if _, err := c.ListProjects(context.Background(), nil); err != nil {
		t.Fatalf("ListProjects() error = %v", err)
	}
	if gotAuth != "Bearer static-token" {
		t.Fatalf("unexpected Authorization header: %q", gotAuth)
	}
}
