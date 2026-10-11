package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/network"
)

const natCLIQuotePrice = 712400

func natCLIFixture(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile("../../testdata/network/" + name + ".json")
	if err != nil {
		t.Fatal(err)
	}
	return strings.NewReplacer("66b500000000000000000001", "zone-1", "pro-00000000-0000-0000-0000-000000000001", "proj-1", "az-1", "HAN01-1B", "example", "edge").Replace(string(raw))
}

func TestNetworkNATCatalogReads(t *testing.T) {
	for _, name := range []string{"list-nat-zones", "list-nat-packages"} {
		for _, format := range []string{"json", "table", "text"} {
			for _, discover := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/discover=%t", name, format, discover), func(t *testing.T) {
					calls := 0
					body := natCLIFixture(t, strings.ReplaceAll(name, "-", "_"))
					run := natCLI(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						calls++
						if r.Method != http.MethodGet {
							t.Errorf("method=%s", r.Method)
						}
						if r.URL.Path == "/vnetwork/v1/regions" {
							jsonHandler(200, `{"success":true,"data":[{"uuid":"zone-1","vnetworkDashboard":"https://han-1-vnetwork.console.greennode.ai"}]}`)(w, r)
							return
						}
						suffix := "zones"
						if name == "list-nat-packages" {
							suffix = "nat-package"
							if r.URL.Query().Get("zoneUuid") != "HAN01-1B" {
								t.Error("wrong availability zone")
							}
						}
						if r.URL.Path != "/vnetwork/v1/zone-1/proj-1/nats/"+suffix || r.URL.Query().Get("params") != `{"search":[],"sort":{},"page":1,"size":1000}` {
							t.Errorf("request=%s", r.URL.RequestURI())
						}
						jsonHandler(200, body)(w, r)
					}))
					args := []string{"--profile", "agent", "--region", "han-1", "network", name, "--output", format}
					if !discover {
						args = append(args, "--zone-id", "zone-1")
					}
					if name == "list-nat-packages" {
						args = append(args, "--availability-zone-id", "HAN01-1B")
					}
					code, out, stderr := run(args...)
					wantCalls := 1
					if discover {
						wantCalls++
					}
					if code != 0 || calls != wantCalls {
						t.Fatalf("exit=%d calls=%d stderr=%s", code, calls, stderr)
					}
					id := "HAN01-1B"
					if name == "list-nat-packages" {
						id = "package-1"
					}
					if !strings.Contains(out, id) {
						t.Errorf("output=%s", out)
					}
					if format == "json" {
						var result struct{ Items []map[string]any }
						if json.Unmarshal([]byte(out), &result) != nil || len(result.Items) != 1 || result.Items[0]["UUID"] != id {
							t.Errorf("output=%s", out)
						}
					}
					if strings.Contains(out+stderr, "catalog-secret-canary") {
						t.Error("catalog leaked excluded fields")
					}
				})
			}
		}
	}
}

type natCLIState struct {
	mu                                sync.Mutex
	orders, deletes, prices, renewals int
	requests                          int
	unpriced                          bool
	ordered, manual                   bool
	whitelist                         bool
	orderStatus                       int
	status                            string
}

func natCLIRoutes(t *testing.T, s *natCLIState) http.Handler {
	t.Helper()
	zones := natCLIFixture(t, "list_nat_zones")
	packages := natCLIFixture(t, "list_nat_packages")
	vpcs := natCLIFixture(t, "nat_vpc_picker")
	inventory := natCLIFixture(t, "nat_write_inventory")
	price := natCLIFixture(t, "quote_create_nat")
	if s.unpriced {
		var envelope map[string]json.RawMessage
		if err := json.Unmarshal([]byte(price), &envelope); err != nil {
			t.Fatal(err)
		}
		var data map[string]json.RawMessage
		if err := json.Unmarshal(envelope["data"], &data); err != nil {
			t.Fatal(err)
		}
		data["optimumPrice"] = json.RawMessage("0")
		raw, err := json.Marshal(data)
		if err != nil {
			t.Fatal(err)
		}
		envelope["data"] = raw
		raw, err = json.Marshal(envelope)
		if err != nil {
			t.Fatal(err)
		}
		price = string(raw)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.requests++
		var body string
		switch {
		case strings.HasSuffix(r.URL.Path, "/nats/v3-whitelist"):
			body = fmt.Sprintf(`{"success":true,"data":{"enabledForAll":%t}}`, s.whitelist)
		case strings.HasSuffix(r.URL.Path, "/nats/zones"):
			body = zones
		case strings.HasSuffix(r.URL.Path, "/nats/nat-package"):
			if r.URL.Query().Get("zoneUuid") != "HAN01-1B" {
				t.Error("wrong package zone")
			}
			body = packages
		case strings.HasSuffix(r.URL.Path, "/nats/vpcs"):
			body = vpcs
		case strings.HasSuffix(r.URL.Path, "/price"):
			s.prices++
			if r.URL.Path != "/vnetwork/billing/v1/zone-1/proj-1/price" || r.Method != http.MethodPost {
				t.Errorf("price request=%s %s", r.Method, r.URL.Path)
			}
			var got struct {
				ResourceInfo struct{ NATName, PackageUUID, VPCUUID, RegionUUID, ProjectUUID string }
			}
			if json.NewDecoder(r.Body).Decode(&got) != nil || got.ResourceInfo.NATName != "edge" || got.ResourceInfo.PackageUUID != "package-1" || got.ResourceInfo.VPCUUID != "vpc-1" || got.ResourceInfo.RegionUUID != "zone-1" || got.ResourceInfo.ProjectUUID != "proj-1" {
				t.Errorf("price input=%+v", got)
			}
			body = price

		case r.URL.Path == "/gateway/api/v1/home/user-info":
			body = `{"code":200,"data":{"userId":123456}}`
		case r.URL.Path == "/gateway/api/v1/resources/autoRenew":
			s.renewals++
			s.manual = true
			if r.Method != http.MethodPut {
				t.Errorf("renewal method=%s", r.Method)
			}
			body = `{"code":200,"data":{"successAll":true,"errorAutoRenewResources":[]}}`
		case r.URL.Path == "/gateway/api/v1/resources":
			renewal := `"renewType":"AUTO-RENEW","renewPeriod":1`
			if s.manual {
				renewal = `"renewType":"MANUAL","renewPeriod":null`
			}
			body = `{"code":200,"data":{"data":[{"product":"vserver","artifactType":"nat","artifactId":"nat-1","billingType":"PREPAID","channel":7,"status":"active","billingElements":[{"sku":"nat.s-standard","quantity":1}],` + renewal + `}]}}`
		case strings.HasSuffix(r.URL.Path, "/nats/nat-1"):
			s.deletes++
			s.ordered = false
			if r.Method != http.MethodDelete || r.URL.Path != "/vnetwork/v1/zone-1/proj-1/nats/nat-1" {
				t.Errorf("delete=%s %s", r.Method, r.URL.Path)
			}
			body = `{"code":0,"success":true}`
		case r.URL.Path == "/vnetwork/v1/zone-1/proj-1/nats":
			switch {
			case r.Method == http.MethodPost:
				s.orders++
				s.ordered = true
				var got struct {
					ResourceInfo struct{ NATName, PackageUUID, VPCUUID string }
				}
				if json.NewDecoder(r.Body).Decode(&got) != nil || got.ResourceInfo.NATName != "edge" || got.ResourceInfo.PackageUUID != "package-1" || got.ResourceInfo.VPCUUID != "vpc-1" {
					t.Errorf("create input=%+v", got)
				}
				status := 201
				if s.orderStatus != 0 {
					status = s.orderStatus
				}
				jsonHandler(status, `{"code":0,"success":true}`)(w, r)
				return
			case s.ordered:
				body = inventory
				if s.status != "" {
					body = strings.Replace(body, "ACTIVE", s.status, 1)
				}
			default:
				body = `{"success":true,"page":1,"size":1000,"totalPage":0,"total":0,"data":[]}`
			}
		default:
			t.Errorf("unexpected request=%s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
			return
		}
		jsonHandler(200, body)(w, r)
	})
}

func natIAMCLI(t *testing.T, handler http.Handler) func(...string) (int, string, string) {
	t.Helper()
	withCleanEnv(t)
	token := httptest.NewServer(http.HandlerFunc(jsonHandler(200, `{"accessToken":"synthetic-token","expiresIn":3600}`)))
	t.Cleanup(token.Close)
	signin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			http.Redirect(w, r, token.URL+"/callback?code=synthetic-code", http.StatusFound)
		} else {
			_, _ = w.Write([]byte(`<input name="_csrf" value="synthetic-csrf">`))
		}
	}))
	t.Cleanup(signin.Close)
	opts := newFakeServer(t, handler)
	withTestOptions(t, append(opts, vngcloud.WithIAMUser(&vngcloud.IAMUserAuth{RootEmail: "root@example.test", Username: "user", Password: "synthetic", SigninBaseURL: signin.URL, TokenURL: token.URL, DashboardURI: token.URL + "/"}), vngcloud.WithRetry(0, 0))...)
	return func(args ...string) (int, string, string) {
		var stdout, stderr bytes.Buffer
		args = append([]string{"--region", "han-1", "--project-id", "proj-1"}, args...)
		code := Main(context.Background(), args, strings.NewReader(""), &stdout, &stderr)
		return code, stdout.String(), stderr.String()
	}
}

func TestNetworkNATSDKCreateAndDelete(t *testing.T) {
	for _, format := range []string{"json", "table", "text"} {
		t.Run(format, func(t *testing.T) {
			state := &natCLIState{whitelist: true}
			run := natIAMCLI(t, natCLIRoutes(t, state))
			code, out, stderr := run(append(natCreateArgs("create-nat-instance"), "--yes", "--max-price", strconv.Itoa(natCLIQuotePrice), "--output", format)...)
			if code != 0 {
				t.Fatalf("exit=%d stderr=%s", code, stderr)
			}
			for _, value := range []string{"nat-1", "edge", strconv.Itoa(natCLIQuotePrice), "VND"} {
				if !strings.Contains(out, value) {
					t.Errorf("output lacks %s: %s", value, out)
				}
			}
			if format == "json" {
				var result network.CreateNATInstanceOutput
				if json.Unmarshal([]byte(out), &result) != nil || result.AutoRenew == nil || *result.AutoRenew || result.TotalPrice != natCLIQuotePrice || result.NATInstance == nil || result.NATInstance.UUID != "nat-1" {
					t.Errorf("create output=%s", out)
				}
			}
			state.mu.Lock()
			orders, renewals, prices := state.orders, state.renewals, state.prices
			state.mu.Unlock()
			if orders != 1 || renewals != 1 || prices != 1 {
				t.Errorf("orders=%d renewals=%d prices=%d", orders, renewals, prices)
			}
			code, out, stderr = run("network", "delete-nat-instance", "--zone-id", "zone-1", "--vpc-id", "vpc-1", "--nat-id", "nat-1", "--yes", "--no-wait")
			if code != 0 {
				t.Fatalf("delete exit=%d stdout=%s stderr=%s", code, out, stderr)
			}
			state.mu.Lock()
			deletes := state.deletes
			state.mu.Unlock()
			if deletes != 1 {
				t.Errorf("deletes=%d", deletes)
			}
		})
	}
}

func TestNetworkNATSDKQuoteReadOnly(t *testing.T) {
	state := &natCLIState{whitelist: true}
	run := natCLI(t, natCLIRoutes(t, state))
	args := append(natCreateArgs("quote-create-nat-instance"), "--region", "han-1", "--profile", "agent")
	code, out, stderr := run(args...)
	if code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, stderr)
	}
	var result network.NetworkQuoteOutput
	if json.Unmarshal([]byte(out), &result) != nil || result.TotalPrice != natCLIQuotePrice || result.Currency != "VND" {
		t.Errorf("quote=%s", out)
	}
	if len(result.Properties) != 1 || result.Properties[0].CurrentPrice != nil {
		t.Errorf("quote properties = %+v, want null CurrentPrice", result.Properties)
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.prices != 1 || state.orders != 0 || state.renewals != 0 {
		t.Errorf("prices=%d orders=%d renewals=%d", state.prices, state.orders, state.renewals)
	}
}

func TestNetworkNATSDKInvalidConfig(t *testing.T) {
	for _, mode := range []string{"auth", "region", "v3"} {
		t.Run(mode, func(t *testing.T) {
			state := &natCLIState{whitelist: mode != "v3"}
			args := append(natCreateArgs("create-nat-instance"), "--yes", "--max-price", strconv.Itoa(natCLIQuotePrice))
			var run func(...string) (int, string, string)
			if mode == "auth" {
				run = natCLI(t, natCLIRoutes(t, state))
				args = append(args, "--region", "han-1")
			} else {
				run = natIAMCLI(t, natCLIRoutes(t, state))
			}
			if mode == "region" {
				args = append(args, "--region", "hcm-3")
			}
			code, out, stderr := run(args...)
			if code != 2 || out != "" || !strings.Contains(stderr, "InvalidConfig") {
				t.Fatalf("exit=%d stdout=%s stderr=%s", code, out, stderr)
			}
			state.mu.Lock()
			defer state.mu.Unlock()
			if (mode != "v3" && state.requests != 0) || state.prices != 0 || state.orders != 0 || state.renewals != 0 {
				t.Errorf("prices=%d orders=%d renewals=%d", state.prices, state.orders, state.renewals)
			}
		})
	}
}

func TestNetworkNATSDKCreateErrors(t *testing.T) {
	for _, tc := range []struct {
		name, code string
		cap        string
		state      *natCLIState
		partial    bool
		orders     int
	}{
		{name: "zero cap", code: "PriceAboveMax", cap: "0", state: &natCLIState{whitelist: true}},
		{name: "below quote", code: "PriceAboveMax", cap: strconv.Itoa(natCLIQuotePrice - 1), state: &natCLIState{whitelist: true}},
		{name: "unpriced", code: "Unpriced", cap: strconv.Itoa(natCLIQuotePrice), state: &natCLIState{whitelist: true, unpriced: true}},
		{name: "unsettled acceptance", code: "NotSettled", cap: strconv.Itoa(natCLIQuotePrice), state: &natCLIState{whitelist: true, orderStatus: 200}, partial: true, orders: 1},
		{name: "terminal error", code: "WriteFailed", cap: strconv.Itoa(natCLIQuotePrice), state: &natCLIState{whitelist: true, status: "ERROR"}, partial: true, orders: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := tc.state
			run := natIAMCLI(t, natCLIRoutes(t, state))
			code, out, stderr := run(append(natCreateArgs("create-nat-instance"), "--yes", "--max-price", tc.cap, "--query", "OrderID")...)
			if code != 1 || !strings.Contains(stderr, `"code":"`+tc.code+`"`) {
				t.Fatalf("exit=%d stdout=%s stderr=%s", code, out, stderr)
			}
			if tc.partial {
				var result network.CreateNATInstanceOutput
				if json.Unmarshal([]byte(out), &result) != nil || result.NATInstance == nil || result.NATInstance.UUID != "nat-1" || result.TotalPrice != natCLIQuotePrice {
					t.Errorf("partial=%s", out)
				}
			} else if out != "" {
				t.Errorf("unexpected stdout=%s", out)
			}
			state.mu.Lock()
			defer state.mu.Unlock()
			if state.orders != tc.orders || state.prices != 1 || state.renewals != 0 {
				t.Errorf("orders=%d prices=%d renewals=%d", state.orders, state.prices, state.renewals)
			}
		})
	}
}

func TestNetworkNATSDKDeleteMissingTarget(t *testing.T) {
	state := &natCLIState{whitelist: true}
	run := natIAMCLI(t, natCLIRoutes(t, state))
	code, out, stderr := run("network", "delete-nat-instance", "--zone-id", "zone-1", "--vpc-id", "vpc-1", "--nat-id", "nat-1", "--yes")
	if code != 4 || out != "" || !strings.Contains(stderr, "NotFound") {
		t.Fatalf("exit=%d stdout=%s stderr=%s", code, out, stderr)
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.deletes != 0 {
		t.Errorf("deletes=%d", state.deletes)
	}
}
