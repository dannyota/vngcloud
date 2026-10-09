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

// dhcpOptionsFields builds one DHCP options set's fields the way
// GetDHCPOptions, ListDHCPOptions, and CreateDHCPOptions's own response
// shapes use them, all named "corp" since no test here needs another name.
func dhcpOptionsFields(vpcIDs ...string) map[string]any {
	if vpcIDs == nil {
		vpcIDs = []string{}
	}
	return map[string]any{
		"uuid": "dop-1", "name": "corp", "status": "ACTIVE",
		"dnsServers": []string{"10.166.12.196", "10.166.12.197"}, "mtu": 1450,
		"associatedNetworks": vpcIDs,
		"createdAt":          "2026-01-01T00:00:00Z", "updatedAt": "2026-01-01T00:00:00Z",
	}
}

func dhcpOptionsJSON(vpcIDs ...string) string {
	b, err := json.Marshal(dhcpOptionsFields(vpcIDs...))
	if err != nil {
		panic(err)
	}
	return string(b)
}

// dhcpOptionsCreateJSON builds CreateDHCPOptions's own response envelope:
// the set wrapped in a "data" field.
func dhcpOptionsCreateJSON() string {
	b, err := json.Marshal(map[string]any{"data": dhcpOptionsFields()})
	if err != nil {
		panic(err)
	}
	return string(b)
}

// --- list-dhcp-options ---

func TestNetworkListDHCPOptionsEndToEnd(t *testing.T) {
	body := `{"listData":[` + dhcpOptionsJSON() + `],"page":1,"pageSize":10000,"totalPage":1,"totalItem":1}`
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/dhcp_option": jsonHandler(http.StatusOK, body),
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1",
		"network", "list-dhcp-options", "--name", "corp",
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("list-dhcp-options: %v (stderr=%s)", err, stderr.String())
	}
	q, ok := fixture.queryFor("/v2/proj-1/dhcp_option")
	if !ok || !strings.Contains(q, "name=corp") {
		t.Fatalf("query = %q, ok=%v, want it to contain name=corp", q, ok)
	}
	if got := stdout.String(); !strings.Contains(got, `"UUID": "dop-1"`) {
		t.Fatalf("stdout = %s, want the listed set", got)
	}
}

// --- get-dhcp-options ---

func TestNetworkGetDHCPOptionsEndToEnd(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/dhcp_option/dop-1": jsonHandler(http.StatusOK, dhcpOptionsJSON("vpc-1")),
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1",
		"network", "get-dhcp-options", "--dhcp-options-id", "dop-1",
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("get-dhcp-options: %v (stderr=%s)", err, stderr.String())
	}
	if got := stdout.String(); !strings.Contains(got, `"Name": "corp"`) || !strings.Contains(got, `"vpc-1"`) {
		t.Fatalf("stdout = %s, want the set with its VPC", got)
	}
}

// --- create-dhcp-options ---

// TestNetworkCreateDHCPOptionsEndToEnd checks that --name and DNSServers
// (given here through --cli-input-json, an alternate way to set the same
// --dns-servers flag) become the POST body, and that the create response
// comes back on stdout.
func TestNetworkCreateDHCPOptionsEndToEnd(t *testing.T) {
	var body []byte
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/dhcp_option": func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				t.Fatalf("method = %s, want POST", r.Method)
			}
			defer func() { _ = r.Body.Close() }()
			body, _ = io.ReadAll(r.Body)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(dhcpOptionsCreateJSON()))
		},
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1",
		"network", "create-dhcp-options", "--name", "corp",
		"--cli-input-json", `{"DNSServers":["10.166.12.196","10.166.12.197"]}`,
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("create-dhcp-options: %v (stderr=%s)", err, stderr.String())
	}

	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("body is not valid JSON: %v (%s)", err, body)
	}
	if len(decoded) != 2 || decoded["name"] != "corp" {
		t.Fatalf("body = %s, want exactly name and dnsServers, no mtu, tags, or zoneId", body)
	}
	if got := stdout.String(); !strings.Contains(got, `"UUID": "dop-1"`) {
		t.Fatalf("stdout = %s, want the created set", got)
	}
}

// TestNetworkCreateDHCPOptionsMissingDNSServersIsUsageErrorWithZeroRequests
// checks that DNSServers, a required field, is still enforced before any
// request when neither --dns-servers nor --cli-input-json sets it.
func TestNetworkCreateDHCPOptionsMissingDNSServersIsUsageErrorWithZeroRequests(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/dhcp_option": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1",
		"network", "create-dhcp-options", "--name", "corp",
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected an error without DNSServers")
	}
	if got := exitCode(err); got != 2 {
		t.Fatalf("exitCode = %d, want 2 (stderr=%s)", got, stderr.String())
	}
	if n := fixture.requestCount(); n != 0 {
		t.Fatalf("requestCount = %d, want 0", n)
	}
}

// --- delete-dhcp-options ---

func TestNetworkDeleteDHCPOptionsRequiresYesWithZeroRequests(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/dhcp_option/dop-1": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1",
		"network", "delete-dhcp-options", "--dhcp-options-id", "dop-1",
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

// TestNetworkDeleteDHCPOptionsWithYesSendsGetThenDelete checks the success
// path: an unattached set (empty associatedNetworks) reads once, then the
// DELETE is sent.
func TestNetworkDeleteDHCPOptionsWithYesSendsGetThenDelete(t *testing.T) {
	deleted := false
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/dhcp_option/dop-1": func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet:
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(dhcpOptionsJSON()))
			case http.MethodDelete:
				deleted = true
				w.WriteHeader(http.StatusNoContent)
			default:
				t.Fatalf("unexpected method %s", r.Method)
			}
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1", "--yes",
		"network", "delete-dhcp-options", "--dhcp-options-id", "dop-1",
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("delete-dhcp-options: %v (stderr=%s)", err, stderr.String())
	}
	if !deleted {
		t.Fatal("the DELETE was never sent")
	}
	if n := fixture.requestCount(); n != 2 {
		t.Fatalf("requestCount = %d, want 2 (the pre-delete read, then the delete)", n)
	}
}

// TestNetworkDeleteDHCPOptionsInUseWithNoDelete checks that a set still
// attached to a VPC stops the delete before any DELETE, with error code
// ResourceInUse.
func TestNetworkDeleteDHCPOptionsInUseWithNoDelete(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/dhcp_option/dop-1": func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet {
				t.Fatalf("unexpected method %s, want only GET", r.Method)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(dhcpOptionsJSON("vpc-1")))
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1", "--yes",
		"network", "delete-dhcp-options", "--dhcp-options-id", "dop-1",
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected an error")
	}
	if got := classify(err).Code; got != "ResourceInUse" {
		t.Fatalf("Code = %q, want ResourceInUse (stderr=%s)", got, stderr.String())
	}
	if got := exitCode(err); got != 1 {
		t.Fatalf("exitCode = %d, want 1", got)
	}
	if n := fixture.requestCount(); n != 1 {
		t.Fatalf("requestCount = %d, want 1 (the read only, no delete)", n)
	}
}

// --- set-vpc-dhcp-options ---

func TestNetworkSetVPCDHCPOptionsRequiresYesWithZeroRequests(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/networks/vpc-1": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1",
		"network", "set-vpc-dhcp-options", "--vpc-id", "vpc-1", "--dhcp-options-id", "dop-1",
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

// TestNetworkSetVPCDHCPOptionsWithYesSendsPatchAndWaits checks the success
// path: the VPC read (DHCPOptionID not yet the target), the target set
// read, the PATCH, then the post-write confirm read settling to the target.
func TestNetworkSetVPCDHCPOptionsWithYesSendsPatchAndWaits(t *testing.T) {
	vpcReads := 0
	patched := false
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/networks/vpc-1": func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet {
				t.Fatalf("unexpected method %s", r.Method)
			}
			vpcReads++
			w.Header().Set("Content-Type", "application/json")
			fields := vpcFields("my-vpc", "DISABLED")
			if vpcReads > 1 {
				fields["dhcpOptionId"] = "dop-1"
			}
			b, err := json.Marshal(fields)
			if err != nil {
				t.Fatal(err)
			}
			_, _ = w.Write(b)
		},
		"/v2/proj-1/dhcp_option/dop-1": jsonHandler(http.StatusOK, dhcpOptionsJSON("vpc-1")),
		"/v2/proj-1/networks/vpc-1/updateDhcpOption": func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPatch {
				t.Fatalf("method = %s, want PATCH", r.Method)
			}
			patched = true
			w.WriteHeader(http.StatusOK)
		},
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1", "--yes",
		"network", "set-vpc-dhcp-options", "--vpc-id", "vpc-1", "--dhcp-options-id", "dop-1",
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("set-vpc-dhcp-options: %v (stderr=%s)", err, stderr.String())
	}
	if !patched {
		t.Fatal("the set PATCH was never sent")
	}
	if got := stdout.String(); !strings.Contains(got, `"Changed": true`) {
		t.Fatalf("stdout = %s, want Changed true", got)
	}
}

// TestNetworkSetVPCDHCPOptionsAlreadySetSendsNoPatch checks that a VPC
// already on the target set sends no PATCH and reports Changed false, even
// though --yes is still required.
func TestNetworkSetVPCDHCPOptionsAlreadySetSendsNoPatch(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/networks/vpc-1": func(w http.ResponseWriter, r *http.Request) {
			fields := vpcFields("my-vpc", "DISABLED")
			fields["dhcpOptionId"] = "dop-1"
			b, err := json.Marshal(fields)
			if err != nil {
				t.Fatal(err)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(b)
		},
		"/v2/proj-1/networks/vpc-1/updateDhcpOption": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1", "--yes",
		"network", "set-vpc-dhcp-options", "--vpc-id", "vpc-1", "--dhcp-options-id", "dop-1",
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("set-vpc-dhcp-options: %v (stderr=%s)", err, stderr.String())
	}
	if n := fixture.requestCount(); n != 1 {
		t.Fatalf("requestCount = %d, want 1 (the pre-read only)", n)
	}
	if got := stdout.String(); !strings.Contains(got, `"Changed": false`) {
		t.Fatalf("stdout = %s, want Changed false", got)
	}
}

// --- clear-vpc-dhcp-options ---

func TestNetworkClearVPCDHCPOptionsRequiresYesWithZeroRequests(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/networks/vpc-1": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1",
		"network", "clear-vpc-dhcp-options", "--vpc-id", "vpc-1",
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

// TestNetworkClearVPCDHCPOptionsWithYesSendsEmptyPatchAndWaits checks the
// success path: the VPC read (DHCPOptionID set), the PATCH with an empty
// JSON body, then the post-write confirm read settling to an empty set.
func TestNetworkClearVPCDHCPOptionsWithYesSendsEmptyPatchAndWaits(t *testing.T) {
	vpcReads := 0
	var patchBody []byte
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/networks/vpc-1": func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet {
				t.Fatalf("unexpected method %s", r.Method)
			}
			vpcReads++
			fields := vpcFields("my-vpc", "DISABLED")
			if vpcReads == 1 {
				fields["dhcpOptionId"] = "dop-1"
			}
			b, err := json.Marshal(fields)
			if err != nil {
				t.Fatal(err)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(b)
		},
		"/v2/proj-1/dhcp_option/dop-1": jsonHandler(http.StatusOK, dhcpOptionsJSON("vpc-1")),
		"/v2/proj-1/networks/vpc-1/updateDhcpOption": func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPatch {
				t.Fatalf("method = %s, want PATCH", r.Method)
			}
			defer func() { _ = r.Body.Close() }()
			patchBody, _ = io.ReadAll(r.Body)
			w.WriteHeader(http.StatusOK)
		},
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1", "--yes",
		"network", "clear-vpc-dhcp-options", "--vpc-id", "vpc-1",
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("clear-vpc-dhcp-options: %v (stderr=%s)", err, stderr.String())
	}
	if got := strings.TrimSpace(string(patchBody)); got != "{}" {
		t.Fatalf("PATCH body = %q, want {}", got)
	}
	if got := stdout.String(); !strings.Contains(got, `"Changed": true`) {
		t.Fatalf("stdout = %s, want Changed true", got)
	}
}

// TestNetworkClearVPCDHCPOptionsAlreadyClearSendsNoPatch checks that a VPC
// already with no set sends no PATCH and reports Changed false, even though
// --yes is still required.
func TestNetworkClearVPCDHCPOptionsAlreadyClearSendsNoPatch(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/networks/vpc-1": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(vpcJSON("my-vpc", "DISABLED")))
		},
		"/v2/proj-1/networks/vpc-1/updateDhcpOption": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1", "--yes",
		"network", "clear-vpc-dhcp-options", "--vpc-id", "vpc-1",
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("clear-vpc-dhcp-options: %v (stderr=%s)", err, stderr.String())
	}
	if n := fixture.requestCount(); n != 1 {
		t.Fatalf("requestCount = %d, want 1 (the pre-read only)", n)
	}
	if got := stdout.String(); !strings.Contains(got, `"Changed": false`) {
		t.Fatalf("stdout = %s, want Changed false", got)
	}
}

// --- read-only ---

// TestNetworkDHCPOptionsWritesReadOnlyRefusedWithZeroRequests checks that
// read-only refuses create-dhcp-options, delete-dhcp-options,
// set-vpc-dhcp-options, and clear-vpc-dhcp-options before any request, the
// same way it already does for every other network write.
func TestNetworkDHCPOptionsWritesReadOnlyRefusedWithZeroRequests(t *testing.T) {
	tests := []struct {
		op   string
		args []string
	}{
		{"create-dhcp-options", []string{
			"create-dhcp-options", "--name", "corp",
			"--cli-input-json", `{"DNSServers":["10.166.12.196"]}`,
		}},
		{"delete-dhcp-options", []string{"delete-dhcp-options", "--dhcp-options-id", "dop-1", "--yes"}},
		{"set-vpc-dhcp-options", []string{
			"set-vpc-dhcp-options", "--vpc-id", "vpc-1", "--dhcp-options-id", "dop-1", "--yes",
		}},
		{"clear-vpc-dhcp-options", []string{"clear-vpc-dhcp-options", "--vpc-id", "vpc-1", "--yes"}},
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
				"/v2/proj-1/dhcp_option":                     unexpected(t),
				"/v2/proj-1/dhcp_option/dop-1":               unexpected(t),
				"/v2/proj-1/networks/vpc-1":                  unexpected(t),
				"/v2/proj-1/networks/vpc-1/updateDhcpOption": unexpected(t),
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
