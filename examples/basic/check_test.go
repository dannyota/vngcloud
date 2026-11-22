package main

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/billing"
	"danny.vn/vngcloud/globalloadbalancer"
	"danny.vn/vngcloud/monitor"
)

func TestSmokeCheckRejectsSDKErrors(t *testing.T) {
	for _, body := range []string{`[]`, `[{"id":"item-1"}]`} {
		t.Run(body, func(t *testing.T) {
			root := t.TempDir()
			writeSmokeArtifact(t, root, "raw", "synthetic/item", `{"regions":[{"statusCode":200,"body":`+body+`}]}`)
			writeSmokeArtifact(t, root, "sdk", "synthetic/item", `{"regions":[{"count":0,"error":"synthetic decode failure"}]}`)
			reports, err := checkSmokeOutput(root)
			if err != nil {
				t.Fatal(err)
			}
			if printSmokeCheck(reports) {
				t.Fatal("SDK decode failure passed the smoke check")
			}
		})
	}
}

func TestSmokeCheckRejectsMissingArtifacts(t *testing.T) {
	for _, kind := range []string{"raw", "sdk"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			writeSmokeArtifact(t, root, "raw", "synthetic/matched", `{"regions":[{"statusCode":200,"body":[{"id":"item-1"}]}]}`)
			writeSmokeArtifact(t, root, "sdk", "synthetic/matched", `{"regions":[{"count":1,"items":[{"id":"item-1"}]}]}`)
			artifact := `{"regions":[{"statusCode":200,"body":[]}]}`
			if kind == "sdk" {
				artifact = `{"regions":[{"count":0,"items":[]}]}`
			}
			writeSmokeArtifact(t, root, kind, "synthetic/orphan", artifact)
			reports, err := checkSmokeOutput(root)
			if err != nil {
				t.Fatal(err)
			}
			if printSmokeCheck(reports) {
				t.Fatal("missing artifact passed the smoke check")
			}
			if len(reports) != 2 || reports[1].resource != "synthetic/orphan" {
				t.Fatalf("orphan artifact report missing: %+v", reports)
			}
			if kind == "sdk" && !reports[1].sdkWithoutRaw {
				t.Fatal("SDK-only artifact was not marked")
			}
		})
	}
}

func TestSmokeCheckReportsSDKOnlyOutput(t *testing.T) {
	root := t.TempDir()
	writeSmokeArtifact(t, root, "sdk", "synthetic/orphan", `{"regions":[{"count":0,"items":[]}]}`)
	reports, err := checkSmokeOutput(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(reports) != 1 || !reports[0].sdkWithoutRaw || printSmokeCheck(reports) {
		t.Fatalf("SDK-only output was not rejected: %+v", reports)
	}
}

func TestSmokeCheckRejectsCoverageLoss(t *testing.T) {
	cases := []struct {
		name  string
		raw   string
		sdk   string
		count int
	}{
		{"decoded empty", `[{"id":"item-1"}]`, `[]`, 0},
		{"raw empty", `[]`, `[{"id":"item-1"}]`, 1},
		{"row dropped", `[{"id":"item-1"},{"id":"item-2"}]`, `[{"id":"item-1"}]`, 1},
		{"row added", `[{"id":"item-1"}]`, `[{"id":"item-1"},{"id":"item-2"}]`, 2},
		{"all keys dropped", `[{"id":"item-1"}]`, `[{}]`, 1},
		{"one key dropped", `[{"id":"item-1","name":"synthetic"}]`, `[{"id":"item-1"}]`, 1},
		{"status dropped", `[{"id":"item-1","status":"ACTIVE"}]`, `[{"id":"item-1"}]`, 1},
		{"count hides dropped row", `[{"id":"item-1"},{"id":"item-2"}]`, `[{"id":"item-1"}]`, 2},
		{"key dropped in one row", `[{"id":"item-1","name":"one"},{"id":"item-2","name":"two"}]`, `[{"id":"item-1","name":"one"},{"id":"item-2"}]`, 2},
		{"nested key dropped", `[{"left":{"name":"one"},"right":{"name":"two"}}]`, `[{"left":{},"right":{"name":"two"}}]`, 1},
		{"literal key moved into object", `[{"left":{},"left.name":"one"}]`, `[{"left":{"name":"one"}}]`, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeSmokeArtifact(t, root, "raw", "synthetic/item", `{"regions":[{"statusCode":200,"body":{"data":`+tc.raw+`}}]}`)
			writeSmokeArtifact(t, root, "sdk", "synthetic/item", fmt.Sprintf(`{"regions":[{"count":%d,"items":%s}]}`, tc.count, tc.sdk))
			reports, err := checkSmokeOutput(root)
			if err != nil {
				t.Fatal(err)
			}
			if printSmokeCheck(reports) {
				t.Fatal("row or key loss passed the smoke check")
			}
		})
	}
}

func TestSmokeCheckRequiresSuccessfulEmptyEvidence(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		sdk  string
	}{
		{"no captures", `{"regions":[]}`, `{"regions":[{"count":0,"items":[]}]}`},
		{"no SDK entries", `{"regions":[{"statusCode":200,"body":[]}]}`, `{"regions":[]}`},
		{"HTTP error", `{"regions":[{"statusCode":403,"body":[]}]}`, `{"regions":[{"count":0,"items":[]}]}`},
		{"missing raw body", `{"regions":[{"statusCode":200}]}`, `{"regions":[{"count":0,"items":[]}]}`},
		{"null raw body", `{"regions":[{"statusCode":200,"body":null}]}`, `{"regions":[{"count":0,"items":[]}]}`},
		{"missing raw payload", `{"regions":[{"statusCode":200,"body":{"code":200}}]}`, `{"regions":[{"count":0,"items":[]}]}`},
		{"null raw payload", `{"regions":[{"statusCode":200,"body":{"data":null}}]}`, `{"regions":[{"count":0,"items":[]}]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeSmokeArtifact(t, root, "raw", "synthetic/item", tc.raw)
			writeSmokeArtifact(t, root, "sdk", "synthetic/item", tc.sdk)
			reports, err := checkSmokeOutput(root)
			if err != nil {
				t.Fatal(err)
			}
			if printSmokeCheck(reports) {
				t.Fatal("unproven empty output passed the smoke check")
			}
		})
	}
}

func TestSmokeCheckAcceptsMatchedArtifacts(t *testing.T) {
	cases := []struct {
		name  string
		raw   string
		sdk   string
		count int
	}{
		{"populated", `{"data":[{"id":"item-1","name":"synthetic"}]}`, `[{"id":"item-1","name":"synthetic"}]`, 1},
		{"empty array", `[]`, `[]`, 0},
		{"empty envelope", `{"data":{"items":[]}}`, `[]`, 0},
		{"empty nil SDK slice", `{"data":[]}`, `null`, 0},
		{"empty storage envelope", `{"success":true,"datas":[]}`, `[]`, 0},
		{"SDK output wrapper", `{"data":{"items":[{"id":"item-1"},{"id":"item-2"}]}}`, `{"Items":[{"id":"item-1"},{"id":"item-2"}],"TotalItem":2}`, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeSmokeArtifact(t, root, "raw", "synthetic/item", `{"regions":[{"statusCode":200,"body":`+tc.raw+`}]}`)
			writeSmokeArtifact(t, root, "sdk", "synthetic/item", fmt.Sprintf(`{"regions":[{"count":%d,"items":%s}]}`, tc.count, tc.sdk))
			reports, err := checkSmokeOutput(root)
			if err != nil {
				t.Fatal(err)
			}
			if !printSmokeCheck(reports) {
				t.Fatalf("matching artifacts failed: %+v", reports)
			}
		})
	}
}

func TestSmokeCheckMatchesCaptureScopes(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		sdk  string
	}{
		{
			"config mismatch",
			`{"regions":[{"config":"one","statusCode":200,"body":[]}]}`,
			`{"regions":[{"config":"two","count":0,"items":[]}]}`,
		},
		{
			"region mismatch",
			`{"regions":[{"region":"han-1","statusCode":200,"body":[]}]}`,
			`{"regions":[{"region":"hcm-3","count":0,"items":[]}]}`,
		},
		{
			"keys moved between regions",
			`{"regions":[{"region":"han-1","statusCode":200,"body":[{"id":"one","name":"one"}]},{"region":"hcm-3","statusCode":200,"body":[{"id":"two"}]}]}`,
			`{"regions":[{"region":"han-1","count":1,"items":[{"id":"one"}]},{"region":"hcm-3","count":1,"items":[{"id":"two","name":"two"}]}]}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeSmokeArtifact(t, root, "raw", "synthetic/item", tc.raw)
			writeSmokeArtifact(t, root, "sdk", "synthetic/item", tc.sdk)
			reports, err := checkSmokeOutput(root)
			if err != nil {
				t.Fatal(err)
			}
			if printSmokeCheck(reports) {
				t.Fatal("unmatched capture scopes passed the smoke check")
			}
		})
	}
}

func TestSmokeCheckAcceptsAggregatedCaptures(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		sdk  string
	}{
		{
			"paginated raw and aggregated SDK",
			`{"regions":[{"config":"synthetic","region":"han-1","statusCode":200,"body":{"data":[{"id":"one"}]}},{"config":"synthetic","region":"han-1","statusCode":200,"body":{"data":[{"id":"two"}]}}]}`,
			`{"regions":[{"config":"synthetic","region":"han-1","count":2,"items":[{"id":"one"},{"id":"two"}]}]}`,
		},
		{
			"account scope ignores capture region",
			`{"regions":[{"config":"synthetic","region":"han-1","statusCode":200,"body":{"data":[]}}]}`,
			`{"regions":[{"config":"synthetic","region":"","scope":"account","count":0}]}`,
		},
		{
			"failed attempt before successful capture",
			`{"regions":[{"statusCode":503,"body":{"error":"synthetic"}},{"statusCode":200,"body":[]}]}`,
			`{"regions":[{"count":0,"items":[]}]}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeSmokeArtifact(t, root, "raw", "synthetic/item", tc.raw)
			writeSmokeArtifact(t, root, "sdk", "synthetic/item", tc.sdk)
			reports, err := checkSmokeOutput(root)
			if err != nil {
				t.Fatal(err)
			}
			if !printSmokeCheck(reports) {
				t.Fatalf("matching captures failed: %+v", reports)
			}
		})
	}
}

func TestSmokeCheckRejectsTrailingJSON(t *testing.T) {
	for _, kind := range []string{"raw", "sdk"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			raw := `{"regions":[{"statusCode":200,"body":[]}]}`
			sdk := `{"regions":[{"count":0,"items":[]}]}`
			if kind == "raw" {
				raw += `{}`
			} else {
				sdk += `{}`
			}
			writeSmokeArtifact(t, root, "raw", "synthetic/item", raw)
			writeSmokeArtifact(t, root, "sdk", "synthetic/item", sdk)
			reports, err := checkSmokeOutput(root)
			if err != nil {
				t.Fatal(err)
			}
			if printSmokeCheck(reports) {
				t.Fatal("invalid JSON artifact passed the smoke check")
			}
		})
	}
}

func TestSmokeCheckAcceptsSuppressedVKSArtifacts(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body string
		switch r.URL.Path {
		case "/v1/clusters":
			body = `{"items":[],"total":0,"page":0,"pageSize":10}`
		case "/v1/cluster-versions":
			body = `[{"version":"synthetic","enable":false,"stage":"test","deprecatedAt":null}]`
		case "/v1/quota":
			body = `{"maxClusters":0,"numClusters":0,"maxNodeGroupsPerCluster":0,"maxNodesPerNodeGroup":0}`
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()
	captures := 0
	cfg, err := vngcloud.NewConfig(
		vngcloud.WithRegion("han-1"), vngcloud.WithStaticToken("synthetic-token"),
		vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{VKS: server.URL}),
		vngcloud.WithResponseCapture(func(vngcloud.ResponseCapture) { captures++ }),
	)
	if err != nil {
		t.Fatal(err)
	}
	sdk := newSDKOutputStore()
	showVKS(t.Context(), cfg, sdk)
	if captures != 0 {
		t.Fatal("VKS capture suppression was weakened")
	}
	root := writeSmokeStores(t, newRawCaptureStore(), sdk)
	reports, err := checkSmokeOutput(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(reports) != 3 || !printSmokeCheck(reports) {
		t.Fatalf("successful capture-suppressed VKS reads failed: %+v", reports)
	}
	for _, report := range reports {
		if !report.sdkWithoutRaw || report.comparisonSkipped == "" {
			t.Fatal("capture-suppressed output lost its SDK-only indication")
		}
	}
}

func TestSmokeCheckAccountsForUncapturedArtifacts(t *testing.T) {
	resources := []string{
		"backup/backends", "backup/policies", "billing/resources",
		"iam/user", "iam/user_detail", "iam/user_filtered", "network/vpn_connection",
		"monitor/synthetic_catalog", "monitor/quota_status", "monitor/quota_usage",
		"monitor/notification_packages_email", "monitor/notification_packages_sms",
		"monitor/email_quota", "monitor/email_quota_usage", "monitor/email_quota_quote",
		"monitor/synthetic_quota", "monitor/synthetic_quota_usage",
		"monitor/check_alarm", "monitor/check_alarm_history",
		"server/flavor_prices", "server/public_ip", "network/prices", "volume/discovery",
		"storage/discovery", "storage/project_types", "storage/project_quote_Gold",
		"storage/project_auto_renew", "monitor/channel", "monitor/channel_detail", "portal/quota_detail",
	}
	for _, resource := range resources {
		t.Run(resource, func(t *testing.T) {
			for _, failed := range []bool{false, true} {
				sdk := newSDKOutputStore()
				var sdkErr error
				if failed {
					sdkErr = errors.New("synthetic decode error")
				}
				sdk.addAccount(resource, []any{}, sdkErr)
				root := writeSmokeStores(t, newRawCaptureStore(), sdk)
				reports, err := checkSmokeOutput(root)
				if err != nil {
					t.Fatal(err)
				}
				if printSmokeCheck(reports) == failed {
					t.Fatalf("uncaptured result outcome does not reflect SDK success: failed=%t", failed)
				}
			}
		})
	}
}

func TestSmokeCheckPairsAdditionalCapturedArtifacts(t *testing.T) {
	for _, tc := range []struct {
		operation string
		resource  string
		url       string
		body      string
	}{
		{"monitor.ListChannelTypes", "monitor/channel_type", "https://synthetic.invalid/types", `{"lstData":[{"id":"item-1"}]}`},
		{"volume.ListSnapshotPolicies", "volume/snapshot_policy/backend-1", "https://synthetic.invalid/snapshot-policies?backendId=backend-1", `{"items":[{"id":"item-1"}]}`},
	} {
		t.Run(tc.operation, func(t *testing.T) {
			raw := newRawCaptureStore()
			raw.add("synthetic", "han-1", vngcloud.ResponseCapture{Operation: tc.operation, Method: "GET", URL: tc.url, StatusCode: 200, Body: []byte(tc.body)})
			sdk := newSDKOutputStore()
			sdk.setConfig("synthetic")
			sdk.addPricing(tc.resource, "han-1", []map[string]string{{"id": "item-1"}}, nil)
			root := writeSmokeStores(t, raw, sdk)
			reports, err := checkSmokeOutput(root)
			if err != nil {
				t.Fatal(err)
			}
			if len(reports) != 1 || reports[0].resource != tc.resource || reports[0].sdkWithoutRaw || !printSmokeCheck(reports) {
				t.Fatalf("captured operation did not pair with its SDK artifact: %+v", reports)
			}
		})
	}
}

func TestSmokeCheckNormalizesMonitorDetailOutput(t *testing.T) {
	for _, dropped := range []bool{false, true} {
		t.Run(fmt.Sprintf("dropped=%t", dropped), func(t *testing.T) {
			body := `{"id":"check-1"}`
			if dropped {
				body = `{"id":"check-1","unmodeled":true}`
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/vmonitor-uptime-manager/v1/uptimes/check-1" {
					t.Error("unexpected Monitor detail request")
					w.WriteHeader(http.StatusNotFound)
					return
				}
				_, _ = w.Write([]byte(body))
			}))
			defer server.Close()
			raw := newRawCaptureStore()
			cfg, err := vngcloud.NewConfig(
				vngcloud.WithRegion("han-1"), vngcloud.WithStaticToken("synthetic-token"),
				vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{Monitor: server.URL}),
				vngcloud.WithResponseCapture(func(c vngcloud.ResponseCapture) { raw.add("synthetic", "han-1", c) }),
			)
			if err != nil {
				t.Fatal(err)
			}
			detail, err := monitor.New(cfg).GetCheck(t.Context(), &monitor.GetCheckInput{CheckID: "check-1"})
			if err != nil {
				t.Fatal(err)
			}
			sdk := newSDKOutputStore()
			sdk.setConfig("synthetic")
			sdk.addAccount("monitor/check_detail", detail, nil)
			root := writeSmokeStores(t, raw, sdk)
			reports, err := checkSmokeOutput(root)
			if err != nil {
				t.Fatal(err)
			}
			if printSmokeCheck(reports) == dropped {
				t.Fatalf("detail wrapper changed key coverage outcome: %+v", reports)
			}
		})
	}
}

func TestSmokeCheckNormalizesKnownOutputWrappers(t *testing.T) {
	for _, tc := range []struct {
		resource string
		raw      string
		sdk      any
	}{
		{"billing/current_period_cost", `{"periodKey":"synthetic","actualCost":1}`, &billing.GetCurrentPeriodCostOutput{PeriodCost: billing.PeriodCost{PeriodKey: "synthetic", ActualCost: 1}}},
		{"monitor/channel_detail", `{"id":"channel-1"}`, &monitor.GetChannelOutput{Channel: monitor.Channel{ID: "channel-1"}}},
		{"glb/usage_history", `[{"timestamp":"synthetic","value":1,"type":"cpu"},{"timestamp":"synthetic","value":2,"type":"cpu"}]`, []*globalloadbalancer.ListUsageHistoriesOutput{
			{Items: []globalloadbalancer.UsageHistory{{Timestamp: "synthetic", Value: 1, Type: "cpu"}, {Timestamp: "synthetic", Value: 2, Type: "cpu"}}},
		}},
	} {
		t.Run(tc.resource, func(t *testing.T) {
			raw := newRawCaptureStore()
			raw.resources[tc.resource] = &rawResourceOutput{Regions: []rawRegionOutput{{StatusCode: 200, Body: []byte(tc.raw)}}}
			sdk := newSDKOutputStore()
			sdk.addAccount(tc.resource, tc.sdk, nil)
			root := writeSmokeStores(t, raw, sdk)
			reports, err := checkSmokeOutput(root)
			if err != nil {
				t.Fatal(err)
			}
			if !printSmokeCheck(reports) {
				t.Fatalf("known SDK output wrapper was not normalized: %+v", reports)
			}
		})
	}
}

func TestSmokeCheckRejectsInvalidUncapturedArtifacts(t *testing.T) {
	for _, tc := range []struct {
		resource string
		data     string
	}{
		{"vks/cluster", `{"regions":[]}`},
		{"vks/cluster", `{"regions":[{"count":1,"items":[]}]}`},
		{"vks/cluster", `{"regions":[{"count":0,"items":[]}]}
{}`},
		{"vks/unknown", `{"regions":[{"count":0,"items":[]}]}`},
	} {
		t.Run(tc.resource+tc.data, func(t *testing.T) {
			root := t.TempDir()
			writeSmokeArtifact(t, root, "sdk", tc.resource, tc.data)
			reports, err := checkSmokeOutput(root)
			if err != nil {
				t.Fatal(err)
			}
			if printSmokeCheck(reports) {
				t.Fatal("invalid or unrecognized SDK-only artifact passed")
			}
		})
	}
}

func TestSmokeCheckMarksUncalledGLBChildren(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != http.MethodGet || r.URL.Path != "/v1/global-load-balancers" {
			t.Error("empty parent inventory triggered a child request")
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(`{"items":[],"offset":0,"limit":10000,"total":0}`))
	}))
	defer server.Close()
	raw := newRawCaptureStore()
	cfg, err := vngcloud.NewConfig(
		vngcloud.WithRegion("han-1"), vngcloud.WithStaticToken("synthetic-token"),
		vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{GLB: server.URL}),
		vngcloud.WithResponseCapture(func(c vngcloud.ResponseCapture) { raw.add("synthetic", "han-1", c) }),
	)
	if err != nil {
		t.Fatal(err)
	}
	sdk := newSDKOutputStore()
	sdk.setConfig("synthetic")
	showGlobalLoadBalancer(t.Context(), cfg, sdk)
	if requests != 1 {
		t.Fatalf("empty inventory made %d requests, want 1", requests)
	}
	root := writeSmokeStores(t, raw, sdk)
	reports, err := checkSmokeOutput(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(reports) != 8 || !printSmokeCheck(reports) {
		t.Fatalf("uncalled child operations were rejected: %+v", reports)
	}
	for _, report := range reports {
		if report.resource != "glb/load_balancer" && (report.skippedCalls != 1 || report.comparisonSkipped == "") {
			t.Fatal("uncalled child operation lacks an explicit skip indication")
		}
	}
}

func TestSmokeCheckMarksUncalledNestedGLBChildren(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		var body string
		switch r.URL.Path {
		case "/v1/global-load-balancers":
			body = `{"items":[{"id":"lb-1"}],"offset":0,"limit":10000,"total":1}`
		case "/v1/global-load-balancers/lb-1":
			body = `{"id":"lb-1"}`
		case "/v1/global-load-balancers/lb-1/global-listeners", "/v1/global-load-balancers/lb-1/global-pools":
			body = `[]`
		case "/v1/global-load-balancers/lb-1/usage-histories":
			body = `{"items":[]}`
		default:
			t.Errorf("empty nested inventory triggered a child request: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()
	raw := newRawCaptureStore()
	cfg, err := vngcloud.NewConfig(
		vngcloud.WithRegion("han-1"), vngcloud.WithStaticToken("synthetic-token"),
		vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{GLB: server.URL}),
		vngcloud.WithResponseCapture(func(c vngcloud.ResponseCapture) { raw.add("synthetic", "han-1", c) }),
	)
	if err != nil {
		t.Fatal(err)
	}
	sdk := newSDKOutputStore()
	sdk.setConfig("synthetic")
	showGlobalLoadBalancer(t.Context(), cfg, sdk)
	if requests != 5 {
		t.Fatalf("nested inventory made %d requests, want 5", requests)
	}
	root := writeSmokeStores(t, raw, sdk)
	reports, err := checkSmokeOutput(root)
	if err != nil {
		t.Fatal(err)
	}
	if !printSmokeCheck(reports) {
		t.Fatalf("uncalled nested child operations were rejected: %+v", reports)
	}
}

func TestSmokeCheckRejectsUnprovenSkippedCalls(t *testing.T) {
	for _, tc := range []struct {
		name      string
		rawParent string
		sdkParent string
		child     string
		childRaw  string
	}{
		{name: "missing raw parent", rawParent: "omit"},
		{name: "missing SDK parent", sdkParent: "omit"},
		{name: "failed parent", rawParent: `{"regions":[{"config":"synthetic","region":"han-1","statusCode":403,"body":[]}]}`},
		{name: "missing parent payload", rawParent: `{"regions":[{"config":"synthetic","region":"han-1","statusCode":200,"body":{"total":0}}]}`},
		{name: "nonempty parent", rawParent: `{"regions":[{"config":"synthetic","region":"han-1","statusCode":200,"body":{"items":[{"id":"lb-1"}],"total":1}}]}`},
		{name: "nonempty parent total", rawParent: `{"regions":[{"config":"synthetic","region":"han-1","statusCode":200,"body":{"items":[],"total":1}}]}`},
		{name: "failed SDK parent", sdkParent: `{"regions":[{"config":"synthetic","region":"han-1","count":0,"error":"synthetic"}]}`},
		{name: "wrong scope", child: `{"regions":[{"config":"different","region":"han-1","count":0,"skippedParent":"glb/load_balancer"}]}`},
		{name: "SDK child error", child: `{"regions":[{"config":"synthetic","region":"han-1","count":0,"error":"synthetic","skippedParent":"glb/load_balancer"}]}`},
		{name: "populated child", child: `{"regions":[{"config":"synthetic","region":"han-1","count":1,"items":[{"id":"lb-1"}],"skippedParent":"glb/load_balancer"}]}`},
		{name: "missing skip marker", child: `{"regions":[{"config":"synthetic","region":"han-1","count":0,"items":[]}]}`},
		{name: "wrong parent", child: `{"regions":[{"config":"synthetic","region":"han-1","count":0,"skippedParent":"../other"}]}`},
		{name: "unexpected child capture", childRaw: `{"regions":[{"config":"synthetic","region":"han-1","statusCode":200,"body":[]}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			rawParent := tc.rawParent
			if rawParent == "" {
				rawParent = `{"regions":[{"config":"synthetic","region":"han-1","statusCode":200,"body":{"items":[],"total":0}}]}`
			}
			sdkParent := tc.sdkParent
			if sdkParent == "" {
				sdkParent = `{"regions":[{"config":"synthetic","region":"han-1","count":0,"items":[]}]}`
			}
			child := tc.child
			if child == "" {
				child = `{"regions":[{"config":"synthetic","region":"han-1","count":0,"skippedParent":"glb/load_balancer"}]}`
			}
			if rawParent != "omit" {
				writeSmokeArtifact(t, root, "raw", "glb/load_balancer", rawParent)
			}
			if sdkParent != "omit" {
				writeSmokeArtifact(t, root, "sdk", "glb/load_balancer", sdkParent)
			}
			writeSmokeArtifact(t, root, "sdk", "glb/load_balancer_detail", child)
			if tc.childRaw != "" {
				writeSmokeArtifact(t, root, "raw", "glb/load_balancer_detail", tc.childRaw)
			}
			reports, err := checkSmokeOutput(root)
			if err != nil {
				t.Fatal(err)
			}
			if printSmokeCheck(reports) {
				t.Fatal("unproven skipped operation passed the check")
			}
		})
	}
}

func TestSmokeCheckComparesOtherScopesBesideSkippedCalls(t *testing.T) {
	root := t.TempDir()
	writeSmokeArtifact(t, root, "raw", "glb/load_balancer", `{"regions":[{"region":"han-1","statusCode":200,"body":{"items":[],"total":0}},{"region":"hcm-3","statusCode":200,"body":{"items":[{"id":"lb-1"}],"total":1}}]}`)
	writeSmokeArtifact(t, root, "sdk", "glb/load_balancer", `{"regions":[{"region":"han-1","count":0,"items":[]},{"region":"hcm-3","count":1,"items":[{"id":"lb-1"}]}]}`)
	writeSmokeArtifact(t, root, "raw", "glb/load_balancer_detail", `{"regions":[{"region":"hcm-3","statusCode":200,"body":{"id":"lb-1"}}]}`)
	writeSmokeArtifact(t, root, "sdk", "glb/load_balancer_detail", `{"regions":[{"region":"han-1","count":0,"skippedParent":"glb/load_balancer"},{"region":"hcm-3","count":1,"items":[{"id":"lb-1"}]}]}`)
	reports, err := checkSmokeOutput(root)
	if err != nil {
		t.Fatal(err)
	}
	if !printSmokeCheck(reports) {
		t.Fatalf("skipped call obscured a compared scope: %+v", reports)
	}
}

func TestSmokeCheckSeparatesMonitorChannelLookupCaptures(t *testing.T) {
	lists := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Error("unexpected write")
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		switch r.URL.Path {
		case "/notification-gateway/api/v1/type/list":
			_, _ = w.Write([]byte(`{"lstData":[]}`))
		case "/notification-gateway/api/v1/notification/list/typeSearch":
			lists++
			_, _ = w.Write([]byte(`{"lstData":[{"id":"channel-1"}],"page":1,"pageSize":10000,"totalPage":1,"totalItem":1}`))
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	raw := newRawCaptureStore()
	cfg, err := vngcloud.NewConfig(
		vngcloud.WithRegion("han-1"), vngcloud.WithStaticToken("synthetic-token"),
		vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{Monitor: server.URL}),
		vngcloud.WithResponseCapture(func(c vngcloud.ResponseCapture) { raw.add("synthetic", "han-1", c) }),
	)
	if err != nil {
		t.Fatal(err)
	}
	sdk := newSDKOutputStore()
	sdk.setConfig("synthetic")
	showMonitorChannels(t.Context(), cfg, sdk)
	if lists != 2 {
		t.Fatalf("channel list and detail made %d list requests, want 2", lists)
	}
	root := writeSmokeStores(t, raw, sdk)
	reports, err := checkSmokeOutput(root)
	if err != nil {
		t.Fatal(err)
	}
	if !printSmokeCheck(reports) {
		t.Fatalf("detail lookup inflated list comparison: %+v", reports)
	}
	for _, report := range reports {
		if report.resource == "monitor/channel" || report.resource == "monitor/channel_detail" {
			if !report.sdkWithoutRaw || report.comparisonSkipped == "" {
				t.Fatal("channel result was presented as raw-verified")
			}
		}
	}
}

func writeSmokeStores(t *testing.T, raw *rawCaptureStore, sdk *sdkOutputStore) string {
	t.Helper()
	root := t.TempDir()
	if err := writeResources(filepath.Join(root, "raw"), raw.resources); err != nil {
		t.Fatal(err)
	}
	if err := writeResources(filepath.Join(root, "sdk"), sdk.resources); err != nil {
		t.Fatal(err)
	}
	return root
}

func writeSmokeArtifact(t *testing.T, root, kind, resource, data string) {
	t.Helper()
	path := filepath.Join(root, kind, resource+".json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}
