package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"danny.vn/vngcloud"
)

func TestNetworkEndpointSelectorsReachSDK(t *testing.T) {
	const project = "pro-00000000-0000-0000-0000-000000000001"
	const region = "66b500000000000000000001"
	const category = "66b500000000000000000009"
	const service = "00000000-0000-0000-0000-0000000000a1"
	const pkg = "00000000-0000-0000-0000-0000000000b1"
	for _, name := range []string{"quote-create-endpoint", "create-endpoint"} {
		t.Run(name, func(t *testing.T) {
			withCleanEnv(t)
			created := false
			var requests []map[string]any
			rt := saCLITransport(func(r *http.Request) (*http.Response, error) {
				w := httptest.NewRecorder()
				var body string
				switch {
				case r.URL.Host == "signin.greennode.ai" && r.Method == http.MethodGet:
					body = `<input name="_csrf" value="synthetic">`
				case r.URL.Host == "signin.greennode.ai":
					w.Header().Set("Location", "https://dashboard.console.greennode.ai/?code=synthetic")
					w.WriteHeader(http.StatusFound)
					return w.Result(), nil
				case r.URL.Host == "dashboard.console.greennode.ai":
					body = `{"accessToken":"synthetic-token","expiresIn":3600}`
				case strings.HasSuffix(r.URL.Path, "/projects"):
					body = `{"projects":[{"projectId":"` + project + `","region":"hcm-3"}]}`
				case strings.HasSuffix(r.URL.Path, "/regions"):
					body = `{"success":true,"data":[{"uuid":"` + region + `","vnetworkDashboard":"https://hcm-3-vnetwork.console.greennode.ai"}]}`
				case strings.HasSuffix(r.URL.Path, "/zones"):
					body = `{"success":true,"data":[{"uuid":"HCM03-1B","zoneType":"AVAILABILITY","isEnabled":true}]}`
				case strings.HasSuffix(r.URL.Path, "/categories"):
					body = `{"success":true,"data":[{"uuid":"` + category + `","isDefault":false}]}`
				case strings.HasSuffix(r.URL.Path, "/services"):
					if r.URL.Query().Get("zoneUuid") != "HCM03-1B" {
						t.Errorf("zone query %s", r.URL.RawQuery)
					}
					body = `{"success":true,"data":[{"uuid":"` + service + `","name":"alternate","endpointType":"Gateway"}]}`
				case strings.HasSuffix(r.URL.Path, "/packages"):
					body = `{"success":true,"data":[{"uuid":"` + pkg + `","name":"Premium","serviceName":"alternate","currencyUnit":"VND"}]}`
				case strings.HasSuffix(r.URL.Path, "/vpcs"):
					body = `{"success":true,"page":1,"size":1000,"totalPage":1,"total":1,"data":[{"uuid":"` + epCLIVPC + `","status":"ACTIVE","projectUuid":"` + project + `","regionUuid":"` + region + `"}]}`
				case strings.HasSuffix(r.URL.Path, "/subnets"):
					body = `{"success":true,"data":[{"uuid":"` + epCLISubnet + `","status":"ACTIVE","vpcUuid":"` + epCLIVPC + `","zone":{"uuid":"HCM03-1B"}}]}`
				case strings.HasSuffix(r.URL.Path, "/price") || r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/endpoints"):
					raw, _ := io.ReadAll(r.Body)
					var data map[string]any
					_ = json.Unmarshal(raw, &data)
					requests = append(requests, data)
					if strings.HasSuffix(r.URL.Path, "/price") {
						body = `{"success":true,"code":0,"data":{"optimumPrice":1,"propertiesPrice":[{"name":"Premium","optimumPrice":1,"monthlyPrice":1}]}}`
					} else {
						created = true
						w.WriteHeader(http.StatusCreated)
						body = `{"success":true,"code":0,"data":{"uuid":"` + epCLIID + `","name":"demo"}}`
					}
				case strings.HasSuffix(r.URL.Path, "/endpoints"):
					body = `{"success":true,"page":1,"size":1000,"totalPage":0,"total":0}`
					if created {
						body = `{"success":true,"page":1,"size":1000,"totalPage":1,"total":1,"data":[{"uuid":"` + epCLIID + `","endpointName":"demo","endpointServiceId":"66b500000000000000000008","vpcId":"` + epCLIVPC + `","status":"ACTIVE","endpointType":"Gateway","zoneUuid":"HCM03-1B","enableDnsName":false,"service":{"uuid":"` + service + `","name":"alternate"},"subnet":{"uuid":"` + epCLISubnet + `"},"packageId":{"uuid":"` + pkg + `"},"project":{"VServerProjectId":"` + project + `"}}]}`
					}
				default:
					t.Errorf("unexpected request %s", r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
					return w.Result(), nil
				}
				_, _ = w.Write([]byte(body))
				return w.Result(), nil
			})
			withTestOptions(t, vngcloud.WithIAMUser(&vngcloud.IAMUserAuth{RootEmail: "root", Username: "user", Password: "synthetic", HTTPClient: &http.Client{Transport: rt}}), vngcloud.WithTransport(rt), vngcloud.WithRetry(0, 0))
			args := append([]string{"--region", "hcm-3"}, epCreateArgs(name)...)
			args = append(args, "--availability-zone-id", "HCM03-1B", "--category-id", category, "--service-id", service, "--package-id", pkg)
			if name == "create-endpoint" {
				args = append(args, "--yes", "--max-price", "1")
			}
			var out, stderr bytes.Buffer
			code := Main(context.Background(), args, strings.NewReader(""), &out, &stderr)
			if code != 0 {
				t.Fatalf("exit=%d stdout=%s stderr=%s", code, out.String(), stderr.String())
			}
			if len(requests) != 1+boolIntCLI(name == "create-endpoint") {
				t.Fatalf("requests=%v", requests)
			}
			for _, request := range requests {
				info, ok := request["resourceInfo"].(map[string]any)
				if !ok || info["categoryUuid"] != category || info["serviceUuid"] != service || info["packageUuid"] != pkg {
					t.Fatalf("request=%v", request)
				}
			}
		})
	}
}

func boolIntCLI(value bool) int {
	if value {
		return 1
	}
	return 0
}
