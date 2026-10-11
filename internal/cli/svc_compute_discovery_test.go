package cli

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"danny.vn/vngcloud/internal/discovery"
	"danny.vn/vngcloud/internal/testutil"
	"danny.vn/vngcloud/pricing"
)

func computeDiscoveryCLIContext() context.Context {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	return discovery.WithTestClock(context.Background(), func() time.Time { return now }, func(ctx context.Context, d time.Duration) error { now = now.Add(d); return ctx.Err() })
}
func computeDiscoveryRoutes(t *testing.T, status int) map[string]func(http.ResponseWriter, *http.Request) {
	t.Helper()
	routes := map[string]func(http.ResponseWriter, *http.Request){"/v1/price": func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		if status == 200 {
			testutil.WriteFixture(t, w, "../../testdata/compute/discovery_quote_server_s2.json")
		} else {
			_, _ = w.Write([]byte(`{"code":"Failure"}`))
		}
	}}
	for path, file := range map[string]string{
		"/v1/proj-1/zones":                                              "portal/discovery_zones.json",
		"/v1/proj-1/flavor_zones/codes":                                 "compute/discovery_platform_codes.json",
		"/v1/proj-1/flavor_zones/families":                              "compute/discovery_families.json",
		"/v1/proj-1/flavors/families/general-purpose/platforms/code-s2": "compute/discovery_family_flavors.json",
		"/v1/proj-1/images/os":                                          "compute/discovery_os_images.json",
		"/v1/proj-1/volume_type_zones":                                  "volume/list_volume_type_zones.json",
		"/v1/proj-1/volume-zone-1/volume_types":                         "volume/discovery_types.json",
	} {
		routes[path] = func(w http.ResponseWriter, _ *http.Request) { testutil.WriteFixture(t, w, "../../testdata/"+file) }
	}
	return routes
}
func TestComputeDiscoveryCLIReadOnlyAndPartialQuery(t *testing.T) {
	for _, status := range []int{200, 500} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			fixture := newSvcFixture(computeDiscoveryRoutes(t, status))
			root, stdout, stderr := newSvcRoot(t, fixture)
			root.SetArgs([]string{"--region", "hcm-3", "--project-id", "proj-1", "--read-only", "--output", "json", "--query", "Items[].RAMUnit", "compute", "list-flavor-prices", "--zone-id", "HCM03-1C", "--family", "general-purpose", "--platform", "code-s2", "--name", "s2-general-1x2", "--allow-partial"})
			err := root.ExecuteContext(computeDiscoveryCLIContext())
			if status == 200 {
				if err != nil {
					t.Fatal(err)
				}
			} else if !errors.Is(err, pricing.ErrDiscoveryIncomplete) || exitCode(err) != 1 {
				t.Fatalf("error %v stderr %s", err, stderr)
			}
			if !strings.Contains(stdout.String(), "unknown") {
				t.Fatalf("stdout %s", stdout)
			}
		})
	}
}
func TestComputeDiscoveryCLIFlagsBeforeRequests(t *testing.T) {
	for _, flags := range [][]string{{"--sort-by", "iops"}, {"--periods", "1"}, {"--min-ram", "-1"}, {"--max-requests", "2001"}} {
		fixture := newSvcFixture(nil)
		root, _, _ := newSvcRoot(t, fixture)
		root.SetArgs(append([]string{"--region", "hcm-3", "--project-id", "proj-1", "compute", "list-flavor-prices"}, flags...))
		err := root.ExecuteContext(computeDiscoveryCLIContext())
		if err == nil || exitCode(err) != 2 || fixture.requestCount() != 0 {
			t.Fatalf("error %v requests %d", err, fixture.requestCount())
		}
	}
}
func TestComputeDiscoveryCLIReflectionFormatsAndQuery(t *testing.T) {
	for _, format := range []string{"json", "table", "text"} {
		for _, query := range []string{"", "Items[].Price"} {
			routes := computeDiscoveryRoutes(t, 200)
			routes["/v1/price"] = jsonHandler(200, `{"optimumPrice":1,"propertiesPrice":[{"name":"test\u002dtoken"}]}`)
			fixture := newSvcFixture(routes)
			root, stdout, stderr := newSvcRoot(t, fixture)
			root.SetArgs([]string{"--region", "hcm-3", "--project-id", "proj-1", "--output", format, "--query", query, "compute", "list-flavor-prices", "--allow-partial", "--zone-id", "HCM03-1C", "--family", "general-purpose", "--platform", "code-s2", "--name", "s2-general-1x2"})
			err := root.ExecuteContext(computeDiscoveryCLIContext())
			if err == nil || exitCode(err) != 1 {
				t.Fatalf("error %v", err)
			}
			printError(stderr, err)
			if strings.Contains(stdout.String()+stderr.String(), "test-token") {
				t.Fatal("credential escaped CLI")
			}
		}
	}
}
