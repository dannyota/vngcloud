package network

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/testutil"
)

func TestNATCatalogFixtures(t *testing.T) {
	c := natTestClient(t, "han-1", "project-1", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("params") != `{"search":[],"sort":{},"page":1,"size":1000}` {
			t.Error("catalog query")
		}
		switch r.URL.Path {
		case "/vnetwork-gateway/vnetwork/v1/zone-1/project-1/nats/zones":
			testutil.WriteFixture(t, w, "../testdata/network/list_nat_zones.json")
		case "/vnetwork-gateway/vnetwork/v1/zone-1/project-1/nats/nat-package":
			if r.URL.Query().Get("zoneUuid") != "az-1" {
				t.Error("missing availability zone")
			}
			testutil.WriteFixture(t, w, "../testdata/network/list_nat_packages.json")
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	zones, err := c.ListNATZones(context.Background(), &ListNATZonesInput{ZoneID: "zone-1"})
	if err != nil || len(zones.Items) != 1 || !zones.Items[0].IsEnabled {
		t.Fatalf("zones: %v", err)
	}
	packages, err := c.ListNATPackages(context.Background(), &ListNATPackagesInput{ZoneID: "zone-1", AvailabilityZoneID: "az-1"})
	if err != nil || len(packages.Items) != 1 || packages.Items[0].Description != nil {
		t.Fatalf("packages: %v", err)
	}
	encoded, _ := json.Marshal(packages)
	if strings.Contains(string(encoded), "catalog-secret-canary") || strings.Contains(string(encoded), "image") {
		t.Fatal("catalog image leaked")
	}
	offer := packages.Items[0]
	if offer.UUID != "package-1" || offer.Name != "Standard" || offer.PackageID != "standard" || offer.ResourceServiceID != "service-1" || offer.BillingSKU != "nat.s-standard" || offer.ServiceName != "nat" || offer.CurrencyUnit != "VND" || offer.CreatedAt != "2026-01-01" || !offer.IsDefault || offer.MonthlyPrice != 100 || offer.Price.OptimumPrice != 100 || offer.Price.OriginalPrice != 100 || offer.Price.DiscountPrice != 0 || offer.Price.DiscountPercent != 0 {
		t.Fatalf("catalog fields %+v", offer)
	}

}

func TestNATCatalogStrictDecoding(t *testing.T) {
	for _, packages := range []bool{false, true} {
		good := natTestZones
		if packages {
			good = natTestPackages
		}
		for _, tt := range []struct {
			name, body string
			valid      bool
		}{
			{"valid", good, true},
			{"missing success", strings.Replace(good, `"success":true,`, "", 1), false},
			{"string code", strings.Replace(good, `"code":200`, `"code":"200"`, 1), false},
			{"wrong code", strings.Replace(good, `"code":200`, `"code":0`, 1), false},
			{"null data", `{"code":200,"success":true,"data":null}`, false},
			{"missing data", `{"code":200,"success":true}`, false},
			{"duplicate keys", strings.Replace(good, `"success":true`, `"success":true,"success":true`, 1), false},
			{"case alias", strings.Replace(good, `"uuid"`, `"UUID"`, 1), false},
			{"absent UUID", strings.Replace(good, `"uuid":"HAN01-1B",`, "", 1), packages},
			{"missing default", strings.Replace(strings.Replace(good, `"isDefault":false,`, "", 1), `"isDefault":true,`, "", 1), false},
			{"null default", strings.Replace(strings.Replace(good, `"isDefault":false`, `"isDefault":null`, 1), `"isDefault":true`, `"isDefault":null`, 1), false},
		} {
			t.Run(fmt.Sprintf("%t/%s", packages, tt.name), func(t *testing.T) {
				c := natTestClient(t, "han-1", "project-1", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(tt.body)) }))
				var err error
				if packages {
					_, err = c.ListNATPackages(context.Background(), &ListNATPackagesInput{ZoneID: "zone-1", AvailabilityZoneID: "az-1"})
				} else {
					_, err = c.ListNATZones(context.Background(), &ListNATZonesInput{ZoneID: "zone-1"})
				}
				if tt.valid {
					if err != nil {
						t.Fatal(err)
					}
				} else if vngcloud.ErrorCode(err) != "InvalidResponse" {
					t.Fatalf("%v", err)
				}
			})
		}
	}
	t.Run("unknown zone type retained", func(t *testing.T) {
		c := natTestClient(t, "han-1", "project-1", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(strings.Replace(natTestZones, "AVAILABILITY", "FUTURE", 1)))
		}))
		out, err := c.ListNATZones(context.Background(), &ListNATZonesInput{ZoneID: "zone-1"})
		if err != nil || out.Items[0].ZoneType != "FUTURE" {
			t.Fatal(err)
		}
	})
}
func TestNATCatalogInputAndDiscovery(t *testing.T) {
	calls := 0
	c := natTestClient(t, "han-1", "project-1", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if strings.HasSuffix(r.URL.Path, "/regions") {
			_, _ = w.Write([]byte(`{"success":true,"data":[{"uuid":"zone-1","vnetworkDashboard":"https://han-1-vnetwork.console.greennode.ai"}]}`))
			return
		}
		_, _ = w.Write([]byte(natTestZones))
	}))
	_, err := c.ListNATZones(context.Background(), nil)
	if err != nil || calls != 2 {
		t.Fatalf("discovery: %v", err)
	}
	before := calls
	for _, in := range []*ListNATPackagesInput{nil, {}, {ZoneID: "zone-1", AvailabilityZoneID: "../escape"}, {ZoneID: "../escape", AvailabilityZoneID: "az-1"}} {
		_, err = c.ListNATPackages(context.Background(), in)
		if !errors.Is(err, vngcloud.ErrInvalidInput) || calls != before {
			t.Fatalf("%v", err)
		}
	}
}

func TestNATPackageDescription(t *testing.T) {
	c := natTestClient(t, "han-1", "project-1", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(strings.Replace(natTestPackages, `"description":null`, `"description":"Example"`, 1)))
	}))
	out, err := c.ListNATPackages(context.Background(), &ListNATPackagesInput{ZoneID: "zone-1", AvailabilityZoneID: "az-1"})
	if err != nil || out.Items[0].Description == nil || *out.Items[0].Description != "Example" {
		t.Fatalf("description: %v", err)
	}
}

func TestNATCatalogDiscoveryWithholdsCaptures(t *testing.T) {
	s := new(natScenario)
	captures := 0
	c := natWriteTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/regions") {
			_, _ = w.Write([]byte(`{"success":true,"data":[{"uuid":"zone-1","vnetworkDashboard":"https://han-1-vnetwork.console.greennode.ai","portalUserId":"portal-secret-canary"}]}`))
			return
		}
		s.serve(t, w, r)
	}), vngcloud.WithResponseCapture(func(vngcloud.ResponseCapture) { captures++ }))
	_, err := c.ListNATZones(context.Background(), nil)
	if err != nil || captures != 0 {
		t.Fatalf("catalog discovery %v captures %d", err, captures)
	}
}

func TestNATNumericCode(t *testing.T) {
	for _, tt := range []struct {
		raw      string
		expected int
		valid    bool
	}{
		{`0`, 0, true}, {`0.0`, 0, true}, {`0e999999999`, 0, true},
		{`1e-999999999`, 0, false}, {`0.0000000000000000001`, 0, false},
		{`200`, 200, true}, {`2e2`, 200, true}, {`200.0000000000000000001`, 200, false},
		{`2e999999999`, 200, false}, {`"200"`, 200, false}, {`null`, 0, false},
	} {
		if natNumericCode(json.RawMessage(tt.raw), tt.expected) != tt.valid {
			t.Errorf("numeric code %s", tt.raw)
		}
	}
}
