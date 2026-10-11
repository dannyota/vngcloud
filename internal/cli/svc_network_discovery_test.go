package cli

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"danny.vn/vngcloud/internal/testutil"
	"danny.vn/vngcloud/pricing"
)

func TestNetworkDiscoveryCLIFlagsBeforeRequests(t *testing.T) {
	for _, flags := range [][]string{{"--sort-by", "unit-price"}, {"--sort-by", "ram"}, {"--sort-by", "iops"}, {"--periods", "1"}, {"--kind", "public"}, {"--max-requests", "2001"}} {
		fixture := newSvcFixture(nil)
		root, _, _ := newSvcRoot(t, fixture)
		root.SetArgs(append([]string{"--region", "hcm-3", "--project-id", "proj-1", "network", "list-network-prices"}, flags...))
		err := root.ExecuteContext(computeDiscoveryCLIContext())
		if err == nil || exitCode(err) != 2 || fixture.requestCount() != 0 {
			t.Fatalf("error %v requests %d", err, fixture.requestCount())
		}
	}
}

func TestNetworkDiscoveryCLIReadOnlyAndPartialQuery(t *testing.T) {
	for _, partial := range []bool{false, true} {
		calls := 0
		fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){"/v1/price": func(w http.ResponseWriter, _ *http.Request) {
			calls++
			if partial && calls == 2 {
				w.WriteHeader(500)
				_, _ = w.Write([]byte(`{"code":"Failure"}`))
				return
			}
			testutil.WriteFixture(t, w, "../../testdata/network/discovery_quote_public.json")
		}})
		root, stdout, stderr := newSvcRoot(t, fixture)
		root.SetArgs([]string{"--region", "hcm-3", "--project-id", "proj-1", "--read-only", "--output", "json", "--query", "Items[].Unit", "network", "list-network-prices", "--allow-partial"})
		err := root.ExecuteContext(computeDiscoveryCLIContext())
		if partial {
			if !errors.Is(err, pricing.ErrDiscoveryIncomplete) || exitCode(err) != 1 {
				t.Fatalf("error %v stderr %s", err, stderr)
			}
		} else if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(stdout.String(), "IP-month") || calls != 3 {
			t.Fatalf("output %s calls %d", stdout, calls)
		}
	}
}

func TestNetworkDiscoveryCLIReflectionFormatsAndQuery(t *testing.T) {
	for _, format := range []string{"json", "table", "text"} {
		for _, query := range []string{"", "Items[].Price"} {
			for _, partial := range []bool{false, true} {
				calls := 0
				fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){"/v1/price": func(w http.ResponseWriter, _ *http.Request) {
					calls++
					if calls == 2 {
						_, _ = w.Write([]byte(`{"optimumPrice":1,"propertiesPrice":[{"name":"test\u002dtoken"}]}`))
						return
					}
					testutil.WriteFixture(t, w, "../../testdata/network/discovery_quote_public.json")
				}})
				root, stdout, stderr := newSvcRoot(t, fixture)
				args := []string{"--region", "hcm-3", "--project-id", "proj-1", "--output", format, "--query", query, "network", "list-network-prices"}
				if partial {
					args = append(args, "--allow-partial")
				}
				root.SetArgs(args)
				err := root.ExecuteContext(computeDiscoveryCLIContext())
				if err == nil || exitCode(err) != 1 {
					t.Fatalf("error %v", err)
				}
				printError(stderr, err)
				if strings.Contains(stdout.String()+stderr.String(), "test-token") {
					t.Fatal("credential escaped CLI")
				}
				if partial && stdout.Len() == 0 {
					t.Fatal("missing partial output")
				}
				if !partial && stdout.Len() != 0 {
					t.Fatal("fail-fast output")
				}
			}
		}
	}
}

func TestNetworkDiscoveryCLIExitCodes(t *testing.T) {
	for _, tc := range []struct{ status, exit int }{{400, 1}, {401, 3}, {403, 1}, {404, 4}, {500, 1}} {
		fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){"/v1/price": jsonHandler(tc.status, `{"code":"failure"}`)})
		root, stdout, _ := newSvcRoot(t, fixture)
		root.SetArgs([]string{"--region", "hcm-3", "--project-id", "proj-1", "network", "list-network-prices"})
		err := root.ExecuteContext(computeDiscoveryCLIContext())
		if err == nil || exitCode(err) != tc.exit || stdout.Len() != 0 {
			t.Fatalf("status %d error %v exit %d output %s", tc.status, err, exitCode(err), stdout)
		}
	}
}
