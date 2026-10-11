package network

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/testutil"
)

func TestVPNRequestAndPage(t *testing.T) {
	for _, region := range []string{"hcm-3", "han-1"} {
		for _, in := range []*ListVPNConnectionsInput{nil, {}, {Page: 3, Size: 27}} {
			c := natTestClient(t, region, "project-1", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/vnetwork-gateway/vnetwork/v1/project-1/vpns" {
					t.Errorf("path = %s", r.URL.Path)
				}
				if r.Header.Get("Authorization") != "Bearer test-token" || r.Header.Get("Cookie") != "" {
					t.Error("unexpected authentication")
				}
				var params struct {
					Search []struct {
						Field string
						Value string
					}
					Sort map[string]string
					Page int
					Size int
				}
				if err := json.Unmarshal([]byte(r.URL.Query().Get("params")), &params); err != nil {
					t.Error(err)
				}
				page, size := 1, 10
				if in != nil && in.Page > 0 {
					page = in.Page
					size = in.Size
				}
				if params.Page != page || params.Size != size || len(params.Search) != 1 || params.Search[0].Field != "any" || params.Search[0].Value != "" || params.Sort == nil || len(params.Sort) != 0 || len(r.URL.Query()) != 1 {
					t.Errorf("params = %+v", params)
				}
				testutil.WriteFixture(t, w, "../testdata/network/list_vpn_connections.json")
			}))
			if _, err := c.ListVPNConnections(context.Background(), in); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestVPNInvalidInputBeforeHTTP(t *testing.T) {
	for _, in := range []*ListVPNConnectionsInput{{Page: -1}, {Size: -1}} {
		c := natTestClient(t, "hcm-3", "", http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("unexpected HTTP") }))
		if _, err := c.ListVPNConnections(context.Background(), in); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Errorf("err = %v", err)
		}
	}
	c := natTestClient(t, "hcm-3", "../bad", http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("unexpected HTTP") }))
	if _, err := c.ListVPNConnections(context.Background(), nil); !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Errorf("err = %v", err)
	}
}

func TestVPNCancellation(t *testing.T) {
	c := natTestClient(t, "hcm-3", "project-1", http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) { t.Error("unexpected request") }))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.ListVPNConnections(ctx, nil); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v", err)
	}
}

type vpnRoundTripper func(*http.Request) (*http.Response, error)

func (f vpnRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestVPNProductionRoutingAndOverride(t *testing.T) {
	for _, region := range []string{"hcm-3", "han-1", "other"} {
		for _, override := range []string{"", "https://override.example/vnetwork-gateway/", "https://hcm-3.console.greennode.ai/vserver/vnetwork-gateway/"} {
			t.Run(region+override, func(t *testing.T) {
				calls := 0
				hc := &http.Client{Transport: vpnRoundTripper(func(r *http.Request) (*http.Response, error) {
					calls++
					expected := natRegionalOrigin(region) + "/vnetwork-gateway/"
					if override != "" {
						expected = override
					}
					if r.URL.String() != expected+"vnetwork/v1/project-1/vpns?params=%7B%22search%22%3A%5B%7B%22field%22%3A%22any%22%2C%22value%22%3A%22%22%7D%5D%2C%22sort%22%3A%7B%7D%2C%22page%22%3A1%2C%22size%22%3A10%7D" {
						t.Errorf("URL = %s", r.URL)
					}
					return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"success":true,"page":1,"size":10,"totalPage":0,"total":0}`)), Request: r}, nil
				})}
				cfg, err := vngcloud.NewConfig(vngcloud.WithRegion(region), vngcloud.WithProjectID("project-1"), vngcloud.WithStaticToken("test-token"), vngcloud.WithHTTPClient(hc), vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{VNetwork: override}))
				if err != nil {
					t.Fatal(err)
				}
				c := New(cfg)
				c.vnetEndpoint = "https://untrusted.example/"
				c.vnetZoneID = "unrelated-zone"
				_, err = c.ListVPNConnections(context.Background(), nil)
				if region == "other" {
					if !errors.Is(err, vngcloud.ErrInvalidConfig) || calls != 0 {
						t.Errorf("unsupported region err = %v, calls = %d", err, calls)
					}
				} else if err != nil || calls != 1 {
					t.Errorf("err = %v, calls = %d", err, calls)
				}
			})
		}
	}
}

func TestVPNGETRetries(t *testing.T) {
	calls := 0
	c := natTestClient(t, "hcm-3", "project-1", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(503)
			return
		}
		_, _ = w.Write([]byte(`{"success":true,"page":1,"size":10,"totalPage":0,"total":0}`))
	}))
	// Reuse the local endpoint with the shared transport's retry policy enabled.
	cfg, err := vngcloud.NewConfig(vngcloud.WithRegion("hcm-3"), vngcloud.WithProjectID("project-1"), vngcloud.WithStaticToken("test-token"), vngcloud.WithRetry(1, time.Millisecond), vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{VNetwork: c.c.Endpoint("vnetwork")}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := New(cfg).ListVPNConnections(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Errorf("calls = %d", calls)
	}
}

func TestVPNProjectDiscovery(t *testing.T) {
	for _, project := range []string{"project-selected", "../bad"} {
		t.Run(project, func(t *testing.T) {
			calls := 0
			c := natTestClient(t, "han-1", "", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.URL.Path == "/portal/v1/projects" {
					_, _ = w.Write([]byte(`{"projects":[{"projectId":"project-other","region":"hcm-3"},{"projectId":"` + project + `","region":"han-1"}]}`))
					return
				}
				if r.URL.Path != "/vnetwork-gateway/vnetwork/v1/project-selected/vpns" {
					t.Errorf("path = %s", r.URL.Path)
				}
				_, _ = w.Write([]byte(`{"success":true,"page":1,"size":10,"totalPage":0,"total":0}`))
			}))
			// Project discovery uses VServer, independently of the VPN endpoint.
			cfg, err := vngcloud.NewConfig(vngcloud.WithRegion("han-1"), vngcloud.WithStaticToken("test-token"), vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{VServer: c.c.Endpoint("portal"), VNetwork: c.c.Endpoint("vnetwork")}))
			if err != nil {
				t.Fatal(err)
			}
			_, err = New(cfg).ListVPNConnections(context.Background(), nil)
			if project == "../bad" {
				if !errors.Is(err, vngcloud.ErrInvalidInput) || calls != 1 {
					t.Errorf("err = %v, calls = %d", err, calls)
				}
			} else if err != nil || calls != 2 {
				t.Errorf("err = %v, calls = %d", err, calls)
			}
		})
	}
}
