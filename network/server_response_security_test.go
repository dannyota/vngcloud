package network

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"danny.vn/vngcloud"
)

func TestSecurityGroupServerResponseSafety(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		fail       bool
	}{
		{"schema-built", `{"data":[{"uuid":"server-1","internalInterfaces":[{"floatingIp":"::ffff:192.0.2.1"}]}]}`, false},
		{"limit", strings.Repeat(" ", 8<<20-len(`{"data":[]}`)) + `{"data":[]}`, false},
		{"limit plus one", strings.Repeat(" ", (8<<20)+1-len(`{"data":[]}`)) + `{"data":[]}`, true},
		{"synthetic truncated", `{"data":[{"name":"hostile-marker"}`, true},
		{"synthetic type error", `{"data":[{"uuid":"hostile-marker","status":[]}]}`, true},
		{"synthetic credential", `{"data":[{"name":"synthetic-token"}]}`, true},
		{"synthetic escaped credential", `{"data":[{"name":"\u0073ynthetic-token"}]}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(tc.body)) }))
			defer srv.Close()
			captures := 0
			cfg, err := vngcloud.NewConfig(vngcloud.WithRegion("hcm-3"), vngcloud.WithProjectID("project-1"), vngcloud.WithStaticToken("synthetic-token"), vngcloud.WithHTTPClient(srv.Client()), vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{VServer: srv.URL}), vngcloud.WithRetry(0, 0), vngcloud.WithResponseCapture(func(vngcloud.ResponseCapture) { captures++ }))
			if err != nil {
				t.Fatal(err)
			}
			out, err := New(cfg).ListServersBySecurityGroup(context.Background(), &ListServersBySecurityGroupInput{SecurityGroupID: "group-1"})
			switch {
			case tc.fail:
				if err == nil || out != nil {
					t.Fatalf("unsafe output present=%v, error=%v", out != nil, err)
				}
				if strings.Contains(err.Error(), "synthetic-token") || strings.Contains(err.Error(), "hostile-marker") {
					t.Fatal("error leaked body")
				}
			case err != nil:
				t.Fatal(err)
			case tc.name == "schema-built" && !strings.Contains(fmt.Sprintf("%+v", out.Items[0]), "PublicIP:192.0.2.1"):
				t.Fatal("missing derived address")
			}
			if captures != 0 {
				t.Fatal("guarded server response captured")
			}
		})
	}
}

// All addresses and credentials are synthetic; the unknown number is valid JSON.
func TestSecurityGroupServerRejectsNumericScannerBypassAndDerivedReflection(t *testing.T) {
	for _, tc := range []struct {
		name, token, body string
		unsafe            bool
	}{
		{"escaped credential after large number", "synthetic-token", `{"ignored":1e999,"data":[{"name":"\u0073ynthetic-token"}]}`, true},
		{"benign large number", "synthetic-token", `{"ignored":1e999,"data":[{"name":"safe"}]}`, false},
		{"derived credential", "2001:db8::1", `{"data":[{"internalInterfaces":[{"floatingIp":"2001:0DB8:0:0:0:0:0:1"}]}]}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(tc.body)) }))
			defer srv.Close()
			cfg, err := vngcloud.NewConfig(vngcloud.WithRegion("hcm-3"), vngcloud.WithProjectID("project-1"), vngcloud.WithStaticToken(tc.token), vngcloud.WithHTTPClient(srv.Client()), vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{VServer: srv.URL}), vngcloud.WithRetry(0, 0))
			if err != nil {
				t.Fatal(err)
			}
			out, err := New(cfg).ListServersBySecurityGroup(context.Background(), &ListServersBySecurityGroupInput{SecurityGroupID: "group-1"})
			if tc.unsafe {
				if out != nil || err == nil {
					t.Fatal("credential-bearing decoded server was returned")
				}
				if strings.Contains(err.Error(), tc.token) {
					t.Fatal("credential leaked through error")
				}
			} else if out == nil || err != nil {
				t.Fatalf("benign response refused: %v", err)
			}
		})
	}
}

func TestSecurityGroupServerRejectsNormalizedNumericCredential(t *testing.T) {
	// The schema-built response uses a synthetic numeric credential.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"gpuMemory":1.23456789e8}]}`))
	}))
	defer srv.Close()
	cfg, err := vngcloud.NewConfig(vngcloud.WithRegion("hcm-3"), vngcloud.WithProjectID("project-1"), vngcloud.WithStaticToken("123456789"), vngcloud.WithHTTPClient(srv.Client()), vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{VServer: srv.URL}), vngcloud.WithRetry(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	out, err := New(cfg).ListServersBySecurityGroup(context.Background(), &ListServersBySecurityGroupInput{SecurityGroupID: "group-1"})
	if out != nil || err == nil {
		t.Fatal("normalized numeric credential escaped through server output")
	}
	if strings.Contains(err.Error(), "123456789") {
		t.Fatal("numeric credential escaped through error")
	}
}
