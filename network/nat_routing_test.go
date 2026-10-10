package network

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"danny.vn/vngcloud"
)

func TestNATZoneResolution(t *testing.T) {
	cases := []struct {
		name, body string
		want       error
		code       string
	}{
		{"no match", `{"success":true,"data":[{"uuid":"zone-example","code":"hcm-3","vnetworkDashboard":"https://untrusted.example/hcm-3"}]}`, vngcloud.ErrInvalidConfig, ""},
		{"other region", `{"success":true,"data":[{"uuid":"zone-example","vnetworkDashboard":"https://han-1-vnetwork.console.greennode.ai"}]}`, vngcloud.ErrInvalidConfig, ""},
		{"duplicate", `{"success":true,"data":[{"uuid":"zone-a","vnetworkDashboard":"https://hcm-3-vnetwork.console.greennode.ai"},{"uuid":"zone-b","gatewayUrl":"https://hcm-3-vnetwork.console.greennode.ai/vnetwork-gateway/"}]}`, vngcloud.ErrInvalidConfig, ""},
		{"invalid uuid", `{"success":true,"data":[{"uuid":"../bad","vnetworkDashboard":"https://hcm-3-vnetwork.console.greennode.ai"}]}`, vngcloud.ErrInvalidConfig, ""},
		{"conflict", `{"success":true,"data":[{"uuid":"zone-a","vnetworkDashboard":"https://hcm-3-vnetwork.console.greennode.ai","gatewayUrl":"https://han-1-vnetwork.console.greennode.ai/vnetwork-gateway"}]}`, vngcloud.ErrInvalidConfig, ""},
		{"missing success", `{"data":[]}`, nil, "InvalidResponse"},
		{"null data", `{"success":true,"data":null}`, nil, "InvalidResponse"},
		{"malformed", `<html>example</html>`, nil, "InvalidResponse"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			c := natTestClient(t, "hcm-3", "project-1", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.URL.Path != "/vnetwork-gateway/vnetwork/v1/regions" {
					t.Errorf("unexpected resource call %s", r.URL.Path)
				}
				_, _ = w.Write([]byte(tc.body))
			}))
			_, err := c.ListNATInstances(context.Background(), nil)
			if err == nil || (tc.want != nil && !errors.Is(err, tc.want)) || (tc.code != "" && vngcloud.ErrorCode(err) != tc.code) {
				t.Errorf("err = %v", err)
			}
			if calls != 1 {
				t.Errorf("calls = %d", calls)
			}
		})
	}
}

func TestNATHTTPFailures(t *testing.T) {
	for _, status := range []int{401, 403, 404, 429, 500, 503} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			calls := 0
			c := natTestClient(t, "hcm-3", "project-1", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls++
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{"code":"ExampleFailure","message":"example failure"}`))
			}))
			_, err := c.ListNATInstances(context.Background(), &ListNATInstancesInput{ZoneID: "zone-example"})
			var apiErr *vngcloud.APIError
			if !errors.As(err, &apiErr) || apiErr.StatusCode != status || apiErr.Operation != listNATOperation || apiErr.Code != "ExampleFailure" {
				t.Fatalf("err = %v", err)
			}
			if apiErr.Retryable != (status == 429 || status == 503) {
				t.Errorf("retryable = %v", apiErr.Retryable)
			}
			switch status {
			case 401:
				if !errors.Is(err, vngcloud.ErrAuth) {
					t.Error("missing auth sentinel")
				}
			case 403:
				if !errors.Is(err, vngcloud.ErrPermission) {
					t.Error("missing permission sentinel")
				}
			case 404:
				if !vngcloud.IsNotFound(err) {
					t.Error("missing not found sentinel")
				}
			case 429:
				if !vngcloud.IsRateLimited(err) {
					t.Error("missing rate limit sentinel")
				}
			}
			wantCalls := 1
			if status == 401 {
				wantCalls = 2
			}
			if calls != wantCalls {
				t.Errorf("calls = %d", calls)
			}
		})
	}
}

func TestNATDiscoveryDenialNoFallback(t *testing.T) {
	calls := 0
	c := natTestClient(t, "han-1", "project-1", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if !strings.HasSuffix(r.URL.Path, "/regions") {
			t.Error("resource called after discovery denial")
		}
		w.WriteHeader(403)
	}))
	if _, err := c.ListNATInstances(context.Background(), nil); !errors.Is(err, vngcloud.ErrPermission) {
		t.Errorf("err = %v", err)
	}
	if calls != 1 {
		t.Errorf("calls = %d", calls)
	}
}

func TestNATCancellation(t *testing.T) {
	c := natTestClient(t, "hcm-3", "project-1", http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) { t.Error("unexpected request") }))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.ListNATInstances(ctx, nil); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v", err)
	}
}

type natRoundTripper func(*http.Request) (*http.Response, error)

func (f natRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestNATProductionRoutingAndOverride(t *testing.T) {
	for _, region := range []string{"hcm-3", "han-1", "other"} {
		for _, override := range []string{"", "https://override.example/vnetwork-gateway/", "https://hcm-3.console.greennode.ai/vserver/vnetwork-gateway/"} {
			t.Run(region+override, func(t *testing.T) {
				calls := 0
				hc := &http.Client{Transport: natRoundTripper(func(r *http.Request) (*http.Response, error) {
					calls++
					expected := natRegionalOrigin(region) + "/vnetwork-gateway/"
					if override != "" {
						expected = override
					}
					if r.URL.String() != expected+"vnetwork/v1/zone-example/project-1/nats?params=%7B%22search%22%3A%5B%5D%2C%22sort%22%3A%7B%7D%2C%22page%22%3A1%2C%22size%22%3A10%7D" {
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
				_, err = c.ListNATInstances(context.Background(), &ListNATInstancesInput{ZoneID: "zone-example"})
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

func TestNATGETRetries(t *testing.T) {
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
	if _, err := New(cfg).ListNATInstances(context.Background(), &ListNATInstancesInput{ZoneID: "zone-example"}); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Errorf("calls = %d", calls)
	}
}

func TestNATProjectDiscovery(t *testing.T) {
	for _, project := range []string{"project-selected", "../bad"} {
		t.Run(project, func(t *testing.T) {
			calls := 0
			c := natTestClient(t, "han-1", "", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.URL.Path == "/portal/v1/projects" {
					_, _ = w.Write([]byte(`{"projects":[{"projectId":"project-other","region":"hcm-3"},{"projectId":"` + project + `","region":"han-1"}]}`))
					return
				}
				if r.URL.Path != "/vnetwork-gateway/vnetwork/v1/zone-example/project-selected/nats" {
					t.Errorf("path = %s", r.URL.Path)
				}
				_, _ = w.Write([]byte(`{"success":true,"page":1,"size":10,"totalPage":0,"total":0}`))
			}))
			// Project discovery uses VServer, independently of the NAT endpoint.
			cfg, err := vngcloud.NewConfig(vngcloud.WithRegion("han-1"), vngcloud.WithStaticToken("test-token"), vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{VServer: c.c.Endpoint("portal"), VNetwork: c.c.Endpoint("vnetwork")}))
			if err != nil {
				t.Fatal(err)
			}
			_, err = New(cfg).ListNATInstances(context.Background(), &ListNATInstancesInput{ZoneID: "zone-example"})
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
