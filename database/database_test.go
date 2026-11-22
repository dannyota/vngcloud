package database

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"danny.vn/vngcloud"
)

func TestNestedSubnetWithNullNetworkUUID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"code":200,"message":"ok","data":[{"uuid":"network","createdAt":"2025-01-01T00:00:00Z","status":"ACTIVE","displayName":"vpc","networkId":1,"zoneId":"zone","subnets":[{"uuid":"subnet","createdAt":"2025-01-01T00:00:00Z","updatedAt":"2025-01-01T00:00:00Z","deletedAt":null,"status":"ACTIVE","cidr":"192.0.2.0/24","networkUuid":null,"routeTableUuid":null,"name":"subnet","interfaceAclPolicyUuid":null,"zoneId":"zone"}]}]}`)
	}))
	defer server.Close()
	cfg, err := vngcloud.NewConfig(vngcloud.WithRegion("hcm-3"), vngcloud.WithStaticToken("test-token"), vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{Database: server.URL}), vngcloud.WithRetry(0, time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	out, err := New(cfg).ListDatabaseNetworks(context.Background(), &ListDatabaseNetworksInput{ZoneID: "zone"})
	if err != nil || len(out.Items) != 1 || len(out.Items[0].Subnets) != 1 || out.Items[0].Subnets[0].UUID != "subnet" {
		t.Fatalf("unexpected result: %#v, %v", out, err)
	}
}

func TestHTMLResponseIsInvalidResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/vdb/vdb-relational/v1/database-instances/zones" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(w, "<html>app</html>")
	}))
	defer server.Close()
	cfg, err := vngcloud.NewConfig(vngcloud.WithRegion("hcm-3"), vngcloud.WithStaticToken("test-token"), vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{Database: server.URL + "/vdb/"}), vngcloud.WithRetry(0, time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	_, err = New(cfg).ListDatabaseZones(context.Background(), nil)
	var api *vngcloud.APIError
	if !errors.As(err, &api) || api.Code != "InvalidResponse" || api.StatusCode != http.StatusOK {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestMissingCatalogFields(t *testing.T) {
	cases := []struct {
		body string
		read func(*Client) error
	}{
		{`{"code":200,"data":[{"type":"mysql","version":null}]}`, func(c *Client) error { _, e := c.ListDatabaseEngineVersions(context.Background(), nil); return e }},
		{`{"code":200,"data":[{"uuid":"zone"}]}`, func(c *Client) error { _, e := c.ListDatabaseZones(context.Background(), nil); return e }},
		{`{"code":200,"data":[{"name":"small"}]}`, func(c *Client) error {
			_, e := c.ListDatabaseFlavors(context.Background(), &ListDatabaseFlavorsInput{Engine: "mysql", EngineVersion: "8.0", ZoneID: "zone"})
			return e
		}},
		{`{"code":200,"data":{"data":[{"type":"SSD"}]}}`, func(c *Client) error {
			_, e := c.ListDatabaseStorageTypes(context.Background(), &ListDatabaseStorageTypesInput{ZoneID: "zone"})
			return e
		}},
		{`{"code":200,"data":[{"uuid":"network","subnets":null}]}`, func(c *Client) error {
			_, e := c.ListDatabaseNetworks(context.Background(), &ListDatabaseNetworksInput{ZoneID: "zone"})
			return e
		}},
	}
	for i, tc := range cases {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, tc.body) }))
			defer server.Close()
			cfg, err := vngcloud.NewConfig(vngcloud.WithRegion("hcm-3"), vngcloud.WithStaticToken("test-token"), vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{Database: server.URL}), vngcloud.WithRetry(0, time.Millisecond))
			if err != nil {
				t.Fatal(err)
			}
			if err = tc.read(New(cfg)); err == nil {
				t.Fatal("invalid catalog accepted")
			}
		})
	}
}

func TestUnsafeDetailResponses(t *testing.T) {
	for _, body := range []string{
		`{"code":200,"data":{"ID":"instance"}}`,
		`{"code":200,"data":{"id":"instance","ID":"instance"}}`,
		`{"code":200,"data":{"id":"other"}}`,
		`{"code":200,"data":{"id":"instance","backupAuto":"false"}}`,
		`{"code":200,"data":{"id":"instance"}}`,
		`{"code":200,"data":{"id":"instance","name":null,"status":"ACTIVE"}}`,
		`{"code":200,"data":{"id":"instance","name":"test-token","status":"ACTIVE"}}`,
	} {
		t.Run(body, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, body) }))
			defer server.Close()
			cfg, err := vngcloud.NewConfig(vngcloud.WithRegion("hcm-3"), vngcloud.WithStaticToken("test-token"), vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{Database: server.URL}), vngcloud.WithRetry(0, time.Millisecond))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := New(cfg).GetDatabaseInstance(context.Background(), &GetDatabaseInstanceInput{ID: "instance"}); err == nil || strings.Contains(err.Error(), body) {
				t.Fatalf("unsafe error %v", err)
			}
		})
	}
}

func TestOversizedResponseIsRejected(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"code":200,"data":{"id":"instance","name":"`+strings.Repeat("x", 32<<20)+`","status":"ACTIVE"}}`)
	}))
	defer server.Close()
	cfg, err := vngcloud.NewConfig(vngcloud.WithRegion("hcm-3"), vngcloud.WithStaticToken("test-token"), vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{Database: server.URL}), vngcloud.WithRetry(0, time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := New(cfg).GetDatabaseInstance(context.Background(), &GetDatabaseInstanceInput{ID: "instance"}); err == nil {
		t.Fatal("oversized response accepted")
	}
}

func TestDetailFixture(t *testing.T) {
	body, err := os.ReadFile("../testdata/database/instance.json")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(body) }))
	defer server.Close()
	cfg, err := vngcloud.NewConfig(vngcloud.WithRegion("hcm-3"), vngcloud.WithStaticToken("test-token"), vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{Database: server.URL}), vngcloud.WithRetry(0, time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	out, err := New(cfg).GetDatabaseInstance(context.Background(), &GetDatabaseInstanceInput{ID: "instance"})
	if err != nil || out.Instance.DatastoreVersion != "8.0" || out.Instance.BackupAuto == nil || *out.Instance.BackupAuto {
		t.Fatalf("output %#v, error %v", out, err)
	}
}

func TestReadRoutes(t *testing.T) {
	cases := []struct {
		path, query, body string
		read              func(*Client) error
	}{
		{"/vdb-relational/v1/database-instances/datastore", "", `{"code":200,"data":[{"type":"mysql","version":"8.0"}]}`, func(c *Client) error { _, err := c.ListDatabaseEngineVersions(context.Background(), nil); return err }},
		{"/vdb-relational/v1/database-instances/zones", "", `{"code":200,"data":[{"uuid":"zone","name":"zone","status":"ACTIVE"}]}`, func(c *Client) error { _, err := c.ListDatabaseZones(context.Background(), nil); return err }},
		{"/vdb-relational/v1/database-instances/flavors", "type=mysql&version=8.0&zoneId=zone", `{"code":200,"data":[{"id":1,"name":"small"}]}`, func(c *Client) error {
			_, err := c.ListDatabaseFlavors(context.Background(), &ListDatabaseFlavorsInput{Engine: "mysql", EngineVersion: "8.0", ZoneID: "zone"})
			return err
		}},
		{"/vdb-relational/v1/database-instances/volume/types", "zoneId=zone", `{"code":200,"data":{"projectId":"project","data":[{"type":"SSD","minVolumeSize":10,"maxVolumeSize":100}]}}`, func(c *Client) error {
			_, err := c.ListDatabaseStorageTypes(context.Background(), &ListDatabaseStorageTypesInput{ZoneID: "zone"})
			return err
		}},
		{"/vdb-relational/v1/database-instances/networks/subnets", "zoneId=zone", `{"code":200,"data":[{"uuid":"network","zoneId":"zone","subnets":[{"uuid":"subnet","networkUuid":"network","zoneId":"zone"}]}]}`, func(c *Client) error {
			_, err := c.ListDatabaseNetworks(context.Background(), &ListDatabaseNetworksInput{ZoneID: "zone"})
			return err
		}},
		{"/vdb-relational/v1/database-instances", "pageNumber=1&pageSize=100", `{"code":200,"data":{"projectId":"project","data":[],"pageObject":{"number":1,"size":100,"totalPages":0,"totalElements":0}}}`, func(c *Client) error { _, err := c.ListDatabaseInstances(context.Background(), nil); return err }},
		{"/vdb-relational/v1/database-instances/id/instance", "", `{"code":200,"data":{"id":"instance","name":"db","status":"ACTIVE"}}`, func(c *Client) error {
			_, err := c.GetDatabaseInstance(context.Background(), &GetDatabaseInstanceInput{ID: "instance"})
			return err
		}},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || r.URL.Path != tc.path || r.URL.RawQuery != tc.query || r.Header.Get("Authorization") != "Bearer test-token" {
					t.Errorf("unexpected request: %s %s?%s", r.Method, r.URL.Path, r.URL.RawQuery)
				}
				_, _ = io.WriteString(w, tc.body)
			}))
			defer server.Close()
			cfg, err := vngcloud.NewConfig(vngcloud.WithRegion("hcm-3"), vngcloud.WithStaticToken("test-token"), vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{Database: server.URL}), vngcloud.WithRetry(0, time.Millisecond))
			if err != nil {
				t.Fatal(err)
			}
			if err := tc.read(New(cfg)); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestFlavorCarriesPricingAndShape(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"code":200,"data":[{"id":211,"name":"small","flavorId":"f-1","vcpus":1,"ram":2,"packageSku":"sku","locateZoneId":"zone","priceKey":"pk-1","monthlyCost":123456.5,"volumeSize":20,"volumeType":"SSD","backupSize":10,"description":"d","familyType":"general","platformType":"x86","bandwidth":100,"bandwidthUnit":"Mbps","zoneUUID":"zone","zoneId":3}]}`)
	}))
	defer server.Close()
	cfg, err := vngcloud.NewConfig(vngcloud.WithRegion("hcm-3"), vngcloud.WithStaticToken("test-token"), vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{Database: server.URL}), vngcloud.WithRetry(0, time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	out, err := New(cfg).ListDatabaseFlavors(context.Background(), &ListDatabaseFlavorsInput{Engine: "MySQL", EngineVersion: "8.0", ZoneID: "zone"})
	if err != nil || len(out.Items) != 1 {
		t.Fatalf("out=%v err=%v", out, err)
	}
	f := out.Items[0]
	if f.MonthlyCost != 123456.5 || f.PriceKey != "pk-1" || f.VolumeSize != 20 || f.VolumeType != "SSD" || f.FamilyType != "general" || f.BackupSize != 10 || f.PlatformType != "x86" || f.Bandwidth != 100 || f.BandwidthUnit != "Mbps" || f.ZoneUUID != "zone" || f.Description != "d" {
		t.Fatalf("flavor %#v", f)
	}
}
