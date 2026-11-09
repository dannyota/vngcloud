//go:build livewrite

package livetest_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync/atomic"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/compute"
	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/network"
	"danny.vn/vngcloud/portal"
)

func TestFloatingIPHarnessGates(t *testing.T) {
	for _, tc := range []struct {
		write, floating, cap string
		run, refuse          bool
	}{
		{"", "1", "467800", false, false},
		{"1", "", "467800", false, false},
		{"1", "1", "", false, true},
		{"1", "1", "467801", false, true},
		{"1", "1", "467800.0", false, true},
		{"1", "1", "467800", true, false},
	} {
		run, err := floatingIPGates(tc.write, tc.floating, tc.cap)
		if run != tc.run || (err != nil) != tc.refuse {
			t.Fatalf("gate result = %v, %v; want %v, refusal %v", run, err, tc.run, tc.refuse)
		}
	}
	for _, price := range []float64{0, -1, 467801} {
		if floatingIPPriceOK(price) {
			t.Fatal("invalid quote accepted")
		}
	}
	if !floatingIPPriceOK(467800) {
		t.Fatal("approved cap refused")
	}
}

func TestFloatingIPHarnessEnabledZone(t *testing.T) {
	zones := []portal.Zone{
		{UUID: vngcloud.Ptr("disabled"), IsEnabled: vngcloud.Ptr(false)},
		{UUID: vngcloud.Ptr("enabled"), IsEnabled: vngcloud.Ptr(true)},
	}
	if _, err := floatingIPZone(zones, "disabled"); err == nil {
		t.Fatal("disabled zone accepted")
	}
	if got, err := floatingIPZone(zones, ""); err != nil || got != "enabled" {
		t.Fatal("enabled zone not selected")
	}
}

func TestFloatingIPHarnessLinkage(t *testing.T) {
	s := compute.Server{UUID: "server-1", PublicIP: "192.0.2.1", InternalInterfaces: []compute.NetworkInterface{
		{UUID: "nic-1", FloatingIPID: "ip-1", FloatingIP: "192.0.2.1", NetworkUUID: "vpc-1", SubnetUUID: "subnet-1"},
	}}
	row := network.WANIP{UUID: "ip-1", NetworkInterfaceID: "nic-1"}
	if !floatingIPLinked(s, []network.WANIP{row}, "ip-1", "nic-1", "vpc-1", "subnet-1") {
		t.Fatal("exact linkage refused")
	}
	for _, rows := range [][]network.WANIP{
		nil, {row, row}, {{UUID: "ip-1", NetworkInterfaceID: "other"}},
	} {
		if floatingIPLinked(s, rows, "ip-1", "nic-1", "vpc-1", "subnet-1") {
			t.Fatal("ambiguous linkage accepted")
		}
	}
}

func TestFloatingIPHarnessCompleteInventory(t *testing.T) {
	calls := 0
	read := func(_ context.Context, page int) (*core.PagedList[network.WANIP], error) {
		calls++
		return &core.PagedList[network.WANIP]{
			Items: []network.WANIP{{UUID: "ip-1"}}, Page: page,
			PageSize: 1000, TotalPage: 2, TotalItem: 1001,
		}, nil
	}
	if _, err := floatingIPWalk(context.Background(), read, func(row network.WANIP) string { return row.UUID }); err == nil {
		t.Fatal("incomplete inventory accepted")
	}
	if calls > 2 {
		t.Fatal("inventory walk did not stop")
	}
}

func TestFloatingIPHarnessCleanupStopsAtChild(t *testing.T) {
	ledger := &floatingIPLedger{ids: map[string]string{"server": "owned-server", "vpc": "owned-vpc"}}
	var calls []string
	steps := []floatingIPCleanupStep{
		{kind: "server", remove: func(context.Context, string) error {
			calls = append(calls, "server")
			return errors.New("child failed")
		}},
		{kind: "vpc", remove: func(context.Context, string) error {
			calls = append(calls, "vpc")
			return nil
		}},
	}
	if ledger.clean(context.Background(), steps) || !slices.Equal(calls, []string{"server"}) {
		t.Fatal("parent deletion continued after child failure")
	}
	if ledger.ids["server"] != "owned-server" || ledger.ids["vpc"] != "owned-vpc" {
		t.Fatal("failed cleanup lost ledger entries")
	}
}

func TestFloatingIPHarnessWriteAllowlist(t *testing.T) {
	var sends atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		sends.Add(1)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()
	guard := &floatingIPTransport{base: server.Client().Transport, host: server.Listener.Addr().String()}
	client := &http.Client{Transport: guard}
	guard.arm(http.MethodPost, "/v2/project-1/servers")
	for i := range 2 {
		req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, server.URL+"/v2/project-1/servers", nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := client.Do(req)
		if resp != nil {
			_ = resp.Body.Close()
		}
		if (err == nil) != (i == 0) {
			t.Fatal("create send-once guard failed")
		}
	}
	if sends.Load() != 1 {
		t.Fatalf("order sends = %d, want 1", sends.Load())
	}
	guard.arm(http.MethodDelete, "/v2/project-1/servers/owned")
	req, err := http.NewRequestWithContext(context.Background(), http.MethodDelete, server.URL+"/v2/project-1/servers/sc365-other", nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp, err := client.Do(req); err == nil {
		_ = resp.Body.Close()
		t.Fatal("unowned delete allowed")
	}
}

func TestFloatingIPHarnessLedgerRejectsUnownedIDs(t *testing.T) {
	ledger := &floatingIPLedger{
		ids: map[string]string{}, seen: map[string]map[string]bool{},
		baseline: map[string]map[string]bool{"server": {"existing": true}},
	}
	ledger.remember("server", "existing")
	if !ledger.blocked || ledger.ids["server"] != "" || !ledger.seen["server"]["existing"] {
		t.Fatal("existing ID was adopted or its uncertain observation was lost")
	}
	called := false
	if ledger.clean(context.Background(), []floatingIPCleanupStep{{"server", func(context.Context, string) error {
		called = true
		return nil
	}}}) || called {
		t.Fatal("uncertain ownership allowed a delete")
	}
}

func TestFloatingIPHarnessBodylessCapture(t *testing.T) {
	capture := &floatingIPCapture{dir: t.TempDir()}
	capture.hook(vngcloud.ResponseCapture{Operation: "network.DeleteSecurityGroup", Method: http.MethodDelete, StatusCode: http.StatusNoContent})
	if capture.failed || capture.saved["network.DeleteSecurityGroup"] != 1 {
		t.Fatal("bodyless cleanup acknowledgement was not captured")
	}
}
