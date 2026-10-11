package network

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/discovery"
	"danny.vn/vngcloud/internal/testutil"
	"danny.vn/vngcloud/internal/transport"
)

func observeNetworkDiscoveryContext(ctx context.Context) (context.Context, <-chan context.Context) {
	contexts := make(chan context.Context, 1)
	return discovery.WithTestContextObserver(ctx, func(ctx context.Context) { contexts <- ctx }), contexts
}

func TestListNetworkPricesReflectionEveryRoute(t *testing.T) {
	for _, route := range []string{"projects", "public", "public-vm", "public-mkp"} {
		for _, status := range []int{200, 401, 500, 503} {
			for _, partial := range []bool{false, true} {
				for _, body := range []string{`{"optimumPrice":1,"propertiesPrice":[{"name":"canary-secret"}]}`, `{"metadata":"canary\u002dsecret"}`, `{"code":"canary-secret"`, `canary-secret`} {
					t.Run(fmt.Sprintf("%s/%d/%t/%s", route, status, partial, body), func(t *testing.T) {
						attempts, captures := 0, 0
						var logs bytes.Buffer
						server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
							kind := "projects"
							if r.URL.Path == "/v1/price" {
								var req struct {
									Info struct {
										Type string `json:"type"`
									} `json:"resourceInfo"`
								}
								if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
									t.Error(err)
								}
								kind = req.Info.Type
							} else if r.URL.Path != "/v1/projects" {
								t.Errorf("route %s", r.URL.Path)
							}
							if kind == route {
								attempts++
								w.Header().Set("Set-Cookie", "secret=canary-secret")
								w.WriteHeader(status)
								_, _ = w.Write([]byte(body))
								return
							}
							if kind == "projects" {
								testutil.WriteFixture(t, w, "../testdata/project/list_projects.json")
							} else {
								testutil.WriteFixture(t, w, "../testdata/network/discovery_quote_public.json")
							}
						}))
						defer server.Close()
						cfg, err := vngcloud.NewConfig(vngcloud.WithRegion("hcm-3"), vngcloud.WithStaticToken("canary-secret"), vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{VServer: server.URL, Portal: server.URL}), vngcloud.WithRetry(2, 0), vngcloud.WithResponseCapture(func(vngcloud.ResponseCapture) { captures++ }), vngcloud.WithLogger(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))))
						if err != nil {
							t.Fatal(err)
						}
						out, err := New(cfg).ListNetworkPrices(networkDiscoveryContext(), &ListNetworkPricesInput{AllowPartial: partial})
						var api *vngcloud.APIError
						if !errors.As(err, &api) || api.Code != "InvalidResponse" || api.StatusCode != status || attempts != 1 || captures != 0 {
							t.Fatalf("error %v attempts %d captures %d", err, attempts, captures)
						}
						printed, _ := json.Marshal(out)
						if strings.Contains(string(printed)+fmt.Sprint(err)+logs.String(), "canary-secret") {
							t.Fatal("credential escaped")
						}
					})
				}
			}
		}
	}
}

func TestListNetworkPricesCancellationBeforeWork(t *testing.T) {
	ctx, cancel := context.WithCancel(networkDiscoveryContext())
	cancel()
	c := New(testutil.NewConfig(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("unexpected request") })))
	out, err := c.ListNetworkPrices(ctx, nil)
	if out != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("output %v error %v", out, err)
	}
}

func TestListNetworkPricesRedirectRefusal(t *testing.T) {
	for _, location := range []string{"/v1/create", "https://foreign.example.test/create"} {
		calls := 0
		c := New(testutil.NewConfig(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			http.Redirect(w, r, location, http.StatusTemporaryRedirect)
		})))
		out, err := c.ListNetworkPrices(networkDiscoveryContext(), nil)
		if out != nil || err == nil || calls != 1 {
			t.Fatalf("output %v error %v calls %d", out, err, calls)
		}
	}
}

func TestListNetworkPricesBodyBoundary(t *testing.T) {
	for _, extra := range []int{0, 1} {
		body := `{"optimumPrice":1}`
		body += strings.Repeat(" ", (8<<20)-len(body)+extra)
		calls := 0
		c := New(testutil.NewConfig(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls++; _, _ = w.Write([]byte(body)) })))
		out, err := c.ListNetworkPrices(networkDiscoveryContext(), nil)
		if extra == 0 {
			if err != nil || out == nil || calls != 3 {
				t.Fatalf("output %v error %v calls %d", out, err, calls)
			}
		} else if out != nil || err == nil || calls != 1 {
			t.Fatalf("output %v error %v calls %d", out, err, calls)
		}
	}
}

type networkDiscoveryWaitProvider struct {
	entered       chan struct{}
	release       chan struct{}
	hold          int64
	calls         atomic.Int64
	invalidations atomic.Int64
}

func (p *networkDiscoveryWaitProvider) Token(context.Context) (vngcloud.Token, error) {
	n := p.calls.Add(1)
	if n == p.hold {
		close(p.entered)
		<-p.release
	}
	return vngcloud.Token{AccessToken: fmt.Sprintf("token-%d", n), ExpiresAt: time.Now().Add(time.Hour)}, nil
}
func (p *networkDiscoveryWaitProvider) Invalidate(string) { p.invalidations.Add(1) }

func TestListNetworkPricesRefreshWaiterDeadline(t *testing.T) {
	for _, refresh := range []bool{false, true} {
		t.Run(strconv.FormatBool(refresh), func(t *testing.T) {
			provider := &networkDiscoveryWaitProvider{entered: make(chan struct{}), release: make(chan struct{}), hold: 1}
			if refresh {
				provider.hold = 2
			}
			var requests atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if refresh && r.URL.Path != "/warm" && r.Header.Get("Authorization") == "Bearer token-1" {
					w.WriteHeader(401)
					return
				}
				_, _ = w.Write([]byte(`{}`))
			}))
			defer server.Close()
			cfg, err := vngcloud.NewConfig(vngcloud.WithRegion("hcm-3"), vngcloud.WithProjectID("project-1"), vngcloud.WithCredentialsProvider(provider), vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{VServer: server.URL, Portal: server.URL}), vngcloud.WithRetry(0, 0))
			if err != nil {
				t.Fatal(err)
			}
			c := New(cfg)
			req := transport.Request{Operation: "test.Read", Method: http.MethodGet, URL: server.URL + "/holder", OK: []int{200}}
			if refresh {
				warm := req
				warm.URL = server.URL + "/warm"
				if err := c.c.DoJSON(context.Background(), warm, nil); err != nil {
					t.Fatal(err)
				}
			}
			holder := make(chan error, 1)
			go func() { holder <- c.c.DoJSON(context.Background(), req, nil) }()
			<-provider.entered
			ctx, cancel := context.WithTimeout(networkDiscoveryContext(), 50*time.Millisecond)
			defer cancel()
			ctx, sessionContexts := observeNetworkDiscoveryContext(ctx)
			waiter := make(chan error, 1)
			go func() { _, err := c.ListNetworkPrices(ctx, nil); waiter <- err }()
			sessionCtx := <-sessionContexts
			<-sessionCtx.Done()
			close(provider.release)
			if err := <-holder; err != nil {
				t.Fatal(err)
			}
			if err := <-waiter; !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("waiter error %v", err)
			}
			wantCalls, wantRequests, wantInvalidations := int64(1), int64(1), int64(0)
			if refresh {
				wantCalls, wantRequests, wantInvalidations = 2, 4, 1
			}
			if provider.calls.Load() != wantCalls || requests.Load() != wantRequests || provider.invalidations.Load() != wantInvalidations {
				t.Fatalf("calls %d requests %d invalidations %d", provider.calls.Load(), requests.Load(), provider.invalidations.Load())
			}
		})
	}
}

func TestListNetworkPricesProjectWaiterDeadline(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var projects atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/projects" {
			projects.Add(1)
			close(entered)
			<-release
			testutil.WriteFixture(t, w, "../testdata/project/list_projects.json")
			return
		}
		testutil.WriteFixture(t, w, "../testdata/network/discovery_quote_public.json")
	}))
	defer server.Close()
	cfg, err := vngcloud.NewConfig(vngcloud.WithRegion("hcm-3"), vngcloud.WithStaticToken("synthetic-token"), vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{VServer: server.URL, Portal: server.URL}), vngcloud.WithRetry(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	c := New(cfg)
	holder := make(chan error, 1)
	go func() { _, err := c.c.RequireProjectID(context.Background()); holder <- err }()
	<-entered
	ctx, cancel := context.WithTimeout(networkDiscoveryContext(), 50*time.Millisecond)
	defer cancel()
	ctx, sessionContexts := observeNetworkDiscoveryContext(ctx)
	waiter := make(chan error, 1)
	go func() { _, err := c.ListNetworkPrices(ctx, nil); waiter <- err }()
	sessionCtx := <-sessionContexts
	<-sessionCtx.Done()
	close(release)
	if err := <-holder; err != nil {
		t.Fatal(err)
	}
	if err := <-waiter; !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("waiter error %v", err)
	}
	if projects.Load() != 1 {
		t.Fatalf("project reads %d", projects.Load())
	}
	out, err := c.ListNetworkPrices(networkDiscoveryContext(), &ListNetworkPricesInput{})
	if err != nil || out.Metadata.Requests != 3 || projects.Load() != 1 {
		t.Fatalf("output %+v error %v reads %d", out, err, projects.Load())
	}
}

func TestListNetworkPricesSharedIAMWaiterDeadline(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var logins, requests atomic.Int64
	token := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		logins.Add(1)
		close(entered)
		<-release
		_, _ = w.Write([]byte(`{"accessToken":"holder-token","expiresIn":3600}`))
	}))
	defer token.Close()
	signin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`<input name="_csrf" value="csrf">`))
			return
		}
		http.Redirect(w, r, token.URL+"/callback?code=code", http.StatusFound)
	}))
	defer signin.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Header.Get("Authorization") != "Bearer holder-token" {
			t.Error("token identity changed")
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()
	auth := &vngcloud.IAMUserAuth{RootEmail: "root@example.test", Username: "user", Password: "synthetic", SigninBaseURL: signin.URL, TokenURL: token.URL, DashboardURI: token.URL + "/"}
	config := func() vngcloud.Config {
		cfg, err := vngcloud.NewConfig(vngcloud.WithRegion("hcm-3"), vngcloud.WithProjectID("project-1"), vngcloud.WithIAMUser(auth), vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{VServer: server.URL, Portal: server.URL}), vngcloud.WithRetry(0, 0))
		if err != nil {
			t.Fatal(err)
		}
		return cfg
	}
	holderClient, waiterClient := New(config()), New(config())
	req := transport.Request{Operation: "test.Read", Method: http.MethodGet, URL: server.URL + "/holder", OK: []int{200}}
	holder := make(chan error, 1)
	go func() { holder <- holderClient.c.DoJSON(context.Background(), req, nil) }()
	<-entered
	ctx, cancel := context.WithTimeout(networkDiscoveryContext(), 50*time.Millisecond)
	defer cancel()
	ctx, sessionContexts := observeNetworkDiscoveryContext(ctx)
	waiter := make(chan error, 1)
	go func() { _, err := waiterClient.ListNetworkPrices(ctx, nil); waiter <- err }()
	sessionCtx := <-sessionContexts
	<-sessionCtx.Done()
	close(release)
	if err := <-holder; err != nil {
		t.Fatal(err)
	}
	if err := <-waiter; !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("waiter error %v", err)
	}
	if requests.Load() != 1 || logins.Load() != 1 {
		t.Fatalf("requests %d logins %d", requests.Load(), logins.Load())
	}
	if err := waiterClient.c.DoJSON(context.Background(), req, nil); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 2 || logins.Load() != 1 {
		t.Fatal("waiter changed shared IAM token")
	}
}

func TestListNetworkPricesRotatedTokenReflection(t *testing.T) {
	provider := &networkDiscoveryWaitProvider{hold: 0}
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") == "Bearer token-1" {
			w.WriteHeader(401)
			return
		}
		if r.Header.Get("Authorization") != "Bearer token-2" {
			t.Errorf("token identity %s", r.Header.Get("Authorization"))
		}
		_, _ = w.Write([]byte(`{"optimumPrice":1,"propertiesPrice":[{"description":"token\u002d2"}]}`))
	}))
	defer server.Close()
	cfg, err := vngcloud.NewConfig(vngcloud.WithRegion("hcm-3"), vngcloud.WithProjectID("project-1"), vngcloud.WithCredentialsProvider(provider), vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{Portal: server.URL}), vngcloud.WithRetry(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	out, err := New(cfg).ListNetworkPrices(networkDiscoveryContext(), nil)
	var api *vngcloud.APIError
	if out != nil || !errors.As(err, &api) || api.Code != "InvalidResponse" || api.StatusCode != 200 || calls != 2 || provider.calls.Load() != 2 || provider.invalidations.Load() != 1 {
		t.Fatalf("output %v error %v calls %d", out, err, calls)
	}
	if strings.Contains(fmt.Sprint(err), "token-2") {
		t.Fatal("rotated token escaped")
	}
}
