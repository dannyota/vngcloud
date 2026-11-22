package vks

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"danny.vn/vngcloud"
)

func writeTestClient(t *testing.T, handler http.HandlerFunc, opts ...vngcloud.LoadOption) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	target, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	api := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		clone := r.Clone(r.Context())
		u := *r.URL
		clone.URL = &u
		clone.URL.Scheme, clone.URL.Host = target.Scheme, target.Host
		clone.URL.Path = strings.TrimPrefix(clone.URL.Path, "/vks-api")
		clone.Host = target.Host
		return server.Client().Transport.RoundTrip(clone)
	})
	auth := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		w := httptest.NewRecorder()
		switch {
		case r.URL.Host == "signin.greennode.ai" && r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`<html><input name="_csrf" value="csrf"></html>`))
		case r.URL.Host == "signin.greennode.ai":
			w.Header().Set("Location", "https://dashboard.console.greennode.ai/?code=synthetic")
			w.WriteHeader(http.StatusFound)
		default:
			_, _ = w.Write([]byte(`{"accessToken":"test-token","expiresIn":3600}`))
		}
		return w.Result(), nil
	})
	base := []vngcloud.LoadOption{vngcloud.WithRegion("hcm-3"), vngcloud.WithIAMUser(&vngcloud.IAMUserAuth{RootEmail: "root", Username: "user", Password: "synthetic", HTTPClient: &http.Client{Transport: auth}}), vngcloud.WithTransport(api), vngcloud.WithRetry(0, time.Millisecond)}
	cfg, err := vngcloud.NewConfig(append(base, opts...)...)
	if err != nil {
		t.Fatal(err)
	}
	return New(cfg)
}

type provenanceProvider struct{ calls int }

func (p *provenanceProvider) Token(context.Context) (vngcloud.Token, error) {
	p.calls++
	return vngcloud.Token{}, nil
}
func (*provenanceProvider) Invalidate(string) {}

func TestWritesRejectUntrustedProvenance(t *testing.T) {
	provider := &provenanceProvider{}
	for _, tc := range []struct {
		name   string
		option vngcloud.LoadOption
	}{
		{"vks", vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{VKS: "https://invalid.example"})},
		{"signin", vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{Signin: "https://invalid.example"})},
		{"token", vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{Token: "https://invalid.example"})},
		{"dashboard", vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{Dashboard: "https://invalid.example"})},
		{"static", vngcloud.WithStaticToken("synthetic")},
		{"provider", vngcloud.WithCredentialsProvider(provider)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requests := 0
			rt := roundTripFunc(func(*http.Request) (*http.Response, error) { requests++; return nil, errors.New("unexpected request") })
			cfg, err := vngcloud.NewConfig(vngcloud.WithRegion("hcm-3"), vngcloud.WithIAMUser(&vngcloud.IAMUserAuth{RootEmail: "root", Username: "user", Password: "synthetic", HTTPClient: &http.Client{Transport: rt}}), vngcloud.WithTransport(rt), tc.option)
			if err != nil {
				t.Fatal(err)
			}
			c := New(cfg)
			_, createErr := c.CreateCluster(context.Background(), validCreate())
			_, deleteErr := c.DeleteCluster(context.Background(), &DeleteClusterInput{ClusterID: "cluster-1"})
			if !errors.Is(createErr, vngcloud.ErrInvalidConfig) || !errors.Is(deleteErr, vngcloud.ErrInvalidConfig) || requests != 0 || provider.calls != 0 {
				t.Fatalf("create=%v delete=%v requests=%d provider=%d", createErr, deleteErr, requests, provider.calls)
			}
		})
	}
}
