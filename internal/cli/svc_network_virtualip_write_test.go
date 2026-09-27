package cli

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"danny.vn/vngcloud"
)

// vipFields builds one virtual IP's fields the way GetVirtualIPAddress's own
// "data" envelope uses them, and the way CreateVirtualIPAddress's and
// UpdateVirtualIPAddress's own "data" envelopes reuse the same shape:
// description is always "d", the value every update test's read-merge
// expects back on the PUT body unchanged, and status is always "ACTIVE",
// the only status any test in this file needs. A nil pairs prints as [],
// the shape addressPairIps always takes over the wire.
func vipFields(name, mode, typ, ip string, pairs []string) map[string]any {
	if pairs == nil {
		pairs = []string{}
	}
	return map[string]any{
		"uuid": "vip-1", "id": "vip-1", "name": name, "ipAddress": ip,
		"networkId": "vpc-1", "subnetId": "subnet-1", "description": "d",
		"status": "ACTIVE", "type": typ, "mode": mode, "addressPairIps": pairs,
	}
}

func vipEnvelopeJSON(name, mode, typ, ip string, pairs []string) string {
	b, err := json.Marshal(map[string]any{"data": vipFields(name, mode, typ, ip, pairs)})
	if err != nil {
		panic(err)
	}
	return string(b)
}

// --- create-virtual-ip-address ---

// TestNetworkCreateVirtualIPAddressEndToEnd checks the flag-to-body mapping
// for every field, and that a create response already ACTIVE sends no
// confirm read.
func TestNetworkCreateVirtualIPAddressEndToEnd(t *testing.T) {
	var body []byte
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/virtualIpAddress": func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				t.Fatalf("method = %s, want POST", r.Method)
			}
			defer func() { _ = r.Body.Close() }()
			body, _ = io.ReadAll(r.Body)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(vipEnvelopeJSON("vip1", "Active/Passive", "", "203.0.113.10", nil)))
		},
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1",
		"network", "create-virtual-ip-address", "--subnet-id", "subnet-1", "--name", "vip1",
		"--mode", "Active/Passive", "--ip-address", "203.0.113.10", "--description", "front door",
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("create-virtual-ip-address: %v (stderr=%s)", err, stderr.String())
	}

	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("body is not valid JSON: %v (%s)", err, body)
	}
	want := map[string]any{
		"subnetId": "subnet-1", "name": "vip1", "mode": "Active/Passive",
		"ipAddress": "203.0.113.10", "description": "front door",
	}
	if len(decoded) != len(want) {
		t.Fatalf("body = %s, want exactly %v", body, want)
	}
	for k, v := range want {
		if decoded[k] != v {
			t.Fatalf("body[%q] = %v, want %v (body = %s)", k, decoded[k], v, body)
		}
	}
	if n := fixture.requestCount(); n != 1 {
		t.Fatalf("requestCount = %d, want 1 (the create response was already ACTIVE, no confirm read)", n)
	}
	if got := stdout.String(); !strings.Contains(got, `"UUID": "vip-1"`) {
		t.Fatalf("stdout = %s, want the created virtual IP", got)
	}
}

// TestNetworkCreateVirtualIPAddressMissingModeIsUsageErrorWithZeroRequests
// checks that Mode, required with no default per the design, is enforced
// before any request.
func TestNetworkCreateVirtualIPAddressMissingModeIsUsageErrorWithZeroRequests(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/virtualIpAddress": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1",
		"network", "create-virtual-ip-address", "--subnet-id", "subnet-1", "--name", "vip1",
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected an error without --mode")
	}
	if got := exitCode(err); got != 2 {
		t.Fatalf("exitCode = %d, want 2 (stderr=%s)", got, stderr.String())
	}
	if n := fixture.requestCount(); n != 0 {
		t.Fatalf("requestCount = %d, want 0", n)
	}
}

// --- update-virtual-ip-address ---

// TestNetworkUpdateVirtualIPAddressEndToEnd checks that a --name-only update
// resends the read Description and Mode unchanged on the PUT, and that the
// read-after-write's new name comes back on stdout.
func TestNetworkUpdateVirtualIPAddressEndToEnd(t *testing.T) {
	var body []byte
	reads := 0
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/virtualIpAddress/vip-1": func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet:
				reads++
				w.Header().Set("Content-Type", "application/json")
				if reads == 1 {
					_, _ = w.Write([]byte(vipEnvelopeJSON("old-name", "Active/Passive", "", "", nil)))
					return
				}
				_, _ = w.Write([]byte(vipEnvelopeJSON("new-name", "Active/Passive", "", "", nil)))
			case http.MethodPut:
				defer func() { _ = r.Body.Close() }()
				body, _ = io.ReadAll(r.Body)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(vipEnvelopeJSON("new-name", "Active/Passive", "", "", nil)))
			default:
				t.Fatalf("unexpected method %s", r.Method)
			}
		},
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1",
		"network", "update-virtual-ip-address", "--virtual-ip-address-id", "vip-1", "--name", "new-name",
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("update-virtual-ip-address: %v (stderr=%s)", err, stderr.String())
	}

	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("body is not valid JSON: %v (%s)", err, body)
	}
	if len(decoded) != 3 || decoded["name"] != "new-name" || decoded["description"] != "d" || decoded["mode"] != "Active/Passive" {
		t.Fatalf("body = %s, want name=new-name plus the read description and mode resent unchanged", body)
	}
	if reads != 2 {
		t.Fatalf("reads = %d, want 2 (pre-read and read-after-write)", reads)
	}
	if got := stdout.String(); !strings.Contains(got, `"Name": "new-name"`) {
		t.Fatalf("stdout = %s, want the renamed virtual IP", got)
	}
}

// TestNetworkUpdateVirtualIPAddressEmptyUpdateIsUsageErrorWithZeroRequests
// checks that leaving every field unset is refused before any request,
// since the API replaces every field on each PUT.
func TestNetworkUpdateVirtualIPAddressEmptyUpdateIsUsageErrorWithZeroRequests(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/virtualIpAddress/vip-1": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1",
		"network", "update-virtual-ip-address", "--virtual-ip-address-id", "vip-1",
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected an error with no field to change")
	}
	if got := exitCode(err); got != 2 {
		t.Fatalf("exitCode = %d, want 2 (stderr=%s)", got, stderr.String())
	}
	if n := fixture.requestCount(); n != 0 {
		t.Fatalf("requestCount = %d, want 0", n)
	}
}

// --- delete-virtual-ip-address ---

func TestNetworkDeleteVirtualIPAddressRequiresYesWithZeroRequests(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/virtualIpAddress/vip-1": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1",
		"network", "delete-virtual-ip-address", "--virtual-ip-address-id", "vip-1",
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected an error without --yes")
	}
	if got := exitCode(err); got != 2 {
		t.Fatalf("exitCode = %d, want 2 (stderr=%s)", got, stderr.String())
	}
	if n := fixture.requestCount(); n != 0 {
		t.Fatalf("requestCount = %d, want 0", n)
	}
}

// TestNetworkDeleteVirtualIPAddressWithYesSendsGetListDelete checks the
// success path's exact request sequence: the pre-delete read, the address
// pairs list, then the DELETE.
func TestNetworkDeleteVirtualIPAddressWithYesSendsGetListDelete(t *testing.T) {
	deleted := false
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/virtualIpAddress/vip-1": func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet:
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(vipEnvelopeJSON("vip1", "Active/Active", "", "", nil)))
			case http.MethodDelete:
				deleted = true
				w.WriteHeader(http.StatusNoContent)
			default:
				t.Fatalf("unexpected method %s", r.Method)
			}
		},
		"/v2/proj-1/virtualIpAddress/vip-1/addressPairs": jsonHandler(http.StatusOK, `{"data":[]}`),
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1", "--yes",
		"network", "delete-virtual-ip-address", "--virtual-ip-address-id", "vip-1",
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("delete-virtual-ip-address: %v (stderr=%s)", err, stderr.String())
	}
	if !deleted {
		t.Fatal("the DELETE was never sent")
	}
	if n := fixture.requestCount(); n != 3 {
		t.Fatalf("requestCount = %d, want 3 (read, address pairs list, delete)", n)
	}
}

// TestNetworkDeleteVirtualIPAddressInUseWithAddressPairsNoDelete checks that
// AddressPairIPs on the pre-delete read alone stops the delete, with error
// code ResourceInUse, before the address pairs list is even read.
func TestNetworkDeleteVirtualIPAddressInUseWithAddressPairsNoDelete(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/virtualIpAddress/vip-1": func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet {
				t.Fatalf("unexpected method %s, want only GET", r.Method)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(vipEnvelopeJSON("vip1", "Active/Active", "", "", []string{"203.0.113.20"})))
		},
		"/v2/proj-1/virtualIpAddress/vip-1/addressPairs": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1", "--yes",
		"network", "delete-virtual-ip-address", "--virtual-ip-address-id", "vip-1",
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected an error")
	}
	if got := classify(err).Code; got != "ResourceInUse" {
		t.Fatalf("Code = %q, want ResourceInUse (stderr=%s)", got, stderr.String())
	}
	if n := fixture.requestCount(); n != 1 {
		t.Fatalf("requestCount = %d, want 1 (the read only, no delete)", n)
	}
}

// TestNetworkDeleteVirtualIPAddressPublicTypeNoDelete checks that a Type
// other than the private value refuses the delete with InvalidUsage, so a
// public virtual IP is never deleted through this command.
func TestNetworkDeleteVirtualIPAddressPublicTypeNoDelete(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/virtualIpAddress/vip-1": func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet {
				t.Fatalf("unexpected method %s, want only GET", r.Method)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(vipEnvelopeJSON("vip1", "Active/Active", "public-vm", "", nil)))
		},
		"/v2/proj-1/virtualIpAddress/vip-1/addressPairs": jsonHandler(http.StatusOK, `{"data":[]}`),
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1", "--yes",
		"network", "delete-virtual-ip-address", "--virtual-ip-address-id", "vip-1",
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected an error")
	}
	if got := classify(err).Code; got != "InvalidUsage" {
		t.Fatalf("Code = %q, want InvalidUsage (stderr=%s)", got, stderr.String())
	}
	if n := fixture.requestCount(); n != 2 {
		t.Fatalf("requestCount = %d, want 2 (read, address pairs list, no delete)", n)
	}
}

// --- read-only ---

// TestNetworkVirtualIPWritesReadOnlyRefusedWithZeroRequests checks that
// read-only refuses every virtual IP write command before any request, the
// same way it already does for VPC and subnet writes.
func TestNetworkVirtualIPWritesReadOnlyRefusedWithZeroRequests(t *testing.T) {
	tests := []struct {
		op   string
		args []string
	}{
		{"create-virtual-ip-address", []string{"create-virtual-ip-address", "--subnet-id", "subnet-1", "--name", "vip1", "--mode", "Active/Active"}},
		{"update-virtual-ip-address", []string{"update-virtual-ip-address", "--virtual-ip-address-id", "vip-1", "--name", "vip2"}},
		{"delete-virtual-ip-address", []string{"delete-virtual-ip-address", "--virtual-ip-address-id", "vip-1", "--yes"}},
	}
	unexpected := func(t *testing.T) func(http.ResponseWriter, *http.Request) {
		return func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}
	for _, tc := range tests {
		t.Run(tc.op, func(t *testing.T) {
			home := withCleanEnv(t)
			writeConfigFile(t, home, "[profile agent]\nregion = hcm-3\nproject_id = proj-1\nread_only = true\n")
			writeCredentialsFile(t, home, "[agent]\nusername = u\npassword = p\nroot_email = e@example.com\n")

			fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
				"/v2/proj-1/virtualIpAddress":                    unexpected(t),
				"/v2/proj-1/virtualIpAddress/vip-1":              unexpected(t),
				"/v2/proj-1/virtualIpAddress/vip-1/addressPairs": unexpected(t),
			})
			opts := newFakeServer(t, fixture.mux)
			withTestOptions(t, append(opts, vngcloud.WithStaticToken("test-token"))...)

			root := newRootCmd(strings.NewReader(""), io.Discard, io.Discard)
			root.SetArgs(append([]string{"--profile", "agent", "network"}, tc.args...))
			err := root.ExecuteContext(context.Background())
			if err == nil {
				t.Fatalf("expected a read-only refusal")
			}
			if got := classify(err).Code; got != "ReadOnly" {
				t.Fatalf("Code = %q, want ReadOnly", got)
			}
			if got := exitCode(err); got != 2 {
				t.Fatalf("exitCode = %d, want 2", got)
			}
			if n := fixture.requestCount(); n != 0 {
				t.Fatalf("requestCount = %d, want 0", n)
			}
		})
	}
}
