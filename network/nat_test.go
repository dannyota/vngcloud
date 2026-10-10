package network

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/testutil"
)

func natTestClient(t *testing.T, region, project string, h http.Handler) *Client {
	t.Helper()
	s := httptest.NewServer(h)
	t.Cleanup(s.Close)
	cfg, err := vngcloud.NewConfig(vngcloud.WithRegion(region), vngcloud.WithProjectID(project), vngcloud.WithStaticToken("test-token"), vngcloud.WithRetry(0, 0), vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{VNetwork: s.URL + "/vnetwork-gateway/", Portal: s.URL + "/portal/"}))
	if err != nil {
		t.Fatal(err)
	}
	return New(cfg)
}

func TestNATRequestAndFixture(t *testing.T) {
	for _, region := range []string{"hcm-3", "han-1"} {
		t.Run(region, func(t *testing.T) {
			calls := 0
			c := natTestClient(t, region, "project-1", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Header.Get("Authorization") != "Bearer test-token" || r.Header.Get("Cookie") != "" {
					t.Error("unexpected authentication")
				}
				if r.URL.Path == "/vnetwork-gateway/vnetwork/v1/regions" {
					_, _ = w.Write([]byte(`{"success":true,"data":[{"uuid":"zone-example","vnetworkDashboard":"https://` + region + `-vnetwork.console.greennode.ai","gatewayUrl":"https://untrusted.example/","code":"example"}]}`))
					return
				}
				if r.URL.Path != "/vnetwork-gateway/vnetwork/v1/zone-example/project-1/nats" {
					t.Errorf("path = %s", r.URL.Path)
				}
				var params struct {
					Search []string          `json:"search"`
					Sort   map[string]string `json:"sort"`
					Page   int               `json:"page"`
					Size   int               `json:"size"`
				}
				if err := json.Unmarshal([]byte(r.URL.Query().Get("params")), &params); err != nil {
					t.Error(err)
				}
				if params.Page != 1 || params.Size != 10 || params.Search == nil || len(params.Search) != 0 || params.Sort == nil || len(params.Sort) != 0 || len(r.URL.Query()) != 1 {
					t.Errorf("params = %+v", params)
				}
				testutil.WriteFixture(t, w, "../testdata/network/list_nat_instances.json")
			}))
			out, err := c.ListNATInstances(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			if calls != 2 || out.Page != 1 || out.PageSize != 10 || out.TotalPage != 7 || out.TotalItem != 63 || len(out.Items) != 4 {
				t.Fatalf("unexpected result: %+v, calls %d", out, calls)
			}
			assertNATFixture(t, out.Items)
		})
	}
}

func assertNATFixture(t *testing.T, items []NATInstance) {
	t.Helper()
	want := NATInstance{UUID: "nat-example", NATName: "nat-example", Status: "ACTIVE", CreatedAt: "2026-01-01T00:00:00Z", UpdatedAt: "2026-01-02T00:00:00Z", ProjectUUID: "project-1", ZoneUUID: "zone-example", NATGatewayIP: natString("203.0.113.10"), PublicIP: natString("198.51.100.10"), BillingStatus: natString("active"), NATPackage: NATPackage{ID: "package-1", UUID: "package-example", Name: "package-example", CreatedAt: "2026-01-01T00:00:00Z", PackageID: "nat-package-example", Default: true, Image: NATImage{ID: "image-1", UUID: "image-example", ImageType: "example", ImageVersion: "1", Licence: "example", FlavorZoneIDs: []string{"zone-example"}, PackageLimit: NATPackageLimit{CPU: 2, Memory: 4, DiskSize: 8}}}, VPC: NATVPC{UUID: "vpc-example", Name: "vpc-example", CIDR: "203.0.113.0/24", Status: "ACTIVE", RegionID: "region-example", ProjectID: "project-1", LastSyncTime: "2026-01-02T00:00:00Z", DNSStatus: "ENABLED"}}
	if !reflect.DeepEqual(items[0], want) {
		t.Errorf("row = %+v, want %+v", items[0], want)
	}
	if items[1].Status != "PROVISIONING" || items[1].PublicIP != nil || items[1].NATGatewayIP != nil || items[1].DeletedAt != nil || items[1].BillingStatus == nil || *items[1].BillingStatus != "provisioning" {
		t.Error("provisioning fields")
	}
	if items[2].Status != "ERROR" || items[2].BillingStatus != nil || items[2].DeletedAt == nil || *items[2].DeletedAt != "2026-01-03T00:00:00Z" {
		t.Error("error fields")
	}
	if items[3].Status != "FUTURE_STATUS" || items[3].PublicIP == nil || *items[3].PublicIP != "" {
		t.Error("future or empty-string fields")
	}
	b, err := json.Marshal(items)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"portalUserId", "visible", "message", "subnet", "monthlyPrice", "licenseKey", "elasticIps", "excluded-canary"} {
		if strings.Contains(string(b), key) {
			t.Errorf("exposed %s", key)
		}
	}
}
func natString(s string) *string { return &s }

func TestNATEnvelope(t *testing.T) {
	cases := []struct {
		name, body string
		valid      bool
	}{
		{"omitted data", `{"success":true,"page":1,"size":10,"totalPage":0,"total":0}`, true},
		{"array", `{"success":true,"data":[],"page":2,"size":3,"totalPage":0,"total":0}`, true},
		{"null data", `{"success":true,"data":null,"page":1,"size":10,"totalPage":0,"total":0}`, false},
		{"missing total", `{"success":true,"page":1,"size":10,"totalPage":0}`, false},
		{"missing totalPage", `{"success":true,"page":1,"size":10,"total":0}`, false},
		{"nonzero total", `{"success":true,"page":1,"size":10,"totalPage":1,"total":1}`, false},
		{"missing success", `{"data":[],"page":1,"size":10,"totalPage":0,"total":0}`, false},
		{"null success", `{"success":null,"data":[],"page":1,"size":10,"totalPage":0,"total":0}`, false},
		{"false success", `{"success":false,"code":"NATFailure","message":"example failure"}`, false},
		{"wrong success", `{"success":"true","data":[],"page":1,"size":10,"totalPage":0,"total":0}`, false},
		{"zero page", `{"success":true,"data":[],"page":0,"size":10,"totalPage":0,"total":0}`, false},
		{"zero size", `{"success":true,"data":[],"page":1,"size":0,"totalPage":0,"total":0}`, false},
		{"negative total", `{"success":true,"data":[],"page":1,"size":10,"totalPage":0,"total":-1}`, false},
		{"fraction", `{"success":true,"data":[],"page":1.1,"size":10,"totalPage":0,"total":0}`, false},
		{"null total", `{"success":true,"data":[],"page":1,"size":10,"totalPage":0,"total":null}`, false},
		{"object data", `{"success":true,"data":{},"page":1,"size":10,"totalPage":0,"total":0}`, false},
		{"empty uuid", `{"success":true,"data":[{"uuid":""}],"page":1,"size":10,"totalPage":1,"total":1}`, false},
		{"wrong uuid", `{"success":true,"data":[{"uuid":1}],"page":1,"size":10,"totalPage":1,"total":1}`, false},
		{"null row", `{"success":true,"data":[null],"page":1,"size":10,"totalPage":1,"total":1}`, false},
		{"html", `<html>example</html>`, false}, {"malformed", `{"success":`, false}, {"empty", ``, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := natTestClient(t, "hcm-3", "project-1", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(tc.body)) }))
			out, err := c.ListNATInstances(context.Background(), &ListNATInstancesInput{ZoneID: "zone-example"})
			if tc.valid {
				if err != nil {
					t.Fatal(err)
				}
				if out.Items == nil || len(out.Items) != 0 {
					t.Error("expected empty slice")
				}
				return
			}
			if out != nil || err == nil {
				t.Fatal("invalid envelope accepted")
			}
			code := vngcloud.ErrorCode(err)
			if tc.name == "false success" {
				if code != "NATFailure" || !strings.Contains(err.Error(), "example failure") {
					t.Errorf("failure = %v", err)
				}
			} else if code != "InvalidResponse" {
				t.Errorf("code = %q, err %v", code, err)
			}
		})
	}
}

func TestNATInvalidInputBeforeHTTP(t *testing.T) {
	for _, in := range []*ListNATInstancesInput{{ZoneID: "../bad"}, {Page: -1}, {Size: -1}} {
		t.Run("input", func(t *testing.T) {
			c := natTestClient(t, "hcm-3", "", http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) { t.Error("unexpected HTTP") }))
			if _, err := c.ListNATInstances(context.Background(), in); !errors.Is(err, vngcloud.ErrInvalidInput) {
				t.Errorf("err = %v", err)
			}
		})
	}
	c := natTestClient(t, "hcm-3", "../bad", http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) { t.Error("unexpected HTTP") }))
	if _, err := c.ListNATInstances(context.Background(), nil); !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Errorf("project err = %v", err)
	}
}

func TestNATExplicitZoneAndPage(t *testing.T) {
	c := natTestClient(t, "han-1", "project-1", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/vnetwork-gateway/vnetwork/v1/zone-explicit/project-1/nats" {
			t.Errorf("path = %s", r.URL.Path)
		}
		var p map[string]json.RawMessage
		if err := json.Unmarshal([]byte(r.URL.Query().Get("params")), &p); err != nil {
			t.Fatal(err)
		}
		if string(p["page"]) != "3" || string(p["size"]) != "27" {
			t.Errorf("params = %v", p)
		}
		_, _ = w.Write([]byte(`{"success":true,"data":[],"page":3,"size":27,"totalPage":8,"total":190}`))
	}))
	out, err := c.ListNATInstances(context.Background(), &ListNATInstancesInput{ZoneID: "zone-explicit", Page: 3, Size: 27})
	if err != nil {
		t.Fatal(err)
	}
	if out.Page != 3 || out.PageSize != 27 || out.TotalPage != 8 || out.TotalItem != 190 {
		t.Errorf("metadata = %+v", out)
	}
}

func TestNATInvalidRowTypes(t *testing.T) {
	for _, row := range []string{
		`{"uuid":"nat-example","natName":null}`,
		`{"uuid":"nat-example","natPackage":null}`,
		`{"uuid":"nat-example","vpc":null}`,
		`{"uuid":"nat-example","natPackage":{"default":null}}`,
		`{"uuid":"nat-example","natPackage":{"image":{"flavorZoneIds":[null]}}}`,
		`{"uuid":"nat-example","natPackage":{"image":{"packageLimit":{"cpu":null}}}}`,
		`{"uuid":"nat-example","natGatewayIp":42}`,
		`{"uuid":"nat-example","natPackage":{"image":{"packageLimit":{"cpu":1.5}}}}`,
	} {
		t.Run(row, func(t *testing.T) {
			c := natTestClient(t, "hcm-3", "project-1", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`{"success":true,"data":[` + row + `],"page":1,"size":10,"totalPage":1,"total":1}`))
			}))
			if _, err := c.ListNATInstances(context.Background(), &ListNATInstancesInput{ZoneID: "zone-example"}); vngcloud.ErrorCode(err) != "InvalidResponse" {
				t.Errorf("err = %v", err)
			}
		})
	}
}

func TestNATFailureEnvelopeRedaction(t *testing.T) {
	c := natTestClient(t, "hcm-3", "project-1", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"success":false,"message":"test-token","code":"test-token"}`))
	}))
	_, err := c.ListNATInstances(context.Background(), &ListNATInstancesInput{ZoneID: "zone-example"})
	var apiErr *vngcloud.APIError
	if !errors.As(err, &apiErr) {
		t.Fatal("missing APIError")
	}
	if strings.Contains(apiErr.Message, "test-token") || strings.Contains(apiErr.Code, "test-token") || strings.Contains(err.Error(), "test-token") {
		t.Error("credential exposed")
	}
}
