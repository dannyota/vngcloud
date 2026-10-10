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
)

const natCLIBody = `{"success":true,"page":2,"size":3,"totalPage":4,"total":11,"data":[{"uuid":"nat-1","natName":"edge","status":"ACTIVE","createdAt":"2026-01-01","updatedAt":"2026-01-02","projectUuid":"proj-1","zoneUuid":"zone-1","natGatewayIp":"10.0.0.1","publicIp":"203.0.113.1","deletedAt":null,"billingStatus":null,"natPackage":{"id":"pkg-1","uuid":"pkg-1","name":"small","packageId":"package-1","default":true,"image":{"uuid":"image-1","licenseKey":"NAT-EXCLUDED-SECRET","packageLimit":{"cpu":1,"memory":2,"diskSize":3}}},"vpc":{"uuid":"vpc-1","name":"private","cidr":"10.0.0.0/24"},"portalUserId":"NAT-EXCLUDED-SECRET"}]}`

func natCLI(t *testing.T, handler http.Handler) func(...string) (int, string, string) {
	t.Helper()
	home := withCleanEnv(t)
	writeConfigFile(t, home, "[profile agent]\nregion = hcm-3\nproject_id = proj-1\nread_only = true\n")
	opts := newFakeServer(t, handler)
	withTestOptions(t, append(opts, vngcloud.WithStaticToken("NAT-TEST-TOKEN"), vngcloud.WithRetry(0, 0))...)
	return func(args ...string) (int, string, string) {
		var stdout, stderr bytes.Buffer
		code := Main(context.Background(), args, strings.NewReader(""), &stdout, &stderr)
		return code, stdout.String(), stderr.String()
	}
}

func TestNetworkNATOutputs(t *testing.T) {
	for _, region := range []string{"hcm-3", "han-1"} {
		for _, format := range []string{"json", "table", "text"} {
			t.Run(region+"/"+format, func(t *testing.T) {
				calls := 0
				run := natCLI(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls++
					if r.Method != http.MethodGet || r.URL.Path != "/vnetwork/v1/zone-1/proj-1/nats" {
						t.Errorf("request = %s %s", r.Method, r.URL.Path)
					}
					if r.Header.Get("Authorization") != "Bearer NAT-TEST-TOKEN" {
						t.Error("missing test credential")
					}
					jsonHandler(http.StatusOK, natCLIBody)(w, r)
				}))
				code, stdout, stderr := run("--profile", "agent", "--region", region, "--debug",
					"--output", format, "network", "list-nat-instances", "--zone-id", "zone-1")
				if code != 0 || calls != 1 {
					t.Fatalf("exit=%d calls=%d stderr=%s", code, calls, stderr)
				}
				for _, value := range []string{"nat-1", "edge", "ACTIVE", "203.0.113.1", "vpc-1", "small"} {
					if !strings.Contains(stdout, value) {
						t.Errorf("output lacks %q: %s", value, stdout)
					}
				}
				if strings.Contains(stdout+stderr, "NAT-TEST-TOKEN") || strings.Contains(stdout+stderr, "NAT-EXCLUDED-SECRET") {
					t.Fatal("credential escaped")
				}
				if !strings.Contains(stderr, "request") {
					t.Fatalf("missing debug request: %s", stderr)
				}
				switch format {
				case "json":
					var out struct {
						Items                                []struct{ UUID, NATName string }
						Page, PageSize, TotalPage, TotalItem int
					}
					if err := json.Unmarshal([]byte(stdout), &out); err != nil {
						t.Fatal(err)
					}
					if len(out.Items) != 1 || out.Items[0].UUID != "nat-1" || out.Items[0].NATName != "edge" || out.Page != 2 || out.PageSize != 3 || out.TotalPage != 4 || out.TotalItem != 11 {
						t.Fatalf("output = %s", stdout)
					}
				case "table":
					headers := strings.Split(stdout, "\n")[1]
					want := []string{"BillingStatus", "CreatedAt", "DeletedAt", "NATGatewayIP", "NATName", "NATPackage", "ProjectUUID", "PublicIP", "Status", "UUID", "UpdatedAt", "VPC", "ZoneUUID"}
					parts := strings.Split(headers, "|")
					if len(parts) != len(want)+2 {
						t.Fatalf("headers = %s", headers)
					}
					for i, name := range want {
						if strings.TrimSpace(parts[i+1]) != name {
							t.Errorf("column %d = %q, want %s", i, parts[i+1], name)
						}
					}
				case "text":
					cells := strings.Split(strings.TrimSuffix(stdout, "\n"), "\t")
					if len(cells) != 13 || cells[0] != "" || cells[2] != "" || cells[4] != "edge" || cells[9] != "nat-1" || cells[12] != "zone-1" {
						t.Fatalf("text = %q", stdout)
					}
				}
			})
		}
	}
}

func TestNetworkNATPaging(t *testing.T) {
	for _, tc := range []struct {
		name       string
		args       []string
		page, size int
	}{
		{"defaults", nil, 1, 10},
		{"flags", []string{"--page", "3", "--size", "7"}, 3, 7},
		{"JSON precedence", []string{"--cli-input-json", `{"ZoneID":"zone-json","Page":5,"Size":9}`, "--page", "3", "--zone-id", "zone-1"}, 3, 9},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			run := natCLI(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				var params struct {
					Page, Size int
					Search     []string
					Sort       map[string]string
				}
				if err := json.Unmarshal([]byte(r.URL.Query().Get("params")), &params); err != nil {
					t.Error(err)
				}
				if r.URL.Path != "/vnetwork/v1/zone-1/proj-1/nats" || params.Page != tc.page || params.Size != tc.size || params.Search == nil || len(params.Search) != 0 || params.Sort == nil || len(params.Sort) != 0 {
					t.Errorf("request = %s", r.URL.RequestURI())
				}
				jsonHandler(http.StatusOK, natCLIBody)(w, r)
			}))
			args := append([]string{"--profile", "agent", "network", "list-nat-instances", "--zone-id", "zone-1"}, tc.args...)
			code, _, stderr := run(args...)
			if code != 0 || calls != 1 {
				t.Fatalf("exit=%d calls=%d stderr=%s", code, calls, stderr)
			}
		})
	}
}

func TestNetworkNATLocalValidation(t *testing.T) {
	for _, args := range [][]string{
		{"--region", "unsupported"}, {"--page", "-1"}, {"--size", "-1"}, {"--zone-id", "bad/zone"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			calls := 0
			run := natCLI(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
			argv := append([]string{"--profile", "agent", "network", "list-nat-instances"}, args...)
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

func TestNetworkNATQuery(t *testing.T) {
	run := natCLI(t, http.HandlerFunc(jsonHandler(http.StatusOK, natCLIBody)))
	code, stdout, stderr := run("--profile", "agent", "network", "list-nat-instances", "--zone-id", "zone-1",
		"--query", "Items[].{ID:UUID,Name:NATName,Status:Status}", "--output", "text")
	if code != 0 || stdout != "nat-1\tedge\tACTIVE\n" {
		t.Fatalf("exit=%d stdout=%q stderr=%s", code, stdout, stderr)
	}
}

func TestNetworkNATTableGolden(t *testing.T) {
	run := natCLI(t, http.HandlerFunc(jsonHandler(http.StatusOK, natCLIBody)))
	code, stdout, stderr := run("--profile", "agent", "network", "list-nat-instances", "--zone-id", "zone-1",
		"--query", "Items[].{ID:UUID,Name:NATName,Status:Status}", "--output", "table")
	if code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, stderr)
	}
	want, err := os.ReadFile("testdata/golden/network-list-nat-instances.table.golden")
	if err != nil {
		t.Fatal(err)
	}
	if stdout != string(want) {
		t.Fatalf("table = %q, want %q", stdout, want)
	}
}

func TestNetworkNATDefaultZone(t *testing.T) {
	calls := 0
	run := natCLI(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		switch r.URL.Path {
		case "/vnetwork/v1/regions":
			jsonHandler(http.StatusOK, `{"success":true,"data":[{"uuid":"zone-1","vnetworkDashboard":"https://hcm-3-vnetwork.console.greennode.ai"}]}`)(w, r)
		case "/vnetwork/v1/zone-1/proj-1/nats":
			jsonHandler(http.StatusOK, natCLIBody)(w, r)
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
	}))
	code, stdout, stderr := run("--profile", "agent", "network", "list-nat-instances")
	if code != 0 || calls != 2 || !strings.Contains(stdout, "nat-1") {
		t.Fatalf("exit=%d calls=%d stdout=%s stderr=%s", code, calls, stdout, stderr)
	}
}

func TestNetworkNATErrorDebugRedactsCredential(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusForbidden} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			calls := 0
			run := natCLI(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
				if token != "NAT-TEST-TOKEN" {
					t.Error("missing test credential")
				}
				body, err := json.Marshal(map[string]any{"success": false, "message": "denied " + token, "code": "code-" + token})
				if err != nil {
					t.Error(err)
					return
				}
				jsonHandler(status, string(body))(w, r)
			}))
			code, stdout, stderr := run("--profile", "agent", "--debug", "network", "list-nat-instances", "--zone-id", "zone-1")
			if code != 1 || calls != 1 || stdout != "" {
				t.Fatalf("exit=%d calls=%d stdout=%s stderr=%s", code, calls, stdout, stderr)
			}
			if strings.Contains(stdout+stderr, "NAT-TEST-TOKEN") {
				t.Fatal("credential escaped")
			}
			if !strings.Contains(stderr, "request") || !strings.Contains(stderr, "network.ListNATInstances") {
				t.Fatalf("missing debug request or operation: %s", stderr)
			}
		})
	}
}

func TestNetworkNATHelp(t *testing.T) {
	run := natCLI(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("help sent a request")
	}))
	code, stdout, stderr := run("network", "list-nat-instances", "--help")
	if code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, stderr)
	}
	for _, value := range []string{"hcm-3", "han-1", "--zone-id", "--page", "--size"} {
		if !strings.Contains(stdout, value) {
			t.Errorf("help lacks %q", value)
		}
	}
}
