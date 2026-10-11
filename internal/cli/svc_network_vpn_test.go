package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/network"
)

const vpnCLISecret = "VPN-EXCLUDED-SECRET"
const vpnCLIToken = "VPN-TEST-TOKEN"
const vpnCLIBody = `{"success":true,"page":2,"size":3,"totalPage":4,"total":11,"data":[{"uuid":"vpn-1","vpnName":"edge","status":"ACTIVE","localNetworkCidr":"192.0.2.0/24","localGatewayIp":null,"vpnGatewayIp":"198.51.100.1","vpnSites":[{"uuid":"site-1","siteName":"office","preShareKey":"VPN-EXCLUDED-SECRET","phase1Configs":[{"phase1IkeLifeTime":"3600"}],"tunnels":[{"uuid":"tunnel-1","tunnelName":"private","remoteNetworkCidr":"203.0.113.0/24","phase2Configs":[{"phase2IkeLifeTime":"1800"}]},{"uuid":"tunnel-2"}]},{"uuid":"site-2","tunnels":[{"uuid":"tunnel-3"}]}]},{"uuid":"vpn-2","vpnName":"pending","status":"PROVISIONING","vpnSites":[]}]}`
const vpnCLISummaryQuery = "Items[].{ID:UUID,Name:VPNName,Sites:length(VPNSites),Status:Status,Tunnels:length(VPNSites[].Tunnels[])}"

func vpnCLI(t *testing.T, handler http.Handler) func(...string) (int, string, string) {
	t.Helper()
	home := withCleanEnv(t)
	writeConfigFile(t, home, "[profile agent]\nregion = hcm-3\nproject_id = proj-1\nread_only = true\n")
	opts := newFakeServer(t, handler)
	withTestOptions(t, append(opts, vngcloud.WithStaticToken(vpnCLIToken), vngcloud.WithRetry(0, 0))...)
	return func(args ...string) (int, string, string) {
		var stdout, stderr bytes.Buffer
		code := Main(context.Background(), args, strings.NewReader(""), &stdout, &stderr)
		return code, stdout.String(), stderr.String()
	}
}

func assertVPNCLISecretAbsent(t *testing.T, stdout, stderr string) {
	t.Helper()
	for _, secret := range []string{vpnCLISecret, vpnCLIToken, "preShareKey"} {
		if strings.Contains(stdout+stderr, secret) {
			t.Fatal("credential escaped")
		}
	}
}

func TestNetworkVPNOutputs(t *testing.T) {
	for _, region := range []string{"hcm-3", "han-1"} {
		for _, format := range []string{"json", "table", "text"} {
			t.Run(region+"/"+format, func(t *testing.T) {
				calls := 0
				run := vpnCLI(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls++
					if r.Method != http.MethodGet || r.URL.Path != "/vnetwork/v1/proj-1/vpns" {
						t.Errorf("request = %s %s", r.Method, r.URL.Path)
					}
					if r.Header.Get("Authorization") != "Bearer "+vpnCLIToken {
						t.Error("missing test credential")
					}
					jsonHandler(http.StatusOK, vpnCLIBody)(w, r)
				}))
				code, stdout, stderr := run("--profile", "agent", "--region", region, "--debug", "network", "list-vpn-connections", "--output", format)
				assertVPNCLISecretAbsent(t, stdout, stderr)
				if code != 0 || calls != 1 {
					t.Fatalf("exit=%d calls=%d stderr=%s", code, calls, stderr)
				}
				if !strings.Contains(stderr, "request") || !strings.Contains(stderr, "/vnetwork/v1/proj-1/vpns") {
					t.Fatalf("missing debug request: %s", stderr)
				}
				for _, value := range []string{"vpn-1", "edge", "ACTIVE", "site-1", "tunnel-1", "vpn-2", "PROVISIONING"} {
					if !strings.Contains(stdout, value) {
						t.Errorf("output lacks %q", value)
					}
				}
				switch format {
				case "json":
					var out network.ListVPNConnectionsOutput
					if err := json.Unmarshal([]byte(stdout), &out); err != nil {
						t.Fatal(err)
					}
					if len(out.Items) != 2 || out.Page != 2 || out.PageSize != 3 || out.TotalPage != 4 || out.TotalItem != 11 {
						t.Fatalf("output = %s", stdout)
					}
					vpn := out.Items[0]
					if vpn.LocalGatewayIP != nil || vpn.VPNGatewayIP == nil || *vpn.VPNGatewayIP != "198.51.100.1" || len(vpn.VPNSites) != 2 || len(vpn.VPNSites[0].Tunnels) != 2 || vpn.VPNSites[0].Phase1Configs[0].Phase1IKELifetime != "3600" || vpn.VPNSites[0].Tunnels[0].Phase2Configs[0].Phase2IKELifetime != "1800" {
						t.Fatalf("inline children = %+v", vpn)
					}
				case "table":
					lines := strings.Split(strings.TrimSuffix(stdout, "\n"), "\n")
					if len(lines) != 6 {
						t.Fatalf("table has %d lines", len(lines))
					}
					headers := strings.Split(lines[1], "|")
					want := []string{"BillingStatus", "CreatedAt", "LocalGatewayIP", "LocalNetworkCIDR", "PackageModel", "PackageUUID", "ProjectDetailModel", "Status", "SubnetDetailModel", "UUID", "VPCDetailModel", "VPNGatewayIP", "VPNName", "VPNSites", "ZoneUUID"}
					if len(headers) != len(want)+2 {
						t.Fatalf("headers = %s", lines[1])
					}
					for i, name := range want {
						if strings.TrimSpace(headers[i+1]) != name {
							t.Errorf("column %d = %q, want %s", i, headers[i+1], name)
						}
					}
				case "text":
					lines := strings.Split(strings.TrimSuffix(stdout, "\n"), "\n")
					if len(lines) != 2 || len(strings.Split(lines[0], "\t")) != 15 {
						t.Fatalf("text = %q", stdout)
					}
				}
			})
		}
	}
}

func TestNetworkVPNPaging(t *testing.T) {
	for _, tc := range []struct {
		name       string
		args       []string
		page, size int
	}{
		{"defaults", nil, 1, 10},
		{"flags", []string{"--page", "3", "--size", "7"}, 3, 7},
		{"JSON precedence", []string{"--cli-input-json", `{"Page":5,"Size":9}`, "--page", "3"}, 3, 9},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			run := vpnCLI(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				var params struct {
					Page, Size int
					Search     []struct{ Field, Value string }
					Sort       map[string]string
				}
				if err := json.Unmarshal([]byte(r.URL.Query().Get("params")), &params); err != nil {
					t.Error(err)
				}
				if r.URL.Path != "/vnetwork/v1/proj-1/vpns" || params.Page != tc.page || params.Size != tc.size || len(params.Search) != 1 || params.Search[0].Field != "any" || params.Search[0].Value != "" || params.Sort == nil || len(params.Sort) != 0 {
					t.Errorf("request = %s", r.URL.RequestURI())
				}
				jsonHandler(http.StatusOK, vpnCLIBody)(w, r)
			}))
			args := append([]string{"--profile", "agent", "network", "list-vpn-connections"}, tc.args...)
			code, _, stderr := run(args...)
			if code != 0 || calls != 1 {
				t.Fatalf("exit=%d calls=%d stderr=%s", code, calls, stderr)
			}
		})
	}
}

func TestNetworkVPNLocalValidation(t *testing.T) {
	for _, args := range [][]string{{"--region", "unsupported"}, {"--page", "-1"}, {"--size", "-1"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			calls := 0
			run := vpnCLI(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
			argv := append([]string{"--profile", "agent", "network", "list-vpn-connections"}, args...)
			code, stdout, stderr := run(argv...)
			if code != 2 || calls != 0 || stdout != "" {
				t.Fatalf("exit=%d calls=%d stdout=%s stderr=%s", code, calls, stdout, stderr)
			}
			if args[0] == "--region" && !strings.Contains(stderr, "InvalidConfig") {
				t.Fatalf("stderr = %s", stderr)
			}
		})
	}
}

func TestNetworkVPNQuery(t *testing.T) {
	for _, format := range []string{"json", "table", "text"} {
		for _, query := range []string{vpnCLISummaryQuery, "Items[].VPNSites[].preShareKey", "Items[].VPNSites[].PreShareKey"} {
			t.Run(format+"/"+query, func(t *testing.T) {
				run := vpnCLI(t, http.HandlerFunc(jsonHandler(http.StatusOK, vpnCLIBody)))
				code, stdout, stderr := run("--profile", "agent", "--debug", "network", "list-vpn-connections", "--query", query, "--output", format)
				assertVPNCLISecretAbsent(t, stdout, stderr)
				if code != 0 {
					t.Fatalf("exit=%d stderr=%s", code, stderr)
				}
				if query != vpnCLISummaryQuery {
					return
				}
				switch format {
				case "table":
					want, err := os.ReadFile("testdata/golden/network-list-vpn-connections.table.golden")
					if err != nil {
						t.Fatal(err)
					}
					if stdout != string(want) {
						t.Fatalf("table = %q, want %q", stdout, want)
					}
				case "text":
					if stdout != "vpn-1\tedge\t2\tACTIVE\t3\nvpn-2\tpending\t0\tPROVISIONING\t0\n" {
						t.Fatalf("text = %q", stdout)
					}
				case "json":
					var rows []struct {
						ID             string
						Sites, Tunnels int
					}
					if err := json.Unmarshal([]byte(stdout), &rows); err != nil {
						t.Fatal(err)
					}
					if len(rows) != 2 || rows[0].ID != "vpn-1" || rows[0].Sites != 2 || rows[0].Tunnels != 3 {
						t.Fatalf("query = %s", stdout)
					}
				}
			})
		}
	}
}

func TestNetworkVPNErrorDebug(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
	}{
		{"HTTP failure", `{"code":"VPN-EXCLUDED-SECRET","message":"VPN-TEST-TOKEN"}`, http.StatusForbidden},
		{"failure envelope", `{"success":false,"code":"VPN-EXCLUDED-SECRET","message":"VPN-TEST-TOKEN"}`, http.StatusOK},
		{"malformed", `{"success":"VPN-EXCLUDED-SECRET`, http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			run := vpnCLI(t, http.HandlerFunc(jsonHandler(tc.status, tc.body)))
			code, stdout, stderr := run("--profile", "agent", "--debug", "network", "list-vpn-connections")
			assertVPNCLISecretAbsent(t, stdout, stderr)
			if code != 1 || stdout != "" || !strings.Contains(stderr, "withheld") || !strings.Contains(stderr, "request") {
				t.Fatalf("exit=%d stdout=%s stderr=%s", code, stdout, stderr)
			}
		})
	}
}

func TestNetworkVPNHelp(t *testing.T) {
	run := vpnCLI(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("help sent a request") }))
	code, stdout, stderr := run("network", "list-vpn-connections", "--help")
	if code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, stderr)
	}
	for _, value := range []string{"hcm-3", "han-1", "--page", "--size"} {
		if !strings.Contains(stdout, value) {
			t.Errorf("help lacks %q", value)
		}
	}
	for _, value := range []string{"--zone-id", "--search", "--sort"} {
		if strings.Contains(stdout, value) {
			t.Errorf("help has unsupported flag %q", value)
		}
	}
}
