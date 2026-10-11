package cli

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"danny.vn/vngcloud/internal/testutil"
)

func storageDiscoveryRoutes(t *testing.T) map[string]func(http.ResponseWriter, *http.Request) {
	t.Helper()
	fixture := func(file string) func(http.ResponseWriter, *http.Request) {
		return func(w http.ResponseWriter, _ *http.Request) {
			testutil.WriteFixture(t, w, "../../testdata/storage/"+file)
		}
	}
	return map[string]func(http.ResponseWriter, *http.Request){
		"/internal/v1/regions":                fixture("discovery_regions.json"),
		"/internal/v1/billing/project_types":  fixture("discovery_project_types.json"),
		"/internal/v1/billing/purchase_types": fixture("discovery_purchase_types.json"),
		"/billing-api/v1/configurations": func(w http.ResponseWriter, r *http.Request) {
			file := map[string]string{"vos_billing_normal_min_quota": "discovery_config_min_quota.json", "vos_billing_normal_max_quota": "discovery_config_max_quota.json", "max_project_per_user_per_region": "discovery_config_max_projects.json", "enable_iam_checkout": "discovery_config_iam_checkout.json"}[r.URL.Query().Get("keys")]
			testutil.WriteFixture(t, w, "../../testdata/storage/"+file)
		},
		"/billing-api/v2/price": fixture("discovery_quote_gold.json"),
	}
}

func TestStorageDiscoveryCLIReadOnlyAndRegion(t *testing.T) {
	fixture := newSvcFixture(storageDiscoveryRoutes(t))
	root, stdout, _ := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "--read-only", "storage", "list-storage-prices", "--cli-input-json", `{"Region":"HAN02","Type":"Gold"}`})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), `"Region": "HAN02"`) {
		t.Fatalf("output %s", stdout)
	}
}

func TestStorageDiscoveryCLIRejectsIrrelevantSortBeforeRequests(t *testing.T) {
	fixture := newSvcFixture(nil)
	root, _, _ := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "storage", "list-storage-prices", "--sort-by", "ram"})
	err := root.ExecuteContext(context.Background())
	if err == nil || exitCode(err) != 2 || fixture.requestCount() != 0 {
		t.Fatalf("error %v requests %d", err, fixture.requestCount())
	}
}

func TestStorageDiscoveryCLIReflectionFormatsAndQuery(t *testing.T) {
	for _, format := range []string{"json", "table", "text"} {
		for _, query := range []string{"", "Items[].Price"} {
			routes := storageDiscoveryRoutes(t)
			routes["/billing-api/v2/price"] = jsonHandler(200, `{"success":true,"data":{"optimumPrice":1,"propertiesPrice":[{"name":"test\u002dtoken"}]}}`)
			fixture := newSvcFixture(routes)
			root, stdout, stderr := newSvcRoot(t, fixture)
			root.SetArgs([]string{"--region", "hcm-3", "--output", format, "--query", query, "storage", "list-storage-prices", "--allow-partial", "--type", "Gold"})
			err := root.ExecuteContext(context.Background())
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
