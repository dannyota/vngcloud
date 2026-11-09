//go:build livewrite

package livetest_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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

func TestFloatingIPHarnessSensitivePurchaseReachesConsoleAndCleanup(t *testing.T) {
	r, peer := newFloatingIPHarnessPeer(t, false)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	input := &compute.CreateServerInput{
		Name: r.name, ZoneID: "zone-1", FlavorID: "flavor-1", ImageID: "image-1",
		VPCID: "vpc-1", SubnetID: "subnet-1", SecurityGroupIDs: []string{"group-1"}, SSHKeyID: "key-1",
		RootDiskSize: 20, RootDiskTypeID: "ssd-1", AttachFloatingIP: true,
	}
	r.quote(ctx, input)
	if err := r.purchase(ctx, input); err != nil {
		t.Fatalf("successful SDK purchase did not reach console release: %v", err)
	}
	if r.capture.saved["compute.CreateServer"] != 0 || r.capture.saved["compute.GetServer"] != 0 {
		t.Fatal("Sensitive operations unexpectedly produced raw captures")
	}
	if r.ledger.ids["server"] != "server-1" || r.ledger.ids["volume"] != "volume-1" || r.ledger.ids["floating IP"] != "ip-1" {
		t.Fatal("SDK-returned relationships did not establish ownership")
	}
	assertFloatingIPSDKOutput(t, r.capture.dir, "compute.CreateServer")
	assertFloatingIPSDKOutput(t, r.capture.dir, "compute.GetServer")
	if r.capture.saved["network.ListWANIPs"] == 0 {
		t.Fatal("WAN raw response was not retained")
	}
	peer.mu.Lock()
	if peer.orders != 1 || peer.serverReads < 2 {
		t.Error("real SDK create, wait, and explicit server read were not exercised")
	}
	peer.released = true // Simulate the manager's console action, without an API write.
	peer.mu.Unlock()
	ticks := make(chan time.Time, 1)
	ticks <- time.Time{}
	if err := r.waitForConsoleRelease(ctx, ticks); err != nil || !r.released {
		t.Fatalf("console release did not confirm absence: %v", err)
	}
	if !r.cleanup(ctx) {
		t.Fatal("cleanup did not proceed after a Sensitive purchase")
	}
	peer.mu.Lock()
	defer peer.mu.Unlock()
	if !slices.Equal(peer.deletes, []string{"server", "volume", "group", "key", "subnet", "vpc"}) || len(r.ledger.ids) != 0 {
		t.Fatalf("cleanup order = %v; want every owned child before its parent", peer.deletes)
	}
}

func TestFloatingIPHarnessCleanupRechecksBlockedBetweenSDKSteps(t *testing.T) {
	r, peer := newFloatingIPHarnessPeer(t, true)
	peer.mu.Lock()
	peer.created, peer.released = true, true
	peer.mu.Unlock()
	r.serverAttempted, r.released = true, true
	r.rememberServer(compute.Server{UUID: "server-1", BootVolumeID: "volume-1"})
	r.interfaceID = "nic-1"
	r.ledger.attempted["server"], r.ledger.attempted["volume"] = true, true
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if r.cleanup(ctx) {
		t.Error("cleanup reported success after ownership became blocked")
	}
	peer.mu.Lock()
	defer peer.mu.Unlock()
	if !r.ledger.blocked || !slices.Equal(peer.deletes, []string{"server"}) {
		t.Fatalf("deletes after block = %v; want only the already completed server step", peer.deletes)
	}
	for _, kind := range []string{"volume", "group", "key", "subnet", "vpc"} {
		if r.ledger.ids[kind] == "" {
			t.Errorf("blocked cleanup lost the %s ledger entry", kind)
		}
	}
}

func TestFloatingIPHarnessCaptureDoesNotEstablishOwnership(t *testing.T) {
	r, _ := newFloatingIPHarnessPeer(t, false)
	r.capture.hook(vngcloud.ResponseCapture{
		Operation: "network.CreateVPC", Method: http.MethodPost, StatusCode: http.StatusCreated,
		Body: []byte(`{"data":{"id":"capture-vpc"}}`),
	})
	if r.ledger.blocked || r.ledger.ids["vpc"] != "vpc-1" || len(r.ledger.seen["vpc"]) != 1 {
		t.Fatal("raw capture changed SDK-established ownership")
	}
}

func assertFloatingIPSDKOutput(t *testing.T, dir, operation string) {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(dir, "*-"+operation+"-sdk.json"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("decoded %s output is missing", operation)
	}
	data, err := os.ReadFile(paths[0])
	if err != nil {
		t.Fatal(err)
	}
	var saved struct {
		SDK struct {
			Server compute.Server
		} `json:"sdk"`
		Body json.RawMessage `json:"body"`
	}
	if json.Unmarshal(data, &saved) != nil || saved.SDK.Server.UUID != "server-1" || saved.SDK.Server.BootVolumeID != "volume-1" || len(saved.Body) != 0 {
		t.Fatal("decoded SDK evidence is missing its relationships or is labeled as raw")
	}
	info, err := os.Stat(paths[0])
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatal("decoded SDK evidence is not private")
	}
}

// The peer serves synthetic responses through real SDK clients and the same
// permit, capture, purchase, release, and cleanup code used by the live test.
type floatingIPHarnessPeer struct {
	mu                  sync.Mutex
	created, released   bool
	orders, serverReads int
	deleted             map[string]bool
	deletes             []string
}

func newFloatingIPHarnessPeer(t *testing.T, blockAfterServer bool) (*floatingIPRun, *floatingIPHarnessPeer) {
	t.Helper()
	peer := &floatingIPHarnessPeer{deleted: map[string]bool{}}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		peer.serve(t, w, req)
	}))
	t.Cleanup(server.Close)
	guard := &floatingIPTransport{base: server.Client().Transport, host: server.Listener.Addr().String()}
	capture := &floatingIPCapture{dir: t.TempDir()}
	var run *floatingIPRun
	cfg, err := vngcloud.NewConfig(
		vngcloud.WithRegion("hcm-3"), vngcloud.WithProjectID("project-1"), vngcloud.WithStaticToken("synthetic-token"),
		vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{VServer: server.URL + "/", Portal: server.URL + "/"}),
		vngcloud.WithTransport(guard), vngcloud.WithRetry(0, 0),
		vngcloud.WithResponseCapture(func(response vngcloud.ResponseCapture) {
			capture.hook(response)
			if blockAfterServer && response.Operation == "compute.DeleteServer" {
				// Change the ledger during a real successful SDK step, without
				// substituting any cleanup step or changing ownership from a body.
				run.ledger.blocked = true
			}
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	run = newFloatingIPRun(t, cfg, guard, capture, "vngcloud-live-fip-1234abcd")
	for kind, id := range map[string]string{"vpc": "vpc-1", "subnet": "subnet-1", "group": "group-1", "key": "key-1"} {
		run.ledger.remember(kind, id)
		run.ledger.attempted[kind] = true
	}
	return run, peer
}

func (p *floatingIPHarnessPeer) serve(t *testing.T, w http.ResponseWriter, req *http.Request) {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	write := func(value any) { _ = json.NewEncoder(w).Encode(value) }
	missing := func() { w.WriteHeader(http.StatusNotFound); write(map[string]string{"message": "not found"}) }
	page := func(rows []any) {
		size, _ := strconv.Atoi(req.URL.Query().Get("size"))
		if size == 0 {
			size = 10000
		}
		totalPages := 0
		if len(rows) > 0 {
			totalPages = 1
		}
		write(map[string]any{"listData": rows, "page": 1, "pageSize": size, "totalPage": totalPages, "totalItem": len(rows)})
	}
	if req.Method == http.MethodDelete {
		kind := map[string]string{
			"/v2/project-1/servers/server-1": "server", "/v2/project-1/volumes/volume-1": "volume",
			"/v2/project-1/secgroups/group-1": "group", "/v2/project-1/sshKeys/key-1": "key",
			"/v2/project-1/networks/vpc-1/subnets/subnet-1": "subnet", "/v2/project-1/networks/vpc-1": "vpc",
		}[req.URL.Path]
		if kind == "" {
			t.Errorf("unowned delete: %s", req.URL.Path)
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if kind == "server" {
			var body map[string]any
			if json.NewDecoder(req.Body).Decode(&body) != nil || body["deleteAllVolume"] != false {
				t.Error("server delete did not preserve volumes")
			}
		}
		p.deleted[kind] = true
		p.deletes = append(p.deletes, kind)
		switch kind {
		case "server", "volume":
			w.WriteHeader(http.StatusAccepted)
		case "group", "key":
			w.WriteHeader(http.StatusNoContent)
		default:
			write(map[string]any{})
		}
		return
	}
	switch req.URL.Path {
	case "/v1/price":
		var body struct {
			ResourceType string `json:"resourceType"`
		}
		if json.NewDecoder(req.Body).Decode(&body) != nil {
			t.Error("invalid quote body")
		}
		price := 347800
		if body.ResourceType == "elastic-ip" {
			price = 120000
		}
		write(map[string]any{"optimumPrice": price, "originalPrice": price, "discountPrice": 0, "discountPercent": 0})
	case "/v2/project-1/servers":
		if req.Method == http.MethodPost {
			var body map[string]any
			if json.NewDecoder(req.Body).Decode(&body) != nil || body["attachFloating"] != true || body["rootDiskTypeId"] != "ssd-1" || body["rootDiskSize"] != float64(20) {
				t.Error("unexpected server order")
			}
			p.created = true
			p.orders++
			w.WriteHeader(http.StatusAccepted)
			write(map[string]any{"data": map[string]string{"uuid": "server-1"}})
		} else {
			page([]any{})
		}
	case "/v2/project-1/servers/server-1":
		if !p.created || p.deleted["server"] {
			missing()
			return
		}
		p.serverReads++
		nic := map[string]string{"uuid": "nic-1", "networkUuid": "vpc-1", "subnetUuid": "subnet-1", "serverUuid": "server-1"}
		if !p.released {
			nic["floatingIpId"], nic["floatingIp"] = "ip-1", "192.0.2.1"
		}
		write(map[string]any{"data": map[string]any{"uuid": "server-1", "name": "vngcloud-live-fip-1234abcd", "status": "ACTIVE", "bootVolumeId": "volume-1", "internalInterfaces": []any{nic}}})
	case "/v2/project-1/wanIps":
		rows := []any{}
		if !p.released {
			rows = append(rows, map[string]string{"uuid": "ip-1", "networkInterfaceId": "nic-1", "serverId": "server-1"})
		}
		page(rows)
	case "/v2/project-1/volumes":
		rows := []any{}
		if !p.deleted["volume"] {
			rows = append(rows, map[string]string{"uuid": "volume-1", "serverId": "server-1"})
		}
		page(rows)
	case "/v2/project-1/volumes/volume-1":
		if p.deleted["volume"] {
			missing()
		} else {
			write(map[string]any{"data": map[string]any{"uuid": "volume-1", "status": "AVAILABLE", "bootable": true}})
		}
	case "/v2/project-1/secgroups/group-1":
		if p.deleted["group"] {
			missing()
		} else {
			write(map[string]any{"data": map[string]string{"id": "group-1", "status": "ACTIVE"}})
		}
	case "/v2/project-1/secgroups/group-1/secGroupRules":
		write(map[string]any{"data": []any{}})
	case "/v2/project-1/secgroups/group-1/servers":
		write(map[string]any{"data": []any{}})
	case "/v2/project-1/sshKeys/key-1":
		if p.deleted["key"] {
			missing()
		} else {
			write(map[string]any{"data": map[string]string{"id": "key-1"}})
		}
	case "/v2/project-1/networks/vpc-1/subnets/subnet-1":
		if p.deleted["subnet"] {
			missing()
		} else {
			write(map[string]any{"uuid": "subnet-1", "networkUuid": "vpc-1", "status": "ACTIVE"})
		}
	case "/v2/project-1/networks/vpc-1":
		if p.deleted["vpc"] {
			missing()
		} else {
			write(map[string]any{"id": "vpc-1", "status": "ACTIVE"})
		}
	case "/v2/project-1/networks/vpc-1/subnets", "/v2/project-1/volumes/servers/server-1", "/v2/project-1/servers/subnets/subnet-1":
		write([]any{})
	case "/v2/project-1/network-interfaces-elastic", "/v2/project-1/virtualIpAddress", "/v2/project-1/network-acl/list":
		page([]any{})
	default:
		t.Errorf("unexpected SDK request: %s %s", req.Method, req.URL.Path)
		w.WriteHeader(http.StatusBadRequest)
	}
}
