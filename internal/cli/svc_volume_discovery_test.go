package cli

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"danny.vn/vngcloud/internal/testutil"
	"danny.vn/vngcloud/pricing"
)

func volumeDiscoveryRoutes(t *testing.T, status int) map[string]func(http.ResponseWriter, *http.Request) {
	t.Helper()
	return map[string]func(http.ResponseWriter, *http.Request){
		"/v1/proj-1/zones":             jsonHandler(200, `{"data":[{"uuid":"zone-1","name":"Zone","isEnabled":true}]}`),
		"/v1/proj-1/volume_type_zones": jsonHandler(200, `{"volumeTypeZones":[{"id":"fz-82","zone":{"uuid":"zone-1"}}]}`),
		"/v1/proj-1/fz-82/volume_types": func(w http.ResponseWriter, _ *http.Request) {
			testutil.WriteFixture(t, w, "../../testdata/volume/discovery_types.json")
		},
		"/v1/proj-1/volumes/encryption_types": jsonHandler(200, `[]`),
		"/v1/price": func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(status)
			if status == 200 {
				testutil.WriteFixture(t, w, "../../testdata/volume/discovery_quote_plain.json")
			} else {
				_, _ = w.Write([]byte(`{"code":"Failure"}`))
			}
		},
	}
}

func TestVolumeDiscoveryCLIReadOnlyAndPartialQuery(t *testing.T) {
	for _, status := range []int{200, 500} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			fixture := newSvcFixture(volumeDiscoveryRoutes(t, status))
			root, stdout, stderr := newSvcRoot(t, fixture)
			root.SetArgs([]string{"--region", "hcm-3", "--project-id", "proj-1", "--read-only", "--output", "json", "--query", "Items[].SizeGB", "volume", "list-volume-prices", "--encryption", "none", "--iops", "3000", "--allow-partial"})
			err := root.ExecuteContext(context.Background())
			if status == 200 {
				if err != nil {
					t.Fatal(err)
				}
			} else if !errors.Is(err, pricing.ErrDiscoveryIncomplete) || exitCode(err) != 1 {
				t.Fatalf("error %v stderr %s", err, stderr)
			}
			if !strings.Contains(stdout.String(), "1") {
				t.Fatalf("missing output: %s", stdout)
			}
		})
	}
}

func TestVolumeDiscoveryCLIFlagsBeforeRequests(t *testing.T) {
	for _, flags := range [][]string{{"--sort-by", "ram"}, {"--periods", "1"}, {"--size", "-1"}} {
		fixture := newSvcFixture(nil)
		root, _, _ := newSvcRoot(t, fixture)
		root.SetArgs(append([]string{"--region", "hcm-3", "--project-id", "proj-1", "volume", "list-volume-prices"}, flags...))
		err := root.ExecuteContext(context.Background())
		if err == nil || exitCode(err) != 2 || fixture.requestCount() != 0 {
			t.Fatalf("error %v requests %d", err, fixture.requestCount())
		}
	}
}

func TestVolumeDiscoveryCLISortFlags(t *testing.T) {
	root := newRootCmd(strings.NewReader(""), nil, nil)
	cmd, _, err := root.Find([]string{"volume", "list-volume-prices"})
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Flags().Lookup("sort-by") == nil || cmd.Flags().Lookup("descending") == nil {
		t.Fatal("missing presentation flags")
	}
}

func TestVolumeDiscoveryCLIReflectionFormatsAndQuery(t *testing.T) {
	for _, format := range []string{"json", "table", "text"} {
		for _, query := range []string{"", "Items[].Price"} {
			routes := volumeDiscoveryRoutes(t, 200)
			routes["/v1/price"] = jsonHandler(200, `{"optimumPrice":1,"propertiesPrice":[{"name":"test\u002dtoken"}]}`)
			fixture := newSvcFixture(routes)
			root, stdout, stderr := newSvcRoot(t, fixture)
			root.SetArgs([]string{"--region", "hcm-3", "--project-id", "proj-1", "--output", format, "--query", query, "volume", "list-volume-prices", "--allow-partial", "--encryption", "none", "--iops", "3000"})
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
