//go:build livewrite

package livetest_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/billing"
	"danny.vn/vngcloud/network"
)

func TestNATHarnessIncompleteRowsThroughSDK(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/route-table"):
			_, _ = w.Write([]byte(`{"listData":[{"uuid":"route-1"}],"page":1,"pageSize":1000,"totalPage":1,"totalItem":1}`))
		case strings.HasSuffix(r.URL.Path, "/resources"):
			_, _ = w.Write([]byte(`{"code":200,"data":{"data":[{"product":"vserver","artifactType":"nat"}]}}`))
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	cfg, err := vngcloud.NewConfig(vngcloud.WithStaticToken("synthetic"), vngcloud.WithProjectID("project-1"), vngcloud.WithRegion("han-1"), vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{VServer: server.URL, Billing: server.URL}), vngcloud.WithRetry(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	routes, err := network.New(cfg).ListRouteTables(ctx, &network.ListRouteTablesInput{Page: 1, Size: 1000})
	if err != nil || len(routes.Items) != 1 || routes.Items[0].NetworkID != "" {
		t.Fatal("SDK did not return the incomplete route")
	}
	if _, err := liveNATRoutes(ctx, cfg, "vpc-1"); err == nil {
		t.Fatal("incomplete route allowed VPC deletion")
	}
	resources, err := billing.New(cfg).ListResources(ctx, nil)
	if err != nil || len(resources.Items) != 1 || resources.Items[0].ArtifactID != "" {
		t.Fatal("SDK did not return the incomplete billing row")
	}
	if liveNATBillingAbsent(ctx, billing.New(cfg), "nat-1") {
		t.Fatal("incomplete billing row allowed VPC deletion")
	}
}

func TestNATHarnessIncompleteRows(t *testing.T) {
	for _, tc := range []struct {
		name          string
		rows          []billing.Resource
		absent, valid bool
	}{
		{"missing id", []billing.Resource{{Product: "vserver", ArtifactType: "nat"}}, false, false},
		{"missing product", []billing.Resource{{ArtifactID: "other", ArtifactType: "nat"}}, false, false},
		{"missing type", []billing.Resource{{ArtifactID: "other", Product: "vserver"}}, false, false},
		{"present", []billing.Resource{{Product: "vserver", ArtifactType: "nat", ArtifactID: "nat-1"}}, false, true},
		{"other", []billing.Resource{{Product: "vserver", ArtifactType: "nat", ArtifactID: "other"}}, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			absent, valid := natBillingRowsAbsent(tc.rows, "nat-1")
			if absent != tc.absent || valid != tc.valid {
				t.Fatalf("absent=%v valid=%v", absent, valid)
			}
		})
	}
	if natRouteIdentityValid(network.RouteTable{UUID: "route-1"}) {
		t.Fatal("route without network ID accepted")
	}
	if !natRouteIdentityValid(network.RouteTable{UUID: "route-1", NetworkID: "vpc-1"}) {
		t.Fatal("valid route rejected")
	}
}
