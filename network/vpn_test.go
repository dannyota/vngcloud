package network

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/core"
)

const vpnCanary = "synthetic-vpn-secret-canary"
const vpnEmpty = `{"success":true,"page":1,"size":10,"totalPage":0,"total":0}`

func vpnClient(t *testing.T, body string, status int) (*Client, *bytes.Buffer, *int) {
	t.Helper()
	logs := new(bytes.Buffer)
	captures := new(int)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/vnetwork-gateway/vnetwork/v1/project-1/vpns" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	cfg, err := vngcloud.NewConfig(vngcloud.WithRegion("hcm-3"), vngcloud.WithProjectID("project-1"), vngcloud.WithStaticToken("test-token"), vngcloud.WithRetry(0, 0), vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{VNetwork: server.URL + "/vnetwork-gateway/"}), vngcloud.WithLogger(slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))), vngcloud.WithResponseCapture(func(vngcloud.ResponseCapture) { *captures++ }))
	if err != nil {
		t.Fatal(err)
	}
	return New(cfg), logs, captures
}

func assertVPNSafe(t *testing.T, out *ListVPNConnectionsOutput, err error, logs *bytes.Buffer, captures int) {
	t.Helper()
	var formatted bytes.Buffer
	fmt.Fprintf(&formatted, "%v %+v %#v %v %+v %#v", out, out, out, err, err, err)
	for _, value := range []any{out, err} {
		raw, marshalErr := json.Marshal(value)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		formatted.Write(raw)
	}
	slog.New(slog.NewJSONHandler(&formatted, nil)).Info("result", "output", out, "error", err)
	formatted.Write(logs.Bytes())
	for _, forbidden := range []string{vpnCanary, "preShareKey", "portalUserId", "excluded-canary"} {
		if strings.Contains(formatted.String(), forbidden) {
			t.Errorf("exposed %s", forbidden)
		}
	}
	if captures != 0 {
		t.Errorf("captures = %d", captures)
	}
}

func TestVPNFixture(t *testing.T) {
	raw, err := os.ReadFile("../testdata/network/list_vpn_connections.json")
	if err != nil {
		t.Fatal(err)
	}
	c, logs, captures := vpnClient(t, string(raw), 200)
	out, err := c.ListVPNConnections(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	assertVPNSafe(t, out, err, logs, *captures)
	if out.Page != 1 || out.PageSize != 10 || out.TotalPage != 7 || out.TotalItem != 63 || len(out.Items) != 4 {
		t.Fatalf("invalid metadata: %+v", out)
	}
	want := VPNConnection{
		UUID: "vpn-example", VPNName: "vpn-example", PackageUUID: "package-example", LocalNetworkCIDR: "192.0.2.0/24", CreatedAt: "2026-01-01T00:00:00Z", Status: "ACTIVE", BillingStatus: "active", ZoneUUID: "zone-example", LocalGatewayIP: natString("192.0.2.10"), VPNGatewayIP: natString("198.51.100.10"),
		SubnetDetailModel:  VPNSubnet{UUID: "subnet-example", Name: "subnet-example", Status: "ACTIVE", CIDR: "192.0.2.0/24", SubnetType: "example", UpdatedAt: "2026-01-02T00:00:00Z", LastSyncTime: "2026-01-02T00:00:00Z", ZoneID: "zone-example"},
		VPCDetailModel:     VPNVPC{UUID: "vpc-example", Name: "vpc-example", CIDR: "192.0.2.0/24", Status: "ACTIVE", RegionID: "region-example", ProjectID: "project-1", LastSyncTime: "2026-01-02T00:00:00Z", DNSStatus: "ENABLED"},
		ProjectDetailModel: VPNProject{ID: "project-1", BackendProjectID: "backend-example", VServerProjectID: "vserver-example"},
		PackageModel:       VPNPackage{UUID: "package-example", Name: "package-example", PackageID: "vpn-package-example", TunnelLimit: 3, Default: true},
		VPNSites:           []VPNSite{{RemoteGatewayIP: "203.0.113.10", UUID: "site-example", Status: "ACTIVE", SiteName: "site-example", CreatedAt: "2026-01-01T00:00:00Z", Phase1Configs: []VPNPhase1Config{{Phase1Algorithm: "algorithm-example", Phase1Hash: "hash-example", Phase1DHGroup: "group-example", Phase1IKELifetime: "12345"}}, Tunnels: []VPNTunnel{{SiteUUID: "site-example", TunnelName: "tunnel-example", RemoteNetworkCIDR: "203.0.113.0/24", UUID: "tunnel-example", Status: "ACTIVE", CreatedAt: "2026-01-01T00:00:00Z", Phase2Configs: []VPNPhase2Config{{Phase2Algorithm: "algorithm-example", Phase2Hash: "hash-example", Phase2DHGroup: "group-example", Phase2IKELifetime: "67890"}}}}}},
	}
	if !reflect.DeepEqual(out.Items[0], want) {
		t.Errorf("row = %+v, want %+v", out.Items[0], want)
	}
	if out.Items[1].Status != "PROVISIONING" || out.Items[1].LocalGatewayIP != nil || out.Items[1].VPNGatewayIP != nil || out.Items[2].Status != "ERROR" || out.Items[3].Status != "FUTURE_STATUS" || out.Items[3].LocalGatewayIP == nil || *out.Items[3].LocalGatewayIP != "" {
		t.Error("status or nullable fields")
	}
}

func TestVPNEnvelopeAndSecrets(t *testing.T) {
	bodies := []string{
		`{"success":false,"code":"` + vpnCanary + `","message":"` + vpnCanary + `"}`,
		`{"success":true,"data":null,"page":1,"size":10,"totalPage":0,"total":0}`,
		`{"success":true,"page":1,"size":10,"totalPage":1,"total":1}`,
		`{"success":true,"page":1,"size":10,"totalPage":0}`,
		`{"success":true,"page":1,"size":10,"total":0}`,
		`{"success":null,"page":1,"size":10,"totalPage":0,"total":0}`,
		`{"success":"true","page":1,"size":10,"totalPage":0,"total":0}`,
		`{"success":true,"page":0,"size":10,"totalPage":0,"total":0}`,
		`{"success":true,"page":1,"size":0,"totalPage":0,"total":0}`,
		`{"success":true,"page":1,"size":10,"totalPage":-1,"total":0}`,
		`{"success":true,"page":1,"size":10,"totalPage":0,"total":-1}`,
		`{"success":true,"page":1.5,"size":10,"totalPage":0,"total":0}`,
		`<html>` + vpnCanary + `</html>`, `{"success":"` + vpnCanary, ``,
	}
	for _, row := range []string{
		`null`, `{}`, `{"uuid":null}`, `{"uuid":1}`, `{"uuid":""}`,
		`{"uuid":"vpn-1","VPNName":"` + vpnCanary + `"}`,
		`{"uuid":"vpn-1","uuid":"vpn-2"}`,
		`{"uuid":"vpn-1","vpnName":null}`,
		`{"uuid":"vpn-1","packageModel":{"tunnelLimit":1.5}}`,
		`{"uuid":"vpn-1","subnetDetailModel":null}`,
		`{"uuid":"vpn-1","vpcDetailModel":{"CIDR":"example"}}`,
		`{"uuid":"vpn-1","projectDetailModel":{"id":null}}`,
		`{"uuid":"vpn-1","vpnSites":null}`,
		`{"uuid":"vpn-1","vpnSites":[{"phase1Configs":[{"phase1IkeLifeTime":1}]}]}`,
		`{"uuid":"vpn-1","vpnSites":[{"phase1Configs":[{"Phase1Hash":"example"}]}]}`,
		`{"uuid":"vpn-1","vpnSites":[{"tunnels":[{"phase2Configs":[{"phase2Hash":null}]}]}]}`,
		`{"uuid":"vpn-1","vpnSites":[{"tunnels":[{"phase2Configs":[{"Phase2Hash":"example"}]}]}]}`,
		`{"uuid":"vpn-1","unknown":{"key":"` + vpnCanary + `","key":1}}`,
	} {
		bodies = append(bodies, `{"success":true,"data":[`+row+`],"page":1,"size":10,"totalPage":1,"total":1}`)
	}
	bodies = append(bodies, strings.Replace(vpnEmpty, `"success":true`, `"success":false,"success":true`, 1), strings.Replace(vpnEmpty, `"success"`, `"Success"`, 1))
	for i, body := range bodies {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			c, logs, captures := vpnClient(t, body, 200)
			out, err := c.ListVPNConnections(context.Background(), nil)
			var apiErr *vngcloud.APIError
			if out != nil || !errors.As(err, &apiErr) || apiErr.Operation != listVPNOperation || apiErr.StatusCode != 200 || apiErr.Err != nil {
				t.Fatalf("invalid error: %+v", err)
			}
			if i != 0 && apiErr.Code != "InvalidResponse" {
				t.Errorf("code = %s", apiErr.Code)
			}
			if i == 0 && apiErr.Code != "" {
				t.Error("server code retained")
			}
			assertVPNSafe(t, out, err, logs, *captures)
		})
	}
	for _, body := range []string{vpnEmpty, `{"success":true,"data":[],"page":1,"size":10,"totalPage":0,"total":0}`} {
		c, logs, captures := vpnClient(t, body, 200)
		out, err := c.ListVPNConnections(context.Background(), nil)
		if err != nil || out.Items == nil || len(out.Items) != 0 {
			t.Fatalf("empty result = %+v, %v", out, err)
		}
		assertVPNSafe(t, out, err, logs, *captures)
	}
}

func TestVPNHTTPFailures(t *testing.T) {
	for _, status := range []int{201, 401, 403, 404, 429, 500, 502, 503, 504} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			c, logs, captures := vpnClient(t, `{"code":"`+vpnCanary+`","message":"`+vpnCanary+`","unknown":{"secret":"`+vpnCanary+`"}}`, status)
			out, err := c.ListVPNConnections(context.Background(), nil)
			var apiErr *vngcloud.APIError
			if !errors.As(err, &apiErr) || apiErr.StatusCode != status || apiErr.Operation != listVPNOperation || apiErr.Code != core.ResolvedCode(status, "") || apiErr.Retryable != (status == 429 || status == 502 || status == 503 || status == 504) {
				t.Fatalf("error = %+v", err)
			}
			want := map[int]error{401: vngcloud.ErrAuth, 403: vngcloud.ErrPermission, 404: vngcloud.ErrNotFound, 429: vngcloud.ErrRateLimited}[status]
			//nolint:errorlint // Exact identity also rejects a joined cause that could expose the server code.
			if apiErr.Err != want {
				t.Error("unsafe or missing cause")
			}
			assertVPNSafe(t, out, err, logs, *captures)
		})
	}
}

func TestVPNNestedSecretFields(t *testing.T) {
	for _, row := range []string{
		`{"uuid":"vpn-1","localGatewayIp":"","vpnGatewayIp":"","preShareKey":"` + vpnCanary + `","projectDetailModel":{"portalUserId":"` + vpnCanary + `"},"vpnSites":[{"preShareKey":{"nested":"` + vpnCanary + `"},"phase1Configs":[{"unknown":"` + vpnCanary + `"}],"tunnels":[{"preShareKey":"` + vpnCanary + `","phase2Configs":[{"unknown":"` + vpnCanary + `"}]}]}]}`,
		`{"uuid":"vpn-1","vpnSites":[{"phase1Configs":[{"phase1Hash":{"secret":"` + vpnCanary + `"}}]}]}`,
		`{"uuid":"vpn-1","vpnSites":[{"tunnels":[{"phase2Configs":[{"phase2Hash":["` + vpnCanary + `"]}]}]}]}`,
	} {
		body := `{"success":true,"data":[` + row + `],"page":1,"size":10,"totalPage":1,"total":1}`
		c, logs, captures := vpnClient(t, body, 200)
		out, err := c.ListVPNConnections(context.Background(), nil)
		if strings.Contains(row, `"phase1Hash"`) || strings.Contains(row, `"phase2Hash"`) {
			if out != nil || vngcloud.ErrorCode(err) != "InvalidResponse" {
				t.Fatalf("expected invalid response, got %v", err)
			}
		} else if err != nil || out.Items[0].LocalGatewayIP == nil || *out.Items[0].LocalGatewayIP != "" || out.Items[0].VPNGatewayIP == nil || *out.Items[0].VPNGatewayIP != "" {
			t.Fatalf("invalid nullable fields: %v", err)
		}
		assertVPNSafe(t, out, err, logs, *captures)
	}
}
