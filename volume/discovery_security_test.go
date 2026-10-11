package volume

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

func TestListVolumePricesReflectionEveryRoute(t *testing.T) {
	for _, route := range []string{"/v1/project-1/zones", "/v1/project-1/volume_type_zones", "/v1/project-1/fz-82/volume_types", "/v1/project-1/volumes/encryption_types", "/v1/price"} {
		for _, status := range []int{200, 401, 503} {
			for _, partial := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%d/%t", route, status, partial), func(t *testing.T) {
					t.Parallel()
					attempts := 0
					captures := 0
					var logs bytes.Buffer
					base := volumeDiscoveryHandler(t, nil, func(w http.ResponseWriter, _ *http.Request) {
						testutil.WriteFixture(t, w, "../testdata/volume/discovery_quote_plain.json")
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
					out, err := New(cfg).ListVolumePrices(discoveryTestContext(), &ListVolumePricesInput{IOPS: 3000, Encryption: "none", AllowPartial: partial})
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

func TestListVolumePricesCancellationBeforeWork(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("unexpected request") }))
	out, err := c.ListVolumePrices(ctx, nil)
	if out != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("output %v error %v", out, err)
	}
}

func TestListVolumePricesRedirectRefusal(t *testing.T) {
	for _, location := range []string{"/v2/project-1/volumes", "https://foreign.example.test/create"} {
		t.Run(location, func(t *testing.T) {
			calls := 0
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				http.Redirect(w, r, location, http.StatusTemporaryRedirect)
			}))
			out, err := c.ListVolumePrices(discoveryTestContext(), nil)
			if out != nil || err == nil || calls != 1 {
				t.Fatalf("output %v error %v calls %d", out, err, calls)
			}
		})
	}
}

func TestListVolumePricesBodyAndRowLimits(t *testing.T) {
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
			out, err := c.ListVolumePrices(discoveryTestContext(), &ListVolumePricesInput{AllowPartial: true})
			if out != nil || err == nil {
				t.Fatalf("output %v error %v", out, err)
			}
		})
	}
}

type volumeDiscoveryWaitProvider struct {
	entered       chan struct{}
	release       chan struct{}
	hold          int64
	calls         atomic.Int64
	invalidations atomic.Int64
}

func (p *volumeDiscoveryWaitProvider) Token(context.Context) (vngcloud.Token, error) {
	n := p.calls.Add(1)
	if n == p.hold {
		close(p.entered)
		<-p.release
	}
	return vngcloud.Token{AccessToken: fmt.Sprintf("token-%d", n), ExpiresAt: time.Now().Add(time.Hour)}, nil
}
func (p *volumeDiscoveryWaitProvider) Invalidate(string) { p.invalidations.Add(1) }

func TestListVolumePricesRefreshWaiterDeadline(t *testing.T) {
	for _, refresh := range []bool{false, true} {
		t.Run(strconv.FormatBool(refresh), func(t *testing.T) {
			provider := &volumeDiscoveryWaitProvider{entered: make(chan struct{}), release: make(chan struct{}), hold: 1}
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
			go func() { _, err := c.ListVolumePrices(ctx, nil); waiter <- err }()
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

func TestListVolumePricesProjectWaiterDeadline(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var projects atomic.Int64
	base := volumeDiscoveryHandler(t, nil, func(w http.ResponseWriter, _ *http.Request) {
		testutil.WriteFixture(t, w, "../testdata/volume/discovery_quote_plain.json")
	})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/projects" {
			projects.Add(1)
			close(entered)
			<-release
			testutil.WriteFixture(t, w, "../testdata/project/list_projects.json")
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
	go func() { _, err := c.ListVolumeTypeZones(context.Background(), nil); holder <- err }()
	<-entered
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	ctx, sessionContexts := observeDiscoveryContext(ctx)
	waiter := make(chan error, 1)
	go func() { _, err := c.ListVolumePrices(ctx, nil); waiter <- err }()
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
	out, err := c.ListVolumePrices(discoveryTestContext(), &ListVolumePricesInput{IOPS: 3000, Encryption: "none"})
	if err != nil || out.Metadata.Requests != 5 || projects.Load() != 1 {
		t.Fatalf("output %+v error %v reads %d", out, err, projects.Load())
	}
}

func TestListVolumePricesSharedIAMWaiterDeadline(t *testing.T) {
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
	go func() { _, err := waiterClient.ListVolumePrices(ctx, nil); waiter <- err }()
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
