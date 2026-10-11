package storage

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
	"danny.vn/vngcloud/internal/testutil"
	"danny.vn/vngcloud/internal/transport"
)

func TestListStoragePricesReflectionEveryRoute(t *testing.T) {
	for _, route := range []string{"/internal/v1/regions", "/internal/v1/billing/project_types", "/internal/v1/billing/purchase_types", "vos_billing_normal_min_quota", "vos_billing_normal_max_quota", "max_project_per_user_per_region", "enable_iam_checkout", "/billing-api/v2/price"} {
		for _, status := range []int{200, 401, 503} {
			for _, partial := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%d/%t", route, status, partial), func(t *testing.T) {
					t.Parallel()
					attempts := 0
					captures := 0
					var logs bytes.Buffer
					base := storageDiscoveryHandler(t, func(w http.ResponseWriter, _ *http.Request) {
						testutil.WriteFixture(t, w, fixtures+"discovery_quote_gold.json")
					})
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						if r.URL.Path == route || r.URL.Query().Get("keys") == route {
							attempts++
							w.Header().Set("Set-Cookie", "secret=canary-secret")
							w.WriteHeader(status)
							_, _ = w.Write([]byte(`{"success":true,"data":{"name":"canary\u002dsecret"}}`))
							return
						}
						base.ServeHTTP(w, r)
					}))
					defer server.Close()
					cfg, err := vngcloud.NewConfig(vngcloud.WithRegion("hcm-3"), vngcloud.WithStaticToken("canary-secret"), vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{Storage: server.URL}), vngcloud.WithRetry(2, 0), vngcloud.WithResponseCapture(func(vngcloud.ResponseCapture) { captures++ }), vngcloud.WithLogger(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))))
					if err != nil {
						t.Fatal(err)
					}
					out, err := New(cfg).ListStoragePrices(discoveryTestContext(), &ListStoragePricesInput{Type: "Gold", AllowPartial: partial})
					var api *vngcloud.APIError
					if !errors.As(err, &api) || api.Code != "InvalidResponse" || api.StatusCode != status || attempts != 1 || captures != 0 {
						t.Fatalf("error %v attempts %d captures %d", err, attempts, captures)
					}
					if strings.Contains(fmt.Sprint(out, err, logs.String()), "canary-secret") {
						t.Fatal("reflected credential escaped")
					}
				})
			}
		}
	}
}

func TestListStoragePricesCancellationBeforeWork(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("unexpected request") }))
	out, err := c.ListStoragePrices(ctx, nil)
	if out != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("output %v error %v", out, err)
	}
}

func TestListStoragePricesRegionCacheGuard(t *testing.T) {
	var reflected = true
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if reflected {
			_, _ = w.Write([]byte(`{"success":true,"datas":[{"regionId":"canary-secret","regionName":"HCM04"}]}`))
		} else {
			testutil.WriteFixture(t, w, fixtures+"discovery_regions.json")
		}
	}))
	defer server.Close()
	cfg, err := vngcloud.NewConfig(vngcloud.WithRegion("hcm-3"), vngcloud.WithStaticToken("canary-secret"), vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{Storage: server.URL}), vngcloud.WithRetry(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	c := New(cfg)
	_, err = c.ListStoragePrices(discoveryTestContext(), nil)
	if err == nil || len(c.regionIDs) != 0 {
		t.Fatal("reflection changed region cache")
	}
	reflected = false
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = c.ListStoragePrices(ctx, nil)
	if !errors.Is(err, context.Canceled) || len(c.regionIDs) != 0 {
		t.Fatal("cancellation changed cache")
	}
}

func TestListStoragePricesBodyAndRowLimits(t *testing.T) {
	for _, rows := range []bool{false, true} {
		t.Run(strconv.FormatBool(rows), func(t *testing.T) {
			body := strings.Repeat(" ", (8<<20)+1)
			if rows {
				var b strings.Builder
				b.WriteString(`{"success":true,"datas":[`)
				for i := 0; i < 10001; i++ {
					if i > 0 {
						b.WriteByte(',')
					}
					fmt.Fprintf(&b, `{"regionId":"region-%d","regionName":"R%d"}`, i, i)
				}
				b.WriteString(`]}`)
				body = b.String()
			}
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) }))
			out, err := c.ListStoragePrices(discoveryTestContext(), &ListStoragePricesInput{AllowPartial: true})
			if out != nil || err == nil || len(c.regionIDs) != 0 {
				t.Fatalf("output %v error %v", out, err)
			}
		})
	}
}

type storageDiscoveryWaitProvider struct {
	entered       chan struct{}
	release       chan struct{}
	hold          int64
	calls         atomic.Int64
	invalidations atomic.Int64
}

func (p *storageDiscoveryWaitProvider) Token(context.Context) (vngcloud.Token, error) {
	n := p.calls.Add(1)
	if n == p.hold {
		close(p.entered)
		<-p.release
	}
	return vngcloud.Token{AccessToken: fmt.Sprintf("token-%d", n), ExpiresAt: time.Now().Add(time.Hour)}, nil
}
func (p *storageDiscoveryWaitProvider) Invalidate(string) { p.invalidations.Add(1) }

func TestListStoragePricesRefreshWaiterDeadline(t *testing.T) {
	for _, refresh := range []bool{false, true} {
		t.Run(strconv.FormatBool(refresh), func(t *testing.T) {
			provider := &storageDiscoveryWaitProvider{entered: make(chan struct{}), release: make(chan struct{}), hold: 1}
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
			cfg, err := vngcloud.NewConfig(vngcloud.WithRegion("hcm-3"), vngcloud.WithProjectID("project-1"), vngcloud.WithCredentialsProvider(provider), vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{Storage: server.URL}), vngcloud.WithRetry(0, 0))
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
			waiter := make(chan error, 1)
			go func() { _, err := c.ListStoragePrices(ctx, nil); waiter <- err }()
			<-ctx.Done()
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

func TestListStoragePricesSharedIAMWaiterDeadline(t *testing.T) {
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
		cfg, err := vngcloud.NewConfig(vngcloud.WithRegion("hcm-3"), vngcloud.WithProjectID("project-1"), vngcloud.WithIAMUser(auth), vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{Storage: server.URL}), vngcloud.WithRetry(0, 0))
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
	waiter := make(chan error, 1)
	go func() { _, err := waiterClient.ListStoragePrices(ctx, nil); waiter <- err }()
	<-ctx.Done()
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

func TestListStoragePricesRegionWaiterDeadline(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("unexpected request") }))
	c.regionIDs = map[string]string{"HCM04": "holder-region"}
	c.regionMu.Lock()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := c.ListStoragePrices(ctx, nil); done <- err }()
	<-ctx.Done()
	c.regionMu.Unlock()
	if err := <-done; !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error %v", err)
	}
	if c.regionIDs["HCM04"] != "holder-region" {
		t.Fatal("waiter changed region cache")
	}
}
