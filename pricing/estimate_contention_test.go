package pricing

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/testutil"
)

type estimateHeldProvider struct {
	entered, release     chan struct{}
	calls, invalidations atomic.Int32
}

func (p *estimateHeldProvider) Token(ctx context.Context) (vngcloud.Token, error) {
	if p.calls.Add(1) == 1 {
		close(p.entered)
		<-p.release
	}
	return vngcloud.Token{AccessToken: "replacement", ExpiresAt: time.Now().Add(time.Hour)}, ctx.Err()
}
func (p *estimateHeldProvider) Invalidate(string) { p.invalidations.Add(1) }

func TestEstimateInitialRefreshWaiterDeadline(t *testing.T) {
	p := &estimateHeldProvider{entered: make(chan struct{}), release: make(chan struct{})}
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = w.Write([]byte(`{"optimumPrice":10}`))
	}))
	defer srv.Close()
	client := New(estimateHTTPConfig(t, srv.URL, vngcloud.WithCredentialsProvider(p)))
	in := &EstimateInput{PublicIPs: []EstimatePublicIP{{Key: "ip", Kind: "public-vip", Type: "public", Quantity: 1}}}
	holder := make(chan error, 1)
	go func() { _, err := client.Estimate(estimateContext(), in); holder <- err }()
	select {
	case <-p.entered:
	case <-time.After(time.Second):
		t.Fatal("holder did not enter auth")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	waiter := make(chan error, 1)
	go func() { _, err := client.Estimate(ctx, in); waiter <- err }()
	<-ctx.Done()
	close(p.release)
	if err := <-holder; err != nil {
		t.Fatal(err)
	}
	if err := <-waiter; !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("waiter %v", err)
	}
	if requests.Load() != 1 || p.calls.Load() != 1 || p.invalidations.Load() != 0 {
		t.Fatal("expired waiter changed auth or requested")
	}
}

func TestEstimateProjectWaiterDeadline(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var projects, quotes atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/projects" {
			projects.Add(1)
			close(entered)
			<-release
			_, _ = w.Write([]byte(`{"projects":[{"projectId":"project-1","region":"hcm-3"}]}`))
			return
		}
		if r.URL.Path == "/v1/price" {
			quotes.Add(1)
			_, _ = w.Write([]byte(`{"optimumPrice":10}`))
			return
		}
		t.Errorf("unexpected route %s", r.URL)
	}))
	defer srv.Close()
	client := New(estimateHTTPConfig(t, srv.URL, vngcloud.WithProjectID("")))
	in := &EstimateInput{PublicIPs: []EstimatePublicIP{{Key: "ip", Kind: "public-vip", Type: "public", Quantity: 1}}}
	holder := make(chan error, 1)
	go func() { _, err := client.Estimate(estimateContext(), in); holder <- err }()
	<-entered
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	waiter := make(chan error, 1)
	go func() { _, err := client.Estimate(ctx, in); waiter <- err }()
	<-ctx.Done()
	close(release)
	if err := <-holder; err != nil {
		t.Fatal(err)
	}
	if err := <-waiter; !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("waiter %v", err)
	}
	if projects.Load() != 1 || quotes.Load() != 1 {
		t.Fatal("expired waiter used project cache")
	}
}

func TestEstimateConcurrentInvocationsHaveFreshQuotes(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte(`{"optimumPrice":10}`))
	}))
	defer srv.Close()
	client := New(estimateHTTPConfig(t, srv.URL))
	in := &EstimateInput{PublicIPs: []EstimatePublicIP{{Key: "ip", Kind: "public-vip", Type: "public", Quantity: 1}, {Key: "copy", Kind: "public-vip", Type: "public", Quantity: 2}}}
	done := make(chan error, 2)
	for range 2 {
		go func() {
			out, err := client.Estimate(estimateContext(), in)
			if err == nil && (out == nil || out.Metadata.Requests != 1) {
				err = errors.New("shared estimate session")
			}
			done <- err
		}()
	}
	for range 2 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	if _, err := client.Estimate(estimateContext(), in); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 3 {
		t.Fatal("persistent price cache")
	}
}

type estimateRotatingProvider struct {
	entered, release     chan struct{}
	calls, invalidations atomic.Int32
}

func (p *estimateRotatingProvider) Token(ctx context.Context) (vngcloud.Token, error) {
	n := p.calls.Add(1)
	value := "original-token"
	if n > 1 {
		close(p.entered)
		<-p.release
		value = "rotated-token"
	}
	return vngcloud.Token{AccessToken: value, ExpiresAt: time.Now().Add(time.Hour)}, ctx.Err()
}
func (p *estimateRotatingProvider) Invalidate(string) { p.invalidations.Add(1) }

func TestEstimate401RefreshWaiterDeadline(t *testing.T) {
	p := &estimateRotatingProvider{entered: make(chan struct{}), release: make(chan struct{})}
	var old, newRequests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer original-token" {
			old.Add(1)
			w.WriteHeader(401)
			_, _ = w.Write([]byte(`{}`))
			return
		}
		newRequests.Add(1)
		_, _ = w.Write([]byte(`{"optimumPrice":10}`))
	}))
	defer srv.Close()
	client := New(estimateHTTPConfig(t, srv.URL, vngcloud.WithCredentialsProvider(p)))
	in := &EstimateInput{PublicIPs: []EstimatePublicIP{{Key: "ip", Kind: "public-vip", Type: "public", Quantity: 1}}}
	holder := make(chan error, 1)
	go func() { _, err := client.Estimate(estimateContext(), in); holder <- err }()
	<-p.entered
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	waiter := make(chan error, 1)
	go func() { _, err := client.Estimate(ctx, in); waiter <- err }()
	<-ctx.Done()
	close(p.release)
	if err := <-holder; err != nil {
		t.Fatal(err)
	}
	if err := <-waiter; !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("waiter %v", err)
	}
	if old.Load() > 2 || newRequests.Load() != 1 || p.calls.Load() != 2 || p.invalidations.Load() != 1 {
		t.Fatal("expired waiter changed 401 recovery")
	}
}

func estimateIAMServer(t *testing.T, tokenHandler http.HandlerFunc) (*httptest.Server, *httptest.Server) {
	t.Helper()
	token := httptest.NewServer(tokenHandler)
	signin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`<html><input name="_csrf" value="csrf"></html>`))
			return
		}
		http.Redirect(w, r, token.URL+"/callback?code=synthetic-code", http.StatusFound)
	}))
	t.Cleanup(signin.Close)
	t.Cleanup(token.Close)
	return signin, token
}

func TestEstimateSharedIAMWaiterDeadline(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var logins, requests atomic.Int32
	signin, token := estimateIAMServer(t, func(w http.ResponseWriter, _ *http.Request) {
		logins.Add(1)
		close(entered)
		<-release
		_, _ = w.Write([]byte(`{"accessToken":"iam-token","expiresIn":3600}`))
	})
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = w.Write([]byte(`{"optimumPrice":10}`))
	}))
	defer api.Close()
	auth := &vngcloud.IAMUserAuth{RootEmail: "root@example.test", Username: "synthetic-user", Password: "synthetic-password", SigninBaseURL: signin.URL, TokenURL: token.URL, DashboardURI: token.URL + "/"}
	build := func() *Client {
		return New(estimateHTTPConfig(t, api.URL, vngcloud.WithStaticToken(""), vngcloud.WithIAMUser(auth)))
	}
	holderClient, waiterClient := build(), build()
	in := &EstimateInput{PublicIPs: []EstimatePublicIP{{Key: "ip", Kind: "public-vip", Type: "public", Quantity: 1}}}
	holder := make(chan error, 1)
	go func() { _, err := holderClient.Estimate(estimateContext(), in); holder <- err }()
	<-entered
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	waiter := make(chan error, 1)
	go func() { _, err := waiterClient.Estimate(ctx, in); waiter <- err }()
	<-ctx.Done()
	close(release)
	if err := <-holder; err != nil {
		t.Fatal(err)
	}
	if err := <-waiter; !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("waiter %v", err)
	}
	if _, err := waiterClient.Estimate(estimateContext(), in); err != nil {
		t.Fatal(err)
	}
	if logins.Load() != 1 || requests.Load() != 2 {
		t.Fatal("shared IAM waiter changed cached token")
	}
}

func TestEstimateStorageRegionsRemainInvocationScoped(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var reads atomic.Int32
	storageHandler := estimateStorageHandler(t, nil)
	cfg := testutil.NewConfig(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/internal/v1/regions" && reads.Add(1) == 1 {
			close(entered)
			<-release
		}
		storageHandler(w, r)
	}))
	client := New(cfg)
	in := &EstimateInput{Storage: []EstimateStorage{{Key: "s", Quantity: 1, Type: "Gold", QuotaGB: 30}}}
	holder := make(chan error, 1)
	go func() { _, err := client.Estimate(estimateContext(), in); holder <- err }()
	<-entered
	out, err := client.Estimate(estimateContext(), in)
	if err != nil || out == nil || out.Metadata.Requests != 8 {
		t.Fatalf("independent region session %v", err)
	}
	close(release)
	if err := <-holder; err != nil {
		t.Fatal(err)
	}
	if reads.Load() != 2 {
		t.Fatal("shared storage region cache")
	}
}
