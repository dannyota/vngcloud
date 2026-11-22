package globalloadbalancer

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"danny.vn/vngcloud"
)

type glbRoundTrip func(*http.Request) (*http.Response, error)

func (f glbRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func newWriteTestClient(t *testing.T, handler http.Handler) *Client {
	t.Helper()
	return newWriteTestClientOpts(t, handler)
}

func newWriteTestClientOpts(t *testing.T, handler http.Handler, extra ...vngcloud.LoadOption) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	target, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	apiTransport := glbRoundTrip(func(r *http.Request) (*http.Response, error) {
		clone := r.Clone(r.Context())
		u := *r.URL
		clone.URL = &u
		clone.URL.Scheme = target.Scheme
		clone.URL.Host = target.Host
		clone.URL.Path = strings.TrimPrefix(clone.URL.Path, "/glb-controller")
		clone.Host = target.Host
		return server.Client().Transport.RoundTrip(clone)
	})
	authTransport := glbRoundTrip(func(r *http.Request) (*http.Response, error) {
		w := httptest.NewRecorder()
		switch {
		case r.URL.Host == "signin.greennode.ai" && r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`<html><input name="_csrf" value="csrf"></html>`))
		case r.URL.Host == "signin.greennode.ai":
			w.Header().Set("Location", "https://dashboard.console.greennode.ai/?code=synthetic")
			w.WriteHeader(http.StatusFound)
		default:
			_, _ = w.Write([]byte(`{"accessToken":"synthetic","expiresIn":3600}`))
		}
		return w.Result(), nil
	})
	options := []vngcloud.LoadOption{vngcloud.WithRegion("hcm-3"), vngcloud.WithIAMUser(&vngcloud.IAMUserAuth{RootEmail: "root", Username: "user", Password: "synthetic", HTTPClient: &http.Client{Transport: authTransport}}), vngcloud.WithTransport(apiTransport), vngcloud.WithRetry(0, 0)}
	cfg, err := vngcloud.NewConfig(append(options, extra...)...)
	if err != nil {
		t.Fatal(err)
	}
	return New(cfg)
}

func TestWritesRejectUntrustedConfigBeforeRequests(t *testing.T) {
	requests := 0
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests++ }))
	_, createErr := c.CreateLoadBalancer(context.Background(), &CreateLoadBalancerInput{Name: "demo", PackageID: "pkg-1"})
	_, deleteErr := c.DeleteLoadBalancer(context.Background(), &DeleteLoadBalancerInput{LoadBalancerID: "glb-1"})
	if !errors.Is(createErr, vngcloud.ErrInvalidConfig) || !errors.Is(deleteErr, vngcloud.ErrInvalidConfig) || requests != 0 {
		t.Fatalf("create=%v delete=%v requests=%d", createErr, deleteErr, requests)
	}
}

type testProvider struct{ calls int }

func (p *testProvider) Token(context.Context) (vngcloud.Token, error) {
	p.calls++
	return vngcloud.Token{}, nil
}
func (*testProvider) Invalidate(string) {}

func TestWritesRejectEndpointAndAuthOverrides(t *testing.T) {
	provider := &testProvider{}
	for _, tc := range []struct {
		name    string
		options []vngcloud.LoadOption
	}{
		{"GLB", []vngcloud.LoadOption{vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{GLB: "https://invalid.example"})}},
		{"signin", []vngcloud.LoadOption{vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{Signin: "https://invalid.example"})}},
		{"token", []vngcloud.LoadOption{vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{Token: "https://invalid.example"})}},
		{"dashboard", []vngcloud.LoadOption{vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{Dashboard: "https://invalid.example"})}},
		{"static", []vngcloud.LoadOption{vngcloud.WithStaticToken("synthetic")}},
		{"provider", []vngcloud.LoadOption{vngcloud.WithCredentialsProvider(provider)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requests := 0
			rt := glbRoundTrip(func(*http.Request) (*http.Response, error) { requests++; return nil, errors.New("unexpected request") })
			options := []vngcloud.LoadOption{vngcloud.WithRegion("hcm-3"), vngcloud.WithIAMUser(&vngcloud.IAMUserAuth{RootEmail: "root", Username: "user", Password: "synthetic", HTTPClient: &http.Client{Transport: rt}}), vngcloud.WithTransport(rt)}
			cfg, err := vngcloud.NewConfig(append(options, tc.options...)...)
			if err != nil {
				t.Fatal(err)
			}
			c := New(cfg)
			_, createErr := c.CreateLoadBalancer(context.Background(), &CreateLoadBalancerInput{Name: "demo", PackageID: "pkg-1"})
			_, deleteErr := c.DeleteLoadBalancer(context.Background(), &DeleteLoadBalancerInput{LoadBalancerID: "glb-1"})
			if !errors.Is(createErr, vngcloud.ErrInvalidConfig) || !errors.Is(deleteErr, vngcloud.ErrInvalidConfig) || requests != 0 || provider.calls != 0 {
				t.Fatalf("create=%v delete=%v requests=%d provider=%d", createErr, deleteErr, requests, provider.calls)
			}
		})
	}
}

func TestCreateLoadBalancerBare(t *testing.T) {
	for _, status := range []int{200, 202} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			posts := 0
			c := newWriteTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "POST" {
					posts++
					var body map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Fatal(err)
					}
					if r.URL.Path != "/v1/global-load-balancers" || body["paymentFlow"] != "automated" || body["type"] != "Layer 4" || body["name"] != "demo" || body["package"] != "pkg-1" {
						t.Fatalf("unexpected request: %s %v", r.URL.Path, body)
					}
					w.WriteHeader(status)
					_, _ = w.Write([]byte(`{"globalLoadBalancer":{"id":"glb-1"}}`))
					return
				}
				_, _ = w.Write([]byte(`{"id":"glb-1","name":"demo","package":"pkg-1","type":"Layer 4","status":"ACTIVE"}`))
			}))
			out, err := c.CreateLoadBalancer(context.Background(), &CreateLoadBalancerInput{Name: "demo", PackageID: "pkg-1"})
			if err != nil || out == nil || out.LoadBalancer.ID != "glb-1" || posts != 1 {
				t.Fatalf("out=%+v err=%v posts=%d", out, err, posts)
			}
		})
	}
}

func TestCreateDefinitiveRejectionMessage(t *testing.T) {
	for _, status := range []int{400, 402, 403, 429} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			requests := 0
			c := newWriteTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests++
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{"message":"Insufficient credits for this order"}`))
			}))
			_, err := c.CreateLoadBalancer(context.Background(), &CreateLoadBalancerInput{Name: "demo", PackageID: "pkg-1"})
			var api *vngcloud.APIError
			if !errors.As(err, &api) || api.StatusCode != status || api.Message != "Insufficient credits for this order" || requests != 1 || errors.Is(err, ErrNotSettled) {
				t.Fatalf("err=%v calls=%d", err, requests)
			}
		})
	}
}

func TestCreateRejectionWithholdsSecretsAndIDs(t *testing.T) {
	for _, msg := range []string{"password is secret-value", "accountId: customer-123", "token=other-secret", "accessToken=opaquevalue", "package pkg-1 rejected", "object glb-123 rejected", "account 123e4567-e89b-12d3-a456-426614174000"} {
		t.Run(msg, func(t *testing.T) {
			c := newWriteTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(400)
				_ = json.NewEncoder(w).Encode(map[string]string{"message": msg})
			}))
			_, err := c.CreateLoadBalancer(context.Background(), &CreateLoadBalancerInput{Name: "demo", PackageID: "pkg-1"})
			var api *vngcloud.APIError
			if !errors.As(err, &api) || api.StatusCode != 400 || strings.Contains(err.Error(), "secret-value") || strings.Contains(err.Error(), "customer-123") || strings.Contains(err.Error(), "other-secret") || strings.Contains(err.Error(), "opaquevalue") || strings.Contains(err.Error(), "pkg-1") || strings.Contains(err.Error(), "glb-123") || strings.Contains(err.Error(), "123e4567-e89b-12d3-a456-426614174000") {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestCreateUnauthorizedIsNotRetried(t *testing.T) {
	requests := 0
	c := newWriteTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { requests++; w.WriteHeader(http.StatusUnauthorized) }))
	_, err := c.CreateLoadBalancer(context.Background(), &CreateLoadBalancerInput{Name: "demo", PackageID: "pkg-1"})
	var api *vngcloud.APIError
	if !errors.As(err, &api) || api.StatusCode != 401 || requests != 1 {
		t.Fatalf("err=%v requests=%d", err, requests)
	}
}

func TestCreateRejectsDuplicateID(t *testing.T) {
	requests := 0
	c := newWriteTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method == http.MethodPost {
			_, _ = w.Write([]byte(`{"globalLoadBalancer":{"id":"glb-1","ID":"glb-2"}}`))
			return
		}
		t.Error("unexpected detail request")
	}))
	_, err := c.CreateLoadBalancer(context.Background(), &CreateLoadBalancerInput{Name: "demo", PackageID: "pkg-1"})
	if !errors.Is(err, ErrNotSettled) || requests != 1 {
		t.Fatalf("err=%v requests=%d", err, requests)
	}
}

func TestCreateRejectsDuplicateDetailID(t *testing.T) {
	c := newWriteTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			_, _ = w.Write([]byte(`{"globalLoadBalancer":{"id":"glb-1"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"wrong","ID":"glb-1","name":"demo","package":"pkg-1","type":"Layer 4","status":"ACTIVE"}`))
	}))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out, err := c.CreateLoadBalancer(ctx, &CreateLoadBalancerInput{Name: "demo", PackageID: "pkg-1"})
	if !errors.Is(err, ErrNotSettled) || out == nil || out.LoadBalancer.ID != "glb-1" {
		t.Fatalf("out=%+v err=%v", out, err)
	}
}

func TestDeletePreflightNotFound(t *testing.T) {
	calls := 0
	c := newWriteTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != http.MethodGet {
			t.Errorf("unexpected %s", r.Method)
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	_, err := c.DeleteLoadBalancer(context.Background(), &DeleteLoadBalancerInput{LoadBalancerID: "glb-1"})
	var api *vngcloud.APIError
	if !errors.As(err, &api) || api.StatusCode != 404 || calls != 1 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
}

func TestDeleteWaitsForAbsence(t *testing.T) {
	for _, status := range []int{200, 202} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			calls := 0
			c := newWriteTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				switch calls {
				case 1:
					_, _ = w.Write([]byte(`{"id":"glb-1","type":"Layer 4","status":"ACTIVE"}`))
				case 2:
					if r.Method != http.MethodDelete || r.URL.Path != "/v1/global-load-balancers/glb-1" {
						t.Errorf("unexpected delete %s %s", r.Method, r.URL.Path)
					}
					w.WriteHeader(status)
				case 3:
					w.WriteHeader(404)
				default:
					t.Error("unexpected request")
				}
			}))
			out, err := c.DeleteLoadBalancer(context.Background(), &DeleteLoadBalancerInput{LoadBalancerID: "glb-1"})
			if err != nil || out == nil || !out.Deleted || calls != 3 {
				t.Fatalf("out=%+v err=%v calls=%d", out, err, calls)
			}
		})
	}
}

func TestCreateNoRetryOnUncertainStatus(t *testing.T) {
	for _, status := range []int{302, 500, 503} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			calls := 0
			c := newWriteTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.Header().Set("Location", "/other")
				w.WriteHeader(status)
			}))
			_, err := c.CreateLoadBalancer(context.Background(), &CreateLoadBalancerInput{Name: "demo", PackageID: "pkg-1"})
			if !errors.Is(err, ErrNotSettled) || calls != 1 {
				t.Fatalf("err=%v calls=%d", err, calls)
			}
		})
	}
}

func TestCreateWaitCancellationPreservesID(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := newWriteTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			_, _ = w.Write([]byte(`{"globalLoadBalancer":{"id":"glb-1"}}`))
			return
		}
		cancel()
	}))
	out, err := c.CreateLoadBalancer(ctx, &CreateLoadBalancerInput{Name: "demo", PackageID: "pkg-1"})
	if !errors.Is(err, ErrNotSettled) || out == nil || out.LoadBalancer.ID != "glb-1" {
		t.Fatalf("out=%+v err=%v", out, err)
	}
}

func TestCreateHostileResponses(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"malformed", `{"globalLoadBalancer":`},
		{"duplicate", `{"globalLoadBalancer":{"id":"glb-1","ID":"glb-2"}}`},
		{"oversize", strings.Repeat("x", 8*1024*1024+1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			c := newWriteTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls++; _, _ = w.Write([]byte(tc.body)) }))
			_, err := c.CreateLoadBalancer(context.Background(), &CreateLoadBalancerInput{Name: "demo", PackageID: "pkg-1"})
			if !errors.Is(err, ErrNotSettled) || calls != 1 {
				t.Fatalf("err=%v calls=%d", err, calls)
			}
		})
	}
}

func TestCreateDetailWrongID(t *testing.T) {
	c := newWriteTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			_, _ = w.Write([]byte(`{"globalLoadBalancer":{"id":"glb-1"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"glb-2","name":"demo","package":"pkg-1","type":"Layer 4","status":"ACTIVE"}`))
	}))
	out, err := c.CreateLoadBalancer(context.Background(), &CreateLoadBalancerInput{Name: "demo", PackageID: "pkg-1"})
	if !errors.Is(err, ErrNotSettled) || out == nil || out.LoadBalancer.ID != "glb-1" {
		t.Fatalf("out=%+v err=%v", out, err)
	}
}

func TestCreateDeadlinePreservesID(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	c := newWriteTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			_, _ = w.Write([]byte(`{"globalLoadBalancer":{"id":"glb-1"}}`))
			return
		}
		<-r.Context().Done()
	}))
	out, err := c.CreateLoadBalancer(ctx, &CreateLoadBalancerInput{Name: "demo", PackageID: "pkg-1"})
	if !errors.Is(err, ErrNotSettled) || out == nil || out.LoadBalancer.ID != "glb-1" {
		t.Fatalf("out=%+v err=%v", out, err)
	}
}

func TestDeleteWaitRejectsWrongID(t *testing.T) {
	calls := 0
	c := newWriteTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		switch calls {
		case 1:
			_, _ = w.Write([]byte(`{"id":"glb-1","type":"Layer 4","status":"ACTIVE"}`))
		case 2:
			w.WriteHeader(202)
		case 3:
			_, _ = w.Write([]byte(`{"id":"wrong"}`))
		default:
			w.WriteHeader(404)
		}
	}))
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	out, err := c.DeleteLoadBalancer(ctx, &DeleteLoadBalancerInput{LoadBalancerID: "glb-1"})
	if !errors.Is(err, ErrNotSettled) || !strings.Contains(err.Error(), "mismatch") || out == nil || out.Deleted || calls != 3 {
		t.Fatalf("out=%+v err=%v calls=%d", out, err, calls)
	}
}

func TestCreateMissingIDNotSettled(t *testing.T) {
	requests := 0
	c := newWriteTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		_, _ = w.Write([]byte(`{"globalLoadBalancer":{}}`))
	}))
	_, err := c.CreateLoadBalancer(context.Background(), &CreateLoadBalancerInput{Name: "demo", PackageID: "pkg-1"})
	if !errors.Is(err, ErrNotSettled) || requests != 1 {
		t.Fatalf("err=%v requests=%d", err, requests)
	}
}
