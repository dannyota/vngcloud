package compute

import (
	"bytes"
	"context"
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

func observeDiscoveryContext(ctx context.Context) (context.Context, <-chan context.Context) {
	contexts := make(chan context.Context, 1)
	return discovery.WithTestContextObserver(ctx, func(ctx context.Context) { contexts <- ctx }), contexts
}

func TestListFlavorPricesReflectionEveryRoute(t *testing.T) {
	for _, route := range []string{"/v1/project-1/zones", "/v1/project-1/volume_type_zones", "/v1/project-1/volume-zone-1/volume_types", "/v1/project-1/flavor_zones/codes", "/v1/project-1/flavor_zones/families", "/v1/project-1/flavors/families/general-purpose/platforms/code-s2", "/v1/project-1/images/os", "/v1/price"} {
		for _, status := range []int{200, 401, 503} {
			for _, partial := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%d/%t", route, status, partial), func(t *testing.T) {
					t.Parallel()
					attempts := 0
					captures := 0
					var logs bytes.Buffer
					base := flavorDiscoveryHandler(t, nil, func(w http.ResponseWriter, _ *http.Request) {
						testutil.WriteFixture(t, w, "../testdata/compute/discovery_quote_server_s2.json")
					})
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						if r.URL.Path == route {
							attempts++
							w.Header().Set("Set-Cookie", "secret=canary-secret")
							w.WriteHeader(status)
							_, _ = w.Write([]byte(`{"name":"canary\u002dsecret"}`))
							return
						}
						base.ServeHTTP(w, r)
					}))
					defer server.Close()
					cfg, err := vngcloud.NewConfig(vngcloud.WithRegion("hcm-3"), vngcloud.WithProjectID("project-1"), vngcloud.WithStaticToken("canary-secret"), vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{VServer: server.URL, Portal: server.URL}), vngcloud.WithRetry(2, 0), vngcloud.WithResponseCapture(func(vngcloud.ResponseCapture) { captures++ }), vngcloud.WithLogger(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))))
					if err != nil {
						t.Fatal(err)
					}
					out, err := New(cfg).ListFlavorPrices(flavorDiscoveryContext(), &ListFlavorPricesInput{ZoneID: "HCM03-1C", Family: "general-purpose", Platform: "code-s2", Name: "s2-general-1x2", AllowPartial: partial})
					var api *vngcloud.APIError
					if !errors.As(err, &api) || api.Code != "InvalidResponse" || api.StatusCode != status || attempts != 1 || captures != 0 {
						t.Fatalf("error %v attempts %d captures %d", err, attempts, captures)
					}
					printed := fmt.Sprint(out, err, logs.String())
					if strings.Contains(printed, "canary-secret") {
						t.Fatal("reflected credential escaped")
					}
				})
			}
		}
	}
}

func TestListFlavorPricesCancellationBeforeWork(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("unexpected request") }))
	out, err := c.ListFlavorPrices(ctx, nil)
	if out != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("output %v error %v", out, err)
	}
}

func TestListFlavorPricesRedirectRefusal(t *testing.T) {
	for _, location := range []string{"/v2/project-1/volumes", "https://foreign.example.test/create"} {
		t.Run(location, func(t *testing.T) {
			calls := 0
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				http.Redirect(w, r, location, http.StatusTemporaryRedirect)
			}))
			out, err := c.ListFlavorPrices(flavorDiscoveryContext(), nil)
			if out != nil || err == nil || calls != 1 {
				t.Fatalf("output %v error %v calls %d", out, err, calls)
			}
		})
	}
}

func TestListFlavorPricesBodyAndRowLimits(t *testing.T) {
	for _, rows := range []bool{false, true} {
		t.Run(strconv.FormatBool(rows), func(t *testing.T) {
			body := strings.Repeat(" ", (8<<20)+1)
			if rows {
				var b strings.Builder
				b.WriteString(`{"data":[`)
				for i := 0; i < 10001; i++ {
					if i > 0 {
						b.WriteByte(',')
					}
					fmt.Fprintf(&b, `{"uuid":"zone-%d","isEnabled":true}`, i)
				}
				b.WriteString(`]}`)
				body = b.String()
			}
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) }))
			out, err := c.ListFlavorPrices(flavorDiscoveryContext(), &ListFlavorPricesInput{AllowPartial: true})
			if out != nil || err == nil {
				t.Fatalf("output %v error %v", out, err)
			}
		})
	}
}

type flavorDiscoveryWaitProvider struct {
	entered       chan struct{}
	release       chan struct{}
	hold          int64
	calls         atomic.Int64
	invalidations atomic.Int64
}

func (p *flavorDiscoveryWaitProvider) Token(context.Context) (vngcloud.Token, error) {
	n := p.calls.Add(1)
	if n == p.hold {
		close(p.entered)
		<-p.release
	}
	return vngcloud.Token{AccessToken: fmt.Sprintf("token-%d", n), ExpiresAt: time.Now().Add(time.Hour)}, nil
}
func (p *flavorDiscoveryWaitProvider) Invalidate(string) { p.invalidations.Add(1) }

func TestListFlavorPricesRefreshWaiterDeadline(t *testing.T) {
	for _, refresh := range []bool{false, true} {
		t.Run(strconv.FormatBool(refresh), func(t *testing.T) {
			provider := &flavorDiscoveryWaitProvider{entered: make(chan struct{}), release: make(chan struct{}), hold: 1}
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
			cfg, err := vngcloud.NewConfig(vngcloud.WithRegion("hcm-3"), vngcloud.WithProjectID("project-1"), vngcloud.WithCredentialsProvider(provider), vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{VServer: server.URL}), vngcloud.WithRetry(0, 0))
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
			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			defer cancel()
			ctx, sessionContexts := observeDiscoveryContext(ctx)
			waiter := make(chan error, 1)
			go func() { _, err := c.ListFlavorPrices(ctx, nil); waiter <- err }()
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

func TestListFlavorPricesProjectWaiterDeadline(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var projects atomic.Int64
	base := flavorDiscoveryHandler(t, nil, func(w http.ResponseWriter, _ *http.Request) {
		testutil.WriteFixture(t, w, "../testdata/compute/discovery_quote_server_s2.json")
	})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/projects" {
			projects.Add(1)
			close(entered)
			<-release
			testutil.WriteFixture(t, w, "../testdata/project/list_projects.json")
			return
		}
		if r.URL.Path == "/v1/project-1/flavor_zones/product" {
			_, _ = w.Write([]byte(`{"flavorZones":[]}`))
			return
		}
		base.ServeHTTP(w, r)
	}))
	defer server.Close()
	cfg, err := vngcloud.NewConfig(vngcloud.WithRegion("hcm-3"), vngcloud.WithStaticToken("synthetic-token"), vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{VServer: server.URL, Portal: server.URL}), vngcloud.WithRetry(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	c := New(cfg)
	holder := make(chan error, 1)
	go func() { _, err := c.ListFlavorZones(context.Background(), nil); holder <- err }()
	<-entered
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	ctx, sessionContexts := observeDiscoveryContext(ctx)
	waiter := make(chan error, 1)
	go func() { _, err := c.ListFlavorPrices(ctx, nil); waiter <- err }()
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
	out, err := c.ListFlavorPrices(flavorDiscoveryContext(), &ListFlavorPricesInput{ZoneID: "HCM03-1C", Family: "general-purpose", Platform: "code-s2", Name: "s2-general-1x2"})
	if err != nil || out.Metadata.Requests != 8 || projects.Load() != 1 {
		t.Fatalf("output %+v error %v reads %d", out, err, projects.Load())
	}
}

func TestListFlavorPricesSharedIAMWaiterDeadline(t *testing.T) {
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
		cfg, err := vngcloud.NewConfig(vngcloud.WithRegion("hcm-3"), vngcloud.WithProjectID("project-1"), vngcloud.WithIAMUser(auth), vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{VServer: server.URL}), vngcloud.WithRetry(0, 0))
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
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	ctx, sessionContexts := observeDiscoveryContext(ctx)
	waiter := make(chan error, 1)
	go func() { _, err := waiterClient.ListFlavorPrices(ctx, nil); waiter <- err }()
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

func TestListFlavorPricesAuxiliaryAndNonPublicReflection(t *testing.T) {
	for _, route := range []string{"/v1/projects", "/v1/project-1/flavor_zones/product", "/v1/project-1/flavor_zones/customs", "/v1/project-1/flavors/customs", "/v1/project-1/fz-5/flavors"} {
		for _, partial := range []bool{false, true} {
			attempts := 0
			base := flavorDiscoveryHandler(t, map[string]string{
				"/v1/project-1/flavor_zones/product": `{"flavorZones":[{"id":"fz-5","zoneId":"HCM03-1C"}]}`,
				"/v1/project-1/flavor_zones/customs": `[]`,
				"/v1/project-1/flavors/customs":      `[]`,
				"/v1/project-1/fz-5/flavors":         `{"flavors":[]}`,
			}, func(w http.ResponseWriter, _ *http.Request) {
				testutil.WriteFixture(t, w, "../testdata/compute/discovery_quote_server_s2.json")
			})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == route {
					attempts++
					_, _ = w.Write([]byte(`{"metadata":"auxiliary\u002dsecret"}`))
					return
				}
				if r.URL.Path == "/v1/projects" {
					testutil.WriteFixture(t, w, "../testdata/project/list_projects.json")
					return
				}
				base.ServeHTTP(w, r)
			}))
			cfg, err := vngcloud.NewConfig(vngcloud.WithRegion("hcm-3"), vngcloud.WithStaticToken("auxiliary-secret"), vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{VServer: server.URL, Portal: server.URL}), vngcloud.WithRetry(0, 0))
			if err != nil {
				t.Fatal(err)
			}
			out, err := New(cfg).ListFlavorPrices(flavorDiscoveryContext(), &ListFlavorPricesInput{ZoneID: "HCM03-1C", Family: "general-purpose", Platform: "code-s2", Name: "s2-general-1x2", IncludeNonPublic: true, AllowPartial: partial})
			server.Close()
			var api *vngcloud.APIError
			if !errors.As(err, &api) || api.Code != "InvalidResponse" || api.StatusCode != 200 || attempts != 1 || strings.Contains(fmt.Sprint(out, err), "auxiliary-secret") {
				t.Fatalf("route %s output %+v error %v attempts %d", route, out, err, attempts)
			}
		}
	}
}

func TestListFlavorPricesCatalogHTTPErrorIdentity(t *testing.T) {
	for _, status := range []int{400, 401, 403, 404, 429, 500, 502, 503, 504} {
		for _, partial := range []bool{false, true} {
			calls := 0
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls++
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{"code":"Failure"}`))
			}))
			out, err := c.ListFlavorPrices(flavorDiscoveryContext(), &ListFlavorPricesInput{AllowPartial: partial})
			var api *vngcloud.APIError
			if out != nil || !errors.As(err, &api) || api.StatusCode != status || calls != 1 {
				t.Fatalf("status %d output %+v error %v calls %d", status, out, err, calls)
			}
		}
	}
}
